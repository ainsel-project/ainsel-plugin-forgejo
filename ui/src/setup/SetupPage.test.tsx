// The wizard router: SetupPage owns the step machine, the plan/apply calls and
// the secret clearing, so these tests drive it the way the console would —
// through the screens — and assert on what the screens answer.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SetupPage } from "./SetupPage";
import { get, post } from "../testing/sdk";
import { assertEveryLabelPointsAtItsControl } from "../testing/dom";
import type { ApplyResponse, PlanResponse, RoleState, SetupOptions } from "../api";

const optionsFixture: SetupOptions = {
  groups: [{ id: "g-1", name: "apps", role: "owner" }],
  pluginGroups: ["g-1"],
  forgejoBaseURL: "https://forgejo.example.com",
  // One configured instance: the step carries it, so a walk through S1 does not
  // have to choose it.
  instances: [{ name: "Primary", baseUrl: "https://forgejo.example.com" }],
  tools: {
    defaults: {
      refiner: ["create_issue_comment", "list_issues"],
      developer: ["create_branch"],
      reviewer: ["create_pull_review"],
    },
    forbidden: { refiner: ["create_branch"], developer: [], reviewer: ["bash"] },
  },
};

// The plan the wizard walks into: nothing pre-existing is named, so the
// rolled-back "refiner-apps" below can only come from the apply answer.
const planFixture: PlanResponse = {
  create: [{ kind: "connector", name: "forgejo-apps", detail: "connector" }],
  change: [],
  delete: [],
  triggers: [],
  verdict: { blocking: [], warnings: [] },
};

// What the three roles of an installed install carry. The tools are what the edit
// walk prefills; the image id stays on the row because the wizard no longer picks
// runtimes from a list — an edit starts with empty runtimes, which means "keep".
const installedRoles: Record<string, RoleState> = {
  refiner: {
    agentId: "",
    imageId: "img-1",
    personaId: "",
    botLogin: "refiner-apps",
    teamId: 1,
    tools: [],
  },
  developer: {
    agentId: "",
    imageId: "img-1",
    personaId: "",
    botLogin: "developer-apps",
    teamId: 2,
    tools: [],
  },
  reviewer: {
    agentId: "",
    imageId: "img-1",
    personaId: "",
    botLogin: "reviewer-apps",
    teamId: 3,
    tools: [],
  },
};

const applyFixture: ApplyResponse = {
  install: {
    slug: "apps",
    groupId: "g-1",
    baseUrl: "https://forgejo.example.com",
    org: "apps",
    connectorId: "conn-1",
    mcpServer: "forgejo-mcp-apps",
    hookId: 7,
    roles: installedRoles,
    excludeLabel: "do-not-refine",
  },
  report: {
    created: [{ kind: "connector", name: "forgejo-apps", detail: "" }],
    rolledBack: [],
    leftovers: [],
  },
};

function inputByPlaceholder(placeholder: string): HTMLInputElement {
  const el = document.querySelector(`input[placeholder="${placeholder}"]`);
  if (!(el instanceof HTMLInputElement)) {
    throw new Error(`no input with placeholder "${placeholder}"`);
  }
  return el;
}

// Fill S1 → S2 and land on S3 (roles), with the admin token in hand.
async function walkToRoles(user: ReturnType<typeof userEvent.setup>) {
  render(<SetupPage />);
  await user.click(await screen.findByRole("button", { name: "New install" }));

  // S1 — instance: slug, group, org
  fireEvent.change(inputByPlaceholder("e.g., apps"), { target: { value: "apps" } });
  const groupSelect = document.querySelector("select") as HTMLSelectElement;
  fireEvent.change(groupSelect, { target: { value: "g-1" } });
  fireEvent.change(inputByPlaceholder("e.g., example-org"), {
    target: { value: "apps" },
  });
  await user.click(screen.getByRole("button", { name: "Continue" }));

  // S2 — the admin token (a secret the wizard must never persist). The forge-MCP
  // step this walk used to stop at is gone: the MCP server an install gets is the
  // plugin's, pointed at by the settings store.
  fireEvent.change(
    inputByPlaceholder("Personal access token with write:admin scope"),
    { target: { value: "admin-token-never-store-me" } },
  );
  await user.click(screen.getByRole("button", { name: "Continue" }));
}

// The step list's button for a step, by its label. Its accessible name is the label and the one
// line under it, so the match is on the start of the name.
function stepButton(label: string): HTMLButtonElement {
  return screen.getByRole("button", { name: new RegExp(`^${label}`) }) as HTMLButtonElement;
}

// The roles step takes a container per role as free text (there is no image pick list to
// choose from any more) and refuses Continue while any of them is empty, so a walk through the
// step types one into each role's card.
async function pickRuntimes(_user: ReturnType<typeof userEvent.setup>) {
  for (const role of ["refiner", "developer", "reviewer"]) {
    fireEvent.change(document.querySelector(`#roles-${role}-runtime`) as HTMLInputElement, {
      target: { value: "registry.example.com/base:latest" },
    });
  }
}

// Click through S0 → S1 → S2 → S3 → S4 → S5 with a filled-in admin token.
async function walkToPreview(user: ReturnType<typeof userEvent.setup>) {
  await walkToRoles(user);

  // Roles: a container per role, then rules: defaults carry through
  await pickRuntimes(user);
  await user.click(screen.getByRole("button", { name: "Continue" }));
  await user.click(screen.getByRole("button", { name: "Continue" }));

  // Preview, once the plan answers
  await screen.findByRole("button", { name: /^Apply/ });
}

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  get.mockImplementation(async (path: string) => {
    if (path === "/setup/options") return optionsFixture;
    if (path === "/setup/installs") return { installs: [] };
    if (path.endsWith("/deliveries")) return { verified: false, at: null };
    throw new Error(`unexpected GET ${path}`);
  });
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("SetupPage (the wizard router)", () => {
  it("carries the one configured instance into the plan without a choice", async () => {
    // One Forgejo is not a choice: the step fills the address in, so the walk
    // never shows an empty select for a thing there is only one of.
    const user = userEvent.setup();
    post.mockImplementation(async (path: string) => {
      if (path === "/setup/plan") return planFixture;
      throw new Error(`unexpected POST ${path}`);
    });

    render(<SetupPage />);
    await user.click(await screen.findByRole("button", { name: "New install" }));
    fireEvent.change(inputByPlaceholder("e.g., apps"), { target: { value: "apps" } });
    fireEvent.change(document.querySelector("select") as HTMLSelectElement, {
      target: { value: "g-1" },
    });
    fireEvent.change(inputByPlaceholder("e.g., example-org"), { target: { value: "apps" } });
    await user.click(screen.getByRole("button", { name: "Continue" }));
    fireEvent.change(inputByPlaceholder("Personal access token with write:admin scope"), {
      target: { value: "admin-token-never-store-me" },
    });
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await pickRuntimes(user);
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await user.click(screen.getByRole("button", { name: "Continue" }));

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/setup/plan", expect.objectContaining({
        baseUrl: "https://forgejo.example.com",
      })),
    );
  });

  it("stops the walk where no instance is configured", async () => {
    // The wizard cannot invent an address any more, so with an empty list it says
    // so and points at the page that owns the list instead of walking on.
    const user = userEvent.setup();
    get.mockImplementation(async (path: string) => {
      if (path === "/setup/options") return { ...optionsFixture, instances: [] };
      if (path === "/setup/installs") return { installs: [] };
      throw new Error(`unexpected GET ${path}`);
    });

    const { container } = render(<SetupPage />);
    await user.click(await screen.findByRole("button", { name: "New install" }));

    const notice = container.querySelector('[data-testid="notice"][data-variant="err"]');
    expect(notice).toBeTruthy();
    expect(notice?.textContent).toContain("No Forgejo instance is configured");
    expect(container.querySelector(".forgejo-next-btn")).toBeFalsy();
  });

  it("names every control the walk asks the admin to fill", async () => {
    // The screens rendered `<Field label="…">` with no control for the label to point at: the
    // text was above the box, the association was not, so a screen reader working through the
    // form met a slug box and a token box with nothing to call them.
    const user = userEvent.setup();
    const { container } = render(<SetupPage />);
    await waitFor(() => {
      expect(container.querySelector(".forgejo-new-install-btn")).toBeTruthy();
    });
    assertEveryLabelPointsAtItsControl("S0", container);

    await user.click(screen.getByRole("button", { name: "New install" }));
    assertEveryLabelPointsAtItsControl("S1", container);
    await user.click(screen.getByRole("button", { name: "Continue" }));

    assertEveryLabelPointsAtItsControl("S2", container);
    // A new install needs the token before it can go on: the accounts are created with it.
    fireEvent.change(inputByPlaceholder("Personal access token with write:admin scope"), {
      target: { value: "admin-token-never-store-me" },
    });
    await user.click(screen.getByRole("button", { name: "Continue" }));

    // The roles step shows the three role cards together, and the labels of all of them are checked.
    assertEveryLabelPointsAtItsControl("S3", container);
    // And the step lets the walk on only once every role has a runtime.
    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(
      screen.getByRole("alert").textContent,
      "the step says what it is still missing",
    ).toBe("Enter a runtime container for: refiner, developer, reviewer");
    await pickRuntimes(user);
    await user.click(screen.getByRole("button", { name: "Continue" }));

    assertEveryLabelPointsAtItsControl("S4", container);
  });
  it("ticks the default tools when a new install starts", async () => {
    // The roles step's checklist is the server's defaults, not an empty box the admin fills in
    // to get what the apply would have filled in anyway (spec §5[4]), and the rails on it come
    // from the same answer rather than from a list the UI keeps for itself.
    const user = userEvent.setup();
    render(<SetupPage />);
    await user.click(await screen.findByRole("button", { name: "New install" }));

    fireEvent.change(inputByPlaceholder("e.g., apps"), { target: { value: "apps" } });
    fireEvent.change(document.querySelector("select") as HTMLSelectElement, {
      target: { value: "g-1" },
    });
    await user.click(screen.getByRole("button", { name: "Continue" }));
    fireEvent.change(inputByPlaceholder("Personal access token with write:admin scope"), {
      target: { value: "admin-token-never-store-me" },
    });
    await user.click(screen.getByRole("button", { name: "Continue" }));

    await screen.findByText("Configure the agents");
    // The cards are in role order, so the refiner's is first. Its chips are read by name, and a
    // chip's text is its state mark and then the tool name.
    const refinerCard = document.querySelector(".forgejo-role-card") as HTMLElement;
    const chips = Array.from(refinerCard.querySelectorAll<HTMLButtonElement>("button.forgejo-tool-item"));
    const byTool = new Map(chips.map((c) => [(c.textContent ?? "").replace(/^[✓+⊘]/, "").trim(), c]));

    for (const tool of ["create_issue_comment", "list_issues"]) {
      expect(byTool.get(tool), `${tool} is offered`).toBeTruthy();
      expect(byTool.get(tool)!.getAttribute("aria-pressed"), `${tool} starts granted`).toBe("true");
    }
    // The refiner's rail is create_branch: not something this role may be given, so it is not offered.
    expect(byTool.has("create_branch"), "a forbidden tool is not addable").toBe(false);
  });

  it("removes an install through the body, and says what the removal left behind", async () => {
    // Two things the old call got wrong: it posted to a route registered as DELETE — so
    // removing an install answered 404 and nothing was removed — and the only place a
    // removal token can go is the body, since a token in a URL is a token in an access log.
    const user = userEvent.setup();
    get.mockImplementation(async (path: string) => {
      if (path === "/setup/options") return optionsFixture;
      if (path === "/setup/installs") return { installs: [applyFixture.install] };
      throw new Error(`unexpected GET ${path}`);
    });
    post.mockImplementation(async (path: string) => {
      if (path === "/setup/installs/apps/remove") {
        return {
          deleted: ["agent:forgejo-refiner-apps"],
          checklist: ["team:ainsel-refiner-apps", "hook:7"],
          kept: true,
        };
      }
      throw new Error(`unexpected POST ${path}`);
    });

    render(<SetupPage />);
    await user.click(await screen.findByRole("button", { name: "Remove" }));
    fireEvent.change(inputByPlaceholder("Admin token (optional)"), {
      target: { value: "ADMINTOKEN-xyz" },
    });
    await user.click(document.querySelector(".forgejo-confirm-remove-btn") as HTMLButtonElement);

    await waitFor(() => {
      expect(post).toHaveBeenCalledWith("/setup/installs/apps/remove", {
        adminToken: "ADMINTOKEN-xyz",
      });
    });

    // The admin is told what the removal could not remove, and that the install is still
    // there to try again — not that it succeeded.
    // The removal's message is found by what it says.
    const notice = await screen.findByText(/still listed/);
    expect(notice.textContent).toContain("team:ainsel-refiner-apps");
    expect(notice.textContent).toContain("hook:7");
  });

  it("opens the webhook watch for an install that has never delivered", async () => {
    // S0 offers Connect on an unverified install. It used to set the step and nothing else,
    // and S7 read the install off the apply answer — which there is none of on that path —
    // so the click landed on "Unknown step: connect".
    const user = userEvent.setup();
    get.mockImplementation(async (path: string) => {
      if (path === "/setup/options") return optionsFixture;
      if (path === "/setup/installs") {
        return { installs: [{ ...applyFixture.install, hookId: 42 }] };
      }
      if (path.endsWith("/deliveries")) return { verified: false, at: null };
      throw new Error(`unexpected GET ${path}`);
    });

    render(<SetupPage />);
    await user.click(await screen.findByRole("button", { name: "Connect" }));

    expect(await screen.findByText(/Waiting for the first event/)).toBeTruthy();
    await waitFor(() => {
      expect(get).toHaveBeenCalledWith(
        "/setup/installs/apps/deliveries",
        expect.objectContaining({ query: { since: expect.any(String) } }),
      );
    });
    // The hook it tells the admin to go and check is this install's, not some other row's.
    expect(screen.getByRole("link").getAttribute("href")).toContain("hooks/42");
  });

  it("shows the 502 apply error and the names it rolled back", async () => {
    const user = userEvent.setup();
    post.mockImplementation(async (path: string) => {
      if (path === "/setup/plan") return planFixture;
      if (path === "/setup/apply") {
        // The shape the console's request() throws for a 502 whose body is
        // { error, report } (frontend/src/api/client.ts).
        const err: any = new Error("CreateUser: user already exists");
        err.status = 502;
        err.body = {
          error: "CreateUser: user already exists",
          report: {
            created: [],
            rolledBack: [{ kind: "user", name: "refiner-apps", detail: "" }],
            leftovers: [],
          },
        };
        throw err;
      }
      throw new Error(`unexpected POST ${path}`);
    });

    await walkToPreview(user);
    await user.click(screen.getByRole("button", { name: /^Apply/ }));

    const notice = await screen.findByRole("alert");
    expect(notice.textContent).toContain("CreateUser: user already exists");
    expect(notice.textContent).toContain("refiner-apps");
  });

  it("a double click applies once", async () => {
    // Apply is not idempotent from the UI's point of view: the second of two clicks would
    // ask the hub to create the same install again. The request stays pending here, and the
    // screen must refuse the second click — even one dispatched by hand, so a disabled
    // attribute alone is not what is being tested.
    const user = userEvent.setup();
    let resolveApply: (value: ApplyResponse) => void = () => {};
    post.mockImplementation(async (path: string) => {
      if (path === "/setup/plan") return planFixture;
      if (path === "/setup/apply") {
        return new Promise<ApplyResponse>((resolve) => {
          resolveApply = resolve;
        });
      }
      throw new Error(`unexpected POST ${path}`);
    });

    await walkToPreview(user);
    const applyBtn = screen.getByRole("button", { name: /^Apply/ }) as HTMLButtonElement;
    await user.click(applyBtn);

    fireEvent.click(applyBtn);
    fireEvent.click(applyBtn);

    expect(
      post.mock.calls.filter((call) => call[0] === "/setup/apply"),
    ).toHaveLength(1);

    // The walk continues on the single request that is in flight.
    resolveApply(applyFixture);
    await screen.findByText("Verify the connection");
    expect(post.mock.calls.filter((call) => call[0] === "/setup/apply")).toHaveLength(1);
  });

  it("persists nothing to storage on a successful apply", async () => {
    const user = userEvent.setup();
    post.mockImplementation(async (path: string) => {
      if (path === "/setup/plan") return planFixture;
      if (path === "/setup/apply") return applyFixture;
      throw new Error(`unexpected POST ${path}`);
    });
    const setItem = vi.spyOn(Storage.prototype, "setItem");

    await walkToPreview(user);
    await user.click(screen.getByRole("button", { name: /^Apply/ }));

    await screen.findByText("Verify the connection");
    expect(setItem).not.toHaveBeenCalled();
  });

  it("after apply, secrets are cleared and Back leaves the walk", async () => {
    // An apply that worked made an install, so the walk is over. Back on S7 returns to the
    // list that now holds a row for it, and the preview where a second Apply would sit is no
    // longer on any way back — a repeat apply would only fail on resources that exist now.
    // The secrets the walk carried went with its requests and are dropped from its state, so
    // the next plan the wizard asks for carries neither the admin token nor a role key.
    const user = userEvent.setup();
    get.mockImplementation(async (path: string) => {
      if (path === "/setup/options") return optionsFixture;
      if (path === "/setup/installs") return { installs: [applyFixture.install] };
      if (path.endsWith("/deliveries")) return { verified: false, at: null };
      throw new Error(`unexpected GET ${path}`);
    });
    post.mockImplementation(async (path: string) => {
      if (path === "/setup/plan") return planFixture;
      if (path === "/setup/apply") return applyFixture;
      throw new Error(`unexpected POST ${path}`);
    });

    await walkToPreview(user);
    await user.click(screen.getByRole("button", { name: /^Apply/ }));
    await screen.findByText("Verify the connection");

    // plan and apply: the two requests allowed to carry the token.
    const asked = post.mock.calls.length;

    await user.click(screen.getByRole("button", { name: "Back" }));

    // The start screen, with the row the apply made on it — and no Apply to click again.
    await screen.findByRole("button", { name: "New install" });
    expect(screen.queryByRole("button", { name: /^Apply/ })).toBeNull();
    expect(post.mock.calls.slice(asked)).toEqual([]);

    // The next walk out of this state asks for a plan with no secret in it.
    await user.click(screen.getByRole("button", { name: "Edit" }));
    for (let step = 0; step < 4; step++) {
      await user.click(screen.getByRole("button", { name: "Continue" }));
    }
    const plans = post.mock.calls.filter((call) => call[0] === "/setup/plan");
    const last = plans[plans.length - 1][1] as any;
    expect(last.adminToken).toBe("");
  });

  it("an edit after a failed apply starts clean", async () => {
    // What a failed apply leaves behind belongs to that apply. Left set, it renders on the next
    // walk's preview too: a rolled-back list and an error for work the admin has not asked for
    // yet, sitting above an Apply that looks like the answer to a failure already read.
    const user = userEvent.setup();
    get.mockImplementation(async (path: string) => {
      if (path === "/setup/options") return optionsFixture;
      if (path === "/setup/installs") return { installs: [applyFixture.install] };
      if (path.endsWith("/deliveries")) return { verified: false, at: null };
      throw new Error(`unexpected GET ${path}`);
    });
    post.mockImplementation(async (path: string) => {
      if (path === "/setup/plan") return planFixture;
      if (path === "/setup/apply") {
        const err: any = new Error("CreateUser: user already exists");
        err.status = 502;
        err.body = {
          error: "CreateUser: user already exists",
          report: {
            created: [],
            rolledBack: [{ kind: "user", name: "refiner-apps", detail: "" }],
            leftovers: [],
          },
        };
        throw err;
      }
      throw new Error(`unexpected POST ${path}`);
    });

    await walkToPreview(user);
    await user.click(screen.getByRole("button", { name: /^Apply/ }));
    await screen.findByText(/CreateUser: user already exists/);

    // Out of the failed walk, and into an edit of the install the list holds.
    for (let step = 0; step < 5; step++) {
      await user.click(screen.getByRole("button", { name: "Back" }));
    }
    await user.click(await screen.findByRole("button", { name: "Edit" }));
    for (let step = 0; step < 4; step++) {
      await user.click(screen.getByRole("button", { name: "Continue" }));
    }

    await screen.findByRole("button", { name: /^Apply/ });
    expect(screen.queryByText(/CreateUser: user already exists/)).toBeNull();
    expect(screen.queryByText("Rolled back:")).toBeNull();
  });

  it("a failed plan shows the error and keeps the step", async () => {
    // A plan that cannot answer is the step's own news, not the wizard's: the admin has to be
    // able to read it and go again. It used to fill the whole screen with a Notice that had no
    // way out of it — the walk was over until the page was reloaded.
    const user = userEvent.setup();
    let planCalls = 0;
    post.mockImplementation(async (path: string) => {
      if (path === "/setup/plan") {
        planCalls++;
        throw new Error("forge MCP is not reachable");
      }
      throw new Error(`unexpected POST ${path}`);
    });

    await walkToRoles(user);
    await pickRuntimes(user);
    await user.click(screen.getByRole("button", { name: "Continue" })); // S4 → S5
    await user.click(screen.getByRole("button", { name: "Continue" })); // S5 asks for the plan

    const notice = await screen.findByRole("alert");
    expect(notice.getAttribute("data-variant")).toBe("err");
    expect(notice.textContent).toContain("forge MCP is not reachable");

    // Still on the step: its own Next is there, and asking again asks the plan again.
    const next = screen.getByRole("button", { name: "Continue" });
    await user.click(next);
    await waitFor(() => expect(planCalls).toBe(2));
  });

  it("a failed remove says so on the list and leaves the list usable", async () => {
    // Same rule one screen earlier: a removal that could not ask is the list's news. It used
    // to take the whole screen with it, so the admin could not even start a new install.
    const user = userEvent.setup();
    get.mockImplementation(async (path: string) => {
      if (path === "/setup/options") return optionsFixture;
      if (path === "/setup/installs") return { installs: [applyFixture.install] };
      throw new Error(`unexpected GET ${path}`);
    });
    post.mockImplementation(async (path: string) => {
      if (path === "/setup/installs/apps/remove") throw new Error("the hub is not answering");
      throw new Error(`unexpected POST ${path}`);
    });

    render(<SetupPage />);
    await user.click(await screen.findByRole("button", { name: "Remove" }));
    await user.click(document.querySelector(".forgejo-confirm-remove-btn") as HTMLButtonElement);

    const notice = await screen.findByText(/the hub is not answering/);
    expect(notice.closest('[data-variant="err"]')).toBeTruthy();

    // The list is still the list: a new install can be started from where the admin stands.
    expect(screen.getByRole("button", { name: "New install" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Edit" })).toBeTruthy();
  });

  it("lets the admin go back through a finished step, and not forward past the cursor", async () => {
    // The step list is a way back, never a way forward: a step the walk has not reached has no
    // answers to show, so it cannot be opened from the list.
    const user = userEvent.setup();
    render(<SetupPage />);
    await user.click(await screen.findByRole("button", { name: "New install" }));
    fireEvent.change(inputByPlaceholder("e.g., apps"), { target: { value: "apps" } });
    fireEvent.change(document.querySelector("select") as HTMLSelectElement, {
      target: { value: "g-1" },
    });
    fireEvent.change(inputByPlaceholder("e.g., example-org"), { target: { value: "apps" } });
    await user.click(screen.getByRole("button", { name: "Continue" }));
    fireEvent.change(inputByPlaceholder("Personal access token with write:admin scope"), {
      target: { value: "admin-token-never-store-me" },
    });
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await screen.findByText("Configure the agents");

    // On Agents: the steps behind it are open, the steps ahead of it are not.
    expect(stepButton("Instance").disabled).toBe(false);
    expect(stepButton("Bot accounts").disabled).toBe(false);
    expect(stepButton("Agents").getAttribute("aria-current")).toBe("step");
    expect(stepButton("Rules").disabled).toBe(true);
    expect(stepButton("Review plan").disabled).toBe(true);
    expect(stepButton("Connect").disabled).toBe(true);

    // Forward is blocked: the disabled step does not move the walk.
    await user.click(stepButton("Rules"));
    expect(screen.getByText("Configure the agents")).toBeTruthy();

    // Back is allowed, and what was typed on the way is still on the screen it returns to.
    await user.click(stepButton("Instance"));
    expect(screen.getByText("Where Forgejo is")).toBeTruthy();
    expect((inputByPlaceholder("e.g., apps") as HTMLInputElement).value).toBe("apps");
    expect(stepButton("Instance").getAttribute("aria-current")).toBe("step");
    expect(stepButton("Agents").disabled).toBe(true);
  });

  it("after an apply the list shows the steps as done, but none of them can be reopened", async () => {
    // Reopening the plan after an apply would offer a second Apply on resources that exist now.
    const user = userEvent.setup();
    post.mockImplementation(async (path: string) => {
      if (path === "/setup/plan") return planFixture;
      if (path === "/setup/apply") return applyFixture;
      throw new Error(`unexpected POST ${path}`);
    });
    get.mockImplementation(async (path: string) => {
      if (path === "/setup/options") return optionsFixture;
      if (path === "/setup/installs") return { installs: [] };
      if (path.endsWith("/deliveries")) return { verified: false, at: null };
      throw new Error(`unexpected GET ${path}`);
    });

    await walkToPreview(user);
    await user.click(screen.getByRole("button", { name: /^Apply/ }));
    await screen.findByText("Verify the connection");

    expect(stepButton("Review plan").disabled).toBe(true);
    expect(stepButton("Instance").disabled).toBe(true);
    expect(stepButton("Connect").getAttribute("aria-current")).toBe("step");
  });

  it("names the walk for the install it is connecting, when the connect came from the list", async () => {
    // A Connect from the installs list did none of the steps: its panel says which install it is for,
    // and no step reads as done.
    const user = userEvent.setup();
    get.mockImplementation(async (path: string) => {
      if (path === "/setup/options") return optionsFixture;
      if (path === "/setup/installs") {
        return { installs: [{ ...applyFixture.install, hookId: 42 }] };
      }
      if (path.endsWith("/deliveries")) return { verified: false, at: null };
      throw new Error(`unexpected GET ${path}`);
    });

    render(<SetupPage />);
    await user.click(await screen.findByRole("button", { name: "Connect" }));

    await screen.findByText("Waiting for the first event…");
    expect(screen.getByText("Connect apps")).toBeTruthy();
    expect(stepButton("Instance").disabled).toBe(true);
    expect(stepButton("Connect").getAttribute("aria-current")).toBe("step");
  });

  it("continue after an apply shows the done screen, and close returns to the list with a success notice", async () => {
    // The walk has to end somewhere that says so. Continue on S7 used to land straight on the
    // installs list, whose only word about the fresh install was "Not verified" — a successful
    // setup reading like a failure. Now it lands on the done screen, and closing that returns
    // to the list with the install named as installed.
    const user = userEvent.setup();
    get.mockImplementation(async (path: string) => {
      if (path === "/setup/options") return optionsFixture;
      if (path === "/setup/installs") return { installs: [applyFixture.install] };
      if (path.endsWith("/deliveries")) return { verified: false, at: null };
      throw new Error(`unexpected GET ${path}`);
    });
    post.mockImplementation(async (path: string) => {
      if (path === "/setup/plan") return planFixture;
      if (path === "/setup/apply") return applyFixture;
      throw new Error(`unexpected POST ${path}`);
    });

    await walkToPreview(user);
    await user.click(screen.getByRole("button", { name: /^Apply/ }));
    await screen.findByText("Verify the connection");

    await user.click(screen.getByRole("button", { name: "Finish without verifying" }));

    // The done screen: installed, and what the apply made.
    await screen.findByText("✓ Installed");
    expect(
      screen.getByText(/"apps" is set up for apps on https:\/\/forgejo\.example\.com/),
    ).toBeTruthy();
    // The created list is a table: the kind, then its count in the cell beside it.
    expect(screen.getByText("Connector", { selector: "td" }).nextElementSibling?.textContent).toBe("1");

    await user.click(screen.getByRole("button", { name: "Close" }));

    // Back on the list, with the install named as installed — an info, not a warning.
    await screen.findByRole("button", { name: "New install" });
    const notice = await screen.findByText(/"apps" was installed\./);
    expect(notice.closest('[data-variant="info"]')).toBeTruthy();
  });
});

// The edit walk's working hours: the row's window is what the form starts with, and what the form
// still holds is what goes back to the plan. Both halves matter — the plan diffs the form against
// the row, so a prefilled window that comes back unchanged is a plan with no op for the field, and
// a window read off the live agent instead would make the next apply overwrite the console's.
describe("the edit walk's working hours", () => {
  const hoursWindow = { timezone: "Europe/Berlin", days: [1, 2, 3, 4, 5], start: "09:00", end: "17:00" };

  // The installed install, with the developer's row recording the window the wizard wrote and the
  // other two rows recording none.
  function installWithHours() {
    return {
      ...applyFixture.install,
      roles: {
        ...applyFixture.install.roles,
        developer: { ...applyFixture.install.roles.developer, workingHours: hoursWindow },
      },
    };
  }

  async function openEdit(user: ReturnType<typeof userEvent.setup>) {
    get.mockImplementation(async (path: string) => {
      if (path === "/setup/options") return optionsFixture;
      if (path === "/setup/installs") return { installs: [installWithHours()] };
      if (path.endsWith("/deliveries")) return { verified: false, at: null };
      throw new Error(`unexpected GET ${path}`);
    });
    post.mockImplementation(async (path: string) => {
      if (path === "/setup/plan") return planFixture;
      throw new Error(`unexpected POST ${path}`);
    });

    render(<SetupPage />);
    await user.click(await screen.findByRole("button", { name: "Edit" }));
  }

  it("prefills the window the row records, and only that one", async () => {
    const user = userEvent.setup();
    await openEdit(user);
    // instance → accounts → roles.
    await user.click(screen.getByRole("button", { name: "Continue" }));
    await user.click(screen.getByRole("button", { name: "Continue" }));

    const card = (role: string) =>
      Array.from(document.querySelectorAll<HTMLElement>(".forgejo-role-card")).find((c) =>
        c.textContent?.startsWith(role),
      )!;
    expect(card("Developer").querySelector<HTMLInputElement>(".forgejo-hours-on")!.checked).toBe(true);
    expect(card("Developer").querySelector(".forgejo-hours-summary")!.textContent).toBe(
      "Mon–Fri 09:00–17:00 Europe/Berlin",
    );
    expect(card("Refiner").querySelector<HTMLInputElement>(".forgejo-hours-on")!.checked).toBe(false);
  });

  it("sends the row's window back to the plan when the admin changes nothing", async () => {
    // The plan is what decides, and it decides from two values: the row's and the form's. So the
    // form has to hand back the row's window verbatim — anything less and an untouched edit would
    // read as the admin clearing a window they never looked at.
    const user = userEvent.setup();
    await openEdit(user);
    for (let step = 0; step < 4; step++) {
      await user.click(screen.getByRole("button", { name: "Continue" }));
    }
    await screen.findByRole("button", { name: /^Apply/ });

    const plans = post.mock.calls.filter((call) => call[0] === "/setup/plan");
    const body = plans[plans.length - 1][1] as any;
    expect(body.roles.developer.workingHours).toEqual(hoursWindow);
    expect(body.roles.refiner.workingHours).toBeUndefined();
  });
});
