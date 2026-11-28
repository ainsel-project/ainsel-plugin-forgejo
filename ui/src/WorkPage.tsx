import { Field, Input, Panel, RegisterTable, Select, Tabs, usePluginContext } from "@ainsel/plugin-sdk";
import { useEffect, useState } from "react";
import type { Repo, WorkResponse } from "./api";
import { threadRuns } from "./format";
import { WorkItemDetail } from "./WorkItemDetail";
import { columns } from "./work/columns";
import { Summary } from "./work/Summary";
import { exportCsv } from "./work/csv";
import { Header } from "./work/Header";
import { DEFAULT_PAGE_SIZE, DEFAULT_PERIOD, VIEWS, itemKey, viewOf } from "./work/model";
import type { Row, View } from "./work/model";
import { Pager } from "./work/Pager";
import { findThread, flatten, roleName, sortRoles, summarise } from "./work/rows";
import { COUNT_BADGE, PAGE_STYLE } from "./work/styles";

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
      (itemKey(row.item) === selectedKey || row.item.prs.some((pr) => itemKey(pr) === selectedKey)),
  );
  const highlightKey = highlightRow ? itemKey(highlightRow.item) : null;

  // The filter options come from the data, so a choice is always one that matches something. A
  // repository or role that the new period no longer holds is dropped rather than left selected
  // over an empty table.
  const repoOptions = [...new Set(rows.map((row) => row.item.subject.repo))].sort();
  const roleOptions = sortRoles(new Set(rows.flatMap((row) => threadRuns(row.item).map((run) => run.role))));
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
      <Summary rows={rows} summary={summary} />

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
                    rows.length === 0 ? "No issues or pull requests worked on yet" : "No threads match this view."
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
