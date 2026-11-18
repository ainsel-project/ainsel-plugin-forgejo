import { useState } from "react";
import type { CSSProperties } from "react";
import { Button, Field, Input, Notice } from "@ainsel/plugin-sdk";
import type { RoleToolSets } from "../api";
import type { Input as InputType, RoleInput, WorkingHours } from "./model";
import {
  ROLE_COLOR,
  TURN_TIMEOUT_DEFAULT_MS,
  newWorkingHours,
  turnTimeoutMinutesFromMs,
  turnTimeoutMsFromMinutes,
  workingHoursText,
} from "./model";
import { FIELD_GRID_STYLE, MONO_FONT, StepFooter } from "./Wizard";

// S4 under the created-only wizard: what the admin still decides per role.
// The pick lists (existing images, existing agents) and the LLM fields are
// gone — a created-only install makes its own image, agent and persona, and
// the LLM each agent runs on is a role setting (Settings page), not a
// per-install fact. What remains is what the plan still reads from the walk:
// the container the role's image is created from, the agent's display name,
// and the role's tool set.
const ROLE_KEYS = ["refiner", "developer", "reviewer"] as const;
type RoleKey = (typeof ROLE_KEYS)[number];

const roleLabel = (role: RoleKey) => role.charAt(0).toUpperCase() + role.slice(1);

// Whether the input says the three roles share one runtime — which is what the
// "use for all" tick means. An all-empty set is not a shared choice: the tick
// marks a decision the admin made, not the blank a new walk starts with.
// (It used to read the LLM too; the LLM left the wizard with the settings move.)
function runtimesShared(roles: InputType["roles"]): boolean {
  return (
    roles.refiner.runtime !== "" &&
    roles.refiner.runtime === roles.developer.runtime &&
    roles.refiner.runtime === roles.reviewer.runtime
  );
}

// Whether a runtime ref names an explicit version: a tag or a @digest. A ref with
// neither floats — the registry resolves it to `latest`, which may not exist at all
// and is never reproducible, and every task pod of the install then fails at pull
// while the tasks sit at QUEUED. The test reads the segment after the last "/", so a
// registry port is not mistaken for a tag: `registry:5000/img` floats,
// `registry:5000/img:8.0` does not. The plan enforces the same rule server-side; this
// is the answer the admin gets without leaving the step.
function runtimeHasVersion(ref: string): boolean {
  if (ref.includes("@")) return true;
  const last = ref.slice(ref.lastIndexOf("/") + 1);
  const i = last.indexOf(":");
  return i > 0 && i < last.length - 1;
}

// What to say about a ref that floats, at the field and in the line naming all of
// them at once. Both forms the fix accepts are named, because either one is enough.
const RUNTIME_VERSION_ERROR = "Add a tag such as :8.0, or a @sha256: digest.";
const RUNTIME_VERSION_SUMMARY =
  "Add a tag such as :8.0, or a @sha256: digest, to the runtime for:";

// The look of one tool chip. A granted tool is ticked in the ok colour; a forbidden one that is
// not granted is dashed and struck through, and it is the one that cannot be selected.
function chipStyle(checked: boolean, addable: boolean): CSSProperties {
  const forbiddenOut = !addable && !checked;
  return {
    display: "inline-flex",
    alignItems: "center",
    gap: "0.375rem",
    padding: "0.25rem 0.625rem",
    borderRadius: "var(--radius-sm, 6px)",
    fontSize: "0.75rem",
    cursor: forbiddenOut ? "not-allowed" : "pointer",
    background: checked ? "color-mix(in srgb, var(--ok) 8%, transparent)" : "var(--paper)",
    color: checked ? "var(--ok)" : forbiddenOut ? "var(--ink-3)" : "var(--ink-2)",
    border: checked
      ? "1px solid color-mix(in srgb, var(--ok) 40%, transparent)"
      : forbiddenOut
        ? "1px dashed var(--rule-soft)"
        : "1px solid var(--rule-soft)",
  };
}

// The day picker's buttons, in the order a working week reads: Monday first, Sunday last. The
// numbers are the hub's — Go's weekday numbers, where Sunday is 0 — and they are what the plan
// sends, so nothing is translated on the way out.
const DAY_PICKER: { day: number; label: string }[] = [
  { day: 1, label: "Mon" },
  { day: 2, label: "Tue" },
  { day: 3, label: "Wed" },
  { day: 4, label: "Thu" },
  { day: 5, label: "Fri" },
  { day: 6, label: "Sat" },
  { day: 0, label: "Sun" },
];

// Whether a day list holds a weekday. A row written by anything that spells Sunday 7 rather than
// 0 holds the same day either way, and the picker shows one Sunday button.
function holdsDay(days: number[], day: number): boolean {
  return days.some((d) => (d === 7 ? 0 : d) === day);
}

// The look of one day button — the same shape as a tool chip, in the quiet colours: a day is not
// granted or forbidden, it is in the window or out of it.
function dayChipStyle(checked: boolean): CSSProperties {
  return {
    display: "inline-flex",
    alignItems: "center",
    padding: "0.25rem 0.625rem",
    borderRadius: "var(--radius-sm, 6px)",
    fontSize: "0.75rem",
    cursor: "pointer",
    background: checked ? "color-mix(in srgb, var(--signal) 10%, transparent)" : "var(--paper)",
    color: checked ? "var(--signal)" : "var(--ink-3)",
    border: checked
      ? "1px solid color-mix(in srgb, var(--signal) 40%, transparent)"
      : "1px solid var(--rule-soft)",
  };
}

interface RolesProps {
  input: InputType;
  setInput: (input: InputType) => void;
  // The set each role starts with and the rails on what it may be given, both from
  // /setup/options — see toolChoices for how they become the checklist.
  tools: RoleToolSets;
  // Whether this walk edits an existing install: an empty runtime then means
  // "keep what the install has" — the plan falls back to the install's prior
  // container — so the step must not refuse it.
  editing: boolean;
  onNext: () => void;
  onBack: () => void;
}

export function Roles({ input, setInput, tools, editing, onNext, onBack }: RolesProps) {
  // Read off the input rather than remembered by this screen: leaving S4 and returning re-mounts
  // it, and a tick the input had no part in would be gone with the screen that held it.
  const [useForAll, setUseForAll] = useState(() => runtimesShared(input.roles));
  // Whether Next has refused since the admin last tried to move on. The per-role runtime fields
  // say which role is short the whole time; the line naming them all at once is only worth its
  // noise after the attempt, and it goes away by itself when nothing is missing any more.
  const [showMissing, setShowMissing] = useState(false);

  // The roles with no runtime, in the order the cards and the preview list them.
  // An edit may leave them all empty — "keep what the install has" is a valid
  // answer there, and the plan honours it — so on an edit nothing is missing.
  const missingRuntimes = editing
    ? []
    : ROLE_KEYS.filter((role) => input.roles[role].runtime === "");

  // The roles whose runtime names no version, in the order the cards and the preview
  // list them. An empty field is not one of them: on an edit empty means "keep what
  // the install has", and a floating ref the install already carries is the plan's
  // warning to make, not a refusal here — an existing install stays editable.
  // Anything the admin types is a new value, and a new floating ref is a broken
  // install, which is what the step refuses.
  const untaggedRuntimes = ROLE_KEYS.filter(
    (role) => input.roles[role].runtime !== "" && !runtimeHasVersion(input.roles[role].runtime),
  );

  const handleNext = () => {
    if (missingRuntimes.length > 0 || untaggedRuntimes.length > 0) {
      // The preview blocks on `role "developer": no runtime container is set`, and on
      // the same sentence for a ref with no tag, one step past the screen where the
      // container was entered. So the step that owns it is the one that refuses.
      setShowMissing(true);
      return;
    }
    onNext();
  };

  const handleRoleChange = (role: RoleKey, updates: Partial<RoleInput>) => {
    const newRoles = { ...input.roles };
    newRoles[role] = { ...newRoles[role], ...updates };
    // While the tick holds, the three roles share one runtime: a change to the
    // refiner's is a change to the set, so the other two go with it — in this one
    // setInput, so no render ever shows the refiner moved and the rest still behind.
    if (useForAll && role === "refiner" && updates.runtime !== undefined) {
      newRoles.developer = { ...newRoles.developer, runtime: updates.runtime };
      newRoles.reviewer = { ...newRoles.reviewer, runtime: updates.runtime };
    }
    setInput({ ...input, roles: newRoles });
  };

  const handleUseForAll = () => {
    if (useForAll) return;
    const refiner = input.roles.refiner;
    // New role objects, not the ones the input holds: the roles are separate values even when
    // they carry the same runtime, and one object in two of them is a change to one felt
    // by both.
    setInput({
      ...input,
      roles: {
        ...input.roles,
        developer: { ...input.roles.developer, runtime: refiner.runtime },
        reviewer: { ...input.roles.reviewer, runtime: refiner.runtime },
      },
    });
  };

  // What the checklist holds for a role: the set it starts with (spec §3.3) and
  // whatever it already has, so a selected tool is never invisible. The rails come
  // from the server too — the same code that refuses a forbidden tool at apply
  // time — because a copy of them in this file is a copy that drifts. (The runtime's
  // own tool list used to be a third source; a created-only install has no picked
  // runtime to read a list off.)
  const toolChoices = (role: RoleKey, roleData: RoleInput) => {
    const forbidden = new Set(tools.forbidden[role] ?? []);
    const seen = new Set<string>();
    const choices: { tool: string; addable: boolean }[] = [];
    for (const tool of [...(tools.defaults[role] ?? []), ...roleData.tools]) {
      if (seen.has(tool)) continue;
      seen.add(tool);
      choices.push({ tool, addable: !forbidden.has(tool) });
    }
    return choices.sort((a, b) => a.tool.localeCompare(b.tool));
  };

  const handleToolToggle = (role: RoleKey, tool: string) => {
    const tools = input.roles[role].tools;
    const newTools = tools.includes(tool) ? tools.filter((t) => t !== tool) : [...tools, tool];
    handleRoleChange(role, { tools: newTools });
  };

  // The bulk actions read `addable` off toolChoices — the single place the two
  // sources and the server's rails are reconciled — and never off the defaults
  // directly. "Check all" adds only what the chips would let you click: a
  // forbidden tool added here would be an input the plan/apply step refuses,
  // turning a convenience click into a broken plan. "Uncheck all" clears the whole
  // selection, forbidden-but-checked tools included — the rails constrain adding,
  // not removing, and a checked forbidden chip exists precisely so the admin can
  // take it away. Each is one handleRoleChange, keeping the one-setInput-per-
  // interaction convention.
  const handleToolSelectAll = (
    role: RoleKey,
    choices: { tool: string; addable: boolean }[],
  ) => {
    const current = input.roles[role].tools;
    const addable = choices.filter((c) => c.addable).map((c) => c.tool);
    // Merge, don't replace: tools the role already holds (offered or not) stay checked.
    handleRoleChange(role, {
      tools: [...current, ...addable.filter((tool) => !current.includes(tool))],
    });
  };

  const handleToolClearAll = (role: RoleKey) => {
    handleRoleChange(role, { tools: [] });
  };

  // The window's days, toggled. Toggling off drops both spellings of Sunday, so the list the
  // plan sends holds a day once however the row it was prefilled from wrote it.
  const handleDayToggle = (role: RoleKey, day: number) => {
    const w = input.roles[role].workingHours;
    if (!w) return;
    const days = holdsDay(w.days, day)
      ? w.days.filter((d) => (d === 7 ? 0 : d) !== day)
      : [...w.days.filter((d) => (d === 7 ? 0 : d) !== day), day];
    handleRoleChange(role, { workingHours: { ...w, days } });
  };

  const handleHoursField = (role: RoleKey, updates: Partial<WorkingHours>) => {
    const w = input.roles[role].workingHours;
    if (!w) return;
    handleRoleChange(role, { workingHours: { ...w, ...updates } });
  };

  // One card per role, stacked: the three are short enough to read together, and the step's
  // refusal names the ones still missing a runtime right where each is entered.
  const renderRoleCard = (role: RoleKey) => {
    const roleData = input.roles[role];
    const choices = toolChoices(role, roleData);
    const addableTools = choices.filter((c) => c.addable).map((c) => c.tool);
    // Both buttons are hidden when the list is, so these only ever describe a visible list.
    const allAddableChecked = addableTools.every((tool) => roleData.tools.includes(tool));
    const runtimeMissing = showMissing && missingRuntimes.includes(role);
    const runtimeUntagged = showMissing && untaggedRuntimes.includes(role);

    return (
      <div
        key={role}
        className="forgejo-role-card"
        style={{
          display: "flex",
          flexDirection: "column",
          gap: "1rem",
          padding: "1rem",
          border: "1px solid var(--rule-soft)",
          borderRadius: "var(--radius, 8px)",
          background: "var(--paper)",
        }}
      >
        <span style={{ display: "inline-flex", alignItems: "center", gap: "0.5rem", fontWeight: 600, fontSize: "0.875rem" }}>
          <span style={{ width: "0.625rem", height: "0.625rem", borderRadius: "999px", background: ROLE_COLOR[role], display: "inline-block" }} />
          {roleLabel(role)}
        </span>

        <div style={FIELD_GRID_STYLE}>
          <Field
            htmlFor={`roles-${role}-agent-name`}
            label="Agent name"
            hint="The agent's display name in AInsel."
          >
            <Input
              id={`roles-${role}-agent-name`}
              value={roleData.agentName}
              onChange={(e: any) => handleRoleChange(role, { agentName: e.target.value })}
              placeholder={`forgejo-${role}-${input.slug}`}
            />
          </Field>

          {/* On an edit the install already has a container, and empty means keep it. */}
          <Field
            htmlFor={`roles-${role}-runtime`}
            label="Runtime image"
            hint={
              editing
                ? "Leave empty to keep the install's current container."
                : "The container image this role's runtime is created from. It needs a tag such as :8.0, or a @sha256: digest."
            }
            error={
              runtimeMissing
                ? "Enter a runtime container for this role."
                : runtimeUntagged
                  ? RUNTIME_VERSION_ERROR
                  : undefined
            }
          >
            <Input
              id={`roles-${role}-runtime`}
              value={roleData.runtime}
              onChange={(e: any) => handleRoleChange(role, { runtime: e.target.value })}
              placeholder="registry.example.com/image:tag"
            />
          </Field>

          {/* The runner's per-turn wall-clock cap, in the minutes the admin thinks
              in; the wire carries milliseconds (issue #61). Empty is never a reset:
              on a new install it means the shipped default, on an edit it means
              keep what the install has — which is what lets a hand-set image value
              survive a re-apply. */}
          <Field
            htmlFor={`roles-${role}-turn-timeout`}
            label="Turn timeout (minutes)"
            hint={
              editing
                ? "Leave empty to keep the install's current value."
                : `Empty uses the shipped default (${turnTimeoutMinutesFromMs(TURN_TIMEOUT_DEFAULT_MS[role])} minutes).`
            }
          >
            <Input
              id={`roles-${role}-turn-timeout`}
              value={turnTimeoutMinutesFromMs(roleData.turnTimeoutMs)}
              onChange={(e: any) =>
                handleRoleChange(role, { turnTimeoutMs: turnTimeoutMsFromMinutes(e.target.value) })
              }
              placeholder={turnTimeoutMinutesFromMs(TURN_TIMEOUT_DEFAULT_MS[role])}
            />
          </Field>
        </div>

        {/* Working hours are optional in the plainest sense the wizard has: unticked, it sends
            nothing for the field, the plan finds no op for it, and the apply writes neither a
            window nor a clear — so a window an admin set in the AInsel console is left alone.
            Ticking it and ticking it back off is the clear: the plan sees the row's window go
            missing and asks the hub to remove it, which is the only way one is ever removed. */}
        <div
          className="forgejo-hours"
          role="group"
          aria-label={`${roleLabel(role)} working hours`}
          style={{ display: "flex", flexDirection: "column", gap: "0.5rem" }}
        >
          <label
            className="forgejo-hours-toggle"
            style={{ display: "inline-flex", alignItems: "center", gap: "0.5rem", fontSize: "0.8125rem", color: "var(--ink-2)" }}
          >
            <input
              className="forgejo-hours-on"
              type="checkbox"
              checked={roleData.workingHours !== undefined}
              onChange={(e: any) =>
                handleRoleChange(role, {
                  workingHours: e.target.checked ? newWorkingHours() : undefined,
                })
              }
            />
            Work only in a set window
          </label>

          {roleData.workingHours !== undefined && (
            <>
              <div
                role="group"
                aria-label={`${roleLabel(role)} working days`}
                style={{ display: "flex", flexWrap: "wrap", gap: "0.375rem" }}
              >
                {DAY_PICKER.map(({ day, label }) => {
                  const checked = holdsDay(roleData.workingHours!.days, day);
                  return (
                    <button
                      key={day}
                      type="button"
                      className="forgejo-hours-day"
                      aria-pressed={checked}
                      onClick={() => handleDayToggle(role, day)}
                      style={dayChipStyle(checked)}
                    >
                      {label}
                    </button>
                  );
                })}
              </div>

              <div style={FIELD_GRID_STYLE}>
                <Field
                  htmlFor={`roles-${role}-hours-start`}
                  label="From"
                  hint="Empty leaves the day open from its start."
                >
                  <Input
                    id={`roles-${role}-hours-start`}
                    type="time"
                    value={roleData.workingHours.start}
                    onChange={(e: any) => handleHoursField(role, { start: e.target.value })}
                    placeholder="09:00"
                  />
                </Field>
                <Field
                  htmlFor={`roles-${role}-hours-end`}
                  label="Until"
                  hint="Earlier than From runs the window past midnight."
                >
                  <Input
                    id={`roles-${role}-hours-end`}
                    type="time"
                    value={roleData.workingHours.end}
                    onChange={(e: any) => handleHoursField(role, { end: e.target.value })}
                    placeholder="17:00"
                  />
                </Field>
                <Field
                  htmlFor={`roles-${role}-hours-tz`}
                  label="Timezone"
                  hint="A zone name such as Europe/Berlin. Empty means UTC."
                >
                  <Input
                    id={`roles-${role}-hours-tz`}
                    value={roleData.workingHours.timezone}
                    onChange={(e: any) => handleHoursField(role, { timezone: e.target.value })}
                    placeholder="UTC"
                  />
                </Field>
              </div>

              {/* The window in one line, in the words the preview uses — so what the admin reads
                  here is what the plan will name on the agent's op. */}
              <p
                className="forgejo-hours-summary"
                style={{ margin: 0, fontSize: "0.75rem", color: "var(--ink-3)", fontFamily: MONO_FONT }}
              >
                {workingHoursText(roleData.workingHours)}
              </p>
            </>
          )}

          {/* What leaving the tick alone means, said where the tick is: "no window" here is not the
              same nothing as a cleared one. An unticked role sends nothing at all, so a window an
              admin set in the AInsel console stays where they set it; a ticked-then-unticked role
              sends the clear, and the hub removes it. */}
          {roleData.workingHours === undefined && (
            <p
              className="forgejo-hours-none-hint"
              style={{ margin: 0, fontSize: "0.75rem", color: "var(--ink-3)" }}
            >
              Left unticked, the agent works around the clock, and a window set in the AInsel
              console stays where it was set.
            </p>
          )}
        </div>

        {/* A tool list is a group of toggles, so it is marked up as the group it is: the label
            names the set, not a member of it. */}
        <div role="group" aria-label={`${roleLabel(role)} tools`} style={{ display: "flex", flexDirection: "column", gap: "0.5rem" }}>
          <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: "0.5rem" }}>
            <span style={{ fontSize: "0.8125rem", fontWeight: 500, color: "var(--ink-2)" }}>Tools</span>
            {choices.length > 0 && (
              <span style={{ marginLeft: "auto", display: "inline-flex", flexWrap: "wrap", gap: "0.5rem" }}>
                <Button
                  onClick={() => handleToolSelectAll(role, choices)}
                  disabled={allAddableChecked}
                >
                  Check all
                </Button>
                <Button
                  onClick={() => handleToolClearAll(role)}
                  disabled={roleData.tools.length === 0}
                >
                  Uncheck all
                </Button>
              </span>
            )}
          </div>
          {/* A role's tool list can run long; the chips wrap and the list scrolls rather than
              pushing the rest of the step off the screen. */}
          <div className="forgejo-tools-scroll" style={{ display: "flex", flexWrap: "wrap", gap: "0.375rem", maxHeight: "16rem", overflowY: "auto" }}>
            {choices.map(({ tool, addable }) => {
              const checked = roleData.tools.includes(tool);
              // A tool the rails forbid stays out of reach while the role does not have it. If
              // the role somehow does, the chip stays clickable: the admin has to be able to take
              // it away.
              const disabled = !addable && !checked;
              const state = checked ? "Granted" : addable ? "Not granted" : "Forbidden for this role";
              return (
                <button
                  key={tool}
                  type="button"
                  className="forgejo-tool-item"
                  aria-pressed={checked}
                  disabled={disabled}
                  title={state}
                  onClick={() => handleToolToggle(role, tool)}
                  style={chipStyle(checked, addable)}
                >
                  <span aria-hidden="true">{checked ? "✓" : addable ? "+" : "⊘"}</span>
                  <span style={{ fontFamily: MONO_FONT, fontSize: "0.6875rem", textDecoration: disabled ? "line-through" : undefined }}>
                    {tool}
                  </span>
                </button>
              );
            })}
            {choices.length === 0 && (
              <p className="forgejo-tools-empty" style={{ margin: 0, fontSize: "0.8125rem", color: "var(--ink-3)" }}>
                No tools to offer for this role.
              </p>
            )}
          </div>
        </div>
      </div>
    );
  };

  return (
    <div className="forgejo-roles" style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      <label className="forgejo-use-for-all" style={{ display: "inline-flex", alignItems: "center", gap: "0.5rem", fontSize: "0.8125rem", color: "var(--ink-2)" }}>
        <input
          type="checkbox"
          checked={useForAll}
          onChange={(e) => {
            setUseForAll(e.target.checked);
            if (e.target.checked) {
              handleUseForAll();
            }
          }}
        />
        Use the refiner&apos;s runtime container for all roles
      </label>

      {ROLE_KEYS.map((role) => renderRoleCard(role))}

      <p style={{ margin: 0, fontSize: "0.75rem", color: "var(--ink-3)" }}>
        Struck-through tools are forbidden for that role by the plugin&apos;s guard rails and cannot be granted.
      </p>

      {showMissing && missingRuntimes.length > 0 && (
        <Notice variant="err">
          Enter a runtime container for: {missingRuntimes.join(", ")}
        </Notice>
      )}

      {showMissing && untaggedRuntimes.length > 0 && (
        <Notice variant="err">
          {RUNTIME_VERSION_SUMMARY} {untaggedRuntimes.join(", ")}
        </Notice>
      )}

      <StepFooter onBack={onBack}>
        <Button variant="primary" onClick={handleNext} className="forgejo-next-btn">
          Continue
        </Button>
      </StepFooter>
    </div>
  );
}
