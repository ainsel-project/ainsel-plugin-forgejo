package forge

import "testing"

// TestRepoURL checks the three ways a forge's base URL get written — bare host,
// trailing slash, and a subpath with a trailing slash — all produce the same
// repository URL, and that a repository whose name carries dots and a dash is
// joined untouched rather than escaped. Every issue, pull request and repo the
// plugin shows carries one of these URLs, so a doubled or dropped slash would
// be a broken link the admin has to notice, not a test.
func TestRepoURL(t *testing.T) {
	tests := []struct {
		base string
		repo string
		want string
	}{
		{"https://git.example.com", "apps/lingo-swipe", "https://git.example.com/apps/lingo-swipe"},
		{"https://git.example.com/", "apps/lingo-swipe", "https://git.example.com/apps/lingo-swipe"},
		{"https://git.example.com/forgejo/", "apps/lingo-swipe", "https://git.example.com/forgejo/apps/lingo-swipe"},
		{"https://git.example.com/forgejo", "apps/lingo-swipe", "https://git.example.com/forgejo/apps/lingo-swipe"},
		{"https://git.example.com", "a.b/c-d", "https://git.example.com/a.b/c-d"},
		{"https://git.example.com/", "a.b/c-d", "https://git.example.com/a.b/c-d"},
		{"https://git.example.com/forgejo/", "a.b/c-d", "https://git.example.com/forgejo/a.b/c-d"},
	}

	for _, tt := range tests {
		if got := RepoURL(tt.base, tt.repo); got != tt.want {
			t.Errorf("RepoURL(%q, %q) = %q, want %q", tt.base, tt.repo, got, tt.want)
		}
	}
}

// TestIssueURL pins the issue link shape: <base>/<owner>/<repo>/issues/<n>.
func TestIssueURL(t *testing.T) {
	tests := []struct {
		base string
		repo string
		n    int
		want string
	}{
		{"https://git.example.com", "apps/lingo-swipe", 7, "https://git.example.com/apps/lingo-swipe/issues/7"},
		{"https://git.example.com/", "apps/lingo-swipe", 7, "https://git.example.com/apps/lingo-swipe/issues/7"},
		{"https://git.example.com/forgejo/", "apps/lingo-swipe", 7, "https://git.example.com/forgejo/apps/lingo-swipe/issues/7"},
		{"https://git.example.com", "a.b/c-d", 142, "https://git.example.com/a.b/c-d/issues/142"},
		{"https://git.example.com/forgejo/", "a.b/c-d", 142, "https://git.example.com/forgejo/a.b/c-d/issues/142"},
		// A number that is not a number has no page to point at.
		{"https://git.example.com", "apps/lingo-swipe", 0, ""},
		{"https://git.example.com", "apps/lingo-swipe", -3, ""},
	}

	for _, tt := range tests {
		if got := IssueURL(tt.base, tt.repo, tt.n); got != tt.want {
			t.Errorf("IssueURL(%q, %q, %d) = %q, want %q", tt.base, tt.repo, tt.n, got, tt.want)
		}
	}
}

// TestPullURL pins the pull request link shape: <base>/<owner>/<repo>/pulls/<n>
// — "pulls", the path Forgejo serves, not "pull".
func TestPullURL(t *testing.T) {
	tests := []struct {
		base string
		repo string
		n    int
		want string
	}{
		{"https://git.example.com", "apps/lingo-swipe", 12, "https://git.example.com/apps/lingo-swipe/pulls/12"},
		{"https://git.example.com/", "apps/lingo-swipe", 12, "https://git.example.com/apps/lingo-swipe/pulls/12"},
		{"https://git.example.com/forgejo/", "apps/lingo-swipe", 12, "https://git.example.com/forgejo/apps/lingo-swipe/pulls/12"},
		{"https://git.example.com", "a.b/c-d", 88, "https://git.example.com/a.b/c-d/pulls/88"},
		{"https://git.example.com/forgejo/", "a.b/c-d", 88, "https://git.example.com/forgejo/a.b/c-d/pulls/88"},
		{"https://git.example.com", "apps/lingo-swipe", 0, ""},
		{"https://git.example.com", "apps/lingo-swipe", -3, ""},
	}

	for _, tt := range tests {
		if got := PullURL(tt.base, tt.repo, tt.n); got != tt.want {
			t.Errorf("PullURL(%q, %q, %d) = %q, want %q", tt.base, tt.repo, tt.n, got, tt.want)
		}
	}
}

// TestSubjectURL checks a subject's link comes from its kind: the same number
// reads as an issue on one subject and a pull request on the other, and a
// subject that cannot be linked at all — no number, no kind, no forge to link
// into — gives the empty string rather than a URL that leads nowhere.
func TestSubjectURL(t *testing.T) {
	const base = "https://git.example.com/forgejo/"

	tests := []struct {
		name    string
		subject Subject
		want    string
	}{
		{
			name:    "issue",
			subject: Subject{Repo: "apps/lingo-swipe", Number: 7, Kind: KindIssue},
			want:    "https://git.example.com/forgejo/apps/lingo-swipe/issues/7",
		},
		{
			name:    "pull",
			subject: Subject{Repo: "apps/lingo-swipe", Number: 12, Kind: KindPull},
			want:    "https://git.example.com/forgejo/apps/lingo-swipe/pulls/12",
		},
		{
			name:    "same number, other kind, other page",
			subject: Subject{Repo: "a.b/c-d", Number: 12, Kind: KindIssue},
			want:    "https://git.example.com/forgejo/a.b/c-d/issues/12",
		},
		{
			name:    "no number",
			subject: Subject{Repo: "apps/lingo-swipe", Number: 0, Kind: KindIssue},
			want:    "",
		},
		{
			name:    "no number on a pull either",
			subject: Subject{Repo: "apps/lingo-swipe", Number: 0, Kind: KindPull},
			want:    "",
		},
		{
			name:    "no repository",
			subject: Subject{Repo: "", Number: 7, Kind: KindIssue},
			want:    "",
		},
		{
			name:    "no kind",
			subject: Subject{Repo: "apps/lingo-swipe", Number: 7, Kind: ""},
			want:    "",
		},
		{
			name:    "empty subject",
			subject: Subject{},
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SubjectURL(base, tt.subject); got != tt.want {
				t.Errorf("SubjectURL(%+v) = %q, want %q", tt.subject, got, tt.want)
			}
		})
	}
}

// TestSubjectURLNoForge checks a plugin that was never pointed at a Forgejo
// (an empty base) links nothing: the answer is the empty string, which UI and
// MCP render as no link, never a root-relative path that looks like one.
func TestSubjectURLNoForge(t *testing.T) {
	s := Subject{Repo: "apps/lingo-swipe", Number: 7, Kind: KindIssue}
	if got := SubjectURL("", s); got != "" {
		t.Errorf(`SubjectURL("", issue) = %q, want ""`, got)
	}
	if got := RepoURL("", "apps/lingo-swipe"); got != "" {
		t.Errorf(`RepoURL("", "apps/lingo-swipe") = %q, want ""`, got)
	}
}
