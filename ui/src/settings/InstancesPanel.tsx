// The Forgejo addresses an install may be created against. The setup wizard's
// Instance step selects over this list and the plan refuses an address outside it,
// so an install cannot point at a host nobody configured. The rows are the draft
// when one is open, so an address half-typed is visible rather than held.

import { Button, Input, Notice } from "@ainsel/plugin-sdk";
import type { ChangeEvent } from "react";
import type { ForgejoInstance } from "../api";
import { cardStyle } from "./model";

export function InstancesPanel({
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
}: {
  instances: ForgejoInstance[];
  draftInstances: ForgejoInstance[];
  instancePending: boolean;
  instanceErrors: Record<string, string>;
  instanceError: string | null;
  instanceSaving: boolean;
  editInstance: (i: number, patch: Partial<ForgejoInstance>) => void;
  addInstance: () => void;
  removeInstance: (i: number) => void;
  saveInstances: () => void;
  discardInstances: () => void;
}) {
  // instancesView is the list of Forgejo addresses this plugin may be installed
  // against. The setup wizard's Instance step selects over it, and the plan
  // refuses an address outside it, so an install cannot point at a host nobody
  // configured. The list is written whole — the same all-or-nothing rule the
  // settings batch follows — and its order is the order the wizard draws.
  return (
    <section style={{ ...cardStyle, overflow: "hidden" }}>
      <div style={{ padding: "16px 20px" }}>
        <h2 style={{ margin: 0, fontSize: 18, fontWeight: 600 }}>Forgejo instances</h2>
        <p style={{ margin: "4px 0 0", fontSize: 13, color: "var(--ink-2)", maxWidth: 640 }}>
          The Forgejo addresses an install may be created against. The setup wizard picks from this list, and a plan
          that names an address outside it is refused. An address is stored without its trailing slash; the same address
          twice is one instance, not two.
        </p>
        {instances.length === 0 && !instancePending && (
          <Notice variant="warn">
            No instance is configured, so the setup wizard has nothing to install against. Add the address of your
            Forgejo here first.
          </Notice>
        )}
        {instanceError && <Notice variant="err">{instanceError}</Notice>}
      </div>

      {draftInstances.map((inst, i) => (
        <div
          key={i}
          data-instance-row={i}
          style={{
            display: "flex",
            flexWrap: "wrap",
            gap: "12px 16px",
            alignItems: "flex-start",
            padding: "16px 20px",
            borderTop: "1px solid var(--rule-soft)",
          }}
        >
          <div style={{ flex: "1 1 160px", minWidth: 0, display: "flex", flexDirection: "column", gap: 6 }}>
            <Input
              aria-label={`Instance ${i + 1} name`}
              value={inst.name}
              onChange={(e: ChangeEvent<HTMLInputElement>) => editInstance(i, { name: e.target.value })}
              placeholder="e.g., Primary"
            />
            <Input
              aria-label={`Instance ${i + 1} base URL`}
              className="forgejo-settings-mono"
              value={inst.baseUrl}
              onChange={(e: ChangeEvent<HTMLInputElement>) => editInstance(i, { baseUrl: e.target.value })}
              placeholder="https://forgejo.example.com"
            />
            {instanceErrors[`instances[${i}]`] && (
              <span style={{ fontSize: 12, color: "var(--err, var(--warn))" }}>
                {instanceErrors[`instances[${i}]`]}
              </span>
            )}
          </div>
          <div style={{ flex: "0 0 auto" }}>
            <Button onClick={() => removeInstance(i)} disabled={instanceSaving} className="forgejo-instance-remove-btn">
              Remove
            </Button>
          </div>
        </div>
      ))}

      <div
        style={{
          display: "flex",
          flexWrap: "wrap",
          gap: 8,
          padding: "16px 20px",
          borderTop: "1px solid var(--rule-soft)",
        }}
      >
        <Button onClick={addInstance} disabled={instanceSaving} className="forgejo-instance-add-btn">
          Add instance
        </Button>
        <Button
          onClick={saveInstances}
          disabled={instanceSaving || !instancePending}
          className="forgejo-instance-save-btn"
        >
          {instanceSaving ? "Saving…" : "Save instances"}
        </Button>
        {instancePending && (
          <Button onClick={discardInstances} disabled={instanceSaving} className="forgejo-instance-discard-btn">
            Discard
          </Button>
        )}
      </div>
    </section>
  );
}
