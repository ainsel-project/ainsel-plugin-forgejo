// SettingsPage is the manifest's admin-only page "/settings": the one offered
// edit surface for what the plugin owns (design decision 1). Every install's
// agents, personas and forge MCP row are created by the plugin from these
// values — global per role, no per-install overrides this round — so this page
// is where the admin sets them, and the reconcile pass is where they land.
//
// The page renders the whole registry the GET answers with (today 3 roles × 6
// fields) rather than a form it builds from its own list of fields, because
// the answer is the registry: a field the console has never heard of still
// renders, under "Other", and still saves. The layout cards only decide where a
// known field sits and how it reads; they never decide whether it renders.
//
// Edits are batched (one PUT per save): every typed change is a draft until the
// admin saves the whole set from the bar at the foot of the page, or discards
// it. A refused batch lands whole or not at all, so the drafts stay where the
// admin left them and each named field says why it was refused. Reset is the one
// exception — it is immediate, per field, because it is a re-adoption rather
// than a draft.
//
// The winner rule (design decision 4) is visible here: an edited field carries
// an "Overridden" tag and survives upgrades; an untouched one follows the shipped
// default and needs no hint. A field that is both dirty and older than the
// running plugin shows an "Update available" box: its edit, made on the older
// version, is kept, and a Compare toggle sets it beside the shipped default.
// Adopting the new default is the same reset as the per-field one.
//
// Membership is not edited here (design decision 3): the skill and the MCP
// servers are the manifest's fixed set, listed read-only under "Managed
// resources" with the plugin version that provides them. Membership changes by
// plugin upgrade, nothing else.
import { Button, Input, Notice, Panel, RegisterTable, Select, Tag, Textarea, usePluginContext } from "@ainsel/plugin-sdk";
import { CardGrid } from "./layout";
import { useEffect, useState } from "react";
import type { CSSProperties } from "react";
import {
  fetchSettings,
  putSettings,
  reconcileSettings,
  fetchInstances,
  putInstances,
} from "./api";
import type { ReconcileSummary, Setting, SettingsEdit, ForgejoInstance } from "./api";

// The roles the registry answers for, in its order. The side navigation offers
// them one at a time, each with its own cards.
const ROLE_KEYS = ["refiner", "developer", "reviewer"] as const;
type RoleKey = (typeof ROLE_KEYS)[number];

// The copy and colour per role. The colours are the ones LivePage draws the
// role dots with — the same token, so a console theme that re-points one role
// re-points it here too.
const ROLES: { key: RoleKey; label: string; desc: string; color: string }[] = [
  {
    key: "refiner",
    label: "Refiner",
    desc: "Turns new issues into ready-to-build specs and hands them to the developer.",
    color: "var(--live-refiner, var(--warn))",
  },
  {
    key: "developer",
    label: "Developer",
    desc: "Implements refined issues on a branch and opens the pull request.",
    color: "var(--live-developer, var(--signal))",
  },
  {
    key: "reviewer",
    label: "Reviewer",
    desc: "Reviews pull requests against the spec and the repository conventions.",
    color: "var(--live-reviewer, var(--stale))",
  },
];

// The cards a role's fields are grouped into, and how each known field renders.
// `multiline` says a row is a Textarea; `mono` marks URLs, env var names and
// model ids, which read as identifiers. A row that carries `options` in the
// answer renders as a dropdown — that comes from the answer, not from this list.
type FieldSpec = {
  field: string;
  label: string;
  help?: string;
  placeholder?: string;
  multiline?: boolean;
  mono?: boolean;
  // secret marks a write-only field: the plugin keeps no copy of its value, so
  // the input always starts empty, an empty save says nothing, and the row never
  // reads as edited — a typed value is written straight onto the role's agents.
  // It renders as a password input.
  secret?: boolean;
};

const GROUPS: { title: string; desc: string; fields: FieldSpec[] }[] = [
  {
    title: "Model",
    desc: "The language model this role's agents run on.",
    fields: [
      { field: "llmProvider", label: "Provider", help: "Where the role's model runs." },
      {
        field: "llmModel",
        label: "Model",
        help: "Model identifier as the provider names it.",
        placeholder: "e.g. llama3.3",
        mono: true,
      },
      {
        field: "llmCustomUrl",
        label: "Custom provider URL",
        help: "Required when the provider is Custom: the OpenAI-compatible endpoint the role's agents call.",
        placeholder: "https://llm.example.com/v1",
        mono: true,
      },
      {
        field: "llmCustomKey",
        label: "Custom provider key",
        help: "Optional. The hub stores it on the role's agents and never shows it again — type a new key to replace it, leave it empty to keep what the agents hold. The role must be installed to hold one.",
        placeholder: "(unchanged)",
        mono: true,
        secret: true,
      },
      // One key row per vendor provider. Each is used only while the role's
      // provider names it; the others have no block on the agent to hold them.
      // All four key rows share the contract: the plugin stores none of them, so
      // the answer is always empty and a typed value is written straight onto
      // the role's agents.
      {
        field: "llmOpenAIKey",
        label: "OpenAI key",
        help: "Used when the provider is OpenAI. The hub stores it on the role's agents and never shows it again — type a new key to replace it, leave it empty to keep what the agents hold.",
        placeholder: "(unchanged)",
        mono: true,
        secret: true,
      },
      // The other half of how an openai role authenticates. It renders as a
      // dropdown because the answer carries options; the pair rule (a sign-in
      // batch carries no key) is the API's, and its refusal names this row.
      {
        field: "llmOpenAIAuthMethod",
        label: "OpenAI auth method",
        help: "How a role on OpenAI authenticates. Sign in with ChatGPT sends no key and leaves the sign-in to finish in the AInsel console.",
      },
      {
        field: "llmAnthropicKey",
        label: "Anthropic key",
        help: "Used when the provider is Anthropic. The hub stores it on the role's agents and never shows it again — type a new key to replace it, leave it empty to keep what the agents hold.",
        placeholder: "(unchanged)",
        mono: true,
        secret: true,
      },
      {
        field: "llmGoogleKey",
        label: "Google Gemini key",
        help: "Used when the provider is Google Gemini. The hub stores it on the role's agents and never shows it again — type a new key to replace it, leave it empty to keep what the agents hold.",
        placeholder: "(unchanged)",
        mono: true,
        secret: true,
      },
    ],
  },
  {
    title: "PR patrol",
    desc: "How the developer goes looking for pull requests nobody mentioned it about. One setting for every install: each patrol trigger is held to it.",
    fields: [
      {
        field: "patrolEnabled",
        label: "Patrol",
        help: "Off switches the patrol triggers off rather than deleting them: they stay on the AInsel console's Schedules UI and can be switched back on here.",
      },
      {
        field: "patrolInterval",
        label: "How often",
        help: "From every 10 minutes to every 24 hours. Every run is a model call on each install, so a short cadence is a long bill.",
      },
    ],
  },
  {
    title: "Forge connection",
    desc: "How agents reach Forgejo. Per-install MCP servers point here.",
    fields: [
      {
        field: "mcpUrl",
        label: "Forge MCP server URL",
        help: "The forgejo-mcp endpoint each install's agents call.",
        placeholder: "https://forge-mcp.example.com:3000",
        mono: true,
      },
      {
        field: "mcpTokenFromEnv",
        label: "Token environment variable",
        help: "Name of the variable that holds the forge token inside the agent.",
        placeholder: "FORGEJO_TOKEN",
        mono: true,
      },
    ],
  },
  {
    title: "Instructions",
    desc: "What the agents are told. Edited values survive plugin upgrades.",
    fields: [
      {
        field: "personaText",
        label: "Persona",
        help: "System prompt the role's agents start from.",
        multiline: true,
        mono: true,
      },
      {
        field: "skillText",
        label: "Skill section",
        // The served skill document is composed of one section per role
        // (content.ComposeSkill), so each role's row edits that role's section
        // and nothing else — the help says so, so an admin does not go looking
        // for a single shared text.
        help: "This role's section of the shared skill document. Editing it changes no other role's text.",
        multiline: true,
        mono: true,
      },
    ],
  },
];

// The fields a card names. Anything the registry answers with beyond these
// lands in "Other".
const KNOWN_FIELDS = new Set(GROUPS.flatMap((g) => g.fields.map((f) => f.field)));

// The plugin's fixed set, as the manifest provides it. Listed here rather than
// read from the API: the page has no endpoint that names them, and the sets only
// change with the plugin itself.
type Resource = { name: string; kind: string; scope: string };
const RESOURCES: Resource[] = [
  { name: "forgejo-forgejo", kind: "Skill", scope: "All installs" },
  { name: "plugin-forgejo", kind: "MCP server", scope: "All installs" },
  { name: "forgejo-mcp-<install>", kind: "MCP server", scope: "Per install" },
];

// A typed change that has not been saved yet. Drafts are keyed by "role.field",
// the same key the field errors use.
type Draft = { role: string; field: string; value: string };

// key names one (role, field) in the local maps (drafts, field errors, the
// compare toggles).
const key = (role: string, field: string) => `${role}.${field}`;

// without returns a copy of a map with one key taken out.
function without<T>(m: Record<string, T>, k: string): Record<string, T> {
  const { [k]: _gone, ...rest } = m;
  return rest;
}

// runningVersion names the plugin version the page is standing on. The API
// does not answer with it directly; it is readable off the registry, because
// every field the admin has not edited carries the running version as its
// defaultVersion — the auto-adopt keeps them current. Only if every field were
// dirty is there no such row, and then the newest version any row names is the
// best reading available.
function runningVersion(settings: Setting[]): string {
  const calm = settings.find((s) => !s.dirty);
  if (calm) return calm.defaultVersion;
  return settings.reduce((v, s) => (s.defaultVersion > v ? s.defaultVersion : v), "");
}

// One entry of the "Check now" answer, in the shape the log writes it: where,
// what, and the change or the refusal.
function changeLine(c: {
  install?: string;
  role?: string;
  kind: string;
  field: string;
}): string {
  const at = [c.install, c.role].filter(Boolean).join("/");
  return `${at ? at + ": " : ""}${c.kind}.${c.field}`;
}

// truncate shortens a shipped default for the one-line "Default: …" hint. The
// full text is still on the row's title for anyone who hovers it.
function truncate(s: string, n = 40): string {
  return s.length > n ? s.slice(0, n) + "…" : s;
}

const clock = (d: Date) => d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });

const MONO = "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace";
const sectionLabel: CSSProperties = {
  fontSize: 11,
  fontWeight: 600,
  letterSpacing: ".06em",
  textTransform: "uppercase",
  color: "var(--ink-3)",
  padding: "6px 10px",
};
const cardStyle: CSSProperties = {
  background: "var(--paper)",
  border: "1px solid var(--rule-soft)",
  borderRadius: "var(--radius)",
  boxSizing: "border-box",
};
const preStyle = (tint: string): CSSProperties => ({
  margin: 0,
  padding: 10,
  borderRadius: "var(--radius-sm)",
  background: tint,
  whiteSpace: "pre-wrap",
  fontFamily: MONO,
  fontSize: 12,
  color: "var(--ink)",
});

// The nav's hover and selected states live in the stylesheet below rather than
// inline: an inline background would win over :hover and never let it show.
// The mono class is for the controls the console draws — a rule the console's
// own input font would otherwise beat — and it is the only other reason for the
// element.
const pageStyles = `
.forgejo-settings-nav-item{background:transparent;color:var(--ink-2)}
.forgejo-settings-nav-item:hover{background:var(--hover-subtle)}
.forgejo-settings-nav-item-on,.forgejo-settings-nav-item-on:hover{background:var(--signal-haze);color:var(--signal-ink);font-weight:600}
.forgejo-settings-mono,.forgejo-settings-mono input,.forgejo-settings-mono textarea{font-family:${MONO} !important}
`;

export function SettingsPage() {
  const { api } = usePluginContext();

  const [settings, setSettings] = useState<Setting[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [pane, setPane] = useState<"role" | "resources" | "instances">("role");
  const [currentRole, setCurrentRole] = useState<RoleKey>("refiner");

  // The configured Forgejo instances, and the list the admin has arranged but
  // not saved. They are written whole — the same all-or-nothing rule the
  // settings batch follows — so the draft is the whole list, not one row, and
  // a refused write names the row and half it refused.
  const [instances, setInstances] = useState<ForgejoInstance[]>([]);
  const [instanceDraft, setInstanceDraft] = useState<ForgejoInstance[] | null>(null);
  const [instanceErrors, setInstanceErrors] = useState<Record<string, string>>({});
  const [instanceError, setInstanceError] = useState<string | null>(null);
  const [instanceSaving, setInstanceSaving] = useState(false);

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

  useEffect(() => {
    const load = async () => {
      try {
        const res = await fetchSettings(api);
        setSettings(res.settings ?? []);
        setLoading(false);
      } catch (err: any) {
        if (err.status === 401 || err.status === 403) {
          setError("Only admins can access the settings.");
        } else {
          setError(err.message || "Failed to load settings.");
        }
        setLoading(false);
      }
    };
    load();
    // The instance list loads beside the settings: it is a second collection on
    // the same page, and the wizard's Instance step reads it, so the admin who
    // lands here to add one should see it without a second trip.
    fetchInstances(api)
      .then((res) => setInstances(res.instances ?? []))
      .catch(() => setInstances([]));
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
    } catch (err: any) {
      // A refused batch is answered with each bad field named (the PUT either
      // lands whole or not at all); the console's client puts the parsed body
      // on `err.body`. The drafts stay exactly where they were.
      const fields: unknown = err.body?.fields;
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
        setBatchError(err.message || "Failed to save.");
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
    } catch (err: any) {
      setFieldErrors((e) => ({ ...e, [k]: err.message || "Failed to reset." }));
    } finally {
      setBusyKey(null);
    }
  };

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
    } catch (err: any) {
      setReconcileError(err.message || "Failed to reconcile.");
    } finally {
      setReconciling(false);
    }
  };

  // The instance list is edited as a whole list, because that is how it is
  // written: the PUT replaces every row. So the draft is the list, an add or a
  // remove is a draft edit, and Save is one request that lands all of it or none.
  const draftInstances = instanceDraft ?? instances;
  const instancePending =
    instanceDraft !== null && JSON.stringify(instanceDraft) !== JSON.stringify(instances);

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
    } catch (err: any) {
      // A refused list names each bad row, the way a refused batch names each bad
      // field. The draft stays where the admin left it, and each row says why.
      const fields: unknown = err.body?.fields;
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
        setInstanceError(err.message || "Failed to save the instances.");
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
  const dirtyCount = (role: string) =>
    settings.filter((s) => s.role === role && s.dirty).length;
  const roleSettings = settings.filter((s) => s.role === currentRole);
  const role = ROLES.find((r) => r.key === currentRole)!;

  // renderField draws one row: label and help with the state tags on the left,
  // the control on the right, and under it whichever hint the field's state asks
  // for — the stale box, or the one-line default with its reset.
  const renderField = (spec: FieldSpec, s: Setting) => {
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
        <Select
          id={id}
          value={value}
          onChange={(next: string) => edit(s, next)}
          options={s.options}
        />
      ) : spec.multiline ? (
        <Textarea
          id={id}
          value={value}
          onChange={(e: any) => edit(s, e.target.value)}
          placeholder={spec.placeholder}
          className={spec.mono ? "forgejo-settings-mono" : undefined}
        />
      ) : (
        <Input
          id={id}
          type={spec.secret ? "password" : "text"}
          value={value}
          onChange={(e: any) => edit(s, e.target.value)}
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
          {spec.help && (
            <span style={{ fontSize: 12, color: "var(--ink-2)", lineHeight: "18px" }}>
              {spec.help}
            </span>
          )}
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
                  <Button
                    onClick={() => setComparing((c) => ({ ...c, [k]: !open }))}
                    className="forgejo-compare-btn"
                  >
                    {open ? "Hide comparison" : "Compare"}
                  </Button>
                  <Button
                    onClick={() => handleReset(s)}
                    disabled={busy || saving}
                    className="forgejo-reset-btn"
                  >
                    Adopt new default
                  </Button>
                </span>
              </div>
              {open && (
                <CardGrid minTrackWidth={240} maxColumns={2} gap={8}>
                  <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
                    <span style={{ fontSize: 12, fontWeight: 600, color: "var(--ink-2)" }}>
                      Your version
                    </span>
                    <pre style={preStyle("color-mix(in srgb, var(--err) 10%, transparent)")}>
                      {s.value}
                    </pre>
                  </div>
                  <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
                    <span style={{ fontSize: 12, fontWeight: 600, color: "var(--ink-2)" }}>
                      {`Default in v${version}`}
                    </span>
                    <pre style={preStyle("color-mix(in srgb, var(--ok) 12%, transparent)")}>
                      {s.default}
                    </pre>
                  </div>
                </CardGrid>
              )}
            </div>
          )}

          {overridden && !stale && (
            <span style={{ fontSize: 12, color: "var(--ink-2)" }}>
              Default:{" "}
              <span
                className="forgejo-settings-mono"
                title={s.default}
                style={{ fontFamily: MONO }}
              >
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
  };

  // Each card's rows, in the card's order, skipping a known field the registry
  // does not answer with: a row for it would only lie about what can be edited.
  const cardRows = (fields: FieldSpec[]) =>
    fields.flatMap((spec) => {
      const s = roleSettings.find((x) => x.field === spec.field);
      return s ? [{ spec, s }] : [];
    });
  const otherRows = roleSettings
    .filter((s) => !KNOWN_FIELDS.has(s.field))
    .map((s) => ({ spec: { field: s.field, label: s.field } as FieldSpec, s }));

  const cards = [
    ...GROUPS.map((g) => ({ title: g.title, desc: g.desc, rows: cardRows(g.fields) })),
    { title: "Other", desc: "Fields the registry answers with that this page has no card for. They edit and save like the rest.", rows: otherRows },
  ].filter((c) => c.rows.length > 0);

  const roleView = (
    <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
      <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
        <h2 style={{ margin: 0, fontSize: 18, fontWeight: 600 }}>{role.label}</h2>
        <span style={{ fontSize: 13, color: "var(--ink-2)" }}>{role.desc}</span>
      </div>
      {cards.map((c) => (
        <section key={c.title} style={cardStyle}>
          <div style={{ padding: "16px 20px" }}>
            <h3 style={{ margin: 0, fontSize: 15, fontWeight: 600 }}>{c.title}</h3>
            <p style={{ margin: "2px 0 0", fontSize: 13, color: "var(--ink-2)" }}>{c.desc}</p>
          </div>
          {c.rows.map(({ spec, s }) => renderField(spec, s))}
        </section>
      ))}
    </div>
  );

  const resourcesView = (
    <section style={{ ...cardStyle, overflow: "hidden" }}>
      <div style={{ padding: "16px 20px" }}>
        <h2 style={{ margin: 0, fontSize: 18, fontWeight: 600 }}>Managed resources</h2>
        <p style={{ margin: "4px 0 0", fontSize: 13, color: "var(--ink-2)", maxWidth: 640 }}>
          Every install receives these. They come from the plugin manifest and change only when the
          plugin is upgraded, so they cannot be edited here.
        </p>
        <p style={{ margin: "8px 0 0", fontSize: 13, color: "var(--ink-2)", maxWidth: 640 }}>
          Each install also gets its forge MCP server,{" "}
          <code>forgejo-mcp-&lt;install&gt;</code>, pointed at by the MCP fields below. The sets are
          the plugin manifest&apos;s — membership changes only when the plugin upgrades.
        </p>
      </div>
      <div style={{ overflowX: "auto" }}>
        <div style={{ minWidth: 640 }}>
          <RegisterTable
            withRowNumbers={false}
            rows={RESOURCES}
            rowKey={(r: Resource) => r.name}
            columns={[
              {
                key: "name",
                header: "Resource",
                cell: (r: Resource) => (
                  <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
                    <svg
                      width="14"
                      height="14"
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth={2}
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      role="img"
                      aria-label="Locked"
                      style={{ color: "var(--ink-3)", flex: "0 0 auto" }}
                    >
                      <rect x="4" y="11" width="16" height="10" rx="2" />
                      <path d="M8 11V7a4 4 0 0 1 8 0v4" />
                    </svg>
                    <span className="forgejo-settings-mono" style={{ fontFamily: MONO, color: "var(--ink)" }}>
                      {r.name}
                    </span>
                  </span>
                ),
              },
              { key: "kind", header: "Kind", cell: (r: Resource) => r.kind },
              { key: "scope", header: "Scope", cell: (r: Resource) => r.scope },
              { key: "source", header: "Source", cell: () => <Tag>{`Plugin v${version}`}</Tag> },
            ]}
          />
        </div>
      </div>
    </section>
  );

  // instancesView is the list of Forgejo addresses this plugin may be installed
  // against. The setup wizard's Instance step selects over it, and the plan
  // refuses an address outside it, so an install cannot point at a host nobody
  // configured. The list is written whole — the same all-or-nothing rule the
  // settings batch follows — and its order is the order the wizard draws.
  const instancesView = (
    <section style={{ ...cardStyle, overflow: "hidden" }}>
      <div style={{ padding: "16px 20px" }}>
        <h2 style={{ margin: 0, fontSize: 18, fontWeight: 600 }}>Forgejo instances</h2>
        <p style={{ margin: "4px 0 0", fontSize: 13, color: "var(--ink-2)", maxWidth: 640 }}>
          The Forgejo addresses an install may be created against. The setup wizard picks from this
          list, and a plan that names an address outside it is refused. An address is stored without
          its trailing slash; the same address twice is one instance, not two.
        </p>
        {instances.length === 0 && !instancePending && (
          <Notice variant="warn">
            No instance is configured, so the setup wizard has nothing to install against. Add the
            address of your Forgejo here first.
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
              onChange={(e: any) => editInstance(i, { name: e.target.value })}
              placeholder="e.g., Primary"
            />
            <Input
              aria-label={`Instance ${i + 1} base URL`}
              className="forgejo-settings-mono"
              value={inst.baseUrl}
              onChange={(e: any) => editInstance(i, { baseUrl: e.target.value })}
              placeholder="https://forgejo.example.com"
            />
            {instanceErrors[`instances[${i}]`] && (
              <span style={{ fontSize: 12, color: "var(--err, var(--warn))" }}>
                {instanceErrors[`instances[${i}]`]}
              </span>
            )}
          </div>
          <div style={{ flex: "0 0 auto" }}>
            <Button
              onClick={() => removeInstance(i)}
              disabled={instanceSaving}
              className="forgejo-instance-remove-btn"
            >
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

  // The header's status: only what this page has seen. Before a pass runs from
  // here there is no status to show, so the tag is left out. A flagged LLM check
  // — unreachable or unknown — is as much "not in sync" as a refused write: the
  // button's whole promise is that it says so when the configured provider cannot
  // serve, or could not be asked at all.
  const syncTag = checkedAt && (
    <Tag variant={checkedAt.refused + checkedAt.llmFlagged > 0 ? "warn" : "ok"}>
      {checkedAt.refused + checkedAt.llmFlagged > 0
        ? `${checkedAt.refused + checkedAt.llmFlagged} flagged · checked ${clock(checkedAt.at)}`
        : `In sync · checked ${clock(checkedAt.at)}`}
    </Tag>
  );

  return (
    <Panel
      className="forgejo-settings"
      title="Settings"
      right={
        <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 10 }}>
          {syncTag}
          <Button
            onClick={handleReconcile}
            disabled={reconciling}
            className="forgejo-reconcile-btn"
          >
            {reconciling ? "Reconciling…" : "Reconcile now"}
          </Button>
        </div>
      }
    >
      <style>{pageStyles}</style>
      <p style={{ margin: 0, fontSize: 14, color: "var(--ink-2)", maxWidth: 640 }}>
        Role defaults for every Forgejo install. Saved changes are pushed to the agents and personas
        on the next reconcile.
      </p>

      {reconcileError && <Notice variant="err">{reconcileError}</Notice>}
      {summary && (
        <Notice
          variant={summary.refused.length > 0 || summary.llmChecks.some((c) => c.state !== "ok") ? "warn" : "info"}
        >
          <div style={{ display: "flex", flexWrap: "wrap", gap: 12, justifyContent: "space-between", alignItems: "flex-start" }}>
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
                setSummary(null);
                setReconcileError(null);
              }}
              className="forgejo-dismiss-btn"
            >
              Dismiss
            </Button>
          </div>
        </Notice>
      )}

      <div style={{ display: "flex", flexWrap: "wrap", gap: 20, alignItems: "flex-start" }}>
        <nav
          aria-label="Settings sections"
          style={{
            flex: "0 0 220px",
            maxWidth: "100%",
            boxSizing: "border-box",
            display: "flex",
            flexDirection: "column",
            gap: 2,
          }}
        >
          <span style={sectionLabel}>Agent roles</span>
          {ROLES.map((r) => {
            const on = pane === "role" && currentRole === r.key;
            const n = dirtyCount(r.key);
            return (
              <button
                key={r.key}
                type="button"
                aria-current={on ? "true" : undefined}
                onClick={() => {
                  setPane("role");
                  setCurrentRole(r.key);
                }}
                className={`forgejo-settings-nav-item${on ? " forgejo-settings-nav-item-on" : ""}`}
                style={{
                  display: "flex",
                  justifyContent: "space-between",
                  alignItems: "center",
                  gap: 8,
                  width: "100%",
                  padding: "8px 10px",
                  border: 0,
                  borderRadius: "var(--radius-sm)",
                  fontSize: 14,
                  cursor: "pointer",
                  textAlign: "left",
                }}
              >
                <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
                  <span
                    aria-hidden="true"
                    style={{
                      width: 8,
                      height: 8,
                      borderRadius: 4,
                      background: r.color,
                      display: "inline-block",
                      flex: "0 0 auto",
                    }}
                  />
                  {r.label}
                </span>
                {n > 0 && (
                  <span
                    title="Fields edited away from the shipped default"
                    style={{
                      minWidth: 18,
                      padding: "0 6px",
                      borderRadius: 9,
                      background: "var(--signal-haze)",
                      color: "var(--signal-ink)",
                      fontSize: 11,
                      fontWeight: 600,
                      lineHeight: "18px",
                      textAlign: "center",
                    }}
                  >
                    {n}
                  </span>
                )}
              </button>
            );
          })}
          <span style={{ ...sectionLabel, padding: "14px 10px 6px" }}>Plugin</span>
          <button
            type="button"
            aria-current={pane === "instances" ? "true" : undefined}
            onClick={() => setPane("instances")}
            className={`forgejo-settings-nav-item${pane === "instances" ? " forgejo-settings-nav-item-on" : ""}`}
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
              gap: 8,
              width: "100%",
              padding: "8px 10px",
              border: 0,
              borderRadius: "var(--radius-sm)",
              fontSize: 14,
              cursor: "pointer",
              textAlign: "left",
            }}
          >
            <span>Forgejo instances</span>
            {instances.length > 0 && (
              <span
                title="Configured Forgejo instances"
                style={{
                  minWidth: 18,
                  padding: "0 6px",
                  borderRadius: 9,
                  background: "var(--signal-haze)",
                  color: "var(--signal-ink)",
                  fontSize: 11,
                  fontWeight: 600,
                  lineHeight: "18px",
                  textAlign: "center",
                }}
              >
                {instances.length}
              </span>
            )}
          </button>
          <button
            type="button"
            aria-current={pane === "resources" ? "true" : undefined}
            onClick={() => setPane("resources")}
            className={`forgejo-settings-nav-item${pane === "resources" ? " forgejo-settings-nav-item-on" : ""}`}
            style={{
              display: "flex",
              alignItems: "center",
              width: "100%",
              padding: "8px 10px",
              border: 0,
              borderRadius: "var(--radius-sm)",
              fontSize: 14,
              cursor: "pointer",
              textAlign: "left",
            }}
          >
            <span>Managed resources</span>
          </button>
        </nav>

        <div style={{ flex: "999 1 560px", minWidth: 0, display: "flex", flexDirection: "column", gap: 16 }}>
          {pane === "role" ? roleView : pane === "instances" ? instancesView : resourcesView}
        </div>
      </div>

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
            <b style={{ fontWeight: 600 }}>
              {`${pendingCount} unsaved ${pendingCount === 1 ? "change" : "changes"}`}
            </b>{" "}
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
    </Panel>
  );
}
