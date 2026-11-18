// Package work reads the work an AInsel run did on Forgejo back out of the runs
// AInsel has recorded — which issue or pull request they were about, which issue
// a pull request belongs to, and how they add up per repository.
//
// AInsel knows about runs; Forgejo knows about issues and pull requests. This
// package is where the two get joined, and it is deliberately made of small
// pure functions over the pieces of a run rather than a client of its own: the
// data comes from the hub each time it is shown, and there is no plugin database
// to keep it in.
package work

import (
	"regexp"
	"strconv"
)

// The three places a pull request says which issue it works on, in the order
// they are trusted. They are ordered by how explicitly the author stated it:
// a closing keyword in the body is a promise that the issue is done, a scope in
// a Conventional-Commit title is how the author filed the pull, a number in a
// branch name is a convention that may be stale by the time the pull opens.
//
// The regexes are the whole rule — a pull request whose issue is named anywhere
// else is a pull request that does not say which issue it belongs to, and the
// plugin shows it unlinked rather than guessing.
var (
	// closingKeyword is Forgejo's own set of closing keywords, matched
	// case-insensitively and as a whole word so "Discloses #5" does not close
	// issue 5. The keyword group captures an alternative of its own — the
	// optional "e" of "fixes" — so the number this one hands back is the third
	// submatch, not the second.
	closingKeyword = regexp.MustCompile(`(?i)\b(close[sd]?|fix(e[sd])?|resolve[sd]?)\s+#(\d+)`)

	// scopedIssue is a Conventional-Commit title carrying the issue in its
	// scope: "feat(#264): search the word list". Anchored at the start, because
	// only the scope groups a pull under an issue — a "#264" elsewhere in the
	// title is a mention.
	scopedIssue = regexp.MustCompile(`^\w+\(#(\d+)\)`)

	// numberedBranch is the branch convention of "prefix/<number>-<summary>",
	// "fix/896-configuration-summary". The dash after the number is required:
	// it is what tells "896" the issue from "896" some other number that
	// happens to sit in the branch name.
	numberedBranch = regexp.MustCompile(`^[\w-]+/(\d+)-`)
)

// LinkedIssue reports the issue number a pull request references, read from its
// title, its branch name and its body.
//
// The three are tried in order and the first one that names a usable issue
// answers: a closing keyword in the body ("Closes #12"), then a Conventional-
// Commit scope in the title ("feat(#264): …"), then a numbered branch
// ("fix/896-…"). Within a source the matches are read in order too, so a body
// that opens "Closes #0, fixes #30" still links 30 — an unusable number does not
// spend the source, it only passes on that one match. It reports false when none
// of the three names an issue, and the caller shows the pull request on its own
// instead of nesting it under an issue nobody claimed.
//
// Only a number that can be an issue answers: "#0" is not one, and neither is a
// run of digits too long to be one — Forgejo serves no such page, and nesting a
// pull under a phantom issue is worse than leaving it unlinked.
func LinkedIssue(title, branch, body string) (int, bool) {
	if n, ok := firstIssueNumber(closingKeyword, 3, body); ok {
		return n, true
	}
	if n, ok := firstIssueNumber(scopedIssue, 1, title); ok {
		return n, true
	}
	if n, ok := firstIssueNumber(numberedBranch, 1, branch); ok {
		return n, true
	}
	return 0, false
}

// firstIssueNumber returns the first match of re in s whose issue number — the
// capture at numGroup — can be one, and false if the string matches nowhere or
// every match names a number that cannot be an issue.
//
// The title and branch regexes are anchored and so can match at most once; it is
// the body that can name several issues, where only the first usable one counts.
func firstIssueNumber(re *regexp.Regexp, numGroup int, s string) (int, bool) {
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		if n, ok := issueNumber(m[numGroup]); ok {
			return n, true
		}
	}
	return 0, false
}

// issueNumber turns a captured run of digits into an issue number, rejecting the
// ones that name no issue: zero and below, and more digits than a number holds.
func issueNumber(digits string) (int, bool) {
	n, err := strconv.Atoi(digits)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}
