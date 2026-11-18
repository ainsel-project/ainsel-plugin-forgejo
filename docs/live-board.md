# Live Board

The Live page is the console's view of what the fleet is doing right now: what is
running, what is waiting, what just finished, and each agent's day. It is the plugin's
`/now` page, and it holds the last 24 hours across every install.

Everything on it is read from the hub **as the signed-in caller**, so a caller who
cannot see a group's runs does not see them here either.

## The five-second read

Six figures across the top, all over the same window:

| | |
|---|---|
| **Active agents** | Agents with a run running or queued now. |
| **Queued runs** | Waiting for a slot, with how many agents are waiting. |
| **Runs (24h)** | With the failed count underneath. |
| **Success rate** | Of the runs that have finished. "No finished runs yet" is different from 0%. |
| **Agent time (24h)** | Finished runs only — time is what a run cost the moment it stopped. |
| **Spend (24h)** | With "N of M runs unpriced" when some runs have no cost line. |

Below them, two charts: **Throughput** — runs started per hour across the day, stacked by
role, each hour readable on hover — and **Agent time by role**, which is where you see
whether the day went to developing or to reviewing.

## The agent fleet

One row per agent that did anything in the window: its status, the work it holds now, a
day of twelve two-hour blocks, and its runs, successes, time and cost.

| Status | Means |
|---|---|
| **Running** | A run is running now. |
| **Queued** | Its next run is waiting. |
| **Last run failed** | Nothing running; the most recent one failed. |
| **Idle** | Activity, but nothing outstanding. |

The twelve blocks are the agent's day: a block with activity reads darker, and a block
that holds a failed run is marked. Hovering names the window — `14:00–16:00 · 3 runs`, or
`3 runs, 1 failed`. An agent with nothing in the window is not in the table at all.

## The run queue

Three tabs over the runs themselves — **Running**, **Queued**, **Recently finished** — each
with a count. Columns are the issue or pull request, the repository, the agent, how long
it has run, and the status:

| Status | |
|---|---|
| Running | In progress. |
| Queued | Published and waiting. |
| Succeeded | Finished cleanly. |
| Failed | Ended badly. |
| Timed out | Stopped by the agent's turn timeout. |

The time column reads **Started** for a running run, **Queued** for a waiting one and
**Finished** for a finished one — a queued run has no duration, and pretending otherwise
would invent one.

## Freshness

The board refreshes every 30 seconds while the tab is open, and says when it last did:
`Live · updated 12s ago`. Switch tabs and it pauses; come back and it refreshes at once.
**Pause** holds the board on what it last showed, with the reason next to it — for
screenshots, and for reading a board without it changing under you.

A run that is not yet in the hub's list is held out of the queue for a moment rather than
flashing a status; a board with no runs says **"No runs started in the last 24 hours."**
— which is the truth rather than a failure.

## What it does not show

- Anything older than 24 hours. History is the [Work View](work-view)'s job.
- Costs beyond the hub's cost series. The window here is short enough not to notice; the
  work view's is not, and [Work View](work-view) says what "—" means.
- Runs AInsel did not start. The board is AInsel's fleet, not Forgejo's CI.

## Where to go next

- [Work View](work-view) — issues and pulls grouped as work, over a longer window
- [Architecture](architecture) — where these numbers come from
