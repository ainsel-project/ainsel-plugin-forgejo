/** @vitest-environment jsdom */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { ForgejoAgentPanel } from "./ForgejoAgentPanel";
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

describe("ForgejoAgentPanel", () => {
  it("renders with agentName prop", async () => {
    get.mockResolvedValue({ items: [] });

    render(<ForgejoAgentPanel agentName="test-agent" />);

    const panel = await screen.findByTestId("panel");
    expect(panel).toBeDefined();
  });
});
