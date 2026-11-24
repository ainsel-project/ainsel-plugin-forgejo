// The shared field and content layouts. The bundle ships no stylesheet, so the rules live here
// as one scoped block next to the components that emit them — the same shape the Settings page's
// scoped `<style>` block uses. The classes are plugin-neutral (`ainsel-*`, no `forgejo` in any
// exported name or class) so this file can move into the plugin SDK verbatim and the GitHub,
// memory and template plugins adopt it by bumping.
//
// The problem this replaces was an auto-fit `minmax(Npx, 1fr)` grid written out at five
// sites: the column count then followed the viewport and which optional fields happened to
// render, so a row's labels, controls and hints shared no baseline and the whole grid reflowed
// when an optional field appeared or disappeared. Columns now follow the grid's own container
// width through a fixed mapping — a hole in the last row when a conditional field is absent is
// the intended trade for a row that never moves.
import { useCallback, useEffect, useRef, useState } from "react";
import type { CSSProperties, ReactNode } from "react";

// Fields → columns, for the wizard's field grids. 0 means "width unknown" (a hidden panel, an
// unmounted tab, jsdom): callers keep their last known count rather than collapsing to one
// column. Edges are inclusive at the lower bound: 639 → 1, 640 → 2, 1079 → 2, 1080 → 3.
export function columnsForWidth(width: number): number {
  if (width <= 0) return 0;
  if (width < 640) return 1;
  if (width < 1080) return 2;
  return 3;
}

// Content tiles (KPI panels, detail cards, the compare diff) → columns: as many tracks of at
// least `minTrackWidth` as fit, from 1 up to `maxColumns`. 0 again means "width unknown".
export function cardColumnsForWidth(
  width: number,
  minTrackWidth: number,
  maxColumns: number,
): number {
  if (width <= 0) return 0;
  return Math.max(1, Math.min(maxColumns, Math.floor(width / minTrackWidth)));
}

// One block for both grids. Children get `min-width: 0` because a fixed track must not be
// pushed past the container by long mono content (runtime image refs, `<pre>` diffs). The
// control height is scoped to `input`/`select` inside a field grid — a blanket height would
// stretch the multiline `textarea` rows the settings screens use. Following the
// `.forgejo-settings-mono` precedent, this is a `!important`-free, theme-neutral rule: it
// moves geometry, never colour.
const LAYOUT_CSS = `
.ainsel-field-grid, .ainsel-card-grid { min-width: 0; }
.ainsel-field-grid > *, .ainsel-card-grid > * { min-width: 0; }
.ainsel-field-grid input, .ainsel-field-grid select { min-height: 2.25rem; box-sizing: border-box; }
`;

// Measures the grid's own content box (initial measurement + every resize) and feeds its width
// through `columnsOf`. A width the mapping answers 0 for — hidden panel, jsdom's layout-less
// box — keeps the last known column count. `columnsOf` must be stable across renders.
function useColumnCount(columnsOf: (width: number) => number) {
  const ref = useRef<HTMLDivElement | null>(null);
  const [columns, setColumns] = useState(1);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const apply = (width: number) => {
      const n = columnsOf(width);
      if (n > 0) setColumns((prev) => (prev === n ? prev : n));
    };
    apply(el.getBoundingClientRect().width);
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver((entries) => {
      for (const entry of entries) apply(entry.contentRect.width);
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, [columnsOf]);
  return [ref, columns] as const;
}

function gridStyle(columns: number, gap: string | number, extra?: CSSProperties): CSSProperties {
  return {
    display: "grid",
    gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))`,
    alignItems: "start",
    gap,
    ...extra,
  };
}

function classes(base: string, className?: string): string {
  return className ? `${base} ${className}` : base;
}

// The grid every set of wizard fields sits in. Its children are the `Field`s themselves —
// wrapping a `Field` in a layout `div` would make the wrapper the grid item and undo the row
// alignment this exists for, so this component renders no wrapper around them. The `<style>`
// is a sibling, not a child: it must never become a phantom grid cell.
export function FieldGrid({ className, children }: { className?: string; children: ReactNode }) {
  const [ref, columns] = useColumnCount(columnsForWidth);
  return (
    <>
      <style>{LAYOUT_CSS}</style>
      <div ref={ref} className={classes("ainsel-field-grid", className)} style={gridStyle(columns, "1rem 1.25rem")}>
        {children}
      </div>
    </>
  );
}

// The grid for read-only content tiles — the Live page's KPI panels, the detail panel's
// summary cards, the Settings page's compare diff. They hold no `Field`s, so they get the
// same fixed-column rule without the field-grid control-height block applying to them.
export function CardGrid({
  minTrackWidth = 170,
  maxColumns = 4,
  gap = 8,
  className,
  style,
  children,
}: {
  minTrackWidth?: number;
  maxColumns?: number;
  gap?: string | number;
  className?: string;
  style?: CSSProperties;
  children: ReactNode;
}) {
  const columnsOf = useCallback(
    (width: number) => cardColumnsForWidth(width, minTrackWidth, maxColumns),
    [minTrackWidth, maxColumns],
  );
  const [ref, columns] = useColumnCount(columnsOf);
  return (
    <>
      <style>{LAYOUT_CSS}</style>
      <div
        ref={ref}
        className={classes("ainsel-card-grid", className)}
        style={gridStyle(columns, gap, style)}
      >
        {children}
      </div>
    </>
  );
}
