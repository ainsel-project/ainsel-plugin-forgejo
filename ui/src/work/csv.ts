// The CSV the header's button downloads: the rows the table is filtered down to, in the
// table's order, with the readings the rows show — so the file and the screen agree, and
// the spreadsheet can add the cost column up.

import { formatDuration, statusLabel, threadStatusRun, threadTotals } from "../format";
import type { Row } from "./model";

// exportCsv downloads the rows the table is filtered down to, all of them rather than the page
// on screen. It is built in the browser: the rows are already here, and a download the server
// would have to answer again could disagree with what the admin is looking at.
export function exportCsv(rows: Row[], period: string) {
  const blob = new Blob([toCsv(rows)], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = `forgejo-work-${period}.csv`;
  document.body.appendChild(link);
  link.click();
  link.remove();
  // Revoked once the click has handed the download to the browser, not before.
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

export const CSV_HEADER = ["kind", "number", "title", "repo", "status", "runs", "agent time", "cost", "last activity"];

// toCsv writes one line per thread, in the order the table shows them, with the same values the
// row shows — the thread's totals, nested pull requests included, each run counted once. Agent time
// keeps the table's reading ("3m 05s") so the file and the screen agree; cost is a plain number of
// dollars, empty when the hub could not price the thread or nothing ran on it, so a spreadsheet can
// add it up.
export function toCsv(rows: Row[]): string {
  const lines = [CSV_HEADER.join(",")];
  for (const { item } of rows) {
    const newest = threadStatusRun(item);
    const totals = threadTotals(item);
    lines.push(
      [
        item.subject.kind === "pulls" ? "PR" : "Issue",
        String(item.subject.number),
        item.subject.title ?? "",
        item.subject.repo,
        newest === null ? "" : statusLabel(newest.status),
        String(totals.runs),
        formatDuration(totals.durationMs),
        totals.costUsd !== null ? totals.costUsd.toFixed(2) : "",
        item.lastActivity,
      ]
        .map(csvCell)
        .join(","),
    );
  }
  return lines.join("\r\n");
}

// csvCell quotes a field that holds a separator, a quote or a line break, doubling any quote
// inside it, as RFC 4180 asks.
export function csvCell(value: string): string {
  return /[",\r\n]/.test(value) ? `"${value.replace(/"/g, '""')}"` : value;
}
