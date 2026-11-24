// The row set the table draws from: every repo's items in one list, newest activity
// first; the lookup the detail panel answers from; and the counts the strip above the
// table shows. Every one of them adds the same rows the table shows, so the strip and
// the rows under it cannot disagree about the money or the count.

import type { Repo } from "../api";
import { formatCost, formatDuration, threadTotals } from "../format";
import { ROLE_ORDER, itemKey } from "./model";
import type { Row } from "./model";

// The items of every repo, newest activity first. The response sorts within a repo, but a
// quiet repo may hold an item older than a busy one's, and the admin scanning for what to
// look at next reads down this column.
export function flatten(repos: Repo[]): Row[] {
  const rows: Row[] = [];
  for (const repo of repos) {
    for (const item of repo.items) {
      rows.push({ item, repoUrl: repo.url });
    }
  }
  return rows.sort((a, b) => new Date(b.item.lastActivity).getTime() - new Date(a.item.lastActivity).getTime());
}

// findThread returns the thread a key names, top-level or nested, with the URL of the repo it
// lives in. A nested pull request is in the same repo as its issue (the /work response nests
// `prs` inside a Repo's Item), so it takes the repo's URL; a cross-repo PR can never nest here
// under that structure.
export function findThread(repos: Repo[], key: string | null): Row | null {
  if (key === null) return null;
  for (const repo of repos) {
    for (const item of repo.items) {
      if (itemKey(item) === key) return { item, repoUrl: repo.url };
      for (const pr of item.prs) {
        if (itemKey(pr) === key) return { item: pr, repoUrl: repo.url };
      }
    }
  }
  return null;
}

// summarise is the strip above the table: threads, repositories, what is in progress, and the
// agent time and spend of all the work. It adds each row's threadTotals, so the strip and the rows
// under it are the same money added two ways: every run counted once, and only a run the hub could
// not price making the spend unknown.
export function summarise(rows: Row[], inProgress: number) {
  let durationMs = 0;
  let costUsd = 0;
  let costKnown = true;
  for (const row of rows) {
    const totals = threadTotals(row.item);
    durationMs += totals.durationMs;
    // A thread nobody ran has no cost to know; only a run the hub could not price makes the sum
    // unknown.
    if (totals.runs === 0) continue;
    if (totals.costUsd !== null) costUsd += totals.costUsd;
    else costKnown = false;
  }
  return [
    { label: "Threads", value: String(rows.length) },
    { label: "Repositories", value: String(countRepos(rows)) },
    { label: "In progress", value: String(inProgress) },
    { label: "Agent time", value: formatDuration(durationMs) },
    { label: "Spend", value: formatCost(costUsd, costKnown) },
  ];
}

// The repositories the table shows a row from, counted off the rows themselves so the strip
// cannot disagree with the rows below it.
export function countRepos(rows: Row[]): number {
  return new Set(rows.map((row) => row.item.subject.repo)).size;
}

export function sortRoles(roles: Set<string>): string[] {
  return [...roles].sort((a, b) => roleRank(a) - roleRank(b) || a.localeCompare(b));
}

export function roleRank(role: string): number {
  const index = ROLE_ORDER.indexOf(role);
  return index === -1 ? ROLE_ORDER.length : index;
}

export function roleName(role: string): string {
  return role.charAt(0).toUpperCase() + role.slice(1);
}
