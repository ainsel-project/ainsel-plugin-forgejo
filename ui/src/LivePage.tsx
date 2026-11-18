import { Button, Notice, Panel, RegisterTable, Tabs, Tag, usePluginContext } from "@ainsel/plugin-sdk";
import { useEffect, useState, type ReactNode } from "react";
import type { Repo, Run, WorkResponse } from "./api";
import { DASH, formatAgo, formatCost, formatDuration, formatRelative } from "./format";

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
// The page re-fetches every REFRESH_MS, and the operator can pause that. There
// is no server push, and the hub polls the same way one level up; a board that
// lies about the last thirty seconds is still the freshest honest answer
// available.

const HOUR_MS = 60 * 60 * 1000;

// The window the whole floor covers: numbers, charts, timelines. One day,
// because the page is "what are the agents doing", and a run from last Tuesday
// is not what an agent is doing.
const WINDOW_MS = 24 * HOUR_MS;

// How often the board refetches. Slow enough that the page costs about
// nothing while open, fast enough that "live" is not a stretch.
const REFRESH_MS = 30_000;

// A failure this recent raises the banner. The banner answers "did something
// just break"; the day's failure count is the KPI's job.
const FAILURE_BANNER_MS = HOUR_MS;

// The height of the throughput chart's plot, in px. Each stacked bar is a share
// of it, so the tallest hour fills it exactly.
const TRACK_PX = 160;

// The newest finished runs the queue's "Recently finished" tab shows.
const RECENT_LIMIT = 6;

// The statuses that mean a run has not finished. Same pair the backend's
// work.Active keeps; the board's queues split them into waiting and working.
const STATUS_QUEUED = "queued";
const STATUS_RUNNING = "running";

// The hub ends an invocation as success, failure or timeout (the hub's
// invocations package). A failure and a timeout both mean the run did not do
// its work, so the board counts them as one kind of failed run.
const STATUS_SUCCESS = "success";
const STATUS_FAILURE = "failure";
const STATUS_TIMEOUT = "timeout";

// Each role gets a colour, and the same colour means the same thing in every
// band: the throughput chart, the split bar, the timelines and the queue rows
// all carry roles. A role the plugin does not know (an install may add its own)
// still draws — in the idle grey — rather than vanishing from the chart because
// nobody anticipated it.
// Each colour is a console theme token behind a plugin-level name, so the
// console's theme moves every hue on the board and an operator can still
// re-point one role (`--live-developer: …`) without touching the bundle. No
// literal colours: the bundle test refuses anything the theme cannot move.
const ROLE_COLORS: Record<string, string> = {
  refiner: "var(--live-refiner, var(--warn))",
  developer: "var(--live-developer, var(--signal))",
  reviewer: "var(--live-reviewer, var(--stale))",
};
const ROLE_OTHER = "var(--ink-4)";

function roleColor(role: string): string {
  return ROLE_COLORS[role] ?? ROLE_OTHER;
}

// roleLabel names a role the way a person reads it: `Developer`, not `developer`.
function roleLabel(role: string): string {
  return role.charAt(0).toUpperCase() + role.slice(1);
}

// rolesOf lists the roles the runs carry, the known ones in their fixed order and
// any others after them alphabetically. The stack and the legend both read this,
// so a role sits in the same place in every bar.
function rolesOf(runs: Run[]): string[] {
  const seen = new Set(runs.map((run) => run.role));
  const known = Object.keys(ROLE_COLORS).filter((role) => seen.has(role));
  const other = [...seen].filter((role) => !Object.keys(ROLE_COLORS).includes(role)).sort();
  return [...known, ...other];
}

// Flatten the response to bare runs. The nested pull requests are worked runs
// like any other, so they join the board beside their parent issue rather than
// hiding inside it.
function flattenRuns(repos: Repo[]): Run[] {
  const runs: Run[] = [];
  for (const repo of repos) {
    for (const item of repo.items) {
      runs.push(...item.runs);
      for (const pr of item.prs) {
        runs.push(...pr.runs);
      }
    }
  }
  return runs;
}

function isActive(run: Run): boolean {
  return run.status === STATUS_QUEUED || run.status === STATUS_RUNNING;
}

function isFailed(run: Run): boolean {
  return run.status === STATUS_FAILURE || run.status === STATUS_TIMEOUT;
}

// endOf is when a run reached the state it is in: when it ended, or when it
// started if it has not ended. The failure banner and the "failed 18 min ago"
// labels age a run by it.
function endOf(run: Run): number {
  return new Date(run.end ?? run.start).getTime();
}

// plural says a count with its noun, `1 run` or `3 runs`, so no sentence on the
// board reads `1 runs`.
function plural(count: number, noun: string): string {
  return `${count} ${noun}${count === 1 ? "" : "s"}`;
}

// spendOf adds up what a set of runs cost, the way the work screens do. The sum is
// over the runs whose cost was measured, and it is known only when every run's was:
// one unpriced run makes the total unknown, which formatCost draws as a dash.
// That is the backend's rule for an item's or a repository's cost, applied here to
// whatever set of runs a figure is about.
function spendOf(runs: Run[]): { costUsd: number; known: boolean; unpriced: number } {
  let costUsd = 0;
  let unpriced = 0;
  for (const run of runs) {
    if (!run.costKnown) {
      unpriced += 1;
      continue;
    }
    costUsd += run.costUsd ?? 0;
  }
  return { costUsd, known: unpriced === 0, unpriced };
}

// successPercent is the share of finished runs that did not fail, or null when
// nothing has finished yet. A run still going has no outcome, so it is not in the
// share at all; a board that counted it as a success would flatter the agents.
function successPercent(runs: Run[]): number | null {
  const finished = runs.filter((run) => !isActive(run));
  if (finished.length === 0) return null;
  const failed = finished.filter(isFailed).length;
  return ((finished.length - failed) / finished.length) * 100;
}

// One row of the agents table: what the agent is, what it holds now, and the
// runs its timeline is drawn from.
interface AgentRow {
  agent: string;
  // The role the agent's newest run was made in: the colour of its avatar.
  role: string;
  current: Run | null;
  runs: Run[];
  durationMs: number;
}

// groupAgents builds one row per agent, ordered by its most recent activity,
// so whoever just did something sits at the top even if its run finished and
// its "Current work" cell is empty.
function groupAgents(runs: Run[]): AgentRow[] {
  const byAgent = new Map<string, Run[]>();
  for (const run of runs) {
    const owned = byAgent.get(run.agent) ?? [];
    owned.push(run);
    byAgent.set(run.agent, owned);
  }
  const rows: AgentRow[] = [];
  for (const [agent, owned] of byAgent) {
    owned.sort((a, b) => new Date(b.start).getTime() - new Date(a.start).getTime());
    // A running run is what the agent is doing; a queued one is what it does
    // next. So a running run wins even when a queued one started earlier.
    const current =
      owned.find((run) => run.status === STATUS_RUNNING) ??
      owned.find((run) => run.status === STATUS_QUEUED) ??
      null;
    rows.push({
      agent,
      role: owned[0].role,
      current,
      runs: owned,
      durationMs: owned.reduce((total, run) => total + (run.durationMs ?? 0), 0),
    });
  }
  rows.sort(
    (a, b) =>
      new Date(b.runs[0].start).getTime() - new Date(a.runs[0].start).getTime(),
  );
  return rows;
}

// AgentStatus is what the fleet's status pill says about an agent right now.
type AgentStatus = "running" | "queued" | "failed" | "idle";

// agentStatus reads an agent's row. Working beats waiting, and both beat the last
// run having failed: an agent that is busy now is busy whatever its last run did.
function agentStatus(row: AgentRow): AgentStatus {
  if (row.current?.status === STATUS_RUNNING) return "running";
  if (row.current) return "queued";
  if (row.runs.length > 0 && isFailed(row.runs[0])) return "failed";
  return "idle";
}

// The pills use the console's tag variants: a working agent is healthy (ok), a
// waiting one is pending (warn), a failed one is an error, and an idle one is
// quiet (stale).
type TagVariant = "default" | "ok" | "warn" | "err" | "stale";

const AGENT_STATUS: Record<AgentStatus, { label: string; variant: TagVariant }> = {
  running: { label: "Running", variant: "ok" },
  queued: { label: "Queued", variant: "warn" },
  failed: { label: "Last run failed", variant: "err" },
  idle: { label: "Idle", variant: "stale" },
};

// runStatus names a run's status for the queue's pill. An unknown status keeps
// its own word rather than being guessed at.
function runStatus(run: Run): { label: string; variant: TagVariant } {
  switch (run.status) {
    case STATUS_RUNNING:
      return { label: "Running", variant: "ok" };
    case STATUS_QUEUED:
      return { label: "Queued", variant: "warn" };
    case STATUS_SUCCESS:
      return { label: "Succeeded", variant: "ok" };
    case STATUS_FAILURE:
      return { label: "Failed", variant: "err" };
    case STATUS_TIMEOUT:
      return { label: "Timed out", variant: "err" };
    default:
      return { label: run.status, variant: "default" };
  }
}

// runsPerHour buckets run *starts* into 24 hourly bins, oldest first, and keeps
// each bin's count per role so the chart can stack it. Starts rather than
// finishes: the board shows work being made, and a run still going at the end of
// the window is work that began.
interface HourBin {
  total: number;
  byRole: Record<string, number>;
}

function runsPerHour(runs: Run[], now: number): HourBin[] {
  const bins: HourBin[] = Array.from({ length: 24 }, () => ({ total: 0, byRole: {} }));
  for (const run of runs) {
    const age = now - new Date(run.start).getTime();
    if (age < 0 || age >= WINDOW_MS) continue;
    const bin = bins[23 - Math.floor(age / HOUR_MS)];
    bin.total += 1;
    bin.byRole[run.role] = (bin.byRole[run.role] ?? 0) + 1;
  }
  return bins;
}

// hourLabel says which hour a bin covers, counting back from now: `0–1h ago` is
// the newest bin, `23–24h ago` the oldest.
function hourLabel(index: number): string {
  return `${23 - index}–${24 - index}h ago`;
}

// roleSplit adds up finished run time per role for the split bar. A run that
// has not ended contributes nothing: its time is not known yet, and guessing
// it from "still going" would inflate the role of whatever is running when the
// page loads.
function roleSplit(runs: Run[]): { role: string; ms: number }[] {
  const byRole = new Map<string, number>();
  for (const run of runs) {
    if (run.durationMs === undefined) continue;
    byRole.set(run.role, (byRole.get(run.role) ?? 0) + run.durationMs);
  }
  return [...byRole.entries()]
    .map(([role, ms]) => ({ role, ms }))
    .sort((a, b) => b.ms - a.ms);
}

// subjectKind is what a run's thread is, in the words the work page uses.
function subjectKind(run: Run): string {
  return run.subject.kind === "pulls" ? "PR" : "Issue";
}

// subjectLabel names a run's thread the way the work page names it: `PR #7` or
// `Issue #12`, so the two screens read as one product.
function subjectLabel(run: Run): string {
  return `${subjectKind(run)} #${run.subject.number}`;
}

// ThreadLink is a run's thread as it should read and behave: a link out to the
// issue or pull, opening in a new tab so the board stays put, and drawn as a
// link (underlined) rather than as text that only happens to have an href. The
// label defaults to the thread's name; the queue passes the title alone, since
// its own column already says what kind of thread it is.
function ThreadLink({ run, label }: { run: Run; label?: string }) {
  const text =
    label ?? `${subjectLabel(run)}${run.subject.title ? ` ${run.subject.title}` : ""}`;
  return (
    <a
      href={run.url}
      target="_blank"
      rel="noreferrer"
      className="forgejo-item-link"
      style={{ textDecoration: "underline", textUnderlineOffset: 2 }}
    >
      {text}
    </a>
  );
}

// Timeline draws one agent's last day as a strip: a grey track with the
// agent's runs painted over it in their role's colour. Overlapping runs (an
// agent can be woken twice) simply paint over each other, oldest first. Faint
// quarter gridlines sit under the runs, so a run's place in the day reads
// against the six-hour marks.
function Timeline({ runs, now }: { runs: Run[]; now: number }) {
  const start = now - WINDOW_MS;
  const segments = runs
    .map((run) => {
      const from = Math.max(new Date(run.start).getTime(), start);
      const to = run.end ? Math.min(new Date(run.end).getTime(), now) : now;
      if (to <= from) return null;
      return {
        key: run.id,
        x: ((from - start) / WINDOW_MS) * 480,
        width: Math.max(2, ((to - from) / WINDOW_MS) * 480),
        fill: roleColor(run.role),
      };
    })
    .filter((segment): segment is NonNullable<typeof segment> => segment !== null);
  return (
    <svg
      viewBox="0 0 480 12"
      preserveAspectRatio="none"
      role="img"
      aria-label="Agent activity over the last 24 hours"
      style={{ width: "100%", height: 12, display: "block", minWidth: 120 }}
    >
      <rect x="0" y="3" width="480" height="6" rx="3" fill="var(--rule-soft)" />
      {[120, 240, 360].map((x) => (
        <line
          key={x}
          x1={x}
          x2={x}
          y1="0"
          y2="12"
          stroke="var(--hair)"
          strokeWidth="1"
          vectorEffect="non-scaling-stroke"
        />
      ))}
      {segments.map((segment) => (
        <rect
          key={segment.key}
          x={segment.x}
          y="3"
          width={segment.width}
          height="6"
          rx="3"
          fill={segment.fill}
        />
      ))}
    </svg>
  );
}

// Throughput draws the 24 hourly bins as bars stacked by role, with the hour scale
// spelled out under them and a caption saying what a bar is — a chart nobody can
// read is decoration. Bars rather than a line: a single run in an hour is a real
// event worth a visible mark, and a line through a zero reads as "about half a run".
// The stack shows which roles did the hour's work, which a single height cannot.
function Throughput({ runs, now }: { runs: Run[]; now: number }) {
  const roles = rolesOf(runs);
  const bins = runsPerHour(runs, now);
  const peak = Math.max(0, ...bins.map((bin) => bin.total));
  const caption =
    peak === 0
      ? "No runs started in the last 24 hours."
      : `Each bar is one hour, stacked by role; its height is the runs started in it — ${
          peak === 1 ? "one run in the busiest hour" : `up to ${peak} in the busiest hour`
        }.`;
  return (
    <div className="forgejo-live-throughput">
      <div
        className="forgejo-live-legend"
        style={{ display: "flex", flexWrap: "wrap", gap: 14, fontSize: 12, color: "var(--ink-2)" }}
      >
        {roles.map((role) => (
          <span key={role} style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
            <span
              aria-hidden="true"
              style={{
                width: 8,
                height: 8,
                borderRadius: 2,
                background: roleColor(role),
                display: "inline-block",
              }}
            />
            {roleLabel(role)}
          </span>
        ))}
      </div>
      <div
        style={{
          position: "relative",
          height: TRACK_PX,
          marginTop: 12,
          borderBottom: "1px solid var(--rule-soft)",
        }}
      >
        <div
          aria-hidden="true"
          style={{ position: "absolute", inset: 0, display: "flex", flexDirection: "column", justifyContent: "space-between" }}
        >
          <div style={{ borderTop: "1px dashed var(--rule-soft)" }} />
          <div style={{ borderTop: "1px dashed var(--rule-soft)" }} />
          <div style={{ borderTop: "1px dashed var(--rule-soft)" }} />
          <div />
        </div>
        <div
          role="img"
          aria-label="Runs started per hour over the last 24 hours, stacked by role"
          style={{ position: "absolute", inset: 0, display: "flex", alignItems: "flex-end", gap: 4 }}
        >
          {bins.map((bin, index) => {
            const parts = roles.filter((role) => bin.byRole[role]);
            const breakdown = parts.map((role) => `${roleLabel(role)} ${bin.byRole[role]}`).join(", ");
            return (
              <div
                key={index}
                title={`${hourLabel(index)}: ${plural(bin.total, "run")} started${breakdown ? ` (${breakdown})` : ""}`}
                style={{
                  flex: "1 1 0",
                  height: "100%",
                  display: "flex",
                  flexDirection: "column-reverse",
                  gap: 1,
                }}
              >
                {parts.map((role) => (
                  <div
                    key={role}
                    style={{
                      height: `${(bin.byRole[role] / peak) * 100}%`,
                      background: roleColor(role),
                      borderRadius: 2,
                    }}
                  />
                ))}
              </div>
            );
          })}
        </div>
      </div>
      <div
        className="forgejo-live-spark-axis"
        style={{ display: "flex", justifyContent: "space-between", fontSize: 11, color: "var(--ink-3)", marginTop: 6 }}
      >
        <span>24h</span>
        <span>18h</span>
        <span>12h</span>
        <span>6h</span>
        <span>Now</span>
      </div>
      <p className="forgejo-live-spark-caption" style={{ fontSize: 12, color: "var(--ink-3)", margin: "8px 0 0" }}>
        {caption}
      </p>
    </div>
  );
}

// RoleTime is the split panel's body: one labelled bar per role, its time and its
// share, and the spend for the same runs at the foot. The bars are the share of
// finished run time, so they add to one hundred per cent between them.
function RoleTime({ runs }: { runs: Run[] }) {
  const split = roleSplit(runs);
  const total = split.reduce((sum, part) => sum + part.ms, 0);
  const spend = spendOf(runs);
  return (
    <>
      {total === 0 ? (
        <p className="forgejo-live-queue-empty">No finished run time in the last 24 hours.</p>
      ) : (
        split.map((part) => {
          const share = (part.ms / total) * 100;
          return (
            <div key={part.role} style={{ display: "flex", flexDirection: "column", gap: 6 }}>
              <div style={{ display: "flex", justifyContent: "space-between", gap: 8, fontSize: 13 }}>
                <span style={{ display: "inline-flex", alignItems: "center", gap: 8, fontWeight: 500 }}>
                  <span
                    aria-hidden="true"
                    style={{
                      width: 8,
                      height: 8,
                      borderRadius: 2,
                      background: roleColor(part.role),
                      display: "inline-block",
                    }}
                  />
                  {roleLabel(part.role)}
                </span>
                <span style={{ color: "var(--ink-2)", fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" }}>
                  {formatDuration(part.ms)} · {Math.round(share)}%
                </span>
              </div>
              <div style={{ height: 8, background: "var(--paper-2)", borderRadius: 4, overflow: "hidden" }}>
                <div style={{ height: 8, width: `${share}%`, background: roleColor(part.role), borderRadius: 4 }} />
              </div>
            </div>
          );
        })
      )}
      <div
        style={{
          marginTop: "auto",
          paddingTop: 12,
          borderTop: "1px solid var(--rule-soft)",
          display: "flex",
          justifyContent: "space-between",
          alignItems: "baseline",
          gap: 8,
          fontSize: 13,
        }}
      >
        <span style={{ color: "var(--ink-2)" }}>Spend · 24h</span>
        <span style={{ fontWeight: 600, fontVariantNumeric: "tabular-nums" }}>
          {formatCost(spend.costUsd, spend.known)}
          {spend.unpriced > 0 ? (
            <span style={{ display: "block", fontSize: 12, fontWeight: 400, color: "var(--ink-3)" }}>
              {spend.unpriced} unpriced
            </span>
          ) : null}
        </span>
      </div>
    </>
  );
}

// Kpi is one headline figure: what it counts, the number, and a line saying what
// the number is made of, so a bare `3` always has its "which" beside it.
function Kpi({ label, value, sub }: { label: string; value: string; sub: string }) {
  return (
    <Panel className="forgejo-live-stat-card">
      <div className="forgejo-live-stat" style={{ display: "flex", flexDirection: "column", gap: 6 }}>
        <span className="forgejo-live-stat-label" style={{ fontSize: 13, color: "var(--ink-2)", fontWeight: 500 }}>
          {label}
        </span>
        <b
          className="forgejo-live-stat-value"
          style={{ fontSize: 26, lineHeight: "32px", fontWeight: 600, fontVariantNumeric: "tabular-nums" }}
        >
          {value}
        </b>
        <span className="forgejo-live-stat-sub" style={{ fontSize: 12, color: "var(--ink-3)" }}>
          {sub}
        </span>
      </div>
    </Panel>
  );
}

// LiveStatus is the page's claim that it is watching: a pill that pulses while the
// board is alive and says how long ago it last heard from the plugin. It ticks on
// its own clock, so the seconds keep moving between refreshes rather than freezing
// until the next fetch. The keyframes ride with the component because the plugin
// ships no stylesheet of its own and the pulse is the only animation on the page.
function LiveStatus({ updatedAt, paused }: { updatedAt: number | null; paused: boolean }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (paused) return;
    // Catch up at once: a resume should not show the seconds the pause stopped at.
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [paused]);

  if (paused) {
    return (
      <Tag variant="stale">
        <span
          className="forgejo-live-dot"
          aria-hidden="true"
          style={{ width: 8, height: 8, borderRadius: 4, background: "var(--stale)", display: "inline-block" }}
        />
        Paused
      </Tag>
    );
  }
  const text = updatedAt === null ? "Live · loading" : `Live · updated ${formatAgo(now - updatedAt)}`;
  return (
    <Tag variant="ok">
      <style>{`@keyframes forgejo-live-pulse{0%,100%{opacity:1}50%{opacity:.35}}`}</style>
      <span
        className="forgejo-live-dot"
        aria-hidden="true"
        style={{
          width: 8,
          height: 8,
          borderRadius: 4,
          background: "var(--ok)",
          display: "inline-block",
          animation: "forgejo-live-pulse 2s ease-in-out infinite",
        }}
      />
      {text}
    </Tag>
  );
}

// TabCount is the number beside a queue tab's name. It is a pill of its own so the
// count reads as a figure, not as a word that happens to end the label.
function TabCount({ n }: { n: number }) {
  return (
    <span
      className="forgejo-live-count"
      style={{
        display: "inline-block",
        minWidth: 18,
        padding: "0 6px",
        marginLeft: 6,
        borderRadius: 9,
        background: "var(--paper-2)",
        color: "var(--ink-2)",
        fontSize: 11,
        lineHeight: "18px",
        textAlign: "center",
      }}
    >
      {n}
    </span>
  );
}

// QueueTab is which band of the run queue is showing.
type QueueTab = "running" | "queued" | "finished";

const QUEUE_EMPTY: Record<QueueTab, string> = {
  running: "Nothing running.",
  queued: "Nothing queued.",
  finished: "Nothing finished yet.",
};

const QUEUE_TIME_HEADER: Record<QueueTab, string> = {
  running: "Started",
  queued: "Queued",
  finished: "Finished",
};

// queueColumns is the run queue's table. Only the time column changes with the
// tab: it is when the run started, was queued, or finished, and the header says
// which.
function queueColumns(tab: QueueTab, nowMs: number, agentName: (id: string) => string) {
  const clock = new Date(nowMs);
  return [
    {
      key: "run",
      header: "Run",
      cell: (run: Run) => (
        <span
          title={run.id}
          style={{ fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace", fontSize: 12, color: "var(--ink-2)" }}
        >
          {run.id.slice(0, 8)}
        </span>
      ),
    },
    {
      key: "thread",
      header: "Issue / PR",
      cell: (run: Run) => (
        <span style={{ display: "flex", gap: 8, alignItems: "baseline", minWidth: 0 }}>
          <span style={{ color: "var(--ink-3)", whiteSpace: "nowrap" }}>{subjectLabel(run)}</span>
          {run.url ? (
            <ThreadLink run={run} label={run.subject.title || "(untitled)"} />
          ) : (
            <span>{run.subject.title || "(untitled)"}</span>
          )}
        </span>
      ),
    },
    {
      key: "repo",
      header: "Repository",
      cell: (run: Run) => <span style={{ color: "var(--ink-2)" }}>{run.subject.repo}</span>,
    },
    {
      key: "agent",
      header: "Agent",
      cell: (run: Run) => (
        <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
          <span
            aria-hidden="true"
            style={{
              width: 8,
              height: 8,
              borderRadius: 4,
              background: roleColor(run.role),
              display: "inline-block",
              flex: "0 0 auto",
            }}
          />
          {agentName(run.agent)}
        </span>
      ),
    },
    {
      key: "when",
      header: QUEUE_TIME_HEADER[tab],
      cell: (run: Run) =>
        formatRelative(tab === "finished" ? (run.end ?? run.start) : run.start, clock),
    },
    {
      key: "duration",
      header: "Duration",
      align: "right" as const,
      cell: (run: Run) => {
        if (run.status === STATUS_RUNNING) {
          return formatDuration(nowMs - new Date(run.start).getTime());
        }
        if (isActive(run) || run.durationMs === undefined) return DASH;
        return formatDuration(run.durationMs);
      },
    },
    {
      key: "status",
      header: "Status",
      cell: (run: Run) => {
        const status = runStatus(run);
        return <Tag variant={status.variant}>{status.label}</Tag>;
      },
    },
  ];
}

// Scroll gives a table its own horizontal scrollbar, so a narrow window scrolls the
// table rather than the whole page.
function Scroll({ minWidth, children }: { minWidth: number; children: ReactNode }) {
  return (
    <div style={{ overflowX: "auto" }}>
      <div style={{ minWidth }}>{children}</div>
    </div>
  );
}

export function LivePage() {
  const { api } = usePluginContext();
  const [runs, setRuns] = useState<Run[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [updatedAt, setUpdatedAt] = useState<number | null>(null);
  const [paused, setPaused] = useState(false);
  const [queueTab, setQueueTab] = useState<QueueTab>("running");

  useEffect(() => {
    // Paused, there is no timer and no fetch: the board holds what it last showed
    // until the operator resumes, which fetches at once rather than waiting out
    // the rest of an interval.
    if (paused) return;
    let cancelled = false;
    // Runs come from the plugin's own work route, each carrying its agent's id
    // (ruling C3) — what identifies — and the display name the plugin resolved for
    // it — what a human reads.
    const load = () => {
      api
        .get("/work", { query: { since: "1d" } })
        .then((response) => {
          if (cancelled) return;
          setRuns(flattenRuns((response as WorkResponse).repos));
          setError(null);
          setUpdatedAt(Date.now());
        })
        .catch((err) => {
          if (!cancelled) setError(err.message);
        });
    };
    load();
    const timer = setInterval(load, REFRESH_MS);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [api, paused]);

  // agentName reads an agent the way a human would: the display name the work API
  // resolved onto its runs, the id where it could not (a deleted agent whose runs
  // outlived it keeps an honest, if unreadable, name).
  const agentNames: Record<string, string> = {};
  for (const run of runs ?? []) {
    if (run.agentName) agentNames[run.agent] = run.agentName;
  }
  const agentName = (id: string) => agentNames[id] ?? id;

  if (error !== null) {
    return <p className="forgejo-error">Could not reach work API</p>;
  }
  if (runs === null) {
    return <p className="forgejo-status">Loading...</p>;
  }

  // Every figure on the board is measured from the same moment: the fetch that
  // produced the runs. Measuring from the render instead would let a slow render
  // age a timeline's runs against a clock the data never saw.
  const nowMs = updatedAt ?? Date.now();
  const clock = new Date(nowMs);
  const active = runs.filter(isActive);
  const running = active.filter((run) => run.status === STATUS_RUNNING);
  const queued = active.filter((run) => run.status === STATUS_QUEUED);
  const finished = runs
    .filter((run) => !isActive(run))
    .sort((a, b) => endOf(b) - endOf(a));
  const recent = finished.slice(0, RECENT_LIMIT);
  const failures = finished.filter(
    (run) => isFailed(run) && nowMs - endOf(run) < FAILURE_BANNER_MS,
  );
  const agents = groupAgents(runs);
  const runningAgents = new Set(running.map((run) => run.agent));
  const waitingAgents = new Set(
    queued.map((run) => run.agent).filter((agent) => !runningAgents.has(agent)),
  );
  const failedRuns = runs.filter(isFailed).length;
  const success = successPercent(runs);
  const spend = spendOf(runs);
  const totalMs = runs.reduce((total, run) => total + (run.durationMs ?? 0), 0);
  const oldestQueued = queued.reduce(
    (min, run) => Math.min(min, new Date(run.start).getTime()),
    Infinity,
  );
  const oldestWait = Math.floor((nowMs - oldestQueued) / 60_000);
  const queueByTab: Record<QueueTab, Run[]> = { running, queued, finished: recent };
  const queueRuns = queueByTab[queueTab];
  const latestFailure = failures[0];

  const agentColumns = [
    {
      key: "agent",
      header: "Agent",
      cell: (row: AgentRow) => (
        <span style={{ display: "inline-flex", alignItems: "center", gap: 10, minWidth: 0 }}>
          <span
            aria-hidden="true"
            style={{
              width: 28,
              height: 28,
              borderRadius: 14,
              background: `color-mix(in srgb, ${roleColor(row.role)} 16%, transparent)`,
              color: roleColor(row.role),
              display: "inline-flex",
              alignItems: "center",
              justifyContent: "center",
              fontSize: 11,
              fontWeight: 700,
              flex: "0 0 auto",
            }}
          >
            {initials(agentName(row.agent))}
          </span>
          <span className="forgejo-live-agent" style={{ fontWeight: 600 }}>
            {agentName(row.agent)}
          </span>
        </span>
      ),
    },
    {
      key: "status",
      header: "Status",
      cell: (row: AgentRow) => {
        const status = AGENT_STATUS[agentStatus(row)];
        return <Tag variant={status.variant}>{status.label}</Tag>;
      },
    },
    {
      key: "work",
      header: "Current work",
      cell: (row: AgentRow) => {
        // A working agent's current run; an agent whose last run failed points at
        // that run, so the failure has a thread to go to. Otherwise there is none.
        const failed = agentStatus(row) === "failed";
        const run = row.current ?? (failed ? row.runs[0] : null);
        if (!run) return DASH;
        const state = row.current
          ? `${run.status} ${formatDuration(nowMs - new Date(run.start).getTime())}`
          : `failed ${formatRelative(run.end ?? run.start, clock)}`;
        return (
          <span style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
            {run.url ? <ThreadLink run={run} /> : subjectLabel(run)}
            <span style={{ fontSize: 12, color: "var(--ink-3)" }}>
              {run.subject.repo} · {state}
            </span>
          </span>
        );
      },
    },
    {
      key: "timeline",
      header: "Last 24 hours",
      cell: (row: AgentRow) => <Timeline runs={row.runs} now={nowMs} />,
    },
    {
      key: "runs",
      header: "Runs",
      align: "right" as const,
      cell: (row: AgentRow) => String(row.runs.length),
    },
    {
      key: "success",
      header: "Success",
      align: "right" as const,
      cell: (row: AgentRow) => {
        const pct = successPercent(row.runs);
        if (pct === null) return DASH;
        return (
          <span style={{ color: pct < 95 ? "var(--err)" : undefined, fontVariantNumeric: "tabular-nums" }}>
            {pct === 100 ? "100%" : `${pct.toFixed(1)}%`}
          </span>
        );
      },
    },
    {
      key: "time",
      header: "Time",
      align: "right" as const,
      cell: (row: AgentRow) =>
        row.durationMs === 0 ? DASH : formatDuration(row.durationMs),
    },
    {
      key: "cost",
      header: "Cost",
      align: "right" as const,
      cell: (row: AgentRow) => {
        const agentSpend = spendOf(row.runs);
        return formatCost(agentSpend.costUsd, agentSpend.known);
      },
    },
  ];

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
          <b>{plural(failures.length, "run")} failed in the last hour.</b> Latest:{" "}
          {agentName(latestFailure.agent)} on{" "}
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
          sub={
            queued.length === 0
              ? "Nothing waiting"
              : `Oldest waiting ${oldestWait < 1 ? "<1" : oldestWait}m`
          }
        />
        <Kpi label="Runs · 24h" value={String(runs.length)} sub={`${failedRuns} failed`} />
        <Kpi
          label="Success rate"
          value={success === null ? DASH : `${success.toFixed(1)}%`}
          sub={
            finished.length === 0
              ? "No run has finished yet"
              : `Of ${plural(finished.length, "finished run")}`
          }
        />
        <Kpi
          label="Agent time · 24h"
          value={totalMs === 0 ? DASH : formatDuration(totalMs)}
          sub="Finished runs only"
        />
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

      <Panel className="forgejo-live-agents" title="Agent fleet">
        <Scroll minWidth={1040}>
          <RegisterTable
            rows={agents}
            columns={agentColumns}
            rowKey={(row: AgentRow) => row.agent}
            withRowNumbers={false}
            emptyLabel="No agent activity in the last 24 hours"
          />
        </Scroll>
      </Panel>

      <Panel
        className="forgejo-live-queue-panel"
        title="Run queue"
        right={
          <span style={{ fontSize: 12, color: "var(--ink-3)" }}>
            {paused ? "Paused: not refreshing" : "Refreshes every 30 s"}
          </span>
        }
      >
        <div style={{ marginBottom: 12 }}>
          <Tabs
            aria-label="Run queue"
            value={queueTab}
            onChange={(next: string) => setQueueTab(next as QueueTab)}
            tabs={[
              { value: "running", label: <>Running<TabCount n={running.length} /></> },
              { value: "queued", label: <>Queued<TabCount n={queued.length} /></> },
              { value: "finished", label: <>Recently finished<TabCount n={recent.length} /></> },
            ]}
          />
        </div>
        <Scroll minWidth={880}>
          <RegisterTable
            rows={queueRuns}
            columns={queueColumns(queueTab, nowMs, agentName)}
            rowKey={(run: Run) => run.id}
            withRowNumbers={false}
            emptyLabel={QUEUE_EMPTY[queueTab]}
          />
        </Scroll>
      </Panel>
    </div>
  );
}

// initials gives an agent's avatar its letters: the first letter of its first two
// words (`forgejo-developer-demo` reads as FD, `Developer 1` as D1), or the first
// two letters of a single word.
function initials(name: string): string {
  const words = name.split(/[\s_-]+/).filter(Boolean);
  if (words.length >= 2) return (words[0][0] + words[1][0]).toUpperCase();
  return name.slice(0, 2).toUpperCase();
}
