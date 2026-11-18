# Managed Agents — created-only, plugin-owned, globally configured

**Date:** 2026-10-30 · **Repos:** `ainsel-plugin-forgejo` (reference), then `ainsel-plugin-github` · **Decided with:** dpinsel

## Problem

The setup wizards can *adopt* existing hub resources (agents, personas, images, MCP
servers). Adoption made resource state unknowable (AInsel #22 / plugin PR #26), split
configuration across the console and the wizard, and left plugin improvements unable
to reach already-installed agents.

## Decisions (validated)

1. **Created-only.** Adoption is deleted. Every install's agent, role image, persona,
   connector, triggers are always created by the plugin and tracked in the install
   record. The plugin is the *sole writer* of what it created; its Settings page is
   the only offered edit surface. Wizard source lists (agents/personas/images/MCP)
   and the `AgentModes created|adopted` machinery are removed. Plugin-github PR #26
   is closed as superseded (its `GetAgent` and patch-shaped `UpdateAgent` survive —
   the reconcile uses them).
2. **Settings are global per role.** Fields (role ∈ refiner/developer/reviewer):
   `personaText`, `skillText`, `llmProvider` + `llmModel`, MCP row config values
   (URL, token env wiring). Values apply to **all installs**; no per-install overrides
   this round (YAGNI).
3. **Fixed membership, editable values.** The set of skills and MCP endpoints is the
   plugin manifest's — read-only in settings UI ("provided by plugin vX"). Membership
   changes only by plugin upgrade. Tools: fixed too (R7 rails + shipped defaults,
   plugin-version decision).
4. **Dirty-flag winner rule.** One boolean per field, set on first admin edit.
   Plugin upgrade: untouched fields auto-adopt the new shipped default and reconcile;
   edited fields are kept and surfaced as "new version available — apply?" with a
   per-field diff. Admin edits are never silently lost; untouched agents improve
   after every upgrade.
5. **Skill text lives plugin-side.** The plugin serves `edited text if present, else
   shipped default` at the manifest's skill path. Hub pluginsync re-fetches skill
   bodies **every pass** (`pluginSyncInterval = 30s`, `SyncOnce` → `reconcileSkills`
   → `fetch` → upsert when body changed), so admin edits reach the skill registry
   within ~30s with **no new hub permission**. A failed fetch keeps the stored body —
   also on plugin pod restarts; convergence is self-healing.
6. **Fresh-start cutover.** No migration. On first start after this upgrade the
   plugin detects legacy install records, deletes every plugin-created resource it
   still tracks ids for, logs loudly, and **leaves adopted resources standing**
   (safer than the old removal, which deleted adopted agents too). It drops a row only
   when every tracked deletion succeeded (a 404 on the delete itself counts: the
   resource is gone) or was deliberately skipped (no Forgejo admin token for the bots,
   team and hook — still a logged manual step). A deletion that merely **failed** — the
   hub refused, was unreachable, or would not mint its background credential — keeps
   the row stamped `retiring`, and the next start re-runs the same pass over it: the row
   is the only record of what the plugin created, so dropping it over a transient
   failure orphans the resource for good. A retiring row is hidden from the wizard's
   install list and the work view, but its slug stays reserved and the remove route
   still reaches it. The new wizard then runs once per install.
7. **`managedBy` bolt-on (phase 2).** Hub REST currently forces `managedBy: "user"`
   on create (refinement of AInsel #146). Follow-up hub issue: *allow plugin-
   authenticated callers to stamp `managedBy: "plugin-<id>"` on resources they
   create*. After it lands, the plugins set the stamp at create time and #146's 409/
   read-only UI makes sole-writership physical. Until then it stays a convention.
8. **Reconcile pass** (replaces forgejo's drift-fix loop, generalizes it).
   Triggers: settings save (affected resources), pod start after upgrade, manual
   "Check now". No periodic timer. Per role: read prior (`GetAgent`), compute desired
   = defaults × settings, diff, write only diffs (exact values — plugin owns these
   resources), update install records, log a summary ("N updated, M refused"); a
   refused write is a logged warning, never a halt. Selection sets (which skills/MCP
   an agent names) come from the manifest — fixed membership enforced in code.
9. **Slimmed wizard.** Setup collects only per-install facts (target, org, admin
   token → bots/teams, webhook, exclude label, AInsel group). Persona/LLM/tools/skill
   choice move to Settings. Plan/preview stays. Plugin-github PR #24 (per-install
   target) rebases onto this afterwards.

## Work plan

Per repo, stacked PRs (`feat/managed-agents` first, later PRs branch off it):

1. `feat(settings): role settings table + GET/PUT API + dirty-flag tracking`
2. `feat(setup): created-only wizard + fresh-start cutover`
3. `feat(reconcile): startup/save/upgrade reconcile pass`
4. `feat(skill): serve settings-overridden skill text`
5. `feat(ui): settings page + wizard simplification` (+ docs, version bump)

Then mirror to `ainsel-plugin-github`, and distill the pattern into
`ainsel-plugin-template`.

## Data model (PR 1)

New `internal/settings` package + `settings` table in the existing sqlite store:

- One row per `(role, field)`; columns: `role TEXT`, `field TEXT`, `value TEXT`,
  `dirty INTEGER` (0 until first admin edit), `default_version TEXT` (plugin version
  whose shipped default produced the current effective default), PK `(role, field)`.
- Shipped defaults: `content/personas/…` (personaText), `content/skills/…/SKILL.md`
  (skillText), compile-time consts for LLM and MCP wiring.
- `GET /api/settings` → per role × field: effective value, is dirty, default text,
  plugin-version of default. `PUT /api/settings` → update values (marks dirty),
  optional `reset: true` per field (clears dirty, re-adopts default). No reconcile in
  PR 1; PR 3 hooks the pass onto PUT.
- Validation: skill text must keep its SKILL.md frontmatter (name/description);
  refused PUTs answer with a field-level error, never a partial silent write.