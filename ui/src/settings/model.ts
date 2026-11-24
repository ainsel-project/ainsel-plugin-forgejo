// The settings page's static half: what the registry's cards name, the plugin's
// fixed resources, the small arithmetic the page does over its rows, and the
// styles those pieces share. None of it reads a hook or renders a component, so
// the page and the pieces can both import it without a cycle.

import type { CSSProperties } from "react";
import type { Setting } from "../api";

export type RoleKey = "refiner" | "developer" | "reviewer";

// The copy and colour per role. The colours are the ones LivePage draws the
// role dots with — the same token, so a console theme that re-points one role
// re-points it here too.
export const ROLES: { key: RoleKey; label: string; desc: string; color: string }[] = [
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
export type FieldSpec = {
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

export const GROUPS: { title: string; desc: string; fields: FieldSpec[] }[] = [
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
export const KNOWN_FIELDS = new Set(GROUPS.flatMap((g) => g.fields.map((f) => f.field)));

// The plugin's fixed set, as the manifest provides it. Listed here rather than
// read from the API: the page has no endpoint that names them, and the sets only
// change with the plugin itself.
export type Resource = { name: string; kind: string; scope: string };
export const RESOURCES: Resource[] = [
  { name: "forgejo-forgejo", kind: "Skill", scope: "All installs" },
  { name: "plugin-forgejo", kind: "MCP server", scope: "All installs" },
  { name: "forgejo-mcp-<install>", kind: "MCP server", scope: "Per install" },
];

// A typed change that has not been saved yet. Drafts are keyed by "role.field",
// the same key the field errors use.
export type Draft = { role: string; field: string; value: string };

// key names one (role, field) in the local maps (drafts, field errors, the
// compare toggles).
export const key = (role: string, field: string) => `${role}.${field}`;

// without returns a copy of a map with one key taken out.
export function without<T>(m: Record<string, T>, k: string): Record<string, T> {
  const { [k]: _gone, ...rest } = m;
  return rest;
}

// runningVersion names the plugin version the page is standing on. The API
// does not answer with it directly; it is readable off the registry, because
// every field the admin has not edited carries the running version as its
// defaultVersion — the auto-adopt keeps them current. Only if every field were
// dirty is there no such row, and then the newest version any row names is the
// best reading available.
export function runningVersion(settings: Setting[]): string {
  const calm = settings.find((s) => !s.dirty);
  if (calm) return calm.defaultVersion;
  return settings.reduce((v, s) => (s.defaultVersion > v ? s.defaultVersion : v), "");
}

// One entry of the "Check now" answer, in the shape the log writes it: where,
// what, and the change or the refusal.
export function changeLine(c: { install?: string; role?: string; kind: string; field: string }): string {
  const at = [c.install, c.role].filter(Boolean).join("/");
  return `${at ? at + ": " : ""}${c.kind}.${c.field}`;
}

// truncate shortens a shipped default for the one-line "Default: …" hint. The
// full text is still on the row's title for anyone who hovers it.
export function truncate(s: string, n = 40): string {
  return s.length > n ? s.slice(0, n) + "…" : s;
}

export const clock = (d: Date) => d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });

export const MONO = "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace";
export const sectionLabel: CSSProperties = {
  fontSize: 11,
  fontWeight: 600,
  letterSpacing: ".06em",
  textTransform: "uppercase",
  color: "var(--ink-3)",
  padding: "6px 10px",
};
export const cardStyle: CSSProperties = {
  background: "var(--paper)",
  border: "1px solid var(--rule-soft)",
  borderRadius: "var(--radius)",
  boxSizing: "border-box",
};
export const preStyle = (tint: string): CSSProperties => ({
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
export const pageStyles = `
.forgejo-settings-nav-item{background:transparent;color:var(--ink-2)}
.forgejo-settings-nav-item:hover{background:var(--hover-subtle)}
.forgejo-settings-nav-item-on,.forgejo-settings-nav-item-on:hover{background:var(--signal-haze);color:var(--signal-ink);font-weight:600}
.forgejo-settings-mono,.forgejo-settings-mono input,.forgejo-settings-mono textarea{font-family:${MONO} !important}
`;
