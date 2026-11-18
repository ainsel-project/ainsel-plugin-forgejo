import { describe, it, expect, vi } from "vitest";
import { render, fireEvent } from "@testing-library/react";
import { Instance } from "./Instance";
import { newInput } from "./model";
import type { ForgejoInstance } from "../api";

const groups = [
  { id: "g-blocked", name: "blocked-group", role: "owner" },
  { id: "g-allowed", name: "allowed-group", role: "owner" },
];

// renderStep draws the step over a given instance list. `overrides` is what a
// test needs beyond the plain input: the install's own address on an edit, and
// the setInput the test wants to watch.
function renderStep(
  instances: ForgejoInstance[],
  input = newInput(),
  overrides: { currentBaseUrl?: string; setInput?: (i: any) => void } = {},
) {
  const setInput = overrides.setInput ?? vi.fn();
  const utils = render(
    <Instance
      input={input}
      setInput={setInput}
      groups={groups}
      pluginGroups={["g-allowed"]}
      instances={instances}
      currentBaseUrl={overrides.currentBaseUrl}
      onNext={vi.fn()}
      onBack={vi.fn()}
    />,
  );
  return { ...utils, setInput };
}

function selectsIn(container: HTMLElement) {
  return Array.from(container.querySelectorAll('[data-testid="select"]')) as HTMLSelectElement[];
}

describe("Instance (S1)", () => {
  it("shows a blocking notice when the chosen group is not in pluginGroups", async () => {
    const input = newInput();
    input.groupId = "g-blocked";

    const { container } = renderStep([{ name: "Primary", baseUrl: "https://a.example.com" }], input);

    const notice = container.querySelector('[data-testid="notice"][data-variant="err"]');
    expect(notice).toBeTruthy();
    expect(notice?.textContent).toContain("Plugins → Forgejo → Group Access");
  });

  it("does not block when the chosen group is in pluginGroups", () => {
    const input = newInput();
    input.groupId = "g-allowed";

    const { container } = renderStep([{ name: "Primary", baseUrl: "https://a.example.com" }], input);

    const errNotice = container.querySelector('[data-testid="notice"][data-variant="err"]');
    expect(errNotice).toBeFalsy();
  });

  // The three shapes the configured list comes in. The step used to carry a
  // free-text address, and a typo there was an install pointed at the wrong
  // host; now the address can only come from the admin's own list.
  it("offers nothing to install against when no instance is configured", () => {
    const { container } = renderStep([]);

    const notice = container.querySelector('[data-testid="notice"][data-variant="err"]');
    expect(notice).toBeTruthy();
    expect(notice?.textContent).toContain("No Forgejo instance is configured");
    // The way out is named, not implied: the list is edited on the Settings page.
    const link = container.querySelector("a.forgejo-settings-link") as HTMLAnchorElement;
    expect(link).toBeTruthy();
    expect(link.getAttribute("href")).toBe("settings");
    // And there is nothing to continue with.
    expect(container.querySelector(".forgejo-next-btn")).toBeFalsy();
  });

  it("selects the only instance instead of offering a one-item choice", () => {
    const input = newInput();
    input.baseUrl = "https://only.example.com";

    const { container } = renderStep([{ name: "Only", baseUrl: "https://only.example.com" }], input);

    // The first select is the group; the second is the instance.
    const instanceSelect = selectsIn(container)[1];
    expect(instanceSelect).toBeTruthy();
    expect(instanceSelect.value).toBe("https://only.example.com");
    const options = Array.from(instanceSelect.querySelectorAll("option"));
    // The name is what the admin reads, the address is what they check.
    expect(options.some((o) => o.textContent?.includes("Only"))).toBe(true);
    expect(options.some((o) => o.textContent?.includes("https://only.example.com"))).toBe(true);
  });

  it("offers every configured instance when several are configured", () => {
    const input = newInput();

    const { container, setInput } = renderStep(
      [
        { name: "Primary", baseUrl: "https://a.example.com" },
        { name: "Lab", baseUrl: "https://b.example.com" },
      ],
      input,
    );

    const instanceSelect = selectsIn(container)[1];
    const values = Array.from(instanceSelect.querySelectorAll("option")).map((o) => o.value);
    expect(values).toContain("https://a.example.com");
    expect(values).toContain("https://b.example.com");

    // Choosing one is the only way the address is set: there is no text field to
    // type an address into any more.
    fireEvent.change(instanceSelect, { target: { value: "https://b.example.com" } });
    expect(setInput).toHaveBeenCalledWith(expect.objectContaining({ baseUrl: "https://b.example.com" }));
  });

  it("carries an edit's own address when it is not a configured instance", () => {
    // The case the plan's blank-baseUrl rule exists for: an install predating the
    // instance list keeps its address, and the step says so instead of forcing a
    // move to a listed host.
    const input = newInput();
    input.baseUrl = "https://legacy.example.com";

    const { container } = renderStep([{ name: "Primary", baseUrl: "https://a.example.com" }], input, {
      currentBaseUrl: "https://legacy.example.com",
    });

    const notice = container.querySelector('[data-testid="notice"][data-variant="warn"]');
    expect(notice).toBeTruthy();
    expect(notice?.textContent).toContain("https://legacy.example.com");
    // The step can still go on: leaving the choice untouched keeps the address.
    expect(container.querySelector(".forgejo-next-btn")).toBeTruthy();
  });

  it("blocks Continue when there is no instance and no install to keep", () => {
    const { container } = renderStep([]);
    expect(container.querySelector(".forgejo-next-btn")).toBeFalsy();
  });

  it("blocks Continue on a create while the instance select is untouched", () => {
    // The hole the backend now refuses too: with several instances configured, a
    // new install that never picks one would land on the plugin's env address and
    // store no address of its own. Picking one opens the step.
    const { container } = renderStep([
      { name: "Primary", baseUrl: "https://a.example.com" },
      { name: "Lab", baseUrl: "https://b.example.com" },
    ]);
    expect(container.querySelector(".forgejo-next-btn")).toBeFalsy();

    const input = newInput();
    input.baseUrl = "https://b.example.com";
    const picked = renderStep(
      [
        { name: "Primary", baseUrl: "https://a.example.com" },
        { name: "Lab", baseUrl: "https://b.example.com" },
      ],
      input,
    );
    expect(picked.container.querySelector(".forgejo-next-btn")).toBeTruthy();
  });
});
