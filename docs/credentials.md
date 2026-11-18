# Credentials and Access

Four credentials pass through a setup, and none of them is stored where a reader can
get it back. This page is the whole account of where each one goes, and of the two
grants that decide what the plugin may do.

## The four credentials

| Credential | Given to | Lives in | Shown again? |
|---|---|---|---|
| **Forgejo site-admin token** | the wizard, on the run that creates (or removes) things | nowhere — used once during that run and dropped | No. It never reaches AInsel and never reaches the install row. |
| **The three bot tokens** | minted by the plugin, one per bot | their role image's environment, as `FORGEJO_TOKEN` | No. Never displayed, never kept. |
| **The LLM keys** | the agent create, per role | dropped — with one exception, below | No, with one exception. |
| **The webhook secret** | created by AInsel with the connector, handed straight to Forgejo in the same flow | AInsel's secret for the connector | No. |

### The site-admin token

A site administrator's personal access token. The wizard needs it to check the token,
create the three bot users, create their teams, mint their tokens and write the
organisation's webhook — and for the same list on removal. It is a body field, never
a URL parameter, so it does not land in an access log. An edit needs no admin token
at all: an edit creates no accounts, and the logins and teams follow the slug.

### The bot tokens

One per bot, minted by the plugin with the scopes `write:issue`,
`write:repository`, `read:user` and `read:organization`. Each goes straight into its
role image's environment as `FORGEJO_TOKEN` — which is also the env var named by
`mcpTokenFromEnv`, so the forge MCP server authenticates back to Forgejo as the same
bot. Deleting a bot user revokes its token, which is why removing an install with an
admin token is the clean path.

### The LLM keys

One per role, sent with the agent create and dropped. The four provider key rows
(custom provider, OpenAI, Anthropic, Google Gemini) are **write-mostly**: the
settings API never serves a key back, saving an empty value keeps what is stored, and
**Reset** is what clears it. The row's "edited" tag is what tells you a key is
there.

The exception is the **custom provider's key**: a role on the `custom` provider keeps
its key in the plugin's settings table — the only secret the table holds — because it
has to be re-applied to the agent on a later edit or reconcile. A vendor key, by
contrast, is handed to the hub, which holds it on the agent; the plugin records only
its fingerprint on the install row.

A stored key of a provider the role does not name is kept and never sent.

### The webhook secret

AInsel generates it with the connector and the setup hands it to Forgejo in the same
flow. Nothing to copy, nothing to store.

## Two grants, two names

**Permissions** answer "what may this plugin call at all": the 15 the manifest
declares — read and write on agents, agent images, personas, connectors, MCP servers
and triggers, read on invocations and observability, and `llm:check`, the question
"can this provider and model serve a request?" that
[Check now](setup#check-now) asks the hub. An admin approves them, or
`plugin.approvedPermissions` pre-approves them at install.

**Group access** answers "which groups may the plugin act for when nobody is signed
in": the background identity behind the re-apply pass. An admin grants it per group —
**Plugins → Forgejo → Group Access** in the console, `plugin.groupAccess` in the
chart, or `set_plugin_group_access`. The plugin may act for a group only if granted.

The wizard refuses a group the plugin cannot act for **before** creating anything,
and names where to fix it. An install created for a group the plugin cannot serve
would be an install nobody could ever reconcile.

Reads are a third thing: the work view, the live board and the wizard all act as the
**signed-in caller**, and answer with what that caller's credential lets the hub
show. Group access grants the plugin nothing for those calls.

## Who may write what

Every AInsel resource the wizard creates carries `managedBy: plugin-forgejo`. The hub
refuses console edits, secret rotations and deletes of them with 409, and admits the
named manager — the wizard, writing as the plugin — on apply, on an edit, on
**Repair connection**, on **Remove install** and on the re-apply pass.

Triggers are the gap: the hub stamps no trigger, so they stay console-editable even
though the connector they fire on is not. Delete one there and the install is quietly
incomplete; make them through the wizard.

## Working with secrets well

- Nothing a reader can read comes back out of the settings API — keys are masked in
  every answer.
- The install row holds ids, logins, labels, tool sets and timestamps. No secret.
- The SQLite file under `/data` holds settings (including a custom provider key if a
  role uses one) and nothing else: treat it as a secret-bearing volume.
- Removing an install without an admin token leaves a checklist rather than a
  half-deleted state, and keeps the install row so the retry can finish the job.

## Where to go next

- [Setup Wizard](setup) — the screens where each credential is asked for
- [Architecture](architecture) — the ownership model behind the stamp
- [Troubleshooting](troubleshooting) — a group that will not save, a webhook that stays unverified
