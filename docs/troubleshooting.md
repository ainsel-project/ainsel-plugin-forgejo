# Troubleshooting

Symptom first, then what to check, in the order that pays off fastest. Where the plugin
answers with a message, the message is quoted as it arrives.

## Nothing appears in the work view or the Live board

The board is the hub's record of runs, read as you.

1. **Has an event ever arrived?** An install that has never received a webhook delivery
   reads **"Waiting for first event"**, and the work view is empty — correctly. Open an
   issue in the organisation and see the next section.
2. **Can you see the group?** Reads go to the hub as the caller. A run filed under a group
   you are not an owner or writer on is not missing; it is not yours to see. Check in
   AInsel's own run list.
3. **Is `HUB_URL` set?** With no hub to reach, the work routes say so rather than reporting
   no work. A standalone plugin has no runs to show.
4. **Old window?** `since` defaults to 30 days on MCP and to the page's own window in the
   console. Widen it before doubting the data.

## The webhook never verifies

The install earns **✓ Verified** when a delivery arrives. It does not verify itself just
because the hook exists.

1. **Make an event happen** — open an issue, or comment on one, in a repository of the
   organisation. The wizard's connect step asks for exactly this.
2. **Is the address Forgejo can reach?** The connector's URL is the hub's, from inside the
   cluster. A Forgejo outside the cluster needs to be able to resolve and reach it.
3. **Read the deliveries** — `GET /api/setup/installs/{slug}/deliveries` shows what Forgejo
   tried and what came back. A redelivery that 404s is a routing problem; a redelivery that
   times out is a network one.
4. **Still nothing, but the events are clearly arriving?** The coverage figures come from the
   re-apply pass, so what the wizard shows can lag a real delivery. **Check now** on Settings
   runs a pass.

Nothing is lost while an install is unverified — events that arrive mark it verified as a
side effect.

## The plan is blocked

The plan refuses before anything is written, and names the fix.

| The answer says | What it means |
|---|---|
| `group "…" is not granted to the plugin: an admin has to grant it in Plugins → Forgejo → Group Access in the AInsel console (or set_plugin_group_access)` | The plugin's background identity cannot act for that group. Grant it and re-plan; the wizard refuses the group on its first screen for the same reason. |
| `baseUrl "…" is not a configured Forgejo instance — configure it on the plugin's Settings page or pick a listed instance` | An install can only point at an address an admin configured. Add it on Settings. |
| `baseUrl "…" is refused: no Forgejo instance is configured — add one on the plugin's Settings page` | Same rule, nothing configured yet — a fresh install has no address to pick. |
| `role "…" may not use: <tools>` | An edited tool set crosses the guard rails. Every violating tool is named, so one fix is enough. |
| A tool name the catalogue does not have | Refused rather than accepted: a name no MCP server provides is not an error at write time, it is a tool that never arrives. Check the spelling against what the forge MCP server advertises. |
| `custom provider needs a URL (set llmCustomUrl in the same save)` | The custom provider's one hard rule — the URL has to come in the same save as the key. |
| `ChatGPT sign-in sends no API key (save an empty llmOpenAIKey to keep the stored key, or keep api_key)` | The two OpenAI auth methods contradict each other. Pick one. |

## An agent never answers

1. **Exact login?** A trigger fires on a mention of the role's **exact** bot login — that is
   its whole matching rule. Autocomplete-inserted handles are the usual miss.
2. **Is the trigger there?** Triggers are the one managed kind the hub does not stamp, so they
   are console-deletable even though the connector is not. A deleted trigger is a quietly
   incomplete install: run the wizard's edit, or **Repair connection**.
3. **Working hours?** Outside a window the hub publishes no due task, so nothing starts. A
   message that arrives at 23:00 for a 09:00–17:00 agent is answered when the window opens —
   work waits, it is not interrupted.
4. **Is it excluded?** An issue carrying the exclude label is left off the refiner, by design.
5. **The right forge?** The bots, hook and triggers belong to one organisation on one
   instance. Check the install's address before suspecting the trigger.

## The LLM check says unreachable or unknown

**Check now** on Settings asks the hub whether each (provider, model) pair the roles name can
serve a request ([Setup → Check now](setup#check-now)). What each answer means:

| State | What to do |
|---|---|
| `unreachable` | The hub asked and the pair could not serve — the hub's own failure sentence rides along with it. Usually the provider name, the key behind it, or the provider being down. |
| `unknown` | The question went unanswered: a hub too old to have the check route, a refused call (approve `llm:check`), or a `custom` pair, which the hub cannot probe from a provider and model name alone. Not health either way. |

A flagged check is a diagnostic, not drift: it blocks nothing, and no pass can write an
unreachable provider away. Only the button asks — the periodic pass never pings a provider
on a timer.

## A console edit is refused with 409

That is the ownership stamp, not a bug: every AInsel resource the wizard created carries
`managedBy: plugin-forgejo`, and the hub admits only the named manager. Edit through the
wizard. The gap to know about is triggers — they carry no stamp, so they *can* be edited or
deleted in the console, which is how an install ends up quietly incomplete.

## A route answers 401 or 403

- **`missing credential` (401)** — the call did not arrive through the hub's proxy. That is
  by design: a background read would see every group the chart grants the plugin. Call the
  hub's `/plugins/forgejo/api/…`, not the pod.
- **`admin only` (403)** — setup and settings routes need a signed-in **admin user**, on HTTP
  and over MCP alike. The work routes need no more than a caller.

## The plugin will not start, or starts and is not Ready

- **`image digest is required`** — the chart refuses to render without `image.digest`, because
  AInsel pins a plugin by digest. A tag is not a substitute: a signed release has to bind to
  exactly what runs.
- **The image is rejected by verification** — AInsel requires a signature
  (`plugins.requireSignature`, default true). The trust config has to name the release
  workflow's OIDC identity; see [Installation](installation).
- **The manifest and the routes disagree** — the binary checks at start that the API base
  path, MCP path, UI entry and skill paths in `ainsel-plugin.json` are the paths the server
  mounts, and refuses to start when they are not. The error names the field on both sides.
  This is a build fault, not a configuration one.
- **`/data` is not writable** — the pod runs with a read-only root filesystem; the PVC is the
  only place the plugin may write. Without it the stores cannot open.

## Cost or duration reads "—"

The hub's cost series covers about seven days. A run older than that has no cost line, and
the work view says "—" rather than showing a number it does not have. Same for a run that
never finished: time is what a run cost the moment it stopped.

## Resources outlived their install row

A row can be lost — a wiped database, the fresh-start cutover — while the bots, teams and
webhook it named are still standing. The install store is the only record of what the plugin
created, so those resources cannot be found from AInsel's side.

Use the wizard's **Clean up leftovers** (`setup_orphans_plan`, then
`setup_orphans_remove`). It works off the naming convention, skips anything another install's
row still references, and deletes exactly the list it showed. Read the plan first: a
convention name can also be one a human chose by hand.

## Links point at the wrong Forgejo

Every link is built from the address the install was **created** against, not from the
environment variable the pod happens to carry. Two installs on two instances produce links to
two instances — that is the intent. If one is wrong, the install was created against the wrong
address: fix the instance list on Settings and re-create or repair the install.

## Where to go next

- [Setup Wizard](setup) — the flow, the rails, the removal and the sweep
- [Credentials and Access](credentials) — the two grants, and what each one unlocks
- [Installation](installation) — digests, signatures, uninstall order
