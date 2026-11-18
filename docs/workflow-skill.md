# The Forgejo Workflow Skill

The workflow skill is the contract between the three roles. It names the tools each
role uses, the handoff that passes work along, and the conventions every pull follows
— so a pull from one role is immediately readable by the next, and no role has to be
told twice.

It ships as an AInsel skill, not as prompt text the plugin appends: the hub registers
it as a skill row on install, and an agent gets it the way it gets any skill.

## What it holds

Each role gets its own section, and each section is that role's contract:

| Section | Covers |
|---|---|
| **Reading the work** | Which MCP tools to read with — `get_issue_by_index`, `list_issue_comments`, `get_file_content`, `list_repo_contents`, `get_repo_tree` — and the rule that all forge access goes through the forge MCP server, with your bot's own token. Never clone; never use `bash` for forge work. |
| **Writing what you found / the change** | The write tools a role has: refinement comments for the refiner, `create_branch` / `create_file` / `update_file` / `create_pull_request` for the developer, `create_pull_review` and `submit_pull_review` for the reviewer. |
| **Your handoff** | Who to mention, when, and when **not** to. |
| **Conventions** | Branch naming, PR title, PR body, commit style, and the two never-mays. |
| **Seeing what is in flight** | The plugin's two read-only tools, `list_work` and `get_work_item`, for asking what the fleet is doing instead of scraping the forge. |

### The handoff

The flow is a chain of mentions, and each role's trigger is a mention of its **exact**
bot login — the handle its persona carries:

1. **refiner → developer** — the refiner posts the refinement, then a handoff comment
   mentioning the developer's bot login with one or two sentences on what to implement
   and where to start. No refinement without it.
2. **developer → reviewer** — opening the pull *is* the handoff: the reviewer is
   triggered by the pull request itself, not by a mention. After review feedback the
   developer pushes fixes and posts one comment saying what was addressed, **without
   mentioning any agent** — a push does not re-trigger the reviewer; a human
   re-requests the review. On approval the developer says nothing beyond a brief
   "Ready to merge."
3. **reviewer** — reviews, and does not merge.

Mentions are the trigger mechanism, so the rules about them are load-bearing: never
mention a role that has nothing to do, never loop mentions between two agents, and
mention humans only to ask or to escalate.

### The conventions

| | |
|---|---|
| **Branch** | `<type>/<issue>-<slug>` — a conventional-commit type and the issue number: `feat/42-confirm-dialog`, `fix/57-null-user`, `chore/61-update-deps` |
| **PR title** | `<type>(#<issue>): <description>` — `feat(#42): add confirmation dialog`. One issue per pull whenever the work is separable |
| **PR body** | Always `Closes #<issue>`, so merging closes the issue; plus what changed, why and how to verify |
| **Commits** | Conventional Commits, one logical change each |
| **Never** | Force push to a shared branch; merge a pull yourself — a maintainer merges |

## How the document is put together

The served document is composed, not stored:

```
SKILL.md (the shell) + "## Refiner" + <refiner text> + "## Developer" + <developer text> + "## Reviewer" + <reviewer text>
```

Each role's section comes from the role's **skill text** in Settings, falling back to
the shipped section (`content/skills/forgejo/<role>.md`) when that role's box is empty.
Frontmatter in an edited text is stripped, so the document carries exactly one block —
the name and description of the skill itself. A section whose text is empty is skipped
rather than left as a bare heading.

Two consequences worth knowing:

- **An edit changes one section.** The refiner's text is the refiner's section, so a
  role on its own branch keeps its own contract without touching the others. The
  heading above it is what makes it a section.
- **What the agent reads is the row, not the box.** The settings answer carries no
  skill text; the copy in the hub's skill row is what a running agent sees, and a save
  pushes it there.

## Where it lives

| | |
|---|---|
| **Served at** | `GET /skills/forgejo/SKILL.md` — public, like the UI bundle, so an agent can read it without a session |
| **Hub row** | `forgejo-forgejo` — the manifest's skill name under the plugin's prefix |
| **Composed from** | `content/skills/forgejo/SKILL.md` plus one file per role |
| **Edited in** | Settings, per role (empty means shipped default) |
| **Removed with** | The `Plugin` resource |

The name in the document's frontmatter has to survive an edit: a document that loses it
has no name to register under. (A plugin-declared skill takes its row name and
description from the manifest — but the document an agent reads is still this one, and
it is expected to read as a SKILL.md.)

## Where to go next

- [Roles and Tool Sets](roles-and-tools) — which tools each role may hold at all
- [Setup Wizard](setup) — where the per-role texts are edited
- [MCP Tools](mcp-tools) — the plugin's own tools for seeing the fleet's work
