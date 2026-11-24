// The header row: what the page is for, and the two actions on the data it shows. The
// period is the one control that changes what the server returns; the export only reads
// what is shown.

import { Button, Field, Select } from "@ainsel/plugin-sdk";
import { PERIODS } from "./model";
export function Header({
  period,
  onPeriod,
  exportable,
  onExport,
}: {
  period: string;
  onPeriod: (next: string) => void;
  exportable: boolean;
  onExport: () => void;
}) {
  return (
    <div
      style={{
        display: "flex",
        flexWrap: "wrap",
        alignItems: "flex-end",
        justifyContent: "space-between",
        gap: 12,
      }}
    >
      <p style={{ margin: 0, fontSize: 14, color: "var(--ink-2)" }}>
        Every thread the agents touched, its hand-off stage, and what it cost.
      </p>
      <div style={{ display: "flex", flexWrap: "wrap", alignItems: "flex-end", gap: 10 }}>
        <Field label="Period" htmlFor="forgejo-work-period">
          <Select id="forgejo-work-period" size="sm" value={period} onChange={onPeriod} options={PERIODS} />
        </Field>
        <Button onClick={onExport} disabled={!exportable}>
          Export CSV
        </Button>
      </div>
    </div>
  );
}
