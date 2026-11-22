// Package setup holds the model of one Forgejo install — what the wizard
// collects from the admin — and the trigger set that model implies. Nothing
// here talks to the hub; the setup flow (a later task) reads this model, asks
// AInsel what already exists, and creates what Missing reports.
package setup

import "time"

// Role is one of the three jobs an install fills: the refiner triages and
// prepares incoming issues, the developer works them, the reviewer reviews the
// pulls. Each role has one AInsel agent, one role image, one persona and one
// Forgejo bot — and, on the Forgejo side, one team of its own.
type Role string

const (
	RoleRefiner   Role = "refiner"
	RoleDeveloper Role = "developer"
	RoleReviewer  Role = "reviewer"
)

// DefaultExcludeLabel is the label a human puts on an issue to keep the refiner
// off it — work in progress, or something the agents should not touch yet. An
// install that does not name its own label uses this one.
const DefaultExcludeLabel = "do-not-refine"

// RoleState is what one install created for one role. Every id here names a
// resource the install owns and the plugin created: the re-apply loop sets drift
// back on these ids and nothing else, and the removal flow deletes exactly them.
//
// It holds no secret. The bot token lives in the role image's env on AInsel
// (FORGEJO_TOKEN), which the hub stores; the plugin never keeps a copy (R4).
// The custom provider key is likewise never stored here — only its sha256
// fingerprint, which says "this exact key was written" without being the key.
type RoleState struct {
	// AgentID is the AInsel agent's id (metadata.name, e.g. "a-reviewer"). A
	// trigger's agentRef is this, and it is what says whether a run was
	// refining, developing or reviewing.
	AgentID string `json:"agentId"`

	// ImageID is the role image's id: the per-install image that carries this
	// role's tool set, its forge MCP server and its bot token.
	ImageID string `json:"imageId"`

	// PersonaID is the predefined persona the install created for this role.
	PersonaID string `json:"personaId"`

	// BotLogin is the Forgejo login the role posts as — the name the mention
	// filters look for after the "@" and the author they filter out.
	BotLogin string `json:"botLogin"`

	// TeamID is the Forgejo team the bot is a member of, the source of the
	// role's permissions on the org's repositories.
	TeamID int64 `json:"teamId"`

	// Tools are the Forgejo tool names enabled on the role image: the role's
	// set, within the R7 guard rails. Empty until the image is built.
	Tools []string `json:"tools"`

	// TurnTimeoutMs is the TURN_TIMEOUT_MS the role image was last written with —
	// the wizard's per-role turn-timeout value, or the shipped default recorded at
	// the create (issue #61). The plan diffs the wizard field against it, so a
	// re-apply that did not change it writes nothing and leaves a hand-patched
	// image value alone. Empty on rows written before the option existed, which
	// the next apply records the shipped default into without touching the image.
	TurnTimeoutMs string `json:"turnTimeoutMs,omitempty"`

	// LLMProvider and LLMModel are what the role's agent was set to run on. The row holds
	// them because the re-apply loop is what sets a drifted llm back (§3.6), and a loop can
	// only set back a value it has. Empty on rows written before this was stored, which the
	// loop reads as "nothing to set back", not as "no model". The key is never here: a
	// secret does not go in a row (R4).
	LLMProvider string `json:"llmProvider,omitempty"`
	LLMModel    string `json:"llmModel,omitempty"`

	// LLMCustomKeyHash is the fingerprint (sha256 hex) of the custom provider key
	// the agent was last written with — never the key itself (R4). The hub cannot
	// read a key back, so this is the only way the plan and the reconcile pass can
	// see that a saved key has not reached the agent yet: settings key hashed ≠
	// this, and the pair is written again. Empty means "no key written".
	//
	// The three hashes after it are the same record for the other providers whose
	// key the plugin delivers — the hub's openAI, anthropic and google blocks.
	// One slot per provider, not one for "whatever provider is active": a
	// provider switch then keeps each key's record, so switching back still shows
	// whether a stored key has moved since the agent last saw it.
	LLMCustomKeyHash string `json:"llmCustomKeyHash,omitempty"`
	OpenAIKeyHash    string `json:"llmOpenAIKeyHash,omitempty"`
	AnthropicKeyHash string `json:"llmAnthropicKeyHash,omitempty"`
	GoogleKeyHash    string `json:"llmGoogleKeyHash,omitempty"`

	// WorkingHours is the window the wizard last wrote to this role's agent, and the
	// baseline the plan diffs the form against. It is deliberately not read back from
	// the hub: an admin's console edit to the window is the hub's to keep, and a plan
	// that diffed against the live agent would write the wizard's value back over it on
	// the next unrelated re-apply. Nil is "the wizard never set one" — which is also
	// what a row written before this was stored says, and the two read the same way: an
	// untouched form writes nothing.
	WorkingHours *WorkingHours `json:"workingHours,omitempty"`

	// LLMOpenAIAuthMethod records the auth method the openai agent was last
	// written with — "APIKey" or "ChatGPTSignIn". It is the method half of "what
	// does this role authenticate with", which the key fingerprint cannot say: a
	// sign-in agent has no key and no fingerprint, and the pass has to know that
	// without reading the hub. Empty is the API key, the hub's own default, so a
	// row written before this existed reads as what the agent actually was.
	LLMOpenAIAuthMethod string `json:"llmOpenAIAuthMethod,omitempty"`
}

// LLMKeyHashOf reads the fingerprint the row records as the key a provider was
// last written with, "" when it records none.
func (s RoleState) LLMKeyHashOf(provider string) string {
	switch provider {
	case LLMProviderCustom:
		return s.LLMCustomKeyHash
	case LLMProviderOpenAI:
		return s.OpenAIKeyHash
	case LLMProviderAnthropic:
		return s.AnthropicKeyHash
	case LLMProviderGoogle:
		return s.GoogleKeyHash
	}
	return ""
}

// SetLLMKeyHash records the fingerprint of the key a provider was last written
// with. An unknown provider is ignored: a key the plugin delivers no key for has
// nothing to record.
func (s *RoleState) SetLLMKeyHash(provider, hash string) {
	switch provider {
	case LLMProviderCustom:
		s.LLMCustomKeyHash = hash
	case LLMProviderOpenAI:
		s.OpenAIKeyHash = hash
	case LLMProviderAnthropic:
		s.AnthropicKeyHash = hash
	case LLMProviderGoogle:
		s.GoogleKeyHash = hash
	}
}

// Install is one Forgejo install as the wizard knows it: the org and AInsel
// group its resources belong to, the connector that carries its events, and the
// ids of everything it created. It is the wizard's state, so its fields are what
// the wizard's form and the setup flow both read — and, notably, what it never
// holds is a secret: the Forgejo admin token, the bot tokens and the LLM keys
// are passed through to AInsel or to Forgejo as the wizard creates the
// resources and are not persisted here (R4).
type Install struct {
	// Slug names the install and, through it, every resource the install
	// creates: a trigger for the "apps" install is forgejo-<role>-<what>-apps.
	// Globally unique, so no two installs can name the same resource.
	Slug string `json:"slug"`

	// GroupID is the AInsel group every resource of this install is filed
	// under. AInsel requires a groupId on every create, and the wizard picks it
	// from the caller's groups where they are owner or writer.
	GroupID string `json:"groupId"`

	// BaseURL is the Forgejo address as the admin wrote it, with or without a
	// trailing slash or subpath — the same address every issue, pull and
	// repository link is built from.
	BaseURL string `json:"baseUrl"`

	// Org is the Forgejo organisation the install works in: the one its teams,
	// its org webhook and its repositories live in.
	Org string `json:"org"`

	// ConnectorID is the AInsel connector (a c-… id) whose events this install
	// receives; every generated trigger points at it. The install creates its
	// own connector, so no two installs share one.
	ConnectorID string `json:"connectorId"`

	// MCPServer is the name of the install's forge MCP server,
	// forgejo-mcp-<slug>, registered with tokenFromEnv: FORGEJO_TOKEN.
	MCPServer string `json:"mcpServer"`

	// HookID is the id of the org webhook in Forgejo, so re-apply and removal
	// can patch and delete the hook this install created and no other.
	HookID int64 `json:"hookId"`

	// Roles maps each role to what the install created for it. A role with no
	// entry is a role that has not been created yet — the state half way
	// through the wizard.
	Roles map[Role]RoleState `json:"roles"`

	// ExcludeLabel is the label that keeps the refiner off an issue; empty
	// means DefaultExcludeLabel.
	ExcludeLabel string `json:"excludeLabel"`

	// WebhookVerifiedAt is when a real delivery was last seen on the hook —
	// the proof, not a guess, that Forgejo can reach the connector. Nil until
	// the wizard's last screen has that proof.
	WebhookVerifiedAt *time.Time `json:"webhookVerifiedAt"`

	// Coverage records, per event family, the first delivery this install saw:
	// "issues", "issue_comment" and "pull_request" (reviews count as part of
	// the pull_request family). It is how the wizard says which of the three
	// the org actually delivers, rather than which the hook is configured for.
	Coverage map[string]time.Time `json:"coverage"`

	// EventsReadUntil is the timestamp of the newest event the re-apply loop has folded
	// into coverage: the next pass reads from here, so a pass reads only what it has not
	// read before. Nil on rows written before the loop kept a cursor.
	EventsReadUntil *time.Time `json:"eventsReadUntil,omitempty"`

	// LayoutVersion names the resource layout this row was created under: 0 is the
	// legacy layout, where the wizard could adopt resources it did not create;
	// LayoutManagedAgents is the created-only layout, where every id on the row
	// names something the plugin made. The fresh-start cutover reads it: a row
	// without the marker is a legacy install, and the first start under the new
	// layout deletes what those rows track and drops them.
	LayoutVersion int `json:"layoutVersion,omitempty"`

	// Retiring marks a legacy row the fresh-start cutover could not finish: some
	// resource it tracks by id is still standing because deleting it failed, so the
	// row — the only pointer the plugin has to those resources — is kept and the next
	// start re-runs the same pass. The cutover is the only writer: it stamps the row
	// when a deletion is pending and drops it once every tracked deletion succeeded or
	// was deliberately skipped. A retiring row is not an install the wizard offers —
	// GET /api/setup/installs and the work view hide it — but its slug stays reserved
	// and it stays removable by slug, which is how an admin gives up on a resource the
	// hub will not delete.
	Retiring bool `json:"retiring,omitempty"`
}

// LayoutManagedAgents is the layout version stamped on every row the created-only
// wizard writes. Rows without it predate the layout; nothing else assigns it.
const LayoutManagedAgents = 1
