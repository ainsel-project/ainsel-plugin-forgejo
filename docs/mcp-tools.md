# MCP Tools

The plugin speaks MCP at `POST /mcp`, and the hub discovers those tools with its own
background request and filters them by **audience** before offering them to anyone. Seven
tools, two audiences, one rule: nothing here writes to Forgejo.

| Tool | Audience | |
|---|---|---|
| `list_work` | both | The threads AInsel agents worked on, with their runs, cost and forge page. |
| `get_work_item` | both | One issue or pull request, and what ran on it. |
| `setup_options` | operator | What a setup can be built on: groups, instances, role defaults and rails. |
| `setup_plan` | operator | The changeset, with nothing written. |
| `setup_apply` | operator | Create the install for real. |
| `setup_orphans_plan` | operator | What a lost install row left behind. |
| `setup_orphans_remove` | operator | Delete exactly that. |

An agent reaches the first two through the hub as plain `list_work` and `get_work_item`;
an operator's agent reaches the same tools prefixed — `forgejo_list_work`,
`forgejo_setup_apply` — because two plugins can ship tools of the same shape. One
description has to serve both spellings.

## `list_work`

The work AInsel agents did on Forgejo: the issues and pull requests they ran on, each with
its runs, cost and page on the forge, newest activity first.

| Argument | |
|---|---|
| `repo` | Narrow to one repository by full name (`owner/repo`). Omit it for every repository this plugin is installed on. |
| `agent` | Narrow to one agent by name. What is left is still whole threads, with the other agents' runs cut out of them. |
| `since` | How far back: a whole number of days (`"7d"`) or a moment in RFC 3339. Defaults to the last 30 days. |

## `get_work_item`

One Forgejo issue or pull request: the runs that worked on it, what they cost, and its page
on the forge.

| Argument | |
|---|---|
| `repo` | **Required.** The repository's full name (`owner/repo`). |
| `number` | **Required.** The issue or pull request number — an integer, because a number typed as text is the common mistake and a schema that says integer lets a client catch it first. |

Both are annotated read-only, non-destructive, idempotent and closed-world: an absent hint
reads as the worst case, and both tools write nothing.

They are read-only in a second sense: they read the hub's record of work **as the caller**,
so a call that arrives with no credential is refused rather than answered from the plugin's
identity. They are in every role's tool set, which is what lets an agent ask what the fleet
is doing without scraping the forge — see
[The Forgejo Workflow Skill](workflow-skill).

## The setup tools

Five tools wrap the wizard's options, plan and apply, and the orphan sweep — the same
service the HTTP routes answer, so the console and an operator's agent cannot disagree.
Their audience is `operator`, and the caller rule is the same one the routes enforce: a
signed-in **admin user**, or `admin only`.

### `setup_options`

Read it first. It answers the AInsel groups an install can be filed under, the plugin
groups, the configured Forgejo instances (name and address), and each role's default and
forbidden tools. The plan and apply tools accept **only** the addresses it lists.

### `setup_plan`

What the setup would create for an organisation — Forgejo accounts and teams, AInsel agents,
images, personas, a connector and triggers — without writing anything. The answer carries the
verdict: a blocked plan is refused by the apply tool until it is fixed.

### `setup_apply`

Creates it for real. **Run `setup_plan` first** — an apply whose plan is blocked is refused
with the plan itself. It rolls back on failure.

| Argument | |
|---|---|
| `slug` | **Required.** Names the install and, through it, every resource it owns. Lowercase, and it cannot change afterwards. |
| `groupId` | **Required.** The AInsel group every resource is filed under; the caller must be an owner or writer on it. |
| `baseUrl` | **Required.** The Forgejo address the install points at, webhook and links included. Must be one of the configured instances. |
| `org` | **Required.** The organisation the bots, teams and hook live in. |
| `adminToken` | **Required.** A site-admin token for the accounts, teams and webhook. Never repeated in an answer. |
| `excludeLabel` | An issue carrying this label is left off the refiner. Empty means the shipped default. |

Per-role fields (model, provider, tools, working directory, working hours, turn timeout) ride
along with the same rails the console enforces: a forbidden tool is refused naming every
violation, and a tool name outside the catalogue is refused rather than accepted and silently
missing at runtime.

### `setup_orphans_plan` and `setup_orphans_remove`

For the case the install store cannot answer: a row is gone — a wiped database, the fresh-start
cutover — and the resources it named are still standing.

| Argument | |
|---|---|
| `slug` | **Required.** Names the leftovers by the plugin's own convention. A slug that still has a row is refused: it is an install, not an orphan. |
| `baseUrl` | **Required.** Must be a configured instance. |
| `org` | **Required.** The organisation they stand in. |
| `adminToken` | **Required.** Reads and deletes accounts, teams and webhooks. Never repeated in an answer. |

`setup_orphans_plan` lists what the convention identifies, and leaves out anything another
install's row still references — so the list is exactly what `setup_orphans_remove` would
delete. The remove tool re-derives the same set and deletes only that, reading the install
store again so a live install's accounts, teams and webhook are never touched. Run the plan
and read it first: **a convention name can also be one a human chose by hand.**

## What is not here

The agents' forge tools — `get_issue_by_index`, `create_pull_request`,
`submit_pull_review` and the rest — belong to the **Forgejo MCP server** each install
registers for (`forgejo-mcp-<slug>`), which is the upstream
[`agentic-forges/forgejo-mcp`](https://git.b4mad.industries/agentic-forges/forgejo-mcp)
deployed by you and pointed at by the wizard. The plugin never implements forge writes: it
creates the row, hands it the bot's token, and stays out of the way.

That split is why a grant's names have to match the catalogue the server actually
advertises — see [Roles and Tool Sets](roles-and-tools).

## Where to go next

- [HTTP API](api-reference) — the same surface over HTTP, which is what the console uses
- [Setup Wizard](setup) — the flow these tools wrap
- [Roles and Tool Sets](roles-and-tools) — which tools a role may hold
