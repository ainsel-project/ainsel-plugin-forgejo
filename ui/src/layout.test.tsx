/** @vitest-environment jsdom */
import { readdirSync, readFileSync, existsSync } from "node:fs";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render } from "@testing-library/react";
import { FieldGrid, CardGrid, columnsForWidth, cardColumnsForWidth } from "./layout";

// jsdom performs no layout: `offsetTop` and `getBoundingClientRect().height` are always 0, so
// geometry assertions would assert nothing. These tests assert what is observable instead:
// the pure width→columns mappings, and the structure the grids emit — declared tracks,
// `align-items: start`, and that the `Field`s stay direct children of the grid.

afterEach(cleanup);

// jsdom has no ResizeObserver; layout.tsx degrades to a single initial measurement without
// one, which in jsdom answers width 0. The fake drives the resize path and is the only way a
// test can hand a grid a container width.
const observed: FakeResizeObserver[] = [];

class FakeResizeObserver {
  callback: ResizeObserverCallback;
  targets: Element[] = [];
  constructor(callback: ResizeObserverCallback) {
    this.callback = callback;
    observed.push(this);
  }
  observe(target: Element) {
    this.targets.push(target);
  }
  unobserve() {}
  disconnect() {
    this.targets = [];
  }
  // Report `width` for every observed target, as the browser does on resize.
  fire(width: number) {
    act(() => {
      this.callback(
        this.targets.map((target) => ({
          target,
          contentRect: { width } as DOMRectReadOnly,
          borderBoxSize: [],
          contentBoxSize: [],
          devicePixelContentBoxSize: [],
        })) as ResizeObserverEntry[],
        this as unknown as ResizeObserver,
      );
    });
  }
}

beforeEach(() => {
  observed.length = 0;
  vi.stubGlobal("ResizeObserver", FakeResizeObserver);
});

afterEach(() => {
  vi.unstubAllGlobals();
});


function gridOf(container: HTMLElement, selector: string): HTMLElement {
  const grid = container.querySelector<HTMLElement>(selector);
  expect(grid).not.toBeNull();
  return grid as HTMLElement;
}

function tracksOf(grid: HTMLElement): string {
  // Read the attribute rather than `.style`: it is what the browser and the bundle both see.
  return grid.getAttribute("style") ?? "";
}

describe("columnsForWidth", () => {
  it("maps container widths to a fixed column count at the breakpoints and their edges", () => {
    expect(columnsForWidth(1)).toBe(1);
    expect(columnsForWidth(639)).toBe(1);
    expect(columnsForWidth(640)).toBe(2);
    expect(columnsForWidth(1079)).toBe(2);
    expect(columnsForWidth(1080)).toBe(3);
    expect(columnsForWidth(2500)).toBe(3);
  });

  it("answers 0 — width unknown — for a hidden or unmeasured container", () => {
    expect(columnsForWidth(0)).toBe(0);
    expect(columnsForWidth(-10)).toBe(0);
  });
});

describe("cardColumnsForWidth", () => {
  it("fits whole tracks and clamps between 1 and maxColumns", () => {
    expect(cardColumnsForWidth(169, 170, 6)).toBe(1);
    expect(cardColumnsForWidth(340, 170, 6)).toBe(2);
    expect(cardColumnsForWidth(5000, 170, 6)).toBe(6);
  });

  it("answers 0 — width unknown — for a hidden or unmeasured container", () => {
    expect(cardColumnsForWidth(0, 170, 6)).toBe(0);
  });
});

function Fields() {
  return (
    <>
      <div data-testid="field">a</div>
      <div data-testid="field">b</div>
      <div data-testid="field">c</div>
    </>
  );
}

describe("FieldGrid", () => {
  it("keeps the Fields as direct children of the grid — no wrapper div", () => {
    const { container } = render(
      <FieldGrid>
        <Fields />
      </FieldGrid>,
    );
    const grid = gridOf(container, ".ainsel-field-grid");
    const fields = grid.querySelectorAll('[data-testid="field"]');
    expect(fields).toHaveLength(3);
    for (const field of fields) expect(field.parentElement).toBe(grid);
  });

  it("declares N equal tracks for the container width and never auto-fit", () => {
    const { container } = render(
      <FieldGrid>
        <Fields />
      </FieldGrid>,
    );
    const grid = gridOf(container, ".ainsel-field-grid");
    const ro = observed[0];
    expect(ro).toBeDefined();

    ro.fire(500);
    expect(tracksOf(grid)).toContain("repeat(1, minmax(0, 1fr))");
    ro.fire(700);
    expect(tracksOf(grid)).toContain("repeat(2, minmax(0, 1fr))");
    ro.fire(1200);
    expect(tracksOf(grid)).toContain("repeat(3, minmax(0, 1fr))");
    expect(tracksOf(grid)).not.toContain("auto-fit");
  });

  it("aligns rows to the start", () => {
    const { container } = render(
      <FieldGrid>
        <Fields />
      </FieldGrid>,
    );
    expect(tracksOf(gridOf(container, ".ainsel-field-grid"))).toContain("align-items: start");
  });

  it("keeps the last known column count when the width reads 0", () => {
    const { container } = render(
      <FieldGrid>
        <Fields />
      </FieldGrid>,
    );
    const grid = gridOf(container, ".ainsel-field-grid");
    const ro = observed[0];
    ro.fire(1200);
    expect(tracksOf(grid)).toContain("repeat(3, minmax(0, 1fr))");
    ro.fire(0);
    expect(tracksOf(grid)).toContain("repeat(3, minmax(0, 1fr))");
  });
});

describe("CardGrid", () => {
  it("maps width through its own track rule and keeps children direct", () => {
    const { container } = render(
      <CardGrid className="forgejo-detail-cards" minTrackWidth={200} maxColumns={4}>
        <div data-testid="card">1</div>
        <div data-testid="card">2</div>
        <div data-testid="card">3</div>
        <div data-testid="card">4</div>
      </CardGrid>,
    );
    const grid = gridOf(container, ".forgejo-detail-cards");
    expect(grid.className).toContain("ainsel-card-grid"); // the theme class survives, the layout class joins it
    const cards = grid.querySelectorAll('[data-testid="card"]');
    expect(cards).toHaveLength(4);
    for (const card of cards) expect(card.parentElement).toBe(grid);

    const ro = observed[0];
    ro.fire(150);
    expect(tracksOf(grid)).toContain("repeat(1, minmax(0, 1fr))");
    ro.fire(420);
    expect(tracksOf(grid)).toContain("repeat(2, minmax(0, 1fr))");
    ro.fire(900);
    expect(tracksOf(grid)).toContain("repeat(4, minmax(0, 1fr))");
    expect(tracksOf(grid)).toContain("align-items: start");
    expect(tracksOf(grid)).not.toContain("auto-fit");
  });

  it("puts the style block beside the grid, never inside it", () => {
    const { container } = render(
      <CardGrid>
        <div data-testid="card">1</div>
      </CardGrid>,
    );
    const grid = gridOf(container, ".ainsel-card-grid");
    expect(grid.querySelector("style")).toBeNull();
  });
});

// The one assertion that cannot rot, mirroring the bundle test's refusal pattern: no grid under
// ui/src may hand its column count back to `auto-fit`, whose shifting columns this whole change
// retires.
describe("no auto-fit remains", () => {
  // Built, not literal: this file must not flag itself.
  const needle = "repeat(" + "auto-fit";

  // The guard dies silently if the needle ever stops matching anything — a typo in either
  // half makes the walk below pass unconditionally. So prove the needle bites a known-bad
  // grid (assembled the same way, so this file still carries no literal) before trusting it.
  it("the needle matches an auto-fit grid", () => {
    expect("repeat(" + "auto-fit, minmax(220px, 1fr))").toContain(needle);
  });

  it("refuses an auto-fit grid in any source file", () => {
    // vitest runs with ui/ as cwd; fall back to the repo root if the runner started there.
    const src = existsSync(join(process.cwd(), "src"))
      ? join(process.cwd(), "src")
      : join(process.cwd(), "ui/src");
    const offenders: string[] = [];
    const walk = (dir: string) => {
      for (const entry of readdirSync(dir, { withFileTypes: true })) {
        const path = join(dir, entry.name);
        if (entry.isDirectory()) walk(path);
        else if (/\.(ts|tsx|css)$/.test(entry.name)) {
          const text = readFileSync(path, "utf8");
          if (text.includes(needle)) offenders.push(path);
        }
      }
    };
    walk(src);
    expect(offenders).toEqual([]);
  });
});
