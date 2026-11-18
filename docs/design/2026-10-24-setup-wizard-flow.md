# Setup wizard: role profiles, decision map and input surface

Status: **v7: decisions taken, facts verified, implemented on
`feat/setup-wizard-restructure`.** This version folds in the owner's answers of
2026-10-24 (§9 lists them). §10 holds what is still open. v3 is kept outside the
repo, for comparison only. Two rulings taken during implementation are folded
into §3.5, §3.6 and §5[7]; they are marked as such.

## 0. The idea in one paragraph

An install of the Forgejo plugin **owns a complete, fresh trio**: one refiner,
one developer and one reviewer for one Forgejo org. Each role has its own
Forgejo bot account (created by the plugin with a Forgejo admin token), its own image with exactly the tools that role needs, its
own predefined persona, and its own model and key. The install also owns its
own connector, forge MCP server and six triggers. The wizard creates all of it.
It never adopts or edits anything that already exists, but it may *copy
settings from* existing resources as a starting point. Everything it creates is
managed by the plugin: kept in shape by a background re-apply, read-only in the
console once AInsel #146 lands, and removed again when the install or the
plugin is removed.

## 1. What this fixes

| Symptom today | Cause |
|---|---|
| A role can do whatever its image allows | the wizard never sets which Forgejo tools a role gets; a "refiner" on a permissive image can push code |
| An adopted agent never gets the plugin's tools or skill | they are attached only to created agents. Moot now: nothing is adopted |
| The token lands under the wrong name | `FORGEJO_TOKEN` is hard-coded, while an existing forge server may read `FORGEJO_PAT`. Moot now: the install's own server always reads `FORGEJO_TOKEN` |
| Image required even when nothing is created; persona silently dropped for adopted agents | the fixed 9-step track. Moot now: there is one branch |
| A model can be picked but never paid for | `llm` is sent without the provider's key |
| A CI-sender field that means nothing on Forgejo | it mirrors a separate CI bot on the dev instance. Dropped |
| Setup "finishes" before Forgejo is connected | nothing confirms the webhook works |
| Two roles can share a bot account | nothing checks; users then can't tell which agent they talk to |
| Preview and apply disagree | two code paths |
| A taken name fails apply halfway as "hub unavailable" | no preflight; the hub's error is swallowed |
| Drift: someone edits a role agent and the trio silently changes | nothing re-applies the profile |

## 2. Rules

- **R1 — Ask only what a write or a filter reads.**
- **R2 — Preview is Apply's preflight.** One function computes both, and the
  preview writes nothing.
- **R3 — Names are derived from the slug**, except an agent's display name,
  which the admin may change (default `forgejo-<role>-<slug>`).
  Every AInsel-side name starts with `forgejo` so it cannot collide with the
  GitHub plugin's resources in the same namespace. The Forgejo-side bot logins
  live only in Forgejo and stay short (`<role>-<slug>`).
- **R4 — Secrets pass through.** The Forgejo admin token, LLM keys and the
  webhook secret are entered once and used once, never stored and never logged.
  The admin token lives only for the request that uses it. Bot tokens are
  minted by the plugin and handed straight to AInsel (the role image's env);
  the plugin never stores or shows them.
- **R5 — Fresh and owned.** The wizard creates every resource an install uses
  and never edits a resource it did not create. "Copy from existing" reads a
  resource once to prefill a field. It creates no link to that resource, and
  nothing is written to it.
- **R6 — Every claim is checkable**, and checked: the admin token's rights and
  free logins (Forgejo API), the webhook by a real delivery, tool profiles against
  the image's catalog, background access against the plugin's groups.
- **R7 — Least privilege per role.** Code-writing tools are the developer's
  only. Review submission is the reviewer's only. Shell is the developer's only.
- **R8 — One account per agent.** Each role's bot login is distinct, so people
  on Forgejo can see which agent they are talking to.

## 3. Role profiles

### 3.1 Where AInsel enforces a role's abilities (verified on AInsel `main`)

- **A role's tool selection is switched per agent** (`spec.enabledTools`), not per
  image. The image's `tools` is the **catalog** of what the runtime ships — the list
  the hub validates an agent's grant against, by exact match — and a catalog entry held
  disabled restricts no agent on its own.

  *Amended for issue #64.* This bullet read "MCP tools are switched per agent image
  (`tools[].enabled` on the image's discovered catalog), not per agent. An agent's
  `enabledTools` drives only container-kind tools" — true until the hub's
  `530070a` (tool and skill selection scoped to the Agent) and `7175690` (the runtime
  enforces the agent's selection with `ainsel-tools`). Writing the role's set onto the
  image has restricted nobody since; the grant has to be named on the agent.
- **Shell comes with the runtime container**: only an image whose container
  provides it lists `bash`.
- **The token reaches the forge MCP server** through the env var named by the
  server's `tokenFromEnv`. Env merges image defaults with agent overrides by
  name.
- **Updates replace lists** on both images and agents. A secret sent with an
  empty value keeps its stored value.

So **a role profile is a role image**, one per role per install. That's how
the dev instance is built today.

### 3.2 What an install owns

| Resource | Name | Content |
|---|---|---|
| Forge MCP server | `forgejo-mcp-<slug>` | the forge's MCP endpoint URL, `tokenFromEnv: FORGEJO_TOKEN` |
| Role image ×3 | `forgejo-<role>-<slug>` | runtime container; `mcpServers` = [`forgejo-mcp-<slug>`, `plugin-forgejo`]; `env` `FORGEJO_TOKEN` = that role's bot token (secret), plus `TURN_TIMEOUT_MS`, the per-turn wall-clock cap the roles step asks for (`7200000` on the developer image and `3600000` on the refiner and reviewer images by default; not `llm.maxTurns` — issues #20, #68); the tool **catalog** the role's grant is validated against — which entries it holds enabled is its own default (issue #64) |
| Role persona ×3 | `forgejo-<role>-<slug>` | the plugin's predefined text (`content/personas/<role>.md`), placeholders filled with the three bot logins and the base URL |
| Role agent ×3 | display name default `forgejo-<role>-<slug>` | `imageRef` = role image; `persona` = role persona; `skills` = [`forgejo-forgejo`]; `enabledTools` = the role's tool set, in the catalog's spellings (issue #64); `llm` = the role's provider and model, plus the provider's key |
| Connector | `forgejo-<slug>` | `signatureHeader: X-Forgejo-Signature` |
| Triggers ×6 | `forgejo-<role>-<what>-<slug>` | the set in §3.4, on the connector, referencing the agents by id |
| Install row | slug | group, every id above, bot logins, exclude label, tool profiles, `webhookVerifiedAt`, event coverage |

No `mem0`, no other MCP servers, no skills other than `forgejo-forgejo`: an
install covers Forgejo only, with Forgejo defaults.

### 3.3 Tool sets: defaults, editable

The plugin's own tools (`list_work`, `get_work_item`) and the `forgejo-forgejo`
skill go to every role. The default Forgejo tools come from the dev instance's
role images. The admin may add or remove tools per role in S4, with the
defaults shown as the baseline. The plugin's own tools and the R7 guard rails
cannot be removed:

| Role | Shell | Default Forgejo tools | Count |
|---|---|---|---|
| **Refiner**: reads issues and code, writes the refinement | no | get_issue_by_index, list_issue_comments, get_issue_comment, create_issue_comment, edit_issue_comment, update_issue, add_issue_labels, list_repo_issues, list_repo_labels, list_repo_contents, get_repo_tree, get_file_content, get_pull_request_by_index, get_my_user_info | 14 |
| **Developer**: branches, writes code, opens and updates the PR, follows CI | `bash` | the refiner's read tools, plus create_branch, delete_branch, list_branches, create_file, update_file, delete_file, create_pull_request, update_pull_request, get_pull_request_by_index, get_pull_request_diff, list_pull_request_files, list_repo_pull_requests, get_pull_review, list_pull_reviews, list_pull_review_comments, create_review_requests, issue_state_change, remove_issue_labels, delete_issue_comment, list_repo_commits, get_pull_request_status, list_workflow_runs, get_workflow_run, list_action_run_jobs, get_action_job_logs, check_notifications, mark_notification_read, mark_repo_notifications_read, list_my_repos, search_repos | 43 + `bash` |
| **Reviewer**: reads the PR and diff, submits a review | no | get_pull_request_by_index, get_pull_request_diff, list_pull_request_files, list_repo_pull_requests, list_repo_commits, list_branches, get_pull_review, list_pull_reviews, list_pull_review_comments, create_pull_review, submit_pull_review, get_issue_by_index, list_issue_comments, get_issue_comment, create_issue_comment, get_repo_tree, list_repo_contents, get_file_content, list_my_repos, search_repos, get_my_user_info, get_pull_request_status, get_commit_statuses | 23 |

Each role's set holds the CI reads its persona performs: the reviewer answers
"is this pull green?" with `get_pull_request_status` (falling back to
`get_pull_request_by_index` + `get_commit_statuses` on a server too old for the
tool), the developer chases a failing verdict down to `get_action_job_logs`.
A persona may not name a tool its role is not granted — the shipped reviewer
persona told the agent to call `list_workflow_runs`, which no reviewer set held,
so its CI check could never run.

**Guard rails (R7), enforced even when an admin edits a set:** the refiner and
the reviewer can't get `create_branch`, `create_file`, `update_file`,
`delete_file`, `delete_branch`, `create_pull_request`, `update_pull_request` or
`bash`. Only the reviewer gets `create_pull_review` and `submit_pull_review`.
Removing a tool the role's persona relies on (e.g. `create_issue_comment` for
any role) is a warning, not a block.

Tools match by their Forgejo name; AInsel's per-server prefix
(`mcp__forgejo-mcp-<slug>__<tool>`) is added by the plugin. The defaults ship as
data (`content/profiles/<role>.json`), pinned by a test against this table.

### 3.4 The six triggers

| Name | Agent | Filters |
|---|---|---|
| `forgejo-refiner-issue-opened-<slug>` | refiner | `type eq issues`, `action eq opened`, `issue.labels.name not-contains <excludeLabel>` |
| `forgejo-refiner-issue-reopened-<slug>` | refiner | same with `action eq reopened` |
| `forgejo-refiner-mention-<slug>` | refiner | `type eq issue_comment`, `comment.body contains @<refiner bot>`, `comment.user.login neq` each of the three bots |
| `forgejo-reviewer-mention-<slug>` | reviewer | the same with `@<reviewer bot>` |
| `forgejo-developer-mention-<slug>` | developer | `type eq issue_comment`, `comment.body contains @<developer bot>` (no author filter: the other bots hand off to it) |
| `forgejo-reviewer-pr-opened-<slug>` | reviewer | `type eq pull_request`, `action eq opened` |

### 3.5 How a role image comes to exist (verified 2026-10-24)

1. **Create** the image: `POST /api/v1/agent-images` with `imageURL`,
   `mcpServers` (`forgejo-mcp-<slug>`, `plugin-forgejo`), `env` (the token) and
   `enabledSkills`. Its tool list starts empty. Creating an image does not
   discover tools (checked: still 0 tools after two minutes).
2. **Discover**: `POST /api/v1/agent-images/{id}/refresh-mcp` (under
   `agent-images:write`). It lists each MCP server's tools synchronously and
   adds new ones **disabled**, keeping the existing flags. It connects without a
   token, so the forge MCP server must answer `tools/list` anonymously (the dev
   one does). `plugin-forgejo` can't be listed this way (it needs the hub's
   internal credential) and is reported as a per-server error, which the plugin
   ignores for that server only.

   *Ruling taken during implementation:* this call discovers **MCP tools only**.
   `bash` is a container tool, not one an MCP server reports, so it never shows
   up in the catalog this fills — which is what §3.1 already said from the other
   side ("shell comes with the runtime container").
3. **Profile**: `PUT /api/v1/agent-images/{id}` with `tools` — the catalog the refresh
   read back, flags as they came. Since issue #64 this is a catalog refresh and not a
   grant: the role's set goes to the agent as `enabledTools`, in the names this call
   reported. A tool from the set that the forge server didn't report is an error — the
   hub validates a grant against the catalog by exact match, so a name the catalog does
   not carry cannot be granted.

   *Ruling taken during implementation:* the developer image **declares** `bash`
   rather than discovering it — it goes in that same `tools` list as
   `{"name": "bash", "kind": "shell", "enabled": true}`. That declaration is the
   exception to "not in the catalog is an error"; it applies to `bash` for the
   developer alone, since R7 gives shell to no other role. The declaration has to land
   before the grant does: the agent can only be granted a name the catalog carries.

`plugin-forgejo`'s two read-only tools are not in the catalog a refresh discovers:
the hub cannot list the plugin's own server, so nothing discovers them. They are
therefore the second hand-declared entry after `bash` — apply puts
`mcp__plugin-forgejo__list_work` and `mcp__plugin-forgejo__get_work_item` into that
same `tools` list, and every role's grant names them. It has to: the runtime reads
`enabledTools` as exhaustive across every MCP server the agent has, so a grant that
left them out would switch them off, which §3.3 and README rule out (V1: they cannot
be switched off per role). The declaration is the exception to "not in the catalog is
an error", for these two names and `bash` alone. The background pass is the one
   exception to "every grant names them": it reads the catalog and never writes it, so a
   row an older plugin version created — whose catalog never carried these two — gets the
   rest of its grant with the pair left out and logged, and the pair arrives with the next
   apply. Naming them on such a catalog would be a write the hub refuses, and it rides the
   same `AgentUpdate` as every other correction that role is owed.

### 3.6 Keeping it in shape: background re-apply

The plugin re-applies every install's managed state once at plugin start and again
whenever its settings are saved; the pass is triggered, not scheduled, and there is
no periodic timer (design decision 8). "Managed state" means the image's env names,
MCP list and skills (the image's tool catalog is only read, to spell the grant —
nothing in the pass writes it); the agent's tool grant, image, persona
and llm; trigger filters; the connector's existence. Drift is set back, and each
correction is logged on the install ("corrected enabledTools on
forgejo-refiner-apps").

*Ruling taken during implementation:* **event coverage belongs to this loop and
to nothing else.** Each pass reads the connector's events since the last one and
folds each family's earliest arrival into the install row, so the loop is the
single writer of `coverage` and there is no second writer racing it from the S7
poll (§5[7] step 3). The cost is that what S0 and the Work page show can lag a
real delivery by up to one pass.

This is background work with no caller, so it runs as the plugin's own
identity. That identity acts only for the groups an admin granted with
`plugin.groupAccess` / `PUT /api/v1/plugins/forgejo/group-access`. S1 therefore
checks, through `GET /api/v1/plugin-context` with the background credential,
that the install's group is in the plugin's groups, and blocks with
instructions if it is not (§10 Q2).

Once AInsel #146 lands, every resource in §3.2 carries `managedBy` for the
plugin and is read-only in the console. Re-apply then becomes a safety net
instead of the main defence.

### 3.7 Removing an install, and uninstalling

- **Remove install** (S0 action): deletes the install's agents, images,
  personas, triggers, connector and forge MCP server through the hub, in
  reverse dependency order, then the install row. With an admin token, it
  also deletes the org webhook, the three teams and the three bot users
  (revoking their tokens). Without one, those go on a checklist.
- **Uninstall the plugin**: removes everything the plugin manages. With #146,
  the hub deletes every `managedBy` resource of the plugin when its `Plugin` CR
  is deleted. Before #146, the README tells the admin to run "Remove install"
  for each install first. The hub already removes the `plugin-forgejo` MCP row
  and the `forgejo-forgejo` skill row itself.

## 4. Screen map

```
   S0  Installs ─┬─ new install ────► S1 Instance       slug · group · base URL  (+ background-access check)
                 ├─ edit install ───► same flow, prefilled; Apply = reconcile to the new values
                 ├─ connect ────────► S7 (a not-yet-verified install)
                 └─ remove install ─► confirm → delete everything → Forgejo checklist

   S1 ─► S2 Forge MCP        endpoint URL  (copy from an existing server)
      ─► S3 Forgejo accounts admin token → the plugin creates 3 bot users, teams, tokens
      ─► S4 Roles ×3         container · tools (defaults, editable) · model + key · agent name
      ─► S5 Rules            exclude label
      ─► S6 Preview          everything that will be created, by name · verdict
      ─► Apply ─► S7 Connect Forgejo   add the webhook → proven by a real delivery
                ─► S8 Done
```

There is no connector screen: the connector is always new, and it appears in
S6 and S7.

## 5. Decision points

### [1] S1 — Instance

| Input | Notes |
|---|---|
| `slug` | `^[a-z][a-z0-9-]{0,29}[a-z0-9]$`; globally unique (an existing slug opens "edit install") |
| `group` | groups where the caller is owner or writer. **Blocks** if the plugin's background identity has no access to it (§3.6), with the exact admin action to fix that |
| `baseURL` | prefilled from `FORGEJO_BASE_URL`, normalised |

### [2] S2 — Forge MCP server

| Input | Notes |
|---|---|
| `mcp.url` | the forge's MCP endpoint. *Copy from existing server* prefills it from any registered server; nothing links to that server |

### [3] S3 — Forgejo accounts (with a Forgejo admin token)

One input: a **Forgejo admin token** (a site administrator's personal access
token with scopes `write:admin`, `write:organization`, `write:user`). It is used
during Apply and then dropped (R4). The plugin then does the account work
itself.

| Step (at Apply, in order) | Forgejo API | Result |
|---|---|---|
| check the token | `GET /api/v1/user` → `is_admin: true` | blocks in the preview if not an admin |
| create the bot users | `POST /api/v1/admin/users` with login `<role>-<slug>` and full name "AInsel <Role> (<slug>)", a generated email, a random one-time password, `must_change_password: false`, `visibility: private` | three distinct accounts (R8) |
| put them in the org | one team per role in `<org>` (`ainsel-<role>-<slug>`, readable to Forgejo admins as AInsel's) with that role's unit permissions (§10 Q7); `PUT /api/v1/teams/{id}/members/{user}` | each bot can reach the org's repos, as far as its role needs |
| mint each bot's token | `POST /api/v1/users/{bot}/tokens`, authenticated as the bot with the one-time password (Forgejo mints a user's token only for that user; V8), scopes for its role | the token goes straight into the role image's env; the password is discarded |

The S3 screen shows only what it will create (logins, teams, permissions) and
the admin token field. The preview checks the token, that the org exists, and
that none of the three logins or teams is taken. A taken login blocks: the
plugin never takes over an existing account (R5).

**Edit install:** account work needs the admin token again, but only when
something about the accounts changes (a renamed slug isn't possible; changed
team permissions are). Otherwise the field stays empty.

**Remove install:** with an admin token, the plugin deletes the three bot
users and the three teams (`DELETE /api/v1/admin/users/{bot}`), which also
revokes their tokens. Without one, the end screen lists them (and the webhook) for manual
deletion, including "revoke these tokens" (Q4).

### [4] S4 — Per role (refiner, developer, reviewer)

| Input | Notes |
|---|---|
| `runtime` | the container image URL. *Copy from existing image* prefills it. The developer's container must provide `bash`; this is checked after phase 1 against the catalog, and blocks |
| `tools` | the default set (§3.3) as a checklist; add or remove within the guard rails |
| `llm.provider` / `llm.model` | Ollama Cloud, OpenCode, Alibaba (Token Plan) or custom (+ `llm.url`). *Copy from existing agent* prefills provider and model (never the key) |
| `llm.key` | required for every keyed provider; pass-through. **"Use for all roles"** checkbox on the refiner step copies provider, model and key to the other two |
| `agent.name` | display name, default `forgejo-<role>-<slug>` |

The persona is predefined and not asked (§3.2). It becomes editable in a later
version.

### [5] S5 — Rules

| Input | Default | Changes |
|---|---|---|
| `excludeLabel` | `do-not-refine` | both refiner issue triggers |

### [6] S6 — Preview

A named list of everything that will be created, grouped as in §3.2, with the
trigger filters readable. On edit, the list is split into *create*, *change*
(field by field) and *delete* (e.g. a tool removed from a set).

| Verdict | Examples |
|---|---|
| blocking | slug invalid or taken; group not writable; background access missing; a token that fails verification; two equal logins; a missing key; a guard-rail violation in a tool set; a name already used in AInsel |
| warning | a persona-relied tool removed from a set |

### [7] S7 — Connect Forgejo (automatic, with the admin token)

Part of Apply, after the connector exists. Setup isn't done until Forgejo
delivers.

1. **Create the org webhook**: `POST /api/v1/orgs/{org}/hooks` with type
   `forgejo`, the connector's `webhookEndpoint` as URL, the
   `webhookSecretValue` as secret (handed from AInsel to Forgejo in the same
   request flow, never shown, never stored), `content_type: json`, events
   `issues`, `issue_comment`, `pull_request`, `pull_request_review` (reviews arrive as `pull_request_approved` / `_rejected` / `_comment`, V5), branch filter
   empty, `active: true`. The hook id goes on the install row.
2. **Prove it**: Forgejo has no way to test an org hook — no test endpoint in the
   API (V10: 404) and no *Test Delivery* button in its UI either, since it offers
   that one on repository hooks only. So the screen asks the admin for one real
   event instead: open an issue, or comment on one, in any repository of the org.
   Continuing is offered as well — the install is already complete — and the
   re-apply loop (§3.6) marks it verified on the first event it reads. The screen
   gives the direct link `<baseURL>/org/<org>/settings/hooks/<id>`. Then poll
   `GET /api/v1/events?connector=<id>&since=<start>`. An event proves URL and
   secret, since the receiver drops bad signatures before storing. ✓ sets
   `webhookVerifiedAt`. Nothing within 60 s → the API exposes no delivery status (V10),
   so the screen points at the hook's *Recent Deliveries* in Forgejo (401/403 =
   secret, connection error = URL or network) and offers Retry. Because the
   plugin wrote URL and secret itself, the likely causes are network and
   ingress, not typos.
3. **Coverage, after setup**: which event types have arrived (`issues`,
   `issue_comment`, `pull_request*`) is recorded by the background re-apply loop
   (§3.6) and nowhere else — *ruling taken during implementation*. This screen's
   own poll has the narrower job: the first event it sees sets
   `webhookVerifiedAt`, which is the proof that the hook reaches the connector —
   and so does the loop, for an install whose admin continued past this screen
   before anything arrived. The poll does not write coverage. S0 and the Work page
   show the coverage the loop wrote, so it lags a delivery by up to one pass.

The secret is never shown in this flow. If the hook is lost or its secret
drifts, "Repair connection" (on S0, admin token required) rotates the secret
(`POST /api/v1/connectors/{id}/rotate-secret`) and updates the hook
(`PATCH /api/v1/orgs/{org}/hooks/{id}`). **Remove install** deletes the hook
too, which removes it from the manual checklist.

### [8] S8 — Done

Everything created, linked. The self-test: open a throwaway issue, watch
`/now`, and the refiner comments.

**Partial failure at Apply:** stop at the first error, show the hub's own
message, then **roll back** what this Apply created (reverse order). Nothing is
adopted any more, so there is no other way to finish a half-created install. If
the rollback itself fails, the leftovers are listed by name and S0 offers
"Remove install".

## 6. Input surface

| # | Field | Screen | Lands in | Persisted |
|---|---|---|---|---|
| 1 | `slug` | S1 | every derived name | yes |
| 2 | `groupId` | S1 | every create | yes |
| 3 | `baseURL` | S1 | links; token verification | yes |
| 4 | `mcp.url` | S2 | forge MCP server | (in AInsel) |
| 5 | `forgejo.adminToken` | S3 | account creation, team setup, token minting, webhook; repair; removal | **never** |
| 6 | *(bot logins, derived)* | — | triggers, persona placeholders | yes |
| 7 | `roles.<role>.runtime` | S4 | role image `imageURL` | (in AInsel) |
| 8 | `roles.<role>.tools` | S4 | role image `tools[].enabled` | yes (the profile) |
| 9 | `roles.<role>.llm.provider/model/url` | S4 | agent `llm`, `customProvider.url` | (in AInsel) |
| 10 | `roles.<role>.llm.key` | S4 | agent provider key | **never** |
| 11 | `roles.<role>.agent.name` | S4 | agent display name | (in AInsel) |
| 12 | `excludeLabel` | S5 | refiner triggers | yes |
| 13 | *(webhook, automatic)* | S7 | hook id, `webhookVerifiedAt`, coverage | yes |

"Copy from existing" adds no fields. It prefills #4, #7 and #9.

## 7. Code surface

- `content/profiles/<role>.json` (default tool sets + guard rails), `content/personas/<role>.md` (exists).
- `internal/setup`: `Plan(input, snapshot) → {create, change, delete, verdict}`, used by `POST /setup/plan`, by apply and by the re-apply loop. The six triggers; `CISender` removed.
- `internal/forgejo` *(new)*: the Forgejo admin client: `WhoAmI`, `CreateUser`, `EnsureTeam` + `AddTeamMember`, `MintUserToken` (basic auth as the bot), `CreateOrgHook` / `UpdateOrgHook` / `DeleteOrgHook` / `TestOrgHook`, `DeleteUser`, `DeleteTeam`. These are the only Forgejo calls the plugin makes, all at setup or removal time, none for work data. Tokens and passwords are never logged.
- README: a "Before you install" note that an existing hand-made trio on the same org reacts to the same events; disable its triggers or webhook yourself (§9).
- `internal/hub`: images (create, update tools, read catalog), agents (create, update, delete), personas, connectors, triggers and MCP servers, each with create and delete; `PluginGroups()` with the background credential.
- `internal/reconcile` *(new)*: the background re-apply loop (§3.6) on the background credential.
- `internal/api/setup.go`: plan, apply with rollback, remove install, the S7 poll (`GET /setup/installs/{slug}/deliveries?since=`).
- `ui/src/setup/steps/*`: one component per screen, rendered from the plan model.
- Chart: no change to `plugin.groupAccess` defaults; the README explains that each install's group must be granted.

## 8. Facts: verified and still to check (2026-10-24)

| Fact | Status | Evidence / what is left |
|---|---|---|
| **V2** — created agents inherit the role image's MCP list | ✓ verified | AInsel `createAgent`: an absent `mcp` inherits the image's servers; the plugin creates every agent, so nothing overrides it |
| **V4** — a signed Forgejo delivery lands as an event on the connector | ✓ verified | the dev instance stores signed Forgejo `push` events (100 of the last 100 `*.push`), so a test delivery (a `push` payload) lands the same way |
| **V5** — event type names | ✓ verified, **with a correction** | stored types equal `X-Forgejo-Event`: `issues`, `issue_comment`, `pull_request`, and reviews as separate types `pull_request_approved` / `pull_request_rejected` (+ `pull_request_comment`). The webhook must subscribe to the review events too (`pull_request_review`), and the coverage check counts `pull_request*` as one family |
| **V6** — background credential groups | ✓ verified | `MintPluginBackgroundCredential` grants exactly `spec.groupAccess` (writer), and `GET /api/v1/plugin-context` returns those groups |
| **V7** — matching deletes exist | ✓ verified | `DELETE` is handled for agents, agent images, personas, triggers, connectors and MCP servers, all under the plugin route table's `*:write` |
| **V9** — the plugin pod can reach Forgejo | ✓ verified for the chart | the chart's NetworkPolicy is ingress-only and AInsel restricts no egress; a cluster with default-deny egress needs a rule (README) |
| **V1** — `plugin-forgejo` tools in an image's catalog | ✓ answered (code) | no: `refresh-mcp` lists servers without a token (`listMCPTools(ctx, url, "")`), and the plugin's agent-mcp endpoint needs the hub's internal credential. Its tools are always on for agents with the MCP row (§3.5) |
| **V3** — how the tool catalog is filled | ✓ verified + code | a new image stays at 0 tools (watched 2 min); the catalog is filled by `POST /api/v1/agent-images/{id}/refresh-mcp`, synchronously, adding new tools disabled. The 60 s wait is gone (§3.5) |
| **V8** — a bot mints its own token | ✓ verified | admin-created user (`must_change_password: false`) → `POST /api/v1/users/{bot}/tokens` over basic auth: 201. Admin `Sudo` minting: 401, so basic auth is the only route. If an instance disables API basic auth, S3 blocks with that reason |
| **V10** — org hooks over the API | ✓ verified | org, team (`units_map`), member, org hook create (type `forgejo`, secret, events incl. `pull_request_review`) and update all work. No way to test an org hook at all — no test endpoint in the API (404), and no *Test Delivery* button in the UI, which Forgejo gives for repository hooks only — and no delivery-status field on the hook. So S7 asks for one real event (an issue or a comment) rather than a test click, and the re-apply loop marks the install verified on the first event it reads; diagnosis lives in Forgejo's UI |

All ten facts are settled. `scripts/check-wizard-facts.sh` re-checks the live
ones (V3, V8, V10) against any instance, and cleans up after itself.

## 9. Decisions (owner, 2026-10-24)

| Question | Decision |
|---|---|
| Editable tool sets | **Yes**: the agents are managed by the plugin, so their tools are editable, from the defaults, within the R7 guard rails |
| Image per install or shared | Per install, because each agent needs its own Forgejo account so people know who they're talking to (R8) |
| Discovery wait | 60 s was decided; moot now, since discovery is a synchronous `refresh-mcp` call (V3). 60 s stays as the wait for the test delivery in S7 |
| Extras such as mem0 | None. An install covers Forgejo only, with its defaults |
| Background reconcile | **Yes, re-apply**: managed resources are kept in shape by the plugin |
| Uninstall | Removes everything the plugin manages (with #146: the hub deletes `managedBy` resources) |
| Existing resources | **Don't reference them.** The plugin creates everything fresh; "use existing" becomes *copy from existing* |
| Personas | Predefined only for now; editable later |
| Webhook | **Created by the plugin** with the admin token, then verified by a real delivery |
| Migrating a hand-made trio | A user issue, not the plugin's: a README note (§7) |
| Background access | S1 blocks until the install's group is granted to the plugin |
| Forgejo accounts | Created by the plugin with a Forgejo admin token (pass-through) instead of typed in |
| Token revocation on remove | Yes, on the removal checklist; automatic when an admin token is given (the bot users are deleted) |
| Model change on edit | The key is needed again only when the provider changes |
| LLM keys | Per role, with a "use for all roles" checkbox (provider and model go with it) |
| CI sender | Dropped |
| Shared bot login / agent | Blocked (R8); one agent per role by construction |
| Slug uniqueness | Global |
| Two installs on one connector | Impossible by construction (each install creates its own) |
| Empty sections | Not rendered |
| Secret rotation | Yes (lost secret, S7) |
| Names | AInsel-side names start with `forgejo` (no collision with the GitHub plugin); Forgejo logins `<role>-<slug>`, full name "AInsel <Role> (<slug>)" |
| Team permissions | As proposed: refiner code read / issues write / PRs read; developer code, issues, PRs write; reviewer code read / issues write / PRs write; on all repos of the org |
| Admin token | Site admin only in v1 |

## 10. Still open

Nothing. The remaining unknowns are facts (§8, V1–V10), checked on the dev
instance before the plan is written.
