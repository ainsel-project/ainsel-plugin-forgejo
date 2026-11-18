# Setup Wizard Restructure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the current adopt-or-create setup wizard with one that creates a fresh, plugin-owned Forgejo trio per install. Each role gets its own bot account, role image with its tool set, predefined persona, and model and key. The install also gets its own connector, forge MCP server, six triggers and an org webhook, all kept in shape by a background re-apply and removable again.

**Architecture:**
- **Pure model:** a pure `setup.Plan(input, snapshot)` decides what will be created, changed or deleted, and whether that's allowed. `POST /setup/plan`, apply and the re-apply loop all call it.
- **Execution:** an executor turns a plan into hub and Forgejo calls in dependency order, with rollback.
- **Forgejo access:** a small Forgejo admin client does accounts, teams, tokens and the org webhook.
- **UI:** the wizard renders from the plan model, one component per screen.

**Tech Stack:** Go 1.26, mcp-go v1.2.1, modernc.org/sqlite, React 19 + Vite 8 + Vitest 5 with `@ainsel/plugin-sdk@^1.1`, Helm 3.

**Spec:** `docs/design/2026-10-24-setup-wizard-flow.md` (v7). Read §2 (rules R1–R8), §3 (what an install owns, tool sets, triggers, image lifecycle, re-apply, removal), §5 (screens) and §8 (verified facts). Where this plan and the spec differ, the spec wins.

## Global Constraints

- AInsel-side names: forge MCP `forgejo-mcp-<slug>`, role image and persona `forgejo-<role>-<slug>`, agent display name default `forgejo-<role>-<slug>`, connector `forgejo-<slug>`, triggers `forgejo-<role>-<what>-<slug>`. Forgejo-side: bot login `<role>-<slug>`, full name `AInsel <Role> (<slug>)`, team `ainsel-<role>-<slug>`.
- Slug `^[a-z][a-z0-9-]{0,29}[a-z0-9]$`, globally unique. Roles are exactly `refiner`, `developer`, `reviewer`.
- Secrets never persisted, logged or echoed in a response (R4): the Forgejo admin token, bot passwords and tokens, LLM keys. The webhook secret is handed from the connector create straight to the org-hook create.
- Nothing pre-existing is edited (R5). "Copy from existing" only reads, and only to prefill `mcp.url`, `roles.<role>.runtime` and `roles.<role>.llm.{provider,model}`.
- Token env name on every role image: `FORGEJO_TOKEN`. The forge MCP server is registered with `tokenFromEnv: FORGEJO_TOKEN`.
- Role image: `mcpServers` = [`forgejo-mcp-<slug>`, `plugin-forgejo`], `enabledSkills` = [`forgejo-forgejo`]. Tools come from `POST /api/v1/agent-images/{id}/refresh-mcp` and are then set with `PUT /api/v1/agent-images/{id}` (`tools`), only the role's set enabled. The `plugin-forgejo` refresh error is expected and ignored for that server only (spec §3.5).
- Tool-set guard rails (R7): refiner and reviewer may not have `create_branch`, `delete_branch`, `create_file`, `update_file`, `delete_file`, `create_pull_request`, `update_pull_request`, `bash`. Only the reviewer may have `create_pull_review`, `submit_pull_review`.
- Six triggers exactly as spec §3.4; no CI-sender trigger.
- Forgejo calls (all with the admin token, except token minting):
  - `GET /api/v1/user` (`is_admin`);
  - `POST /api/v1/admin/users` (`must_change_password:false`, `visibility:private`);
  - `POST /api/v1/users/{bot}/tokens` over **basic auth as the bot** (V8);
  - `POST /api/v1/orgs/{org}/teams` with `units_map` and `includes_all_repositories:true`, then `PUT /api/v1/teams/{id}/members/{bot}`;
  - `POST`/`PATCH`/`DELETE /api/v1/orgs/{org}/hooks[/{id}]` (type `forgejo`, `content_type: json`, events `issues`, `issue_comment`, `pull_request`, `pull_request_review`);
  - `DELETE /api/v1/admin/users/{bot}?purge=true`, `DELETE /api/v1/teams/{id}`.
- Team permissions (spec §9):
  - refiner `{"repo.code":"read","repo.issues":"write","repo.pulls":"read"}`;
  - developer `{"repo.code":"write","repo.issues":"write","repo.pulls":"write"}`;
  - reviewer `{"repo.code":"read","repo.issues":"write","repo.pulls":"write"}`.
- Token scopes per bot: `write:issue`, `write:repository`, `read:user`, `read:organization`.
- Every `/api/setup/*` route keeps the existing gate: credential (401), then admin user (403). Background work runs on `hub.WithBackground(ctx)` only.
- S1 blocks when the install's group is not among the plugin's background groups (`GET /api/v1/plugin-context` with the background credential).
- The S7 delivery wait is 60 s. Re-apply runs every 10 minutes and once at start.
- Commits: conventional, ending with the `Co-Authored-By` trailer; exact `git add` paths.

## Review Focus

1. **Apply fails halfway** (e.g. the third bot login is taken in Forgejo after the preview). Everything this Apply created is rolled back in reverse order, and the response names the hub's or Forgejo's own error. Pinned in Task 6.
2. **A secret in a response, log or the store.** The admin token, a bot password, a bot token or an LLM key must never appear in any API response body, the saved install row, or a log line. Pinned in Tasks 3, 6 and 7.
3. **A taken name found only at Apply.** Every name the preview reports as free is checked against the same snapshot Apply uses: logins, teams, image, persona, agent, connector, MCP server. A taken one is blocking in the preview, not an Apply-time crash. Pinned in Task 5.
4. **Edit install with blank secrets.** A re-run with an empty admin token, bot token or LLM key keeps what is stored, and asks again only for what needs it (a provider change needs a key; account changes need the admin token). Pinned in Task 5.
5. **Re-apply only touches what an install owns.** The loop sets back drift on resources whose ids are in an install row, and nothing else: not another install's resources, nor a hand-made agent with a similar name. It skips installs whose group the plugin can no longer act for. Pinned in Task 8.

---

### Task 1: Install model v2 and the six triggers

**Files:**
- Modify: `internal/setup/install.go`, `internal/setup/triggers.go`, `internal/setup/triggers_test.go`
- Modify: `internal/store/store.go`, `internal/store/store_test.go`

**Interfaces — produces:**
```go
type RoleState struct {
    AgentID, ImageID, PersonaID, BotLogin string
    TeamID int64
    Tools []string // the Forgejo tool names enabled on the role image
}
type Install struct {
    Slug, GroupID, BaseURL, Org string
    ConnectorID, MCPServer string
    HookID int64
    Roles map[Role]RoleState
    ExcludeLabel string
    WebhookVerifiedAt *time.Time
    Coverage map[string]time.Time // event family → first seen: "issues", "issue_comment", "pull_request"
}
func Triggers(in Install) []Trigger // exactly six, names forgejo-<role>-<what>-<slug>
func Missing(want, existing []Trigger) []Trigger // unchanged
```
JSON tags camelCase.

- [ ] **Step 1:** Update the tests.
  - `Triggers` returns 6 triggers named as in spec §3.4, each `AgentRef == in.Roles[role].AgentID`.
  - The golden test still loads `testdata/dev-apps-triggers.json`, drops `trigger-developer-ci-failure-apps`, renames `trigger-` → `forgejo-`, and maps `issue.labels` → `issue.labels.name` before comparing.
  - `Missing` cases unchanged.
  - Store round-trip and drift-guard tests run on the new `Install`.
- [ ] **Step 2:** Run `go test ./internal/setup/ ./internal/store/`. Expected: FAIL (old model).
- [ ] **Step 3:** Implement.
  - Remove `CISender` and `ciFailureTrigger`.
  - The store keeps one row per install as `slug TEXT PRIMARY KEY, data TEXT` (the JSON of `Install`). The plugin is unreleased, so drop the old `installs` table on open, with a comment saying so. `ConnectorIDs` reads `data`.
- [ ] **Step 4:** Run `go test ./internal/setup/ ./internal/store/ && go vet ./...`. Expected: PASS.
- [ ] **Step 5:** Commit `refactor: model an install as a plugin-owned trio`.

### Task 2: Tool profiles and guard rails

**Files:**
- Create: `content/profiles/refiner.json`, `content/profiles/developer.json`, `content/profiles/reviewer.json`
- Modify: `content/content.go`, `content/content_test.go`

**Interfaces — produces:**
```go
func DefaultTools(r setup.Role) []string           // sorted Forgejo tool names; developer also "bash"
func CheckTools(r setup.Role, tools []string) error // guard rails; error names every violating tool
func PersonaFor(r setup.Role, bots map[setup.Role]string, baseURL string) string // placeholders filled
```

- [ ] **Step 1:** Write the failing tests.
  - The default counts are refiner 12, developer 39 + `bash`, reviewer 20, with the exact names of spec §3.3.
  - `CheckTools("refiner", [...,"create_pull_request"])` fails, naming `create_pull_request`.
  - `CheckTools("developer", ["create_pull_review"])` fails.
  - Every default passes its own check.
  - `PersonaFor` leaves no `{{`.
- [ ] **Step 2:** Run `go test ./content/`. Expected: FAIL.
- [ ] **Step 3:** Implement.
  - Embed the JSON files as `{"tools": [...]}`.
  - Keep the guard-rail lists in `content.go` as constants from Global Constraints.
  - `PersonaFor` wraps the existing `Persona` with the replacer the setup API uses today, moved here.
- [ ] **Step 4:** Run `go test ./content/`. Expected: PASS.
- [ ] **Step 5:** Commit `feat: ship the role tool sets and their guard rails`.

### Task 3: Forgejo admin client

**Files:**
- Create: `internal/forgejo/client.go`, `internal/forgejo/client_test.go`

**Interfaces — produces:**
```go
type Client struct{ /* base URL, http client (10 s timeout) */ }
func New(baseURL string, hc *http.Client) *Client
type Admin struct{ c *Client; token string } // never prints the token
func (c *Client) Admin(token string) *Admin
func (a *Admin) WhoAmI(ctx) (login string, isAdmin bool, err error)
func (a *Admin) OrgExists(ctx, org string) (bool, error)
func (a *Admin) UserExists(ctx, login string) (bool, error)
func (a *Admin) TeamExists(ctx, org, name string) (bool, error)
func (a *Admin) CreateUser(ctx, login, fullName, password string) error
func (c *Client) MintToken(ctx, login, password, name string, scopes []string) (string, error) // basic auth
func (a *Admin) CreateTeam(ctx, org, name string, units map[string]string) (int64, error)
func (a *Admin) AddTeamMember(ctx, teamID int64, login string) error
func (a *Admin) CreateOrgHook(ctx, org string, h Hook) (int64, error)
func (a *Admin) UpdateOrgHook(ctx, org string, id int64, h Hook) error
func (a *Admin) DeleteOrgHook(ctx, org string, id int64) error
func (a *Admin) DeleteUser(ctx, login string) error   // ?purge=true
func (a *Admin) DeleteTeam(ctx, id int64) error
type Hook struct{ URL, Secret string; Events []string }
type StatusError struct{ Code int; Message string } // Forgejo's own message, never a token
```

- [ ] **Step 1:** Write the failing tests against `httptest`.
  - Each method hits the path, method and body of Global Constraints.
  - `MintToken` sends basic auth and no `Authorization: token`.
  - A 401 or 422 becomes `*StatusError` with Forgejo's `message`.
  - No error string or `%v` of `Admin` contains the token or the password (assert with `strings.Contains`).
  - `DeleteUser` sends `purge=true`.
- [ ] **Step 2:** Run `go test ./internal/forgejo/`. Expected: FAIL.
- [ ] **Step 3:** Implement with `net/http` + `encoding/json`. `Admin` implements `fmt.Stringer`, returning `forgejo.Admin{…}`.
- [ ] **Step 4:** Run `go test ./internal/forgejo/ && go vet ./internal/forgejo/`. Expected: PASS.
- [ ] **Step 5:** Commit `feat: add the forgejo admin client the wizard needs`.

### Task 4: Hub client: images, deletes, events, plugin groups

**Files:**
- Modify: `internal/hub/hub.go`, `internal/hub/http.go`, `internal/hub/fake.go`, tests beside them

**Interfaces — produces:** additions to `hub.Client`:
```go
CreateAgentImage(ctx, ImageSpec) (Ref, error)        // POST /api/v1/agent-images
RefreshImageTools(ctx, id string) ([]ImageTool, error) // POST …/{id}/refresh-mcp; per-server errors returned as ToolRefreshError, tools still returned
GetAgentImage(ctx, id string) (Image, error)          // tools with enabled, env names, mcpServers, enabledSkills, imageURL
SetImageTools(ctx, id string, tools []ImageTool) error // PUT …/{id} {tools}
UpdateAgentImage(ctx, id string, u ImageUpdate) error  // env / mcpServers / enabledSkills / imageURL, nil = keep
UpdateAgent(ctx, id string, u AgentUpdate) error       // imageRef / persona / llm + provider key, nil = keep
GetAgent(ctx, id string) (Agent, error)
DeleteAgent, DeleteAgentImage, DeletePersona, DeleteConnector, DeleteMCPServer func(ctx, id string) error
DeleteTrigger(ctx, id string) error
RotateConnectorSecret(ctx, id string) (Connector, error)
Events(ctx, connectorID string, since time.Time) ([]Event, error) // GET /api/v1/events?connector=&since=
PluginGroups(ctx) ([]string, error) // GET /api/v1/plugin-context on the background credential
```
`ImageSpec{DisplayName, GroupID, ImageURL string; MCPServers, EnabledSkills []string; Env []EnvVar}`; `ImageTool{Name, Kind, McpSource string; Enabled bool}`; `Event{ID, Type string; At time.Time}`. `AgentSpec` gains the provider key fields (`OllamaCloudKey`, `OpenCodeKey`, `AlibabaKey`, `CustomURL`, `CustomKey`), mapped to AInsel's `ollamaCloud.apiKey`, `openCode.apiKey`, `alibabaCloud.apiKey` and `customProvider.{url,apiKey}`. Remove `ListTriggers`/`CreateTrigger`'s Fake-only assumptions if Task 1 changed names. The `Fake` records every call and every delete.

- [ ] **Step 1:** Write the failing tests against an `httptest` hub.
  - Each method's path, method and body.
  - `RefreshImageTools` with one failing server still returns the others' tools.
  - `PluginGroups` uses the background credential (minted) even when the ctx carries a user credential.
  - Provider keys appear only in the request body, never in errors.
- [ ] **Step 2:** Run `go test ./internal/hub/`. Expected: FAIL.
- [ ] **Step 3:** Implement on the existing `HTTPClient` patterns (`get`/`post`, `listAll`, `StatusError`).
- [ ] **Step 4:** Run `go test ./internal/hub/ && go vet ./...`. Expected: PASS.
- [ ] **Step 5:** Commit `feat: let the hub client manage images and delete what it made`.

### Task 5: The plan

**Files:**
- Create: `internal/setup/plan.go`, `internal/setup/plan_test.go`

**Interfaces — produces:**
```go
type Input struct {
    Slug, GroupID, BaseURL, Org, MCPURL, ExcludeLabel string
    AdminToken string // "" on an edit that changes no accounts
    Roles map[Role]RoleInput
}
type RoleInput struct {
    Runtime, AgentName string
    Tools []string // nil = defaults
    LLM LLMInput
}
type LLMInput struct{ Provider, Model, URL, Key string } // Key "" = keep (edit) or missing (new)
type Snapshot struct {
    Existing *Install // nil for a new install
    CallerGroups []string // writable
    PluginGroups []string
    TakenNames map[string]bool // "image:forgejo-refiner-apps", "login:refiner-apps", "team:ainsel-refiner-apps", …
    AdminIsAdmin bool; AdminChecked bool
    OrgExists bool
}
type Op struct{ Kind, Name, Detail string }
type Verdict struct{ Blocking, Warnings []string }
type Result struct{ Create, Change, Delete []Op; Triggers []Trigger; Verdict Verdict }
func Plan(in Input, snap Snapshot) Result
```

- [ ] **Step 1:** Write the failing table tests, one row per rule.
  - A new install with a clean snapshot creates, in spec §5[7] order: 3 users, 3 teams, the MCP server, 3 personas, 3 images, 3 agents, the connector, 6 triggers and the org hook, with the names of Global Constraints.
  - Each blocking case yields exactly its message: invalid slug; slug taken (new); group not writable; group not in `PluginGroups` (message names `set_plugin_group_access`); missing admin token on new; not admin; org missing; any taken name; a keyed provider with no key on new; a `CheckTools` violation.
  - Removing `create_issue_comment` from any role is a warning.
  - On edit with blank key, same provider: no change and no block. Provider changed with blank key: blocking.
  - On edit, a tool removed shows up in `Change` for that image, and a role's runtime change shows up in `Change`.
- [ ] **Step 2:** Run `go test ./internal/setup/ -run Plan`. Expected: FAIL.
- [ ] **Step 3:** Implement as a pure function: no I/O, no clock.
- [ ] **Step 4:** Run `go test ./internal/setup/`. Expected: PASS.
- [ ] **Step 5:** Commit `feat: plan an install from its input and a snapshot`.

### Task 6: Apply with rollback

**Files:**
- Create: `internal/setup/apply.go` (or `internal/api/apply.go` if it needs the hub types; keep `internal/setup` free of `internal/hub` imports by defining the small interfaces it needs)
- Create: its test

**Interfaces — produces:**
```go
type Apply struct{ Hub HubOps; Forgejo ForgejoOps; Now func() time.Time; Rand io.Reader }
func (a Apply) Run(ctx, in Input, plan Result, prev *Install) (Install, ApplyReport, error)
type ApplyReport struct{ Created []Op; RolledBack []Op; Leftovers []Op }
```
`HubOps` and `ForgejoOps` are the minimal interfaces over Task 3 and Task 4 that `Run` calls.

The steps, in order:
1. Per role: `CreateUser` with a random 24-byte password, then `MintToken` (password discarded), `CreateTeam`, `AddTeamMember`.
2. `RegisterMCPServer` (`tokenFromEnv: FORGEJO_TOKEN`).
3. `CreatePersona`, with `PersonaFor` text.
4. `CreateAgentImage`, `RefreshImageTools` (ignore the `plugin-forgejo` server error only), then `SetImageTools` with only `in.Roles[r].Tools` (or the defaults) enabled. A tool not in the catalog is an error naming it.
5. `CreateAgent` (`imageRef`, `persona`, `llm` + key).
6. `CreateConnector`.
7. The six `CreateTrigger` calls.
8. `CreateOrgHook` with the connector's endpoint and secret.
9. Return the `Install` to save.

On any error: delete everything created so far in reverse order, and return the original error wrapped with the step name.

- [ ] **Step 1:** Write the failing tests with fakes.
  - Happy path call order.
  - The image gets exactly the role's tools enabled.
  - A failure at step 7 rolls back connector, agents, images, personas, MCP server, teams and users, and `Leftovers` is empty.
  - A rollback delete that fails lands in `Leftovers`.
  - No fake ever receives the admin token except `Forgejo.Admin`; the returned `Install` and `ApplyReport` serialize without any token, password or key (scan the JSON).
- [ ] **Step 2:** Run `go test ./internal/setup/ -run Apply`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test ./internal/setup/`. Expected: PASS.
- [ ] **Step 5:** Commit `feat: apply an install plan and roll it back on failure`.

### Task 7: Setup API routes

**Files:**
- Modify: `internal/api/setup.go` (replace `wizardInput` and the old plan/apply bodies), `internal/api/setup_test.go`

**Routes** (all behind the existing gate):
- `GET /api/setup/installs` → installs with `connected` (`webhookVerifiedAt != nil`) and coverage.
- `GET /api/setup/options` → writable groups, `pluginGroups`, `forgejoBaseURL`, and copy sources:
  - `mcpServers` `{name, url}`;
  - `images` `{id, displayName, imageURL, hasBash}`;
  - `agents` `{id, name, provider, model}`.
- `POST /api/setup/plan` → `Result` (body `Input`; the snapshot is built from hub, Forgejo with the given admin token, and the store).
- `POST /api/setup/apply` → re-plans; 409 with the verdict if blocking; otherwise `Apply.Run`, saves the install, and answers `{install, report}`.
- `GET /api/setup/installs/{slug}/deliveries?since=` → `{verified bool, at}`. On the first event since `since` it sets `webhookVerifiedAt` and updates coverage.
- `POST /api/setup/installs/{slug}/repair-connection` (admin token in body) → rotates the secret, then `UpdateOrgHook`.
- `DELETE /api/setup/installs/{slug}` (optional admin token) → deletes in reverse order. With a token it also deletes the hook, teams and users. It answers the checklist of what is left for the admin.

- [ ] **Step 1:** Write the failing tests with `hub.Fake` + an `httptest` Forgejo.
  - Gate cases (401, 403).
  - `plan` makes no writes.
  - `apply` with a blocking verdict → 409 and no writes.
  - `apply` happy path → install saved, no secret in the body.
  - `deliveries` turns verified on the first event.
  - `DELETE` without a token deletes only hub resources and lists users, teams and hook in the checklist.
- [ ] **Step 2:** Run `go test ./internal/api/ -run Setup`. Expected: FAIL.
- [ ] **Step 3:** Implement. Remove the old adopt/existing code paths and their tests.
- [ ] **Step 4:** Run `go test ./... && go vet ./...`. Expected: PASS.
- [ ] **Step 5:** Commit `feat: serve the new setup flow`.

### Task 8: Background re-apply

**Files:**
- Create: `internal/reconcile/reconcile.go`, test
- Modify: `cmd/plugin/main.go` (start it)

**Interfaces — produces:**
```go
type Loop struct{ Hub hub.Client; Store *store.Store; Every time.Duration; Log func(slug, msg string) }
func (l Loop) Once(ctx context.Context) error // ctx is wrapped with hub.WithBackground
func (l Loop) Run(ctx context.Context)        // Once at start, then every Every (10m)
```
Per install whose `GroupID` is in `PluginGroups`:
- **Images:** enabled tools == `Roles[r].Tools`; `mcpServers` and `enabledSkills` as in Global Constraints; `FORGEJO_TOKEN` env name present.
- **Agents:** `imageRef`, `persona` and `llm` provider/model as stored.
- **Triggers:** `Missing` against the connector's triggers.
- **Coverage:** events seen on the connector since the last pass (`hub.Events`), folded into `Coverage`, saved to the row.
- Anything off is set back, with one `Log` line per correction.

It never creates users, never touches Forgejo, and never touches a resource whose id isn't in an install row. An install whose group isn't in `PluginGroups` is skipped and logged once.

- [ ] **Step 1:** Write the failing tests with `hub.Fake`.
  - A disabled tool is re-enabled and logged.
  - A resource with a similar name but an id that isn't in the row is untouched.
  - A skipped group makes no calls.
  - The ctx passed to the fake is marked background.
  - A new `issue_comment` event lands in the row's `Coverage`.
- [ ] **Step 2:** Run `go test ./internal/reconcile/`. Expected: FAIL.
- [ ] **Step 3:** Implement. In `main.go`, start `Loop{Every: 10*time.Minute}` when `HUB_URL` is set.
- [ ] **Step 4:** Run `go test ./... && go vet ./...`. Expected: PASS.
- [ ] **Step 5:** Commit `feat: keep every install's trio in shape`.

### Task 9: Wizard UI

**Files:**
- Create: `ui/src/setup/` with `Installs.tsx` (S0), `Instance.tsx` (S1), `ForgeMcp.tsx` (S2), `Accounts.tsx` (S3), `Roles.tsx` (S4, one role per tab), `Rules.tsx` (S5), `Preview.tsx` (S6), `Connect.tsx` (S7), `Done.tsx` (S8), `model.ts` (the `Input` shape and step logic), a test per screen
- Modify: `ui/src/SetupPage.tsx` (becomes the step router), `ui/src/api.ts` (types for Task 7's routes)

- [ ] **Step 1:** Write the failing tests (SDK mocked as in `ui/src/testing/sdk.tsx`).
  - S1 shows the blocking message when the chosen group isn't in `pluginGroups`.
  - S4 tools: unchecking `create_issue_comment` shows the warning. A guard-rail tool doesn't render as addable for refiner or reviewer. "Use for all roles" copies provider, model and key to the other two roles.
  - S6 renders `create` entries by name, and Apply is disabled while `verdict.blocking` is non-empty.
  - S7 polls `deliveries` until verified, then shows ✓. Without delivery after 60 s it shows the *Recent Deliveries* hint and the direct hook link `<baseURL>/org/<org>/settings/hooks/<id>`.
  - Secrets: after Apply the admin token and keys are cleared from state; `localStorage.setItem` is never called.
  - S0 "Remove install" asks for confirmation and an optional admin token.
  - Copy from existing: picking an image fills that role's runtime, and picking an agent fills provider and model but never the key.
  - Edit install: S0 "Edit" opens S1–S5 prefilled from the install row, with empty token and key fields labelled "leave empty to keep".
- [ ] **Step 2:** Run `pnpm -C ui test`. Expected: FAIL.
- [ ] **Step 3:** Implement with SDK primitives (`Panel`, `Field`, `Input`, `Select`, `Button`, `Notice`, `Tabs`, `RegisterTable`, `Tag`). Classes go on plugin-owned wrappers with the `forgejo-` prefix.
- [ ] **Step 4:** Run `pnpm -C ui test && pnpm -C ui build && go test ./internal/server/`. Expected: PASS.
- [ ] **Step 5:** Commit `feat: rebuild the setup wizard on the plan model`.

### Task 10: Docs

**Files:** `README.md`, `docs/setup.md`

- [ ] **Step 1:** `docs/setup.md` covers the screens S0–S8 as built.
- [ ] **Step 2:** The README covers:
  - what an install creates, in Forgejo and in AInsel, by name;
  - the site-admin token (used once, never stored) and the required plugin group access (`set_plugin_group_access`);
  - "Before you install": an existing hand-made trio on the same org reacts to the same events, so disable its triggers or webhook yourself;
  - Remove install and uninstall;
  - with #146, managed resources become read-only and are deleted with the plugin;
  - that every tool set is editable within the guard rails.
- [ ] **Step 3:** Run `make test build && bash chart/tests/render_test.sh`. Expected: PASS.
- [ ] **Step 4:** Commit `docs: document the new setup flow`.
