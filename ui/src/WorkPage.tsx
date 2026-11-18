import { Button, Field, Input, Panel, RegisterTable, Select, Tabs, Tag, usePluginContext } from "@ainsel/plugin-sdk";
import { useEffect, useState } from "react";
import type { Item, Repo, Run, WorkResponse } from "./api";
import {
  DASH,
  formatCost,
  formatDuration,
  formatRelative,
  isActiveRun,
  isFailedRun,
  newestRun,
  statusLabel,
  statusTone,
  threadRuns,
  threadStatusRun,
  threadTotals,
} from "./format";
import { MONO, OPEN_BUTTON, WorkItemDetail } from "./WorkItemDetail";

// A row is one issue or pull request, and the repo URL the item does not carry of its
// own. The /work response nests a pull request under the issue it references, so a
// top-level item is already one row: the issue with its pull requests in the row, and a
// pull request that references no issue on its own.
interface Row {
  item: Item;
  repoUrl: string;
}

// The hand-off stages in the order an agent hand-off happens, so the column reads as the story
// of the work rather than as a set. The role ids are what the hub stores on each run.
const STAGES = [
  { role: "refiner", name: "Refine" },
  { role: "developer", name: "Develop" },
  { role: "reviewer", name: "Review" },
];

// Roles outside the hand-off sort after these, alphabetically, in the role filter.
const ROLE_ORDER = STAGES.map((stage) => stage.role);

// The periods the select offers, each a `since` window the /work endpoint reads directly: a
// whole number of days. Thirty days is the default: the window the server answers when asked for
// nothing, and the one the GitHub plugin keeps too.
const PERIODS = [
  { value: "1d", label: "Last 24 hours" },
  { value: "7d", label: "Last 7 days" },
  { value: "30d", label: "Last 30 days" },
];
const DEFAULT_PERIOD = "30d";

// The rows-per-page choices the pager offers, largest last. 25 is the default: about a
// screenful of rows once the console's own chrome is counted. 100 is the ceiling because
// past it the table is a scroll again, which is what the pager exists to avoid.
const PAGE_SIZES = [25, 50, 100];
const DEFAULT_PAGE_SIZE = PAGE_SIZES[0];

// The view tabs. Every thread sits in exactly one of the three groups below, so the tab counts
// add up to All. A thread with work still running is In progress whatever its newest run did; a
// thread whose newest run failed, and that is not running, needs attention; the rest are Done.
type View = "all" | "progress" | "attention" | "done";
const VIEWS: { value: View; label: string }[] = [
  { value: "all", label: "All" },
  { value: "progress", label: "In progress" },
  { value: "attention", label: "Needs attention" },
  { value: "done", label: "Done" },
];

// Where a thread sits in the view tabs. Any run still queued or running makes it In progress,
// so the tab answers "what is being worked on" even when an older run failed.
function viewOf(item: Item): Exclude<View, "all"> {
  const runs = threadRuns(item);
  if (runs.some(isActiveRun)) return "progress";
  const newest = newestRun(runs);
  if (newest !== null && isFailedRun(newest)) return "attention";
  return "done";
}

// A stage is one role's place in the hand-off, read off the newest run that role has on the
// thread. "pending" is a role that has not run yet.
type StageState = "done" | "active" | "queued" | "failed" | "pending";

const STAGE_WORD: Record<StageState, string> = {
  done: "done",
  active: "in progress",
  queued: "waiting",
  failed: "failed",
  pending: "not started",
};

// The segment colours are the console's state tokens, so a stage reads the same as the
// status pills beside it.
const STAGE_TOKEN: Record<StageState, string> = {
  done: "var(--ok)",
  active: "var(--signal)",
  queued: "var(--warn)",
  failed: "var(--err)",
  pending: "var(--rule-soft)",
};

function stageStateOf(run: Run | null): StageState {
  if (run === null) return "pending";
  switch (run.status) {
    case "success":
      return "done";
    case "running":
      return "active";
    case "queued":
      return "queued";
    case "failure":
    case "timeout":
      return "failed";
    default:
      return "pending";
  }
}

// handOffStates reads each stage's state off the thread's runs, in hand-off order.
function handOffStates(item: Item): StageState[] {
  const runs = threadRuns(item);
  return STAGES.map((stage) =>
    stageStateOf(newestRun(runs.filter((run) => run.role === stage.role))),
  );
}

// handOffLabel names the furthest stage the thread has reached, and what it is doing there:
// "Review · in progress" once a review is running, "Develop · waiting" while a developer is queued.
function handOffLabel(states: StageState[]): string {
  for (let i = STAGES.length - 1; i >= 0; i--) {
    if (states[i] !== "pending") return `${STAGES[i].name} · ${STAGE_WORD[states[i]]}`;
  }
  return "Not started";
}

// The columns are built per render because the first one opens the detail panel: the item's
// title is a button that sets WorkPage's selection, while a small anchor next to it keeps the
// old meaning of going to Forgejo in a new tab. The two link semantics stay visibly distinct —
// in-app panel (button) vs external page (anchor). The row itself cannot take a click through
// the console's table primitive, so the title button is the way in.
function columns({ onOpen, highlightKey }: { onOpen: (row: Row) => void; highlightKey: string | null }) {
  return [
    {
      key: "thread",
      header: "Thread",
      width: "34%",
      cell: (row: Row) => {
        const { item } = row;
        const number = item.subject.number;
        // The selected thread gets a tint and an accent bar. The bundle ships no stylesheet, so
        // the highlight is drawn on the cell's own block rather than on the row.
        const selected = itemKey(item) === highlightKey;
        return (
          <span
            className="forgejo-item"
            style={{
              display: "flex",
              alignItems: "flex-start",
              gap: 10,
              minWidth: 0,
              margin: "-6px -8px",
              padding: "6px 8px",
              borderRadius: "var(--radius-sm)",
              background: selected ? "var(--signal-haze)" : undefined,
              boxShadow: selected ? "inset 3px 0 0 var(--signal)" : undefined,
            }}
          >
            <KindIcon isPr={item.subject.kind === "pulls"} />
            <span style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
              <span style={{ display: "flex", alignItems: "center", gap: 6, minWidth: 0 }}>
                <button
                  type="button"
                  className="forgejo-item-open"
                  onClick={() => onOpen(row)}
                  style={{ ...OPEN_BUTTON, minWidth: 0 }}
                >
                  {item.subject.title || `#${number}`}
                </button>
                <a
                  href={item.url}
                  target="_blank"
                  rel="noreferrer"
                  className="forgejo-item-link"
                  aria-label={`Open #${number} on Forgejo`}
                  style={{ flex: "0 0 auto" }}
                >
                  ↗
                </a>
              </span>
              <span style={{ fontSize: 12, color: "var(--ink-3)" }}>
                {item.subject.repo}{" "}
                <span className="forgejo-mono" style={MONO}>
                  #{number}
                </span>
              </span>
            </span>
          </span>
        );
      },
    },
    {
      key: "handoff",
      header: "Hand-off",
      width: 190,
      cell: (row: Row) => <HandOff states={handOffStates(row.item)} />,
    },
    {
      key: "prs",
      header: "Linked PRs",
      width: 120,
      cell: (row: Row) =>
        row.item.prs.length === 0 ? (
          DASH
        ) : (
          row.item.prs.map((pr, index) => (
            <span key={`${pr.subject.repo}#${pr.subject.number}`} className="forgejo-nested-pr">
              {index > 0 ? ", " : ""}
              <a
                href={pr.url}
                target="_blank"
                rel="noreferrer"
                className="forgejo-pr-link"
                style={{ ...MONO, color: "var(--signal)" }}
              >
                #{pr.subject.number}
              </a>
            </span>
          ))
        ),
    },
    {
      key: "status",
      header: "Status",
      width: 130,
      cell: (row: Row) => {
        const newest = threadStatusRun(row.item);
        return newest === null ? (
          <Tag variant="stale">No runs</Tag>
        ) : (
          <Tag variant={statusTone(newest.status)}>{statusLabel(newest.status)}</Tag>
        );
      },
    },
    {
      key: "runs",
      header: "Runs",
      align: "right" as const,
      width: 64,
      // Every run on the thread, counted once: the item's own plus each nested pull request's, so
      // the reviewer that ran on the pull request shows up in the row that links it.
      cell: (row: Row) => String(threadTotals(row.item).runs),
    },
    {
      key: "time",
      header: "Agent time",
      align: "right" as const,
      width: 96,
      cell: (row: Row) => formatDuration(threadTotals(row.item).durationMs),
    },
    {
      key: "cost",
      header: "Cost",
      align: "right" as const,
      width: 84,
      // The thread's cost: one run the hub could not price anywhere on the thread makes the row
      // unknown rather than partly priced, and a thread nobody ran has nothing to price at all.
      cell: (row: Row) => {
        const totals = threadTotals(row.item);
        return formatCost(totals.costUsd, totals.costKnown);
      },
    },
    {
      key: "updated",
      header: "Updated",
      width: 96,
      cell: (row: Row) => (
        <span
          style={{ color: "var(--ink-2)" }}
          title={new Date(row.item.lastActivity).toLocaleString()}
        >
          {formatRelative(row.item.lastActivity, new Date())}
        </span>
      ),
    },
  ];
}

// HandOff is the three-stage bar and its label. The bar is one image for assistive tech with the
// stage names and states spelled out, and the label says where the thread is in words.
function HandOff({ states }: { states: StageState[] }) {
  const description = STAGES.map((stage, i) => `${stage.name}: ${STAGE_WORD[states[i]]}`).join(", ");
  return (
    <span style={{ display: "flex", flexDirection: "column", gap: 6 }}>
      <span role="img" aria-label={description} title={description} style={{ display: "flex", gap: 3 }}>
        {STAGES.map((stage, i) => (
          <span
            key={stage.role}
            aria-hidden="true"
            style={{
              display: "inline-block",
              width: 26,
              height: 6,
              borderRadius: 3,
              background: STAGE_TOKEN[states[i]],
            }}
          />
        ))}
      </span>
      <span style={{ fontSize: 12, color: "var(--ink-2)" }}>{handOffLabel(states)}</span>
    </span>
  );
}

// KindIcon marks a thread as an issue or a pull request, with the name spelled out for screen
// readers. The icons are drawn in theme tokens so they move with the console's theme.
function KindIcon({ isPr }: { isPr: boolean }) {
  if (isPr) {
    return (
      <svg
        width="16"
        height="16"
        viewBox="0 0 24 24"
        fill="none"
        stroke="var(--signal)"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
        role="img"
        aria-label="Pull request"
        style={{ flex: "0 0 auto", marginTop: 1 }}
      >
        <circle cx="18" cy="18" r="3" />
        <circle cx="6" cy="6" r="3" />
        <path d="M13 6h3a2 2 0 0 1 2 2v7" />
        <path d="M6 9v12" />
      </svg>
    );
  }
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="var(--ok)"
      strokeWidth="2"
      role="img"
      aria-label="Issue"
      style={{ flex: "0 0 auto", marginTop: 1 }}
    >
      <circle cx="12" cy="12" r="9" />
      <circle cx="12" cy="12" r="1.5" fill="var(--ok)" />
    </svg>
  );
}

// The key a thread is known by: its repository and number. Forgejo shares one number space
// across issues and pull requests within a repository, so the pair is unique.
function itemKey(item: Item): string {
  return `${item.subject.repo}#${item.subject.number}`;
}

export function WorkPage() {
  const { api } = usePluginContext();
  const [period, setPeriod] = useState(DEFAULT_PERIOD);
  const [repos, setRepos] = useState<Repo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // The thread whose detail panel is open, by key, or null for the table alone. The key rather
  // than the item: the answer is refetched when the period changes, and a panel that held the old
  // object would show numbers from a window the page no longer shows.
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [view, setView] = useState<View>("all");
  const [query, setQuery] = useState("");
  const [repo, setRepo] = useState("");
  const [role, setRole] = useState("");
  // Which page of the table is showing (1-based) and how many rows a page holds. Component
  // state like the selection is, for the same reason: the console router only knows the static
  // manifest pages, so a page number cannot travel in a URL either. It survives opening and
  // closing the detail panel, since the panel renders inside this same instance.
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);

  useEffect(() => {
    // A period picked while an earlier answer is still in flight must not be overwritten when
    // that older answer lands, so each run of this effect only applies its answer while current.
    let current = true;
    setLoading(true);
    setError(null);
    api
      .get("/work", { query: { since: period } })
      .then((response) => {
        if (current) setRepos((response as WorkResponse).repos);
      })
      .catch((err) => {
        if (current) setError(err.message);
      })
      .finally(() => {
        if (current) setLoading(false);
      });
    return () => {
      current = false;
    };
  }, [api, period]);

  // Every filter returns to the first page: a page number chosen for one set of rows says
  // nothing about the page a different filter produces.
  function changePeriod(next: string) {
    setPeriod(next);
    setPage(1);
  }
  function changeView(next: View) {
    setView(next);
    setPage(1);
  }
  function changeQuery(next: string) {
    setQuery(next);
    setPage(1);
  }
  function changeRepo(next: string) {
    setRepo(next);
    setPage(1);
  }
  function changeRole(next: string) {
    setRole(next);
    setPage(1);
  }

  // Nothing to export until there is an answer to export from.
  if (loading) {
    return (
      <div className="forgejo-work-page" style={PAGE_STYLE}>
        <Header period={period} onPeriod={changePeriod} exportable={false} onExport={() => {}} />
        <p className="forgejo-status">Loading...</p>
      </div>
    );
  }

  if (error) {
    return (
      <div className="forgejo-work-page" style={PAGE_STYLE}>
        <Header period={period} onPeriod={changePeriod} exportable={false} onExport={() => {}} />
        <p className="forgejo-error">Could not reach work API</p>
      </div>
    );
  }

  const rows = flatten(repos);
  const selected = findThread(repos, selectedKey);
  // The table row the open thread sits in: its own, or the issue its pull request is nested under.
  const highlightRow = rows.find(
    (row) =>
      selectedKey !== null &&
      (itemKey(row.item) === selectedKey ||
        row.item.prs.some((pr) => itemKey(pr) === selectedKey)),
  );
  const highlightKey = highlightRow ? itemKey(highlightRow.item) : null;

  // The filter options come from the data, so a choice is always one that matches something. A
  // repository or role that the new period no longer holds is dropped rather than left selected
  // over an empty table.
  const repoOptions = [...new Set(rows.map((row) => row.item.subject.repo))].sort();
  const roleOptions = sortRoles(
    new Set(rows.flatMap((row) => threadRuns(row.item).map((run) => run.role))),
  );
  const activeRepo = repoOptions.includes(repo) ? repo : "";
  const activeRole = roleOptions.includes(role) ? role : "";
  const search = query.trim().toLowerCase().replace(/^#/, "");

  const visible = rows.filter((row) => {
    if (view !== "all" && viewOf(row.item) !== view) return false;
    if (activeRepo !== "" && row.item.subject.repo !== activeRepo) return false;
    if (activeRole !== "" && !threadRuns(row.item).some((run) => run.role === activeRole)) return false;
    if (search !== "") {
      const title = (row.item.subject.title ?? "").toLowerCase();
      if (!title.includes(search) && !String(row.item.subject.number).includes(search)) return false;
    }
    return true;
  });

  const counts: Record<View, number> = { all: rows.length, progress: 0, attention: 0, done: 0 };
  for (const row of rows) counts[viewOf(row.item)] += 1;

  // The page the table shows. `page` is clamped at render rather than in an effect because the
  // row set can shrink underneath it — the retention window ages items out — and a table that
  // renders nothing while the line above it counts forty items says something untrue. The same
  // clamp covers a rows-per-page change that leaves the current page past the end.
  const total = visible.length;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const current = Math.min(page, totalPages);
  const from = (current - 1) * pageSize;
  const pageRows = visible.slice(from, from + pageSize);

  // The summary counts every row of the period, whatever the filters show — the strip answers
  // "how much work is there", the filters answer "which of it am I looking at".
  const summary = summarise(rows, counts.progress);

  return (
    <div className="forgejo-work-page" style={PAGE_STYLE}>
      <Header
        period={period}
        onPeriod={changePeriod}
        exportable={visible.length > 0}
        onExport={() => exportCsv(visible, period)}
      />
      {rows.length > 0 && (
        <Panel className="forgejo-work-summary">
          <div style={{ display: "flex", flexWrap: "wrap" }}>
            {summary.map((stat) => (
              <div
                key={stat.label}
                style={{
                  flex: "1 1 160px",
                  padding: "14px 20px",
                  borderRight: "1px solid var(--rule-soft)",
                  display: "flex",
                  flexDirection: "column",
                  gap: 2,
                  minWidth: 0,
                }}
              >
                <span style={{ fontSize: 12, color: "var(--ink-2)", fontWeight: 500 }}>{stat.label}</span>
                <span
                  className="forgejo-work-stat-value"
                  style={{ fontSize: 20, fontWeight: 600, fontVariantNumeric: "tabular-nums" }}
                >
                  {stat.value}
                </span>
              </div>
            ))}
          </div>
        </Panel>
      )}

      {/* The table and the detail panel sit side by side and wrap under each other on a narrow
          page. The table keeps the larger share, and the panel never grows past its maximum. */}
      <div style={{ display: "flex", flexWrap: "wrap", gap: 16, alignItems: "flex-start" }}>
        <div style={{ flex: "999 1 640px", minWidth: 0 }}>
          <Panel className="forgejo-work-main">
            <Tabs
              aria-label="Views"
              value={view}
              onChange={(next: View) => changeView(next)}
              tabs={VIEWS.map((v) => ({
                value: v.value,
                label: (
                  <>
                    {v.label}
                    <span className="forgejo-count" style={COUNT_BADGE}>
                      {counts[v.value]}
                    </span>
                  </>
                ),
              }))}
            />

            <div
              style={{
                display: "flex",
                flexWrap: "wrap",
                gap: 10,
                alignItems: "flex-end",
                padding: "12px 16px",
              }}
            >
              <div style={{ flex: "1 1 240px", minWidth: 0 }}>
                <Field label="Search" htmlFor="forgejo-work-search">
                  <Input
                    id="forgejo-work-search"
                    type="search"
                    placeholder="Search by title or number"
                    value={query}
                    onChange={(e: { target: { value: string } }) => changeQuery(e.target.value)}
                    className="forgejo-work-search"
                  />
                </Field>
              </div>
              <Field label="Repository" htmlFor="forgejo-work-repo">
                <Select
                  id="forgejo-work-repo"
                  size="sm"
                  value={activeRepo}
                  onChange={changeRepo}
                  emptyLabel="All repositories"
                  options={repoOptions.map((name) => ({ value: name, label: name }))}
                />
              </Field>
              <Field label="Role" htmlFor="forgejo-work-role">
                <Select
                  id="forgejo-work-role"
                  size="sm"
                  value={activeRole}
                  onChange={changeRole}
                  emptyLabel="Any role"
                  options={roleOptions.map((name) => ({ value: name, label: roleName(name) }))}
                />
              </Field>
            </div>

            {/* The table gets one page, the summary keeps counting every row: the strip above
                answers "how much work is there", the footer below answers "where in it am I".
                They are meant to disagree about the count, which is what the footer's "of Z"
                disambiguates.

                The row-number column is the host RegisterTable's own index of the rows it is
                handed, and it has no offset prop, so it restarts at 01 on every page. Accepted:
                the numbers are decorative within a page, and the footer's "X–Y of Z" carries the
                real position. Do not "fix" it by dropping `withRowNumbers` — that takes away a
                working column to correct a cosmetic one. */}
            <div style={{ overflowX: "auto" }}>
              <div style={{ minWidth: 880 }}>
                <RegisterTable
                  rows={pageRows}
                  columns={columns({
                    onOpen: (row) => setSelectedKey(itemKey(row.item)),
                    highlightKey,
                  })}
                  rowKey={(row: Row) => itemKey(row.item)}
                  rowClassName={(row: Row) =>
                    itemKey(row.item) === highlightKey ? "forgejo-work-row-selected" : undefined
                  }
                  emptyLabel={
                    rows.length === 0
                      ? "No issues or pull requests worked on yet"
                      : "No threads match this view."
                  }
                />
              </div>
            </div>

            {total > pageSize && (
              <Pager
                first={from + 1}
                last={Math.min(from + pageSize, total)}
                total={total}
                page={current}
                totalPages={totalPages}
                pageSize={pageSize}
                onPrev={() => setPage(current - 1)}
                onNext={() => setPage(current + 1)}
                onPageSize={setPageSize}
              />
            )}
          </Panel>
        </div>

        {selected !== null && (
          <aside aria-label="Thread details" style={{ flex: "1 1 360px", maxWidth: 460, minWidth: 0 }}>
            <WorkItemDetail
              item={selected.item}
              repoUrl={selected.repoUrl}
              onClose={() => setSelectedKey(null)}
              onOpenItem={(item) => setSelectedKey(itemKey(item))}
            />
          </aside>
        )}
      </div>
    </div>
  );
}

const PAGE_STYLE = { display: "flex", flexDirection: "column" as const, gap: 16 };

// The header row: what the page is for, and the two actions on the data it shows. The period
// is the one control that changes what the server returns; the export only reads what is shown.
function Header({
  period,
  onPeriod,
  exportable,
  onExport,
}: {
  period: string;
  onPeriod: (next: string) => void;
  exportable: boolean;
  onExport: () => void;
}) {
  return (
    <div
      style={{
        display: "flex",
        flexWrap: "wrap",
        alignItems: "flex-end",
        justifyContent: "space-between",
        gap: 12,
      }}
    >
      <p style={{ margin: 0, fontSize: 14, color: "var(--ink-2)" }}>
        Every thread the agents touched, its hand-off stage, and what it cost.
      </p>
      <div style={{ display: "flex", flexWrap: "wrap", alignItems: "flex-end", gap: 10 }}>
        <Field label="Period" htmlFor="forgejo-work-period">
          <Select
            id="forgejo-work-period"
            size="sm"
            value={period}
            onChange={onPeriod}
            options={PERIODS}
          />
        </Field>
        <Button onClick={onExport} disabled={!exportable}>
          Export CSV
        </Button>
      </div>
    </div>
  );
}

// The row under the table: where in the list you are, and how many rows a page holds. Built
// from the primitives the SDK exposes rather than the console's own Pager, which the SDK does
// not register — a plugin cannot reach it. Layout is inline because the bundle ships no
// stylesheet of its own; the classes are there for the console to hang theme on.
function Pager(props: {
  first: number;
  last: number;
  total: number;
  page: number;
  totalPages: number;
  pageSize: number;
  onPrev: () => void;
  onNext: () => void;
  onPageSize: (next: number) => void;
}) {
  const { first, last, total, page, totalPages, pageSize, onPrev, onNext, onPageSize } = props;
  return (
    <div
      className="forgejo-pager"
      style={{
        display: "flex",
        flexWrap: "wrap",
        justifyContent: "space-between",
        alignItems: "center",
        gap: 10,
        padding: "12px 16px",
        borderTop: "1px solid var(--rule-soft)",
        fontSize: 13,
        color: "var(--ink-2)",
      }}
    >
      <span className="forgejo-pager-info">
        Showing {first}–{last} of {total} threads
      </span>
      <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
        <span>Rows per page</span>
        <Select
          aria-label="Rows per page"
          className="forgejo-pager-size"
          size="sm"
          value={String(pageSize)}
          onChange={(next: string) => onPageSize(Number(next))}
          options={PAGE_SIZES.map((size) => ({ value: String(size), label: String(size) }))}
        />
        <button
          type="button"
          className="forgejo-pager-prev"
          aria-label="Previous page"
          disabled={page <= 1}
          onClick={onPrev}
          style={{ ...ICON_BUTTON, opacity: page <= 1 ? 0.5 : 1 }}
        >
          <ChevronIcon direction="left" />
        </button>
        <span className="forgejo-pager-position">
          Page {page} of {totalPages}
        </span>
        <button
          type="button"
          className="forgejo-pager-next"
          aria-label="Next page"
          disabled={page >= totalPages}
          onClick={onNext}
          style={{ ...ICON_BUTTON, opacity: page >= totalPages ? 0.5 : 1 }}
        >
          <ChevronIcon direction="right" />
        </button>
      </div>
    </div>
  );
}

function ChevronIcon({ direction }: { direction: "left" | "right" }) {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
      <path d={direction === "left" ? "M15 18l-6-6 6-6" : "M9 18l6-6-6-6"} />
    </svg>
  );
}

const ICON_BUTTON = {
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  width: 32,
  height: 32,
  padding: 0,
  border: "1px solid var(--rule-soft)",
  borderRadius: "var(--radius-sm)",
  background: "var(--paper)",
  color: "var(--ink-2)",
  cursor: "pointer",
};

const COUNT_BADGE = {
  display: "inline-block",
  minWidth: 18,
  padding: "0 6px",
  marginLeft: 6,
  borderRadius: 9,
  background: "var(--paper-2)",
  color: "var(--ink-2)",
  fontSize: 11,
  lineHeight: "18px",
  textAlign: "center" as const,
};

// The items of every repo, newest activity first. The response sorts within a repo, but a
// quiet repo may hold an item older than a busy one's, and the admin scanning for what to
// look at next reads down this column.
function flatten(repos: Repo[]): Row[] {
  const rows: Row[] = [];
  for (const repo of repos) {
    for (const item of repo.items) {
      rows.push({ item, repoUrl: repo.url });
    }
  }
  return rows.sort(
    (a, b) =>
      new Date(b.item.lastActivity).getTime() - new Date(a.item.lastActivity).getTime(),
  );
}

// findThread returns the thread a key names, top-level or nested, with the URL of the repo it
// lives in. A nested pull request is in the same repo as its issue (the /work response nests
// `prs` inside a Repo's Item), so it takes the repo's URL; a cross-repo PR can never nest here
// under that structure.
function findThread(repos: Repo[], key: string | null): Row | null {
  if (key === null) return null;
  for (const repo of repos) {
    for (const item of repo.items) {
      if (itemKey(item) === key) return { item, repoUrl: repo.url };
      for (const pr of item.prs) {
        if (itemKey(pr) === key) return { item: pr, repoUrl: repo.url };
      }
    }
  }
  return null;
}

// summarise is the strip above the table: threads, repositories, what is in progress, and the
// agent time and spend of all the work. It adds each row's threadTotals, so the strip and the rows
// under it are the same money added two ways: every run counted once, and only a run the hub could
// not price making the spend unknown.
function summarise(rows: Row[], inProgress: number) {
  let durationMs = 0;
  let costUsd = 0;
  let costKnown = true;
  for (const row of rows) {
    const totals = threadTotals(row.item);
    durationMs += totals.durationMs;
    // A thread nobody ran has no cost to know; only a run the hub could not price makes the sum
    // unknown.
    if (totals.runs === 0) continue;
    if (totals.costUsd !== null) costUsd += totals.costUsd;
    else costKnown = false;
  }
  return [
    { label: "Threads", value: String(rows.length) },
    { label: "Repositories", value: String(countRepos(rows)) },
    { label: "In progress", value: String(inProgress) },
    { label: "Agent time", value: formatDuration(durationMs) },
    { label: "Spend", value: formatCost(costUsd, costKnown) },
  ];
}

// The repositories the table shows a row from, counted off the rows themselves so the strip
// cannot disagree with the rows below it.
function countRepos(rows: Row[]): number {
  return new Set(rows.map((row) => row.item.subject.repo)).size;
}

function sortRoles(roles: Set<string>): string[] {
  return [...roles].sort((a, b) => roleRank(a) - roleRank(b) || a.localeCompare(b));
}

function roleRank(role: string): number {
  const index = ROLE_ORDER.indexOf(role);
  return index === -1 ? ROLE_ORDER.length : index;
}

function roleName(role: string): string {
  return role.charAt(0).toUpperCase() + role.slice(1);
}

// exportCsv downloads the rows the table is filtered down to, all of them rather than the page
// on screen. It is built in the browser: the rows are already here, and a download the server
// would have to answer again could disagree with what the admin is looking at.
function exportCsv(rows: Row[], period: string) {
  const blob = new Blob([toCsv(rows)], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = `forgejo-work-${period}.csv`;
  document.body.appendChild(link);
  link.click();
  link.remove();
  // Revoked once the click has handed the download to the browser, not before.
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

const CSV_HEADER = [
  "kind",
  "number",
  "title",
  "repo",
  "status",
  "runs",
  "agent time",
  "cost",
  "last activity",
];

// toCsv writes one line per thread, in the order the table shows them, with the same values the
// row shows — the thread's totals, nested pull requests included, each run counted once. Agent time
// keeps the table's reading ("3m 05s") so the file and the screen agree; cost is a plain number of
// dollars, empty when the hub could not price the thread or nothing ran on it, so a spreadsheet can
// add it up.
function toCsv(rows: Row[]): string {
  const lines = [CSV_HEADER.join(",")];
  for (const { item } of rows) {
    const newest = threadStatusRun(item);
    const totals = threadTotals(item);
    lines.push(
      [
        item.subject.kind === "pulls" ? "PR" : "Issue",
        String(item.subject.number),
        item.subject.title ?? "",
        item.subject.repo,
        newest === null ? "" : statusLabel(newest.status),
        String(totals.runs),
        formatDuration(totals.durationMs),
        totals.costUsd !== null ? totals.costUsd.toFixed(2) : "",
        item.lastActivity,
      ]
        .map(csvCell)
        .join(","),
    );
  }
  return lines.join("\r\n");
}

// csvCell quotes a field that holds a separator, a quote or a line break, doubling any quote
// inside it, as RFC 4180 asks.
function csvCell(value: string): string {
  return /[",\r\n]/.test(value) ? `"${value.replace(/"/g, '""')}"` : value;
}
