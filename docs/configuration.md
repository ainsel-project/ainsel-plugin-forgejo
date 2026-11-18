# Configuration

Two layers of configuration, and they do not overlap. The **chart** configures the
running plugin — image, hub address, storage, who may reach it. The **plugin's own
settings** configure an install — models, keys, personas, tool sets, the forge MCP row,
the list of Forgejo instances — and those are edited in the console, never in YAML.

## Chart values

From `chart/values.yaml`:

| Key | Default | What it does |
|---|---|---|
| `image.repository` | `ghcr.io/ainsel-project/ainsel-plugin-forgejo` | The image to run. |
| `image.digest` | `""` | **Required.** `sha256:…` of the release image. The chart refuses to render without it: AInsel pins a plugin by digest, so a signed release binds to exactly what runs. |
| `service.port` | `8080` | One number for the container port, `$PORT`, the Service port and the NetworkPolicy's allowed port. The hub's port is a different one and lives in `hub.url`. |
| `hub.url` | `http://hub:8080` | Where the plugin reaches the hub, as `$HUB_URL`. The hub's Service name and port in AInsel's namespace — plugins are installed there. Empty leaves hub calls disabled, so the plugin runs standalone. |
| `forgejo.baseURL` | `""` | The Forgejo instance this plugin talks about, as `$FORGEJO_BASE_URL`, and the default offered in the wizard. Every issue, PR and repository link is built from it. |
| `persistence.enabled` | `true` | The PVC the install store lives on. The pod runs with a read-only root filesystem, so this volume is the only place it may write. |
| `persistence.size` | `1Gi` | Its size. |
| `persistence.storageClass` | `""` | The StorageClass to ask for. Empty uses the cluster default. |
| `plugin.create` | `true` | Whether to render the `ainsel.dev/v1alpha1` Plugin resource. Turn it off when that CR is managed elsewhere (GitOps, an app-of-apps). |
| `plugin.disabled` | `false` | Registers the plugin in a disabled state. |
| `plugin.approvedPermissions` | `[]` | Permissions granted without an admin approving each one. Empty leaves the list off the resource. |
| `plugin.groupAccess` | `[]` | Group ids the plugin's **background** identity may act for. Empty leaves the list off the resource; an admin can grant it later, in the console or with `set_plugin_group_access`. |
| `networkPolicy.enabled` | `true` | Whether to restrict ingress at all. |
| `networkPolicy.allowFrom` | hub, event-gateway-operator | Which pod labels may reach the service port. |
| `resources.requests` | 10m / 32Mi | Requests. |
| `resources.limits.memory` | 128Mi | Memory limit. |

Render tests pin the load-bearing couplings — that `image.digest` is required, and that
one `service.port` really does drive all four places — in `chart/tests/render_test.sh`
(`make chart-lint`).

### Why nothing else may reach the pod

A pod that reached the plugin directly would bypass the hub's authentication, its
permission checks and its group filtering. So the NetworkPolicy admits exactly the two
label sets AInsel uses for plugin traffic — the hub and the event-gateway operator — and
every other caller, browser included, goes through the hub proxy. That is also why no
Ingress is rendered: there is nothing here for a browser to talk to directly.

### Egress

The chart's NetworkPolicy is ingress-only and AInsel restricts no egress, so the plugin
reaches Forgejo by default. A cluster with default-deny egress needs a rule for it — this
is the one network fact the chart does not express.

## Environment variables

The container reads five, all set by the chart except the last:

| Variable | Set by | Meaning |
|---|---|---|
| `PORT` | `service.port` | The port to listen on. |
| `HUB_URL` | `hub.url` | The hub to talk to. Unset or empty means there is no hub: the work routes say so rather than reporting no work. |
| `DATA_DIR` | `/data` | Where the SQLite stores live. |
| `FORGEJO_BASE_URL` | `forgejo.baseURL` | The default instance for the wizard and for links. |
| `FORGEJO_ADMIN_TOKEN` | — (dev/CI only) | A site-admin token for the **fresh-start cutover**: the first start under the created-only layout retires what an older wizard left behind, deleting the Forgejo side (bots, teams, webhook) of any install row that predates it. Without it those resources are logged as left standing by choice. Set it in development, not in a steady-state deployment. |

## What is not in the chart

Everything about an install. The instance list, the per-role provider, model, keys and
working directory, the persona and skill texts, the tool sets, the turn timeouts, the
working-hours windows and the forge MCP row all live in the plugin's settings store on the
data volume, edited on the Settings screen and in the wizard. They are what an admin owns,
which is why they survive an upgrade and why they are not in a values file:

- A values change cannot reach an install that already exists — a settings save can, and
  is stamped so the console cannot fight it.
- Settings are per install and per role; a values file is per release.
- Secrets belong where they are write-mostly — see
  [Credentials and Access](credentials).

The one chart value that overlaps them is `forgejo.baseURL`, and only as a **default**:
an install points at the instance chosen when it was created, and an install created
against a different Forgejo links to that one.

## Where to go next

- [Installation](installation) — installing, trusting the signature, uninstalling
- [Setup Wizard](setup) — the Settings screen and what each field does
- [Architecture](architecture) — the store behind the settings
