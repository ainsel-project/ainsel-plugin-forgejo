# Installation

The plugin ships as one signed container image and one Helm chart, published by the
same tag that bumps the version in `ainsel-plugin.json`. What runs is pinned by
digest, and the signature says who built it.

## Requirements

- **An AInsel platform** with the hub reachable from the namespace plugins are
  installed into. The plugin talks to the hub for every read and every write of a
  managed resource.
- **A Forgejo instance** and a **site-admin personal access token** for it. The
  wizard creates three bot accounts, three teams and the organisation's webhook
  with that token, uses it once, and keeps nothing of it.
- **A deployed Forgejo MCP server** for the agents to act on the forge with. The
  plugin does not implement it: the implementation an install expects is the
  upstream [`agentic-forges/forgejo-mcp`](https://git.b4mad.industries/agentic-forges/forgejo-mcp),
  which you deploy yourself and point the install at. See
  [MCP Tools](mcp-tools).
- **Egress to Forgejo** from the plugin pod. The chart's NetworkPolicy is
  ingress-only and AInsel restricts no egress, so this works by default; a cluster
  with default-deny egress needs a rule for it.

## Compatibility

`ainsel-plugin.json` is the contract with the hub, and its `pluginApi` field is
that contract's version — not the plugin's own version. Keep the plugin release
and the hub within one another's API level.

<!-- x-release-please-start-version -->

| Plugin version | Plugin API (`pluginApi`) | AInsel platform |
| --- | --- | --- |
| 0.3.1 – 0.3.0 (every release so far) | 1.1 | a hub serving the Plugin API 1.1 contract |

<!-- x-release-please-end -->

A new row belongs here whenever `pluginApi` changes. The exact minimum AInsel
release per row still needs the author's release history — `pluginApi` is what
the hub negotiates, so the contract column cannot drift from the code.

## Install with Helm

<!-- x-release-please-start-version -->
```bash
helm install forgejo oci://ghcr.io/ainsel-project/charts/forgejo \
  --version 0.3.1 \
  --set image.repository=ghcr.io/ainsel-project/ainsel-plugin-forgejo \
  --set image.digest=sha256:… \
  --set forgejo.baseURL=https://forgejo.example.com \
  --set plugin.groupAccess[0]=<group-id>
```
<!-- x-release-please-end -->

The chart renders four things: the Deployment and its Service, the PVC the install
store lives on, a NetworkPolicy admitting only AInsel's own pods, and the
`ainsel.dev/v1alpha1` Plugin resource that registers the plugin with the hub
(turn it off with `plugin.create: false` when that CR is managed elsewhere).

`image.digest` is required — the chart refuses to render without it. AInsel pins a
plugin by digest so a signed release binds to exactly what runs; a mutable tag
would let the image under the signature change. Every value is in
[Configuration](configuration).

## Trusting the signature

AInsel verifies plugin images (`plugins.requireSignature: true`, the default).
Releases are signed **keyless**: the signing identity is the GitHub Actions
workflow's OIDC identity, so there is no private key to hold or rotate. Trust it
in AInsel's own values:

```yaml
plugins:
  signing:
    keyless:
      - issuer: https://token.actions.githubusercontent.com
        subjectRegExp: ^https://github\.com/ainsel-project/ainsel-plugin-forgejo/\.github/workflows/release\.yml@refs/tags/v.*$
```

The subject is a tag ref, deliberately: the tag is the identity installers trust, so
an image built from any other ref does not satisfy it.

For a development build with no signature, set `plugins.requireSignature: false`.

## Permissions and group access

Two grants decide what the plugin may do, and they are not the same thing.

**Permissions** are the 15 the manifest declares — agents, agent images, personas,
connectors, MCP servers and triggers, read and write, plus read access to
invocations and observability, and `llm:check` for the reachability question
[Check now](setup#check-now) asks the hub. An admin approves them, or the chart pre-approves
them with `plugin.approvedPermissions`.

**Group access** is different: it is the plugin's *background* identity, used when
no user is behind the call — the re-apply loop, the reconcile pass. It may act only
for the groups an admin names, in `plugin.groupAccess`, in the console under
**Plugins → Forgejo → Group Access**, or with `set_plugin_group_access`. Every
install's group has to be in that list, or the wizard refuses the group on its
first screen.

Signed-in reads are unaffected by both: the work view and the wizard always act as
the caller, and answer with what that caller's credential lets the hub show. See
[Credentials and Access](credentials).

## Upgrading

Upgrade the chart the way you installed it: pull the new digest, point
`image.digest` at it, `helm upgrade`. The version in `ainsel-plugin.json` is what
the hub shows and what the plugin's API and MCP endpoint report, and release
tooling keeps it, the image tag and the chart version in step — see
[Publishing a Release](release).

Settings the admin edited win over the shipped defaults; untouched ones follow the
plugin. The Settings screen shows what a new version ships for a field you have not
touched before you decide — see [Setup Wizard](setup).

## Uninstalling

**Remove every install first.** Each install's AInsel resources are stamped
`managedBy: plugin-forgejo`, which means the console cannot delete them — only the
wizard can, because only the named manager may write them. So:

1. **Setup → Remove** for every install. With a Forgejo admin token this also
   deletes the organisation's webhook, the three teams and the three bot users —
   which revokes their tokens. Without one you are handed a checklist of what is
   left, and the install row is kept until nothing is.
2. Delete the `forgejo-mcp-<slug>` registry rows in the console — removal leaves
   those in AInsel today.
3. Remove the `Plugin` resource (and the chart with it). That takes the
   `plugin-forgejo` MCP server row and the `forgejo-forgejo` skill row with it, both
   of which the hub maintains.

A half-removed install is exactly the half-edit the stamp exists to prevent, which
is why the order matters. If a row was lost anyway — a wiped database, the
fresh-start cutover — the wizard's **Clean up leftovers** sweep finds what the
naming convention still identifies and deletes exactly that. See
[Setup Wizard](setup).

## Development installs

A dev build installs the chart from this checkout, not the released `oci://` chart:

```bash
make image                     # or: docker build -t <registry>/ainsel-plugin-forgejo:dev .
# push it, note the digest of what you pushed, then:
helm upgrade --install forgejo ./chart \
  --set image.repository=<registry>/ainsel-plugin-forgejo \
  --set image.digest=sha256:<digest>
```

With `plugins.requireSignature: false` in AInsel's values. See
[Development](development) for running the plugin and the docs site locally.
