import { describe, expect, it } from "vitest";
import type { Item, Run } from "./api";
import {
  formatAgo,
  formatCost,
  formatDuration,
  formatRelative,
  newestRun,
  statusLabel,
  statusTone,
  threadStatusRun,
  threadTotals,
} from "./format";

describe("formatDuration", () => {
  it("says a run under a minute in seconds", () => {
    expect(formatDuration(42_000)).toBe("42s");
  });

  it("says a run under an hour in minutes and zero-padded seconds", () => {
    expect(formatDuration(185_000)).toBe("3m 05s");
  });

  it("says a longer run in hours and zero-padded minutes", () => {
    expect(formatDuration(3_720_000)).toBe("1h 02m");
  });

  it("says nothing for a run that recorded no time", () => {
    expect(formatDuration(0)).toBe("0s");
    expect(formatDuration(900)).toBe("0s");
  });
});

describe("formatCost", () => {
  it("prices a known cost to two decimals", () => {
    expect(formatCost(0.1234, true)).toBe("$0.12");
  });

  it("shows a dash when the cost is not known", () => {
    expect(formatCost(null, false)).toBe("—");
    expect(formatCost(0.1, false)).toBe("—");
    expect(formatCost(undefined, false)).toBe("—");
  });

  it("says $0.00 for a cost known to be zero", () => {
    expect(formatCost(0, true)).toBe("$0.00");
  });
});

describe("formatRelative", () => {
  const now = new Date("2024-06-02T12:00:00Z");

  function ago(offsetMs: number): string {
    return formatRelative(new Date(now.getTime() - offsetMs).toISOString(), now);
  }

  it("says just now for the last minute", () => {
    expect(ago(30_000)).toBe("just now");
  });

  it("counts minutes up to the hour", () => {
    expect(ago(5 * 60_000)).toBe("5 min ago");
    expect(ago(59 * 60_000)).toBe("59 min ago");
  });

  it("counts hours up to the day", () => {
    expect(ago(3 * 60 * 60_000)).toBe("3 h ago");
    expect(ago(23 * 60 * 60_000)).toBe("23 h ago");
  });

  it("counts days beyond that", () => {
    expect(ago(2 * 24 * 60 * 60_000)).toBe("2 d ago");
    expect(ago(30 * 24 * 60 * 60_000)).toBe("30 d ago");
  });

  it("says just now for something stamped in the future", () => {
    expect(ago(-10_000)).toBe("just now");
  });
});

describe("formatAgo", () => {
  it("reads a fresh refresh as just now, not as a run of zeros", () => {
    expect(formatAgo(0)).toBe("just now");
    expect(formatAgo(4_999)).toBe("just now");
  });

  it("counts seconds under a minute, where formatRelative would stop at just now", () => {
    expect(formatAgo(12_000)).toBe("12s ago");
    expect(formatAgo(59_999)).toBe("59s ago");
  });

  it("steps to minutes and hours after that", () => {
    expect(formatAgo(90_000)).toBe("1 min ago");
    expect(formatAgo(3 * 60 * 60_000)).toBe("3 h ago");
  });
});

describe("newestRun", () => {
  function run(id: string, start: string): Run {
    return {
      id,
      agent: "agent",
      role: "developer",
      status: "completed",
      connector: "forgejo",
      start,
      costKnown: true,
      subject: { repo: "owner/repo", number: 1, kind: "issues" },
      url: "https://forgejo.example/owner/repo/runs/" + id,
    };
  }

  it("says there is no newest run in an empty list", () => {
    expect(newestRun([])).toBeNull();
  });

  it("picks the run that started last, wherever it sits in the list", () => {
    const runs = [
      run("old", "2024-06-01T10:00:00Z"),
      run("newest", "2024-06-01T12:00:00Z"),
      run("middle", "2024-06-01T11:00:00Z"),
    ];
    expect(newestRun(runs)?.id).toBe("newest");
  });

  it("keeps the earlier-listed run when two start at the same time", () => {
    const runs = [run("first", "2024-06-01T10:00:00Z"), run("second", "2024-06-01T10:00:00Z")];
    expect(newestRun(runs)?.id).toBe("first");
  });
});

describe("statusLabel and statusTone", () => {
  it("names each status the hub reports, keeping a timeout apart from a failure", () => {
    expect(statusLabel("queued")).toBe("Queued");
    expect(statusLabel("running")).toBe("Running");
    expect(statusLabel("success")).toBe("Succeeded");
    expect(statusLabel("failure")).toBe("Failed");
    expect(statusLabel("timeout")).toBe("Timed out");
  });

  it("colours a waiting run as a warning and a finished failure as an error", () => {
    expect(statusTone("queued")).toBe("warn");
    expect(statusTone("success")).toBe("ok");
    expect(statusTone("failure")).toBe("err");
    expect(statusTone("timeout")).toBe("err");
    expect(statusTone("running")).toBe("default");
  });

  it("reads a status it does not know as stale, not as a success", () => {
    expect(statusTone("pending")).toBe("stale");
    expect(statusLabel("pending")).toBe("pending");
  });
});

describe("threadStatusRun", () => {
  const subjectRef = { repo: "owner/apps", number: 1, kind: "issues" };
  const item = (runs: Run[], prs: Item[] = []): Item => ({
    subject: subjectRef,
    url: "",
    runs,
    durationMs: 0,
    costKnown: true,
    costUsd: 0,
    lastActivity: "2024-06-02T12:00:00Z",
    prs,
  });
  const at = (status: string, start: string): Run => ({
    id: `${status}-${start}`,
    agent: "a",
    role: "developer",
    status,
    connector: "forgejo",
    start,
    costKnown: true,
    subject: subjectRef,
    url: "",
  });

  it("reports the run still going over one that finished later", () => {
    const queued = at("queued", "2024-06-01T10:00:00Z");
    const finishedLater = at("success", "2024-06-02T10:00:00Z");
    const pr = item([finishedLater]);
    expect(threadStatusRun(item([queued], [pr]))?.status).toBe("queued");
  });

  it("reports the newest run when nothing is going", () => {
    const older = at("failure", "2024-06-01T10:00:00Z");
    const newer = at("success", "2024-06-02T10:00:00Z");
    expect(threadStatusRun(item([older, newer]))?.status).toBe("success");
  });

  it("has nothing to report for a thread with no runs", () => {
    expect(threadStatusRun(item([]))).toBeNull();
  });
});

describe("threadTotals", () => {
  const subjectRef = { repo: "owner/apps", number: 1, kind: "issues" };

  // One member of a thread — an item or a pull request nested under it — with the numbers the
  // backend gives it: its own runs, and the totals those runs add up to.
  const member = (
    runs: number,
    durationMs: number,
    costUsd: number | null,
    costKnown: boolean,
    prs: Item[] = [],
  ): Item => ({
    subject: subjectRef,
    url: "",
    runs: Array.from({ length: runs }, (_, i) => ({
      id: `run-${i}`,
      agent: "a",
      role: "developer",
      status: "success",
      connector: "forgejo",
      start: "2024-06-01T10:00:00Z",
      costKnown,
      subject: subjectRef,
      url: "",
    })),
    durationMs,
    costUsd,
    costKnown,
    lastActivity: "2024-06-02T12:00:00Z",
    prs,
  });

  it("adds each nested pull request's runs, time and cost to the item's own", () => {
    const issue = member(2, 100_000, 0.25, true, [member(1, 50_000, 0.125, true)]);
    expect(threadTotals(issue)).toEqual({
      runs: 3,
      durationMs: 150_000,
      costUsd: 0.375,
      costKnown: true,
    });
  });

  it("is the item's own numbers when nothing is nested under it", () => {
    const solo = member(2, 100_000, 0.25, true);
    expect(threadTotals(solo)).toEqual({
      runs: 2,
      durationMs: 100_000,
      costUsd: 0.25,
      costKnown: true,
    });
  });

  it("leaves the cost unknown when any nested pull request has an unpriced run", () => {
    // One run nobody priced makes the thread unknown rather than partly billed, which is how the
    // item-level rule already reads for the item's own runs.
    const issue = member(2, 100_000, 0.25, true, [member(1, 50_000, null, false)]);
    const totals = threadTotals(issue);
    expect(totals.costKnown).toBe(false);
    expect(totals.costUsd).toBeNull();
    // The counts do not depend on the price.
    expect(totals.runs).toBe(3);
    expect(totals.durationMs).toBe(150_000);
  });

  it("adds nothing for an item with no runs, and does not make the cost unknown", () => {
    // An item built to hold a pull request has no runs of its own and no cost to know; the pull
    // request's priced run is still a known cost for the thread.
    const issue = member(0, 0, null, false, [member(1, 50_000, 0.125, true)]);
    expect(threadTotals(issue)).toEqual({
      runs: 1,
      durationMs: 50_000,
      costUsd: 0.125,
      costKnown: true,
    });
  });

  it("has nothing to price for a thread nobody ran", () => {
    const empty = member(0, 0, null, false);
    const totals = threadTotals(empty);
    expect(totals.runs).toBe(0);
    expect(totals.costUsd).toBeNull();
    // Empty rather than unknown: formatCost turns the null into a dash, not a $0.00.
    expect(totals.costKnown).toBe(true);
  });
});
