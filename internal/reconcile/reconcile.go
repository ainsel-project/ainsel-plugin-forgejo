// Package reconcile is the plugin's one reconcile engine: it reads every
// install's managed resources back from the hub, computes the state the plugin
// owes them — the shipped defaults crossed with the admin's settings — and
// writes the difference. The plugin created these resources and is their sole
// writer (design decision 1), so a write states the exact value rather than
// merging with whatever drift arrived; the sets an agent names (its skills, its
// MCP servers, its tool grant) are the manifest's or the install row's fixed
// sets, enforced here in code rather than unioned with what the hub happens to
// hold.
//
// The pass is triggered, not scheduled (design decision 8): saving settings
// reconciles the resources those fields touch, a plugin start after an upgrade
// adopts the new shipped defaults and reconciles everything, and "Check now"
// reconciles everything. There is no periodic timer. A refused write is a
// warning in the summary and the log, never a halt: one resource the hub will
// not take today does not hold the rest of the fleet behind it.
package reconcile

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ainsel-project/ainsel-plugin-forgejo/content"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/settings"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
)

// Scope says which kinds of resource a pass is allowed to read and write. It
// exists for the settings-save trigger: a changed persona text has nothing to
// say about an agent's llm, and a pass that touched everything anyway is the
// old periodic loop with a new name. The bits map onto the settings fields
// explicitly (see the API's mapping), and ScopeAll is what a startup or a
// "Check now" runs.
type Scope uint8

const (
	// ScopeNone names nothing: the answer for a saved field that owns no hub
	// resource — a skill text (until PR 4 serves it) asks for no pass at all.
	ScopeNone Scope = 0

	ScopePersona Scope = 1 << iota
	ScopeAgent
	ScopeImage
	ScopeMCPServer
	ScopeTrigger
	ScopeCoverage

	ScopeAll = ScopePersona | ScopeAgent | ScopeImage | ScopeMCPServer | ScopeTrigger | ScopeCoverage
)

// has reports whether the scope includes the kind.
func (s Scope) has(kind Scope) bool { return s&kind != 0 }

// Change is one resource the pass brought back to the state the plugin owes.
// From and To are short renderings for the log and the console, not payloads:
// the full text of a persona is not something to hand a browser unsolicited.
type Change struct {
	Install string `json:"install"`
	Role    string `json:"role,omitempty"`
	Kind    string `json:"kind"`
	Field   string `json:"field"`
	From    string `json:"from,omitempty"`
	To      string `json:"to"`
}

// Refusal is one write the hub would not take. It is the pass saying "this is
// still wrong" rather than the pass failing: the rest of the resources went on
// being reconciled around it.
type Refusal struct {
	Install string `json:"install"`
	Role    string `json:"role,omitempty"`
	Kind    string `json:"kind"`
	Field   string `json:"field"`
	Error   string `json:"error"`
}

// Summary is what a pass answers with: what it updated and what the hub
// refused, per resource. "Check now" hands it to the console; the log carries
// the same lines as they happen.
type Summary struct {
	Updated []Change  `json:"updated"`
	Refused []Refusal `json:"refused"`
}

// newSummary is the empty answer with its lists already there: a pass that
// wrote nothing answers with [], not null, so the console reads one shape.
func newSummary() *Summary {
	return &Summary{Updated: []Change{}, Refused: []Refusal{}}
}

func (s *Summary) updated(c Change) { s.Updated = append(s.Updated, c) }

func (s *Summary) refused(r Refusal) { s.Refused = append(s.Refused, r) }

// storeOps is what the pass needs of the install store: read every row, and
// write one back. The concrete *store.Store satisfies it; a test can hold the
// rows in memory instead and say whether a write happened at all.
type storeOps interface {
	List(context.Context) ([]setup.Install, error)
	Save(context.Context, setup.Install) error
}

// settingsReader is what the pass needs of the settings store: the effective
// value of every field. An Engine with no Settings reads the shipped defaults
// instead — the same answer an unedited store gives.
type settingsReader interface {
	All(context.Context) ([]settings.Setting, error)
}

// defaultsAdopter is what the startup pass needs of the settings store to
// settle the upgrade: move every not-dirty row whose default_version lags onto
// what this plugin ships (the winner rule of design decision 4).
type defaultsAdopter interface {
	AdoptDefaults(context.Context) ([]settings.Adopted, error)
}

// Engine is the reconcile pass: the hub to read and write, the installs to
// reconcile, the settings to reconcile them against, and a log for every
// correction and refusal as it happens.
type Engine struct {
	// Hub is the client to read and write AInsel resources.
	Hub hub.Client

	// Store is the database that holds installs.
	Store storeOps

	// Settings is the settings store, read once per pass for the effective
	// values. Nil reads the shipped defaults.
	Settings settingsReader

	// Defaults is the settings store seen as the upgrade's adopter; only
	// Startup uses it. Nil means Startup reconciles without adopting — a
	// plugin built without a settings store, which nothing in the tree does.
	Defaults defaultsAdopter

	// Log is called once per correction or refusal, e.g. "corrected the tool grant
	// on forgejo-refiner-app". Called as log(slug, message). Must not panic.
	Log func(slug, msg string)
}

// log is Log with the two things a caller forgets: that it can be nil, and that
// a pass logs both per-install lines and bare ones.
func (e Engine) log(slug, msg string) {
	if e.Log != nil {
		e.Log(slug, msg)
	}
}

// subjectRetention is how long a cached event subject is kept: past it, the
// event is older than any page's window can show, and its subject is kept for a
// question no page asks any more.
const subjectRetention = 45 * 24 * time.Hour

// Pass reconciles every install the plugin can act for, within scope: which
// resources it reads and may write. ctx is wrapped with hub.WithBackground for
// all hub calls — the reconcile is the plugin writing its own resources, not
// acting as whoever happened to save a setting. It answers with what it
// updated and what the hub refused; the error is reserved for the pass itself
// not running (no group access to read, no installs to ask about).
func (e Engine) Pass(ctx context.Context, scope Scope) (Summary, error) {
	ctx = hub.WithBackground(ctx)
	sum := newSummary()

	// Read the plugin's group access.
	pluginGroups, err := e.Hub.PluginGroups(ctx)
	if err != nil {
		return *sum, fmt.Errorf("PluginGroups: %w", err)
	}
	groupAccess := make(map[string]bool, len(pluginGroups))
	for _, g := range pluginGroups {
		groupAccess[g] = true
	}

	// Read all installs.
	installs, err := e.Store.List(ctx)
	if err != nil {
		return *sum, fmt.Errorf("Store.List: %w", err)
	}

	// Read the settings once, so every install is reconciled against one
	// reading rather than one reading each.
	want, err := e.desiredState(ctx)
	if err != nil {
		return *sum, fmt.Errorf("settings: %w", err)
	}

	// The forge MCP rows are one registry list, read once for the whole pass.
	var mcpRows map[string]hub.MCPServer
	if scope.has(ScopeMCPServer) {
		servers, err := e.Hub.ListMCPServers(ctx)
		if err != nil {
			// The pass goes on without them: refusing the MCP step for
			// every install is honest; guessing is not.
			e.log("", fmt.Sprintf("ListMCPServers: %v: mcp rows skipped", err))
		} else {
			mcpRows = make(map[string]hub.MCPServer, len(servers))
			for _, s := range servers {
				mcpRows[s.Name] = s
			}
		}
	}

	// Process each install.
	for _, in := range installs {
		if !groupAccess[in.GroupID] {
			// Skip installs the plugin can't act for, logged once per pass
			e.log(in.Slug, "skipped: group not in plugin access")
			continue
		}

		if in.LayoutVersion != setup.LayoutManagedAgents {
			// A legacy row is an install whose resources the old wizard may
			// have adopted — resources the plugin did not create and has no
			// claim to write. The fresh-start cutover deletes these rows at
			// start; until it has (or a cutover refusal left one), the pass
			// leaves them alone rather than acting as sole writer of
			// something it never made.
			e.log(in.Slug, "legacy install: left alone (awaiting cutover)")
			continue
		}

		// Coverage is what a full pass earns on the row, so hold on to what
		// the row said: reconcileCoverage fills the map in, in place, so the
		// copy is what still answers "what was there before this pass". The
		// cursor is the other half of what the pass earns: a pass that read
		// events and moved it has to save, even when coverage says the same
		// as it did.
		before := coverageSnapshot(in.Coverage)
		beforeCursor := cursorSnapshot(in.EventsReadUntil)
		beforeLLM := llmSnapshot(in.Roles)

		// The fold reports the earliest event it took in, if it took in any:
		// that first event is the proof the hook reaches the connector, which
		// is what verification means.
		firstEvent := e.reconcileInstall(ctx, &in, want, scope, mcpRows, sum)

		// The llm the row holds follows the settings when the agent was set
		// to them: a row that still names the model an old version created
		// the agent on is a row that would tell the next reader a lie.
		llmMoved := !llmsMatch(beforeLLM, llmSnapshot(in.Roles))

		// Nothing new learned, nothing to write. Writing anyway is how a pass
		// overwrites an edit the API saved while it was running, which the
		// next pass would then undo. A pass that only has a first event to
		// record does have something to write: an admin who continued past S7
		// before anything arrived left the row saying "Not verified", and
		// this is the pass that can say otherwise.
		verifies := in.WebhookVerifiedAt == nil && firstEvent != nil
		if !coverageChanged(before, in.Coverage) && !cursorChanged(beforeCursor, in.EventsReadUntil) && !verifies && !llmMoved {
			continue
		}

		// Write onto the row that is there now, not the copy read at the top
		// of the pass: an edit saved mid-pass keeps everything on it except
		// what this pass earned — coverage, the cursor, the verification, and
		// the role's llm fields. A row that is gone by now was removed, and
		// stays removed.
		current, err := e.Store.List(ctx)
		if err != nil {
			e.log(in.Slug, fmt.Sprintf("error reloading installs: %v", err))
			continue
		}
		row, found := findInstall(current, in.Slug)
		if !found {
			e.log(in.Slug, "install removed during the pass: nothing saved")
			continue
		}
		// Only what this pass actually earned. A targeted pass owns no
		// coverage: writing the copy it read at the top would roll back
		// whatever a fuller pass saved while it ran.
		if scope.has(ScopeCoverage) {
			row.Coverage = in.Coverage
			row.EventsReadUntil = in.EventsReadUntil
		}
		// The first event read verifies the install, but never over a
		// verification the API recorded from the S7 poll: that one was seen on
		// a real delivery, and is earlier.
		if firstEvent != nil && row.WebhookVerifiedAt == nil {
			at := *firstEvent
			row.WebhookVerifiedAt = &at
		}
		for role, state := range in.Roles {
			if rs, ok := row.Roles[role]; ok {
				rs.LLMProvider = state.LLMProvider
				rs.LLMModel = state.LLMModel
				rs.LLMCustomKeyHash = state.LLMCustomKeyHash
				rs.OpenAIKeyHash = state.OpenAIKeyHash
				rs.AnthropicKeyHash = state.AnthropicKeyHash
				rs.GoogleKeyHash = state.GoogleKeyHash
				rs.LLMOpenAIAuthMethod = state.LLMOpenAIAuthMethod
				row.Roles[role] = rs
			}
		}

		// Save the updated install (with new coverage and, where the settings
		// moved it, the row's own llm).
		if err := e.Store.Save(ctx, row); err != nil {
			e.log(in.Slug, fmt.Sprintf("error saving: %v", err))
			continue
		}
	}

	// The event subject cache is pruned at the end of a pass that read events:
	// subjects of events older than any page's window answer a question no page
	// asks any more. A store that cannot prune simply keeps them a while
	// longer, and a prune that fails is logged rather than a pass that fails —
	// the installs are already reconciled.
	if scope.has(ScopeCoverage) {
		if p, ok := e.Store.(interface {
			PruneSubjects(context.Context, time.Time) (int64, error)
		}); ok {
			if _, err := p.PruneSubjects(ctx, time.Now().Add(-subjectRetention)); err != nil {
				e.log("", fmt.Sprintf("error pruning event subjects: %v", err))
			}
		}
	}

	return *sum, nil
}

// Startup is the pass a plugin start runs after an upgrade: the settings first
// (the winner rule — a field nobody edited moves to what the new version
// ships, a field an admin edited stays, down to the version stamped on it),
// then every install, so agents that follow the defaults pick the new values up
// without anyone asking. The adoption lines join the summary, so "the upgrade
// moved these fields" is part of the same answer as "and these resources
// followed".
func (e Engine) Startup(ctx context.Context) (Summary, error) {
	sum := newSummary()

	if e.Defaults != nil {
		adopted, err := e.Defaults.AdoptDefaults(ctx)
		if err != nil {
			// The upgrade half failing does not cancel the reconcile half:
			// the log says the defaults did not move, and the pass goes on to
			// reconcile against what the store still holds.
			e.log("", fmt.Sprintf("error adopting new defaults: %v", err))
		}
		for _, a := range adopted {
			e.log("", fmt.Sprintf("adopted the shipped default for %s.%s at %s", a.Role, a.Field, settings.PluginVersion()))
			sum.updated(Change{Kind: "setting", Field: a.Role + "." + a.Field, To: settings.PluginVersion()})
		}
	}

	pass, err := e.Pass(ctx, ScopeAll)
	sum.Updated = append(sum.Updated, pass.Updated...)
	sum.Refused = append(sum.Refused, pass.Refused...)
	return *sum, err
}

// desiredState is the settings read as the pass wants them: the persona
// template per role, the llm per role, and the forge MCP row's wiring. The
// persona and skill texts are the effective values — the admin's edit where
// there is one, the shipped default where there is not — because "defaults ×
// settings" is what the plugin owes, not the shipped text alone.
type desiredState struct {
	// persona holds the raw template per role, placeholders intact: the pass
	// renders each install's bots and address into it as it works.
	persona map[setup.Role]string
	llm     map[setup.Role]hub.LLMSpec

	// The custom endpoint per role, read from the settings: the URL is what a
	// pass diffs against the hub's answer; a key cannot be read back, so its
	// drift is seen against the install row's fingerprint (setup.RoleState).
	// Each key rides an agent write only for the provider the role runs on.
	//
	// llmKeys doubles as the reading of the OpenAI auth method, which is not a
	// key: it is the other half of how an openai role authenticates, and it is
	// the half the hub can read back.
	customURL map[setup.Role]string
	llmKeys   map[setup.Role]setup.RoleLLM

	// MCP wiring. Like the wizard's reading, the pair comes off the first
	// role's rows: the registry keeps a value per role because every settings
	// field lives per role, but an install has one forge MCP server.
	mcpURL      string
	mcpTokenEnv string
}

// desiredState reads the settings once. With no Settings source the answer is
// the shipped defaults — which is exactly what an empty settings store says.
func (e Engine) desiredState(ctx context.Context) (desiredState, error) {
	d := desiredState{
		persona:   make(map[setup.Role]string, len(settings.Roles)),
		llm:       make(map[setup.Role]hub.LLMSpec, len(settings.Roles)),
		customURL: make(map[setup.Role]string, len(settings.Roles)),
		llmKeys:   make(map[setup.Role]setup.RoleLLM, len(settings.Roles)),
	}
	for _, role := range settings.Roles {
		d.persona[role] = settings.Default(role, settings.FieldPersonaText)
		d.llm[role] = hub.LLMSpec{
			Provider: settings.Default(role, settings.FieldLLMProvider),
			Model:    settings.Default(role, settings.FieldLLMModel),
		}
		// The shipped auth method too, so a pass with no settings source still
		// wants the method an untouched install was created with rather than
		// reading an empty one as a statement about the agent.
		d.llmKeys[role] = setup.RoleLLM{
			OpenAIAuthMethod: settings.Default(role, settings.FieldLLMOpenAIAuthMethod),
		}
	}
	d.mcpURL = settings.Default(settings.Roles[0], settings.FieldMCPUrl)
	d.mcpTokenEnv = settings.Default(settings.Roles[0], settings.FieldMCPTokenFromEnv)

	if e.Settings == nil {
		return d, nil
	}
	all, err := e.Settings.All(ctx)
	if err != nil {
		return d, err
	}
	for _, s := range all {
		role := setup.Role(s.Role)
		switch s.Field {
		case settings.FieldPersonaText:
			if s.Value != "" {
				d.persona[role] = s.Value
			}
		case settings.FieldLLMProvider, settings.FieldLLMModel:
			llm := d.llm[role]
			if s.Field == settings.FieldLLMProvider {
				llm.Provider = s.Value
			} else {
				llm.Model = s.Value
			}
			d.llm[role] = llm
		case settings.FieldLLMCustomUrl:
			d.customURL[role] = s.Value
		case settings.FieldLLMCustomKey, settings.FieldLLMOpenAIKey,
			settings.FieldLLMAnthropicKey, settings.FieldLLMGoogleKey,
			settings.FieldLLMOpenAIAuthMethod:
			// The store's raw value: masking (settings.MaskSecrets) belongs to
			// the console answer, not to this reading — the pass may write a key
			// back to the hub.
			keys := d.llmKeys[role]
			switch s.Field {
			case settings.FieldLLMCustomKey:
				keys.CustomKey = s.Value
			case settings.FieldLLMOpenAIKey:
				keys.OpenAIKey = s.Value
			case settings.FieldLLMAnthropicKey:
				keys.AnthropicKey = s.Value
			case settings.FieldLLMGoogleKey:
				keys.GoogleKey = s.Value
			case settings.FieldLLMOpenAIAuthMethod:
				keys.OpenAIAuthMethod = s.Value
			}
			d.llmKeys[role] = keys
		case settings.FieldMCPUrl:
			if role == settings.Roles[0] {
				d.mcpURL = s.Value
			}
		case settings.FieldMCPTokenFromEnv:
			if role == settings.Roles[0] {
				d.mcpTokenEnv = s.Value
			}
		}
	}
	return d, nil
}

// The manifest's fixed sets (design decision 3): which skill and which MCP
// servers an agent names are not settings — membership changes only by plugin
// upgrade — so the pass writes exactly these and nothing else.
const pluginMCPServerName = "plugin-forgejo"

func wantedSkills() []string { return []string{"forgejo-forgejo"} }
func wantedMCPServers(slug string) []string {
	return []string{setup.MCPServerName(slug), pluginMCPServerName}
}

// reconcileInstall brings one install's resources within scope back to the
// wanted state, writing the summary as it goes. It reports the earliest event
// folded into coverage by this pass, or nil when it folded in none. A read that
// fails stops that resource and nothing else: the next resource is read and
// fixed the same as if the first had agreed.
func (e Engine) reconcileInstall(ctx context.Context, in *setup.Install, want desiredState, scope Scope, mcpRows map[string]hub.MCPServer, sum *Summary) *time.Time {
	// The bots the persona names, from the row itself.
	bots := make(map[setup.Role]string, len(in.Roles))
	for role, state := range in.Roles {
		bots[role] = state.BotLogin
	}

	// Reconcile each role's persona, image and agent.
	for _, role := range settings.Roles {
		roleState, ok := in.Roles[role]
		if !ok {
			continue // Role not created yet
		}

		if scope.has(ScopePersona) && roleState.PersonaID != "" {
			e.reconcilePersona(ctx, in, role, roleState, bots, want, sum)
		}

		// The role's tool grant, resolved from the image's catalog. The hub validates a
		// grant against the catalog by exact match, so the names an agent is granted have
		// to come from there — which is why the agent's check reads the image too, and why
		// this one runs whenever either of the two is in scope.
		var grant []string
		if roleState.ImageID != "" && (scope.has(ScopeImage) || scope.has(ScopeAgent)) {
			grant = e.reconcileImage(ctx, in, role, &roleState)
		}

		if scope.has(ScopeAgent) && roleState.AgentID != "" {
			e.reconcileAgent(ctx, in, role, &roleState, want, grant, sum)
		}

		// Update the role state in case the pass moved the llm the row holds.
		in.Roles[role] = roleState
	}

	// The forge MCP row: the install-level wiring the settings own.
	if scope.has(ScopeMCPServer) && mcpRows != nil {
		e.reconcileMCPServer(ctx, in, want, mcpRows, sum)
	}

	// Reconcile the cron triggers (the developer's PR patrol). They belong to no
	// connector, so they are reconciled on their own — but only once the
	// developer's agent exists: a trigger with no agent to fire at is refused
	// by the hub.
	if scope.has(ScopeTrigger) && in.Roles[setup.RoleDeveloper].AgentID != "" {
		e.reconcileCronTriggers(ctx, in, sum)
	}

	if in.ConnectorID == "" {
		return nil
	}

	// Reconcile triggers.
	if scope.has(ScopeTrigger) {
		e.reconcileTriggers(ctx, in, sum)
	}

	// Reconcile coverage (collect new events).
	if !scope.has(ScopeCoverage) {
		return nil
	}
	firstEvent, err := e.reconcileCoverage(ctx, in)
	if err != nil {
		e.log(in.Slug, fmt.Sprintf("reconcile coverage: %v", err))
		return nil
	}
	return firstEvent
}

// reconcilePersona sets a role's persona back to the effective personaText,
// rendered with this install's bots and address. An empty wanted text is no
// text at all: it is never what the settings meant, and blanking a persona is
// not a correction.
func (e Engine) reconcilePersona(ctx context.Context, in *setup.Install, role setup.Role, roleState setup.RoleState, bots map[setup.Role]string, want desiredState, sum *Summary) {
	template := want.persona[role]
	if template == "" {
		return
	}
	wanted := content.RenderPersona(template, bots, in.BaseURL)

	persona, err := e.Hub.GetPersona(ctx, roleState.PersonaID)
	if err != nil {
		e.log(in.Slug, fmt.Sprintf("read persona %s: %v: persona left alone", roleState.PersonaID, err))
		return
	}
	if persona.Text == wanted {
		return
	}

	name := setup.PersonaName(role, in.Slug)
	if err := e.Hub.UpdatePersona(ctx, roleState.PersonaID, wanted); err != nil {
		e.log(in.Slug, fmt.Sprintf("refused persona rewrite on %s: %v", name, err))
		sum.refused(Refusal{Install: in.Slug, Role: string(role), Kind: "persona", Field: "personaText", Error: err.Error()})
		return
	}
	e.log(in.Slug, fmt.Sprintf("rewrote persona on %s to the settings' text", name))
	sum.updated(Change{Install: in.Slug, Role: string(role), Kind: "persona", Field: "personaText", To: "settings' text"})
}

// reconcileImage reads a role image's catalog and answers with the names the row's tool
// set carries in it — the grant reconcileAgent writes onto the agent.
//
// It no longer enables and disables anything. Since the hub moved tool selection onto
// the agent, an image's enabled flag restricts no agent on its own: it is the catalog's
// own default for a tool the last refresh discovered, and switching a tool off here said
// nothing about what any agent may call. What the catalog lists still matters, because
// the hub validates an agent's grant against it by exact match — a name it dropped
// cannot be granted, and is left out of the answer with a line in the log saying so.
//
// A nil answer means there is nothing to grant: the catalog could not be read, or the
// row names no tools. The agent's grant is then left exactly as the hub holds it, which
// is the pass reading a failed read as "say nothing" rather than as "clear it".
func (e Engine) reconcileImage(ctx context.Context, in *setup.Install, role setup.Role, roleState *setup.RoleState) []string {
	img, err := e.Hub.GetAgentImage(ctx, roleState.ImageID)
	if err != nil {
		e.log(in.Slug, fmt.Sprintf("read image %s: %v: image left alone", roleState.ImageID, err))
		return nil
	}

	granted, missing := grantedToolNames(in.Slug, img.Tools, roleState.Tools)
	for _, name := range missing {
		e.log(in.Slug, fmt.Sprintf("image %s no longer lists %s: it is left out of %s's grant",
			setup.ImageName(role, in.Slug), name, setup.AgentName(role, in.Slug)))
	}
	return granted
}

// grantedToolNames resolves the bare Forgejo names a row holds into the names the
// image's catalog carries them under, which is what a grant has to be written in. A name
// the catalog lists under neither spelling is left out and named in the second answer:
// granting it would be a write the hub refuses, and a grant that names less than the row
// is narrower, which is the safe direction to be wrong in. That now includes `bash`: the
// pass reads the catalog and never writes it, so a hand-declared entry a refresh dropped
// is not this pass's to restore — the next apply re-declares it.
func grantedToolNames(slug string, catalog []hub.ImageTool, row []string) (granted, missing []string) {
	have := make(map[string]bool, len(catalog))
	for _, t := range catalog {
		have[t.Name] = true
	}

	seen := make(map[string]bool, len(row))
	for _, name := range row {
		written := name
		if !have[name] {
			if mcp := setup.MCPToolName(slug, name); have[mcp] {
				written = mcp
			} else {
				// Neither spelling the catalog carries, bash included: this pass no longer
				// writes the image, so it cannot put a hand-declared entry back, and a grant
				// naming a name the catalog lacks is a write the hub refuses — which would
				// leave every other correction this role needs unwritten.
				missing = append(missing, name)
				continue
			}
		}
		if seen[written] {
			continue
		}
		seen[written] = true
		granted = append(granted, written)
	}
	// The plugin's own two read-only tools ride every grant, exactly as apply writes them:
	// the runtime reads a grant as exhaustive across the agent's MCP servers, so a pass that
	// left them out would switch them off, and one that added them to a grant that already
	// holds them is a write that changes nothing. They are still subject to the catalog rule
	// this function states for every other name: apply declares them into the image, but this
	// pass never writes the catalog, so an install an older plugin version created has a
	// catalog that provably lacks them. Naming them there would be a write the hub refuses,
	// and it rides the same AgentUpdate as every other correction this role needs — so the
	// name is left out and logged like any other, and the rest of the grant still lands.
	if len(granted) > 0 {
		for _, name := range setup.PluginToolNames() {
			if !have[name] {
				missing = append(missing, name)
				continue
			}
			if !seen[name] {
				seen[name] = true
				granted = append(granted, name)
			}
		}
	}
	return granted, missing
}

// grantNames is a tool list read as the row holds it: this install's MCP prefix stripped
// off, so a grant the hub returns in the catalog's spelling and the row's bare Forgejo
// names read as one set. Duplicates collapse, because a grant is a set and sameNames
// compares sets.
func grantNames(names []string, slug string) []string {
	prefix := setup.MCPToolName(slug, "")
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		n = strings.TrimPrefix(n, prefix)
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// keyPresence is what a correction line says about a provider key: whether one
// was there, never what it was (R4).
func keyPresence(hash string) string {
	if hash == "" {
		return "none"
	}
	return "set"
}

// keyFieldName names the correction a key write is recorded under — "openaiKey",
// "customKey" — the provider's own name, because an agent now has four key
// blocks and one bare name would not say which one moved.
func keyFieldName(provider string) string {
	return provider + "Key"
}

// reconcileAgent ensures an agent points at the image and the persona the
// install stored, runs on the model the settings name, names exactly the skills
// and MCP servers the manifest ships, and holds the grant the row's tool set
// resolves to in the image's catalog. Every write is the exact wanted value: the
// plugin owns these resources, so there is no "keep what drift added" — a skill
// or server the hub holds that the manifest does not name gets switched off by
// the same pass that switches a missing one on, and so does a tool an admin
// handed the agent by hand.
//
// grant is what reconcileImage resolved from the image's catalog. Empty means the
// pass has nothing to say about the grant: the catalog could not be read, or the row
// names no tools. Writing an empty grant would be the widening the runtime reads it
// as — no grant is "every tool the image offers" — so the field stays out of the
// request.
func (e Engine) reconcileAgent(ctx context.Context, in *setup.Install, role setup.Role, roleState *setup.RoleState, want desiredState, grant []string, sum *Summary) {
	agent, err := e.Hub.GetAgent(ctx, roleState.AgentID)
	if err != nil {
		e.log(in.Slug, fmt.Sprintf("read agent %s: %v: agent left alone", roleState.AgentID, err))
		return
	}

	needsUpdate := false
	update := hub.AgentUpdate{}
	name := setup.AgentName(role, in.Slug)
	// fixed names the fields this check corrected, so a refused write says
	// exactly which corrections the hub would not take.
	var fixed []Change

	// record is the correction line: the log, the pending corrections, and the
	// patch that carries the exact value.
	record := func(field, from, to string) {
		e.log(in.Slug, fmt.Sprintf("reset %s on %s from %s to %s", field, name, from, to))
		fixed = append(fixed, Change{Install: in.Slug, Role: string(role), Kind: "agent", Field: field, From: from, To: to})
		needsUpdate = true
	}

	if agent.ImageRef != roleState.ImageID {
		record("imageRef", agent.ImageRef, roleState.ImageID)
		update.ImageRef = &roleState.ImageID
	}

	if agent.PersonaID != roleState.PersonaID {
		record("persona", agent.PersonaID, roleState.PersonaID)
		update.PersonaID = &roleState.PersonaID
	}

	// The llm is the settings' value now, not the row's: the settings are what
	// the admin edits and the row is what follows. A settings reading that
	// names nothing for the role says nothing, and saying nothing must not
	// read as "reset this agent to no model".
	if wantLLM := want.llm[role]; wantLLM.Provider != "" || wantLLM.Model != "" {
		var current hub.LLMSpec
		if agent.LLM != nil {
			current = *agent.LLM
		}
		if current != wantLLM {
			record("llm", current.Provider+"/"+current.Model, wantLLM.Provider+"/"+wantLLM.Model)
			llm := wantLLM
			update.LLM = &llm
			// The row holds what the agent was set to run on, so it moves
			// with the write — but only once the hub has actually taken it.
			roleState.LLMProvider = wantLLM.Provider
			roleState.LLMModel = wantLLM.Model
		} else if roleState.LLMProvider != wantLLM.Provider || roleState.LLMModel != wantLLM.Model {
			// The agent agrees and the row does not: the row moves anyway,
			// and the write below is skipped. It is rare (an upgrade that
			// moved the default under a row written at an older version), and
			// "what does the row say the agent runs on" should not need a
			// second source to trust.
			roleState.LLMProvider = wantLLM.Provider
			roleState.LLMModel = wantLLM.Model
		}
	}

	// The provider's key block, while the role runs on that provider. The custom
	// URL is readable from the hub, so drift against the settings is corrected
	// directly. A key cannot be read back, so its drift is seen through the row's
	// fingerprint: a settings key the row does not record has not reached the
	// agent, and the pass writes it — always as the settings hold it, never as the
	// row's (stale) text. The correction line names the key's provider and its
	// presence, never its value (R4). Away from a provider the hub's block for it
	// is inert and is left exactly where it is.
	provider := want.llm[role].Provider
	prevKeyHash := roleState.LLMKeyHashOf(provider)

	// The OpenAI auth method, while the role runs on openai. It is the one half
	// of the credential the hub can read back, so drift is seen against the
	// agent rather than against the row's record: a settings save that moved a
	// role onto the ChatGPT sign-in (or back off it) is corrected here, and the
	// row moves with the write. Away from openai the hub's openAI block is inert
	// and the method it holds is left exactly where it is.
	signIn := false
	prevAuthMethod := roleState.LLMOpenAIAuthMethod
	if provider == setup.LLMProviderOpenAI {
		wantMethod := setup.OpenAIAuthMethodOf(want.llmKeys[role])
		haveMethod := agent.OpenAIAuthMethod
		if haveMethod == "" {
			// An agent that answers no method holds the hub's default.
			haveMethod = setup.LLMAuthMethodAPIKey
		}
		if haveMethod != wantMethod {
			record("openAI authMethod", haveMethod, wantMethod)
			method := wantMethod
			update.OpenAIAuthMethod = &method
		}
		roleState.LLMOpenAIAuthMethod = wantMethod
		signIn = wantMethod == setup.LLMAuthMethodChatGPTSignIn
	}

	// A role that signs in has no key to write: the hub refuses an openAI block
	// that names both, so the settings' key is left alone however it reads — it
	// stays inert in the hub's block until a Reset clears it.
	if wantKey := want.llmKeys[role].LLMKeyOf(provider); wantKey != "" && !signIn {
		wantKeyHash := setup.CustomKeyFingerprint(wantKey)
		keyDrift := wantKeyHash != prevKeyHash
		if provider == setup.LLMProviderCustom {
			// The custom block is the one with a URL beside it. A URL the hub does
			// not hold as the settings hold it moves with the key, so a rotated key
			// never lands on an endpoint nobody meant.
			if wantURL := want.customURL[role]; wantURL != "" && agent.CustomURL != wantURL {
				record("customURL", agent.CustomURL, wantURL)
				url := wantURL
				update.CustomURL = &url
				keyDrift = true
			}
		}
		if keyDrift {
			record(keyFieldName(provider), keyPresence(prevKeyHash), "set")
			key := wantKey
			setLLMKeyField(&update, provider, &key)
			roleState.SetLLMKeyHash(provider, wantKeyHash)
		}
	} else if provider == setup.LLMProviderCustom {
		// No key in the settings to write: the URL moves alone, and the hub keeps
		// whatever key it holds.
		if wantURL := want.customURL[role]; wantURL != "" && agent.CustomURL != wantURL {
			record("customURL", agent.CustomURL, wantURL)
			url := wantURL
			update.CustomURL = &url
		}
	}

	// The manifest's fixed sets, written exact when the hub disagrees. Order
	// is not drift: a hub that answers the same names in another order has
	// nothing corrected.
	if !sameNames(agent.MCPServers, wantedMCPServers(in.Slug)) {
		record("mcpServers", strings.Join(agent.MCPServers, ","), strings.Join(wantedMCPServers(in.Slug), ","))
		servers := wantedMCPServers(in.Slug)
		update.MCPServers = &servers
	}
	if !sameNames(agent.Skills, wantedSkills()) {
		record("skills", strings.Join(agent.Skills, ","), strings.Join(wantedSkills(), ","))
		skills := wantedSkills()
		update.Skills = &skills
	}

	// The role's tool grant. The row's set is what the agent is restricted to, spelled
	// the way the image's catalog carries it; both spellings of a name read as the same
	// set, so a grant written by an older apply is not drift.
	if len(grant) > 0 && !sameNames(grantNames(agent.EnabledTools, in.Slug), grantNames(grant, in.Slug)) {
		record("enabledTools", strings.Join(agent.EnabledTools, ","), strings.Join(grant, ","))
		g := grant
		update.EnabledTools = &g
	}

	if needsUpdate {
		if err := e.Hub.UpdateAgent(ctx, roleState.AgentID, update); err != nil {
			e.log(in.Slug, fmt.Sprintf("refused agent update on %s: %v", name, err))
			for _, c := range fixed {
				sum.refused(Refusal{Install: c.Install, Role: c.Role, Kind: c.Kind, Field: c.Field, Error: err.Error()})
			}
			// Undo the row's claimed llm and key-fingerprint moves: the hub
			// does not run them.
			// (Nothing else on the row moved from what it said before.)
			var current hub.LLMSpec
			if agent.LLM != nil {
				current = *agent.LLM
			}
			roleState.LLMProvider = current.Provider
			roleState.LLMModel = current.Model
			roleState.SetLLMKeyHash(provider, prevKeyHash)
			roleState.LLMOpenAIAuthMethod = prevAuthMethod
			return
		}
		for _, c := range fixed {
			sum.updated(c)
		}
	}
}

// reconcileMCPServer ensures the install's forge MCP row is registered and
// wired as the settings say: the URL it points at and the env var its token
// arrives in. The registry keys a server by name, so re-registering the name
// with the wanted wiring is the write. A row the hub does not have at all is
// recreated — the plugin created it once and is still the only writer;
// a registry that lost it did not decide to.
func (e Engine) reconcileMCPServer(ctx context.Context, in *setup.Install, want desiredState, rows map[string]hub.MCPServer, sum *Summary) {
	name := setup.MCPServerName(in.Slug)
	row, exists := rows[name]

	// An empty wanted URL says "nobody has said where the forge is yet". That
	// is a fact about the settings, not a write: it never registers an empty
	// server, and never blanks one that exists. The guard covers both cases —
	// an empty URL is no statement about the forge, whatever the row says.
	if want.mcpURL == "" {
		return
	}
	if exists && row.URL == want.mcpURL && row.TokenFromEnv == want.mcpTokenEnv {
		return
	}

	from := "(missing)"
	if exists {
		from = row.URL + " token:" + row.TokenFromEnv
	}
	_, err := e.Hub.RegisterMCPServer(ctx, hub.MCPServerSpec{
		Name:         name,
		GroupID:      in.GroupID,
		URL:          want.mcpURL,
		TokenFromEnv: want.mcpTokenEnv,
	})
	if err != nil {
		e.log(in.Slug, fmt.Sprintf("refused mcp row %s: %v", name, err))
		sum.refused(Refusal{Install: in.Slug, Kind: "mcpServer", Field: name, Error: err.Error()})
		return
	}
	e.log(in.Slug, fmt.Sprintf("set mcp row %s to %s token:%s", name, want.mcpURL, want.mcpTokenEnv))
	sum.updated(Change{Install: in.Slug, Kind: "mcpServer", Field: name, From: from, To: want.mcpURL + " token:" + want.mcpTokenEnv})
}

// reconcileTriggers ensures all required triggers exist. Like the old loop, it
// only creates what is missing: a trigger is created by the install and a
// deleted one is drift back, but a trigger with changed filters is a trigger
// the wizard's edit flow replaces, not this pass.
func (e Engine) reconcileTriggers(ctx context.Context, in *setup.Install, sum *Summary) {
	// List existing triggers
	existing, err := e.Hub.ListTriggers(ctx, in.ConnectorID)
	if err != nil {
		e.log(in.Slug, fmt.Sprintf("read triggers: %v: triggers left alone", err))
		return
	}

	// Generate the wanted trigger set
	wanted := setup.Triggers(*in)

	// Find missing triggers
	missing := setup.Missing(wanted, existing)

	// Create each missing trigger
	for _, t := range missing {
		if _, err := e.Hub.CreateTrigger(ctx, in.GroupID, t); err != nil {
			e.log(in.Slug, fmt.Sprintf("refused trigger %s: %v", t.Name, err))
			sum.refused(Refusal{Install: in.Slug, Kind: "trigger", Field: t.Name, Error: err.Error()})
			continue
		}
		e.log(in.Slug, fmt.Sprintf("created trigger %s", t.Name))
		sum.updated(Change{Install: in.Slug, Kind: "trigger", Field: t.Name, To: "created"})
	}
}

// reconcileCronTriggers ensures the install's scheduled prompts exist — the
// developer's PR patrol. Discovery is by name, like the event triggers' by filter
// set: a pass creates what is missing and reports no drift on one that is there,
// whatever an admin has since retuned on it.
func (e Engine) reconcileCronTriggers(ctx context.Context, in *setup.Install, sum *Summary) {
	existing, err := e.Hub.ListCronTriggers(ctx)
	if err != nil {
		e.log(in.Slug, fmt.Sprintf("list cron triggers: %v", err))
		sum.refused(Refusal{Install: in.Slug, Kind: "trigger", Field: "cron", Error: err.Error()})
		return
	}

	for _, ct := range setup.CronMissing(setup.CronTriggers(*in), existing) {
		if _, err := e.Hub.CreateCronTrigger(ctx, in.GroupID, ct); err != nil {
			e.log(in.Slug, fmt.Sprintf("refused cron trigger %s: %v", ct.Name, err))
			sum.refused(Refusal{Install: in.Slug, Kind: "trigger", Field: ct.Name, Error: err.Error()})
			continue
		}
		e.log(in.Slug, fmt.Sprintf("created cron trigger %s", ct.Name))
		sum.updated(Change{Install: in.Slug, Kind: "trigger", Field: ct.Name, To: "created"})
	}
}

// reconcileCoverage reads new events since the last pass and folds them into
// coverage. It reports the earliest event folded in, or nil when it folded in
// none: that first event is what proves the hook reaches the connector, and a
// row nobody verified on S7 is still unverified until some pass reads one.
func (e Engine) reconcileCoverage(ctx context.Context, in *setup.Install) (*time.Time, error) {
	if in.Coverage == nil {
		in.Coverage = make(map[string]time.Time)
	}

	// Read from where the last pass stopped. A row written before the loop kept a cursor
	// says nothing, and the fallback is the old reading: the latest first-seen time, or
	// the epoch when no family has been seen yet.
	var since time.Time
	if in.EventsReadUntil != nil {
		since = *in.EventsReadUntil
	} else {
		for _, t := range in.Coverage {
			if t.After(since) {
				since = t
			}
		}
	}

	// Read new events
	events, err := e.Hub.Events(ctx, in.ConnectorID, since)
	if err != nil {
		return nil, err
	}

	// Fold events into coverage by type family, and leave the cursor on the newest event
	// seen: the hub can answer with an event the cursor already covers, and the cursor
	// never moves backwards over it. No events leaves the cursor where it stands.
	var first *time.Time
	for _, e := range events {
		family := eventFamily(e.Type)
		// Only update if this is the first (earliest) event in this family,
		// or if the new event is earlier
		if seen, ok := in.Coverage[family]; !ok || e.At.Before(seen) {
			in.Coverage[family] = e.At
		}
		if in.EventsReadUntil == nil || e.At.After(*in.EventsReadUntil) {
			at := e.At
			in.EventsReadUntil = &at
		}
		if first == nil || e.At.Before(*first) {
			at := e.At
			first = &at
		}
	}

	return first, nil
}

// eventFamily maps an event type to its family for coverage tracking.
// Each event type returns its own family: "issues" → "issues", "issue_comment" → "issue_comment", pull_request* → "pull_request".
func eventFamily(eventType string) string {
	switch {
	case eventType == "issues" || eventType == "issue_comment":
		if eventType == "issues" {
			return "issues"
		}
		return "issue_comment"
	case strings.HasPrefix(eventType, "pull_request"):
		return "pull_request"
	default:
		return eventType
	}
}

// coverageSnapshot copies what a row's coverage said before a pass worked on
// it. No map is no coverage at all, and that is a state of its own rather than
// the same thing as an empty one.
func coverageSnapshot(coverage map[string]time.Time) map[string]time.Time {
	if coverage == nil {
		return nil
	}
	snapshot := make(map[string]time.Time, len(coverage))
	for family, at := range coverage {
		snapshot[family] = at
	}
	return snapshot
}

// coverageChanged reports whether a pass left coverage saying more than the row
// did. A row with no coverage map at all gains one on its first pass — worth
// the write, so the row describes itself — and after that only a new or an
// earlier family earns one.
func coverageChanged(before, after map[string]time.Time) bool {
	if before == nil && after != nil {
		return true
	}
	if len(before) != len(after) {
		return true
	}
	for family, at := range after {
		if was, ok := before[family]; !ok || !was.Equal(at) {
			return true
		}
	}
	return false
}

// cursorSnapshot copies where a row's event cursor stood before a pass worked
// on it. No cursor is a state of its own rather than the same thing as one
// standing at the epoch.
func cursorSnapshot(cursor *time.Time) *time.Time {
	if cursor == nil {
		return nil
	}
	at := *cursor
	return &at
}

// cursorChanged reports whether a pass moved the event cursor: a pass that read
// events and learned nothing new about coverage still has to record where it
// stopped.
func cursorChanged(before, after *time.Time) bool {
	if before == nil || after == nil {
		return (before == nil) != (after == nil)
	}
	return !before.Equal(*after)
}

// llmSnapshot copies what the row said each role runs on, so "did the pass
// move the row's llm" is a comparison and not a memory. The fifth slot is the
// OpenAI auth method: a pass that moved a role onto the sign-in moved the row's
// statement about how that agent authenticates, and that earns a row write like
// any other move.
func llmSnapshot(roles map[setup.Role]setup.RoleState) map[setup.Role][5]string {
	out := make(map[setup.Role][5]string, len(roles))
	for role, st := range roles {
		out[role] = [5]string{st.LLMProvider, st.LLMModel, st.LLMCustomKeyHash, st.LLMKeyHashOf(st.LLMProvider), st.LLMOpenAIAuthMethod}
	}
	return out
}

// llmsMatch compares two snapshots by the five each role names: provider,
// model, the custom key's fingerprint, the fingerprint of the key of whatever
// provider the row says the role runs on, and the OpenAI auth method — a pass
// that moved any of them earned a row write like one that moved the model.
func llmsMatch(before, after map[setup.Role][5]string) bool {
	if len(before) != len(after) {
		return false
	}
	for role, pair := range after {
		if was, ok := before[role]; !ok || was != pair {
			return false
		}
	}
	return true
}

// setLLMKeyField points the update's key field for one provider at the value.
// A block the update leaves nil is absent from the request: "keep what the hub
// holds".
func setLLMKeyField(u *hub.AgentUpdate, provider string, key *string) {
	switch provider {
	case setup.LLMProviderCustom:
		u.CustomKey = key
	case setup.LLMProviderOpenAI:
		u.OpenAIKey = key
	case setup.LLMProviderAnthropic:
		u.AnthropicKey = key
	case setup.LLMProviderGoogle:
		u.GoogleKey = key
	}
}

// findInstall picks the row with this slug out of a freshly read list.
func findInstall(installs []setup.Install, slug string) (setup.Install, bool) {
	for _, in := range installs {
		if in.Slug == slug {
			return in, true
		}
	}
	return setup.Install{}, false
}

// sameNames reports whether two lists hold the same set of names, each at most
// once. Order is not a difference; a name listed twice is the set's mistake,
// and a set is what is wanted either way.
func sameNames(have, want []string) bool {
	if len(have) != len(want) {
		return false
	}
	seen := make(map[string]int, len(want))
	for _, n := range want {
		seen[n]++
	}
	for _, n := range have {
		seen[n]--
		if seen[n] < 0 {
			return false
		}
	}
	for _, n := range want {
		if seen[n] != 0 {
			return false
		}
	}
	return true
}
