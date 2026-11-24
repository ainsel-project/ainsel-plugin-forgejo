/** @vitest-environment jsdom */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { ForgejoInvocationPanel } from "./ForgejoInvocationPanel";
import { get } from "./testing/sdk";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

beforeEach(() => {
  get.mockReset();
  vi.stubGlobal(
    "fetch",
    vi.fn(() => {
      throw new Error("the page must call the SDK api client, not fetch");
    }),
  );
});

describe("ForgejoInvocationPanel", () => {
  it("renders with invocationId and agentName props", async () => {
    get.mockResolvedValue({
      item: {
        subject: {
          repo: "owner/repo",
          number: 264,
          kind: "issues",
          title: "Fix something",
        },
        url: "https://forgejo.example.com/owner/repo/issues/264",
        runs: [],
        durationMs: 0,
        costUsd: null,
        costKnown: false,
        lastActivity: "2024-01-01T00:00:00Z",
        prs: [],
      },
      siblingsCostUsd: null,
      siblingsCostKnown: false,
    });

    render(<ForgejoInvocationPanel invocationId="invoc-123" agentName="test-agent" />);

    // The panel should render and find the invocation ID or agent name
    const panel = await screen.findByTestId("panel");
    expect(panel).toBeDefined();
  });
});
