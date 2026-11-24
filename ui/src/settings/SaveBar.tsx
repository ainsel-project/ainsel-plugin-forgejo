// The sticky bar over the page's edits. It counts what is genuinely unsaved — the
// drafts, not the fields touched — and says where saved changes go next, because
// nothing here reaches an agent until a reconcile runs.

import { Button, Notice } from "@ainsel/plugin-sdk";

export function SaveBar({
  batchError,
  pendingCount,
  saving,
  handleSave,
  handleDiscard,
}: {
  batchError: string | null;
  pendingCount: number;
  saving: boolean;
  handleSave: () => void;
  handleDiscard: () => void;
}) {
  return (
    <>
      {batchError && <Notice variant="err">{batchError}</Notice>}
      {pendingCount > 0 && (
        <div
          role="region"
          aria-label="Unsaved changes"
          style={{
            position: "sticky",
            bottom: 16,
            display: "flex",
            flexWrap: "wrap",
            justifyContent: "space-between",
            alignItems: "center",
            gap: 12,
            padding: "12px 16px",
            background: "var(--paper-2)",
            border: "1px solid var(--rule-soft)",
            borderRadius: "var(--radius)",
          }}
        >
          <span style={{ fontSize: 14, color: "var(--ink)" }}>
            <b style={{ fontWeight: 600 }}>{`${pendingCount} unsaved ${pendingCount === 1 ? "change" : "changes"}`}</b>{" "}
            · saved changes reach the agents on the next reconcile
          </span>
          <span style={{ display: "flex", gap: 8 }}>
            <Button onClick={handleDiscard} disabled={saving} className="forgejo-discard-btn">
              Discard
            </Button>
            <Button onClick={handleSave} disabled={saving} className="forgejo-save-btn">
              {saving ? "Saving…" : "Save changes"}
            </Button>
          </span>
        </div>
      )}
    </>
  );
}
