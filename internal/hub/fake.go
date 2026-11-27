package hub

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/work"
)

var _ Client = (*Fake)(nil)

// Fake is a Client that answers from what a test seeded and writes down what it was
// asked to do, so a test of the setup flow or the API can assert on the calls the hub
// would have received without running a hub.
//
// Seeds are read by the List methods and are meant to be set before the fake is handed
// to anything; the records of what was created are separate, so what a test seeded and
// what the code under test created never mix in one list. Both are read and written
// under a mutex, which is what makes the fake safe when the code under test calls the
// hub from more than one goroutine — read the records once those calls have returned.
//
// A created resource gets a synthetic id (p-1, a-1, c-2, …) and the name the caller
// gave it, because a caller that creates an agent and then wires a trigger to it needs
// an id to wire.
type Fake struct {
	// SeedRuns is what Runs answers with. It cannot be named Runs: the method of
	// that name owns the name.
	SeedRuns []work.Run

	// RunsQueries records the query every Runs call was asked with, in order, so a test
	// can assert on what the caller read rather than only on what it got back.
	RunsQueries []RunsQuery

	// Personas, Agents, Connectors and MCPServers are what the matching List
	// returns; Images is what ListAgentImages returns, with the display name in
	// Name the way the real list's answer puts it.
	Personas   []Ref
	Agents     []Ref
	Images     []Ref
	Connectors []Ref
	MCPServers []MCPServer

	// Triggers holds each connector's triggers, keyed by connector id, which is
	// what ListTriggers is asked for.
	Triggers map[string][]setup.Trigger

	// CronTriggers seeds what ListCronTriggers answers with. A cron trigger has no
	// connector, so there is one flat list, as the hub's list is.
	CronTriggers []setup.CronTrigger

	// SeedImages holds the agent images GetAgentImage reads back and
	// RefreshImageTools answers from, keyed by image id: an image seeded with its
	// Tools is a refreshed image, one seeded without them answers with none.
	SeedImages map[string]Image

	// DefaultRefreshImageTools provides tools for images not in SeedImages.
	// If set and a RefreshImageTools is called for an unseeded image, these tools
	// are returned instead of nil.
	DefaultRefreshImageTools []ImageTool

	// ImageMCPServers maps image id to the MCP servers it was created with, for
	// RefreshImageTools to return per-server errors (e.g. plugin-forgejo).
	ImageMCPServers map[string][]string

	// NextConnector is what the first CreateConnector answers with — the endpoint and
	// the secret a real hub hands out exactly once. It is consumed by that first call:
	// a later create gets a generated id, so a flow creating two connectors never
	// finds them sharing one.
	NextConnector Connector

	// CreatedPersonas, CreatedAgents, CreatedConnectors, CreatedTriggers and
	// CreatedImages record the creates in the order they were asked for;
	// RegisteredMCPServers records the MCP servers. They are what a test asserts on.
	CreatedPersonas      []CreatedPersona
	CreatedAgents        []AgentSpec
	CreatedConnectors    []CreatedConnector
	CreatedTriggers      []CreatedTrigger
	CreatedCronTriggers  []CreatedCronTrigger
	CreatedImages        []ImageSpec
	RegisteredMCPServers []MCPServerSpec

	// RefreshedImages records the ids RefreshImageTools was asked to refresh, in
	// order; ImageToolSets and ImageUpdates record the writes to an image's tools
	// and to its managed state.
	RefreshedImages []string
	ImageToolSets   []ImageToolSet
	ImageUpdates    []UpdatedImage

	// SeedAgents holds agents that GetAgent reads back, keyed by agent id.
	SeedAgents map[string]Agent

	// SeedPersonas holds personas that GetPersona reads back, keyed by persona
	// id. UpdatePersona writes the text back here, so a second pass over the
	// same fake reads what the first pass wrote instead of the original drift.
	SeedPersonas map[string]Persona

	// UpdateErr, when set, fails every UpdateAgent, UpdatePersona, SetImageTools and
	// UpdateCronTrigger call: the hub refusing a write the plugin is entitled to
	// make is what a caller must survive as a warning rather than a halt.
	// RegisterErr does the same for RegisterMCPServer.
	UpdateErr   error
	RegisterErr error

	// DeletedAgents, DeletedAgentImages, DeletedPersonas, DeletedConnectors,
	// DeletedMCPServers, DeletedTriggers and DeletedCronTriggers record the ids
	// deleted, in order.
	// DeleteErr fails every delete: the hub keeping a resource it was told to remove is
	// how a removal ends with the row still there.
	DeleteErr           error
	DeletedAgents       []string
	DeletedAgentImages  []string
	DeletedPersonas     []string
	DeletedConnectors   []string
	DeletedMCPServers   []string
	DeletedTriggers     []string
	DeletedCronTriggers []string
	UpdatedAgents       []UpdatedAgent

	// UpdatedCronTriggers records each UpdateCronTrigger call — the row it addressed
	// and the halves it moved — which is what a patrol test reads instead of trusting
	// the row's own claim about what it wrote.
	UpdatedCronTriggers []UpdatedCronTrigger
	PersonaUpdates      []UpdatedPersona
	RotatedConnectors   []string
	SeedEvents          []Event
	PluginGroupsSeed    []string

	// LLMChecks is what CheckLLM answers with, keyed by the (provider, model) pair:
	// a test scripts each pair's state and reason. A pair with no entry is answered
	// as a hub that has no route is answered — unknown, never ok — so a test that
	// wants "reachable" has to say so.
	LLMChecks map[LLMCheckPair]LLMCheckResult
	// LLMCheckErr fails every CheckLLM as a call that never completed (the shape a
	// transport failure has, not a status): the hub that cannot be asked at all.
	LLMCheckErr error
	// LLMChecksAsked records each CheckLLM pair in the order it was asked, which is
	// how a test asserts a pair shared by two roles was pinged once, not twice.
	LLMChecksAsked []LLMCheckPair

	// ManagedBy records, per resource, the manager the fake stamped at create the way
	// the hub does: managerStamp for a create that arrived as the plugin — a context
	// marked WithBackground with no user credential on it — and "user" for one that
	// arrived as the console caller. Keys are "<kind>:<id>": "agent:a-1",
	// "image:img-2", "persona:p-3", "connector:c-4", "mcp:<name>". Triggers and cron
	// triggers are left out on purpose: the hub stamps neither.
	ManagedBy map[string]string

	// EnforceManaged makes the fake answer the hub's named-manager rule: a mutation
	// of a plugin-stamped resource that does not arrive as the plugin is refused with
	// the hub's 409 ("<kind> is managed by <manager>"). Off by default, so a test that
	// only reads records keeps its reading; on, it is what proves the wizard writes
	// as itself — an apply, edit, rotate or remove that slipped back onto the
	// caller's credential fails instead of quietly passing.
	EnforceManaged bool

	// Calls names every method that was called, in order, the lists included: the
	// cheap answer to "did the wizard look before it created".
	Calls []string

	mu  sync.Mutex
	seq int

	// nextConnectorUsed records that the NextConnector seed has been handed out.
	nextConnectorUsed bool
}

// CreatedPersona is one CreatePersona call as the fake saw it.
type CreatedPersona struct {
	GroupID     string
	Name        string
	Description string
	Text        string
}

// CreatedConnector is one CreateConnector call as the fake saw it. The endpoint and the
// secret the create answers with are not recorded here: a real hub hands them out once
// and the plugin keeps neither.
type CreatedConnector struct {
	GroupID string
	Name    string
}

// CreatedTrigger is one CreateTrigger call: the group it was filed under and the
// trigger itself.
type CreatedTrigger struct {
	GroupID string
	setup.Trigger
}

// CreatedCronTrigger is one CreateCronTrigger call: the group it was filed under
// and the cron trigger itself.
type CreatedCronTrigger struct {
	GroupID string
	setup.CronTrigger
}

// ImageToolSet is one SetImageTools call as the fake saw it: the image and the whole
// catalog it restated, with only the role's tools enabled.
type ImageToolSet struct {
	ID    string
	Tools []ImageTool
}

// UpdatedImage is one UpdateAgentImage call: the image and the update it applied.
type UpdatedImage struct {
	ID     string
	Update ImageUpdate
}

// UpdatedAgent is one UpdateAgent call: the agent and the update it applied.
type UpdatedAgent struct {
	ID     string
	Update AgentUpdate
}

// UpdatedCronTrigger is one UpdateCronTrigger call: the cron trigger it was addressed
// to and the write it carried.
type UpdatedCronTrigger struct {
	ID     string
	Update CronTriggerUpdate
}

// UpdatedPersona is one UpdatePersona call: the persona and the text it was given.
type UpdatedPersona struct {
	ID   string
	Text string
}

// Runs answers with the seeded runs whose connector the caller named and whose start
// falls in the caller's window — Start at or after since, with a zero since asking for
// the whole history. The filters are not decoration: they are what make a test of the
// work list prove that a caller who named one forge sees one forge's runs, and a caller
// who asked for recent work does not get work from before the question was asked.
//
// Naming no connector answers with nothing, as the real client does — there is no forge
// to read, and "everything the hub has" is not the same answer. Naming no agent answers
// with nothing for the same reason: the real client reads no invocation at all.
//
// Agents names which agents' invocations the real client reads, which this fake cannot
// represent — its seeds are finished runs, not invocations, and a run's Agent is the
// name the hub gave it — so it is recorded and left to the test to assert on. Costs
// names whether the real client reads the metrics endpoint: a seeded run carries its own
// cost either way.
func (f *Fake) Runs(_ context.Context, q RunsQuery) ([]work.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("Runs")
	f.RunsQueries = append(f.RunsQueries, q)

	if len(q.Agents) == 0 {
		return nil, nil
	}

	want := make(map[string]bool, len(q.Connectors))
	for _, id := range q.Connectors {
		want[id] = true
	}
	var out []work.Run
	for _, run := range f.SeedRuns {
		if !want[run.Connector] {
			continue
		}
		if !q.Since.IsZero() && run.Start.Before(q.Since) {
			continue
		}
		out = append(out, run)
	}
	return out, nil
}

func (f *Fake) ListPersonas(ctx context.Context) ([]Ref, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("ListPersonas")
	return refsOf(f.Personas), nil
}

// pluginCallerContext reports whether a call made on ctx reaches the hub as the plugin
// itself: no user credential staged, and the context marked background — the same rule
// the real client's credential resolution applies.
func pluginCallerContext(ctx context.Context) bool {
	return backgroundFromContext(ctx) && credentialFromContext(ctx) == ""
}

// stampLocked records who owns a created resource, the way the hub derives it: from the
// authenticated caller, not from the request body. The caller holds the lock.
func (f *Fake) stampLocked(ctx context.Context, key string) {
	who := "user"
	if pluginCallerContext(ctx) {
		who = managerStamp
	}
	if f.ManagedBy == nil {
		f.ManagedBy = map[string]string{}
	}
	f.ManagedBy[key] = who
}

// guardLocked is the hub's named-manager rule for mutations: a resource stamped as the
// plugin's answers every other caller with 409. Only a fake asked to enforce it does.
// The caller holds the lock.
func (f *Fake) guardLocked(ctx context.Context, kind, id string) error {
	if !f.EnforceManaged {
		return nil
	}
	who := f.ManagedBy[kind+":"+id]
	if who != managerStamp {
		return nil
	}
	if pluginCallerContext(ctx) {
		return nil
	}
	return &StatusError{Code: 409, Body: kind + " is managed by " + who}
}

func (f *Fake) CreatePersona(ctx context.Context, groupID, name, description, text string) (Ref, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("CreatePersona")

	f.CreatedPersonas = append(f.CreatedPersonas, CreatedPersona{
		GroupID: groupID, Name: name, Description: description, Text: text,
	})
	f.seq++
	ref := Ref{ID: fmt.Sprintf("p-%d", f.seq), Name: name}
	f.stampLocked(ctx, "persona:"+ref.ID)
	return ref, nil
}

func (f *Fake) ListAgents(ctx context.Context) ([]Ref, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("ListAgents")
	return refsOf(f.Agents), nil
}

func (f *Fake) CreateAgent(ctx context.Context, spec AgentSpec) (Ref, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("CreateAgent")

	f.CreatedAgents = append(f.CreatedAgents, spec)
	f.seq++
	ref := Ref{ID: fmt.Sprintf("a-%d", f.seq), Name: spec.Name}
	f.stampLocked(ctx, "agent:"+ref.ID)
	return ref, nil
}

func (f *Fake) ListAgentImages(ctx context.Context) ([]Ref, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("ListAgentImages")
	return refsOf(f.Images), nil
}

func (f *Fake) ListConnectors(ctx context.Context) ([]Ref, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("ListConnectors")
	return refsOf(f.Connectors), nil
}

func (f *Fake) CreateConnector(ctx context.Context, groupID, name string) (Connector, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("CreateConnector")

	f.CreatedConnectors = append(f.CreatedConnectors, CreatedConnector{GroupID: groupID, Name: name})

	created := f.NextConnector
	if created.ID != "" && !f.nextConnectorUsed {
		// The seed answers this create and no later one: a real hub hands the
		// endpoint and secret out once, so a second create must not read as the
		// first connector again.
		f.nextConnectorUsed = true
	} else {
		// Nothing seeded (or the seed is spent): a caller that creates a connector
		// still needs the c-… id to point a trigger at, so it gets one rather than
		// an empty string.
		f.seq++
		created = Connector{Ref: Ref{ID: fmt.Sprintf("c-%d", f.seq)}}
	}
	if created.Name == "" {
		created.Name = name
	}
	f.stampLocked(ctx, "connector:"+created.ID)
	return created, nil
}

// ListTriggers answers one connector's triggers, or — with an empty connectorID — every
// trigger the fake holds, which is what the real client's unfiltered read gets. A
// connector with no triggers answers empty, the way a hub whose connector has not been
// triggered yet does.
func (f *Fake) ListTriggers(_ context.Context, connectorID string) ([]setup.Trigger, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("ListTriggers")

	if connectorID != "" {
		return triggersOf(f.Triggers[connectorID]), nil
	}
	// The keys are visited in map order on purpose: a caller that reads every
	// connector's triggers gets a set, not a queue, and setup.Missing compares by name.
	var out []setup.Trigger
	for _, list := range f.Triggers {
		out = append(out, triggersOf(list)...)
	}
	return out, nil
}

func (f *Fake) CreateTrigger(ctx context.Context, groupID string, t setup.Trigger) (Ref, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("CreateTrigger")

	f.CreatedTriggers = append(f.CreatedTriggers, CreatedTrigger{GroupID: groupID, Trigger: t})
	f.seq++
	return Ref{ID: fmt.Sprintf("t-%d", f.seq), Name: t.Name}, nil
}

func (f *Fake) ListCronTriggers(_ context.Context) ([]setup.CronTrigger, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("ListCronTriggers")

	return append([]setup.CronTrigger(nil), f.CronTriggers...), nil
}

func (f *Fake) CreateCronTrigger(ctx context.Context, groupID string, ct setup.CronTrigger) (Ref, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("CreateCronTrigger")

	f.CreatedCronTriggers = append(f.CreatedCronTriggers, CreatedCronTrigger{GroupID: groupID, CronTrigger: ct})
	f.seq++
	ref := Ref{ID: fmt.Sprintf("c-%d", f.seq), Name: ct.Name}
	// The create persists: a later ListCronTriggers sees what was created, as a
	// real hub's does — which is what lets a test run two passes and assert the
	// second one finds nothing missing.
	created := ct
	created.ID = ref.ID
	f.CronTriggers = append(f.CronTriggers, created)
	return ref, nil
}

func (f *Fake) UpdateCronTrigger(ctx context.Context, id string, u CronTriggerUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("UpdateCronTrigger")

	if f.UpdateErr != nil {
		return f.UpdateErr
	}
	f.UpdatedCronTriggers = append(f.UpdatedCronTriggers, UpdatedCronTrigger{ID: id, Update: u})
	// The write persists, as a create's does: a later ListCronTriggers answers with
	// what a real hub would now hold, which is what lets a test run a second pass and
	// assert the patrol is left alone.
	for i, ct := range f.CronTriggers {
		if ct.ID != id {
			continue
		}
		if u.Schedule != nil {
			f.CronTriggers[i].Schedule = *u.Schedule
		}
		if u.Enabled != nil {
			f.CronTriggers[i].Enabled = *u.Enabled
		}
	}
	return nil
}

func (f *Fake) DeleteCronTrigger(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("DeleteCronTrigger")

	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	f.DeletedCronTriggers = append(f.DeletedCronTriggers, id)
	return nil
}

func (f *Fake) ListMCPServers(ctx context.Context) ([]MCPServer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("ListMCPServers")
	if f.MCPServers == nil {
		return nil, nil
	}
	return append([]MCPServer(nil), f.MCPServers...), nil
}

func (f *Fake) RegisterMCPServer(ctx context.Context, spec MCPServerSpec) (Ref, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("RegisterMCPServer")

	if f.RegisterErr != nil {
		return Ref{}, f.RegisterErr
	}
	f.RegisteredMCPServers = append(f.RegisteredMCPServers, spec)
	f.stampLocked(ctx, "mcp:"+spec.Name)
	// The registry keys an MCP server by its name alone, which is both halves of
	// the Ref. The registry list is updated in place, so a later read-back (the
	// reconcile's next pass) sees what this call wrote.
	row := MCPServer{Name: spec.Name, DisplayName: spec.DisplayName, URL: spec.URL, TokenFromEnv: spec.TokenFromEnv}
	replaced := false
	for i, s := range f.MCPServers {
		if s.Name == spec.Name {
			f.MCPServers[i] = row
			replaced = true
			break
		}
	}
	if !replaced {
		f.MCPServers = append(f.MCPServers, row)
	}
	return Ref{ID: spec.Name, Name: spec.Name}, nil
}

func (f *Fake) CreateAgentImage(ctx context.Context, spec ImageSpec) (Ref, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("CreateAgentImage")

	f.CreatedImages = append(f.CreatedImages, spec)
	// A caller that created an image and wants to refresh or profile it needs an id
	// to act on it by, the same way a created agent gets an a-… one.
	f.seq++
	id := fmt.Sprintf("img-%d", f.seq)
	f.stampLocked(ctx, "image:"+id)
	// Store the MCP servers for RefreshImageTools to use for per-server errors
	if f.ImageMCPServers == nil {
		f.ImageMCPServers = make(map[string][]string)
	}
	f.ImageMCPServers[id] = spec.MCPServers
	return Ref{ID: id, Name: spec.DisplayName}, nil
}

// RefreshImageTools answers with the seeded image's catalog, so a test can hand the
// wizard the tools the hub would have discovered. An id nothing was seeded for
// answers with DefaultRefreshImageTools if set, otherwise no tools and no error:
// the refresh itself succeeded, the servers behind it simply had nothing to list.
// If the image has plugin-forgejo in its MCP servers, a ToolRefreshError is
// returned naming only that server (expected failure).
func (f *Fake) RefreshImageTools(ctx context.Context, id string) ([]ImageTool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("RefreshImageTools")

	if err := f.guardLocked(ctx, "image", id); err != nil {
		return nil, err
	}
	f.RefreshedImages = append(f.RefreshedImages, id)
	tools := imageToolsOf(f.SeedImages[id].Tools)

	// If no tools were seeded for this image, use the default if provided
	if tools == nil && len(f.DefaultRefreshImageTools) > 0 {
		tools = imageToolsOf(f.DefaultRefreshImageTools)
	}

	// If plugin-forgejo is in the image's MCP servers, return a ToolRefreshError for it
	if servers, ok := f.ImageMCPServers[id]; ok {
		for _, srv := range servers {
			if srv == "plugin-forgejo" {
				return tools, &ToolRefreshError{Servers: []string{"plugin-forgejo"}}
			}
		}
	}
	return tools, nil
}

// GetAgentImage reads back a seeded image. An id nothing was seeded for answers with
// the zero Image carrying just the id it was asked for — nothing was made of it, so
// there is nothing to read.
func (f *Fake) GetAgentImage(ctx context.Context, id string) (Image, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("GetAgentImage")

	img, ok := f.SeedImages[id]
	if !ok {
		return Image{ID: id}, nil
	}
	img.ID = id
	img.Tools = imageToolsOf(img.Tools)
	return img, nil
}

func (f *Fake) SetImageTools(ctx context.Context, id string, tools []ImageTool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("SetImageTools")

	if err := f.guardLocked(ctx, "image", id); err != nil {
		return err
	}
	if f.UpdateErr != nil {
		return f.UpdateErr
	}
	f.ImageToolSets = append(f.ImageToolSets, ImageToolSet{ID: id, Tools: imageToolsOf(tools)})
	// The write lands on the seeded image too: the next read-back (a second
	// reconcile pass) must see the catalog as it was left, not as it was seeded.
	img := f.SeedImages[id]
	img.ID = id
	if img.Tools == nil {
		img.Tools = []ImageTool{}
	}
	img.Tools = imageToolsOf(tools)
	if f.SeedImages == nil {
		f.SeedImages = map[string]Image{}
	}
	f.SeedImages[id] = img
	return nil
}

func (f *Fake) UpdateAgentImage(ctx context.Context, id string, u ImageUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("UpdateAgentImage")

	if err := f.guardLocked(ctx, "image", id); err != nil {
		return err
	}
	f.ImageUpdates = append(f.ImageUpdates, UpdatedImage{ID: id, Update: u})
	return nil
}

// record notes that a method was called. The caller holds the lock: every method takes
// it once and reads its seed, records and returns under that one hold.
func (f *Fake) record(method string) {
	f.Calls = append(f.Calls, method)
}

// refsOf hands out a seeded list as a copy, so a caller that appends to or sorts what it
// read cannot rewrite the fixture a later assertion reads. The Refs inside are shared:
// they are values, and nothing here edits one in place.
func refsOf(in []Ref) []Ref {
	if in == nil {
		return nil
	}
	return append([]Ref(nil), in...)
}

// triggersOf copies a seeded trigger list the same way, one level deeper: a trigger's
// filters are the slice a caller is most likely to sort or trim.
func triggersOf(in []setup.Trigger) []setup.Trigger {
	if in == nil {
		return nil
	}
	out := make([]setup.Trigger, 0, len(in))
	for _, t := range in {
		if t.Filters != nil {
			t.Filters = append([]setup.Filter(nil), t.Filters...)
		}
		out = append(out, t)
	}
	return out
}

// imageToolsOf copies a tool catalog the way refsOf copies a Ref list, so a caller
// that edits or sorts what it read cannot rewrite the seeds a later assertion reads.
func imageToolsOf(in []ImageTool) []ImageTool {
	if in == nil {
		return nil
	}
	return append([]ImageTool(nil), in...)
}

// GetAgent reads back a seeded agent. An id nothing was seeded for answers with the
// zero Agent carrying just the id it was asked for.
func (f *Fake) GetAgent(ctx context.Context, id string) (Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("GetAgent")

	agent, ok := f.SeedAgents[id]
	if !ok {
		return Agent{ID: id}, nil
	}
	agent.ID = id
	return agent, nil
}

// GetPersona reads back a seeded persona. An id nothing was seeded for answers
// with the zero Persona carrying just the id it was asked for, the way GetAgent
// answers an unknown agent.
func (f *Fake) GetPersona(ctx context.Context, id string) (Persona, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("GetPersona")

	p, ok := f.SeedPersonas[id]
	if !ok {
		return Persona{ID: id}, nil
	}
	p.ID = id
	return p, nil
}

// UpdateAgent records an agent update and applies it to the seeded agent, so a
// later GetAgent reads back what the update wrote — which is what lets a second
// reconcile pass see that there is nothing left to fix.
func (f *Fake) UpdateAgent(ctx context.Context, id string, u AgentUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("UpdateAgent")

	if err := f.guardLocked(ctx, "agent", id); err != nil {
		return err
	}
	if f.UpdateErr != nil {
		return f.UpdateErr
	}
	f.UpdatedAgents = append(f.UpdatedAgents, UpdatedAgent{ID: id, Update: u})
	if f.SeedAgents == nil {
		f.SeedAgents = map[string]Agent{}
	}
	agent := f.SeedAgents[id]
	agent.ID = id
	if u.Name != nil {
		agent.Name = *u.Name
	}
	if u.ImageRef != nil {
		agent.ImageRef = *u.ImageRef
	}
	if u.PersonaID != nil {
		agent.PersonaID = *u.PersonaID
	}
	if u.LLM != nil {
		llm := *u.LLM
		agent.LLM = &llm
	}
	// The provider key blocks: a real hub stores the key and never reads it back,
	// so a read of these is presence at most. The fake keeps what was written,
	// which is what lets a test assert a block was sent at all.
	if u.OpenAIKey != nil {
		agent.OpenAIKey = *u.OpenAIKey
	}
	if u.OpenAIAuthMethod != nil {
		agent.OpenAIAuthMethod = *u.OpenAIAuthMethod
	}
	if u.AnthropicKey != nil {
		agent.AnthropicKey = *u.AnthropicKey
	}
	if u.GoogleKey != nil {
		agent.GoogleKey = *u.GoogleKey
	}
	if u.MCPServers != nil {
		agent.MCPServers = append([]string(nil), (*u.MCPServers)...)
	}
	if u.Skills != nil {
		agent.Skills = append([]string(nil), (*u.Skills)...)
	}
	// The grant follows the client's own rule: nil keeps, an empty list is dropped
	// rather than cleared, because an unset grant is the runtime's "everything".
	if u.EnabledTools != nil && len(*u.EnabledTools) > 0 {
		agent.EnabledTools = append([]string(nil), (*u.EnabledTools)...)
	}
	if u.CustomURL != nil {
		agent.CustomURL = *u.CustomURL
	}
	if u.CustomKey != nil {
		agent.CustomKey = *u.CustomKey
	}
	// The window follows the hub's rule, which is the rule the client's whole clear
	// depends on: an absent write keeps what the agent holds, the empty window the
	// client sends for a clear empties it, and a set window replaces it. A test that
	// wants to see a console-set window survive a re-apply needs a fake that holds one.
	switch {
	case u.ClearWorkingHours:
		agent.WorkingHours = &WorkingHours{}
	case u.WorkingHours != nil:
		hours := *u.WorkingHours
		agent.WorkingHours = &hours
	}
	f.SeedAgents[id] = agent
	return nil
}

// UpdatePersona records a persona text rewrite and writes the text back to the
// seeded persona, so a later GetPersona reads what this call stored.
func (f *Fake) UpdatePersona(ctx context.Context, id, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("UpdatePersona")

	if err := f.guardLocked(ctx, "persona", id); err != nil {
		return err
	}
	if f.UpdateErr != nil {
		return f.UpdateErr
	}
	f.PersonaUpdates = append(f.PersonaUpdates, UpdatedPersona{ID: id, Text: text})
	if f.SeedPersonas == nil {
		f.SeedPersonas = map[string]Persona{}
	}
	p := f.SeedPersonas[id]
	p.ID = id
	p.Text = text
	f.SeedPersonas[id] = p
	return nil
}

// DeleteAgent records an agent deletion.
func (f *Fake) DeleteAgent(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("DeleteAgent")

	if err := f.guardLocked(ctx, "agent", id); err != nil {
		return err
	}
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	f.DeletedAgents = append(f.DeletedAgents, id)
	return nil
}

// DeleteAgentImage records an agent image deletion.
func (f *Fake) DeleteAgentImage(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("DeleteAgentImage")

	if err := f.guardLocked(ctx, "image", id); err != nil {
		return err
	}
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	f.DeletedAgentImages = append(f.DeletedAgentImages, id)
	return nil
}

// DeletePersona records a persona deletion.
func (f *Fake) DeletePersona(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("DeletePersona")

	if err := f.guardLocked(ctx, "persona", id); err != nil {
		return err
	}
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	f.DeletedPersonas = append(f.DeletedPersonas, id)
	return nil
}

// DeleteConnector records a connector deletion.
func (f *Fake) DeleteConnector(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("DeleteConnector")

	if err := f.guardLocked(ctx, "connector", id); err != nil {
		return err
	}
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	f.DeletedConnectors = append(f.DeletedConnectors, id)
	return nil
}

// DeleteMCPServer records an MCP server deletion.
func (f *Fake) DeleteMCPServer(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("DeleteMCPServer")

	if err := f.guardLocked(ctx, "mcp", id); err != nil {
		return err
	}
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	f.DeletedMCPServers = append(f.DeletedMCPServers, id)
	return nil
}

// DeleteTrigger records a trigger deletion.
func (f *Fake) DeleteTrigger(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("DeleteTrigger")

	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	f.DeletedTriggers = append(f.DeletedTriggers, id)
	return nil
}

// RotateConnectorSecret records a connector secret rotation.
func (f *Fake) RotateConnectorSecret(ctx context.Context, id string) (Connector, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("RotateConnectorSecret")

	if err := f.guardLocked(ctx, "connector", id); err != nil {
		return Connector{}, err
	}
	f.RotatedConnectors = append(f.RotatedConnectors, id)
	f.seq++
	return Connector{
		Ref:                Ref{ID: id, Name: "rotated"},
		WebhookSecretValue: fmt.Sprintf("secret-%d", f.seq),
	}, nil
}

// Events returns seeded events for the given connector since the given time.
func (f *Fake) Events(ctx context.Context, connectorID string, since time.Time) ([]Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("Events")

	var out []Event
	for _, e := range f.SeedEvents {
		// The fake doesn't track connector info on events, so it answers with
		// all seeded events for any named connector and none for any other.
		// A real hub filters by the connector field.
		if !since.IsZero() && e.At.Before(since) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// PluginGroups returns the seeded plugin group ids.
func (f *Fake) PluginGroups(ctx context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("PluginGroups")

	// Empty, not nil, the same as the real client: a plugin with no group access.
	return append([]string{}, f.PluginGroupsSeed...), nil
}

// CheckLLM answers from the scripted per-pair results. A pair nobody scripted is
// answered the way a hub without the route is answered — unknown, never ok — and
// LLMCheckErr fails every call as one that never completed, the shape of a hub that
// cannot be asked at all.
func (f *Fake) CheckLLM(_ context.Context, provider, model string) (LLMCheckResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("CheckLLM")

	pair := LLMCheckPair{Provider: provider, Model: model}
	f.LLMChecksAsked = append(f.LLMChecksAsked, pair)
	if f.LLMCheckErr != nil {
		return LLMCheckResult{}, f.LLMCheckErr
	}
	if res, ok := f.LLMChecks[pair]; ok {
		return res, nil
	}
	return LLMCheckResult{
		State:  LLMCheckUnknown,
		Reason: "cannot check: the hub would not answer the check",
	}, nil
}
