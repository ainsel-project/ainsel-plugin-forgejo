// useSettingsDrafts owns the registry and everything typed over it: the loaded
// rows, the drafts behind the save bar, the per-field refusals, and the three
// writes the page can make — one batch of edits, one reset, and the discard that
// only throws local state away. The page renders what it answers; no component
// under it writes to the backend on its own.

import { useEffect, useState } from "react";
import type { HttpClient } from "@ainsel/plugin-sdk";
import { asApiError, fetchSettings, putSettings } from "../api";
import type { Setting, SettingsEdit } from "../api";
import { key, without } from "./model";
import type { Draft } from "./model";

export function useSettingsDrafts(api: HttpClient) {
  const [settings, setSettings] = useState<Setting[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // What the admin has typed but not saved, per (role, field). The server's
  // value is what renders until a draft exists for a field. A draft typed back
  // to the saved value is dropped, so the bar's count is the real change count.
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  // The last refusal per field: from a refused batch (each named field), or from
  // a reset that failed.
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  // A refused batch that named no field, or a save that could not be sent.
  const [batchError, setBatchError] = useState<string | null>(null);
  // The batch PUT in flight. The bar's buttons are out of reach for its duration,
  // so a second save cannot ask the backend to write the same drafts twice.
  const [saving, setSaving] = useState(false);
  // Which (role, field) has a reset in flight. Resets are immediate, so this is
  // the one per-field busy state.
  const [busyKey, setBusyKey] = useState<string | null>(null);
  // Which "Update available" boxes have their side-by-side open.
  const [comparing, setComparing] = useState<Record<string, boolean>>({});

  useEffect(() => {
    const load = async () => {
      try {
        const res = await fetchSettings(api);
        setSettings(res.settings ?? []);
        setLoading(false);
      } catch (err) {
        const apiErr = asApiError(err);
        if (apiErr.status === 401 || apiErr.status === 403) {
          setError("Only admins can access the settings.");
        } else {
          setError(apiErr.message || "Failed to load settings.");
        }
        setLoading(false);
      }
    };
    load();
  }, [api]);

  // edit records a typed value as a draft. Typing the saved value back is not a
  // change, so the draft goes; any refusal the field carried goes with the edit,
  // since the value it named is no longer the one on screen.
  const edit = (s: Setting, value: string) => {
    const k = key(s.role, s.field);
    setDrafts((d) => {
      if (value === s.value) return without(d, k);
      return { ...d, [k]: { role: s.role, field: s.field, value } };
    });
    setFieldErrors((e) => without(e, k));
  };

  // handleSave sends every draft as one PUT. On success the answer is the new
  // registry and the drafts it wrote retire; a draft the admin typed while the
  // call was in flight stays, because its value was not in the request.
  const handleSave = async () => {
    const sent = drafts;
    const edits: SettingsEdit[] = Object.values(sent).map((d) => ({
      role: d.role,
      field: d.field,
      value: d.value,
    }));
    if (edits.length === 0) return;
    setSaving(true);
    setBatchError(null);
    setFieldErrors({});
    try {
      const res = await putSettings(api, edits);
      setSettings(res.settings ?? []);
      setDrafts((d) => {
        const next = { ...d };
        for (const [k, s] of Object.entries(sent)) {
          if (next[k]?.value === s.value) delete next[k];
        }
        return next;
      });
      // A save can land and still leave a key behind: a key's storage is a write
      // onto the role's agents, and an agent that refused it kept its old key.
      // Nothing retries it later — the plugin kept no copy to retry from — so the
      // field says so, in the same place a refusal does.
      const landed: unknown = res.fields;
      if (Array.isArray(landed) && landed.length > 0) {
        const next: Record<string, string> = {};
        for (const f of landed) {
          const fe = f as { role?: string; field?: string; error?: string };
          if (fe.role && fe.field) {
            next[key(fe.role, fe.field)] = fe.error || "did not reach the agents";
          }
        }
        setFieldErrors(next);
      }
    } catch (err) {
      // A refused batch is answered with each bad field named (the PUT either
      // lands whole or not at all); the console's client puts the parsed body
      // on the error's `body`. The drafts stay exactly where they were.
      const fields: unknown = asApiError(err).body?.fields;
      if (Array.isArray(fields) && fields.length > 0) {
        const next: Record<string, string> = {};
        for (const f of fields) {
          const fe = f as { role?: string; field?: string; error?: string };
          if (fe.role && fe.field) {
            next[key(fe.role, fe.field)] = fe.error || "refused";
          }
        }
        setFieldErrors(next);
      } else {
        setBatchError(asApiError(err).message || "Failed to save.");
      }
    } finally {
      setSaving(false);
    }
  };

  const handleDiscard = () => {
    setDrafts({});
    setFieldErrors({});
    setBatchError(null);
  };

  // handleReset re-adopts a field's shipped default, immediately. Any draft the
  // admin was typing over the field goes with it: the field now says "the
  // default", which is what the reset was asked to do.
  const handleReset = async (s: Setting) => {
    const k = key(s.role, s.field);
    setBusyKey(k);
    try {
      const res = await putSettings(api, [{ role: s.role, field: s.field, reset: true }]);
      setSettings(res.settings ?? []);
      setDrafts((d) => without(d, k));
      setFieldErrors((e) => without(e, k));
      setComparing((c) => without(c, k));
    } catch (err) {
      const message = asApiError(err).message;
      setFieldErrors((e) => ({ ...e, [k]: message || "Failed to reset." }));
    } finally {
      setBusyKey(null);
    }
  };

  return {
    settings,
    loading,
    error,
    drafts,
    fieldErrors,
    batchError,
    saving,
    busyKey,
    comparing,
    setComparing,
    edit,
    handleSave,
    handleDiscard,
    handleReset,
  };
}
