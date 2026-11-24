import { Button, Notice, Tag } from "@ainsel/plugin-sdk";
import type { ReconcileSummary } from "../api";
import { changeLine, clock } from "./model";

// The header's status: only what this page has seen. Before a pass runs from here
// there is no status to show, so the tag is left out. A flagged LLM check —
// unreachable or unknown — is as much "not in sync" as a refused write: the button's
// whole promise is that it says so when the configured provider cannot serve, or
// could not be asked at all.
export function SyncTag({ checkedAt }: { checkedAt: { at: Date; refused: number; llmFlagged: number } | null }) {
  // The header's status: only what this page has seen. Before a pass runs from
  // here there is no status to show, so the tag is left out. A flagged LLM check
  // — unreachable or unknown — is as much "not in sync" as a refused write: the
  // button's whole promise is that it says so when the configured provider cannot
  // serve, or could not be asked at all.
  return checkedAt ? (
    <Tag variant={checkedAt.refused + checkedAt.llmFlagged > 0 ? "warn" : "ok"}>
      {checkedAt.refused + checkedAt.llmFlagged > 0
        ? `${checkedAt.refused + checkedAt.llmFlagged} flagged · checked ${clock(checkedAt.at)}`
        : `In sync · checked ${clock(checkedAt.at)}`}
    </Tag>
  ) : null;
}

export function ReconcileNotice({
  reconcileError,
  summary,
  dismiss,
}: {
  reconcileError: string | null;
  summary: ReconcileSummary | null;
  dismiss: () => void;
}) {
  return (
    <>
      {reconcileError && <Notice variant="err">{reconcileError}</Notice>}
      {summary && (
        <Notice
          variant={summary.refused.length > 0 || summary.llmChecks.some((c) => c.state !== "ok") ? "warn" : "info"}
        >
          <div
            style={{
              display: "flex",
              flexWrap: "wrap",
              gap: 12,
              justifyContent: "space-between",
              alignItems: "flex-start",
            }}
          >
            <div style={{ display: "flex", flexDirection: "column", gap: 6, minWidth: 0 }}>
              {summary.updated.length === 0 &&
              summary.refused.length === 0 &&
              summary.llmChecks.every((c) => c.state === "ok") ? (
                <div>Checked: nothing to fix.</div>
              ) : (
                <>
                  <div>{`Reconciled: ${summary.updated.length} updated, ${summary.refused.length} refused`}</div>
                  {summary.updated.length > 0 && (
                    <ul style={{ margin: 0 }}>
                      {summary.updated.map((c, i) => (
                        <li key={`${i}`}>
                          {changeLine(c)}: {c.from ? `${c.from} → ` : ""}
                          {c.to}
                        </li>
                      ))}
                    </ul>
                  )}
                  {summary.refused.length > 0 && (
                    <ul style={{ margin: 0 }}>
                      {summary.refused.map((r, i) => (
                        <li key={`${i}`}>
                          {changeLine(r)}: {r.error}
                        </li>
                      ))}
                    </ul>
                  )}
                  {summary.llmChecks.length > 0 && (
                    <>
                      {/* The lists above answer "what did the pass fix"; this one
                          answers a different question, and says so — otherwise its
                          lines read as more drift entries. */}
                      <div>
                        <strong>LLM checks</strong>
                      </div>
                      <ul style={{ margin: 0 }}>
                        {summary.llmChecks.map((c, i) => (
                          <li key={`${i}`}>
                            {`${c.role}: ${c.model} @${c.provider} — ${c.state}`}
                            {c.reason ? `: ${c.reason}` : ""}
                          </li>
                        ))}
                      </ul>
                    </>
                  )}
                </>
              )}
            </div>
            <Button
              onClick={() => {
                dismiss();
              }}
              className="forgejo-dismiss-btn"
            >
              Dismiss
            </Button>
          </div>
        </Notice>
      )}
    </>
  );
}
