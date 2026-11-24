// The work table's columns. They are built per render because the first one opens the
// detail panel: the title is a button that sets the page's selection, while the anchor
// beside it keeps its old meaning of leaving for Forgejo — the two link semantics stay
// visibly distinct, and the row itself cannot take a click through the table primitive.

import { Tag } from "@ainsel/plugin-sdk";
import {
  DASH,
  formatCost,
  formatDuration,
  formatRelative,
  statusLabel,
  statusTone,
  threadStatusRun,
  threadTotals,
} from "../format";
import { MONO, OPEN_BUTTON } from "../WorkItemDetail";
import { handOffStates, itemKey } from "./model";
import type { Row } from "./model";
import { HandOff, KindIcon } from "./parts";

// The columns are built per render because the first one opens the detail panel: the item's
// title is a button that sets WorkPage's selection, while a small anchor next to it keeps the
// old meaning of going to Forgejo in a new tab. The two link semantics stay visibly distinct —
// in-app panel (button) vs external page (anchor). The row itself cannot take a click through
// the console's table primitive, so the title button is the way in.
export function columns({ onOpen, highlightKey }: { onOpen: (row: Row) => void; highlightKey: string | null }) {
  return [
    {
      key: "thread",
      header: "Thread",
      width: "34%",
      cell: (row: Row) => {
        const { item } = row;
        const number = item.subject.number;
        // The selected thread gets a tint and an accent bar. The bundle ships no stylesheet, so
        // the highlight is drawn on the cell's own block rather than on the row.
        const selected = itemKey(item) === highlightKey;
        return (
          <span
            className="forgejo-item"
            style={{
              display: "flex",
              alignItems: "flex-start",
              gap: 10,
              minWidth: 0,
              margin: "-6px -8px",
              padding: "6px 8px",
              borderRadius: "var(--radius-sm)",
              background: selected ? "var(--signal-haze)" : undefined,
              boxShadow: selected ? "inset 3px 0 0 var(--signal)" : undefined,
            }}
          >
            <KindIcon isPr={item.subject.kind === "pulls"} />
            <span style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
              <span style={{ display: "flex", alignItems: "center", gap: 6, minWidth: 0 }}>
                <button
                  type="button"
                  className="forgejo-item-open"
                  onClick={() => onOpen(row)}
                  style={{ ...OPEN_BUTTON, minWidth: 0 }}
                >
                  {item.subject.title || `#${number}`}
                </button>
                <a
                  href={item.url}
                  target="_blank"
                  rel="noreferrer"
                  className="forgejo-item-link"
                  aria-label={`Open #${number} on Forgejo`}
                  style={{ flex: "0 0 auto" }}
                >
                  ↗
                </a>
              </span>
              <span style={{ fontSize: 12, color: "var(--ink-3)" }}>
                {item.subject.repo}{" "}
                <span className="forgejo-mono" style={MONO}>
                  #{number}
                </span>
              </span>
            </span>
          </span>
        );
      },
    },
    {
      key: "handoff",
      header: "Hand-off",
      width: 190,
      cell: (row: Row) => <HandOff states={handOffStates(row.item)} />,
    },
    {
      key: "prs",
      header: "Linked PRs",
      width: 120,
      cell: (row: Row) =>
        row.item.prs.length === 0
          ? DASH
          : row.item.prs.map((pr, index) => (
              <span key={`${pr.subject.repo}#${pr.subject.number}`} className="forgejo-nested-pr">
                {index > 0 ? ", " : ""}
                <a
                  href={pr.url}
                  target="_blank"
                  rel="noreferrer"
                  className="forgejo-pr-link"
                  style={{ ...MONO, color: "var(--signal)" }}
                >
                  #{pr.subject.number}
                </a>
              </span>
            )),
    },
    {
      key: "status",
      header: "Status",
      width: 130,
      cell: (row: Row) => {
        const newest = threadStatusRun(row.item);
        return newest === null ? (
          <Tag variant="stale">No runs</Tag>
        ) : (
          <Tag variant={statusTone(newest.status)}>{statusLabel(newest.status)}</Tag>
        );
      },
    },
    {
      key: "runs",
      header: "Runs",
      align: "right" as const,
      width: 64,
      // Every run on the thread, counted once: the item's own plus each nested pull request's, so
      // the reviewer that ran on the pull request shows up in the row that links it.
      cell: (row: Row) => String(threadTotals(row.item).runs),
    },
    {
      key: "time",
      header: "Agent time",
      align: "right" as const,
      width: 96,
      cell: (row: Row) => formatDuration(threadTotals(row.item).durationMs),
    },
    {
      key: "cost",
      header: "Cost",
      align: "right" as const,
      width: 84,
      // The thread's cost: one run the hub could not price anywhere on the thread makes the row
      // unknown rather than partly priced, and a thread nobody ran has nothing to price at all.
      cell: (row: Row) => {
        const totals = threadTotals(row.item);
        return formatCost(totals.costUsd, totals.costKnown);
      },
    },
    {
      key: "updated",
      header: "Updated",
      width: 96,
      cell: (row: Row) => (
        <span style={{ color: "var(--ink-2)" }} title={new Date(row.item.lastActivity).toLocaleString()}>
          {formatRelative(row.item.lastActivity, new Date())}
        </span>
      ),
    },
  ];
}
