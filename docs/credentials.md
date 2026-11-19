# Credentials and Access

Four credentials pass through a setup, and none of them is stored where a reader can
get it back. This page is the whole account of where each one goes, and of the two
grants that decide what the plugin may do.

## The four credentials

| Credential | Given to | Lives in | Shown again? |
|---|---|---|---|
| **Forgejo site-admin token** | the wizard, on the run that creates (or removes) things | nowhere — used once during that run and dropped | No. It never reaches AInsel and never reaches the install row. |
| **The three bot tokens** | minted by the plugin, one per bot | their role image's environment, as `FORGEJO_TOKEN` | No. Never displayed, never kept. |
| **The LLM keys** | the role's agents, per role | the hub's per-agent secret — never the plugin | No. |
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

One per role. The four provider key rows (custom provider, OpenAI, Anthropic, Google
Gemini) are **write-only**: typing a key hands it to the hub, which stores it as the
per-agent secret of that role's agents, and the settings API never serves a key back.
The plugin keeps no copy — not in the settings table, not on the install row, which
carries only the key's **fingerprint** so a re-apply does not write the same key again.

What that means on the Settings page: a key row always starts empty and never reads as
edited (there is nothing here to have edited), saving it empty says nothing at all, and
there is **no reset** — the hub has no way to clear an agent's key, so a new key typed
over the old one is the only write a key row makes.

A key is accepted only for a role that has an installed agent to hold it. A role with no
agent is refused **that field** — the rest of the batch saves as it stood, because a
refusal that took the whole batch with it would throw away edits that had nothing to do
with the key.

The wizard's Roles step is the one place a key can be given before those agents exist:
its optional **Provider key** field rides with the agent create. For a provider that
holds no key (`ollama-cloud`, `opencode`, `alibaba-cloud`) the plan drops it.

An older version kept a key in the plugin's settings table. On startup the plugin
forwards any such row to the role's agents and deletes it once the hub has taken it; a
row the hub refused stays for the next start, because until the key lands the row is the
only copy there is.

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
- The SQLite file under `/data` holds settings and nothing else. It no longer holds a
  provider key: an older version's key row is forwarded to the role's agents and
  deleted at startup, so a fresh install's file carries no secret at all.
- Removing an install without an admin token leaves a checklist rather than a
  half-deleted state, and keeps the install row so the retry can finish the job.

## Where to go next

- [Setup Wizard](setup) — the screens where each credential is asked for
- [Architecture](architecture) — the ownership model behind the stamp
- [Troubleshooting](troubleshooting) — a group that will not save, a webhook that stays unverified
