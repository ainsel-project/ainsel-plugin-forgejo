// The shared readings of the work screens: the three ways they say what a run cost and how
// long it took, plus the newest-run lookup. They live here rather than inside one page
// because the same numbers appear on the now page, in the work table, and on an item's own
// page, and an agent time written `3m 5s` in one place and `185000ms` in another reads like
// two different things — as would two different answers for which run is the newest.

import type { Item, Run } from "./api";

// formatDuration says a length of agent time in the largest unit that still leaves
// something to say: `42s` under a minute, `3m 05s` under an hour, `1h 02m` beyond it. The
// short unit is zero-padded so a column of them lines up and reads as a time rather than
// two numbers.
export function formatDuration(ms: number): string {
  const total = Math.floor(ms / 1000);
  if (total < 60) return `${total}s`;
  const minutes = Math.floor(total / 60);
  if (minutes < 60) return `${minutes}m ${pad(total % 60)}s`;
  return `${Math.floor(minutes / 60)}h ${pad(minutes % 60)}m`;
}

// formatCost says what a run cost, or that nobody knows. A cost the hub could not
// price is not zero: an agent that ran out of quota and an agent that ran for free
// look the same under a `$0.00`, and the first is the one the admin needs to see.
export function formatCost(costUsd: number | null | undefined, known: boolean): string {
  if (!known || costUsd === null || costUsd === undefined) return DASH;
  return `$${costUsd.toFixed(2)}`;
}

// formatRelative says how long ago something happened, in the coarsest step that
// still distinguishes it from what came before: `just now`, `5 min ago`, `3 h ago`,
// `2 d ago`. The caller passes `now` so a screen that renders several of these
// agrees with itself, and so a test can fix the clock instead of racing it.
export function formatRelative(iso: string, now: Date): string {
  const then = new Date(iso).getTime();
  const elapsed = now.getTime() - then;
  if (elapsed < 60_000) return "just now";
  const minutes = Math.floor(elapsed / 60_000);
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} h ago`;
  return `${Math.floor(hours / 24)} d ago`;
}

// formatAgo says how long ago the live board last refreshed, for its status line. It
// counts seconds where formatRelative would say `just now`, because the board refreshes
// every half minute and a status that reads `just now` for the whole wait says nothing.
// The first few seconds read as `just now` so a fresh refresh does not flicker `0s`.
export function formatAgo(elapsedMs: number): string {
  const seconds = Math.floor(Math.max(0, elapsedMs) / 1000);
  if (seconds < 5) return "just now";
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} min ago`;
  return `${Math.floor(minutes / 60)} h ago`;
}

// The quiet answer for "nothing to show here". A blank cell reads as a rendering
// that failed; a dash says the screen looked and found nothing.
export const DASH = "—";

// newestRun is the run that started last: its status is what the thread is doing right
// now, where an older run's would report work that has since moved on. The work table's
// Runs column and the item subview both read it, so ties and the empty list answer the
// same way in both: on equal start times the earlier-listed run wins, and an empty list
// has no newest run and returns null.
export function newestRun(runs: Run[]): Run | null {
  let newest: Run | null = null;
  for (const run of runs) {
    if (newest === null || new Date(run.start).getTime() > new Date(newest.start).getTime()) {
      newest = run;
    }
  }
  return newest;
}

// The run statuses the hub reports are queued, running, success, failure and timeout. The
// screens say them in these words: a timeout is not a failure to the admin, who acts on the two
// differently, so the labels keep them apart.
export function statusLabel(status: string): string {
  switch (status) {
    case "queued":
      return "Queued";
    case "running":
      return "Running";
    case "success":
      return "Succeeded";
    case "failure":
      return "Failed";
    case "timeout":
      return "Timed out";
    default:
      return status;
  }
}

// statusTone is the colour a status pill takes. A running run is neutral, because it is
// happening rather than news; a queued one is waiting, so it is a warning. A status the screens
// do not know reads as stale, not as a success.
export type StatusTone = "default" | "ok" | "warn" | "err" | "stale";

export function statusTone(status: string): StatusTone {
  switch (status) {
    case "running":
      return "default";
    case "queued":
      return "warn";
    case "success":
      return "ok";
    case "failure":
    case "timeout":
      return "err";
    default:
      return "stale";
  }
}

// threadStatusRun is the run a thread's status pill reports. While any run on the thread is
// still going, it is the newest of those, so a thread in the In progress view says what is
// running rather than what finished last; otherwise it is the thread's newest run of all.
export function threadStatusRun(item: Item): Run | null {
  const runs = threadRuns(item);
  const active = runs.filter(isActiveRun);
  return newestRun(active.length > 0 ? active : runs);
}

// isActiveRun is a run that has not finished yet. Same pair the backend's work.Active keeps.
export function isActiveRun(run: Run): boolean {
  return run.status === "queued" || run.status === "running";
}

// isFailedRun is a run that ended without doing its work: a failure, or a timeout, which the
// hub records as its own status.
export function isFailedRun(run: Run): boolean {
  return run.status === "failure" || run.status === "timeout";
}

// threadRuns is every run on a thread: the item's own runs and the runs of the pull requests
// nested under it. A thread's hand-off, status and view are about all of that work, and so are the
// numbers the rows add up — see threadTotals, which uses the same set.
export function threadRuns(item: Item): Run[] {
  const runs = [...item.runs];
  for (const pr of item.prs) runs.push(...pr.runs);
  return runs;
}

// ThreadTotals is a whole thread added up: the item's own numbers plus each pull request nested
// under it, with every run counted once.
export interface ThreadTotals {
  runs: number;
  durationMs: number;
  // costUsd is the thread's cost, or null when there is nothing to price: some run in the thread
  // was never priced, or no run ran at all.
  costUsd: number | null;
  // costKnown is the summary strip's rule — false when a run the hub could not price is
  // somewhere in the thread. A thread with no runs is not unknown; it is empty.
  costKnown: boolean;
}

// threadTotals adds up a thread with the rules the summary strip has always used: an item's own
// numbers, then each nested pull request's own, never a pull request folded into its issue a
// second time. An item with no runs contributes nothing — it has no cost to know, and it does not
// make the sum unknown. The table's Runs, Agent time and Cost columns, the CSV export, the detail
// panel's cards and the strip all read it, so a row's cost and the strip's Spend are the same
// money added two ways (docs/work-view.md).
export function threadTotals(item: Item): ThreadTotals {
  let runs = 0;
  let durationMs = 0;
  let costUsd = 0;
  let costKnown = true;
  let priced = false;
  const add = (member: Item) => {
    runs += member.runs.length;
    durationMs += member.durationMs;
    if (member.runs.length === 0) return;
    priced = true;
    if (member.costKnown && member.costUsd != null) costUsd += member.costUsd;
    else costKnown = false;
  };
  add(item);
  for (const pr of item.prs) add(pr);
  return { runs, durationMs, costUsd: priced && costKnown ? costUsd : null, costKnown };
}

function pad(n: number): string {
  return String(n).padStart(2, "0");
}
