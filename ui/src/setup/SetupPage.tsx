import { Notice, Panel, usePluginContext } from "@ainsel/plugin-sdk";
import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import type { SetupStep, Input } from "./model";
import { newInput, nextStep, prevStep, TURN_TIMEOUT_DEFAULT_MS } from "./model";
import { Installs } from "./Installs";
import { Instance } from "./Instance";
import { Accounts } from "./Accounts";
import { Roles } from "./Roles";
import { Rules } from "./Rules";
import { Preview, triggerRole } from "./Preview";
import { Connect } from "./Connect";
import { Done } from "./Done";
import { WIZARD_STEPS, WizardPanel } from "./Wizard";
import type { SetupOptions, PlanResponse, ApplyResponse, ApplyReport, Install, RemoveResponse } from "../api";

export function SetupPage() {
  const { api, hub } = usePluginContext();

  // State
  const [currentStep, setCurrentStep] = useState<SetupStep | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [options, setOptions] = useState<SetupOptions | null>(null);
  const [installs, setInstalls] = useState<Install[]>([]);

  // Form state
  const [input, setInput] = useState<Input>(newInput());
  // Whether the current walk edits an existing install (S0 "Edit") or creates a
  // new one. The screens take it as a prop: secret fields read "leave empty to
  // keep" only on an edit, and the account step can be passed on empty there.
  const [editing, setEditing] = useState(false);
  const [plan, setPlan] = useState<PlanResponse | null>(null);
  // Agent display names by id, read from the hub when a plan arrives: the preview names a
  // trigger's agent the way an admin knows it, not by its hub id.
  const [agentNames, setAgentNames] = useState<Record<string, string>>({});
  useEffect(() => {
    if (plan === null) return;
    let cancelled = false;
    hub
      .get("/agents", { query: { pageSize: "200" } })
      .then((body) => {
        if (cancelled) return;
        const items = (body as { items?: { id?: string; displayName?: string }[] } | null)?.items ?? [];
        const names: Record<string, string> = {};
        for (const entry of items) {
          if (entry?.id && entry.displayName) names[entry.id] = entry.displayName;
        }
        setAgentNames(names);
      })
      .catch(() => {
        // Cosmetic: the role's own name stands in until the hub answers.
      });
    return () => {
      cancelled = true;
    };
  }, [plan, hub]);
  const [applyResult, setApplyResult] = useState<ApplyResponse | null>(null);
  // The install S7 is watching. Set by apply (a fresh install, whose webhook has never
  // delivered) and by S0's Connect (an install that is still unverified). S7 reads this and
  // nothing else: the install it polls is the one the admin is looking at.
  const [connectTarget, setConnectTarget] = useState<Install | null>(null);
  // Whether S7 saw a delivery before the admin left it. The done screen says "verified" only
  // when it is true; continuing early leaves it false, which is the normal state of a fresh
  // install rather than a problem.
  const [connectVerified, setConnectVerified] = useState(false);
  const [applyError, setApplyError] = useState<string | null>(null);
  const [applyReport, setApplyReport] = useState<ApplyReport | null>(null);
  // What the step the admin stands on could not do: a plan that would not answer, a removal the
  // hub would not accept. It is shown on that step, which is the one they can correct and ask
  // again — `error` is for the load that leaves the wizard with nothing to show at all.
  const [stepError, setStepError] = useState<string | null>(null);
  // Whether an apply is in flight. The state is what keeps the button out of reach; the ref
  // is what makes the guard hold for a second click that arrives before the re-render.
  const [applying, setApplying] = useState(false);
  const applyingRef = useRef(false);
  // What the last removal left behind, in the admin's words: the checklist is the point of
  // a removal that could not finish, and S0 is where they will read it.
  const [removalNotice, setRemovalNotice] = useState<string | null>(null);
  // What the last walk left on the list in the admin's favour: the install it made. Set when
  // the done screen closes, cleared when the next walk starts.
  const [installedNotice, setInstalledNotice] = useState<string | null>(null);

  // Load options and installs on mount
  useEffect(() => {
    const load = async () => {
      try {
        const opts = (await api.get("/setup/options")) as SetupOptions;
        setOptions(opts);
        const instList = (await api.get("/setup/installs")) as { installs: Install[] };
        setInstalls(instList.installs);
        setCurrentStep("installs");
        setLoading(false);
      } catch (err: any) {
        if (err.status === 401 || err.status === 403) {
          setError("Only admins can access the setup wizard.");
        } else {
          setError(err.message || "Failed to load setup wizard.");
        }
        setLoading(false);
      }
    };
    load();
  }, [api]);

  // What a walk leaves behind, cleared before another one starts. Left standing, they read as
  // news about the walk in hand: a rolled-back list and an error on a preview that has not
  // applied anything yet, a connect target pointing at the last install rather than this one.
  const resetWalk = () => {
    setPlan(null);
    setApplyResult(null);
    setApplyError(null);
    setApplyReport(null);
    setConnectTarget(null);
    setConnectVerified(false);
    setStepError(null);
    setInstalledNotice(null);
  };

  const handleNewInstall = () => {
    const fresh = newInput();
    // One configured instance is not a choice: the step carries it, so the admin
    // who has exactly one Forgejo never sees an empty select for it.
    if (options?.instances?.length === 1) {
      fresh.baseUrl = options.instances[0].baseUrl;
    }
    // The checklist shows a role's starting set already ticked (spec §5[4]): the apply fills
    // the same defaults when a role's list goes in empty, so leaving them unticked here would
    // only ask the admin to tick them by hand to get what they were already going to get.
    for (const role of ["refiner", "developer", "reviewer"] as const) {
      fresh.roles[role].tools = [...(options?.tools.defaults[role] ?? [])];
      // The turn timeout starts at the shipped default, shown in the field's
      // minutes: the admin sees the cap their install will carry and can move
      // it (issue #61). Leaving it as shipped sends the same value the server
      // would fall back to anyway.
      fresh.roles[role].turnTimeoutMs = TURN_TIMEOUT_DEFAULT_MS[role];
    }
    setInput(fresh);
    setEditing(false);
    resetWalk();
    setCurrentStep("instance");
  };

  const handleEditInstall = (slug: string) => {
    const install = installs.find((i) => i.slug === slug);
    if (install && options) {
      // Prefill the input from the install
      const newIn = newInput();
      newIn.slug = install.slug;
      newIn.groupId = install.groupId;
      newIn.baseUrl = install.baseUrl;
      newIn.org = install.org;
      newIn.excludeLabel = install.excludeLabel || "do-not-refine";
      // Admin token is empty for edit (user must provide if needed)

      // Prefill roles from the install. The runtime stays empty: the row keeps
      // the image id, not the container URL, and an empty runtime on an edit
      // means "keep what the install has" — which is exactly what it has.
      // The agent's display name is not on the row either; empty means the
      // default name, which is what a created-only install's agent carries.
      // The working hours do come off the row, and only off it: the row records the window the
      // wizard last wrote, which is what the plan diffs the form against. Reading the agent's
      // live window instead would show an admin's own window as though the wizard had set it —
      // and the next apply would then rewrite it. Copied, not shared: the form edits its own
      // values, and a day list the install row still points at would move with them.
      // The turn timeout is on the row (issue #61): the field shows what the
      // image was last written with, and empty — from a row that predates the
      // option — keeps whatever the image carries.
      const roles = ["refiner", "developer", "reviewer"] as const;
      for (const role of roles) {
        const roleState = install.roles[role];
        if (roleState) {
          newIn.roles[role] = {
            runtime: "",
            agentName: "",
            tools: roleState.tools || [],
            workingHours: roleState.workingHours
              ? {
                  timezone: roleState.workingHours.timezone || "",
                  days: [...(roleState.workingHours.days ?? [])],
                  start: roleState.workingHours.start || "",
                  end: roleState.workingHours.end || "",
                }
              : undefined,
            turnTimeoutMs: roleState.turnTimeoutMs || "",
          };
        }
      }

      setInput(newIn);
      setEditing(true);
      resetWalk();
      setCurrentStep("instance");
    }
  };

  const handleConnectInstall = (slug: string) => {
    const install = installs.find((i) => i.slug === slug);
    if (install) {
      setConnectTarget(install);
      setCurrentStep("connect");
    }
  };

  const handleRemoveInstall = async (slug: string, adminToken?: string) => {
    setRemovalNotice(null);
    setStepError(null);
    try {
      // The token goes in the body. The console's client sends a body with POST — a DELETE
      // route would have to do without the token, and the removal would then always leave
      // the bots and the team behind — and a token in a URL is a token in an access log (R4).
      const res = (await api.post(`/setup/installs/${slug}/remove`, {
        adminToken: adminToken || "",
      })) as RemoveResponse;
      setRemovalNotice(removalMessage(slug, res));
      // Reload installs
      const instList = (await api.get("/setup/installs")) as { installs: Install[] };
      setInstalls(instList.installs);
    } catch (err: any) {
      // The list is where the admin was standing and where they will want to try again, so the
      // failure is the list's to show — not a full screen with no way out of it.
      setStepError(err.message || "Failed to remove install.");
    }
  };

  // The address a walk submits. On an edit the instance step can be left on
  // "Keep <url>" — the install's own address, which is not on the configured
  // list. The server keeps the row's address when the request leaves baseUrl
  // blank (plan.go's wanted-install rule) and refuses a non-empty address
  // nobody configured, so the keep state has to go out blank: that blank is the
  // only submit that keeps an unlisted address, which is what the step's notice
  // and docs/setup.md's "Editing an install" promise. Picking a listed instance
  // still submits it, because that is a move the admin chose.
  const submitBaseUrl = (): string => {
    if (!editing || !input.baseUrl) return input.baseUrl;
    const listed = (options?.instances ?? []).some((i) => i.baseUrl === input.baseUrl);
    return listed ? input.baseUrl : "";
  };

  const handleNext = async () => {
    // Moving on is the admin's way of saying the last failure is behind them.
    setStepError(null);
    const next = nextStep(currentStep as SetupStep);
    if (next === "preview") {
      // Call plan API
      try {
        const response = (await api.post("/setup/plan", {
          slug: input.slug,
          groupId: input.groupId,
          baseUrl: submitBaseUrl(),
          org: input.org,
          adminToken: input.adminToken,
          excludeLabel: input.excludeLabel,
          roles: input.roles,
        })) as PlanResponse;
        setPlan(response);
        setCurrentStep(next);
      } catch (err: any) {
        // Stay on the step that asked: the plan's own message goes above the screen the admin
        // can still correct, and Next asks again. Taking the screen away would end a walk that
        // has done nothing wrong.
        setStepError(err.message || "Failed to plan install.");
      }
    } else if (next) {
      setCurrentStep(next);
    }
  };

  const handleApply = async () => {
    // Apply creates users, teams, images, agents and a hook. A second submit while the first
    // is still answering meets resources that already exist and fails on them, so the walk
    // answers one apply per click.
    if (applyingRef.current) return;
    applyingRef.current = true;
    setApplying(true);
    setApplyError(null);
    setApplyReport(null);
    try {
      const response = (await api.post("/setup/apply", {
        slug: input.slug,
        groupId: input.groupId,
        baseUrl: submitBaseUrl(),
        org: input.org,
        adminToken: input.adminToken,
        excludeLabel: input.excludeLabel,
        roles: input.roles,
      })) as ApplyResponse;

      // Clear the secret out of the walk's state — through setState, on a new object, so no
      // component still holding the previous input reads a field the apply emptied under it.
      // The role LLM keys used to go with it; the wizard carries no keys any more.
      setInput({
        ...input,
        adminToken: "",
      });

      setApplyResult(response);
      setApplyError(null);
      setApplyReport(null);
      setConnectTarget(response.install);
      setCurrentStep("connect");
      // Reload installs
      const instList = (await api.get("/setup/installs")) as { installs: Install[] };
      setInstalls(instList.installs);
    } catch (err: any) {
      // A 502 from Apply carries the hub's or Forgejo's own message and the
      // report of what was created, rolled back and left behind (spec §5[8]).
      // The console's request() puts the parsed body on `body`.
      setApplyError(err.message || "Failed to apply install.");
      const report = err.body?.report ?? err.report;
      setApplyReport(report ?? null);
    } finally {
      applyingRef.current = false;
      setApplying(false);
    }
  };

  const handleBack = () => {
    setStepError(null);
    // A walk that reached S7 has an install on the list by the time it leaves it — whether the
    // apply just made it or S0 offered one that had never delivered. The way out is that list:
    // going back into the walk would put the preview, and a second Apply on resources that
    // exist now, back under the admin's cursor.
    if (currentStep === "connect") {
      setCurrentStep("installs");
      return;
    }

    const back = prevStep(currentStep as SetupStep);
    if (back) {
      setCurrentStep(back);
    } else if (currentStep !== "installs") {
      setCurrentStep("installs");
    }
  };

  const handleDone = () => {
    setCurrentStep("installs");
    setInput(newInput());
    setEditing(false);
    resetWalk();
  };

  // Leaving S7. A walk that applied an install ends on the done screen, which says what was
  // made and whether the webhook has seen an event; a Connect opened from the list has no
  // apply behind it and just goes back to the list.
  const handleConnectDone = (verified: boolean) => {
    if (applyResult) {
      setConnectVerified(verified);
      setCurrentStep("done");
    } else {
      handleDone();
    }
  };

  // Closing the done screen ends the walk for good. The list says once what the walk made —
  // resetWalk above clears it, so it goes with the next walk that starts.
  const handleFinishWalk = () => {
    const slug = applyResult?.install.slug;
    handleDone();
    if (slug) setInstalledNotice(`"${slug}" was installed.`);
  };

  // Cancel leaves the walk for the list, the way Back does from its first step. It is not a way
  // back into a walk: the answers are kept on the form but nothing is planned or applied by it.
  const handleCancelWalk = () => {
    setStepError(null);
    setCurrentStep("installs");
  };

  // The walk's panel. Steps behind the cursor are done and, while the walk can still be
  // re-entered, clickable. A Connect opened from the list did none of the steps, and a Connect
  // after an apply must not reach the plan again: its done steps show, and none of them is
  // reachable.
  const renderWalk = (step: SetupStep, children: ReactNode) => {
    const index = WIZARD_STEPS.findIndex((s) => s.step === step);
    const fromList = step === "connect" && !applyResult;
    const title = fromList && connectTarget
      ? `Connect ${connectTarget.slug}`
      : editing
        ? `Edit ${input.slug}`
        : "New install";
    return (
      <WizardPanel
        step={step}
        title={title}
        completed={fromList ? 0 : index}
        onPick={step === "connect" ? undefined : handlePick}
        onCancel={handleCancelWalk}
      >
        {children}
      </WizardPanel>
    );
  };

  // Going back through the step list is Back, reached from any finished step: the same walk, the
  // same answers, with the error the step showed cleared as Back clears it.
  const handlePick = (step: SetupStep) => {
    setStepError(null);
    setCurrentStep(step);
  };

  if (loading) {
    return <Panel className="forgejo-loading">Loading...</Panel>;
  }

  if (error) {
    return (
      <Panel className="forgejo-error">
        <Notice variant="err">{error}</Notice>
      </Panel>
    );
  }

  if (!options) {
    return (
      <Panel className="forgejo-error">
        <Notice variant="err">Could not load options.</Notice>
      </Panel>
    );
  }

  if (currentStep === "installs" || !currentStep) {
    return (
      <Installs
        installs={installs}
        groups={options.groups}
        instances={options.instances}
        notice={removalNotice}
        installedNotice={installedNotice}
        error={stepError}
        onNewInstall={handleNewInstall}
        onEditInstall={handleEditInstall}
        onConnectInstall={handleConnectInstall}
        onRemoveInstall={handleRemoveInstall}
      />
    );
  }

  if (currentStep === "instance") {
    return renderWalk(
      "instance",
      <StepError error={stepError}>
        <Instance
          input={input}
          setInput={setInput}
          groups={options.groups}
          pluginGroups={options.pluginGroups}
          instances={options.instances ?? []}
          currentBaseUrl={editing ? installs.find((i) => i.slug === input.slug)?.baseUrl : undefined}
          onNext={handleNext}
          onBack={handleBack}
        />
      </StepError>,
    );
  }

  if (currentStep === "accounts") {
    return renderWalk(
      "accounts",
      <StepError error={stepError}>
        <Accounts
          input={input}
          setInput={setInput}
          editing={editing}
          onNext={handleNext}
          onBack={handleBack}
        />
      </StepError>,
    );
  }

  if (currentStep === "roles") {
    return renderWalk(
      "roles",
      <StepError error={stepError}>
        <Roles
          input={input}
          setInput={setInput}
          tools={options.tools}
          editing={editing}
          onNext={handleNext}
          onBack={handleBack}
        />
      </StepError>,
    );
  }

  if (currentStep === "rules") {
    return renderWalk(
      "rules",
      <StepError error={stepError}>
        <Rules
          input={input}
          setInput={setInput}
          onNext={handleNext}
          onBack={handleBack}
        />
      </StepError>,
    );
  }

  if (currentStep === "preview" && plan) {
    return renderWalk(
      "preview",
      <>
        {applyError && (
          <div className="forgejo-apply-error">
            <Notice variant="err">
              <div>{applyError}</div>
              {applyReport && applyReport.rolledBack.length > 0 && (
                <div>
                  <div>Rolled back:</div>
                  <ul>
                    {applyReport.rolledBack.map((op) => (
                      <li key={`${op.kind}/${op.name}`}>
                        {op.kind}: {op.name}
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              {applyReport && applyReport.leftovers.length > 0 && (
                <div>
                  <div>Left behind — remove manually:</div>
                  <ul>
                    {applyReport.leftovers.map((op) => (
                      <li key={`${op.kind}/${op.name}`}>
                        {op.kind}: {op.name}
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </Notice>
          </div>
        )}
        <Preview
          plan={plan}
          agentLabel={(trig) => {
            // An agent that stands is named by the hub; one the apply has yet to create
            // by the name this walk gives it — typed, or the role's generated default.
            if (trig.agentRef && agentNames[trig.agentRef]) return agentNames[trig.agentRef];
            const role = triggerRole(trig.name) as keyof typeof input.roles | null;
            if (role && input.roles[role]) return input.roles[role].agentName || `forgejo-${role}-${input.slug}`;
            return trig.agentRef || "—";
          }}
          applying={applying}
          onApply={handleApply}
          onBack={handleBack}
        />
      </>,
    );
  }

  if (currentStep === "connect") {
    // The install S0 offered or apply just made. Reading it off applyResult alone — as this
    // did — meant Connect from S0 rendered "Unknown step: connect" and went nowhere.
    const target = connectTarget;
    if (target) {
      return renderWalk(
        "connect",
        <StepError error={stepError}>
          <Connect
            install={target}
            onDone={handleConnectDone}
            onBack={handleBack}
          />
        </StepError>,
      );
    }
  }

  if (currentStep === "done" && applyResult) {
    return (
      <Done
        install={applyResult.install}
        report={applyResult.report}
        verified={connectVerified}
        onClose={handleFinishWalk}
      />
    );
  }

  return (
    <Panel className="forgejo-setup-error">
      <Notice variant="err">Unknown step: {currentStep}</Notice>
    </Panel>
  );
}

// A step's own failure, rendered above the step it happened on rather than in place of it: the
// message and the screen the admin can correct and ask again from have to be on the same page.
function StepError({ error, children }: { error: string | null; children: ReactNode }) {
  if (!error) return <>{children}</>;
  return (
    <>
      <div className="forgejo-step-error">
        <Notice variant="err">{error}</Notice>
      </div>
      {children}
    </>
  );
}

// What a removal leaves the admin with. A row that survived says the hub kept part of the
// install and the removal can be tried again; a row that went with a checklist behind it
// says the Forgejo side is the admin's to finish by hand.
function removalMessage(slug: string, res: RemoveResponse): string | null {
  if (res.checklist.length === 0) return null;
  const names = res.checklist.join(", ");
  if (res.kept) {
    return (
      `"${slug}" is still listed: the hub kept ${res.checklist.length} of its resources ` +
      `(${names}). Remove it again once the hub answers.`
    );
  }
  return (
    `"${slug}" is removed, but ${res.checklist.length} Forgejo resources are left behind ` +
    `(${names}). Remove them by hand.`
  );
}
