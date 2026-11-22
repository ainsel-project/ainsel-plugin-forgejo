// Model for the setup wizard form
// The Input shape and step logic follow the spec in docs/design/2026-10-24-setup-wizard-flow.md,
// trimmed by the managed-agents design (docs/plans/2026-10-30-managed-agents-design.md,
// decision 9): the wizard collects only per-install facts. The forge MCP URL and the
// role LLMs left for the settings store; the forge-MCP pick step went with them.

import type { Patrol, PatrolInput, WorkingHours } from "../api";

export type { Patrol, PatrolInput, WorkingHours };

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
  // The developer's scheduled PR patrol: how often it runs, and whether it runs at all.
  // Undefined means "the admin said nothing about the patrol", so the plan draws no op for it and
  // the apply neither retunes it nor switches it off — which is what leaves a patrol an admin set
  // on the AInsel console where they left it. Unlike the window above, an explicit answer here is
  // never a delete: switching the patrol off writes the hub row's `enabled: false`, so its prompt
  // and its history survive and the tick can be moved back.
  patrol?: PatrolInput;
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

// The cadences the wizard offers for the patrol, smallest first — and they are the validation, on
// the server and here. Every minute value divides 60 and every hour value divides 24, and that is
// not a matter of taste: a cron step counts from zero within its unit and resets at the top of it,
// so `*/45 * * * *` fires at :00 and :45 and starts over — a 45/15 loop wearing a normal-looking
// expression. A cadence the list cannot offer is typed into the expression box instead, where the
// hub is the one that says whether it means what the admin thinks.
export const PATROL_INTERVALS = [
  "1m", "2m", "3m", "4m", "5m", "6m", "10m", "12m", "15m", "20m", "30m",
  "1h", "2h", "3h", "4h", "6h", "8h", "12h",
];

// The patrol's shipped cadence — what an install that never chose runs at, and what an empty
// expression box means. The server owns this value; the field's placeholder shows it.
export const PATROL_DEFAULT_SCHEDULE = "*/30 * * * *";

// patrolScheduleFor is setup.PatrolSchedule, mirrored: the chip rendered as the cron expression it
// means. A step of one is not a step — `*` and `0 * * * *` are what a reader recognises on the
// hub's Schedules UI. An interval the list does not offer renders as nothing at all, which is how
// the form refuses one without a second list of what is bad.
export function patrolScheduleFor(interval: string): string {
  if (!PATROL_INTERVALS.includes(interval)) return "";
  const n = Number(interval.slice(0, -1));
  if (n === 1) return interval.endsWith("h") ? "0 * * * *" : "* * * * *";
  return interval.endsWith("h") ? `0 */${n} * * *` : `*/${n} * * * *`;
}

// patrolIntervalFor is the reading back: the chip an expression means, or "" when no chip does —
// which is the honest answer for a hand-typed expression, and the reason the box exists.
export function patrolIntervalFor(schedule: string): string {
  if (!schedule) return "";
  return PATROL_INTERVALS.find((iv) => patrolScheduleFor(iv) === schedule) ?? "";
}

// patrolIntervalMinutes is how often a cadence fires, in minutes, or 0 for a cadence no chip
// offers. The form's cost hint reads it; a hand-typed expression gets no hint, because this plugin
// does not own cron's grammar and will not guess at what an expression means.
export function patrolIntervalMinutes(schedule: string): number {
  const iv = patrolIntervalFor(schedule);
  if (!iv) return 0;
  const n = Number(iv.slice(0, -1));
  return iv.endsWith("h") ? n * 60 : n;
}

// The cadence below which the form says what it costs, mirroring the plan's warning: every fire is
// a model call against the install's model, so the shortest thing on offer is 1 440 wakes a day.
export const PATROL_WARN_MINUTES = 10;

// patrolScheduleOf is the cadence the form's answer leaves in force: the expression box wins over
// a chip, a chip renders, and neither keeps whatever the install was running — which is what an
// answer that only moved the tick means. Empty on the end of that reads as the shipped cadence.
export function patrolScheduleOf(p: PatrolInput | undefined): string {
  if (!p) return PATROL_DEFAULT_SCHEDULE;
  if (p.schedule) return p.schedule;
  if (p.interval) return patrolScheduleFor(p.interval) || PATROL_DEFAULT_SCHEDULE;
  return p.schedule || PATROL_DEFAULT_SCHEDULE;
}

// patrolEnabledOf is whether the patrol is meant to fire at all: the tick if the form moved it,
// and otherwise the state the install row prefilled — running, for an install that never chose.
export function patrolEnabledOf(p: PatrolInput | undefined): boolean {
  return p?.enabled ?? true;
}

// patrolCadenceText names a cadence the way an admin would: the chip when one means it, and the
// expression itself when none does.
export function patrolCadenceText(schedule: string): string {
  const iv = patrolIntervalFor(schedule);
  return iv ? `every ${iv}` : `cron ${schedule}`;
}

// patrolSummary is the form's one-line reading of the patrol, in the same words the plan's preview
// uses — "on, every 30m" — so what the admin reads while choosing is what the plan will name on
// the op. It reads the form's answer, which on an edit starts as the row's own.
export function patrolSummary(p: PatrolInput | undefined): string {
  return `${patrolEnabledOf(p) ? "on" : "off"}, ${patrolCadenceText(patrolScheduleOf(p))}`;
}

// patrolCost is the form's warning about a cadence that is very short, or "" for one that is not.
// The plan warns in the same breath; the field says it where the choice is made.
export function patrolCost(p: PatrolInput | undefined): string {
  if (!patrolEnabledOf(p)) return "";
  const mins = patrolIntervalMinutes(patrolScheduleOf(p));
  if (!mins || mins >= PATROL_WARN_MINUTES) return "";
  return `A patrol every ${mins} minutes is up to ${Math.floor(1440 / mins)} model calls a day on this install.`;
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
    w.start && w.end
      ? `${w.start}–${w.end}`
      : w.start
        ? `from ${w.start}`
        : w.end
          ? `until ${w.end}`
          : "all day";
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
