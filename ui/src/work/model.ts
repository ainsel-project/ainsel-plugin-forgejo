// The work page's vocabulary: what a row is, the hand-off the thread column reads,
// the tabs and windows the page offers, and the key a thread is known by. It reads
// nothing from the plugin and renders nothing, so the table, the pager and the page
// can all agree on the same definitions without importing one another.

import type { Item, Run } from "../api";
import { isActiveRun, isFailedRun, newestRun, threadRuns } from "../format";

// A row is one issue or pull request, and the repo URL the item does not carry of its
// own. The /work response nests a pull request under the issue it references, so a
// top-level item is already one row: the issue with its pull requests in the row, and a
// pull request that references no issue on its own.
export interface Row {
  item: Item;
  repoUrl: string;
}

// The hand-off stages in the order an agent hand-off happens, so the column reads as the story
// of the work rather than as a set. The role ids are what the hub stores on each run.
export const STAGES = [
  { role: "refiner", name: "Refine" },
  { role: "developer", name: "Develop" },
  { role: "reviewer", name: "Review" },
];

// Roles outside the hand-off sort after these, alphabetically, in the role filter.
export const ROLE_ORDER = STAGES.map((stage) => stage.role);

// The periods the select offers, each a `since` window the /work endpoint reads directly: a
// whole number of days. Thirty days is the default: the window the server answers when asked for
// nothing, and the one the GitHub plugin keeps too.
export const PERIODS = [
  { value: "1d", label: "Last 24 hours" },
  { value: "7d", label: "Last 7 days" },
  { value: "30d", label: "Last 30 days" },
];
export const DEFAULT_PERIOD = "30d";

// The rows-per-page choices the pager offers, largest last. 25 is the default: about a
// screenful of rows once the console's own chrome is counted. 100 is the ceiling because
// past it the table is a scroll again, which is what the pager exists to avoid.
export const PAGE_SIZES = [25, 50, 100];
export const DEFAULT_PAGE_SIZE = PAGE_SIZES[0];

// The view tabs. Every thread sits in exactly one of the three groups below, so the tab counts
// add up to All. A thread with work still running is In progress whatever its newest run did; a
// thread whose newest run failed, and that is not running, needs attention; the rest are Done.
export type View = "all" | "progress" | "attention" | "done";
export const VIEWS: { value: View; label: string }[] = [
  { value: "all", label: "All" },
  { value: "progress", label: "In progress" },
  { value: "attention", label: "Needs attention" },
  { value: "done", label: "Done" },
];

// Where a thread sits in the view tabs. Any run still queued or running makes it In progress,
// so the tab answers "what is being worked on" even when an older run failed.
export function viewOf(item: Item): Exclude<View, "all"> {
  const runs = threadRuns(item);
  if (runs.some(isActiveRun)) return "progress";
  const newest = newestRun(runs);
  if (newest !== null && isFailedRun(newest)) return "attention";
  return "done";
}

// A stage is one role's place in the hand-off, read off the newest run that role has on the
// thread. "pending" is a role that has not run yet.
export type StageState = "done" | "active" | "queued" | "failed" | "pending";

export const STAGE_WORD: Record<StageState, string> = {
  done: "done",
  active: "in progress",
  queued: "waiting",
  failed: "failed",
  pending: "not started",
};

// The segment colours are the console's state tokens, so a stage reads the same as the
// status pills beside it.
export const STAGE_TOKEN: Record<StageState, string> = {
  done: "var(--ok)",
  active: "var(--signal)",
  queued: "var(--warn)",
  failed: "var(--err)",
  pending: "var(--rule-soft)",
};

export function stageStateOf(run: Run | null): StageState {
  if (run === null) return "pending";
  switch (run.status) {
    case "success":
      return "done";
    case "running":
      return "active";
    case "queued":
      return "queued";
    case "failure":
    case "timeout":
      return "failed";
    default:
      return "pending";
  }
}

// handOffStates reads each stage's state off the thread's runs, in hand-off order.
export function handOffStates(item: Item): StageState[] {
  const runs = threadRuns(item);
  return STAGES.map((stage) => stageStateOf(newestRun(runs.filter((run) => run.role === stage.role))));
}

// handOffLabel names the furthest stage the thread has reached, and what it is doing there:
// "Review · in progress" once a review is running, "Develop · waiting" while a developer is queued.
export function handOffLabel(states: StageState[]): string {
  for (let i = STAGES.length - 1; i >= 0; i--) {
    if (states[i] !== "pending") return `${STAGES[i].name} · ${STAGE_WORD[states[i]]}`;
  }
  return "Not started";
}

// The key a thread is known by: its repository and number. Forgejo shares one number space
// across issues and pull requests within a repository, so the pair is unique.
export function itemKey(item: Item): string {
  return `${item.subject.repo}#${item.subject.number}`;
}
