# Roles and Tool Sets

Three roles work a Forgejo organisation. Each ships with a persona, a workflow
contract, a working directory and a set of tools — and with rails on what it may
never be given.

## The three roles

| | Refiner | Developer | Reviewer |
|---|---|---|---|
| **Works on** | Issues | Branches and pull requests | Pull requests |
| **Reads** | The issue and its links, the code | The code and the spec | The pull request, its diff and history |
| **Writes** | Issue comments, issue state, labels | Branches, commits, files, pull requests | Reviews and review comments |
| **Ships** | — | — | The PR author's reviews |
| **Persona** | `content/personas/refiner.md` | `content/personas/developer.md` | `content/personas/reviewer.md` |
| **Workflow section** | `content/skills/forgejo/refiner.md` | `…/developer.md` | `…/reviewer.md` |
| **Default working directory** | `/agents/refiner` | `/agents/developer` | `/agents/reviewer` |

The refiner's persona is the one to read first: it carries the "Decide, don't ask"
principle the whole flow depends on — an ambiguity is resolved by investigation and
proposed as a decision, not pushed back at the issue author.

## What a tool set is

A tool set is a named list of MCP tool names, and it is what the guard rails are
written against. A role's grant lands on its **agent**: `enabledTools` holds the name
of the set that belongs to that role (`forgejo-<role>-<slug>`), filled in at create and
corrected by the re-apply pass.

Names have to match the catalogue **exactly**, because a name no MCP server provides
is not an error at write time — it is a tool that never arrives at runtime. The
wizard rejects a name outside the catalogue instead, and names every bad one in one
answer rather than one per round trip.

Two spellings are the ones to get wrong:

- **`fetch_github_file` belongs to the plugin's `ainsel` MCP server** — the spelling
  that works, because the plugin registers that server for its agents. A bare
  `fetch_url` is a different tool and one nothing provides.
- **`search_code` is a real `forgejo` tool** — the upstream server offers it, so a
  grant may name it. What the refiner's persona does not do is rely on it: it has no
  code-search step, so to find a symbol it lists the likely directories and reads the
  candidates.

## The shipped sets

| Role | Tools | What they are |
|---|---|---|
| **Refiner** | 14 | Reading an issue, its comments, a file, a directory, a tree; reading a pull request; creating and editing issue comments; adding and removing labels; updating an issue. |
| **Developer** | 44 | Everything the refiner has, plus `bash`, branches, file create/update/delete, pull requests, notifications, actions and workflow runs. |
| **Reviewer** | 23 | Reading a pull request, its diff, files, commits, statuses, reviews and comments; `create_pull_review` and `submit_pull_review`; issue comments; labels. |

The refiner's set is a strict subset of the developer's: the difference is 30 tools
that touch code and pulls. The reviewer shares little with either — it reads pulls and
writes reviews, and holds no file or branch tool at all.

What is in no set at all is the point. **`merge_pull_request` appears nowhere**:
merging stays a human act, so a bot that can read, comment and request changes cannot
push the button. Nothing deletes an issue or a pull request either — the only delete
tools anywhere are the developer's `delete_branch`, `delete_file` and
`delete_issue_comment`.

Every set also carries the plugin's own `list_work` and `get_work_item`.

## What a role may never have

The rails are code, not documentation, and the wizard enforces them
(`content.ForbiddenTools` and `content.CheckTools`):

| Role | May never be given |
|---|---|
| **Refiner** | The eight tools that write code or open pulls — `bash`, `create_branch`, `delete_branch`, `create_file`, `update_file`, `delete_file`, `create_pull_request`, `update_pull_request` — and both review-writing tools |
| **Reviewer** | The same eight; it writes reviews instead |
| **Developer** | The two review-writing tools — `create_pull_review`, `submit_pull_review`. It may *request* a review (`create_review_requests`); it may not cast one |

So the developer opens pulls but never reviews one, and the reviewer reviews but never
touches a branch. An edit that names a forbidden tool is refused, and the answer lists
every violating tool rather than the first one. The checklist the wizard shows you is
generated from the same list, so a tool the role may not have never sits in front of
you to be checked.

The rails are the whole story on what is refused; they are not a claim that the
shipped sets are minimal. A set that leaves out a tool the persona leans on is a
warning for you to weigh, not a refusal.

## Editing a set

The wizard's role step is a checklist over the catalogue, pre-ticked with the shipped
set. The saved value replaces the default for that install, and a later wizard run
uses what is stored there rather than the shipped copy — which is what keeps a
deliberate edit from being reset by the next apply.

The rails apply to an edit exactly as they apply to a default. A set that leaves out a
tool the persona leans on is a warning for you to weigh, not a refusal.

## Turn timeout

Every agent has a **turn timeout**: how long the hub lets a task started for that
agent run before it stops it. Defaults are 10 minutes for the refiner, 60 for the
developer and 20 for the reviewer, and the wizard's role step carries them.

## Persona and workflow text

A role's persona and its section of the workflow skill are per-role texts an admin
owns, kept in the plugin's settings and pushed to the persona resource and the skill
row on save. A role on its own branch keeps its own copy of both. Empty means shipped
default — see [Setup Wizard](setup) for the Settings screen, and
[The Forgejo Workflow Skill](workflow-skill) for what a role's section is composed into.

## Where to go next

- [The Forgejo Workflow Skill](workflow-skill) — the handoff the three roles share
- [Setup Wizard](setup) — where the checklist and the defaults are edited
- [MCP Tools](mcp-tools) — the tools the agents call
