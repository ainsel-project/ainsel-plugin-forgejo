package store

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/forge"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
)

// fillTime is the timestamp the filled install uses for its webhook times. UTC
// with sub-second precision: a stored time has to come back equal to the one
// that went in, and a non-UTC location would come back with a fixed offset
// rather than the location, which reflect.DeepEqual would call a difference.
var fillTime = time.Date(2026, 10, 24, 12, 34, 56, 789000000, time.UTC)

// filledInstall builds an install with every field of setup.Install set to a
// distinct non-zero value, found by reflection rather than written out by hand.
// A field of a kind it cannot fill fails the test by name: that is the point.
// When someone adds a field to setup.Install they have to be able to fill it
// here, otherwise the round-trip test below fails instead of the field silently
// vanishing on every Save.
//
// The store now keeps the install as one JSON document, so what this guards
// against specifically is a new field that does not serialise — an unexported
// one, or one tagged json:"-" — which no other test would notice.
func filledInstall(t *testing.T, slug string) setup.Install {
	t.Helper()

	f := &filler{}
	inst := reflect.New(reflect.TypeOf(setup.Install{})).Elem()
	f.fill(t, inst, "")

	in := inst.Interface().(setup.Install)
	in.Slug = slug
	return in
}

// filler fills a value field by field, naming each value after the field path it
// belongs to so a value that reads back from the wrong place shows up as a
// mismatch rather than as two fields agreeing by accident.
type filler struct {
	n int
}

func (f *filler) fill(t *testing.T, v reflect.Value, path string) {
	t.Helper()
	if path == "" {
		path = v.Type().Name()
	}

	switch v.Type() {
	case reflect.TypeOf(""), reflect.TypeOf(int64(0)):
		f.n++
		if v.Kind() == reflect.String {
			v.SetString("fill-" + path)
		} else {
			v.SetInt(int64(f.n) * 7)
		}
	case reflect.TypeOf(0):
		// LayoutVersion is the one plain int on the row: a small value, so it reads
		// as a plausible layout marker rather than an accident.
		f.n++
		v.SetInt(int64(f.n) % 4)
	case reflect.TypeOf(false):
		// Retiring is the one bool on the row: true, so the round trip says the stamp
		// survives a save rather than that a zero value happened to match.
		v.SetBool(true)
	case reflect.TypeOf([]string{}):
		f.n++
		v.Set(reflect.ValueOf([]string{"fill-" + path + "-a", "fill-" + path + "-b"}))
	case reflect.TypeOf(time.Time{}):
		v.Set(reflect.ValueOf(fillTime))
	case reflect.TypeOf((*time.Time)(nil)):
		ts := fillTime
		v.Set(reflect.ValueOf(&ts))
	case reflect.TypeOf((*setup.WorkingHours)(nil)):
		// The window the wizard last wrote. A real zone, so the fixture is a window the
		// hub would accept, and a day list that moves with the field path, so a value read
		// back from the wrong place shows up as a mismatch.
		f.n++
		v.Set(reflect.ValueOf(&setup.WorkingHours{
			Timezone: "Europe/Berlin",
			Days:     []int{f.n % 7, 1, 2},
			Start:    "09:00",
			End:      "17:00",
		}))
	case reflect.TypeOf(map[setup.Role]setup.RoleState{}):
		m := reflect.MakeMap(v.Type())
		for _, role := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
			roleState := reflect.New(v.Type().Elem()).Elem()
			f.fill(t, roleState, path+"."+string(role))
			m.SetMapIndex(reflect.ValueOf(role), roleState)
		}
		v.Set(m)
	case reflect.TypeOf(map[string]time.Time{}):
		m := reflect.MakeMap(v.Type())
		for _, family := range []string{"issues", "issue_comment", "pull_request"} {
			m.SetMapIndex(reflect.ValueOf(family), reflect.ValueOf(fillTime))
		}
		v.Set(m)
	case reflect.TypeOf(setup.Install{}), reflect.TypeOf(setup.RoleState{}):
		for i := 0; i < v.NumField(); i++ {
			f.fill(t, v.Field(i), path+"."+v.Type().Field(i).Name)
		}
	default:
		t.Fatalf("setup.Install field %s has type %s, which this fill cannot set; "+
			"teach filledInstall this type — until then a value of it cannot be "+
			"shown to survive a save", path, v.Type())
	}
}

// openTest opens a store in a fresh temp directory and closes it when the test
// ends. Every store test starts from an empty database, so what it reads back is
// only ever what it wrote.
func openTest(t *testing.T) *Store {
	t.Helper()

	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

// fullInstall is an install with every field set, the shape a finished wizard
// leaves behind: three roles with their hub and Forgejo ids, a verified webhook
// and the events seen on it. Saving and listing it is the round-trip every part
// of the model has to survive — nested structs, maps, the int64 ids, the
// pointer and the timestamps included.
func fullInstall(slug string) setup.Install {
	return setup.Install{
		Slug:        slug,
		GroupID:     "g-1",
		BaseURL:     "https://devoper.duckdns.org/forgejo",
		Org:         "dpinsel",
		ConnectorID: "c-1",
		MCPServer:   "forgejo-mcp-" + slug,
		HookID:      42,
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleRefiner: {
				AgentID:   "refiner-" + slug,
				ImageID:   "forgejo-refiner-" + slug,
				PersonaID: "p-refiner",
				BotLogin:  "refiner-" + slug,
				TeamID:    7,
				Tools:     []string{"get_issue_by_index", "create_issue_comment"},
			},
			setup.RoleReviewer: {
				AgentID:   "reviewer-" + slug,
				ImageID:   "forgejo-reviewer-" + slug,
				PersonaID: "p-reviewer",
				BotLogin:  "forge-reviewer",
				TeamID:    9,
				Tools:     []string{"create_pull_review", "submit_pull_review"},
			},
		},
		ExcludeLabel:      "wip",
		WebhookVerifiedAt: &fillTime,
		Coverage: map[string]time.Time{
			"issues":       fillTime,
			"pull_request": fillTime,
		},
	}
}

// TestEveryInstallFieldRoundTrips is the drift guard on the model. It saves the
// reflection-filled install and requires it back unchanged, so a field added to
// setup.Install that cannot be written to the row — unexported, or tagged
// json:"-", or of a type the store cannot encode — fails here. Without it such a
// field would vanish on every Save with no compile error and no other test
// failing, and fullInstall cannot catch it on its own: it is hand-maintained, so
// it quietly keeps on omitting whatever was added.
func TestEveryInstallFieldRoundTrips(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	in := filledInstall(t, "drift-guard")
	if err := s.Save(ctx, in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List returned %d installs, want 1: %+v", len(got), got)
	}
	if !reflect.DeepEqual(got[0], in) {
		// Name the fields that got lost, so the failure says which field is
		// missing rather than just that the two installs differ.
		back := reflect.ValueOf(got[0])
		want := reflect.ValueOf(in)
		for i := 0; i < want.NumField(); i++ {
			if !reflect.DeepEqual(back.Field(i).Interface(), want.Field(i).Interface()) {
				t.Errorf("field %s did not round-trip: got %v, want %v",
					want.Type().Field(i).Name, back.Field(i).Interface(), want.Field(i).Interface())
			}
		}
	}
}

func TestSaveThenListRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	full := fullInstall("apps")
	// The sparse install is the one half way through a wizard: no roles assigned
	// yet, no org, no webhook, no exclude label. Its emptiness has to come back
	// as emptiness, not as an empty map or a stray default — the wizard reads
	// this state back to decide what still has to be created.
	sparse := setup.Install{
		GroupID:     "g-2",
		Slug:        "home",
		BaseURL:     "https://git.example.com",
		ConnectorID: "c-2",
	}

	for _, in := range []setup.Install{sparse, full} {
		if err := s.Save(ctx, in); err != nil {
			t.Fatalf("Save(%s): %v", in.Slug, err)
		}
	}

	got, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// List answers in slug order, so the wizard and the API show the same list
	// every time it is drawn.
	want := []setup.Install{full, sparse}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List returned\n%+v\nwant\n%+v", got, want)
	}
}

// TestSaveSameSlugUpdates is the upsert: the wizard re-saves an install as the
// admin moves through it, and a second Save of the same slug has to replace the
// row, never add a second install nobody asked for — and never keep anything the
// new install left empty.
func TestSaveSameSlugUpdates(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if err := s.Save(ctx, fullInstall("apps")); err != nil {
		t.Fatalf("Save(first): %v", err)
	}

	updated := fullInstall("apps")
	updated.BaseURL = "https://forge.example.com"
	updated.ConnectorID = "c-9"
	updated.HookID = 0
	updated.WebhookVerifiedAt = nil
	updated.Coverage = nil
	updated.Roles = map[setup.Role]setup.RoleState{
		setup.RoleRefiner: {AgentID: "other-refiner", BotLogin: "other-refiner"},
	}
	if err := s.Save(ctx, updated); err != nil {
		t.Fatalf("Save(second): %v", err)
	}

	got, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List returned %d installs, want 1: %+v", len(got), got)
	}
	if !reflect.DeepEqual(got[0], updated) {
		t.Errorf("List returned\n%+v\nwant\n%+v", got[0], updated)
	}
}

// TestListOnAnEmptyStore is the shape of "nothing installed yet": an empty list,
// not a nil one. A nil slice marshals to null, and the console and the API both
// read the list as a list — so the first admin to open the wizard gets [] and
// never has to distinguish "no installs" from "the store said nothing".
func TestListOnAnEmptyStore(t *testing.T) {
	s := openTest(t)

	got, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got == nil {
		t.Fatal("List on an empty store returned nil, want an empty slice")
	}
	if len(got) != 0 {
		t.Errorf("List on an empty store returned %d installs, want 0: %+v", len(got), got)
	}
}

// TestConnectorIDs covers the deduplication the cleanup flows depend on: three
// installs using two connectors name two connectors, and an install that has no
// connector yet names none. Both come out of the install's JSON, since the row
// holds the model rather than one column per field.
func TestConnectorIDs(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	a := fullInstall("apps") // connector c-1
	b := fullInstall("home") // c-1 as well
	c := fullInstall("lab")
	c.ConnectorID = "c-2"
	none := setup.Install{GroupID: "g-3", Slug: "draft"} // no connector yet

	for _, in := range []setup.Install{a, b, c, none} {
		if err := s.Save(ctx, in); err != nil {
			t.Fatalf("Save(%s): %v", in.Slug, err)
		}
	}

	got, err := s.ConnectorIDs(ctx)
	if err != nil {
		t.Fatalf("ConnectorIDs: %v", err)
	}
	want := []string{"c-1", "c-2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ConnectorIDs returned %q, want %q", got, want)
	}
}

func TestDelete(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	for _, in := range []setup.Install{fullInstall("apps"), fullInstall("home")} {
		if err := s.Save(ctx, in); err != nil {
			t.Fatalf("Save(%s): %v", in.Slug, err)
		}
	}

	if err := s.Delete(ctx, "apps"); err != nil {
		t.Fatalf("Delete(apps): %v", err)
	}

	got, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Slug != "home" {
		t.Errorf("List after Delete returned %+v, want only home", got)
	}

	// Deleting what is not there is not an error — the uninstall flow may run
	// twice, and the second run has nothing left to remove.
	if err := s.Delete(ctx, "apps"); err != nil {
		t.Errorf("Delete of a missing slug: %v", err)
	}
}

// TestReopenKeepsData is the point of the whole store: the pod restarts and the
// directory it mounted still holds its installs. It is also the guard on the
// migration below: dropping the old table on open may only ever happen to a
// database that still has the old shape, never to one that has the current one.
func TestReopenKeepsData(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(first): %v", err)
	}
	saved := fullInstall("apps")
	if err := s.Save(ctx, saved); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(second): %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close(reopened): %v", err)
		}
	})

	got, err := reopened.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], saved) {
		t.Errorf("List after reopen returned\n%+v\nwant one %+v", got, saved)
	}
}

// TestOpenDropsThePreReleaseTable covers the one migration the store has: a
// database written by an unreleased build, whose installs table still has one
// column per field of the old model, is dropped and recreated rather than
// migrated — the plugin has no users to migrate.
func TestOpenDropsThePreReleaseTable(t *testing.T) {
	dir := t.TempDir()

	// The old shape, written by hand: the columns the first wizard used, with a
	// row in it that the new model cannot read.
	db, err := sql.Open("sqlite", dir+"/forgejo.db")
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	const legacy = `
CREATE TABLE installs (
	slug          TEXT PRIMARY KEY,
	group_id      TEXT NOT NULL,
	base_url      TEXT NOT NULL,
	connector_id  TEXT NOT NULL,
	agents        TEXT NOT NULL,
	bots          TEXT NOT NULL,
	exclude_label TEXT NOT NULL,
	ci_sender     TEXT NOT NULL
);
INSERT INTO installs (slug, group_id, base_url, connector_id, agents, bots, exclude_label, ci_sender)
VALUES ('old', 'g-1', 'https://git.example.com', 'c-1', '{}', '{}', 'do-not-refine', 'ci-bot');`
	if _, err := db.Exec(legacy); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	// Opening it as the plugin does has to leave a working table in the new
	// shape: no error, no old row, and a save that reads back.
	s := openTestAt(t, dir)
	got, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List after opening a legacy database: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List returned %+v, want the old row dropped with the old table", got)
	}

	in := fullInstall("apps")
	if err := s.Save(context.Background(), in); err != nil {
		t.Fatalf("Save into the recreated table: %v", err)
	}
	back, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(back) != 1 || !reflect.DeepEqual(back[0], in) {
		t.Errorf("List returned %+v, want the saved install", back)
	}
}

// TestSubjectsRoundTrip is the event subject cache's own round trip: two subjects
// put together — one of them the zero subject, meaning "this event names no issue
// or PR" — come back exactly as they went in, and an id that was never put is
// simply absent rather than an error or a made-up zero.
func TestSubjectsRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	issue := forge.Subject{Repo: "o/r", Number: 1, Kind: forge.KindIssue, Title: "One", Body: "b1"}
	want := map[string]forge.Subject{"evt-1": issue, "evt-2": {}}
	if err := s.PutSubjects(ctx, want, fillTime); err != nil {
		t.Fatalf("PutSubjects: %v", err)
	}

	got, err := s.Subjects(ctx, []string{"evt-1", "evt-2", "evt-3"})
	if err != nil {
		t.Fatalf("Subjects: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Subjects returned %d subjects, want 2: %+v", len(got), got)
	}
	if !reflect.DeepEqual(got["evt-1"], issue) {
		t.Errorf("evt-1 came back as %+v, want %+v", got["evt-1"], issue)
	}
	if back, ok := got["evt-2"]; !ok || back != (forge.Subject{}) {
		t.Errorf("evt-2 came back as %+v (present: %v), want the zero subject", back, ok)
	}
	if _, ok := got["evt-3"]; ok {
		t.Errorf("Subjects returned a subject for an id that was never put: %+v", got["evt-3"])
	}
}

// TestSubjectsChunksLargeLookups covers the chunked IN list: a page of runs can
// name nearly a page of distinct events, and a lookup of far more ids than one
// chunk still has to answer for all of them.
func TestSubjectsChunksLargeLookups(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	const n = 1200
	want := make(map[string]forge.Subject, n)
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("evt-%d", i)
		want[id] = forge.Subject{Repo: "o/r", Number: i, Kind: forge.KindIssue, Title: id}
		ids = append(ids, id)
	}
	if err := s.PutSubjects(ctx, want, fillTime); err != nil {
		t.Fatalf("PutSubjects: %v", err)
	}

	got, err := s.Subjects(ctx, ids)
	if err != nil {
		t.Fatalf("Subjects: %v", err)
	}
	if len(got) != n {
		t.Fatalf("Subjects returned %d subjects, want %d", len(got), n)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Subjects did not return what was put")
	}
}

// TestPruneSubjects is the retention the cache depends on: a subject of an event
// old enough to have fallen out of any page's window is deleted, and a recent one
// survives — and the count names exactly what went.
func TestPruneSubjects(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	if err := s.PutSubjects(ctx, map[string]forge.Subject{
		"old": {},
	}, now.Add(-50*24*time.Hour)); err != nil {
		t.Fatalf("PutSubjects(old): %v", err)
	}
	if err := s.PutSubjects(ctx, map[string]forge.Subject{
		"recent": {Repo: "o/r", Number: 1, Kind: forge.KindIssue},
	}, now.Add(-10*24*time.Hour)); err != nil {
		t.Fatalf("PutSubjects(recent): %v", err)
	}

	deleted, err := s.PruneSubjects(ctx, now.Add(-45*24*time.Hour))
	if err != nil {
		t.Fatalf("PruneSubjects: %v", err)
	}
	if deleted != 1 {
		t.Errorf("PruneSubjects deleted %d rows, want 1", deleted)
	}

	got, err := s.Subjects(ctx, []string{"old", "recent"})
	if err != nil {
		t.Fatalf("Subjects: %v", err)
	}
	if _, ok := got["old"]; ok {
		t.Errorf("the row read 50 days ago survived a prune at 45 days")
	}
	if _, ok := got["recent"]; !ok {
		t.Errorf("the row read 10 days ago did not survive the prune")
	}
}

// TestSubjectsTableOnExistingDatabase covers the upgrade path: a database that
// was created before the event_subjects table existed is not recreated — the
// installs in it stay — and opening it with this code adds the missing table,
// because the schema runs IF NOT EXISTS on every open.
func TestSubjectsTableOnExistingDatabase(t *testing.T) {
	dir := t.TempDir()

	db, err := sql.Open("sqlite", dir+"/forgejo.db")
	if err != nil {
		t.Fatalf("open pre-subjects database: %v", err)
	}
	const old = `
CREATE TABLE IF NOT EXISTS installs (
	slug TEXT PRIMARY KEY,
	data TEXT NOT NULL
);`
	if _, err := db.Exec(old); err != nil {
		t.Fatalf("create the old schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s := openTestAt(t, dir)
	ctx := context.Background()

	want := map[string]forge.Subject{"evt-1": {Repo: "o/r", Number: 1, Kind: forge.KindIssue}}
	if err := s.PutSubjects(ctx, want, fillTime); err != nil {
		t.Fatalf("PutSubjects into a database opened over the old schema: %v", err)
	}
	got, err := s.Subjects(ctx, []string{"evt-1"})
	if err != nil {
		t.Fatalf("Subjects: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Subjects returned %+v, want %+v", got, want)
	}

	// The installs that were already in the database are still there: adding the
	// subjects table never touches them.
	if err := s.Save(ctx, fullInstall("apps")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	installs, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(installs) != 1 || installs[0].Slug != "apps" {
		t.Errorf("List returned %+v, want the install", installs)
	}
}

// openTestAt opens the store in a directory the test already prepared, and
// closes it when the test ends.
func openTestAt(t *testing.T, dir string) *Store {
	t.Helper()

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(%q): %v", dir, err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}
