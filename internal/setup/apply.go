package setup

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

// HubOps is the minimal set of hub operations Apply needs to create an install.
// Task 7 will adapt the real hub.Client to this interface.
type HubOps interface {
	CreateAgentImage(ctx context.Context, groupID, displayName, imageURL string, mcpServers []string, env []EnvVar) (imageID string, err error)
	RefreshImageTools(ctx context.Context, imageID string) ([]ImageTool, map[string]string, error)
	SetImageTools(ctx context.Context, imageID string, tools []ImageTool) error
	CreatePersona(ctx context.Context, groupID, name, description, text string) (personaID string, err error)
	// CreateAgent files the role agent. enabledTools is its tool grant, in the image
	// catalog's own spellings — what setRoleTools resolved the role's set to. It is
	// never empty: an agent created without a grant runs with every tool the image
	// offers, which is the opposite of what a role's tool set asks for.
	CreateAgent(ctx context.Context, groupID, agentName, imageID, personaID string, mcpServers, skills []string, env []EnvVar, llm LLMWrite, enabledTools []string, hours *WorkingHours) (agentID string, err error)
	CreateConnector(ctx context.Context, groupID, connectorName string) (connectorID, webhookEndpoint, webhookSecret string, err error)
	CreateTrigger(ctx context.Context, groupID string, trigger Trigger) (triggerID string, err error)
	CreateCronTrigger(ctx context.Context, groupID string, trigger CronTrigger) (cronTriggerID string, err error)
	RegisterMCPServer(ctx context.Context, groupID, name, url, tokenFromEnv string) (serverID string, err error)

	UpdateAgentImage(ctx context.Context, imageID, imageURL string) error
	// UpdateAgentImageEnv restates an image's whole environment list: the hub's
	// image update replaces the env it holds with exactly what arrives, so the
	// caller sends every entry the image is to keep. An entry whose value is
	// empty and whose name the hub stores as a secret keeps the stored value —
	// which is how a write that only means to move TURN_TIMEOUT_MS resends
	// FORGEJO_TOKEN without knowing it (issue #61).
	UpdateAgentImageEnv(ctx context.Context, imageID string, env []EnvVar) error
	// UpdateAgent writes whatever the edit changed. The llm it writes is the
	// settings' value, not the admin's: the wizard asks for no model, and the
	// only keys and URLs that ever reach a hub write are the ones the settings
	// store holds for the provider the role runs on — an empty one leaves
	// whatever the hub has alone.
	//
	// enabledTools is the role's tool grant, in the image catalog's spellings, and it
	// follows the hub's keep-versus-clear rule from the plugin's side: an empty slice
	// writes nothing, because a grant left off the request is the hub keeping what it
	// holds and a grant sent empty is the runtime granting everything.
	UpdateAgent(ctx context.Context, agentID, displayName string, llm LLMWrite, enabledTools []string, hours WorkingHoursWrite) error
	UpdatePersona(ctx context.Context, personaID, text string) error
	ListTriggers(ctx context.Context, connectorID string) ([]TriggerEntry, error)

	DeleteAgent(ctx context.Context, agentID string) error
	DeleteAgentImage(ctx context.Context, imageID string) error
	DeletePersona(ctx context.Context, personaID string) error
	DeleteConnector(ctx context.Context, connectorID string) error
	DeleteMCPServer(ctx context.Context, serverID string) error
	DeleteTrigger(ctx context.Context, triggerID string) error
	DeleteCronTrigger(ctx context.Context, cronTriggerID string) error
}

// LLMWrite is what one agent write carries for the model a role runs on: the
// llm itself plus the credential of the provider it names. It replaces a tail
// of positional strings on CreateAgent and UpdateAgent — seven of them was a
// misuse magnet.
//
// Only the pair or key of the provider the role actually runs on is filled (see
// llmWriteFor); away from a provider its block stays empty, so the hub keeps
// whatever it holds for the providers the role is not using — the same
// keep-on-omit semantics the hub defines for its own console.
type LLMWrite struct {
	Provider  string
	Model     string
	CustomURL string
	CustomKey string
	OpenAIKey string
	// AnthropicKey and GoogleKey carry the hub's anthropic and google blocks.
	AnthropicKey string
	GoogleKey    string

	// OpenAIAuthMethod carries the hub's openAI.authMethod for a role that runs
	// on openai: LLMAuthMethodChatGPTSignIn, or LLMAuthMethodAPIKey when the
	// write has to move an agent back from a sign-in to a key. Empty means "say
	// nothing": the hub keeps the method the agent holds, which is the same
	// keep-on-omit rule every other field of this struct follows.
	//
	// A write that names the sign-in method never names a key — the hub refuses
	// the pair (see llmWriteFor).
	OpenAIAuthMethod string
}

// LLMKeyOf is the key this write carries for one provider, "" when it carries
// none for it — the same reading RoleLLM.LLMKeyOf gives, from the write's side.
func (w LLMWrite) LLMKeyOf(provider string) string {
	switch provider {
	case LLMProviderCustom:
		return w.CustomKey
	case LLMProviderOpenAI:
		return w.OpenAIKey
	case LLMProviderAnthropic:
		return w.AnthropicKey
	case LLMProviderGoogle:
		return w.GoogleKey
	}
	return ""
}

// llmWriteFor is the write a role's settings call for: the llm, and the custom
// pair or the vendor key of the provider the role runs on. Every other key is
// left empty — sending a stale URL would point a listed provider's agent at an
// endpoint nobody meant, and sending a key for a provider the agent does not use
// writes a secret the admin never chose to store on that agent.
//
// An openai role that signs in with ChatGPT is the one case where the settings
// hold a key and the write must not carry it: the hub refuses an openAI block
// that names both, so the method is sent and the key is left alone however the
// key row reads. Clearing the stored key is what a Reset does (docs/setup.md).
func llmWriteFor(s Settings, r Role) LLMWrite {
	llm := s.llm(r)
	w := LLMWrite{Provider: llm.Provider, Model: llm.Model}
	if llm.Provider == LLMProviderCustom {
		w.CustomURL = llm.CustomURL
	}
	switch llm.Provider {
	case LLMProviderCustom:
		w.CustomKey = llm.CustomKey
	case LLMProviderOpenAI:
		if IsChatGPTSignIn(llm.OpenAIAuthMethod) {
			w.OpenAIAuthMethod = LLMAuthMethodChatGPTSignIn
		} else {
			w.OpenAIKey = llm.OpenAIKey
		}
	case LLMProviderAnthropic:
		w.AnthropicKey = llm.AnthropicKey
	case LLMProviderGoogle:
		w.GoogleKey = llm.GoogleKey
	}
	return w
}

// WorkingHoursWrite is what one agent write carries for the window the agent holds queued
// work to. The three states are the hub's, and they are named apart so a clear cannot be
// reached by accident — an omitted field is "say nothing", never "clear":
//
//   - the zero write leaves the key out, and the hub keeps the window it holds;
//   - Set writes that window;
//   - Clear sends the hub's empty object, which removes it.
//
// Apply answers the plan's op for the field with one of the two, and a plan with no op for
// it answers with the zero write — which is what keeps a window an admin set in the console
// alive through a re-apply that changed something else.
type WorkingHoursWrite struct {
	Set   *WorkingHours
	Clear bool
}

// workingHoursWriteFor is the write the plan's verdict on one role's window calls for: the
// form's window when the plan says the field moved, the hub's clear when the plan says it
// moved to nothing, and nothing at all when the plan says it did not move.
func workingHoursWriteFor(in Input, r Role, moved bool) WorkingHoursWrite {
	if !moved {
		return WorkingHoursWrite{}
	}
	if w := in.role(r).WorkingHours; w != nil {
		return WorkingHoursWrite{Set: CloneWorkingHours(w)}
	}
	return WorkingHoursWrite{Clear: true}
}

// EnvVar is one environment entry on an image or agent.
type EnvVar struct {
	Name   string
	Value  string
	Secret bool
}

// DefaultTurnTimeoutMs is the per-turn wall-clock cap, in milliseconds, the wizard
// ships on the role image's environment when the admin sets none; "" would mean
// leave it unset and accept the runner's 600 s default. The wizard's per-role
// turn-timeout field (RoleInput.TurnTimeoutMs) overrides it on create and on every
// later change the admin makes in the wizard (issue #61); this is only the
// fallback.
//
// The values are the ones the admin hand-applied on the `apps` install on
// 2026-11-15, after the shipped 30/15-minute caps failed in production: the
// refiner died at 15 min four times in a row on a large refinement, and the
// developer hit 30 min twice while still progressing (issue #61; the developer
// matches agent-developer-gh's 7200000 from issue #20). They are ceilings, so the
// only cost of the headroom is later detection of a hung turn.
//
// It goes on the image, not the agent, so that env-merge precedence keeps an
// admin's hand-patched agent-level value winning over this shipped default.
func DefaultTurnTimeoutMs(role Role) string {
	if role == RoleDeveloper {
		return "7200000" // 2 h — the developer's real work runs long (issue #61)
	}
	return "3600000" // 1 h — a refiner on a large issue outgrows 15 min (issue #61)
}

// ImageTool is one tool in an agent image's catalog.
type ImageTool struct {
	Name    string
	Kind    string
	Enabled bool
}

// TriggerEntry is one trigger returned by ListTriggers.
type TriggerEntry struct {
	ID   string
	Name string
}

// ForgejoOps is the minimal set of Forgejo operations Apply needs.
// Task 7 will adapt the real forgejo.Admin to this interface.
type ForgejoOps interface {
	CreateUser(ctx context.Context, login, fullName, password string) error
	MintToken(ctx context.Context, login, password string, scopes []string) (token string, err error)
	CreateTeam(ctx context.Context, teamName string, units map[string]string) (teamID int64, err error)
	AddTeamMember(ctx context.Context, teamID int64, login string) error
	CreateOrgHook(ctx context.Context, url, secret string, events []string) (hookID int64, err error)

	DeleteUser(ctx context.Context, login string) error
	DeleteTeam(ctx context.Context, teamID int64) error
	DeleteOrgHook(ctx context.Context, hookID int64) error
}

// Apply executes a plan to create an install, with rollback on failure.
type Apply struct {
	Hub          HubOps
	Forgejo      ForgejoOps
	Now          func() time.Time
	Rand         io.Reader
	PersonaFor   func(role Role, bots map[Role]string, baseURL string) string
	DefaultTools func(role Role) []string

	// Settings are the effective role settings the plan was drawn against: the
	// model each role's agent is created on and the forge MCP server's wiring.
	// The wizard no longer collects them — the settings store owns them — so the
	// caller hands the same reading it built the snapshot with in here, and the
	// creates cannot disagree with the preview.
	Settings Settings
}

// ApplyReport tracks what was created and rolled back during an apply.
type ApplyReport struct {
	Created    []Op `json:"created"`
	RolledBack []Op `json:"rolledBack"`
	Leftovers  []Op `json:"leftovers"`

	// Notes are the steps an apply leaves for the admin to finish by hand — today
	// only the ChatGPT sign-in an openai role on that method still needs, per
	// agent, in the AInsel console. Nothing here is a resource: an apply that
	// created no sign-in agent says nothing.
	Notes []string `json:"notes"`
}

// signInNote is the line an apply leaves for one role whose agent authenticates
// with a ChatGPT sign-in. The credential is not the plugin's to create — the
// hub runs the sign-in interactively, per agent, from the console's agent
// workspace page — so the apply can only say where to go and what is waiting.
// The path named is that page's route in the hub console (`/agents/<agentID>`).
func signInNote(role Role, agentName, agentID string) string {
	return fmt.Sprintf("role %q: agent %q signs in with ChatGPT — finish the sign-in in the AInsel console (Agents → %s, at /agents/%s); the agent runs nothing until it is done", role, agentName, agentID, agentID)
}

// Run executes the plan to create or update an install. On any error, everything
// created in this run is deleted in reverse order, and the original error is returned
// wrapped with the step name.
//
// The ctx it is handed should be the plugin's own (hub.WithBackground over
// hub.WithoutCredential): the hub stamps a create with the caller's manager, and only
// the named manager may write what was stamped — so creates, edits, deletes and the
// rollback all have to arrive as the plugin for the install to stay wholly the
// wizard's. The stamp itself is the hub client's (managerStamp in internal/hub), not
// a choice made here.
//
// A new install has prev == nil; an edit has prev as what was already created.
//
// The results are named because the rollback runs in a deferred function: it adds
// to report, and an unnamed result would be a local copy the caller never sees —
// the API's error answer would then claim nothing was rolled back (spec §5[8]).
func (a Apply) Run(ctx context.Context, in Input, plan Result, prev *Install) (install Install, report ApplyReport, err error) {
	// The same cleaning Plan draws on, so what the apply writes is what the plan
	// previewed even when a caller hands Run an input it never planned — and so the
	// row it stores holds a trimmed slug, address and runtime. Normalize is
	// idempotent, so Plan having run first changes nothing here.
	in = Normalize(in)

	if prev == nil {
		return a.createInstall(ctx, in, plan)
	}
	return a.editInstall(ctx, in, plan, prev)
}

func (a Apply) createInstall(ctx context.Context, in Input, plan Result) (install Install, report ApplyReport, err error) {
	report = ApplyReport{
		Created:    []Op{},
		RolledBack: []Op{},
		Leftovers:  []Op{},
		Notes:      []string{},
	}

	install = Install{
		Slug:          in.Slug,
		GroupID:       in.GroupID,
		BaseURL:       in.BaseURL,
		Org:           in.Org,
		MCPServer:     MCPServerName(in.Slug),
		ExcludeLabel:  in.ExcludeLabel,
		LayoutVersion: LayoutManagedAgents,
		Roles:         make(map[Role]RoleState),
	}

	// Track created resources for rollback (in reverse order).
	var rollbackOps []struct {
		kind   string
		op     Op
		action func(ctx context.Context) error
	}

	// Helper to delete on error
	defer func() {
		if len(rollbackOps) > 0 {
			// Reverse order
			for i := len(rollbackOps) - 1; i >= 0; i-- {
				if err := rollbackOps[i].action(ctx); err != nil {
					report.Leftovers = append(report.Leftovers, rollbackOps[i].op)
				} else {
					report.RolledBack = append(report.RolledBack, rollbackOps[i].op)
				}
			}
		}
	}()

	// Step 1: Create users, teams, mint tokens
	botTokens := make(map[Role]string)
	for _, role := range []Role{RoleRefiner, RoleDeveloper, RoleReviewer} {
		login := BotLogin(role, in.Slug)
		fullName := BotFullName(role, in.Slug)

		// Generate a fresh 24-byte password for this bot
		passwordBuf := make([]byte, 24)
		if _, err := io.ReadFull(a.Rand, passwordBuf); err != nil {
			return Install{}, report, fmt.Errorf("generating password: %w", err)
		}
		password := hex.EncodeToString(passwordBuf)

		// CreateUser
		if err := a.Forgejo.CreateUser(ctx, login, fullName, password); err != nil {
			return Install{}, report, fmt.Errorf("CreateUser: %w", err)
		}
		op := Op{Kind: KindUser, Name: login, Detail: fullName}
		report.Created = append(report.Created, op)
		rollbackOps = append(rollbackOps, struct {
			kind   string
			op     Op
			action func(ctx context.Context) error
		}{
			kind: KindUser,
			op:   op,
			action: func(ctx context.Context) error {
				return a.Forgejo.DeleteUser(ctx, login)
			},
		})

		// MintToken (password is discarded after this)
		botToken, err := a.Forgejo.MintToken(ctx, login, password, []string{"write:issue", "write:repository", "read:user", "read:organization"})
		if err != nil {
			return Install{}, report, fmt.Errorf("MintToken: %w", err)
		}
		botTokens[role] = botToken

		// CreateTeam
		teamName := TeamName(role, in.Slug)
		teamID, err := a.Forgejo.CreateTeam(ctx, teamName, TeamUnits(role))
		if err != nil {
			return Install{}, report, fmt.Errorf("CreateTeam: %w", err)
		}
		op = Op{Kind: KindTeam, Name: teamName, Detail: fmt.Sprintf("role=%s, id=%d", role, teamID)}
		report.Created = append(report.Created, op)
		rollbackOps = append(rollbackOps, struct {
			kind   string
			op     Op
			action func(ctx context.Context) error
		}{
			kind: KindTeam,
			op:   op,
			action: func(ctx context.Context) error {
				return a.Forgejo.DeleteTeam(ctx, teamID)
			},
		})

		// AddTeamMember
		if err := a.Forgejo.AddTeamMember(ctx, teamID, login); err != nil {
			return Install{}, report, fmt.Errorf("AddTeamMember: %w", err)
		}

		install.Roles[role] = RoleState{
			BotLogin: login,
			TeamID:   teamID,
		}
	}

	// Step 2: RegisterMCPServer
	mcpServerID, err := a.Hub.RegisterMCPServer(ctx, in.GroupID, MCPServerName(in.Slug), a.Settings.MCPURL, a.Settings.MCPTokenFromEnv)
	if err != nil {
		return Install{}, report, fmt.Errorf("RegisterMCPServer: %w", err)
	}
	op := Op{Kind: KindMCPServer, Name: MCPServerName(in.Slug), Detail: a.Settings.MCPURL}
	report.Created = append(report.Created, op)
	rollbackOps = append(rollbackOps, struct {
		kind   string
		op     Op
		action func(ctx context.Context) error
	}{
		kind: KindMCPServer,
		op:   op,
		action: func(ctx context.Context) error {
			return a.Hub.DeleteMCPServer(ctx, mcpServerID)
		},
	})
	install.MCPServer = MCPServerName(in.Slug)

	// Step 3: CreatePersona (for each role)
	personaIDs := make(map[Role]string)
	bots := make(map[Role]string)
	for role := range botTokens {
		bots[role] = BotLogin(role, in.Slug)
	}

	for _, role := range []Role{RoleRefiner, RoleDeveloper, RoleReviewer} {
		personaName := PersonaName(role, in.Slug)
		var personaText string
		if a.PersonaFor != nil {
			personaText = a.PersonaFor(role, bots, in.BaseURL)
		}

		personaID, err := a.Hub.CreatePersona(ctx, in.GroupID, personaName, fmt.Sprintf("Persona for %s", role), personaText)
		if err != nil {
			return Install{}, report, fmt.Errorf("CreatePersona: %w", err)
		}
		personaIDs[role] = personaID

		// Store PersonaID in the RoleState
		roleState := install.Roles[role]
		roleState.PersonaID = personaID
		install.Roles[role] = roleState

		op := Op{Kind: KindPersona, Name: personaName, Detail: fmt.Sprintf("role=%s", role)}
		report.Created = append(report.Created, op)
		rollbackOps = append(rollbackOps, struct {
			kind   string
			op     Op
			action func(ctx context.Context) error
		}{
			kind: KindPersona,
			op:   op,
			action: func(ctx context.Context) error {
				return a.Hub.DeletePersona(ctx, personaID)
			},
		})
	}

	// Step 4: CreateAgentImage, RefreshImageTools, SetImageTools (for each role)
	imageIDs := make(map[Role]string)
	// The names each role's set resolves to in its image's catalog, which step 5 hands
	// the agent as its grant. The catalog is what the hub validates a grant against, so
	// the resolution happens once, here, and is not redone at the agent.
	grants := make(map[Role][]string)
	for _, role := range []Role{RoleRefiner, RoleDeveloper, RoleReviewer} {
		imageName := ImageName(role, in.Slug)
		runtime := in.role(role).Runtime
		if runtime == "" {
			runtime = "default"
		}

		// CreateAgentImage
		env := []EnvVar{
			{Name: TokenEnvName, Value: botTokens[role], Secret: true},
		}
		// The wizard's value when the admin set one, the shipped default when not
		// (issue #61). Recorded on the row right away, so the next plan diffs
		// against what the image actually carries.
		timeout := in.role(role).TurnTimeoutMs
		if timeout == "" {
			timeout = DefaultTurnTimeoutMs(role)
		}
		if timeout != "" {
			// Not a credential: a plain (non-secret) entry, so the value stays
			// readable on the image and an agent-level override still wins.
			env = append(env, EnvVar{Name: TurnTimeoutEnvName, Value: timeout})
		}
		imageID, err := a.Hub.CreateAgentImage(ctx, in.GroupID, imageName, runtime,
			[]string{MCPServerName(in.Slug), "plugin-forgejo"},
			env)
		if err != nil {
			return Install{}, report, fmt.Errorf("CreateAgentImage: %w", err)
		}
		imageIDs[role] = imageID
		op := Op{Kind: KindImage, Name: imageName, Detail: fmt.Sprintf("role=%s, id=%s", role, imageID)}
		report.Created = append(report.Created, op)
		rollbackOps = append(rollbackOps, struct {
			kind   string
			op     Op
			action func(ctx context.Context) error
		}{
			kind: KindImage,
			op:   op,
			action: func(ctx context.Context) error {
				return a.Hub.DeleteAgentImage(ctx, imageID)
			},
		})

		// RefreshImageTools, then SetImageTools: what the role can do is read back from
		// the hub before it is written, so a tool the MCP server does not actually
		// offer is never sent.
		//
		// An input naming no tools is the plan's "use the role's defaults", not "no
		// tools": an agent with nothing to do is never what an admin meant (spec §3.2).
		tools := in.role(role).Tools
		if len(tools) == 0 && a.DefaultTools != nil {
			tools = a.DefaultTools(role)
		}
		granted, _, err := a.setRoleTools(ctx, imageID, in.Slug, tools, true)
		if err != nil {
			return Install{}, report, fmt.Errorf("SetImageTools for %s: %w", role, err)
		}
		grants[role] = granted

		// Update RoleState with image information, preserving fields from earlier steps
		roleState := install.Roles[role]
		roleState.ImageID = imageID
		roleState.Tools = tools
		roleState.TurnTimeoutMs = timeout
		install.Roles[role] = roleState
	}

	// Step 5: CreateAgent (for each role)
	agentIDs := make(map[Role]string)
	for _, role := range []Role{RoleRefiner, RoleDeveloper, RoleReviewer} {
		agentName := in.role(role).AgentName
		if agentName == "" {
			agentName = AgentName(role, in.Slug)
		}

		llmWrite := llmWriteFor(a.Settings, role)

		agentEnv := []EnvVar{
			{Name: TokenEnvName, Value: botTokens[role], Secret: true},
		}

		// The grant the image catalog was just refreshed to carry. An agent created
		// without one runs with every tool the image offers, so a role whose set the
		// catalog resolved to nothing is a failed apply rather than a wide-open agent.
		grant := grants[role]
		if len(grant) == 0 {
			return Install{}, report, fmt.Errorf("CreateAgent for %s: no tool grant was resolved", role)
		}

		agentID, err := a.Hub.CreateAgent(ctx, in.GroupID, agentName, imageIDs[role], personaIDs[role],
			[]string{MCPServerName(in.Slug), "plugin-forgejo"},
			[]string{"forgejo-forgejo"},
			agentEnv,
			llmWrite,
			grant,
			in.role(role).WorkingHours)
		if err != nil {
			return Install{}, report, fmt.Errorf("CreateAgent: %w", err)
		}
		agentIDs[role] = agentID
		op := Op{Kind: KindAgent, Name: agentName, Detail: fmt.Sprintf("role=%s, id=%s", role, agentID)}
		report.Created = append(report.Created, op)
		rollbackOps = append(rollbackOps, struct {
			kind   string
			op     Op
			action func(ctx context.Context) error
		}{
			kind: KindAgent,
			op:   op,
			action: func(ctx context.Context) error {
				return a.Hub.DeleteAgent(ctx, agentID)
			},
		})

		roleState := install.Roles[role]
		roleState.AgentID = agentID
		// What the agent runs on goes in the row for the re-apply loop to hold (§3.6).
		// It is the settings' value the create was made with; the key itself never
		// goes in the row (R4) — only its fingerprint, so a later rotation is
		// visible as drift. The fingerprint is per provider, so a key stored for a
		// provider the role is not on keeps its own record across a switch.
		roleState.LLMProvider = llmWrite.Provider
		roleState.LLMModel = llmWrite.Model
		roleState.SetLLMKeyHash(llmWrite.Provider, CustomKeyFingerprint(llmWrite.LLMKeyOf(llmWrite.Provider)))
		// The window the agent was created with goes in the row too, as the baseline the
		// next edit diffs against — not as a copy of what the hub holds, which an admin may
		// change in the console.
		roleState.WorkingHours = CloneWorkingHours(in.role(role).WorkingHours)
		// The auth method the agent was created with goes in the row beside the
		// provider: it is the other half of "what does this agent authenticate
		// with", and the re-apply loop can only hold what a row records. A role on
		// another provider records nothing, so the row keeps saying "the default".
		roleState.LLMOpenAIAuthMethod = openAIAuthMethodForRole(a.Settings.llm(role))
		install.Roles[role] = roleState
		if llmWrite.OpenAIAuthMethod == LLMAuthMethodChatGPTSignIn {
			report.Notes = append(report.Notes, signInNote(role, agentName, agentID))
		}
	}

	// Step 6: CreateConnector
	connectorName := ConnectorName(in.Slug)
	connectorID, webhookEndpoint, webhookSecret, err := a.Hub.CreateConnector(ctx, in.GroupID, connectorName)
	if err != nil {
		return Install{}, report, fmt.Errorf("CreateConnector: %w", err)
	}
	op = Op{Kind: KindConnector, Name: connectorName, Detail: webhookEndpoint}
	report.Created = append(report.Created, op)
	rollbackOps = append(rollbackOps, struct {
		kind   string
		op     Op
		action func(ctx context.Context) error
	}{
		kind: KindConnector,
		op:   op,
		action: func(ctx context.Context) error {
			return a.Hub.DeleteConnector(ctx, connectorID)
		},
	})
	install.ConnectorID = connectorID

	// Step 7: CreateTrigger (six triggers)
	triggers := Triggers(install)
	for _, trigger := range triggers {
		triggerID, err := a.Hub.CreateTrigger(ctx, in.GroupID, trigger)
		if err != nil {
			return Install{}, report, fmt.Errorf("CreateTrigger: %w", err)
		}
		op := Op{Kind: KindTrigger, Name: trigger.Name, Detail: "trigger"}
		report.Created = append(report.Created, op)

		// Register for rollback
		rollbackOps = append(rollbackOps, struct {
			kind   string
			op     Op
			action func(ctx context.Context) error
		}{
			kind: KindTrigger,
			op:   op,
			action: func(ctx context.Context) error {
				return a.Hub.DeleteTrigger(ctx, triggerID)
			},
		})
	}

	// Step 7b: CreateCronTrigger (the developer's scheduled PR patrol). Created
	// with the event triggers, in the same phase: an install is not complete until
	// the developer is woken for the pulls nobody mentions it about.
	for _, cronTrigger := range CronTriggers(install) {
		cronTriggerID, err := a.Hub.CreateCronTrigger(ctx, in.GroupID, cronTrigger)
		if err != nil {
			return Install{}, report, fmt.Errorf("CreateCronTrigger: %w", err)
		}
		op := Op{Kind: KindCronTrigger, Name: cronTrigger.Name, Detail: "cron trigger"}
		report.Created = append(report.Created, op)

		// Register for rollback
		rollbackOps = append(rollbackOps, struct {
			kind   string
			op     Op
			action func(ctx context.Context) error
		}{
			kind: KindCronTrigger,
			op:   op,
			action: func(ctx context.Context) error {
				return a.Hub.DeleteCronTrigger(ctx, cronTriggerID)
			},
		})
	}

	// Step 8: CreateOrgHook
	hookEvents := HookEvents()
	hookID, err := a.Forgejo.CreateOrgHook(ctx, webhookEndpoint, webhookSecret, hookEvents)
	if err != nil {
		return Install{}, report, fmt.Errorf("CreateOrgHook: %w", err)
	}
	op = Op{Kind: KindHook, Name: in.Org, Detail: strings.Join(hookEvents, ", ")}
	report.Created = append(report.Created, op)
	rollbackOps = append(rollbackOps, struct {
		kind   string
		op     Op
		action func(ctx context.Context) error
	}{
		kind: KindHook,
		op:   op,
		action: func(ctx context.Context) error {
			return a.Forgejo.DeleteOrgHook(ctx, hookID)
		},
	})
	install.HookID = hookID

	// Step 9: Return the install
	// Clear the rollback queue since everything succeeded
	rollbackOps = nil

	return install, report, nil
}

func (a Apply) editInstall(ctx context.Context, in Input, plan Result, prev *Install) (install Install, report ApplyReport, err error) {
	report = ApplyReport{
		Created:    []Op{},
		RolledBack: []Op{},
		Leftovers:  []Op{},
		Notes:      []string{},
	}

	// Start with prev and update only the fields the admin changed: an empty field
	// on an edit means "keep", the same rule the plan reads.
	//
	// The maps are copied, not shared. An edit writes role by role, so an edit that
	// fails halfway through would otherwise leave the row the caller handed in
	// carrying some of the changes it was told to reject — and the caller is the same
	// process that has to report the failure.
	install = *prev
	install.Roles = make(map[Role]RoleState, len(prev.Roles))
	for r, state := range prev.Roles {
		install.Roles[r] = state
	}
	if prev.Coverage != nil {
		install.Coverage = make(map[string]time.Time, len(prev.Coverage))
		for event, seen := range prev.Coverage {
			install.Coverage[event] = seen
		}
	}
	if in.BaseURL != "" {
		install.BaseURL = in.BaseURL
	}
	if in.ExcludeLabel != "" {
		install.ExcludeLabel = in.ExcludeLabel
	}

	planRoles := []Role{RoleRefiner, RoleDeveloper, RoleReviewer}

	// changed reports whether the plan says one field of one role changed. It reads
	// the op the plan decided on rather than the text that op renders for the
	// admin, which is the difference between matching a role and guessing at a name.
	changed := func(kind string, role Role, field string) bool {
		for _, op := range plan.Change {
			if op.Kind == kind && op.Role == role && op.Field == field {
				return true
			}
		}
		return false
	}

	// The persona text is written for one forge: it names the address the bots post to.
	// An install pointed at a different address has to tell its agents, the same as on a
	// create (spec §3.5), and the logins it names are the ones the install already has.
	bots := make(map[Role]string, len(prev.Roles))
	for r, state := range prev.Roles {
		bots[r] = state.BotLogin
	}

	// Update each role the plan says changed
	for _, role := range planRoles {
		roleState := install.Roles[role]

		// What the role is told about the forge it works in
		if changed(KindPersona, role, FieldBaseURL) && roleState.PersonaID != "" && a.PersonaFor != nil {
			text := a.PersonaFor(role, bots, install.BaseURL)
			if text == "" {
				return Install{}, report, fmt.Errorf("UpdatePersona for %s: no persona text was offered", role)
			}
			if err := a.Hub.UpdatePersona(ctx, roleState.PersonaID, text); err != nil {
				return Install{}, report, fmt.Errorf("UpdatePersona for %s: %w", role, err)
			}
			report.Created = append(report.Created, Op{Kind: KindPersona, Name: PersonaName(role, prev.Slug), Detail: "persona rewritten", Role: role, Field: FieldBaseURL})
		}

		// The container the role's image runs
		if changed(KindImage, role, FieldRuntime) {
			newRuntime := in.role(role).Runtime
			if newRuntime != "" {
				if err := a.Hub.UpdateAgentImage(ctx, roleState.ImageID, newRuntime); err != nil {
					return Install{}, report, fmt.Errorf("UpdateAgentImage for %s: %w", role, err)
				}
				report.Created = append(report.Created, Op{Kind: KindImage, Name: ImageName(role, prev.Slug), Detail: "runtime updated", Role: role, Field: FieldRuntime})
			}
		}

		// The role's tool set: the image's catalog first, then the agent's grant.
		//
		// The grant is what restricts an agent now — the image's catalog only lists what
		// the runtime offers — so it is written on every apply, not only when the plan
		// reports a FieldTools change. A row written before the hub moved selection onto
		// the agent, or one the install adopted, holds a tool set that has not changed
		// while its agent carries no grant at all: a pass gated on the plan would leave
		// exactly the rows that need the write alone. The write is idempotent, so running
		// it again costs nothing and repairs a grant an admin edited by hand.
		//
		// The order is load-bearing: the hub validates a grant against the catalog, so
		// the catalog write has to land first.
		//
		// A set left empty on an edit is the set the role already has — the defaults are
		// a create-time notion, not an edit-time one. Nothing to write is skipped rather
		// than refused, except where the plan asked for the change: an apply the admin
		// asked for that names no tools is an error, the same as it always was.
		toolsChanged := changed(KindImage, role, FieldTools)
		newTools := in.role(role).Tools
		if len(newTools) == 0 {
			newTools = roleState.Tools
		}
		var grant []string
		if roleState.ImageID != "" && (toolsChanged || len(newTools) > 0) {
			// Strict only when the admin asked for this set: a name the forge server stopped
			// reporting is then a refusal the preview should have caught. On a re-apply that
			// changed nothing it is drift to degrade, the way the drift pass already does it —
			// one renamed tool upstream must not make the whole install unsavable.
			granted, dropped, err := a.setRoleTools(ctx, roleState.ImageID, in.Slug, newTools, toolsChanged)
			if err != nil {
				return Install{}, report, fmt.Errorf("SetImageTools for %s: %w", role, err)
			}
			for _, name := range dropped {
				report.Notes = append(report.Notes, fmt.Sprintf(
					"role %q: the image's catalog no longer lists %q, so it is left out of the grant and the rest of the set was written; check the forge MCP server's tool list", role, name))
			}
			grant = granted
			if !sameToolSet(grant, roleState.Tools, in.Slug) {
				roleState.Tools = newTools
				report.Created = append(report.Created, Op{Kind: KindImage, Name: ImageName(role, prev.Slug), Detail: "tools updated", Role: role, Field: FieldTools})
			}
		}
		if roleState.AgentID != "" && len(grant) > 0 {
			if err := a.Hub.UpdateAgent(ctx, roleState.AgentID, "", LLMWrite{}, grant, WorkingHoursWrite{}); err != nil {
				return Install{}, report, fmt.Errorf("UpdateAgent tools for %s: %w", role, err)
			}
		}

		// The role's turn timeout on that image. The plan only sends this op when
		// the wizard value differs from the recorded one, and an empty wizard value
		// is never a change — which is what keeps a hand-patched image value alive
		// through a re-apply that did not touch the field (issue #61).
		if changed(KindImage, role, FieldTurnTimeout) {
			newTimeout := in.role(role).TurnTimeoutMs
			if newTimeout != "" && roleState.ImageID != "" {
				// The hub replaces the whole env list, so the token rides back as an
				// empty secret entry: the hub's keep-on-empty rule holds its stored
				// value, and the timeout is the only entry that actually moves. Anything
				// else the image carried is dropped by the same write — the plan detail
				// and this report both say so, because the plugin cannot read the list
				// back to merge it.
				env := []EnvVar{
					{Name: TokenEnvName, Value: "", Secret: true},
					{Name: TurnTimeoutEnvName, Value: newTimeout},
				}
				if err := a.Hub.UpdateAgentImageEnv(ctx, roleState.ImageID, env); err != nil {
					return Install{}, report, fmt.Errorf("UpdateAgentImageEnv for %s: %w", role, err)
				}
				roleState.TurnTimeoutMs = newTimeout
				report.Created = append(report.Created, Op{Kind: KindImage, Name: ImageName(role, prev.Slug), Detail: "turn timeout updated; " + turnTimeoutEnvRewrite, Role: role, Field: FieldTurnTimeout})
			}
		} else if roleState.TurnTimeoutMs == "" {
			// A row written before the option existed records nothing; the image it
			// names carries the shipped default of its creation date. Record the
			// current one without writing to the image: a hand-patched value there
			// survives until the admin sets the field once, and from then on the row
			// diffs against a real value.
			roleState.TurnTimeoutMs = DefaultTurnTimeoutMs(role)
		}

		// The agent: its display name and its llm. One call carries whatever the
		// plan says changed, because an empty field of an update keeps what the hub
		// has rather than clearing it. The llm written is the settings' value —
		// what the plan diffed against the prior — never text from the form. The
		// key's op is the one the plan can only see through the row's fingerprint:
		// a settings key the row does not record has not reached the agent, and
		// this write carries it.
		agentChanged := changed(KindAgent, role, FieldAgentName) ||
			changed(KindAgent, role, FieldLLMProvider) ||
			changed(KindAgent, role, FieldLLMModel) ||
			changed(KindAgent, role, FieldLLMCustomUrl) ||
			changed(KindAgent, role, FieldLLMCustomKey) ||
			changed(KindAgent, role, FieldLLMOpenAIKey) ||
			changed(KindAgent, role, FieldLLMAnthropicKey) ||
			changed(KindAgent, role, FieldLLMGoogleKey) ||
			changed(KindAgent, role, FieldWorkingHours) ||
			changed(KindAgent, role, FieldLLMOpenAIAuthMethod)
		if agentChanged {
			newAgentName := in.role(role).AgentName
			if newAgentName == "" {
				newAgentName = AgentName(role, prev.Slug)
			}
			// The custom pair and the vendor key ride only while the role runs on
			// the provider they belong to. Moving away from a provider sends only
			// the new one: the hub keeps the block it holds for the provider the
			// role left, and it is inert while the llm names another provider —
			// the same keep-on-omit semantics the hub itself defines.
			llmWrite := llmWriteFor(a.Settings, role)
			// The method the plan says moved is the method the write names. A role
			// that moved *to* the sign-in already carries it from llmWriteFor; a role
			// that moved *off* it has to say so, because the hub keeps the method it
			// holds on an empty one and an agent left on a sign-in with a key
			// authenticates with neither. Any other edit leaves the field empty and
			// the hub's stored method where it is.
			if llmWrite.OpenAIAuthMethod == "" && changed(KindAgent, role, FieldLLMOpenAIAuthMethod) {
				llmWrite.OpenAIAuthMethod = LLMAuthMethodAPIKey
			}

			// The window rides only on the plan's own verdict for the field: no op,
			// no key — neither a write of the form's value nor the hub's clear. That
			// is the whole of what keeps a console-set window alive here.
			hours := workingHoursWriteFor(in, role, changed(KindAgent, role, FieldWorkingHours))

			if err := a.Hub.UpdateAgent(ctx, roleState.AgentID, newAgentName, llmWrite, nil, hours); err != nil {
				return Install{}, report, fmt.Errorf("UpdateAgent for %s: %w", role, err)
			}
			// An empty field of an update leaves what the hub has, so the row keeps what is
			// stored when the settings answer nothing rather than recording the blank.
			if llmWrite.Provider != "" {
				roleState.LLMProvider = llmWrite.Provider
			}
			if llmWrite.Model != "" {
				roleState.LLMModel = llmWrite.Model
			}
			// The fingerprint records the key the hub now holds. An empty write
			// kept the hub's key, so the recorded one still names it; a written
			// key moves the fingerprint to it.
			if key := llmWrite.LLMKeyOf(llmWrite.Provider); key != "" {
				roleState.SetLLMKeyHash(llmWrite.Provider, CustomKeyFingerprint(key))
			}
			// The row records the window the wizard now stands behind, which is the form's
			// answer — the write above or the clear it asked for. Nothing is recorded for a
			// field the plan said nothing about, so the row keeps saying what the wizard last
			// wrote rather than what the hub happens to hold.
			if hours.Clear {
				roleState.WorkingHours = nil
			} else if hours.Set != nil {
				roleState.WorkingHours = hours.Set
			}
			// The row records the method the agent now holds, for the provider that
			// has one. Away from openai there is nothing to record and the row keeps
			// what it said — the hub's openAI block is inert there, and so is this.
			if llmWrite.Provider == LLMProviderOpenAI {
				roleState.LLMOpenAIAuthMethod = OpenAIAuthMethodOf(a.Settings.llm(role))
				if llmWrite.OpenAIAuthMethod == LLMAuthMethodChatGPTSignIn {
					report.Notes = append(report.Notes, signInNote(role, newAgentName, roleState.AgentID))
				}
			}
			report.Created = append(report.Created, Op{Kind: KindAgent, Name: newAgentName, Detail: "agent updated", Role: role})
		}

		install.Roles[role] = roleState
	}

	// The exclude label is a filter on the refiner's two issue triggers, so changing
	// it means those two triggers, and nothing else.
	if changed(KindTrigger, RoleRefiner, FieldExcludeLabel) {
		existingTriggers, err := a.Hub.ListTriggers(ctx, prev.ConnectorID)
		if err != nil {
			return Install{}, report, fmt.Errorf("ListTriggers: %w", err)
		}

		oldIssueTriggerNames := []string{
			triggerName(RoleRefiner, "issue-opened", prev.Slug),
			triggerName(RoleRefiner, "issue-reopened", prev.Slug),
		}

		for _, oldName := range oldIssueTriggerNames {
			for _, existingTrigger := range existingTriggers {
				if existingTrigger.Name == oldName {
					if err := a.Hub.DeleteTrigger(ctx, existingTrigger.ID); err != nil {
						return Install{}, report, fmt.Errorf("DeleteTrigger for %s: %w", oldName, err)
					}
					report.RolledBack = append(report.RolledBack, Op{Kind: KindTrigger, Name: oldName, Detail: "trigger deleted"})
					break
				}
			}
		}

		newInstall := install
		newInstall.ExcludeLabel = in.ExcludeLabel
		newTriggers := Triggers(newInstall)

		for _, newTrigger := range newTriggers {
			if newTrigger.Name == triggerName(RoleRefiner, "issue-opened", prev.Slug) ||
				newTrigger.Name == triggerName(RoleRefiner, "issue-reopened", prev.Slug) {
				_, err := a.Hub.CreateTrigger(ctx, install.GroupID, newTrigger)
				if err != nil {
					return Install{}, report, fmt.Errorf("CreateTrigger for exclude label change: %w", err)
				}
				report.Created = append(report.Created, Op{Kind: KindTrigger, Name: newTrigger.Name, Detail: "trigger created"})
			}
		}
	}

	return install, report, nil
}

// setRoleTools refreshes one role image's catalog and answers with the names the role's
// agent is granted, plus the names it had to leave out.
//
// The catalog is read back first, because only the hub knows the names the MCP server
// actually offers: a tool the plan calls "list_repo_issues" reaches the hub as
// "mcp__forgejo-mcp-<slug>__list_repo_issues", and bash is in the catalog neither way —
// the hub discovers no shell tools (spec §3.5). A name matching none of the three is
// refused when strict, and dropped from the grant (and named in the second answer) when
// not: strict is "the admin asked for this set", lenient is "this apply restates a set
// nobody changed", where one tool renamed upstream must not abort the whole install.
//
// The write is a catalog refresh, not a grant. Since the hub moved tool selection onto
// the agent, an image's enabled flag restricts no agent on its own: it is the catalog's
// own default for a tool the last refresh discovered. So the tools the refresh found go
// back as they came, flags included, and the only entries this pass switches on are the
// hand-declared ones — the shell tool the role's set names, and the plugin's own two
// read-only tools, which every grant carries (see PluginToolNames). Both have to be in
// the catalog before the agent can be granted them.
//
// The names it answers with are the role's set in the catalog's own spellings, which is
// what an agent's grant has to be built from: the hub validates a grant against the
// image's catalog by exact match.
//
// An empty request is refused: writing it would grant nothing, and an agent with no
// grant is the runtime's "everything", not the role's "nothing".
func (a Apply) setRoleTools(ctx context.Context, imageID, slug string, requested []string, strict bool) ([]string, []string, error) {
	if len(requested) == 0 {
		return nil, nil, fmt.Errorf("no tools: the request named none and no default set was offered")
	}

	allTools, serverErrors, refreshErr := a.Hub.RefreshImageTools(ctx, imageID)
	if err := refreshToolsError(serverErrors, refreshErr); err != nil {
		return nil, nil, err
	}

	// The catalog goes back as the refresh read it. Restating the whole thing is what
	// SetImageTools takes, so the hand-declared entries below are the only addition.
	tools := append([]ImageTool(nil), allTools...)
	present := make(map[string]bool, len(allTools))
	for _, t := range allTools {
		present[t.Name] = true
	}

	granted := make([]string, 0, len(requested))
	var dropped []string
	seen := make(map[string]bool, len(requested))
	for _, name := range requested {
		written := name
		if !present[name] {
			switch mcp := MCPToolName(slug, name); {
			case present[mcp]:
				written = mcp
			case name == "bash":
				// Declared by hand: the hub discovers no shell tools, so bash is never in
				// the catalog a refresh returns until this pass puts it there.
				if !seen[name] {
					tools = append(tools, ImageTool{Name: name, Kind: "shell", Enabled: true})
					present[name] = true
				}
			case strict:
				return nil, nil, fmt.Errorf("tool %q not in catalog", name)
			default:
				// The set did not change on this apply, so a name the server no longer
				// reports is drift to degrade rather than a request to refuse: the drift pass
				// (grantedToolNames) drops it, logs it and grants the rest, and an apply that
				// gates nothing on the plan has to agree with it or one renamed tool upstream
				// leaves the install un-savable.
				dropped = append(dropped, name)
				continue
			}
		}
		if seen[written] {
			continue
		}
		seen[written] = true
		granted = append(granted, written)
	}

	// Every grant names the plugin's own two read-only tools. The runtime reads a grant as
	// exhaustive across the agent's MCP servers, so leaving them out is what switches them
	// off — which README and spec §3.5 rule out. They are the second hand-declared entry
	// after bash: the hub cannot list `plugin-forgejo`, so a refresh never puts them in the
	// catalog, and the hub refuses a grant name the catalog does not carry.
	for _, name := range PluginToolNames() {
		if !present[name] {
			tools = append(tools, ImageTool{Name: name, Kind: "mcp", Enabled: true})
			present[name] = true
		}
		if !seen[name] {
			seen[name] = true
			granted = append(granted, name)
		}
	}

	if err := a.Hub.SetImageTools(ctx, imageID, tools); err != nil {
		return nil, nil, err
	}
	return granted, dropped, nil
}

// PluginToolName is one of the plugin's own MCP tools in the name the runtime matches it
// by: they are served by the plugin's MCP row (`plugin-forgejo`), and ainsel-tools matches
// an MCP tool by its full `mcp__<server>__<tool>` name — a bare tool name is a pi tool, and
// a bare server name is not something the Agent CR can express.
func PluginToolName(tool string) string {
	return fmt.Sprintf("mcp__%s__%s", pluginServerName, tool)
}

// PluginToolNames are the plugin's two read-only tools as a grant has to spell them.
// README promises them to every role agent, switched off by nothing; a grant that left
// them out would break that promise, because the runtime narrows the model's tool set to
// the selection across every MCP server the agent has. They are declared into the image's
// catalog by setRoleTools, since the hub cannot list the plugin's own server (spec §3.5).
func PluginToolNames() []string {
	return []string{PluginToolName("list_work"), PluginToolName("get_work_item")}
}

// sameToolSet reports whether a resolved grant names the set a row holds. The row holds
// the bare Forgejo names and the grant spells the MCP ones the way the catalog does, so
// the two agree by name underneath that prefix rather than as written. The plugin's own
// two tools are read as part of every grant and of no row, so they are not a difference.
func sameToolSet(granted, row []string, slug string) bool {
	prefix := MCPToolName(slug, "")
	bare := func(name string) string { return strings.TrimPrefix(name, prefix) }
	always := PluginToolNames()

	have := make([]string, 0, len(granted))
	seen := make(map[string]bool, len(granted))
	for _, g := range granted {
		if slices.Contains(always, g) {
			continue
		}
		if n := bare(g); !seen[n] {
			seen[n] = true
			have = append(have, n)
		}
	}
	want := make([]string, 0, len(row))
	seenRow := make(map[string]bool, len(row))
	for _, r := range row {
		if n := bare(r); !seenRow[n] {
			seenRow[n] = true
			want = append(want, n)
		}
	}
	slices.Sort(have)
	slices.Sort(want)
	return slices.Equal(have, want)
}

// MCPToolName is the name the hub gives a tool that comes from this install's own MCP
// server, which is how the catalog lists it and so how an image must name it. Anything
// matching a tool over the row's stored set has to spell it this way, because the row holds
// the bare Forgejo name and the catalog holds this one.
func MCPToolName(slug, tool string) string {
	return fmt.Sprintf("mcp__%s__%s", MCPServerName(slug), tool)
}

// refreshToolsError decides what a failed image-tool refresh means. The plugin's
// own MCP server cannot be listed by the hub, and that one error is expected
// (spec §3.5); any other failure is real and names the server that failed —
// including a failure the hub reported without a per-server breakdown, which
// must not read as an empty catalog.
func refreshToolsError(serverErrors map[string]string, refreshErr error) error {
	if refreshErr == nil {
		return nil
	}
	if len(serverErrors) == 1 {
		if _, isPlugin := serverErrors[pluginServerName]; isPlugin {
			return nil
		}
	}
	if len(serverErrors) == 0 {
		return fmt.Errorf("RefreshImageTools: %w", refreshErr)
	}
	var parts []string
	for server, errMsg := range serverErrors {
		parts = append(parts, fmt.Sprintf("hub could not read MCP server %s: %s", server, errMsg))
	}
	slices.Sort(parts)
	return fmt.Errorf("RefreshImageTools: %s", strings.Join(parts, "; "))
}
