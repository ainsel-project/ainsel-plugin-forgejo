---
name: forgejo
description: Forgejo workflow conventions for AInsel agents — which Forgejo MCP tools to use, the refiner → developer → reviewer @mention handoff, and the branch, PR title and issue-closing conventions every pull must follow.
---

### Reading the work

All forge access goes through the Forgejo MCP server registered for your
agent — it authenticates with your bot's own token. Never clone repositories,
never use `bash` for forge work; the MCP tools are the only supported path.

- `get_issue_by_index` — read one issue
- `list_issue_comments` — read an issue's or pull's comments
- `get_pull_request_by_index` — read one pull request
- `get_file_content` — read one file (owner, repo, filepath, ref)
- `list_repo_contents` — list the entries of a directory (`path` must be a
  directory; a file path errors and points at `get_file_content`)
- `get_repo_tree` — the file tree, `recursive=true` for a subtree in one call.
  There is no code-search tool: list the likely directories, then read them

### Writing what you found

- `create_issue_comment` — post the refinement, and then the handoff comment

A refiner writes comments and nothing else: no branch, no file, no pull. Do not
apply or remove labels — label management belongs to humans.

### Your handoff: refiner → developer

After posting the refinement, post a **second, separate** comment mentioning the
developer's bot login — the exact handle your persona carries for the handoff —
with a 1–2 sentence summary of what to implement and where to start. No handoff
comment when open questions or blocking dependencies remain.

### The conventions your spec is read against

Work is traced by issue number: the developer's branch is `<type>/<issue>-<slug>`
and their pull is titled `<type>(#<issue>): <description>`, and the pull body
carries `Closes #<issue>`. Refine one change per issue, and name the affected
files in the spec, so the pull that closes your issue can be read back from it.

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
