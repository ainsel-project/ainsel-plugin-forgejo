// The fleet panel: one row per agent of the last day, with its status, what it
// holds, the strip of its day, and its totals. The columns are built here rather
// than in the page because they are this table's shape and nothing else's.

import { Panel, RegisterTable, Tag } from "@ainsel/plugin-sdk";
import { DASH, formatCost, formatDuration, formatRelative } from "../format";
import { AGENT_STATUS, agentStatus, initials, roleColor, spendOf, subjectLabel, successPercent } from "./model";
import type { AgentRow } from "./model";
import { Timeline } from "./charts";
import { Scroll, ThreadLink } from "./parts";

// AgentFleet is the "Agent fleet" panel: the agents of the window, most recently
// active first, each with its day beside it.
export function AgentFleet({
  agents,
  nowMs,
  agentName,
}: {
  agents: AgentRow[];
  nowMs: number;
  agentName: (id: string) => string;
}) {
  const clock = new Date(nowMs);

  const columns = [
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
      cell: (row: AgentRow) => (row.durationMs === 0 ? DASH : formatDuration(row.durationMs)),
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
    <Panel className="forgejo-live-agents" title="Agent fleet">
      <Scroll minWidth={1040}>
        <RegisterTable
          rows={agents}
          columns={columns}
          rowKey={(row: AgentRow) => row.agent}
          withRowNumbers={false}
          emptyLabel="No agent activity in the last 24 hours"
        />
      </Scroll>
    </Panel>
  );
}
