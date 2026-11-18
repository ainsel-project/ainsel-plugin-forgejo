import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// The console loads this bundle as an ES module and provides React and the
// AInsel SDK itself, so those stay external (global-constraints: the UI bundle
// must not include them) — all five specifiers its import map answers, from
// `react` to `@ainsel/plugin-sdk`, including the two-level `react-dom/client`,
// which would otherwise pull in a second renderer. One file, one entry:
// dist/index.js exports the page and the slot components named by the manifest.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
    // Unminified: the bundle is meant to be read, and the bundle
    // test asserts on its text.
    minify: false,
    lib: {
      entry: "src/index.tsx",
      formats: ["es"],
      fileName: () => "index.js",
    },
    rollupOptions: {
      external: [
        "react",
        "react-dom",
        "react-dom/client",
        "react/jsx-runtime",
        "@ainsel/plugin-sdk",
      ],
    },
  },
  test: {
    environment: "jsdom",
    // Mocks `@ainsel/plugin-sdk` for every test file: the console provides the
    // real one at runtime, and no component test has it (src/testing/sdk.tsx).
    setupFiles: ["./src/testing/setup.ts"],
  },
});
