// The strip above the table. It counts every row of the period whatever the filters
// show — the strip answers "how much work is there", the filters answer "which of it
// am I looking at" — and there is nothing to say before the answer has rows.

import { Panel } from "@ainsel/plugin-sdk";
import type { Row } from "./model";

export function Summary({ rows, summary }: { rows: Row[]; summary: { label: string; value: string }[] }) {
  return (
    <>
      {rows.length > 0 && (
        <Panel className="forgejo-work-summary">
          <div style={{ display: "flex", flexWrap: "wrap" }}>
            {summary.map((stat) => (
              <div
                key={stat.label}
                style={{
                  flex: "1 1 160px",
                  padding: "14px 20px",
                  borderRight: "1px solid var(--rule-soft)",
                  display: "flex",
                  flexDirection: "column",
                  gap: 2,
                  minWidth: 0,
                }}
              >
                <span style={{ fontSize: 12, color: "var(--ink-2)", fontWeight: 500 }}>{stat.label}</span>
                <span
                  className="forgejo-work-stat-value"
                  style={{ fontSize: 20, fontWeight: 600, fontVariantNumeric: "tabular-nums" }}
                >
                  {stat.value}
                </span>
              </div>
            ))}
          </div>
        </Panel>
      )}
    </>
  );
}
