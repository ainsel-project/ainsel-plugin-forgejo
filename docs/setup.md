# Setup wizard

The setup wizard creates a complete Forgejo trio for this plugin: one refiner, one
developer and one reviewer, each with its own Forgejo bot account, its own AInsel
role image, its own tool grant on its own agent, its own predefined persona, its
own model, and its own connector, forge MCP server and triggers.

Everything an install uses is created by the install. The wizard never adopts or
edits a resource it did not create; *copy from existing* reads another resource
once to prefill a field and leaves it untouched.

The wizard is admin-only. Every `/api/setup/*` route first requires the plugin
credential (401 without it) and then a caller who is a site administrator on
AInsel (403 otherwise).

Run it once per Forgejo organisation. The same slug again opens **edit install**
rather than creating a second install.

## What one install creates

`<slug>` is the install's short name, chosen in S1 and used in every name;
`<role>` is `refiner`, `developer` or `reviewer`.

| Where | Name | What it is |
|---|---|---|
| Forgejo | `<role>-<slug>` | the bot account — full name "AInsel <Role> (<slug>)", private visibility |
| Forgejo | `ainsel-<role>-<slug>` | the bot's team: that role's permissions on every repository of the org |
| Forgejo | one org webhook | type `forgejo`, on the connector's endpoint, events `issues`, `issue_comment`, `pull_request`, `pull_request_review` |
| AInsel | `forgejo-mcp-<slug>` | the forge MCP server, `tokenFromEnv: FORGEJO_TOKEN` |
| AInsel | `forgejo-<role>-<slug>` | the role image: its container, `mcpServers` = [`forgejo-mcp-<slug>`, `plugin-forgejo`], the bot token in `FORGEJO_TOKEN`, the tool **catalog** the role's grant is validated against, and `TURN_TIMEOUT_MS`, the turn timeout chosen on the roles step (`7200000` on the developer image, `3600000` on the refiner and reviewer images by default). No skills and no tool selection: both belong to the agent |
| AInsel | `forgejo-<role>-<slug>` | the role persona, from the plugin's predefined text with the install's bot logins and address filled in |
| AInsel | `forgejo-<role>-<slug>` by default | the agent (display name — the one name you may change), on the role image and persona, with `skills` = [`forgejo-forgejo`] and `enabledTools` = the role's tool set, spelled the way the image's catalog spells them |
| AInsel | `forgejo-<slug>` | the connector that receives the webhook (`signatureHeader: X-Forgejo-Signature`) |
| AInsel | `forgejo-<role>-<what>-<slug>` | the six event triggers: refiner × issue opened, issue reopened, mention; reviewer × pull request opened, mention; developer × mention — plus the seventh, the developer's PR patrol cron trigger `forgejo-developer-pr-patrol-<slug>` |
| the plugin | the install row, keyed by slug | the ids of everything above except the triggers — those are recognised by their slug-derived names, not stored — the bot logins, the exclude label, the tool sets, `webhookVerifiedAt` and the event coverage. No secret. |

**Hub version.** Skill selection belongs to the agent: an agent's `skills` is a plain list
of skill ids and an image carries none. This plugin speaks that shape, so it needs a hub
that does too — and on a cluster, the `agents.ainsel.dev` CRD and the existing `Agent`
objects have to be migrated before either side moves (the hub's upgrade guide, "Upgrade
order"). An install whose skills were enabled only at image level keeps them once this
plugin re-applies it: the agent's own selection is the only one the hub reads.

### Who owns what the wizard creates

Every AInsel resource the wizard creates — the MCP server row, the personas, the role
images, the agents and the connector — is stamped `managedBy: plugin-forgejo` at
create. The hub then shows them as plugin-managed and refuses console writes of them
with 409: edits, secret rotations and deletes all answer "managed by
plugin-forgejo". The wizard is the only way in, and it writes as the plugin's own
identity — the one caller the hub lets write a stamped resource — on apply, on an
edit, on **Repair connection**, on **Remove install** and on the re-apply loop.

Two honest gaps:

- **Triggers are not stamped.** The hub stamps no trigger, so the six event triggers
  and the patrol cron trigger stay console-editable even though their connector is
  not. Deleting one from the console leaves an install quietly incomplete — change
  triggers through the wizard.
- **The stamp is set at birth only.** No API can move `managedBy`, so installs
  created before this change stay console-managed until they are removed and
  re-created by the wizard. Admin changes to a stamped install go through the
  wizard, never the console.

## S0 — Installs

The wizard opens on a table of installs. Each row says what the install is and
what it made:

| Column | Contents |
|---|---|
| **Install** | the slug, the organisation and the address. |
| **Results** | what the install created — bot accounts, teams, agents, agent images, personas, triggers, the MCP server, the connector, the webhook — counted from the ids on the install row. Every kind shows, zeros included: a row saved half way through the wizard, or left by a failed apply, legitimately has gaps, and a missing count is shown de-emphasised, never as a warning. The trigger and patrol counts are a convention — apply makes the six event triggers and the patrol as one batch, so a complete row reads Triggers 6 and Patrol 1, and a row that is not there reads —. |
| **Webhook** | **✓ Verified** when a real delivery was seen, **Waiting for first event** until then. |
| **Coverage** | which event families the org has actually delivered — `issues`, `issue_comment`, `pull_request*` (reviews count as the pull family). Written by the [background loop](#the-re-apply-loop), so it reads "seen", not "live": it can lag a real delivery by up to one pass. Nothing shows until the first event arrives — the webhook column already says that. |
| **Actions** | **Edit**, **Connect** (while unverified) and **Remove**. |

- **New install** starts the flow below.
- **Edit** opens the same flow prefilled with what the install is.
- **Connect** opens S7 for an install that was created but never verified.
- **Remove** asks for confirmation, and optionally for a Forgejo admin token —
  see [Removing an install](#removing-an-install).

## S1 — Instance

| Field | Notes |
|---|---|
| Install slug | `^[a-z][a-z0-9-]{0,29}[a-z0-9]$`, and globally unique — every name in the table above is built from it. An existing slug opens "edit install" instead. |
| Group | the AInsel group every resource of this install is filed under; only groups where you are an owner or a writer are offered. |
| Forgejo instance | a **selection** over the instances configured on the [Settings page](#settings--per-role-configuration) — the address the org webhook, the persona and every link in the work view are built from. There is no field to type an address into: a plan naming an address outside the list is refused, and a new install has to pick one of the listed instances, so an install cannot point at a host nobody configured — nor at no host at all. With exactly one instance configured it is carried for you; with none, the step stops and points at Settings. |
| Organization | the Forgejo organisation the bots, the teams and the webhook live in. |

The group is checked twice before you can continue. You have to be able to write
it, and the plugin's **background identity** has to be allowed to act for it —
otherwise the install would be created now and unreconcilable for ever. A group
the plugin cannot act for is refused on this screen with the admin action that
fixes it: grant the group to the plugin in the AInsel console under
**Plugins → Forgejo → Group Access** (Edit groups, then Save), or with
`set_plugin_group_access`, or `plugin.groupAccess` in the chart.

## S2 — Forge MCP server

The endpoint URL of the forge MCP server this install's agents will use. The
install always registers its own `forgejo-mcp-<slug>` pointing at it. The server
behind the URL is deployed by the operator; the implementation this install expects
is the upstream Forgejo MCP server,
[`agentic-forges/forgejo-mcp`](https://git.b4mad.industries/agentic-forges/forgejo-mcp).

*Copy from an existing server* picks any MCP server already registered with
AInsel and fills the URL in for you. Nothing then links to that server; it is a
starting point only.

## S3 — Forgejo accounts

One field: a **Forgejo admin token** — a site administrator's personal access
token. The screen lists what the install will create in the organisation before
it asks for anything: the three bot logins and the three team names.

The token is used once, during Apply, and then dropped. It is never stored, never
logged and never returned in a response. The plugin needs it to:

1. check it (`GET /api/v1/user`, `is_admin` — a token belonging to a
   non-administrator blocks the plan);
2. create the three bot users, each with a random one-time password, a generated
   `<login>@ainsel.invalid` address, `must_change_password: false` and private
   visibility;
3. create one team per role with that role's permissions over all the org's
   repositories, and add the role's bot to it:

   | Role | `repo.code` | `repo.issues` | `repo.pulls` |
   |---|---|---|---|
   | refiner | read | write | read |
   | developer | write | write | write |
   | reviewer | read | write | write |

4. mint each bot's own token — `POST /api/v1/users/<bot>/tokens`, authenticated
   as the bot with its one-time password, because Forgejo mints a token only for
   the user who asks. Scopes: `write:issue`, `write:repository`, `read:user`,
   `read:organization`.

The minted token goes straight into that role's image environment as
`FORGEJO_TOKEN`; the one-time password is discarded on the spot. Neither is kept
by the plugin, and neither is shown to you.

Every role image additionally carries `TURN_TIMEOUT_MS`: the runner's per-turn
wall-clock cap, which bounds how long one turn may run — distinct from
`llm.maxTurns`, which counts turns. The value is a per-role wizard field (see
[S4](#s4--roles)); left alone, it ships at `7200000` (2 hours) on the developer
image and `3600000` (1 hour) on the refiner and reviewer images — the caps the
`apps` incident of 2026-11-15 showed the old 30/15-minute defaults needed to
reach, after turns that were still making progress were killed on the ceiling
(issues #20, #61). An agent-level value set by an admin overrides the image
value.

One cost comes with that write. The hub's image update **replaces** the image's
whole environment list, and the plugin cannot read the list back to merge into it,
so setting the turn timeout in the wizard restates the image as exactly
`FORGEJO_TOKEN` plus `TURN_TIMEOUT_MS`. Any other env entry you added to a role
image by hand is dropped by that write — the plan preview and the apply report say
so on the op (`rewrites the image env: only FORGEJO_TOKEN and TURN_TIMEOUT_MS
remain on it`). Add hand-set entries after the last timeout change, or re-add them
afterwards.

A login or a team that is already taken blocks the plan: the plugin never takes
over an account it did not create. When a taken name matches the plugin's own
naming for the slug, the block adds that a previous install may have left it
behind, and where to delete it (the hub, or Forgejo as an administrator).

## S4 — Roles

Three tabs, one per role. For each:

| Field | Notes |
|---|---|
| Runtime | the container image the role runs in. It has to name an explicit version — a tag such as `:8.0`, or a `@sha256:` digest: a ref with neither floats to `latest`, which may not exist and is never reproducible. Whitespace around it is trimmed before anything reads it, so a pasted `…:1.24` with a tab after the tag is stored clean; whitespace *inside* a ref is never a valid image URL, and blocks. The developer's container has to provide `bash`. *Copy from an existing image* fills it in, and a copied value is checked the same way. |
| Agent name | the agent's display name, default `forgejo-<role>-<slug>`. The only name you may change. |
| Tools | a scrollable checklist, with the shipped defaults for the role as the baseline. Editable within the guard rails below. Per role, **Check all** ticks every offered tool the rails allow (it never adds a forbidden one) and **Uncheck all** clears the whole selection, a forbidden-but-checked tool included. |
| Working hours | optional, and per role — there is no *same for all roles* tick here, because three agents moving together is three decisions. Tick it and the role's agent works a window: which days, from when to when, in which zone. Unticked, the agent works any hour, which is what an install without a window means. |
| Turn timeout | minutes one turn of this role's agent may run, written to the role image as `TURN_TIMEOUT_MS`. Prefilled with the shipped default (120 for the developer, 60 for the refiner and reviewer); any whole number of milliseconds from 1 minute to 1 day is accepted. On an edit the field starts at the value the install was last written with, and **empty means keep** — a re-apply never resets the image's timeout, so a value set by hand on the image survives. Setting it, though, restates the image's whole env list as `FORGEJO_TOKEN` plus `TURN_TIMEOUT_MS`, dropping any other entry the image carried (the `TURN_TIMEOUT_MS` paragraph under [S3](#s3--forgejo-accounts) spells that out). The plan shows the effective value per role before anything is written. |

| Provider key | optional, and per role — the one secret this step asks for, because it is the only place a key can be given before the role's agent exists. It is used only while the role's provider holds one (`custom`, `openai`, `anthropic`, `google`); for any other provider the plan drops it, and an empty field is not a write of any kind. What you type goes to the hub as the agent's secret and is kept nowhere here. |

The LLM each role runs on is not asked here: the provider and model are per-role
settings on the [Settings page](#settings--per-role-configuration), edited after
the install exists and pushed to the agents by the reconcile pass. The key is the
exception — it has no row in the settings table to wait in, so the step above is
where a brand-new agent gets one.

**The tool sets are editable, inside two fixed limits.** The shipped defaults are
the baseline, and an admin's set has to respect the rules of least privilege:

- the refiner and the reviewer can never have `bash`, `create_branch`,
  `delete_branch`, `create_file`, `update_file`, `delete_file`,
  `create_pull_request` or `update_pull_request` — those are the developer's;
- `create_pull_review` and `submit_pull_review` belong to the reviewer alone (the
  developer can *request* reviews, not cast them).

A set that breaks one of these rules blocks the plan, and the message names every
violating tool at once. Turning off a tool the role's persona writes as its own —
`create_issue_comment`, for all three — is a warning, not a block: the install
works, the agent just cannot do what its persona says it does.

**Where the selection lives.** The set you pick becomes the agent's
`enabledTools`, not a switch on the image. The image's `tools` is the catalog of
what the runtime ships — the hub validates each granted name against it by exact
match — and an entry the catalog holds disabled restricts no agent. So Apply
refreshes the image's catalog and then writes the role's set onto the agent, in
the catalog's own spellings (`mcp__forgejo-mcp-<slug>__<tool>`, and `bash` under
its bare name, which the wizard declares by hand because the hub discovers no
shell tools). Every grant also names the plugin's own two read-only tools —
`mcp__plugin-forgejo__list_work` and `mcp__plugin-forgejo__get_work_item` — declared
into the catalog the same way: the runtime reads a grant as exhaustive across every MCP
server the agent has, so a name left out of it is a tool switched off, and these two are
promised to every role. That promise is the state an Apply leaves behind: the background
pass reads the catalog and never writes it, so on an install an older plugin version
created — whose catalog never carried these two — it grants the rest and leaves the pair
out, naming them in the log, until the next Apply declares them. An agent with no grant runs with everything the catalog offers, so
Apply never writes an empty one: a role whose set the catalog resolves to nothing
is a failed apply, not a wide-open agent.

**Working hours are the agent's own window, not the install's.** AInsel keeps the
window on the agent: the days it works, and the opening and closing time in a zone
of your choosing (`Europe/Berlin`, say — an empty zone means UTC). A day whose close
is earlier than its open runs past midnight into the next day, which is how a night
shift is written. Days are the weekdays the agent works; an empty set is every day.

What the wizard writes is what *it* last wrote, and that is the whole rule. The plan
diffs the form against the install row's stored window, never against the live agent,
so **a window set in the AInsel console is never read into the row and never
overwritten by an apply** — an edit that leaves the box unticked says nothing about
hours, and the console's window stands. Ticking it and filling it in writes the
window and records it on the row; ticking a window the row already carries, then
ticking it off, clears it — the apply says *clear* and the agent goes back to any
hour. Leaving the box unticked on an install whose window the wizard wrote is not a
clear: it is silence about hours, and the stored window stands.

Blocking here: a role with no runtime container; a runtime with no tag and no digest
(the step refuses it before the preview and names the roles that need the fix); a
runtime with whitespace inside it (around one is trimmed, not blocked); two
roles ending up with the same agent name; a window whose zone is not a zone AInsel
knows, whose times are not `HH:MM`, or whose days are not weekdays; a turn timeout
outside 1 minute to 1 day or not a whole number of milliseconds.

## S5 — Rules

One field: the **exclude label** (default `do-not-refine`). An issue carrying it
is skipped by both refiner issue triggers, so a human can park work the agents
should not touch yet. It changes nothing else.

## S6 — Preview

The named list of everything Apply will do, in the order it does it: the bot
users and teams, the MCP server, the personas, the images, the agents, the
connector, the six event triggers with their filters spelled out, the developer's
PR patrol cron trigger, and the org webhook.
On an edit, the list is what changes, field by field (`llm model: "old" -> "new"`,
`tools: +create_issue_comment -list_repo_commits`). A window is one of those fields:
a create names the agent's hours on the line it prints (`; working hours Mon–Fri
09:00–17:00 Europe/Berlin`), and an edit says
`working hours: "Mon–Fri 09:00–17:00 Europe/Berlin" -> "Mon–Thu 10:00–16:00 UTC"`,
or `working hours: "Mon–Fri 09:00–17:00 Europe/Berlin" -> "any hours"` when the box was
ticked off. An edit that says nothing about hours writes nothing about hours.

Alongside it, the verdict:

- **Blocking** — anything Apply would fail on or refuse to do: an invalid or taken
  slug, a group you cannot write or the plugin cannot act for, a token that is not
  an administrator's, an organisation that does not exist, a taken login, team or
  AInsel name (if the name matches the plugin's convention for the slug, the block
  notes that a previous install may have left it behind and where to delete it), a
  missing runtime or key, a runtime with no tag and no digest on a new install or one
  an edit changes, a runtime with whitespace inside it, a guard-rail violation. Apply
  is not offered while anything
  blocks; the list stays visible so you can see what the install would create once
  the reason is fixed.
- **Warnings** — the install will work, but not the way the shipped persona
  expects. An edit that keeps an untagged runtime the install already carries, or one
  with whitespace inside it, is a
  warning, not a block: the install stays editable, and the line names the tag or
  digest to add.

The preview is not a guess at what Apply will do: it is the same function, run
against the same snapshot, that Apply runs. The preview writes nothing.

## Apply

Apply creates in one order — accounts, MCP server, personas, images (created,
then tools refreshed, then profiled), agents, connector, triggers, org webhook —
and stops at the first error. Everything that run created is then deleted again
in reverse order, and the answer names the step and the service's own words for
why it failed. If a deletion in that rollback fails, the leftover is named by
kind and name rather than left unmentioned, and S0's **Remove install** is the way
to finish the job.

The webhook secret is created by AInsel when the connector is created and handed
straight to Forgejo in the same request. It is never shown in this flow and never
stored.

An agent created on the **Sign in with ChatGPT** method arrives with no credential
of its own, so apply finishes with a note naming it: open the agent in the AInsel
console and complete the sign-in there. Until you do, that role runs nothing — the
plan warns the same thing before apply, and the re-apply loop leaves the agent
alone while it waits.

## Running the setup from an MCP session

The same three steps are also MCP tools — `setup_options`, `setup_plan` and
`setup_apply` — for an operator working from an agent session instead of the
browser. They run the wizard's own service, the same code the `/api/setup/*`
routes run, so the answers are the same bytes: the options, the plan with its
verdict, and the apply report or its failure. The leftover sweep
([Cleaning up leftovers](#cleaning-up-leftovers-the-orphan-sweep)) is two more
tools on the same service: `setup_orphans_plan` and `setup_orphans_remove`.

Both transports clean the answer the same way before anything reads it: whitespace
*around* the slug, the org, the Forgejo address, the exclude label and each role's
runtime, agent name and turn timeout comes off, so a runtime pasted as `…:1.24` with a
trailing tab is stored as `…:1.24` rather than as an image no task pod can start from.
The `adminToken` and the `groupId` are the two fields left exactly as sent.

The tools are declared `"operator"` in the manifest's `mcp.audience`, so the hub
offers them to operator sessions only — an agent run does not get them. Planning
and applying an install is what a human watching the wizard does, and the
`adminToken` argument is the caller's own site-admin token, offered by the
operator and never read from the plugin's settings.

What each takes:

- **`setup_options`** — nothing. It answers the caller's writable groups, the
  plugin groups, the configured Forgejo instances and each role's default and
  forbidden tools, exactly as `GET /api/setup/options` does.
- **`setup_plan`** — the wizard's request: `slug`, `groupId`, `baseUrl`, `org`,
  `adminToken`, optionally `excludeLabel` and a `roles` object keyed `refiner`,
  `developer`, `reviewer`. Each role takes `runtime`, `agentName`, `tools` and
  `workingHours` — `{timezone, days, start, end}`, with the days as Go weekday
  numbers (`0` is Sunday, and `7` reads as Sunday too). Omit a role for the shipped
  defaults, and an unknown role is refused rather than quietly defaulted. `baseUrl`
  has to name one of the instances `setup_options` listed — an address nobody
  configured is refused, naming it. A blank `baseUrl` keeps an existing install's
  own address on an edit; on a create it is refused while any instance is configured.
- `workingHours` is read as an object or not at all, and the two are different answers.
  **Omitting** it means *any hour*, the same thing a role with no window means on the
  console — and on an edit that is how a window the wizard wrote before is **cleared**:
  the plan diffs the answer against the install row, so an omitted window against a row
  that records one is a clear, not a silence. What it never touches is a window an admin
  set in the AInsel console, because the diff reads the row and never the live agent.
  An **empty object** is not a fourth answer: every field of the window is optional, so
  `{}` goes on the wire as the hub's own clear marker and behaves exactly like the omit —
  to the hub "every hour of every day" and "no window" are the same state. A full object
  replaces the window. A window that is not an object — a string, a list — is refused as
  a bad argument; a window that is an object but nonsense, a zone that does not exist, is
  blocked by the plan rather than failing on the apply.
- **`setup_apply`** — the same request. It creates for real and rolls back on
  failure, exactly as the apply route does.
- **`setup_orphans_plan`** / **`setup_orphans_remove`** — `slug`, `baseUrl`,
  `org`, `adminToken`. The plan lists what the slug left in Forgejo and deletes
  nothing; the remove re-derives the same set and deletes it, reporting what
  went and what failed. Run the plan first — the remove is annotated
  destructive, and the plan is what makes a convention-named resource nameable
  before it is deletable.

The refusals are the routes' refusals, named the same way: no credential is
refused with `missing credential`, a caller who is not a site administrator with
`admin only`, and a group the caller cannot write with `groupId` — the request
names the field that failed, the way the routes have always answered.

And the rule the preview button enforces holds on the tool path too: **an apply
whose plan is blocked is refused with the plan itself** — the refusal is the plan
JSON, verdict included, because the block only reads against the thing it blocks.
Fix what it names, plan again, apply.

No answer from any of the three carries a secret: the admin token is used for the
calls and dropped, and the settings keys are never repeated back — the same R4
the wizard holds, pinned by a test that plants sentinels in every secret field
and reads the raw tool answers for them.

## S7 — Connect Forgejo

Apply has already created the organisation's webhook. Setup is not done until
Forgejo proves it can reach the connector.

1. Make Forgejo send one event: open an issue, or comment on one, in any
   repository of `<org>`. Forgejo cannot test an org hook for you — it has no test
   endpoint in its API and no **Test Delivery** button for org hooks, only for
   repository hooks — so this has to come from you. The link the screen gives you,
   `<baseURL>/org/<org>/settings/hooks/<id>`, shows the same deliveries in Forgejo.
2. The screen polls the plugin for events on the connector every 3 seconds. The
   first one that arrives proves the URL and the secret (a bad signature is
   dropped before anything is stored) and marks the install **verified**.
3. Nothing after 60 seconds: the plugin cannot see Forgejo's delivery status, so
   the screen points at the hook's **Recent Deliveries** in Forgejo — a 401 or 403
   there means the secret, a connection error means the URL or the network — and
   offers **Retry** or **Continue**. Continuing is fine: the install is already
   complete, and the background loop marks it **verified** when the first event
   arrives. **Connect** on the installs list brings you back here.

Which event families have actually arrived (`issues`, `issue_comment`,
`pull_request*`) is recorded separately, by the [background re-apply
loop](#the-re-apply-loop), not by this screen.

If the hook or its secret is ever lost, `POST /api/setup/installs/<slug>/repair-connection`
with an admin token rotates the connector's secret and patches this install's hook
to match.

## S8 — Done

The install is created and linked. The self-test: open a throwaway issue in the
organisation, watch the AInsel dashboard, and the refiner comments on it.

## Settings — per-role configuration

The wizard installs; the **Settings** page (admin only) is where the configuration
the plugin owns lives: three roles × twelve fields — persona text, skill text, the
LLM the role's agent runs on (the custom provider's URL and key, one key row per
vendor provider, and how an `openai` role authenticates), and the install's forge
MCP row — global per role, no per-install overrides. Four of those twelve are provider
keys, and none of them has a row in the plugin's settings table: a key's home is the
hub's per-agent secret, so a saved key is written straight onto the role's agents and
the table keeps no copy of it. Every other saved value lands on the agents, personas
and MCP row with the next reconcile pass ("Check now" runs one on the spot).

| Field | Notes |
|---|---|
| Persona text / Skill text | the role's persona and its section of the workflow skill. An edit that drops the skill's SKILL.md frontmatter is refused. |
| LLM provider | a **dropdown** of the providers the plugin defines: `ollama-cloud` (the shipped default), `opencode`, `alibaba-cloud`, `openai`, `anthropic`, `google`, `custom`. The list ships with the plugin — a value outside it is refused on save, so a typo cannot silently mis-route the role's model. A provider stored by an older version that is not on the list stays visible and selectable in the dropdown, labeled as such, until the admin moves it onto a listed one. |
| LLM model | free text — the model asked of the chosen provider. |
| Custom provider URL | free text — the OpenAI-compatible endpoint a role on `custom` talks to. Required while the role's provider is `custom`: a save that moves a role onto `custom` without naming a URL (in the same batch or already stored) is refused, naming the field. |
| Vendor provider keys (OpenAI, Anthropic, Google Gemini) | one write-only key row each, under the same contract as the custom provider key below. A key is used only while the role's provider names its vendor; a key typed for a provider the role does not name is refused, naming the field. The hub holds the key itself (blocks `openAI`, `anthropic`, `google` on the agent) — the plugin stores none of it and records only its fingerprint on the install row. **Needs a hub that knows these providers** (ainsel-project/ainsel#242): an older hub accepts the write and ignores the block, so the key never reaches the agent and nothing reports it — check the hub version before relying on a vendor key. |
| OpenAI auth method | a **dropdown** with the two ways a role on `openai` authenticates: `APIKey` (the shipped default, and what the `llmOpenAIKey` row feeds) or `ChatGPTSignIn`, which sends no key at all and waits for an admin to sign the agent in in the AInsel console. The two are mutually exclusive: a save that picks the sign-in while naming an OpenAI key in the same batch is refused, naming the method row — the hub answers that pair with a 400. A key the agent already holds under a role that moves to the sign-in is left where it is and never sent; the hub offers no way to clear an agent's key. The value means something only while the role's provider is `openai`; elsewhere it is stored and never written. **Needs a hub that knows the sign-in method**: an older hub accepts the write and ignores the field, leaving a keyless agent that reads as an API-key one. |
| Custom provider key | write-only — typed on save, written onto the role's agents, never served back. The console's answer always shows it empty, and the row never reads as edited: the plugin keeps no key to have edited. Saving it empty says nothing at all — the agents keep the key they hold — and there is no reset, because the hub has no way to clear an agent's key; typing a new one over the old is the only write the row makes. The role has to have an installed agent to hold the key: a key for a role nobody installed is refused, that field alone, and the rest of the batch saves. Optional: a key-less local endpoint is legitimate. The wizard's Roles step carries the same optional key, which is how a key reaches an agent that does not exist yet. |
| MCP server URL / MCP token env var | where the install's forge MCP server lives, and the *name* of the env var its token arrives in — never a token itself. The server itself is the operator-deployed upstream [Forgejo MCP server](https://git.b4mad.industries/agentic-forges/forgejo-mcp). |

The winner rule is visible here: an edited field carries an "edited" tag and
survives upgrades; an untouched one follows the shipped default. **Reset**
re-adopts the default, and a dirty field older than the running plugin shows what
the new version ships before you decide.

### Check now

The **Check now** button in the page header answers two questions in one click.
The first is drift: one reconcile pass over every install compares the agents,
personas and MCP row against these settings and writes back what moved — the
answer lists what it fixed and what the hub refused, exactly as the periodic pass
logs it.

The second is reachability: a pass only compares strings, so a provider name that
resolves to nothing reads as "correct" to it. After the pass, the plugin asks the
hub — once per distinct (provider, model) pair the roles name, not once per role —
whether that pair can actually serve a request. Only the hub can ask: the provider
name resolves to a base URL and a key hub-side, and the plugin holds neither; the
question carries the two strings and no secret. Each answer arrives in one of three
states, reported per role beside the pair it is about:

| State | Meaning |
|---|---|
| `ok` | the hub resolved the provider and the model answered. |
| `unreachable` | the hub asked and the pair could not serve — the hub's own failure sentence rides along. |
| `unknown` | the question was not answered — an old hub without the check route, a refused call (the answer names the `llm:check` permission to approve), or a `custom` pair, which the hub cannot probe from a provider/model name alone. Never rendered as health. |

"Checked: nothing to fix." appears only when the pass is clean **and** every check
came back ok; a flagged check warns like a refused write does, and the header's
checked tag counts flags. A flagged check is a diagnostic, not drift: it never
becomes a refusal and never blocks anything — and saving a field is never gated on
reachability, because a provider that is down right now does not make the name the
role names wrong. The reachability question is asked only when you press the
button: the periodic pass never pings a provider on a timer.

### Forgejo instances

The same page carries a second, smaller collection: the Forgejo addresses an
install may be created against. The wizard's S1 selects over this list, and a plan
that names an address outside it is refused — which is the point: a mistyped
address used to become an install pointed at the wrong host, with the typo only
visible when a webhook failed to arrive.

| | |
|---|---|
| Name | what the wizard shows beside the address, so two Forgejos are told apart by more than their hostname. |
| Base URL | the address itself, stored without its trailing slash. `http://` or `https://`, host required; the scheme, host and path are kept exactly as typed, because a Forgejo under a subpath is a different address. |

The list is written whole — one save replaces every row, and the write lands only
if every row is valid, so a bad row cannot half-save. A refused save names the row
and the half of it it refused. Duplicate addresses are refused: two rows for one
Forgejo would be two names for the same install to choose between.

An empty list is legal, and it means the wizard has nothing to offer: S1 says so
and points back here rather than letting an install be created against nothing.

The `FORGEJO_BASE_URL` the plugin starts with seeds this list **once** — on the
first start of a plugin that has never had a list. It is not re-applied after that,
so an admin who edits or empties the list is not overwritten by the next restart.

Each install keeps the address it was created against, and the plugin talks to that
address for that install: two installs on two Forgejos are served by two clients,
and the work view builds each run's links from the Forgejo the event arrived on.

## Editing an install

Opening an install from S0 runs the same flow prefilled. Blank fields keep what
is stored, so an edit only has to name what it changes:

- the slug, and therefore every derived name, cannot change;
- an edit creates no accounts, so it needs no admin token — the logins and teams
  follow the slug;
- an empty LLM key says nothing: the agents keep the key the hub holds. A key is asked
  for again only when the provider changed, because that is the only case the key the
  agents hold does not fit;
- a changed exclude label rewrites the two refiner issue triggers; a changed base
  URL rewrites the three personas, which name the forge's address;
- the base URL is chosen from the configured instances, and leaving the choice
  untouched keeps the address the install already has — even an address that is
  not on the list, which the step flags rather than overwriting;
- hours are the same story: the form's window is diffed against the window the row
  carries, so an edit writes a window only when the wizard's own box changed. A
  window the plugin never wrote — one set in the AInsel console — is not on the row,
  so no edit of the wizard can overwrite it or clear it.

The stamped resources cannot be edited from the console while the install stands —
the hub refuses with 409 — so this edit flow is the only way to change them. It
writes as the plugin, the named manager, which is the one caller the hub lets
through.

## Removing an install

**Remove install** on S0 deletes the install's own resources: the connector, the
agents, the role images and the personas of the install, then the install row.
Its six event triggers and the patrol cron trigger are deleted with it, by name.
The `forgejo-mcp-<slug>` registry row is the one thing removal leaves in AInsel
today; delete it in the console.

Because the stamped resources refuse console deletes, this wizard removal is the
only removal path for them — which is the point: a half-removed install is exactly
the half-edit the stamp exists to prevent. The removal writes as the plugin, so it
succeeds where the console would be refused.

With a Forgejo admin token it also deletes the organisation's webhook, the three
teams and the three bot users — which revokes the tokens those bots were
minted. Without one — or when a Forgejo delete fails — those items are kept:
the answer lists them as a checklist and **keeps the install row**, because the
row is the only record of which hook, teams and bots belong to the install. The
removal is then retried: ask again with a token and the same targets are
deleted, the checklist empties and the row goes. Deletes are tolerant of a
resource the last pass already removed, so a retry converges rather than
re-reporting what is gone.

## Cleaning up leftovers (the orphan sweep)

A row can still be lost with Forgejo items under it — the fresh-start cutover
deleted the whole store, and an operator who cleared a database by hand can
lose rows in flight. What is left then has nothing in the plugin naming it:
no row says which hook, teams and bots were the install's.

S0 carries a **Clean up leftovers** panel for exactly that. It is two steps,
always in that order:

1. **Plan** — name the lost install's `slug`, the Forgejo instance and the
   organisation, with a site-admin token. The sweep first reads the install store:
   a slug that still has a row there is an install, not an orphan, and the sweep
   is refused. What is left is looked for by the three bot logins
   (`<role>-<slug>`), the three team names (`ainsel-<role>-<slug>`) and the org
   hooks whose URL points at the hub — the same naming convention the wizard
   creates with, and the one hook tell that survives a lost row. Anything another
   row still references — a bot login, a team id, a hook id — is left out of the
   list, so the plan can tell a live install's resources from an orphan's. Every
   candidate is listed, by kind and name, before anything goes.
2. **Delete** — removes exactly that set. The delete re-derives the list
   server-side from the slug rather than trusting a list sent back to it, so
   the plan is a rendering of what delete will do, not a ticket that scopes it.
   It reads the store the same way, so a resource a row still points at is never
   deleted even if the plan was read at a different moment.

One caution the plan cannot answer: a convention name can also be one a human
chose by hand. Read the plan before deleting. The sweep needs the hub's own
address configured (`HUB_URL`) to claim any hook; without it it names users and
teams and leaves every hook alone. The instance must be one of the configured
ones, like the wizard's plan, and the token is a body field, never a URL.

The same two steps are the MCP tools `setup_orphans_plan` and
`setup_orphans_remove` — two tools for the same reason: the delete only makes
sense after the plan has been a visible, separate call.

## The re-apply loop

While the plugin runs, it re-applies every install's managed state once at start
and again whenever its settings are saved — the pass is triggered, not scheduled,
and there is no periodic timer — as its own background identity: the tool grant on each
agent, each agent's image and persona, the six event triggers, the patrol cron
trigger, and the event coverage.
Drift is set back and each correction is logged against the install
("corrected `enabledTools` on `forgejo-refiner-apps`"). The grant is the row's
set and nothing else, so a tool an admin handed an agent by hand is switched off
by the same pass that switches a missing one on.

An install whose group the plugin can no longer act for is skipped and logged;
nothing else about it is touched. The loop only ever writes to the resource ids
recorded on an install's own row — never another install's resources, and never a
hand-made agent that happens to have a similar name.

A working-hours window is not on that list, and the loop does not set one back. It is
the wizard's field and the console's field: whoever wrote it last owns it, and a pass
that rewrote it would be a pass that overwrote one of them.

Because the coverage figures come from this loop, what S0 and the work view show
can lag a real delivery by up to one pass.
