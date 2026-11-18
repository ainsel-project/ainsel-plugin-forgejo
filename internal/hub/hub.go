package hub

import (
	"context"
	"strings"
	"time"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/forge"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/work"
)

// Client is the hub as this plugin needs it: the runs the plugin shows work from,
// and the AInsel resources the wizard lists, adopts and creates. It is the contract
// the API and the setup flow code against — HTTPClient is the one that talks to a
// real hub, Fake is the one the tests hand them.
//
// Everything on it is called as a caller: with a credential on the context
// (WithCredential) the call is made as that user, and without one it is made with
// the plugin's own background credential. AInsel decides what either may see.
//
// There is deliberately no method for this plugin's own MCP endpoint or for skills:
// the hub registers both of those itself, from the manifest. ListMCPServers and
// RegisterMCPServer are for the *Forgejo* MCP server — the forge's own MCP, which
// the agents use to work on Forgejo — that the wizard registers with the bots'
// tokens.
type Client interface {
	// Runs lists the runs the hub recorded as the query asks — each with the issue or
	// pull request it was about and, when the query asks for costs, what it cost. A
	// query that names no connector or no agent reads nothing and returns no runs.
	Runs(ctx context.Context, q RunsQuery) ([]work.Run, error)

	// ListPersonas and CreatePersona are the wizard's three role personas: shared
	// templates, so they are created with no owner agent.
	ListPersonas(ctx context.Context) ([]Ref, error)
	CreatePersona(ctx context.Context, groupID, name, description, text string) (Ref, error)

	// GetPersona reads one persona back with its text, which the list does not
	// carry: the reconcile pass compares what an agent is being told against
	// what the settings say it should be told, and only the read-back can say.
	GetPersona(ctx context.Context, id string) (Persona, error)

	// ListAgents and CreateAgent are the three role agents. A trigger points at an
	// agent by NAME, so a caller that wires one up needs the name back.
	ListAgents(ctx context.Context) ([]Ref, error)
	CreateAgent(ctx context.Context, spec AgentSpec) (Ref, error)

	// ListAgentImages lists the runtime images an agent can be created on, which is
	// what the wizard offers the admin to pick from.
	ListAgentImages(ctx context.Context) ([]Ref, error)

	// ListConnectors and CreateConnector are the webhook receivers. A created
	// connector answers once with the endpoint to point Forgejo at and the secret
	// to sign with; neither is ever persisted by the plugin.
	ListConnectors(ctx context.Context) ([]Ref, error)
	CreateConnector(ctx context.Context, groupID, name string) (Connector, error)

	// ListTriggers reads the triggers of one connector, so setup can tell what an
	// install already has from what it is missing. CreateTrigger files one under a
	// group — AInsel requires a groupId on every create (ruling R12).
	ListTriggers(ctx context.Context, connectorID string) ([]setup.Trigger, error)
	CreateTrigger(ctx context.Context, groupID string, t setup.Trigger) (Ref, error)

	// ListCronTriggers, CreateCronTrigger and DeleteCronTrigger are the install's
	// scheduled prompts — the developer's PR patrol. A cron trigger has no
	// connector: the hub lists them all (optionally filtered by agent), ids are
	// generated on create, and the install recognises its own by name — so a
	// delete takes the id a list carried, never the name.
	ListCronTriggers(ctx context.Context) ([]setup.CronTrigger, error)
	CreateCronTrigger(ctx context.Context, groupID string, ct setup.CronTrigger) (Ref, error)
	DeleteCronTrigger(ctx context.Context, id string) error

	// ListMCPServers and RegisterMCPServer are the forge's own MCP server, the one
	// the agents work Forgejo through; it is registered with the name of the env
	// var that holds a bot token rather than with the token itself.
	ListMCPServers(ctx context.Context) ([]MCPServer, error)
	RegisterMCPServer(ctx context.Context, spec MCPServerSpec) (Ref, error)

	// CreateAgentImage makes one of an install's role images: the runtime it runs
	// on, the MCP servers its tools come from and the bot token in its env. Skills
	// are not here: they belong to the agent, not the image. Its tool list starts
	// empty — the hub fills it on RefreshImageTools, not on create.
	CreateAgentImage(ctx context.Context, spec ImageSpec) (Ref, error)

	// RefreshImageTools makes the hub list the tools of each of an image's MCP
	// servers now, synchronously, and hands back the catalog that resulted: every
	// tool of every server it could read. A server it could not read comes back as
	// a *ToolRefreshError naming it — alongside those tools rather than instead of
	// them, because this plugin's own MCP server can never be listed this way and a
	// wizard that lost the forge server's tools over it would have nothing to
	// profile.
	RefreshImageTools(ctx context.Context, id string) ([]ImageTool, error)

	// GetAgentImage reads one image back as the managed state it is: the tool
	// catalog with its enabled flags, the env by name, the MCP servers and the
	// runtime. A value the hub stores as a secret is never among them.
	GetAgentImage(ctx context.Context, id string) (Image, error)

	// SetImageTools states the whole catalog again with only the role's tools
	// enabled. The catalog comes from RefreshImageTools, so the write drops nothing
	// the hub holds.
	SetImageTools(ctx context.Context, id string, tools []ImageTool) error

	// UpdateAgentImage changes what an install's image runs on and what it runs
	// with. A field left nil is left alone — the hub keeps what it holds — which is
	// what lets a re-run with blank secrets keep the stored ones.
	UpdateAgentImage(ctx context.Context, id string, u ImageUpdate) error

	// GetAgent reads one agent back as the managed state it is.
	GetAgent(ctx context.Context, id string) (Agent, error)

	// UpdateAgent changes an agent's properties. A field left nil is left alone.
	UpdateAgent(ctx context.Context, id string, u AgentUpdate) error

	// UpdatePersona rewrites the text of one persona. The text is the whole of what a
	// persona says — it names the forge the install works against — so an install whose
	// address changed has to write it again rather than leave its agents describing a
	// forge that is not there.
	UpdatePersona(ctx context.Context, id, text string) error

	// DeleteAgent, DeleteAgentImage, DeletePersona, DeleteConnector, DeleteMCPServer,
	// DeleteTrigger and DeleteCronTrigger each delete a resource by id.
	DeleteAgent(ctx context.Context, id string) error
	DeleteAgentImage(ctx context.Context, id string) error
	DeletePersona(ctx context.Context, id string) error
	DeleteConnector(ctx context.Context, id string) error
	DeleteMCPServer(ctx context.Context, id string) error
	DeleteTrigger(ctx context.Context, id string) error

	// RotateConnectorSecret rotates a connector's webhook secret and returns the new connector state.
	RotateConnectorSecret(ctx context.Context, id string) (Connector, error)

	// Events reads the events the hub recorded for one connector since the given moment.
	Events(ctx context.Context, connectorID string, since time.Time) ([]Event, error)

	// PluginGroups returns the group ids the plugin has access to.
	PluginGroups(ctx context.Context) ([]string, error)
}

// SubjectCache keeps what each event was about, so an event is read from the hub once.
// The plugin's own store answers it: an event payload never changes once received, so
// the subject derived from it never changes either — and a page load should not pay a
// full payload fetch for a question that was answered on the last one.
type SubjectCache interface {
	Subjects(ctx context.Context, eventIDs []string) (map[string]forge.Subject, error)
	PutSubjects(ctx context.Context, subjects map[string]forge.Subject, readAt time.Time) error
}

// RunsQuery names the reads one Runs call is prepared to make. It is a struct rather
// than three arguments because the three are not the same kind of thing: two of them
// scope what the hub is asked for, and one says whether a whole endpoint is worth
// reading at all.
//
// Every field narrows the read. A Runs call that names nothing is a call that wants
// nothing, so it asks the hub nothing rather than reading everything and filtering.
type RunsQuery struct {
	// Connectors are the runs whose event came from these connectors; none → no runs.
	Connectors []string

	// Agents are the agents whose invocations to read — the hub's `agent=` filter, read
	// once per agent rather than reading every agent's invocations and throwing most
	// of them away; none → no runs.
	Agents []string

	// Since is how far back to read.
	Since time.Time

	// Costs says whether to read the token cost metrics. False leaves every run's cost
	// unknown — the now page shows work that has not finished, where a cost is not
	// asked for and the endpoint is not worth a round trip.
	Costs bool

	// Cache is where the subjects of already-read events are kept and read back, so
	// an event is fetched from the hub at most once. Nil means there is no cache: the
	// hub answers for every event, as it always did.
	Cache SubjectCache
}

// MCPServer is one server as the hub's registry lists it. A server has no id of its own:
// the registry keys it by name, and that name is what an image's or an agent's mcp list
// carries. The URL is the half that says which server it is — a name alone cannot tell an
// install's own forge from another server an admin registered under something similar.
type MCPServer struct {
	Name         string `json:"name"`
	DisplayName  string `json:"displayName"`
	URL          string `json:"url"`
	TokenFromEnv string `json:"tokenFromEnv,omitempty"`
}

// Persona is one persona as the hub holds it: the whole text its agents are
// given. The reconcile pass reads it to tell "already says the settings' text"
// from "says the old text", and writes it back with UpdatePersona.
type Persona struct {
	ID   string
	Name string
	Text string
}

// Ref is one AInsel resource reduced to what the plugin does with it: the id to act
// on it by and the name to show or refer to it with. Which of the two a caller needs
// differs per resource and is spelled out on the method that returns it.
//
// The tags are the hub's list items' field names: a Ref is what an item is decoded
// into, with everything the plugin does not use left off.
type Ref struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Connector is a created connector with the two pieces that make it usable: the
// endpoint Forgejo posts its webhooks to, and the HMAC secret it signs them with.
//
// The secret is the hub's plaintext, returned exactly once by the create (and again
// by a rotate). It is a credential: pass it to the admin, do not store it and do not
// log it.
type Connector struct {
	Ref

	WebhookEndpoint    string
	WebhookSecretValue string
}

// EnvVar is one environment entry on an agent. A secret entry carries a value the
// plugin only ever passes through to AInsel — a bot token, in this plugin's case —
// and AInsel stores it in a Secret rather than in the clear.
//
// The tags are the hub's field names: an EnvVar is posted as it stands.
type EnvVar struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

// LLMSpec is the model an agent runs on. Provider is optional: an empty one leaves
// the choice to the image.
type LLMSpec struct {
	Model    string
	Provider string
}

// WorkingHours is the weekly window an agent takes queued work in. Outside it the hub
// publishes no due task, so no task job and no pod is created — a message that arrives at
// 23:00 for a 09:00–17:00 agent is answered when the window opens. It is a hold and not a
// stop: ingestion and routing keep running, nothing is lost, and a task already running
// when the window shuts finishes.
//
// The shape is the hub's own. Timezone is an IANA zone name (empty reads as UTC); Days are
// cron weekday numbers, where 0 and 7 are both Sunday (empty is every day); Start and End
// are HH:MM wall-clock bounds in that zone, and an End before its Start is one window
// crossing midnight, so a 22:00–06:00 night shift is a single window rather than two.
type WorkingHours struct {
	Timezone string `json:"timezone,omitempty"`
	Days     []int  `json:"days,omitempty"`
	Start    string `json:"start,omitempty"`
	End      string `json:"end,omitempty"`
}

// AgentSpec is an agent as the wizard wants it created. The names and shapes here
// are the plugin's; http.go translates them into the nested request AInsel's create
// endpoint takes (imageRef{name}, persona{id}, mcp{servers[]}).
//
// An absent (nil) MCPServers means "inherit whatever the image carries", which is
// AInsel's own distinction rather than a plugin invention. Skills are not inherited:
// since the hub moved tool and skill selection onto the agent there is nothing on the
// image to inherit, so a nil or empty Skills means no skills. The wizard sets both
// explicitly (ruling R13).
//
// EnabledTools is the agent's tool grant, and it follows the same rule as skills: the
// image's catalog only lists what the runtime offers, so a role's restriction has to be
// named here or it restricts nothing. The names must be the catalog's own spellings —
// the hub validates each against the image's tools by exact match — which is what
// setup.Apply resolves them to before it creates the agent. An empty list is never
// sent: at the runtime an unset grant means "everything", so an empty one would widen
// the agent rather than narrow it.
type AgentSpec struct {
	Name        string
	GroupID     string
	Description string

	// ImageRef is the agent-image registry id, not its display name.
	ImageRef  string
	PersonaID string

	MCPServers []string
	Skills     []string
	// EnabledTools is the agent's tool grant, in the image catalog's own spellings.
	// Empty means "send nothing", which at the hub means "no grant written" — never
	// "grant nothing", since an unset grant is unrestricted at the runtime.
	EnabledTools []string
	Env          []EnvVar
	LLM          *LLMSpec

	// WorkingHours is the window the agent is created with. Nil is the hub's "any
	// hour": the key stays out of the request and the agent has no window.
	WorkingHours *WorkingHours

	// Provider keys for the LLM providers the plugin delivers a credential for:
	// mapped from AInsel's nested structure. The three vendor keys are the hub's
	// openAI, anthropic and google blocks (ainsel-project/ainsel#242) — a hub
	// without it rejects both the provider id and the block.
	OllamaCloudKey string
	OpenCodeKey    string
	AlibabaKey     string
	OpenAIKey      string
	AnthropicKey   string
	GoogleKey      string
	CustomURL      string
	CustomKey      string

	// OpenAIAuthMethod is the hub's openAI.authMethod: "APIKey" (its default, and
	// what OpenAIKey feeds) or "ChatGPTSignIn", which carries no key and waits for
	// an admin to finish the sign-in in the console. Empty leaves it unsaid.
	// Sending it beside OpenAIKey is the pair the hub refuses.
	OpenAIAuthMethod string
}

// ImageSpec is a role image as the wizard wants it made. The tags are the hub's field
// names: an ImageSpec is posted as it stands.
//
// An image's tools are not here, because an image has none at this point: the hub
// discovers them from the MCP servers named here, and the wizard then enables the role's
// set with SetImageTools.
type ImageSpec struct {
	DisplayName string `json:"displayName"`
	GroupID     string `json:"groupId"`
	ImageURL    string `json:"imageURL"`

	// MCPServers are registry names, resolved by the hub to their URLs — the same
	// list an agent's mcp.servers takes, and the one RefreshImageTools reads.
	MCPServers []string `json:"mcpServers,omitempty"`

	// Env is the image's environment, the bot token among it as a secret.
	Env []EnvVar `json:"env,omitempty"`

	// ManagedBy is the hub's create-time ownership field. The client always stamps
	// this plugin (managerStamp in http.go); a caller leaves it empty and never
	// writes it — the hub accepts only that one value from this plugin, and only on
	// create.
	ManagedBy string `json:"managedBy,omitempty"`
}

// ImageTool is one tool of an image's catalog: what the hub discovered, and whether an
// agent run on this image may use it. Name already carries the hub's per-server prefix
// (mcp__<server>__<tool>), so a caller compares whole names, not the bare Forgejo one.
//
// The tags are the hub's field names: an ImageTool is decoded from a catalog item and
// posted back as one, so a tool that goes out as it came in keeps its description.
type ImageTool struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	McpSource   string `json:"mcpSource,omitempty"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
}

// Image is one agent image read back as the state the plugin keeps under it: what the
// wizard compares an install's profile against, and what the re-apply loop sets back.
//
// Env and MCPServers hold names only. A secret has no readable value at the hub, and an
// MCP server's URL and token env var are the registry's, which the name is the handle to.
type Image struct {
	ID          string
	DisplayName string
	ImageURL    string

	// Tools is the whole catalog, each tool with the flag the hub holds for it.
	Tools      []ImageTool
	Env        []string
	MCPServers []string
}

// ImageUpdate is a change to an image's managed state. Every field is optional: a nil one
// is left out of the request, and the hub keeps what it holds — the difference between
// "keep" and "clear" that an edit-install with blank secrets depends on.
//
// Env entries whose value is empty keep the stored value for that name, as the hub
// defines it, so a re-run that did not retype the bot token does not blank it. A name the
// hub holds as a secret is never sent as an explicit secret: false, and so cannot be
// declassified by an update that did not mean to.
type ImageUpdate struct {
	ImageURL   *string   `json:"imageURL,omitempty"`
	Env        *[]EnvVar `json:"env,omitempty"`
	MCPServers *[]string `json:"mcpServers,omitempty"`
}

// Agent is an agent read from the hub.
type Agent struct {
	ID          string
	Name        string
	Description string
	ImageRef    string
	PersonaID   string
	LLM         *LLMSpec
	Env         []EnvVar
	MCPServers  []string
	Skills      []string
	// EnabledTools is the agent's tool grant as the hub holds it. Empty means the
	// agent has no grant and runs with everything the runtime offers.
	EnabledTools []string

	// WorkingHours is the window the hub holds the agent to, nil when it has none.
	// The three after it are the hub's own reading of that window, computed for the
	// caller rather than stored by it: whether the agent is open right now, and the
	// bounds of the current and the next opening. All empty when there is no window.
	WorkingHours    *WorkingHours
	HoursOpen       bool
	HoursClosesAt   string
	HoursNextOpenAt string

	// Provider keys for the LLM providers the plugin delivers a credential for.
	// A key read back is the hub saying a block exists: the value never comes
	// back, so these hold "" or whatever the hub chooses to answer with.
	OllamaCloudKey string
	OpenCodeKey    string
	AlibabaKey     string
	OpenAIKey      string
	AnthropicKey   string
	GoogleKey      string
	CustomURL      string
	CustomKey      string

	// OpenAIAuthMethod is the agent's openAI.authMethod as the hub answers it —
	// "APIKey" when the agent names none, which is the hub's own default.
	OpenAIAuthMethod string
}

// AgentUpdate is a change to an agent's managed state. Every field is optional.
type AgentUpdate struct {
	// Name is the agent's display name — the name the hub lists it by and a trigger's
	// agentRef is built from. It is the admin's to choose; the agent's id does not move.
	Name       *string   `json:"name,omitempty"`
	ImageRef   *string   `json:"imageRef,omitempty"`
	PersonaID  *string   `json:"persona,omitempty"`
	LLM        *LLMSpec  `json:"llm,omitempty"`
	Env        *[]EnvVar `json:"env,omitempty"`
	MCPServers *[]string `json:"mcp,omitempty"`
	// Skills is the hub's own keep-versus-clear rule: nil is left out of the body and
	// the hub keeps what it holds, a non-nil empty slice arrives as [] and clears the
	// agent's selection. Nothing is inherited from the image, so clearing really
	// leaves the agent with no skills.
	Skills *[]string `json:"skills,omitempty"`
	// EnabledTools is the agent's tool grant, on the same keep-versus-clear rule as
	// Skills: nil is left out and the hub keeps what it holds.
	//
	// Unlike skills, a non-nil empty slice is never sent: an unset grant is the
	// runtime's "everything", so clearing a grant would widen the agent instead of
	// restricting it. A caller that has no grant to write leaves this nil.
	EnabledTools *[]string `json:"enabledTools,omitempty"`

	// WorkingHours and ClearWorkingHours are that same keep-versus-clear rule for the
	// agent's window, spelled as two fields so an accidental clear cannot be
	// expressed: a nil WorkingHours leaves the key out and the hub keeps the window it
	// holds, a non-nil one writes that window, and ClearWorkingHours sends the empty
	// object the hub reads as "no window". The two are never both — the plan emits one
	// op for the field and the apply answers it with one of them.
	WorkingHours      *WorkingHours `json:"workingHours,omitempty"`
	ClearWorkingHours bool          `json:"-"`

	// Provider keys for the LLM providers the plugin delivers a credential for.
	// A nil one is left out of the request and the hub keeps what it holds; a
	// non-empty one writes that provider's block and leaves the others alone.
	OllamaCloudKey *string `json:"ollamaCloudKey,omitempty"`
	OpenCodeKey    *string `json:"openCodeKey,omitempty"`
	AlibabaKey     *string `json:"alibabaKey,omitempty"`
	OpenAIKey      *string `json:"openAIKey,omitempty"`
	AnthropicKey   *string `json:"anthropicKey,omitempty"`
	GoogleKey      *string `json:"googleKey,omitempty"`
	CustomURL      *string `json:"customURL,omitempty"`
	CustomKey      *string `json:"customKey,omitempty"`

	// OpenAIAuthMethod is the hub's openAI.authMethod. Nil leaves the agent's
	// method alone, as every other field here does; a non-empty one writes it, and
	// the hub refuses one that arrives beside OpenAIKey.
	OpenAIAuthMethod *string `json:"openAIAuthMethod,omitempty"`
}

// Event is one event recorded by the hub.
type Event struct {
	ID   string
	Type string
	At   time.Time
}

// ToolRefreshError is the hub saying it could not read one or more of an image's MCP
// servers while refreshing its tools. The refresh still answered with the tools of the
// servers it could reach, so this is reported with them rather than in place of them.
//
// Servers holds the failing server's name as the hub prefixed the warning with, or the
// warning itself when it names no server — nothing here is dropped, and nothing here is
// a credential: the hub lists MCP tools without sending one, which is why the plugin's
// own MCP server is in this list and not among the tools.
type ToolRefreshError struct {
	Servers []string
}

func (e *ToolRefreshError) Error() string {
	switch len(e.Servers) {
	case 0:
		return "hub could not refresh the image's MCP tools"
	case 1:
		return "hub could not read MCP server " + e.Servers[0]
	default:
		return "hub could not read MCP servers " + strings.Join(e.Servers, ", ")
	}
}

// OnlyMissing reports whether this failed server set is exactly the one a caller is
// prepared to do without. For this plugin that is "plugin-forgejo" and nothing else:
// its tools are always on for an agent that has the MCP row and can't be listed
// anonymously, so that one failure is expected — but expected only when it stands
// alone. With the forge server among the failures the catalog is not usable, and a
// caller that ignored the error would profile an image on tools it never saw.
func (e *ToolRefreshError) OnlyMissing(name string) bool {
	return len(e.Servers) == 1 && e.Servers[0] == name
}

// MCPServerSpec is one entry of AInsel's MCP server registry.
//
// TokenFromEnv names an environment variable on the agent pod whose value the
// runtime forwards as the Authorization bearer token; it is never a token itself. An
// empty one means the server is anonymous.
type MCPServerSpec struct {
	Name         string
	GroupID      string
	DisplayName  string
	Description  string
	URL          string
	TokenFromEnv string
}
