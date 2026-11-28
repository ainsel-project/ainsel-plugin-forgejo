// One role's page: its heading, then the cards its fields fall into. A card is
// drawn only when the registry answers with at least one of its fields — a row for
// a field the answer has none of would only lie about what can be edited — and the
// "Other" card catches whatever the card list does not name, so the answer, not
// this page, decides what renders.

import { cardStyle, GROUPS, KNOWN_FIELDS, ROLES } from "./model";
import type { FieldSpec, RoleKey } from "./model";
import type { Setting } from "../api";
import { FieldRow } from "./FieldRow";
import type { Draft } from "./model";

export function RoleCards({
  roleKey,
  roleSettings,
  version,
  drafts,
  fieldErrors,
  busyKey,
  comparing,
  saving,
  edit,
  handleReset,
  setComparing,
}: {
  roleKey: RoleKey;
  roleSettings: Setting[];
  version: string;
  drafts: Record<string, Draft>;
  fieldErrors: Record<string, string>;
  busyKey: string | null;
  comparing: Record<string, boolean>;
  saving: boolean;
  edit: (s: Setting, value: string) => void;
  handleReset: (s: Setting) => void;
  setComparing: (updater: (c: Record<string, boolean>) => Record<string, boolean>) => void;
}) {
  const role = ROLES.find((r) => r.key === roleKey)!;
  // Each card's rows, in the card's order, skipping a known field the registry
  // does not answer with: a row for it would only lie about what can be edited.
  const cardRows = (fields: FieldSpec[]) =>
    fields.flatMap((spec) => {
      const s = roleSettings.find((x) => x.field === spec.field);
      return s ? [{ spec, s }] : [];
    });
  const otherRows = roleSettings
    .filter((s) => !KNOWN_FIELDS.has(s.field))
    .map((s) => ({ spec: { field: s.field, label: s.field } as FieldSpec, s }));

  const cards = [
    ...GROUPS.map((g) => ({ title: g.title, desc: g.desc, rows: cardRows(g.fields) })),
    {
      title: "Other",
      desc: "Fields the registry answers with that this page has no card for. They edit and save like the rest.",
      rows: otherRows,
    },
  ].filter((c) => c.rows.length > 0);

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
      <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
        <h2 style={{ margin: 0, fontSize: 18, fontWeight: 600 }}>{role.label}</h2>
        <span style={{ fontSize: 13, color: "var(--ink-2)" }}>{role.desc}</span>
      </div>
      {cards.map((c) => (
        <section key={c.title} style={cardStyle}>
          <div style={{ padding: "16px 20px" }}>
            <h3 style={{ margin: 0, fontSize: 15, fontWeight: 600 }}>{c.title}</h3>
            <p style={{ margin: "2px 0 0", fontSize: 13, color: "var(--ink-2)" }}>{c.desc}</p>
          </div>
          {c.rows.map(({ spec, s }) => (
            <FieldRow
              key={s.field}
              spec={spec}
              s={s}
              version={version}
              drafts={drafts}
              fieldErrors={fieldErrors}
              busyKey={busyKey}
              comparing={comparing}
              saving={saving}
              edit={edit}
              handleReset={handleReset}
              setComparing={setComparing}
            />
          ))}
        </section>
      ))}
    </div>
  );
}
