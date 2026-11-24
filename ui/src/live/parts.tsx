// The small pieces the board is built from: a thread as it should read and
// behave, one headline figure, the pill that says the board is watching, a tab's
// count, and the wrapper that gives a table its own scrollbar. Each is drawn
// once and reused by the panels; none of them fetches or owns page state.

import { Panel, Tag } from "@ainsel/plugin-sdk";
import { useEffect, useState, type ReactNode } from "react";
import { formatAgo } from "../format";
import { subjectLabel } from "./model";
import type { Run } from "../api";

// ThreadLink is a run's thread as it should read and behave: a link out to the
// issue or pull, opening in a new tab so the board stays put, and drawn as a
// link (underlined) rather than as text that only happens to have an href. The
// label defaults to the thread's name; the queue passes the title alone, since
// its own column already says what kind of thread it is.
export function ThreadLink({ run, label }: { run: Run; label?: string }) {
  const text = label ?? `${subjectLabel(run)}${run.subject.title ? ` ${run.subject.title}` : ""}`;
  return (
    <a
      href={run.url}
      target="_blank"
      rel="noreferrer"
      className="forgejo-item-link"
      style={{ textDecoration: "underline", textUnderlineOffset: 2 }}
    >
      {text}
    </a>
  );
}

// Kpi is one headline figure: what it counts, the number, and a line saying what
// the number is made of, so a bare `3` always has its "which" beside it.
export function Kpi({ label, value, sub }: { label: string; value: string; sub: string }) {
  return (
    <Panel className="forgejo-live-stat-card">
      <div className="forgejo-live-stat" style={{ display: "flex", flexDirection: "column", gap: 6 }}>
        <span className="forgejo-live-stat-label" style={{ fontSize: 13, color: "var(--ink-2)", fontWeight: 500 }}>
          {label}
        </span>
        <b
          className="forgejo-live-stat-value"
          style={{ fontSize: 26, lineHeight: "32px", fontWeight: 600, fontVariantNumeric: "tabular-nums" }}
        >
          {value}
        </b>
        <span className="forgejo-live-stat-sub" style={{ fontSize: 12, color: "var(--ink-3)" }}>
          {sub}
        </span>
      </div>
    </Panel>
  );
}

// LiveStatus is the page's claim that it is watching: a pill that pulses while the
// board is alive and says how long ago it last heard from the plugin. It ticks on
// its own clock, so the seconds keep moving between refreshes rather than freezing
// until the next fetch. The keyframes ride with the component because the plugin
// ships no stylesheet of its own and the pulse is the only animation on the page.
export function LiveStatus({ updatedAt, paused }: { updatedAt: number | null; paused: boolean }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (paused) return;
    // Catch up at once: a resume should not show the seconds the pause stopped at.
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [paused]);

  if (paused) {
    return (
      <Tag variant="stale">
        <span
          className="forgejo-live-dot"
          aria-hidden="true"
          style={{ width: 8, height: 8, borderRadius: 4, background: "var(--stale)", display: "inline-block" }}
        />
        Paused
      </Tag>
    );
  }
  const text = updatedAt === null ? "Live · loading" : `Live · updated ${formatAgo(now - updatedAt)}`;
  return (
    <Tag variant="ok">
      <style>{`@keyframes forgejo-live-pulse{0%,100%{opacity:1}50%{opacity:.35}}`}</style>
      <span
        className="forgejo-live-dot"
        aria-hidden="true"
        style={{
          width: 8,
          height: 8,
          borderRadius: 4,
          background: "var(--ok)",
          display: "inline-block",
          animation: "forgejo-live-pulse 2s ease-in-out infinite",
        }}
      />
      {text}
    </Tag>
  );
}

// TabCount is the number beside a queue tab's name. It is a pill of its own so the
// count reads as a figure, not as a word that happens to end the label.
export function TabCount({ n }: { n: number }) {
  return (
    <span
      className="forgejo-live-count"
      style={{
        display: "inline-block",
        minWidth: 18,
        padding: "0 6px",
        marginLeft: 6,
        borderRadius: 9,
        background: "var(--paper-2)",
        color: "var(--ink-2)",
        fontSize: 11,
        lineHeight: "18px",
        textAlign: "center",
      }}
    >
      {n}
    </span>
  );
}

// Scroll gives a table its own horizontal scrollbar, so a narrow window scrolls the
// table rather than the whole page.
export function Scroll({ minWidth, children }: { minWidth: number; children: ReactNode }) {
  return (
    <div style={{ overflowX: "auto" }}>
      <div style={{ minWidth }}>{children}</div>
    </div>
  );
}
