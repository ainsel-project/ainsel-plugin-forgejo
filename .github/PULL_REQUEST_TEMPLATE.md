## What changed

<!-- One paragraph. What is different after this pull request. -->

## Why

<!-- The problem it solves, with `Closes #N` here when it closes an issue. -->

## Which docs does it affect

<!-- A doc that disagrees with the code is wrong, so say whether docs/ changed, or why not.
Same for the README and the plugin manifest (ainsel-plugin.json) if either moved. -->

## How to verify

- [ ] `make test` (Go + UI)
- [ ] `make chart-lint`, if anything under `chart/` changed
- [ ] `pnpm -C docs-site lint && pnpm -C docs-site test && pnpm -C docs-site build`, if anything under `docs/` changed
- [ ] `bash scripts/check-no-internal-names.sh`

## Notes for the reviewer

<!-- Anything a reviewer should know that the diff cannot say: a deliberate deviation, a
migration, a known gap. -->
