/**
 * SettingsPage is the settings console: the role defaults, the Forgejo instances an
 * install may be created against, and the resources the plugin manifest manages.
 *
 * Each of its sections answers two questions at once, and the answers are not
 * independent. What the console lets you edit here is what the backend declares —
 * the registry names each field, its kind, its default, whether a change needs a
 * restart or a reconcile, and whether an update can be offered. The page holds none
 * of that: it renders whatever /settings answers with, so a field added at the
 * server appears here without a change to this file. A field the registry answers
 * with but this page has no card for is not lost — it lands in the "Other" card,
 * where it reads, edits and saves like the rest.
 *
 * What saving a default means downstream: every install carries the role defaults,
 * and a reconcile pass is what carries a saved value onto the agents and personas —
 * the same pass that heals drift, and the same pass that reads an agent's own file
 * before changing it and refuses to overwrite an operator's hand edit. So an edit
 * here is a request, not a write: it reaches the fleet on the next reconcile, and it
 * can be refused there in ways this page cannot anticipate. That is why a row can
 * carry a refusal, why the summary lists refusals beside changes, and why the field
 * still shows what was asked for after a refusal rather than quietly snapping back
 * to the server's value. The answer's `dirty` flag is what this page calls "stale":
 * the install has moved away from the role default, and the row says so.
 *
 * Two kinds of field are not plain text, and neither is edited by a box alone.
 * An enum is a select over the values the registry lists — the page never types a
 * model name by hand, because a typo at the top of a cascade is a fleet of agents
 * that fail hours later. An "Update available" field is an offer: the registry knows
 * the newest value in its model catalog and which are stable, and the row puts the
 * current value, the newest, and the stable list on screen together — because the
 * honest question is never only "is there something newer?" but "is the newer thing
 * one the project actually recommends?". Confirming an update sets exactly that
 * value; it is a choice the page makes visible, not a default it applies.
 *
 * A row whose default is empty is not a field that wants filling in. Every role
 * default here is a fallback, and the role can reach a value by another route: the
 * task it is spawned on, or its own definition in the plan. Those routes are decided
 * per install and per task, so whether a blank field matters is a question about the
 * install, not about the field — and this page cannot see the installs. It is the
 * pass that runs there, against each one, that names a field it could not resolve.
 * So a blank default here stays blank, and says what blank means, rather than being
 * filled with a value the page has no way to justify. The plugin's own model is the
 * one field where that argument does not hold, because that value is a constant this
 * plugin ships — and its row says which of the two cases it is in.
 *
 * The pieces live under ./settings: ./model for the registry's cards and the shared
 * styles, one hook per collection the page writes to, and the components the page
 * composes — a row, a role's cards, the instances list, the nav, the reconcile
 * notice and the save bar.
 */

import { useState } from "react";
import { Button, Notice, Panel, usePluginContext } from "@ainsel/plugin-sdk";
import type { RoleKey } from "./settings/model";
import { pageStyles, runningVersion } from "./settings/model";
import { useSettingsDrafts } from "./settings/useSettingsDrafts";
import { useInstances } from "./settings/useInstances";
import { useReconcile } from "./settings/useReconcile";
import { RoleCards } from "./settings/RoleCards";
import { ManagedResources } from "./settings/ManagedResources";
import { InstancesPanel } from "./settings/InstancesPanel";
import { SettingsNav } from "./settings/SettingsNav";
import { ReconcileNotice, SyncTag } from "./settings/ReconcileNotice";
import { SaveBar } from "./settings/SaveBar";

export function SettingsPage() {
  const { api } = usePluginContext();

  // One hook per collection the page writes to. The role defaults are one batch —
  // every draft goes in one PUT and a refusal names the field it refused — while
  // the instances are written whole, and a reconcile is a pass the page asks for
  // rather than a value it stores.
  const draftState = useSettingsDrafts(api);
  const instanceState = useInstances(api);
  const reconcile = useReconcile(api);

  const [pane, setPane] = useState<"role" | "resources" | "instances">("role");
  const [currentRole, setCurrentRole] = useState<RoleKey>("refiner");

  const { settings, loading, error, drafts, fieldErrors, batchError, saving, busyKey, comparing, edit, handleSave } =
    draftState;

  if (loading) {
    return <Panel className="forgejo-loading">Loading...</Panel>;
  }

  if (error || !settings) {
    return (
      <Panel className="forgejo-error">
        <Notice variant="err">{error || "Could not load settings."}</Notice>
      </Panel>
    );
  }

  const version = runningVersion(settings);
  const pendingCount = Object.keys(drafts).length;
  const dirtyCount = (role: string) => settings.filter((s) => s.role === role && s.dirty).length;
  const roleSettings = settings.filter((s) => s.role === currentRole);

  return (
    <Panel
      className="forgejo-settings"
      title="Settings"
      right={
        <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 10 }}>
          <SyncTag checkedAt={reconcile.checkedAt} />
          <Button
            onClick={reconcile.handleReconcile}
            disabled={reconcile.reconciling}
            className="forgejo-reconcile-btn"
          >
            {reconcile.reconciling ? "Reconciling…" : "Reconcile now"}
          </Button>
        </div>
      }
    >
      <style>{pageStyles}</style>
      <p style={{ margin: 0, fontSize: 14, color: "var(--ink-2)", maxWidth: 640 }}>
        Role defaults for every Forgejo install. Saved changes are pushed to the agents and personas on the next
        reconcile.
      </p>

      <ReconcileNotice
        reconcileError={reconcile.reconcileError}
        summary={reconcile.summary}
        dismiss={reconcile.dismiss}
      />

      <div style={{ display: "flex", flexWrap: "wrap", gap: 20, alignItems: "flex-start" }}>
        <SettingsNav
          pane={pane}
          currentRole={currentRole}
          dirtyCount={dirtyCount}
          instances={instanceState.instances}
          setPane={setPane}
          setCurrentRole={setCurrentRole}
        />

        <div style={{ flex: "999 1 560px", minWidth: 0, display: "flex", flexDirection: "column", gap: 16 }}>
          {pane === "role" ? (
            <RoleCards
              roleKey={currentRole}
              roleSettings={roleSettings}
              version={version}
              drafts={drafts}
              fieldErrors={fieldErrors}
              busyKey={busyKey}
              comparing={comparing}
              saving={saving}
              edit={edit}
              handleReset={draftState.handleReset}
              setComparing={draftState.setComparing}
            />
          ) : pane === "instances" ? (
            <InstancesPanel {...instanceState} />
          ) : (
            <ManagedResources version={version} />
          )}
        </div>
      </div>

      <SaveBar
        batchError={batchError}
        pendingCount={pendingCount}
        saving={saving}
        handleSave={handleSave}
        handleDiscard={draftState.handleDiscard}
      />
    </Panel>
  );
}
