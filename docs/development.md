# Development

One Go binary, one UI bundle embedded in it, one chart, one set of markdown docs. Everything
builds from the repository root.

## Toolchain

| | |
|---|---|
| **Go** | 1.26 (`go.mod`) |
| **Node / pnpm** | Node 24, pnpm 9.15.9 — the versions CI installs |
| **Helm** | For the chart and its render tests |
| **Docker (or any OCI builder)** | For the image |

## Layout

```
cmd/plugin/            main: assets, stores, hub client, cutover, reconcile, server
internal/
  api/                 the HTTP surface: work reads, setup, settings, instances
  mcp/                 the MCP tools — the same services over MCP
  server/              the server, and the manifest/route agreement check
  manifest/            ainsel-plugin.json, parsed and validated
  setup/               the wizard's plan, rails and apply
  reconcile/           the re-apply engine
  cutover/             the fresh-start retirement pass
  forgejo/ hub/ forge/ the three clients
  work/                 the read model behind the work view
  settings/ store/     the settings store and the install store (SQLite)
  skill/               the composed workflow skill
content/               shipped personas, role tool profiles, skill sections — the defaults
ui/                    the console UI: a Vite library build with its own pnpm lockfile
chart/                 the Helm chart, with render tests in chart/tests/
docs/                  the documentation this site is built from
docs-site/             the GitHub Pages site — see below
```

The rule that holds the two halves together: `content/` holds what ships as a default, the
plugin's API holds what an admin owns, and the wizard holds the flow between them.

## Make targets

```bash
make test        # go test ./... and pnpm -C ui test
make ui          # pnpm install --frozen-lockfile && build the UI bundle
make build       # the plugin binary (needs the UI bundle, so it depends on `ui`)
make image       # docker build of the plugin image
make chart-lint  # helm lint plus the chart's render tests
make run         # go run ./cmd/plugin
```

## Running it locally

The plugin needs a hub to be interesting. Standalone it starts, serves its manifest, UI and
MCP endpoint, and the work routes say there is no hub rather than reporting no work.

```bash
export PORT=8080
export HUB_URL=http://localhost:8080        # e.g. via kubectl port-forward
export DATA_DIR=./.data
export FORGEJO_BASE_URL=https://forgejo.example.com
make build && ./bin/plugin
```

`FORGEJO_ADMIN_TOKEN` is for the fresh-start cutover only — leave it unset in ordinary
development. Settings and installs go to the SQLite stores under `DATA_DIR`, so a scratch
directory is a clean slate.

## Tests

```bash
go test ./...                       # the Go half, including the plan/apply rails
go vet ./...
pnpm -C ui test                     # the UI's vitest suite
bash chart/tests/render_test.sh     # the chart's render tests (make chart-lint)
```

CI runs vet, the Go tests, `make ui`, the UI tests, the chart render tests and a build of the
image — so a branch that fails any of them is not a branch that merges.

## The UI

`ui/` is a Vite **library** build: it exports the page components the console mounts, and the
bundle is embedded in the binary and served under `/ui/`. It has its own `package.json` and
lockfile, installed with pnpm:

```bash
pnpm -C ui install
pnpm -C ui test
make ui        # install --frozen-lockfile + build
```

The console is the caller — the pages fetch their own data from the plugin's API through the
hub proxy, so nothing here holds a token.

## Docs

`docs/` is the single source of truth: the AInsel console's Docs page and the published site
both read these files, and the sidebar is `docs/_sidebar.md`.

- **Add a page**: create `docs/<slug>.md` and add a `- [Title](slug)` line to
  `docs/_sidebar.md`. No code change, no build step to remember.
- **Link between docs** with the bare slug — `[Live board](live-board)` — not a `.md` path. The
  site turns those into in-site routes; a `foo.md` link is left as a dead click.
- **In-page anchors** (`[section](#the-section)`) scroll the article.
- `docs/design/` and `docs/plans/` are the reasoning behind the wizard and the plans that built
  it. They stay in git, where the links from the README and the code work, and they are not
  published to the site.

### The docs site

`docs-site/` is a Vite + React site that renders `docs/` in the AInsel design system — the same
tokens, primitives and Docs page layout the console uses, vendored under
`docs-site/src/design/` rather than depended on, so the site builds from this repository alone.

```bash
cd docs-site
pnpm install
pnpm dev       # http://localhost:5174/ainsel-plugin-forgejo/
pnpm test
pnpm lint
pnpm build     # tsc -b && vite build → dist/
```

A `vite` plugin serves `../docs/` in development and copies it into `dist/docs` at build time —
so a doc is edited in place and appears in the artifact, and a doc removed from git does not
survive in it. `design/` and `plans/` are the two directories left out.

The site is published from `main` by `.github/workflows/deploy-docs-pages.yml`, which builds
`docs-site/` and uploads `dist/` to GitHub Pages. It is served from a subpath named after the
repository, which is why the base path is `/ainsel-plugin-forgejo/` (override with
`DOCS_SITE_BASE_PATH` when hosting elsewhere) and why routing is a `HashRouter`: a Pages
request goes to the path the browser asked for, and no `_redirects` file can be served by a
workflow-built Pages site.

Two things about that publish step are worth knowing before the first deploy:

- **Pages has to be told to deploy from Actions, once, per repository** — Settings → Pages →
  Build source → GitHub Actions. With the default source the workflow runs, succeeds, and
  nothing appears at the URL, because the default source expects a `gh-pages` branch and is not
  listening to the upload. This is a repository setting, so no workflow can set it.
- **CI checks the site too**, in the `Docs site` job of `.github/workflows/ci.yml`: lint, format,
  the content tests, and a build. The deploy workflow repeats them because repeating them is
  what keeps a broken site from being published from `main`.

The design system's fonts load from Google Fonts at runtime: a reader with no route to them
gets the fallback stack, which is legible but is not this design. Everything else is in the
bundle, so the site works offline once opened.

## Making a change

Work on a branch from the current `origin/main`, one purpose per branch, and open a pull
request — never push to `main`, and never merge your own. Conventional Commits
(`feat: …`, `fix: …`, `docs: …`), one logical change each.

```bash
git fetch origin && git checkout -b fix/something origin/main
# … change, test, review the diff …
git add -A && git commit -m "fix: what and why" && git push -u origin fix/something
```

The gate on documentation is the same as the gate on code: **every statement that names a UI
string, a route, a flag or a default has to hold in the code**. If it names something the code
calls another thing, the document is wrong even when the sentence is true.

Releases are cut by release-please and published by `.github/workflows/release.yml`, which keeps
the version in `ainsel-plugin.json`, the image tag and the chart version in step, signs the image
keyless, and prints the digest in the job summary — see [Publishing a Release](release).

## Where to go next

- [Architecture](architecture) — how the pieces fit before you change one
- [Configuration](configuration) — what the chart and the environment set
- [Publishing a Release](release) — tagging through to the published digest
