package cutover

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/forgejo"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
)

// The cutover is a once-at-start handover with no second chances of its own: these rows
// pin that it deletes exactly what a legacy row tracks, keeps the row when a delete is
// pending so the next start retries it, drops it when nothing is left to retry, leaves
// everything the row never named standing, and finds nothing to do the second time around.

// --- fakes -------------------------------------------------------------------

type fakeStore struct {
	installs  []setup.Install
	dropped   []string
	saved     []setup.Install
	deleteErr map[string]error // by slug
	saveErr   map[string]error // by slug
	listErr   error
}

func (s *fakeStore) List(ctx context.Context) ([]setup.Install, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return slices.Clone(s.installs), nil
}

func (s *fakeStore) Save(ctx context.Context, in setup.Install) error {
	if err := s.saveErr[in.Slug]; err != nil {
		return err
	}
	s.saved = append(s.saved, in)
	for i, existing := range s.installs {
		if existing.Slug == in.Slug {
			s.installs[i] = in
			return nil
		}
	}
	s.installs = append(s.installs, in)
	return nil
}

func (s *fakeStore) Delete(ctx context.Context, slug string) error {
	if err := s.deleteErr[slug]; err != nil {
		return err
	}
	s.dropped = append(s.dropped, slug)
	var rest []setup.Install
	for _, in := range s.installs {
		if in.Slug != slug {
			rest = append(rest, in)
		}
	}
	s.installs = rest
	return nil
}

type fakeHub struct {
	triggers      []setup.Trigger
	refuse        map[string]error // by resource id, refused on every attempt
	refuseOnce    map[string]error // by resource id, refused on its first attempt only
	deleted       []string
	attempts      map[string]int
	listError     error
	listErrorOnce bool
}

func (h *fakeHub) attempt(key string) int {
	if h.attempts == nil {
		h.attempts = map[string]int{}
	}
	h.attempts[key]++
	return h.attempts[key]
}

func (h *fakeHub) ListTriggers(ctx context.Context, connectorID string) ([]setup.Trigger, error) {
	if h.attempt("list") > 1 && h.listErrorOnce {
		return h.triggers, nil
	}
	if h.listError != nil {
		return nil, h.listError
	}
	return h.triggers, nil
}

func (h *fakeHub) del(kind, id string) error {
	attempt := h.attempt(id)
	if err := h.refuse[id]; err != nil {
		return err
	}
	if err := h.refuseOnce[id]; err != nil && attempt == 1 {
		return err
	}
	h.deleted = append(h.deleted, kind+":"+id)
	return nil
}

func (h *fakeHub) DeleteTrigger(ctx context.Context, id string) error { return h.del("trigger", id) }
func (h *fakeHub) DeleteConnector(ctx context.Context, id string) error {
	return h.del("connector", id)
}
func (h *fakeHub) DeleteAgent(ctx context.Context, id string) error      { return h.del("agent", id) }
func (h *fakeHub) DeleteAgentImage(ctx context.Context, id string) error { return h.del("image", id) }
func (h *fakeHub) DeletePersona(ctx context.Context, id string) error    { return h.del("persona", id) }
func (h *fakeHub) DeleteMCPServer(ctx context.Context, id string) error  { return h.del("mcp", id) }

type fakeForgejo struct {
	refuse  map[string]error // by "hook:1", "team:2", "user:login"
	deleted []string
}

func (f *fakeForgejo) DeleteOrgHook(ctx context.Context, org string, id int64) error {
	key := fmt.Sprintf("hook:%d", id)
	return f.del(key)
}
func (f *fakeForgejo) DeleteTeam(ctx context.Context, id int64) error {
	return f.del(fmt.Sprintf("team:%d", id))
}

func (f *fakeForgejo) DeleteUser(ctx context.Context, login string) error {
	return f.del("user:" + login)
}

func (f *fakeForgejo) del(key string) error {
	if err := f.refuse[key]; err != nil {
		return err
	}
	f.deleted = append(f.deleted, key)
	return nil
}

// --- fixtures ----------------------------------------------------------------

// legacyRow is a complete install as the old wizard left it: LayoutVersion unset,
// every hub id and forge id the row can name filled in.
func legacyRow() setup.Install {
	return setup.Install{
		Slug:        "apps",
		Org:         "acme",
		ConnectorID: "c-1",
		MCPServer:   "forgejo-mcp-apps",
		HookID:      77,
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleRefiner: {
				BotLogin: "refiner-apps", TeamID: 1, PersonaID: "persona-1", ImageID: "image-1", AgentID: "agent-1",
			},
			setup.RoleDeveloper: {
				BotLogin: "developer-apps", TeamID: 2, PersonaID: "persona-2", ImageID: "image-2", AgentID: "agent-2",
			},
			setup.RoleReviewer: {
				BotLogin: "reviewer-apps", TeamID: 3, PersonaID: "persona-3", ImageID: "image-3", AgentID: "agent-3",
			},
		},
	}
}

func newCutover(store *fakeStore, h *fakeHub, fg forgejoOps) (*Cutover, *[]string) {
	var lines []string
	c := Cutover{
		Hub:     h,
		Forgejo: fg,
		Store:   store,
		Log:     func(msg string) { lines = append(lines, msg) },
	}
	return &c, &lines
}

func logged(lines []string, want string) bool {
	for _, l := range lines {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

// --- tests -------------------------------------------------------------------

// TestRunRetiresLegacyInstall is the happy path: every resource the row tracks
// by id is deleted on both sides, the row goes, and the log names the install.
// A trigger belonging to another install shares the connector and is left alone.
func TestRunRetiresLegacyInstall(t *testing.T) {
	store := &fakeStore{installs: []setup.Install{legacyRow()}}
	h := &fakeHub{triggers: []setup.Trigger{
		{ID: "t-1", Name: "forgejo-refiner-issue-opened-apps"},
		{ID: "t-2", Name: "forgejo-developer-mention-apps"},
		{ID: "t-9", Name: "forgejo-refiner-issue-opened-other"}, // another install's
	}}
	fg := &fakeForgejo{}
	c, lines := newCutover(store, h, fg)

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	wantDeleted := []string{
		"trigger:t-1", "trigger:t-2",
		"connector:c-1",
		"agent:agent-1", "agent:agent-2", "agent:agent-3",
		"image:image-1", "image:image-2", "image:image-3",
		"persona:persona-1", "persona:persona-2", "persona:persona-3",
		"mcp:forgejo-mcp-apps",
	}
	for _, w := range wantDeleted {
		if !slices.Contains(h.deleted, w) {
			t.Errorf("%s was not deleted; deleted = %v", w, h.deleted)
		}
	}
	if slices.Contains(h.deleted, "trigger:t-9") {
		t.Errorf("another install's trigger was deleted — the cutover takes nothing it did not name")
	}

	wantForge := []string{"hook:77", "team:1", "team:2", "team:3",
		"user:refiner-apps", "user:developer-apps", "user:reviewer-apps"}
	for _, w := range wantForge {
		if !slices.Contains(fg.deleted, w) {
			t.Errorf("%s was not deleted; deleted = %v", w, fg.deleted)
		}
	}

	if !slices.Equal(store.dropped, []string{"apps"}) {
		t.Errorf("dropped rows = %v, want [apps]", store.dropped)
	}
	if !logged(*lines, `legacy install "apps" retired`) {
		t.Errorf("log = %q, want a retirement line naming the install", *lines)
	}
}

// TestRunRefusedDeleteKeepsRowRetiring is the bug the cutover had: the hub says no to one
// agent, and the row used to go anyway — taking with it the only names the plugin had for
// what was still standing. Now the row stays, stamped retiring, and the next start retries.
func TestRunRefusedDeleteKeepsRowRetiring(t *testing.T) {
	store := &fakeStore{installs: []setup.Install{legacyRow()}}
	h := &fakeHub{
		triggers: []setup.Trigger{{ID: "t-1", Name: "forgejo-refiner-mention-apps"}},
		refuse:   map[string]error{"agent-2": errors.New("403 forbidden")},
	}
	fg := &fakeForgejo{}
	c, lines := newCutover(store, h, fg)

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !slices.Contains(h.deleted, "agent:agent-1") {
		t.Errorf("the refused sibling was not attempted after all: %v", h.deleted)
	}
	if slices.Contains(h.deleted, "agent:agent-2") {
		t.Error("the refused agent was reported as deleted")
	}
	if len(store.dropped) != 0 {
		t.Errorf("dropped rows = %v, want none: a pending delete keeps the row", store.dropped)
	}
	if len(store.saved) != 1 || !store.saved[0].Retiring {
		t.Errorf("saved rows = %+v, want the install saved with Retiring set", store.saved)
	}
	if !logged(*lines, "pending") || !logged(*lines, "403 forbidden") {
		t.Errorf("log = %q, want the refusal named as pending with its reason", *lines)
	}
	if !logged(*lines, `legacy install "apps" retired`) {
		t.Errorf("log = %q, want the first pass named as a retirement", *lines)
	}
	if !logged(*lines, "1 kept retiring for the next start") {
		t.Errorf("log = %q, want the summary counting the retiring row", *lines)
	}
}

// TestRunRetriesRetiringRowAndDropsItOnceNothingIsPending: the retry is the same pass, so
// a hub that answers the second time lets the row go — and the log says retry, not retired.
func TestRunRetriesRetiringRowAndDropsItOnceNothingIsPending(t *testing.T) {
	row := legacyRow()
	row.Retiring = true
	store := &fakeStore{installs: []setup.Install{row}}
	h := &fakeHub{
		refuse:        map[string]error{},
		refuseOnce:    map[string]error{"agent-2": errors.New("hub unreachable")},
		listErrorOnce: true,
		listError:     errors.New("hub unreachable"),
	}
	fg := &fakeForgejo{}
	c, lines := newCutover(store, h, fg)

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if len(store.dropped) != 0 {
		t.Fatalf("the first pass dropped the row despite the failures: %v", store.dropped)
	}

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if !slices.Equal(store.dropped, []string{"apps"}) {
		t.Errorf("dropped rows = %v, want [apps] once every delete succeeded", store.dropped)
	}
	if !logged(*lines, `legacy install "apps" retry`) {
		t.Errorf("log = %q, want the second pass named as a retry", *lines)
	}
}

// TestRunCredentialFailureIsPendingNotAlreadyGone is the incident regression: the hub's
// background-credential endpoint answers 404 while a rollout is still bringing it up, so
// every delete fails before it reaches its resource. That 404 is the endpoint's, not the
// resource's — reading it as "already gone" is what orphaned 26 resources.
func TestRunCredentialFailureIsPendingNotAlreadyGone(t *testing.T) {
	mintFailed := &hub.CredentialError{Err: &hub.StatusError{Code: http.StatusNotFound}}
	store := &fakeStore{installs: []setup.Install{legacyRow()}}
	h := &fakeHub{refuse: map[string]error{
		"c-1":              mintFailed,
		"agent-1":          mintFailed,
		"agent-2":          mintFailed,
		"agent-3":          mintFailed,
		"image-1":          mintFailed,
		"image-2":          mintFailed,
		"image-3":          mintFailed,
		"persona-1":        mintFailed,
		"persona-2":        mintFailed,
		"persona-3":        mintFailed,
		"forgejo-mcp-apps": mintFailed,
	}}
	fg := &fakeForgejo{}
	c, lines := newCutover(store, h, fg)

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(store.dropped) != 0 {
		t.Errorf("dropped rows = %v, want none: a credential mint that 404s is not a resource that is gone", store.dropped)
	}
	if len(store.saved) != 1 || !store.saved[0].Retiring {
		t.Errorf("saved rows = %+v, want the install kept retiring", store.saved)
	}
	if !logged(*lines, "pending, will retry") {
		t.Errorf("log = %q, want the mint failures named as pending", *lines)
	}
}

// TestRunDeleteNotFoundCountsAsDeleted: a resource someone removed out of band is a
// resource that is done. Counting it as pending would pin the row to it forever.
func TestRunDeleteNotFoundCountsAsDeleted(t *testing.T) {
	store := &fakeStore{installs: []setup.Install{legacyRow()}}
	h := &fakeHub{refuse: map[string]error{"agent-2": &hub.StatusError{Code: http.StatusNotFound}}}
	fg := &fakeForgejo{refuse: map[string]error{"team:2": &forgejo.StatusError{Code: http.StatusNotFound}}}
	c, lines := newCutover(store, h, fg)

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !slices.Equal(store.dropped, []string{"apps"}) {
		t.Errorf("dropped rows = %v, want [apps]: a 404 on the delete itself is a completed deletion", store.dropped)
	}
	if !logged(*lines, "already gone") {
		t.Errorf("log = %q, want the two 404s reported as already gone", *lines)
	}
}

// TestRunForgejoFailureKeepsRow: the forge side is tracked too — a bot the forge refuses
// to delete keeps the row, a plain error on a team does as well.
func TestRunForgejoFailureKeepsRow(t *testing.T) {
	store := &fakeStore{installs: []setup.Install{legacyRow()}}
	h := &fakeHub{}
	fg := &fakeForgejo{refuse: map[string]error{"user:reviewer-apps": errors.New("403 forbidden")}}
	c, lines := newCutover(store, h, fg)

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(store.dropped) != 0 {
		t.Errorf("dropped rows = %v, want none", store.dropped)
	}
	if !logged(*lines, "user:reviewer-apps") {
		t.Errorf("log = %q, want the refused bot named as pending", *lines)
	}
}

// TestRunTriggerListFailureKeepsRow: without the trigger list the cutover cannot say its
// triggers are gone, so nothing about the install is finished.
func TestRunTriggerListFailureKeepsRow(t *testing.T) {
	store := &fakeStore{installs: []setup.Install{legacyRow()}}
	h := &fakeHub{listError: errors.New("hub unreachable")}
	fg := &fakeForgejo{}
	c, lines := newCutover(store, h, fg)

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(store.dropped) != 0 {
		t.Errorf("dropped rows = %v, want none", store.dropped)
	}
	if !logged(*lines, "triggers:list failed") {
		t.Errorf("log = %q, want the failed list named as pending", *lines)
	}
}

// TestRunRetiringManagedRowIsStillRetried: the layout marker says a row has nothing to
// hand over, but a row stamped retiring by the cutover always has — the guard is the
// marker and the stamp together, so a stamp alone keeps it in the pass.
func TestRunRetiringManagedRowIsStillRetried(t *testing.T) {
	row := legacyRow()
	row.Slug = "stamped"
	row.LayoutVersion = setup.LayoutManagedAgents
	row.Retiring = true
	store := &fakeStore{installs: []setup.Install{row}}
	h := &fakeHub{}
	fg := &fakeForgejo{}
	c, _ := newCutover(store, h, fg)

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !slices.Equal(store.dropped, []string{"stamped"}) {
		t.Errorf("dropped rows = %v, want [stamped]", store.dropped)
	}
}

// TestRunKeepsRowWhenItCannotBeStampedRetiring: the row has to survive to be retried, and
// a store that will not write it is reported the same way an undroppable row is.
func TestRunKeepsRowWhenItCannotBeStampedRetiring(t *testing.T) {
	store := &fakeStore{
		installs: []setup.Install{legacyRow()},
		saveErr:  map[string]error{"apps": errors.New("store is sealed")},
	}
	h := &fakeHub{refuse: map[string]error{"agent-2": errors.New("hub unreachable")}}
	fg := &fakeForgejo{}
	c, lines := newCutover(store, h, fg)

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run must not fail on a row it cannot keep: %v", err)
	}
	if len(store.dropped) != 0 {
		t.Errorf("the row was dropped despite the pending delete and the failed save")
	}
	if !logged(*lines, "cannot be stamped retiring") {
		t.Errorf("log = %q, want the failed stamp announced", *lines)
	}
}

// TestRunWithNoForgejoLeavesForgeStanding: a plugin that was never given an admin
// token cannot reach the forge. The hub side still goes; the forge side is named
// in the log as left standing — and the row still drops.
func TestRunWithNoForgejoLeavesForgeStanding(t *testing.T) {
	store := &fakeStore{installs: []setup.Install{legacyRow()}}
	h := &fakeHub{}
	c, lines := newCutover(store, h, nil)

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(store.dropped) != 1 {
		t.Fatalf("dropped rows = %v, want [apps]", store.dropped)
	}
	for _, want := range []string{"hook:77", "team:1", "user:refiner-apps"} {
		if !logged(*lines, want) {
			t.Errorf("log = %q, want %s named as left standing", *lines, want)
		}
	}
	if !logged(*lines, "no Forgejo admin token") {
		t.Errorf("log = %q, want the reason the forge side stands", *lines)
	}
	if !logged(*lines, "left standing (skipped)") {
		t.Errorf("log = %q, want the deliberate skips named as skipped, not pending", *lines)
	}
	if logged(*lines, "pending, will retry") {
		t.Errorf("log = %q, want nothing counted as pending: a missing admin token is a choice, not a failure", *lines)
	}
}

// TestRunSkipsManagedRowsAndIsANoOpOnSecondRun: the pass leaves rows created
// under the new layout alone, and once the legacy rows are gone a second start
// has nothing to do — no deletes, no drops, no retirement lines.
func TestRunSkipsManagedRowsAndIsANoOpOnSecondRun(t *testing.T) {
	managed := legacyRow()
	managed.Slug = "newer"
	managed.LayoutVersion = setup.LayoutManagedAgents

	store := &fakeStore{installs: []setup.Install{legacyRow(), managed}}
	h := &fakeHub{}
	fg := &fakeForgejo{}
	c, _ := newCutover(store, h, fg)

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if !slices.Equal(store.dropped, []string{"apps"}) {
		t.Fatalf("dropped = %v, want only the legacy row [apps]", store.dropped)
	}

	before := len(h.deleted)
	var lines []string
	c.Log = func(msg string) { lines = append(lines, msg) }
	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	if len(store.dropped) != 1 {
		t.Errorf("second run dropped rows: %v", store.dropped)
	}
	if len(h.deleted) != before {
		t.Errorf("second run deleted more hub resources: %v", h.deleted[before:])
	}
	for _, l := range lines {
		if strings.Contains(l, "retired") {
			t.Errorf("second run logged a retirement: %q", l)
		}
	}
}

// TestRunKeepsRowWhenStoreRefusesDrop is the crash-shape: the resources are
// deleted but the row cannot be dropped. Run reports no error, says so in the
// log, and the next start re-runs the row — finding its resources already gone.
func TestRunKeepsRowWhenStoreRefusesDrop(t *testing.T) {
	row := legacyRow()
	store := &fakeStore{
		installs:  []setup.Install{row},
		deleteErr: map[string]error{"apps": errors.New("store is sealed")},
	}
	h := &fakeHub{}
	fg := &fakeForgejo{}
	c, lines := newCutover(store, h, fg)

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run must not fail on a row it cannot drop: %v", err)
	}
	if len(store.dropped) != 0 {
		t.Errorf("row was dropped despite the refusal")
	}
	if !logged(*lines, "row kept, cannot be dropped") {
		t.Errorf("log = %q, want the kept row announced", *lines)
	}
	if !logged(*lines, "0 legacy installs dropped, 0 kept retiring for the next start, 1 rows kept") {
		t.Errorf("log = %q, want the summary counting the kept row", *lines)
	}
}

// TestRunFailsOnlyWhenTheStoreCannotBeRead: with no rows there is nothing to
// say about them, and the next start tries again.
func TestRunFailsOnlyWhenTheStoreCannotBeRead(t *testing.T) {
	store := &fakeStore{listErr: errors.New("store is down")}
	c, _ := newCutover(store, &fakeHub{}, &fakeForgejo{})

	err := c.Run(context.Background())
	if err == nil {
		t.Fatal("Run returned nil, want the store failure reported")
	}
	if !strings.Contains(err.Error(), "store is down") {
		t.Errorf("error = %q, want it to wrap the store failure", err)
	}
}
