# Quick Start

Five steps from an empty cluster to an agent that refines the issues you open.
Plan on ten minutes; the wizard does the rest of the work.

You need an AInsel platform that is already running, a Forgejo instance with an
organisation to work on, and a **site-admin personal access token** for that
Forgejo — the wizard creates the bot accounts with it.

## 1. Install the plugin

Helm releases are at `oci://ghcr.io/ainsel-project/charts/forgejo`. The chart
refuses to render without an image digest, because AInsel pins a plugin by digest:

```bash
helm install forgejo oci://ghcr.io/ainsel-project/charts/forgejo \
  <!-- x-release-please-start-version -->
  --version 0.3.0 \
  <!-- x-release-please-end -->
  --set image.repository=ghcr.io/ainsel-project/ainsel-plugin-forgejo \
  --set image.digest=sha256:… \
  --set forgejo.baseURL=https://forgejo.example.com
```

The digest of a release is printed in that release's workflow job summary.
Releases are signed keyless; the trust config that accepts them lives in AInsel's
own values — [Installation](installation) has both.

## 2. Approve the permissions and grant the group

The plugin declares 15 permissions: read and write on agents, agent images, personas,
connectors, triggers and MCP servers, read on invocations and observability, and
`llm:check` — the question "can this provider and model serve a request?" that
[Check now](setup#check-now) asks the hub. Approve them at install time or in the
console's permission UI.

Then grant the plugin the AInsel **group** your install will live in:
**Plugins → Forgejo → Group Access** in the console, or `plugin.groupAccess` in
the chart, or `set_plugin_group_access` from an MCP session. The plugin's
background identity — the one that keeps an install in shape when no user is
behind the call — may act only for groups an admin granted, and the wizard
refuses a group it cannot act for rather than creating an install nobody can
reconcile later. See [Credentials and Access](credentials).

## 3. Run the setup wizard

**Setup** in the console's Forgejo section (admin only). Eight screens: name the
install, pick the Forgejo instance and the organisation, point at the forge MCP
server, hand over the Forgejo admin token, choose the three roles' models and tool
sets, review, apply, then connect. It is walked through screen by screen in
[Setup Wizard](setup); the fields and rails are in
[Roles and Tool Sets](roles-and-tools).

Have ready: the site-admin token, the endpoint of a deployed
[Forgejo MCP server](https://git.b4mad.industries/agentic-forges/forgejo-mcp), and
a provider and model per role.

## 4. Verify the webhook

The wizard asks you to make Forgejo send one event — open an issue, or comment on
one, in any repository of the organisation. The moment that event arrives the
install is marked **✓ Verified**. If you continue first, that is fine too: the
background loop marks it verified when the first event lands.

Nothing appears in the work view until a real event has been delivered, so an
install that has never seen one reads "Waiting for first event" — which is the
plugin being honest rather than broken.

## 5. Open an issue and watch the refiner answer

Open an issue that mentions the refiner's bot login, and watch the run on the
[Live Board](live-board). The refiner reads the issue and the code, and posts a
refinement with the plan; the developer and the reviewer are woken by the handoff
@mentions the workflow skill defines — [The Forgejo Workflow Skill](workflow-skill).

## Where to go next

- [Installation](installation) — the chart, the signature, the values, uninstall order
- [Setup Wizard](setup) — the wizard screen by screen, including edits, removal and the orphan sweep
- [Troubleshooting](troubleshooting) — what to check when no event arrives
