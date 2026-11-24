# Security Policy

## Reporting a vulnerability

**Please report a vulnerability through GitHub's private vulnerability reporting** —
[Security → Report a vulnerability](https://github.com/ainsel-project/ainsel-plugin-forgejo/security/advisories/new)
on this repository. Do not open a public issue for anything that exposes a credential, a
token, or a way to reach one: an issue here is read by everyone, including the people the
report is about.

You get an acknowledgement, and a fix timeline once we understand the problem. Please give
us time to ship it before anything is disclosed.

## What this plugin touches

The whole account is [Credentials and access](docs/credentials.md). In short — and a claim
here that the code does not support is a bug, report it:

| Credential | Where it goes | Can it come back out? |
|---|---|---|
| **Forgejo site-admin token** | the wizard, on the run that creates or removes things; a body field, never a URL parameter, so it does not land in an access log | No. Used once during that run and dropped. It never reaches AInsel and never reaches the install row. |
| **The three bot tokens** | minted by the plugin and written straight into their role image's environment as `FORGEJO_TOKEN` | No. Never displayed and never kept by the plugin. |
| **The LLM keys** | the hub, as the per-agent secret of the role's agents — **the hub stores them, the plugin does not** | No. The settings API never serves a key back; the install row carries only a **fingerprint**, so a re-apply can tell it has the key already. |
| **The webhook secret** | created by AInsel with the connector and handed to Forgejo in the same flow | No. It lives in AInsel's secret for the connector and is never displayed. |

Two more facts worth having in hand:

- **The install row holds no secret** — ids, logins, labels, tool sets, timestamps.
- **The SQLite file under `/data` holds settings and nothing else.** An older version kept
  a provider key there; on startup such a row is forwarded to the role's agents and
  deleted, so a current install's file carries no secret at all.

Nothing here is claimed to be encrypted at rest or rotated automatically: this plugin
neither does nor promises either.

## If a credential leaked

What to do depends on which one it is — deleting a Forgejo bot user is what revokes its
token, so the order matters.

1. **A bot token.** Run **Setup → Remove** for the install **with a Forgejo site-admin
   token**: that deletes the organisation's webhook, the three teams and the three bot
   users, which revokes the tokens those bots were minted with. Without an admin token the
   removal stops at a checklist and keeps the install row — the row is the only record of
   which hook, teams and bots belong to the install — so retry it with a token. Revoke the
   same tokens by hand in Forgejo (**User settings → Applications**) if the install must
   stay up.
2. **A Forgejo site-admin token.** Revoke it in Forgejo like any personal access token.
   The plugin kept no copy of it, so there is nothing here to clear.
3. **An LLM key.** Type a new key over the old one on the role's Settings row — the hub has
   no way to clear an agent's key, so an overwrite is the only write a key row makes — and
   revoke the old key with the provider.
4. **The webhook secret.** Re-run the wizard's **Repair connection** (or re-apply the
   install), which rewrites the hook on Forgejo; a delivery with the old secret then fails
   signature rather than being accepted.
5. **The plugin's own database file.** It holds settings and install rows, no credential —
   but treat a readable `/data` as a reason to walk the list above anyway, since it names
   every install, bot login and connector on the instance.

## Scope

This policy covers this plugin, its chart and its console UI. AInsel itself — the hub, the
agents, the images the role agents run in — and the Forgejo instance the plugin is pointed
at are separate projects with their own security posture; a finding that belongs to one of
them is still worth reporting here if you cannot tell, and we will forward it.
