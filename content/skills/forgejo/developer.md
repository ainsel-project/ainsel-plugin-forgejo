---
name: forgejo
description: Forgejo workflow conventions for AInsel agents — which Forgejo MCP tools to use, the refiner → developer → reviewer @mention handoff, and the branch, PR title and issue-closing conventions every pull must follow.
---

### Reading the work

All forge access goes through the Forgejo MCP server registered for your
agent — it authenticates with your bot's own token. Never clone repositories for
forge work; the MCP tools are the supported path to the forge itself.

- `get_issue_by_index` — read one issue, and the refinement on it
- `list_issue_comments` — read an issue's or pull's comments
- `get_pull_request_by_index` — read one pull request
- `list_repo_pull_requests` — list a repository's pull requests (filter by
  author, state)
- `list_repo_contents` — list the entries of a directory (a file path errors and
  points at `get_file_content`)
- `get_file_content` — read one file (owner, repo, filepath, ref)
- `get_repo_tree` — the file tree, `recursive=true` for a subtree in one call.
  There is no code-search tool: list the likely directories, then read them
- `list_pull_request_files` — the changed files of a pull
- `get_pull_request_diff` — the full diff of a pull

### Reading CI

- `get_pull_request_status` — the CI verdict for one pull, in one call: it resolves
  the pull's own head commit and answers `green`, `failing`, `pending`, `no_ci`, or
  `unknown`. Ask with the pull's index; read the verdict, never the lists
- `list_workflow_runs` — Actions runs for a repository. Meaningful only with
  `head_sha` set to the pull's head commit; unfiltered it answers about the
  repository, not about your pull
- `get_workflow_run`, `list_action_run_jobs`, `get_action_job_logs` — the failing
  run, its jobs, and the log text that says why

`no_ci` means nothing reported for that commit — report it as missing CI, never as
"CI passed".

### Writing the change

- `create_branch` — create the working branch, from the default branch's latest state
- `create_file` / `update_file` — write changes
- `create_pull_request` — open the pull (never a second pull for one issue)
- `create_issue_comment` — post the summary comment a review outcome asks for

### Your handoff: developer → reviewer

Opening the pull is the handoff: the reviewer is triggered by the pull request
itself, not by a mention. After review feedback, push the fixes and post one
comment summarising what was addressed, **without mentioning any agent** — a push
does not re-trigger the reviewer; a human re-requests the review by mentioning the
reviewer's bot login. On approval, say nothing further except a brief "Ready to
merge."

### Conventions every pull follows

- **Branch naming** — `<type>/<issue>-<slug>`, where `<type>` is a
  conventional-commit type and `<issue>` is the issue number being closed:
  `feat/42-confirm-dialog`, `fix/57-null-user`, `chore/61-update-deps`.
- **PR title** — `<type>(#<issue>): <description>`, e.g.
  `feat(#42): add confirmation dialog`. One issue per pull whenever the work
  is separable.
- **PR body** — always contains `Closes #<issue>` so merging the pull closes
  the issue; plus what changed, why, and how to verify.
- **Commits** — Conventional Commits (`feat: …`, `fix: …`, `docs: …`), one
  logical change each.
- Never force push to a shared branch; never merge a pull yourself — a
  maintainer merges.

Never mention a role that has nothing to do; never loop mentions between two
agents. Humans and the issue owner are mentioned only to ask or escalate.

### Seeing what is in flight

The AInsel Forgejo plugin contributes two **read-only** tools for seeing what the
agent fleet is working on. Use them instead of scraping the forge when you just
need to know what is in flight:

- `list_work` — the work items an AInsel run has touched (filter by repo, agent,
  since), with their Forgejo URL, cost, duration and run count
- `get_work_item` — one work item by repo and number (both required), with its
  runs, cost and URL

Both answer "what is being done where" from AInsel's record of the runs; they
never mutate anything.
