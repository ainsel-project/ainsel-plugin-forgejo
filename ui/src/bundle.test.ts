/** @vitest-environment node */
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { build } from "vite";

// The bundle the console loads is ui/dist/index.js. This test builds it with
// the same config `pnpm build` uses, so the two can never drift apart.
// `emptyOutDir` clears dist, so the tracked dist/.gitkeep is restored in
// afterAll exactly as the postbuild script does for `pnpm build`.
const uiRoot = fileURLToPath(new URL("..", import.meta.url));
const distDir = join(uiRoot, "dist");
const bundlePath = join(distDir, "index.js");

beforeAll(async () => {
  // The `vite build` CLI runs with NODE_ENV=production (vite sets it when
  // unset), which is what makes the JSX transform use the production runtime
  // and keep `react/jsx-runtime` a bare external import. Vitest runs with
  // NODE_ENV=test, so set it for the duration of this build — otherwise the
  // test would build a different bundle than `pnpm build` ships.
  const prevNodeEnv = process.env.NODE_ENV;
  process.env.NODE_ENV = "production";
  try {
    await build({ root: uiRoot, logLevel: "silent" });
  } finally {
    if (prevNodeEnv === undefined) delete process.env.NODE_ENV;
    else process.env.NODE_ENV = prevNodeEnv;
  }
}, 120_000);

afterAll(() => {
  writeFileSync(join(distDir, ".gitkeep"), "");
});

function bundle(): string {
  return readFileSync(bundlePath, "utf8");
}

// The specifiers the console's import map answers (global-constraints): the
// bundle names them and never contains them. `react-dom/client` belongs with
// `react-dom` — a two-level path that is not declared external gets its own
// copy of the renderer, which is exactly the single-renderer rule breaking.
const consoleSpecifiers = [
  "react",
  "react-dom",
  "react-dom/client",
  "react/jsx-runtime",
  "@ainsel/plugin-sdk",
];

// The module specifiers of the built bundle: every `from "…"` it carries, plus
// a bare side-effect `import "…"`. What the bundle imports is the observable
// part of what it does not ship.
function importSpecifiers(source: string): string[] {
  const specifiers: string[] = [];

  // Match: ^import ... from "spec"
  // Multiline mode: ^ matches start of line
  for (const match of source.matchAll(/^[\t ]*import\s+[^;]*\s+from\s+["']([^"']+)["']/gm)) {
    specifiers.push(match[1]);
  }

  // Match: ^import "spec" (side-effect import, no from)
  // This pattern only matches if there's a quote immediately after import and whitespace
  for (const match of source.matchAll(/^[\t ]*import\s+["']([^"']+)["']/gm)) {
    specifiers.push(match[1]);
  }

  // Match: ^export ... from "spec"
  for (const match of source.matchAll(/^[\t ]*export\s+[^;]*\s+from\s+["']([^"']+)["']/gm)) {
    specifiers.push(match[1]);
  }

  return [...new Set(specifiers)];
}

// The component names the manifest resolves: every page's `component` and every
// slot's. They are read from the shipped manifest rather than written down a second
// time here, because the two disagreeing is exactly what this test is for.
function manifestComponents(): string[] {
  const manifest = JSON.parse(
    readFileSync(join(uiRoot, "..", "ainsel-plugin.json"), "utf8"),
  ) as {
    ui?: { pages?: { component?: string }[]; slots?: Record<string, string> };
  };
  return [
    ...(manifest.ui?.pages ?? []).map((page) => page.component ?? ""),
    ...Object.values(manifest.ui?.slots ?? {}),
  ].filter((name) => name !== "");
}

// Whether the ES bundle exports `name`: either in a trailing
// `export { ... }` statement (honouring `local as exported` renames) or
// inline on the declaration itself.
function hasExport(source: string, name: string): boolean {
  if (
    new RegExp(`export\\s+(?:async\\s+)?(?:function|const|let|var|class)\\s+${name}\\b`).test(
      source,
    )
  ) {
    return true;
  }
  return [...source.matchAll(/export\s*\{([^}]*)\}/g)].some((statement) =>
    statement[1]
      .split(",")
      .some((specifier) => {
        const parts = specifier.trim().split(/\s+as\s+/);
        return parts[parts.length - 1] === name;
      }),
  );
}

describe("dist/index.js", () => {
  it("is built as a single ES file", () => {
    expect(existsSync(bundlePath)).toBe(true);
  });

  it("keeps react and react/jsx-runtime as bare external imports", () => {
    // The console provides React; shipping a second copy would break the
    // single-renderer rule, so the bundle must only import them, never
    // contain them.
    const source = bundle();
    expect(source).toMatch(/\bimport\b[^;]*from\s*["']react["']/);
    expect(source).toMatch(/\bimport\b[^;]*from\s*["']react\/jsx-runtime["']/);
  });

  it("imports the sdk as a bare specifier", () => {
    // The console provides the SDK, and hands the plugin its own copy — the one
    // whose host primitives are registered and whose context the console fills.
    // A relative or bundled path would be a second SDK with an empty context.
    const source = bundle();
    expect(source).toMatch(/\bimport\b[^;]*from\s*["']@ainsel\/plugin-sdk["']/);
  });

  it("exports every component the manifest names", () => {
    // The console loads this bundle and resolves each page and each slot by the
    // name the manifest carries, so a name the manifest has and the bundle does
    // not export is a page that renders nothing.
    const components = manifestComponents();
    // Four pages and two slots: the count is checked so a manifest whose names
    // went missing cannot make the loop below vacuous.
    expect(components).toHaveLength(6);
    for (const name of components) {
      expect(hasExport(bundle(), name)).toBe(true);
    }
  });

  it("names no console module beyond the five the import map answers", () => {
    // A subpath of one of them (`react-dom/server`, `@ainsel/plugin-sdk/…`) is a
    // module the console's import map has no entry for, so it would load a second
    // copy of the renderer instead of the shared one.
    for (const specifier of importSpecifiers(bundle())) {
      expect(consoleSpecifiers).toContain(specifier);
    }
  });

  it("contains no bundled copy of any of the five", () => {
    // Markers of the modules' own source: production-bundle filenames, React's
    // private internals objects, the renderer entry the console calls, and the
    // two messages the SDK throws when it is used without the console — the
    // strings a bundled SDK would carry with it.
    const source = bundle();
    expect(source).not.toContain("react.production");
    expect(source).not.toContain("react-dom.production");
    expect(source).not.toContain("__CLIENT_INTERNALS_DO_NOT_USE");
    expect(source).not.toContain("__SECRET_INTERNALS");
    expect(source).not.toContain("createRoot");
    expect(source).not.toContain("must be used inside a plugin page or slot");
    expect(source).not.toContain("host primitives not registered");
  });

  it("names no colour the console's theme cannot move", () => {
    // The console defines its colours as custom properties per theme, so a
    // literal here would be a colour that ignores dark mode. (The plugin uses
    // `var(--…)` wherever it colours something of its own.)
    const source = bundle();
    expect(source).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(source).not.toMatch(/\b(?:rgba?|hsla?)\(/);
  });
});
