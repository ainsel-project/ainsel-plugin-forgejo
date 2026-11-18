// The Forgejo instances panel: the list the setup wizard selects over, edited
// here and written whole. The tests follow the settings page's own habits — the
// list is read on load, a save is one PUT of the whole list, and a refused write
// names each bad row and leaves the drafts where the admin left them.
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { RenderResult } from "@testing-library/react";
import { SettingsPage } from "./SettingsPage";
import { get, post, put } from "./testing/sdk";
import type { ForgejoInstance, Setting } from "./api";

// A registry small enough to read: the page renders whatever the answer carries,
// and these tests are about the second collection on the page, not the first.
const settingsFixture: Setting[] = [
  {
    role: "refiner",
    field: "llmModel",
    value: "llama3.3",
    dirty: false,
    default: "llama3.3",
    defaultVersion: "1.0.0",
  },
];

function mockReads(instances: ForgejoInstance[]) {
  get.mockImplementation(async (path: string) => {
    if (path === "/settings") return { settings: settingsFixture };
    if (path === "/instances") return { instances };
    throw new Error(`unexpected GET ${path}`);
  });
  post.mockImplementation(async (path: string) => {
    if (path === "/settings/reconcile") return { updated: [], refused: [] };
    throw new Error(`unexpected POST ${path}`);
  });
}

// Open the instances pane. The nav names it; the pane's heading says it opened.
async function openInstances(user: ReturnType<typeof userEvent.setup>): Promise<RenderResult> {
  const utils = render(<SettingsPage />);
  await screen.findByRole("button", { name: "Reconcile now" });
  await user.click(screen.getByRole("button", { name: /^Forgejo instances/ }));
  await screen.findByRole("heading", { name: "Forgejo instances" });
  return utils;
}

function rows(container: HTMLElement) {
  return Array.from(container.querySelectorAll("[data-instance-row]")) as HTMLElement[];
}

function nameOf(row: HTMLElement) {
  return row.querySelector('input[aria-label$="name"]') as HTMLInputElement;
}
function urlOf(row: HTMLElement) {
  return row.querySelector('input[aria-label$="base URL"]') as HTMLInputElement;
}

// rowButton finds a button inside one instance row: every row carries the same
// buttons, so a name alone is not enough.
function rowButton(row: HTMLElement, name: string): HTMLButtonElement {
  const btn = Array.from(row.querySelectorAll("button")).find((b) =>
    (b.textContent ?? "").trim().startsWith(name),
  );
  if (!btn) throw new Error(`no "${name}" button in the row`);
  return btn as HTMLButtonElement;
}

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  put.mockReset();
});

afterEach(() => {
  cleanup();
});

describe("SettingsPage — Forgejo instances", () => {
  it("lists the configured instances in the order the answer carries them", async () => {
    const user = userEvent.setup();
    mockReads([
      { name: "Primary", baseUrl: "https://a.example.com" },
      { name: "Lab", baseUrl: "https://b.example.com" },
    ]);

    const { container } = await openInstances(user);

    const all = rows(container);
    expect(all).toHaveLength(2);
    expect(nameOf(all[0]).value).toBe("Primary");
    expect(urlOf(all[1]).value).toBe("https://b.example.com");
  });

  it("says there is nothing to install against when the list is empty", async () => {
    const user = userEvent.setup();
    mockReads([]);

    const { container } = await openInstances(user);

    const notice = container.querySelector('[data-testid="notice"][data-variant="warn"]');
    expect(notice).toBeTruthy();
    expect(notice?.textContent).toContain("No instance is configured");
  });

  it("saves the whole list in one PUT, including the row it added", async () => {
    const user = userEvent.setup();
    mockReads([{ name: "Primary", baseUrl: "https://a.example.com" }]);
    put.mockImplementation(async (path: string, body: any) => {
      if (path === "/instances") return { instances: body.instances };
      throw new Error(`unexpected PUT ${path}`);
    });

    const { container } = await openInstances(user);

    await user.click(screen.getByRole("button", { name: "Add instance" }));
    const all = rows(container);
    expect(all).toHaveLength(2);

    fireEvent.change(nameOf(all[1]), { target: { value: "Lab" } });
    fireEvent.change(urlOf(all[1]), { target: { value: "https://b.example.com" } });

    await user.click(screen.getByRole("button", { name: "Save instances" }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/instances", {
        instances: [
          { name: "Primary", baseUrl: "https://a.example.com" },
          { name: "Lab", baseUrl: "https://b.example.com" },
        ],
      }),
    );
  });

  it("removes a row from the list it saves", async () => {
    const user = userEvent.setup();
    mockReads([
      { name: "Keep", baseUrl: "https://a.example.com" },
      { name: "Drop", baseUrl: "https://b.example.com" },
    ]);
    put.mockImplementation(async (path: string, body: any) => {
      if (path === "/instances") return { instances: body.instances };
      throw new Error(`unexpected PUT ${path}`);
    });

    const { container } = await openInstances(user);

    const before = rows(container);
    expect(before).toHaveLength(2);
    await user.click(rowButton(before[1], "Remove"));
    // Removing is a draft edit, not a write: the list is written whole, so the
    // admin's Save is what takes the row out for good.
    await user.click(screen.getByRole("button", { name: "Save instances" }));

    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/instances", {
        instances: [{ name: "Keep", baseUrl: "https://a.example.com" }],
      }),
    );
  });

  it("names each refused row and keeps the draft the admin typed", async () => {
    const user = userEvent.setup();
    mockReads([{ name: "Primary", baseUrl: "https://a.example.com" }]);
    put.mockImplementation(async () => {
      const err: any = new Error("invalid instances");
      err.body = {
        error: "invalid instances",
        fields: [
          { role: "", field: "instances[0].name", error: "instance name is required" },
          { role: "", field: "instances[0].baseUrl", error: "not an absolute http(s) address" },
        ],
      };
      throw err;
    });

    const { container } = await openInstances(user);

    const row = rows(container)[0];
    fireEvent.change(nameOf(row), { target: { value: "" } });
    fireEvent.change(urlOf(row), { target: { value: "not-a-url" } });
    await user.click(screen.getByRole("button", { name: "Save instances" }));

    // Both halves of the one bad row are said, and the typed values stay on the
    // screen — the write refused, so there is nothing to reload.
    await waitFor(() => expect(row.textContent).toContain("instance name is required"));
    expect(row.textContent).toContain("not an absolute http(s) address");
    expect(nameOf(row).value).toBe("");
    expect(urlOf(row).value).toBe("not-a-url");
  });

  it("offers Discard while the list has unsaved changes", async () => {
    const user = userEvent.setup();
    mockReads([{ name: "Primary", baseUrl: "https://a.example.com" }]);

    const { container } = await openInstances(user);

    const row = rows(container)[0];
    fireEvent.change(nameOf(row), { target: { value: "Renamed" } });
    expect((screen.getByRole("button", { name: "Save instances" }) as HTMLButtonElement).disabled).toBe(false);

    await user.click(screen.getByRole("button", { name: "Discard" }));
    await waitFor(() => expect(nameOf(rows(container)[0]).value).toBe("Primary"));
    expect(put).not.toHaveBeenCalled();
  });
});
