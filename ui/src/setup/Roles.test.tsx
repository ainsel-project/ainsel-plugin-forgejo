import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { Roles } from "./Roles";
import type { RoleToolSets } from "../api";
import type { Input } from "./model";

// The created-only wizard's role step: the runtime container is a free-input
// text field (no image pick list any more), and the LLM fields are gone —
// they live in the settings store now. What is left here is the container,
// the agent name, and the tool chips with their server-declared rails. The three
// roles are cards stacked on one screen, so a test reaches a role by its card.

const toolsFixture: RoleToolSets = {
  defaults: {
    refiner: ["forgejo_list_issues", "forgejo_update_issue"],
    developer: ["forgejo_submit_pr"],
    reviewer: ["forgejo_review_pr"],
  },
  forbidden: {
    refiner: ["forgejo_merge_pr"],
    developer: ["forgejo_close_issue"],
    reviewer: ["forgejo_approve_pr"],
  },
};

const baseInput: Input = {
  slug: "test-org",
  groupId: "g-1",
  baseUrl: "https://forgejo.example.com",
  org: "test-org",
  adminToken: "",
  excludeLabel: "do-not-refine",
  roles: {
    refiner: {
      runtime: "registry.example.com/base:latest",
      agentName: "",
      tools: ["forgejo_list_issues"],
      turnTimeoutMs: "3600000",
    },
    developer: {
      runtime: "registry.example.com/claude-code:latest",
      agentName: "",
      tools: ["forgejo_list_issues"],
    },
    reviewer: {
      runtime: "registry.example.com/codex:latest",
      agentName: "",
      tools: ["forgejo_list_issues"],
    },
  },
};

afterEach(() => {
  cleanup();
});

// The card a role is drawn in. Scoping a query to it keeps a test about one role from reading
// another role's field or chip.
const roleCard = (container: HTMLElement, role: "Refiner" | "Developer" | "Reviewer") =>
  Array.from(container.querySelectorAll<HTMLElement>(".forgejo-role-card")).find((card) =>
    card.textContent?.startsWith(role),
  ) as HTMLElement;

// The tool group of a role, which holds the chips and the two bulk buttons. Named by its label
// rather than taken as the card's first group: the working-hours group is a group too, and it
// sits above the tools.
const toolGroup = (container: HTMLElement, role: "Refiner" | "Developer" | "Reviewer") =>
  roleCard(container, role).querySelector('[aria-label$=" tools"]') as HTMLElement;

// The working-hours group of a role: the tick that asks for a window, and the day, hour and zone
// controls a window is made of.
const hoursGroup = (container: HTMLElement, role: "Refiner" | "Developer" | "Reviewer") =>
  roleCard(container, role).querySelector('[aria-label$=" working hours"]') as HTMLElement;

// One chip by the tool it names. A chip's text is its state mark (✓, +, ⊘) and then the tool name;
// the mark is decoration, so the match is on the name alone.
const chipName = (chip: HTMLElement) => (chip.textContent ?? "").replace(/^[✓+⊘]/, "").trim();
const chipFor = (container: HTMLElement, role: "Refiner" | "Developer" | "Reviewer", tool: string) =>
  Array.from(toolGroup(container, role).querySelectorAll<HTMLButtonElement>("button.forgejo-tool-item")).find(
    (b) => chipName(b) === tool,
  ) as HTMLButtonElement;

// The two bulk buttons of a role's tool group: Check all, then Uncheck all.
const bulkButtons = (container: HTMLElement, role: "Refiner" | "Developer" | "Reviewer") =>
  toolGroup(container, role).querySelectorAll<HTMLButtonElement>("button:not(.forgejo-tool-item)");

function renderRoles(overrides: Partial<React.ComponentProps<typeof Roles>> = {}) {
  // A stateful stand-in for the wizard: a real parent re-renders the step with the
  // input it was given, so a typed value is visible on the next render and a disabled
  // button reflects the selection the last setInput made. An inert `vi.fn()` would
  // freeze the step at its first render and make "type, then advance" untestable.
  let state: Input = (overrides.input as Input | undefined) ?? baseInput;
  const setInput = vi.fn((next: Input) => {
    state = next;
    rerender(<Roles {...makeProps()} />);
  });
  const makeProps = (): React.ComponentProps<typeof Roles> => ({
    ...{
      input: baseInput,
      setInput,
      tools: toolsFixture,
      editing: false,
      onNext: vi.fn(),
      onBack: vi.fn(),
    },
    ...overrides,
    input: state,
    setInput,
  });
  const { container, rerender } = render(<Roles {...makeProps()} />);
  return { container, setInput, props: makeProps() };
}

const blankInput = (roles: Partial<Record<"refiner" | "developer" | "reviewer", string>>): Input => ({
  ...baseInput,
  roles: {
    refiner: { runtime: roles.refiner ?? "", agentName: "", tools: [] },
    developer: { runtime: roles.developer ?? "", agentName: "", tools: [] },
    reviewer: { runtime: roles.reviewer ?? "", agentName: "", tools: [] },
  },
});

describe("Roles (S4)", () => {
  it("shows the three role cards stacked, each with its own fields", () => {
    const { container } = renderRoles();
    const cards = container.querySelectorAll(".forgejo-role-card");
    expect(cards).toHaveLength(3);
    for (const role of ["Refiner", "Developer", "Reviewer"] as const) {
      const card = roleCard(container, role);
      expect(card, `${role} has a card`).toBeTruthy();
      expect(card.querySelector(`#roles-${role.toLowerCase()}-runtime`)).toBeTruthy();
      expect(card.querySelector(`#roles-${role.toLowerCase()}-agent-name`)).toBeTruthy();
    }
  });

  it("names every field of a role with a label that points at it", () => {
    const { container } = renderRoles();
    for (const role of ["refiner", "developer", "reviewer"]) {
      const runtime = container.querySelector(`#roles-${role}-runtime`) as HTMLInputElement;
      expect(container.querySelector(`label[for="roles-${role}-runtime"]`)?.textContent).toBe(
        "Runtime image",
      );
      expect(runtime).toBeTruthy();
      expect(container.querySelector(`label[for="roles-${role}-agent-name"]`)?.textContent).toBe(
        "Agent name",
      );
    }
  });

  it("marks a role whose runtime is missing where its field is, once Continue is tried", () => {
    const input = blankInput({ refiner: "registry.example.com/base:latest", reviewer: "registry.example.com/codex:latest" });
    const { container } = renderRoles({ input });
    // Nothing is refused until the admin tries to move on.
    expect(container.querySelector('[data-variant="err"]')).toBeNull();
    expect(container.textContent).not.toContain("Enter a runtime container for this role.");

    fireEvent.click(screen.getByRole("button", { name: "Continue" }));

    expect(roleCard(container, "Developer").textContent).toContain(
      "Enter a runtime container for this role.",
    );
    expect(roleCard(container, "Refiner").textContent).not.toContain(
      "Enter a runtime container for this role.",
    );
  });

  it("stays on the step when Continue is clicked with runtimes missing", () => {
    const input = blankInput({ refiner: "registry.example.com/base:latest" });
    const onNext = vi.fn();
    renderRoles({ input, onNext });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(onNext).not.toHaveBeenCalled();
    // It says which roles, not only where to fill in.
    expect(screen.getByRole("alert").textContent).toBe(
      "Enter a runtime container for: developer, reviewer",
    );
  });

  it("names the missing runtimes together, in the order the preview lists them", () => {
    const input = blankInput({ refiner: "registry.example.com/base:latest" });
    const { container } = renderRoles({ input });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    // One line naming both — not one per role.
    const errors = container.querySelectorAll('[data-variant="err"]');
    expect(errors).toHaveLength(1);
    expect(errors[0].textContent).toContain("Enter a runtime container for: developer, reviewer");
  });

  it("keeps the refusal off the step until Continue is attempted", () => {
    const input = blankInput({
      refiner: "registry.example.com/base:latest",
      developer: "registry.example.com/claude-code:latest",
    });
    const { container } = renderRoles({ input });
    expect(container.querySelector('[data-variant="err"]')).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(container.querySelector('[data-variant="err"]')).not.toBeNull();
  });

  it("lets the step go again once the last runtime is typed", () => {
    const input = blankInput({
      refiner: "registry.example.com/base:latest",
      developer: "registry.example.com/claude-code:latest",
    });
    const onNext = vi.fn();
    const { container } = renderRoles({ input, onNext });

    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(onNext).not.toHaveBeenCalled();

    fireEvent.change(container.querySelector("#roles-reviewer-runtime") as HTMLInputElement, {
      target: { value: "registry.example.com/codex:latest" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(onNext).toHaveBeenCalled();
  });

  // A ref with no tag and no digest is a floating one: the registry pulls `latest`,
  // which may not exist — and the install's task pods then fail at pull while the
  // work view keeps the tasks at QUEUED. The plan blocks on it; the step refuses it
  // one screen earlier, in the same shape the missing runtime takes.
  const DIGEST = `@sha256:${"0".repeat(64)}`;

  it("refuses a runtime with no tag or digest, and says what to add", () => {
    const input = blankInput({
      refiner: "registry.example.com/base:latest",
      developer: "dpinsel/ainsel-pi-maui",
      reviewer: "registry.example.com/codex:latest",
    });
    const onNext = vi.fn();
    const { container } = renderRoles({ input, onNext });

    // Nothing is said until the admin tries to move on, as with a missing runtime.
    expect(container.textContent).not.toContain("Add a tag such as :8.0");

    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(onNext).not.toHaveBeenCalled();
    expect(roleCard(container, "Developer").textContent).toContain(
      "Add a tag such as :8.0, or a @sha256: digest.",
    );
    // The role that did name a version is left alone — and it is not told the field
    // is empty, which it is not.
    expect(roleCard(container, "Refiner").textContent).not.toContain("Add a tag such as :8.0");
    expect(roleCard(container, "Refiner").textContent).not.toContain(
      "Enter a runtime container for this role.",
    );
  });

  it("names every untagged runtime together, in the order the cards list them", () => {
    const input = blankInput({
      refiner: "registry.example.com/base:latest",
      developer: "dpinsel/ainsel-pi-maui",
      reviewer: "dpinsel/ainsel-pi-maui",
    });
    const { container } = renderRoles({ input });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    const errors = container.querySelectorAll('[data-variant="err"]');
    expect(errors).toHaveLength(1);
    expect(errors[0].textContent).toBe(
      "Add a tag such as :8.0, or a @sha256: digest, to the runtime for: developer, reviewer",
    );
  });

  it("lets a runtime that names a version through", () => {
    for (const ref of [
      "dpinsel/ainsel-pi-maui:8.0",
      `dpinsel/ainsel-pi-maui${DIGEST}`,
      `dpinsel/ainsel-pi-maui:8.0${DIGEST}`,
      "registry:5000/img:8.0",
    ]) {
      cleanup();
      const input = blankInput({ refiner: ref, developer: ref, reviewer: ref });
      const onNext = vi.fn();
      renderRoles({ input, onNext });
      fireEvent.click(screen.getByRole("button", { name: "Continue" }));
      expect(onNext, ref).toHaveBeenCalled();
    }
  });

  it("reads a registry port as a port, not as a tag", () => {
    const ref = "registry:5000/img";
    const input = blankInput({ refiner: ref, developer: ref, reviewer: ref });
    const onNext = vi.fn();
    const { container } = renderRoles({ input, onNext });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(onNext).not.toHaveBeenCalled();
    expect(container.querySelector('[data-variant="err"]')).not.toBeNull();
  });

  it("flags all three cards when the shared runtime floats", () => {
    // The tick copies one value into three roles, so one bad ref is three bad refs —
    // and the refusal names all three, not just the one that was typed.
    const shared = "dpinsel/ainsel-pi-maui";
    const input = blankInput({ refiner: shared, developer: shared, reviewer: shared });
    const onNext = vi.fn();
    const { container } = renderRoles({ input, onNext });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(onNext).not.toHaveBeenCalled();
    for (const role of ["Refiner", "Developer", "Reviewer"] as const) {
      expect(roleCard(container, role).textContent).toContain(
        "Add a tag such as :8.0, or a @sha256: digest.",
      );
    }
    expect(screen.getByRole("alert").textContent).toContain("refiner, developer, reviewer");
  });

  it("still lets an edit leave the runtime empty, which means keep", () => {
    // Empty on an edit is the install's own value, floating or not — the plan warns
    // about a retained one, and the step has nothing to refuse.
    const input = blankInput({});
    const onNext = vi.fn();
    renderRoles({ input, onNext, editing: true });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(onNext).toHaveBeenCalled();
  });

  it("keeps the tool list scrolling inside the card, so the step's buttons stay in reach", () => {
    const input: Input = {
      ...baseInput,
      roles: {
        ...baseInput.roles,
        refiner: {
          ...baseInput.roles.refiner,
          tools: ["a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"],
        },
      },
    };
    const { container } = renderRoles({ input });
    const scroll = roleCard(container, "Refiner").querySelector(".forgejo-tools-scroll") as HTMLElement;
    expect(scroll).not.toBeNull();
    expect(scroll.style.maxHeight).toBe("16rem");
    expect(scroll.style.overflowY).toBe("auto");
  });

  it("moves the refiner's runtime to the other roles while 'use for all' is ticked", () => {
    const { setInput, container } = renderRoles();
    // Tick the box first, then change the refiner's runtime. The box's meaning is "the
    // three share a runtime", so moving it is moving it for all three — in one setInput,
    // not three.
    fireEvent.click(screen.getByLabelText("Use the refiner's runtime container for all roles"));
    fireEvent.change(container.querySelector("#roles-refiner-runtime") as HTMLInputElement, {
      target: { value: "registry.example.com/shared:latest" },
    });
    expect(setInput).toHaveBeenCalledTimes(2);
    const lastInput = setInput.mock.calls[1][0] as Input;
    expect(lastInput.roles.developer.runtime).toBe("registry.example.com/shared:latest");
    expect(lastInput.roles.reviewer.runtime).toBe("registry.example.com/shared:latest");
    expect(lastInput.roles.refiner.runtime).toBe("registry.example.com/shared:latest");
    // The other fields are left alone — the copy is the runtime, not the whole role.
    expect(lastInput.roles.developer.agentName).toBe(baseInput.roles.developer.agentName);
    expect(lastInput.roles.developer.tools).toEqual(baseInput.roles.developer.tools);
  });

  it("moves only the refiner's runtime once the box is unticked", () => {
    const { setInput, container } = renderRoles();
    // Tick, then untick. The tick already moved the refiner's runtime onto the other
    // two — that copy stands, unticking does not undo it — but after the untick a new
    // refiner value moves nowhere: the roles keep their own answers from here on.
    const box = screen.getByLabelText("Use the refiner's runtime container for all roles");
    fireEvent.click(box);
    fireEvent.click(box);
    fireEvent.change(container.querySelector("#roles-refiner-runtime") as HTMLInputElement, {
      target: { value: "registry.example.com/only-refiner:latest" },
    });
    const lastInput = setInput.mock.calls.at(-1)![0] as Input;
    expect(lastInput.roles.refiner.runtime).toBe("registry.example.com/only-refiner:latest");
    expect(lastInput.roles.developer.runtime).toBe(baseInput.roles.refiner.runtime);
    expect(lastInput.roles.reviewer.runtime).toBe(baseInput.roles.refiner.runtime);
  });

  it("starts the tick off for what the input already says, not for nothing", () => {
    // The tick is state this screen cannot hold across a Next/Back re-mount, so it reads
    // it off the input at mount. All three sharing a runtime means it starts ticked.
    const shared = "registry.example.com/shared:latest";
    const input = blankInput({ refiner: shared, developer: shared, reviewer: shared });
    const { container } = renderRoles({ input });
    const box = container.querySelector(
      ".forgejo-use-for-all input[type=checkbox]",
    ) as HTMLInputElement;
    expect(box.checked).toBe(true);
  });

  it("starts the tick off where the runtimes differ, and on nothing when they are all empty", () => {
    // Different runtimes: the tick is an offer, not a claim about the input.
    const { container } = renderRoles();
    const box = () =>
      container.querySelector(".forgejo-use-for-all input[type=checkbox]") as HTMLInputElement;
    expect(box().checked).toBe(false);

    // A brand-new walk: all three empty. Unticked — the tick marks a decision the
    // admin made, not the blank the walk started from.
    cleanup();
    const fresh = renderRoles({ input: blankInput({}) });
    expect(
      (fresh.container.querySelector(
        ".forgejo-use-for-all input[type=checkbox]",
      ) as HTMLInputElement).checked,
    ).toBe(false);
  });

  it("copies the refiner's runtime onto the other roles when the box is ticked", () => {
    // The box ticked is one setInput, and it carries the same runtime to the other two.
    const input = blankInput({ refiner: "registry.example.com/base:latest" });
    const { setInput } = renderRoles({ input });
    const box = screen.getByLabelText("Use the refiner's runtime container for all roles");
    expect(box.hasAttribute("checked")).toBe(false); // it is an offer, not a claim
    fireEvent.click(box);
    expect(setInput).toHaveBeenCalledTimes(1);
    const next = setInput.mock.calls[0][0] as Input;
    expect(next.roles.developer.runtime).toBe(baseInput.roles.refiner.runtime);
    expect(next.roles.reviewer.runtime).toBe(baseInput.roles.refiner.runtime);
    // Everything else on those roles is left where it was.
    expect(next.roles.developer.tools).toEqual([]);
    expect(next.roles.reviewer.tools).toEqual([]);
  });

  it("gives the copied roles their own objects, not one shared with the refiner", () => {
    // One object in two role slots is a change to one felt by both: editing the developer's
    // agent name would move the reviewer's too.
    const input = blankInput({ refiner: "registry.example.com/base:latest" });
    const { setInput } = renderRoles({ input });
    fireEvent.click(screen.getByLabelText("Use the refiner's runtime container for all roles"));
    const next = setInput.mock.calls[0][0] as Input;
    expect(next.roles.developer).not.toBe(next.roles.reviewer);
    expect(next.roles.developer).not.toBe(next.roles.refiner);
    // Same answer in all three, held by three separate objects.
    expect(next.roles.developer.runtime).toBe(next.roles.refiner.runtime);
  });

  it("leaves the box alone when the input already says the runtime is shared", () => {
    // Already ticked: clicking it is the admin's business, not a re-copy on mount.
    const { setInput } = renderRoles();
    expect(setInput).not.toHaveBeenCalled();
  });

  it("offers the defaults for each role, and no other role's tools", () => {
    const { container } = renderRoles();
    const refiner = toolGroup(container, "Refiner");
    expect(refiner.textContent).toContain("forgejo_list_issues");
    expect(refiner.textContent).not.toContain("forgejo_review_pr");
    expect(toolGroup(container, "Reviewer").textContent).toContain("forgejo_review_pr");
  });

  it("never offers a tool the rails forbid, as something to add", () => {
    // The rails are what a created-only install refuses at apply time; the checklist
    // simply never puts a forbidden tool where the admin could click it. The refiner's
    // rail is forgejo_merge_pr, and it is not among the refiner's defaults.
    const { container } = renderRoles();
    expect(chipFor(container, "Refiner", "forgejo_merge_pr")).toBeUndefined();
  });

  it("shows a forbidden tool the role already has, checked, marked out, and removable", () => {
    // A forbidden tool that the install's role already holds is the one case where the
    // rails show themselves by name: the chip stays pressed and clickable — the admin
    // has to be able to take it away — but it is marked in words, not only by colour.
    const input: Input = {
      ...baseInput,
      roles: {
        ...baseInput.roles,
        refiner: { ...baseInput.roles.refiner, tools: ["forgejo_merge_pr"] },
      },
    };
    const { container } = renderRoles({ input });
    const chip = chipFor(container, "Refiner", "forgejo_merge_pr");
    expect(chip.getAttribute("aria-pressed")).toBe("true");
    expect(chip.disabled).toBe(false);
    expect(chip.title).toBe("Granted");
  });

  it("lets a checked forbidden tool be unchecked", () => {
    // A checked forbidden chip is not a decoration: the rails constrain adding, not removing.
    const input: Input = {
      ...baseInput,
      roles: {
        ...baseInput.roles,
        refiner: {
          ...baseInput.roles.refiner,
          runtime: "legacy-image",
          tools: ["forgejo_list_issues"],
        },
      },
    };
    const { setInput, container } = renderRoles({ input });
    const chip = chipFor(container, "Refiner", "forgejo_list_issues");
    // It is pressed and it is clickable — the admin has to be able to take it away.
    expect(chip.getAttribute("aria-pressed")).toBe("true");
    expect(chip.disabled).toBe(false);
    fireEvent.click(chip);
    expect(setInput).toHaveBeenCalledTimes(1);
    const next = setInput.mock.calls[0][0] as Input;
    expect(next.roles.refiner.tools).not.toContain("forgejo_list_issues");
  });

  it("grants a tool the role does not hold when its chip is clicked", () => {
    const { setInput, container } = renderRoles();
    const chip = chipFor(container, "Refiner", "forgejo_update_issue");
    expect(chip.getAttribute("aria-pressed")).toBe("false");
    fireEvent.click(chip);
    const next = setInput.mock.calls[0][0] as Input;
    expect(next.roles.refiner.tools).toContain("forgejo_update_issue");
  });

  it("reflects the current selection in the chips' pressed state", () => {
    const input: Input = {
      ...baseInput,
      roles: { ...baseInput.roles, refiner: { ...baseInput.roles.refiner, tools: ["forgejo_list_issues"] } },
    };
    const { container } = renderRoles({ input });
    const pressed = Array.from(toolGroup(container, "Refiner").querySelectorAll('button[aria-pressed="true"]'));
    expect(pressed.map((c) => chipName(c as HTMLElement))).toEqual(["forgejo_list_issues"]);
  });

  it("treats the tool group as a group, not a single field", () => {
    const { container } = renderRoles();
    for (const role of ["Refiner", "Developer", "Reviewer"] as const) {
      expect(toolGroup(container, role).getAttribute("aria-label")).toBe(`${role} tools`);
    }
  });

  it("puts both bulk buttons inside the tool group, beside its label", () => {
    const { container } = renderRoles();
    const buttons = bulkButtons(container, "Refiner");
    expect(buttons.length).toBe(2);
    expect(buttons[0].textContent).toBe("Check all");
    expect(buttons[1].textContent).toBe("Uncheck all");
    expect(toolGroup(container, "Refiner").textContent).toContain("Tools");
  });

  it("moves an entire role's selection in one step", () => {
    // One click, one setInput — so a re-render never sees a half-checked role.
    const input: Input = {
      ...baseInput,
      roles: { ...baseInput.roles, refiner: { ...baseInput.roles.refiner, tools: [] } },
    };
    const { container, setInput } = renderRoles({ input });
    setInput.mockClear();

    fireEvent.click(bulkButtons(container, "Refiner")[0]);

    expect(setInput).toHaveBeenCalledTimes(1);
    const next = setInput.mock.calls[0][0] as Input;
    expect(next.roles.refiner.tools.sort()).toEqual(
      ["forgejo_list_issues", "forgejo_update_issue"].sort(),
    );

    setInput.mockClear();
    fireEvent.click(bulkButtons(container, "Refiner")[1]);
    expect(setInput).toHaveBeenCalledTimes(1);
    expect(setInput.mock.calls[0][0].roles.refiner.tools).toEqual([]);
  });

  it("adds only the addable tools when the list mixes addable and forbidden ones", () => {
    // A single role whose defaults include a tool the server also declares forbidden: the
    // mixed case the bulk buttons must get right. The forbidden tool stays out of the
    // selection even when "Check all" is clicked, and "Uncheck all" takes even a checked
    // forbidden tool away.
    const mixedTools: RoleToolSets = {
      defaults: { refiner: ["forgejo_list_issues", "forgejo_update_issue"], developer: [], reviewer: [] },
      forbidden: { refiner: ["forgejo_update_issue"], developer: [], reviewer: [] },
    };
    const input: Input = {
      ...baseInput,
      roles: { ...baseInput.roles, refiner: { ...baseInput.roles.refiner, tools: [] } },
    };
    const { container, setInput } = renderRoles({ input, tools: mixedTools });

    const chips = toolGroup(container, "Refiner").querySelectorAll<HTMLButtonElement>("button.forgejo-tool-item");
    expect(chips).toHaveLength(2);
    expect(Array.from(chips).filter((c) => c.getAttribute("aria-pressed") === "true")).toHaveLength(0);
    // The forbidden tool is struck out and cannot be selected (its role holds none of it).
    const forbidden = chipFor(container, "Refiner", "forgejo_update_issue");
    expect(forbidden.disabled).toBe(true);
    expect(forbidden.textContent).toContain("⊘");

    // "Check all" — one click, one setInput, and the forbidden tool is not in it.
    setInput.mockClear();
    fireEvent.click(bulkButtons(container, "Refiner")[0]);
    expect(setInput).toHaveBeenCalledTimes(1);
    const added = setInput.mock.calls[0][0].roles.refiner.tools;
    expect(added.sort()).toEqual(["forgejo_list_issues"]);
    expect(added).not.toContain("forgejo_update_issue");
  });

  it("clears even a checked forbidden tool when 'Uncheck all' is clicked", () => {
    // The rails constrain adding, not removing: a checked forbidden chip exists so the
    // admin can take it away, and the bulk action must be able to do it in one click.
    const mixedTools: RoleToolSets = {
      defaults: { refiner: ["forgejo_list_issues", "forgejo_update_issue"], developer: [], reviewer: [] },
      forbidden: { refiner: ["forgejo_update_issue"], developer: [], reviewer: [] },
    };
    const input: Input = {
      ...baseInput,
      roles: {
        ...baseInput.roles,
        refiner: { ...baseInput.roles.refiner, tools: ["forgejo_list_issues", "forgejo_update_issue"] },
      },
    };
    const { container, setInput } = renderRoles({ input, tools: mixedTools });

    // Both chips are pressed; the forbidden one is clickable so it can be removed.
    const forbidden = chipFor(container, "Refiner", "forgejo_update_issue");
    expect(forbidden.getAttribute("aria-pressed")).toBe("true");
    expect(forbidden.disabled).toBe(false);

    setInput.mockClear();
    fireEvent.click(bulkButtons(container, "Refiner")[1]);
    expect(setInput).toHaveBeenCalledTimes(1);
    expect(setInput.mock.calls[0][0].roles.refiner.tools).toEqual([]);
  });

  it("hides the bulk buttons and reports empty when a role has no tool list", () => {
    const input: Input = {
      ...baseInput,
      roles: { ...baseInput.roles, refiner: { ...baseInput.roles.refiner, tools: [] } },
    };
    const tools: RoleToolSets = { defaults: {}, forbidden: {} };
    const { container } = renderRoles({ input, tools });
    const group = toolGroup(container, "Refiner");
    expect(group.querySelectorAll("button")).toHaveLength(0);
    expect(group.textContent).toContain("No tools to offer");
  });

  it("disables 'Check all' when every addable tool is already selected", () => {
    // The state after a click that did nothing: all the defaults are held, so the
    // button stands down — while "Uncheck all" is free to clear everything.
    const input: Input = {
      ...baseInput,
      roles: {
        ...baseInput.roles,
        refiner: {
          runtime: "img",
          agentName: "",
          tools: ["forgejo_list_issues", "forgejo_update_issue"],
        },
      },
    };
    const { container } = renderRoles({ input });
    expect(bulkButtons(container, "Refiner")[0].disabled).toBe(true); // Check all
    expect(bulkButtons(container, "Refiner")[1].disabled).toBe(false); // Uncheck all
  });

  it("disables both bulk buttons while a selection is empty", () => {
    const input: Input = {
      ...baseInput,
      roles: { ...baseInput.roles, refiner: { ...baseInput.roles.refiner, tools: [] } },
    };
    const { container } = renderRoles({ input });
    const buttons = bulkButtons(container, "Refiner");
    // Nothing to clear, so "Uncheck all" stands down.
    expect(buttons[1].disabled).toBe(true);
    expect(buttons[0].disabled).toBe(false);
  });

  it("keeps bulk actions per-role", () => {
    // The reviewer's "Check all" must not touch the refiner's selection.
    const { container, setInput } = renderRoles();
    setInput.mockClear();

    const reviewerCheckAll = toolGroup(container, "Reviewer").querySelector(
      "button:not(.forgejo-tool-item)",
    ) as HTMLButtonElement;
    fireEvent.click(reviewerCheckAll);
    expect(setInput).toHaveBeenCalledTimes(1);
    const next = setInput.mock.calls[0][0] as Input;
    expect(next.roles.reviewer.tools.sort()).toEqual(
      ["forgejo_list_issues", "forgejo_review_pr"].sort(),
    );
    expect(next.roles.refiner.tools).toEqual(baseInput.roles.refiner.tools);
  });

  it("leaves the step when Continue is clicked with every runtime set", () => {
    const onNext = vi.fn();
    renderRoles({ onNext });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(onNext).toHaveBeenCalled();
  });

  it("leaves the step when the runtimes are all empty on an edit", () => {
    // On an edit an empty runtime means "keep what the install has" — the plan falls back
    // to the install's prior container — so the step must not refuse it.
    const input = blankInput({});
    const onNext = vi.fn();
    const { container } = renderRoles({ input, editing: true, onNext });
    // Nothing is marked missing either — the marker claims what the step refuses.
    expect(container.querySelector('[data-variant="err"]')).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(onNext).toHaveBeenCalled();
  });

  it("tells the admin an empty runtime on an edit keeps the current container", () => {
    const { container } = renderRoles({ editing: true });
    expect(container.textContent).toContain("Leave empty to keep the install's current container.");
  });

  // The per-role turn timeout of issue #61: the field speaks minutes, the wire
  // speaks milliseconds, and empty is never a reset.
  it("shows the role's turn timeout in minutes off the millisecond value", () => {
    const { container } = renderRoles();
    const field = container.querySelector("#roles-refiner-turn-timeout") as HTMLInputElement;
    expect(field.value).toBe("60"); // baseInput's refiner carries 3600000ms
  });

  it("writes a typed turn timeout back as milliseconds", () => {
    const { container, setInput } = renderRoles();
    setInput.mockClear();
    const field = container.querySelector("#roles-developer-turn-timeout") as HTMLInputElement;
    fireEvent.change(field, { target: { value: "90" } });
    expect(setInput).toHaveBeenCalledTimes(1);
    const next = setInput.mock.calls[0][0] as Input;
    expect(next.roles.developer.turnTimeoutMs).toBe("5400000");
  });

  it("reads a cleared turn timeout as keep, not zero", () => {
    const { container, setInput } = renderRoles({ editing: true });
    setInput.mockClear();
    const field = container.querySelector("#roles-refiner-turn-timeout") as HTMLInputElement;
    fireEvent.change(field, { target: { value: "" } });
    const next = setInput.mock.calls[0][0] as Input;
    expect(next.roles.refiner.turnTimeoutMs).toBe("");
  });

  it("tells the admin an empty turn timeout on an edit keeps the current value", () => {
    const { container } = renderRoles({ editing: true });
    expect(container.textContent).toContain("Leave empty to keep the install's current value.");
  });

  // The optional provider key: the one secret the walk carries, because it is the
  // only place a key can be given before the agents exist.
  it("offers an empty password row for the role's provider key", () => {
    const { container } = renderRoles();
    const field = container.querySelector("#roles-refiner-llm-key") as HTMLInputElement;
    expect(field.type).toBe("password");
    // Nothing here holds a key to show: the plugin keeps no copy, so the row always
    // starts empty, on an edit as on a new install.
    expect(field.value).toBe("");
  });

  it("writes a typed provider key back to the role", () => {
    const { container, setInput } = renderRoles();
    setInput.mockClear();
    const field = container.querySelector("#roles-developer-llm-key") as HTMLInputElement;
    fireEvent.change(field, { target: { value: "sk-dev" } });
    const next = setInput.mock.calls[0][0] as Input;
    expect(next.roles.developer.llmKey).toBe("sk-dev");
  });

  it("sends no key when the row is left empty", () => {
    // Empty is not a write: a row nobody typed in makes no edit at all, so the role's
    // input carries no key field, the plan draws no op, and the agent keeps the key it
    // holds — the same rule the working-hours row follows.
    const { container, setInput } = renderRoles({ editing: true });
    setInput.mockClear();
    const field = container.querySelector("#roles-refiner-llm-key") as HTMLInputElement;
    expect(field.value).toBe("");
    expect(setInput).not.toHaveBeenCalled();
  });

  it("names the shipped default next to the turn timeout on a new install", () => {
    const { container } = renderRoles();
    // The developer ships with the 2-hour cap; the refiner and reviewer with 1 hour.
    expect(container.textContent).toContain("shipped default (120 minutes)");
    expect(container.textContent).toContain("shipped default (60 minutes)");
  });
});

// The role step's working hours: what the admin asks for, what the form then holds, and what it
// gives back when the ask is taken away. The window's meaning is the plan's business; what belongs
// to this screen is that a window nobody asked for is never sent, and that the one the admin did
// ask for is described in the same words the preview will use.
describe("working hours", () => {
  type Hours = NonNullable<Input["roles"]["refiner"]["workingHours"]>;

  // The walk's input with one role's window set — or cleared, which is what `undefined` is
  // here rather than a value to be skipped.
  function inputWithHours(
    role: "refiner" | "developer" | "reviewer",
    hours?: Hours,
  ): Input {
    const next: Input = { ...baseInput, roles: { ...baseInput.roles } };
    next.roles[role] = { ...baseInput.roles[role], workingHours: hours };
    return next;
  }

  // The window the form starts a role with, for the tests that go on to change one part of it.
  const week = (): Hours => ({ timezone: "", days: [1, 2, 3, 4, 5], start: "09:00", end: "17:00" });

  const hoursTick = (container: HTMLElement, role: "Refiner" | "Developer" | "Reviewer") =>
    hoursGroup(container, role).querySelector<HTMLInputElement>(".forgejo-hours-on")!;
  const dayButton = (
    container: HTMLElement,
    role: "Refiner" | "Developer" | "Reviewer",
    label: string,
  ) =>
    Array.from(hoursGroup(container, role).querySelectorAll<HTMLButtonElement>(".forgejo-hours-day")).find(
      (b) => b.textContent === label,
    )!;
  const lastInput = (setInput: ReturnType<typeof vi.fn>) =>
    setInput.mock.calls[setInput.mock.calls.length - 1][0] as Input;

  it("offers no window until the admin asks for one", () => {
    const { container } = renderRoles();
    const group = hoursGroup(container, "Refiner");

    expect(hoursTick(container, "Refiner").checked).toBe(false);
    // No day buttons, no hour fields, no zone: an unanswered ask holds no window at all, which
    // is what the plan needs to hear in order to say nothing about the field.
    expect(group.querySelector(".forgejo-hours-day")).toBeNull();
    expect(container.querySelector("#roles-refiner-hours-start")).toBeNull();
    expect(group.querySelector(".forgejo-hours-summary")).toBeNull();
    // And the screen says what leaving it alone means, because it is not the same nothing as a
    // window taken back off.
    expect(group.querySelector(".forgejo-hours-none-hint")?.textContent).toContain(
      "works around the clock",
    );
  });

  it("asks for a working week when the admin ticks the window on", () => {
    const { container, setInput } = renderRoles();
    fireEvent.click(hoursTick(container, "Refiner"));

    expect(lastInput(setInput).roles.refiner.workingHours).toEqual({
      timezone: "",
      days: [1, 2, 3, 4, 5],
      start: "09:00",
      end: "17:00",
    });
    // The controls arrive with the window they describe.
    expect(container.querySelector("#roles-refiner-hours-start")).not.toBeNull();
    expect(container.querySelector("#roles-refiner-hours-tz")).not.toBeNull();
    expect(hoursGroup(container, "Refiner").querySelectorAll(".forgejo-hours-day")).toHaveLength(7);
  });

  it("sends no window at all when the admin ticks it back off", () => {
    // The clear's other half: the form stops holding a window, so the request carries no
    // workingHours key. The plan is what turns a row that had one into the hub's removal.
    const { container, setInput } = renderRoles({
      input: inputWithHours("developer", { timezone: "UTC", days: [1], start: "09:00", end: "17:00" }),
    });
    fireEvent.click(hoursTick(container, "Developer"));

    const next = lastInput(setInput);
    expect(next.roles.developer.workingHours).toBeUndefined();
    expect("workingHours" in next.roles.developer).toBe(true);
  });

  it("moves a day into the window and back out of it", () => {
    const { container, setInput } = renderRoles({ input: inputWithHours("refiner", week()) });
    fireEvent.click(dayButton(container, "Refiner", "Sat"));
    expect(lastInput(setInput).roles.refiner.workingHours?.days).toEqual([1, 2, 3, 4, 5, 6]);

    fireEvent.click(dayButton(container, "Refiner", "Sat"));
    expect(lastInput(setInput).roles.refiner.workingHours?.days).toEqual([1, 2, 3, 4, 5]);
  });

  it("presses the Sunday button for a Sunday written as 7", () => {
    // The hub takes 0 or 7 for Sunday, and an install row can hold either. One button, pressed
    // either way — and one entry in the list that goes back out.
    const { container } = renderRoles({
      input: inputWithHours("reviewer", { timezone: "UTC", days: [7], start: "", end: "" }),
    });
    expect(dayButton(container, "Reviewer", "Sun").getAttribute("aria-pressed")).toBe("true");
    expect(dayButton(container, "Reviewer", "Mon").getAttribute("aria-pressed")).toBe("false");
  });

  it("types the bounds and the zone into the window", () => {
    const { container, setInput } = renderRoles({
      input: inputWithHours("developer", { timezone: "", days: [1], start: "09:00", end: "17:00" }),
    });
    fireEvent.change(container.querySelector("#roles-developer-hours-start")!, {
      target: { value: "07:30" },
    });
    expect(lastInput(setInput).roles.developer.workingHours?.start).toBe("07:30");

    fireEvent.change(container.querySelector("#roles-developer-hours-tz")!, {
      target: { value: "Europe/Berlin" },
    });
    expect(lastInput(setInput).roles.developer.workingHours?.timezone).toBe("Europe/Berlin");
  });

  it("says the window in the words the preview will use", () => {
    const { container } = renderRoles({
      input: inputWithHours("developer", {
        timezone: "Europe/Berlin",
        days: [1, 2, 3, 4, 5],
        start: "09:00",
        end: "17:00",
      }),
    });
    expect(container.querySelector(".forgejo-hours-summary")?.textContent).toBe(
      "Mon–Fri 09:00–17:00 Europe/Berlin",
    );
  });

  it("reads a night shift as the one window it is", () => {
    const { container } = renderRoles({
      input: inputWithHours("refiner", { timezone: "", days: [], start: "22:00", end: "06:00" }),
    });
    expect(container.querySelector(".forgejo-hours-summary")?.textContent).toBe(
      "every day 22:00–06:00 UTC",
    );
  });

  it("keeps each role's window its own", () => {
    // The runtime has a tick that shares one value across the three roles; the window has no such
    // tick, and the bulk of a shared object here would be three agents moved by one click.
    const { container, setInput } = renderRoles({
      input: inputWithHours("refiner", week()),
    });
    fireEvent.click(dayButton(container, "Refiner", "Sat"));

    const next = lastInput(setInput);
    expect(next.roles.refiner.workingHours?.days).toEqual([1, 2, 3, 4, 5, 6]);
    expect(next.roles.developer.workingHours).toBeUndefined();
    expect(next.roles.reviewer.workingHours).toBeUndefined();
  });
});
