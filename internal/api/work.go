package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/forge"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/work"
)

// NewWork serves the routes the plugin's work is read through: the Now page's runs, the Work
// page's repositories, one thread on its own, and the two panels — an agent's threads and the
// item an invocation belongs to.
//
// They are plain handlers on their own paths, mounted by internal/server under the manifest's
// api.basePath; the console reaches them through the hub proxy, which is what puts a caller and
// a credential on the request. Every route is a read made as that caller, and answers with what
// that caller's credential lets the hub show.
//
// The bodies are the plugin's own shapes rather than the work types as Go declares them: a
// duration goes out in milliseconds, a cost nobody measured goes out as null, and a list that is
// empty goes out as an empty list rather than a missing one.
func NewWork(svc *WorkService) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/now", func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, func(ctx context.Context) (any, error) {
			since, err := parseSince(r)
			if err != nil {
				return nil, err
			}
			runs, err := svc.activeSince(ctx, since)
			if err != nil {
				return nil, err
			}
			views := make([]runView, 0, len(runs))
			for _, run := range runs {
				views = append(views, nowRunToView(run))
			}
			return nowResponse{Runs: views}, nil
		})
	})

	mux.HandleFunc("GET /api/work", func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, func(ctx context.Context) (any, error) {
			since, err := parseSince(r)
			if err != nil {
				return nil, err
			}
			query := r.URL.Query()
			groups, err := svc.Work(ctx, Filter{
				Repo:  query.Get("repo"),
				Agent: query.Get("agent"),
				Since: since,
			})
			if err != nil {
				return nil, err
			}
			return workResponse{Repos: reposToViews(groups)}, nil
		})
	})

	mux.HandleFunc("GET /api/work/{owner}/{repo}/{number}", func(w http.ResponseWriter, r *http.Request) {
		// Forgejo's pages are addressable by owner and repository separately, and the
		// route is written the way the forge's URLs are so the console can build one
		// from the same two path segments it got the link from.
		repo := r.PathValue("owner") + "/" + r.PathValue("repo")

		serve(w, r, func(ctx context.Context) (any, error) {
			number, err := strconv.Atoi(r.PathValue("number"))
			if err != nil || number <= 0 {
				return nil, badRequest("invalid item number")
			}
			item, err := svc.Item(ctx, repo, number)
			if err != nil {
				return nil, err
			}
			if item == nil {
				return nil, notFound("no such work item")
			}
			return itemResponse{Item: itemToView(*item)}, nil
		})
	})

	mux.HandleFunc("GET /api/agents/{name}/work", func(w http.ResponseWriter, r *http.Request) {
		agent := r.PathValue("name")

		serve(w, r, func(ctx context.Context) (any, error) {
			since, err := parseSince(r)
			if err != nil {
				return nil, err
			}
			groups, err := svc.Work(ctx, Filter{Agent: agent, Since: since})
			if err != nil {
				return nil, err
			}
			items := make([]work.Item, 0)
			for _, g := range groups {
				items = append(items, g.Items...)
			}
			return itemsResponse{Items: itemsToViews(items)}, nil
		})
	})

	mux.HandleFunc("GET /api/invocations/{id}/work", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		serve(w, r, func(ctx context.Context) (any, error) {
			got, err := svc.InvocationWork(ctx, id)
			if err != nil {
				return nil, err
			}
			if got == nil {
				return nil, notFound("no such invocation")
			}
			return invocationResponse{
				Item:              itemToView(got.Item),
				SiblingsCostUSD:   money(got.SiblingsCostUSD, got.SiblingsCostKnown),
				SiblingsCostKnown: got.SiblingsCostKnown,
			}, nil
		})
	})

	return mux
}

// -- the routes' answers ---------------------------------------------------------------

// The bodies these routes answer with. They are the console's contract: what it maps over is
// written here, and nothing that was only ever needed to build the answer travels with it.

type nowResponse struct {
	Runs []runView `json:"runs"`
}

type workResponse struct {
	Repos []repoView `json:"repos"`
}

type itemResponse struct {
	Item itemView `json:"item"`
}

type itemsResponse struct {
	Items []itemView `json:"items"`
}

type invocationResponse struct {
	Item itemView `json:"item"`

	// SiblingsCostUSD is what the item's other runs have cost — null when any of them
	// was never priced.
	SiblingsCostUSD   *float64 `json:"siblingsCostUsd"`
	SiblingsCostKnown bool     `json:"siblingsCostKnown"`
}

// subjectView is the issue or pull request a thread is, in the words the console shows it in.
type subjectView struct {
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Kind   string `json:"kind"`
	Title  string `json:"title,omitempty"`
	Branch string `json:"branch,omitempty"`

	// Body is the text the run was handed: the issue or pull body, or the comment a run
	// was answering. It is the longest thing on this page, and it is what a reader
	// recognises the thread by.
	Body string `json:"body,omitempty"`
}

// runView is one AInsel run as the work list shows it.
type runView struct {
	ID        string     `json:"id"`
	Agent     string     `json:"agent"`
	Role      string     `json:"role"`
	Status    string     `json:"status"`
	Connector string     `json:"connector"`
	Start     time.Time  `json:"start"`
	End       *time.Time `json:"end,omitempty"`

	// DurationMs is the run's finished time in whole milliseconds. A duration that goes
	// out in nanoseconds is a number no reader can show as a duration. A run that has
	// not finished has no finished duration, and no field of one: zero would read as a
	// run that ended the moment it started.
	DurationMs *int64 `json:"durationMs,omitempty"`

	// CostUSD is null whenever CostKnown is false. A cost nobody measured is not zero,
	// and a work page that says $0.00 for a run it never priced tells the admin their
	// agents were free.
	CostUSD   *float64 `json:"costUsd"`
	CostKnown bool     `json:"costKnown"`

	Subject subjectView `json:"subject"`

	// URL is the thread's page on the forge. For a run inside an item it is that item's
	// page, because every run of an item is about that same thread.
	URL string `json:"url"`

	// IssueURL is the issue a run's pull request closes, when the pull says which. The
	// routes that show runs inside items leave it out: those already show the nesting
	// the item's own shape carries.
	IssueURL string `json:"issueUrl,omitempty"`
}

// itemView is one issue or pull request the agents worked on.
type itemView struct {
	Subject subjectView `json:"subject"`
	URL     string      `json:"url"`
	Runs    []runView   `json:"runs"`

	// DurationMs is the item's finished runs' time, added together, in milliseconds. An
	// item still running has the finished part of its work in here and nothing more.
	DurationMs int64 `json:"durationMs"`

	CostUSD      *float64  `json:"costUsd"`
	CostKnown    bool      `json:"costKnown"`
	LastActivity time.Time `json:"lastActivity"`

	// PRs are the pulls filed under this issue, empty on a pull. Never null: the
	// console maps over it, and an empty list is a shorter answer than a missing one.
	PRs []itemView `json:"prs"`
}

// repoView is one repository's work, with its runs' cost counted once each — including the runs
// of the pulls nested under an issue.
type repoView struct {
	Repo      string     `json:"repo"`
	URL       string     `json:"url"`
	Items     []itemView `json:"items"`
	CostUSD   *float64   `json:"costUsd"`
	CostKnown bool       `json:"costKnown"`
}

func reposToViews(groups []work.RepoGroup) []repoView {
	views := make([]repoView, 0, len(groups))
	for _, g := range groups {
		views = append(views, repoView{
			Repo:      g.Repo,
			URL:       g.URL,
			Items:     itemsToViews(g.Items),
			CostUSD:   money(g.CostUSD, g.CostKnown),
			CostKnown: g.CostKnown,
		})
	}
	return views
}

func itemsToViews(items []work.Item) []itemView {
	views := make([]itemView, 0, len(items))
	for _, it := range items {
		views = append(views, itemToView(it))
	}
	return views
}

func itemToView(it work.Item) itemView {
	return itemView{
		Subject:      subjectToView(it.Subject),
		URL:          it.URL,
		Runs:         runsToViews(it.Runs, it.URL),
		DurationMs:   it.Duration.Milliseconds(),
		CostUSD:      money(it.CostUSD, it.CostKnown),
		CostKnown:    it.CostKnown,
		LastActivity: it.LastActivity,
		PRs:          itemsToViews(it.PRs),
	}
}

// runsToViews is an item's runs, each on the item's page.
func runsToViews(runs []work.Run, url string) []runView {
	views := make([]runView, 0, len(runs))
	for _, r := range runs {
		views = append(views, runToView(r, url))
	}
	return views
}

func runToView(r work.Run, url string) runView {
	return runView{
		ID:         r.ID,
		Agent:      r.Agent,
		Role:       r.Role,
		Status:     r.Status,
		Connector:  r.Connector,
		Start:      r.Start,
		End:        r.End,
		DurationMs: finishedMs(r),
		CostUSD:    money(r.CostUSD, r.CostKnown),
		CostKnown:  r.CostKnown,
		Subject:    subjectToView(r.Subject),
		URL:        url,
	}
}

func nowRunToView(n NowRun) runView {
	view := runToView(n.Run, n.URL)
	view.IssueURL = n.IssueURL
	return view
}

func subjectToView(s forge.Subject) subjectView {
	return subjectView{
		Repo:   s.Repo,
		Number: s.Number,
		Kind:   string(s.Kind),
		Title:  s.Title,
		Branch: s.Branch,
		Body:   s.Body,
	}
}

// money is a cost as these bodies carry it: a number when it was measured, and null when it was
// not. A pointer rather than an omitempty field, so the null arrives in the answer rather than
// the field going missing and leaving the console to guess which of the two it was.
func money(usd float64, known bool) *float64 {
	if !known {
		return nil
	}
	return &usd
}

// finishedMs is a run's finished time in milliseconds, or nil for a run that has not finished.
// An end that lands before its start is bad data and reports no duration rather than a negative
// one, which is what work.Build leaves out of a total for the same reason.
func finishedMs(r work.Run) *int64 {
	if r.End == nil {
		return nil
	}
	d := r.End.Sub(r.Start)
	if d < 0 {
		return nil
	}
	ms := d.Milliseconds()
	return &ms
}

// -- the answers every route gives the same way ----------------------------------------

// serve runs one read as the caller the request arrived with, and answers with what it returns.
func serve(w http.ResponseWriter, r *http.Request, read func(context.Context) (any, error)) {
	ctx, ok := callerContext(w, r)
	if !ok {
		return
	}
	out, err := read(ctx)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// callerContext puts the credential the hub forwarded on the request's context, so every hub
// call the read makes is made as the caller who asked, and the hub answers with exactly what
// they may see (ruling R21).
//
// A request that arrived with no credential is refused rather than answered from the plugin's
// own identity: a background read sees every group the chart grants the plugin, and work that
// nobody behind this request asked for has no business arriving in a console.
func callerContext(w http.ResponseWriter, r *http.Request) (context.Context, bool) {
	credential := hub.CredentialFromRequest(r)
	if credential == "" {
		writeError(w, http.StatusUnauthorized, "missing credential")
		return nil, false
	}
	return hub.WithCredential(r.Context(), credential), true
}

// answerError is a request's answer that is a status rather than a body: what went wrong, and
// the one line to say it with. A read returns one instead of writing it, so the gate above and
// the mapping from error to reply stay in one place.
type answerError struct {
	status  int
	message string
}

func (e *answerError) Error() string { return "api: " + e.message }

func badRequest(message string) error {
	return &answerError{status: http.StatusBadRequest, message: message}
}

func notFound(message string) error {
	return &answerError{status: http.StatusNotFound, message: message}
}

// ApplyError is returned when Apply.Run fails: it carries both the error message
// (which names the failing step and service) and the partial report of what was
// created and rolled back.
//
// The report is typed, not an interface{}: the only thing that answers with it reads it
// as setup.ApplyReport, and an assertion there would be a panic waiting for whoever first
// constructs one that does not fit.
type ApplyError struct {
	message string
	report  setup.ApplyReport
}

func (e *ApplyError) Error() string { return "api: apply: " + e.message }

// writeFailure turns what a read came back with into an answer, rendered by
// RenderFailure — the same rendering the MCP tools refuse with.
func writeFailure(w http.ResponseWriter, err error) {
	status, message, report := RenderFailure(err)
	if report != nil {
		// Apply errors return 502 with error message and rollback report
		writeJSON(w, status, struct {
			Error  string            `json:"error"`
			Report setup.ApplyReport `json:"report"`
		}{
			Error:  message,
			Report: *report,
		})
		return
	}
	writeError(w, status, message)
}

// RenderFailure renders what a call came back with as the answer the routes give:
// status is the status the route writes, message the error text its body carries,
// and report is the rollback report — non-nil only for a failed apply, whose body
// carries both. The MCP tools answer with the same message, so a caller refused by
// a route and a caller refused by a tool read the same words.
//
// The three hub answers are the ones a caller has to tell apart: nothing was
// configured, the caller may not see this, the hub did not answer.
func RenderFailure(err error) (status int, message string, report *setup.ApplyReport) {
	var applyErr *ApplyError
	if errors.As(err, &applyErr) {
		return http.StatusBadGateway, applyErr.message, &applyErr.report
	}

	var answer *answerError
	if errors.As(err, &answer) {
		return answer.status, answer.message, nil
	}

	switch {
	case errors.Is(err, ErrHubNotConfigured):
		// A steady state of a plugin run on its own, so it is answered rather than
		// logged: a plugin that logs on every poll is a plugin nobody reads.
		return http.StatusServiceUnavailable, "hub not configured", nil
	case errors.Is(err, errStore):
		// The plugin's own database: neither the hub's answer nor the caller's
		// fault. The text names the query and the directory, never a credential.
		log.Printf("api: %v", err)
		return http.StatusInternalServerError, "install store unavailable", nil
	case errors.Is(err, errSettings):
		// The same answer for the settings table in that same database.
		log.Printf("api: %v", err)
		return http.StatusInternalServerError, "settings store unavailable", nil
	default:
		// Anything else came from the hub. A 403 is the hub working and saying this
		// caller may not see it, which is the caller's answer, not a failure to hide;
		// everything else is a hub the plugin got no reply out of.
		var statusErr *hub.StatusError
		if errors.As(err, &statusErr) && statusErr.Code == http.StatusForbidden {
			log.Printf("api: hub refused the caller (%d)", statusErr.Code)
			return http.StatusForbidden, "forbidden", nil
		}
		if errors.As(err, &statusErr) {
			// The status is the whole of it: a hub error's text names the path, and
			// a request's never carries the credential — but nothing here is worth
			// the risk of echoing a header into a log.
			log.Printf("api: hub returned %d", statusErr.Code)
		} else {
			log.Printf("api: hub did not answer")
		}
		return http.StatusBadGateway, "hub unavailable", nil
	}
}

// writeJSON answers with a body.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status line is already out, so an encode failure leaves the client with a
	// short body; the usual cause is the client having gone.
	_ = json.NewEncoder(w).Encode(body)
}

// writeError answers with the one field a failure has.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{Error: message})
}

// parseSince reads the `since` parameter: a window in days ("7d"), a moment in RFC3339, or
// nothing at all, which is the service's own default.
//
// A value that is neither is refused rather than read as the default: a console that asks for a
// week and is handed a month has been told something it did not ask.
func parseSince(r *http.Request) (time.Time, error) {
	raw := r.URL.Query().Get("since")
	if raw == "" {
		return time.Time{}, nil
	}
	if d, ok := daysBack(raw); ok {
		return time.Now().Add(-d), nil
	}
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, badRequest("invalid since")
	}
	return at, nil
}

// daysBack reads the "<N>d" form: a whole number of days back from now. A negative count is not
// a window into the future, and a non-integer is not a window at all.
func daysBack(raw string) (time.Duration, bool) {
	digits, found := strings.CutSuffix(raw, "d")
	if !found {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 0 {
		return 0, false
	}
	return time.Duration(n) * 24 * time.Hour, true
}
