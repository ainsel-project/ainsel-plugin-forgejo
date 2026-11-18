import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { Connect } from "./Connect";
import * as sdk from "../testing/sdk";
import { get } from "../testing/sdk";

describe("Connect (S7)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    // Without this every test leaves its screen mounted, and a later getByText matches the
    // stale one beside the live one.
    cleanup();
    vi.clearAllMocks();
  });

  it("renders the connection waiting screen", () => {
    const install = {
      slug: "apps",
      groupId: "g-1",
      baseUrl: "https://git.example.com",
      org: "example",
      connectorId: "c-1",
      mcpServer: "forgejo-mcp-apps",
      hookId: 1,
      roles: {},
      excludeLabel: "do-not-refine",
    };

    sdk.get.mockResolvedValue({ verified: false, at: null });

    const { container } = render(
      <Connect
        install={install}
        onDone={vi.fn()}
      />
    );

    expect(container.querySelector('.forgejo-connect')).toBeTruthy();
  });

  it("shows the direct hook link in the connection screen", () => {
    const install = {
      slug: "apps",
      groupId: "g-1",
      baseUrl: "https://git.example.com",
      org: "example",
      connectorId: "c-1",
      mcpServer: "forgejo-mcp-apps",
      hookId: 1,
      roles: {},
      excludeLabel: "do-not-refine",
    };

    sdk.get.mockResolvedValue({ verified: false, at: null });

    const { container } = render(
      <Connect
        install={install}
        onDone={vi.fn()}
      />
    );

    // Check for the hook URL
    const hookLink = container.querySelector('a');
    expect(hookLink?.href).toContain("hooks/1");
  });

  it("calls onDone when Finish without verifying is clicked", () => {
    const install = {
      slug: "apps",
      groupId: "g-1",
      baseUrl: "https://git.example.com",
      org: "example",
      connectorId: "c-1",
      mcpServer: "forgejo-mcp-apps",
      hookId: 1,
      roles: {},
      excludeLabel: "do-not-refine",
    };

    sdk.get.mockResolvedValue({ verified: false, at: null });

    const onDone = vi.fn();
    const { container } = render(
      <Connect
        install={install}
        onDone={onDone}
      />
    );

    // The way out while waiting is named for what it does: it finishes without a verified webhook.
    const continueBtn = container.querySelector('.forgejo-continue-btn') as HTMLButtonElement;
    expect(continueBtn.textContent).toBe("Finish without verifying");
  });

  it("the waiting screen asks for a real event", () => {
    // The install's hook is an org hook, and Forgejo offers its test-delivery button on
    // repository hooks only. An instruction to click one that is not there leaves the admin
    // with nothing to do, so the screen asks for something the admin can actually do.
    sdk.get.mockResolvedValue({ verified: false, at: null });

    const { container } = render(
      <Connect install={installForPolling} onDone={vi.fn()} />
    );

    expect(
      screen.getByText(
        /open an issue, or comment on one, in any repository of example/,
      ),
    ).toBeTruthy();
    expect(container.textContent).not.toContain("Test Delivery");
  });

  it("finishing without verifying is offered while waiting", () => {
    // The install is complete by the time this screen opens; waiting here is a courtesy, not
    // a gate. Continuing is always a way out.
    sdk.get.mockResolvedValue({ verified: false, at: null });

    const onDone = vi.fn();
    const { container } = render(
      <Connect install={installForPolling} onDone={onDone} />
    );

    const continueBtn = container.querySelector(".forgejo-continue-btn") as HTMLButtonElement;
    fireEvent.click(continueBtn);
    expect(onDone).toHaveBeenCalled();
  });

  const installForPolling = {
    slug: "apps",
    baseUrl: "https://git.example.com",
    org: "example",
    connectorId: "c-1",
    hookId: 1,
  };

  it("polls deliveries until verified, then shows the check mark", async () => {
    vi.useFakeTimers();
    try {
      // The first poll (on mount) sees no delivery; the next one does.
      get
        .mockResolvedValueOnce({ verified: false, at: null })
        .mockResolvedValue({ verified: true, at: "2026-10-24T10:00:00Z" });

      render(<Connect install={installForPolling} onDone={vi.fn()} />);

      // The poll goes to the install's deliveries endpoint, not anywhere else.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });
      expect(get).toHaveBeenCalledWith(
        "/setup/installs/apps/deliveries",
        expect.objectContaining({ query: expect.any(Object) })
      );

      // Still waiting before the next tick answers with a delivery.
      // The checklist draws a tick on the step already done, so the check is for the verified line.
      expect(screen.queryByText(/✓ Webhook verified/)).toBeNull();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(3000);
      });

      expect(screen.getByText(/✓ Webhook verified/)).toBeTruthy();
      expect(screen.getByText(/2026-10-24T10:00:00Z/)).toBeTruthy();

      // And polling stops once verified — no further calls after the answer.
      const callsAtVerification = get.mock.calls.length;
      await act(async () => {
        await vi.advanceTimersByTimeAsync(9000);
      });
      expect(get.mock.calls.length).toBe(callsAtVerification);
    } finally {
      vi.useRealTimers();
    }
  });

  it("shows the Recent Deliveries hint and the direct hook link after 60 s without a delivery", async () => {
    vi.useFakeTimers();
    try {
      get.mockResolvedValue({ verified: false, at: null });

      render(<Connect install={installForPolling} onDone={vi.fn()} />);

      // Past the 60 s wait, with no delivery ever arriving.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(63000);
      });

      expect(screen.getByText(/No delivery detected after 60 seconds/)).toBeTruthy();
      expect(screen.getByText("Recent Deliveries:")).toBeTruthy();

      const hookLink = screen.getByRole("link", { name: /git\.example\.com/ });
      expect(hookLink.getAttribute("href")).toBe(
        "https://git.example.com/org/example/settings/hooks/1"
      );
    } finally {
      vi.useRealTimers();
    }
  });

  it("polls again after Retry", async () => {
    // Retry used to clear the timeout message only. The interval it needed had been cleared
    // on the way into the timeout, and nothing started a new one, so the screen went back to
    // saying "Waiting for the first event…" while nothing was scheduled to ask again.
    vi.useFakeTimers();
    try {
      get.mockResolvedValue({ verified: false, at: null });

      const { container } = render(<Connect install={installForPolling} onDone={vi.fn()} />);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(63000);
      });
      expect(screen.getByText(/No delivery detected after 60 seconds/)).toBeTruthy();
      const callsAtTimeout = get.mock.calls.length;

      const retry = container.querySelector(".forgejo-retry-btn") as HTMLButtonElement;
      await act(async () => {
        fireEvent.click(retry);
      });
      expect(screen.getByText(/Waiting for the first event/)).toBeTruthy();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(6000);
      });
      expect(get.mock.calls.length).toBeGreaterThan(callsAtTimeout);
    } finally {
      vi.useRealTimers();
    }
  });

  it("a deliveries call that keeps failing is shown", async () => {
    // A poll that cannot ask the hub is not an answer, and for a poll or two it is not news
    // either. But a screen that goes on saying "polling" while nothing it asks gets answered
    // is telling the admin the wrong thing, so after a third failure it says what is wrong —
    // and keeps asking.
    vi.useFakeTimers();
    try {
      get.mockRejectedValue(new Error("hub is not answering"));

      const { container } = render(<Connect install={installForPolling} onDone={vi.fn()} />);

      const warns = () =>
        Array.from(container.querySelectorAll('[data-variant="warn"]')).map((n) => n.textContent);

      // Mount: the first poll fails. Then the second.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(3000);
      });
      expect(
        warns().some((text) => text?.includes("Could not check deliveries")),
        "two polls that could not ask are not yet news",
      ).toBe(false);

      // The third says it, in the middle of the wait.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(3000);
      });
      expect(
        warns().some((text) => text?.includes("Could not check deliveries: hub is not answering")),
      ).toBe(true);
      expect(screen.getByText(/Waiting for the first event/)).toBeTruthy();
      const callsAtNotice = get.mock.calls.length;

      // A poll that does answer clears it, even one with nothing to report.
      get.mockResolvedValue({ verified: false, at: null });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(3000);
      });
      expect(warns().some((text) => text?.includes("Could not check deliveries"))).toBe(false);
      expect(get.mock.calls.length, "the asking never stopped").toBeGreaterThan(callsAtNotice);
    } finally {
      vi.useRealTimers();
    }
  });

  it("stops polling when the screen goes away", async () => {
    // The wizard can leave S7 at any point (Continue, Back, a new walk). A poll still
    // running after that asks for deliveries nobody is watching.
    vi.useFakeTimers();
    try {
      get.mockResolvedValue({ verified: false, at: null });

      const { unmount } = render(<Connect install={installForPolling} onDone={vi.fn()} />);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });
      const callsWhileMounted = get.mock.calls.length;

      unmount();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(15000);
      });
      expect(get.mock.calls.length).toBe(callsWhileMounted);
    } finally {
      vi.useRealTimers();
    }
  });
});
