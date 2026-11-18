// Package store keeps the installs the wizard has set up, in a SQLite file of
// its own. It is the plugin's only database: work data is read from AInsel each
// time it is shown, and the one thing that has to outlive a pod restart is the
// install model — what the admin told the wizard.
//
// Nothing here holds a secret. setup.Install carries none by design: bot tokens
// and webhook secrets are passed through to AInsel as the wizard creates the
// resources and never reach this file.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/forge"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"

	// The pure-Go SQLite driver; the plugin image is distroless with CGO off.
	_ "modernc.org/sqlite"
)

// dbFileName is the SQLite file inside the directory given to Open. The chart
// mounts a volume at that directory, which is what makes the installs survive.
const dbFileName = "forgejo.db"

// schema is the whole database: one row per install, keyed by slug, holding the
// install's JSON, and one row per event the work list has ever asked about,
// holding the subject derived from it. The model's nested maps and per-role
// states go in as one document rather than one column or table each — nothing
// ever queries inside an install, it is only ever read back whole, and a new
// model field then needs no migration: it is simply part of the JSON. Every
// table is created IF NOT EXISTS, so opening a database that predates a table
// adds it and keeps everything the rows already hold.
const schema = `
CREATE TABLE IF NOT EXISTS installs (
	slug TEXT PRIMARY KEY,
	data TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS event_subjects (
	event_id TEXT PRIMARY KEY,
	data TEXT NOT NULL,
	read_at TEXT NOT NULL
);`

// preReleaseSchema is the shape the first, unreleased wizard wrote: one column
// per field of the old model, with no data column. The plugin has no users to
// migrate, so Open drops that table on open instead of carrying a migration —
// it only ever drops a table that lacks the data column, so a database already
// in the current shape keeps its rows (see TestReopenKeepsData).
const preReleaseSchema = `DROP TABLE installs;`

// Store holds the one open database handle. It is safe for concurrent use:
// database/sql hands each query its own connection, and SQLite serialises the
// writes between them.
type Store struct {
	db *sql.DB
}

// Open opens the store in dir, creating the directory and the schema if they
// are not there yet — the state on a fresh install, where the volume is empty.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("store: dir is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("store: cannot create dir %q: %w", dir, err)
	}

	// busy_timeout makes a write that finds the file locked wait for the
	// lock to clear instead of failing on the spot.
	db, err := sql.Open("sqlite", filepath.Join(dir, dbFileName)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("store: cannot open database in %q: %w", dir, err)
	}
	// One connection, not a pool: SQLite has a single writer, and a query that
	// waits for the write lock on a second connection is exactly the deadlock
	// a pool invites.
	db.SetMaxOpenConns(1)

	ctx := context.Background()
	if err := dropPreReleaseTable(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: cannot migrate database in %q: %w", dir, err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: cannot create schema in %q: %w", dir, err)
	}
	return &Store{db: db}, nil
}

// dropPreReleaseTable drops the installs table written by a pre-release build —
// one without a data column — so the current schema can take its place. A
// missing table (a fresh database) and a table that already has the data
// column (the current shape) are both left as they are.
func dropPreReleaseTable(ctx context.Context, db *sql.DB) error {
	var dataCols int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM pragma_table_info('installs') WHERE name = 'data';`,
	).Scan(&dataCols); err != nil {
		return fmt.Errorf("cannot inspect the installs table: %w", err)
	}
	if dataCols > 0 {
		return nil
	}

	var tables int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'installs';`,
	).Scan(&tables); err != nil {
		return fmt.Errorf("cannot look for the installs table: %w", err)
	}
	if tables == 0 {
		return nil
	}

	if _, err := db.ExecContext(ctx, preReleaseSchema); err != nil {
		return fmt.Errorf("cannot drop the pre-release installs table: %w", err)
	}
	return nil
}

// Close releases the database handle. Open pairs with it; leaving it open only
// leaks the file on exit.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("store: cannot close database: %w", err)
	}
	return nil
}

// Save stores an install, replacing the one with the same slug if there is one
// — the wizard saves the same install again as the admin fills it in. The
// whole model goes in as one JSON document: an empty field of the new install
// replaces what the old one had, exactly as a column-per-field row would.
// The empty slug is refused: a row nobody can name is a row nobody can delete.
func (s *Store) Save(ctx context.Context, in setup.Install) error {
	if in.Slug == "" {
		return errors.New("store: install slug is required")
	}

	data, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("store: cannot encode install %q: %w", in.Slug, err)
	}

	const q = `
INSERT INTO installs (slug, data)
VALUES (?, ?)
ON CONFLICT (slug) DO UPDATE SET
	data = excluded.data;`
	if _, err := s.db.ExecContext(ctx, q, in.Slug, string(data)); err != nil {
		return fmt.Errorf("store: cannot save install %q: %w", in.Slug, err)
	}
	return nil
}

// List returns every install, ordered by slug so the same call answers the same
// list every time. No installs is no rows, not an error — and it is an empty
// slice rather than a nil one, because a nil marshals to null and the callers
// read the result as a list: GET /api/setup/installs has to answer {"installs":[]}.
func (s *Store) List(ctx context.Context) ([]setup.Install, error) {
	const q = `SELECT data FROM installs ORDER BY slug;`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: cannot list installs: %w", err)
	}
	defer rows.Close()

	installs := []setup.Install{}
	for rows.Next() {
		in, err := scanInstall(rows)
		if err != nil {
			return nil, err
		}
		installs = append(installs, in)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: cannot list installs: %w", err)
	}
	return installs, nil
}

// ConnectorIDs returns the connector ids the installs point at, each named
// once, an install that has no connector yet naming nothing. The cleanup flows
// use it to ask which connectors belong to this plugin. The row holds the whole
// install, so the ids are read out of its JSON rather than a column; with a
// handful of installs per plugin there is nothing to index.
func (s *Store) ConnectorIDs(ctx context.Context) ([]string, error) {
	const q = `SELECT data FROM installs ORDER BY slug;`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: cannot list connector ids: %w", err)
	}
	defer rows.Close()

	var ids []string
	seen := map[string]bool{}
	for rows.Next() {
		in, err := scanInstall(rows)
		if err != nil {
			return nil, err
		}
		if in.ConnectorID == "" || seen[in.ConnectorID] {
			continue
		}
		seen[in.ConnectorID] = true
		ids = append(ids, in.ConnectorID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: cannot list connector ids: %w", err)
	}
	return ids, nil
}

// Delete removes the install with this slug. A slug that is not there is not
// an error — an uninstall that runs twice has simply nothing left to remove.
func (s *Store) Delete(ctx context.Context, slug string) error {
	const q = `DELETE FROM installs WHERE slug = ?;`
	if _, err := s.db.ExecContext(ctx, q, slug); err != nil {
		return fmt.Errorf("store: cannot delete install %q: %w", slug, err)
	}
	return nil
}

// scanner is the one row of rows.Scan that scanInstall needs; it is a named
// interface so both *sql.Rows and *sql.Row can be handed to it.
type scanner interface {
	Scan(dest ...any) error
}

// subjectChunk is how many event ids one Subjects lookup asks for in its IN
// list. A page of runs names nearly a page of distinct events, and an
// unbounded IN list is a statement the database has to be able to build.
const subjectChunk = 500

// Subjects returns the cached subjects for the event ids named, keyed by event
// id. An id the store has never heard of is simply absent from the map — it is
// the caller's read-miss to answer from the hub. The lookup is chunked, because
// a page of runs can name hundreds of ids at once.
func (s *Store) Subjects(ctx context.Context, eventIDs []string) (map[string]forge.Subject, error) {
	out := make(map[string]forge.Subject, len(eventIDs))
	for start := 0; start < len(eventIDs); start += subjectChunk {
		ids := eventIDs[start:min(start+subjectChunk, len(eventIDs))]

		args := make([]any, len(ids))
		for i, id := range ids {
			args[i] = id
		}
		q := `SELECT event_id, data FROM event_subjects WHERE event_id IN (` +
			strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + `);`

		rows, err := s.db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, fmt.Errorf("store: cannot read event subjects: %w", err)
		}
		for rows.Next() {
			var id, data string
			if err := rows.Scan(&id, &data); err != nil {
				rows.Close()
				return nil, fmt.Errorf("store: cannot read an event subject: %w", err)
			}
			var subject forge.Subject
			if err := json.Unmarshal([]byte(data), &subject); err != nil {
				rows.Close()
				return nil, fmt.Errorf("store: cannot decode the subject of event %q: %w", id, err)
			}
			out[id] = subject
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: cannot read event subjects: %w", err)
		}
		rows.Close()
	}
	return out, nil
}

// PutSubjects stores what each of these events was about, replacing what an
// earlier read had kept for the same id. The whole map goes in one transaction:
// a page of runs is answered by one write, not by a hundred that a crash can
// halve. A zero subject is stored like any other — it means the event names no
// issue or PR, or the hub no longer has the payload, and that answer is final
// too, so the event should not be fetched again.
func (s *Store) PutSubjects(ctx context.Context, subjects map[string]forge.Subject, readAt time.Time) error {
	if len(subjects) == 0 {
		return nil
	}

	// RFC3339Nano UTC, so the prune's comparison of these strings is a
	// comparison of the moments they name.
	read := readAt.UTC().Format(time.RFC3339Nano)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: cannot begin the event subject write: %w", err)
	}
	defer tx.Rollback()

	const q = `
INSERT INTO event_subjects (event_id, data, read_at)
VALUES (?, ?, ?)
ON CONFLICT (event_id) DO UPDATE SET
	data = excluded.data,
	read_at = excluded.read_at;`
	for id, subject := range subjects {
		data, err := json.Marshal(subject)
		if err != nil {
			return fmt.Errorf("store: cannot encode the subject of event %q: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx, q, id, string(data), read); err != nil {
			return fmt.Errorf("store: cannot save the subject of event %q: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: cannot commit the event subject write: %w", err)
	}
	return nil
}

// PruneSubjects deletes the subjects read before the given moment and returns
// how many went. Events age out of every page's window eventually, and their
// subjects should age out with them rather than sit in the database forever.
func (s *Store) PruneSubjects(ctx context.Context, before time.Time) (int64, error) {
	const q = `DELETE FROM event_subjects WHERE read_at < ?;`
	res, err := s.db.ExecContext(ctx, q, before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("store: cannot prune event subjects: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: cannot count the pruned event subjects: %w", err)
	}
	return n, nil
}

// scanInstall reads one row's JSON back into the model it came from.
func scanInstall(r scanner) (setup.Install, error) {
	var in setup.Install
	var data string
	if err := r.Scan(&data); err != nil {
		return in, fmt.Errorf("store: cannot read an install: %w", err)
	}
	if err := json.Unmarshal([]byte(data), &in); err != nil {
		return in, fmt.Errorf("store: cannot decode install %q: %w", in.Slug, err)
	}
	return in, nil
}
