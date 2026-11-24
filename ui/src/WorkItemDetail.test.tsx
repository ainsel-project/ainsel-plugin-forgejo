/** @vitest-environment jsdom */
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { WorkItemDetail } from "./WorkItemDetail";
import type { Item, Run, Subject } from "./api";

afterEach(cleanup);

function subject(repo: string, number: number, kind: string, title: string): Subject {
  return { repo, number, kind, title };
}

function run(role: string, status: string, start: string, subjectRef: Subject, overrides: Partial<Run> = {}): Run {
  return {
    id: `${subjectRef.repo}#${subjectRef.number}-${role}-${start}`,
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
    ...overrides,
  };
}

const BASE = "https://forgejo.example.com";
const ISSUE_SUBJECT = subject("owner/apps", 10, "issues", "Add the widget");
const PR_SUBJECT = subject("owner/apps", 11, "pulls", "Widget implementation");

const NESTED_PR: Item = {
  subject: PR_SUBJECT,
  url: `${BASE}/owner/apps/pulls/11`,
  runs: [run("reviewer", "success", "2024-06-02T12:00:00Z", PR_SUBJECT)],
  durationMs: 45_000,
  costUsd: 0.03,
  costKnown: true,
  lastActivity: "2024-06-02T12:00:00Z",
  prs: [],
};

// An issue with two runs of its own and one pull request nested under it. The cards add the thread
// up — $0.1234 + $0.03, 185s + 45s, 2 + 1 runs — and the split line under them keeps the issue's own
// numbers readable.
const ISSUE: Item = {
  subject: ISSUE_SUBJECT,
  url: `${BASE}/owner/apps/issues/10`,
  runs: [
    run("developer", "success", "2024-06-01T10:00:00Z", ISSUE_SUBJECT),
    run("refiner", "failure", "2024-06-01T09:00:00Z", ISSUE_SUBJECT),
  ],
  durationMs: 185_000,
  costUsd: 0.1234,
  costKnown: true,
  lastActivity: "2024-06-02T12:00:00Z",
  prs: [NESTED_PR],
};

function renderDetail(item: Item = ISSUE) {
  const onClose = vi.fn();
  const onOpenItem = vi.fn();
  const utils = render(
    <WorkItemDetail item={item} repoUrl={`${BASE}/owner/apps`} onClose={onClose} onOpenItem={onOpenItem} />,
  );
  return { ...utils, onClose, onOpenItem };
}

function cardsOf(container: HTMLElement): HTMLElement {
  const cards = container.querySelector(".forgejo-detail-cards");
  if (!cards) throw new Error("no cards container");
  return cards as HTMLElement;
}

describe("WorkItemDetail", () => {
  it("shows the thread's totals as four cards, with the issue's own figures beside them", async () => {
    const { container } = renderDetail();

    const cards = cardsOf(container);
    expect(within(cards).getByText("Cost")).toBeDefined();
    expect(within(cards).getByText("Agent time")).toBeDefined();
    expect(within(cards).getByText("Runs")).toBeDefined();
    expect(within(cards).getByText("Updated")).toBeDefined();

    // The thread: the issue's own two runs plus the nested pull request's one, its cost and its
    // time added in — the same sums the row and the Spend strip make.
    expect(within(cards).getByText("$0.15")).toBeDefined();
    expect(within(cards).getByText("3m 50s")).toBeDefined();
    expect(within(cards).getByText("3")).toBeDefined();
    expect(within(cards).queryByText("—")).toBeNull();

    // And the split stays visible: the issue's own numbers, then the nested pull request's.
    const split = container.querySelector(".forgejo-detail-split");
    if (!split) throw new Error("no split line");
    expect(within(split as HTMLElement).getByText("This issue: $0.12 · 2 runs · 3m 05s")).toBeDefined();
    expect(within(split as HTMLElement).getByText("#11: $0.03 · 1 run · 45s")).toBeDefined();
  });

  it("shows no split line for an item with nothing nested under it", async () => {
    // A pull request that references no issue is its own thread, so the cards are already the
    // whole story and there is nothing to split out.
    const { container } = renderDetail({ ...ISSUE, prs: [] });

    expect(container.querySelector(".forgejo-detail-split")).toBeNull();
    expect(within(cardsOf(container)).getByText("$0.12")).toBeDefined();
  });

  it("names the kind, repository and number, with the title as the heading", async () => {
    renderDetail();

    const heading = await screen.findByRole("heading", { name: "Add the widget" });
    expect(heading).toBeDefined();
    expect(screen.getByText(/Issue ·/).textContent).toContain("owner/apps #10");

    const forgejoLink = screen.getByRole("link", { name: "Open on Forgejo ↗" });
    expect(forgejoLink.getAttribute("href")).toBe(ISSUE.url);
    expect(forgejoLink.getAttribute("target")).toBe("_blank");
    expect(forgejoLink.getAttribute("rel")).toBe("noreferrer");
  });

  it("draws the run history as a timeline, newest first, with status and cost", async () => {
    renderDetail();

    const history = await screen.findByRole("list", { name: "Run history" });
    const steps = within(history).getAllByRole("listitem");
    // The item's own runs only, in the order the response gave them.
    expect(steps).toHaveLength(2);
    expect(within(steps[0]).getByText("Developer")).toBeDefined();
    expect(within(steps[0]).getByText("Succeeded")).toBeDefined();
    expect(within(steps[0]).getByText(/\$0\.04/)).toBeDefined();
    expect(within(steps[1]).getByText("Refiner")).toBeDefined();
    expect(within(steps[1]).getByText("Failed")).toBeDefined();
    // Each run shows its id, the handle to quote to the hub.
    expect(within(steps[0]).getByText("owner/apps#10-developer-2024-06-01T10:00:00Z")).toBeDefined();
  });

  it("shows an unknown cost as a dash, never a partial sum", async () => {
    // One run the hub could not price makes the item's total unknown, exactly as the
    // backend's allCostKnown rule says: the card must read —, not the sum of the rest.
    const item: Item = {
      ...ISSUE,
      costUsd: null,
      costKnown: false,
      prs: [],
    };
    const { container } = renderDetail(item);

    expect(within(cardsOf(container)).getByText("—")).toBeDefined();
    expect(screen.queryByText("$0.08")).toBeNull();
    expect(screen.queryByText("$0.12")).toBeNull();
  });

  it("counts active runs and dashes an unfinished run's duration", async () => {
    const item: Item = {
      ...ISSUE,
      prs: [],
      runs: [
        run("developer", "running", "2024-06-01T10:00:00Z", ISSUE_SUBJECT, {
          durationMs: undefined,
          end: undefined,
        }),
        run("refiner", "success", "2024-06-01T09:00:00Z", ISSUE_SUBJECT),
      ],
    };
    const { container } = renderDetail(item);

    // The Runs card says how much of the work is still in flight...
    expect(within(cardsOf(container)).getByText("2 (1 active)")).toBeDefined();
    // ...and the unfinished run has no duration to report — a dash, not a zero.
    const history = await screen.findByRole("list", { name: "Run history" });
    expect(within(history).getByText(/— ·/)).toBeDefined();
  });

  it("says an item with no runs of its own so, instead of an empty list", async () => {
    // An item created only to hold a pull request has no runs of its own.
    const item: Item = { ...ISSUE, runs: [], prs: [] };
    const { container } = renderDetail(item);

    expect(await screen.findByText("No runs of their own")).toBeDefined();
    expect(within(cardsOf(container)).getByText("0")).toBeDefined();
  });

  it("lists nested pull requests with their own status, cost and time", async () => {
    const { container } = renderDetail();

    expect(await screen.findByText("Linked pull requests")).toBeDefined();
    // The nested pull request's own line: its cost, its time and its newest run's status.
    expect(screen.getByText("$0.03 · 45s")).toBeDefined();
    const prs = container.querySelector(".forgejo-detail-prs");
    if (!prs) throw new Error("no pull request section");
    expect(within(prs as HTMLElement).getByRole("button", { name: /#11 Widget implementation/ })).toBeDefined();
    // The cards hold the thread, which is more than this one pull request's own $0.03.
    expect(within(cardsOf(container)).getByText("$0.15")).toBeDefined();
  });

  it("opens a nested pull request in this panel via its button", async () => {
    const { onOpenItem } = renderDetail();

    const prButton = await screen.findByRole("button", { name: /#11/ });
    await userEvent.click(prButton);

    expect(onOpenItem).toHaveBeenCalledWith(NESTED_PR);
  });

  it("calls onClose from the close control", async () => {
    const { onClose } = renderDetail();

    await userEvent.click(screen.getByRole("button", { name: "Close details" }));

    expect(onClose).toHaveBeenCalled();
  });

  it("emits only classes prefixed with forgejo-", async () => {
    const { container } = renderDetail();
    await screen.findByRole("heading", { name: "Add the widget" });

    for (const cls of [...container.querySelectorAll("[class]")].flatMap((el) => [...el.classList])) {
      expect(cls.startsWith("forgejo-")).toBe(true);
    }
  });
});
