import { Panel, Tag } from "@ainsel/plugin-sdk";
import type { ReactNode } from "react";
import type { Item, Run } from "./api";
import {
  DASH,
  formatCost,
  formatDuration,
  formatRelative,
  isActiveRun,
  newestRun,
  statusLabel,
  statusTone,
  threadRuns,
  threadStatusRun,
  threadTotals,
} from "./format";

// WorkItemDetail is the side panel beside the work table: the thread's totals as four
// cards, the item's own runs as a timeline, and — for an issue — the pull requests nested under it,
// each of which can be opened in this same panel. It renders from the data the row already
// carries (the /work response includes every run), so opening it is instant and needs no
// second fetch.
//
// The cards add the thread up — the item's own numbers plus each nested pull request's, every run
// counted once — so they agree with the row that opened them and with the strip above the table.
// The split stays visible: a line under the cards gives the item's own figures, and each nested
// pull request still shows its own cost and time in the list below (docs/work-view.md).

interface Props {
  item: Item;
  repoUrl: string;
  // Close the panel and show the table alone. Selection is component state in WorkPage: the
  // console's router only knows the static manifest pages and cannot carry an item's identity
  // in a path, so the panel is not a page of its own.
  onClose: () => void;
  // Show another item (a nested pull request) in this panel. This switches the selection, not
  // the browser URL, so the browser's back button does not step through it: an accepted
  // limitation of state routing.
  onOpenItem: (item: Item) => void;
}

// The role colours the run timeline draws its dots in. They are the live board's own tokens
// (LivePage.tsx), so a refiner is the same colour on both screens, and an operator can re-point
// one role in the console's theme without touching this bundle.
const ROLE_COLORS: Record<string, string> = {
  refiner: "var(--live-refiner, var(--warn))",
  developer: "var(--live-developer, var(--signal))",
  reviewer: "var(--live-reviewer, var(--stale))",
};

export function WorkItemDetail({ item, repoUrl, onClose, onOpenItem }: Props) {
  const now = new Date();
  const kindLabel = item.subject.kind === "pulls" ? "Pull request" : "Issue";
  const number = `#${item.subject.number}`;
  const active = threadRuns(item).filter(isActiveRun).length;
  // The cards are the thread's totals, the same sums the row shows, so the panel never disagrees
  // with the row that opened it. `own` is the same addition over the item alone, which is what the
  // split line under the cards reports.
  const totals = threadTotals(item);
  const own = threadTotals({ ...item, prs: [] });
  // The header pill is the thread's state, the same one the table row shows, so the panel and
  // the row that opened it never disagree about what the thread is doing.
  const threadNewest = threadStatusRun(item);

  return (
    <Panel className="forgejo-detail-page">
      <div
        className="forgejo-detail-header"
        style={{
          display: "flex",
          flexDirection: "column",
          gap: 8,
          padding: "16px 20px",
          borderBottom: "1px solid var(--rule-soft)",
        }}
      >
        <div
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
            gap: 8,
          }}
        >
          <span style={{ fontSize: 12, color: "var(--ink-3)" }}>
            {kindLabel} ·{" "}
            <a
              href={repoUrl}
              target="_blank"
              rel="noreferrer"
              className="forgejo-repo-link"
            >
              {item.subject.repo}
            </a>{" "}
            <span className="forgejo-mono" style={MONO}>
              {number}
            </span>
          </span>
          <button
            type="button"
            className="forgejo-detail-close"
            aria-label="Close details"
            onClick={onClose}
            style={ICON_BUTTON}
          >
            <CloseIcon />
          </button>
        </div>
        <h3
          className="forgejo-detail-title"
          style={{ margin: 0, fontSize: 17, lineHeight: "24px", fontWeight: 600 }}
        >
          {item.subject.title || number}
        </h3>
        <div style={{ display: "flex", flexWrap: "wrap", gap: 8, alignItems: "center" }}>
          {threadNewest === null ? (
            <Tag variant="stale">No runs</Tag>
          ) : (
            <Tag variant={statusTone(threadNewest.status)}>
              {statusLabel(threadNewest.status)}
            </Tag>
          )}
          <a
            href={item.url}
            target="_blank"
            rel="noreferrer"
            className="forgejo-item-link"
            style={{ fontSize: 13, fontWeight: 500 }}
          >
            Open on Forgejo ↗
          </a>
        </div>
      </div>

      <div
        className="forgejo-detail-cards"
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(2, minmax(0, 1fr))",
          gap: 8,
          padding: "16px 20px",
        }}
      >
        <Card label="Cost" value={formatCost(totals.costUsd, totals.costKnown)} />
        <Card label="Agent time" value={formatDuration(totals.durationMs)} />
        <Card
          label="Runs"
          value={
            active > 0
              ? `${totals.runs} (${active} active)`
              : String(totals.runs)
          }
        />
        <Card
          label="Updated"
          value={
            <span title={new Date(item.lastActivity).toLocaleString()}>
              {formatRelative(item.lastActivity, now)}
            </span>
          }
        />
      </div>

      {item.prs.length > 0 && (
        // The cards add the thread up, so this line keeps the split readable: the issue's own
        // figures, then each nested pull request's, in the order the list below shows them.
        <div
          className="forgejo-detail-split"
          style={{
            display: "flex",
            flexWrap: "wrap",
            gap: "2px 12px",
            padding: "0 20px 12px",
            fontSize: 12,
            color: "var(--ink-3)",
          }}
        >
          <span>
            {`Thread totals include ${item.prs.length} linked ${item.prs.length === 1 ? "pull request" : "pull requests"}`}
          </span>
          <span>
            {`This ${kindLabel.toLowerCase()}: ${formatCost(own.costUsd, own.costKnown)} · ${own.runs} ${own.runs === 1 ? "run" : "runs"} · ${formatDuration(own.durationMs)}`}
          </span>
          {item.prs.map((pr) => (
            <span key={`${pr.subject.repo}#${pr.subject.number}`}>
              {`#${pr.subject.number}: ${formatCost(pr.costUsd, pr.costKnown)} · ${pr.runs.length} ${pr.runs.length === 1 ? "run" : "runs"} · ${formatDuration(pr.durationMs)}`}
            </span>
          ))}
        </div>
      )}

      <section style={{ padding: "4px 20px 16px" }}>
        <h4 className="forgejo-detail-section" style={SECTION_HEADING}>
          Run history
        </h4>
        {item.runs.length === 0 ? (
          <p style={{ margin: 0, fontSize: 13, color: "var(--ink-3)" }}>
            No runs of their own
          </p>
        ) : (
          // Newest first, as the response orders the runs. The list is an ordered list because
          // the order is the story: each run is the one the hand-off took after the one above.
          <ol
            aria-label="Run history"
            style={{ listStyle: "none", margin: 0, padding: 0, display: "flex", flexDirection: "column" }}
          >
            {item.runs.map((run, index) => (
              <RunStep
                key={run.id}
                run={run}
                now={now}
                last={index === item.runs.length - 1}
              />
            ))}
          </ol>
        )}
      </section>

      {item.prs.length > 0 && (
        <section
          className="forgejo-detail-prs"
          style={{ padding: "16px 20px", borderTop: "1px solid var(--rule-soft)" }}
        >
          <h4 className="forgejo-detail-section" style={SECTION_HEADING}>
            Linked pull requests
          </h4>
          <ul style={{ listStyle: "none", margin: 0, padding: 0, display: "flex", flexDirection: "column", gap: 8 }}>
            {item.prs.map((pr) => {
              const newest = newestRun(pr.runs);
              return (
                <li
                  key={`${pr.subject.repo}#${pr.subject.number}`}
                  className="forgejo-detail-pr"
                  style={{
                    display: "flex",
                    justifyContent: "space-between",
                    alignItems: "center",
                    gap: 8,
                    padding: "10px 12px",
                    border: "1px solid var(--rule-soft)",
                    borderRadius: "var(--radius-sm)",
                    fontSize: 13,
                  }}
                >
                  <span style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
                    <button
                      type="button"
                      className="forgejo-item-open"
                      onClick={() => onOpenItem(pr)}
                      style={{ ...OPEN_BUTTON, fontSize: 13 }}
                    >
                      #{pr.subject.number} {pr.subject.title ?? ""}
                    </button>
                    <span className="forgejo-detail-pr-meta" style={{ fontSize: 12, color: "var(--ink-2)" }}>
                      {formatCost(pr.costUsd, pr.costKnown)} · {formatDuration(pr.durationMs)}
                    </span>
                  </span>
                  <span style={{ display: "flex", alignItems: "center", gap: 8, flex: "0 0 auto" }}>
                    {newest === null ? (
                      <Tag variant="stale">No runs</Tag>
                    ) : (
                      <Tag variant={statusTone(newest.status)}>{statusLabel(newest.status)}</Tag>
                    )}
                    <a
                      href={pr.url}
                      target="_blank"
                      rel="noreferrer"
                      className="forgejo-item-link"
                      aria-label={`Open #${pr.subject.number} on Forgejo`}
                    >
                      ↗
                    </a>
                  </span>
                </li>
              );
            })}
          </ul>
        </section>
      )}
    </Panel>
  );
}

// RunStep is one run on the timeline: a dot in its role's colour, the role and how it ended,
// then who ran it, when, for how long, and what it cost. The run's id sits under it in mono, the
// handle the admin would quote to the hub.
function RunStep({ run, now, last }: { run: Run; now: Date; last: boolean }) {
  return (
    <li style={{ display: "flex", gap: 12 }}>
      <div style={{ display: "flex", flexDirection: "column", alignItems: "center", flex: "0 0 auto" }}>
        <span
          aria-hidden="true"
          style={{
            width: 10,
            height: 10,
            borderRadius: 5,
            background: ROLE_COLORS[run.role] ?? "var(--ink-4)",
            marginTop: 4,
          }}
        />
        {!last && (
          <span
            aria-hidden="true"
            style={{ flex: "1 1 auto", width: 2, background: "var(--rule-soft)", margin: "4px 0" }}
          />
        )}
      </div>
      <div
        style={{
          flex: "1 1 auto",
          minWidth: 0,
          paddingBottom: last ? 0 : 14,
          display: "flex",
          flexDirection: "column",
          gap: 2,
        }}
      >
        <div style={{ display: "flex", justifyContent: "space-between", gap: 8, fontSize: 13 }}>
          <span style={{ fontWeight: 600 }}>{roleName(run.role)}</span>
          <Tag variant={statusTone(run.status)}>{statusLabel(run.status)}</Tag>
        </div>
        <span style={{ fontSize: 12, color: "var(--ink-2)" }}>
          {run.agentName || run.agent} ·{" "}
          <span title={new Date(run.start).toLocaleString()}>{formatRelative(run.start, now)}</span>
          {" · "}
          {/* A run that has not finished has no duration to report, so it reads as a dash. */}
          {run.durationMs === undefined ? DASH : formatDuration(run.durationMs)}
          {" · "}
          {formatCost(run.costUsd, run.costKnown)}
        </span>
        <span className="forgejo-mono" style={{ ...MONO, fontSize: 11, color: "var(--ink-3)" }}>
          {run.id}
        </span>
      </div>
    </li>
  );
}

// roleName capitalises a role id for display: the hub stores it lower-case.
function roleName(role: string): string {
  return role.charAt(0).toUpperCase() + role.slice(1);
}

// One summary card: a label over the number, nothing more. The values come from the item's own
// totals and go through the shared formatters, so a cost the hub could not price reads as a dash
// here exactly as it does in the table.
function Card({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div
      className="forgejo-card"
      style={{
        display: "flex",
        flexDirection: "column",
        gap: 2,
        padding: 12,
        border: "1px solid var(--rule-soft)",
        borderRadius: "var(--radius-sm)",
      }}
    >
      <span style={{ fontSize: 12, color: "var(--ink-2)" }}>{label}</span>
      <span className="forgejo-card-value" style={{ fontSize: 16, fontWeight: 600, fontVariantNumeric: "tabular-nums" }}>
        {value}
      </span>
    </div>
  );
}

function CloseIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
      <path d="M18 6L6 18M6 6l12 12" />
    </svg>
  );
}

// The shared look of the panel's small pieces. Inline because the bundle ships no stylesheet;
// the classes are there for the console to hang theme on.
export const MONO = { fontFamily: "ui-monospace, monospace", fontSize: 12 };
const SECTION_HEADING = { margin: "0 0 12px", fontSize: 13, fontWeight: 600, color: "var(--ink-2)" };
const ICON_BUTTON = {
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  width: 32,
  height: 32,
  padding: 0,
  border: "1px solid var(--rule-soft)",
  borderRadius: "var(--radius-sm)",
  background: "var(--paper)",
  color: "var(--ink-2)",
  cursor: "pointer",
};
export const OPEN_BUTTON = {
  border: 0,
  background: "transparent",
  padding: 0,
  textAlign: "left" as const,
  font: "inherit",
  fontWeight: 500,
  color: "var(--ink)",
  cursor: "pointer",
  maxWidth: "100%",
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap" as const,
};
