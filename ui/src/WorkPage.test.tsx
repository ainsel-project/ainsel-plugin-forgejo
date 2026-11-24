/** @vitest-environment jsdom */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { WorkPage } from "./WorkPage";
import { get } from "./testing/sdk";
import type { Item, Run, Subject, WorkResponse } from "./api";

afterEach(() => {
  cleanup();
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

function answers(body: WorkResponse) {
  get.mockResolvedValue(body);
}

function subject(repo: string, number: number, kind: string, title: string): Subject {
  return { repo, number, kind, title };
}

function run(role: string, status: string, start: string, subjectRef: Subject): Run {
  return {
    id: `${subjectRef.repo}#${subjectRef.number}-${role}`,
    agent: role,
    role,
    status,
    connector: "forgejo",
    start,
    durationMs: 60_000,
    costUsd: 0.04,
    costKnown: true,
    subject: subjectRef,
    url: `https://ainsel.example.com/runs/${subjectRef.number}-${role}`,
  };
}

const BASE = "https://forgejo.example.com";
const ISSUE_SUBJECT = subject("owner/apps", 10, "issues", "Add the widget");
const ISSUE_URL = `${BASE}/owner/apps/issues/10`;
const PR_SUBJECT = subject("owner/apps", 11, "pulls", "Widget implementation");
const PR_URL = `${BASE}/owner/apps/pulls/11`;
const TOP_PR_SUBJECT = subject("owner/data", 5, "pulls", "Ship the schema");
const TOP_PR_URL = `${BASE}/owner/data/pulls/5`;

// The issue the table is mostly about: three roles over three runs — one of them the reviewer on
// the pull request nested under it, at $0.04 — the newest of which is still queued. The row shows
// the thread: 3 runs, $0.1234 + $0.04.
const ISSUE: Item = {
  subject: ISSUE_SUBJECT,
  url: ISSUE_URL,
  runs: [
    run("developer", "queued", "2024-06-01T10:00:00Z", ISSUE_SUBJECT),
    run("refiner", "failure", "2024-06-01T09:00:00Z", ISSUE_SUBJECT),
  ],
  durationMs: 185_000,
  costUsd: 0.1234,
  costKnown: true,
  lastActivity: "2024-06-02T12:00:00Z",
  prs: [
    {
      subject: PR_SUBJECT,
      url: PR_URL,
      runs: [run("reviewer", "success", "2024-06-02T12:00:00Z", PR_SUBJECT)],
      durationMs: 0,
      costUsd: 0.04,
      costKnown: true,
      lastActivity: "2024-06-02T12:00:00Z",
      prs: [],
    },
  ],
};

// A pull request that references no issue, with nothing to report but itself.
const TOP_PR: Item = {
  subject: TOP_PR_SUBJECT,
  url: TOP_PR_URL,
  runs: [],
  durationMs: 0,
  costUsd: null,
  costKnown: false,
  lastActivity: "2024-06-01T12:00:00Z",
  prs: [],
};

// Two repos, the older one listed first: the table sorts across repos, not within them, so
// the order of the answer must not be the order of the rows.
const TWO_REPOS: WorkResponse = {
  repos: [
    {
      repo: "owner/data",
      url: `${BASE}/owner/data`,
      items: [TOP_PR],
      costUsd: null,
      costKnown: false,
    },
    {
      repo: "owner/apps",
      url: `${BASE}/owner/apps`,
      items: [ISSUE],
      costUsd: 0.1234,
      costKnown: true,
    },
  ],
};

function itemBody(item: Item, repo = item.subject.repo): WorkResponse {
  return {
    repos: [
      {
        repo,
        url: `${BASE}/${repo}`,
        items: [item],
        costUsd: null,
        costKnown: false,
      },
    ],
  };
}

// Standalone PRs in the order the table will show them: the i-th entry gets activity a day
// older than the one before it, so flatten()'s newest-first sort is a known order the tests
// read straight off the item numbers — row 1 is refs[0], row 26 is refs[25]. The refs may name
// several repos, which is the point: the sort runs across repos, so a page boundary must never
// turn out to be a repo boundary.
function newestFirst(refs: { repo: string; number: number }[]): WorkResponse {
  const byRepo = new Map<string, Item[]>();
  refs.forEach((ref, index) => {
    const item: Item = {
      subject: subject(ref.repo, ref.number, "pulls", `Thread ${ref.number}`),
      url: `${BASE}/${ref.repo}/pulls/${ref.number}`,
      runs: [],
      durationMs: 0,
      costUsd: null,
      costKnown: false,
      lastActivity: new Date(Date.UTC(2024, 0, 1) - index * 86_400_000).toISOString(),
      prs: [],
    };
    const items = byRepo.get(ref.repo);
    if (items) items.push(item);
    else byRepo.set(ref.repo, [item]);
  });
  return {
    repos: [...byRepo].map(([repo, items]) => ({
      repo,
      url: `${BASE}/${repo}`,
      items,
      costUsd: null,
      costKnown: false,
    })),
  };
}

// `count` threads numbered 1..count in one repo, newest first.
function oneRepo(count: number, repo = "owner/apps"): WorkResponse {
  return newestFirst(Array.from({ length: count }, (_, i) => ({ repo, number: i + 1 })));
}

// The item numbers of the rows the table currently shows, in the order they are drawn. Read off
// each row's Forgejo anchor — the one control in a row whose name is nothing but its number.
function rowNumbers(container: HTMLElement): number[] {
  return [...container.querySelectorAll("tbody tr")].map((tr) => {
    const anchor = within(tr as HTMLElement).getByRole("link", {
      name: /Open #\d+ on Forgejo/,
    });
    return Number(anchor.getAttribute("aria-label")!.match(/#(\d+)/)![1]);
  });
}

function range(from: number, to: number): number[] {
  return Array.from({ length: to - from + 1 }, (_, i) => from + i);
}

// The text of the cell under one column header, read off the header row so the test names the
// column it means rather than counting cells.
function cellFor(row: HTMLElement, header: string): string {
  const headers = [...document.querySelectorAll("thead th")].map((th) => th.textContent ?? "");
  const index = headers.indexOf(header);
  if (index === -1) throw new Error(`no "${header}" column`);
  return row.querySelectorAll("td")[index]?.textContent ?? "";
}

// The row a thread is drawn in, found by the Forgejo link in its first column.
async function rowFor(number: number): Promise<HTMLElement> {
  const link = await screen.findByRole("link", {
    name: new RegExp(`#${number}\\b`),
  });
  const row = link.closest("tr");
  if (!row) throw new Error(`#${number} is not rendered inside a row`);
  return row as HTMLElement;
}

// A thread whose only run timed out: its newest run failed and nothing is running, so it is the
// one thread that needs attention.
const FAILED_SUBJECT = subject("owner/apps", 12, "issues", "Flaky trigger");
const FAILED: Item = {
  subject: FAILED_SUBJECT,
  url: `${BASE}/owner/apps/issues/12`,
  runs: [run("developer", "timeout", "2024-05-30T10:00:00Z", FAILED_SUBJECT)],
  durationMs: 600_000,
  costUsd: 0.5,
  costKnown: true,
  lastActivity: "2024-05-30T12:00:00Z",
  prs: [],
};

// One thread in each view: ISSUE is in progress, FAILED needs attention, TOP_PR is done.
const MIXED: WorkResponse = {
  repos: [
    {
      repo: "owner/apps",
      url: `${BASE}/owner/apps`,
      items: [ISSUE, FAILED],
      costUsd: 0.1234,
      costKnown: true,
    },
    {
      repo: "owner/data",
      url: `${BASE}/owner/data`,
      items: [TOP_PR],
      costUsd: null,
      costKnown: false,
    },
  ],
};

// The value the summary strip shows under a label.
function stat(container: HTMLElement, label: string): string | undefined {
  const strip = container.querySelector(".forgejo-work-summary");
  if (!strip) throw new Error("no summary strip");
  const name = [...strip.querySelectorAll("span")].find((el) => el.textContent === label);
  return name?.nextElementSibling?.textContent ?? undefined;
}

function readBlob(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error);
    reader.readAsText(blob);
  });
}

describe("WorkPage", () => {
  it("renders one row per issue or PR, newest first, with links", async () => {
    answers(TWO_REPOS);

    const { container } = render(<WorkPage />);

    const rows = await screen.findAllByRole("row");
    // The header row plus one row for the issue and one for the top-level PR. The PR
    // nested under the issue is not a row of its own.
    expect(rows).toHaveLength(3);
    expect(container.querySelectorAll("tbody tr")).toHaveLength(2);

    const [issueRow, prRow] = rows.slice(1);

    const issueLink = within(issueRow).getByRole("link", { name: /Open #10 on Forgejo/ });
    expect(issueLink.getAttribute("href")).toBe(ISSUE_URL);
    expect(issueLink.getAttribute("target")).toBe("_blank");
    expect(issueLink.getAttribute("rel")).toBe("noreferrer");
    // The title itself is a button that opens the detail panel, not a link.
    expect(within(issueRow).getByRole("button", { name: "Add the widget" })).toBeDefined();

    const nestedPrLink = within(issueRow).getByRole("link", { name: "#11" });
    expect(nestedPrLink.getAttribute("href")).toBe(PR_URL);

    expect(within(prRow).getByRole("link", { name: /#5/ })).toBeDefined();
  });

  it("shows the thread's status, runs, time and cost", async () => {
    answers(TWO_REPOS);

    render(<WorkPage />);

    const row = await rowFor(10);
    // The status is the run still going, not the run that finished last.
    expect(within(row).getByText("Queued")).toBeDefined();
    // The thread, not the issue alone: the reviewer ran on the nested pull request, and its run,
    // its time and its $0.04 are counted in this row.
    expect(cellFor(row, "Runs")).toBe("3");
    expect(cellFor(row, "Agent time")).toBe("3m 05s");
    expect(cellFor(row, "Cost")).toBe("$0.16");
  });

  it("adds the nested pull request's runs to the row that links it", async () => {
    // A thread whose only reviewer run is on its pull request: the row still counts that run and
    // still bills for it, so review cost is not invisible on every row.
    const item: Item = {
      ...ISSUE,
      runs: [run("developer", "success", "2024-06-01T10:00:00Z", ISSUE_SUBJECT)],
      durationMs: 60_000,
      costUsd: 0.04,
      costKnown: true,
      prs: [{ ...ISSUE.prs[0], durationMs: 60_000 }],
    };
    answers(itemBody(item));

    render(<WorkPage />);

    const row = await rowFor(10);
    expect(within(row).getByText("Review · done")).toBeDefined();
    expect(cellFor(row, "Runs")).toBe("2");
    expect(cellFor(row, "Agent time")).toBe("2m 00s");
    expect(cellFor(row, "Cost")).toBe("$0.08");
  });

  it("leaves the row's cost unknown when a nested pull request has an unpriced run", async () => {
    // One run the hub could not price anywhere on the thread makes the whole row unknown, rather
    // than a sum that quietly leaves the unpriced run out.
    const item: Item = {
      ...ISSUE,
      prs: [{ ...ISSUE.prs[0], costUsd: null, costKnown: false }],
    };
    answers(itemBody(item));

    render(<WorkPage />);

    const row = await rowFor(10);
    expect(cellFor(row, "Cost")).toBe("—");
    // The run and time counts do not depend on the price.
    expect(cellFor(row, "Runs")).toBe("3");
  });

  it("shows the hand-off as three stages and where the thread has got to", async () => {
    answers(TWO_REPOS);

    render(<WorkPage />);

    const row = await rowFor(10);
    // Refine failed, develop is queued, review (on the nested pull request) succeeded.
    expect(within(row).getByRole("img", { name: "Refine: failed, Develop: waiting, Review: done" })).toBeDefined();
    expect(within(row).getByText("Review · done")).toBeDefined();
  });

  it("unknown cost and no runs show dashes", async () => {
    answers(TWO_REPOS);

    render(<WorkPage />);

    const row = await rowFor(5);
    // Nothing linked, no agent touched it, no price the hub could read: two dashes, and the
    // status says why there is nothing to report.
    expect(within(row).getAllByText("—")).toHaveLength(2);
    expect(within(row).getByText("No runs")).toBeDefined();
    expect(within(row).getByText("Not started")).toBeDefined();
    expect(within(row).getByText("0")).toBeDefined();
  });

  it("the summary strip counts threads, repositories, work in progress and agent time", async () => {
    answers(TWO_REPOS);

    const { container } = render(<WorkPage />);

    await screen.findByRole("button", { name: "Add the widget" });
    expect(stat(container, "Threads")).toBe("2");
    expect(stat(container, "Repositories")).toBe("2");
    expect(stat(container, "In progress")).toBe("1");
    expect(stat(container, "Agent time")).toBe("3m 05s");
    // The strip's spend is the rows' costs added up: #10's $0.16 (its issue's own $0.12 plus the
    // $0.04 its nested pull request ran for) and nothing from #5, which nobody ran.
    expect(stat(container, "Spend")).toBe("$0.16");
    expect(parseFloat(cellFor(await rowFor(10), "Cost").slice(1))).toBeCloseTo(0.16, 2);
  });

  it("says so when nothing has been worked on", async () => {
    answers({ repos: [] });

    render(<WorkPage />);

    expect(await screen.findByText("No issues or pull requests worked on yet")).toBeDefined();
    // Nothing to page through, so nothing to page with either.
    expect(screen.queryByRole("button", { name: /prev/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /next/i })).toBeNull();
    expect(screen.queryByLabelText("Rows per page")).toBeNull();
    // And nothing to export.
    expect((screen.getByRole("button", { name: "Export CSV" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("shows the repository as muted text under the title", async () => {
    answers(itemBody(ISSUE));

    render(<WorkPage />);

    const row = await rowFor(10);
    expect(within(row).getByText("owner/apps", { exact: false })).toBeDefined();
  });

  it("labels the kind of each item with an icon", async () => {
    answers(TWO_REPOS);

    render(<WorkPage />);

    const issueRow = await rowFor(10);
    const prRow = await rowFor(5);
    expect(within(issueRow).getByRole("img", { name: "Issue" })).toBeDefined();
    expect(within(prRow).getByRole("img", { name: "Pull request" })).toBeDefined();
  });

  it("links an item that has no title by its number", async () => {
    // The subject's title is whatever the last run happened to record; a thread
    // still has a number and a page even when nobody wrote it one.
    const item: Item = {
      ...ISSUE,
      subject: { repo: "owner/apps", number: 12, kind: "pulls" },
      prs: [],
    };
    answers(itemBody(item));

    render(<WorkPage />);

    const link = await screen.findByRole("link", { name: /#12/ });
    expect(link.getAttribute("href")).toBe(item.url);
    expect(screen.getByRole("button", { name: "#12" })).toBeDefined();
  });

  it("the Forgejo anchor stays external and does not open the detail panel", async () => {
    answers(TWO_REPOS);

    render(<WorkPage />);

    const row = await rowFor(10);
    const anchor = within(row).getByRole("link", { name: /#10/ });
    await userEvent.click(anchor);

    expect(screen.queryByRole("complementary", { name: "Thread details" })).toBeNull();
    expect(anchor.getAttribute("target")).toBe("_blank");
  });

  it("emits only classes prefixed with forgejo-", async () => {
    answers(TWO_REPOS);

    const { container } = render(<WorkPage />);
    await screen.findByRole("button", { name: "Add the widget" });

    const emitted = emittedClasses(container);
    expect(emitted.length).toBeGreaterThan(0);
    for (const cls of emitted) {
      expect(cls.startsWith("forgejo-")).toBe(true);
    }
  });
});

describe("WorkPage views and filters", () => {
  it("counts each view and shows only the one picked", async () => {
    answers(MIXED);

    render(<WorkPage />);
    await screen.findByRole("button", { name: "Add the widget" });

    const tab = (name: RegExp) => screen.getByRole("tab", { name });
    expect(tab(/^All/).textContent).toBe("All3");
    expect(tab(/In progress/).textContent).toBe("In progress1");
    expect(tab(/Needs attention/).textContent).toBe("Needs attention1");
    expect(tab(/^Done/).textContent).toBe("Done1");

    await userEvent.click(tab(/Needs attention/));
    expect(tab(/Needs attention/).getAttribute("aria-selected")).toBe("true");
    expect(screen.getByRole("button", { name: "Flaky trigger" })).toBeDefined();
    expect(screen.queryByRole("button", { name: "Add the widget" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Ship the schema" })).toBeNull();

    await userEvent.click(tab(/^Done/));
    expect(screen.getByRole("button", { name: "Ship the schema" })).toBeDefined();
    expect(screen.queryByRole("button", { name: "Flaky trigger" })).toBeNull();
  });

  it("says so when the chosen view is empty", async () => {
    answers(TWO_REPOS);

    render(<WorkPage />);
    await screen.findByRole("button", { name: "Add the widget" });

    await userEvent.click(screen.getByRole("tab", { name: /Needs attention/ }));

    expect(screen.getByText("No threads match this view.")).toBeDefined();
    // The view tabs stay, so the admin can go back to where the threads are.
    expect(screen.getByRole("tab", { name: /^All/ })).toBeDefined();
  });

  it("searches by title or number, ignoring case and a leading #", async () => {
    answers(MIXED);

    render(<WorkPage />);
    await screen.findByRole("button", { name: "Add the widget" });
    const search = screen.getByRole("searchbox", { name: "Search" });

    await userEvent.type(search, "WIDGET");
    expect(screen.getByRole("button", { name: "Add the widget" })).toBeDefined();
    expect(screen.queryByRole("button", { name: "Flaky trigger" })).toBeNull();

    await userEvent.clear(search);
    await userEvent.type(search, "#5");
    expect(screen.getByRole("button", { name: "Ship the schema" })).toBeDefined();
    expect(screen.queryByRole("button", { name: "Add the widget" })).toBeNull();
  });

  it("filters by repository, with the choices taken from the data", async () => {
    answers(MIXED);

    render(<WorkPage />);
    await screen.findByRole("button", { name: "Add the widget" });

    const repo = screen.getByLabelText("Repository") as HTMLSelectElement;
    expect([...repo.options].map((o) => o.textContent)).toEqual(["All repositories", "owner/apps", "owner/data"]);

    await userEvent.selectOptions(repo, "owner/data");
    expect(screen.getByRole("button", { name: "Ship the schema" })).toBeDefined();
    expect(screen.queryByRole("button", { name: "Add the widget" })).toBeNull();
  });

  it("filters by role, offering the roles in hand-off order", async () => {
    answers(MIXED);

    render(<WorkPage />);
    await screen.findByRole("button", { name: "Add the widget" });

    const role = screen.getByLabelText("Role") as HTMLSelectElement;
    expect([...role.options].map((o) => o.textContent)).toEqual(["Any role", "Refiner", "Developer", "Reviewer"]);

    // The reviewer ran on the pull request nested under the issue, so the issue is the thread.
    await userEvent.selectOptions(role, "reviewer");
    expect(screen.getByRole("button", { name: "Add the widget" })).toBeDefined();
    expect(screen.queryByRole("button", { name: "Flaky trigger" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Ship the schema" })).toBeNull();
  });

  it("returns to the first page when a filter changes", async () => {
    answers(oneRepo(60));

    render(<WorkPage />);
    await screen.findByRole("link", { name: /#1\b/ });
    await userEvent.click(screen.getByRole("button", { name: /next/i }));
    expect(screen.getByText("Page 2 of 3")).toBeDefined();

    // Every thread's title contains "thread", so the search keeps all sixty and the page must
    // still go back to the first one.
    await userEvent.type(screen.getByRole("searchbox", { name: "Search" }), "t");

    expect(screen.getByText("Showing 1–25 of 60 threads")).toBeDefined();
    expect(screen.getByText("Page 1 of 3")).toBeDefined();
  });
});

describe("WorkPage period", () => {
  it("asks the work API for the thirty-day window by default", async () => {
    answers(TWO_REPOS);

    render(<WorkPage />);
    await screen.findByRole("button", { name: "Add the widget" });

    expect(get).toHaveBeenCalledWith("/work", { query: { since: "30d" } });
  });

  it("asks again for the chosen window when the period changes", async () => {
    answers(TWO_REPOS);

    render(<WorkPage />);
    await screen.findByRole("button", { name: "Add the widget" });

    await userEvent.selectOptions(screen.getByLabelText("Period"), "1d");

    expect(get).toHaveBeenLastCalledWith("/work", { query: { since: "1d" } });
    expect(await screen.findByRole("button", { name: "Add the widget" })).toBeDefined();
  });
});

describe("WorkPage export", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    // jsdom has no object URLs; the tests that need them install their own and take them away.
    delete (URL as { createObjectURL?: unknown }).createObjectURL;
    delete (URL as { revokeObjectURL?: unknown }).revokeObjectURL;
  });

  // Captures the CSV the page hands to the browser and the file name it gives the download.
  async function exportedCsv(): Promise<{ text: string; name: string }> {
    const createObjectURL = vi.fn((_blob: Blob) => "blob:forgejo-work");
    Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() });
    const names: string[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      names.push(this.download);
    });

    await userEvent.click(screen.getByRole("button", { name: "Export CSV" }));

    const blob = createObjectURL.mock.calls[0][0];
    return { text: await readBlob(blob), name: names[0] };
  }

  it("downloads every filtered row as CSV, not just the page on screen", async () => {
    answers(MIXED);

    render(<WorkPage />);
    await screen.findByRole("button", { name: "Add the widget" });

    const { text, name } = await exportedCsv();

    expect(name).toBe("forgejo-work-30d.csv");
    const lines = text.split("\r\n");
    expect(lines[0]).toBe("kind,number,title,repo,status,runs,agent time,cost,last activity");
    expect(lines).toHaveLength(4);
    expect(lines).toContain("Issue,10,Add the widget,owner/apps,Queued,3,3m 05s,0.16,2024-06-02T12:00:00Z");
    // A thread nobody priced leaves its cost empty rather than writing a zero.
    expect(lines).toContain("PR,5,Ship the schema,owner/data,,0,0s,,2024-06-01T12:00:00Z");
  });

  it("exports only the rows the view and filters leave", async () => {
    answers(MIXED);

    render(<WorkPage />);
    await screen.findByRole("button", { name: "Add the widget" });
    await userEvent.click(screen.getByRole("tab", { name: /Needs attention/ }));

    const { text } = await exportedCsv();

    const lines = text.split("\r\n");
    expect(lines).toHaveLength(2);
    expect(lines[1].startsWith("Issue,12,Flaky trigger,owner/apps,Timed out,1,10m 00s,0.50,")).toBe(true);
  });
});

describe("WorkPage detail panel", () => {
  it("opens the thread beside the table and highlights its row", async () => {
    answers(TWO_REPOS);

    render(<WorkPage />);
    const row = await rowFor(10);
    await userEvent.click(within(row).getByRole("button", { name: "Add the widget" }));

    const panel = await screen.findByRole("complementary", { name: "Thread details" });
    expect(within(panel).getByRole("heading", { name: "Add the widget" })).toBeDefined();
    // The panel holds the issue's own runs, not the nested pull request's.
    expect(within(within(panel).getByRole("list", { name: "Run history" })).getAllByRole("listitem")).toHaveLength(2);
    // The table is still there, with the open row marked.
    expect(screen.getByRole("button", { name: "Ship the schema" })).toBeDefined();
    expect((await rowFor(5)).className).toBe("");
    expect(row.className).toBe("forgejo-work-row-selected");
  });

  it("closes from its close control and drops the highlight", async () => {
    answers(TWO_REPOS);

    render(<WorkPage />);
    const row = await rowFor(10);
    await userEvent.click(within(row).getByRole("button", { name: "Add the widget" }));
    await screen.findByRole("complementary", { name: "Thread details" });

    await userEvent.click(screen.getByRole("button", { name: "Close details" }));

    expect(screen.queryByRole("complementary", { name: "Thread details" })).toBeNull();
    expect(row.className).not.toContain("forgejo-work-row-selected");
  });

  it("opens a nested pull request in the same panel, keeping the issue's row highlighted", async () => {
    answers(TWO_REPOS);

    render(<WorkPage />);
    const row = await rowFor(10);
    await userEvent.click(within(row).getByRole("button", { name: "Add the widget" }));
    const panel = await screen.findByRole("complementary", { name: "Thread details" });

    await userEvent.click(within(panel).getByRole("button", { name: /#11 Widget implementation/ }));

    expect(within(panel).getByRole("heading", { name: "Widget implementation" })).toBeDefined();
    expect(row.className).toBe("forgejo-work-row-selected");
  });

  it("keeps the page when the panel opens and closes", async () => {
    answers(oneRepo(60));

    const { container } = render(<WorkPage />);
    await screen.findByRole("link", { name: /#1\b/ });
    await userEvent.click(screen.getByRole("button", { name: /next/i }));
    expect(screen.getByText("Page 2 of 3")).toBeDefined();

    // The panel renders inside this same WorkPage, so the page it was opened from is the page
    // it returns to — the router knows neither, and neither does the pagination.
    const row = await rowFor(30);
    await userEvent.click(within(row).getByRole("button", { name: "Thread 30" }));
    await screen.findByRole("complementary", { name: "Thread details" });

    await userEvent.click(screen.getByRole("button", { name: "Close details" }));

    expect(await screen.findByText("Page 2 of 3")).toBeDefined();
    expect(rowNumbers(container)).toEqual(range(26, 50));
  });
});

describe("WorkPage pagination", () => {
  it("renders no pager when every row fits on one page", async () => {
    answers(oneRepo(25));

    const { container } = render(<WorkPage />);

    await screen.findByRole("link", { name: /#25\b/ });
    expect(container.querySelectorAll("tbody tr")).toHaveLength(25);
    expect(screen.queryByRole("button", { name: /prev/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /next/i })).toBeNull();
    expect(screen.queryByLabelText("Rows per page")).toBeNull();
    expect(screen.queryByText(/Showing /)).toBeNull();
  });

  it("shows the first 25 rows newest first and says which ones they are", async () => {
    answers(oneRepo(60));

    const { container } = render(<WorkPage />);

    await screen.findByRole("link", { name: /#1\b/ });
    // The whole set is fetched; only a page of it is drawn, and in the order the whole
    // set was sorted in — not in the order the response happened to carry.
    expect(container.querySelectorAll("tbody tr")).toHaveLength(25);
    expect(rowNumbers(container)).toEqual(range(1, 25));
    expect(screen.getByText("Showing 1–25 of 60 threads")).toBeDefined();
    expect(screen.getByText("Page 1 of 3")).toBeDefined();
  });

  it("steps to the next page and back, disabling the ends", async () => {
    answers(oneRepo(60));

    const { container } = render(<WorkPage />);
    await screen.findByRole("link", { name: /#1\b/ });

    const prev = screen.getByRole("button", { name: "Previous page" }) as HTMLButtonElement;
    const next = screen.getByRole("button", { name: "Next page" }) as HTMLButtonElement;
    expect(prev.disabled).toBe(true);
    expect(next.disabled).toBe(false);

    await userEvent.click(next);
    // Exactly rows 26-50 of the sorted set: the page boundary cuts the list, it does not
    // re-order it.
    expect(rowNumbers(container)).toEqual(range(26, 50));
    expect(screen.getByText("Showing 26–50 of 60 threads")).toBeDefined();
    expect(screen.getByText("Page 2 of 3")).toBeDefined();
    expect(prev.disabled).toBe(false);

    await userEvent.click(next);
    expect(rowNumbers(container)).toEqual(range(51, 60));
    expect(screen.getByText("Showing 51–60 of 60 threads")).toBeDefined();
    expect((screen.getByRole("button", { name: "Next page" }) as HTMLButtonElement).disabled).toBe(true);

    await userEvent.click(prev);
    expect(rowNumbers(container)).toEqual(range(26, 50));
  });

  it("changes the rows per page", async () => {
    answers(oneRepo(60));

    const { container } = render(<WorkPage />);
    await screen.findByRole("link", { name: /#1\b/ });

    await userEvent.selectOptions(screen.getByLabelText("Rows per page"), "50");

    expect(container.querySelectorAll("tbody tr")).toHaveLength(50);
    expect(rowNumbers(container)).toEqual(range(1, 50));
    expect(screen.getByText("Showing 1–50 of 60 threads")).toBeDefined();
    expect(screen.getByText("Page 1 of 2")).toBeDefined();
  });

  it("clamps to a real page when a larger page size leaves the current one past the end", async () => {
    answers(oneRepo(120));

    const { container } = render(<WorkPage />);
    await screen.findByRole("link", { name: /#1\b/ });

    // Page 5 of 5 at 25 a page: the last 20 rows.
    const next = screen.getByRole("button", { name: "Next page" });
    for (let i = 0; i < 4; i++) await userEvent.click(next);
    expect(screen.getByText("Page 5 of 5")).toBeDefined();
    expect(rowNumbers(container)).toEqual(range(101, 120));

    // 100 a page leaves only two pages. The page clamps to the last real one rather than
    // rendering an empty table under a summary that still counts 120 items.
    await userEvent.selectOptions(screen.getByLabelText("Rows per page"), "100");

    expect(screen.getByText("Page 2 of 2")).toBeDefined();
    expect(screen.getByText("Showing 101–120 of 120 threads")).toBeDefined();
    expect(container.querySelectorAll("tbody tr")).toHaveLength(20);
  });

  it("counts every row and repository in the summary, not just the page", async () => {
    // 40 threads alternating between two repos: the summary has to count across both while
    // the table draws a quarter of them.
    answers(
      newestFirst(
        Array.from({ length: 40 }, (_, i) => ({
          repo: i % 2 === 0 ? "owner/apps" : "owner/data",
          number: i + 1,
        })),
      ),
    );

    const { container } = render(<WorkPage />);
    await screen.findByRole("link", { name: /#1\b/ });

    expect(stat(container, "Threads")).toBe("40");
    expect(stat(container, "Repositories")).toBe("2");
    expect(container.querySelectorAll("tbody tr")).toHaveLength(25);
    expect(rowNumbers(container)).toEqual(range(1, 25));
    expect(screen.getByText("Showing 1–25 of 40 threads")).toBeDefined();
  });

  it("numbers rows within the page, leaving the real position to the pager", async () => {
    // The host RegisterTable numbers the rows it is handed and takes no offset, so the column
    // restarts at 01 on every page. Accepted on purpose — the pager's "X–Y of Z" carries the
    // position — and pinned here so the next reader changes it knowingly rather than by accident.
    answers(oneRepo(60));

    const { container } = render(<WorkPage />);
    await screen.findByRole("link", { name: /#1\b/ });
    await userEvent.click(screen.getByRole("button", { name: "Next page" }));

    const numbers = [...container.querySelectorAll("tbody tr")].map((tr) => tr.querySelector("td")?.textContent);
    expect(numbers[0]).toBe("01");
    expect(numbers[24]).toBe("25");
    expect(screen.getByText("Showing 26–50 of 60 threads")).toBeDefined();
  });

  it("emits only classes prefixed with forgejo- when the pager is showing", async () => {
    answers(oneRepo(60));

    const { container } = render(<WorkPage />);
    await screen.findByRole("link", { name: /#1\b/ });

    const emitted = emittedClasses(container);
    expect(emitted.length).toBeGreaterThan(0);
    for (const cls of emitted) {
      expect(cls.startsWith("forgejo-")).toBe(true);
    }
  });
});

function emittedClasses(container: HTMLElement): string[] {
  return [...container.querySelectorAll("[class]")].flatMap((element) => [...element.classList]);
}
