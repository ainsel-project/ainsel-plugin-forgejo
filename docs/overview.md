# Overview

The Forgejo plugin is the half of AInsel that knows what AInsel agents did on a
Forgejo forge — and the half that sets them up. It shows repositories, issues,
pull requests and the runs that touched them, with time, cost and links back to
Forgejo, and it installs the trio of agents that works on an organisation: a
refiner, a developer and a reviewer.

It is an [AInsel plugin](https://github.com/ainsel-project/ainsel/blob/main/docs/writing-a-plugin.md):
one container, one manifest, its own UI bundle, its own MCP server, its own Helm
chart. It does not replace AInsel's console — it adds four pages to it.

## What it gives you

| | |
|---|---|
| **A picture of the work** | Every issue, pull request and run an AInsel agent touched, grouped by repository, with duration, cost and a link to the thread on Forgejo. See [Work View](work-view). |
| **A live board** | The last 24 hours across every install: what is running, what is queued, what just finished, and each agent's day. See [Live Board](live-board). |
| **A setup that finishes the job** | One wizard creates the whole install — three bot accounts and teams in Forgejo, three agents with their images, personas, tool grants, connector, MCP server and seven triggers, and the organisation's webhook. See [Setup Wizard](setup). |
| **Two tools for the agents themselves** | `list_work` and `get_work_item`, so an agent can ask what AInsel has been working on. See [MCP Tools](mcp-tools). |
| **A workflow contract** | The handoff conventions, branch and PR naming and issue-closing rules the three roles share, shipped as an AInsel skill. See [The Forgejo Workflow Skill](workflow-skill). |

## The install

An **install** is one organisation on one Forgejo, and everything the plugin had
to create for it: the trio, their bots, their tools, the connector that receives
the forge's webhook, and the triggers that wake the agents. Installs are keyed by
a short **slug** you choose, and every name the plugin creates is built from it —
so two installs, and the GitHub plugin beside them, never collide.

The wizard creates everything and adopts nothing. It never edits a resource it did
not create, and AInsel refuses console edits of the resources it did create, so
one path — the wizard — owns an install. See [Architecture](architecture).

Run one install per Forgejo organisation. [Installation](installation) covers
getting the plugin running; [Quick Start](quickstart) is the five-minute path
through both.

## The four pages

The plugin contributes four pages to the AInsel console, plus a panel on two
other screens:

| Page | Console path | Who sees it | What it is |
|---|---|---|---|
| **Live** | `/now` | everyone | The self-updating board of the last 24 hours. |
| **Issues & PRs** | `/work` | everyone | The work view: threads, runs, cost, links to Forgejo. |
| **Settings** | `/settings` | admins | Per-role configuration (persona, skill text, model, keys, the forge MCP row) and the list of Forgejo instances an install may be created against. Documented inside [Setup Wizard](setup). |
| **Setup** | `/setup` | admins | The wizard: installs, new install, edit, connect, remove, and the orphan sweep. |

Its two slot components add a Forgejo panel to an agent's page and to an
invocation's detail, so a run you reach from the console still names the issue or
pull request it was about.

## Where the documents are

Every page of this site is a markdown file in the repository's `docs/` directory,
and the site is built from it — one source of truth for the console's Docs page
and the published site alike. Add a `- [Title](slug)` line to
`docs/_sidebar.md` and a `slug.md` beside it and the page appears with no
code change.

The repository's own `README.md` holds the short version of this picture; the
design notes under `docs/design/` are the reasoning behind the wizard and are not
published here.
