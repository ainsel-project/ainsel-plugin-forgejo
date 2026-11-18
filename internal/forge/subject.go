// Package forge adapts Forgejo's own shapes — webhook event payloads and web
// URLs — to the way AInsel talks about the work an agent did on the forge.
//
// A run in AInsel is about one thing: an issue or a pull request. Forgejo says
// the same thing in a webhook payload whose shape depends on which event fired,
// and points at it with a URL built from the forge's base address. This package
// is where both get read once, so the API, the MCP tools and the console agree
// on what a run's subject is and where it lives.
package forge

import (
	"encoding/json"
	"strings"
)

// Kind is what a subject is on the forge: an issue or a pull request. It is not
// decoration — it decides which page a subject links to, and it is what tells a
// reader whether "number 12" is an issue or a pull request.
type Kind string

const (
	// KindIssue is a Forgejo issue.
	KindIssue Kind = "issue"

	// KindPull is a Forgejo pull request.
	KindPull Kind = "pull"
)

// Event types this adapter reads, named as Forgejo names them in its event
// header. AInsel stores an event's type in the header rather than in the body,
// so whoever fetched the event passes it in here.
const (
	eventIssues       = "issues"
	eventIssueComment = "issue_comment"
	eventPullRequest  = "pull_request"
)

// Subject is the issue or pull request a run worked on — the forge-side half of
// an AInsel run, and the only reason the plugin shows that issue or pull
// request at all.
type Subject struct {
	// Repo is the repository's full name, "owner/repo" — Forgejo's
	// repository.full_name, and the segment every subject URL is built from.
	Repo string

	// Number is the issue or pull request number within Repo. It means
	// different things per Kind, which is why Kind travels with it.
	Number int

	// Kind says whether this subject is an issue or a pull request.
	Kind Kind

	// Title is the issue or pull request title — the thread's title for a
	// comment, since a comment is about the thread it sits under.
	Title string

	// Branch is the source branch of a pull request. Empty for an issue, which
	// has no branch of its own.
	Branch string

	// Body is the text the event carried: the issue or pull request body, or,
	// for a comment event, the comment itself — what the run was asked to act
	// on.
	Body string
}

// webhookPayload is a Forgejo (Gitea-compatible) webhook body, read only as far
// as a subject needs. Everything else a payload carries — labels, assignees,
// reviewers, the commit list, the sender — is left unread, so a payload with
// fields this build has never seen still parses.
type webhookPayload struct {
	Repository webhookRepository `json:"repository"`
	Issue      *webhookIssue     `json:"issue"`
	Comment    *webhookComment   `json:"comment"`

	// PullRequest is present on a pull_request event and on an issue_comment
	// whose issue is really a pull request, which is how a comment on a pull
	// request is told from a comment on an issue.
	PullRequest *webhookPullRequest `json:"pull_request"`
}

// webhookRepository is the repository half of a payload: its full name.
type webhookRepository struct {
	FullName string `json:"full_name"`
}

// webhookIssue is the issue half of an issues or issue_comment payload. For a
// comment on a pull request this is the pull's mirror issue, and its number is
// the pull's number.
type webhookIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

// webhookComment is the comment an issue_comment event carries.
type webhookComment struct {
	Body string `json:"body"`
}

// webhookPullRequest is the pull request half of a pull_request payload, or of
// an issue_comment that comments on one.
type webhookPullRequest struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Head   struct {
		Ref string `json:"ref"`
	} `json:"head"`
}

// SubjectFromEvent reads the issue or pull request an event is about out of the
// payload Forgejo sent for it. The event type decides the reading, because the
// same body shape means different things per event: a comment is about its
// thread, a pull request event is about the pull.
//
// It reports false when the event is about nothing an AInsel run works on — a
// push, an installation event, any event type this build does not know, or a
// body that names no repository. Callers skip those runs rather than show them
// as subject-less work.
func SubjectFromEvent(eventType string, payload []byte) (Subject, bool) {
	var p webhookPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return Subject{}, false
	}

	repo := strings.TrimSpace(p.Repository.FullName)
	if repo == "" {
		return Subject{}, false
	}

	switch {
	case eventType == eventIssues || eventType == eventIssueComment:
		if p.Issue == nil {
			return Subject{}, false
		}
		s := Subject{
			Repo:   repo,
			Number: p.Issue.Number,
			Kind:   KindIssue,
			Title:  p.Issue.Title,
			Body:   p.Issue.Body,
		}
		if eventType == eventIssueComment {
			// What the run was handed is the comment, not the text of the
			// thread it was written under.
			if p.Comment != nil {
				s.Body = p.Comment.Body
			}
			// A pull request is an issue to Forgejo, so a comment on one
			// arrives with the pull alongside its issue. That pull is what
			// the run worked on: it sets the kind, and it has the branch.
			if p.PullRequest != nil {
				s.Kind = KindPull
				s.Branch = p.PullRequest.Head.Ref
			}
		}
		return s, true

	case strings.HasPrefix(eventType, eventPullRequest):
		// pull_request and its siblings (pull_request_approved, and any the
		// forge adds later) all carry the pull in the same place.
		pr := p.PullRequest
		if pr == nil {
			return Subject{}, false
		}
		return Subject{
			Repo:   repo,
			Number: pr.Number,
			Kind:   KindPull,
			Title:  pr.Title,
			Branch: pr.Head.Ref,
			Body:   pr.Body,
		}, true
	}

	// push, installation_* and anything unknown: no issue, no pull request.
	return Subject{}, false
}
