# AInsel Forgejo Plugin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A plugin with id `forgejo` that shows what AInsel agents did and are doing on Forgejo (repo → issue → PR → runs, with time, cost and a link to Forgejo everywhere), and that sets up the refiner/developer/reviewer trio, connector, triggers and Forgejo MCP through a wizard.

**Architecture:** Copied from `ainsel-plugin-template` (one Go binary serving the manifest, UI, `/api`, `/mcp`). The domain logic is pure, testable Go: event subject extraction, PR→issue linking, work aggregation and trigger generation. It talks to AInsel only through a `hub.Client` interface. Tests use a fake. The real implementation uses the contracted scoped credential and hub proxy (phases 1–5); only the durable run subject is still open. Install state lives in SQLite on the chart's PVC.

**Tech Stack:** Go 1.26, mcp-go v1.2.1, `modernc.org/sqlite` (no cgo), React 19 + Vite 8 + Vitest 5, pnpm 9, Helm 3.

**Spec:** `https://github.com/ainsel-project/ainsel-plugin-template/blob/main/docs/design/2026-10-22-first-plugins-design.md`, §4a, §4b, §4c (Forgejo parts), §5 n/a, "AInsel-side additions".

**Before starting:** this plan was aligned on 2026-10-23 with AInsel's `feat/plugin-system` branch — plugin system phases 1–5 implemented, phase 6 specified. Bootstrap from the template's `main` (aligned in template PR #10) and re-read the AInsel code as it then stands. Tasks marked **[BLOCKED: …]** are what is still open; implement the rest against AInsel's actual contract, not a stub.

## Global Constraints

- Everything from the template's Global Constraints applies (manifest rules, externals, `Plugin` CR, commits).
- Plugin id `forgejo`; module `github.com/ainsel-project/ainsel-plugin-forgejo`; image `ghcr.io/ainsel-project/ainsel-plugin-forgejo`; MCP tools appear in AInsel as `forgejo_<tool>`.
- **Only work AInsel touched.** A forge issue or PR is shown only if an AInsel run has it as its subject. Don't fetch forge data that no run touched.
- Every issue, PR and repo shown in the UI, API or MCP carries its Forgejo URL: issue `<base>/<owner>/<repo>/issues/<n>`, PR `<base>/<owner>/<repo>/pulls/<n>`, repo `<base>/<owner>/<repo>`.
- No plugin database beyond the install store; work data is read from AInsel each time.
- The wizard never creates anything on Forgejo; it creates or adopts AInsel resources only.
- **Contracted, implement for real:** the plugin credential (`ainselp_` JWT, 5-minute TTL, background mint endpoint), the hub proxy prefix `/api/v1/plugins/<id>/{ui,api,mcp}`, the forwarded headers (`X-Ainsel-Credential`, `X-Ainsel-Context`, `X-Ainsel-Invocation-Id`), group context, page→`component` mapping, slot props, admin page visibility, keyless signing. Still open: durable run subject (addition 2), installable `@ainsel/plugin-sdk` (template #2), local dev loop (template #5), published Go module for the manifest (template #7). Tasks marked **[BLOCKED: …]** implement against the interfaces here with fakes and finish when the named item lands.
- Manifest permissions declare only the exact v1 vocabulary, and only what is used: `invocations:read`, `observability:read`, `agents:read`, `agents:write`, `agent-images:read`, `agent-images:write`, `personas:read`, `personas:write`, `connectors:read`, `connectors:write`, `triggers:read`, `triggers:write`, `mcp-servers:read`, `mcp-servers:write`. `mcp-servers:*` is for the **Forgejo MCP server** the wizard registers with the bots' tokens (the forge's own MCP, which the agents use to work on Forgejo) — not for this plugin's `/mcp`, which the hub registers itself (phase 6). No `skills:*`: the hub registers manifest skills itself (AInsel phase 6, Task 7).

## Review Focus

1. **PR without an issue reference.** It must appear as a standalone item, not dropped and not attached to issue 0. Pinned in Task 3.
2. **Same number, PR and issue.** Forgejo shares one number space, so `#265` the PR and `#264` the issue it closes must not merge or double count cost. Pinned in Task 4.
3. **Run with no subject** (cron, panel, matrix connectors). Excluded from the work view and never shown as `/issues/0`. Pinned in Task 2.
4. **Adopting existing resources.** Re-running setup for an org whose connector already has triggers must not create duplicates. Pinned in Task 5.
5. **Repo names with dots and dashes, and base URLs with or without a trailing slash or subpath** (`https://git.example.com/forgejo/`). Links must still be exact. Pinned in Task 2.

---

### Task 1: Bootstrap from the template

**Files:** everything from `ainsel-plugin-template` at its first release tag, renamed; plus `internal/store/` (Task 6) and the chart's `persistence` (below).

- [ ] **Step 1:** Copy the template tree (no `.git`, no `docs/design`, no `docs/plans`). Rename per the template's `docs/getting-started.md`: id `forgejo`, module path, chart name `forgejo`, image, UI API base `/api/v1/plugins/forgejo/api` — only `/api/v1` is published by the ingress, and the console's api client is fixed at `${BASE_URL}api/v1/plugins/<id>/api`. Keep the template's `internal/hub` package (`CallerFromRequest`, `CredentialFromRequest`, `NewClient`, `GetJSON`, `StatusError`); Tasks 8 and 9 build on it. The UI keeps plain `fetch` against the full prefix — the SDK is not installable (template #2). Run `grep -rn hello-world .`. Expected: no hits.
- [ ] **Step 2:** Delete the hello feature (`internal/api/hello*`, `HelloPage`, `HelloAgentPanel`, the `hello` MCP tool) and keep the server, MCP wiring, UI build, chart and CI.
- [ ] **Step 3:** Manifest:
  ```json
  { "id": "forgejo", "displayName": "Forgejo", "version": "0.1.0", "pluginApi": "1.1",
    "description": "What AInsel agents do on Forgejo, and a quick setup for them.",
    "ui": { "entry": "/ui/index.js",
      "pages": [ { "path": "/now", "label": "Now", "section": "Forgejo", "icon": "activity", "component": "NowPage" },
                 { "path": "/work", "label": "Work", "section": "Forgejo", "icon": "git-pull-request", "component": "WorkPage" },
                 { "path": "/setup", "label": "Setup", "section": "Forgejo", "icon": "settings", "component": "SetupPage", "visibility": "admin" } ],
      "slots": { "invocation.panel": "ForgejoInvocationPanel", "agent.panel": "ForgejoAgentPanel" } },
    "api": { "basePath": "/api" }, "mcp": { "path": "/mcp" },
    "permissions": ["invocations:read", "observability:read", "agents:read", "agents:write",
                    "agent-images:read", "agent-images:write", "personas:read", "personas:write",
                    "connectors:read", "connectors:write", "triggers:read", "triggers:write", "mcp-servers:read", "mcp-servers:write"] }
  ```
  `pluginApi: "1.1"` is what gives pages their `component` (export name) and `visibility`. Only the four known slot ids are accepted; `api.basePath` must stay `/api` so the Task 9 paths line up with what the proxy forwards. The manifest is strict-decoded (256 KiB cap), so the phase-6 fields `skills` (Task 7) and `mcp.audience` (Task 10) are added only once phase 6 is merged.
- [ ] **Step 4:** Chart: keep the template's plugin pod label `ainsel.io/plugin: "true"` (AInsel's `plugins` NetworkPolicy selects plugin pods on it, and the hub only accepts ingress from labelled pods), its `networkPolicy.allowFrom` (`hub-backend`, `k8s-event-source-gateway-operator`), `hub.url` → env `HUB_URL` (default `http://hub-backend:8080`) and `plugin.groupAccess` (the background identity acts only for these groups). Add `persistence: { enabled: true, size: 1Gi, storageClass: "" }`, a PVC mounted at `/data`, env `DATA_DIR=/data`, and `forgejo: { baseURL: "" }` passed as `FORGEJO_BASE_URL` (used as the default in the wizard). Extend `chart/tests/render_test.sh` to assert the PVC, the mount and the plugin pod label.
- [ ] **Step 5:** Run `make test build && bash chart/tests/render_test.sh`. Expected: PASS. Commit `chore: bootstrap the forgejo plugin from the template`.

### Task 2: Forge adapter: event subjects and links

**Files:** Create `internal/forge/subject.go`, `internal/forge/links.go`, tests next to them, and `internal/forge/testdata/*.json` (trimmed real payloads: `issues`, `issue_comment` on an issue, `issue_comment` on a PR, `pull_request`, `pull_request_approved`, `push`).

**Interfaces:**
```go
type Kind string // "issue" | "pull"
type Subject struct { Repo string; Number int; Kind Kind; Title string; Branch string; Body string }
func SubjectFromEvent(eventType string, payload []byte) (Subject, bool)
func IssueURL(base, repo string, n int) string
func PullURL(base, repo string, n int) string
func RepoURL(base, repo string) string
func SubjectURL(base string, s Subject) string
```
- `issue_comment` on a PR has both `issue` and `pull_request` in the payload: Kind is `pull`. `pull_request*` events take Branch from `pull_request.head.ref` and Body from `pull_request.body`. `push`, `installation_*` and unknown events return `false`. Repo is `repository.full_name`.

- [ ] **Step 1: Failing tests:** one table row per fixture (expected Repo, Number, Kind, Title); `push` returns false. Link table: base `https://git.example.com`, `https://git.example.com/`, `https://git.example.com/forgejo/` with repo `apps/lingo-swipe` and repo `a.b/c-d` produce exact URLs; Number 0 makes `SubjectURL` return `""`.
- [ ] **Step 2:** Run `go test ./internal/forge/`. Expected: FAIL. **Step 3:** Implement. **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat: extract issue and pr subjects from forgejo events`.

### Task 3: PR → issue linking

**Files:** Create `internal/work/link.go`, `internal/work/link_test.go`.

**Interfaces:** `func LinkedIssue(title, branch, body string) (int, bool)`.
Precedence: a closing keyword in body (`(?i)\b(close[sd]?|fix(e[sd])?|resolve[sd]?)\s+#(\d+)`), then title `#N` in a Conventional-Commit scope (`^\w+\(#(\d+)\)`), then branch `^[\w-]+/(\d+)-`. Otherwise false.

- [ ] **Step 1: Failing tests:** `("feat(#264): search the word list","feat/264-word-list-search","")` → 264; `("fix: x","fix/896-configuration-summary","")` → 896; body `"Closes #12"` beats title `feat(#9)` → 12; `("chore: bump deps","renovate/deps","")` → false; `"#abc"` → false.
- [ ] **Step 2–4:** Run, implement, PASS. **Step 5:** Commit `feat: link pull requests to the issues they reference`.

### Task 4: Work aggregation

**Files:** Create `internal/work/work.go`, `internal/work/work_test.go`.

**Interfaces:**
```go
type Run struct { ID, Agent, Role, Status, Connector string; Start time.Time; End *time.Time; CostUSD float64; Subject forge.Subject }
type Item struct { Subject forge.Subject; URL string; Runs []Run; Duration time.Duration; CostUSD float64; LastActivity time.Time; PRs []Item }
type RepoGroup struct { Repo, URL string; Items []Item; CostUSD float64 }
func Build(base string, runs []Run) []RepoGroup
func Active(runs []Run) []Run // Status "pending"|"running", newest first
```
- Runs group by (Repo, Number, Kind). PR items nest under their linked issue's `PRs` when `LinkedIssue` finds one and that issue is in the set. If the issue has no runs, an issue item with no runs is created as the parent (its URL still set), so the PR is never orphaned. The issue's totals are its own runs only, and the repo total sums every run once. Items sort by LastActivity, newest first. Runs with `Subject.Number == 0` are dropped. Duration is the sum of `End-Start` over finished runs.

- [ ] **Step 1: Failing tests:** PR #265 `feat(#264)` + issue #264 → one repo, one item #264 with PRs=[#265]; the repo cost equals the sum of both items' run costs, with no double counting; PR #300 without a reference stays top-level; a run with Number 0 is absent; `Active` keeps only pending/running.
- [ ] **Step 2–4:** Run, implement, PASS. **Step 5:** Commit `feat: aggregate agent runs into issue and pr work items`.

### Task 5: Setup model and trigger generation

**Files:** Create `internal/setup/install.go`, `internal/setup/triggers.go`, `internal/setup/triggers_test.go`, `internal/setup/testdata/dev-apps-triggers.json` (the owner's current triggers for connector `c-724d9d68`, as the golden file).

**Interfaces:**
```go
type Role string // "refiner" | "developer" | "reviewer"
type Install struct {
  Slug string                 // e.g. "apps", used in names
  BaseURL string
  ConnectorID string
  Agents map[Role]string       // agent ids
  Bots map[Role]string         // forgejo logins, e.g. refine-agent
  ExcludeLabel string          // default "do-not-refine"
  CISender string              // optional, e.g. "maestro-bot"
}
type Filter struct { Field, Op, Value string; Values []string `json:",omitempty"` }
type Trigger struct { Name, AgentRef, ConnectorRef string; Filters []Filter }
func Triggers(in Install) []Trigger
func Missing(want, existing []Trigger) []Trigger // by (AgentRef, ConnectorRef, filter set), names ignored
```
Before trusting the golden file, check every filter field (`issue.labels`, `comment.body`, `comment.user.login`, `trigger_user_login`) and each `Op` against AInsel's actual trigger API — the filter vocabulary is not part of the plugin contract files.

Pattern (from the dev instance), names `trigger-<role>-<what>-<slug>`:
- refiner `issue-opened` / `issue-reopened`: `type eq issues`, `action eq opened|reopened`, `issue.labels not-contains <ExcludeLabel>`.
- refiner `mention`: `type eq issue_comment`, `comment.body contains @<Bots[refiner]>`, plus `comment.user.login neq <bot>` for each of the three bots.
- reviewer `mention`: the same with `@<Bots[reviewer]>`.
- developer `mention`: `type eq issue_comment`, `comment.body contains @<Bots[developer]>` (no author filter: the other bots hand off to it).
- reviewer `pr-opened`: `type eq pull_request`, `action eq opened`.
- developer `ci-failure` (only if CISender set): `trigger_user_login eq <CISender>`.

- [ ] **Step 1: Failing tests:** `Triggers` for slug `apps` with the dev bots and `maestro-bot` equals the golden file (filters order-insensitive); without CISender there are 6 triggers, with it 7; `Missing(Triggers(x), golden)` is empty; `Missing` with one golden trigger removed returns exactly that one.
- [ ] **Step 2–4:** Run, implement, PASS. **Step 5:** Commit `feat: generate the agent trigger set for a forgejo install`.

### Task 6: Install store

**Files:** Create `internal/store/store.go`, `internal/store/store_test.go`.

**Interfaces:** `func Open(dir string) (*Store, error)` (file `forgejo.db`, schema created on open); `Save(ctx, setup.Install) error` (upsert by Slug); `List(ctx) ([]setup.Install, error)`; `ConnectorIDs(ctx) ([]string, error)`; `Delete(ctx, slug string) error`. Maps go in as JSON columns.

- [ ] **Step 1: Failing tests** (temp dir): save → list round-trips; saving the same slug twice updates; `ConnectorIDs` is deduplicated; reopening the dir keeps the data.
- [ ] **Step 2–4:** Run, implement, PASS. **Step 5:** Commit `feat: persist forgejo installs in sqlite`.

### Task 7: Default personas and agent skill [BLOCKED in part: skill registration needs AInsel phase 6]

**Files:** Create `content/personas/{refiner,developer,reviewer}.md`, `content/skills/forgejo/SKILL.md`, `content/content.go` (`//go:embed`), `content/content_test.go`.

**Interfaces:** `func Persona(r setup.Role) string`, `func Skill() string`.
- The skill is **not** created through the hub. Declare it in the manifest — top-level `skills: [{ "name": "forgejo", "description": "…", "path": "/skills/forgejo/SKILL.md" }]` — and serve the file at that path; the hub fetches it from the plugin's verified endpoint (≤ 64 KiB) and registers it read-only as `forgejo-forgejo` (`<id>-<name>`, ManagedBy plugin). No `CreateSkill` call. **[BLOCKED: AInsel phase 6]** — personas and the content itself proceed.
- Persona texts start from the owner's current Forgejo personas (export them with the AInsel MCP `get_persona` for the refiner, developer and reviewer agents; strip install-specific names). The skill explains the Forgejo MCP tools, the `@mention` handoff between roles, the branch naming `<type>/<issue>-<slug>`, the PR title `<type>(#<issue>): …` and the `Closes #<issue>` body. These conventions feed Task 3. Refer to the MCP tools by their bare names (`list_work`, `get_work_item`): operators see them hub-prefixed as `forgejo_list_work`, so a skill body must not hard-code a prefixed name.

- [ ] **Step 1: Failing test:** each persona and the skill is non-empty, and the skill contains `Closes #` and `(#`. **Step 2–4:** Run, write, PASS. **Step 5:** Commit `feat: ship default personas and the forgejo skill`.

### Task 8: Hub client interface [BLOCKED in part: durable run subject (addition 2)]

**Files:** Create `internal/hub/hub.go` (interface + types), `internal/hub/fake.go` (in-memory fake used by tests), `internal/hub/http.go` (real client).

**Interfaces:**
```go
type Client interface {
  Runs(ctx context.Context, connectorIDs []string, since time.Time) ([]work.Run, error) // runs with subject + cost
  ListPersonas(ctx) ([]Ref, error); CreatePersona(ctx, name, body string) (Ref, error)
  ListAgents(ctx) ([]Ref, error);   CreateAgent(ctx, AgentSpec) (Ref, error)
  ListAgentImages(ctx) ([]Ref, error)
  ListConnectors(ctx) ([]Ref, error); CreateConnector(ctx, name string) (Connector, error) // Connector{Ref; WebhookEndpoint, WebhookSecretValue string}
  ListTriggers(ctx, connectorID string) ([]setup.Trigger, error); CreateTrigger(ctx, setup.Trigger) error
  ListMCPServers(ctx) ([]Ref, error); RegisterMCPServer(ctx, MCPServerSpec) (Ref, error) // the Forgejo MCP server, with the bots' tokens
}
type Ref struct { ID, Name string }
```
No `IsAdmin` (Task 9 reads the caller from the request), no skill method and nothing for this plugin's own `/mcp` (the hub registers both itself — phase 6, Tasks 7 and 10). `ListMCPServers` / `RegisterMCPServer` are only for the Forgejo MCP server (`POST /api/v1/mcp-servers`, `mcp-servers:write`); check the request shape for its tokens against AInsel's MCP server API when the task runs. `CreateConnector` maps `POST /api/v1/connectors`'s `webhookEndpoint` and `webhookSecretValue` (returned once, never persisted).

`NewClient` uses the `HUB_URL` from the chart and sends `Authorization: Bearer ainselp_…`. Hub calls made for a console user forward that request's credential (`hub.CredentialFromRequest` → `X-Ainsel-Credential`); work with no user behind it (webhooks, polling, sync — not `apply`, which runs inside the user's request) uses the **background** credential: `POST {HUB_URL}/api/internal/plugins/forgejo/credentials` → `{"token","expiresAt"}`, 5-minute TTL, answered only from the plugin's verified pod IPs; cache it and re-mint 30 s before `expiresAt`. Background calls act only for the groups in chart `plugin.groupAccess`.

`Runs` uses `GET /api/v1/invocations` (agent, connector, eventId, status, times), `GET /api/v1/events/{id}` for the subject via `forge.SubjectFromEvent`, and `GET /api/v1/observability/metrics/tokens/by-event` for cost — the route gate matches by prefix, so everything under `/api/v1/observability/` (`metrics/tokens/by-event`, `metrics/tokens/by-subject`, `conversations`) is allowed with `observability:read`, and `/api/v1/invocations/…` and `/api/v1/events/…` with `invocations:read`. Once AInsel's durable run subject exists (addition 2), switch to it. Events past retention: Subject has Repo and Number from tasklogs, empty Title (spec: "title unknown").

- [ ] **Step 1:** Write `fake.go` with the behaviour later tasks' tests need (records creations, returns seeded data).
- [ ] **Step 2:** `http.go`: build on the template's `hub.NewClient` / `GetJSON` / `StatusError` and implement the methods against the contract above, including the background token source. Leave one `// blocked:` line for the durable run subject (addition 2).
- [ ] **Step 3:** Run `go vet ./... && go test ./...`. Expected: PASS. Commit `feat: define the hub client the plugin needs`.

### Task 9: Backend API

**Files:** Create `internal/api/work.go`, `internal/api/setup.go`, tests next to them; modify `internal/server/server.go` to mount them.

**Interfaces:** `func NewWork(h hub.Client, s *store.Store, base string) http.Handler`, `func NewSetup(h hub.Client, s *store.Store) http.Handler`.
- `GET /api/now` → `{ runs: [ {…Run, url, issueUrl?} ] }` (`work.Active`)
- `GET /api/work?repo=&agent=&since=` (default 30 days) → `{ repos: []work.RepoGroup }`
- `GET /api/work/{owner}/{repo}/{number}` → one item, or 404
- `GET /api/agents/{name}/work` → items this agent touched (for the agent slot)
- `GET /api/invocations/{id}/work` → the item of that run plus its siblings' total cost (invocation slot)
- `GET /api/setup/installs`, `GET /api/setup/options` (existing personas/agents/images/connectors/MCP servers), `POST /api/setup/plan` (body: wizard input → returns the resources to create or adopt and the triggers diff, without side effects), `POST /api/setup/apply` (performs the plan — personas, agents, connector, Forgejo MCP registration with the bots' tokens, missing triggers — saves the install, and returns `webhookEndpoint` + `webhookSecretValue` per created connector to show once). The Forgejo MCP server, the plugin's own MCP server (`plugin-forgejo`) and the `forgejo-forgejo` skill are attached to the agents it creates by name; the hub registers them (phase 6).

Every `/api/setup/*` route is gated on the per-request caller, not on a hub round-trip: `hub.CallerFromRequest(r)` must be present with `Kind == "user"` and `IsAdmin == true`, otherwise 403 — fail closed when there is no caller (standalone/dev mode forwards without credential or context, and a user the hub does not know gets no context either). Never ask `GET /api/v1/plugin-context` for `isAdmin` with a background credential: it is computed for user callers only. The proxy strips every client-supplied `X-Ainsel-*`, so the context header is unspoofable.

- [ ] **Step 1: Failing tests** with `hub.Fake`: `/api/work` groups as in Task 4 and every item has a `url`; non-admin `POST /api/setup/apply` → 403 and no caller at all → 403; `apply` with "use existing connector" creates no connector and only the `Missing` triggers; `apply` with "create new" returns the fake's webhook endpoint and secret once and saves the install (the secret is not persisted); `plan` performs no creations.
- [ ] **Step 2–4:** Run, implement, PASS. **Step 5:** Commit `feat: add work and setup api`.

### Task 10: MCP tools

**Files:** Modify `internal/mcp/server.go`; test `internal/mcp/server_test.go`.

The hub registers this server itself as `plugin-forgejo` (ManagedBy plugin, read-only) and re-syncs it by hitting `mcp.path` directly every 30 s — so `/mcp` must serve stateless streamable HTTP without a user context. Register **bare** tool names; the hub exposes them to operators as `forgejo_<tool>`. AInsel phase 6 (merged) supports hub registration and the optional `mcp.audience` declaration (both tools here are read-only and default to audience `both`).
- `tools/list` arrives with the plugin's background credential: return all tools regardless of caller kind — the hub filters by audience.
- A tool call from an agent carries an agent credential and `X-Ainsel-Invocation-Id` but no `X-Ainsel-Context`. Handlers call the hub with that forwarded credential; a tool that needs the caller resolves it via `GET /api/v1/plugin-context` with the same credential. The background credential is only for work with no caller behind it.

**Interfaces:** `func NewHandler(version string, w *api.WorkService) http.Handler` (extract the read logic from Task 9 into `WorkService` so the API and MCP share it). Tools (read-only): `list_work` (args `repo?`, `agent?`, `since?`) → compact list with URL, cost, duration and run count; `get_work_item` (args `repo`, `number` required) → runs, cost and URL; a missing item is a tool error `not found`.

- [ ] **Step 1: Failing test** over JSON-RPC as in the template: both tools listed; `get_work_item` without `number` → error; with seeded data the result contains the Forgejo URL. **Step 2–4:** PASS. **Step 5:** Commit `feat: expose agent work as mcp tools`.

### Task 11: UI

**Files:** `ui/src/{index.tsx,api.ts,NowPage.tsx,WorkPage.tsx,SetupPage.tsx,ForgejoInvocationPanel.tsx,ForgejoAgentPanel.tsx}` and a test per component.

- Exports match the manifest `component` / slot names exactly: `ForgejoInvocationPanel({ invocationId, agentName })`, `ForgejoAgentPanel({ agentName })` — those are the contracted slot props (`invocation.panel` gets both, `agent.panel` only `agentName`). Pages receive **no props**; their export name is what `ui.pages[].component` points at.
- API calls go to `/api/v1/plugins/forgejo/api/<path>` with `@ainsel/plugin-sdk` (published at v1.1.0) via `usePluginContext().api`.
- Every issue, PR and repo renders as an `<a href={url} target="_blank" rel="noreferrer">`.
- The wizard follows spec §4b steps 1–8. Each resource step has a "Create new / Use existing" choice filled from `/api/setup/options`. Step 7 shows the `plan` diff before `apply`. The last step shows the webhook endpoint and secret with copy buttons and the warning that the secret is shown once; neither is persisted.
- The console exposes the host primitives through the import map and wraps every contribution in `PluginBoundary`, so failures stay per-plugin; `SetupPage` needs no client-side admin check of its own — `visibility: "admin"` strips it from the non-admin listing and the console redirects.

- [ ] **Step 1: Failing tests:** `WorkPage` with mocked `/api/work` renders the issue link `…/issues/264` and the nested PR link `…/pulls/265`; `SetupPage` shows `Only admins can run setup` when the API answers 403 (the fallback behind the nav/route gate); `ForgejoInvocationPanel` renders with `{ invocationId, agentName }`; the wizard's "Use existing" for the connector hides the create fields. **Step 2–4:** PASS. **Step 5:** Commit `feat: add now, work and setup pages`.

### Task 12: Docs and verification

- [ ] `README.md`: what the plugin does, what you create by hand on Forgejo (bot users + tokens, org webhook pasted from the wizard), install (`helm install … --set image.digest=…` — `spec.image.digest` is required and mutable tags are rejected), approve permissions, run the wizard, and the signing step: `plugins.requireSignature` defaults to true, so the installer trusts the release workflow with AInsel values `plugins.signing.keyless: [{ issuer: https://token.actions.githubusercontent.com, subjectRegExp: ^https://github.com/ainsel-project/ainsel-plugin-forgejo/\.github/workflows/release\.yml@refs/tags/v.*$ }]`.
- [ ] Status section: still open — durable run subject (addition 2, subject fallback for events past retention), per-run cost beyond 7 days (metrics limit), manifest as a Go module (template #7).
- [ ] `docs/setup.md` (wizard steps), `docs/work-view.md` (how runs map to issues and PRs, the linking rules from Task 3, "title unknown").
- [ ] Run `make test build && bash chart/tests/render_test.sh && docker build .`. Expected: PASS. Commit `docs: document setup and the work view`.
