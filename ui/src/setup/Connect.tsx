import { Button, Notice } from "@ainsel/plugin-sdk";
import { useEffect, useState } from "react";
import type { CSSProperties } from "react";
import { usePluginContext } from "@ainsel/plugin-sdk";
import { asApiError } from "../api";
import { StepFooter } from "./Wizard";

// How long to wait for Forgejo to deliver the test event, and how often to ask.
const POLL_INTERVAL_MS = 3000;
const VERIFY_TIMEOUT_MS = 60000;

interface Install {
  slug: string;
  baseUrl: string;
  org: string;
  hookId: number;
  connectorId: string;
  webhookVerifiedAt?: string;
}

interface ConnectProps {
  install: Install;
  // Leaving S7 carries whether a delivery was seen: the done screen says "verified" only when
  // one was, and the installs list needs the truth too.
  onDone: (verified: boolean) => void;
  onBack?: () => void;
}

export function Connect({ install, onDone, onBack }: ConnectProps) {
  const { api } = usePluginContext();
  const [verified, setVerified] = useState(false);
  const [verificationTime, setVerificationTime] = useState<string | null>(null);
  const [timedOut, setTimedOut] = useState(false);
  // Why the polls have not been able to ask — the last failure's own message, once they have
  // failed in a row enough to be more than one bad moment. The wait goes on regardless: this is
  // the screen saying the ask is not getting through, not giving up on it.
  const [pollError, setPollError] = useState<string | null>(null);
  // Bumped by Retry. The effect below polls once per value, so asking again is asking the
  // effect to run again — there is no interval held in a ref for a click handler to lose.
  const [attempt, setAttempt] = useState(0);

  const hookUrl = `${install.baseUrl}/org/${install.org}/settings/hooks/${install.hookId}`;

  // The effect owns the polling: it starts when the screen opens or when Retry asks again,
  // and it stops when a delivery lands, the window closes or the screen goes away. Polling
  // used to be a ref handed to a click handler, which cleared the interval on timeout and
  // never started a new one — so Retry left the admin reading "Polling for webhook
  // delivery…" on a screen that had nothing left to poll with.
  useEffect(() => {
    const startedAt = Date.now();
    let cancelled = false;
    // eslint-disable-next-line prefer-const -- `let`, not `const`: the first pollDeliveries() runs before the assignment below, and its timeout path calls stop(), which reads `timer`. A const would TDZ-crash there.
    let timer: ReturnType<typeof setInterval> | undefined;

    const stop = () => {
      if (timer) clearInterval(timer);
    };

    // Asked and not answered, in a row. Counted here rather than in state: it decides only
    // whether to say anything, and it starts again with the asking when this effect does.
    let failures = 0;

    const pollDeliveries = async () => {
      if (Date.now() - startedAt > VERIFY_TIMEOUT_MS) {
        stop();
        if (!cancelled) setTimedOut(true);
        return;
      }

      try {
        const response = (await api.get(`/setup/installs/${install.slug}/deliveries`, {
          query: { since: new Date(startedAt).toISOString() },
        })) as { verified: boolean; at: string | null };

        if (cancelled) return;
        failures = 0;
        setPollError(null);
        if (response.verified) {
          stop();
          setVerified(true);
          setVerificationTime(response.at);
        }
      } catch (err) {
        // A poll that failed to answer is not an answer; the next one asks again. Three of them
        // in a row is worth the admin hearing, though — "polling" over a hub this screen cannot
        // reach is a wait that will not end on its own.
        failures += 1;
        if (failures >= 3) {
          setPollError(asApiError(err).message || "The deliveries endpoint did not answer");
        }
      }
    };

    pollDeliveries();
    timer = setInterval(pollDeliveries, POLL_INTERVAL_MS);

    return () => {
      cancelled = true;
      stop();
    };
  }, [api, install.slug, attempt]);

  // The install's host as the checklist names it: the address without its scheme, then the org.
  const host = install.baseUrl.replace(/^https?:\/\//, "");

  if (verified) {
    return (
      <div className="forgejo-connect" style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
        {/* The verified state is an ok notice; the console's Notice has no ok variant, so the
            tick in the text is what makes it read as a success. */}
        <Notice variant="info">✓ Webhook verified at {verificationTime}</Notice>
        <StepFooter onBack={onBack}>
          <Button variant="primary" onClick={() => onDone(true)} className="forgejo-done-btn">
            Done
          </Button>
        </StepFooter>
      </div>
    );
  }

  if (timedOut) {
    return (
      <div className="forgejo-connect" style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
        <Notice variant="warn">No delivery detected after 60 seconds. Check the webhook status manually.</Notice>
        <div className="forgejo-hint" style={{ fontSize: "0.8125rem", color: "var(--ink-2)" }}>
          <p>Recent Deliveries:</p>
          <p>
            <a href={hookUrl} target="_blank" rel="noopener noreferrer">
              {hookUrl}
            </a>
          </p>
          <p>Click the hook and check "Recent Deliveries" to see if deliveries are arriving.</p>
        </div>
        <StepFooter onBack={onBack}>
          <Button
            onClick={() => {
              setTimedOut(false);
              setVerified(false);
              setVerificationTime(null);
              setPollError(null);
              setAttempt((a) => a + 1);
            }}
            className="forgejo-retry-btn"
          >
            Retry
          </Button>
          <Button onClick={() => onDone(false)} className="forgejo-continue-btn">
            Finish without verifying
          </Button>
        </StepFooter>
      </div>
    );
  }

  return (
    <div className="forgejo-connect" style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      {/* The waiting state: a pulsing dot, because the screen is watching for the event and will
          turn green by itself when it lands. */}
      <style>{"@keyframes forgejo-setup-pulse{0%,100%{opacity:1}50%{opacity:.35}}"}</style>
      <div
        role="status"
        style={{
          display: "flex",
          alignItems: "center",
          gap: "0.75rem",
          padding: "1rem",
          borderRadius: "var(--radius, 8px)",
          border: "1px solid color-mix(in srgb, var(--signal) 35%, transparent)",
          background: "var(--signal-haze)",
          color: "var(--signal-ink)",
          fontSize: "0.875rem",
        }}
      >
        <span
          aria-hidden="true"
          style={{
            width: "0.625rem",
            height: "0.625rem",
            borderRadius: "999px",
            background: "var(--signal)",
            flex: "0 0 auto",
            animation: "forgejo-setup-pulse 1.6s ease-in-out infinite",
          }}
        />
        <span>
          <strong style={{ fontWeight: 600 }}>Waiting for the first event…</strong> This turns green when Forgejo
          delivers a webhook to the install.
        </span>
      </div>

      {/* Polling is still on while the screen waits: a failure to ask is worth the admin hearing,
          so it is said here, while the wait itself goes on. */}
      {pollError && <Notice variant="warn">Could not check deliveries: {pollError}</Notice>}

      {/* The checklist is what the admin does, in order. Forgejo offers its test-delivery button on
          repository hooks only (V10), so the one step the admin takes for an organization webhook
          is a real event. */}
      <ol
        style={{
          margin: 0,
          padding: 0,
          listStyle: "none",
          display: "flex",
          flexDirection: "column",
          gap: "0.625rem",
          fontSize: "0.875rem",
        }}
      >
        <li style={{ display: "flex", gap: "0.625rem", alignItems: "flex-start" }}>
          <span aria-hidden="true" style={checkNumStyle("done")}>
            ✓
          </span>
          <span style={{ color: "var(--ink-2)", paddingTop: "0.0625rem" }}>
            {`Organization webhook created on ${host}/${install.org}`}
          </span>
        </li>
        <li style={{ display: "flex", gap: "0.625rem", alignItems: "flex-start" }}>
          <span aria-hidden="true" style={checkNumStyle("current")}>
            2
          </span>
          <span style={{ color: "var(--ink-2)", paddingTop: "0.0625rem" }}>
            {`Send one event: open an issue, or comment on one, in any repository of ${install.org}.`}
          </span>
        </li>
        <li style={{ display: "flex", gap: "0.625rem", alignItems: "flex-start" }}>
          <span aria-hidden="true" style={checkNumStyle("todo")}>
            3
          </span>
          <span style={{ color: "var(--ink-3)", paddingTop: "0.0625rem" }}>First event received</span>
        </li>
      </ol>

      <div className="forgejo-hint" style={{ fontSize: "0.8125rem", color: "var(--ink-2)" }}>
        <p style={{ margin: 0 }}>
          You can finish now. The install is complete. It is marked verified when the first event arrives, and you can
          come back here from the installs list.
        </p>
        <p style={{ margin: "0.5rem 0 0" }}>
          <a href={hookUrl} target="_blank" rel="noopener noreferrer">
            Open the webhook settings
          </a>{" "}
          to see Recent Deliveries in Forgejo.
        </p>
      </div>

      <StepFooter onBack={onBack}>
        <Button onClick={() => onDone(false)} className="forgejo-continue-btn">
          Finish without verifying
        </Button>
      </StepFooter>
    </div>
  );
}

// The number or tick at the start of a checklist line: done in the accent, the line the admin is
// on outlined, the lines still to come grey.
function checkNumStyle(state: "done" | "current" | "todo"): CSSProperties {
  const base: CSSProperties = {
    width: "1.25rem",
    height: "1.25rem",
    borderRadius: "999px",
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    flex: "0 0 auto",
    boxSizing: "border-box",
    fontSize: "0.6875rem",
    fontWeight: 600,
  };
  if (state === "done")
    return { ...base, background: "var(--signal)", color: "var(--paper)", border: "1px solid var(--signal)" };
  if (state === "current")
    return { ...base, background: "var(--paper)", color: "var(--signal)", border: "2px solid var(--signal)" };
  return { ...base, background: "var(--paper)", color: "var(--ink-3)", border: "1px solid var(--hair)" };
}
