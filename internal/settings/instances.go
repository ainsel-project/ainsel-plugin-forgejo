package settings

// Forgejo instances: the list of Forgejo addresses the admin says this plugin
// may be installed against. The setup wizard picks from this list instead of
// typing a free-text URL, and the plan and apply refuse a baseUrl that is not
// on it — so an install can never point at a host nobody configured.
//
// The rows live in the same SQLite file as the settings and the installs, and
// the list is written whole: a PUT replaces every row in one transaction, so
// the stored list is always exactly what the admin saw on the Settings page.
// Position is the row's order on the page; the list is read in that order, so
// the wizard's dropdown and the Settings list never disagree about which
// instance is first.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// instancesSeededKey is the meta row that says the instance list has been
// written at all — by the env seed or by the admin. Its absence is the only
// state from which SeedInstanceFromEnv will write.
const instancesSeededKey = "instances_seeded"

// Instance is one configured Forgejo: what the admin named it, and its
// address. The address is stored normalized (see NormalizeBaseURL); the name is
// stored as the admin wrote it, trimmed.
type Instance struct {
	Name    string `json:"name"`
	BaseURL string `json:"baseUrl"`
}

// NormalizeBaseURL is the comparison form of a Forgejo address: trimmed, and
// with the trailing slashes off, so "https://forgejo.example.com" and
// "https://forgejo.example.com/" are the same instance. It is what the plan's
// "is this baseUrl configured?" check compares, and what the store writes, so
// the stored list is already in the form the check reads.
//
// It deliberately does not rewrite the rest of the URL: a Forgejo under a
// subpath is a different address than its root, and the admin's spelling of the
// path is the fact the install links from.
func NormalizeBaseURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

// checkInstance refuses one instance's bad half, naming it the way a
// FieldError names a settings field: "instances[i].name" / "instances[i].baseUrl".
// An empty Role is right for these rows — an instance belongs to no role.
func checkInstance(i int, in Instance) *FieldError {
	bad := func(field, msg string) *FieldError {
		return &FieldError{Field: fmt.Sprintf("instances[%d].%s", i, field), Error: msg}
	}
	if strings.TrimSpace(in.Name) == "" {
		return bad("name", "instance name is required")
	}
	base := NormalizeBaseURL(in.BaseURL)
	if base == "" {
		return bad("baseUrl", "instance base URL is required")
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return bad("baseUrl", fmt.Sprintf("instance base URL %q is not an absolute http(s) address", in.BaseURL))
	}
	return nil
}

// ListInstances returns the configured instances in their stored order. The
// answer is empty, not nil, when nothing is configured — the wizard reads an
// empty list as "no instance to pick" and links the admin to Settings.
func (s *Store) ListInstances(ctx context.Context) ([]Instance, error) {
	const q = `SELECT name, base_url FROM forgejo_instances ORDER BY position;`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("settings: cannot read instances: %w", err)
	}
	defer rows.Close()

	out := []Instance{}
	for rows.Next() {
		var in Instance
		if err := rows.Scan(&in.Name, &in.BaseURL); err != nil {
			return nil, fmt.Errorf("settings: cannot read an instance: %w", err)
		}
		out = append(out, in)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("settings: cannot read instances: %w", err)
	}
	return out, nil
}

// SaveInstances replaces the whole list, all of it or none of it — the same
// all-or-nothing rule Save applies to the settings rows. Every instance is
// checked before the first row moves, and a refusal comes back as a
// ValidationError naming each bad one (and any duplicate address). An empty
// list is legal: it says the admin removed every instance, and the wizard then
// offers nothing to install against.
//
// Any write of the list marks it as written, which is what keeps the env seed
// from coming behind an admin who emptied it.
func (s *Store) SaveInstances(ctx context.Context, instances []Instance) error {
	var bad []FieldError
	seen := map[string]int{}
	for i, in := range instances {
		if fe := checkInstance(i, in); fe != nil {
			bad = append(bad, *fe)
			continue
		}
		base := NormalizeBaseURL(in.BaseURL)
		if first, dup := seen[base]; dup {
			bad = append(bad, FieldError{
				Field: fmt.Sprintf("instances[%d].baseUrl", i),
				Error: fmt.Sprintf("same address as instances[%d]", first),
			})
			continue
		}
		seen[base] = i
	}
	if len(bad) > 0 {
		return &ValidationError{Fields: bad}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("settings: cannot begin the instances write: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM forgejo_instances;`); err != nil {
		return fmt.Errorf("settings: cannot clear the instances: %w", err)
	}
	const q = `INSERT INTO forgejo_instances (position, name, base_url) VALUES (?, ?, ?);`
	for i, in := range instances {
		if _, err := tx.ExecContext(ctx, q, i, strings.TrimSpace(in.Name), NormalizeBaseURL(in.BaseURL)); err != nil {
			return fmt.Errorf("settings: cannot save instance %q: %w", in.Name, err)
		}
	}
	// Even the empty write counts: the admin has spoken, and the env seed is
	// the migration for a list nobody has touched.
	const marker = `INSERT OR REPLACE INTO meta (key, value) VALUES (?, '1');`
	if _, err := tx.ExecContext(ctx, marker, instancesSeededKey); err != nil {
		return fmt.Errorf("settings: cannot record the instance write: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("settings: cannot commit the instances write: %w", err)
	}
	return nil
}

// SeedInstanceFromEnv writes one instance from FORGEJO_BASE_URL the first time
// the plugin starts with the variable set and the admin has never written the
// list — the migration that keeps an existing deployment installable without an
// admin step. It seeds once: a list the admin has edited (or emptied on purpose)
// is never re-seeded, and a start with the variable unset writes nothing. It
// reports whether it seeded.
//
// "Never written" is the meta marker, not an empty table: an admin who deletes
// the seeded instance has said something, and the next start must not put it
// back.
func (s *Store) SeedInstanceFromEnv(ctx context.Context, baseURL string) (bool, error) {
	base := NormalizeBaseURL(baseURL)
	if base == "" {
		return false, nil
	}

	var written string
	const marker = `SELECT value FROM meta WHERE key = ?;`
	err := s.db.QueryRowContext(ctx, marker, instancesSeededKey).Scan(&written)
	if err == nil && written != "" {
		return false, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("settings: cannot read the instance seed marker: %w", err)
	}

	// The host names the seeded instance: an admin who never set a display
	// name still gets a row that says which Forgejo it is.
	name := base
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		name = u.Host
	}
	if err := s.SaveInstances(ctx, []Instance{{Name: name, BaseURL: base}}); err != nil {
		var invalid *ValidationError
		if errors.As(err, &invalid) {
			// Unreachable for an address NormalizeBaseURL already accepted;
			// still not worth panicking over — the admin can add it by hand.
			return false, fmt.Errorf("settings: cannot seed the instance from FORGEJO_BASE_URL: %w", err)
		}
		return false, err
	}
	return true, nil
}
