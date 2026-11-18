import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Installs } from "./Installs";
import { post } from "../testing/sdk";

describe("Installs (S0)", () => {
  afterEach(() => {
    // Without this every test leaves its screen mounted, and a later getByText matches the
    // stale one beside the live one.
    cleanup();
  });

  it("renders the installs list", () => {
    const installs = [
      {
        slug: "apps",
        groupId: "g-1",
        baseUrl: "https://git.example.com",
        org: "example",
        connectorId: "c-1",
        mcpServer: "forgejo-mcp-apps",
        hookId: 1,
        roles: {},
        excludeLabel: "do-not-refine",
      },
    ];

    const { container } = render(
      <Installs
        installs={installs}
        onNewInstall={vi.fn()}
        onEditInstall={vi.fn()}
        onConnectInstall={vi.fn()}
        onRemoveInstall={vi.fn()}
      />
    );

    // Should render the install list
    expect(container.querySelector('.forgejo-installs')).toBeTruthy();
    expect(container.textContent).toContain("apps");
  });

  it("has a remove button for each install", () => {
    const installs = [
      {
        slug: "apps",
        groupId: "g-1",
        baseUrl: "https://git.example.com",
        org: "example",
        connectorId: "c-1",
        mcpServer: "forgejo-mcp-apps",
        hookId: 1,
        roles: {},
        excludeLabel: "do-not-refine",
      },
    ];

    const { container } = render(
      <Installs
        installs={installs}
        onNewInstall={vi.fn()}
        onEditInstall={vi.fn()}
        onConnectInstall={vi.fn()}
        onRemoveInstall={vi.fn()}
      />
    );

    // Should have a remove button
    const removeBtn = container.querySelector('.forgejo-remove-btn');
    expect(removeBtn).toBeTruthy();
  });

  it("remove confirms once while running", async () => {
    // A removal walks the hub and Forgejo in order. A second confirm while the first is
    // still running works on resources that are partly gone: it reports failures the admin
    // cannot act on and can leave a row listed that a single removal would have cleared.
    const installs = [
      {
        slug: "apps",
        groupId: "g-1",
        baseUrl: "https://git.example.com",
        org: "example",
        connectorId: "c-1",
        mcpServer: "forgejo-mcp-apps",
        hookId: 1,
        roles: {},
        excludeLabel: "do-not-refine",
      },
    ];

    let resolveRemove: () => void = () => {};
    const onRemoveInstall = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolveRemove = () => resolve();
        }),
    );

    const user = userEvent.setup();
    const { container } = render(
      <Installs
        installs={installs}
        onNewInstall={vi.fn()}
        onEditInstall={vi.fn()}
        onConnectInstall={vi.fn()}
        onRemoveInstall={onRemoveInstall}
      />,
    );

    await user.click(container.querySelector(".forgejo-remove-btn") as HTMLButtonElement);
    const confirm = container.querySelector(
      ".forgejo-confirm-remove-btn",
    ) as HTMLButtonElement;
    expect(confirm).toBeTruthy();

    await user.click(confirm);
    expect(onRemoveInstall).toHaveBeenCalledTimes(1);

    // The dialog is still open — it closes when the removal answers — and it refuses a
    // second submit while that answer is outstanding.
    expect(container.querySelector(".forgejo-remove-dialog")).toBeTruthy();
    expect(confirm.disabled, "the confirm button is out of reach while running").toBe(true);
    fireEvent.click(confirm);
    fireEvent.click(confirm);
    expect(onRemoveInstall).toHaveBeenCalledTimes(1);

    resolveRemove();
    await waitFor(() => {
      expect(container.querySelector(".forgejo-remove-dialog")).toBeNull();
    });
  });

  it("an unverified install reads as waiting, not as an error", () => {
    // An install whose first event has not arrived is complete and waiting — the webhook is
    // marked verified on the first delivery. An error tag made that row read like a failure.
    const installs = [
      {
        slug: "apps",
        groupId: "g-1",
        baseUrl: "https://git.example.com",
        org: "example",
        connectorId: "c-1",
        mcpServer: "forgejo-mcp-apps",
        hookId: 1,
        roles: {},
        excludeLabel: "do-not-refine",
      },
    ];

    const { container } = render(
      <Installs
        installs={installs}
        onNewInstall={vi.fn()}
        onEditInstall={vi.fn()}
        onConnectInstall={vi.fn()}
        onRemoveInstall={vi.fn()}
      />,
    );

    // Waiting is a warn tag on the row, and nothing on the list is an error.
    const waiting = screen.getByText("Waiting for first event");
    expect(waiting.getAttribute("data-variant")).toBe("warn");
    expect(container.querySelector('[data-variant="err"]')).toBeNull();
    // The way back to the watching screen stays where it was.
    expect(container.querySelector(".forgejo-connect-btn")).toBeTruthy();
  });

  it("calls onEditInstall when Edit button is clicked", async () => {
    const onEditInstall = vi.fn();
    const user = userEvent.setup();
    const installs = [
      {
        slug: "apps",
        groupId: "g-1",
        baseUrl: "https://git.example.com",
        org: "example",
        connectorId: "c-1",
        mcpServer: "forgejo-mcp-apps",
        hookId: 1,
        roles: {
          refiner: {
            agentId: "a-refiner",
            imageId: "i-refiner",
            personaId: "p-refiner",
            botLogin: "refiner-apps",
            teamId: 1,
            tools: ["get_issue_by_index", "list_issue_comments"],
          },
          developer: {
            agentId: "a-dev",
            imageId: "i-dev",
            personaId: "p-dev",
            botLogin: "developer-apps",
            teamId: 2,
            tools: ["create_file", "create_branch"],
          },
          reviewer: {
            agentId: "a-reviewer",
            imageId: "i-reviewer",
            personaId: "p-reviewer",
            botLogin: "reviewer-apps",
            teamId: 3,
            tools: ["get_pull_request_by_index"],
          },
        },
        excludeLabel: "custom-label",
      },
    ];

    const { container } = render(
      <Installs
        installs={installs}
        onNewInstall={vi.fn()}
        onEditInstall={onEditInstall}
        onConnectInstall={vi.fn()}
        onRemoveInstall={vi.fn()}
      />
    );

    const editBtn = container.querySelector('.forgejo-edit-btn');
    expect(editBtn).toBeTruthy();
    await user.click(editBtn!);
    expect(onEditInstall).toHaveBeenCalledWith("apps");
  });

  // The installs table (S0) shows what each setup made, derived from the ids on the
  // install row itself — the record of results lives beside the setup, not only on the
  // done screen that closes.

  const completeRoles = {
    refiner: {
      agentId: "a-refiner",
      imageId: "i-refiner",
      personaId: "p-refiner",
      botLogin: "refiner-apps",
      teamId: 11,
      tools: [],
    },
    developer: {
      agentId: "a-dev",
      imageId: "i-dev",
      personaId: "p-dev",
      botLogin: "developer-apps",
      teamId: 12,
      tools: [],
    },
    reviewer: {
      agentId: "a-reviewer",
      imageId: "i-reviewer",
      personaId: "p-reviewer",
      botLogin: "reviewer-apps",
      teamId: 13,
      tools: [],
    },
  };

  const fullInstall = {
    slug: "apps",
    groupId: "g-1",
    baseUrl: "https://git.example.com",
    org: "example",
    connectorId: "c-1",
    mcpServer: "forgejo-mcp-apps",
    hookId: 7,
    roles: completeRoles,
    excludeLabel: "do-not-refine",
    webhookVerifiedAt: "2026-10-26T19:00:00Z",
    coverage: {
      issues: "2026-10-26T19:01:00Z",
      issue_comment: "2026-10-26T19:02:00Z",
      pull_request: "2026-10-26T19:03:00Z",
    },
  };

  const renderInstalls = (installs: any[]) =>
    render(
      <Installs
        installs={installs}
        onNewInstall={vi.fn()}
        onEditInstall={vi.fn()}
        onConnectInstall={vi.fn()}
        onRemoveInstall={vi.fn()}
      />,
    );

  // The event family chips read "✓" then the name when the org has delivered that family, and
  // "–" then the name when it has not. A chip's text is the mark and the name, with no space.
  const chip = (container: HTMLElement, label: string) =>
    Array.from(container.querySelectorAll("span")).find(
      (el) => el.textContent === `✓${label}` || el.textContent === `–${label}`,
    );

  it("a complete install shows its group, its agents, a verified webhook and every family seen", () => {
    const { container } = render(
      <Installs
        installs={[fullInstall]}
        groups={[{ id: "g-1", name: "platform", role: "owner" }]}
        onNewInstall={vi.fn()}
        onEditInstall={vi.fn()}
        onConnectInstall={vi.fn()}
        onRemoveInstall={vi.fn()}
      />,
    );

    // A real table, as the issue asks.
    expect(container.querySelector('[data-testid="register-table"]')).toBeTruthy();
    // The group is read by its name, not the id the install stores.
    expect(screen.getByText("platform")).toBeTruthy();
    expect(screen.getByText("3 agents")).toBeTruthy();
    expect(screen.getByText(/^Verified /).getAttribute("data-variant")).toBe("ok");

    for (const label of ["Issues", "Comments", "Pull requests"]) {
      expect(chip(container, label), `${label} is seen`).toBeTruthy();
    }

    // The verified row still hides the way back to the watching screen.
    expect(container.querySelector(".forgejo-connect-btn")).toBeNull();
  });

  it("a partial row counts the agents it has, and dashes the families it has not seen", () => {
    // A row saved half way through the wizard is the normal state of a row: the counts
    // say what exists, and nothing on the screen reads as broken.
    const partial = {
      ...fullInstall,
      slug: "half",
      connectorId: "",
      mcpServer: "",
      hookId: 0,
      webhookVerifiedAt: undefined,
      coverage: undefined,
      roles: { refiner: completeRoles.refiner },
    };

    const { container } = renderInstalls([partial]);

    expect(screen.getByText("1 agent")).toBeTruthy();
    for (const label of ["Issues", "Comments", "Pull requests"]) {
      expect(chip(container, label)?.textContent, `${label} is not seen yet`).toBe(`–${label}`);
    }
    expect(screen.getByText("Waiting for first event").getAttribute("data-variant")).toBe("warn");
    expect(container.querySelector('[data-variant="err"]')).toBeNull();

    // An unverified row still offers the way back to the watching screen.
    expect(container.querySelector(".forgejo-connect-btn")).toBeTruthy();
  });

  it("an empty roles map is zero agents, not a crash", () => {
    const bare = { ...fullInstall, slug: "bare", roles: {}, coverage: undefined };
    renderInstalls([bare]);

    expect(screen.getByText("0 agents")).toBeTruthy();
  });

  it("coverage fills only the families the row has seen", () => {
    const oneFamily = {
      ...fullInstall,
      slug: "one",
      coverage: { issues: "2026-10-26T19:01:00Z" },
    };

    const { container } = renderInstalls([oneFamily]);
    expect(chip(container, "Issues")?.textContent).toBe("✓Issues");
    expect(chip(container, "Comments")?.textContent).toBe("–Comments");
    expect(chip(container, "Pull requests")?.textContent).toBe("–Pull requests");
  });

  it("offers New install in the page header once there is an install to sit beside", () => {
    const { container } = renderInstalls([fullInstall]);
    const buttons = container.querySelectorAll(".forgejo-new-install-btn");
    expect(buttons).toHaveLength(1);
    expect(buttons[0].textContent).toBe("New install");
  });

  it("an empty installs list keeps its copy and offers New install in the panel", () => {
    const { container } = renderInstalls([]);
    expect(container.textContent).toContain("No installs configured yet.");
    // One button, in the empty state: the header does not repeat it.
    expect(container.querySelectorAll(".forgejo-new-install-btn")).toHaveLength(1);
    expect(container.querySelector('[data-testid="register-table"]')).toBeNull();
  });

  // -- the leftover sweep -----------------------------------------------------------

  const sweepInstances = [{ name: "prod", baseUrl: "https://git.example.com" }];

  const renderSweep = () =>
    render(
      <Installs
        installs={[]}
        instances={sweepInstances}
        onNewInstall={vi.fn()}
        onEditInstall={vi.fn()}
        onConnectInstall={vi.fn()}
        onRemoveInstall={vi.fn()}
      />,
    );

  // Fill the sweep form and ask for the plan. The one configured instance is
  // already the selected one, so only the three typed fields are touched.
  const askPlan = async (container: HTMLElement) => {
    const user = userEvent.setup();
    await user.click(container.querySelector(".forgejo-orphans-open-btn") as HTMLButtonElement);
    await user.type(container.querySelector("#orphans-slug") as HTMLInputElement, "old");
    await user.type(container.querySelector("#orphans-org") as HTMLInputElement, "example");
    await user.type(container.querySelector("#orphans-token") as HTMLInputElement, "ADMINTOKEN");
    await user.click(container.querySelector(".forgejo-orphans-plan-btn") as HTMLButtonElement);
    return user;
  };

  it("the sweep plans first: the plan lists every candidate before a delete is offered", async () => {
    post.mockImplementation(async (path: string) => {
      if (path === "/setup/orphans/old/plan") {
        return {
          slug: "old",
          baseUrl: "https://git.example.com",
          org: "example",
          items: [
            { kind: "user", name: "refiner-old", id: "refiner-old", detail: "purges content" },
            { kind: "hook", name: "https://hub.test/api/v1/connectors/c-9/webhook", id: "9" },
          ],
        };
      }
      throw new Error(`unexpected POST ${path}`);
    });

    const { container } = renderSweep();
    // No delete button exists before the plan has been shown.
    expect(container.querySelector(".forgejo-orphans-delete-btn")).toBeNull();

    await askPlan(container);
    await waitFor(() => expect(screen.getByText("refiner-old")).toBeTruthy());
    expect(screen.getByText("https://hub.test/api/v1/connectors/c-9/webhook")).toBeTruthy();

    const calls = post.mock.calls.filter(([p]) => p === "/setup/orphans/old/plan");
    expect(calls).toHaveLength(1);
    expect(calls[0][1]).toEqual({
      baseUrl: "https://git.example.com",
      org: "example",
      adminToken: "ADMINTOKEN",
    });

    // Now the delete stands before the admin — and it is the same route pair the
    // console documents.
    const deleteBtn = container.querySelector(".forgejo-orphans-delete-btn") as HTMLButtonElement;
    expect(deleteBtn).toBeTruthy();
    expect(deleteBtn.disabled).toBe(false);
  });

  it("the sweep's delete removes the planned set and reports what went", async () => {
    post.mockImplementation(async (path: string) => {
      if (path.endsWith("/plan")) {
        return {
          slug: "old",
          baseUrl: "https://git.example.com",
          org: "example",
          items: [{ kind: "user", name: "refiner-old", id: "refiner-old" }],
        };
      }
      if (path.endsWith("/delete")) {
        return { deleted: ["user:refiner-old"], failed: [] };
      }
      throw new Error(`unexpected POST ${path}`);
    });

    const { container } = renderSweep();
    const user = await askPlan(container);
    await waitFor(() => expect(screen.getByText("refiner-old")).toBeTruthy());

    await user.click(container.querySelector(".forgejo-orphans-delete-btn") as HTMLButtonElement);
    await waitFor(() => expect(screen.getByText(/Deleted 1 resource\./)).toBeTruthy());
    expect(post.mock.calls.some(([p]) => p === "/setup/orphans/old/delete")).toBe(true);
  });

  it("a plan that finds nothing says so and offers no delete", async () => {
    post.mockImplementation(async (path: string) => {
      if (path.endsWith("/plan")) {
        return { slug: "old", baseUrl: "https://git.example.com", org: "example", items: [] };
      }
      throw new Error(`unexpected POST ${path}`);
    });

    const { container } = renderSweep();
    await askPlan(container);
    await waitFor(() => expect(screen.getByText(/Nothing was found/)).toBeTruthy());
    const deleteBtn = container.querySelector(".forgejo-orphans-delete-btn") as HTMLButtonElement;
    expect(deleteBtn.disabled, "nothing to delete").toBe(true);
  });

  it("a refused plan names the refusal and leaves no delete behind", async () => {
    post.mockImplementation(async () => {
      const err: any = new Error("adminToken is required");
      err.status = 400;
      throw err;
    });

    const { container } = renderSweep();
    await askPlan(container);
    await waitFor(() => expect(screen.getByText("adminToken is required")).toBeTruthy());
    expect(container.querySelector(".forgejo-orphans-delete-btn")).toBeNull();
  });
});
