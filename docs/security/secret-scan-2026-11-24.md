# Secret scan — 2026-11-24

The first scan of this repository's **full git history**, run before the repo gets wide
attention. It is committed here rather than attached to an issue because the finding of a
scan is only useful next to the code it describes, and because CI can repeat the same gate on
every pull (`.github/workflows/secret-scan.yml`).

## How it was run

| Tool | Version | Scope | Result |
|---|---|---|---|
| [gitleaks](https://github.com/gitleaks/gitleaks) | 8.30.1 | git mode, **all 40 commits** of `main` from a full (unshallowed) clone | 1 finding, triaged below |
| [trufflehog](https://github.com/trufflesecurity/trufflehog) | 3.100.1 | `git file://.`, whole history | 0 findings |

trufflehog ran with verification off: a finding here would have been a string whose owner has
not been asked about it, and sending an unknown credential to its provider to confirm it is
the wrong first move. Neither scan produced raw output in git — this page is the triage, not a
dump, and it quotes no secret in full.

A shallow clone would have made either scan vacuous while still exiting 0, so the full history
is what was scanned, and the CI job that repeats it checks out with `fetch-depth: 0`.

## Findings and triage

### 1. A test sentinel matched `generic-api-key` — false positive, ignored by fingerprint

- **Where:** `internal/mcp/setup_tools_test.go:726`, at the importing commit
  (`fd5171a`, the single-commit import of the pre-public tree).
- **What:** one of the five sentinel strings `TestMCPSetupToolsCarryNoSecrets` plants as the
  admin token and the four LLM keys, to assert that none of them comes back out of a plan, an
  apply, or a rendered apply error. The value is a canary, not a credential: it names no
  provider's key shape, and the test fails if the string ever *reappears* in an answer.
- **Why it matched:** gitleaks' generic rule reads a high-entropy token in a `*_KEY` position
  and cannot know the test's intent.
- **Action:** none in the code. Recorded in `.gitleaksignore` as a fingerprint, so the entry
  is per-finding — deleting the line re-opens the finding, and no new finding is silenced by
  it. No `.gitleaks.toml` exists: an allowlist broad enough to be convenient is broad enough
  to hide a real leak.

### 2. No live credential in history

gitleaks found nothing else across all 40 commits, and trufflehog — whose ruleset is shaped
like a provider's actual key formats rather than a generic entropy test — found nothing at
all. The four credentials a setup passes through ([Credentials and access](https://ainsel-project.github.io/ainsel-plugin-forgejo/credentials)
is the account of where each goes) never appear to have been committed: the site-admin token
is used once inside the create/remove run and dropped, the bot tokens go straight into a role
image's environment, the LLM keys are held by the hub, and the webhook secret is handed to
Forgejo in the same flow that creates it.

Nothing in this scan contradicts that story. It is worth repeating after any change that adds
a place a credential is written — a log line, an export, a fixture — which is what the CI step
is for.

## Not a secret, but not for the public record either

Three things a secret scanner does not report, found by grep in the same pass, all
**still present in history** and none fixable by a commit on `main`:

- **Internal planning documents.** `docs/plans/` — three implementation plans naming a real
  person and internal issue numbers — was deleted on `main` by this change. The blobs are
  reachable from every earlier commit, and `git log --diff-filter=D` shows when they went.
- **Real logins in test data.** `internal/forge/testdata/*.json`, `internal/hub/*_test.go`,
  `internal/api/*_test.go`, `internal/setup/*_test.go`, `internal/store/store_test.go`,
  `internal/mcp/server_test.go`, `ui/src/**` and the scripts carried a personal login and
  display name in fixture values. All renamed to the neutral cast (`acme`, `acme-dev`,
  Morgan Doe) on `main`, with each expectation moved in the same commit. Same caveat: the
  old values remain in history.
- **A dev-instance host in two examples.** `scripts/check-wizard-facts.sh` and
  `internal/store/store_test.go` carry `devoper.duckdns.org` as an example URL. Decision,
  recorded so it is tracked rather than silent: **left as-is**. It is a host name from the
  private install's dev instance, not one of the retired personal identifiers this issue is
  about, and `duckdns` stays in the persona guard's list (`content/content_test.go`) so the
  string cannot spread unnoticed. Retire it in a follow-up if the owner wants the host gone
  from the record too.

Whether to rewrite history (`git filter-repo`, force-push `main`, re-tag `v0.1.0`/`v0.2.0`/
`v0.3.0`) is the repository owner's call, not a change bundled into a chore pull. Two facts
bear on the timing: there are 0 forks and one released-with-nothing-else surface today, and
any clone already made keeps the old blobs whatever happens — so the cost of rewriting rises
weekly while the benefit decays the same way.

## Continuous from here

`.github/workflows/secret-scan.yml` repeats this over the **whole history on every trigger** —
`pull_request`, the weekly schedule, and a `workflow_dispatch` for running it by hand after a
history change — checking out with `fetch-depth: 0` and failing if the clone came out shallow.
A new finding there is a finding to triage into this page or to fix, in the same shape as above.

This page is deliberately off the docs-site navigation: it records one scan run at one point in
time, and it is reached from the Development guide (`docs/development.md`) rather than listed as
a living page. Its own links are therefore absolute, matching the style above.

A fingerprint names a commit, a file and a line, so it is only stable while none of the three
move: an edit above the sentinel line changes the line number, and **a history rewrite changes
every commit sha — after one, `.gitleaksignore` must be re-derived from a fresh scan**, or the
gate goes red on entries that no longer match anything.
