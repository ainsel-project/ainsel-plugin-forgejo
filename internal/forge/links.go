package forge

import (
	"strconv"
	"strings"
)

// The three pages of the forge a subject or a repository links to. These are
// Forgejo's own paths, and the console, the plugin API and the MCP tools all
// show the same ones, so they are built here once.
const (
	issuesSegment = "issues"
	pullsSegment  = "pulls"
)

// RepoURL is the forge's page for a repository: <base>/<owner>/<repo>.
//
// base is the forge's address as an admin wrote it — with or without a trailing
// slash, and with or without a subpath, since a self-hosted Forgejo commonly
// lives under one. An empty base means there is no forge to link to and no URL
// comes back; the caller shows the name without a link rather than a link to
// nowhere.
func RepoURL(base, repo string) string {
	return rootURL(base, repo)
}

// IssueURL is the forge's page for an issue: <base>/<owner>/<repo>/issues/<n>.
// A number that is not one — zero or negative — has no page, and no URL comes
// back.
func IssueURL(base, repo string, n int) string {
	return numberedURL(base, repo, issuesSegment, n)
}

// PullURL is the forge's page for a pull request:
// <base>/<owner>/<repo>/pulls/<n>. "pulls" is the path Forgejo serves; "pull"
// redirects to it and is not what a link should say.
func PullURL(base, repo string, n int) string {
	return numberedURL(base, repo, pullsSegment, n)
}

// SubjectURL is the forge's page for a subject — the pull request page for a
// pull, the issue page for an issue. A subject with no number, no repository or
// a kind that is neither has no page to point at and gets the empty string, so a
// caller can tell "no link" from a link and render it as plain text.
func SubjectURL(base string, s Subject) string {
	switch s.Kind {
	case KindPull:
		return PullURL(base, s.Repo, s.Number)
	case KindIssue:
		return IssueURL(base, s.Repo, s.Number)
	default:
		return ""
	}
}

// numberedURL joins a repository's page with one of its numbered pages.
func numberedURL(base, repo, segment string, n int) string {
	if n <= 0 {
		return ""
	}
	root := rootURL(base, repo)
	if root == "" {
		return ""
	}
	return root + "/" + segment + "/" + strconv.Itoa(n)
}

// rootURL joins the forge's address and a repository's full name into the
// repository's page, tolerating the trailing slash a base URL is often written
// with so the result never carries a doubled one. A repository name is joined as
// it is: "owner/repo" is already a path, and its dots and dashes need no
// escaping.
func rootURL(base, repo string) string {
	base = strings.TrimRight(base, "/")
	if base == "" || repo == "" {
		return ""
	}
	return base + "/" + repo
}
