package api

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/forge"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/store"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/work"
)

// The Forgejo the service is pointed at, and the two repositories its installs have
// worked on. Expected URLs are written out in full from these rather than rebuilt with
// the helpers that produce them, so a change to the URL shape fails here.
const (
	testBase    = "https://git.example.test"
	appsRepo    = "acme/apps"
	otherRepo   = "acme/other"
	otherForge  = "someone/else"
	connectorID = "c-1"
)

// day is the date the fixture runs hang off; the times below read as clock times on it.
// It is a fixed distance back from now rather than a calendar date: hub.Fake.Runs filters by
// since, and a fixed date stops being answerable for once the default 30-day window passes it.
var day = time.Now().UTC().Add(-24 * time.Hour).Truncate(24 * time.Hour)

// at is a run time on the fixture day: at(9, 30) is 09:30 UTC.
func at(hour, minute int) time.Time {
	return day.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

// ending spells a finished run's end time as the *time.Time the type carries.
func ending(t time.Time) *time.Time { return &t }

// issueOf is the subject of a run about an issue in the named repository.
func issueOf(repo string, number int, title string) forge.Subject {
	return forge.Subject{Repo: repo, Number: number, Kind: forge.KindIssue, Title: title}
}

// pullOf is the subject of a run about a pull request, carrying the title and branch the
// linking rule reads: this one names issue 12 in its scope and in its branch.
func pullOf(repo string, number int, title, branch string) forge.Subject {
	return forge.Subject{Repo: repo, Number: number, Kind: forge.KindPull, Title: title, Branch: branch}
}

// appsInstall is the install the fixtures are read under: one connector and the three role
// agents the fixture runs are named after.
func appsInstall() setup.Install {
	return setup.Install{
		Slug:        "apps",
		GroupID:     "g-1",
		BaseURL:     testBase,
		ConnectorID: connectorID,
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleRefiner: {
				AgentID:  "forgejo-refiner",
				BotLogin: "ainsel-refiner",
			},
			setup.RoleDeveloper: {
				AgentID:  "forgejo-developer",
				BotLogin: "ainsel-developer",
			},
			setup.RoleReviewer: {
				AgentID:  "forgejo-reviewer",
				BotLogin: "ainsel-reviewer",
			},
		},
		ExcludeLabel: "do-not-refine",
	}
}

// fixtureRuns are the runs the hub answers with for the installed connector: two on issue
// #12, two on the pull request that closes it (one of them still running and unpriced), one
// queued on another repository, one whose event carried no subject, and one on a connector
// no install points at — which is the run the work list must never see.
func fixtureRuns() []work.Run {
	return []work.Run{
		{
			ID: "inv-1", Agent: "forgejo-refiner", Status: "success", Connector: connectorID,
			Start: at(9, 0), End: ending(at(9, 30)), CostUSD: 0.25, CostKnown: true,
			Subject: issueOf(appsRepo, 12, "Search the word list"),
		},
		{
			ID: "inv-2", Agent: "forgejo-developer", Status: "success", Connector: connectorID,
			Start: at(10, 0), End: ending(at(11, 0)), CostUSD: 1.5, CostKnown: true,
			Subject: pullOf(appsRepo, 34, "feat(#12): search the word list", "feat/12-word-list"),
		},
		{
			// Running, and older than the seven days AInsel's token metrics reach
			// back, so its cost is unknown rather than zero.
			ID: "inv-3", Agent: "forgejo-reviewer", Status: "running", Connector: connectorID,
			Start:   at(12, 0),
			Subject: pullOf(appsRepo, 34, "feat(#12): search the word list", "feat/12-word-list"),
		},
		{
			ID: "inv-4", Agent: "forgejo-developer", Status: "success", Connector: connectorID,
			Start: at(13, 0), End: ending(at(13, 10)), CostUSD: 0.5, CostKnown: true,
			Subject: issueOf(appsRepo, 12, "Search the word list"),
		},
		{
			ID: "inv-5", Agent: "forgejo-refiner", Status: "queued", Connector: connectorID,
			Start:   at(14, 0),
			Subject: issueOf(otherRepo, 3, "Ship it"),
		},
		{
			// The event behind this run was past retention, so nothing says what it
			// was about and the work list has no page to name.
			ID: "inv-6", Agent: "forgejo-developer", Status: "success", Connector: connectorID,
			Start: at(14, 30), End: ending(at(14, 40)), CostUSD: 0.1, CostKnown: true,
		},
		{
			// Another forge's connector: not this install's work, and not in any
			// answer below.
			ID: "inv-7", Agent: "forgejo-developer", Status: "success", Connector: "c-2",
			Start: at(15, 0), End: ending(at(15, 5)), CostUSD: 9, CostKnown: true,
			Subject: issueOf(otherForge, 7, "Not ours"),
		},
	}
}

// spyClient is the fake hub with the two answers a hub.Fake cannot give: the credential a Runs
// call was made with, and a failure to hand back. The window it was made with is copied off the
// query for the assertions that read `since`; what else the call asked for — the connectors, the
// agents, whether costs were read — the embedded fake records itself, in RunsQueries.
type spyClient struct {
	*hub.Fake
	since time.Time
	// credential is the caller's credential the Runs call carried, "" when it carried
	// none. A hub call without one is a call the real hub would refuse, so the work
	// routes assert that the credential they were handed is the one they pass on.
	credential string
	err        error
}

var _ hub.Client = (*spyClient)(nil)

func (c *spyClient) Runs(ctx context.Context, q hub.RunsQuery) ([]work.Run, error) {
	c.since = q.Since
	c.credential, _ = hub.CredentialFromContext(ctx)
	if c.err != nil {
		return nil, c.err
	}
	return c.Fake.Runs(ctx, q)
}

// newSpy builds the spy over the fixture runs.
func newSpy() *spyClient {
	return &spyClient{Fake: &hub.Fake{SeedRuns: fixtureRuns()}}
}

// testStore opens a real store in a temporary directory, holding the installs given.
func testStore(t *testing.T, installs ...setup.Install) *store.Store {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})
	for _, in := range installs {
		if err := st.Save(context.Background(), in); err != nil {
			t.Fatalf("store.Save(%q): %v", in.Slug, err)
		}
	}
	return st
}

// testService builds the service every test reads: the fixture hub, one install, the
// fixture Forgejo.
func testService(t *testing.T) *WorkService {
	t.Helper()
	return NewWorkService(newSpy(), testStore(t, appsInstall()), testBase)
}

// TestNowListsActiveRunsWithTheirPages is the Now page's contract: the runs that have not
// finished, newest first, each with the page on the forge a reader can open — and a run on
// a pull request that names the issue it closes with its issue page too, because that is
// the thread the work is really for.
func TestNowListsActiveRunsWithTheirPages(t *testing.T) {
	svc := testService(t)

	runs, err := svc.Now(context.Background())
	if err != nil {
		t.Fatalf("Now: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("Now returned %d runs, want the 2 that have not finished: %+v", len(runs), runs)
	}

	// The run that started last is the one being waited on.
	if got := runs[0].ID; got != "inv-5" {
		t.Errorf("first run = %q, want inv-5 (started last)", got)
	}
	if got, want := runs[0].URL, testBase+"/"+otherRepo+"/issues/3"; got != want {
		t.Errorf("run URL = %q, want %q", got, want)
	}
	if runs[0].IssueURL != "" {
		t.Errorf("an issue's issueUrl = %q, want none", runs[0].IssueURL)
	}
	// The role is not something the hub knows: it comes from the install that
	// names this agent (ruling R18).
	if got := runs[0].Role; got != "refiner" {
		t.Errorf("run role = %q, want %q", got, "refiner")
	}

	pr := runs[1]
	if got := pr.ID; got != "inv-3" {
		t.Fatalf("second run = %q, want inv-3", got)
	}
	if got, want := pr.URL, testBase+"/"+appsRepo+"/pulls/34"; got != want {
		t.Errorf("pull run URL = %q, want %q", got, want)
	}
	if got, want := pr.IssueURL, testBase+"/"+appsRepo+"/issues/12"; got != want {
		t.Errorf("pull run issueUrl = %q, want %q", got, want)
	}
	if got := pr.Role; got != "reviewer" {
		t.Errorf("pull run role = %q, want reviewer", got)
	}
}

// TestWorkGroupsTheRunsTheHubAnswered is the work list as Task 4's Build groups it — one
// repository per forge, the pull request filed under the issue it closes, every item with a
// page of its own — with another forge's runs left out because no install names that
// connector.
func TestWorkGroupsTheRunsTheHubAnswered(t *testing.T) {
	svc := testService(t)

	groups, err := svc.Work(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("Work returned %d repo groups, want 2: %+v", len(groups), groups)
	}
	if got, want := groups[0].Repo, otherRepo; got != want {
		t.Errorf("first group = %q, want %q (newest work first)", got, want)
	}
	if got, want := groups[1].Repo, appsRepo; got != want {
		t.Errorf("second group = %q, want %q", got, want)
	}
	for _, g := range groups {
		if g.URL == "" {
			t.Errorf("group %q has no URL", g.Repo)
		}
		for _, it := range g.Items {
			checkItemURL(t, g.Repo, it)
		}
	}

	// Another install's connector is not this plugin's work, even when the hub
	// happens to answer with it.
	for _, g := range groups {
		if g.Repo == otherForge {
			t.Fatalf("group %q is another connector's work and must not be listed", g.Repo)
		}
	}
	// A run whose event carried no subject has no page, so it is not work.
	for _, g := range groups {
		for _, it := range g.Items {
			if it.Subject.Number == 0 {
				t.Fatalf("group %q lists a subject with no number", g.Repo)
			}
		}
	}

	apps := groups[1]
	if got, want := apps.URL, testBase+"/"+appsRepo; got != want {
		t.Errorf("apps group URL = %q, want %q", got, want)
	}
	if len(apps.Items) != 1 {
		t.Fatalf("apps group has %d items, want 1 (the pull nests under the issue): %+v",
			len(apps.Items), apps.Items)
	}
	issue := apps.Items[0]
	if issue.Subject.Number != 12 {
		t.Fatalf("top-level item = %d, want issue 12", issue.Subject.Number)
	}
	if got, want := issue.URL, testBase+"/"+appsRepo+"/issues/12"; got != want {
		t.Errorf("issue URL = %q, want %q", got, want)
	}
	// The issue's own runs, newest first: #4 at 13:10 is later than #1 at 9:30.
	if len(issue.Runs) != 2 || issue.Runs[0].ID != "inv-4" || issue.Runs[1].ID != "inv-1" {
		t.Errorf("issue runs = %+v, want inv-4 then inv-1", issue.Runs)
	}
	if want := 40 * time.Minute; issue.Duration != want {
		t.Errorf("issue duration = %v, want %v", issue.Duration, want)
	}
	if want := 0.75; issue.CostUSD != want || !issue.CostKnown {
		t.Errorf("issue cost = %v (known %v), want %v (known)", issue.CostUSD, issue.CostKnown, want)
	}
	// The issue's totals are its own runs: the pull's hour is the pull's.
	if len(issue.PRs) != 1 {
		t.Fatalf("issue has %d nested pulls, want 1: %+v", len(issue.PRs), issue.PRs)
	}
	pr := issue.PRs[0]
	if got, want := pr.URL, testBase+"/"+appsRepo+"/pulls/34"; got != want {
		t.Errorf("nested pull URL = %q, want %q", got, want)
	}
	if want := time.Hour; pr.Duration != want {
		t.Errorf("pull duration = %v, want %v", pr.Duration, want)
	}
	// One of the pull's two runs was never priced, so the pull's total is unknown
	// rather than the sum of the part that was.
	if pr.CostKnown {
		t.Errorf("pull costKnown = true, want false (one run is unpriced)")
	}
	if want := 1.5; pr.CostUSD != want {
		t.Errorf("pull cost = %v, want the priced runs' %v", pr.CostUSD, want)
	}
	// The repository's total counts every run once, and is unknown for the same
	// reason the pull's is.
	if apps.CostKnown {
		t.Errorf("group costKnown = true, want false")
	}
	if want := 2.25; apps.CostUSD != want {
		t.Errorf("group cost = %v, want every priced run added once (%v)", apps.CostUSD, want)
	}
}

// checkItemURL asserts an item — and every pull nested in it — has a page to link to.
func checkItemURL(t *testing.T, repo string, it work.Item) {
	t.Helper()
	if it.URL == "" {
		t.Errorf("item %d in %s has no URL", it.Subject.Number, repo)
	}
	for _, pr := range it.PRs {
		checkItemURL(t, repo, pr)
	}
}

// TestWorkFiltersByRepoAndAgent checks the two filters the console's work page offers.
// They filter the runs, not the answer: an agent's list is that agent's work with the
// nesting intact, not the full thread with the other agents' runs cut out of it.
func TestWorkFiltersByRepoAndAgent(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	t.Run("repo", func(t *testing.T) {
		groups, err := svc.Work(ctx, Filter{Repo: appsRepo})
		if err != nil {
			t.Fatalf("Work: %v", err)
		}
		if len(groups) != 1 || groups[0].Repo != appsRepo {
			t.Fatalf("Work(repo) = %+v, want only %s", groups, appsRepo)
		}
	})

	t.Run("agent", func(t *testing.T) {
		groups, err := svc.Work(ctx, Filter{Agent: "forgejo-developer"})
		if err != nil {
			t.Fatalf("Work: %v", err)
		}
		if len(groups) != 1 || groups[0].Repo != appsRepo {
			t.Fatalf("Work(agent) = %+v, want only the apps group", groups)
		}
		items := groups[0].Items
		if len(items) != 1 || items[0].Subject.Number != 12 {
			t.Fatalf("items = %+v, want issue 12 alone at the top level", items)
		}
		if len(items[0].Runs) != 1 || items[0].Runs[0].ID != "inv-4" {
			t.Errorf("issue runs = %+v, want only inv-4", items[0].Runs)
		}
		// The developer's pull request is still the issue's, even though no run of
		// the refiner's or the reviewer's is in the answer.
		if len(items[0].PRs) != 1 || items[0].PRs[0].Subject.Number != 34 {
			t.Errorf("nested pulls = %+v, want pull 34", items[0].PRs)
		}
	})

	t.Run("repo and agent", func(t *testing.T) {
		groups, err := svc.Work(ctx, Filter{Repo: otherRepo, Agent: "forgejo-refiner"})
		if err != nil {
			t.Fatalf("Work: %v", err)
		}
		if len(groups) != 1 || groups[0].Repo != otherRepo {
			t.Fatalf("Work(repo, agent) = %+v, want only %s", groups, otherRepo)
		}
	})

	t.Run("an agent with no runs", func(t *testing.T) {
		groups, err := svc.Work(ctx, Filter{Agent: "nobody"})
		if err != nil {
			t.Fatalf("Work: %v", err)
		}
		if len(groups) != 0 {
			t.Fatalf("Work(nobody) = %+v, want no groups", groups)
		}
	})
}

// TestWorkReadsTheLastThirtyDaysUnlessToldOtherwise pins the default window and the
// override: a console that asks for nothing sees a month of work, and a caller who names a
// moment gets the hub asked for exactly that moment.
func TestWorkReadsTheLastThirtyDaysUnlessToldOtherwise(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	spy := newSpy()
	svc.hub = spy
	if _, err := svc.Work(ctx, Filter{}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	want := time.Now().Add(-defaultWorkWindow)
	if d := spy.since.Sub(want); d > time.Minute || d < -time.Minute {
		t.Errorf("default since = %v, want about %v (30 days back)", spy.since, want)
	}

	// A caller who names a moment gets it, and the default does not creep in.
	deadline := at(9, 0)
	if _, err := svc.Work(ctx, Filter{Since: deadline}); err != nil {
		t.Fatalf("Work(since): %v", err)
	}
	if !spy.since.Equal(deadline) {
		t.Errorf("since = %v, want %v", spy.since, deadline)
	}
}

// wantInstallAgents is the read scope every page that reads runs asks the hub for: each
// agent the test install names, sorted the way the service sorts it so the assertion does not
// depend on the order a map happened to iterate in.
var wantInstallAgents = []string{"forgejo-developer", "forgejo-refiner", "forgejo-reviewer"}

// wantRunQuery is the one Runs query the call under test made, so a test asserts on what the
// page read rather than only on what it got back.
func wantRunQuery(t *testing.T, spy *spyClient) hub.RunsQuery {
	t.Helper()
	if len(spy.RunsQueries) != 1 {
		t.Fatalf("Runs was called %d times, want 1", len(spy.RunsQueries))
	}
	return spy.RunsQueries[0]
}

// TestNowPageAsksForNoCosts is the now page's read: a run that has not finished has no cost
// to show, so the token metrics are not read for it at all — and the agents it does read are
// the install's own, not every agent on the hub.
func TestNowPageAsksForNoCosts(t *testing.T) {
	spy := newSpy()
	svc := NewWorkService(spy, testStore(t, appsInstall()), testBase)

	if _, err := svc.Now(context.Background()); err != nil {
		t.Fatalf("Now: %v", err)
	}

	q := wantRunQuery(t, spy)
	if q.Costs {
		t.Errorf("Now asked with Costs = true, want false (the page shows no cost)")
	}
	if !reflect.DeepEqual(q.Connectors, []string{connectorID}) {
		t.Errorf("Now asked with connectors %+v, want %v", q.Connectors, []string{connectorID})
	}
	if !reflect.DeepEqual(q.Agents, wantInstallAgents) {
		t.Errorf("Now asked with agents %+v, want the install's %v", q.Agents, wantInstallAgents)
	}
}

// TestRunsPassesTheStoreAsSubjectCache is the service's half of reading an event once:
// the query it hands the hub carries the install store as the subject cache, so Runs
// consults the plugin's own database before it asks the hub. A service with no store
// has no installs and so no connectors, and asks the hub nothing at all — which is
// also why the cache can never be a non-nil interface holding a nil *store.Store.
func TestRunsPassesTheStoreAsSubjectCache(t *testing.T) {
	spy := newSpy()
	svc := NewWorkService(spy, testStore(t, appsInstall()), testBase)

	if _, err := svc.Now(context.Background()); err != nil {
		t.Fatalf("Now: %v", err)
	}
	if q := wantRunQuery(t, spy); q.Cache == nil {
		t.Error("the page asked for runs with no subject cache, want the install store as its cache")
	}

	spyNoStore := newSpy()
	noStore := NewWorkService(spyNoStore, nil, testBase)
	if _, err := noStore.Now(context.Background()); err != nil {
		t.Fatalf("Now without a store: %v", err)
	}
	if n := len(spyNoStore.RunsQueries); n != 0 {
		t.Errorf("a service with no store made %d Runs calls, want 0 (no install, no connector, no read)", n)
	}
}

// TestWorkPageAsksForCosts is the other half: the work list totals what a thread cost, so it
// reads the metrics — and it reads them over the same install scope the now page reads.
func TestWorkPageAsksForCosts(t *testing.T) {
	spy := newSpy()
	svc := NewWorkService(spy, testStore(t, appsInstall()), testBase)

	if _, err := svc.Work(context.Background(), Filter{}); err != nil {
		t.Fatalf("Work: %v", err)
	}

	q := wantRunQuery(t, spy)
	if !q.Costs {
		t.Errorf("Work asked with Costs = false, want true (the page totals costs)")
	}
	if !reflect.DeepEqual(q.Connectors, []string{connectorID}) {
		t.Errorf("Work asked with connectors %+v, want %v", q.Connectors, []string{connectorID})
	}
	if !reflect.DeepEqual(q.Agents, wantInstallAgents) {
		t.Errorf("Work asked with agents %+v, want the install's %v", q.Agents, wantInstallAgents)
	}
}

// TestInvocationWorkPageAsksForCosts covers the third page that reads runs: the invocation
// slot shows what the rest of the thread cost, so it asks for the metrics too.
func TestInvocationWorkPageAsksForCosts(t *testing.T) {
	spy := newSpy()
	svc := NewWorkService(spy, testStore(t, appsInstall()), testBase)

	if _, err := svc.InvocationWork(context.Background(), "inv-1"); err != nil {
		t.Fatalf("InvocationWork: %v", err)
	}

	q := wantRunQuery(t, spy)
	if !q.Costs {
		t.Errorf("InvocationWork asked with Costs = false, want true (the slot shows the thread's cost)")
	}
	if !reflect.DeepEqual(q.Agents, wantInstallAgents) {
		t.Errorf("InvocationWork asked with agents %+v, want the install's %v", q.Agents, wantInstallAgents)
	}
}

// TestWorkWithNoInstallsAsksTheHubNothing is the plugin's whole premise stated at its
// boundary: no install means no connector, and no connector means no forge is read at all —
// an empty list, and not one request out to the hub.
func TestWorkWithNoInstallsAsksTheHubNothing(t *testing.T) {
	spy := newSpy()
	svc := NewWorkService(spy, testStore(t), testBase)
	ctx := context.Background()

	groups, err := svc.Work(ctx, Filter{})
	if err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("Work with no installs = %+v, want none", groups)
	}
	if runs, err := svc.Now(ctx); err != nil || len(runs) != 0 {
		t.Errorf("Now with no installs = %+v (%v), want none", runs, err)
	}
	if len(spy.Calls) != 0 {
		t.Errorf("the hub was called with %+v, want no calls at all", spy.Calls)
	}
}

// TestServiceWithoutHubIsAnErrorNotAnEmptyAnswer keeps a plugin with no HUB_URL from
// reading as a plugin with no work: the caller has to be told nothing was read.
func TestServiceWithoutHubIsAnErrorNotAnEmptyAnswer(t *testing.T) {
	svc := NewWorkService(nil, testStore(t, appsInstall()), testBase)

	if _, err := svc.Work(context.Background(), Filter{}); !errors.Is(err, ErrHubNotConfigured) {
		t.Errorf("Work error = %v, want ErrHubNotConfigured", err)
	}
	if _, err := svc.Now(context.Background()); !errors.Is(err, ErrHubNotConfigured) {
		t.Errorf("Now error = %v, want ErrHubNotConfigured", err)
	}
}

// TestItemFindsOneThread covers the drill-down route's three answers: an issue, a pull
// request that only exists nested under its issue, and a number nobody ran on.
func TestItemFindsOneThread(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	issue, err := svc.Item(ctx, appsRepo, 12)
	if err != nil {
		t.Fatalf("Item(issue 12): %v", err)
	}
	if issue == nil {
		t.Fatal("Item(issue 12) = nil, want the item")
	}
	if len(issue.Runs) != 2 {
		t.Errorf("issue 12 has %d runs, want 2", len(issue.Runs))
	}

	// Pull 34 is not a top-level item — finding it is what makes the route usable
	// for the pulls the work list nests.
	pr, err := svc.Item(ctx, appsRepo, 34)
	if err != nil {
		t.Fatalf("Item(pull 34): %v", err)
	}
	if pr == nil {
		t.Fatal("Item(pull 34) = nil, want the nested item")
	}
	if pr.Subject.Kind != forge.KindPull {
		t.Errorf("Item(34) kind = %q, want pull", pr.Subject.Kind)
	}

	missing, err := svc.Item(ctx, appsRepo, 99)
	if err != nil {
		t.Fatalf("Item(99): %v", err)
	}
	if missing != nil {
		t.Errorf("Item(99) = %+v, want nil (and no error: nobody's work was missing)", missing)
	}

	// Another repository's number is not this repository's thread: Forgejo numbers
	// them apart.
	other, err := svc.Item(ctx, otherRepo, 12)
	if err != nil {
		t.Fatalf("Item(other, 12): %v", err)
	}
	if other != nil {
		t.Errorf("Item(other, 12) = %+v, want nil", other)
	}
}

// TestAgentWorkListsTheItemsAnAgentTouched is the agent slot's question: what has this
// agent worked on — the threads, with the pulls still filed under the issues they close.
func TestAgentWorkListsTheItemsAnAgentTouched(t *testing.T) {
	svc := testService(t)

	items, err := svc.AgentWork(context.Background(), "forgejo-developer")
	if err != nil {
		t.Fatalf("AgentWork: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("AgentWork = %+v, want issue 12", items)
	}
	it := items[0]
	if it.Subject.Number != 12 || it.URL == "" {
		t.Errorf("item = %+v, want issue 12 with a URL", it.Subject)
	}
	if len(it.Runs) != 1 || it.Runs[0].ID != "inv-4" {
		t.Errorf("runs = %+v, want only the developer's inv-4", it.Runs)
	}
	if len(it.PRs) != 1 || it.PRs[0].Subject.Number != 34 {
		t.Errorf("nested pulls = %+v, want pull 34", it.PRs)
	}
	if got := it.Runs[0].Role; got != "developer" {
		t.Errorf("run role = %q, want developer (filled from the install)", got)
	}
}

// TestRoleMappingByAgentID verifies that role mapping uses agent IDs, not display names.
// A run with an agent ID that matches install.Roles[role].AgentID gets that role.
// A run with the display name instead does NOT get the role.
func TestRoleMappingByAgentID(t *testing.T) {
	// Create an install with specific agent IDs for roles
	install := setup.Install{
		GroupID:     "g-test",
		Slug:        "test",
		ConnectorID: connectorID, // Must match the runs' connector
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleReviewer: {AgentID: "a-5950e4c3"}, // A specific agent ID
		},
	}

	// Create a store that persists this install
	st := testStore(t, install)

	// Create a hub with seed runs
	fakeHub := &hub.Fake{
		SeedRuns: []work.Run{
			// Run with the correct agent ID - should get the role
			{
				ID:        "run-1",
				Agent:     "a-5950e4c3", // This is the ID in the install
				Connector: connectorID,
				Start:     day.Add(9 * time.Hour),
				Subject: forge.Subject{
					Repo:   appsRepo,
					Number: 1,
					Kind:   forge.KindIssue,
					Title:  "test",
				},
			},
			// Run with the display name instead of ID - should NOT get the role
			{
				ID:        "run-2",
				Agent:     "reviewer-name", // This is the display name, not the ID
				Connector: connectorID,
				Start:     day.Add(10 * time.Hour),
				Subject: forge.Subject{
					Repo:   appsRepo,
					Number: 2,
					Kind:   forge.KindIssue,
					Title:  "test2",
				},
			},
		},
	}

	svc := NewWorkService(fakeHub, st, testBase)
	groups, err := svc.Work(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("Work: %v", err)
	}

	if len(groups) == 0 {
		t.Fatal("no work groups returned")
	}

	// Check the roles of both runs
	for _, item := range groups[0].Items {
		for _, run := range item.Runs {
			switch run.ID {
			case "run-1":
				// This run should have Agent ID that matches the install, so it gets the role
				if run.Role != "reviewer" {
					t.Errorf("run-1 role = %q, want reviewer (agent id a-5950e4c3 matches install)", run.Role)
				}
			case "run-2":
				// This run has the display name, not the ID, so it should NOT get the role
				if run.Role != "" {
					t.Errorf("run-2 role = %q, want empty (agent name doesn't match any install agent id)", run.Role)
				}
			}
		}
	}
}

// TestInvocationWorkReturnsTheItemAndItsSiblings is the invocation slot's question: what is
// this run about, and what has the rest of the work on that cost so far — the sum of the
// other runs of the same issue or pull, never of its own.
func TestInvocationWorkReturnsTheItemAndItsSiblings(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	t.Run("an issue with another run", func(t *testing.T) {
		got, err := svc.InvocationWork(ctx, "inv-1")
		if err != nil {
			t.Fatalf("InvocationWork: %v", err)
		}
		if got == nil {
			t.Fatal("InvocationWork = nil, want issue 12")
		}
		if got.Item.Subject.Number != 12 {
			t.Errorf("item = %d, want issue 12", got.Item.Subject.Number)
		}
		if want := 0.5; got.SiblingsCostUSD != want {
			t.Errorf("siblingsCostUsd = %v, want the other run's %v", got.SiblingsCostUSD, want)
		}
		if !got.SiblingsCostKnown {
			t.Errorf("siblingsCostKnown = false, want true (the other run is priced)")
		}
	})

	t.Run("a sibling nobody priced", func(t *testing.T) {
		got, err := svc.InvocationWork(ctx, "inv-2")
		if err != nil {
			t.Fatalf("InvocationWork: %v", err)
		}
		if got == nil {
			t.Fatal("InvocationWork = nil, want pull 34")
		}
		if got.Item.Subject.Number != 34 {
			t.Errorf("item = %d, want pull 34", got.Item.Subject.Number)
		}
		// The other run on this pull is still running and unpriced: the total is
		// unknown, and a console reading it renders "—" rather than "$0.00".
		if got.SiblingsCostKnown {
			t.Errorf("siblingsCostKnown = true, want false")
		}
	})

	t.Run("the only run of its item", func(t *testing.T) {
		got, err := svc.InvocationWork(ctx, "inv-5")
		if err != nil {
			t.Fatalf("InvocationWork: %v", err)
		}
		if got == nil {
			t.Fatal("InvocationWork = nil, want issue 3")
		}
		if got.SiblingsCostUSD != 0 || !got.SiblingsCostKnown {
			t.Errorf("siblings = (%v, known %v), want (0, true): nothing else, nothing unknown",
				got.SiblingsCostUSD, got.SiblingsCostKnown)
		}
	})

	t.Run("not one of ours", func(t *testing.T) {
		// Another connector's run: the work list does not hold it, so the slot has
		// nothing to say about it.
		got, err := svc.InvocationWork(ctx, "inv-7")
		if err != nil {
			t.Fatalf("InvocationWork: %v", err)
		}
		if got != nil {
			t.Errorf("InvocationWork = %+v, want nil", got)
		}
	})

	t.Run("a run with no subject", func(t *testing.T) {
		// The run happened; its event is gone, so no page names what it was about.
		got, err := svc.InvocationWork(ctx, "inv-6")
		if err != nil {
			t.Fatalf("InvocationWork: %v", err)
		}
		if got != nil {
			t.Errorf("InvocationWork = %+v, want nil", got)
		}
	})

	t.Run("no such invocation", func(t *testing.T) {
		got, err := svc.InvocationWork(ctx, "inv-nope")
		if err != nil {
			t.Fatalf("InvocationWork: %v", err)
		}
		if got != nil {
			t.Errorf("InvocationWork = %+v, want nil", got)
		}
	})
}

// TestWorkSkipsRetiringInstalls: a row the cutover keeps because a tracked resource is
// still standing is not an install the console has work for — its connector and agents are
// being deleted, and reading runs for them would show work nobody can act on any more.
func TestWorkSkipsRetiringInstalls(t *testing.T) {
	spy := newSpy()
	retiring := appsInstall()
	retiring.Slug = "legacy"
	retiring.ConnectorID = "c-legacy"
	retiring.Retiring = true
	svc := NewWorkService(spy, testStore(t, appsInstall(), retiring), testBase)

	if _, err := svc.Work(context.Background(), Filter{}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(spy.RunsQueries) == 0 {
		t.Fatal("no Runs call was made")
	}
	for _, q := range spy.RunsQueries {
		if slices.Contains(q.Connectors, "c-legacy") {
			t.Errorf("the work view read a retiring install's connector: %+v", q.Connectors)
		}
		if !slices.Contains(q.Connectors, connectorID) {
			t.Errorf("the work view stopped reading the live install: %+v", q.Connectors)
		}
	}
}

// TestWorkNamesEachRunsAgentByItsDisplayName: a page shows an agent as the admin named it,
// not as the hub's id — and an agent the hub does not list keeps its id rather than going
// blank.
func TestWorkNamesEachRunsAgentByItsDisplayName(t *testing.T) {
	spy := newSpy()
	runs := fixtureRuns()
	spy.Agents = []hub.Ref{{ID: runs[0].Agent, Name: "Refiner of apps"}}
	svc := NewWorkService(spy, testStore(t, appsInstall()), testBase)

	now, err := svc.Now(context.Background())
	if err != nil {
		t.Fatalf("Now: %v", err)
	}
	named := false
	for _, r := range now {
		want := r.Agent
		if r.Agent == runs[0].Agent {
			want = "Refiner of apps"
			named = true
		}
		if r.AgentName != want {
			t.Errorf("run %s agentName = %q, want %q", r.ID, r.AgentName, want)
		}
	}
	if !named {
		t.Fatalf("no active run belongs to %s: the test names nobody", runs[0].Agent)
	}
}
