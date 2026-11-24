// The board's model: what the Live page counts, groups and colours, kept apart
// from how any of it is drawn. Everything here is pure over the runs one
// /work fetch returned, so the same answers back the KPIs, the charts, the
// fleet table and the queue — and each can be read without JSX in the way.

import type { Repo, Run } from "../api";

export const HOUR_MS = 60 * 60 * 1000;

// The window the whole floor covers: numbers, charts, timelines. One day,
// because the page is "what are the agents doing", and a run from last Tuesday
// is not what an agent is doing.
export const WINDOW_MS = 24 * HOUR_MS;

// How often the board refetches. Slow enough that the page costs about
// nothing while open, fast enough that "live" is not a stretch.
export const REFRESH_MS = 30_000;

// A failure this recent raises the banner. The banner answers "did something
// just break"; the day's failure count is the KPI's job.
export const FAILURE_BANNER_MS = HOUR_MS;

// The height of the throughput chart's plot, in px. Each stacked bar is a share
// of it, so the tallest hour fills it exactly.
export const TRACK_PX = 160;

// The newest finished runs the queue's "Recently finished" tab shows.
export const RECENT_LIMIT = 6;

// The statuses that mean a run has not finished. Same pair the backend's
// work.Active keeps; the board's queues split them into waiting and working.
export const STATUS_QUEUED = "queued";
export const STATUS_RUNNING = "running";

// The hub ends an invocation as success, failure or timeout (the hub's
// invocations package). A failure and a timeout both mean the run did not do
// its work, so the board counts them as one kind of failed run.
export const STATUS_SUCCESS = "success";
export const STATUS_FAILURE = "failure";
export const STATUS_TIMEOUT = "timeout";

// Each role gets a colour, and the same colour means the same thing in every
// band: the throughput chart, the split bar, the timelines and the queue rows
// all carry roles. A role the plugin does not know (an install may add its own)
// still draws — in the idle grey — rather than vanishing from the chart because
// nobody anticipated it.
// Each colour is a console theme token behind a plugin-level name, so the
// console's theme moves every hue on the board and an operator can still
// re-point one role (`--live-developer: …`) without touching the bundle. No
// literal colours: the bundle test refuses anything the theme cannot move.
export const ROLE_COLORS: Record<string, string> = {
  refiner: "var(--live-refiner, var(--warn))",
  developer: "var(--live-developer, var(--signal))",
  reviewer: "var(--live-reviewer, var(--stale))",
};
const ROLE_OTHER = "var(--ink-4)";

export function roleColor(role: string): string {
  return ROLE_COLORS[role] ?? ROLE_OTHER;
}

// roleLabel names a role the way a person reads it: `Developer`, not `developer`.
export function roleLabel(role: string): string {
  return role.charAt(0).toUpperCase() + role.slice(1);
}

// rolesOf lists the roles the runs carry, the known ones in their fixed order and
// any others after them alphabetically. The stack and the legend both read this,
// so a role sits in the same place in every bar.
export function rolesOf(runs: Run[]): string[] {
  const seen = new Set(runs.map((run) => run.role));
  const known = Object.keys(ROLE_COLORS).filter((role) => seen.has(role));
  const other = [...seen].filter((role) => !Object.keys(ROLE_COLORS).includes(role)).sort();
  return [...known, ...other];
}

// Flatten the response to bare runs. The nested pull requests are worked runs
// like any other, so they join the board beside their parent issue rather than
// hiding inside it.
export function flattenRuns(repos: Repo[]): Run[] {
  const runs: Run[] = [];
  for (const repo of repos) {
    for (const item of repo.items) {
      runs.push(...item.runs);
      for (const pr of item.prs) {
        runs.push(...pr.runs);
      }
    }
  }
  return runs;
}

export function isActive(run: Run): boolean {
  return run.status === STATUS_QUEUED || run.status === STATUS_RUNNING;
}

export function isFailed(run: Run): boolean {
  return run.status === STATUS_FAILURE || run.status === STATUS_TIMEOUT;
}

// endOf is when a run reached the state it is in: when it ended, or when it
// started if it has not ended. The failure banner and the "failed 18 min ago"
// labels age a run by it.
export function endOf(run: Run): number {
  return new Date(run.end ?? run.start).getTime();
}

// plural says a count with its noun, `1 run` or `3 runs`, so no sentence on the
// board reads `1 runs`.
export function plural(count: number, noun: string): string {
  return `${count} ${noun}${count === 1 ? "" : "s"}`;
}

// spendOf adds up what a set of runs cost, the way the work screens do. The sum is
// over the runs whose cost was measured, and it is known only when every run's was:
// one unpriced run makes the total unknown, which formatCost draws as a dash.
// That is the backend's rule for an item's or a repository's cost, applied here to
// whatever set of runs a figure is about.
export function spendOf(runs: Run[]): { costUsd: number; known: boolean; unpriced: number } {
  let costUsd = 0;
  let unpriced = 0;
  for (const run of runs) {
    if (!run.costKnown) {
      unpriced += 1;
      continue;
    }
    costUsd += run.costUsd ?? 0;
  }
  return { costUsd, known: unpriced === 0, unpriced };
}

// successPercent is the share of finished runs that did not fail, or null when
// nothing has finished yet. A run still going has no outcome, so it is not in the
// share at all; a board that counted it as a success would flatter the agents.
export function successPercent(runs: Run[]): number | null {
  const finished = runs.filter((run) => !isActive(run));
  if (finished.length === 0) return null;
  const failed = finished.filter(isFailed).length;
  return ((finished.length - failed) / finished.length) * 100;
}

// One row of the agents table: what the agent is, what it holds now, and the
// runs its timeline is drawn from.
export interface AgentRow {
  agent: string;
  // The role the agent's newest run was made in: the colour of its avatar.
  role: string;
  current: Run | null;
  runs: Run[];
  durationMs: number;
}

// groupAgents builds one row per agent, ordered by its most recent activity,
// so whoever just did something sits at the top even if its run finished and
// its "Current work" cell is empty.
export function groupAgents(runs: Run[]): AgentRow[] {
  const byAgent = new Map<string, Run[]>();
  for (const run of runs) {
    const owned = byAgent.get(run.agent) ?? [];
    owned.push(run);
    byAgent.set(run.agent, owned);
  }
  const rows: AgentRow[] = [];
  for (const [agent, owned] of byAgent) {
    owned.sort((a, b) => new Date(b.start).getTime() - new Date(a.start).getTime());
    // A running run is what the agent is doing; a queued one is what it does
    // next. So a running run wins even when a queued one started earlier.
    const current =
      owned.find((run) => run.status === STATUS_RUNNING) ?? owned.find((run) => run.status === STATUS_QUEUED) ?? null;
    rows.push({
      agent,
      role: owned[0].role,
      current,
      runs: owned,
      durationMs: owned.reduce((total, run) => total + (run.durationMs ?? 0), 0),
    });
  }
  rows.sort((a, b) => new Date(b.runs[0].start).getTime() - new Date(a.runs[0].start).getTime());
  return rows;
}

// AgentStatus is what the fleet's status pill says about an agent right now.
export type AgentStatus = "running" | "queued" | "failed" | "idle";

// agentStatus reads an agent's row. Working beats waiting, and both beat the last
// run having failed: an agent that is busy now is busy whatever its last run did.
export function agentStatus(row: AgentRow): AgentStatus {
  if (row.current?.status === STATUS_RUNNING) return "running";
  if (row.current) return "queued";
  if (row.runs.length > 0 && isFailed(row.runs[0])) return "failed";
  return "idle";
}

// The pills use the console's tag variants: a working agent is healthy (ok), a
// waiting one is pending (warn), a failed one is an error, and an idle one is
// quiet (stale).
export type TagVariant = "default" | "ok" | "warn" | "err" | "stale";

export const AGENT_STATUS: Record<AgentStatus, { label: string; variant: TagVariant }> = {
  running: { label: "Running", variant: "ok" },
  queued: { label: "Queued", variant: "warn" },
  failed: { label: "Last run failed", variant: "err" },
  idle: { label: "Idle", variant: "stale" },
};

// runStatus names a run's status for the queue's pill. An unknown status keeps
// its own word rather than being guessed at.
export function runStatus(run: Run): { label: string; variant: TagVariant } {
  switch (run.status) {
    case STATUS_RUNNING:
      return { label: "Running", variant: "ok" };
    case STATUS_QUEUED:
      return { label: "Queued", variant: "warn" };
    case STATUS_SUCCESS:
      return { label: "Succeeded", variant: "ok" };
    case STATUS_FAILURE:
      return { label: "Failed", variant: "err" };
    case STATUS_TIMEOUT:
      return { label: "Timed out", variant: "err" };
    default:
      return { label: run.status, variant: "default" };
  }
}

// runsPerHour buckets run *starts* into 24 hourly bins, oldest first, and keeps
// each bin's count per role so the chart can stack it. Starts rather than
// finishes: the board shows work being made, and a run still going at the end of
// the window is work that began.
export interface HourBin {
  total: number;
  byRole: Record<string, number>;
}

export function runsPerHour(runs: Run[], now: number): HourBin[] {
  const bins: HourBin[] = Array.from({ length: 24 }, () => ({ total: 0, byRole: {} }));
  for (const run of runs) {
    const age = now - new Date(run.start).getTime();
    if (age < 0 || age >= WINDOW_MS) continue;
    const bin = bins[23 - Math.floor(age / HOUR_MS)];
    bin.total += 1;
    bin.byRole[run.role] = (bin.byRole[run.role] ?? 0) + 1;
  }
  return bins;
}

// hourLabel says which hour a bin covers, counting back from now: `0–1h ago` is
// the newest bin, `23–24h ago` the oldest.
export function hourLabel(index: number): string {
  return `${23 - index}–${24 - index}h ago`;
}

// roleSplit adds up finished run time per role for the split bar. A run that
// has not ended contributes nothing: its time is not known yet, and guessing
// it from "still going" would inflate the role of whatever is running when the
// page loads.
export function roleSplit(runs: Run[]): { role: string; ms: number }[] {
  const byRole = new Map<string, number>();
  for (const run of runs) {
    if (run.durationMs === undefined) continue;
    byRole.set(run.role, (byRole.get(run.role) ?? 0) + run.durationMs);
  }
  return [...byRole.entries()].map(([role, ms]) => ({ role, ms })).sort((a, b) => b.ms - a.ms);
}

// subjectKind is what a run's thread is, in the words the work page uses.
export function subjectKind(run: Run): string {
  return run.subject.kind === "pulls" ? "PR" : "Issue";
}

// subjectLabel names a run's thread the way the work page names it: `PR #7` or
// `Issue #12`, so the two screens read as one product.
export function subjectLabel(run: Run): string {
  return `${subjectKind(run)} #${run.subject.number}`;
}

// agentNames reads agents the way a human would: the display name the work API
// resolved onto their runs, the id where it could not (a deleted agent whose
// runs outlived it keeps an honest, if unreadable, name).
export function agentNames(runs: Run[]): (id: string) => string {
  const names: Record<string, string> = {};
  for (const run of runs) {
    if (run.agentName) names[run.agent] = run.agentName;
  }
  return (id: string) => names[id] ?? id;
}

// initials gives an agent's avatar its letters: the first letter of its first two
// words (`forgejo-developer-demo` reads as FD, `Developer 1` as D1), or the first
// two letters of a single word.
export function initials(name: string): string {
  const words = name.split(/[\s_-]+/).filter(Boolean);
  if (words.length >= 2) return (words[0][0] + words[1][0]).toUpperCase();
  return name.slice(0, 2).toUpperCase();
}
