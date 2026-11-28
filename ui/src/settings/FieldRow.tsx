// One settings row: the label and its help with the state tags, the control the
// field's answer asks for, and under it whichever hint the field's state earns —
// the stale box with its side-by-side, or the one-line default with its reset.
// It owns no state: the draft, the refusal, the busy flag and the open compare are
// the page's, because the save that writes them is a batch across every row.

import { Button, Input, Select, Tag, Textarea } from "@ainsel/plugin-sdk";
import { CardGrid } from "../layout";
import type { ChangeEvent } from "react";
import type { Setting } from "../api";
import { key, MONO, preStyle, truncate } from "./model";
import type { Draft, FieldSpec } from "./model";

// renderField draws one row: label and help with the state tags on the left,
// the control on the right, and under it whichever hint the field's state asks
// for — the stale box, or the one-line default with its reset.
export function FieldRow({
  spec,
  s,
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
  spec: FieldSpec;
  s: Setting;
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
  const k = key(s.role, s.field);
  const draft = drafts[k];
  const value = draft ? draft.value : s.value;
  const unsaved = draft !== undefined;
  // A key row is never an edited one: the plugin keeps no key, so there is no
  // edit here to have made. The flag is read off the spec rather than trusted
  // from the answer, so a stale row from an older version — one that still
  // carries the dirty flag a stored key used to set — cannot show a reset for a
  // value nothing here holds.
  const dirty = s.dirty && !spec.secret;
  const overridden = dirty;
  // The upgrade hint (design decision 4): this edit was made away from an older
  // shipped default, and the running plugin ships a newer one. Untouched stale
  // fields need no hint — they were auto-adopted at startup already, which is why
  // their defaultVersion reads as the running version.
  const stale = dirty && version !== "" && s.defaultVersion !== version;
  const id = `settings-${s.role}-${s.field}`;
  const error = fieldErrors[k];
  const busy = busyKey === k;
  const open = !!comparing[k];

  const control =
    s.options && s.options.length > 0 ? (
      <Select id={id} value={value} onChange={(next: string) => edit(s, next)} options={s.options} />
    ) : spec.multiline ? (
      <Textarea
        id={id}
        value={value}
        onChange={(e: ChangeEvent<HTMLTextAreaElement>) => edit(s, e.target.value)}
        placeholder={spec.placeholder}
        className={spec.mono ? "forgejo-settings-mono" : undefined}
      />
    ) : (
      <Input
        id={id}
        type={spec.secret ? "password" : "text"}
        value={value}
        onChange={(e: ChangeEvent<HTMLInputElement>) => edit(s, e.target.value)}
        placeholder={spec.placeholder}
        className={spec.mono ? "forgejo-settings-mono" : undefined}
      />
    );

  return (
    <div
      key={s.field}
      data-field={s.field}
      style={{
        display: "flex",
        flexWrap: "wrap",
        gap: "12px 24px",
        padding: "18px 20px",
        borderTop: "1px solid var(--rule-soft)",
      }}
    >
      <div
        style={{
          flex: "1 1 220px",
          maxWidth: 280,
          display: "flex",
          flexDirection: "column",
          gap: 6,
        }}
      >
        <label htmlFor={id} style={{ fontSize: 14, fontWeight: 500, color: "var(--ink)" }}>
          {spec.label}
        </label>
        {spec.help && <span style={{ fontSize: 12, color: "var(--ink-2)", lineHeight: "18px" }}>{spec.help}</span>}
        <span style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 6 }}>
          <Tag>{overridden ? "Overridden" : "Default"}</Tag>
          {unsaved && <Tag variant="warn">Unsaved</Tag>}
        </span>
      </div>

      <div
        style={{
          flex: "999 1 320px",
          minWidth: 0,
          display: "flex",
          flexDirection: "column",
          gap: 8,
        }}
      >
        {control}
        {error && (
          <div role="alert" style={{ fontSize: 12, color: "var(--err)" }}>
            {error}
          </div>
        )}

        {stale && (
          <div
            role="group"
            aria-label={`${spec.label}: update available`}
            style={{
              border: "1px solid var(--warn)",
              background: "var(--warn-haze)",
              borderRadius: "var(--radius-sm)",
              padding: "12px 14px",
              display: "flex",
              flexDirection: "column",
              gap: 10,
            }}
          >
            <div
              style={{
                display: "flex",
                flexWrap: "wrap",
                gap: 8,
                alignItems: "center",
                justifyContent: "space-between",
              }}
            >
              <span style={{ fontSize: 13, color: "var(--warn-ink)" }}>
                <b style={{ fontWeight: 600 }}>Update available.</b>{" "}
                {`Plugin v${version} ships a new default for this field. Your edit, made on v${s.defaultVersion}, is kept until you adopt it.`}
              </span>
              <span style={{ display: "flex", gap: 6 }}>
                <Button onClick={() => setComparing((c) => ({ ...c, [k]: !open }))} className="forgejo-compare-btn">
                  {open ? "Hide comparison" : "Compare"}
                </Button>
                <Button onClick={() => handleReset(s)} disabled={busy || saving} className="forgejo-reset-btn">
                  Adopt new default
                </Button>
              </span>
            </div>
            {open && (
              <CardGrid minTrackWidth={240} maxColumns={2} gap={8}>
                <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
                  <span style={{ fontSize: 12, fontWeight: 600, color: "var(--ink-2)" }}>Your version</span>
                  <pre style={preStyle("color-mix(in srgb, var(--err) 10%, transparent)")}>{s.value}</pre>
                </div>
                <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
                  <span style={{ fontSize: 12, fontWeight: 600, color: "var(--ink-2)" }}>
                    {`Default in v${version}`}
                  </span>
                  <pre style={preStyle("color-mix(in srgb, var(--ok) 12%, transparent)")}>{s.default}</pre>
                </div>
              </CardGrid>
            )}
          </div>
        )}

        {overridden && !stale && (
          <span style={{ fontSize: 12, color: "var(--ink-2)" }}>
            Default:{" "}
            <span className="forgejo-settings-mono" title={s.default} style={{ fontFamily: MONO }}>
              {truncate(s.default)}
            </span>{" "}
            ·{" "}
            <Button onClick={() => handleReset(s)} disabled={busy || saving} className="forgejo-reset-btn">
              Reset to default
            </Button>
          </span>
        )}
      </div>
    </div>
  );
}
