// useReconcile owns the "Reconcile now" answer: the pass in flight, the summary it
// left, the failure to get one, and the moment the last pass finished — the only
// thing on this page that lets the header say when the installs were last seen in
// sync, because the page cannot know it otherwise.

import { useState } from "react";
import type { HttpClient } from "@ainsel/plugin-sdk";
import { asApiError, reconcileSettings } from "../api";
import type { ReconcileSummary } from "../api";

export function useReconcile(api: HttpClient) {
  // The last "Reconcile now" answer, and any failure to get one. Both clear when
  // a new pass starts — the admin is asking again, the old answer is stale.
  // checkedAt remembers the last pass that finished, so the header can say when
  // the page last saw the installs in sync; it is absent until a pass has run
  // from this page, because the page cannot know otherwise.
  const [summary, setSummary] = useState<ReconcileSummary | null>(null);
  const [reconcileError, setReconcileError] = useState<string | null>(null);
  const [reconciling, setReconciling] = useState(false);
  const [checkedAt, setCheckedAt] = useState<{
    at: Date;
    refused: number;
    llmFlagged: number;
  } | null>(null);

  const handleReconcile = async () => {
    setReconciling(true);
    setSummary(null);
    setReconcileError(null);
    try {
      const res = await reconcileSettings(api);
      const next = {
        updated: res.updated ?? [],
        refused: res.refused ?? [],
        // An answer from an older plugin build carries no check list; reading it as
        // empty is honest — nothing was checked — and one shape keeps the render simple.
        llmChecks: res.llmChecks ?? [],
      };
      setSummary(next);
      setCheckedAt({
        at: new Date(),
        refused: next.refused.length,
        llmFlagged: next.llmChecks.filter((c) => c.state !== "ok").length,
      });
    } catch (err) {
      setReconcileError(asApiError(err).message || "Failed to reconcile.");
    } finally {
      setReconciling(false);
    }
  };

  // dismiss clears both halves of the last answer: the admin has read it.
  const dismiss = () => {
    setSummary(null);
    setReconcileError(null);
  };

  return { summary, reconcileError, reconciling, checkedAt, handleReconcile, dismiss };
}
