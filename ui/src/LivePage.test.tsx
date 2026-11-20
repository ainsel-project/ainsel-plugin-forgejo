/** @vitest-environment jsdom */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { LivePage } from "./LivePage";
import { get } from "./testing/sdk";
import type { Item, Repo, Run, Subject } from "./api";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

beforeEach(() => {
  get.mockReset();
  // A fetch left anywhere in the page would go past the console's api client —
  // with no credential of the caller's own — so it has to fail loudly here.
  vi.stubGlobal(
    "fetch",
    vi.fn(() => {
      throw new Error("the page must call the SDK api client, not fetch");
    }),
  );
});

const BASE = "https://forgejo.example.com";

// A moment a fixed distance in the past, so relative labels and window maths
// can be asserted without racing the clock.
function ago(ms: number): string {
  return new Date(Date.now() - ms).toISOString();
}

function subject(
  repo: string,
  number: number,
  kind: string,
  title?: string,
): Subject {
  return { repo, number, kind, title };
}

function makeRun(id: string, overrides: Partial<Run> = {}): Run {
  return {
    id,
    agent: "forgejo-developer-demo",
    role: "developer",
    status: "running",
    connector: "forgejo",
    start: ago(10 * 60_000),
    costKnown: false,
    subject: subject("owner/apps", 10, "issues", "Add the widget"),
    url: `${BASE}/owner/apps/issues/10`,
    ...overrides,
  };
}

function makeItem(runs: Run[], overrides: Partial<Item> = {}): Item {
  return {
    subject: runs[0]?.subject ?? subject("owner/apps", 10, "issues"),
    url: runs[0]?.url ?? `${BASE}/owner/apps/issues/10`,
    runs,
    durationMs: runs.reduce((total, run) => total + (run.durationMs ?? 0), 0),
    costKnown: false,
    lastActivity: runs[0]?.start ?? ago(60_000),
    prs: [],
    ...overrides,
  };
}

function answers(runs: Run[], prs: Run[] = []): void {
  const items: Item[] = [];
  if (runs.length > 0) items.push(makeItem(runs));
  const repos: Repo[] =
    items.length > 0 || prs.length > 0
      ? [
          {
            repo: "owner/apps",
            url: `${BASE}/owner/apps`,
            items:
              prs.length > 0
                ? [...items, makeItem(prs, { prs: [] })]
                : items,
            costKnown: false,
          },
        ]
      : [];
  // One mock answers both reads the page makes — the plugin's /work and the
  // hub's /agents — split by path, the way the console binds the two clients.
  // With no names registered the page falls back to ids, which is what most
  // of these tests assert on.
  get.mockImplementation(async (path: string) =>
    path === "/agents" ? agentsBody : { repos },
  );
}

// What the hub's /agents answers by default: nothing, so every agent keeps its
// id. A test that wants names sets this before rendering.
let agentsBody: unknown = { items: [] };

beforeEach(() => {
  agentsBody = { items: [] };
});

async function rendered(): Promise<void> {
  await screen.findByText("Agent fleet");
}

// The page has two tables: the agent fleet first, the run queue second.
function tables(): { fleet: HTMLElement; queue: HTMLElement } {
  const all = screen.getAllByTestId("register-table");
  return { fleet: all[0], queue: all[1] };
}

// kpi finds one headline card by its label and returns it, so a figure is read
// from the card it belongs to rather than from anywhere on the page.
function kpi(label: string): HTMLElement {
  // The label also names a footer on the role-time panel; the card comes first.
  return screen.getAllByText(label)[0].closest("section") as HTMLElement;
}

// headers reads a table's column titles, so a test can say which time the queue shows.
function headers(table: HTMLElement): string[] {
  return within(table)
    .getAllByRole("columnheader")
    .map((th) => th.textContent ?? "");
}

function queueTab(name: RegExp): HTMLElement {
  return screen.getByRole("tab", { name });
}

describe("LivePage", () => {
  it("asks the work API for a one-day window", async () => {
    answers([]);
    render(<LivePage />);
    await rendered();
    expect(get).toHaveBeenCalledWith("/work", {
      query: expect.objectContaining({ since: "1d" }),
    });
  });

  it("shows the headline figures for the runs in the window", async () => {
    answers([
      makeRun("r-run", { status: "running" }),
      makeRun("r-queued", {
        status: "queued",
        agent: "forgejo-refiner-demo",
        role: "refiner",
        subject: subject("owner/apps", 15, "issues", "Export the audit log"),
        start: ago(2 * 60_000),
      }),
      makeRun("r-done", {
        status: "success",
        end: ago(5 * 60_000),
        durationMs: 90_000,
      }),
    ]);

    render(<LivePage />);
    await rendered();

    // Two agents, both active: one running, one waiting.
    expect(within(kpi("Active agents")).getByText("2 / 2")).toBeDefined();
    expect(within(kpi("Active agents")).getByText("1 running · 1 waiting")).toBeDefined();
    expect(within(kpi("Queued runs")).getByText("1")).toBeDefined();
    expect(within(kpi("Queued runs")).getByText("Oldest waiting 2m")).toBeDefined();
    expect(within(kpi("Runs · 24h")).getByText("3")).toBeDefined();
    expect(within(kpi("Runs · 24h")).getByText("0 failed")).toBeDefined();
    expect(within(kpi("Success rate")).getByText("100.0%")).toBeDefined();
    expect(within(kpi("Success rate")).getByText("Of 1 finished run")).toBeDefined();
    expect(within(kpi("Agent time · 24h")).getByText("1m 30s")).toBeDefined();
    // Each figure is a card of its own, not a run-on line inside one panel.
    expect(screen.getAllByTestId("panel").length).toBeGreaterThanOrEqual(6);
  });

  it("counts a timeout as a failed run and reads the success rate over finished runs only", async () => {
    answers([
      makeRun("r-run", { status: "running" }),
      makeRun("r-ok", { status: "success", end: ago(30 * 60_000), durationMs: 60_000 }),
      makeRun("r-timeout", {
        status: "timeout",
        end: ago(20 * 60_000),
        durationMs: 60_000,
      }),
    ]);

    render(<LivePage />);
    await rendered();

    // The running run has no outcome, so it is not in the two finished runs.
    expect(within(kpi("Success rate")).getByText("50.0%")).toBeDefined();
    expect(within(kpi("Success rate")).getByText("Of 2 finished runs")).toBeDefined();
    expect(within(kpi("Runs · 24h")).getByText("1 failed")).toBeDefined();
  });

  it("says a success rate is not yet known when nothing has finished", async () => {
    answers([makeRun("r-run", { status: "running" })]);
    render(<LivePage />);
    await rendered();
    expect(within(kpi("Success rate")).getByText("—")).toBeDefined();
    expect(within(kpi("Success rate")).getByText("No run has finished yet")).toBeDefined();
  });

  it("sums spend when every run was priced and says so when one was not", async () => {
    answers([
      makeRun("r-priced", {
        status: "success",
        end: ago(5 * 60_000),
        durationMs: 60_000,
        costKnown: true,
        costUsd: 1.25,
      }),
      makeRun("r-priced-too", {
        status: "success",
        end: ago(6 * 60_000),
        durationMs: 60_000,
        costKnown: true,
        costUsd: 0.75,
      }),
    ]);
    render(<LivePage />);
    await rendered();
    expect(within(kpi("Spend · 24h")).getByText("$2.00")).toBeDefined();
    expect(within(kpi("Spend · 24h")).getByText("Across 2 runs")).toBeDefined();
  });

  it("draws a dash for spend when any run's cost is unknown, as formatCost does", async () => {
    answers([
      makeRun("r-priced", {
        status: "success",
        end: ago(5 * 60_000),
        durationMs: 60_000,
        costKnown: true,
        costUsd: 1.25,
      }),
      makeRun("r-unpriced", { status: "success", end: ago(6 * 60_000), durationMs: 60_000 }),
    ]);
    render(<LivePage />);
    await rendered();
    expect(within(kpi("Spend · 24h")).getByText("—")).toBeDefined();
    expect(within(kpi("Spend · 24h")).getByText("1 of 2 runs unpriced")).toBeDefined();
  });

  it("draws a timeline row per agent with its current run", async () => {
    answers([
      makeRun("r-run", { status: "running" }),
      makeRun("r-done", {
        status: "success",
        end: ago(2 * 60 * 60_000),
        durationMs: 60_000,
      }),
      makeRun("r-other", {
        agent: "forgejo-reviewer-demo",
        role: "reviewer",
        status: "success",
        // Rows sort by each agent's newest run start. Left at the default it
        // ties with the developer's runs, leaving the order to Map insertion
        // order — which flips when the ago() calls straddle a millisecond, so
        // pin it strictly older than the running run's start.
        start: ago(30 * 60_000),
        end: ago(60_000),
        durationMs: 30_000,
      }),
    ]);

    render(<LivePage />);
    await rendered();

    const rows = within(tables().fleet).getAllByRole("row");
    // Header plus one row per agent, most recent activity first.
    expect(rows).toHaveLength(3);
    expect(within(rows[1]).getByText("forgejo-developer-demo")).toBeDefined();
    const link = within(rows[1]).getByRole("link", { name: "Issue #10 Add the widget" });
    // The thread opens in a new tab: the board stays put behind it.
    expect((link as HTMLAnchorElement).target).toBe("_blank");
    // The status pill says what the agent is doing; the cell beside the thread says for how long.
    expect(within(rows[1]).getByText("Running")).toBeDefined();
    expect(within(rows[1]).getByText(/owner\/apps · running/)).toBeDefined();
    expect(within(rows[2]).getByText("forgejo-reviewer-demo")).toBeDefined();
    expect(within(rows[2]).getByText("Idle")).toBeDefined();
    // The dev agent's two runs add to 1m 00s; the reviewer's single run is 30s.
    expect(within(rows[1]).getByText("1m 00s")).toBeDefined();
    expect(within(rows[2]).getByText("30s")).toBeDefined();
  });

  it("marks an agent whose last run failed, and points at that run", async () => {
    answers([
      makeRun("r-failed", {
        agent: "forgejo-reviewer-demo",
        role: "reviewer",
        status: "failure",
        start: ago(20 * 60_000),
        end: ago(18 * 60_000),
        durationMs: 120_000,
        subject: subject("owner/apps", 417, "pulls", "chore(deps): bump pgx"),
        url: `${BASE}/owner/apps/pulls/417`,
      }),
    ]);

    render(<LivePage />);
    await rendered();

    const rows = within(tables().fleet).getAllByRole("row");
    expect(within(rows[1]).getByText("Last run failed")).toBeDefined();
    expect(within(rows[1]).getByRole("link", { name: "PR #417 chore(deps): bump pgx" })).toBeDefined();
    expect(within(rows[1]).getByText(/failed 18 min ago/)).toBeDefined();
  });

  it("shows the run queue with the runs of the selected tab", async () => {
    answers([
      // The display name rides on the run: the work API resolves it.
      makeRun("r-run", { status: "running", agentName: "Wedge" }),
      makeRun("r-queued", {
        status: "queued",
        agent: "forgejo-refiner-demo",
        role: "refiner",
        subject: subject("owner/apps", 15, "issues", "Export the audit log"),
      }),
      makeRun("r-done", {
        status: "failure",
        end: ago(60_000),
        durationMs: 20_000,
      }),
    ]);

    render(<LivePage />);
    await rendered();

    // Running is the default tab: its run, named by the hub's display name.
    const running = tables().queue;
    expect(within(running).getByRole("link", { name: "Add the widget" })).toBeDefined();
    expect(within(running).getByText("Wedge")).toBeDefined();
    expect(within(running).queryByText("forgejo-developer-demo")).toBeNull();
    expect(within(running).queryByRole("link", { name: "Export the audit log" })).toBeNull();

    fireEvent.click(queueTab(/^Queued/));
    const queued = tables().queue;
    expect(within(queued).getByRole("link", { name: "Export the audit log" })).toBeDefined();
    expect(headers(queued)).toContain("Queued");
    expect(headers(queued)).not.toContain("Started");

    fireEvent.click(queueTab(/^Recently finished/));
    const finished = tables().queue;
    // The finished run is a failure, and its status says so.
    expect(within(finished).getByText("Failed")).toBeDefined();
    expect(within(finished).getByText("20s")).toBeDefined();
    expect(headers(finished)).toContain("Finished");
  });

  it("lists only the newest six finished runs under Recently finished", async () => {
    const finishedRuns = Array.from({ length: 8 }, (_, i) =>
      makeRun(`r-${i + 1}`, {
        status: "success",
        end: ago((i + 1) * 60_000),
        durationMs: 60_000,
        subject: subject("owner/apps", i + 1, "issues", `Thread ${i + 1}`),
        url: `${BASE}/owner/apps/issues/${i + 1}`,
      }),
    );
    answers(finishedRuns);

    render(<LivePage />);
    await rendered();
    fireEvent.click(queueTab(/^Recently finished/));

    const queue = tables().queue;
    expect(within(queue).getAllByRole("row")).toHaveLength(7); // header plus six
    for (let n = 1; n <= 6; n++) {
      expect(within(queue).getByRole("link", { name: `Thread ${n}` })).toBeDefined();
    }
    expect(within(queue).queryByRole("link", { name: "Thread 7" })).toBeNull();
    expect(within(queue).queryByRole("link", { name: "Thread 8" })).toBeNull();
  });

  it("falls back to the agent id when the run carries no name", async () => {
    answers([makeRun("r-run", { status: "running" })]);

    render(<LivePage />);
    await rendered();

    const rows = within(tables().fleet).getAllByRole("row");
    expect(within(rows[1]).getByText("forgejo-developer-demo")).toBeDefined();
  });

  it("says so when the window holds nothing", async () => {
    answers([]);
    render(<LivePage />);
    await rendered();
    expect(
      screen.getByText("No agent activity in the last 24 hours"),
    ).toBeDefined();
    expect(screen.getByText("Nothing running.")).toBeDefined();
    fireEvent.click(queueTab(/^Queued/));
    expect(screen.getByText("Nothing queued.")).toBeDefined();
    fireEvent.click(queueTab(/^Recently finished/));
    expect(screen.getByText("Nothing finished yet.")).toBeDefined();
  });

  it("names the failure when the API cannot be reached", async () => {
    get.mockRejectedValue(new Error("boom"));
    render(<LivePage />);
    expect(await screen.findByText("Could not reach work API")).toBeDefined();
  });

  it("draws the throughput chart and the role time from the window's runs", async () => {
    answers([
      makeRun("r-dev", {
        status: "success",
        end: ago(5 * 60_000),
        durationMs: 60_000,
      }),
      makeRun("r-rev", {
        role: "reviewer",
        status: "success",
        end: ago(4 * 60_000),
        durationMs: 60_000,
      }),
    ]);

    render(<LivePage />);
    await rendered();

    expect(
      screen.getByRole("img", {
        name: "Runs started per hour over the last 24 hours, stacked by role",
      }),
    ).toBeDefined();
    // The chart says what it is: an hour scale under the bars, a legend for the
    // roles in the stack, and a caption spelling out what a bar's height means.
    expect(screen.getByText("24h")).toBeDefined();
    expect(screen.getByText("Now")).toBeDefined();
    expect(screen.getByText(/Each bar is one hour, stacked by role/)).toBeDefined();
    // Each role's name is in the legend and again on its time bar.
    expect(screen.getAllByText("Developer")).toHaveLength(2);
    expect(screen.getAllByText("Reviewer")).toHaveLength(2);
    // Each role has half the finished time, one labelled bar apiece.
    expect(screen.getAllByText("1m 00s · 50%")).toHaveLength(2);
  });

  it("warns when a run failed in the last hour, naming the agent and linking the thread", async () => {
    answers([
      makeRun("r-failed", {
        agent: "forgejo-reviewer-demo",
        role: "reviewer",
        status: "failure",
        end: ago(20 * 60_000),
        durationMs: 60_000,
        subject: subject("owner/apps", 417, "pulls", "chore(deps): bump pgx"),
        url: `${BASE}/owner/apps/pulls/417`,
      }),
    ]);

    render(<LivePage />);
    await rendered();

    const alert = screen.getByRole("alert");
    expect(within(alert).getByText("1 run failed in the last hour.")).toBeDefined();
    expect(within(alert).getByText(/forgejo-reviewer-demo/)).toBeDefined();
    const link = within(alert).getByRole("link", { name: "PR #417 chore(deps): bump pgx" });
    expect((link as HTMLAnchorElement).href).toBe(`${BASE}/owner/apps/pulls/417`);
  });

  it("names a timeout in the banner as timed out", async () => {
    answers([
      makeRun("r-timeout", {
        status: "timeout",
        end: ago(10 * 60_000),
        durationMs: 60_000,
      }),
    ]);
    render(<LivePage />);
    await rendered();
    expect(within(screen.getByRole("alert")).getByText(/timed out/)).toBeDefined();
  });

  it("keeps the banner away from failures older than an hour", async () => {
    answers([
      makeRun("r-old", {
        status: "failure",
        end: ago(3 * 60 * 60_000),
        durationMs: 60_000,
      }),
    ]);
    render(<LivePage />);
    await rendered();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("keeps the banner away when nothing failed", async () => {
    answers([
      makeRun("r-ok", { status: "success", end: ago(5 * 60_000), durationMs: 60_000 }),
    ]);
    render(<LivePage />);
    await rendered();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("stops refreshing while paused and fetches again on resume", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    answers([makeRun("r-run", { status: "running" })]);

    render(<LivePage />);
    await rendered();
    const workCalls = () => get.mock.calls.filter(([path]) => path === "/work").length;
    expect(workCalls()).toBe(1);

    act(() => {
      vi.advanceTimersByTime(30_000);
    });
    expect(workCalls()).toBe(2);

    fireEvent.click(screen.getByRole("button", { name: "Pause" }));
    expect(screen.getByText("Paused")).toBeDefined();
    act(() => {
      vi.advanceTimersByTime(90_000);
    });
    expect(workCalls()).toBe(2);

    fireEvent.click(screen.getByRole("button", { name: "Resume" }));
    expect(workCalls()).toBe(3);
    expect(screen.getByText(/Live · updated/)).toBeDefined();
  });
});
