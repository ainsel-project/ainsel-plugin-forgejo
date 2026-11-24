# docs-site

Static publication of this repository's documentation at
<https://ainsel-project.github.io/ainsel-plugin-forgejo/>, built from the same `docs/`
directory the console's Docs page serves.

## How it works

A Vite + React app that renders `docs/` as a docsify-style site: `docs/_sidebar.md` drives
the navigation, and each page is fetched from `docs/` at runtime (the build copies `docs/`
into `dist/docs/`). `docs/design/` is not published — it is the reasoning behind
the wizard, and it stays in git where the links to it work.

The design system and the Docs page layout are **vendored** under `src/design/` and
`src/components/`, copied from AInsel rather than imported from a sibling checkout: this
repository has no `frontend/` to import from, and a public site must build from its own
tree. The copies carry their upstream commit in their header, so they can be re-diffed
against AInsel and refreshed deliberately.

Routing uses `HashRouter` so links work on GitHub Pages without SPA fallback rewrites — a
Pages request goes to the path the browser asked for, and a workflow-built site has no
`_redirects` file to rewrite it. The Vite base path defaults to `/ainsel-plugin-forgejo/`
(the GitHub Pages project subpath); override with `DOCS_SITE_BASE_PATH` when hosting
elsewhere.

Fonts are the one thing this site fetches at run time (Google Fonts, as the console loads them);
everything else is in the bundle, so an opened page keeps working without a network.

## Local development

```sh
pnpm install
pnpm dev      # http://localhost:5174/ainsel-plugin-forgejo/
```

The dev server serves `../docs/` directly, so a doc edited in the repository is there on
reload. Port 5174 by default, leaving 5173 free for `ui/`.

```sh
pnpm test     # vitest — the docs shell renders a stubbed docs tree, and docs/ is checked
pnpm lint     # eslint, over src/ and dev-server/
pnpm build    # tsc -b && vite build → dist/
```

`src/docs.test.ts` reads the real `docs/` and holds it to four rules: every sidebar entry
names a file that exists, every published file is reachable from the sidebar, no doc links to
a slug that has no page, and every doc opens with a heading. All four fail quietly once
published — an empty page, an orphan, a dead click, a doc whose title came out blank — so they
fail here instead.

`dev-server/docsPath.ts` holds the one rule the dev server has: which file a request for
`<base>docs/<path>` may read. It is a function rather than four lines in the middleware so that
the paths which escape the docs directory have a test, since a prefix test on the resolved path
lets one through (see the comment there).

## Build & publish

Publishing is automated: `.github/workflows/deploy-docs-pages.yml` builds this package and
deploys `dist/` to GitHub Pages on a push to `main` that touches `docs/` or `docs-site/`.

Two settings are outside the workflow's reach. **Build source** has to be set to **GitHub
Actions** once on the repository (Settings → Pages): with the default source the workflow runs,
succeeds, and nothing appears at the URL. And `docs-site/.prettierignore` is what keeps the
vendored CSS out of the formatter — without it, `pnpm format` would rewrite the design files
away from the upstream they are copied from.

The same checks run on every pull request, in the `Docs site` job of
`.github/workflows/ci.yml`. The deploy workflow repeats them because that is the one which
decides whether to publish.

## Adding a doc

Drop a markdown file into `docs/` and add a `- [Title](slug)` line to `docs/_sidebar.md` —
no changes here required. The two belong together: a file the sidebar does not name fails
`pnpm test` as an orphan. Link between docs with the bare slug (`[Live board](live-board)`),
not a `.md` path, so the link becomes an in-site route; `[Setup](setup#check-now)` reaches a
heading in another doc.
