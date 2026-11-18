package hub

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/forge"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/work"
)

// A Fake stands in for the hub in this plugin's tests, so a test of the setup flow or the
// API can assert on the hub calls the code under test would have made without running a
// hub. These are the checks the plugin's own tests rely on it holding to.

func newFake() *Fake {
	return &Fake{
		Personas:   []Ref{{ID: "p-seed", Name: "Refiner"}},
		Agents:     []Ref{{ID: "a-seed", Name: "apps-refiner"}},
		Images:     []Ref{{ID: "ai-1", Name: "Claude Code"}},
		Connectors: []Ref{{ID: "c-1", Name: "apps"}, {ID: "c-2", Name: "docs"}},
		MCPServers: []MCPServer{{Name: "forgejo", URL: "https://forgejo/mcp"}},
		Triggers: map[string][]setup.Trigger{
			"c-1": {{Name: "trigger-refiner-mention-apps", AgentRef: "apps-refiner", ConnectorRef: "c-1"}},
			"c-2": {{Name: "trigger-developer-opened-docs", AgentRef: "docs-developer", ConnectorRef: "c-2"}},
		},
		SeedRuns: []work.Run{
			{ID: "inv-1", Connector: "c-1", Start: time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC),
				Subject: forge.Subject{Repo: "dpinsel/apps", Number: 7, Kind: forge.KindIssue}},
			{ID: "inv-2", Connector: "c-2", Start: time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC),
				Subject: forge.Subject{Repo: "dpinsel/docs", Number: 3, Kind: forge.KindIssue}},
		},
	}
}

// TestFakeAnswersSeededLists checks the lists a wizard reads come back as seeded, in the
// shape the real hub's answer arrives in.
func TestFakeAnswersSeededLists(t *testing.T) {
	f := newFake()
	ctx := context.Background()

	if got, err := f.ListPersonas(ctx); err != nil || len(got) != 1 || got[0].ID != "p-seed" {
		t.Errorf("ListPersonas = %+v, %v, want the seeded persona", got, err)
	}
	if got, err := f.ListAgents(ctx); err != nil || len(got) != 1 || got[0].Name != "apps-refiner" {
		t.Errorf("ListAgents = %+v, %v, want the seeded agent", got, err)
	}
	if got, err := f.ListAgentImages(ctx); err != nil || len(got) != 1 || got[0].Name != "Claude Code" {
		t.Errorf("ListAgentImages = %+v, %v, want the seeded image by its display name", got, err)
	}
	// A server has no id: the name is what an image's or agent's mcp list carries, and the
	// URL is what says which server it is.
	if got, err := f.ListMCPServers(ctx); err != nil || len(got) != 1 || got[0].Name != "forgejo" || got[0].URL != "https://forgejo/mcp" {
		t.Errorf("ListMCPServers = %+v, %v, want the seeded server", got, err)
	}
	if got, err := f.ListConnectors(ctx); err != nil || len(got) != 2 {
		t.Errorf("ListConnectors = %+v, %v, want both seeded connectors", got, err)
	}
}

// TestFakeTriggersFollowTheConnectorFilter checks the read setup makes before it creates:
// one connector's triggers when one is named, all of them when none is — the two readings
// the real client's filter produces.
func TestFakeTriggersFollowTheConnectorFilter(t *testing.T) {
	f := newFake()
	ctx := context.Background()

	got, err := f.ListTriggers(ctx, "c-1")
	if err != nil {
		t.Fatalf("ListTriggers(c-1): %v", err)
	}
	if len(got) != 1 || got[0].ConnectorRef != "c-1" {
		t.Errorf("triggers for c-1 = %+v, want only c-1's", got)
	}

	all, err := f.ListTriggers(ctx, "")
	if err != nil {
		t.Fatalf("ListTriggers: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("triggers with no connector named = %+v, want both connectors' — an empty answer here would tell a setup flow to create triggers that already exist", all)
	}

	// A connector that exists and has not been triggered yet answers empty rather than
	// as an error: "none yet" is the answer that makes a flow create them.
	if got, err := f.ListTriggers(ctx, "c-empty"); err != nil || len(got) != 0 {
		t.Errorf("triggers for an unknown connector = %+v, %v, want an empty list", got, err)
	}
}

// TestFakeRunsNameOnlyTheAskedConnectors checks the isolation invariant of the work list.
// A fake that answered every run no matter which connectors the caller named would let a
// test pass that the real client would fail: another install's work would show.
func TestFakeRunsNameOnlyTheAskedConnectors(t *testing.T) {
	f := newFake()
	ctx := context.Background()

	runs, err := f.Runs(ctx, RunsQuery{Connectors: []string{"c-1"}, Agents: []string{"a-seed"}})
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Connector != "c-1" {
		t.Fatalf("runs = %+v, want only c-1's", runs)
	}

	both, err := f.Runs(ctx, RunsQuery{Connectors: []string{"c-1", "c-2"}, Agents: []string{"a-seed"}})
	if err != nil || len(both) != 2 {
		t.Errorf("Runs for both = %+v, %v, want both runs", both, err)
	}

	// Naming no forge names no work: this is the call that must never fall back to
	// "everything the hub has".
	if got, err := f.Runs(ctx, RunsQuery{Agents: []string{"a-seed"}}); err != nil || len(got) != 0 {
		t.Errorf("Runs with no connector ids = %+v, %v, want no runs", got, err)
	}
}

// TestFakeRunsFilterBySince checks the window the fake applies to its seed: a test that
// asks for runs since a moment gets the runs from that moment on, and not the older ones
// it would have had to page past on a real hub. A zero since is the whole history.
func TestFakeRunsFilterBySince(t *testing.T) {
	f := newFake()
	ctx := context.Background()
	boundary := time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)

	// Start == since still counts as since then: the boundary belongs to the answer.
	if got, err := f.Runs(ctx, RunsQuery{Connectors: []string{"c-1", "c-2"}, Agents: []string{"a-seed"}, Since: boundary}); err != nil || len(got) != 1 || got[0].ID != "inv-2" {
		t.Errorf("Runs since the boundary = %+v, %v, want only inv-2 (Start == since is kept)", got, err)
	}

	if got, err := f.Runs(ctx, RunsQuery{Connectors: []string{"c-1", "c-2"}, Agents: []string{"a-seed"}, Since: boundary.Add(time.Hour)}); err != nil || len(got) != 0 {
		t.Errorf("Runs after every seed = %+v, %v, want no runs", got, err)
	}

	if got, err := f.Runs(ctx, RunsQuery{Connectors: []string{"c-1", "c-2"}, Agents: []string{"a-seed"}}); err != nil || len(got) != 2 {
		t.Errorf("Runs with a zero since = %+v, %v, want both (no window asked)", got, err)
	}
}

// TestFakeRecordsCreations checks the half a flow test asserts on: what a flow made is
// readable afterwards, whole — including the parts the caller sent and the hub would have
// stored.
func TestFakeRecordsCreations(t *testing.T) {
	f := newFake()
	ctx := context.Background()

	persona, err := f.CreatePersona(ctx, "g-1", "Refiner", "Refines issues", "You refine issues.")
	if err != nil {
		t.Fatalf("CreatePersona: %v", err)
	}
	if persona.ID == "" || persona.Name != "Refiner" {
		t.Errorf("persona = %+v, want the name back and an id the fake assigned", persona)
	}
	wantPersona := CreatedPersona{GroupID: "g-1", Name: "Refiner", Description: "Refines issues", Text: "You refine issues."}
	if got := f.CreatedPersonas; len(got) != 1 || got[0] != wantPersona {
		t.Errorf("CreatedPersonas = %+v, want %+v", got, wantPersona)
	}

	agent, err := f.CreateAgent(ctx, AgentSpec{
		Name: "apps-developer", GroupID: "g-1", ImageRef: "ai-1", PersonaID: persona.ID,
		MCPServers: []string{"forgejo", "plugin-forgejo"}, Skills: []string{"forgejo"},
		Env: []EnvVar{{Name: "FORGEJO_TOKEN", Value: "bot", Secret: true}},
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if agent.Name != "apps-developer" {
		t.Errorf("agent = %+v, want the name back (a trigger's agentRef is the name)", agent)
	}
	if len(f.CreatedAgents) != 1 {
		t.Fatalf("CreatedAgents = %+v, want the one agent", f.CreatedAgents)
	}
	// The spec comes back as sent: a test that asserts "the wizard selected the forgejo
	// MCP server and passed the bot token as a secret" reads this record.
	created := f.CreatedAgents[0]
	if created.PersonaID != persona.ID || created.ImageRef != "ai-1" {
		t.Errorf("created agent = %+v, want the persona and image the wizard chose", created)
	}
	if got := strings.Join(created.MCPServers, ","); got != "forgejo,plugin-forgejo" {
		t.Errorf("agent MCP servers = %q, want the ones the wizard selected", got)
	}
	if got := strings.Join(created.Skills, ","); got != "forgejo" {
		t.Errorf("agent skills = %q, want the ones the wizard selected", got)
	}
	if env := created.Env; len(env) != 1 || env[0].Name != "FORGEJO_TOKEN" || env[0].Value != "bot" || !env[0].Secret {
		t.Errorf("agent env = %+v, want the bot token marked secret as it was sent", env)
	}

	conn, err := f.CreateConnector(ctx, "g-1", "apps")
	if err != nil {
		t.Fatalf("CreateConnector: %v", err)
	}
	if conn.ID == "" || conn.Name != "apps" {
		t.Errorf("connector = %+v, want an id to wire a trigger to and the name back", conn)
	}
	if got := f.CreatedConnectors; len(got) != 1 || got[0] != (CreatedConnector{GroupID: "g-1", Name: "apps"}) {
		t.Errorf("CreatedConnectors = %+v, want the group and name the flow sent", got)
	}

	_, err = f.CreateTrigger(ctx, "g-1", setup.Trigger{
		Name: "trigger-x", AgentRef: "apps-developer", ConnectorRef: conn.ID,
		Filters: []setup.Filter{{Field: "type", Op: "eq", Value: "issues"}},
	})
	if err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}
	if got := f.CreatedTriggers; len(got) != 1 || got[0].GroupID != "g-1" ||
		got[0].ConnectorRef != conn.ID || len(got[0].Filters) != 1 {
		t.Errorf("CreatedTriggers = %+v, want the trigger with its group and filters", got)
	}

	// The connector's own triggers, read back through the filter: the flow's next step
	// is to check what it just made is there.
	if got, err := f.ListTriggers(ctx, conn.ID); err != nil || len(got) != 0 {
		t.Errorf("triggers for the new connector = %+v, %v, want empty (a create is not a seed)", got, err)
	}

	server, err := f.RegisterMCPServer(ctx, MCPServerSpec{Name: "plugin-forgejo", GroupID: "g-1", URL: "https://f/api/mcp"})
	if err != nil {
		t.Fatalf("RegisterMCPServer: %v", err)
	}
	if server.ID != "plugin-forgejo" {
		t.Errorf("server = %+v, want the registry name as both halves of the ref", server)
	}
	if got := f.RegisteredMCPServers; len(got) != 1 || got[0].GroupID != "g-1" {
		t.Errorf("RegisteredMCPServers = %+v, want the spec the flow sent", got)
	}
}

// TestFakeSeededConnectorAnswerIsUsed checks the connector a flow gets back: when a test
// seeds one, its endpoint and secret are the answer, because setup hands both to the admin
// and a flow test has to be able to assert on the endpoint it will tell them to post to.
// The seed answers the first create only — a real hub hands an endpoint and secret out
// once, and a flow that creates a second connector must not be handed the first one's id.
func TestFakeSeededConnectorAnswerIsUsed(t *testing.T) {
	f := &Fake{NextConnector: Connector{
		Ref:                Ref{ID: "c-42", Name: "seeded"},
		WebhookEndpoint:    "https://hub.example/webhooks/c-42",
		WebhookSecretValue: "s3cr3t",
	}}
	ctx := context.Background()

	conn, err := f.CreateConnector(ctx, "g-1", "apps")
	if err != nil {
		t.Fatalf("CreateConnector: %v", err)
	}
	if conn.WebhookEndpoint != "https://hub.example/webhooks/c-42" || conn.WebhookSecretValue != "s3cr3t" {
		t.Errorf("connector = %+v, want the seeded endpoint and secret", conn)
	}
	if conn.ID != "c-42" {
		t.Errorf("connector id = %q, want the seeded id, not a synthetic one", conn.ID)
	}

	// The seed is spent: the next create gets a generated id and the name it was
	// given, not the seed's answer a second time.
	second, err := f.CreateConnector(ctx, "g-1", "docs")
	if err != nil {
		t.Fatalf("CreateConnector(docs): %v", err)
	}
	if second.ID == "" || second.ID == "c-42" {
		t.Errorf("second connector id = %q, want a generated one, not the spent seed's", second.ID)
	}
	if second.Name != "docs" {
		t.Errorf("second connector name = %q, want the name the create gave", second.Name)
	}
}

// TestFakeCreationsGetDistinctIDs checks the ids a fake invents do not collide: a flow
// that creates two connectors and gets the same id twice would wire the second trigger onto
// the first and still read as working.
func TestFakeCreationsGetDistinctIDs(t *testing.T) {
	f := newFake()
	ctx := context.Background()

	seen := map[string]bool{}
	for _, name := range []string{"apps", "docs", "infra"} {
		conn, err := f.CreateConnector(ctx, "g-1", name)
		if err != nil {
			t.Fatalf("CreateConnector(%s): %v", name, err)
		}
		if conn.ID == "" || seen[conn.ID] {
			t.Fatalf("connector %s got id %q, want a fresh one", name, conn.ID)
		}
		seen[conn.ID] = true
	}
	for _, name := range []string{"one", "two"} {
		ref, err := f.CreatePersona(ctx, "g-1", name, "", "")
		if err != nil || ref.ID == "" || seen[ref.ID] {
			t.Fatalf("persona %s got %+v, %v, want a fresh id", name, ref, err)
		}
		seen[ref.ID] = true
	}
	for _, name := range []string{"one", "two"} {
		ref, err := f.CreateAgent(ctx, AgentSpec{Name: name, GroupID: "g-1"})
		if err != nil || ref.ID == "" || seen[ref.ID] {
			t.Fatalf("agent %s got %+v, %v, want a fresh id", name, ref, err)
		}
		seen[ref.ID] = true
	}

	if len(f.CreatedConnectors) != 3 || len(f.CreatedPersonas) != 2 || len(f.CreatedAgents) != 2 {
		t.Errorf("records = %d/%d/%d, want every create recorded",
			len(f.CreatedConnectors), len(f.CreatedPersonas), len(f.CreatedAgents))
	}
}

// TestFakeSeedsSurviveCreation checks that what a test seeded stays what it seeded: a
// create that landed in the same list a List reads would make a later assertion read the
// fake's own bookkeeping instead of the fixture.
func TestFakeSeedsSurviveCreation(t *testing.T) {
	f := newFake()
	ctx := context.Background()

	if _, err := f.CreatePersona(ctx, "g-1", "New", "", ""); err != nil {
		t.Fatalf("CreatePersona: %v", err)
	}
	if _, err := f.CreateAgent(ctx, AgentSpec{Name: "new", GroupID: "g-1"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := f.CreateConnector(ctx, "g-1", "new"); err != nil {
		t.Fatalf("CreateConnector: %v", err)
	}
	if _, err := f.RegisterMCPServer(ctx, MCPServerSpec{Name: "new", GroupID: "g-1"}); err != nil {
		t.Fatalf("RegisterMCPServer: %v", err)
	}

	if got, _ := f.ListPersonas(ctx); len(got) != 1 || got[0].ID != "p-seed" {
		t.Errorf("ListPersonas = %+v, want the seed unchanged by a creation", got)
	}
	if got, _ := f.ListAgents(ctx); len(got) != 1 {
		t.Errorf("ListAgents = %+v, want the seed unchanged by a creation", got)
	}
	if got, _ := f.ListConnectors(ctx); len(got) != 2 {
		t.Errorf("ListConnectors = %+v, want the seed unchanged by a creation", got)
	}
	// The MCP registry is the one list that is stateful on purpose: the
	// registry keys a server by name, and the reconcile pass reads this list
	// back to tell "already wired so" from "still wired otherwise". A
	// registration with a new name adds a row; one with an existing name moves
	// that row's wiring.
	if got, _ := f.ListMCPServers(ctx); len(got) != 2 || got[1].Name != "new" {
		t.Errorf("ListMCPServers = %+v, want the seed plus the registered name", got)
	}
	if _, err := f.RegisterMCPServer(ctx, MCPServerSpec{Name: "forgejo", GroupID: "g-1", URL: "https://moved/mcp"}); err != nil {
		t.Fatalf("RegisterMCPServer (again): %v", err)
	}
	if got, _ := f.ListMCPServers(ctx); len(got) != 2 || got[0].URL != "https://moved/mcp" {
		t.Errorf("ListMCPServers = %+v, want the seeded row re-wired in place", got)
	}
}

// TestFakeListsAreNotWritable checks that a caller cannot write into a fixture by way of a
// list it read: a flow that sorts or appends to what ListConnectors handed it would
// otherwise change the answer the next assertion reads.
func TestFakeListsAreNotWritable(t *testing.T) {
	f := newFake()
	ctx := context.Background()

	got, err := f.ListConnectors(ctx)
	if err != nil {
		t.Fatalf("ListConnectors: %v", err)
	}
	got[0] = Ref{ID: "tampered"}
	if again, _ := f.ListConnectors(ctx); again[0].ID != "c-1" {
		t.Errorf("second ListConnectors = %+v, want the fixture intact", again)
	}

	triggers, err := f.ListTriggers(ctx, "c-1")
	if err != nil || len(triggers) != 1 {
		t.Fatalf("ListTriggers = %+v, %v, want the seeded trigger", triggers, err)
	}
	triggers[0].Filters = append(triggers[0].Filters, setup.Filter{Field: "tampered"})
	if again, _ := f.ListTriggers(ctx, "c-1"); len(again[0].Filters) != 0 {
		t.Errorf("seeded trigger filters = %+v, want the fixture intact", again[0].Filters)
	}

	if seeded := f.Triggers["c-1"][0]; len(seeded.Filters) != 0 {
		t.Errorf("fixture's own filters = %+v, want untouched", seeded.Filters)
	}
}

// TestFakeRunsAnswerIndependentOfTheSeed checks that the run list a caller reads is not the
// slice a test holds: a flow that sorted or trimmed it would otherwise reorder the fixture
// for the next call.
func TestFakeRunsAreCopiesNotTheFixture(t *testing.T) {
	f := newFake()

	first, err := f.Runs(context.Background(), RunsQuery{Connectors: []string{"c-1", "c-2"}, Agents: []string{"a-seed"}})
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("runs = %+v, want both", first)
	}
	first[0] = work.Run{ID: "tampered"}

	again, err := f.Runs(context.Background(), RunsQuery{Connectors: []string{"c-1", "c-2"}, Agents: []string{"a-seed"}})
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if again[0].ID != "inv-1" {
		t.Errorf("second Runs = %+v, want the fixture intact", again)
	}
	// reflect on the seed itself, not on the fake's answer, to be sure nothing wrote
	// through the slice the caller got.
	if !reflect.DeepEqual(f.SeedRuns[0].ID, "inv-1") {
		t.Errorf("seed = %q, want inv-1", f.SeedRuns[0].ID)
	}
}

// TestFakeRecordsCallsInOrder checks the shape a flow test asserts on: the calls in the
// order they were made, so a test can say "the wizard looked before it created".
func TestFakeRecordsCallsInOrder(t *testing.T) {
	f := newFake()
	ctx := context.Background()

	if _, err := f.ListConnectors(ctx); err != nil {
		t.Fatalf("ListConnectors: %v", err)
	}
	if _, err := f.CreateConnector(ctx, "g-1", "apps"); err != nil {
		t.Fatalf("CreateConnector: %v", err)
	}
	if _, err := f.CreateTrigger(ctx, "g-1", setup.Trigger{Name: "trigger-x"}); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	want := []string{"ListConnectors", "CreateConnector", "CreateTrigger"}
	if got := f.Calls; !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
}
