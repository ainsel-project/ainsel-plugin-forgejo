---
name: forgejo
description: Forgejo workflow conventions for AInsel agents — which Forgejo MCP tools to use, the refiner → developer → reviewer @mention handoff, and the branch, PR title and issue-closing conventions every pull must follow.
---

### Reading the pull

All forge access goes through the Forgejo MCP server registered for your
agent — it authenticates with your bot's own token. Never clone repositories,
never use `bash` for forge work; the MCP tools are the only supported path.

- `get_pull_request_by_index` — read one pull request
- `get_issue_by_index` — read the issue the pull closes
- `list_issue_comments` — read an issue's or pull's comments
- `list_pull_request_files` — the changed files of a pull
- `get_pull_request_diff` — the full diff of a pull
- `get_file_content` — read one file for context (owner, repo, filepath, ref)

### Reading CI

- `get_pull_request_status` — the CI verdict for one pull, in one call: it resolves
  the pull's own head commit and answers `green`, `failing`, `pending`, `no_ci`, or
  `unknown`. Read the verdict, never the lists
- `get_commit_statuses` — the per-context statuses for one commit (it takes a sha:
  the pull's `head.sha`, never a branch guess)
- `list_workflow_runs` — Actions runs for a repository, by `head_sha`. Not in the
  reviewer's own tool set: on a forge too old for `get_pull_request_status`, use
  `get_pull_request_by_index` + `get_commit_statuses` instead. Unfiltered, this tool
  returns the repository's newest runs — usually another branch's

`no_ci` means nothing reported for that commit. It is the absence of evidence, not
a pass — the same goes for `unknown`, where the forge would not answer.

### Writing the review

- `create_pull_review` then `submit_pull_review` — a review is two calls; a
  PENDING review is invisible, never stop after the first
- `create_issue_comment` — the one notification comment the review is answered by

### Your handoff: reviewer → developer

Submit a formal review, then post one notification comment mentioning the pull's
opener. On a pull opened by an agent that is the developer's bot login, which
your persona carries.

### The conventions a pull is written against

A pull that follows the conventions reads predictably: branch
`<type>/<issue>-<slug>`, title `<type>(#<issue>): <description>`, a body that
carries `Closes #<issue>` along with what changed, why, and how to verify, and
Conventional Commits — one logical change each. Deviations from them are
suggestions, not blockers. Never merge a pull yourself — a maintainer merges.

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
