// useInstances owns the list of Forgejo addresses an install may be created
// against. It is a second collection on the settings page, written whole: the PUT
// replaces every row, so the draft is the list rather than one field, and a
// refused write names each row it refused.

import { useEffect, useState } from "react";
import type { HttpClient } from "@ainsel/plugin-sdk";
import { asApiError, fetchInstances, putInstances } from "../api";
import type { ForgejoInstance } from "../api";
import { without } from "./model";

export function useInstances(api: HttpClient) {
  // The configured Forgejo instances, and the list the admin has arranged but
  // not saved. They are written whole — the same all-or-nothing rule the
  // settings batch follows — so the draft is the whole list, not one row, and
  // a refused write names the row and half it refused.
  const [instances, setInstances] = useState<ForgejoInstance[]>([]);
  const [instanceDraft, setInstanceDraft] = useState<ForgejoInstance[] | null>(null);
  const [instanceErrors, setInstanceErrors] = useState<Record<string, string>>({});
  const [instanceError, setInstanceError] = useState<string | null>(null);
  const [instanceSaving, setInstanceSaving] = useState(false);

  useEffect(() => {
    // The instance list loads beside the settings: it is a second collection on
    // the same page, and the wizard's Instance step reads it, so the admin who
    // lands here to add one should see it without a second trip.
    fetchInstances(api)
      .then((res) => setInstances(res.instances ?? []))
      .catch(() => setInstances([]));
  }, [api]);

  const draftInstances = instanceDraft ?? instances;
  const instancePending = instanceDraft !== null && JSON.stringify(instanceDraft) !== JSON.stringify(instances);

  const editInstance = (i: number, patch: Partial<ForgejoInstance>) => {
    setInstanceDraft((d) => {
      const list = [...(d ?? instances)];
      list[i] = { ...list[i], ...patch };
      return list;
    });
    // The row's refusal was about the value that is no longer on screen.
    setInstanceErrors((e) => without(e, `instances[${i}]`));
  };

  const addInstance = () => {
    setInstanceDraft([...draftInstances, { name: "", baseUrl: "" }]);
  };

  const removeInstance = (i: number) => {
    setInstanceDraft(draftInstances.filter((_, j) => j !== i));
    setInstanceErrors({});
  };

  const saveInstances = async () => {
    setInstanceSaving(true);
    setInstanceError(null);
    try {
      const res = await putInstances(api, draftInstances);
      const stored = res.instances ?? [];
      setInstances(stored);
      setInstanceDraft(null);
      setInstanceErrors({});
    } catch (err) {
      // A refused list names each bad row, the way a refused batch names each bad
      // field. The draft stays where the admin left it, and each row says why.
      const fields: unknown = asApiError(err).body?.fields;
      if (Array.isArray(fields) && fields.length > 0) {
        const next: Record<string, string> = {};
        for (const f of fields) {
          const fe = f as { field?: string; error?: string };
          if (!fe.field) continue;
          // "instances[2].baseUrl" → the row it belongs to; a row with two bad
          // halves shows both.
          const m = /^instances\[(\d+)\]/.exec(fe.field);
          if (m) {
            const k = `instances[${m[1]}]`;
            next[k] = next[k] ? `${next[k]} · ${fe.error || "refused"}` : fe.error || "refused";
          }
        }
        setInstanceErrors(next);
      } else {
        setInstanceError(asApiError(err).message || "Failed to save the instances.");
      }
    } finally {
      setInstanceSaving(false);
    }
  };

  const discardInstances = () => {
    setInstanceDraft(null);
    setInstanceErrors({});
    setInstanceError(null);
  };

  return {
    instances,
    draftInstances,
    instancePending,
    instanceErrors,
    instanceError,
    instanceSaving,
    editInstance,
    addInstance,
    removeInstance,
    saveInstances,
    discardInstances,
  };
}
