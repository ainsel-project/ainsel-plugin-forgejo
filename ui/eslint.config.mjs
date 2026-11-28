import js from "@eslint/js";
import tseslint from "typescript-eslint";
import reactHooks from "eslint-plugin-react-hooks";

// Flat config, mirroring docs-site/eslint.config.mjs — one JS convention for the
// repo, not two. File selection lives in the `files` globs below and in the
// plugins' own configs (ESLint 9 dropped .eslintrc.* support and the --ext flag).
//
// Note there is no `globals` block: tseslint.configs.recommended turns off
// no-undef (TypeScript already reports undeclared names), so browser and vitest
// globals need no declaration.
//
// no-explicit-any is listed explicitly even though the recommended set already
// errors on it, because it is the point of this config: the console's primitives
// are typed `React.ComponentProps<any>`, so an untyped handler is invisible to
// tsc — only this rule keeps `(e: any)` from coming back.
export default [
  js.configs.recommended,
  ...tseslint.configs.recommended,
  reactHooks.configs.flat.recommended,
  {
    files: ["src/**/*.ts", "src/**/*.tsx"],
    languageOptions: {
      ecmaVersion: "latest",
      sourceType: "module",
    },
    rules: {
      "@typescript-eslint/no-unused-vars": [
        "error",
        {
          argsIgnorePattern: "^_",
          varsIgnorePattern: "^_",
          // `const { [k]: _gone, ...rest } = m` — the dropped name is the pattern's
          // punctuation, not a binding.
          ignoreRestSiblings: true,
        },
      ],
      "@typescript-eslint/no-explicit-any": "error",
      // The same three the docs-site config turns off, for the same reason: they
      // police patterns the console's SDK already blesses, not bugs.
      "react-hooks/refs": "off",
      "react-hooks/set-state-in-effect": "off",
      "react-hooks/incompatible-library": "off",
    },
  },
];
