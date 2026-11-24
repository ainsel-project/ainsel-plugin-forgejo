// The end of the walk. These tests hold the screen to what the admin needs from it: that the
// install is done, what it made, and whether the webhook has seen an event yet — without the
// screen reading like a failure when nothing failed.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { Done } from "./Done";

const install = {
  slug: "apps",
  baseUrl: "https://f.example",
  org: "apps",
};

// A report with the counts the test names: 3 users, 3 teams, 3 agents, 3 images, 3 personas,
// 6 triggers, 1 patrol cron trigger, 1 MCP server, 1 hook.
const created = () => [
  ...["a", "b", "c"].map((n) => ({ kind: "user", name: `refiner-${n}` })),
  ...["a", "b", "c"].map((n) => ({ kind: "team", name: `team-${n}` })),
  ...["a", "b", "c"].map((n) => ({ kind: "agent", name: `agent-${n}` })),
  ...["a", "b", "c"].map((n) => ({ kind: "image", name: `image-${n}` })),
  ...["a", "b", "c"].map((n) => ({ kind: "persona", name: `persona-${n}` })),
  ...["1", "2", "3", "4", "5", "6"].map((n) => ({ kind: "trigger", name: `trigger-${n}` })),
  { kind: "cronTrigger", name: "forgejo-developer-pr-patrol-apps" },
  { kind: "mcpServer", name: "forgejo-mcp-apps" },
  { kind: "hook", name: "7" },
];

// The count the created table gives a kind: the cell after the one that names it.
function countOf(kind: string): string | undefined {
  const name = screen.getByText(kind, { selector: "td" });
  return name.nextElementSibling?.textContent ?? undefined;
}

describe("Done (S8)", () => {
  afterEach(() => {
    // Without this every test leaves its screen mounted, and a later getByText matches the
    // stale one beside the live one.
    cleanup();
  });

  it("the done screen says it is installed and lists what was created", () => {
    render(<Done install={install} report={{ created: created() }} verified={true} onClose={vi.fn()} />);

    expect(screen.getByText("✓ Installed")).toBeTruthy();
    expect(screen.getByText(/"apps" is set up for apps on https:\/\/f\.example/)).toBeTruthy();
    // The created list is a table: each kind is a row, its count the cell beside the name.
    expect(countOf("Bot accounts")).toBe("3");
    expect(countOf("Triggers")).toBe("6");
    expect(countOf("PR patrol")).toBe("1");
    expect(countOf("Webhook")).toBe("1");
  });

  it("an unverified webhook is not a warning", () => {
    // The install is complete either way; a first event simply has not arrived yet. A warn
    // here made a successful setup read like a failure.
    const { container } = render(
      <Done install={install} report={{ created: created() }} verified={false} onClose={vi.fn()} />,
    );

    expect(container.querySelector('[data-variant="warn"]')).toBeNull();
    const notice = screen.getByText(/marked verified when the first event arrives/);
    expect(notice.closest('[data-variant="info"]')).toBeTruthy();
    expect(notice.textContent).toContain("any repository of apps");
  });

  it("a verified webhook shows the tag", () => {
    render(<Done install={install} report={{ created: created() }} verified={true} onClose={vi.fn()} />);

    const tag = screen.getByText("✓ Webhook verified");
    expect(tag.getAttribute("data-variant")).toBe("ok");
  });

  it("the org link is the org page", () => {
    // Forgejo has no org-level new-issue page; a link to one is a 404 the admin meets on the
    // screen that is supposed to send them off to try the install.
    render(<Done install={install} report={{ created: created() }} verified={false} onClose={vi.fn()} />);

    const link = screen.getByRole("link", { name: /Open apps in Forgejo/ });
    expect(link.getAttribute("href")).toBe("https://f.example/apps");
  });
});
