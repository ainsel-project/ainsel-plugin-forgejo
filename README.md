# AInsel Forgejo Plugin

Shows what AInsel agents did and are doing on Forgejo — repo → issue → PR → runs, with time, cost and links straight to Forgejo.

An **install** of this plugin is a complete, fresh trio for one Forgejo organisation: a refiner, a developer and a reviewer, each with its own Forgejo bot account, its own AInsel role image, its own tool grant on its own agent, its own predefined persona and its own model. The install also owns its own connector, forge MCP server and seven triggers: six event triggers and the developer's half-hourly pull-request patrol. The setup wizard creates all of it; nothing is adopted, and nothing that already exists is edited.

Documentation lives in [`docs/`](docs/) and is published at **[ainsel-project.github.io/ainsel-plugin-forgejo](https://ainsel-project.github.io/ainsel-plugin-forgejo/)** — start at [Overview](docs/overview.md) for what the plugin is, or [Quick Start](docs/quickstart.md) to install it.

## Before you install

- **An existing hand-made trio on the same organisation reacts to the same events.** Your install subscribes to `issues`, `issue_comment`, `pull_request` and `pull_request_review` on the whole organisation, so an issue that mentions a bot wakes the hand-made agent beside the new one. The plugin never edits a resource it did not create, so it will not quiet the old one for you: **disable those triggers or that webhook yourself** before you install.
- **A Forgejo site-admin token has to be at hand** — the wizard creates the three bot accounts, their teams and their tokens with it. See [Credentials and access](#credentials-and-access).
- **The install's AInsel group has to be granted to the plugin.** The plugin's background identity — the one that keeps an install in shape when no user is behind the call — may act only for groups an admin granted. S1 of the wizard refuses to continue with a group the plugin cannot act for, and names where to fix it: **Plugins → Forgejo → Group Access** in the AInsel console (or `set_plugin_group_access`).
- **The plugin pod has to be able to reach Forgejo.** The chart's NetworkPolicy is ingress-only and AInsel restricts no egress, so this works by default; a cluster with default-deny egress needs a rule for it.

## Quick start

1. Install the plugin with Helm, with a pinned image digest (see [Installation](#installation)).
2. Approve the 15 declared permissions, and grant the plugin the group your install will live in (`plugin.groupAccess`, or afterwards in the console under Plugins → Forgejo → Group Access).
3. Run the setup wizard (admin only): it creates the bots, teams and tokens in Forgejo, then the MCP server, personas, role images, agents, connector, triggers and the organisation's webhook.
4. When the wizard asks, make Forgejo send one event: open an issue, or comment on one, in any repository of the organisation. The wizard marks the install verified the moment that event arrives — and if you continue first, the background loop marks it verified when the first event lands.
5. Open an issue and watch the refiner answer.

Full walkthrough, screen by screen: [`docs/setup.md`](docs/setup.md).

## What an install creates

`<slug>` is the install's short name, chosen in the wizard; `<role>` is `refiner`, `developer` or `reviewer`. Every AInsel-side name starts with `forgejo`, so two installs — and the GitHub plugin — cannot collide in one namespace.

**In Forgejo:**

| Name | What it is |
|---|---|
| `<role>-<slug>` | the role's bot account. Full name "AInsel <Role> (<slug>)", private visibility, its own personal access token. |
| `ainsel-<role>-<slug>` | the bot's team, holding the role's permissions over every repository of the organisation: refiner reads code and pulls and writes issues; developer writes all three; reviewer reads code and writes issues and pulls. |
| one organisation webhook | type `forgejo`, pointed at the install's connector, events `issues`, `issue_comment`, `pull_request`, `pull_request_review`. |

**In AInsel:**

| Name | What it is |
|---|---|
| `forgejo-mcp-<slug>` | the install's forge MCP server, registered with `tokenFromEnv: FORGEJO_TOKEN`. |
| `forgejo-<role>-<slug>` | the role image: its container, `mcpServers` = [`forgejo-mcp-<slug>`, `plugin-forgejo`], the bot token under `FORGEJO_TOKEN`, and the tool catalog the role's grant is validated against. No skills and no tool selection — both belong to the agent. |
| `forgejo-<role>-<slug>` | the role persona, from the plugin's predefined text with the install's three bot logins and its Forgejo address filled in. |
| `forgejo-<role>-<slug>` (default) | the agent, on that image and persona, with the role's provider and model. Its display name is the one name you may change. |
| `forgejo-<slug>` | the connector that receives the webhook (`signatureHeader: X-Forgejo-Signature`). Each install creates its own, so no two ever share one. |
| `forgejo-<role>-<what>-<slug>` | the six event triggers: refiner × issue opened, issue reopened and mention; reviewer × pull request opened and mention; developer × mention. |
| `forgejo-developer-pr-patrol-<slug>` | the seventh: a cron trigger that wakes the developer every 30 minutes to check its open pull requests — at most two repositories per run — for unaddressed review comments, failing CI and merge conflicts — and to post nothing when nothing changed. |

Plus, in the plugin's own database, one install row: the ids of everything above, the bot logins, the exclude label, the three tool sets, when the webhook was last verified and which event families have arrived. It holds no secret.

## Credentials and access

Four credentials pass through the wizard, and none of them is stored, logged or returned in a response:

- **The Forgejo site-admin token** — a site administrator's personal access token. It is used once, during the run that creates (or removes) the accounts, and then dropped. The wizard needs it to check the token, create the three bot users, create their teams, mint their tokens and write the organisation's webhook. It never reaches AInsel and never reaches the install row.
- **The bot tokens** — minted by the plugin, one per bot, with the scopes `write:issue`, `write:repository`, `read:user`, `read:organization`. Each goes straight into its role image's environment as `FORGEJO_TOKEN`. They are never shown and never kept.
- **The LLM keys** — one per role, held by the hub as the per-agent secret of that
  role's agents. The four provider key rows on the Settings page are write-only: the
  settings API never serves a key back, an empty save says nothing, and there is no reset
  — the hub has no way to clear an agent's key, so a new key typed over the old is the
  only write a key row makes. The plugin keeps no copy; the install row carries only the
  key's fingerprint, so a re-apply does not write the same key twice.
- **The webhook secret** — created by AInsel with the connector and handed straight to Forgejo in the same flow. It is never displayed.

**Plugin group access** is the other half. Every route of the setup API requires the plugin credential and a caller who is an AInsel site administrator; the background re-apply has no user behind it at all and runs on the identity the admin grants with `set_plugin_group_access` (chart value `plugin.groupAccess`). Without that grant for the install's group, the install would be created now and unreconcilable for ever, so the wizard blocks it up front.

## Tool sets

Each role's tools come from a shipped default — the refiner reads issues and code and writes the refinement, the developer is the only role with file, branch and pull-request tools (and `bash`), the reviewer is the only one that casts a review. Every tool set is **editable in the wizard**, from those defaults, within two guard rails:

- the refiner and the reviewer can never have `bash`, `create_branch`, `delete_branch`, `create_file`, `update_file`, `delete_file`, `create_pull_request` or `update_pull_request`;
- `create_pull_review` and `submit_pull_review` belong to the reviewer alone.

A set that breaks a rule is refused before anything is created, and the message names every offending tool. The plugin's own two read-only tools (`list_work`, `get_work_item`) are available to every role agent and cannot be switched off — so every grant names them, declared into the image's catalog by hand like `bash` is, because the hub cannot list the plugin's own server and refuses a grant name the catalog does not carry.

The set you pick lands on the **agent**, as its `enabledTools` — not as switches on the image. The image's `tools` is only the catalog of what the runtime ships; the hub validates each granted name against it by exact match, and an entry the catalog holds disabled restricts no agent. Apply therefore refreshes the image's catalog and then writes the role's set onto the agent, in the catalog's own spellings. An agent with no grant runs with everything the catalog offers, so a grant is never written empty — and a grant that exists is exhaustive: the runtime narrows the agent to the names it holds across every MCP server it has, which is why the plugin's own two read-only tools are named in every grant.

## Pages

- **Live** — a self-updating board of the last 24 hours across all installs: agents working, running and waiting counts, an hour-by-hour activity strip, agent time by role, one row per agent with a timeline of its day and the thread it holds now, and queues of what is waiting, working and just finished.
- **Work** — the full work view for your install: repositories, issues, pull requests and their runs, with cost, duration and links to Forgejo. Click a row to open its runs and totals in a detail view.
- **Setup** — the wizard (admin only).

## MCP tools

Agents access work via two read-only MCP tools:

- `list_work` — filter work items by repository, agent or time range, with Forgejo URLs, cost, duration and run count.
- `get_work_item` — fetch one work item by repository and issue/PR number, with all its runs.

Operators reach these as `forgejo_list_work` and `forgejo_get_work_item` through AInsel's own MCP server; agents reach them by their bare names.

### Forge MCP server (upstream source)

The two tools above are the plugin's own MCP server (`plugin-forgejo`). Each install registers a second, separate server with AInsel — `forgejo-mcp-<slug>` — the one its agents use to operate on Forgejo itself. The plugin does not implement that server: the install registers a row pointing at a URL the operator deploys and configures (Settings → *MCP server URL*, empty by default), and the server authenticates back to Forgejo with the role's bot token, which arrives in the env var named by `mcpTokenFromEnv` (default `FORGEJO_TOKEN`).

The implementation an install expects behind that URL is the upstream Forgejo MCP server, [`agentic-forges/forgejo-mcp`](https://git.b4mad.industries/agentic-forges/forgejo-mcp) — deploy it yourself (binary or container image) and point the URL at it.

One coupling to be aware of: the forge tool names in the role profiles (`content/profiles/*.json`, the source of truth) follow that server's tool catalog — `create_pull_request`, `get_pull_request_diff`, `list_repo_pull_requests` as examples. If an upgrade renames or drops a tool there, the roles that list it silently lose that capability, so cross-check the profiles against the new catalog when upgrading the deployment.

## The Forgejo workflow

The three roles share one workflow document: the handoff conventions, MCP tools and branch/PR/commit naming rules in [`content/skills/forgejo`](content/skills/forgejo) — a shipped shell plus one section per role ([refiner](content/skills/forgejo/refiner.md), [developer](content/skills/forgejo/developer.md), [reviewer](content/skills/forgejo/reviewer.md)).

The plugin serves the composed document at `/skills/forgejo/SKILL.md`, which is the path the manifest declares and the one the hub fetches into its skill row. Each role's section is that role's `skillText` setting, so an edit saved in Settings is the body the hub's next fetch reads — the document is read from the settings on every request, and a role nobody has edited keeps its shipped section.

## Keeping an install in shape

The plugin re-applies every install's managed state once at start and again whenever the plugin's settings are saved — the pass is triggered, not scheduled, and there is no periodic timer — as its background identity: the tool grant on each agent, each agent's image and persona, the six event triggers, the patrol cron trigger, and the event coverage. Drift is set back and each correction is logged against the install. An install whose group the plugin can no longer act for is skipped, and the loop only ever touches the resource ids recorded on that install's own row.

Every AInsel resource the wizard creates is stamped `managedBy: plugin-forgejo` at create: they are **read-only in the AInsel console** — the hub refuses console writes of them with 409 — and the wizard, which writes as the plugin's own identity, is the only way to change them. Two gaps the hub leaves today: triggers carry no stamp, so they stay console-editable, and the stamp cannot be set on an existing resource, so installs created before this stamping shipped stay console-managed until they are removed and re-created. Drift is what the re-apply loop above is for.

## Removing an install, and uninstalling the plugin

**Remove install** (on the wizard's first screen) deletes what that install created: its connector, agents, role images and personas, then its row. With a Forgejo admin token it also deletes the organisation's webhook, the three teams and the three bot users — which revokes their tokens. Without one, you get a checklist of what is left, including the tokens to revoke.

**Uninstall the plugin** removes the `Plugin` resource and, with it, the `plugin-forgejo` MCP server row and the `forgejo-forgejo` skill row, both of which the hub maintains. What it does *not* do today is delete the resources each install created — and since those carry the plugin as their manager, the console cannot delete them either: **run "Remove install" for every install first**, which deletes them as the named manager.

## Installation

Helm releases are at `oci://ghcr.io/ainsel-project/charts/forgejo`. A release is created with every tag `v*` pushed to this repo; the release workflow builds, signs and charts the image, and prints the digest to the job summary.

Install with a pinned image digest (required; mutable tags are rejected):

```bash
helm install forgejo oci://ghcr.io/ainsel-project/charts/forgejo \
  --version 0.1.0 \
  --set image.repository=ghcr.io/ainsel-project/ainsel-plugin-forgejo \
  --set image.digest=sha256:…
```

The plugin requires AInsel's `plugins.requireSignature: true` (the default) and trusts releases signed with the keyless identity from the GitHub Actions workflow:

```yaml
plugins:
  signing:
    keyless:
      - issuer: https://token.actions.githubusercontent.com
        subjectRegExp: ^https://github\.com/ainsel-project/ainsel-plugin-forgejo/\.github/workflows/release\.yml@refs/tags/v.*$
```

For development without a signature, set `plugins.requireSignature: false`.

## Permissions

The plugin declares 15 permissions to create and manage agents, agent images, personas, connectors, MCP servers and triggers, to read invocations and observability, and to ask the hub whether a provider and model can serve a request (`llm:check`). Approve them during installation or later via AInsel's permission UI.

## Configuration

The plugin chart accepts these values:

- `forgejo.baseURL` — the root URL of your Forgejo instance; every issue, PR and repository link is built from it.
- `hub.url` — where the plugin reaches the AInsel hub (default `http://hub:8080`). Set to empty to run the plugin standalone (hub calls are disabled). The name follows AInsel's hub Service, renamed from `hub-backend` in the core chart; on a cluster still running the pre-rename core, set `hub.url` and `networkPolicy.allowFrom` to the old names (`http://hub-backend:8080`, `component: hub-backend`, `component: k8s-event-source-gateway-operator`) until it upgrades.
- `plugin.groupAccess` — an array of group IDs the plugin's background identity may act for. **Every install's group has to be in here**, or the wizard refuses that group. This is separate from signed-in user access; the work view and the wizard always act as the user.
- `plugin.approvedPermissions` — an array of permission names to pre-approve without an admin granting them one by one.
- `persistence` — the install store. The pod runs with a read-only root filesystem, so the volume mounted at `/data` is the one place it may write, and it is what keeps the installs across a restart.

See [`chart/values.yaml`](chart/values.yaml) for all values.

## Development

For local testing with `plugins.requireSignature: false`:

1. Build the image: `make image` or `docker build -t ghcr.io/ainsel-project/ainsel-plugin-forgejo:dev .`
2. Push it to a registry the cluster can reach, or load it directly if using kind/minikube, and note the digest of the image you pushed.
3. Install the chart from this checkout — the released `oci://` chart is for releases, a dev build installs `./chart`:

   ```bash
   helm upgrade --install forgejo ./chart \
     --set image.repository=<your registry>/ainsel-plugin-forgejo \
     --set image.digest=sha256:<digest of the image you pushed>
   ```

Then set `plugins.requireSignature: false` in AInsel's values and reconcile the plugin.

Documentation is `docs/` — markdown, with `docs/_sidebar.md` driving the navigation. The site that publishes it is `docs-site/`: `pnpm install && pnpm dev` there serves the same files at <http://localhost:5174/ainsel-plugin-forgejo/>. See [`docs-site/README.md`](docs-site/README.md).

## References

The published documentation is built from [`docs/`](docs/) — see [`docs-site/README.md`](docs-site/README.md) for how the site is built and deployed.

- [Plugin Reference](https://github.com/ainsel-project/ainsel/blob/main/docs/plugin-reference.md) — the manifest and hub contract.
- [Author Guide](https://github.com/ainsel-project/ainsel/blob/main/docs/writing-a-plugin.md) — building a plugin from scratch.
- [Template](https://github.com/ainsel-project/ainsel-plugin-template) — the starting point for new AInsel plugins.
- [`docs/overview.md`](docs/overview.md) — what the plugin gives you, and what an install is.
- [`docs/architecture.md`](docs/architecture.md) — the read path, the write path, and who owns what.
- [`docs/setup.md`](docs/setup.md) — the wizard, screen by screen.
- [`docs/work-view.md`](docs/work-view.md) — how runs are matched to issues and pull requests.
- [`docs/troubleshooting.md`](docs/troubleshooting.md) — when nothing arrives, or a plan is blocked.
- [`docs/design/2026-10-24-setup-wizard-flow.md`](docs/design/2026-10-24-setup-wizard-flow.md) — the design the wizard implements.

## Still open

- **Durable run subject** — Events older than AInsel's retention window drop from the work view, and their runs lose the subject context. A permanent subject link and fallback mechanism are planned in [AInsel addition 2](https://github.com/ainsel-project/ainsel/issues).
- **Per-run cost beyond 7 days** — AInsel's token metrics API only reaches back 7 days, so runs older than that show "—" (unknown cost) instead of a figure. See [template issue #11](https://github.com/ainsel-project/ainsel-plugin-template/issues/11).
- **Manifest as a Go module** — The manifest package is vendored from AInsel main. Tracked in [template issue #7](https://github.com/ainsel-project/ainsel-plugin-template/issues/7); will be published as a module when AInsel's shared APIs stabilize.
