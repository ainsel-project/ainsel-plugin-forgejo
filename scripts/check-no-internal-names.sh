#!/usr/bin/env bash
# Fails when a retired personal identifier comes back into the published tree.
#
# This repo is public, and its fixtures were once seeded with a real person's login.
# A rename without a guard is a rename that undoes itself the next time someone
# copies a fixture out of the private install, so the rule is checked on every pull
# request, beside the other Go gates in .github/workflows/ci.yml.
#
# The retired strings are built by concatenation: a script that spelled out the name
# it guards would reintroduce the leak in the file meant to prevent it — and would
# then fail its own grep. Test data must use the cast this repo already uses: the
# acme owner, acme-dev as a sender and Morgan Doe as a display name.
set -uo pipefail

retired=(
  "dpi" "nsel"           # the personal login
  "Dominik" "Pinsel"     # the same person's handle on the public host
  "D. " "Pinsel"         # the display name as the fixtures spelled it
)

# The pairs above, joined into the strings to look for.
patterns=()
for ((i = 0; i < ${#retired[@]}; i += 2)); do
  patterns+=("${retired[i]}${retired[i + 1]}")
done

# The text this repo publishes or serves: docs, the README, Go and UI sources with
# their fixtures, the plugin content, and the scripts. git ls-files keeps tracked
# files only, so an untracked scratch file cannot fail someone's CI.
files=$(git ls-files -- \
  'docs' 'README.md' 'content' 'ui/src' 'scripts' '.github' \
  '*.go' '*_test.go' '*.json' '*.md' '*.yml' '*.yaml')

# .github/CODEOWNERS is the one tracked file allowed to name a person: naming the maintainer is
# what that file is for, and an entry that named nobody would own nothing.
files=$(printf '%s\n' "$files" | grep -v '^\.github/CODEOWNERS$')

status=0
for needle in "${patterns[@]}"; do
  hits=$(grep -Iin -- "$needle" $files || true)
  if [ -n "$hits" ]; then
    status=1
    printf '%s found in the published tree:\n' "$needle"
    printf '%s\n' "$hits"
  fi
done

if [ "$status" -ne 0 ]; then
  echo
  echo "These identifiers are retired (issue #19). Use the neutral cast instead."
  exit 1
fi

echo "No retired personal identifiers in the published tree."
