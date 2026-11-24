package setup

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// The plan is the wizard's one decision function (R2), so these rows are the
// wizard's rules: what a clean install creates and in which order, what blocks,
// what only warns, and what an edit with blank secrets does. Every blocking row
// asserts the exact sentence, because the preview shows it to the admin as the
// reason and as the thing to fix.
const (
	testRuntime  = "registry.example/ainsel/agent:runtime"
	testAdminTok = "admin-token-not-to-be-echoed"
)

// testMCPURL is the settings' forge MCP endpoint: the install's own MCP server
// points at it, and the admin no longer types it anywhere (the settings store
// owns it).
const testMCPURL = "https://mcp.example/forgejo/mcp"

// testSettings is one reading of the settings store: every role runs on the same
// model, and the forge MCP server has its endpoint. The plan reads exactly this;
// the wizard collects nothing of it any more.
func testSettings() Settings {
	llm := map[Role]RoleLLM{}
	for _, r := range []Role{RoleRefiner, RoleDeveloper, RoleReviewer} {
		llm[r] = RoleLLM{Provider: "ollama-cloud", Model: "glm-4.7"}
	}
	return Settings{
		LLM:             llm,
		MCPURL:          testMCPURL,
		MCPTokenFromEnv: TokenEnvName,
	}
}

// fakeDefaults stands in for content.DefaultTools. The plan cannot import
// content (content imports setup), so the caller injects the profile data; these
// are the tools every role's fake set carries — the persona-critical ones
// (personaTools) included, so a clean install plans without warnings.
func fakeDefaults(Role) []string {
	return []string{
		"get_issue_by_index", "create_issue_comment",
		"get_file_content", "get_pull_request_status", "get_commit_statuses",
	}
}

// withoutTool is a tool set minus one tool: everything else the persona relies on
// stays, so the warning or diff under test is the only difference.
func withoutTool(tools []string, gone string) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		if t != gone {
			out = append(out, t)
		}
	}
	return out
}

// fakeCheckTools stands in for content.CheckTools: the same R7 rule, reduced to
// the one tool this file needs to see blocked.
func fakeCheckTools(r Role, tools []string) error {
	if r != RoleDeveloper && slices.Contains(tools, "bash") {
		return fmt.Errorf("role %q may not use: bash", r)
	}
	return nil
}

// baseInput is a complete new install: everything S1 through S5 collects — which
// is no longer the llms or the MCP URL: those are the settings' (cleanSnapshot
// carries the reading).
func baseInput() Input {
	roles := map[Role]RoleInput{}
	for _, r := range []Role{RoleRefiner, RoleDeveloper, RoleReviewer} {
		roles[r] = RoleInput{Runtime: testRuntime}
	}
	return Input{
		Slug:       "apps",
		GroupID:    "g-apps",
		BaseURL:    "https://forgejo.example",
		Org:        "acme",
		Roles:      roles,
		AdminToken: testAdminTok,
	}
}

// cleanSnapshot is an instance with nothing of this install's names in it yet,
// the group granted both to the caller and to the plugin, a verified admin token
// and an existing org.
func cleanSnapshot() Snapshot {
	return Snapshot{
		CallerGroups: []string{"g-apps"},
		PluginGroups: []string{"g-apps"},
		AdminChecked: true,
		AdminIsAdmin: true,
		OrgExists:    true,
		DefaultTools: fakeDefaults,
		CheckTools:   fakeCheckTools,
		Settings:     testSettings(),
	}
}

func planInput(t *testing.T) (Input, Snapshot) {
	t.Helper()
	return baseInput(), cleanSnapshot()
}

// TestPlanNewInstall is the happy path: a clean instance gets the whole trio, in
// the order the wizard applies it, named as the constraints name it.
func TestPlanNewInstall(t *testing.T) {
	in, snap := planInput(t)
	got := Plan(in, snap)

	if len(got.Verdict.Blocking) != 0 {
		t.Fatalf("blocking = %q, want none", got.Verdict.Blocking)
	}
	if len(got.Verdict.Warnings) != 0 {
		t.Errorf("warnings = %q, want none", got.Verdict.Warnings)
	}

	want := []struct{ kind, name string }{
		{KindUser, "refiner-apps"},
		{KindUser, "developer-apps"},
		{KindUser, "reviewer-apps"},
		{KindTeam, "ainsel-refiner-apps"},
		{KindTeam, "ainsel-developer-apps"},
		{KindTeam, "ainsel-reviewer-apps"},
		{KindMCPServer, "forgejo-mcp-apps"},
		{KindPersona, "forgejo-refiner-apps"},
		{KindPersona, "forgejo-developer-apps"},
		{KindPersona, "forgejo-reviewer-apps"},
		{KindImage, "forgejo-refiner-apps"},
		{KindImage, "forgejo-developer-apps"},
		{KindImage, "forgejo-reviewer-apps"},
		{KindAgent, "forgejo-refiner-apps"},
		{KindAgent, "forgejo-developer-apps"},
		{KindAgent, "forgejo-reviewer-apps"},
		{KindConnector, "forgejo-apps"},
		{KindTrigger, "forgejo-refiner-issue-opened-apps"},
		{KindTrigger, "forgejo-refiner-issue-reopened-apps"},
		{KindTrigger, "forgejo-refiner-mention-apps"},
		{KindTrigger, "forgejo-reviewer-pr-opened-apps"},
		{KindTrigger, "forgejo-reviewer-mention-apps"},
		{KindTrigger, "forgejo-developer-mention-apps"},
		{KindCronTrigger, "forgejo-developer-pr-patrol-apps"},
		{KindHook, "acme"},
	}
	if len(got.Create) != len(want) {
		t.Fatalf("create = %d ops, want %d:\n%s", len(got.Create), len(want), opsString(got.Create))
	}
	for i, w := range want {
		if got.Create[i].Kind != w.kind || got.Create[i].Name != w.name {
			t.Errorf("create[%d] = %s %q, want %s %q", i, got.Create[i].Kind, got.Create[i].Name, w.kind, w.name)
		}
	}
	if len(got.Change) != 0 {
		t.Errorf("change = %s, want none on a new install", opsString(got.Change))
	}
	if len(got.Delete) != 0 {
		t.Errorf("delete = %s, want none", opsString(got.Delete))
	}

	// The Detail of the ops the admin reads in the preview.
	details := map[string]string{}
	for _, op := range got.Create {
		details[op.Kind+"/"+op.Name] = op.Detail
	}
	for _, c := range []struct{ key, want string }{
		{"user/refiner-apps", "AInsel Refiner (apps)"},
		{"team/ainsel-refiner-apps", "repo.code=read, repo.issues=write, repo.pulls=read; all repositories; member refiner-apps"},
		{"team/ainsel-developer-apps", "repo.code=write, repo.issues=write, repo.pulls=write; all repositories; member developer-apps"},
		{"team/ainsel-reviewer-apps", "repo.code=read, repo.issues=write, repo.pulls=write; all repositories; member reviewer-apps"},
		{"mcpServer/forgejo-mcp-apps", "https://mcp.example/forgejo/mcp; tokenFromEnv FORGEJO_TOKEN"},
		{"image/forgejo-refiner-apps", "runtime " + testRuntime + "; mcpServers forgejo-mcp-apps, plugin-forgejo; skill forgejo-forgejo; " + fmt.Sprintf("%d tools", len(fakeDefaults(RoleRefiner))) + "; turn timeout " + DefaultTurnTimeoutMs(RoleRefiner) + "ms (60 min)"},
		{"agent/forgejo-refiner-apps", "image forgejo-refiner-apps; persona forgejo-refiner-apps; llm ollama-cloud/glm-4.7"},
		{"connector/forgejo-apps", "signatureHeader X-Forgejo-Signature"},
		{"trigger/forgejo-refiner-issue-opened-apps", "type eq issues; action eq opened; issue.labels.name not-contains do-not-refine"},
		{"cronTrigger/forgejo-developer-pr-patrol-apps", "schedule */30 * * * *; Scheduled PR patrol."},
		{"hook/acme", "issues, issue_comment, pull_request, pull_request_review"},
	} {
		if got := details[c.key]; got != c.want {
			t.Errorf("detail %s = %q, want %q", c.key, got, c.want)
		}
	}

	// The trigger set comes along with the ops, so the preview and the apply read
	// the same six. On a new install the agent and connector ids do not exist
	// yet, so the refs are empty; apply fills them after the creates.
	wantTriggers := []string{
		"forgejo-refiner-issue-opened-apps",
		"forgejo-refiner-issue-reopened-apps",
		"forgejo-refiner-mention-apps",
		"forgejo-reviewer-pr-opened-apps",
		"forgejo-reviewer-mention-apps",
		"forgejo-developer-mention-apps",
	}
	if len(got.Triggers) != len(wantTriggers) {
		t.Fatalf("triggers = %d, want %d", len(got.Triggers), len(wantTriggers))
	}
	for i, name := range wantTriggers {
		if got.Triggers[i].Name != name {
			t.Errorf("triggers[%d] = %q, want %q", i, got.Triggers[i].Name, name)
		}
		if got.Triggers[i].AgentRef != "" || got.Triggers[i].ConnectorRef != "" {
			t.Errorf("trigger %q refs = %q/%q, want both empty before the resources exist",
				name, got.Triggers[i].AgentRef, got.Triggers[i].ConnectorRef)
		}
		if got.Triggers[i].Filters == nil {
			t.Errorf("trigger %q has no filters", name)
		}
	}

	// The cron trigger comes along with the ops too. On a new install the
	// developer's agent id does not exist yet, so its ref is empty; apply fills it
	// after the creates, exactly as with the event triggers.
	if len(got.CronTriggers) != 1 {
		t.Fatalf("cron triggers = %d, want 1", len(got.CronTriggers))
	}
	ct := got.CronTriggers[0]
	if ct.Name != "forgejo-developer-pr-patrol-apps" || ct.AgentRef != "" ||
		ct.Schedule != "*/30 * * * *" || !ct.Enabled {
		t.Errorf("cron trigger = %+v, want the patrol named, scheduled, and with an empty ref before the agent exists", ct)
	}

	// R4: the plan's output is shown to the admin, so it may not carry the admin
	// token it was handed. There is no key to hand any more.
	blob := fmt.Sprintf("%+v", got)
	for _, secret := range []string{testAdminTok} {
		if strings.Contains(blob, secret) {
			t.Errorf("plan output echoes a secret (%s) in %s", secret, blob)
		}
	}
}

// TestPlanNewInstallUsesDefaultTools pins that an input naming no tools gets the
// defaults: the image op counts the injected default set, not zero.
func TestPlanNewInstallUsesDefaultTools(t *testing.T) {
	in, snap := planInput(t)
	in.Roles[RoleRefiner] = RoleInput{Runtime: testRuntime}
	got := Plan(in, snap)
	if len(got.Verdict.Blocking) != 0 {
		t.Fatalf("blocking = %q, want none", got.Verdict.Blocking)
	}
	for _, op := range got.Create {
		if op.Kind == KindImage && op.Name == "forgejo-refiner-apps" {
			if want := fmt.Sprintf("%d tools; turn timeout %sms (60 min)", len(fakeDefaults(RoleRefiner)), DefaultTurnTimeoutMs(RoleRefiner)); !strings.HasSuffix(op.Detail, want) {
				t.Errorf("image detail = %q, want the default tool count %q", op.Detail, want)
			}
			return
		}
	}
	t.Error("no image op for the refiner")
}

// TestPlanBlocking is one row per blocking rule. Each row starts from the clean
// new install and breaks exactly one thing, so exactly one sentence comes back.
func TestPlanBlocking(t *testing.T) {
	tests := []struct {
		name  string
		input func(*Input)
		snap  func(*Snapshot)
		want  string
	}{
		{
			name:  "invalid slug",
			input: func(in *Input) { in.Slug = "Apps" },
			want:  `slug "Apps" is invalid: it must match ^[a-z][a-z0-9-]{0,29}[a-z0-9]$`,
		},
		{
			name: "slug taken",
			snap: func(s *Snapshot) { s.TakenNames = map[string]bool{"slug:apps": true} },
			want: `slug "apps" is already used by another install`,
		},
		{
			name:  "no group selected",
			input: func(in *Input) { in.GroupID = "" },
			want:  `no AInsel group is selected for the install`,
		},
		{
			name: "group not writable",
			snap: func(s *Snapshot) { s.CallerGroups = []string{"g-other"} },
			want: `group "g-apps" is not writable: pick a group where you are an owner or a writer`,
		},
		{
			name: "group not granted to the plugin",
			snap: func(s *Snapshot) { s.PluginGroups = []string{"g-other"} },
			want: `group "g-apps" is not granted to the plugin: an admin has to grant it in Plugins → Forgejo → Group Access in the AInsel console (or set_plugin_group_access)`,
		},
		{
			name:  "missing admin token on a new install",
			input: func(in *Input) { in.AdminToken = "" },
			want:  `a Forgejo admin token is required to create the install's bot accounts`,
		},
		{
			name: "token is not an admin",
			snap: func(s *Snapshot) { s.AdminIsAdmin = false },
			want: `the Forgejo admin token belongs to a user who is not a site administrator`,
		},
		{
			name: "org missing",
			snap: func(s *Snapshot) { s.OrgExists = false },
			want: `the Forgejo organisation "acme" does not exist`,
		},
		{
			name: "bot login taken",
			snap: func(s *Snapshot) { s.TakenNames = map[string]bool{"login:developer-apps": true} },
			want: `login "developer-apps" is already taken: an install only creates its own resources — the name matches this plugin's convention for slug "apps", so a previous install may have left it behind: delete it in Forgejo as an administrator, or choose another slug.`,
		},
		{
			name: "team name taken",
			snap: func(s *Snapshot) { s.TakenNames = map[string]bool{"team:ainsel-refiner-apps": true} },
			want: `team "ainsel-refiner-apps" is already taken: an install only creates its own resources — the name matches this plugin's convention for slug "apps", so a previous install may have left it behind: delete it in Forgejo as an administrator, or choose another slug.`,
		},
		{
			name: "image name taken",
			snap: func(s *Snapshot) { s.TakenNames = map[string]bool{"image:forgejo-refiner-apps": true} },
			want: `image "forgejo-refiner-apps" is already taken: an install only creates its own resources — the name matches this plugin's convention for slug "apps", so a previous install may have left it behind: delete it in the hub, or choose another slug.`,
		},
		{
			name: "mcp server name taken",
			snap: func(s *Snapshot) { s.TakenNames = map[string]bool{"mcp:forgejo-mcp-apps": true} },
			want: `MCP server "forgejo-mcp-apps" is already taken: an install only creates its own resources — the name matches this plugin's convention for slug "apps", so a previous install may have left it behind: delete it in the hub, or choose another slug.`,
		},
		{
			name: "cron trigger name taken",
			snap: func(s *Snapshot) {
				s.TakenNames = map[string]bool{"cronTrigger:forgejo-developer-pr-patrol-apps": true}
			},
			want: `cronTrigger "forgejo-developer-pr-patrol-apps" is already taken: an install only creates its own resources — the name matches this plugin's convention for slug "apps", so a previous install may have left it behind: delete it in the hub, or choose another slug.`,
		},
		{
			name: "agent default name taken",
			snap: func(s *Snapshot) { s.TakenNames = map[string]bool{"agent:forgejo-reviewer-apps": true} },
			want: `agent "forgejo-reviewer-apps" is already taken: an install only creates its own resources — the name matches this plugin's convention for slug "apps", so a previous install may have left it behind: delete it in the hub, or choose another slug.`,
		},
		{
			// A leftover from the pre-managed-agents build: the admin re-entered the
			// old display name, and the legacy <role>-<slug> shape still earns the hint.
			name: "legacy agent name taken",
			input: func(in *Input) {
				r := RoleRefiner
				ri := in.Roles[r]
				ri.AgentName = "refiner-apps"
				in.Roles[r] = ri
			},
			snap: func(s *Snapshot) { s.TakenNames = map[string]bool{"agent:refiner-apps": true} },
			want: `agent "refiner-apps" is already taken: an install only creates its own resources — the name matches this plugin's convention for slug "apps", so a previous install may have left it behind: delete it in the hub, or choose another slug.`,
		},
		{
			// The negative case: a name matching no convention shape keeps the plain
			// message — the hint must not claim provenance it cannot back up.
			name: "custom agent name taken",
			input: func(in *Input) {
				r := RoleRefiner
				ri := in.Roles[r]
				ri.AgentName = "Team Refiner"
				in.Roles[r] = ri
			},
			snap: func(s *Snapshot) { s.TakenNames = map[string]bool{"agent:Team Refiner": true} },
			want: `agent "Team Refiner" is already taken: an install only creates its own resources`,
		},
		{
			name: "guard rail violated",
			input: func(in *Input) {
				r := RoleRefiner
				ri := in.Roles[r]
				ri.Tools = []string{"get_issue_by_index", "bash"}
				in.Roles[r] = ri
			},
			want: `role "refiner" may not use: bash`,
		},
		{
			name: "no runtime for a role",
			input: func(in *Input) {
				r := RoleDeveloper
				ri := in.Roles[r]
				ri.Runtime = ""
				in.Roles[r] = ri
			},
			want: `role "developer": no runtime container is set`,
		},
		{
			// A ref with neither tag nor digest is a floating one: the hub pulls
			// `latest`, which may not exist — and the tasks then sit at QUEUED while
			// every pod fails at pull. A new install must not be created with one.
			name: "runtime without a tag or digest",
			input: func(in *Input) {
				r := RoleDeveloper
				ri := in.Roles[r]
				ri.Runtime = "acme/ainsel-pi-maui"
				in.Roles[r] = ri
			},
			want: `role "developer": runtime acme/ainsel-pi-maui has no tag or digest — add a tag such as :8.0, or a @sha256: digest`,
		},
		{
			name: "role without a model in the settings",
			snap: func(s *Snapshot) {
				st := s.Settings
				st.LLM[RoleReviewer] = RoleLLM{}
				s.Settings = st
			},
			want: `role "reviewer": no llm is set — the plugin's Settings answer nothing for this role`,
		},
		{
			name: "forge MCP server without a URL",
			snap: func(s *Snapshot) {
				st := s.Settings
				st.MCPURL = ""
				s.Settings = st
			},
			want: `the forge MCP server needs its endpoint URL: set it in the plugin's Settings`,
		},
		{
			name: "two roles, one agent name",
			input: func(in *Input) {
				r := RoleDeveloper
				ri := in.Roles[r]
				ri.AgentName = "forgejo-refiner-apps"
				in.Roles[r] = ri
			},
			want: `roles "refiner" and "developer" would both create the agent "forgejo-refiner-apps": every agent needs its own name`,
		},
		{
			name: "DefaultTools not set",
			snap: func(s *Snapshot) { s.DefaultTools = nil },
			want: `internal: role tool checks are not configured`,
		},
		{
			name: "CheckTools not set",
			snap: func(s *Snapshot) { s.CheckTools = nil },
			want: `internal: role tool checks are not configured`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, snap := planInput(t)
			if tt.input != nil {
				tt.input(&in)
			}
			if tt.snap != nil {
				tt.snap(&snap)
			}
			got := Plan(in, snap)
			if len(got.Verdict.Blocking) != 1 {
				t.Fatalf("blocking = %q, want exactly %q", got.Verdict.Blocking, tt.want)
			}
			if got.Verdict.Blocking[0] != tt.want {
				t.Errorf("blocking = %q, want %q", got.Verdict.Blocking[0], tt.want)
			}
		})
	}
}

// TestPlanWarningNamesTheRemovedTool pins the warning the spec names: a tool the
// persona relies on, switched off, warns instead of blocking. The set keeps every
// other persona tool so the warning under test stands alone.
func TestPlanWarningNamesTheRemovedTool(t *testing.T) {
	for _, r := range []Role{RoleRefiner, RoleDeveloper, RoleReviewer} {
		t.Run(string(r), func(t *testing.T) {
			in, snap := planInput(t)
			in.Roles[r] = RoleInput{
				Runtime: testRuntime,
				Tools:   withoutTool(fakeDefaults(r), "create_issue_comment"),
			}
			got := Plan(in, snap)
			want := fmt.Sprintf("role %q: create_issue_comment is off, but the persona relies on it", r)
			if len(got.Verdict.Warnings) != 1 || got.Verdict.Warnings[0] != want {
				t.Errorf("warnings = %q, want exactly [%q]", got.Verdict.Warnings, want)
			}
			if len(got.Verdict.Blocking) != 0 {
				t.Errorf("blocking = %q, want none: a removed tool is a warning", got.Verdict.Blocking)
			}
		})
	}
}

// editBaseline is the edit of the same install with nothing changed and no
// secret typed: the case that must neither block nor change anything.
func editBaseline() (Input, Snapshot) {
	in := baseInput()
	in.AdminToken = ""
	for r := range in.Roles {
		in.Roles[r] = RoleInput{
			Runtime: testRuntime,
			Tools:   slices.Clone(fakeDefaults(r)),
		}
	}
	snap := cleanSnapshot()
	snap.Existing = existingInstall()
	snap.Prior = map[Role]PriorState{}
	for _, r := range []Role{RoleRefiner, RoleDeveloper, RoleReviewer} {
		snap.Prior[r] = PriorState{
			Runtime:  testRuntime,
			Provider: "ollama-cloud",
			Model:    "glm-4.7",
		}
	}
	// The install's own resources exist, so a caller that lists names honestly
	// reports them as taken. An edit must not read that as a conflict.
	snap.TakenNames = map[string]bool{}
	for _, r := range []Role{RoleRefiner, RoleDeveloper, RoleReviewer} {
		snap.TakenNames["login:"+BotLogin(r, "apps")] = true
		snap.TakenNames["team:"+TeamName(r, "apps")] = true
		snap.TakenNames["image:"+ImageName(r, "apps")] = true
		snap.TakenNames["persona:"+PersonaName(r, "apps")] = true
		snap.TakenNames["slug:apps"] = true
	}
	return in, snap
}

func existingInstall() *Install {
	roles := map[Role]RoleState{}
	for i, r := range []Role{RoleRefiner, RoleDeveloper, RoleReviewer} {
		roles[r] = RoleState{
			AgentID:   "a-" + string(r),
			ImageID:   "ai-" + string(r),
			PersonaID: "p-" + string(r),
			BotLogin:  BotLogin(r, "apps"),
			TeamID:    int64(11 + i),
			Tools:     slices.Clone(fakeDefaults(r)),
		}
	}
	return &Install{
		Slug:         "apps",
		GroupID:      "g-apps",
		BaseURL:      "https://forgejo.example",
		Org:          "acme",
		ConnectorID:  "c-724d",
		MCPServer:    "forgejo-mcp-apps",
		HookID:       7,
		ExcludeLabel: "do-not-refine",
		Roles:        roles,
	}
}

// TestPlanEditWithBlankSecretsChangesNothing is edit install with nothing but
// blanks typed in: the stored key is kept, so there is neither a block nor a
// change, and nothing is created either.
func TestPlanEditWithBlankSecretsChangesNothing(t *testing.T) {
	in, snap := editBaseline()
	got := Plan(in, snap)

	if len(got.Verdict.Blocking) != 0 {
		t.Errorf("blocking = %q, want none", got.Verdict.Blocking)
	}
	if len(got.Change) != 0 {
		t.Errorf("change = %s, want none", opsString(got.Change))
	}
	if len(got.Create) != 0 {
		t.Errorf("create = %s, want none: the install already exists and is connected", opsString(got.Create))
	}
	// On an edit the ids are known, so the trigger set is complete.
	if got.Triggers[0].AgentRef != "a-refiner" || got.Triggers[0].ConnectorRef != "c-724d" {
		t.Errorf("triggers[0] refs = %q/%q, want a-refiner/c-724d",
			got.Triggers[0].AgentRef, got.Triggers[0].ConnectorRef)
	}
}

// TestPlanEditSettingsChangeMovesTheAgent pins that the agent's model follows the
// settings: the admin changed the role's llm in the plugin's Settings, and the
// edit — which types nothing about the llm — reports the agent's provider and
// model moving. The same diff is what corrects an install whose settings changed
// after it was created.
func TestPlanEditSettingsChangeMovesTheAgent(t *testing.T) {
	t.Run("provider", func(t *testing.T) {
		in, snap := editBaseline()
		snap.Settings.LLM[RoleReviewer] = RoleLLM{Provider: "custom", Model: "glm-4.7"}
		got := Plan(in, snap)
		if len(got.Verdict.Blocking) != 0 {
			t.Fatalf("blocking = %q, want none", got.Verdict.Blocking)
		}
		want := Op{Kind: KindAgent, Name: "forgejo-reviewer-apps", Detail: `llm provider: "ollama-cloud" -> "custom"`, Role: RoleReviewer, Field: FieldLLMProvider}
		if !slices.Contains(got.Change, want) {
			t.Errorf("change = %s, want it to contain %s", opsString(got.Change), want)
		}
	})

	t.Run("model", func(t *testing.T) {
		in, snap := editBaseline()
		snap.Settings.LLM[RoleDeveloper] = RoleLLM{Provider: "ollama-cloud", Model: "glm-5"}
		got := Plan(in, snap)
		want := Op{Kind: KindAgent, Name: "forgejo-developer-apps", Detail: `llm model: "glm-4.7" -> "glm-5"`, Role: RoleDeveloper, Field: FieldLLMModel}
		if !slices.Contains(got.Change, want) {
			t.Errorf("change = %s, want it to contain %s", opsString(got.Change), want)
		}
	})

	// The custom endpoint's URL is a diffed field like the provider and model:
	// the prior state reads it off the agent, so a moved URL previews as a
	// change to that role's agent. The key cannot be read back, so its drift is
	// seen through the row's fingerprint instead — see the key subtests below.
	t.Run("custom URL", func(t *testing.T) {
		in, snap := editBaseline()
		snap.Prior[RoleReviewer] = PriorState{
			Runtime: testRuntime, Provider: "custom", Model: "glm-4.7", CustomURL: "https://old.example/v1",
		}
		snap.Settings.LLM[RoleReviewer] = RoleLLM{
			Provider: "custom", Model: "glm-4.7", CustomURL: "https://new.example/v1",
		}
		got := Plan(in, snap)
		want := Op{Kind: KindAgent, Name: "forgejo-reviewer-apps", Detail: `llm URL: "https://old.example/v1" -> "https://new.example/v1"`, Role: RoleReviewer, Field: FieldLLMCustomUrl}
		if !slices.Contains(got.Change, want) {
			t.Errorf("change = %s, want it to contain %s", opsString(got.Change), want)
		}
	})

	// The key's drift is seen through the row's fingerprint: the hub cannot
	// read a key back, so the plan compares the settings' key hashed against
	// what the install row recorded as last written. The detail names the
	// change without spelling the key.
	t.Run("custom key first set", func(t *testing.T) {
		in, snap := editBaseline()
		snap.Prior[RoleReviewer] = PriorState{
			Runtime: testRuntime, Provider: "custom", Model: "glm-4.7", CustomURL: "https://llm.example/v1",
		}
		snap.Settings.LLM[RoleReviewer] = RoleLLM{
			Provider: "custom", Model: "glm-4.7", CustomURL: "https://llm.example/v1", CustomKey: "new-provider-key",
		}
		got := Plan(in, snap)
		want := Op{Kind: KindAgent, Name: "forgejo-reviewer-apps", Detail: `llm key (custom): "none" -> "set"`, Role: RoleReviewer, Field: FieldLLMCustomKey}
		if !slices.Contains(got.Change, want) {
			t.Errorf("change = %s, want it to contain %s", opsString(got.Change), want)
		}
		for _, op := range got.Change {
			if strings.Contains(op.Detail, "new-provider-key") {
				t.Errorf("op detail %q spells the key", op.Detail)
			}
		}
	})

	t.Run("custom key replaced", func(t *testing.T) {
		in, snap := editBaseline()
		snap.Prior[RoleReviewer] = PriorState{
			Runtime: testRuntime, Provider: "custom", Model: "glm-4.7", CustomURL: "https://llm.example/v1",
			CustomKeyHash: CustomKeyFingerprint("old-provider-key"),
		}
		snap.Settings.LLM[RoleReviewer] = RoleLLM{
			Provider: "custom", Model: "glm-4.7", CustomURL: "https://llm.example/v1", CustomKey: "new-provider-key",
		}
		got := Plan(in, snap)
		want := Op{Kind: KindAgent, Name: "forgejo-reviewer-apps", Detail: "llm key (custom) replaced", Role: RoleReviewer, Field: FieldLLMCustomKey}
		if !slices.Contains(got.Change, want) {
			t.Errorf("change = %s, want it to contain %s", opsString(got.Change), want)
		}
	})

	t.Run("custom key already recorded changes nothing", func(t *testing.T) {
		in, snap := editBaseline()
		snap.Prior[RoleReviewer] = PriorState{
			Runtime: testRuntime, Provider: "custom", Model: "glm-4.7", CustomURL: "https://llm.example/v1",
			CustomKeyHash: CustomKeyFingerprint("new-provider-key"),
		}
		snap.Settings.LLM[RoleReviewer] = RoleLLM{
			Provider: "custom", Model: "glm-4.7", CustomURL: "https://llm.example/v1", CustomKey: "new-provider-key",
		}
		got := Plan(in, snap)
		for _, op := range got.Change {
			if op.Field == FieldLLMCustomKey || op.Field == FieldLLMCustomUrl {
				t.Errorf("change = %s, want no custom op when the row already records the key", opsString(got.Change))
			}
		}
	})

	t.Run("custom key away from custom changes nothing", func(t *testing.T) {
		in, snap := editBaseline()
		// A listed provider with a stale pair left in the settings: the pair is
		// inert, and diffing it would preview a write that changes nothing.
		snap.Settings.LLM[RoleReviewer] = RoleLLM{
			Provider: "ollama-cloud", Model: "glm-4.7", CustomURL: "https://stale.example/v1", CustomKey: "sk-stale",
		}
		got := Plan(in, snap)
		for _, op := range got.Change {
			if op.Field == FieldLLMCustomKey || op.Field == FieldLLMCustomUrl {
				t.Errorf("change = %s, want no custom op away from the custom provider", opsString(got.Change))
			}
		}
	})

	t.Run("empty custom key says nothing", func(t *testing.T) {
		in, snap := editBaseline()
		// An empty settings key is no statement — the hub keeps what it holds
		// on an empty write — so it previews no change, even against a row that
		// records a different key.
		snap.Prior[RoleReviewer] = PriorState{
			Runtime: testRuntime, Provider: "custom", Model: "glm-4.7", CustomURL: "https://llm.example/v1",
			CustomKeyHash: CustomKeyFingerprint("sk-stored"),
		}
		snap.Settings.LLM[RoleReviewer] = RoleLLM{
			Provider: "custom", Model: "glm-4.7", CustomURL: "https://llm.example/v1",
		}
		got := Plan(in, snap)
		for _, op := range got.Change {
			if op.Field == FieldLLMCustomKey {
				t.Errorf("change = %s, want no key op from an empty setting", opsString(got.Change))
			}
		}
	})

	// The three vendor keys are diffed the same way: the hub cannot read one
	// back, so the plan compares the settings' key hashed against the fingerprint
	// the row recorded for the provider the role runs on.
	t.Run("vendor key first set", func(t *testing.T) {
		in, snap := editBaseline()
		snap.Prior[RoleReviewer] = PriorState{
			Runtime: testRuntime, Provider: LLMProviderOpenAI, Model: "gpt-5.2",
		}
		snap.Settings.LLM[RoleReviewer] = RoleLLM{
			Provider: LLMProviderOpenAI, Model: "gpt-5.2", OpenAIKey: "new-provider-key",
		}
		got := Plan(in, snap)
		want := Op{Kind: KindAgent, Name: "forgejo-reviewer-apps", Detail: `llm key (openai): "none" -> "set"`, Role: RoleReviewer, Field: FieldLLMOpenAIKey}
		if !slices.Contains(got.Change, want) {
			t.Errorf("change = %s, want it to contain %s", opsString(got.Change), want)
		}
		for _, op := range got.Change {
			if strings.Contains(op.Detail, "new-provider-key") {
				t.Errorf("op detail %q spells the key", op.Detail)
			}
		}
	})

	t.Run("vendor key replaced", func(t *testing.T) {
		in, snap := editBaseline()
		snap.Prior[RoleReviewer] = PriorState{
			Runtime: testRuntime, Provider: LLMProviderAnthropic, Model: "claude",
			AnthropicKeyHash: CustomKeyFingerprint("old-provider-key"),
		}
		snap.Settings.LLM[RoleReviewer] = RoleLLM{
			Provider: LLMProviderAnthropic, Model: "claude", AnthropicKey: "new-provider-key",
		}
		got := Plan(in, snap)
		want := Op{Kind: KindAgent, Name: "forgejo-reviewer-apps", Detail: "llm key (anthropic) replaced", Role: RoleReviewer, Field: FieldLLMAnthropicKey}
		if !slices.Contains(got.Change, want) {
			t.Errorf("change = %s, want it to contain %s", opsString(got.Change), want)
		}
	})

	t.Run("vendor key already recorded changes nothing", func(t *testing.T) {
		in, snap := editBaseline()
		snap.Prior[RoleReviewer] = PriorState{
			Runtime: testRuntime, Provider: LLMProviderGoogle, Model: "gemini",
			GoogleKeyHash: CustomKeyFingerprint("sk-same"),
		}
		snap.Settings.LLM[RoleReviewer] = RoleLLM{
			Provider: LLMProviderGoogle, Model: "gemini", GoogleKey: "sk-same",
		}
		got := Plan(in, snap)
		for _, op := range got.Change {
			if op.Field == FieldLLMGoogleKey {
				t.Errorf("change = %s, want no key op when the row already records the key", opsString(got.Change))
			}
		}
	})

	t.Run("vendor key of another provider changes nothing", func(t *testing.T) {
		in, snap := editBaseline()
		// The role runs on OpenAI; the settings also hold an Anthropic key nobody
		// asked the agent to carry. Its block is inert while the llm names another
		// provider, so diffing it would preview a write that changes nothing.
		snap.Prior[RoleReviewer] = PriorState{
			Runtime: testRuntime, Provider: LLMProviderOpenAI, Model: "gpt-5.2",
			OpenAIKeyHash: CustomKeyFingerprint("sk-openai"),
		}
		snap.Settings.LLM[RoleReviewer] = RoleLLM{
			Provider: LLMProviderOpenAI, Model: "gpt-5.2", OpenAIKey: "sk-openai", AnthropicKey: "sk-stray",
		}
		got := Plan(in, snap)
		for _, op := range got.Change {
			if op.Field == FieldLLMAnthropicKey {
				t.Errorf("change = %s, want no anthropic op away from that provider", opsString(got.Change))
			}
		}
	})

	// A settings value that answers nothing must not silently erase the agent's
	// model: an empty diff side emits no change op. (A new install blocks on it
	// instead; see TestPlanBlocking.)
	t.Run("empty settings change nothing", func(t *testing.T) {
		in, snap := editBaseline()
		snap.Settings.LLM[RoleReviewer] = RoleLLM{}
		got := Plan(in, snap)
		for _, op := range got.Change {
			if op.Field == FieldLLMProvider || op.Field == FieldLLMModel {
				t.Errorf("change = %s, want no llm op from an empty setting", opsString(got.Change))
			}
		}
	})
}

// TestPlanEditShowsRemovedToolAndRuntimeOnTheImage is the preview's field-by-field
// diff: a tool taken off a set and a new container both surface as a change to
// that role's image.
func TestPlanEditShowsRemovedToolAndRuntimeOnTheImage(t *testing.T) {
	t.Run("a tool removed", func(t *testing.T) {
		in, snap := editBaseline()
		r := RoleReviewer
		ri := in.Roles[r]
		ri.Tools = withoutTool(fakeDefaults(r), "create_issue_comment")
		in.Roles[r] = ri
		got := Plan(in, snap)
		want := Op{Kind: KindImage, Name: "forgejo-reviewer-apps", Detail: "tools: -create_issue_comment", Role: RoleReviewer, Field: FieldTools}
		if !slices.Contains(got.Change, want) {
			t.Errorf("change = %s, want it to contain %s", opsString(got.Change), want)
		}
		if len(got.Verdict.Blocking) != 0 {
			t.Errorf("blocking = %q, want none", got.Verdict.Blocking)
		}
		wantWarn := `role "reviewer": create_issue_comment is off, but the persona relies on it`
		if !slices.Contains(got.Verdict.Warnings, wantWarn) {
			t.Errorf("warnings = %q, want it to contain %q", got.Verdict.Warnings, wantWarn)
		}
	})

	t.Run("a runtime changed", func(t *testing.T) {
		in, snap := editBaseline()
		r := RoleDeveloper
		ri := in.Roles[r]
		ri.Runtime = "registry.example/ainsel/agent:v2"
		in.Roles[r] = ri
		got := Plan(in, snap)
		want := Op{Kind: KindImage, Name: "forgejo-developer-apps", Detail: `runtime: "` + testRuntime + `" -> "registry.example/ainsel/agent:v2"`, Role: RoleDeveloper, Field: FieldRuntime}
		if !slices.Contains(got.Change, want) {
			t.Errorf("change = %s, want it to contain %s", opsString(got.Change), want)
		}
	})

	t.Run("a tool added", func(t *testing.T) {
		in, snap := editBaseline()
		r := RoleRefiner
		ri := in.Roles[r]
		ri.Tools = append(slices.Clone(fakeDefaults(r)), "list_branches")
		in.Roles[r] = ri
		got := Plan(in, snap)
		want := Op{Kind: KindImage, Name: "forgejo-refiner-apps", Detail: "tools: +list_branches", Role: RoleRefiner, Field: FieldTools}
		if !slices.Contains(got.Change, want) {
			t.Errorf("change = %s, want it to contain %s", opsString(got.Change), want)
		}
	})
}

// TestPlanEditExcludeLabelAndUnconnectedHook covers the two remaining diffs: the
// refiner's triggers follow the exclude label, and an install whose org webhook
// was never created still has it to create.
func TestPlanEditExcludeLabelAndUnconnectedHook(t *testing.T) {
	t.Run("exclude label", func(t *testing.T) {
		in, snap := editBaseline()
		in.ExcludeLabel = "hold"
		got := Plan(in, snap)
		for _, name := range []string{"forgejo-refiner-issue-opened-apps", "forgejo-refiner-issue-reopened-apps"} {
			want := Op{Kind: KindTrigger, Name: name, Detail: `exclude label: "do-not-refine" -> "hold"`, Role: RoleRefiner, Field: FieldExcludeLabel}
			if !slices.Contains(got.Change, want) {
				t.Errorf("change = %s, want it to contain %s", opsString(got.Change), want)
			}
		}
		if len(got.Change) != 2 {
			t.Errorf("change = %s, want exactly the two refiner triggers", opsString(got.Change))
		}
		if got.Triggers[0].Filters[2].Value != "hold" {
			t.Errorf("trigger filter = %+v, want the new exclude label", got.Triggers[0].Filters[2])
		}
	})

	t.Run("hook still to create", func(t *testing.T) {
		in, snap := editBaseline()
		snap.Existing.HookID = 0
		got := Plan(in, snap)
		want := Op{Kind: KindHook, Name: "acme", Detail: "issues, issue_comment, pull_request, pull_request_review"}
		if len(got.Create) != 1 || got.Create[0] != want {
			t.Errorf("create = %s, want exactly %s", opsString(got.Create), want)
		}
	})
}

// opsString renders ops for a failure message, one per line. It spells out the
// detail too, because a wrong detail is exactly what these tests look for.
func opsString(ops []Op) string {
	if len(ops) == 0 {
		return "[]"
	}
	var b strings.Builder
	for _, op := range ops {
		fmt.Fprintf(&b, "\n  %s %s: %s", op.Kind, op.Name, op.Detail)
	}
	return b.String()
}

// window is a window written the way the form writes one. The tests name hours as
// the hub's shape, not as a helper's guess at one.
func window(zone, start, end string, days ...int) *WorkingHours {
	return &WorkingHours{Timezone: zone, Days: days, Start: start, End: end}
}

// setFormHours and setRowHours put a window on a map's value, which Go will not let a test
// do to a struct field directly. The form is what the admin typed; the row is what the
// wizard wrote last, and the pair of them is the whole diff.
func setFormHours(in Input, r Role, w *WorkingHours) {
	ri := in.Roles[r]
	ri.WorkingHours = w
	in.Roles[r] = ri
}

func setRowHours(inst *Install, r Role, w *WorkingHours) {
	rs := inst.Roles[r]
	rs.WorkingHours = w
	inst.Roles[r] = rs
}

// TestPlanWorkingHoursOnACreateNamesTheWindow is the preview's half of AC1: an
// install created with a window says so on the agent's create op, and a role that
// set none says nothing — "any hour" is the default, and a preview that named it
// for every role would say nothing at all.
func TestPlanWorkingHoursOnACreateNamesTheWindow(t *testing.T) {
	in, snap := planInput(t)
	setFormHours(in, RoleDeveloper, window("Europe/Berlin", "09:00", "17:00", 1, 2, 3, 4, 5))
	got := Plan(in, snap)

	if len(got.Verdict.Blocking) != 0 {
		t.Fatalf("blocking = %q, want none", got.Verdict.Blocking)
	}
	byRole := map[Role]string{}
	for _, op := range got.Create {
		if op.Kind == KindAgent {
			name := strings.TrimSuffix(op.Name, "-apps")
			byRole[Role(strings.TrimPrefix(name, "forgejo-"))] = op.Detail
		}
	}
	if want := "working hours Mon–Fri 09:00–17:00 Europe/Berlin"; !strings.Contains(byRole[RoleDeveloper], want) {
		t.Errorf("developer create = %q, want it to name %q", byRole[RoleDeveloper], want)
	}
	for _, r := range []Role{RoleRefiner, RoleReviewer} {
		if strings.Contains(byRole[r], "working hours") {
			t.Errorf("%s create = %q, want no window: the form set none", r, byRole[r])
		}
	}
}

// TestPlanWorkingHoursDiffIsAgainstTheRow is the whole of AC3, and the one place
// the re-apply loop does not diff against what AInsel holds today. The row's
// last-applied window is the baseline, so: a form prefilled from the row and left
// alone writes nothing, and a window the wizard never set is never written — which
// is what leaves a window an admin set in the console alone, because the plan never
// looks at the console's answer at all.
func TestPlanWorkingHoursDiffIsAgainstTheRow(t *testing.T) {
	const field = FieldWorkingHours

	hours := window("Europe/Berlin", "09:00", "17:00", 1, 2, 3, 4, 5)

	hoursOp := func(r Role, detail string) Op {
		return Op{Kind: KindAgent, Name: AgentName(r, "apps"), Detail: detail, Role: r, Field: field}
	}

	t.Run("prefilled and untouched changes nothing", func(t *testing.T) {
		in, snap := editBaseline()
		setRowHours(snap.Existing, RoleReviewer, CloneWorkingHours(hours))
		setFormHours(in, RoleReviewer, CloneWorkingHours(hours))
		got := Plan(in, snap)

		if len(got.Verdict.Blocking) != 0 {
			t.Fatalf("blocking = %q, want none", got.Verdict.Blocking)
		}
		if len(got.Change) != 0 {
			t.Errorf("change = %s, want none: the form says what the row already records", opsString(got.Change))
		}
	})

	t.Run("a window the wizard never set is never written", func(t *testing.T) {
		// The row records nothing, which is both "the wizard never set hours" and what a
		// row written before this field existed says. The agent may hold a window an
		// admin set in the console; the plan cannot see it and so cannot overwrite it.
		in, snap := editBaseline()
		got := Plan(in, snap)

		for _, op := range got.Change {
			if op.Field == field {
				t.Errorf("change = %s, want no working-hours op: nothing was set in the wizard", opsString(got.Change))
			}
		}
	})

	t.Run("a moved window is written", func(t *testing.T) {
		in, snap := editBaseline()
		setRowHours(snap.Existing, RoleReviewer, CloneWorkingHours(hours))
		setFormHours(in, RoleReviewer, window("Europe/Berlin", "10:00", "16:00", 1, 2, 3, 4, 5))
		got := Plan(in, snap)

		want := hoursOp(RoleReviewer, `working hours: "Mon–Fri 09:00–17:00 Europe/Berlin" -> "Mon–Fri 10:00–16:00 Europe/Berlin"`)
		if !slices.Contains(got.Change, want) {
			t.Errorf("change = %s, want it to contain %s", opsString(got.Change), want)
		}
	})

	t.Run("an explicit clear is an op", func(t *testing.T) {
		in, snap := editBaseline()
		setRowHours(snap.Existing, RoleDeveloper, CloneWorkingHours(hours))
		setFormHours(in, RoleDeveloper, nil)
		got := Plan(in, snap)

		want := hoursOp(RoleDeveloper, `working hours: "Mon–Fri 09:00–17:00 Europe/Berlin" -> "any hours"`)
		if !slices.Contains(got.Change, want) {
			t.Errorf("change = %s, want it to contain %s", opsString(got.Change), want)
		}
	})

	t.Run("a window set for the first time is an op", func(t *testing.T) {
		in, snap := editBaseline()
		setFormHours(in, RoleRefiner, window("", "22:00", "06:00"))
		got := Plan(in, snap)

		want := hoursOp(RoleRefiner, `working hours: "any hours" -> "every day 22:00–06:00 UTC"`)
		if !slices.Contains(got.Change, want) {
			t.Errorf("change = %s, want it to contain %s", opsString(got.Change), want)
		}
	})

	// 0 and 7 are both Sunday, and a form that prefilled [0 1] and sent [7 1] has
	// changed nothing. Comparing the lists as they came in would preview a write the
	// admin never meant.
	t.Run("sunday written two ways is one window", func(t *testing.T) {
		in, snap := editBaseline()
		setRowHours(snap.Existing, RoleReviewer, window("UTC", "09:00", "17:00", 0, 1))
		setFormHours(in, RoleReviewer, window("UTC", "09:00", "17:00", 7, 1))
		got := Plan(in, snap)

		for _, op := range got.Change {
			if op.Field == field {
				t.Errorf("change = %s, want no working-hours op: 0 and 7 are the same day", opsString(got.Change))
			}
		}
	})
}

// TestPlanWorkingHoursBlocksBeforeTheApply is the plan-time guard: the hub refuses a
// window it cannot use with a 400, and a refusal that arrives mid-apply leaves the
// accounts and the image behind. Every case names the role and says what to fix.
func TestPlanWorkingHoursBlocksBeforeTheApply(t *testing.T) {
	for _, tc := range []struct {
		name  string
		w     *WorkingHours
		block string
	}{
		{"start is not HH:MM", window("UTC", "9am", "17:00", 1), `not an HH:MM time`},
		{"zone cannot be loaded", window("Mars/Olympus_Mons", "09:00", "17:00", 1), `not a zone that can be loaded`},
		{"bounds leave no window", window("UTC", "09:00", "09:00", 1), `leaves no window at all`},
		{"one bound only", window("UTC", "09:00", "", 1), `set both bounds or neither`},
		{"day out of range", window("UTC", "09:00", "17:00", 9), `not a weekday`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, snap := editBaseline()
			setFormHours(in, RoleDeveloper, tc.w)
			got := Plan(in, snap)

			var found string
			for _, b := range got.Verdict.Blocking {
				if strings.Contains(b, "working hours") {
					found = b
				}
			}
			if found == "" {
				t.Fatalf("blocking = %q, want a working-hours block", got.Verdict.Blocking)
			}
			if !strings.Contains(found, "developer") {
				t.Errorf("block %q, want it to name the role", found)
			}
			if !strings.Contains(found, tc.block) {
				t.Errorf("block %q, want it to say %q", found, tc.block)
			}
			// The plan still lists what it would have written — the preview is the
			// admin's diagnosis — and the apply is what refuses to write any of it while a
			// block stands, which is the rule every other block here leans on too.
			if len(got.Verdict.Blocking) == 0 {
				t.Errorf("blocking = %q, want the plan to stay blocked", got.Verdict.Blocking)
			}
		})
	}
}

// TestPlanTurnTimeoutBlocksNonsenseValues is the guard rail of issue #61: a turn
// timeout is a wall-clock ceiling the runner enforces, so a typo is not a
// preference — it blocks the plan, naming the bounds, rather than silently
// falling back to a default the admin did not choose. Values inside the bounds
// (one minute to one day, whole milliseconds) plan clean.
func TestPlanTurnTimeoutBlocksNonsenseValues(t *testing.T) {
	for _, v := range []string{"abc", "0", "-1", "59999", "86400001", "90000000000000", "3600.5"} {
		in, snap := planInput(t)
		{
			ri := in.Roles[RoleRefiner]
			ri.TurnTimeoutMs = v
			in.Roles[RoleRefiner] = ri
		}
		got := Plan(in, snap)
		want := fmt.Sprintf("role %q: turn timeout %q is not a whole number of milliseconds between 60000 and 86400000", RoleRefiner, v)
		if !slices.Contains(got.Verdict.Blocking, want) {
			t.Errorf("value %q: blocking = %q, want it to contain %q", v, got.Verdict.Blocking, want)
		}
	}
	// A field holding nothing but whitespace is a field left blank: Normalize trims it
	// (issue #85), and blank is "the shipped default" here rather than a nonsense value
	// the admin never typed.
	for _, v := range []string{" ", "\t", " \n "} {
		in, snap := planInput(t)
		{
			ri := in.Roles[RoleRefiner]
			ri.TurnTimeoutMs = v
			in.Roles[RoleRefiner] = ri
		}
		got := Plan(in, snap)
		if len(got.Verdict.Blocking) != 0 {
			t.Errorf("value %q: blocking = %q, want none: whitespace-only reads as blank", v, got.Verdict.Blocking)
		}
	}
	for _, v := range []string{"60000", "3600000", "86400000"} {
		in, snap := planInput(t)
		{
			ri := in.Roles[RoleRefiner]
			ri.TurnTimeoutMs = v
			in.Roles[RoleRefiner] = ri
		}
		got := Plan(in, snap)
		if len(got.Verdict.Blocking) != 0 {
			t.Errorf("value %q: blocking = %q, want none", v, got.Verdict.Blocking)
		}
	}
}

// TestRuntimeHasVersion is the syntax rule the floating-ref gate is built on, in the
// one place it is decided. The port pair is the reason the rule reads the segment
// after the last "/" rather than the whole ref.
func TestRuntimeHasVersion(t *testing.T) {
	const digest = "@sha256" + ":0000000000000000000000000000000000000000000000000000000000000000"
	for _, tc := range []struct {
		ref  string
		want bool
	}{
		{"registry.example.com/img:8.0", true},
		{"registry.example.com/img:latest", true},
		{"registry.example.com/img", false},
		{"acme/ainsel-pi-maui", false},
		// A registry port is not a tag, and a port plus a tag is both.
		{"registry:5000/img", false},
		{"registry:5000/img:8.0", true},
		// A digest pins the ref whatever else it carries, a tag included.
		{"registry.example.com/img" + digest, true},
		{"registry:5000/img:8.0" + digest, true},
		// A colon with nothing after it names no version.
		{"registry.example.com/img:", false},
		{"", false},
	} {
		if got := runtimeHasVersion(tc.ref); got != tc.want {
			t.Errorf("runtimeHasVersion(%q) = %v, want %v", tc.ref, got, tc.want)
		}
	}
}

// TestPlanRuntimeTagOrDigest is the floating-ref gate. A runtime with neither tag nor
// digest is a ref the registry resolves to `latest` — which may not exist at all, in
// which case every task pod fails at pull while the tasks sit at QUEUED. A new or
// changed one blocks; one the install already carries only warns, so a live install
// stays editable while the plan nags for the fix.
func TestPlanRuntimeTagOrDigest(t *testing.T) {
	const (
		untagged = "acme/ainsel-pi-maui"
		tagged   = "acme/ainsel-pi-maui:8.0"
		digest   = "acme/ainsel-pi-maui@sha256:0000000000000000000000000000000000000000000000000000000000000000"
	)
	msg := func(r Role, ref string) string {
		return fmt.Sprintf("role %q: runtime %s has no tag or digest — add a tag such as :8.0, or a @sha256: digest", r, ref)
	}
	setRuntime := func(in *Input, r Role, ref string) {
		ri := in.Roles[r]
		ri.Runtime = ref
		in.Roles[r] = ri
	}

	t.Run("a new install blocks on an untagged ref", func(t *testing.T) {
		in, snap := planInput(t)
		setRuntime(&in, RoleDeveloper, untagged)
		got := Plan(in, snap)
		if !slices.Contains(got.Verdict.Blocking, msg(RoleDeveloper, untagged)) {
			t.Errorf("blocking = %q, want it to contain %q", got.Verdict.Blocking, msg(RoleDeveloper, untagged))
		}
		// Only the role that floats is named.
		for _, b := range got.Verdict.Blocking {
			if strings.Contains(b, "no tag or digest") && !strings.Contains(b, "developer") {
				t.Errorf("blocking = %q, want the untagged role alone", got.Verdict.Blocking)
			}
		}
	})

	t.Run("a tag or a digest plans clean", func(t *testing.T) {
		for _, ref := range []string{tagged, digest, "registry:5000/img:8.0"} {
			in, snap := planInput(t)
			setRuntime(&in, RoleDeveloper, ref)
			got := Plan(in, snap)
			if len(got.Verdict.Blocking) != 0 {
				t.Errorf("ref %q: blocking = %q, want none", ref, got.Verdict.Blocking)
			}
			if len(got.Verdict.Warnings) != 0 {
				t.Errorf("ref %q: warnings = %q, want none", ref, got.Verdict.Warnings)
			}
		}
	})

	t.Run("a registry port is not a tag", func(t *testing.T) {
		in, snap := planInput(t)
		setRuntime(&in, RoleRefiner, "registry:5000/img")
		got := Plan(in, snap)
		if !slices.Contains(got.Verdict.Blocking, msg(RoleRefiner, "registry:5000/img")) {
			t.Errorf("blocking = %q, want it to contain the port ref", got.Verdict.Blocking)
		}
	})

	t.Run("an edit that keeps the install's untagged ref warns", func(t *testing.T) {
		in, snap := editBaseline()
		// The install carries the floating ref; the admin edits something else, so the
		// field is left empty — which means keep, not pick.
		snap.Prior[RoleReviewer] = PriorState{Runtime: untagged, Provider: "ollama-cloud", Model: "glm-4.7"}
		setRuntime(&in, RoleReviewer, "")
		got := Plan(in, snap)
		if !slices.Contains(got.Verdict.Warnings, msg(RoleReviewer, untagged)) {
			t.Errorf("warnings = %q, want it to contain %q", got.Verdict.Warnings, msg(RoleReviewer, untagged))
		}
		if len(got.Verdict.Blocking) != 0 {
			t.Errorf("blocking = %q, want none: an existing install stays editable", got.Verdict.Blocking)
		}
		// The warning is the nudge, not a write: nothing changes the image.
		for _, op := range got.Change {
			if op.Field == FieldRuntime {
				t.Errorf("change = %s, want no runtime op: the value did not move", opsString(got.Change))
			}
		}
	})

	t.Run("an edit that moves to an untagged ref blocks", func(t *testing.T) {
		in, snap := editBaseline()
		setRuntime(&in, RoleReviewer, untagged)
		got := Plan(in, snap)
		if !slices.Contains(got.Verdict.Blocking, msg(RoleReviewer, untagged)) {
			t.Errorf("blocking = %q, want it to contain %q", got.Verdict.Blocking, msg(RoleReviewer, untagged))
		}
	})

	t.Run("an edit that fixes an untagged ref is clean", func(t *testing.T) {
		in, snap := editBaseline()
		snap.Prior[RoleReviewer] = PriorState{Runtime: untagged, Provider: "ollama-cloud", Model: "glm-4.7"}
		setRuntime(&in, RoleReviewer, tagged)
		got := Plan(in, snap)
		if len(got.Verdict.Blocking) != 0 || len(got.Verdict.Warnings) != 0 {
			t.Errorf("blocking = %q, warnings = %q, want neither", got.Verdict.Blocking, got.Verdict.Warnings)
		}
		want := Op{Kind: KindImage, Name: ImageName(RoleReviewer, "apps"),
			Detail: diff("runtime", untagged, tagged), Role: RoleReviewer, Field: FieldRuntime}
		if !slices.Contains(got.Change, want) {
			t.Errorf("change = %s, want it to contain %s", opsString(got.Change), want)
		}
	})
}

// TestPlanRuntimeWhitespace is issue #85. The console pasted a runtime with a tab
// after the tag; the plan and the apply both accepted it, the hub stored it on the
// AgentImage, and every task Job of all three roles then failed FailedCreate —
// `must not have leading or trailing whitespace` — while the Job that never got a pod
// held the agent's only task slot, so the install went quiet with nothing in the hub.
// Whitespace around a ref is trimmed before anything reads it, so the ref the hub
// stores is one a pod can start from. Whitespace inside one is never a valid image URL
// and has no repair but typing it out, so it blocks — or warns, when the install
// already carries it, which is the same split the floating ref above uses.
func TestPlanRuntimeWhitespace(t *testing.T) {
	const clean = "acme/ainsel-pi-go:1.24"
	msg := func(r Role, ref string) string {
		return fmt.Sprintf("role %q: runtime %q contains whitespace — a container image URL has none", r, ref)
	}
	setRuntime := func(in *Input, r Role, ref string) {
		ri := in.Roles[r]
		ri.Runtime = ref
		in.Roles[r] = ri
	}

	t.Run("whitespace around a ref is trimmed before the plan reads it", func(t *testing.T) {
		for _, dirty := range []string{clean + "\t", " " + clean, clean + "\n", "\t " + clean + " \t\n"} {
			in, snap := planInput(t)
			setRuntime(&in, RoleDeveloper, dirty)
			got := Plan(in, snap)
			if len(got.Verdict.Blocking) != 0 {
				t.Errorf("ref %q: blocking = %q, want none", dirty, got.Verdict.Blocking)
			}
			if len(got.Verdict.Warnings) != 0 {
				t.Errorf("ref %q: warnings = %q, want none", dirty, got.Verdict.Warnings)
			}
		}
	})

	t.Run("an edit writes the trimmed ref, not the pasted one", func(t *testing.T) {
		const prior = "acme/ainsel-pi-go:1.23"
		in, snap := editBaseline()
		snap.Prior[RoleReviewer] = PriorState{Runtime: prior, Provider: "ollama-cloud", Model: "glm-4.7"}
		setRuntime(&in, RoleReviewer, clean+"\t")
		got := Plan(in, snap)
		if len(got.Verdict.Blocking) != 0 || len(got.Verdict.Warnings) != 0 {
			t.Fatalf("blocking = %q, warnings = %q, want neither", got.Verdict.Blocking, got.Verdict.Warnings)
		}
		want := Op{Kind: KindImage, Name: ImageName(RoleReviewer, "apps"),
			Detail: diff("runtime", prior, clean), Role: RoleReviewer, Field: FieldRuntime}
		if !slices.Contains(got.Change, want) {
			t.Errorf("change = %s, want it to contain %s", opsString(got.Change), want)
		}
		for _, op := range got.Change {
			if strings.Contains(op.Detail, "\t") {
				t.Errorf("change = %s, want no op to carry the pasted tab", opsString(got.Change))
			}
		}
	})

	t.Run("whitespace inside a ref blocks", func(t *testing.T) {
		for _, bad := range []string{"acme/ainsel pi-go:1.24", "acme/ainsel-pi-go: 1.24", "registry.example.com/img:8\t0"} {
			in, snap := planInput(t)
			setRuntime(&in, RoleDeveloper, bad)
			got := Plan(in, snap)
			if !slices.Contains(got.Verdict.Blocking, msg(RoleDeveloper, bad)) {
				t.Errorf("ref %q: blocking = %q, want it to contain %q", bad, got.Verdict.Blocking, msg(RoleDeveloper, bad))
			}
			// The whitespace block is the one line the ref earns: the version rule does
			// not pile on behind it.
			for _, b := range got.Verdict.Blocking {
				if strings.Contains(b, "no tag or digest") {
					t.Errorf("ref %q: blocking = %q, want the whitespace line alone", bad, got.Verdict.Blocking)
				}
			}
		}
	})

	t.Run("a ref the install already carries warns instead of blocking", func(t *testing.T) {
		const bad = "acme/ainsel-pi-go:1.2 4"
		in, snap := editBaseline()
		snap.Prior[RoleReviewer] = PriorState{Runtime: bad, Provider: "ollama-cloud", Model: "glm-4.7"}
		// The field is left empty, which is "keep what the install has" — the admin is
		// editing something else, and a live install stays editable.
		setRuntime(&in, RoleReviewer, "")
		got := Plan(in, snap)
		if !slices.Contains(got.Verdict.Warnings, msg(RoleReviewer, bad)) {
			t.Errorf("warnings = %q, want it to contain %q", got.Verdict.Warnings, msg(RoleReviewer, bad))
		}
		if len(got.Verdict.Blocking) != 0 {
			t.Errorf("blocking = %q, want none", got.Verdict.Blocking)
		}
	})

	t.Run("a ref of nothing but whitespace is a missing ref", func(t *testing.T) {
		in, snap := planInput(t)
		setRuntime(&in, RoleDeveloper, "\t ")
		got := Plan(in, snap)
		if !slices.Contains(got.Verdict.Blocking, `role "developer": no runtime container is set`) {
			t.Errorf("blocking = %q, want the missing-runtime block", got.Verdict.Blocking)
		}
	})
}

// TestPlanCreateShowsEffectiveTurnTimeout is the plan's promise of issue #61:
// the effective value per role is visible before apply — the wizard's where the
// admin set one, the shipped default where they did not.
func TestPlanCreateShowsEffectiveTurnTimeout(t *testing.T) {
	in, snap := planInput(t)
	{
		ri := in.Roles[RoleDeveloper]
		ri.TurnTimeoutMs = "5400000"
		in.Roles[RoleDeveloper] = ri
	} // 90 min, the admin's own
	got := Plan(in, snap)
	if len(got.Verdict.Blocking) != 0 {
		t.Fatalf("blocking = %q, want none", got.Verdict.Blocking)
	}
	wants := map[Role]string{
		RoleRefiner:   "turn timeout 3600000ms (60 min)",
		RoleDeveloper: "turn timeout 5400000ms (90 min)",
		RoleReviewer:  "turn timeout 3600000ms (60 min)",
	}
	for role, want := range wants {
		found := false
		for _, op := range got.Create {
			if op.Kind == KindImage && op.Name == ImageName(role, "apps") {
				if !strings.Contains(op.Detail, want) {
					t.Errorf("image op %s: detail %q, want it to contain %q", op.Name, op.Detail, want)
				}
				found = true
			}
		}
		if !found {
			t.Errorf("create = %s, want an image op for %s", opsString(got.Create), role)
		}
	}
}

// TestPlanEditTurnTimeoutOnlyOnRealDifference is the keep-semantics of issue #61
// on the plan side: an empty wizard field is never a change — that is what lets a
// hand-patched image value survive a re-apply — and a field that says what the
// install already has is not a change either. Only a real difference previews a
// write, as a KindImage op on the FieldTurnTimeout the apply matches on.
func TestPlanEditTurnTimeoutOnlyOnRealDifference(t *testing.T) {
	t.Run("empty keeps", func(t *testing.T) {
		in, snap := editBaseline()
		{
			rs := snap.Existing.Roles[RoleRefiner]
			rs.TurnTimeoutMs = "3600000"
			snap.Existing.Roles[RoleRefiner] = rs
		}
		got := Plan(in, snap)
		for _, op := range got.Change {
			if op.Field == FieldTurnTimeout {
				t.Errorf("change = %s, want no turn timeout op: empty means keep", opsString(got.Change))
			}
		}
	})

	t.Run("same value changes nothing", func(t *testing.T) {
		in, snap := editBaseline()
		{
			rs := snap.Existing.Roles[RoleRefiner]
			rs.TurnTimeoutMs = "3600000"
			snap.Existing.Roles[RoleRefiner] = rs
		}
		{
			ri := in.Roles[RoleRefiner]
			ri.TurnTimeoutMs = "3600000"
			in.Roles[RoleRefiner] = ri
		}
		got := Plan(in, snap)
		for _, op := range got.Change {
			if op.Field == FieldTurnTimeout {
				t.Errorf("change = %s, want no turn timeout op: the value did not move", opsString(got.Change))
			}
		}
	})

	t.Run("changed value previews one op", func(t *testing.T) {
		in, snap := editBaseline()
		{
			rs := snap.Existing.Roles[RoleRefiner]
			rs.TurnTimeoutMs = "3600000"
			snap.Existing.Roles[RoleRefiner] = rs
		}
		{
			ri := in.Roles[RoleRefiner]
			ri.TurnTimeoutMs = "7200000"
			in.Roles[RoleRefiner] = ri
		}
		got := Plan(in, snap)
		want := Op{
			Kind: KindImage, Name: ImageName(RoleRefiner, "apps"),
			Detail: `turn timeout: "3600000" -> "7200000"; ` + turnTimeoutEnvRewrite,
			Role:   RoleRefiner, Field: FieldTurnTimeout,
		}
		if !slices.Contains(got.Change, want) {
			t.Errorf("change = %s, want it to contain %s", opsString(got.Change), want)
		}
		// One role's change touches one image: no other role gets an op.
		for _, op := range got.Change {
			if op.Field == FieldTurnTimeout && op.Role != RoleRefiner {
				t.Errorf("change = %s, want the op on the refiner alone", opsString(got.Change))
			}
		}
	})
}

// TestPlanOpenAIAuthMethod is the method half of an openai role's credential. It
// is the half the hub can read back, so — unlike a key — it is diffed by value:
// the prior state carries what the agent answers, and a moved method previews as a
// change to that role's agent. While a role signs in, the key row goes quiet: the
// apply never sends a key beside the sign-in method, and previewing one would
// promise a write that cannot happen.
func TestPlanOpenAIAuthMethod(t *testing.T) {
	// An openai role on the API key, with the key the row records.
	openAI := func(prior func(*PriorState)) PriorState {
		p := PriorState{Runtime: testRuntime, Provider: LLMProviderOpenAI, Model: "gpt-5.2"}
		if prior != nil {
			prior(&p)
		}
		return p
	}

	t.Run("moved to the sign-in previews the write", func(t *testing.T) {
		in, snap := editBaseline()
		snap.Prior[RoleReviewer] = openAI(func(p *PriorState) {
			p.OpenAIKeyHash = CustomKeyFingerprint("sk-stored")
		})
		snap.Settings.LLM[RoleReviewer] = RoleLLM{
			Provider: LLMProviderOpenAI, Model: "gpt-5.2",
			OpenAIKey: "sk-stored", OpenAIAuthMethod: LLMAuthMethodChatGPTSignIn,
		}
		got := Plan(in, snap)
		want := Op{Kind: KindAgent, Name: "forgejo-reviewer-apps",
			Detail: `llm auth method: "APIKey" -> "ChatGPTSignIn"`,
			Role:   RoleReviewer, Field: FieldLLMOpenAIAuthMethod}
		if !slices.Contains(got.Change, want) {
			t.Errorf("change = %s, want it to contain %s", opsString(got.Change), want)
		}
		for _, op := range got.Change {
			if op.Field == FieldLLMOpenAIKey {
				t.Errorf("change = %s, want no key op for a role that signs in", opsString(got.Change))
			}
		}
	})

	t.Run("the sign-in already recorded changes nothing", func(t *testing.T) {
		in, snap := editBaseline()
		snap.Prior[RoleReviewer] = openAI(func(p *PriorState) {
			p.OpenAIAuthMethod = LLMAuthMethodChatGPTSignIn
			p.OpenAIKeyHash = CustomKeyFingerprint("sk-stale")
		})
		snap.Settings.LLM[RoleReviewer] = RoleLLM{
			Provider: LLMProviderOpenAI, Model: "gpt-5.2",
			OpenAIKey: "sk-stale", OpenAIAuthMethod: LLMAuthMethodChatGPTSignIn,
		}
		got := Plan(in, snap)
		for _, op := range got.Change {
			if op.Field == FieldLLMOpenAIAuthMethod || op.Field == FieldLLMOpenAIKey {
				t.Errorf("change = %s, want nothing: the agent already signs in and the key is never sent",
					opsString(got.Change))
			}
		}
	})

	t.Run("back to the API key previews the write", func(t *testing.T) {
		in, snap := editBaseline()
		snap.Prior[RoleReviewer] = openAI(func(p *PriorState) {
			p.OpenAIAuthMethod = LLMAuthMethodChatGPTSignIn
			p.OpenAIKeyHash = CustomKeyFingerprint("sk-stored")
		})
		snap.Settings.LLM[RoleReviewer] = RoleLLM{
			Provider: LLMProviderOpenAI, Model: "gpt-5.2",
			OpenAIKey: "sk-stored", OpenAIAuthMethod: LLMAuthMethodAPIKey,
		}
		got := Plan(in, snap)
		want := Op{Kind: KindAgent, Name: "forgejo-reviewer-apps",
			Detail: `llm auth method: "ChatGPTSignIn" -> "APIKey"`,
			Role:   RoleReviewer, Field: FieldLLMOpenAIAuthMethod}
		if !slices.Contains(got.Change, want) {
			t.Errorf("change = %s, want it to contain %s", opsString(got.Change), want)
		}
	})

	t.Run("away from openai the method changes nothing", func(t *testing.T) {
		in, snap := editBaseline()
		// The role runs elsewhere; the settings also name a sign-in. The hub's
		// openAI block is inert there, so the row's value is nobody's business and
		// diffing it would preview a write that changes nothing.
		snap.Settings.LLM[RoleReviewer] = RoleLLM{
			Provider: "ollama-cloud", Model: "glm-4.7", OpenAIAuthMethod: LLMAuthMethodChatGPTSignIn,
		}
		got := Plan(in, snap)
		for _, op := range got.Change {
			if op.Field == FieldLLMOpenAIAuthMethod {
				t.Errorf("change = %s, want no auth-method op away from openai", opsString(got.Change))
			}
		}
	})

	// The two states a warning is for, both of which leave an agent that exists
	// and runs nothing: a sign-in nobody has finished, and a key the sign-in makes
	// inert. Neither blocks — the first is the normal state of a sign-in agent
	// until an admin finishes it in the console, which is what the apply's hint
	// says as well.
	t.Run("the sign-in warns about the console step", func(t *testing.T) {
		in, snap := editBaseline()
		snap.Prior[RoleReviewer] = openAI(nil)
		snap.Settings.LLM[RoleReviewer] = RoleLLM{
			Provider: LLMProviderOpenAI, Model: "gpt-5.2", OpenAIAuthMethod: LLMAuthMethodChatGPTSignIn,
		}
		got := Plan(in, snap)
		if len(got.Verdict.Blocking) != 0 {
			t.Fatalf("blocking = %q, want none: choosing the sign-in is how an admin starts", got.Verdict.Blocking)
		}
		if !hasWarningContaining(got.Verdict.Warnings, "ChatGPT sign-in") {
			t.Errorf("warnings = %q, want one naming the sign-in the admin has to finish", got.Verdict.Warnings)
		}
	})

	t.Run("the sign-in warns about a key it will never send", func(t *testing.T) {
		in, snap := editBaseline()
		snap.Prior[RoleReviewer] = openAI(func(p *PriorState) {
			p.OpenAIAuthMethod = LLMAuthMethodChatGPTSignIn
			p.OpenAIKeyHash = CustomKeyFingerprint("sk-stale")
		})
		snap.Settings.LLM[RoleReviewer] = RoleLLM{
			Provider: LLMProviderOpenAI, Model: "gpt-5.2",
			OpenAIKey: "sk-stale", OpenAIAuthMethod: LLMAuthMethodChatGPTSignIn,
		}
		got := Plan(in, snap)
		if !hasWarningContaining(got.Verdict.Warnings, "never sent") {
			t.Errorf("warnings = %q, want one naming the stored key the sign-in leaves unused", got.Verdict.Warnings)
		}
		for _, w := range got.Verdict.Warnings {
			if strings.Contains(w, "sk-stale") {
				t.Errorf("warning %q spells a key", w)
			}
		}
	})

	t.Run("back to a key with none stored warns", func(t *testing.T) {
		in, snap := editBaseline()
		snap.Prior[RoleReviewer] = openAI(func(p *PriorState) {
			p.OpenAIAuthMethod = LLMAuthMethodChatGPTSignIn
		})
		snap.Settings.LLM[RoleReviewer] = RoleLLM{
			Provider: LLMProviderOpenAI, Model: "gpt-5.2", OpenAIAuthMethod: LLMAuthMethodAPIKey,
		}
		got := Plan(in, snap)
		if !hasWarningContaining(got.Verdict.Warnings, "no credential") {
			t.Errorf("warnings = %q, want one saying the agent would have no credential", got.Verdict.Warnings)
		}
	})

	// A new install names the method on the agent it creates, so the create op
	// says which one it is — the wizard's last screen is where an admin learns the
	// agent will arrive keyless.
	t.Run("a new install says so on the create op", func(t *testing.T) {
		in, snap := planInput(t)
		snap.Settings.LLM[RoleReviewer] = RoleLLM{
			Provider: LLMProviderOpenAI, Model: "gpt-5.2", OpenAIAuthMethod: LLMAuthMethodChatGPTSignIn,
		}
		got := Plan(in, snap)
		found := false
		for _, op := range got.Create {
			if op.Kind != KindAgent {
				continue
			}
			if op.Name != AgentName(RoleReviewer, "apps") {
				continue
			}
			found = true
			if !strings.Contains(op.Detail, "ChatGPT sign-in") {
				t.Errorf("create op %q, want it to name the sign-in", op.Detail)
			}
			if strings.Contains(op.Detail, "sk-") {
				t.Errorf("create op %q spells a key", op.Detail)
			}
		}
		if !found {
			t.Fatalf("create ops name no reviewer agent: %s", opsString(got.Create))
		}
	})
}

// hasWarningContaining says whether any warning carries the phrase.
func hasWarningContaining(warnings []string, phrase string) bool {
	for _, w := range warnings {
		if strings.Contains(w, phrase) {
			return true
		}
	}
	return false
}
