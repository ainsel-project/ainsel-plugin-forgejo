// LivePage is the manifest's page "/now" ("Live"), the board that replaces the
// old Active-runs list. Where the work page answers "what happened on which
// thread", this page answers "what are the agents doing": a self-updating floor
// of the last day of work — a failure banner when something just broke, the
// headline figures, a throughput chart and the time split by role, one row per
// agent with a timeline of its day and what it holds right now, and the run
// queue of what is working, waiting, and just finished.
//
// It reads one endpoint — /work with a one-day window — rather than /work plus
// /now: the work response already carries the queued and running runs (only
// the work page hides them behind its grouping), and a board assembled from
// two fetches can show a run as both working and finished for the few seconds
// the two answers disagree. One fetch, one moment.
//
// This file is the composition root only: it fetches through useLiveBoard,
// derives the board's figures from the runs with the helpers in live/model.ts,
// and places the panels. The drawing lives beside them — live/parts.tsx for the
// pieces (KPI, status pill, thread link), live/charts.tsx for the three
// drawings, live/AgentFleet.tsx and live/RunQueue.tsx for the two tables. The
// exported name is the manifest's contract: `ui.pages[].component` names
// "LivePage", and the console resolves it against the built bundle, so it stays
// exported from here.

import { Button, Notice, Panel, usePluginContext } from "@ainsel/plugin-sdk";
import { DASH, formatCost, formatDuration } from "./format";
import { AgentFleet } from "./live/AgentFleet";
import { RoleTime, Throughput } from "./live/charts";
import {
  agentNames,
  endOf,
  FAILURE_BANNER_MS,
  groupAgents,
  isFailed,
  isActive,
  plural,
  RECENT_LIMIT,
  roleSplit,
  spendOf,
  successPercent,
  STATUS_QUEUED,
  STATUS_RUNNING,
  STATUS_TIMEOUT,
  subjectLabel,
} from "./live/model";
import { Kpi, LiveStatus, ThreadLink } from "./live/parts";
import { RunQueue } from "./live/RunQueue";
import { useLiveBoard } from "./live/useLiveBoard";

export function LivePage() {
  const { api } = usePluginContext();
  const { runs, error, updatedAt, paused, setPaused } = useLiveBoard(api);

  // agentName reads an agent the way a human would: the display name the work API
  // resolved onto its runs, the id where it could not (a deleted agent whose runs
  // outlived it keeps an honest, if unreadable, name).
  const agentName = agentNames(runs ?? []);

  if (error !== null) {
    return <p className="forgejo-error">Could not reach work API</p>;
  }
  if (runs === null) {
    return <p className="forgejo-status">Loading...</p>;
  }

  // Every figure on the board is measured from the same moment: the fetch that
  // produced the runs. Measuring from the render instead would let a slow render
  // age a timeline's runs against a clock the data never saw.
  // eslint-disable-next-line react-hooks/purity -- unreachable fallback: every path that sets `runs` also sets `updatedAt`, so the impure read never answers.
  const nowMs = updatedAt ?? Date.now();
  const active = runs.filter(isActive);
  const running = active.filter((run) => run.status === STATUS_RUNNING);
  const queued = active.filter((run) => run.status === STATUS_QUEUED);
  const finished = runs.filter((run) => !isActive(run)).sort((a, b) => endOf(b) - endOf(a));
  const recent = finished.slice(0, RECENT_LIMIT);
  const failures = finished.filter((run) => isFailed(run) && nowMs - endOf(run) < FAILURE_BANNER_MS);
  const agents = groupAgents(runs);
  const runningAgents = new Set(running.map((run) => run.agent));
  const waitingAgents = new Set(queued.map((run) => run.agent).filter((agent) => !runningAgents.has(agent)));
  const failedRuns = runs.filter(isFailed).length;
  const success = successPercent(runs);
  const spend = spendOf(runs);
  const totalMs = runs.reduce((total, run) => total + (run.durationMs ?? 0), 0);
  const oldestQueued = queued.reduce((min, run) => Math.min(min, new Date(run.start).getTime()), Infinity);
  const oldestWait = Math.floor((nowMs - oldestQueued) / 60_000);
  const latestFailure = failures[0];

  return (
    <div className="forgejo-live-page" style={{ display: "flex", flexDirection: "column", gap: 16 }}>
      <div
        className="forgejo-live-header"
        style={{ display: "flex", flexWrap: "wrap", alignItems: "center", justifyContent: "space-between", gap: 10 }}
      >
        <LiveStatus updatedAt={updatedAt} paused={paused} />
        <Button onClick={() => setPaused((value) => !value)} className="forgejo-live-pause">
          {paused ? "Resume" : "Pause"}
        </Button>
      </div>

      {latestFailure && (
        <Notice variant="err">
          <b>{plural(failures.length, "run")} failed in the last hour.</b> Latest: {agentName(latestFailure.agent)} on{" "}
          {latestFailure.url ? <ThreadLink run={latestFailure} /> : subjectLabel(latestFailure)} (
          {latestFailure.status === STATUS_TIMEOUT ? "timed out" : "failed"}).
        </Notice>
      )}

      <div
        className="forgejo-live-stats"
        style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(170px, 1fr))", gap: 12 }}
      >
        <Kpi
          label="Active agents"
          value={`${runningAgents.size + waitingAgents.size} / ${agents.length}`}
          sub={`${runningAgents.size} running · ${waitingAgents.size} waiting`}
        />
        <Kpi
          label="Queued runs"
          value={String(queued.length)}
          sub={queued.length === 0 ? "Nothing waiting" : `Oldest waiting ${oldestWait < 1 ? "<1" : oldestWait}m`}
        />
        <Kpi label="Runs · 24h" value={String(runs.length)} sub={`${failedRuns} failed`} />
        <Kpi
          label="Success rate"
          value={success === null ? DASH : `${success.toFixed(1)}%`}
          sub={finished.length === 0 ? "No run has finished yet" : `Of ${plural(finished.length, "finished run")}`}
        />
        <Kpi label="Agent time · 24h" value={totalMs === 0 ? DASH : formatDuration(totalMs)} sub="Finished runs only" />
        <Kpi
          label="Spend · 24h"
          value={formatCost(spend.costUsd, spend.known)}
          sub={
            spend.unpriced > 0
              ? `${spend.unpriced} of ${runs.length} runs unpriced`
              : `Across ${plural(runs.length, "run")}`
          }
        />
      </div>

      <div className="forgejo-live-charts" style={{ display: "flex", flexWrap: "wrap", gap: 16 }}>
        <div style={{ flex: "2 1 480px", minWidth: 0 }}>
          <Panel
            className="forgejo-live-spark"
            title="Throughput"
            right={<span style={{ fontSize: 12, color: "var(--ink-3)" }}>{plural(runs.length, "run")} in 24h</span>}
          >
            <Throughput runs={runs} now={nowMs} />
          </Panel>
        </div>
        <div style={{ flex: "1 1 300px", minWidth: 0 }}>
          <Panel
            className="forgejo-live-split"
            title="Agent time by role"
            right={
              <span style={{ fontSize: 12, color: "var(--ink-3)" }}>
                {formatDuration(roleSplit(runs).reduce((total, part) => total + part.ms, 0))}
              </span>
            }
          >
            <RoleTime runs={runs} />
          </Panel>
        </div>
      </div>

      <AgentFleet agents={agents} nowMs={nowMs} agentName={agentName} />

      <RunQueue
        queueByTab={{ running, queued, finished: recent }}
        nowMs={nowMs}
        agentName={agentName}
        paused={paused}
      />
    </div>
  );
}
