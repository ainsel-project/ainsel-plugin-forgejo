import { Button, Notice, RegisterTable, Tag } from "@ainsel/plugin-sdk";
import { MONO_FONT, StepFooter } from "./Wizard";

interface Op {
  kind: string;
  name: string;
  detail: string;
}

interface Trigger {
  name: string;
  agentRef: string;
  connectorRef: string;
  filters: any[];
}

interface Verdict {
  blocking: string[];
  warnings: string[];
}

interface Plan {
  create: Op[];
  change: Op[];
  delete: Op[];
  triggers: Trigger[];
  verdict: Verdict;
}

// One row of the plan's table: an op with the sign the plan gives it. The three lists are
// read into one table, so the sign is what says which list a row came from.
interface OpRow extends Op {
  sign: "+" | "~" | "−";
  // The colour of the sign: create is additive (ok), change is a warn, delete is destructive (err).
  color: string;
}

const OP_COLUMNS = [
  {
    key: "op",
    header: "",
    cell: (row: OpRow) => (
      <span style={{ fontFamily: MONO_FONT, fontWeight: 600, fontSize: "0.8125rem", color: row.color }}>
        {row.sign}
      </span>
    ),
  },
  { key: "kind", header: "Kind", cell: (row: OpRow) => <span style={{ color: "var(--ink-2)" }}>{row.kind}</span> },
  {
    key: "name",
    header: "Name",
    cell: (row: OpRow) => <span style={{ fontFamily: MONO_FONT, color: "var(--ink)" }}>{row.name}</span>,
  },
  { key: "detail", header: "Detail", cell: (row: OpRow) => <span style={{ color: "var(--ink-2)" }}>{row.detail}</span> },
];

const TRIGGER_COLUMNS = [
  { key: "name", header: "Trigger", cell: (trig: Trigger) => <span style={{ fontFamily: MONO_FONT }}>{trig.name}</span> },
  { key: "agent", header: "Agent", cell: (trig: Trigger) => <span style={{ fontFamily: MONO_FONT }}>{trig.agentRef}</span> },
];

// A plan can name the same kind twice (three agents, three images), so the key is the
// whole row rather than the two fields that repeat.
function opKey(op: OpRow): string {
  return `${op.sign}:${op.kind}:${op.name}:${op.detail}`;
}

// The count on the apply button: every op the plan would make, change or delete.
function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

interface PreviewProps {
  plan: Plan;
  // Whether an apply is in flight. Apply is not idempotent — it creates the whole install —
  // so the screen takes the state and keeps the button out of reach for its duration.
  applying?: boolean;
  onApply: () => void;
  onBack: () => void;
}

export function Preview({ plan, applying = false, onApply, onBack }: PreviewProps) {
  const hasBlocking = plan.verdict.blocking.length > 0;
  const total = plan.create.length + plan.change.length + plan.delete.length;

  // The three lists as one table, in the order the plan lists them: what is made, what is
  // changed, what is removed.
  const rows: OpRow[] = [
    ...plan.create.map((op) => ({ ...op, sign: "+" as const, color: "var(--ok)" })),
    ...plan.change.map((op) => ({ ...op, sign: "~" as const, color: "var(--warn)" })),
    ...plan.delete.map((op) => ({ ...op, sign: "−" as const, color: "var(--err)" })),
  ];

  return (
    <div className="forgejo-preview" style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      {/* The shape of the plan in five counts. Blocking and warnings are green and quiet when there
          are none, so an empty count does not look like something to fix. */}
      <div style={{ display: "flex", flexWrap: "wrap", gap: "0.5rem" }}>
        <Tag variant="ok">{`+${plan.create.length} to create`}</Tag>
        <Tag>{`~${plan.change.length} to change`}</Tag>
        <Tag variant={plan.delete.length > 0 ? "err" : "default"}>{`−${plan.delete.length} to delete`}</Tag>
        <Tag variant={hasBlocking ? "err" : "ok"}>{`${plan.verdict.blocking.length} blocking`}</Tag>
        <Tag variant={plan.verdict.warnings.length > 0 ? "warn" : "default"}>
          {`${plan.verdict.warnings.length} ${plan.verdict.warnings.length === 1 ? "warning" : "warnings"}`}
        </Tag>
      </div>

      {plan.verdict.blocking.map((msg) => (
        <Notice key={msg} variant="err">
          {msg}
        </Notice>
      ))}

      {plan.verdict.warnings.map((msg) => (
        <Notice key={msg} variant="warn">
          {msg}
        </Notice>
      ))}

      <div style={{ overflowX: "auto" }}>
        <RegisterTable
          rows={rows}
          columns={OP_COLUMNS}
          rowKey={opKey}
          emptyLabel="The plan changes nothing."
          withRowNumbers={false}
        />
      </div>

      {plan.triggers.length > 0 && (
        <div className="forgejo-preview-section" style={{ display: "flex", flexDirection: "column", gap: "0.5rem" }}>
          <h5 style={{ margin: 0, fontSize: "0.8125rem", fontWeight: 600, color: "var(--ink-2)" }}>Triggers</h5>
          <div style={{ overflowX: "auto" }}>
            <RegisterTable
              rows={plan.triggers}
              columns={TRIGGER_COLUMNS}
              rowKey={(trig: Trigger) => trig.name}
              withRowNumbers={false}
            />
          </div>
        </div>
      )}

      {/* Apply stays on the screen while the plan blocks it, out of reach: the admin sees the
          button the plan will enable, and the blocking lines say what to fix to get there. */}
      <StepFooter onBack={onBack}>
        <Button
          variant="primary"
          onClick={onApply}
          disabled={hasBlocking || applying}
          className="forgejo-apply-btn"
        >
          {applying ? "Applying…" : `Apply ${plural(total, "change")}`}
        </Button>
      </StepFooter>
    </div>
  );
}
