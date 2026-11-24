// The board's three drawings: an agent's day as a strip, the day's throughput as
// stacked hourly bars, and finished run time split by role. Each takes the runs
// and the moment they were fetched, and draws nothing else — no fetch, no
// header, no state. The page places them.

import { formatCost, formatDuration } from "../format";
import {
  roleColor,
  roleLabel,
  roleSplit,
  rolesOf,
  hourLabel,
  plural,
  runsPerHour,
  spendOf,
  TRACK_PX,
  WINDOW_MS,
} from "./model";
import type { Run } from "../api";

// Timeline draws one agent's last day as a strip: a grey track with the
// agent's runs painted over it in their role's colour. Overlapping runs (an
// agent can be woken twice) simply paint over each other, oldest first. Faint
// quarter gridlines sit under the runs, so a run's place in the day reads
// against the six-hour marks.
export function Timeline({ runs, now }: { runs: Run[]; now: number }) {
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
        <rect key={segment.key} x={segment.x} y="3" width={segment.width} height="6" rx="3" fill={segment.fill} />
      ))}
    </svg>
  );
}

// Throughput draws the 24 hourly bins as bars stacked by role, with the hour scale
// spelled out under them and a caption saying what a bar is — a chart nobody can
// read is decoration. Bars rather than a line: a single run in an hour is a real
// event worth a visible mark, and a line through a zero reads as "about half a run".
// The stack shows which roles did the hour's work, which a single height cannot.
export function Throughput({ runs, now }: { runs: Run[]; now: number }) {
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
          style={{
            position: "absolute",
            inset: 0,
            display: "flex",
            flexDirection: "column",
            justifyContent: "space-between",
          }}
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
export function RoleTime({ runs }: { runs: Run[] }) {
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
