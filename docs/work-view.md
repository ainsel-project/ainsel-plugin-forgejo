# Work view

The work view shows what AInsel agents are doing on Forgejo: repositories, the issues and pull requests in each, the runs that touched them, and their cumulative cost and duration.

Only work that AInsel runs touched appears in the view. Runs with no subject (e.g., cron or panel invocations) are excluded, as are events older than AInsel's retention window.

## How runs map to issues and pull requests

A run's subject is the Forgejo issue or PR it was about. AInsel knows the subject from the webhook event that triggered the run; the plugin reads it from the invocation record.

For pull requests, the subject is the PR itself *or* the issue it closes, depending on which one the PR references. The three sources are checked in order:

1. **Closing keyword in the PR body** — e.g., `Closes #42`, `Fixes #100`, `Resolves #30`. The first usable number wins. (Match is case-insensitive and whole-word, so "Discloses #5" does not close issue 5.)
2. **Conventional-Commit scope in the PR title** — e.g., `feat(#264): add search` links the PR to issue 264.
3. **Numbered branch name** — e.g., `feat/896-…`, `fix/42-something`. The number after the first slash and before the first dash is the issue. (The dash is required; `feat/896-something` links to 896, but `feat/896something` does not.)

If none of these match, the PR is shown as a standalone item.

## How work is grouped and displayed

The page answers "which threads need me" first, so its header and summary come before the table.

### Period

The **Period** select sets the window the server reads, passed as the `since` query on `GET /work`: last 24 hours (`1d`), last 7 days (`7d`) or last 30 days (`30d`). The default is 30 days, the same window the server answers when asked for nothing. Changing the period fetches again; the page does not filter a window it already holds.

### Summary strip

Five figures sit above the table, counting every thread of the period whatever the filters show:

- **Threads** — every issue and standalone PR in the period.
- **Repositories** — the repositories those threads are in.
- **In progress** — the threads in the In progress view.
- **Agent time** and **Spend** — each run counted once. An issue's own time and cost are added, then each nested pull request's own, so a pull request is never folded into its issue twice. Spend reads "—" if any priced run's cost is unknown, the same rule as a row's cost. Because a row adds up the same thread the same way, the rows' costs add up to Spend.

### Views and filters

The main panel has four view tabs, each with its count: **All**, **In progress**, **Needs attention** and **Done**. The three groups are exclusive, so the counts add up to All:

- **In progress** — any run on the thread is `queued` or `running`, whatever its newest run did.
- **Needs attention** — not in progress, and the thread's newest run is `failure` or `timeout`.
- **Done** — everything else.

Beside the tabs, a search box matches the title (case-insensitive) or the number, with or without a leading `#`. A **Repository** select and a **Role** select narrow the rows further. Both take their choices from the data on the page, so every choice matches something. Any change to the view, the search or a select returns the table to its first page. The counts on the tabs ignore the search and the selects: they say how much work is in each view.

### The table

Rows are sorted by last activity (the most recent run, or for issues with nested PRs, the most recent run of any PR), across every repository.

Cost reads "—" when it is unknown. A thread's cost is unknown if any run on it — the item's own or a nested pull request's — has an unknown cost, the `CostKnown` flag on each run and item reflects this, and AInsel's metrics only reach back 7 days, so an older run's cost is unknown rather than zero. A thread no agent ran has nothing to price either, and reads "—" rather than `$0.00`.

Each row is one thread, with:

- **Thread** — an issue or PR icon, the title as the button that opens the thread's panel, then the repository and `#number` in muted text. The `↗` anchor beside the title still opens Forgejo in a new tab.
- **Hand-off** — three segments for refine, develop and review, each coloured by that role's newest run across the thread: `done`, `in progress`, `waiting`, `failed`, or `not started`. A label beside them names the furthest stage reached, such as `Review · in progress`. A nested pull request's review therefore moves its issue's hand-off along.
- **Linked PRs** — the pull requests nested under the issue.
- **Status** — a pill for the thread's newest run. While any run is still going, the pill names the newest of those, so a thread in In progress says what is running rather than what finished last.
- **Runs**, **Agent time**, **Cost** — the thread's totals: the item's own numbers plus each nested pull request's, every run counted once (`threadTotals`, `ui/src/format.ts`) — the same addition the summary strip makes, so the rows add up to Spend. A reviewer that ran on the pull request therefore shows in the row that links it, and an unpriced run anywhere on the thread makes the row's cost unknown.
- **Updated** — how long ago the thread last had activity (its last activity time), with the absolute time as a tooltip.

### Paging the table

The table is paginated in the browser. Pagination is client-side because the flattened, cross-repo ordering above exists only there, and because the payload is already bounded by AInsel's retention window — `GET /work` still returns every item, unchanged.

The page cuts the *sorted* list, so ordering is a property of the whole set and never of one page: the first row of page 2 is the 26th-newest item, a page boundary never reorders anything, and it never lands on a repository boundary either.

- **25 rows per page by default**, selectable at 25 / 50 / 100.
- The footer under the table shows **"Showing X–Y of Z threads"**, the rows-per-page selector, the page position, and icon prev/next buttons. Prev is disabled on the first page, next on the last.
- The footer appears only when there is more than one page. At 25 rows or fewer the table already is the whole list, and there is nothing to page with.
- **The summary strip counts every row and every repository**, never the visible page. It answers "how much work is there"; the footer answers "where in it am I". The two are meant to disagree about the count — the footer's "of Z" is what makes that unambiguous.
- If the row set shrinks while you are deep in it — the retention window ages items out — the page clamps to the last page that has rows. The table never renders empty while the summary above it counts items.

The row-number column is drawn by the console's table primitive, which numbers the rows it is given and takes no offset, so it restarts at `01` on every page. The footer's "X–Y of Z" is what carries the real position.

Which page you are on is component state. It survives opening and closing a thread's panel, and it is lost when you navigate away, for the same reason the panel is: the console router only knows the static manifest paths, so a page number cannot travel in a URL either.

### Opening an item

Clicking a thread's title opens its detail as a panel beside the table, and the table keeps its place. The open row is highlighted. The panel shows:

- **Header** — the kind, repository and number, the title, the thread's status pill, and an "Open on Forgejo ↗" link.
- **Summary cards** — the thread's cost, agent time and run count (with the number of active runs when any are `queued` or `running`), and last activity. These are the thread's totals, the same numbers the row shows, so the panel and the row that opened it never disagree. For an item with nested pull requests, a line under the cards keeps the split visible — the item's own cost, runs and time, then each nested pull request's — so the item's own figures are still readable.
- **Run history** — the item's own runs as a timeline, newest first, each with its role, status, agent, start, duration and cost, and its run id. A run that has not finished shows no duration rather than a zero, and an item with no runs of its own (a PR-holder item) says so. The timeline stays the item's own even though the cards above it count the whole thread.
- **Linked pull requests** — for an issue, each nested pull request with its newest run's status, its own cost and time. Its title opens that pull request in the same panel, which then shows that pull request's own numbers and runs and, since a pull request has none nested under it, no list of its own. The issue's row stays highlighted while its pull request is open. The split line above counts each of these pull requests once, in the thread it belongs to.

The panel closes from its **Close details** control, which returns to the table alone. Selection is component state, not a page of its own: the console router only knows the static manifest paths, so an item's identity cannot travel in a URL. One accepted limitation follows from that — the browser's back button does not close the panel; the "Close details" control does.

### Exporting

**Export CSV** downloads the rows the view, search and selects leave, all of them and not just the page on screen. It is built in the browser from the rows already on the page, so the file matches what the admin is looking at. The columns are `kind`, `number`, `title`, `repo`, `status`, `runs`, `agent time`, `cost` and `last activity`. Agent time uses the table's own reading (`3m 05s`), cost is a plain number of dollars and empty where the hub could not price it, and the last activity is an ISO timestamp. Runs, agent time and cost are the thread's totals — the item's own plus each nested pull request's, each run counted once — the same numbers the row shows, so the file adds up to the Spend strip.

## Linking rules and edge cases

- **PR and issue with the same number** — Forgejo shares one number space, so `#265` could be either a PR or an issue. The first source that names a number wins (closing keyword > scope > branch), so a PR that says `Closes #264` links to 264, not to any PR numbered 265.
- **PR with no reference** — appears as a standalone work item, not under issue 0 or any phantom issue.
- **Run with no subject** — excluded entirely. Background runs (cron, matrix, panel connectors with no event context) have no subject and do not appear in the work view.
- **Event past retention** — AInsel keeps events for a finite window (typically days or weeks). Runs whose event has been garbage-collected are dropped from the work view. When durable run subjects are implemented (AInsel addition 2), older runs will stay visible with a placeholder subject.

## MCP tools

The plugin also exposes two read-only MCP tools for agents to inspect what is in flight:

- `list_work` — filter work items by repository, agent or time range, with Forgejo URLs, cost, duration and run count.
- `get_work_item` — fetch one work item by repository and issue/PR number, with all its runs.

These are useful for agents that need to know what other agents are working on, without scraping the forge directly.
