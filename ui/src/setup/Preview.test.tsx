import { afterEach, describe, it, expect, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { Preview, triggerRole } from "./Preview";

// A plan with the given lists and verdict; the rest is empty, so each test states only what it
// is about.
const planOf = (
  parts: Partial<{
    create: { kind: string; name: string; detail: string }[];
    change: { kind: string; name: string; detail: string }[];
    delete: { kind: string; name: string; detail: string }[];
    triggers: { name: string; agentRef: string; connectorRef: string; filters: unknown[] }[];
    blocking: string[];
    warnings: string[];
  }>,
) => ({
  create: parts.create ?? [],
  change: parts.change ?? [],
  delete: parts.delete ?? [],
  triggers: parts.triggers ?? [],
  verdict: { blocking: parts.blocking ?? [], warnings: parts.warnings ?? [] },
});

// The apply button is the one button on the screen whose label starts with "Apply".
const applyButton = (container: HTMLElement) =>
  Array.from(container.querySelectorAll<HTMLButtonElement>('[data-testid="button"]')).find((b) =>
    /^Apply|^Applying/.test(b.textContent ?? ""),
  );

describe("Preview (S6)", () => {
  afterEach(() => {
    // Without this every test leaves its screen mounted, and a later getByText matches the
    // stale one beside the live one.
    cleanup();
  });

  it("counts the plan in five pills: to create, to change, to delete, blocking and warnings", () => {
    const plan = planOf({
      create: [
        { kind: "user", name: "refiner-apps", detail: "" },
        { kind: "team", name: "ainsel-refiner-apps", detail: "" },
        { kind: "connector", name: "forgejo-apps", detail: "" },
      ],
      change: [{ kind: "team", name: "ainsel-apps", detail: "role=owner" }],
      delete: [{ kind: "hook", name: "7", detail: "" }],
      warnings: ["the reviewer has no git_token"],
    });

    render(<Preview plan={plan} onApply={vi.fn()} onBack={vi.fn()} />);

    expect(screen.getByText("+3 to create")).toBeTruthy();
    expect(screen.getByText("~1 to change")).toBeTruthy();
    expect(screen.getByText("−1 to delete")).toBeTruthy();
    expect(screen.getByText("0 blocking")).toBeTruthy();
    expect(screen.getByText("1 warning")).toBeTruthy();
  });

  it("says a warning count in the plural, and marks blocking when there is some", () => {
    const plan = planOf({ blocking: ["slug is taken", "group is not granted"], warnings: ["a", "b"] });
    render(<Preview plan={plan} onApply={vi.fn()} onBack={vi.fn()} />);

    expect(screen.getByText("2 blocking").getAttribute("data-variant")).toBe("err");
    expect(screen.getByText("2 warnings").getAttribute("data-variant")).toBe("warn");
  });

  it("puts every op in one table, with a sign, the kind and the name and detail of each", () => {
    // The three lists are one table: the sign is what says which list a row came from. The
    // screen used to draw three tables, one per list, which read as three separate things to do.
    const plan = planOf({
      create: [
        { kind: "user", name: "refiner-apps", detail: "AInsel Refiner (apps)" },
        { kind: "team", name: "ainsel-refiner-apps", detail: "role=refiner" },
      ],
      change: [{ kind: "team", name: "ainsel-apps", detail: "role=owner" }],
      delete: [{ kind: "hook", name: "7", detail: "old webhook" }],
    });

    const { container } = render(<Preview plan={plan} onApply={vi.fn()} onBack={vi.fn()} />);

    const tables = Array.from(container.querySelectorAll('[data-testid="register-table"]'));
    expect(tables).toHaveLength(1);
    const [ops] = tables;
    expect(Array.from(ops.querySelectorAll("thead th")).map((th) => th.textContent)).toEqual([
      "",
      "Kind",
      "Name",
      "Detail",
    ]);
    const rows = Array.from(ops.querySelectorAll("tbody tr")).map((tr) =>
      Array.from(tr.querySelectorAll("td")).map((td) => td.textContent),
    );
    expect(rows).toEqual([
      ["+", "user", "refiner-apps", "AInsel Refiner (apps)"],
      ["+", "team", "ainsel-refiner-apps", "role=refiner"],
      ["~", "team", "ainsel-apps", "role=owner"],
      ["−", "hook", "7", "old webhook"],
    ]);
  });

  it("colours the sign by what the op does: created is ok, changed is warn, removed is err", () => {
    const plan = planOf({
      create: [{ kind: "user", name: "a", detail: "" }],
      change: [{ kind: "team", name: "b", detail: "" }],
      delete: [{ kind: "hook", name: "c", detail: "" }],
    });
    const { container } = render(<Preview plan={plan} onApply={vi.fn()} onBack={vi.fn()} />);

    const signs = Array.from(container.querySelectorAll("tbody tr td:first-child span"));
    const colours = signs.map((s) => (s as HTMLElement).style.color);
    expect(colours).toEqual(["var(--ok)", "var(--warn)", "var(--err)"]);
  });

  it("says the plan changes nothing when it has no ops", () => {
    render(<Preview plan={planOf({})} onApply={vi.fn()} onBack={vi.fn()} />);
    expect(screen.getByText("The plan changes nothing.")).toBeTruthy();
  });

  it("shows the triggers in a second, smaller table", () => {
    const plan = planOf({
      create: [{ kind: "user", name: "refiner-apps", detail: "" }],
      triggers: [{ name: "forgejo-refiner-push-apps", agentRef: "agent-1", connectorRef: "c-1", filters: [] }],
    });

    const { container } = render(<Preview plan={plan} onApply={vi.fn()} onBack={vi.fn()} />);

    const tables = Array.from(container.querySelectorAll('[data-testid="register-table"]'));
    expect(tables).toHaveLength(2);
    const triggers = tables[1];
    expect(Array.from(triggers.querySelectorAll("thead th")).map((th) => th.textContent)).toEqual([
      "Trigger",
      "Agent",
    ]);
    expect(
      Array.from(triggers.querySelectorAll("tbody tr")).map(
        (tr) => tr.querySelector("td:nth-child(1)")?.textContent,
      ),
    ).toEqual(["forgejo-refiner-push-apps"]);
  });

  it("shows each blocking issue as an err notice and each warning as a warn notice", () => {
    const plan = planOf({ blocking: ["slug is taken"], warnings: ["the reviewer has no git_token"] });
    render(<Preview plan={plan} onApply={vi.fn()} onBack={vi.fn()} />);

    expect(screen.getByText("slug is taken").closest('[data-variant="err"]')).toBeTruthy();
    expect(screen.getByText("the reviewer has no git_token").closest('[data-variant="warn"]')).toBeTruthy();
  });

  it("keeps Apply on the screen, disabled, while verdict.blocking is non-empty", () => {
    // Apply is in reach but out of use: the admin sees what the plan will enable, and the
    // blocking line says what to fix to get there.
    const plan = planOf({ create: [{ kind: "user", name: "a", detail: "" }], blocking: ["slug is taken"] });
    const onApply = vi.fn();
    const { container } = render(<Preview plan={plan} onApply={onApply} onBack={vi.fn()} />);

    const apply = applyButton(container);
    expect(apply, "Apply is on the screen").toBeTruthy();
    expect(apply!.disabled).toBe(true);
    expect(apply!.textContent).toBe("Apply 1 change");
  });

  it("labels Apply with the number of changes, and says change in the singular for one", () => {
    const plan = planOf({
      create: [{ kind: "user", name: "a", detail: "" }],
      change: [{ kind: "team", name: "b", detail: "" }],
      delete: [{ kind: "hook", name: "c", detail: "" }],
    });
    const { container } = render(<Preview plan={plan} onApply={vi.fn()} onBack={vi.fn()} />);
    expect(applyButton(container)!.textContent).toBe("Apply 3 changes");
  });

  it("disables Apply while applying", () => {
    // Apply creates the whole install. A second click while the first is still answering
    // would ask for the same resources twice, so the button is out of reach for the
    // duration of the call and says what it is waiting for.
    const plan = planOf({ create: [{ kind: "user", name: "refiner-apps", detail: "" }] });

    const { container } = render(
      <Preview plan={plan} applying onApply={vi.fn()} onBack={vi.fn()} />
    );

    const applyBtn = applyButton(container);
    expect(applyBtn, "the button says it is applying").toBeTruthy();
    expect(applyBtn!.textContent).toBe("Applying…");
    expect(applyBtn!.disabled, "a second click is not possible").toBe(true);
  });

  it("enables Apply when verdict.blocking is empty, and calls onApply on click", async () => {
    const plan = planOf({ create: [{ kind: "user", name: "refiner-apps", detail: "" }] });
    const onApply = vi.fn();
    const { container } = render(<Preview plan={plan} onApply={onApply} onBack={vi.fn()} />);

    const applyBtn = applyButton(container)!;
    expect(applyBtn.disabled).toBe(false);
    applyBtn.click();
    expect(onApply).toHaveBeenCalledTimes(1);
  });
});

describe("Preview agent names", () => {
  afterEach(cleanup);

  it("names a trigger's agent through agentLabel, not by its hub id", () => {
    const plan = planOf({
      triggers: [{ name: "forgejo-refiner-issue-opened-demo", agentRef: "a-5950e4c3", connectorRef: "c-1", filters: [] }],
    });
    render(<Preview plan={plan} onApply={vi.fn()} onBack={vi.fn()} agentLabel={() => "Refiner of demo"} />);
    expect(screen.getByText("Refiner of demo")).toBeTruthy();
    expect(screen.queryByText("a-5950e4c3")).toBeNull();
  });

  it("reads the role off a trigger's generated name", () => {
    expect(triggerRole("forgejo-developer-mention-demo")).toBe("developer");
    expect(triggerRole("someone-elses-trigger")).toBeNull();
  });
});
