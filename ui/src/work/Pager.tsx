// The row under the table: where in the list you are, and how many rows a page holds.
// Built from the primitives the SDK exposes rather than the console's own Pager, which
// the SDK does not register — a plugin cannot reach it. Layout is inline because the
// bundle ships no stylesheet of its own; the classes are there for the console to hang
// theme on.

import { Select } from "@ainsel/plugin-sdk";
import { PAGE_SIZES } from "./model";
import { ChevronIcon } from "./parts";
import { ICON_BUTTON } from "./styles";
export function Pager(props: {
  first: number;
  last: number;
  total: number;
  page: number;
  totalPages: number;
  pageSize: number;
  onPrev: () => void;
  onNext: () => void;
  onPageSize: (next: number) => void;
}) {
  const { first, last, total, page, totalPages, pageSize, onPrev, onNext, onPageSize } = props;
  return (
    <div
      className="forgejo-pager"
      style={{
        display: "flex",
        flexWrap: "wrap",
        justifyContent: "space-between",
        alignItems: "center",
        gap: 10,
        padding: "12px 16px",
        borderTop: "1px solid var(--rule-soft)",
        fontSize: 13,
        color: "var(--ink-2)",
      }}
    >
      <span className="forgejo-pager-info">
        Showing {first}–{last} of {total} threads
      </span>
      <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
        <span>Rows per page</span>
        <Select
          aria-label="Rows per page"
          className="forgejo-pager-size"
          size="sm"
          value={String(pageSize)}
          onChange={(next: string) => onPageSize(Number(next))}
          options={PAGE_SIZES.map((size) => ({ value: String(size), label: String(size) }))}
        />
        <button
          type="button"
          className="forgejo-pager-prev"
          aria-label="Previous page"
          disabled={page <= 1}
          onClick={onPrev}
          style={{ ...ICON_BUTTON, opacity: page <= 1 ? 0.5 : 1 }}
        >
          <ChevronIcon direction="left" />
        </button>
        <span className="forgejo-pager-position">
          Page {page} of {totalPages}
        </span>
        <button
          type="button"
          className="forgejo-pager-next"
          aria-label="Next page"
          disabled={page >= totalPages}
          onClick={onNext}
          style={{ ...ICON_BUTTON, opacity: page >= totalPages ? 0.5 : 1 }}
        >
          <ChevronIcon direction="right" />
        </button>
      </div>
    </div>
  );
}
