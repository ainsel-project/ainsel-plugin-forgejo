package work

import "testing"

// TestLinkedIssueClosingKeyword covers the strongest signal: the pull request
// body says in so many words that it closes, fixes or resolves an issue. Every
// spelling Forgejo and GitHub accept has to read, the keyword has to be a whole
// word, and it has to be followed by "#<digits>" — nothing else counts, because
// a pull that merely mentions an issue is not a pull that finishes it.
func TestLinkedIssueClosingKeyword(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
		ok   bool
	}{
		{"closes", "Closes #12", 12, true},
		{"lowercase", "closes #12", 12, true},
		{"shouted", "CLOSES #12", 12, true},
		{"close", "Close #12", 12, true},
		{"closed", "Closed #12", 12, true},
		{"fixes", "Fixes #12", 12, true},
		{"fix", "Fix #12", 12, true},
		{"fixed", "Fixed #12", 12, true},
		{"resolves", "Resolves #12", 12, true},
		{"resolve", "Resolve #12", 12, true},
		{"resolved", "Resolved #12", 12, true},
		{"mid-sentence", "This closes #12 for good.", 12, true},
		{"on its own line", "Some text\n\nFixes #12\n", 12, true},
		{"multiple digits", "Closes #1234567", 1234567, true},
		// The first statement of intent wins; the rest of the body may name
		// issues the pull only touches.
		{"first of several", "Closes #12, then fixes #30", 12, true},
		{"comma before the hash", "Fixes #12, #30", 12, true},

		// Not a closing keyword, so not a link.
		{"no hash", "Closes 12", 0, false},
		{"no space before the hash", "Closes#12", 0, false},
		{"hash without digits", "Closes #abc", 0, false},
		{"keyword is not a whole word", "Discloses #5", 0, false},
		{"keyword without an issue after it", "Close the #5 door", 0, false},
		{"a bare mention is not a closing", "Related to #5", 0, false},
		// #0 names no issue, so it links nothing rather than a page that
		// Forgejo does not serve.
		{"zero", "Closes #0", 0, false},
		// An unusable number does not use up the body, though: the body is
		// read match by match and the next keyword still answers.
		{"zero falls to the next keyword", "Closes #0, fixes #30", 30, true},
		// More digits than a number holds name no issue, for the same reason.
		{"overflow", "Closes #99999999999999999999999", 0, false},
		{"overflow falls to the next keyword", "Closes #99999999999999999999999, fixes #30", 30, true},
		{"empty body", "", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, ok := LinkedIssue("", "", tt.body)
			if n != tt.want || ok != tt.ok {
				t.Errorf("LinkedIssue(\"\", \"\", %q) = %d, %v, want %d, %v", tt.body, n, ok, tt.want, tt.ok)
			}
		})
	}
}

// TestLinkedIssueTitleScope covers the second signal: a Conventional-Commit
// title that carries the issue in its scope, "feat(#264): ...". The scope has to
// be a number, and it has to be the scope — a "#7" anywhere else in the title is
// the author mentioning an issue, not filing the pull under it.
func TestLinkedIssueTitleScope(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  int
		ok    bool
	}{
		{"feature", "feat(#264): search the word list", 264, true},
		{"fix", "fix(#5): handle an empty list", 5, true},
		{"two-digit scope", "chore(#142): drop the old flag", 142, true},
		{"scope only", "feat(#9)", 9, true},

		{"no scope", "chore: bump deps", 0, false},
		{"no number in the scope", "feat(#abc): x", 0, false},
		{"scope without a hash", "feat(264): x", 0, false},
		{"no type before the scope", "(#9): x", 0, false},
		{"space before the scope", "feat (#9): x", 0, false},
		{"second paren is not the scope", "feat(api)(#9): x", 0, false},
		{"mention rather than scope", "fix: address #5", 0, false},
		// The title is read from its start, so a heading or a leading space
		// before the type is not a Conventional-Commit title.
		{"leading space", " feat(#9): x", 0, false},
		{"empty title", "", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, ok := LinkedIssue(tt.title, "", "")
			if n != tt.want || ok != tt.ok {
				t.Errorf("LinkedIssue(%q, \"\", \"\") = %d, %v, want %d, %v", tt.title, n, ok, tt.want, tt.ok)
			}
		})
	}
}

// TestLinkedIssueBranch covers the last signal: the branch name, which by the
// usual convention carries the issue it was cut for — "fix/896-configuration-
// summary". A branch that is not named that way (renovate's, or a bare "fix/896"
// with no description after the number) says nothing and must not invent a link.
func TestLinkedIssueBranch(t *testing.T) {
	tests := []struct {
		name   string
		branch string
		want   int
		ok     bool
	}{
		{"fix", "fix/896-configuration-summary", 896, true},
		{"feature", "feat/264-word-list-search", 264, true},
		{"single digit", "feature/1-add-x", 1, true},
		{"dash in the prefix", "chore-legacy/7-clean-up", 7, true},
		{"more slashes after the description", "fix/896-a/b", 896, true},

		{"no number", "renovate/deps", 0, false},
		{"number without a description", "fix/896", 0, false},
		{"number with a hash", "fix/#896-x", 0, false},
		{"number in a later segment", "fix/sub/896-x", 0, false},
		{"no prefix", "/896-x", 0, false},
		{"empty branch", "", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, ok := LinkedIssue("", tt.branch, "")
			if n != tt.want || ok != tt.ok {
				t.Errorf("LinkedIssue(\"\", %q, \"\") = %d, %v, want %d, %v", tt.branch, n, ok, tt.want, tt.ok)
			}
		})
	}
}

// TestLinkedIssuePrecedence pins the order the three are read in: what the body
// states outright beats what the title groups under, which beats what the branch
// hints at. The order is the point — a pull titled for one issue and titled to
// close another closes the one it says it closes.
func TestLinkedIssuePrecedence(t *testing.T) {
	tests := []struct {
		name   string
		title  string
		branch string
		body   string
		want   int
		ok     bool
	}{
		{
			// The brief's case: title and branch agree, so either reads.
			name:   "title scope and branch agree",
			title:  "feat(#264): search the word list",
			branch: "feat/264-word-list-search",
			body:   "",
			want:   264, ok: true,
		},
		{
			// The brief's case: no scope in the title, so the branch answers.
			name:   "branch alone",
			title:  "fix: x",
			branch: "fix/896-configuration-summary",
			body:   "",
			want:   896, ok: true,
		},
		{
			// The brief's case: the body's closing keyword wins over the scope.
			name:   "body beats title",
			title:  "feat(#9): x",
			branch: "",
			body:   "Closes #12",
			want:   12, ok: true,
		},
		{
			name:   "body beats branch",
			title:  "",
			branch: "fix/30-x",
			body:   "Fixes #12",
			want:   12, ok: true,
		},
		{
			name:   "body beats both",
			title:  "feat(#9): x",
			branch: "feat/264-x",
			body:   "Closes #12",
			want:   12, ok: true,
		},
		{
			// The body mentions an issue but closes none, so it does not
			// get to answer at all and the title still does.
			name:   "unclosing body falls to the title",
			title:  "feat(#9): x",
			branch: "feat/264-x",
			body:   "Mentions #7 in passing",
			want:   9, ok: true,
		},
		{
			// The body's only closing keyword names an issue that cannot
			// exist, which does not settle the body — the title still gets
			// to answer.
			name:   "unusable body number falls to the title",
			title:  "feat(#9)",
			branch: "",
			body:   "Closes #0",
			want:   9, ok: true,
		},
		{
			name:   "title beats branch",
			title:  "feat(#9): x",
			branch: "feat/264-x",
			body:   "",
			want:   9, ok: true,
		},
		{
			// The brief's case: nothing anywhere names an issue.
			name:   "nothing names an issue",
			title:  "chore: bump deps",
			branch: "renovate/deps",
			body:   "",
			want:   0, ok: false,
		},
		{
			// The brief's case: a hash with no number behind it is not a
			// number, wherever it appears.
			name:   "hash without a number in the title",
			title:  "#abc",
			branch: "",
			body:   "",
			want:   0, ok: false,
		},
		{
			name:   "hash without a number in the branch",
			title:  "",
			branch: "#abc",
			body:   "",
			want:   0, ok: false,
		},
		{
			name:   "hash without a number in the body",
			title:  "",
			branch: "",
			body:   "#abc",
			want:   0, ok: false,
		},
		{
			name:   "all empty",
			title:  "",
			branch: "",
			body:   "",
			want:   0, ok: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, ok := LinkedIssue(tt.title, tt.branch, tt.body)
			if n != tt.want || ok != tt.ok {
				t.Errorf("LinkedIssue(%q, %q, %q) = %d, %v, want %d, %v",
					tt.title, tt.branch, tt.body, n, ok, tt.want, tt.ok)
			}
		})
	}
}
