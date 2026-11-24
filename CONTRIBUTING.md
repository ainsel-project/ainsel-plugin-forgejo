# Contributing

The full development guide is [docs/development.md](docs/development.md) — toolchain, layout,
make targets, how to run the plugin locally, and how the docs and the docs site fit together.
This page is only the part a first pull request needs; where the two disagree, the doc wins,
because the doc is what the console's Docs page publishes.

## Before you start

- Read [the published docs](https://ainsel-project.github.io/ainsel-plugin-forgejo/), starting
  at [Overview](docs/overview.md). A change to behaviour is a change to a doc; a doc that
  disagrees with the code is treated here as a bug, not a nuance.
- [Security issues are handled privately](SECURITY.md) — not through an issue.
- [Contributor Covenant 2.1](CODE_OF_CONDUCT.md) is how we behave in issues and reviews.

## Making the change

1. Branch from `origin/main`. One purpose per branch.
2. Commit with [Conventional Commits](https://www.conventionalcommits.org/) — release notes and
   releases are read out of them by release-please.
3. Never push to `main`, and never merge your own pull request.
   (Required review from code owners is deliberately **off**: this repo's pull requests are
   routinely opened by AInsel agents, and a mandatory human sign-off on every merge is not the
   working rule here. `CODEOWNERS` names who owns which path — take the review from them.)
4. Run the gates before pushing:

   ```sh
   make test        # go test ./... and pnpm -C ui test
   make chart-lint  # helm lint plus the chart's render tests
   bash scripts/check-no-internal-names.sh   # no retired internal names in the tree
   ```

   The docs site has its own (`pnpm -C docs-site lint && pnpm -C docs-site test && pnpm -C
   docs-site build`) — CI runs it, so a doc that breaks the site fails here rather than at
   deploy. See [docs/development.md](docs/development.md#the-docs-site).

## What a pull request should say

What changed, why, and how to check it — and which docs it touches. `.github/PULL_REQUEST_TEMPLATE.md`
is the shape. An issue-closing line (`Closes #N`) belongs in the body.

## Tests

New behaviour arrives with a test for it, and the repo's rule applies to tests as it does to
docs: a test asserts what the code does, not what a plan said it would. The chart's render
tests, the docs site's checks on `docs/`, and the fixture tests under `internal/forge/testdata`
are all part of `make test` / the docs job — a green Go suite alone is not the gate.
