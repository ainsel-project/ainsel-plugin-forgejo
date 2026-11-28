// Model for the setup wizard form
// The Input shape and step logic follow the spec in docs/design/2026-10-24-setup-wizard-flow.md,
// trimmed by the managed-agents design: the wizard collects only per-install facts.
// The forge MCP URL and the role LLMs left for the settings store; the forge-MCP pick
// step went with them.

import type { WorkingHours } from "../api";

export type { WorkingHours };

export type SetupStep = "installs" | "instance" | "accounts" | "roles" | "rules" | "preview" | "connect" | "done";

// What the wizard still hands the plan per role: the container image the role's
// image is created from, the agent's display name, the tool set, and the role's
// turn timeout. The LLM the agent is created on comes from the settings store,
// not from here.
export interface RoleInput {
  runtime: string;
  agentName: string;
  tools: string[];
  // Undefined means "any hour": nothing goes out for the field, so the plan has no op for it
  // and the apply writes neither a window nor a clear. That is what leaves a window an admin
  // set in the AInsel console sitting where they left it. Setting one here and then clearing
  // it is the only way the wizard ever asks the hub to remove one.
  workingHours?: WorkingHours;
  // The role image's TURN_TIMEOUT_MS in milliseconds, as a decimal string — the
  // unit the wire, the env var and the hub all speak; the field on S4 takes
  // minutes and converts (issue #61). Empty means the shipped default on a new
  // install and "keep what the install has" on an edit — never "reset".
  turnTimeoutMs?: string;
  // llmKey is the optional provider key the admin may type for the role. The
  // wizard holds no settings of its own — a key's home is the hub, on the role's
  // agents — so this is the one place a key can be given before the agents exist.
  // It is used only while the role's provider holds one (custom, OpenAI, Anthropic,
  // Google); for any other provider the plan drops it. Empty says nothing at all:
  // no op, no write, the agents keep the key they hold.
  llmKey?: string;
}

export interface Input {
  slug: string;
  groupId: string;
  baseUrl: string;
  org: string;
  adminToken: string;
  excludeLabel: string;
  roles: {
    refiner: RoleInput;
    developer: RoleInput;
    reviewer: RoleInput;
  };
}

export function newInput(): Input {
  return {
    slug: "",
    groupId: "",
    baseUrl: "",
    org: "",
    adminToken: "",
    excludeLabel: "do-not-refine",
    roles: {
      refiner: newRoleInput(),
      developer: newRoleInput(),
      reviewer: newRoleInput(),
    },
  };
}

export function newRoleInput(): RoleInput {
  return {
    runtime: "",
    agentName: "",
    tools: [],
    turnTimeoutMs: "",
  };
}

// The window a role starts with when the admin asks for one: a working week, in UTC until they
// name a zone of their own. UTC rather than the browser's, because the wizard writes what the
// field says — and a zone read out of the browser is a zone the admin never typed.
export function newWorkingHours(): WorkingHours {
  return { timezone: "", days: [1, 2, 3, 4, 5], start: "09:00", end: "17:00" };
}

const DAY_NAMES = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

// daysText renders a day list the way the plan's preview renders it, so the form and the
// preview say the same window in the same words: a run of three or more as a run, days apart
// named one by one, and nothing named as every day. 7 is Sunday, the same day 0 is, so the two
// ways of writing it read the same.
export function daysText(days: number[]): string {
  const set = new Set(days.map((d) => (d === 7 ? 0 : d)));
  const all = [0, 1, 2, 3, 4, 5, 6];
  const sorted = all.filter((d) => set.has(d));
  if (sorted.length === 0 || sorted.length === all.length) return "every day";
  if (sorted.length < 3) return sorted.map((d) => DAY_NAMES[d]).join(", ");
  const contiguous = sorted.every((d, i) => i === 0 || d === sorted[i - 1] + 1);
  if (contiguous) return `${DAY_NAMES[sorted[0]]}–${DAY_NAMES[sorted[sorted.length - 1]]}`;
  return sorted.map((d) => DAY_NAMES[d]).join(", ");
}

// workingHoursText is the form's one-line reading of the window the admin is building — the same
// words the plan's detail uses. Nothing to say is no text at all rather than "any hours": the
// line only appears under a window the admin asked for.
export function workingHoursText(w: WorkingHours | undefined): string {
  if (!w) return "";
  const bounds =
    w.start && w.end ? `${w.start}–${w.end}` : w.start ? `from ${w.start}` : w.end ? `until ${w.end}` : "all day";
  return `${daysText(w.days)} ${bounds} ${w.timezone || "UTC"}`;
}

// The shipped per-role turn timeouts, in milliseconds — the plugin's
// DefaultTurnTimeoutMs mirrored for the field's placeholder and hint. The server
// is the authority: an empty field gets these on the image anyway; the UI only
// shows the admin what empty means.
export const TURN_TIMEOUT_DEFAULT_MS: Record<"refiner" | "developer" | "reviewer", string> = {
  refiner: "3600000",
  developer: "7200000",
  reviewer: "3600000",
};

// The wizard speaks minutes; the wire speaks milliseconds. Both directions are
// exact: a value the wizard wrote always reads back as the same number of
// minutes, and a hand-set value that is not a whole minute reads as empty — the
// field then says "keep", which is what leaving it alone means.
export function turnTimeoutMinutesFromMs(ms: string | undefined): string {
  if (!ms) return "";
  const n = Number(ms);
  if (!Number.isInteger(n) || n <= 0 || n % 60000 !== 0) return "";
  return String(n / 60000);
}

export function turnTimeoutMsFromMinutes(minutes: string): string {
  const t = minutes.trim();
  if (t === "") return "";
  if (!/^\d+$/.test(t)) return "";
  const n = Number(t);
  return Number.isSafeInteger(n) ? String(n * 60000) : "";
}

// Step progression order
export const stepOrder: SetupStep[] = [
  "installs",
  "instance",
  "accounts",
  "roles",
  "rules",
  "preview",
  "connect",
  "done",
];

export function nextStep(current: SetupStep): SetupStep | null {
  const idx = stepOrder.indexOf(current);
  return idx >= 0 && idx < stepOrder.length - 1 ? stepOrder[idx + 1] : null;
}

export function prevStep(current: SetupStep): SetupStep | null {
  const idx = stepOrder.indexOf(current);
  return idx > 0 ? stepOrder[idx - 1] : null;
}

// The colour each role is drawn in on the setup screens: the dot before a role's name and the
// role's card. The same tokens the live board uses, so a role has one colour across the plugin;
// the fallbacks keep the dot visible on a theme that does not define them.
export const ROLE_COLOR: Record<"refiner" | "developer" | "reviewer", string> = {
  refiner: "var(--live-refiner, var(--warn))",
  developer: "var(--live-developer, var(--signal))",
  reviewer: "var(--live-reviewer, var(--stale))",
};
