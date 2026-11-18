import { Button, Notice, Panel, Field, Input, Select, Tag, RegisterTable, usePluginContext } from "@ainsel/plugin-sdk";
import { useState } from "react";
import type { ForgejoInstance, Group, Install, OrphanDeleteReport, OrphanPlan } from "../api";
import { deleteOrphans, planOrphans } from "../api";
import { formatRelative } from "../format";
import { COVERAGE_FAMILIES, deriveResults } from "./createdKinds";
import { ROLE_COLOR } from "./model";
import { MONO_FONT } from "./Wizard";

interface InstallsProps {
  installs: Install[];
  // The groups the options answer with, so a row can say which group owns the install by its
  // name rather than by the id the install stores. Empty by default: a row then shows the id.
  groups?: Group[];
  // The configured Forgejo instances, for the leftover sweep's instance select — the sweep
  // accepts only these, the way the wizard's plan does.
  instances?: ForgejoInstance[];
  // What the last removal left behind, if anything. Empty by default because most removals
  // finish and have nothing to say.
  notice?: string | null;
  // That the walk just finished installing an install, in the admin's words. Set when the done
  // screen closes, so the list says what happened rather than leaving the row's "waiting" as
  // the only word about a fresh install.
  installedNotice?: string | null;
  // Why the last removal could not run. The list is where the admin was standing and where
  // they will want to ask again, so the failure is shown here and the list stays usable.
  error?: string | null;
  onNewInstall: () => void;
  onEditInstall: (slug: string) => void;
  onConnectInstall: (slug: string) => void;
  onRemoveInstall: (slug: string, adminToken?: string) => Promise<void>;
}

// The three roles an install has, in the order the agents column draws their dots.
const ROLES = ["refiner", "developer", "reviewer"] as const;

// A row's webhook is verified or it is waiting for its first event. Waiting is the normal state
// of a fresh install, so it is a warn Tag rather than an error: the install is complete, only the
// first delivery has not come in yet.
function WebhookCell({ install }: { install: Install }) {
  if (install.webhookVerifiedAt) {
    return <Tag variant="ok">{`Verified ${formatRelative(install.webhookVerifiedAt, new Date())}`}</Tag>;
  }
  return <Tag variant="warn">Waiting for first event</Tag>;
}

// Which event families the org has actually delivered. The row's coverage is written by
// the background reconcile loop, so this reads "seen", never "live": it can lag a real
// delivery by up to one pass. A family not seen yet is drawn dashed, not as an error.
function EventsCell({ install }: { install: Install }) {
  const coverage = install.coverage ?? {};
  return (
    <span style={{ display: "flex", flexWrap: "wrap", gap: "0.375rem" }}>
      {COVERAGE_FAMILIES.map(({ key, label }) => {
        const seen = coverage[key] !== undefined;
        return (
          <span
            key={key}
            title={seen ? `${label}: seen` : `${label}: not seen yet`}
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: "0.25rem",
              padding: "0.125rem 0.5rem",
              borderRadius: "var(--radius-sm, 6px)",
              fontSize: "0.75rem",
              border: seen
                ? "1px solid color-mix(in srgb, var(--ok) 40%, transparent)"
                : "1px dashed var(--rule-soft)",
              background: seen ? "color-mix(in srgb, var(--ok) 8%, transparent)" : "var(--paper)",
              color: seen ? "var(--ok)" : "var(--ink-3)",
              whiteSpace: "nowrap",
            }}
          >
            <span aria-hidden="true">{seen ? "✓" : "–"}</span>
            {label}
          </span>
        );
      })}
    </span>
  );
}

// The columns an installs row has: what the install is, which group owns it, what agents it has,
// whether the webhook has proven itself, which events came in, and what the admin can do about it.
// Built per render because the actions cell closes over the screen's own handlers.
function columnsFor(
  handlers: {
    onEditInstall: (slug: string) => void;
    onConnectInstall: (slug: string) => void;
    onRemove: (slug: string) => void;
  },
  groups: Group[],
) {
  return [
    {
      key: "install",
      header: "Install",
      cell: (install: Install) => (
        <span style={{ display: "flex", flexDirection: "column", gap: "0.125rem" }}>
          <strong>{install.slug}</strong>
          <span style={{ fontFamily: MONO_FONT, fontSize: "0.75rem", color: "var(--ink-3)" }}>
            {`${install.baseUrl.replace(/^https?:\/\//, "")}/${install.org}`}
          </span>
        </span>
      ),
    },
    {
      key: "group",
      header: "Group",
      cell: (install: Install) => (
        <span style={{ color: "var(--ink-2)" }}>
          {groups.find((g) => g.id === install.groupId)?.name ?? install.groupId}
        </span>
      ),
    },
    {
      key: "agents",
      header: "Agents",
      cell: (install: Install) => {
        const count = deriveResults(install).agent ?? 0;
        return (
          <span style={{ display: "inline-flex", alignItems: "center", gap: "0.25rem", whiteSpace: "nowrap" }}>
            {ROLES.map((role) => (
              <span
                key={role}
                title={role.charAt(0).toUpperCase() + role.slice(1)}
                style={{ width: "0.5rem", height: "0.5rem", borderRadius: "999px", background: ROLE_COLOR[role], display: "inline-block" }}
              />
            ))}
            <span style={{ marginLeft: "0.375rem", color: "var(--ink-2)" }}>
              {count === 1 ? "1 agent" : `${count} agents`}
            </span>
          </span>
        );
      },
    },
    {
      key: "webhook",
      header: "Webhook",
      cell: (install: Install) => <WebhookCell install={install} />,
    },
    {
      key: "events",
      header: "Events received",
      cell: (install: Install) => <EventsCell install={install} />,
    },
    {
      key: "actions",
      header: "Actions",
      cell: (install: Install) => (
        <span style={{ display: "inline-flex", flexWrap: "wrap", justifyContent: "flex-end", gap: "0.375rem" }}>
          <Button
            onClick={() => handlers.onEditInstall(install.slug)}
            className="forgejo-edit-btn"
          >
            Edit
          </Button>
          {!install.webhookVerifiedAt && (
            <Button
              onClick={() => handlers.onConnectInstall(install.slug)}
              className="forgejo-connect-btn"
            >
              Connect
            </Button>
          )}
          <Button
            onClick={() => handlers.onRemove(install.slug)}
            className="forgejo-remove-btn"
          >
            Remove
          </Button>
        </span>
      ),
    },
  ];
}

// The leftover sweep. An install whose row is gone — a remove that ran without the
// admin token, or the fresh-start cutover — leaves its bot accounts, its teams and its
// org webhook in Forgejo with nothing in the plugin naming them. The sweep finds them
// again from the slug's naming convention and the hook's hub URL. Two steps, always in
// that order: the plan lists every candidate — a convention name can also be one a
// human chose by hand — and the delete only ever removes what the same derivation
// produces, server-side, from the slug the admin typed.
function OrphanCleanup({ instances }: { instances: ForgejoInstance[] }) {
  const { api } = usePluginContext();
  const [open, setOpen] = useState(false);
  const [slug, setSlug] = useState("");
  const [baseUrl, setBaseUrl] = useState(instances.length === 1 ? instances[0].baseUrl : "");
  const [org, setOrg] = useState("");
  const [adminToken, setAdminToken] = useState("");
  const [plan, setPlan] = useState<OrphanPlan | null>(null);
  const [report, setReport] = useState<OrphanDeleteReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  // Whether a sweep call is still running. The button is out of reach while it does,
  // so a double click cannot ask for the delete twice.
  const [busy, setBusy] = useState(false);

  const request = () => ({ baseUrl, org: org.trim(), adminToken });

  // Any edit to what the sweep looks for invalidates the plan on screen: the delete
  // re-derives the set from the form when it runs, and a stale list above a changed
  // form would be a list of something else.
  const edit = (setter: (v: string) => void) => (e: any) => {
    setter(e.target.value);
    setPlan(null);
    setReport(null);
  };

  const runPlan = async () => {
    if (busy) return;
    setBusy(true);
    setError(null);
    setReport(null);
    try {
      setPlan(await planOrphans(api, slug.trim(), request()));
    } catch (err: any) {
      setPlan(null);
      setError(err.message || "Failed to plan the cleanup.");
    } finally {
      setBusy(false);
    }
  };

  const runDelete = async () => {
    if (busy || !plan) return;
    setBusy(true);
    setError(null);
    try {
      setReport(await deleteOrphans(api, slug.trim(), request()));
      setPlan(null);
    } catch (err: any) {
      setError(err.message || "Failed to run the cleanup.");
    } finally {
      setBusy(false);
    }
  };

  const close = () => {
    if (busy) return;
    setOpen(false);
    setSlug("");
    setAdminToken("");
    setPlan(null);
    setReport(null);
    setError(null);
  };

  const canAsk = slug.trim() !== "" && baseUrl !== "" && org.trim() !== "" && adminToken !== "";

  return (
    <Panel title="Leftovers from a removed install">
      <p style={{ margin: "0 0 1rem", fontSize: "0.75rem", color: "var(--ink-3)", maxWidth: "44rem" }}>
        A removal that could not finish — or an install lost before its row was cleared —
        leaves its bot accounts, teams and webhook in Forgejo. This finds them again by the
        slug&apos;s naming convention and deletes them. It shows you every candidate first.
      </p>

      {!open ? (
        <Button onClick={() => setOpen(true)} className="forgejo-orphans-open-btn">
          Clean up leftovers
        </Button>
      ) : (
        <div
          className="forgejo-orphans-dialog"
          style={{ display: "flex", flexDirection: "column", gap: "0.75rem" }}
        >
          {error && <Notice variant="err">{error}</Notice>}

          <div style={{ display: "flex", flexWrap: "wrap", gap: "0.75rem" }}>
            <Field htmlFor="orphans-slug" label="Slug of the removed install">
              <Input
                id="orphans-slug"
                value={slug}
                onChange={edit(setSlug)}
                placeholder="the slug the install had"
              />
            </Field>
            <Field htmlFor="orphans-base" label="Forgejo instance">
              {instances.length === 0 ? (
                <p style={{ margin: 0, fontSize: "0.75rem", color: "var(--ink-3)" }}>
                  No instances configured — add the instance on the Settings page first.
                </p>
              ) : (
                <Select
                  id="orphans-base"
                  value={baseUrl}
                  onChange={(next: string) => {
                    setBaseUrl(next);
                    setPlan(null);
                    setReport(null);
                  }}
                  options={instances.map((i) => ({ value: i.baseUrl, label: `${i.name} — ${i.baseUrl}` }))}
                  emptyLabel="Select an instance"
                />
              )}
            </Field>
            <Field htmlFor="orphans-org" label="Organisation">
              <Input id="orphans-org" value={org} onChange={edit(setOrg)} placeholder="the org the bots lived in" />
            </Field>
            <Field htmlFor="orphans-token" label="Forgejo Admin Token" hint="Reads and deletes accounts, teams and webhooks.">
              <Input
                id="orphans-token"
                type="password"
                value={adminToken}
                onChange={edit(setAdminToken)}
                placeholder="Admin token"
              />
            </Field>
          </div>

          <div style={{ display: "flex", flexWrap: "wrap", gap: "0.5rem" }}>
            <Button
              onClick={runPlan}
              disabled={busy || !canAsk}
              className="forgejo-orphans-plan-btn"
            >
              Show what would be deleted
            </Button>
          </div>

          {plan && (
            <div className="forgejo-orphans-plan" style={{ display: "flex", flexDirection: "column", gap: "0.375rem" }}>
              {plan.items.length === 0 ? (
                <Notice variant="info">Nothing was found for this slug — there is nothing to delete.</Notice>
              ) : (
                <>
                  <Notice variant="warn">
                    {plan.items.length === 1
                      ? "One resource will be deleted:"
                      : `${plan.items.length} resources will be deleted:`}
                  </Notice>
                  {plan.items.map((item) => (
                    <div
                      key={`${item.kind}:${item.id}`}
                      style={{ display: "flex", alignItems: "baseline", gap: "0.5rem", flexWrap: "wrap" }}
                    >
                      <Tag variant={item.kind === "hook" ? "warn" : "err"}>{item.kind}</Tag>
                      <span style={{ fontFamily: MONO_FONT, fontSize: "0.8125rem" }}>{item.name}</span>
                      {item.detail && (
                        <span style={{ fontSize: "0.75rem", color: "var(--ink-3)" }}>{item.detail}</span>
                      )}
                    </div>
                  ))}
                </>
              )}
              <div className="forgejo-dialog-buttons" style={{ display: "flex", flexWrap: "wrap", gap: "0.5rem" }}>
                <Button
                  onClick={runDelete}
                  disabled={busy || plan.items.length === 0}
                  className="forgejo-orphans-delete-btn"
                >
                  Delete
                </Button>
              </div>
            </div>
          )}

          {report && (
            <Notice variant={report.failed.length === 0 ? "info" : "warn"}>
              {`Deleted ${report.deleted.length} resource${report.deleted.length === 1 ? "" : "s"}` +
                (report.failed.length === 0
                  ? "."
                  : `; ${report.failed.length} failed (${report.failed.join(", ")}) — run the cleanup again to retry them.`)}
            </Notice>
          )}

          <div className="forgejo-dialog-buttons" style={{ display: "flex", flexWrap: "wrap", gap: "0.5rem" }}>
            <Button onClick={close} disabled={busy} className="forgejo-orphans-close-btn">
              Close
            </Button>
          </div>
        </div>
      )}
    </Panel>
  );
}

export function Installs({
  installs,
  groups = [],
  instances = [],
  notice,
  installedNotice,
  error,
  onNewInstall,
  onEditInstall,
  onConnectInstall,
  onRemoveInstall,
}: InstallsProps) {
  const [removing, setRemoving] = useState<string | null>(null);
  const [removeToken, setRemoveToken] = useState("");
  // Whether the removal behind the open dialog is still running. The dialog stays open until
  // it answers, and the confirm button is out of reach while it does.
  const [removingRunning, setRemovingRunning] = useState(false);

  const handleRemoveConfirm = async (slug: string) => {
    if (removingRunning) return;
    setRemovingRunning(true);
    try {
      await onRemoveInstall(slug, removeToken || undefined);
    } finally {
      // The dialog closes when the removal settles, not when it is asked for: the admin reads
      // what it left behind on the list the dialog was covering.
      setRemoving(null);
      setRemoveToken("");
      setRemovingRunning(false);
    }
  };

  const columns = columnsFor(
    {
      onEditInstall,
      onConnectInstall,
      onRemove: (slug: string) => setRemoving(slug),
    },
    groups,
  );

  return (
    <div className="forgejo-installs" style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      {/* The page's own actions. The console draws the page title; this row carries what the
          admin does on the page, and it is shown only where there is a row to act beside — the
          empty state has its own button in the panel. */}
      <div style={{ display: "flex", flexWrap: "wrap", justifyContent: "space-between", alignItems: "center", gap: "0.75rem" }}>
        <p style={{ margin: 0, fontSize: "0.875rem", color: "var(--ink-2)", maxWidth: "40rem" }}>
          Connect a Forgejo organization. The plugin creates the bot accounts, webhook, agents and triggers for you.
        </p>
        {installs.length > 0 && (
          <Button variant="primary" onClick={onNewInstall} className="forgejo-new-install-btn">
            New install
          </Button>
        )}
      </div>

      {error && <Notice variant="err">{error}</Notice>}
      {notice && <Notice variant="warn">{notice}</Notice>}
      {installedNotice && <Notice variant="info">{installedNotice}</Notice>}

      <Panel title="Installs">
        <p style={{ margin: "0 0 1rem", fontSize: "0.75rem", color: "var(--ink-3)" }}>
          Health is read from the hub&apos;s event log
        </p>

        {installs.length === 0 ? (
          <div className="forgejo-no-installs" style={{ display: "flex", flexDirection: "column", alignItems: "flex-start", gap: "0.75rem" }}>
            <p style={{ margin: 0 }}>No installs configured yet.</p>
            <Button variant="primary" onClick={onNewInstall} className="forgejo-new-install-btn">
              New install
            </Button>
          </div>
        ) : (
          <>
            {/* The table scrolls sideways on a narrow window instead of pushing the page wider. */}
            <div style={{ overflowX: "auto" }}>
              <RegisterTable
                rows={installs}
                columns={columns}
                rowKey={(install: Install) => install.slug}
                withRowNumbers={false}
              />
            </div>

            {removing && (
              <div
                className="forgejo-remove-dialog"
                style={{ display: "flex", flexDirection: "column", gap: "0.75rem", marginTop: "1rem" }}
              >
                <Notice variant="warn">
                  Are you sure you want to remove install "{removing}"?
                </Notice>
                <Field htmlFor="installs-remove-token" label="Optional: Forgejo Admin Token (to delete bots and teams)" hint="Without it, the bot users and teams are left in Forgejo.">
                  <Input
                    id="installs-remove-token"
                    type="password"
                    value={removeToken}
                    onChange={(e: any) => setRemoveToken(e.target.value)}
                    placeholder="Admin token (optional)"
                  />
                </Field>
                <div className="forgejo-dialog-buttons" style={{ display: "flex", flexWrap: "wrap", gap: "0.5rem" }}>
                  <Button
                    onClick={() => {
                      if (removingRunning) return;
                      setRemoving(null);
                      setRemoveToken("");
                    }}
                    disabled={removingRunning}
                    className="forgejo-cancel-btn"
                  >
                    Cancel
                  </Button>
                  <Button
                    onClick={() => handleRemoveConfirm(removing)}
                    disabled={removingRunning}
                    className="forgejo-confirm-remove-btn"
                  >
                    Remove
                  </Button>
                </div>
              </div>
            )}
          </>
        )}
      </Panel>

      {/* The sweep is for what the list no longer shows — including when the list shows
          nothing at all — so it stands beside the list, not inside a row of it. */}
      <OrphanCleanup instances={instances} />
    </div>
  );
}
