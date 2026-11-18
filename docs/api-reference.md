# HTTP API

Everything the plugin's UI shows comes from these routes, and the UI reaches them the way
everything else does: through the hub's plugin proxy. `GET /api/work` on the plugin is
`GET /plugins/forgejo/api/work` on the hub, and that is the only way a browser ever calls
them — the NetworkPolicy admits only the hub and the event-gateway operator.

## Two rules every route follows

**You are the caller.** The credential the hub forwards on the request goes on the
context, so every hub call the read makes is made *as the person who asked*, and the hub
answers with exactly what they may see. A request that arrives with no credential is
refused rather than answered from the plugin's own identity — a background read would see
every group the chart grants the plugin, and work nobody behind this request asked for has
no business arriving in a console.

**A bad parameter is refused, not defaulted.** `since` reads as a window in days (`7d`) or
a moment in RFC3339; anything else is a 400. A console that asks for a week and is handed a
month has been told something it did not ask.

Failures answer with the one field they have:

```json
{ "error": "invalid since" }
```

## Liveness and assets

Public, no caller credential:

| Route | |
|---|---|
| `GET /healthz` | Liveness and readiness. |
| `GET /ainsel-plugin.json` | The manifest the hub reads. |
| `GET /ui/…` | The built UI bundle, served under its manifest prefix. |
| `GET /skills/forgejo/SKILL.md` | The composed workflow skill — public so an agent can read it without a session. |
| `/mcp` | The MCP endpoint, any method. See [MCP Tools](mcp-tools). |

## Work routes

Reads, as the caller. All are `GET`.

| Route | Returns | Parameters |
|---|---|---|
| `/api/now` | The live board's runs: what is running and queued now. | `since` |
| `/api/work` | The work view: items grouped by repository, each with its runs, cost and Forgejo URL. | `since`, `repo`, `agent` |
| `/api/work/{owner}/{repo}/{number}` | One thread and every run that touched it. The path is split the way Forgejo's own URLs are, so the console builds a link from the two segments it got. | — |
| `/api/agents/{name}/work` | The work one agent has touched. | `since` |
| `/api/invocations/{id}/work` | The thread behind one run — what the invocation panel renders. | — |

A number that is not a positive integer is a 400; a thread no run has touched is a 404, not
an empty answer.

Cost and duration are the hub's numbers, and the hub's cost series covers about seven days:
older runs read `null` and the UI shows "—".

## Setup routes

Admin only, on this transport as on MCP: a caller who is not a signed-in **admin user** gets
`403 admin only` before the service is reached.

| Route | |
|---|---|
| `GET /api/setup/options` | What the wizard can pick from: the groups to file an install under, the configured instances, the role defaults and the tool-set rails. It does not list hub agents, personas, images or MCP servers — an install creates those itself, so a running instance's resources are not a source to pick from. |
| `POST /api/setup/plan` | The changeset: what will be created or changed, with nothing written. |
| `POST /api/setup/apply` | Create or edit an install. A failure partway answers with what was created and what is left, and the retry picks up. |
| `GET /api/setup/installs` | The installs and their saved state — the settings screen reads its state here. |
| `GET /api/setup/installs/{slug}/deliveries` | The webhook deliveries Forgejo recorded, which is how an install earns **✓ Verified**. |
| `POST /api/setup/installs/{slug}/repair-connection` | Re-write the connector and webhook for an install whose AInsel side was deleted. |
| `POST /api/setup/installs/{slug}/remove` | Remove an install. With an admin token, also the Forgejo side; without one, a checklist of what is left. |
| `POST /api/setup/orphans/{slug}/plan` | What the naming convention identifies as leftovers of a lost row, and what will be skipped because something else still points at it. |
| `POST /api/setup/orphans/{slug}/delete` | Delete exactly that list. |

## Settings routes

Admin only.

| Route | |
|---|---|
| `GET /api/settings` | The saved per-role settings. Keys are masked, always. |
| `PUT /api/settings` | Save them. A field left empty keeps what is stored; **Reset** is what clears one. Saving reconciles the resources that field touches. |
| `POST /api/settings/reconcile` | The Settings page's **Check now** — a full pass over every install, plus the on-demand LLM reachability check, in one answer ([setup.md](setup#check-now) is the prose). |
| `GET /api/instances` | The configured Forgejo instances. |
| `PUT /api/instances` | Save the instance list. A plan naming an address outside it is refused. |

## What is deliberately not here

- **No `GET /settings/defaults`.** What a new version ships for a field you have not
  touched is answered on `GET /api/setup/options`, so the Settings screen can show it
  against what you saved.
- **No route that writes Forgejo resources directly.** Bots, teams and the webhook are
  written by the apply path, with the admin token on that request, and by nothing else.
- **No route that reads another plugin's data.** The work view's runs are the hub's, and
  the hub decides what the caller may see.

## Where to go next

- [Architecture](architecture) — how a read reaches the hub
- [Setup Wizard](setup) — what each setup route is for in the flow
- [MCP Tools](mcp-tools) — the same setup service over MCP, for an operator's agent
