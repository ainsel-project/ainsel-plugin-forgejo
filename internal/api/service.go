// Package api holds the plugin's backend HTTP handlers — the routes declared by
// `api.basePath` in the manifest. The console reaches them only through the AInsel hub
// proxy (which mounts them under /api/v1/plugins/<id>/api), but they are plain handlers with
// no hub-specific assumptions, so they run unchanged when the plugin is started on its own.
package api

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/forge"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/store"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/work"
)

// defaultWorkWindow is how far back work is read when the caller does not say. A month is
// long enough to hold the work an admin is asking about and short enough that the answer is
// a page rather than an archive.
const defaultWorkWindow = 30 * 24 * time.Hour

// ErrHubNotConfigured is the service saying it read nothing because there is no hub to read
// it from — a plugin started without HUB_URL. It is an error rather than an empty list so a
// console cannot mistake "nothing was read" for "nothing happened".
var ErrHubNotConfigured = errors.New("api: no hub is configured")

// errStore marks a failure of the plugin's own install store. It matters because it is the
// one failure on this path that is neither the hub's answer nor the caller's fault, and gets
// an answer of its own rather than a 502 that blames a hub that is fine.
var errStore = errors.New("api: install store")

// errSettings marks a failure of the plugin's own settings store, for the same
// reason errStore exists: the plugin's SQLite is neither the hub's answer nor
// the caller's fault, and a 502 blaming a fine hub would send the admin to the
// wrong service.
var errSettings = errors.New("api: settings store")

// WorkService is the plugin's read path to the work AInsel did on Forgejo: the runs the hub
// recorded for this plugin's connectors, grouped into the issues and pull requests they were
// about, each with the page on the forge a reader can open.
//
// It is shared by design: the API routes in this package and the MCP tools both ask it the
// same questions, and neither talks to the hub or to the install store on its own. Every
// method is called as the caller on the context (hub.WithCredential) — the hub then answers
// with exactly what that caller may see. There is no background read here: showing a caller
// work nobody asked for is not what these routes are for.
type WorkService struct {
	// hub is the hub to read runs from, or nil when there is no hub to read from —
	// which every method answers with ErrHubNotConfigured rather than with an empty
	// list that would read like a quiet month.
	hub hub.Client

	// store is what says which connectors are this plugin's work, and which role each
	// agent plays.
	store *store.Store

	// base is the fallback Forgejo address every issue, pull and repository link
	// is built from — the one configured for the plugin (FORGEJO_BASE_URL). Each
	// install names its own address, and the links are built from that; this is
	// what an install whose row carries no address (or a plugin whose instance
	// list came from the env var alone) still links against.
	base string

	// now is the clock the default window is measured against. A field so a test can
	// name the moment instead of waiting for one.
	now func() time.Time
}

// NewWorkService builds the read path: the hub to ask, the installs that say which work is
// this plugin's, and the Forgejo the links point at.
func NewWorkService(h hub.Client, s *store.Store, base string) *WorkService {
	return &WorkService{hub: h, store: s, base: base, now: time.Now}
}

// Filter narrows a Work read. An empty field names no filter rather than an empty value.
type Filter struct {
	// Repo is a repository's full name, "owner/repo".
	Repo string

	// Agent is an agent's name — the runs of that agent and no other.
	Agent string

	// Since is how far back to read. The zero time is the default window.
	Since time.Time
}

// NowRun is a run that has not finished, with the pages on the forge it is about: the issue
// or pull request it is working on, and — when it is working on a pull request that names the
// issue it closes — that issue too, because that is the thread a reader is waiting on.
type NowRun struct {
	work.Run

	URL string

	// IssueURL is empty unless the run's subject is a pull request that says which
	// issue it works on.
	IssueURL string
}

// InvocationWork is the answer to "what is this run about, and what has the rest of the work
// on it cost?": the thread the run belongs to, and what the runs around it add up to.
type InvocationWork struct {
	Item work.Item

	// SiblingsCostUSD is the cost of the item's other runs — the run in question is
	// left out, because the slot showing it is already about that one.
	SiblingsCostUSD float64

	// SiblingsCostKnown is false when any of those runs was never priced, which makes
	// the total unknown rather than short.
	SiblingsCostKnown bool
}

// baseFor resolves the Forgejo address one run's links are built from: the
// address of the install whose connector received the run, falling back to the
// plugin's configured address when no install names one — so a run is linked on
// the forge it happened on, not on whichever host the environment named.
func baseFor(installs []setup.Install, fallback string) func(work.Run) string {
	byConnector := make(map[string]string, len(installs))
	for _, in := range installs {
		if in.ConnectorID == "" || in.BaseURL == "" {
			continue
		}
		byConnector[in.ConnectorID] = in.BaseURL
	}
	return func(r work.Run) string {
		if base, ok := byConnector[r.Connector]; ok {
			return base
		}
		return fallback
	}
}

// Now returns the runs that are still going, newest first — queued and running, which is what
// AInsel calls work in progress. A run whose event has left the hub carries no subject and so
// no URL: the run is still happening, and the honest answer is that nothing says where.
func (s *WorkService) Now(ctx context.Context) ([]NowRun, error) {
	return s.activeSince(ctx, time.Time{})
}

// activeSince is Now read over a window the caller names: the same runs, the same pages,
// measured back from now to the moment the caller asked for. The zero moment is the default
// window, the same one Work answers with.
func (s *WorkService) activeSince(ctx context.Context, since time.Time) ([]NowRun, error) {
	if since.IsZero() {
		since = s.now().Add(-defaultWorkWindow)
	}
	// The now page shows work that has not finished: there is no cost to show on it, so
	// the token metrics are not read for it.
	runs, installs, err := s.runs(ctx, since, false)
	if err != nil {
		return nil, err
	}
	linkBase := baseFor(installs, s.base)

	out := make([]NowRun, 0, len(runs))
	for _, r := range work.Active(runs) {
		base := linkBase(r)
		nr := NowRun{Run: r, URL: forge.SubjectURL(base, r.Subject)}
		// The issue a pull request is closing is worth a link of its own on the
		// Now page: the pull is the work, the issue is the reason.
		if r.Subject.Kind == forge.KindPull {
			if number, ok := work.LinkedIssue(r.Subject.Title, r.Subject.Branch, r.Subject.Body); ok {
				nr.IssueURL = forge.IssueURL(base, r.Subject.Repo, number)
			}
		}
		out = append(out, nr)
	}
	return out, nil
}

// Work returns the repositories with work in them, newest activity first, as work.Build
// groups them: one item per issue or pull request, and every pull request filed under the
// issue it references.
//
// The filters are applied to the runs before that grouping rather than to the answer after
// it, so a filtered list is still a true picture of what is left — an agent's own work, with
// its pulls still nested in the issues they close — and not a full thread with the other
// agents' runs cut out of the middle of it.
func (s *WorkService) Work(ctx context.Context, f Filter) ([]work.RepoGroup, error) {
	since := f.Since
	if since.IsZero() {
		since = s.now().Add(-defaultWorkWindow)
	}

	runs, installs, err := s.runs(ctx, since, true)
	if err != nil {
		return nil, err
	}
	if f.Agent != "" {
		runs = runsOfAgent(runs, f.Agent)
	}

	groups := work.BuildWithBase(runs, baseFor(installs, s.base))
	if f.Repo != "" {
		groups = groupsWithRepo(groups, f.Repo)
	}
	return groups, nil
}

// Item returns the one issue or pull request a repository is being asked about, including a
// pull request the work list nests under its issue — which is why this searches the nested
// pulls and not only the top level. It returns nil, with no error, when nothing was run under
// that number: "no work here" is the answer, not a failure.
//
// A repository numbers its issues and its pull requests apart, so a number can name one of
// each. The most recently active of them answers, and its subject says which it is; a caller
// that means the other one reads kind off the item it gets back.
func (s *WorkService) Item(ctx context.Context, repo string, number int) (*work.Item, error) {
	groups, err := s.Work(ctx, Filter{Repo: repo})
	if err != nil {
		return nil, err
	}
	// A repository is one group, or none when nothing was run in it.
	if len(groups) == 0 {
		return nil, nil
	}
	for _, it := range readingOrder(groups[0].Items) {
		if it.Subject.Number == number {
			found := it
			return &found, nil
		}
	}
	return nil, nil
}

// AgentWork returns the threads one agent has touched, newest first — the answer to the
// question an agent's panel asks. The pulls stay nested in the issues their runs close, so the
// list reads as work rather than as a run log.
func (s *WorkService) AgentWork(ctx context.Context, agent string) ([]work.Item, error) {
	groups, err := s.Work(ctx, Filter{Agent: agent})
	if err != nil {
		return nil, err
	}
	items := make([]work.Item, 0, len(groups))
	for _, g := range groups {
		items = append(items, g.Items...)
	}
	return items, nil
}

// InvocationWork returns the item a run belongs to and what the item's other runs have cost.
// It returns nil, with no error, when the run is not in this plugin's work: it belongs to
// another connector's forge, or its event carried no subject, and either way there is nothing
// here to say what it was about.
func (s *WorkService) InvocationWork(ctx context.Context, id string) (*InvocationWork, error) {
	runs, installs, err := s.runs(ctx, s.now().Add(-defaultWorkWindow), true)
	if err != nil {
		return nil, err
	}

	// The run has to be one this plugin's installs received before anything is said
	// about it: the work list below would drop another connector's run anyway, and
	// finding it dropped is not the same as never having been asked.
	if !holdsRun(runs, id) {
		return nil, nil
	}

	for _, g := range work.BuildWithBase(runs, baseFor(installs, s.base)) {
		for _, it := range readingOrder(g.Items) {
			if !holdsRun(it.Runs, id) {
				continue
			}
			cost, known := costOf(siblingsOf(it, id))
			return &InvocationWork{
				Item:              it,
				SiblingsCostUSD:   cost,
				SiblingsCostKnown: known,
			}, nil
		}
	}
	// The run is ours but shows no work: its subject has no number, so there is no
	// thread to put it in.
	return nil, nil
}

// -- the reads every answer is built from ---------------------------------------------

// runs reads the runs of this plugin's installs since a moment, and says which role each
// one's agent plays.
//
// Which connectors to read comes from the install store rather than from the caller: these are
// the forges the admin set this plugin up on, and the plugin shows no work from a forge it was
// never installed on — including a forge the caller could otherwise see on their own. Which
// agents to read comes from the same rows: the plugin's work is these agents' work, and the hub
// can read one agent's invocations instead of every agent's.
//
// costs says whether the answer is a page that shows what a run cost. A page that does not —
// the now page — asks for no costs and the hub's metrics endpoint is never called.
//
// The installs come back with the runs because the links are built from them: each run's
// connector names the install, and the install names the Forgejo the run's pages live on.
func (s *WorkService) runs(ctx context.Context, since time.Time, costs bool) ([]work.Run, []setup.Install, error) {
	if s.hub == nil {
		return nil, nil, ErrHubNotConfigured
	}

	// One read of the installs: their connectors are the set to read runs for — the
	// same set store.ConnectorIDs answers with — and their agents are what says which
	// role a run's agent was playing.
	installs, err := s.installs(ctx)
	if err != nil {
		return nil, nil, err
	}
	connectors := connectorsOf(installs)
	if len(connectors) == 0 {
		// No install, no forge, and nothing to ask the hub about: an empty answer
		// that asked for nothing, rather than an error.
		return []work.Run{}, installs, nil
	}

	// The install store is also the subject cache the page reads through: an event's
	// subject was derived once and lives here now, so the hub is asked only for the
	// events this store has never seen. The interface is named only over a store that
	// is there — a nil *store.Store in a non-nil interface would hand Runs a cache
	// that panics the moment it is asked anything.
	var cache hub.SubjectCache
	if s.store != nil {
		cache = s.store
	}

	runs, err := s.hub.Runs(ctx, hub.RunsQuery{
		Connectors: connectors,
		Agents:     agentsOf(installs),
		Since:      since,
		Costs:      costs,
		Cache:      cache,
	})
	if err != nil {
		return nil, nil, err
	}

	// The hub has no notion of a role — it knows agents — so the install that names
	// an agent is what says whether it was refining, developing or reviewing (ruling
	// R18).
	roles := rolesOf(installs)
	for i := range runs {
		if role, ok := roles[runs[i].Agent]; ok {
			runs[i].Role = role
		}
	}
	return runs, installs, nil
}

// installs reads what the wizard saved. No installs is no rows, not an error. A
// retiring row is skipped: its connector and agents are being torn down, so its
// runs are not work the console can act on.
func (s *WorkService) installs(ctx context.Context) ([]setup.Install, error) {
	if s.store == nil {
		return nil, nil
	}
	installs, err := s.store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errStore, err)
	}
	visible := installs[:0]
	for _, in := range installs {
		if in.Retiring {
			continue
		}
		visible = append(visible, in)
	}
	return visible, nil
}

// connectorsOf names each connector the installs point at once, and names nothing for an
// install that has no connector yet.
func connectorsOf(installs []setup.Install) []string {
	var out []string
	seen := make(map[string]bool, len(installs))
	for _, in := range installs {
		if in.ConnectorID == "" || seen[in.ConnectorID] {
			continue
		}
		seen[in.ConnectorID] = true
		out = append(out, in.ConnectorID)
	}
	return out
}

// agentsOf names each agent the installs' roles point at once, sorted so the read scope a
// caller asks for does not depend on the order a map happened to iterate in. An install
// that has not been given an agent for a role yet names nothing: there is no agent whose
// invocations to read.
func agentsOf(installs []setup.Install) []string {
	var out []string
	seen := make(map[string]bool, len(installs))
	for _, in := range installs {
		for _, st := range in.Roles {
			if st.AgentID == "" || seen[st.AgentID] {
				continue
			}
			seen[st.AgentID] = true
			out = append(out, st.AgentID)
		}
	}
	sort.Strings(out)
	return out
}

// rolesOf maps each agent's name to the role the install gives it. An agent named by two
// installs keeps the first one's role: installs are read in slug order, so the answer at least
// does not change between calls.
func rolesOf(installs []setup.Install) map[string]string {
	roles := make(map[string]string)
	for _, in := range installs {
		for role, st := range in.Roles {
			if st.AgentID == "" {
				continue
			}
			if _, ok := roles[st.AgentID]; !ok {
				roles[st.AgentID] = string(role)
			}
		}
	}
	return roles
}

// -- the shapes the answers are read out of --------------------------------------------

// runsOfAgent is the runs one agent ran, in the order they came.
func runsOfAgent(runs []work.Run, agent string) []work.Run {
	out := make([]work.Run, 0, len(runs))
	for _, r := range runs {
		if r.Agent == agent {
			out = append(out, r)
		}
	}
	return out
}

// groupsWithRepo is the one group for a repository, or none.
func groupsWithRepo(groups []work.RepoGroup, repo string) []work.RepoGroup {
	out := make([]work.RepoGroup, 0, 1)
	for _, g := range groups {
		if g.Repo == repo {
			out = append(out, g)
		}
	}
	return out
}

// readingOrder lists a repository's items the way the work list shows them: each top-level
// thread, then the pulls filed under it. It is what lets a route that asks for one number find
// a pull request the list itself keeps nested.
func readingOrder(items []work.Item) []work.Item {
	out := make([]work.Item, 0, len(items))
	for _, it := range items {
		out = append(out, it)
		out = append(out, it.PRs...)
	}
	return out
}

// holdsRun reports whether one of these runs is the one asked about.
func holdsRun(runs []work.Run, id string) bool {
	for _, r := range runs {
		if r.ID == id {
			return true
		}
	}
	return false
}

// siblingsOf is an item's runs other than the run asked about — the rest of the work on the
// same issue or pull request.
func siblingsOf(it work.Item, id string) []work.Run {
	out := make([]work.Run, 0, len(it.Runs))
	for _, r := range it.Runs {
		if r.ID != id {
			out = append(out, r)
		}
	}
	return out
}

// costOf adds up the runs whose cost was measured and reports whether all of them were — the
// same pair work.Build reports for an item's own runs, here for the siblings' round. One
// unpriced run makes the total unknown rather than short: a cost nobody measured is an
// absence, not a zero, and a slot that shows one as $0.00 tells the admin their agent was free.
func costOf(runs []work.Run) (float64, bool) {
	var total float64
	known := true
	for _, r := range runs {
		if !r.CostKnown {
			known = false
			continue
		}
		total += r.CostUSD
	}
	return total, known
}
