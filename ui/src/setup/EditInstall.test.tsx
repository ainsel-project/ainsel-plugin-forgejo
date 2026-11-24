// The edit walk: S0 "Edit" opens the same flow as a new install, but every screen
// must show what the install already has, and no secret may come back from the
// hub (spec §4 "edit install ─ same flow, prefilled", §6: keys are never prefilled,
// the admin token stays empty until the account work needs it).
//
// Under the created-only wizard the edit walk prefills the per-install facts only:
// slug, group, base URL, org, exclude label and the roles' tool sets. The runtime
// container starts empty because the install row keeps the image id, not the
// container — and empty means "keep what the install has". The LLM fields are gone
// from this flow entirely: they are role settings on the Settings page now.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SetupPage } from "./SetupPage";
import * as sdk from "../testing/sdk";
import type { PlanResponse, SetupOptions } from "../api";

const ROLE_ORDER = ["refiner", "developer", "reviewer"] as const;

const optionsFixture: SetupOptions = {
  groups: [{ id: "g-1", name: "apps", role: "owner" }],
  pluginGroups: ["g-1"],
  forgejoBaseURL: "https://forgejo.example.com",
  // The install's own address is a configured instance, so the edit step shows it
  // chosen rather than flagging it as outside the list.
  instances: [{ name: "Primary", baseUrl: "https://git.example.com" }],
  tools: {
    defaults: { refiner: ["list_issues"], developer: ["create_branch"], reviewer: ["create_pull_review"] },
    forbidden: { refiner: ["create_branch"], developer: [], reviewer: ["bash"] },
  },
};

const installFixture = {
  slug: "apps",
  groupId: "g-1",
  baseUrl: "https://git.example.com",
  org: "example-org",
  connectorId: "c-1",
  mcpServer: "forgejo-mcp-apps",
  hookId: 1,
  excludeLabel: "hold-for-human",
  roles: {
    refiner: { agentId: "a-ref", imageId: "img-ref", personaId: "p-ref", botLogin: "refiner-apps", teamId: 1, tools: ["get_issue_by_index"], turnTimeoutMs: "7200000" },
    developer: { agentId: "a-dev", imageId: "img-dev", personaId: "p-dev", botLogin: "developer-apps", teamId: 2, tools: ["create_file", "bash"] },
    reviewer: { agentId: "a-rev", imageId: "img-rev", personaId: "p-rev", botLogin: "reviewer-apps", teamId: 3, tools: ["submit_pull_review"] }, // no turnTimeoutMs: a row from before the option
  },
};

// The plan the edit walk walks into on its way to the preview: the preview only
// needs a shape to render, and the assertion below is on what the plan was asked
// for, not on what it answered.
const planFixture: PlanResponse = {
  create: [],
  change: [],
  delete: [],
  triggers: [],
  verdict: { blocking: [], warnings: [] },
};

function mockSetupReads() {
  sdk.get.mockImplementation(async (path: string) => {
    if (path === "/setup/options") return optionsFixture;
    if (path === "/setup/installs") return { installs: [installFixture] };
    throw new Error(`unexpected GET ${path}`);
  });
}

// The step's own heading, the one the wizard frame draws under the panel's title.
function panelTitle(container: HTMLElement): string {
  return container.querySelector("h4")?.textContent ?? "";
}

// Click "Edit" on the one install, then click through the wizard until the screen
// named `title` is showing. Fails loudly rather than skipping a screen the walk
// never reached — an assertion behind `if (reached) {}` proves nothing.
async function editAndWalkTo(
  user: ReturnType<typeof userEvent.setup>,
  title: string,
): Promise<HTMLElement> {
  const { container } = render(<SetupPage />);
  await waitFor(() => {
    expect(container.querySelector(".forgejo-edit-btn")).toBeTruthy();
  });
  await user.click(container.querySelector(".forgejo-edit-btn") as HTMLButtonElement);

  for (let step = 0; step < 6; step++) {
    if (panelTitle(container) === title) return container;
    const next = container.querySelector(".forgejo-next-btn") as HTMLButtonElement | null;
    if (!next) break;
    await user.click(next);
  }
  throw new Error(`never reached "${title}"; the walk stopped at "${panelTitle(container)}"`);
}

// S4 draws the three roles as cards on one screen, each card named by its role. Reading the
// roles is reading each card in turn.
const ROLE_LABEL = { refiner: "Refiner", developer: "Developer", reviewer: "Reviewer" };

async function eachRolePanel(
  _user: ReturnType<typeof userEvent.setup>,
  container: HTMLElement,
  read: (panel: Element, role: string) => void,
): Promise<void> {
  for (const role of ROLE_ORDER) {
    const card = Array.from(container.querySelectorAll(".forgejo-role-card")).find((c) =>
      c.textContent?.startsWith(ROLE_LABEL[role]),
    );
    expect(card, `the ${role} card`).toBeTruthy();
    read(card!, role);
  }
}

function selectsIn(el: Element): HTMLSelectElement[] {
  return Array.from(el.querySelectorAll('[data-testid="select"]')) as HTMLSelectElement[];
}

beforeEach(() => {
  vi.clearAllMocks();
  mockSetupReads();
});

afterEach(() => {
  cleanup();
});

describe("Edit Install (the walk from S0)", () => {
  it("prefills S1 from the install", async () => {
    const user = userEvent.setup();
    const container = await editAndWalkTo(user, "Where Forgejo is");

    const slug = container.querySelector('input[placeholder="e.g., apps"]') as HTMLInputElement;
    const baseUrl = selectsIn(container)[1] as HTMLSelectElement;
    const org = container.querySelector(
      'input[placeholder="e.g., example-org"]',
    ) as HTMLInputElement;

    expect(slug.value).toBe("apps");
    expect(baseUrl.value).toBe("https://git.example.com");
    expect(org.value).toBe("example-org");
    expect((selectsIn(container)[0] as HTMLSelectElement).value).toBe("g-1");
  });

  it("keeps the admin token empty on S3 and offers to keep it", async () => {
    const user = userEvent.setup();
    const container = await editAndWalkTo(user, "Create bot accounts");

    const token = container.querySelector('[type="password"]') as HTMLInputElement;
    expect(token.value).toBe("");
    expect(token.placeholder).toBe("leave empty to keep");
    // And the walk can go on without one: an edit changes no accounts by default.
    expect(container.querySelector(".forgejo-next-btn")).toBeTruthy();
  });

  it("starts the roles step with empty runtimes that mean keep, not pick", async () => {
    const user = userEvent.setup();
    const container = await editAndWalkTo(user, "Configure the agents");

    await eachRolePanel(user, container, (panel, role) => {
      // The install row keeps the image id, not the container URL — there is nothing
      // honest to prefill, and an empty runtime on an edit means the plan keeps the
      // install's prior container. The screen says so.
      const runtime = panel.querySelector(`#roles-${role}-runtime`) as HTMLInputElement;
      expect(runtime.value, `${role} runtime starts empty`).toBe("");
      expect(panel.textContent).toContain("keep");
    });
  });

  it("prefills the tool selection from the install", async () => {
    const user = userEvent.setup();
    const container = await editAndWalkTo(user, "Configure the agents");

    const expectedTool: Record<string, string> = {
      refiner: "get_issue_by_index",
      developer: "bash",
      reviewer: "submit_pull_review",
    };

    await eachRolePanel(user, container, (panel, role) => {
      const checked = Array.from(
        panel.querySelectorAll('button.forgejo-tool-item[aria-pressed="true"]'),
      );
      // A chip's text is its state mark, then the tool name.
      const labels = checked.map((b) => (b.textContent ?? "").replace(/^[✓+⊘]/, "").trim());
      expect(labels, `${role} keeps its selection`).toContain(expectedTool[role]);
    });
  });

  it("carries no provider or model row on the roles step, and one optional key", async () => {
    // The wizard asked for a provider and a model per role until the settings move:
    // every agent is created from the settings store. The key is the one secret the
    // walk carries again — it is the only place a key can be given before the agents
    // exist, since the settings page refuses a key for a role nobody installed.
    const user = userEvent.setup();
    const container = await editAndWalkTo(user, "Configure the agents");

    await eachRolePanel(user, container, (panel, role) => {
      expect(panel.querySelectorAll('[type="password"]'), `${role} carries one key row`).toHaveLength(1);
      expect(panel.textContent).not.toContain("LLM Provider");
      expect(panel.textContent).not.toContain("LLM Model");
    });
  });

  it("prefills the turn timeout from the row, and keeps it empty when the row has none", async () => {
    // Issue #61: the field shows what the image was last written with; a row from
    // before the option existed starts empty, which means keep, never reset.
    const user = userEvent.setup();
    const container = await editAndWalkTo(user, "Configure the agents");

    await eachRolePanel(user, container, (panel, role) => {
      const field = panel.querySelector(`#roles-${role}-turn-timeout`) as HTMLInputElement;
      if (role === "refiner") {
        expect(field.value, "refiner shows the recorded 2 h").toBe("120");
      } else {
        expect(field.value, `${role} row predates the option: empty means keep`).toBe("");
      }
    });
  });

  it("submits a blank baseUrl when the edit keeps an unlisted address", async () => {
    // The step's "Keep <url>" state is the edit form's only way to leave an address
    // that is not on the configured list alone, and the server keeps the row's own
    // address on a blank baseUrl while it refuses a non-empty unlisted one. So the
    // keep state has to go out blank: submit the stored address instead and the walk
    // is refused at the plan step — the notice would promise an edit that cannot be
    // finished (docs/setup.md, "Editing an install").
    const user = userEvent.setup();
    sdk.get.mockImplementation(async (path: string) => {
      if (path === "/setup/options") {
        return {
          ...optionsFixture,
          instances: [{ name: "Primary", baseUrl: "https://listed.example.com" }],
        };
      }
      if (path === "/setup/installs") {
        return { installs: [{ ...installFixture, baseUrl: "https://legacy.example.com" }] };
      }
      throw new Error(`unexpected GET ${path}`);
    });
    sdk.post.mockImplementation(async (path: string) => {
      if (path === "/setup/plan") return planFixture;
      throw new Error(`unexpected POST ${path}`);
    });

    const container = await editAndWalkTo(user, "Review the plan");
    expect(panelTitle(container)).toBe("Review the plan");

    const planCall = sdk.post.mock.calls.find((c) => c[0] === "/setup/plan");
    expect(planCall, "the walk planned").toBeTruthy();
    expect((planCall![1] as { baseUrl: string }).baseUrl).toBe("");
  });

  it("submits the chosen address when the edit moves the install to a listed instance", async () => {
    // The other side of the same submit: a pick is a move the admin chose, and it
    // goes out as the address it names — blanking it here would silently keep the
    // old host instead.
    const user = userEvent.setup();
    sdk.get.mockImplementation(async (path: string) => {
      if (path === "/setup/options") {
        return {
          ...optionsFixture,
          instances: [
            { name: "Primary", baseUrl: "https://listed.example.com" },
            { name: "Second", baseUrl: "https://second.example.com" },
          ],
        };
      }
      if (path === "/setup/installs") {
        return { installs: [{ ...installFixture, baseUrl: "https://legacy.example.com" }] };
      }
      throw new Error(`unexpected GET ${path}`);
    });
    sdk.post.mockImplementation(async (path: string) => {
      if (path === "/setup/plan") return planFixture;
      throw new Error(`unexpected POST ${path}`);
    });

    const container = await editAndWalkTo(user, "Where Forgejo is");
    const baseUrlSelect = selectsIn(container)[1] as HTMLSelectElement;
    await user.selectOptions(baseUrlSelect, "https://second.example.com");

    for (let step = 0; step < 6; step++) {
      if (panelTitle(container) === "Review the plan") break;
      const next = container.querySelector(".forgejo-next-btn") as HTMLButtonElement | null;
      if (!next) break;
      await user.click(next);
    }

    const planCall = sdk.post.mock.calls.find((c) => c[0] === "/setup/plan");
    expect(planCall, "the walk planned").toBeTruthy();
    expect((planCall![1] as { baseUrl: string }).baseUrl).toBe("https://second.example.com");
  });

  it("prefills S5 with the install's exclude label", async () => {
    const user = userEvent.setup();
    const container = await editAndWalkTo(user, "Routing rules");

    const label = container.querySelector(
      'input[placeholder="do-not-refine"]',
    ) as HTMLInputElement;
    // "hold-for-human", not the wizard's default: the screen shows the install.
    expect(label.value).toBe("hold-for-human");
  });
});
