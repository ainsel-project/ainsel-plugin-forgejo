package forge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture reads one trimmed Forgejo webhook payload from testdata. The payloads
// are the fixtures a subject is extracted from, so a test that misnames a file
// has to fail loudly rather than parse an empty body.
func fixture(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// TestSubjectFromEvent walks the webhook payloads a Forgejo connector delivers
// and checks which of them name an issue or pull request the run worked on, and
// what that subject is. A push has no issue or pull request behind it, and
// neither does an event type the forge does not send for one, so those are the
// cases that must come back as "no subject" rather than as a half-empty one.
func TestSubjectFromEvent(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		fixture   string
		repo      string
		number    int
		kind      Kind
		title     string
		wantOK    bool
	}{
		{
			name:      "issues",
			eventType: "issues",
			fixture:   "issues.json",
			repo:      "apps/lingo-swipe",
			number:    7,
			kind:      KindIssue,
			title:     "Swipe deck loses progress on reload",
			wantOK:    true,
		},
		{
			name:      "comment on an issue stays an issue",
			eventType: "issue_comment",
			fixture:   "issue_comment_issue.json",
			repo:      "dpinsel/cockpit",
			number:    142,
			kind:      KindIssue,
			title:     "Nightly sync job silently skips deleted repos",
			wantOK:    true,
		},
		{
			// The payload carries both the issue and the pull request; the pull
			// is what the run worked on, so it is what the subject has to be.
			name:      "comment on a pull request is a pull",
			eventType: "issue_comment",
			fixture:   "issue_comment_pull.json",
			repo:      "apps/lingo-swipe",
			number:    12,
			kind:      KindPull,
			title:     "Add offline deck cache",
			wantOK:    true,
		},
		{
			name:      "pull_request",
			eventType: "pull_request",
			fixture:   "pull_request.json",
			repo:      "dpinsel/cockpit",
			number:    88,
			kind:      KindPull,
			title:     "Register the forgejo plugin in the chart",
			wantOK:    true,
		},
		{
			// Approvals arrive as their own event type, named after the pull
			// they belong to, with the same payload shape.
			name:      "pull_request_approved",
			eventType: "pull_request_approved",
			fixture:   "pull_request_approved.json",
			repo:      "apps/lingo-swipe",
			number:    12,
			kind:      KindPull,
			title:     "Add offline deck cache",
			wantOK:    true,
		},
		{
			name:      "push has no subject",
			eventType: "push",
			fixture:   "push.json",
			wantOK:    false,
		},
		{
			// The event type decides, not the payload: a body that does name a
			// pull request is still not one when the event says otherwise.
			name:      "installation events have no subject",
			eventType: "installation_created",
			fixture:   "issue_comment_pull.json",
			wantOK:    false,
		},
		{
			name:      "unknown event type has no subject",
			eventType: "some_future_event",
			fixture:   "issues.json",
			wantOK:    false,
		},
		{
			name:      "missing event type has no subject",
			eventType: "",
			fixture:   "issues.json",
			wantOK:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, ok := SubjectFromEvent(tt.eventType, fixture(t, tt.fixture))
			if ok != tt.wantOK {
				t.Fatalf("SubjectFromEvent(%q) ok = %v, want %v (subject %+v)", tt.eventType, ok, tt.wantOK, s)
			}
			if !ok {
				return
			}
			if s.Repo != tt.repo {
				t.Errorf("Repo = %q, want %q", s.Repo, tt.repo)
			}
			if s.Number != tt.number {
				t.Errorf("Number = %d, want %d", s.Number, tt.number)
			}
			if s.Kind != tt.kind {
				t.Errorf("Kind = %q, want %q", s.Kind, tt.kind)
			}
			if s.Title != tt.title {
				t.Errorf("Title = %q, want %q", s.Title, tt.title)
			}
		})
	}
}

// TestSubjectFromEventBranchAndBody checks the two fields that say what the run
// was actually handed: the branch a pull request works on, and the text the
// event carried. For a pull request that is the pull's own body; for a comment
// it is the comment, not the body of the issue or pull the comment sits under.
func TestSubjectFromEventBranchAndBody(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		fixture   string
		branch    string
		body      string
	}{
		{
			name:      "issue body",
			eventType: "issues",
			fixture:   "issues.json",
			branch:    "",
			body:      "Open a deck, swipe three cards, reload the page: the deck starts over at the first card.",
		},
		{
			name:      "comment on an issue carries the comment",
			eventType: "issue_comment",
			fixture:   "issue_comment_issue.json",
			branch:    "",
			body:      "Reproduced on a fresh install: the two deleted remotes are the ones missing.",
		},
		{
			name:      "comment on a pull carries the comment and the branch",
			eventType: "issue_comment",
			fixture:   "issue_comment_pull.json",
			branch:    "feat/offline-cache",
			body:      "Please cover the eviction path before this merges.",
		},
		{
			name:      "pull request carries its own body and head branch",
			eventType: "pull_request",
			fixture:   "pull_request.json",
			branch:    "feat/register-forgejo-plugin",
			body:      "Adds the Plugin resource for the forgejo plugin, off by default.",
		},
		{
			name:      "approved pull request carries its own body and head branch",
			eventType: "pull_request_approved",
			fixture:   "pull_request_approved.json",
			branch:    "feat/offline-cache",
			body:      "Caches the active deck in IndexedDB and falls back to the network on a miss.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, ok := SubjectFromEvent(tt.eventType, fixture(t, tt.fixture))
			if !ok {
				t.Fatalf("SubjectFromEvent(%q) reported no subject", tt.eventType)
			}
			if s.Branch != tt.branch {
				t.Errorf("Branch = %q, want %q", s.Branch, tt.branch)
			}
			// A real body carries several paragraphs; the fixtures keep the
			// first line as the part worth pinning.
			if !strings.HasPrefix(s.Body, tt.body) {
				t.Errorf("Body = %q, want it to start with %q", s.Body, tt.body)
			}
		})
	}
}

// TestSubjectFromEventMalformedPayload checks that a body which is not JSON at
// all, or is JSON that names no repository, is reported as "no subject" instead
// of a panic or an empty subject a later link builder would have to guess at.
func TestSubjectFromEventMalformedPayload(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		payload   string
	}{
		{"not json", "issues", `not json at all`},
		{"empty body", "issues", ``},
		{"no repository", "issues", `{"issue":{"number":7,"title":"t"}}`},
		{"issue event without an issue", "issues", `{"repository":{"full_name":"apps/lingo-swipe"}}`},
		{"comment event without an issue", "issue_comment", `{"repository":{"full_name":"apps/lingo-swipe"}}`},
		{"pull request event without a pull", "pull_request", `{"repository":{"full_name":"apps/lingo-swipe"}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if s, ok := SubjectFromEvent(tt.eventType, []byte(tt.payload)); ok {
				t.Errorf("SubjectFromEvent(%q, %s) = %+v, want no subject", tt.eventType, tt.payload, s)
			}
		})
	}
}
