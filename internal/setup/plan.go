package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Plan is the wizard's one decision function. The preview (S6) and the apply (S7)
// are the same call (R2), and so is the re-apply loop: the input is what the admin
// typed, the snapshot is what the instance already holds, and the result says what
// to create, what to change, what to delete and — in the verdict — why the install
// cannot go through at all.
//
// It is pure: no I/O, no clock. Everything it knows about the outside world the
// caller read beforehand and handed over, which is what lets the preview answer
// without writing anything, and what makes a name the preview called free the same
// name apply checks.
//
// A plan that blocks still comes back with its ops: the preview lists what the
// install would create next to the reason it cannot be created yet.

// Op is one resource the plan creates, changes or deletes, in the shape the
// preview shows: the kind of resource, its name, and a detail that says what
// about it. Kind and name are also what apply matches its own progress on.
type Op struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Detail string `json:"detail"`

	// Role and Field say which role an op is about and, for a change, which field
	// of it. The preview renders neither; apply matches on them, because an
	// agent's display name is the admin's to choose and need not contain the
	// role's name, and a slug may contain one (spec §3.5).
	Role  Role   `json:"role,omitempty"`
	Field string `json:"field,omitempty"`
}

// The fields a change op names. They are the vocabulary the plan and the apply
// share: the plan says which field of which role changed, the apply reads that
// instead of guessing from the text it rendered for the admin.
//
// The llm fields are the ones the settings store owns a value for. There is no
// key value on a row: a key is never stored on the install (R4 keeps it out of
// the row whatever the future holds); the four provider keys the plugin
// delivers (custom, openai, anthropic, google) are read from the settings at
// write time, and only the custom URL is ever diffed — a key cannot be read
// back from the hub, so its drift is seen through the row's fingerprint. Each
// key field is named by the settings field that holds it, so the op and the
// settings row the admin edits carry the same name.
const (
	FieldBaseURL         = "baseURL"
	FieldRuntime         = "runtime"
	FieldTools           = "tools"
	FieldTurnTimeout     = "turnTimeoutMs"
	FieldAgentName       = "agentName"
	FieldLLMProvider     = "llmProvider"
	FieldLLMModel        = "llmModel"
	FieldLLMCustomUrl    = "llmCustomUrl"
	FieldLLMCustomKey    = "llmCustomKey"
	FieldLLMOpenAIKey    = "llmOpenAIKey"
	FieldLLMAnthropicKey = "llmAnthropicKey"
	FieldLLMGoogleKey    = "llmGoogleKey"
	// FieldWorkingHours names the window the wizard sets on a role's agent. It is the
	// one field the plan diffs against the install row rather than against the live
	// agent — see changeOps.
	FieldWorkingHours = "workingHours"

	// FieldLLMOpenAIAuthMethod names the row that chooses how an OpenAI role
	// authenticates: an API key or a ChatGPT sign-in finished in the console.
	// It belongs to the openai provider alone, the way the vendor key rows
	// belong to their own provider.
	FieldLLMOpenAIAuthMethod = "llmOpenAIAuthMethod"
	FieldExcludeLabel        = "excludeLabel"
)

// The two OpenAI auth methods the hub's openAI block carries
// (`AgentOpenAI.authMethod`). "APIKey" is the hub's default and the plugin's:
// the key row's value is what authenticates. "ChatGPTSignIn" carries no key at
// all — the credential is created by an interactive sign-in an admin finishes
// per agent in the console, and the agent runs nothing until it is done.
//
// They are spelled exactly as the hub's API names them, so the settings list,
// the plan and the wire format cannot drift.
const (
	LLMAuthMethodAPIKey        = "APIKey"
	LLMAuthMethodChatGPTSignIn = "ChatGPTSignIn"
)

// IsChatGPTSignIn says whether an auth-method value names the sign-in method.
// Anything else — including the empty value a settings row that nobody edited
// carries — is the API key, which is the hub's default too.
func IsChatGPTSignIn(method string) bool {
	return method == LLMAuthMethodChatGPTSignIn
}

// OpenAIAuthMethodOf is the auth method a role's settings name for the openai
// provider, normalized: an unset value reads as LLMAuthMethodAPIKey, the value
// the hub holds when nobody chose one.
func OpenAIAuthMethodOf(llm RoleLLM) string {
	if IsChatGPTSignIn(llm.OpenAIAuthMethod) {
		return LLMAuthMethodChatGPTSignIn
	}
	return LLMAuthMethodAPIKey
}

// The provider ids whose credential the plugin delivers from the settings
// store. "custom" is an admin's own OpenAI-compatible endpoint: the hub's
// customProvider object (a URL plus an optional apiKey). The other three are
// the vendor providers a hub with ainsel-project/ainsel#242 accepts, each with
// its own key block (openAI, anthropic, google) — a hub that predates it
// rejects both the id and the block.
//
// The settings registry names its dropdown entries with these same constants,
// so the two lists cannot drift.
const (
	LLMProviderCustom    = "custom"
	LLMProviderOpenAI    = "openai"
	LLMProviderAnthropic = "anthropic"
	LLMProviderGoogle    = "google"
)

// The op kinds. They name the resources an install owns, the same set the
// install row keeps ids for.
const (
	KindUser        = "user"
	KindTeam        = "team"
	KindMCPServer   = "mcpServer"
	KindPersona     = "persona"
	KindImage       = "image"
	KindAgent       = "agent"
	KindConnector   = "connector"
	KindTrigger     = "trigger"
	KindCronTrigger = "cronTrigger"
	KindHook        = "hook"
)

// Verdict is the plan's judgement on whether the install can be applied. A
// blocking reason says the apply would fail or do something the plugin never
// does (take over a resource it did not create); a warning says the install will
// work, but not the way the shipped persona expects.
type Verdict struct {
	Blocking []string `json:"blocking"`
	Warnings []string `json:"warnings"`
}

// Result is what Plan decided. The slices are never nil, so a caller can render
// them straight away.
type Result struct {
	Create   []Op      `json:"create"`
	Change   []Op      `json:"change"`
	Delete   []Op      `json:"delete"`
	Triggers []Trigger `json:"triggers"`
	// CronTriggers are the install's scheduled prompts — the developer's PR
	// patrol. They are planned and applied with the event triggers but live on
	// their own: no connector, no filters, and identity by name.
	CronTriggers []CronTrigger `json:"cronTriggers"`
	Verdict      Verdict       `json:"verdict"`
}

// Input is the wizard's whole form: the instance, the admin token the accounts
// need, and the three roles. What used to be on it — the forge MCP endpoint and
// each role's llm — the settings store owns now, and arrives on the snapshot as
// the effective values instead. It holds secrets, and they are pass-through:
// Plan never puts one in an Op or a verdict message (R4).
type Input struct {
	// Slug names the install and, through it, every resource it owns. Globally
	// unique, and it must match slugPattern.
	Slug string

	// GroupID is the AInsel group every resource is filed under.
	GroupID string

	// BaseURL is the Forgejo address, org hook and links included.
	BaseURL string

	// Org is the Forgejo organisation the bots, the teams and the hook live in.
	Org string

	// ExcludeLabel keeps the refiner off an issue; empty means DefaultExcludeLabel.
	ExcludeLabel string

	// AdminToken is the Forgejo admin token the account work needs. Empty on an
	// edit that changes no accounts — which is every edit: the logins and teams
	// follow the slug, and the slug cannot change.
	AdminToken string

	// Roles holds one entry per role. A role the map does not name reads as the
	// zero RoleInput: default tools, default agent name, and no runtime — which
	// blocks.
	Roles map[Role]RoleInput
}

// RoleInput is what the wizard collects for one role (S4) — only what is per
// install. The role's llm is not on it: the settings store owns that, and the
// plan reads the effective value off the snapshot. A picked-up agent, persona,
// image or MCP server is not on it either: an install creates its own, always.
type RoleInput struct {
	// Runtime is the role image's container image URL. Blank means "keep what the
	// install has" on an edit, and blocks on a new install.
	Runtime string

	// AgentName is the agent's display name. Blank means the default,
	// forgejo-<role>-<slug> (R3).
	AgentName string

	// Tools is the role's tool set. nil means the shipped defaults.
	Tools []string

	// WorkingHours is the window the role's agent takes queued work in. nil is the
	// answer "any hours", which is the default and what the wizard prefills an install
	// that never set one with — and, on an edit, the answer that clears a window the
	// wizard wrote before. It is never read back from the hub: the diff below is
	// against what the wizard last wrote, so a window an admin set in the console is
	// left alone.
	WorkingHours *WorkingHours

	// TurnTimeoutMs is the role image's TURN_TIMEOUT_MS: the runner's per-turn
	// wall-clock cap, in milliseconds, as a decimal string — the same unit the
	// env var, the shipped defaults and the hub all speak, so the diff stays an
	// exact string comparison like the other fields (the UI converts from the
	// minutes the admin types). Empty means the shipped default on a new
	// install, and "keep what the install has" on an edit — never "reset to
	// the default", which is what lets a hand-patched image value survive.
	TurnTimeoutMs string

	// LLMKey is the optional provider key the admin may type for the role in the
	// wizard itself. It is a pass-through like the settings' key rows: it reaches
	// the create (or the update) as the agent's own hub secret and never lands on
	// the install row or in the settings table (R4). Empty says nothing — the
	// agent is created without a key and the admin can enter one on Settings once
	// the install exists, the way a key entered before any install has to be.
	LLMKey string `json:"llmKey,omitempty"`
}

// RoleLLM is the model one role's agent is created on — the effective value of
// the llm settings, in the shape the setup flow reads them.
//
// CustomURL and CustomKey are read only when Provider is LLMProviderCustom:
// they are what the apply feeds the hub's customProvider object. OpenAIKey,
// AnthropicKey and GoogleKey are read only when Provider names their own
// provider — the hub's openAI, anthropic and google blocks. The four keys are
// no longer the settings store's to hold (they are a pass-through written onto
// the agents); what reaches this shape is either the wizard's optional per-role
// key (KeyFromWizard) or a key an upgrade migration is forwarding. No key ever
// lands on the install row (R4).
//
// OpenAIAuthMethod is read only when Provider is openai: it says whether the
// role authenticates with OpenAIKey or with a ChatGPT sign-in an admin finishes
// in the console (see LLMAuthMethodChatGPTSignIn). A role on the sign-in method
// sends no key, whatever its key row holds.
//
// KeyFromWizard says the key this reading carries came from the wizard's own
// optional per-role key rather than from the settings store — and the settings
// store holds no key at all any more (the four key fields are a pass-through
// written straight onto the role's agents). A wizard key is always a write the
// admin asked for: the plan previews it even when the row's fingerprint already
// matches, because the plugin keeps no copy to compare against.
type RoleLLM struct {
	Provider         string
	Model            string
	CustomURL        string
	CustomKey        string
	OpenAIKey        string
	AnthropicKey     string
	GoogleKey        string
	OpenAIAuthMethod string
	KeyFromWizard    bool
}

// LLMKeyOf is the key a role's settings hold for the provider the role runs
// on, "" for a provider the plugin holds no key for (ollama-cloud, opencode,
// alibaba-cloud). One lookup per provider keeps the apply, the plan and the
// reconcile pass from each carrying their own copy of that switch.
func (l RoleLLM) LLMKeyOf(provider string) string {
	switch provider {
	case LLMProviderCustom:
		return l.CustomKey
	case LLMProviderOpenAI:
		return l.OpenAIKey
	case LLMProviderAnthropic:
		return l.AnthropicKey
	case LLMProviderGoogle:
		return l.GoogleKey
	}
	return ""
}

// LLMKeyFieldOf names the settings field a provider's key lives in — the field
// a change op reports on, so the preview line and the settings row the admin
// edits agree on one name. It is also what the settings route and the startup
// migration use to name the field a pushed key came from.
func LLMKeyFieldOf(provider string) string {
	switch provider {
	case LLMProviderCustom:
		return FieldLLMCustomKey
	case LLMProviderOpenAI:
		return FieldLLMOpenAIKey
	case LLMProviderAnthropic:
		return FieldLLMAnthropicKey
	case LLMProviderGoogle:
		return FieldLLMGoogleKey
	}
	return ""
}

// Snapshot is what the instance looked like when the caller read it. Building it
// is the API's job — the hub listings, the plugin's groups, one Forgejo call for
// the token and one for the org — so that Plan itself stays pure and the preview
// and the apply can be shown to agree.
type Snapshot struct {
	// Existing is the install being edited, nil for a new one. It decides the
	// whole shape of the plan: a new install creates everything, an edit diffs
	// against this.
	Existing *Install

	// Prior is what the install's resources hold today, read back from AInsel:
	// the image's runtime, the agent's display name and its llm. The install row
	// keeps no copy of those — they live in AInsel — so an edit has no baseline
	// without them. Empty on a new install. It never carries a key: a key is
	// never stored (R4).
	Prior map[Role]PriorState

	// CallerGroups are the groups the caller may write to. The install's group has
	// to be one of them, or the creates are refused.
	CallerGroups []string

	// PluginGroups are the groups the plugin's background identity may act for
	// (GET /api/v1/plugin-context). An install in another group would be created
	// now and unreconcilable for ever, so it blocks (S1).
	PluginGroups []string

	// TakenNames reports, per "<kind>:<name>", whether that name already exists.
	// The kinds the plan asks about are slug, login, team, mcp, persona, image,
	// agent, connector, trigger and cronTrigger — every name a create would use, so
	// a taken one blocks in the preview instead of failing the apply halfway.
	TakenNames map[string]bool

	// AdminChecked says the caller asked Forgejo who the admin token belongs to;
	// AdminIsAdmin is the answer. A token that was not checked is not treated as
	// an admin's, and not treated as a non-admin's either: only a checked
	// non-admin blocks here.
	AdminChecked bool
	AdminIsAdmin bool

	// OrgExists says the Forgejo organisation was found.
	OrgExists bool

	// DefaultTools and CheckTools are the shipped role profiles and their R7 guard
	// rails. They come from package content, which imports this package — so the
	// plan takes them as functions instead of importing them back. Both must be
	// set; a plan with either missing fails closed with a blocking message.
	DefaultTools func(Role) []string
	CheckTools   func(Role, []string) error

	// Settings are the effective values of the fields the settings store owns:
	// each role's llm and the forge MCP server's wiring. The wizard used to
	// collect them from the admin; now the API reads the settings store before
	// planning and hands the answer over here, the same way it hands over the
	// taken names — which keeps Plan pure and the preview and the apply in
	// agreement about what the install will be created with.
	Settings Settings
}

// Settings is one reading of the settings store, in the shape the setup flow
// reads it. The persona and skill texts are not here: the install's persona is
// rendered from the shipped template and the skill is the manifest's, and what
// the settings say for those reaches the resources through the reconcile pass,
// not through a create.
type Settings struct {
	// LLM is the model each role's agent is created on. A role with no entry —
	// or an empty one — has no model to create with, which blocks.
	LLM map[Role]RoleLLM

	// MCPURL is the forge MCP endpoint the install's own MCP server points at;
	// MCPTokenFromEnv names the env var its token arrives in.
	MCPURL          string
	MCPTokenFromEnv string
}

// llm is what the reading says a role runs on; the zero value when it named no
// entry for the role, which the plan blocks on and the apply never reaches.
func (s Settings) llm(r Role) RoleLLM {
	return s.LLM[r]
}

// PriorState is one role's current state as AInsel has it, without secrets. It is
// the baseline an edit is diffed against.
type PriorState struct {
	// Runtime is the role image's container image URL.
	Runtime string

	// AgentName is the agent's display name.
	AgentName string

	// Provider and Model are the agent's llm — what the settings values are
	// diffed against on an edit. CustomURL is the agent's custom endpoint when
	// it runs on the custom provider; there is no key value here: a key is
	// never stored and never read back. The hashes are the fingerprints the
	// install row recorded as last written, one per provider whose key the
	// plugin delivers — the only key state the plan can see, since the hub
	// cannot answer one.
	Provider         string
	Model            string
	CustomURL        string
	CustomKeyHash    string
	OpenAIKeyHash    string
	AnthropicKeyHash string
	GoogleKeyHash    string

	// OpenAIAuthMethod is the auth method the hub reports for the agent —
	// "APIKey" or "ChatGPTSignIn". The hub answers LLMAuthMethodAPIKey when the
	// agent carries none, so an empty value here means the caller read no agent
	// at all, which the diff treats as the API key.
	OpenAIAuthMethod string
}

// LLMKeyHashOf is the recorded fingerprint of the key the agent was last
// written with for one provider, "" when the row records none.
func (p PriorState) LLMKeyHashOf(provider string) string {
	switch provider {
	case LLMProviderCustom:
		return p.CustomKeyHash
	case LLMProviderOpenAI:
		return p.OpenAIKeyHash
	case LLMProviderAnthropic:
		return p.AnthropicKeyHash
	case LLMProviderGoogle:
		return p.GoogleKeyHash
	}
	return ""
}

// slugPattern is the slug the constraints allow: a lowercase letter, then up to 30
// lowercase letters, digits and dashes, ending in a letter or a digit. Globally
// unique is checked against the snapshot, not here.
var slugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,29}[a-z0-9]$`)

// planRoles is the order the plan lists roles everywhere: the refiner prepares the
// work, the developer does it, the reviewer reviews it.
var planRoles = []Role{RoleRefiner, RoleDeveloper, RoleReviewer}

// TokenEnvName is the env var every role image carries the role's bot token in,
// and the name the install's forge MCP server reads its token from.
const TokenEnvName = "FORGEJO_TOKEN"

// TurnTimeoutEnvName is the env var the ainsel runner reads its per-turn wall-clock
// cap (milliseconds) from. It is not llm.maxTurns: maxTurns counts turns, this
// bounds how long one may run. The runner's default (600 s) is too short for the
// developer role's real work (issue #20), so the wizard ships a per-role default
// on the role image — see DefaultTurnTimeoutMs — and the admin can set a per-role
// value in the wizard (issue #61) — see RoleInput.TurnTimeoutMs.
const TurnTimeoutEnvName = "TURN_TIMEOUT_MS"

// turnTimeoutEnvRewrite says what a turn-timeout write does to the rest of the
// image's environment. The hub's image update replaces the whole env list, and the
// plugin cannot read that list back to merge into it, so the write restates the
// image as exactly TokenEnvName plus TurnTimeoutEnvName: an env entry an admin added
// to the role image by hand does not outlive a timeout change. The plan detail and
// the apply report both carry the sentence, so the admin reads it before the write
// rather than finding it in the image afterwards.
const turnTimeoutEnvRewrite = "rewrites the image env: only " + TokenEnvName + " and " + TurnTimeoutEnvName + " remain on it"

// The bounds a wizard-set turn timeout has to fall inside: one minute to one day,
// in milliseconds. A typo like 90000000000000 is worse than no timeout at all — a
// turn that can never be seen to fail — so an out-of-range value blocks the plan
// rather than silently falling back.
const (
	minTurnTimeoutMs = 60000
	maxTurnTimeoutMs = 86400000
)

// signatureHeader is the header the install's connector verifies its deliveries
// with: Forgejo signs with the connector's secret, bare hex.
const signatureHeader = "X-Forgejo-Signature"

// pluginGrantAction says where an admin grants the plugin a group. A blocked plan
// that is only about the group has to say it, because that is the fix: the console's
// plugin page first, since that is where an admin is, and the MCP tool beside it.
const pluginGrantAction = "Plugins → Forgejo → Group Access in the AInsel console (or set_plugin_group_access)"

// hookEvents are the events the install's org webhook subscribes to. Reviews come
// as their own event type (V5), so they are listed beside the pull requests.
var hookEvents = []string{"issues", "issue_comment", "pull_request", "pull_request_review"}

// HookEvents returns the events the install's org webhook subscribes to — a copy,
// because the caller hands them to Forgejo and may edit what it gets.
func HookEvents() []string { return slices.Clone(hookEvents) }

// personaTools are the tools each role's shipped persona writes as its own: switch
// one off and the persona asks for a tool the agent no longer has. The spec names
// create_issue_comment for every role; the CI reads are pinned per role because
// each persona answers "is this green?" with a specific tool — a reviewer without
// one has to conclude something anyway, and history shows it concludes "passed".
var personaTools = map[Role][]string{
	RoleRefiner:   {"create_issue_comment", "get_file_content"},
	RoleDeveloper: {"create_issue_comment", "get_file_content", "get_pull_request_status"},
	RoleReviewer:  {"create_issue_comment", "get_file_content", "get_pull_request_status", "get_commit_statuses"},
}

// teamUnits is the role's permissions on the org's repositories (spec §9), in the
// units_map Forgejo's team API takes. Code is the developer's alone; the reviewer
// is the only one who reviews.
var teamUnits = map[Role]map[string]string{
	RoleRefiner:   {"repo.code": "read", "repo.issues": "write", "repo.pulls": "read"},
	RoleDeveloper: {"repo.code": "write", "repo.issues": "write", "repo.pulls": "write"},
	RoleReviewer:  {"repo.code": "read", "repo.issues": "write", "repo.pulls": "write"},
}

// TeamUnits returns the role's team permissions, in the units_map shape Forgejo
// takes. A copy, because the caller hands it to the API.
func TeamUnits(r Role) map[string]string {
	units := teamUnits[r]
	out := make(map[string]string, len(units))
	for k, v := range units {
		out[k] = v
	}
	return out
}

// Plan decides what an install does to the instance.
func Plan(in Input, snap Snapshot) Result {
	// The form arrives as typed. A trailing tab on a runtime is a copy-paste artefact,
	// not a value, and the hub takes it onto the image while the runtime refuses the
	// pod — so the plan is drawn on the cleaned input, and every diff, op and stored
	// row below reads the same value the apply writes.
	in = Normalize(in)

	res := Result{
		Create:       []Op{},
		Change:       []Op{},
		Delete:       []Op{},
		Triggers:     []Trigger{},
		CronTriggers: []CronTrigger{},
		Verdict:      Verdict{Blocking: []string{}, Warnings: []string{}},
	}
	block := func(format string, a ...any) {
		res.Verdict.Blocking = append(res.Verdict.Blocking, fmt.Sprintf(format, a...))
	}
	warn := func(format string, a ...any) {
		res.Verdict.Warnings = append(res.Verdict.Warnings, fmt.Sprintf(format, a...))
	}

	// --- the slug: its shape, and, on a new install, that no install has it
	if !slugPattern.MatchString(in.Slug) {
		block("slug %q is invalid: it must match %s", in.Slug, slugPattern.String())
	} else if snap.Existing == nil && snap.TakenNames["slug:"+in.Slug] {
		block("slug %q is already used by another install", in.Slug)
	}

	// --- the group: the caller has to be able to write it, and the plugin to act
	// for it later, which is what makes the install reconcilable at all (§3.6).
	if in.GroupID == "" {
		block("no AInsel group is selected for the install")
	} else {
		if !slices.Contains(snap.CallerGroups, in.GroupID) {
			block("group %q is not writable: pick a group where you are an owner or a writer", in.GroupID)
		}
		if !slices.Contains(snap.PluginGroups, in.GroupID) {
			block("group %q is not granted to the plugin: an admin has to grant it in %s", in.GroupID, pluginGrantAction)
		}
	}

	// --- the Forgejo side of a new install: an admin's token and an org to put the
	// bots in. An edit changes no accounts — the logins and teams follow the slug,
	// which cannot change — so it needs neither.
	if snap.Existing == nil {
		if in.AdminToken == "" {
			block("a Forgejo admin token is required to create the install's bot accounts")
		} else if snap.AdminChecked && !snap.AdminIsAdmin {
			block("the Forgejo admin token belongs to a user who is not a site administrator")
		}
		if !snap.OrgExists {
			block("the Forgejo organisation %q does not exist", in.Org)
		}
	}

	// --- role tool guard rails: both DefaultTools and CheckTools must be set
	// (they are a security property: least privilege per role). Missing either
	// is a failure that blocks any tool-dependent operations.
	if snap.DefaultTools == nil || snap.CheckTools == nil {
		block("internal: role tool checks are not configured")
		// Return early with the blocking message and no ops. No tool-dependent
		// operations are computed when the security properties are missing.
		return res
	}

	// --- the forge MCP endpoint: the hub requires a URL on the server the install
	// creates for itself. The admin does not type it any more — the settings
	// store holds it — but an install cannot be created while the setting is
	// still the empty shipped default, and the block says where to set it.
	if snap.Existing == nil && snap.Settings.MCPURL == "" {
		block("the forge MCP server needs its endpoint URL: set it in the plugin's Settings")
	}

	// --- every name a create would use. On an edit nothing new is created — the
	// trio is fixed by construction — so the install's own names, which are
	// honestly reported as taken, must not read as a conflict.
	if snap.Existing == nil {
		for _, n := range wantedNames(in, snap) {
			if snap.TakenNames[n.key] {
				msg := fmt.Sprintf("%s %q is already taken: an install only creates its own resources", takenKind(n.kind), n.name)
				msg += leftBehindHint(n.kind, n.name, in.Slug)
				block("%s", msg)
			}
		}
	}

	// --- per role: the guard rails, the container, and a model to run on.
	agentNames := map[string]Role{}
	for _, r := range planRoles {
		tools := effectiveTools(r, in, snap)

		if err := snap.CheckTools(r, tools); err != nil {
			block("%s", err.Error())
		}
		rt := effectiveRuntime(r, in, snap)
		if rt == "" {
			block("role %q: no runtime container is set", r)
		}

		// Whitespace inside a ref is never a valid image URL: the hub takes it onto the
		// AgentImage, and the pod for every task of the role is then refused with
		// "must not have leading or trailing whitespace" — a Job that never gets a pod
		// still holds the agent's task slot, so the install goes quiet with no error in
		// the hub. Normalize trimmed the copy-paste case (a tab around the ref); what
		// survives is whitespace inside it, which has no sensible repair but typing it out.
		//
		// A runtime with no tag and no digest is a floating ref: the registry resolves it
		// to `latest`, which may not exist at all and is never reproducible. An install
		// created with one has every task pod fail at pull while the tasks sit at QUEUED.
		//
		// Both are a value the admin just typed, so both block — the same way a missing
		// runtime does. A value the install already carries is different: it exists and
		// may well be working, so the edit that merely keeps it warns rather than
		// freezing the install — the split the OpenAI auth-method warnings above use.
		if rt != "" && runtimeHasWhitespace(rt) {
			if rt == snap.prior(r).Runtime {
				warn("role %q: runtime %q contains whitespace — a container image URL has none", r, rt)
			} else {
				block("role %q: runtime %q contains whitespace — a container image URL has none", r, rt)
			}
		} else if rt != "" && !runtimeHasVersion(rt) {
			if rt == snap.prior(r).Runtime {
				warn("role %q: runtime %s has no tag or digest — add a tag such as :8.0, or a @sha256: digest", r, rt)
			} else {
				block("role %q: runtime %s has no tag or digest — add a tag such as :8.0, or a @sha256: digest", r, rt)
			}
		}

		// A window the hub cannot use is a 400 from the hub, mid-apply, after the
		// accounts and the image are already made. The plan names the role instead.
		if err := ValidateWorkingHours(in.role(r).WorkingHours); err != nil {
			block("role %q: %s", r, err.Error())
		}

		// A wizard-set turn timeout is a wall-clock ceiling the runner enforces, so a
		// nonsense one is not a preference — it is an install whose turns either die
		// in seconds or never time out. Block on it, the same way a missing runtime
		// blocks, rather than falling back to a default the admin did not choose.
		if v := in.role(r).TurnTimeoutMs; v != "" && !validTurnTimeoutMs(v) {
			block("role %q: turn timeout %q is not a whole number of milliseconds between %d and %d", r, v, minTurnTimeoutMs, maxTurnTimeoutMs)
		}

		// The model the agent is created on is the setting's, whatever the install
		// has: an install that predates a settings change is corrected by the next
		// apply, the same way the container is. A role whose setting answers nothing
		// has no model to create with, and saying so beats sending an agent with no
		// brain.
		llm := snap.llm(r)
		if llm.Provider == "" || llm.Model == "" {
			block("role %q: no llm is set — the plugin's Settings answer nothing for this role", r)
		}

		// The OpenAI auth method, warned about rather than blocked: both of its
		// broken states leave an agent that exists and does not work, which is
		// what a warning is for. A role on ChatGPT sign-in runs nothing until an
		// admin finishes the sign-in in the console, and a stored OpenAI key is
		// then inert — the apply never sends it beside the sign-in method, because
		// the hub refuses the pair. A role that left sign-in with no key has the
		// other half: the hub keeps the method it holds until this write moves it,
		// and an API-key agent with no key answers nothing.
		if llm.Provider == LLMProviderOpenAI {
			if IsChatGPTSignIn(llm.OpenAIAuthMethod) {
				warn("role %q: ChatGPT sign-in is chosen — the agent runs nothing until an admin finishes the sign-in for it in the AInsel console", r)
				if llm.OpenAIKey != "" {
					warn("role %q: an OpenAI API key is stored but the role signs in with ChatGPT, so it is never sent — Reset the %s row to clear it", r, FieldLLMOpenAIKey)
				}
			} else if priorAuthMethod(snap.prior(r)) == LLMAuthMethodChatGPTSignIn && llm.OpenAIKey == "" {
				warn("role %q: the role moves from ChatGPT sign-in back to an API key with none stored — set %s or the agent will have no credential", r, FieldLLMOpenAIKey)
			}
		}

		// One agent per role (R8): an agent named twice is one agent two roles
		// fight over.
		name := effectiveAgentName(r, in, snap)
		if other, dup := agentNames[name]; dup {
			block("roles %q and %q would both create the agent %q: every agent needs its own name", other, r, name)
		} else {
			agentNames[name] = r
		}

		// A tool the persona writes as its own, switched off: the install works and
		// the agent just cannot do what its persona says it does. A warning (spec
		// §3.3), never a block. An unknown tool set warns about nothing.
		if tools != nil {
			for _, t := range personaTools[r] {
				if !slices.Contains(tools, t) {
					warn("role %q: %s is off, but the persona relies on it", r, t)
				}
			}
		}
	}

	// --- the ops. The triggers are generated from the install the plan ends up
	// wanting, so the preview's list, the verdict and the apply read one set.
	want := wantedInstall(in, snap)
	res.Triggers = Triggers(want)
	res.CronTriggers = CronTriggers(want)

	if snap.Existing == nil {
		res.Create = append(res.Create, createOps(in, snap, want)...)
	}
	if want.HookID == 0 {
		// S7's webhook: an install that was created but never connected still has
		// it to create, and nothing else.
		res.Create = append(res.Create, Op{Kind: KindHook, Name: want.Org, Detail: strings.Join(hookEvents, ", ")})
	}
	if snap.Existing != nil {
		res.Change = append(res.Change, changeOps(in, snap, want)...)
	}
	return res
}

// wantedInstall is the install the plan ends up wanting: the input over what the
// install already is, with the bot logins the slug derives and the ids the install
// has. It is what the trigger generator reads, so a new install — whose agent and
// connector do not exist yet — has empty refs that apply fills after the creates.
func wantedInstall(in Input, snap Snapshot) Install {
	slug := in.Slug
	want := Install{
		Slug:         slug,
		GroupID:      in.GroupID,
		BaseURL:      in.BaseURL,
		Org:          in.Org,
		MCPServer:    MCPServerName(slug),
		ExcludeLabel: in.ExcludeLabel,
		Roles:        map[Role]RoleState{},
	}
	if ex := snap.Existing; ex != nil {
		if want.GroupID == "" {
			want.GroupID = ex.GroupID
		}
		if want.BaseURL == "" {
			want.BaseURL = ex.BaseURL
		}
		if want.Org == "" {
			want.Org = ex.Org
		}
		if want.ExcludeLabel == "" {
			want.ExcludeLabel = ex.ExcludeLabel
		}
		want.ConnectorID = ex.ConnectorID
		want.HookID = ex.HookID
		for _, r := range planRoles {
			st := ex.Roles[r]
			want.Roles[r] = RoleState{
				AgentID:  st.AgentID,
				ImageID:  st.ImageID,
				BotLogin: st.BotLogin,
				Tools:    effectiveTools(r, in, snap),
			}
		}
		return want
	}
	for _, r := range planRoles {
		want.Roles[r] = RoleState{
			BotLogin: BotLogin(r, slug),
			Tools:    effectiveTools(r, in, snap),
		}
	}
	return want
}

// createOps are the ops a new install runs, in the order apply runs them: the
// accounts first, because everything else posts as one of them, then the MCP
// server the images list, then the personas the agents carry, the images the
// agents run, the agents, the connector the triggers read and the triggers
// themselves. The org hook comes last of all, because it needs the connector.
func createOps(in Input, snap Snapshot, want Install) []Op {
	ops := make([]Op, 0, 24)
	slug := want.Slug
	for _, r := range planRoles {
		ops = append(ops, Op{Kind: KindUser, Name: BotLogin(r, slug), Detail: BotFullName(r, slug)})
	}
	for _, r := range planRoles {
		ops = append(ops, Op{
			Kind: KindTeam,
			Name: TeamName(r, slug),
			Detail: fmt.Sprintf("%s; all repositories; member %s",
				unitsSummary(r), BotLogin(r, slug)),
		})
	}
	ops = append(ops, Op{
		Kind:   KindMCPServer,
		Name:   MCPServerName(slug),
		Detail: fmt.Sprintf("%s; tokenFromEnv %s", snap.Settings.MCPURL, snap.Settings.MCPTokenFromEnv),
	})
	for _, r := range planRoles {
		ops = append(ops, Op{
			Kind: KindPersona, Name: PersonaName(r, slug),
			Detail: fmt.Sprintf("predefined %s persona, with the install's names", r),
		})
	}
	for _, r := range planRoles {
		ops = append(ops, Op{
			Kind: KindImage, Name: ImageName(r, slug),
			Detail: fmt.Sprintf("runtime %s; mcpServers %s, %s; skill %s; %d tools; turn timeout %s",
				effectiveRuntime(r, in, snap), MCPServerName(slug), pluginServerName, skillName,
				len(effectiveTools(r, in, snap)), turnTimeoutSummary(r, in, snap)),
		})
	}
	for _, r := range planRoles {
		llm := snap.llm(r)
		detail := fmt.Sprintf("image %s; persona %s; llm %s/%s%s",
			ImageName(r, slug), PersonaName(r, slug), llm.Provider, llm.Model, llmAuthSuffix(llm))
		// A window is only worth naming on the create when there is one: an install that
		// set none leaves the agent open at every hour, which is what the preview already
		// says by not saying anything.
		if w := in.role(r).WorkingHours; w != nil {
			detail += "; working hours " + WorkingHoursText(w)
		}
		ops = append(ops, Op{
			Kind: KindAgent, Name: effectiveAgentName(r, in, snap),
			Detail: detail,
		})
	}
	ops = append(ops, Op{Kind: KindConnector, Name: ConnectorName(slug), Detail: "signatureHeader " + signatureHeader})
	for _, t := range Triggers(want) {
		ops = append(ops, Op{Kind: KindTrigger, Name: t.Name, Detail: filtersSummary(t)})
	}
	for _, ct := range CronTriggers(want) {
		ops = append(ops, Op{Kind: KindCronTrigger, Name: ct.Name, Detail: cronSummary(ct)})
	}
	return ops
}

// changeOps are the field-by-field diffs of an edit: what the input changes about
// a resource the install already has. Nothing an edit does deletes a resource — the
// three roles are fixed, and a tool taken off a set is a change to the image that
// carries it — so a plan has nothing to delete.
func changeOps(in Input, snap Snapshot, want Install) []Op {
	ex := snap.Existing
	ops := make([]Op, 0, 4)

	// The persona text names the forge's address, so a new base URL rewrites all
	// three personas.
	if in.BaseURL != "" && in.BaseURL != ex.BaseURL {
		for _, r := range planRoles {
			ops = append(ops, Op{
				Kind: KindPersona, Name: PersonaName(r, ex.Slug),
				Detail: diff("base URL", ex.BaseURL, in.BaseURL),
				Role:   r, Field: FieldBaseURL,
			})
		}
	}

	for _, r := range planRoles {
		prior := snap.prior(r)

		if rt := effectiveRuntime(r, in, snap); rt != "" && prior.Runtime != "" && rt != prior.Runtime {
			ops = append(ops, Op{
				Kind: KindImage, Name: ImageName(r, ex.Slug),
				Detail: diff("runtime", prior.Runtime, rt),
				Role:   r, Field: FieldRuntime,
			})
		}
		if d := toolsDiff(ex.Roles[r].Tools, effectiveTools(r, in, snap)); d != "" {
			ops = append(ops, Op{
				Kind: KindImage, Name: ImageName(r, ex.Slug),
				Detail: d,
				Role:   r, Field: FieldTools,
			})
		}

		// The turn timeout is diffed against the value the row recorded at the last
		// write — not against the live image, which the plugin cannot read back in
		// full. An empty input is never a change: "keep" is what lets a value set on
		// the image by hand survive a re-apply (issue #61).
		if v := in.role(r).TurnTimeoutMs; v != "" && v != ex.Roles[r].TurnTimeoutMs {
			ops = append(ops, Op{
				Kind: KindImage, Name: ImageName(r, ex.Slug),
				Detail: diff("turn timeout", ex.Roles[r].TurnTimeoutMs, v) + "; " + turnTimeoutEnvRewrite,
				Role:   r, Field: FieldTurnTimeout,
			})
		}

		// An agent with no stored display name has the default one, which is what
		// an empty input keeps.
		current := prior.AgentName
		if current == "" {
			current = AgentName(r, ex.Slug)
		}
		if name := effectiveAgentName(r, in, snap); name != current {
			ops = append(ops, Op{
				Kind: KindAgent, Name: current,
				Detail: diff("name", current, name),
				Role:   r, Field: FieldAgentName,
			})
		}
		for _, c := range []struct {
			field, was, now string
		}{
			{FieldLLMProvider, prior.Provider, snap.llm(r).Provider},
			{FieldLLMModel, prior.Model, snap.llm(r).Model},
			// The custom endpoint only means something while the role runs
			// on the custom provider: away from it the hub's stored
			// customProvider is inert, and diffing it would preview a write
			// that changes nothing.
			{FieldLLMCustomUrl, prior.CustomURL, customURLOf(snap.llm(r))},
			// The OpenAI auth method, likewise only while the role runs on
			// openai: away from it the hub's openAI block is inert, and the
			// method it holds is nobody's business.
			{FieldLLMOpenAIAuthMethod, priorAuthMethod(prior), openAIAuthMethodForRole(snap.llm(r))},
		} {
			if c.now != "" && c.now != c.was {
				ops = append(ops, Op{
					Kind: KindAgent, Name: current,
					Detail: diff(llmFieldWord(c.field, ""), c.was, c.now),
					Role:   r, Field: c.field,
				})
			}
		}

		// A key cannot be diffed by value — the hub never reads one back — so
		// its drift is seen through the row's fingerprint: a settings key whose
		// hash the row does not record has not reached the agent. Only the
		// provider the role runs on is looked at: away from it the hub's block
		// for that provider is inert, and previewing it would promise a write
		// that changes nothing. The detail names the change and the provider,
		// without spelling the key. An empty settings key says nothing (the hub
		// keeps what it holds on an empty write); clearing is what a reset does,
		// and a reset's row carries the empty default — see docs/setup.md.
		if provider := snap.llm(r).Provider; provider != "" &&
			!(provider == LLMProviderOpenAI && IsChatGPTSignIn(snap.llm(r).OpenAIAuthMethod)) {
			// A role on ChatGPT sign-in has no key to write: the apply never sends
			// one beside the sign-in method, so previewing a key the agent will
			// never be given would promise a write that cannot happen (and repeat
			// it on every re-apply, since the row's fingerprint would never move).
			if key := snap.llm(r).LLMKeyOf(provider); key != "" &&
				(snap.llm(r).KeyFromWizard || CustomKeyFingerprint(key) != prior.LLMKeyHashOf(provider)) {
				detail := llmFieldWord(LLMKeyFieldOf(provider), provider) + " replaced"
				if prior.LLMKeyHashOf(provider) == "" {
					detail = diff(llmFieldWord(LLMKeyFieldOf(provider), provider), "none", "set")
				}
				ops = append(ops, Op{
					Kind: KindAgent, Name: current,
					Detail: detail,
					Role:   r, Field: LLMKeyFieldOf(provider),
				})
			}
		}

		// Working hours are diffed against what the wizard last wrote — the row's record —
		// and not against the agent's live window, which is the one place the re-apply
		// loop departs from "diff against what AInsel has today". It has to: an admin who
		// sets a window in the console is the hub's to keep, and a live diff would write
		// the wizard's value back over it on the next re-apply that changed anything at
		// all. So an untouched form (prefilled from the row) reports no change, a window
		// the wizard never set is never written, and only a form the admin moved wins.
		if was, now := ex.Roles[r].WorkingHours, in.role(r).WorkingHours; !WorkingHoursEqual(was, now) {
			ops = append(ops, Op{
				Kind: KindAgent, Name: current,
				Detail: diff("working hours", anyHoursText(was), anyHoursText(now)),
				Role:   r, Field: FieldWorkingHours,
			})
		}
	}

	// The exclude label is a filter on the refiner's two issue triggers, and on
	// nothing else.
	if was, now := excludeOf(ex.ExcludeLabel), excludeOf(want.ExcludeLabel); was != now {
		for _, t := range Triggers(want) {
			if t.Name == triggerName(RoleRefiner, "issue-opened", want.Slug) ||
				t.Name == triggerName(RoleRefiner, "issue-reopened", want.Slug) {
				ops = append(ops, Op{
					Kind: KindTrigger, Name: t.Name,
					Detail: diff("exclude label", was, now),
					Role:   RoleRefiner, Field: FieldExcludeLabel,
				})
			}
		}
	}
	return ops
}

// customURLOf is the custom endpoint a role's settings name, or "" when the
// role does not run on the custom provider — away from it the URL is inert and
// must not preview a change.
func customURLOf(llm RoleLLM) string {
	if llm.Provider != LLMProviderCustom {
		return ""
	}
	return llm.CustomURL
}

// openAIAuthMethodForRole is the auth method a role's settings name, or "" when
// the role does not run on openai — away from it the hub's openAI block is
// inert and must not preview a change.
func openAIAuthMethodForRole(llm RoleLLM) string {
	if llm.Provider != LLMProviderOpenAI {
		return ""
	}
	return OpenAIAuthMethodOf(llm)
}

// priorAuthMethod is what the hub says the agent authenticates with today,
// defaulted to the method the hub itself answers for an agent that never named
// one — so a first edit of an older install does not preview a change that
// changes nothing.
func priorAuthMethod(prior PriorState) string {
	if IsChatGPTSignIn(prior.OpenAIAuthMethod) {
		return LLMAuthMethodChatGPTSignIn
	}
	return LLMAuthMethodAPIKey
}

// CustomKeyFingerprint is the hash the install row keeps of a provider key —
// any of the four the plugin delivers, not only the custom one: enough for the
// plan and the reconcile pass to see that the settings' key is not the one the
// agent was last written with, and nothing — not the key — for anyone who reads
// the row (R4). The empty key has the empty fingerprint.
func CustomKeyFingerprint(key string) string {
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// llmFieldWord spells the field a change op reports on, for the line the admin
// reads in the preview. A key field names its provider, because an agent now
// has four key rows and "llm key" alone would not say which one moved.
func llmFieldWord(field, provider string) string {
	switch field {
	case FieldLLMProvider:
		return "llm provider"
	case FieldLLMModel:
		return "llm model"
	case FieldLLMCustomUrl:
		return "llm URL"
	case FieldLLMOpenAIAuthMethod:
		return "llm auth method"
	case FieldLLMCustomKey, FieldLLMOpenAIKey, FieldLLMAnthropicKey, FieldLLMGoogleKey:
		if provider == "" {
			return "llm key"
		}
		return "llm key (" + provider + ")"
	}
	return field
}

// llmAuthSuffix spells how an openai role authenticates, on the create line the
// preview shows: the API key is the ordinary case and says nothing extra, the
// sign-in method names the step the admin still has to take, because an agent
// created that way runs nothing until it happens. Empty for every other
// provider, which has one auth shape and no choice to name.
func llmAuthSuffix(llm RoleLLM) string {
	if llm.Provider != LLMProviderOpenAI {
		return ""
	}
	if IsChatGPTSignIn(llm.OpenAIAuthMethod) {
		return "; via ChatGPT sign-in — sign-in required after apply"
	}
	return "; via API key"
}

// wantedNames are the names a new install would create, each with the key its
// caller reports it under in Snapshot.TakenNames and the word the blocking message
// uses for it. The order is the order the creates run in.
func wantedNames(in Input, snap Snapshot) []struct {
	key, kind, name string
} {
	slug := in.Slug
	out := make([]struct {
		key, kind, name string
	}, 0, 24)
	add := func(kind, name string) {
		out = append(out, struct {
			key, kind, name string
		}{kind + ":" + name, kind, name})
	}
	for _, r := range planRoles {
		add("login", BotLogin(r, slug))
	}
	for _, r := range planRoles {
		add("team", TeamName(r, slug))
	}
	add("mcp", MCPServerName(slug))
	for _, r := range planRoles {
		add("persona", PersonaName(r, slug))
	}
	for _, r := range planRoles {
		add("image", ImageName(r, slug))
	}
	for _, r := range planRoles {
		add("agent", effectiveAgentName(r, in, snap))
	}
	add("connector", ConnectorName(slug))
	for _, t := range Triggers(wantedInstall(in, snap)) {
		add("trigger", t.Name)
	}
	for _, ct := range CronTriggers(wantedInstall(in, snap)) {
		add("cronTrigger", ct.Name)
	}
	return out
}

// takenKindNames spells the one key that does not read as a noun on its own.
func takenKind(kind string) string {
	if kind == "mcp" {
		return "MCP server"
	}
	return kind
}

// leftBehindHint is the tail appended to a taken-name block when the name
// matches the naming convention an install of this slug uses: without an
// install row the plugin cannot know provenance, so convention-matching is the
// best signal that a retired install left the resource behind (issue #44 — the
// fresh-start cutover can drop rows while their deletes fail, and it can never
// delete Forgejo users, teams or hooks without an admin token). The hint only
// adds context to an existing block; it never changes the verdict, so a
// hand-created name that happens to match is a hedged false positive at worst.
// The delete pointer is kind-aware: hub kinds live in the hub UI, logins and
// teams only a Forgejo administrator can remove. Empty means the name matches
// no shape and the plain message stands. Note that a failed hub-list read in
// buildSnapshot silently drops that list's names (#42's retry lesson) — that
// can lose a collision, never invent one.
func leftBehindHint(kind, name, slug string) string {
	if !matchesInstallNames(name, slug) {
		return ""
	}
	switch kind {
	case "login", "team":
		return fmt.Sprintf(" — the name matches this plugin's convention for slug %q, so a previous install may have left it behind: delete it in Forgejo as an administrator, or choose another slug.", slug)
	default:
		return fmt.Sprintf(" — the name matches this plugin's convention for slug %q, so a previous install may have left it behind: delete it in the hub, or choose another slug.", slug)
	}
}

// matchesInstallNames reports whether name is one of the names an install of
// this slug would create — re-derived from the naming helpers, which mirror the
// naming table in docs/setup.md — or one of the two shapes a leftover from an
// older build can carry: the cutover's forgejo-* + -<slug> (which covers the
// trigger names too, see internal/cutover) or the pre-managed-agents
// <role>-<slug> hub display names.
func matchesInstallNames(name, slug string) bool {
	for _, r := range planRoles {
		switch name {
		case BotLogin(r, slug), TeamName(r, slug), PersonaName(r, slug), ImageName(r, slug), AgentName(r, slug),
			// the legacy hub shape; same spelling as BotLogin, named here so the
			// rule reads against the table it mirrors.
			string(r) + "-" + slug:
			return true
		}
	}
	if name == MCPServerName(slug) || name == ConnectorName(slug) {
		return true
	}
	return strings.HasPrefix(name, "forgejo-") && strings.HasSuffix(name, "-"+slug)
}

// --- naming
//
// One install, one slug, and every name derived from it (R3). The AInsel-side
// names start with "forgejo" so two installs, and the GitHub plugin, cannot clash
// in one namespace; the Forgejo logins stay short.

// MCPServerName names the install's forge MCP server, forgejo-mcp-<slug>.
func MCPServerName(slug string) string { return "forgejo-mcp-" + slug }

// ImageName names a role's image, forgejo-<role>-<slug>.
func ImageName(r Role, slug string) string { return "forgejo-" + string(r) + "-" + slug }

// PersonaName names a role's persona, forgejo-<role>-<slug>.
func PersonaName(r Role, slug string) string { return ImageName(r, slug) }

// AgentName is a role agent's default display name. The admin may change it; it is
// the only derived name that is not fixed.
func AgentName(r Role, slug string) string { return ImageName(r, slug) }

// ConnectorName names the install's connector, forgejo-<slug>. The install creates
// its own connector, so no two installs ever share one.
func ConnectorName(slug string) string { return "forgejo-" + slug }

// BotLogin is the login a role's bot posts as, <role>-<slug>: short, because
// people on Forgejo read it in a mention.
func BotLogin(r Role, slug string) string { return string(r) + "-" + slug }

// TeamName names the role's team in the org, ainsel-<role>-<slug>.
func TeamName(r Role, slug string) string { return "ainsel-" + string(r) + "-" + slug }

// BotFullName is a bot's full name in Forgejo, "AInsel <Role> (<slug>)", so the
// account says whose install it belongs to.
func BotFullName(r Role, slug string) string {
	return "AInsel " + roleTitle(r) + " (" + slug + ")"
}

// roleTitle capitalises a role's name for the full name of its bot.
func roleTitle(r Role) string {
	s := string(r)
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// The two MCP servers and the one skill every role image lists (spec §3.2). The
// plugin's own server is there because the hub maintains its row; its tools are on
// for every role agent and cannot be switched off (V1).
const (
	pluginServerName = "plugin-forgejo"
	skillName        = "forgejo-forgejo"
)

// --- effective values
//
// An empty input field means the shipped default where there is one, and what the
// install already has where there is something to keep.

func (in Input) role(r Role) RoleInput { return in.Roles[r] }

// prior is what the install has for a role today; the zero value on a new install.
func (s Snapshot) prior(r Role) PriorState {
	if s.Existing == nil {
		return PriorState{}
	}
	return s.Prior[r]
}

// llm is what the settings store says a role runs on; the zero value when the
// reading named no entry for it, which the plan blocks on.
func (s Snapshot) llm(r Role) RoleLLM {
	return s.Settings.LLM[r]
}

// effectiveTools is the set a role ends up with: the admin's, or the shipped
// defaults when the input names none. nil when the caller supplied no defaults,
// which means the plan does not know the set — and does not warn about it.
func effectiveTools(r Role, in Input, snap Snapshot) []string {
	if t := in.role(r).Tools; t != nil {
		return t
	}
	if snap.DefaultTools == nil {
		return nil
	}
	return snap.DefaultTools(r)
}

// effectiveRuntime is the role image's container: the input's, or what the install
// has. Empty on a new install, where it blocks.
func effectiveRuntime(r Role, in Input, snap Snapshot) string {
	if v := in.role(r).Runtime; v != "" {
		return v
	}
	return snap.prior(r).Runtime
}

// runtimeHasWhitespace is whether a container ref still holds whitespace after
// Normalize has trimmed its ends — a space, tab or newline anywhere inside it. Such
// a ref is never valid: the hub stores it and the runtime refuses the pod, so the
// plan says so instead of writing an image no task can start.
func runtimeHasWhitespace(ref string) bool {
	return strings.ContainsFunc(ref, unicode.IsSpace)
}

// runtimeHasVersion is whether a container ref names an explicit version: a tag or
// a @digest. The test is on the segment after the last "/", so a registry port is
// not mistaken for a tag — `registry:5000/img` floats, `registry:5000/img:8.0` does
// not. A digest ref passes whatever else it carries (`repo:8.0@sha256:…` included).
// The same five lines exist in the wizard's Roles step; this is the one that counts.
func runtimeHasVersion(ref string) bool {
	if strings.Contains(ref, "@") {
		return true
	}
	last := ref[strings.LastIndex(ref, "/")+1:]
	i := strings.Index(last, ":")
	return i > 0 && i < len(last)-1
}

// effectiveAgentName is the agent's display name: the admin's, or what the install
// has, or the default.
func effectiveAgentName(r Role, in Input, snap Snapshot) string {
	if v := in.role(r).AgentName; v != "" {
		return v
	}
	if v := snap.prior(r).AgentName; v != "" {
		return v
	}
	return AgentName(r, in.Slug)
}

// effectiveTurnTimeoutMs is the turn timeout the role ends up with: the admin's,
// or what the install's row recorded at the last write, or the shipped default.
// The recorded value is empty for an install created before the option existed;
// the shipped default is then the honest display — the image carries it unless an
// admin hand-patched it, and the first wizard-set value re-syncs the row.
func effectiveTurnTimeoutMs(r Role, in Input, snap Snapshot) string {
	if v := in.role(r).TurnTimeoutMs; v != "" {
		return v
	}
	if snap.Existing != nil {
		if v := snap.Existing.Roles[r].TurnTimeoutMs; v != "" {
			return v
		}
	}
	return DefaultTurnTimeoutMs(r)
}

// turnTimeoutSummary renders the effective timeout for the create line: the
// milliseconds the image will carry, spelled with the minutes the admin typed in
// the wizard when they divide evenly.
func turnTimeoutSummary(r Role, in Input, snap Snapshot) string {
	v := effectiveTurnTimeoutMs(r, in, snap)
	if ms, err := parseTurnTimeoutMs(v); err == nil && ms%60000 == 0 {
		return fmt.Sprintf("%sms (%d min)", v, ms/60000)
	}
	return v + "ms"
}

// parseTurnTimeoutMs reads a timeout value as whole milliseconds.
func parseTurnTimeoutMs(v string) (int64, error) {
	return strconv.ParseInt(v, 10, 64)
}

// validTurnTimeoutMs is whether v is a whole number of milliseconds inside the
// bounds — the check the plan blocks on and nothing else needs to repeat.
func validTurnTimeoutMs(v string) bool {
	ms, err := parseTurnTimeoutMs(v)
	return err == nil && ms >= minTurnTimeoutMs && ms <= maxTurnTimeoutMs
}

// excludeOf is the label the refiner keeps off, defaulted — so an install that
// never named one and an install that named the default compare equal.
func excludeOf(label string) string {
	if label == "" {
		return DefaultExcludeLabel
	}
	return label
}

// --- rendering

// unitsSummary renders a role's units_map in one deterministic line: sorted keys,
// so the same permissions always read the same way.
func unitsSummary(r Role) string {
	units := teamUnits[r]
	keys := make([]string, 0, len(units))
	for k := range units {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+units[k])
	}
	return strings.Join(parts, ", ")
}

// filtersSummary renders a trigger's filters the way the preview reads them: the
// field, the op, the value.
func filtersSummary(t Trigger) string {
	parts := make([]string, 0, len(t.Filters))
	for _, f := range t.Filters {
		if len(f.Values) > 0 {
			parts = append(parts, fmt.Sprintf("%s %s %s", f.Field, f.Op, strings.Join(f.Values, "|")))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s %s", f.Field, f.Op, f.Value))
	}
	return strings.Join(parts, "; ")
}

// cronSummary renders a cron trigger the way the preview reads it: the schedule,
// and the prompt reduced to its first sentence — the whole prompt is a paragraph,
// and the preview is one line per op.
func cronSummary(ct CronTrigger) string {
	summary := ct.Prompt
	if i := strings.Index(summary, ". "); i >= 0 {
		summary = summary[:i+1]
	}
	return fmt.Sprintf("schedule %s; %s", ct.Schedule, summary)
}

// diff renders one changed field, the way the preview lists changes field by
// field.
func diff(field, was, now string) string {
	return fmt.Sprintf("%s: %q -> %q", field, was, now)
}

// anyHoursText is a window in the preview's words, with "any hours" for no window at all —
// the name for the state on both sides of a change, so a clear reads as a move to a named
// place rather than to nothing.
func anyHoursText(w *WorkingHours) string {
	if w == nil {
		return "any hours"
	}
	return WorkingHoursText(w)
}

// toolsDiff renders what an edited tool set adds and drops, each sorted so the
// same edit always reads the same. Empty means nothing changed.
func toolsDiff(was, now []string) string {
	if now == nil {
		return "" // the caller does not know the set, so it cannot claim a change
	}
	var adds, drops []string
	for _, t := range now {
		if !slices.Contains(was, t) {
			adds = append(adds, t)
		}
	}
	for _, t := range was {
		if !slices.Contains(now, t) {
			drops = append(drops, t)
		}
	}
	slices.Sort(adds)
	slices.Sort(drops)
	var parts []string
	if len(adds) > 0 {
		parts = append(parts, "+"+strings.Join(adds, ","))
	}
	if len(drops) > 0 {
		parts = append(parts, "-"+strings.Join(drops, ","))
	}
	if len(parts) == 0 {
		return ""
	}
	return "tools: " + strings.Join(parts, " ")
}
