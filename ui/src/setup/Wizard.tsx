// The frame every step of the new-install walk is drawn in: one panel, a step list on the left
// that says where the admin is, and the step's own title, sentence and form on the right. The
// steps supply their forms; this file owns what they have in common, so the six screens cannot
// drift apart in how they show their place or where Back and the primary action sit.
import { Button, Panel } from "@ainsel/plugin-sdk";
import type { ReactNode } from "react";
import type { SetupStep } from "./model";

export interface WizardStepInfo {
  step: SetupStep;
  // The step list's name for the step, and the one line under it.
  label: string;
  sub: string;
  // The heading and the sentence on the step's own screen.
  title: string;
  desc: string;
}

// The walk, in order. Done is not in it: the done screen is what the walk ends on, after the
// connect step, and it has a screen of its own.
export const WIZARD_STEPS: WizardStepInfo[] = [
  {
    step: "instance",
    label: "Instance",
    sub: "Forgejo, slug and group",
    title: "Where Forgejo is",
    desc: "Name the install, choose the group that owns its agents, and point it at your Forgejo organization.",
  },
  {
    step: "accounts",
    label: "Bot accounts",
    sub: "Users and teams",
    title: "Create bot accounts",
    desc: "The plugin creates one bot user and team per role, so every comment and push is attributable.",
  },
  {
    step: "roles",
    label: "Agents",
    sub: "Names, images, tools",
    title: "Configure the agents",
    desc: "One agent per role. Models and personas come from Settings, so here you only name the agents and choose their tools.",
  },
  {
    step: "rules",
    label: "Rules",
    sub: "Labels",
    title: "Routing rules",
    desc: "How work moves between the roles on this install.",
  },
  {
    step: "preview",
    label: "Review plan",
    sub: "What will be created",
    title: "Review the plan",
    desc: "Nothing has been written yet. Applying creates exactly the resources below, and rolls back if any step fails.",
  },
  {
    step: "connect",
    label: "Connect",
    sub: "Verify the webhook",
    title: "Verify the connection",
    desc: "The plugin created the organization webhook. Trigger any event to prove deliveries arrive.",
  },
];

interface WizardPanelProps {
  // The step on screen. It must be one of WIZARD_STEPS.
  step: SetupStep;
  // The panel header: "New install", "Edit <slug>" or "Connect <slug>".
  title: string;
  // How many steps before the current one count as done (shown ✓ and clickable when `onPick`
  // is given). The walk passes the steps it really went through: a Connect opened from the
  // installs list did none of them, so none of them read as done.
  completed: number;
  // Going back through the step list. Left undefined where going back would re-run a finished
  // walk (after an apply), so the completed steps stay on show but cannot be reached.
  onPick?: (step: SetupStep) => void;
  onCancel: () => void;
  children: ReactNode;
}

export function WizardPanel({ step, title, completed, onPick, onCancel, children }: WizardPanelProps) {
  const current = WIZARD_STEPS.findIndex((s) => s.step === step);
  const info = WIZARD_STEPS[current];

  return (
    <Panel
      className="forgejo-wizard"
      title={title}
      right={
        <Button onClick={onCancel} className="forgejo-cancel-btn">
          Cancel
        </Button>
      }
    >
      <p className="forgejo-wizard-subtitle">
        {`Step ${current + 1} of ${WIZARD_STEPS.length} · nothing is created until you apply the plan`}
      </p>

      <div style={{ display: "flex", flexWrap: "wrap", gap: "1.5rem", alignItems: "flex-start" }}>
        <ol
          aria-label="Setup steps"
          style={{
            listStyle: "none",
            margin: 0,
            padding: 0,
            flex: "1 1 200px",
            maxWidth: "16rem",
            display: "flex",
            flexDirection: "column",
            gap: "0.25rem",
          }}
        >
          {WIZARD_STEPS.map((s, index) => {
            const done = index < completed;
            const isCurrent = index === current;
            // A step is reachable only when it is behind the cursor and the screen allows going
            // back to it. Ahead of the cursor nothing is reachable: the next step is the one the
            // step's own Continue answers for, and the current one has nothing to go to.
            const reachable = done && onPick !== undefined;
            return (
              <li key={s.step}>
                <button
                  type="button"
                  className="forgejo-wizard-step"
                  disabled={!reachable}
                  aria-current={isCurrent ? "step" : undefined}
                  onClick={() => onPick?.(s.step)}
                  style={{
                    display: "flex",
                    gap: "0.75rem",
                    alignItems: "flex-start",
                    width: "100%",
                    padding: "0.625rem 0.75rem",
                    border: 0,
                    borderRadius: "var(--radius-sm, 6px)",
                    background: isCurrent ? "var(--signal-haze)" : "transparent",
                    color: isCurrent ? "var(--ink)" : "var(--ink-2)",
                    textAlign: "left",
                    cursor: reachable ? "pointer" : "default",
                    font: "inherit",
                  }}
                >
                  <span
                    aria-hidden="true"
                    style={{
                      width: "1.5rem",
                      height: "1.5rem",
                      borderRadius: "999px",
                      display: "inline-flex",
                      alignItems: "center",
                      justifyContent: "center",
                      flex: "0 0 auto",
                      boxSizing: "border-box",
                      fontSize: "0.75rem",
                      fontWeight: 600,
                      ...(done
                        ? { background: "var(--signal)", color: "var(--paper)", border: "1px solid var(--signal)" }
                        : isCurrent
                          ? { background: "var(--paper)", color: "var(--signal)", border: "2px solid var(--signal)" }
                          : { background: "var(--paper)", color: "var(--ink-3)", border: "1px solid var(--hair)" }),
                    }}
                  >
                    {done ? "✓" : index + 1}
                  </span>
                  <span style={{ display: "flex", flexDirection: "column", gap: "0.125rem" }}>
                    <span style={{ fontWeight: 600 }}>{s.label}</span>
                    <span style={{ fontSize: "0.75rem", color: "var(--ink-3)" }}>{s.sub}</span>
                  </span>
                </button>
              </li>
            );
          })}
        </ol>

        <div style={{ flex: "999 1 480px", minWidth: 0, display: "flex", flexDirection: "column", gap: "1.25rem" }}>
          <div style={{ display: "flex", flexDirection: "column", gap: "0.25rem" }}>
            <h4 style={{ margin: 0, fontSize: "1.125rem", fontWeight: 600 }}>{info.title}</h4>
            <p style={{ margin: 0, fontSize: "0.8125rem", color: "var(--ink-2)", maxWidth: "40rem" }}>{info.desc}</p>
          </div>
          {children}
        </div>
      </div>
    </Panel>
  );
}

// The footer of a step: Back on the left, the step's own actions on the right. Each step passes
// its primary action, because which one it is (Continue, Apply N changes, Done) is the step's
// to say, and whether it is out of reach is too.
export function StepFooter({ onBack, children }: { onBack?: () => void; children?: ReactNode }) {
  return (
    <div
      className="forgejo-buttons"
      style={{
        display: "flex",
        flexWrap: "wrap",
        justifyContent: "space-between",
        alignItems: "center",
        gap: "0.5rem",
        paddingTop: "1rem",
        borderTop: "1px solid var(--rule-soft)",
      }}
    >
      {onBack ? (
        <Button onClick={onBack} className="forgejo-back-btn">
          Back
        </Button>
      ) : (
        <span />
      )}
      <span style={{ display: "inline-flex", flexWrap: "wrap", gap: "0.5rem" }}>{children}</span>
    </div>
  );
}

// The monospace stack for names the admin may copy: slugs, bot logins, image references. The
// bundle ships no stylesheet, so it is written out where it is used.
export const MONO_FONT = "ui-monospace, SFMono-Regular, Menlo, monospace";
