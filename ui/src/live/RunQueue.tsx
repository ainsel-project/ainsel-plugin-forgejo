// The run queue: what is working, what is waiting, and what just finished. Which
// of the three is showing is the panel's own state — the page has no use for it —
// and the only thing the tabs change about the table is what its time column means.

import { Panel, RegisterTable, Tabs, Tag } from "@ainsel/plugin-sdk";
import { useState } from "react";
import { DASH, formatDuration, formatRelative } from "../format";
import { isActive, roleColor, runStatus, subjectLabel, STATUS_RUNNING } from "./model";
import type { Run } from "../api";
import { Scroll, TabCount, ThreadLink } from "./parts";

// QueueTab is which band of the run queue is showing.
export type QueueTab = "running" | "queued" | "finished";

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
      cell: (run: Run) => formatRelative(tab === "finished" ? (run.end ?? run.start) : run.start, clock),
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

// RunQueue is the "Run queue" panel: the tabs, their counts, and the table for
// whichever tab is open. `paused` only reaches the caption saying whether the
// board still refreshes — the queue shows what the last fetch answered.
export function RunQueue({
  queueByTab,
  nowMs,
  agentName,
  paused,
}: {
  queueByTab: Record<QueueTab, Run[]>;
  nowMs: number;
  agentName: (id: string) => string;
  paused: boolean;
}) {
  const [tab, setTab] = useState<QueueTab>("running");
  return (
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
          value={tab}
          onChange={(next: string) => setTab(next as QueueTab)}
          tabs={[
            {
              value: "running",
              label: (
                <>
                  Running
                  <TabCount n={queueByTab.running.length} />
                </>
              ),
            },
            {
              value: "queued",
              label: (
                <>
                  Queued
                  <TabCount n={queueByTab.queued.length} />
                </>
              ),
            },
            {
              value: "finished",
              label: (
                <>
                  Recently finished
                  <TabCount n={queueByTab.finished.length} />
                </>
              ),
            },
          ]}
        />
      </div>
      <Scroll minWidth={880}>
        <RegisterTable
          rows={queueByTab[tab]}
          columns={queueColumns(tab, nowMs, agentName)}
          rowKey={(run: Run) => run.id}
          withRowNumbers={false}
          emptyLabel={QUEUE_EMPTY[tab]}
        />
      </Scroll>
    </Panel>
  );
}
