package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/forge"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
)

// hubRequest is one request the fake hub answered, kept so a test asserts on the call
// the plugin made rather than only on the answer it got.
type hubRequest struct {
	Method string
	Path   string
	Query  url.Values
	Auth   string
	Body   string
}

// hubServer is a hub that answers by route. A test says what each endpoint returns and
// the fake returns that; a request to a route the test never gave is an error, so a
// client that quietly asks a hub for something nobody expected it to ask cannot pass.
type hubServer struct {
	t   *testing.T
	srv *httptest.Server

	mu     sync.Mutex
	routes map[string]func(http.ResponseWriter, *http.Request, int)
	seen   []hubRequest
}

func newHubServer(t *testing.T) *hubServer {
	t.Helper()

	h := &hubServer{t: t, routes: map[string]func(http.ResponseWriter, *http.Request, int){}}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body string
		if raw, err := io.ReadAll(r.Body); err == nil {
			body = string(raw)
		}

		key := r.Method + " " + r.URL.Path
		h.mu.Lock()
		h.seen = append(h.seen, hubRequest{
			Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(),
			Auth: r.Header.Get("Authorization"), Body: body,
		})
		n := 0
		for _, req := range h.seen {
			if req.Method+" "+req.Path == key {
				n++
			}
		}
		route := h.routes[key]
		h.mu.Unlock()

		if route == nil {
			h.t.Errorf("unexpected hub call: %s", key)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		route(w, r, n)
	}))
	t.Cleanup(h.srv.Close)

	// A hub a plugin talks to can mint the plugin's background credential, so the
	// fake answers it by default: a test that means a hub which refuses or is not
	// Ready yet replaces this route.
	h.route(http.MethodPost, credentialsPath, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.WriteHeader(http.StatusOK)
		body := fmt.Sprintf(`{"token":"ainselp_bg","expiresAt":%q}`,
			time.Now().Add(5*time.Minute).Format(time.RFC3339))
		_, _ = w.Write([]byte(body))
	})
	return h
}

// route gives one endpoint an answer, told how many times it has been asked so a route
// can page or hand out a different token each time.
func (h *hubServer) route(method, path string, fn func(http.ResponseWriter, *http.Request, int)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.routes[method+" "+path] = fn
}

// reply answers an endpoint with a fixed status and body.
func (h *hubServer) reply(method, path string, status int, body string) {
	h.route(method, path, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

func (h *hubServer) get(path, body string) {
	h.reply(http.MethodGet, path, http.StatusOK, body)
}

func (h *hubServer) post(path string, status int, body string) {
	h.reply(http.MethodPost, path, status, body)
}

// client builds the client under test against this hub.
func (h *hubServer) client() *HTTPClient {
	return NewHTTPClient(NewTransport(h.srv.URL))
}

// calls returns the requests that arrived for one method and path, in order.
func (h *hubServer) calls(method, path string) []hubRequest {
	h.mu.Lock()
	defer h.mu.Unlock()

	var out []hubRequest
	for _, req := range h.seen {
		if req.Method == method && req.Path == path {
			out = append(out, req)
		}
	}
	return out
}

// writePage answers with one page of the list envelope, for a route whose answer changes
// with how often it has been asked.
func writePage(w http.ResponseWriter, items string, totalPages int) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(pageOf(items, totalPages)))
}

// pageOf is one page of the envelope every paginated hub list answers with, so a test
// writes a list without repeating the envelope's fields.
func pageOf(items string, totalPages int) string {
	return fmt.Sprintf(`{"items":%s,"total":0,"page":1,"pageSize":200,"totalPages":%d}`, items, totalPages)
}

// asOf is the RFC3339 moment a test's "now" stands at, formatted the way the hub formats
// its own expiries.
func asOf(t time.Time, d time.Duration) string {
	return t.Add(d).Format(time.RFC3339)
}

// TestNewHTTPClient checks the convention the rest of the package follows: no transport,
// because there was no HUB_URL, means no client.
func TestNewHTTPClient(t *testing.T) {
	if c := NewHTTPClient(nil); c != nil {
		t.Errorf("NewHTTPClient(nil) = %+v, want nil (hub calls disabled)", c)
	}
}

// TestListPersonasAsCaller checks the common case: a console request arrives with the
// caller's credential, and the hub call the plugin makes from it carries exactly that —
// so the hub answers with what the caller may see and no more.
func TestListPersonasAsCaller(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/personas", pageOf(`[{"id":"p-1","name":"Refiner"},{"id":"p-2","name":"Reviewer"}]`, 1))

	c := h.client()
	refs, err := c.ListPersonas(WithCredential(context.Background(), "ainselp_user"))
	if err != nil {
		t.Fatalf("ListPersonas: %v", err)
	}

	want := []Ref{{ID: "p-1", Name: "Refiner"}, {ID: "p-2", Name: "Reviewer"}}
	if len(refs) != len(want) || refs[0] != want[0] || refs[1] != want[1] {
		t.Errorf("refs = %+v, want %+v", refs, want)
	}

	calls := h.calls(http.MethodGet, "/api/v1/personas")
	if len(calls) != 1 {
		t.Fatalf("hub was called %d times, want 1", len(calls))
	}
	if got, want := calls[0].Auth, "Bearer ainselp_user"; got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
	// The list is asked for at AInsel's largest page, so a hub with more resources
	// than one page holds is still read to the end.
	if got := calls[0].Query.Get("pageSize"); got != "200" {
		t.Errorf("pageSize = %q, want 200", got)
	}
	if got := calls[0].Query.Get("page"); got != "1" {
		t.Errorf("page = %q, want 1", got)
	}
	// The caller's own credential went out, so the plugin's own stayed unminted: a
	// background call made from a user's request would answer with what the plugin may
	// see rather than with what the caller may see.
	if n := len(h.calls(http.MethodPost, credentialsPath)); n != 0 {
		t.Errorf("minted %d background credentials for a call made as a caller, want none", n)
	}
}

// TestBackgroundCredential checks what a plugin does with no user behind it — a webhook,
// a poll, a sync: it mints its own credential once, reuses it while it lasts, and replaces
// it before it expires rather than after.
func TestBackgroundCredential(t *testing.T) {
	fixed := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

	h := newHubServer(t)
	h.route(http.MethodPost, credentialsPath, func(w http.ResponseWriter, r *http.Request, n int) {
		// The mint is answered on the strength of the pod's address. Nothing goes out
		// with it — and nothing comes back into a log.
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("mint sent Authorization %q, want none", auth)
		}
		w.WriteHeader(http.StatusOK)
		body := fmt.Sprintf(`{"token":"ainselp_bg%d","expiresAt":%q}`, n, asOf(fixed, 5*time.Minute))
		_, _ = w.Write([]byte(body))
	})
	h.get("/api/v1/personas", pageOf(`[]`, 1))

	clock := fixed
	c := h.client()
	c.now = func() time.Time { return clock }

	// The marker a webhook handler or poller puts on its context: this call has no
	// user behind it, so the plugin's own credential is the right one to send.
	ctx := WithBackground(context.Background())
	if _, err := c.ListPersonas(ctx); err != nil {
		t.Fatalf("ListPersonas: %v", err)
	}
	if _, err := c.ListPersonas(ctx); err != nil {
		t.Fatalf("ListPersonas: %v", err)
	}

	calls := h.calls(http.MethodGet, "/api/v1/personas")
	if got, want := calls[0].Auth, "Bearer ainselp_bg1"; got != want {
		t.Errorf("first call Authorization = %q, want %q", got, want)
	}
	if got, want := calls[1].Auth, "Bearer ainselp_bg1"; got != want {
		t.Errorf("second call Authorization = %q, want the cached %q", got, want)
	}
	if n := len(h.calls(http.MethodPost, credentialsPath)); n != 1 {
		t.Errorf("minted %d credentials, want 1 (the first is still valid)", n)
	}

	// Inside the thirty-second margin the cached token is replaced rather than used,
	// so no call ever leaves with a credential about to be refused.
	clock = fixed.Add(4*time.Minute + 31*time.Second)
	if _, err := c.ListPersonas(ctx); err != nil {
		t.Fatalf("ListPersonas after the margin: %v", err)
	}

	calls = h.calls(http.MethodGet, "/api/v1/personas")
	if got, want := calls[2].Auth, "Bearer ainselp_bg2"; got != want {
		t.Errorf("third call Authorization = %q, want a freshly minted %q", got, want)
	}
	if n := len(h.calls(http.MethodPost, credentialsPath)); n != 2 {
		t.Errorf("minted %d credentials, want 2", n)
	}
}

// TestBackgroundCredentialRefused checks a hub that will not mint one — the plugin is not
// Ready, the pod's address is not one it verified: the call fails rather than going out
// with no credential and being refused for the wrong reason.
func TestBackgroundCredentialRefused(t *testing.T) {
	h := newHubServer(t)
	h.post(credentialsPath, http.StatusForbidden, `{"error":"forbidden"}`)

	c := h.client()
	_, err := c.ListPersonas(WithBackground(context.Background()))
	if err == nil {
		t.Fatal("ListPersonas succeeded with a hub that refused to mint a credential")
	}
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusForbidden {
		t.Errorf("error = %v, want it to carry the 403", err)
	}
	if len(h.calls(http.MethodGet, "/api/v1/personas")) != 0 {
		t.Error("the list was still asked for after the mint failed")
	}
}

// TestBackgroundCredentialFailureIsTyped is the classification the cutover depends on: a
// credentials endpoint that answers 404 — a hub still coming up after a rollout — is the
// endpoint's 404, not a resource's. It arrives as a *CredentialError carrying it, so a
// caller that reads a bare 404 as "the resource is gone" cannot mistake a mint failure for
// a deletion that finished.
func TestBackgroundCredentialFailureIsTyped(t *testing.T) {
	h := newHubServer(t)
	h.post(credentialsPath, http.StatusNotFound, `{"error":"no such plugin"}`)

	c := h.client()
	_, err := c.ListPersonas(WithBackground(context.Background()))
	if err == nil {
		t.Fatal("ListPersonas succeeded with a hub that has no credentials endpoint")
	}
	var ce *CredentialError
	if !errors.As(err, &ce) {
		t.Fatalf("error = %v, want it to be a *CredentialError", err)
	}
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusNotFound {
		t.Errorf("error = %v, want the 404 still readable off it", err)
	}
}

// TestRunsJoinsInvocationsEventsAndCost checks the assembly the work list is built from:
// the invocation for the run's identity and timing, the event for its subject, the token
// metrics for its cost — and only for the connectors the caller named.
func TestRunsJoinsInvocationsEventsAndCost(t *testing.T) {
	const (
		invocations = `{"invocations":[
			{"id":"inv-1","agentName":"developer","status":"success","connector":"c-1","eventId":"e-1",
			 "startTime":"2026-10-22T10:00:00Z","endTime":"2026-10-22T10:02:00Z"},
			{"id":"inv-2","agentName":"reviewer","status":"success","connector":"c-other","eventId":"e-9",
			 "startTime":"2026-10-22T09:00:00Z","endTime":"2026-10-22T09:01:00Z"},
			{"id":"inv-3","agentName":"refiner","status":"running","connector":"c-1","eventId":"e-2",
			 "startTime":"2026-10-22T11:00:00Z"},
			{"id":"inv-4","agentName":"refiner","status":"success","connector":"c-1","eventId":"e-1",
			 "startTime":"2026-10-22T10:30:00Z","endTime":"2026-10-22T10:31:00Z"},
			{"id":"inv-5","agentName":"developer","status":"queued","connector":"c-1",
			 "startTime":"2026-10-22T11:30:00Z"}
		],"total":5,"page":1,"pageSize":200,"totalPages":1}`

		eventE1 = `{"id":"e-1","timestamp":"2026-10-22T10:00:00Z","connector":"c-1","status":"matched",
			"payload":{"repository":{"full_name":"acme/apps"},
				"issue":{"number":7,"title":"Add export","body":"please"}}}`

		// e-2 is past retention: the hub has the invocation and no longer has the event.
		eventE2 = `{"error":"event not found"}`

		costs = `{"range":"7d","rows":[
			{"event":"e-1","costUsd":0.25,"costReported":true},
			{"event":"e-1-nope","costUsd":9,"costReported":false}
		],"updatedAt":"2026-10-22T12:00:00Z"}`
	)

	h := newHubServer(t)
	h.get("/api/v1/invocations", invocations)
	h.get("/api/v1/events/e-1", eventE1)
	h.reply(http.MethodGet, "/api/v1/events/e-2", http.StatusNotFound, eventE2)
	h.get("/api/v1/observability/metrics/tokens/by-event", costs)

	c := h.client()
	since := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	runs, err := c.Runs(WithCredential(context.Background(), "ainselp_user"), RunsQuery{
		Connectors: []string{"c-1"}, Agents: []string{"a-1"}, Since: since, Costs: true,
	})
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}

	// inv-2 belongs to another connector, and so is not this forge's work.
	if len(runs) != 4 {
		t.Fatalf("got %d runs, want 4 (the c-1 ones): %+v", len(runs), runs)
	}

	first := runs[0]
	if first.ID != "inv-1" || first.Agent != "developer" || first.Status != "success" || first.Connector != "c-1" {
		t.Errorf("run 0 = %+v, want the inv-1 identity", first)
	}
	if want := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC); !first.Start.Equal(want) {
		t.Errorf("run 0 start = %s, want %s", first.Start, want)
	}
	if first.End == nil || !first.End.Equal(time.Date(2026, 10, 22, 10, 2, 0, 0, time.UTC)) {
		t.Errorf("run 0 end = %v, want 10:02", first.End)
	}
	if first.Subject.Repo != "acme/apps" || first.Subject.Number != 7 || first.Subject.Kind != forge.KindIssue {
		t.Errorf("run 0 subject = %+v, want issue acme/apps#7", first.Subject)
	}
	if first.Subject.Title != "Add export" {
		t.Errorf("run 0 title = %q, want %q", first.Subject.Title, "Add export")
	}
	if first.CostUSD != 0.25 || !first.CostKnown {
		t.Errorf("run 0 cost = %v known=%v, want 0.25 known", first.CostUSD, first.CostKnown)
	}
	// The persona a run ran as is not on the invocation, and the plugin does not
	// invent one.
	if first.Role != "" {
		t.Errorf("run 0 role = %q, want empty", first.Role)
	}

	// Past retention: the hub has no payload for e-2, so nothing here can say what the
	// run was about — and there is no cost to claim for it either.
	if runs[1].ID != "inv-3" || runs[1].Subject.Repo != "" || runs[1].Subject.Number != 0 || runs[1].CostKnown {
		t.Errorf("run 1 = %+v, want a subject-less, cost-less run for an expired event", runs[1])
	}

	third := runs[2]
	if third.ID != "inv-4" {
		t.Fatalf("run 2 = %+v, want inv-4", third)
	}
	if third.CostUSD != 0.25 || !third.CostKnown {
		t.Errorf("run 2 cost = %v known=%v, want the same event's 0.25 known", third.CostUSD, third.CostKnown)
	}
	if len(h.calls(http.MethodGet, "/api/v1/events/e-1")) != 1 {
		t.Error("e-1 was read twice; one event is one read, however many runs it started")
	}

	// inv-5 has no event at all: nothing about it, and no cost to claim.
	last := runs[3]
	if last.ID != "inv-5" || last.Subject.Repo != "" || last.CostKnown {
		t.Errorf("run 3 = %+v, want a subject-less, cost-less queued run", last)
	}
	if last.End != nil {
		t.Errorf("run 3 end = %v, want nil while the run has not finished", last.End)
	}

	// The window is asked for as the hub takes it, and the whole page is asked for.
	calls := h.calls(http.MethodGet, "/api/v1/invocations")
	if got, want := calls[0].Query.Get("since"), "2026-10-21T00:00:00Z"; got != want {
		t.Errorf("since = %q, want %q", got, want)
	}
	if got := calls[0].Query.Get("pageSize"); got != "200" {
		t.Errorf("pageSize = %q, want 200", got)
	}
	if got, want := calls[0].Auth, "Bearer ainselp_user"; got != want {
		t.Errorf("Authorization = %q, want the caller's credential", got)
	}
	if got, want := h.calls(http.MethodGet, "/api/v1/observability/metrics/tokens/by-event")[0].Query.Get("range"), "7d"; got != want {
		t.Errorf("cost range = %q, want %q (the widest the hub accepts)", got, want)
	}
}

// TestRunsUnknownCostAndMissingSubject covers the two ways a run comes back incomplete:
// an event whose cost nobody measured, and — checked above — an event that is gone.
func TestRunsUnknownCostAndMissingSubject(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/invocations", `{"invocations":[
		{"id":"inv-1","agentName":"developer","status":"success","connector":"c-1","eventId":"e-3",
		 "startTime":"2026-10-22T10:00:00Z"}
	],"total":1,"page":1,"pageSize":200,"totalPages":1}`)
	h.get("/api/v1/events/e-3", `{"id":"e-3","timestamp":"2026-10-22T10:00:00Z","status":"matched",
		"payload":{"repository":{"full_name":"o/r"},"pull_request":{"number":4,"title":"Ship","head":{"ref":"feat"}}}}`)
	// The metrics endpoint is not ready — no log store yet. The work is still work.
	h.reply(http.MethodGet, "/api/v1/observability/metrics/tokens/by-event", http.StatusServiceUnavailable, `{}`)

	c := h.client()
	runs, err := c.Runs(WithBackground(context.Background()), RunsQuery{Connectors: []string{"c-1"}, Agents: []string{"a-1"}, Since: time.Unix(0, 0), Costs: true})
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}

	run := runs[0]
	if run.Subject.Kind != forge.KindPull || run.Subject.Number != 4 || run.Subject.Branch != "feat" {
		t.Errorf("subject = %+v, want pull o/r#4 on branch feat", run.Subject)
	}
	if run.CostUSD != 0 || run.CostKnown {
		t.Errorf("cost = %v known=%v, want unknown rather than a zero that reads as free", run.CostUSD, run.CostKnown)
	}
}

// TestRunsNoConnectorIDs checks that naming no connector asks the hub nothing: no forge
// is named, so there is no work to list, and reading every invocation to find that out
// would be both slow and someone else's business.
func TestRunsNoConnectorIDs(t *testing.T) {
	h := newHubServer(t)

	c := h.client()
	runs, err := c.Runs(context.Background(), RunsQuery{Agents: []string{"a-1"}, Since: time.Now().Add(-24 * time.Hour), Costs: true})
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("runs = %+v, want none", runs)
	}
	if len(h.seen) != 0 {
		t.Errorf("hub was called %d times, want no call at all", len(h.seen))
	}
}

// TestRunsPagesThroughInvocations checks a week of work that does not fit on one page:
// the answer is every page of it, not the first.
func TestRunsPagesThroughInvocations(t *testing.T) {
	h := newHubServer(t)
	h.route(http.MethodGet, "/api/v1/invocations", func(w http.ResponseWriter, _ *http.Request, n int) {
		w.WriteHeader(http.StatusOK)
		if n == 1 {
			_, _ = w.Write([]byte(`{"invocations":[{"id":"inv-1","connector":"c-1","eventId":"e-1","status":"success","startTime":"2026-10-22T10:00:00Z"}],"total":2,"page":1,"pageSize":200,"totalPages":2}`))
			return
		}
		_, _ = w.Write([]byte(`{"invocations":[{"id":"inv-2","connector":"c-1","eventId":"e-1","status":"success","startTime":"2026-10-22T11:00:00Z"}],"total":2,"page":2,"pageSize":200,"totalPages":2}`))
	})
	h.get("/api/v1/events/e-1", `{"id":"e-1","payload":{"repository":{"full_name":"o/r"},"issue":{"number":1,"title":"T"}}}`)
	h.get("/api/v1/observability/metrics/tokens/by-event", `{"range":"7d","rows":[]}`)

	c := h.client()
	runs, err := c.Runs(WithBackground(context.Background()), RunsQuery{Connectors: []string{"c-1"}, Agents: []string{"a-1"}, Since: time.Unix(0, 0), Costs: true})
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 2 || runs[0].ID != "inv-1" || runs[1].ID != "inv-2" {
		t.Fatalf("runs = %+v, want both pages' invocations", runs)
	}

	calls := h.calls(http.MethodGet, "/api/v1/invocations")
	if len(calls) != 2 {
		t.Fatalf("asked for %d pages, want 2", len(calls))
	}
	if got := calls[1].Query.Get("page"); got != "2" {
		t.Errorf("second page param = %q, want 2", got)
	}
}

// writeInvocations answers an invocation page with the items given, as one full page.
func writeInvocations(w http.ResponseWriter, items ...string) {
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `{"invocations":[%s],"total":%d,"page":1,"pageSize":200,"totalPages":1}`,
		strings.Join(items, ","), len(items))
}

// TestRunsReadsOnlyTheNamedAgents checks the read scope the work pages ask for: one
// invocation read per agent the caller named, narrowed by the hub's own `agent=` filter,
// rather than every agent's invocations read and most of them thrown away. An invocation the
// hub hands back under both agents is still one run.
func TestRunsReadsOnlyTheNamedAgents(t *testing.T) {
	h := newHubServer(t)
	h.route(http.MethodGet, "/api/v1/invocations", func(w http.ResponseWriter, r *http.Request, _ int) {
		switch agent := r.URL.Query().Get("agent"); agent {
		case "a-1":
			writeInvocations(w,
				`{"id":"inv-1","agentName":"refiner","connector":"c-1","status":"success","startTime":"2026-10-22T10:00:00Z"}`,
				`{"id":"inv-shared","agentName":"developer","connector":"c-1","status":"success","startTime":"2026-10-22T10:05:00Z"}`)
		case "a-2":
			writeInvocations(w,
				`{"id":"inv-2","agentName":"reviewer","connector":"c-1","status":"success","startTime":"2026-10-22T11:00:00Z"}`,
				`{"id":"inv-shared","agentName":"developer","connector":"c-1","status":"success","startTime":"2026-10-22T10:05:00Z"}`)
		default:
			t.Errorf("invocations were read with agent=%q, want one of the named agents", agent)
			writeInvocations(w)
		}
	})
	h.get("/api/v1/observability/metrics/tokens/by-event", `{"range":"7d","rows":[]}`)

	c := h.client()
	runs, err := c.Runs(WithBackground(context.Background()), RunsQuery{
		Connectors: []string{"c-1"}, Agents: []string{"a-1", "a-2"}, Since: time.Unix(0, 0), Costs: true,
	})
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}

	seen := map[string]int{}
	for _, run := range runs {
		seen[run.ID]++
	}
	if len(runs) != 3 || seen["inv-1"] != 1 || seen["inv-shared"] != 1 || seen["inv-2"] != 1 {
		t.Fatalf("runs = %+v, want each of inv-1, inv-shared and inv-2 exactly once", runs)
	}

	calls := h.calls(http.MethodGet, "/api/v1/invocations")
	if len(calls) != 2 {
		t.Fatalf("invocations were read %d times, want once per named agent", len(calls))
	}
	for i, call := range calls {
		if got := call.Query.Get("agent"); got != "a-1" && got != "a-2" {
			t.Errorf("read %d asked with agent=%q, want a-1 or a-2", i+1, got)
		}
		if got := call.Query.Get("since"); got != time.Unix(0, 0).UTC().Format(time.RFC3339) {
			t.Errorf("read %d asked with since=%q, want the window asked for", i+1, got)
		}
	}
	if calls[0].Query.Get("agent") == calls[1].Query.Get("agent") {
		t.Errorf("both reads asked for the same agent, want one read each: %q, %q",
			calls[0].Query.Get("agent"), calls[1].Query.Get("agent"))
	}
}

// TestRunsWithoutAgentsAsksNothing is the other half of the scope: an agent set that names
// nothing reads nothing. There is no agent whose work this plugin was set up to show, and
// reading every invocation on the hub to find that out is both slow and someone else's
// business.
func TestRunsWithoutAgentsAsksNothing(t *testing.T) {
	h := newHubServer(t)

	c := h.client()
	runs, err := c.Runs(WithBackground(context.Background()), RunsQuery{
		Connectors: []string{"c-1"}, Since: time.Unix(0, 0), Costs: true,
	})
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("runs = %+v, want none", runs)
	}
	if len(h.seen) != 0 {
		t.Errorf("hub was called %d times, want no call at all", len(h.seen))
	}
}

// TestRunsWithoutCostsSkipsTheCostEndpoint checks the now page's read: a run that has not
// finished has no cost to show, so the metrics endpoint is not asked for at all and every run
// comes back with its cost unknown rather than free.
func TestRunsWithoutCostsSkipsTheCostEndpoint(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/invocations", `{"invocations":[
		{"id":"inv-1","agentName":"developer","status":"running","connector":"c-1","eventId":"e-1",
		 "startTime":"2026-10-22T10:00:00Z"}
	],"total":1,"page":1,"pageSize":200,"totalPages":1}`)
	h.get("/api/v1/events/e-1", `{"id":"e-1","payload":{"repository":{"full_name":"o/r"},"issue":{"number":1,"title":"T"}}}`)
	h.get("/api/v1/observability/metrics/tokens/by-event", `{"range":"7d","rows":[
		{"event":"e-1","costUsd":1.25,"costReported":true}
	]}`)

	c := h.client()
	runs, err := c.Runs(WithBackground(context.Background()), RunsQuery{
		Connectors: []string{"c-1"}, Agents: []string{"a-1"}, Since: time.Unix(0, 0),
	})
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	if n := len(h.calls(http.MethodGet, "/api/v1/observability/metrics/tokens/by-event")); n != 0 {
		t.Errorf("the cost endpoint was read %d times, want 0 (the query asked for no costs)", n)
	}
	if runs[0].CostKnown || runs[0].CostUSD != 0 {
		t.Errorf("run cost = %v known=%v, want unknown when no costs were asked for", runs[0].CostUSD, runs[0].CostKnown)
	}
	// The subject is still read: the now page links each run to the page it is about.
	if runs[0].Subject.Number != 1 || runs[0].Subject.Repo != "o/r" {
		t.Errorf("subject = %+v, want issue o/r#1 read without the costs", runs[0].Subject)
	}
}

// TestRunsReadsEventsConcurrently checks the fan-out the pages wait on: ten events read one
// after another at a tenth of a second each is a second of waiting for one page, read in
// parallel they arrive in a few hundred milliseconds.
func TestRunsReadsEventsConcurrently(t *testing.T) {
	const events = 10

	var items []string
	for i := 1; i <= events; i++ {
		items = append(items, fmt.Sprintf(
			`{"id":"inv-%d","agentName":"developer","status":"success","connector":"c-1","eventId":"e-%d","startTime":"2026-10-22T10:00:00Z"}`, i, i))
	}

	h := newHubServer(t)
	h.route(http.MethodGet, "/api/v1/invocations", func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeInvocations(w, items...)
	})
	h.get("/api/v1/observability/metrics/tokens/by-event", `{"range":"7d","rows":[]}`)
	for i := 1; i <= events; i++ {
		path := fmt.Sprintf("/api/v1/events/e-%d", i)
		h.route(http.MethodGet, path, func(w http.ResponseWriter, _ *http.Request, _ int) {
			// One slow hub: each payload takes a tenth of a second to read.
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"e-1","payload":{"repository":{"full_name":"o/r"},"issue":{"number":1,"title":"T"}}}`))
		})
	}

	c := h.client()
	started := time.Now()
	runs, err := c.Runs(WithBackground(context.Background()), RunsQuery{
		Connectors: []string{"c-1"}, Agents: []string{"a-1"}, Since: time.Unix(0, 0), Costs: true,
	})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != events {
		t.Fatalf("got %d runs, want %d", len(runs), events)
	}
	for i, run := range runs {
		if run.Subject.Number != 1 {
			t.Fatalf("run %d subject = %+v, want the event's issue", i, run.Subject)
		}
	}
	// Ten events at 100 ms each is a second read one after another; bound generously so a
	// loaded machine is not the failure.
	if elapsed >= 500*time.Millisecond {
		t.Errorf("reading %d events took %s, want well under 500ms (they are read in parallel)", events, elapsed)
	}
}

// TestRunsTreatsAPendingTaskAsActive checks a run the hub is still retrying: the latest
// invocation has already failed, but the task behind it is pending and running, so the run
// is still work rather than a failure the Active page drops.
func TestRunsTreatsAPendingTaskAsActive(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/invocations", `{"invocations":[
		{"id":"inv-af0e5292","agentName":"developer","status":"failure","connector":"c-1",
		 "task":{"status":"pending","runState":"running"},"startTime":"2026-10-22T10:00:00Z"}
	],"total":1,"page":1,"pageSize":200,"totalPages":1}`)

	c := h.client()
	runs, err := c.Runs(WithBackground(context.Background()), RunsQuery{Connectors: []string{"c-1"}, Agents: []string{"a-1"}, Since: time.Unix(0, 0)})
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	if runs[0].Status != "running" {
		t.Errorf("run status = %q, want running while the hub still retries the task", runs[0].Status)
	}
}

// TestRunsTreatsAClaimedTaskAsActive checks a task a worker has claimed: the hub's task status
// is claimed rather than pending, and the run is still in progress.
func TestRunsTreatsAClaimedTaskAsActive(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/invocations", `{"invocations":[
		{"id":"inv-af0e5292","agentName":"developer","status":"failure","connector":"c-1",
		 "task":{"status":"claimed","runState":"running"},"startTime":"2026-10-22T10:00:00Z"}
	],"total":1,"page":1,"pageSize":200,"totalPages":1}`)

	runs, err := h.client().Runs(WithBackground(context.Background()), RunsQuery{Connectors: []string{"c-1"}, Agents: []string{"developer"}, Since: time.Unix(0, 0)})
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != "running" {
		t.Errorf("runs = %+v, want one running run while a worker holds the task", runs)
	}
}

// TestRunsTreatsAQueuedTaskAsQueued checks the same invocation read while the hub has the
// task queued: the run is queued, not the failure of the attempt before it.
func TestRunsTreatsAQueuedTaskAsQueued(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/invocations", `{"invocations":[
		{"id":"inv-af0e5292","agentName":"developer","status":"failure","connector":"c-1",
		 "task":{"status":"pending","runState":"queued"},"startTime":"2026-10-22T10:00:00Z"}
	],"total":1,"page":1,"pageSize":200,"totalPages":1}`)

	c := h.client()
	runs, err := c.Runs(WithBackground(context.Background()), RunsQuery{Connectors: []string{"c-1"}, Agents: []string{"a-1"}, Since: time.Unix(0, 0)})
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	if runs[0].Status != "queued" {
		t.Errorf("run status = %q, want queued while the task waits for an attempt", runs[0].Status)
	}
}

// TestRunsKeepsAFinishedTasksStatus checks the runs that are done keep the status the hub
// gives them: a completed task and an invocation with no task at all are both finished work.
func TestRunsKeepsAFinishedTasksStatus(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/invocations", `{"invocations":[
		{"id":"inv-1","agentName":"developer","status":"failure","connector":"c-1",
		 "task":{"status":"completed","runState":"failure"},"startTime":"2026-10-22T10:00:00Z"},
		{"id":"inv-2","agentName":"reviewer","status":"success","connector":"c-1",
		 "startTime":"2026-10-22T10:10:00Z"}
	],"total":2,"page":1,"pageSize":200,"totalPages":1}`)

	c := h.client()
	runs, err := c.Runs(WithBackground(context.Background()), RunsQuery{Connectors: []string{"c-1"}, Agents: []string{"a-1"}, Since: time.Unix(0, 0)})
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2", len(runs))
	}
	if runs[0].ID != "inv-1" || runs[0].Status != "failure" {
		t.Errorf("run 0 = %+v, want inv-1 keeping its failure", runs[0])
	}
	if runs[1].ID != "inv-2" || runs[1].Status != "success" {
		t.Errorf("run 1 = %+v, want inv-2 keeping its success", runs[1])
	}
}

// TestCreateConnectorSendsTheForgejoSignatureHeader checks the one thing that makes a
// connector this plugin's: a connector has no type, so the signature header is what says
// Forgejo posts to it — and the endpoint and the secret the hub answers with get back to
// the admin, who is the only place either of them belongs.
func TestCreateConnectorSendsTheForgejoSignatureHeader(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/connectors", http.StatusCreated, `{"id":"c-42","name":"apps","signatureHeader":"X-Forgejo-Signature",
		"webhookEndpoint":"https://hub.example/webhooks/c-42","webhookSecretValue":"webhook-secret-value"}`)

	c := h.client()
	conn, err := c.CreateConnector(WithCredential(context.Background(), "ainselp_user"), "g-1", "apps")
	if err != nil {
		t.Fatalf("CreateConnector: %v", err)
	}

	if conn.ID != "c-42" || conn.Name != "apps" {
		t.Errorf("connector = %+v, want c-42/apps", conn)
	}
	if conn.WebhookEndpoint != "https://hub.example/webhooks/c-42" {
		t.Errorf("endpoint = %q, want the hub's", conn.WebhookEndpoint)
	}
	if conn.WebhookSecretValue != "webhook-secret-value" {
		t.Errorf("secret = %q, want the hub's plaintext", conn.WebhookSecretValue)
	}

	calls := h.calls(http.MethodPost, "/api/v1/connectors")
	if len(calls) != 1 {
		t.Fatalf("created %d connectors, want 1", len(calls))
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(calls[0].Body), &body); err != nil {
		t.Fatalf("body is not JSON: %s", calls[0].Body)
	}
	if got, want := body["signatureHeader"], "X-Forgejo-Signature"; got != want {
		t.Errorf("signatureHeader = %v, want %v", got, want)
	}
	if got, want := body["groupId"], "g-1"; got != want {
		t.Errorf("groupId = %v, want %v (every create is filed under a group)", got, want)
	}
	if got, want := body["name"], "apps"; got != want {
		t.Errorf("name = %v, want %v", got, want)
	}
}

// TestListMCPServersReadsBareArray checks the collection that has no envelope: the MCP
// registry answers with the array itself.
func TestListMCPServersReadsBareArray(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/mcp-servers", `[{"name":"forgejo","displayName":"Forgejo","url":"https://forgejo/api/mcp"},
		{"name":"sentry","displayName":"Sentry","url":"https://sentry/mcp"}]`)

	c := h.client()
	refs, err := c.ListMCPServers(WithBackground(context.Background()))
	if err != nil {
		t.Fatalf("ListMCPServers: %v", err)
	}

	want := []MCPServer{
		{Name: "forgejo", DisplayName: "Forgejo", URL: "https://forgejo/api/mcp"},
		{Name: "sentry", DisplayName: "Sentry", URL: "https://sentry/mcp"},
	}
	if len(refs) != 2 || refs[0] != want[0] || refs[1] != want[1] {
		t.Errorf("refs = %+v, want %+v (an MCP server is keyed by its name, and the URL is "+
			"what says which one it is)", refs, want)
	}
	// No page params: this list is not paginated, and asking for pages of it would
	// only confuse the hub.
	if got := h.calls(http.MethodGet, "/api/v1/mcp-servers")[0].Query.Get("page"); got != "" {
		t.Errorf("page = %q, want no pagination on a bare array", got)
	}
}

// TestRegisterMCPServer checks the shape the registry takes: the server's URL and the
// name of the env var that holds its token — never a token in the body.
func TestRegisterMCPServer(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/mcp-servers", http.StatusCreated, `{"name":"forgejo","displayName":"Forgejo","url":"https://forgejo/api/mcp","tokenFromEnv":"FORGEJO_TOKEN"}`)

	c := h.client()
	ref, err := c.RegisterMCPServer(WithBackground(context.Background()), MCPServerSpec{
		Name: "forgejo", GroupID: "g-1", DisplayName: "Forgejo",
		Description: "The forge's own tools", URL: "https://forgejo/api/mcp",
		TokenFromEnv: "FORGEJO_TOKEN",
	})
	if err != nil {
		t.Fatalf("RegisterMCPServer: %v", err)
	}
	if ref.ID != "forgejo" || ref.Name != "forgejo" {
		t.Errorf("ref = %+v, want forgejo/forgejo", ref)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(h.calls(http.MethodPost, "/api/v1/mcp-servers")[0].Body), &body); err != nil {
		t.Fatalf("body is not JSON: %s", h.calls(http.MethodPost, "/api/v1/mcp-servers")[0].Body)
	}
	if got, want := body["tokenFromEnv"], "FORGEJO_TOKEN"; got != want {
		t.Errorf("tokenFromEnv = %v, want %v", got, want)
	}
	if got, want := body["groupId"], "g-1"; got != want {
		t.Errorf("groupId = %v, want %v", got, want)
	}
}

// TestListAgentImagesNamesImagesByDisplayName checks the one list whose Name is not the
// item's name: an image is offered to an admin by its display name and created on by its
// registry id.
func TestListAgentImagesNamesImagesByDisplayName(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/agent-images", pageOf(`[{"id":"ai-1","displayName":"Claude Code"},{"id":"ai-2","displayName":"OpenCode"}]`, 1))

	c := h.client()
	refs, err := c.ListAgentImages(WithBackground(context.Background()))
	if err != nil {
		t.Fatalf("ListAgentImages: %v", err)
	}

	want := []Ref{{ID: "ai-1", Name: "Claude Code"}, {ID: "ai-2", Name: "OpenCode"}}
	if len(refs) != 2 || refs[0] != want[0] || refs[1] != want[1] {
		t.Errorf("refs = %+v, want %+v", refs, want)
	}
}

// TestListPagesToTheEnd checks that a collection bigger than a page is read to the end:
// a wizard that saw only the first 200 triggers would tell an admin their install has
// fewer than it does.
func TestListPagesToTheEnd(t *testing.T) {
	h := newHubServer(t)
	h.route(http.MethodGet, "/api/v1/personas", func(w http.ResponseWriter, _ *http.Request, n int) {
		w.WriteHeader(http.StatusOK)
		if n == 1 {
			writePage(w, `[{"id":"p-1","name":"one"}]`, 3)
			return
		}
		if n == 2 {
			writePage(w, `[{"id":"p-2","name":"two"}]`, 3)
			return
		}
		writePage(w, `[{"id":"p-3","name":"three"}]`, 3)
	})

	c := h.client()
	refs, err := c.ListPersonas(WithBackground(context.Background()))
	if err != nil {
		t.Fatalf("ListPersonas: %v", err)
	}
	if len(refs) != 3 || refs[2].ID != "p-3" {
		t.Fatalf("refs = %+v, want all three pages", refs)
	}

	calls := h.calls(http.MethodGet, "/api/v1/personas")
	if len(calls) != 3 {
		t.Fatalf("read %d pages, want 3", len(calls))
	}
	for i, want := range []string{"1", "2", "3"} {
		if got := calls[i].Query.Get("page"); got != want {
			t.Errorf("page %d param = %q, want %q", i+1, got, want)
		}
	}
}

// TestCreateAgentBody checks the nested request AInsel's create takes: the image by
// registry id, the persona by id, the MCP servers by name, the bot token as a secret env
// entry (ruling R13) — and what an absent selection leaves out.
func TestCreateAgentBody(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/agents", http.StatusCreated, `{"id":"a-9","name":"apps-developer"}`)

	c := h.client()
	ref, err := c.CreateAgent(WithBackground(context.Background()), AgentSpec{
		Name:        "apps-developer",
		GroupID:     "g-1",
		Description: "Works the issues",
		ImageRef:    "ai-1",
		PersonaID:   "p-2",
		MCPServers:  []string{"forgejo", "plugin-forgejo"},
		Skills:      []string{"forgejo"},
		Env:         []EnvVar{{Name: "FORGEJO_TOKEN", Value: "bot-token", Secret: true}},
		LLM:         &LLMSpec{Model: "claude-sonnet-4.5"},
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if ref.ID != "a-9" || ref.Name != "apps-developer" {
		t.Errorf("ref = %+v, want a-9/apps-developer (a trigger's agentRef is the name)", ref)
	}

	var body map[string]any
	raw := h.calls(http.MethodPost, "/api/v1/agents")[0].Body
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("body is not JSON: %s", raw)
	}
	if got, want := body["groupId"], "g-1"; got != want {
		t.Errorf("groupId = %v, want %v", got, want)
	}
	if img, ok := body["imageRef"].(map[string]any); !ok || img["name"] != "ai-1" {
		t.Errorf("imageRef = %v, want {name: ai-1}", body["imageRef"])
	}
	if persona, ok := body["persona"].(map[string]any); !ok || persona["id"] != "p-2" {
		t.Errorf("persona = %v, want {id: p-2}", body["persona"])
	}
	if mcp, ok := body["mcp"].(map[string]any); !ok {
		t.Errorf("mcp = %v, want {servers: [...]}", body["mcp"])
	} else if got, want := fmt.Sprint(mcp["servers"]), "[forgejo plugin-forgejo]"; got != want {
		t.Errorf("mcp.servers = %s, want %s", got, want)
	}
	if got, want := fmt.Sprint(body["skills"]), "[forgejo]"; got != want {
		t.Errorf("skills = %v, want a plain list of skill ids", body["skills"])
	}
	env, ok := body["env"].([]any)
	if !ok || len(env) != 1 {
		t.Fatalf("env = %v, want one entry", body["env"])
	}
	if entry, ok := env[0].(map[string]any); !ok || entry["name"] != "FORGEJO_TOKEN" ||
		entry["value"] != "bot-token" || entry["secret"] != true {
		t.Errorf("env[0] = %v, want the bot token marked secret", env[0])
	}
	if llm, ok := body["llm"].(map[string]any); !ok || llm["model"] != "claude-sonnet-4.5" {
		t.Errorf("llm = %v, want {model: claude-sonnet-4.5}", body["llm"])
	}
	// An empty provider is left out rather than sent as "", which is the hub's
	// "choose for me" rather than a chosen empty provider.
	if _, ok := llmOf(body)["provider"]; ok {
		t.Errorf("llm = %v, want no provider key when the spec names none", body["llm"])
	}
}

// TestCreateAgentBodyNonSecretEnvEntry checks that a non-secret env entry — the
// developer image's shipped TURN_TIMEOUT_MS default (issue #20) — is posted as it
// stands with "secret": false, and is not dropped next to a secret entry.
func TestCreateAgentBodyNonSecretEnvEntry(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/agents", http.StatusCreated, `{"id":"a-9","name":"apps-developer"}`)

	c := h.client()
	_, err := c.CreateAgent(WithBackground(context.Background()), AgentSpec{
		Name:     "apps-developer",
		GroupID:  "g-1",
		ImageRef: "ai-1",
		Env: []EnvVar{
			{Name: "FORGEJO_TOKEN", Value: "bot-token", Secret: true},
			{Name: "TURN_TIMEOUT_MS", Value: "1800000"},
		},
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	var body map[string]any
	raw := h.calls(http.MethodPost, "/api/v1/agents")[0].Body
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("body is not JSON: %s", raw)
	}
	env, ok := body["env"].([]any)
	if !ok || len(env) != 2 {
		t.Fatalf("env = %v, want two entries", body["env"])
	}
	entry, ok := env[1].(map[string]any)
	if !ok || entry["name"] != "TURN_TIMEOUT_MS" || entry["value"] != "1800000" {
		t.Fatalf("env[1] = %v, want the turn timeout entry", env[1])
	}
	if secret, present := entry["secret"]; !present || secret != false {
		t.Errorf("env[1].secret = %v (present %v), want explicit false", secret, present)
	}
}

// llmOf is the llm object of a posted agent body, for the assertions that read inside it.
func llmOf(body map[string]any) map[string]any {
	llm, _ := body["llm"].(map[string]any)
	return llm
}

// TestCreateAgentLeavesUnsetSelectionsOut checks the other side of the hub's
// absent-versus-empty distinction. An unset MCP selection stays absent so the image's
// holds; an unset skills list stays absent too, but it means something else there — skills
// belong to the agent and inherit nothing, so absent is "no skills". An empty list is the
// same answer as no list, so neither reaches the wire.
func TestCreateAgentLeavesUnsetSelectionsOut(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/agents", http.StatusCreated, `{"id":"a-1","name":"a"}`)

	c := h.client()
	if _, err := c.CreateAgent(WithBackground(context.Background()), AgentSpec{
		Name: "a", GroupID: "g-1", ImageRef: "ai-1",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := c.CreateAgent(WithBackground(context.Background()), AgentSpec{
		Name: "a", GroupID: "g-1", ImageRef: "ai-1", Skills: []string{},
	}); err != nil {
		t.Fatalf("CreateAgent with an empty skills list: %v", err)
	}

	for i, call := range h.calls(http.MethodPost, "/api/v1/agents") {
		var body map[string]any
		if err := json.Unmarshal([]byte(call.Body), &body); err != nil {
			t.Fatalf("body %d is not JSON: %s", i, call.Body)
		}
		for _, key := range []string{"mcp", "skills", "persona", "llm", "env"} {
			if _, ok := body[key]; ok {
				t.Errorf("body %d carries %q: %s — an unset selection has to stay absent", i, key, call.Body)
			}
		}
	}
}

// TestCreateAgentSendsSkillsAsAPlainList is the wire-shape guard for the hub moving skill
// selection onto the agent: "skills" is a plain JSON array of skill ids, not the old
// {items: [...]} wrapper. The assertion is on the raw body rather than on the plugin's own
// structs, because a wrapper both sides' structs agree on is exactly what this catches —
// the hub answers 400, the wizard aborts, and the log said nothing but "hub returned 400".
func TestCreateAgentSendsSkillsAsAPlainList(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/agents", http.StatusCreated, `{"id":"a-1","name":"a"}`)

	c := h.client()
	if _, err := c.CreateAgent(WithBackground(context.Background()), AgentSpec{
		Name:     "a",
		GroupID:  "g-1",
		ImageRef: "ai-1",
		Skills:   []string{"forgejo-forgejo"},
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	raw := h.calls(http.MethodPost, "/api/v1/agents")[0].Body
	if !strings.Contains(raw, `"skills":["forgejo-forgejo"]`) {
		t.Errorf("body = %s, want \"skills\":[\"forgejo-forgejo\"]", raw)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("body is not JSON: %s", raw)
	}
	if got, want := body["skills"], []any{"forgejo-forgejo"}; !reflect.DeepEqual(got, want) {
		t.Errorf("skills = %#v, want %#v", got, want)
	}
}

// TestUpdateAgentSkillsFollowsTheHubsPointerRule is the keep-versus-clear rule on an
// agent's skills, on the wire: nil leaves the key out and the hub keeps what it holds, a
// non-nil empty list arrives as [] and clears the selection, and a non-empty one arrives as
// a plain array. Nothing is inherited from the image, so clearing really is none.
func TestUpdateAgentSkillsFollowsTheHubsPointerRule(t *testing.T) {
	h := newHubServer(t)
	h.reply(http.MethodPut, "/api/v1/agents/a-1", http.StatusOK, "")

	c := h.client()
	for _, u := range []AgentUpdate{
		{Name: ptrTo("a")},                           // nil: leave the stored selection alone
		{Skills: ptrTo([]string{"forgejo-forgejo"})}, // a list: send it as an array
		{Skills: ptrTo([]string{})},                  // empty: send [] and clear it
	} {
		if err := c.UpdateAgent(WithBackground(context.Background()), "a-1", u); err != nil {
			t.Fatalf("UpdateAgent: %v", err)
		}
	}

	calls := h.calls(http.MethodPut, "/api/v1/agents/a-1")
	if len(calls) != 3 {
		t.Fatalf("hub was called %d times, want 3", len(calls))
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(calls[0].Body), &first); err != nil {
		t.Fatalf("body 1 is not JSON: %s", calls[0].Body)
	}
	if _, ok := first["skills"]; ok {
		t.Errorf("body 1 = %s, want no skills key: nil leaves the stored selection alone", calls[0].Body)
	}
	if !strings.Contains(calls[1].Body, `"skills":["forgejo-forgejo"]`) {
		t.Errorf("body 2 = %s, want \"skills\":[\"forgejo-forgejo\"]", calls[1].Body)
	}
	if !strings.Contains(calls[2].Body, `"skills":[]`) {
		t.Errorf("body 3 = %s, want \"skills\":[] — an empty list is a clear, not an omission", calls[2].Body)
	}
}

// TestCreateAgentSendsEnabledTools is the tool grant on the wire. A spec that names one sends
// it as a plain array, in the image catalog's own spellings — the hub validates each name
// against the catalog by exact match. A spec that names none leaves the key out: the runtime
// reads an unset grant as "every tool the image offers", so an empty array on the wire would
// be a widening dressed up as a restriction.
func TestCreateAgentSendsEnabledTools(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/agents", http.StatusCreated, `{"id":"a-1","name":"a"}`)

	c := h.client()
	if _, err := c.CreateAgent(WithBackground(context.Background()), AgentSpec{
		Name:         "a",
		GroupID:      "g-1",
		ImageRef:     "ai-1",
		EnabledTools: []string{"mcp__forgejo-mcp-app__get_issue", "bash"},
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := c.CreateAgent(WithBackground(context.Background()), AgentSpec{
		Name:         "a",
		GroupID:      "g-1",
		ImageRef:     "ai-1",
		EnabledTools: []string{},
	}); err != nil {
		t.Fatalf("CreateAgent with an empty grant: %v", err)
	}

	calls := h.calls(http.MethodPost, "/api/v1/agents")
	if len(calls) != 2 {
		t.Fatalf("hub was called %d times, want 2", len(calls))
	}
	if !strings.Contains(calls[0].Body, `"enabledTools":["mcp__forgejo-mcp-app__get_issue","bash"]`) {
		t.Errorf("body 1 = %s, want the grant as a plain array", calls[0].Body)
	}
	var second map[string]any
	if err := json.Unmarshal([]byte(calls[1].Body), &second); err != nil {
		t.Fatalf("body 2 is not JSON: %s", calls[1].Body)
	}
	if _, ok := second["enabledTools"]; ok {
		t.Errorf("body 2 = %s, want no enabledTools: an empty grant is never sent", calls[1].Body)
	}
}

// TestUpdateAgentNeverSendsAnEmptyGrant is the one rule a grant has that skills do not. Nil
// leaves the stored grant alone and a list replaces it, but an empty one is left off the
// request rather than sent as []: clearing a grant is not "no tools", it is "every tool".
func TestUpdateAgentNeverSendsAnEmptyGrant(t *testing.T) {
	h := newHubServer(t)
	h.reply(http.MethodPut, "/api/v1/agents/a-1", http.StatusOK, "")

	c := h.client()
	for _, u := range []AgentUpdate{
		{Name: ptrTo("a")},                      // nil: leave the stored grant alone
		{EnabledTools: ptrTo([]string{"bash"})}, // a grant: send it
		{EnabledTools: ptrTo([]string{})},       // empty: leave it off, never clear
	} {
		if err := c.UpdateAgent(WithBackground(context.Background()), "a-1", u); err != nil {
			t.Fatalf("UpdateAgent: %v", err)
		}
	}

	calls := h.calls(http.MethodPut, "/api/v1/agents/a-1")
	if len(calls) != 3 {
		t.Fatalf("hub was called %d times, want 3", len(calls))
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(calls[0].Body), &first); err != nil {
		t.Fatalf("body 1 is not JSON: %s", calls[0].Body)
	}
	if _, ok := first["enabledTools"]; ok {
		t.Errorf("body 1 = %s, want no enabledTools: nil leaves the stored grant alone", calls[0].Body)
	}
	if !strings.Contains(calls[1].Body, `"enabledTools":["bash"]`) {
		t.Errorf("body 2 = %s, want \"enabledTools\":[\"bash\"]", calls[1].Body)
	}
	var third map[string]any
	if err := json.Unmarshal([]byte(calls[2].Body), &third); err != nil {
		t.Fatalf("body 3 is not JSON: %s", calls[2].Body)
	}
	if _, ok := third["enabledTools"]; ok {
		t.Errorf("body 3 = %s, want no enabledTools — an empty grant is a widening, not a clear", calls[2].Body)
	}
}

// TestGetAgentReadsEnabledTools is the read half of the drift pass: a pass that could not see
// the grant an agent holds would rewrite it on every run.
func TestGetAgentReadsEnabledTools(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/agents/a-1",
		`{"id":"a-1","name":"a","imageRef":{"name":"ai-1"},"enabledTools":["mcp__forgejo-mcp-app__get_issue","bash"]}`)

	agent, err := h.client().GetAgent(WithBackground(context.Background()), "a-1")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	want := []string{"mcp__forgejo-mcp-app__get_issue", "bash"}
	if !reflect.DeepEqual(agent.EnabledTools, want) {
		t.Errorf("EnabledTools = %v, want %v", agent.EnabledTools, want)
	}
}

// TestCreateAgentSendsCustomProvider checks a custom-provider agent is created with its
// URL and key: without them its runtime falls back to the OpenAI default endpoint and
// hangs.
func TestCreateAgentSendsCustomProvider(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/agents", http.StatusCreated, `{"id":"a-1","name":"a"}`)

	c := h.client()
	if _, err := c.CreateAgent(WithBackground(context.Background()), AgentSpec{
		Name: "a", GroupID: "g-1", ImageRef: "ai-1",
		LLM:       &LLMSpec{Provider: "custom", Model: "m"},
		CustomURL: "http://llm.local/v1",
		CustomKey: "k-c",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	var body map[string]any
	raw := h.calls(http.MethodPost, "/api/v1/agents")[0].Body
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("body is not JSON: %s", raw)
	}
	custom, ok := body["customProvider"].(map[string]any)
	if !ok {
		t.Fatalf("customProvider = %v, want {url, apiKey}", body["customProvider"])
	}
	if custom["url"] != "http://llm.local/v1" || custom["apiKey"] != "k-c" {
		t.Errorf("customProvider = %v, want url and key", custom)
	}
}

// TestCreateAgentSendsOllamaCloudKey checks a named provider's key is sent and the others
// stay absent.
func TestCreateAgentSendsOllamaCloudKey(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/agents", http.StatusCreated, `{"id":"a-1","name":"a"}`)

	c := h.client()
	if _, err := c.CreateAgent(WithBackground(context.Background()), AgentSpec{
		Name: "a", GroupID: "g-1", ImageRef: "ai-1",
		OllamaCloudKey: "k-o",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	var body map[string]any
	raw := h.calls(http.MethodPost, "/api/v1/agents")[0].Body
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("body is not JSON: %s", raw)
	}
	if ollama, ok := body["ollamaCloud"].(map[string]any); !ok || ollama["apiKey"] != "k-o" {
		t.Errorf("ollamaCloud = %v, want the key in request", body["ollamaCloud"])
	}
	if _, ok := body["customProvider"]; ok {
		t.Errorf("body carries customProvider: %s — an unset provider has to stay absent", raw)
	}
}

// TestCreateAgentOmitsEmptyProviders checks a spec with no keys and no custom URL sends
// none of the four provider objects, so the image's or hub's defaults hold.
func TestCreateAgentOmitsEmptyProviders(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/agents", http.StatusCreated, `{"id":"a-1","name":"a"}`)

	c := h.client()
	if _, err := c.CreateAgent(WithBackground(context.Background()), AgentSpec{
		Name: "a", GroupID: "g-1", ImageRef: "ai-1",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	var body map[string]any
	raw := h.calls(http.MethodPost, "/api/v1/agents")[0].Body
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("body is not JSON: %s", raw)
	}
	for _, key := range []string{"ollamaCloud", "openCode", "alibabaCloud", "customProvider"} {
		if _, ok := body[key]; ok {
			t.Errorf("body carries %q: %s — an unset provider has to stay absent", key, raw)
		}
	}
}

// TestCreateAgentSendsTheNewVendorKeys is the hub contract ainsel#242 adds: the
// three vendor blocks are openAI (capital AI), anthropic and google, each with
// its apiKey. A block spelled openai or OpenAI is a block the hub never reads,
// so the casing is the assertion here as much as the key is.
func TestCreateAgentSendsTheNewVendorKeys(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/agents", http.StatusCreated, `{"id":"a-1","name":"a"}`)

	c := h.client()
	if _, err := c.CreateAgent(WithBackground(context.Background()), AgentSpec{
		Name: "a", GroupID: "g-1", ImageRef: "ai-1",
		LLM:          &LLMSpec{Provider: "openai", Model: "m"},
		OpenAIKey:    "k-openai",
		AnthropicKey: "k-ant",
		GoogleKey:    "k-goog",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	var body map[string]any
	raw := h.calls(http.MethodPost, "/api/v1/agents")[0].Body
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("body is not JSON: %s", raw)
	}
	for name, want := range map[string]string{"openAI": "k-openai", "anthropic": "k-ant", "google": "k-goog"} {
		block, ok := body[name].(map[string]any)
		if !ok {
			t.Errorf("body has no %q block: %s", name, raw)
			continue
		}
		if block["apiKey"] != want {
			t.Errorf("%s.apiKey = %v, want %q", name, block["apiKey"], want)
		}
	}
	for _, key := range []string{"openai", "OpenAI", "ollamaCloud", "customProvider"} {
		if _, ok := body[key]; ok {
			t.Errorf("body carries %q: %s — an unset provider has to stay absent", key, raw)
		}
	}
}

// TestCreateAgentOmitsEmptyNewVendorBlocks is the same contract's other half: a
// spec that names none of the three vendors sends none of their blocks, so the
// hub keeps whatever it holds for them.
func TestCreateAgentOmitsEmptyNewVendorBlocks(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/agents", http.StatusCreated, `{"id":"a-1","name":"a"}`)

	c := h.client()
	if _, err := c.CreateAgent(WithBackground(context.Background()), AgentSpec{
		Name: "a", GroupID: "g-1", ImageRef: "ai-1",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	raw := h.calls(http.MethodPost, "/api/v1/agents")[0].Body
	for _, key := range []string{"openAI", "anthropic", "google"} {
		if strings.Contains(raw, "\""+key+"\"") {
			t.Errorf("body carries %q: %s — an unset provider has to stay absent", key, raw)
		}
	}
}

// TestUpdateAgentSendsTheNewVendorKeys is a vendor rotation's write: each named
// block rides with its key, and a block the update leaves nil stays absent — the
// hub's keep-on-omit semantics.
func TestUpdateAgentSendsTheNewVendorKeys(t *testing.T) {
	h := newHubServer(t)
	h.reply(http.MethodPut, "/api/v1/agents/a-1", http.StatusOK, "")

	c := h.client()
	err := c.UpdateAgent(WithBackground(context.Background()), "a-1", AgentUpdate{
		OpenAIKey:    ptrTo("k-openai"),
		AnthropicKey: ptrTo("k-ant"),
	})
	if err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}

	var body map[string]any
	raw := h.calls(http.MethodPut, "/api/v1/agents/a-1")[0].Body
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("body is not JSON: %s", raw)
	}
	if block, ok := body["openAI"].(map[string]any); !ok || block["apiKey"] != "k-openai" {
		t.Errorf("openAI = %v, want the rotated key", body["openAI"])
	}
	if block, ok := body["anthropic"].(map[string]any); !ok || block["apiKey"] != "k-ant" {
		t.Errorf("anthropic = %v, want the rotated key", body["anthropic"])
	}
	if _, ok := body["google"]; ok {
		t.Errorf("body carries google: %s — an unnamed block has to stay absent", raw)
	}
}

// TestGetAgentReadsTheNewVendorBlocks is the read side: a hub that answers the
// blocks gives their presence back (a real hub keeps the key itself, so what
// comes back is at most whether one is held).
func TestGetAgentReadsTheNewVendorBlocks(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/agents/a-1", `{
		"id":"a-1","name":"a",
		"openAI":{"apiKey":""},
		"anthropic":{"apiKey":""},
		"google":{"apiKey":""}
	}`)

	c := h.client()
	agent, err := c.GetAgent(WithCredential(context.Background(), "ainselp_user"), "a-1")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if agent.OpenAIKey != "" || agent.AnthropicKey != "" || agent.GoogleKey != "" {
		t.Errorf("vendor keys = %q/%q/%q, want the empty answers the hub gives", agent.OpenAIKey, agent.AnthropicKey, agent.GoogleKey)
	}
	if agent.LLM != nil {
		t.Errorf("llm = %+v, want nothing read", agent.LLM)
	}
}

// TestCreateTriggerFilesItUnderAGroup checks the write a setup flow makes most: the
// trigger as setup generated it, plus the group it belongs to, and the created id is returned.
func TestCreateTriggerFilesItUnderAGroup(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/triggers", http.StatusCreated, `{"id":"t-1","name":"trigger-refiner-issue-opened-apps"}`)

	c := h.client()
	ref, err := c.CreateTrigger(WithBackground(context.Background()), "g-1", setup.Trigger{
		Name:         "trigger-refiner-issue-opened-apps",
		AgentRef:     "apps-refiner",
		ConnectorRef: "c-42",
		Filters: []setup.Filter{
			{Field: "type", Op: "eq", Value: "issues"},
			{Field: "issue.labels.name", Op: "not-contains", Value: "do-not-refine"},
		},
	})
	if err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}
	if ref.ID != "t-1" || ref.Name != "trigger-refiner-issue-opened-apps" {
		t.Errorf("ref = %+v, want t-1 with name trigger-refiner-issue-opened-apps", ref)
	}

	var body struct {
		Name         string `json:"name"`
		GroupID      string `json:"groupId"`
		AgentRef     string `json:"agentRef"`
		ConnectorRef string `json:"connectorRef"`
		Filters      []struct {
			Field string `json:"field"`
			Op    string `json:"op"`
			Value string `json:"value"`
		} `json:"filters"`
	}
	if err := json.Unmarshal([]byte(h.calls(http.MethodPost, "/api/v1/triggers")[0].Body), &body); err != nil {
		t.Fatalf("body is not JSON: %s", h.calls(http.MethodPost, "/api/v1/triggers")[0].Body)
	}
	if body.GroupID != "g-1" || body.Name != "trigger-refiner-issue-opened-apps" {
		t.Errorf("body = %+v, want the trigger named and filed under g-1", body)
	}
	// A trigger's agentRef is the agent's name and its connectorRef the connector's id
	// (ruling R13) — the two are not interchangeable.
	if body.AgentRef != "apps-refiner" || body.ConnectorRef != "c-42" {
		t.Errorf("refs = %q/%q, want the agent name and the connector id", body.AgentRef, body.ConnectorRef)
	}
	if len(body.Filters) != 2 || body.Filters[0].Field != "type" || body.Filters[1].Op != "not-contains" {
		t.Errorf("filters = %+v, want the generated filter set", body.Filters)
	}
}

// TestListTriggersReadsOneConnectorsTriggers checks the read setup makes before it
// creates: the hub does the filtering by connector, and the filters come back in the shape
// setup.Missing compares.
func TestListTriggersReadsOneConnectorsTriggers(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/triggers", pageOf(`[{"id":"t-1","name":"trigger-refiner-mention-apps",
		"agentRef":"apps-refiner","connectorRef":"c-42",
		"filters":[{"field":"comment.body","op":"contains","value":"@apps-refiner"},
			{"field":"issue.labels","op":"in","values":["a","b"]}]}]`, 1))

	c := h.client()
	triggers, err := c.ListTriggers(WithCredential(context.Background(), "ainselp_user"), "c-42")
	if err != nil {
		t.Fatalf("ListTriggers: %v", err)
	}
	if len(triggers) != 1 {
		t.Fatalf("got %d triggers, want 1", len(triggers))
	}

	got := triggers[0]
	if got.Name != "trigger-refiner-mention-apps" || got.AgentRef != "apps-refiner" || got.ConnectorRef != "c-42" {
		t.Errorf("trigger = %+v, want the listed one", got)
	}
	if len(got.Filters) != 2 || got.Filters[1].Values == nil ||
		got.Filters[1].Values[0] != "a" || got.Filters[1].Values[1] != "b" {
		t.Errorf("filters = %+v, want both filters with the set operand list kept", got.Filters)
	}
	if got := h.calls(http.MethodGet, "/api/v1/triggers")[0].Query.Get("connector"); got != "c-42" {
		t.Errorf("connector = %q, want the hub asked for one connector's triggers", got)
	}
}

// TestEventTypeFromPayload checks the reading of an event's type from its payload shape
// (ruling R10), which is what decides which subject a run gets.
func TestEventTypeFromPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "pull request",
			payload: `{"repository":{"full_name":"o/r"},"pull_request":{"number":1}}`,
			want:    "pull_request",
		},
		{
			name:    "comment on an issue",
			payload: `{"repository":{"full_name":"o/r"},"issue":{"number":1},"comment":{"body":"hi"}}`,
			want:    "issue_comment",
		},
		{
			name:    "issue opened",
			payload: `{"repository":{"full_name":"o/r"},"issue":{"number":1}}`,
			want:    "issues",
		},
		{
			name:    "push",
			payload: `{"repository":{"full_name":"o/r"},"commits":[]}`,
			want:    "",
		},
		{
			// An event whose payload the hub stored empty, or that is not JSON at all:
			// no reading, and so no subject.
			name:    "not an object",
			payload: `[]`,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := eventTypeFromPayload([]byte(tt.payload)); got != tt.want {
				t.Errorf("eventTypeFromPayload = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRunsEventErrorStillFails checks the difference between an event the hub has thrown
// away and a hub that would not say: only the first is a run with an unknown subject, the
// second is a failed call — however many other events were read alongside it.
func TestRunsEventErrorStillFails(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/invocations", `{"invocations":[{"id":"inv-1","connector":"c-1","eventId":"e-1","status":"success","startTime":"2026-10-22T10:00:00Z"}],"total":1,"page":1,"pageSize":200,"totalPages":1}`)
	h.reply(http.MethodGet, "/api/v1/events/e-1", http.StatusInternalServerError, `{}`)
	h.get("/api/v1/observability/metrics/tokens/by-event", `{"range":"7d","rows":[]}`)

	c := h.client()
	_, err := c.Runs(WithBackground(context.Background()), RunsQuery{Connectors: []string{"c-1"}, Agents: []string{"a-1"}, Since: time.Unix(0, 0), Costs: true})
	if err == nil {
		t.Fatal("Runs succeeded with a hub that answered 500 for the event")
	}
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusInternalServerError {
		t.Errorf("error = %v, want it to carry the 500", err)
	}
}

// TestRunsEvent404IsAZeroSubject checks the one failure that is not a failure: an event past
// retention. The hub has the invocation and no longer has the payload, so nothing can say what
// the run was about — and the run stays in the list with a subject nobody can open rather than
// failing the whole read.
func TestRunsEvent404IsAZeroSubject(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/invocations", `{"invocations":[
		{"id":"inv-1","agentName":"developer","status":"success","connector":"c-1","eventId":"e-gone",
		 "startTime":"2026-10-22T10:00:00Z"}
	],"total":1,"page":1,"pageSize":200,"totalPages":1}`)
	h.reply(http.MethodGet, "/api/v1/events/e-gone", http.StatusNotFound, `{"error":"event not found"}`)
	h.get("/api/v1/observability/metrics/tokens/by-event", `{"range":"7d","rows":[]}`)

	c := h.client()
	runs, err := c.Runs(WithBackground(context.Background()), RunsQuery{
		Connectors: []string{"c-1"}, Agents: []string{"a-1"}, Since: time.Unix(0, 0), Costs: true,
	})
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1 (a gone event is not a failed read)", len(runs))
	}
	if runs[0].Subject != (forge.Subject{}) {
		t.Errorf("subject = %+v, want the zero subject for an event the hub no longer has", runs[0].Subject)
	}
	if runs[0].CostKnown {
		t.Errorf("cost known = true, want false (no cost was measured for an event with no row)")
	}
}

// memCache is a SubjectCache kept in memory, for the checks a real store would make
// the hub test reach across packages for. It answers from what was put into it, so a
// second Runs over the same cache is the second read the tests assert on; getErr and
// putErr make either half fail, and puts counts the PutSubjects calls made.
type memCache struct {
	mu     sync.Mutex
	subs   map[string]forge.Subject
	getErr error
	putErr error
	puts   int
}

var _ SubjectCache = (*memCache)(nil)

func (c *memCache) Subjects(_ context.Context, eventIDs []string) (map[string]forge.Subject, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.getErr != nil {
		return nil, c.getErr
	}
	out := make(map[string]forge.Subject, len(eventIDs))
	for _, id := range eventIDs {
		if s, ok := c.subs[id]; ok {
			out[id] = s
		}
	}
	return out, nil
}

func (c *memCache) PutSubjects(_ context.Context, subjects map[string]forge.Subject, _ time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.putErr != nil {
		return c.putErr
	}
	c.puts++
	if c.subs == nil {
		c.subs = make(map[string]forge.Subject, len(subjects))
	}
	for id, s := range subjects {
		c.subs[id] = s
	}
	return nil
}

func (c *memCache) stored(id string) (forge.Subject, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.subs[id]
	return s, ok
}

func (c *memCache) putCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.puts
}

// subjectInvocations is the one page of invocations the cache tests answer with: two
// runs of this forge's connector, each behind its own event.
const subjectInvocations = `{"invocations":[
	{"id":"inv-1","agentName":"developer","status":"success","connector":"c-1","eventId":"evt-1",
	 "startTime":"2026-10-22T10:00:00Z"},
	{"id":"inv-2","agentName":"refiner","status":"success","connector":"c-1","eventId":"evt-2",
	 "startTime":"2026-10-22T11:00:00Z"}
],"total":2,"page":1,"pageSize":200,"totalPages":1}`

// issueEvent is one event's answer, carrying an issue payload for the given number.
func issueEvent(id string, number int) string {
	return fmt.Sprintf(`{"id":%q,"timestamp":"2026-10-22T10:00:00Z","status":"matched",
		"payload":{"repository":{"full_name":"o/r"},"issue":{"number":%d,"title":"T%d"}}}`, id, number, number)
}

// TestRunsReadsCachedSubjectsWithoutFetching is the whole point of the cache: an event
// the plugin has already read is never read again. The test hub has no route for
// evt-1 at all, so a fetch would be an unexpected call — and the run still carries the
// subject the cache was seeded with.
func TestRunsReadsCachedSubjectsWithoutFetching(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/invocations", `{"invocations":[
		{"id":"inv-1","agentName":"developer","status":"success","connector":"c-1","eventId":"evt-1",
		 "startTime":"2026-10-22T10:00:00Z"}
	],"total":1,"page":1,"pageSize":200,"totalPages":1}`)

	cached := forge.Subject{Repo: "o/r", Number: 7, Kind: forge.KindIssue, Title: "Cached"}
	cache := &memCache{subs: map[string]forge.Subject{"evt-1": cached}}

	c := h.client()
	runs, err := c.Runs(WithBackground(context.Background()), RunsQuery{
		Connectors: []string{"c-1"}, Agents: []string{"a-1"}, Since: time.Unix(0, 0), Cache: cache,
	})
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	if runs[0].Subject != cached {
		t.Errorf("subject = %+v, want the cached %+v", runs[0].Subject, cached)
	}
	if n := len(h.calls(http.MethodGet, "/api/v1/events/evt-1")); n != 0 {
		t.Errorf("the hub was asked for evt-1 %d times, want 0 — its subject was already cached", n)
	}
}

// TestRunsFetchesAndCachesMisses is the other half: what the cache does not have is
// fetched — once — and handed to the cache, so the next read of the same page asks
// the hub for nothing.
func TestRunsFetchesAndCachesMisses(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/invocations", subjectInvocations)
	h.get("/api/v1/events/evt-1", issueEvent("evt-1", 1))
	h.get("/api/v1/events/evt-2", issueEvent("evt-2", 2))

	cache := &memCache{}
	c := h.client()
	runs, err := c.Runs(WithBackground(context.Background()), RunsQuery{
		Connectors: []string{"c-1"}, Agents: []string{"a-1"}, Since: time.Unix(0, 0), Cache: cache,
	})
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2", len(runs))
	}
	for _, id := range []string{"evt-1", "evt-2"} {
		if n := len(h.calls(http.MethodGet, "/api/v1/events/"+id)); n != 1 {
			t.Errorf("%s was fetched %d times, want 1", id, n)
		}
	}
	if n := cache.putCount(); n != 1 {
		t.Errorf("PutSubjects was called %d times, want 1 (one page is one write)", n)
	}
	for _, id := range []string{"evt-1", "evt-2"} {
		if _, ok := cache.stored(id); !ok {
			t.Errorf("%s was not put into the cache", id)
		}
	}

	// The second page is the one the cache answers: no event is fetched again.
	if _, err := c.Runs(WithBackground(context.Background()), RunsQuery{
		Connectors: []string{"c-1"}, Agents: []string{"a-1"}, Since: time.Unix(0, 0), Cache: cache,
	}); err != nil {
		t.Fatalf("Runs(second): %v", err)
	}
	for _, id := range []string{"evt-1", "evt-2"} {
		if n := len(h.calls(http.MethodGet, "/api/v1/events/"+id)); n != 1 {
			t.Errorf("%s was fetched %d times across two pages, want 1", id, n)
		}
	}
}

// TestRunsCaches404AsNoSubject is the answer that is final too: an event the hub has
// already thrown away is put into the cache as the zero subject, so a page keeps on
// showing the run without asking a hub that has stopped answering for it.
func TestRunsCaches404AsNoSubject(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/invocations", `{"invocations":[
		{"id":"inv-1","agentName":"developer","status":"success","connector":"c-1","eventId":"evt-gone",
		 "startTime":"2026-10-22T10:00:00Z"}
	],"total":1,"page":1,"pageSize":200,"totalPages":1}`)
	h.reply(http.MethodGet, "/api/v1/events/evt-gone", http.StatusNotFound, `{"error":"event not found"}`)

	cache := &memCache{}
	c := h.client()
	runs, err := c.Runs(WithBackground(context.Background()), RunsQuery{
		Connectors: []string{"c-1"}, Agents: []string{"a-1"}, Since: time.Unix(0, 0), Cache: cache,
	})
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Subject != (forge.Subject{}) {
		t.Fatalf("runs = %+v, want one run with the zero subject", runs)
	}
	if got, ok := cache.stored("evt-gone"); !ok || got != (forge.Subject{}) {
		t.Errorf("cache holds %v for evt-gone (present: %v), want a stored zero subject", got, ok)
	}

	if _, err := c.Runs(WithBackground(context.Background()), RunsQuery{
		Connectors: []string{"c-1"}, Agents: []string{"a-1"}, Since: time.Unix(0, 0), Cache: cache,
	}); err != nil {
		t.Fatalf("Runs(second): %v", err)
	}
	if n := len(h.calls(http.MethodGet, "/api/v1/events/evt-gone")); n != 1 {
		t.Errorf("the gone event was fetched %d times, want 1 — its 404 is a final answer", n)
	}
}

// TestRunsCacheErrorsDoNotFailRuns is the cache's place: it is a speed, not a
// dependency. A cache that cannot be read means everything is fetched; a cache that
// cannot be written means the runs are returned anyway. Neither fails the read.
func TestRunsCacheErrorsDoNotFailRuns(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/invocations", `{"invocations":[
		{"id":"inv-1","agentName":"developer","status":"success","connector":"c-1","eventId":"evt-1",
		 "startTime":"2026-10-22T10:00:00Z"}
	],"total":1,"page":1,"pageSize":200,"totalPages":1}`)
	h.get("/api/v1/events/evt-1", issueEvent("evt-1", 1))

	c := h.client()

	readFailed := &memCache{getErr: errors.New("cache down")}
	runs, err := c.Runs(WithBackground(context.Background()), RunsQuery{
		Connectors: []string{"c-1"}, Agents: []string{"a-1"}, Since: time.Unix(0, 0), Cache: readFailed,
	})
	if err != nil {
		t.Fatalf("Runs with a cache that cannot be read: %v", err)
	}
	if len(runs) != 1 || runs[0].Subject.Number != 1 {
		t.Fatalf("runs = %+v, want the run fetched from the hub", runs)
	}

	writeFailed := &memCache{putErr: errors.New("cache full")}
	runs, err = c.Runs(WithBackground(context.Background()), RunsQuery{
		Connectors: []string{"c-1"}, Agents: []string{"a-1"}, Since: time.Unix(0, 0), Cache: writeFailed,
	})
	if err != nil {
		t.Fatalf("Runs with a cache that cannot be written: %v", err)
	}
	if len(runs) != 1 || runs[0].Subject.Number != 1 {
		t.Fatalf("runs = %+v, want the run fetched from the hub", runs)
	}
}

// TestRunsFetchErrorWritesNothing keeps a failed read out of the cache: an event the
// hub answered 500 for has not been seen, and writing its subject would turn a hub in
// trouble into a permanently wrong answer.
func TestRunsFetchErrorWritesNothing(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/invocations", `{"invocations":[
		{"id":"inv-1","agentName":"developer","status":"success","connector":"c-1","eventId":"evt-1",
		 "startTime":"2026-10-22T10:00:00Z"}
	],"total":1,"page":1,"pageSize":200,"totalPages":1}`)
	h.reply(http.MethodGet, "/api/v1/events/evt-1", http.StatusInternalServerError, `{}`)

	cache := &memCache{}
	c := h.client()
	if _, err := c.Runs(WithBackground(context.Background()), RunsQuery{
		Connectors: []string{"c-1"}, Agents: []string{"a-1"}, Since: time.Unix(0, 0), Cache: cache,
	}); err == nil {
		t.Fatal("Runs succeeded with a hub that answered 500 for the event")
	}
	if n := cache.putCount(); n != 0 {
		t.Errorf("PutSubjects was called %d times, want 0 — nothing from a failed read is cached", n)
	}
}

// TestRunsErrorsCarryNoCredential is the rule the whole package writes under: a
// credential never reaches a string a handler may print. Nothing Runs and company return
// names one, so the checks above assert on the errors they build.
func TestRunsErrorsCarryNoCredential(t *testing.T) {
	h := newHubServer(t)
	h.post(credentialsPath, http.StatusForbidden, `{"error":"forbidden"}`)

	c := h.client()
	_, err := c.Runs(WithBackground(context.Background()), RunsQuery{Connectors: []string{"c-1"}, Agents: []string{"a-1"}, Since: time.Unix(0, 0), Costs: true})
	if err == nil {
		t.Fatal("Runs succeeded with a hub that refused to mint a credential")
	}
	for _, leak := range []string{"ainselp", "Bearer"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error %q leaks a credential", err)
		}
	}
}

// TestCallWithoutCredentialOrBackgroundFails checks the guard behind ruling R21: a hub
// call made on a context that carries neither a caller's credential nor the background
// marker is a bug in the calling code, and it fails there — it does not silently reach
// the hub as the plugin. The marker is what makes background intent a decision at the
// entry point rather than an accident of a handler that forgot to stage a credential.
func TestCallWithoutCredentialOrBackgroundFails(t *testing.T) {
	h := newHubServer(t)

	c := h.client()
	_, err := c.ListPersonas(context.Background())
	if err == nil {
		t.Fatal("ListPersonas succeeded on a context with no credential and no background marker")
	}
	if want := "hub: no credential for this call"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	// The failure is local: nothing was asked of the hub — not the list, not even a
	// background mint the caller never authorised.
	if len(h.seen) != 0 {
		t.Errorf("hub was called %d times, want no call at all", len(h.seen))
	}
}

// -- images -----------------------------------------------------------------------

// imageRoutes answers the three calls that make a role image: create it, discover its
// tools, and state which of them the role may use.
const imageCreated = `{"id":"img-1","displayName":"forgejo-developer-apps","imageURL":"registry/developer:1"}`

func TestCreateAgentImage(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/agent-images", http.StatusCreated, imageCreated)

	c := h.client()
	ref, err := c.CreateAgentImage(WithCredential(context.Background(), "ainselp_user"), ImageSpec{
		DisplayName: "forgejo-developer-apps",
		GroupID:     "g-1",
		ImageURL:    "registry/developer:1",
		MCPServers:  []string{"forgejo-mcp-apps", "plugin-forgejo"},
		Env:         []EnvVar{{Name: "FORGEJO_TOKEN", Value: "bot-token", Secret: true}},
	})
	if err != nil {
		t.Fatalf("CreateAgentImage: %v", err)
	}
	// The image's id is what every later call (refresh, tools, an agent's imageRef)
	// acts on it by; the display name is what the wizard shows.
	if ref.ID != "img-1" || ref.Name != "forgejo-developer-apps" {
		t.Errorf("ref = %+v, want img-1/forgejo-developer-apps", ref)
	}

	calls := h.calls(http.MethodPost, "/api/v1/agent-images")
	if len(calls) != 1 {
		t.Fatalf("hub was called %d times, want 1", len(calls))
	}
	if got, want := calls[0].Auth, "Bearer ainselp_user"; got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(calls[0].Body), &body); err != nil {
		t.Fatalf("body is not JSON: %s", calls[0].Body)
	}
	if got, want := body["groupId"], "g-1"; got != want {
		t.Errorf("groupId = %v, want %v", got, want)
	}
	if got, want := body["imageURL"], "registry/developer:1"; got != want {
		t.Errorf("imageURL = %v, want %v", got, want)
	}
	if got, want := fmt.Sprint(body["mcpServers"]), "[forgejo-mcp-apps plugin-forgejo]"; got != want {
		t.Errorf("mcpServers = %s, want the forge MCP and the plugin's own", got)
	}
	// An image carries no skills: skill selection is the agent's, so nothing named
	// here reaches one — and a field the hub only ignores is a trap, not a default.
	if _, ok := body["enabledSkills"]; ok {
		t.Errorf("body = %s, want no enabledSkills: skills belong to the agent", calls[0].Body)
	}
	env, ok := body["env"].([]any)
	if !ok || len(env) != 1 {
		t.Fatalf("env = %v, want one entry", body["env"])
	}
	if entry, ok := env[0].(map[string]any); !ok || entry["name"] != "FORGEJO_TOKEN" ||
		entry["value"] != "bot-token" || entry["secret"] != true {
		t.Errorf("env[0] = %v, want the bot token marked secret", env[0])
	}
}

// TestRefreshImageTools checks the synchronous discovery call: it answers with the image
// whose catalog it just filled, and the tools of that answer are what the caller then
// enables.
func TestRefreshImageTools(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/agent-images/img-1/refresh-mcp", http.StatusOK, `{
		"image":{"id":"img-1","tools":[
			{"name":"mcp__forgejo-mcp-apps__create_issue","kind":"mcp","mcpSource":"forgejo-mcp-apps",
			 "description":"Open an issue","enabled":false},
			{"name":"mcp__forgejo-mcp-apps__delete_file","kind":"mcp","mcpSource":"forgejo-mcp-apps","enabled":false}
		]},
		"warnings":[]
	}`)

	c := h.client()
	tools, err := c.RefreshImageTools(WithBackground(context.Background()), "img-1")
	if err != nil {
		t.Fatalf("RefreshImageTools: %v", err)
	}

	want := []ImageTool{
		{Name: "mcp__forgejo-mcp-apps__create_issue", Kind: "mcp", McpSource: "forgejo-mcp-apps",
			Description: "Open an issue", Enabled: false},
		{Name: "mcp__forgejo-mcp-apps__delete_file", Kind: "mcp", McpSource: "forgejo-mcp-apps"},
	}
	if !reflect.DeepEqual(tools, want) {
		t.Errorf("tools = %+v, want %+v", tools, want)
	}
	calls := h.calls(http.MethodPost, "/api/v1/agent-images/img-1/refresh-mcp")
	if len(calls) != 1 {
		t.Fatalf("refresh was called %d times, want 1", len(calls))
	}
	if calls[0].Body != "" {
		t.Errorf("refresh posted body %q, want none", calls[0].Body)
	}
}

// TestRefreshImageToolsOneServerFailing is the case the wizard lives with: the plugin's own
// MCP server cannot be listed anonymously (spec §3.5), so the hub reports it per server and
// still returns the forge server's tools. The tools are the point of the call, so they come
// back — with the failing server named, so a caller can ignore that one and only that one.
func TestRefreshImageToolsOneServerFailing(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/agent-images/img-1/refresh-mcp", http.StatusOK, `{
		"image":{"id":"img-1","tools":[
			{"name":"mcp__forgejo-mcp-apps__create_issue","kind":"mcp","mcpSource":"forgejo-mcp-apps","enabled":false}
		]},
		"warnings":["plugin-forgejo: create transport: unsupported protocol scheme \"\""]
	}`)

	c := h.client()
	tools, err := c.RefreshImageTools(WithBackground(context.Background()), "img-1")
	if len(tools) != 1 || tools[0].Name != "mcp__forgejo-mcp-apps__create_issue" {
		t.Errorf("tools = %+v, want the forge server's tool despite the other server failing", tools)
	}
	if err == nil {
		t.Fatal("RefreshImageTools reported no error with a server the hub could not read")
	}

	var tre *ToolRefreshError
	if !errors.As(err, &tre) {
		t.Fatalf("error = %v (%T), want a *ToolRefreshError", err, err)
	}
	if got, want := fmt.Sprint(tre.Servers), "[plugin-forgejo]"; got != want {
		t.Errorf("Servers = %s, want %s", got, want)
	}
	// The one thing the wizard needs to know about this error: it is allowed to
	// ignore the plugin's own server, and only when nothing else failed.
	if !tre.OnlyMissing("plugin-forgejo") {
		t.Error("OnlyMissing(plugin-forgejo) = false, want true for the expected per-server failure")
	}
	if tre.OnlyMissing("forgejo-mcp-apps") {
		t.Error("OnlyMissing(forgejo-mcp-apps) = true, want false: that server answered")
	}
	for _, leak := range []string{"ainselp", "Bearer"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error %q leaks a credential", err)
		}
	}
}

// TestRefreshImageToolsTwoServersFailing keeps that permission narrow: with the forge
// server among the failures, the call's own result is not usable and the error has to say
// both servers.
func TestRefreshImageToolsTwoServersFailing(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/agent-images/img-1/refresh-mcp", http.StatusOK, `{
		"image":{"id":"img-1","tools":[]},
		"warnings":["plugin-forgejo: initialize client: eof","forgejo-mcp-apps: list tools: refused"]
	}`)

	c := h.client()
	_, err := c.RefreshImageTools(WithBackground(context.Background()), "img-1")

	var tre *ToolRefreshError
	if !errors.As(err, &tre) {
		t.Fatalf("error = %v (%T), want a *ToolRefreshError", err, err)
	}
	if got, want := fmt.Sprint(tre.Servers), "[plugin-forgejo forgejo-mcp-apps]"; got != want {
		t.Errorf("Servers = %s, want %s", got, want)
	}
	if tre.OnlyMissing("plugin-forgejo") {
		t.Error("OnlyMissing(plugin-forgejo) = true, want false: the forge server failed too")
	}
}

// TestRefreshImageToolsUnnameableWarning keeps a warning the client cannot attribute to a
// server from reading as the expected one: it is reported as-is, so a caller that ignores
// plugin-forgejo does not ignore an error it cannot name.
func TestRefreshImageToolsUnnameableWarning(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/agent-images/img-1/refresh-mcp", http.StatusOK, `{
		"image":{"id":"img-1","tools":[]},
		"warnings":["something went wrong without a server prefix"]
	}`)

	c := h.client()
	_, err := c.RefreshImageTools(WithBackground(context.Background()), "img-1")

	var tre *ToolRefreshError
	if !errors.As(err, &tre) {
		t.Fatalf("error = %v (%T), want a *ToolRefreshError", err, err)
	}
	if tre.OnlyMissing("plugin-forgejo") {
		t.Error("OnlyMissing(plugin-forgejo) = true, want false for a warning that names no server")
	}
	if got, want := fmt.Sprint(tre.Servers), "[something went wrong without a server prefix]"; got != want {
		t.Errorf("Servers = %s, want the warning text kept", got)
	}
}

func TestRefreshImageToolsFailure(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/agent-images/img-1/refresh-mcp", http.StatusBadRequest, `{"error":"no MCP servers configured on this image"}`)

	c := h.client()
	tools, err := c.RefreshImageTools(WithBackground(context.Background()), "img-1")
	if tools != nil {
		t.Errorf("tools = %+v, want none from a call that failed", tools)
	}
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusBadRequest {
		t.Errorf("error = %v, want it to carry the 400", err)
	}
}

// TestSetImageTools checks the profile step: the whole catalog goes back with only the
// role's tools enabled, in the shape PUT takes (name, kind, source, enabled).
func TestSetImageTools(t *testing.T) {
	h := newHubServer(t)
	h.reply(http.MethodPut, "/api/v1/agent-images/img-1", http.StatusOK, imageCreated)

	c := h.client()
	err := c.SetImageTools(WithBackground(context.Background()), "img-1", []ImageTool{
		{Name: "mcp__forgejo-mcp-apps__create_issue", Kind: "mcp", McpSource: "forgejo-mcp-apps",
			Description: "Open an issue", Enabled: true},
		{Name: "mcp__forgejo-mcp-apps__delete_file", Kind: "mcp", McpSource: "forgejo-mcp-apps"},
	})
	if err != nil {
		t.Fatalf("SetImageTools: %v", err)
	}

	calls := h.calls(http.MethodPut, "/api/v1/agent-images/img-1")
	if len(calls) != 1 {
		t.Fatalf("hub was called %d times, want 1", len(calls))
	}
	var body struct {
		Tools []struct {
			Name        string `json:"name"`
			Kind        string `json:"kind"`
			McpSource   string `json:"mcpSource"`
			Description string `json:"description"`
			Enabled     bool   `json:"enabled"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(calls[0].Body), &body); err != nil {
		t.Fatalf("body is not JSON: %s", calls[0].Body)
	}
	if len(body.Tools) != 2 {
		t.Fatalf("tools = %s, want the whole catalog restated", calls[0].Body)
	}
	if body.Tools[0].Name != "mcp__forgejo-mcp-apps__create_issue" || !body.Tools[0].Enabled {
		t.Errorf("tools[0] = %+v, want the role's tool enabled", body.Tools[0])
	}
	if body.Tools[0].Description != "Open an issue" {
		t.Errorf("tools[0].description = %q, want it carried so restating a tool does not blank it", body.Tools[0].Description)
	}
	if body.Tools[1].Enabled {
		t.Errorf("tools[1] = %+v, want the tool outside the role's set left disabled", body.Tools[1])
	}
}

// TestUpdateAgentImageKeepsWhatItSendsNothing checks the absent-versus-empty rule an
// edit-install relies on: a field the caller leaves nil is absent from the body, so the
// hub keeps what it holds, and a field it does send arrives even when empty.
func TestUpdateAgentImageKeepsWhatItSendsNothing(t *testing.T) {
	h := newHubServer(t)
	h.reply(http.MethodPut, "/api/v1/agent-images/img-1", http.StatusOK, imageCreated)

	c := h.client()
	err := c.UpdateAgentImage(WithBackground(context.Background()), "img-1", ImageUpdate{
		ImageURL: ptrTo("registry/developer:2"),
	})
	if err != nil {
		t.Fatalf("UpdateAgentImage: %v", err)
	}

	body := map[string]any{}
	raw := h.calls(http.MethodPut, "/api/v1/agent-images/img-1")[0].Body
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("body is not JSON: %s", raw)
	}
	if got, want := body["imageURL"], "registry/developer:2"; got != want {
		t.Errorf("imageURL = %v, want %v", got, want)
	}
	for _, key := range []string{"env", "mcpServers"} {
		if _, ok := body[key]; ok {
			t.Errorf("body carries %q: %s — an unset field has to stay absent so the stored one holds", key, raw)
		}
	}
}

// TestUpdateAgentImageClearsAndKeepsSecrets is the secret rule of an image env update: an
// empty value on a name the hub stores as a secret keeps the stored value, and because the
// client never sends an explicit secret:false, a name the hub holds as a secret cannot be
// declassified by an update that did not mean to.
func TestUpdateAgentImageClearsAndKeepsSecrets(t *testing.T) {
	h := newHubServer(t)
	h.reply(http.MethodPut, "/api/v1/agent-images/img-1", http.StatusOK, imageCreated)

	c := h.client()
	err := c.UpdateAgentImage(WithBackground(context.Background()), "img-1", ImageUpdate{
		Env:        &[]EnvVar{{Name: "FORGEJO_TOKEN", Value: "fresh-token", Secret: true}},
		MCPServers: &[]string{},
	})
	if err != nil {
		t.Fatalf("UpdateAgentImage: %v", err)
	}

	body := map[string]any{}
	raw := h.calls(http.MethodPut, "/api/v1/agent-images/img-1")[0].Body
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("body is not JSON: %s", raw)
	}
	env, ok := body["env"].([]any)
	if !ok || len(env) != 1 {
		t.Fatalf("env = %v, want one entry", body["env"])
	}
	entry, _ := env[0].(map[string]any)
	if entry["name"] != "FORGEJO_TOKEN" || entry["value"] != "fresh-token" || entry["secret"] != true {
		t.Errorf("env[0] = %v, want the token sent as a secret", env[0])
	}
	// An explicit empty list means "none", which is a different answer from the
	// field being absent.
	servers, ok := body["mcpServers"].([]any)
	if !ok || len(servers) != 0 {
		t.Errorf("mcpServers = %v, want an explicit empty list", body["mcpServers"])
	}
}

// TestGetAgentImage reads back the managed state of an image: the tool catalog with its
// enabled flags, the env by name only, the MCP servers and the image URL. Skills are not
// among it: they belong to the agent, and a hub that still answers with an
// enabledSkills list is a hub this plugin no longer reads.
func TestGetAgentImage(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/agent-images/img-1", `{
		"id":"img-1","displayName":"forgejo-developer-apps","imageURL":"registry/developer:1",
		"toolCount":2,
		"tools":[
			{"name":"mcp__forgejo-mcp-apps__create_issue","kind":"mcp","mcpSource":"forgejo-mcp-apps","enabled":true},
			{"name":"mcp__plugin-forgejo__whoami","kind":"mcp","mcpSource":"plugin-forgejo","enabled":false}
		],
		"env":[{"name":"FORGEJO_TOKEN","value":"","secret":true}],
		"mcpServers":[{"name":"forgejo-mcp-apps","url":"https://forgejo/api/mcp"},{"name":"plugin-forgejo","url":"http://plugin/api/mcp"}],
		"status":{"phase":"Ready"}
	}`)

	c := h.client()
	img, err := c.GetAgentImage(WithBackground(context.Background()), "img-1")
	if err != nil {
		t.Fatalf("GetAgentImage: %v", err)
	}

	if img.ID != "img-1" || img.DisplayName != "forgejo-developer-apps" {
		t.Errorf("image = %+v, want img-1/forgejo-developer-apps", img)
	}
	if img.ImageURL != "registry/developer:1" {
		t.Errorf("imageURL = %q, want the image the install runs on", img.ImageURL)
	}
	if len(img.Tools) != 2 || !img.Tools[0].Enabled || img.Tools[1].Enabled {
		t.Errorf("tools = %+v, want the catalog with its enabled flags", img.Tools)
	}
	if got, want := fmt.Sprint(img.Env), "[FORGEJO_TOKEN]"; got != want {
		t.Errorf("env = %s, want %s — names only, a stored value never comes back", got, want)
	}
	if got, want := fmt.Sprint(img.MCPServers), "[forgejo-mcp-apps plugin-forgejo]"; got != want {
		t.Errorf("mcpServers = %s, want %s", got, want)
	}
}

// ptrTo is a test's way of setting an ImageUpdate or AgentUpdate field, where a pointer is
// the difference between "set this" and "keep what is stored".
func ptrTo[T any](v T) *T { return &v }

// TestGetAgent reads back an agent with all its managed state.
func TestGetAgent(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/agents/a-1", `{
		"id":"a-1","name":"architect","description":"the role that architects solutions",
		"imageRef":{"name":"img-1","displayName":"forgejo-developer-apps"},
		"persona":{"id":"p-1"},
		"llm":{"model":"claude-3-5-sonnet-20241022","provider":"anthropic"},
		"env":[{"name":"FORGEJO_TOKEN","value":"","secret":true}],
		"mcp":{"servers":[{"name":"forgejo-mcp-apps","url":"https://forgejo/api/mcp","tokenFromEnv":""}]},
		"skills":["forgejo-forge","forgejo-forgejo"],
		"ollamaCloud":{"apiKey":""},
		"openCode":{"apiKey":""},
		"alibabaCloud":{"apiKey":""},
		"customProvider":{"url":"","apiKey":""}
	}`)

	c := h.client()
	agent, err := c.GetAgent(WithCredential(context.Background(), "ainselp_user"), "a-1")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}

	if agent.ID != "a-1" || agent.Name != "architect" {
		t.Errorf("agent = %+v, want a-1/architect", agent)
	}
	if agent.ImageRef != "img-1" || agent.PersonaID != "p-1" {
		t.Errorf("imageRef/persona = %q/%q, want img-1/p-1", agent.ImageRef, agent.PersonaID)
	}
	if agent.LLM == nil || agent.LLM.Model != "claude-3-5-sonnet-20241022" {
		t.Errorf("llm = %+v, want the model", agent.LLM)
	}
	if len(agent.MCPServers) != 1 || agent.MCPServers[0] != "forgejo-mcp-apps" {
		t.Errorf("mcp = %v, want forgejo-mcp-apps", agent.MCPServers)
	}
	if got, want := fmt.Sprint(agent.Skills), "[forgejo-forge forgejo-forgejo]"; got != want {
		t.Errorf("skills = %s, want %s — the hub answers with a plain array", got, want)
	}

	calls := h.calls(http.MethodGet, "/api/v1/agents/a-1")
	if len(calls) != 1 {
		t.Fatalf("hub was called %d times, want 1", len(calls))
	}
	if got, want := calls[0].Auth, "Bearer ainselp_user"; got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
}

// TestGetAgentWithoutSkillsIsNoSkills is the other half of the read-back: an agent whose
// skills the hub left out reads as none rather than as an error, which is what an agent
// created without a selection looks like. The old wrapper shape made every read of an agent
// that did have skills fail, so both cases have to be asserted.
func TestGetAgentWithoutSkillsIsNoSkills(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/agents/a-1", `{
		"id":"a-1","name":"architect",
		"imageRef":{"name":"img-1"}
	}`)

	c := h.client()
	agent, err := c.GetAgent(WithBackground(context.Background()), "a-1")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if agent.Skills != nil {
		t.Errorf("skills = %v, want none for an agent the hub answers without the field", agent.Skills)
	}
}

// TestUpdateAgent changes an agent's properties with provider keys.
func TestUpdateAgent(t *testing.T) {
	h := newHubServer(t)
	h.reply(http.MethodPut, "/api/v1/agents/a-1", http.StatusOK, "")

	c := h.client()
	err := c.UpdateAgent(WithBackground(context.Background()), "a-1", AgentUpdate{
		Name:           ptrTo("The Critic"),
		ImageRef:       ptrTo("img-2"),
		OllamaCloudKey: ptrTo("ollama-key"),
		CustomURL:      ptrTo("https://custom.example.com/v1"),
		CustomKey:      ptrTo("custom-key"),
	})
	if err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}

	body := map[string]any{}
	raw := h.calls(http.MethodPut, "/api/v1/agents/a-1")[0].Body
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("body is not JSON: %s", raw)
	}
	if body["name"] != "The Critic" {
		t.Errorf("name = %v, want the display name the admin chose", body["name"])
	}
	if img, ok := body["imageRef"].(map[string]any); !ok || img["name"] != "img-2" {
		t.Errorf("imageRef = %v, want img-2", body["imageRef"])
	}
	if ollama, ok := body["ollamaCloud"].(map[string]any); !ok || ollama["apiKey"] != "ollama-key" {
		t.Errorf("ollamaCloud = %v, want the key in request", body["ollamaCloud"])
	}
	if custom, ok := body["customProvider"].(map[string]any); !ok {
		t.Errorf("customProvider = %v, want custom provider", body["customProvider"])
	} else {
		if custom["url"] != "https://custom.example.com/v1" || custom["apiKey"] != "custom-key" {
			t.Errorf("customProvider = %v, want url and key", custom)
		}
	}
}

// TestUpdateAgentSendsSkillsAsAPlainList is the update side of the skills shape: the
// hub takes a plain list of ids and answers the old {items: [...]} wrapper with a 400.
// An explicit empty list is sent as [] (clear every skill), and a nil update leaves
// the key out so the hub keeps what the agent has.
func TestUpdateAgentSendsSkillsAsAPlainList(t *testing.T) {
	for _, tc := range []struct {
		name   string
		skills *[]string
		want   string
	}{
		{"set", &[]string{"forgejo-forgejo"}, `["forgejo-forgejo"]`},
		{"clear", &[]string{}, `[]`},
		{"keep", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHubServer(t)
			h.reply(http.MethodPut, "/api/v1/agents/a-1", http.StatusOK, "")

			c := h.client()
			if err := c.UpdateAgent(WithBackground(context.Background()), "a-1", AgentUpdate{Skills: tc.skills}); err != nil {
				t.Fatalf("UpdateAgent: %v", err)
			}

			var body map[string]json.RawMessage
			raw := h.calls(http.MethodPut, "/api/v1/agents/a-1")[0].Body
			if err := json.Unmarshal([]byte(raw), &body); err != nil {
				t.Fatalf("body is not JSON: %s", raw)
			}
			got, ok := body["skills"]
			if tc.want == "" {
				if ok {
					t.Errorf("body carries skills %s, want it absent", got)
				}
				return
			}
			if string(got) != tc.want {
				t.Errorf("skills = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestUpdatePersonaSendsOnlyText is the absent-versus-kept rule on a persona: rewriting
// what the persona says sends the text and nothing else, so the name and description the
// hub has survive. A persona's text names the forge the install works in, which is the
// reason an edit has to be able to write it again.
func TestUpdatePersonaSendsOnlyText(t *testing.T) {
	h := newHubServer(t)
	h.reply(http.MethodPut, "/api/v1/personas/p-1", http.StatusOK, "")

	c := h.client()
	err := c.UpdatePersona(WithBackground(context.Background()), "p-1", "You refine issues at http://forgejo.example.")
	if err != nil {
		t.Fatalf("UpdatePersona: %v", err)
	}

	calls := h.calls(http.MethodPut, "/api/v1/personas/p-1")
	if len(calls) != 1 {
		t.Fatalf("hub was called %d times, want 1", len(calls))
	}
	body := map[string]any{}
	if err := json.Unmarshal([]byte(calls[0].Body), &body); err != nil {
		t.Fatalf("body is not JSON: %s", calls[0].Body)
	}
	if body["text"] != "You refine issues at http://forgejo.example." {
		t.Errorf("text = %v, want the new text", body["text"])
	}
	if _, ok := body["name"]; ok {
		t.Errorf("body = %v, want no name: the hub keeps what a request leaves out", body)
	}
}

// TestDeleteAgent deletes an agent.
func TestDeleteAgent(t *testing.T) {
	h := newHubServer(t)
	h.reply(http.MethodDelete, "/api/v1/agents/a-1", http.StatusOK, "")

	c := h.client()
	err := c.DeleteAgent(WithBackground(context.Background()), "a-1")
	if err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}

	calls := h.calls(http.MethodDelete, "/api/v1/agents/a-1")
	if len(calls) != 1 {
		t.Fatalf("hub was called %d times, want 1", len(calls))
	}
}

// TestDeleteAgentImage deletes an agent image.
func TestDeleteAgentImage(t *testing.T) {
	h := newHubServer(t)
	h.reply(http.MethodDelete, "/api/v1/agent-images/img-1", http.StatusOK, "")

	c := h.client()
	err := c.DeleteAgentImage(WithBackground(context.Background()), "img-1")
	if err != nil {
		t.Fatalf("DeleteAgentImage: %v", err)
	}

	calls := h.calls(http.MethodDelete, "/api/v1/agent-images/img-1")
	if len(calls) != 1 {
		t.Fatalf("hub was called %d times, want 1", len(calls))
	}
}

// TestDeletePersona deletes a persona.
func TestDeletePersona(t *testing.T) {
	h := newHubServer(t)
	h.reply(http.MethodDelete, "/api/v1/personas/p-1", http.StatusOK, "")

	c := h.client()
	err := c.DeletePersona(WithBackground(context.Background()), "p-1")
	if err != nil {
		t.Fatalf("DeletePersona: %v", err)
	}

	calls := h.calls(http.MethodDelete, "/api/v1/personas/p-1")
	if len(calls) != 1 {
		t.Fatalf("hub was called %d times, want 1", len(calls))
	}
}

// TestDeleteConnector deletes a connector.
func TestDeleteConnector(t *testing.T) {
	h := newHubServer(t)
	h.reply(http.MethodDelete, "/api/v1/connectors/c-1", http.StatusOK, "")

	c := h.client()
	err := c.DeleteConnector(WithBackground(context.Background()), "c-1")
	if err != nil {
		t.Fatalf("DeleteConnector: %v", err)
	}

	calls := h.calls(http.MethodDelete, "/api/v1/connectors/c-1")
	if len(calls) != 1 {
		t.Fatalf("hub was called %d times, want 1", len(calls))
	}
}

// TestDeleteMCPServer deletes an MCP server.
func TestDeleteMCPServer(t *testing.T) {
	h := newHubServer(t)
	h.reply(http.MethodDelete, "/api/v1/mcp-servers/forgejo-mcp", http.StatusOK, "")

	c := h.client()
	err := c.DeleteMCPServer(WithBackground(context.Background()), "forgejo-mcp")
	if err != nil {
		t.Fatalf("DeleteMCPServer: %v", err)
	}

	calls := h.calls(http.MethodDelete, "/api/v1/mcp-servers/forgejo-mcp")
	if len(calls) != 1 {
		t.Fatalf("hub was called %d times, want 1", len(calls))
	}
}

// TestDeleteTrigger deletes a trigger.
func TestDeleteTrigger(t *testing.T) {
	h := newHubServer(t)
	h.reply(http.MethodDelete, "/api/v1/triggers/t-1", http.StatusOK, "")

	c := h.client()
	err := c.DeleteTrigger(WithBackground(context.Background()), "t-1")
	if err != nil {
		t.Fatalf("DeleteTrigger: %v", err)
	}

	calls := h.calls(http.MethodDelete, "/api/v1/triggers/t-1")
	if len(calls) != 1 {
		t.Fatalf("hub was called %d times, want 1", len(calls))
	}
}

// TestRotateConnectorSecret rotates a connector's webhook secret.
func TestRotateConnectorSecret(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/connectors/c-1/rotate-secret", http.StatusOK, `{
		"id":"c-1","name":"forgejo","webhookEndpoint":"https://hub.example.com/api/connectors/c-1/webhook",
		"webhookSecretValue":"new-secret-value"
	}`)

	c := h.client()
	conn, err := c.RotateConnectorSecret(WithBackground(context.Background()), "c-1")
	if err != nil {
		t.Fatalf("RotateConnectorSecret: %v", err)
	}

	if conn.ID != "c-1" || conn.Name != "forgejo" {
		t.Errorf("connector = %+v, want c-1/forgejo", conn)
	}
	if conn.WebhookSecretValue != "new-secret-value" {
		t.Errorf("secret = %q, want new-secret-value", conn.WebhookSecretValue)
	}

	calls := h.calls(http.MethodPost, "/api/v1/connectors/c-1/rotate-secret")
	if len(calls) != 1 {
		t.Fatalf("hub was called %d times, want 1", len(calls))
	}
}

// TestEvents reads events for a connector since a moment.
func TestEvents(t *testing.T) {
	h := newHubServer(t)
	h.route(http.MethodGet, "/api/v1/events", func(w http.ResponseWriter, r *http.Request, n int) {
		w.WriteHeader(http.StatusOK)
		body := `{
			"events":[
				{"id":"ev-1","type":"push","timestamp":"2025-01-01T10:00:00Z"},
				{"id":"ev-2","type":"issue","timestamp":"2025-01-01T11:00:00Z"}
			],"total":2
		}`
		_, _ = w.Write([]byte(body))
	})

	c := h.client()
	since, _ := time.Parse(time.RFC3339, "2025-01-01T09:00:00Z")
	events, err := c.Events(WithBackground(context.Background()), "c-1", since)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("events = %d items, want 2", len(events))
	}
	if events[0].ID != "ev-1" || events[0].Type != "push" {
		t.Errorf("events[0] = %+v, want ev-1/push", events[0])
	}

	calls := h.calls(http.MethodGet, "/api/v1/events")
	if len(calls) != 1 {
		t.Fatalf("hub was called %d times, want 1", len(calls))
	}
	if got := calls[0].Query.Get("connector"); got != "c-1" {
		t.Errorf("connector query = %q, want c-1", got)
	}
	if got := calls[0].Query.Get("since"); got == "" {
		t.Errorf("since query is empty, want RFC3339 timestamp")
	}
}

// TestPluginGroupsUsesBackgroundCredential checks that PluginGroups uses the background
// credential even when the context carries a user credential.
func TestPluginGroupsUsesBackgroundCredential(t *testing.T) {
	h := newHubServer(t)
	h.route(http.MethodGet, "/api/v1/plugin-context", func(w http.ResponseWriter, r *http.Request, n int) {
		// The test verifies the credential used is the background token, not the user token.
		auth := r.Header.Get("Authorization")
		if !strings.Contains(auth, "ainselp_bg") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
		body := `{"pluginId":"forgejo","kind":"plugin","groups":[{"id":"g-1","name":"team-a","role":"member"}]}`
		_, _ = w.Write([]byte(body))
	})

	c := h.client()
	// Call with a user credential in the context.
	groups, err := c.PluginGroups(WithCredential(context.Background(), "ainselp_user"))
	if err != nil {
		t.Fatalf("PluginGroups: %v", err)
	}

	if len(groups) != 1 || groups[0] != "g-1" {
		t.Errorf("groups = %v, want [g-1]", groups)
	}

	calls := h.calls(http.MethodGet, "/api/v1/plugin-context")
	if len(calls) != 1 {
		t.Fatalf("hub was called %d times, want 1", len(calls))
	}
	// Verify that the background token was used, not the user token.
	if !strings.Contains(calls[0].Auth, "ainselp_bg") {
		t.Errorf("Authorization = %q, want background token ainselp_bg", calls[0].Auth)
	}
}

// TestPluginGroupsWithoutAccessIsEmpty checks that a plugin with no group access gets an
// empty list, not nil: the setup options pass it to the UI as JSON, where nil is null and
// the wizard calls .includes on it.
func TestPluginGroupsWithoutAccessIsEmpty(t *testing.T) {
	h := newHubServer(t)
	h.route(http.MethodGet, "/api/v1/plugin-context", func(w http.ResponseWriter, r *http.Request, n int) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"pluginId":"forgejo","kind":"plugin","groups":null}`))
	})

	groups, err := h.client().PluginGroups(context.Background())
	if err != nil {
		t.Fatalf("PluginGroups: %v", err)
	}
	b, _ := json.Marshal(groups)
	if string(b) != "[]" {
		t.Errorf("groups marshal to %s, want []", b)
	}
}

// TestPluginGroupsRespectsCancelledContext verifies that PluginGroups returns promptly
// when the context is cancelled, preserving the caller's deadline and cancellation even
// while switching to the background credential.
func TestPluginGroupsRespectsCancelledContext(t *testing.T) {
	h := newHubServer(t)
	// Handler that waits for a signal before responding, so we can verify the
	// request is cancelled before the response is ready.
	ready := make(chan struct{})
	h.route(http.MethodGet, "/api/v1/plugin-context", func(w http.ResponseWriter, r *http.Request, n int) {
		close(ready)
		// Wait for the context to be cancelled
		<-r.Context().Done()
		w.WriteHeader(http.StatusRequestTimeout)
	})

	c := h.client()
	ctx, cancel := context.WithCancel(context.Background())

	// Start the call in a goroutine
	done := make(chan error, 1)
	go func() {
		_, err := c.PluginGroups(WithCredential(ctx, "ainselp_user"))
		done <- err
	}()

	// Wait for the request to be ready
	<-ready

	// Cancel the context
	cancel()

	// The call should return quickly with the cancelled context error
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("PluginGroups returned error %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("PluginGroups did not return after context was cancelled")
	}
}

// TestListCronTriggersReadsTheHubList checks the read the reconcile loop and the
// removal make: the hub's whole cron list, decoded into setup.CronTrigger — the
// status the hub adds is left off, the fields the plugin matches on come back.
func TestListCronTriggersReadsTheHubList(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/cron-triggers", pageOf(`[{"id":"c-9","name":"forgejo-developer-pr-patrol-apps",
		"agentRef":"a-developer","schedule":"*/30 * * * *","prompt":"Scheduled PR patrol.",
		"enabled":true,"status":{"agentValid":true,"scheduleValid":true,"ready":true}}]`, 1))

	c := h.client()
	cronTriggers, err := c.ListCronTriggers(WithBackground(context.Background()))
	if err != nil {
		t.Fatalf("ListCronTriggers: %v", err)
	}
	if len(cronTriggers) != 1 {
		t.Fatalf("got %d cron triggers, want 1", len(cronTriggers))
	}
	got := cronTriggers[0]
	if got.ID != "c-9" || got.Name != "forgejo-developer-pr-patrol-apps" ||
		got.AgentRef != "a-developer" || got.Schedule != "*/30 * * * *" || !got.Enabled {
		t.Errorf("cron trigger = %+v, want the listed one", got)
	}
}

// TestCreateCronTriggerFilesItUnderAGroup checks the create the apply makes: the
// trigger's fields plus the group, no id (the hub generates it), and the created
// id comes back — the delete needs it.
func TestCreateCronTriggerFilesItUnderAGroup(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/cron-triggers", http.StatusCreated,
		`{"id":"c-9","name":"forgejo-developer-pr-patrol-apps","schedule":"*/30 * * * *"}`)

	c := h.client()
	ref, err := c.CreateCronTrigger(WithBackground(context.Background()), "g-1", setup.CronTrigger{
		Name:     "forgejo-developer-pr-patrol-apps",
		AgentRef: "a-developer",
		Schedule: "*/30 * * * *",
		Prompt:   "Scheduled PR patrol.",
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("CreateCronTrigger: %v", err)
	}
	if ref.ID != "c-9" || ref.Name != "forgejo-developer-pr-patrol-apps" {
		t.Errorf("ref = %+v, want c-9 with the patrol name", ref)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(h.calls(http.MethodPost, "/api/v1/cron-triggers")[0].Body), &body); err != nil {
		t.Fatalf("body is not JSON: %s", h.calls(http.MethodPost, "/api/v1/cron-triggers")[0].Body)
	}
	if body["groupId"] != "g-1" || body["name"] != "forgejo-developer-pr-patrol-apps" ||
		body["agentRef"] != "a-developer" || body["schedule"] != "*/30 * * * *" {
		t.Errorf("body = %v, want the trigger filed under g-1 with its fields", body)
	}
	if _, ok := body["id"]; ok {
		t.Errorf("body carries an id: a create has none — the hub generates it")
	}
	if body["enabled"] != true {
		t.Errorf("body enabled = %v, want true", body["enabled"])
	}
}

// TestDeleteCronTriggerDeletesByID checks the removal's delete: by the generated
// id a list carried — never by display name, which is not a lookup.
func TestDeleteCronTriggerDeletesByID(t *testing.T) {
	h := newHubServer(t)
	h.reply(http.MethodDelete, "/api/v1/cron-triggers/c-9", http.StatusNoContent, "")

	c := h.client()
	if err := c.DeleteCronTrigger(WithBackground(context.Background()), "c-9"); err != nil {
		t.Fatalf("DeleteCronTrigger: %v", err)
	}
	if got := h.calls(http.MethodDelete, "/api/v1/cron-triggers/c-9"); len(got) != 1 {
		t.Errorf("delete calls = %d, want 1", len(got))
	}
}

// TestUpdateCronTriggerSendsOnlyWhatMoved is the wire the patrol's cadence and enablement
// ride on. Three fields are always sent, because the hub's update takes them: the name, the
// agent and the prompt — which is also how a prompt an admin edited on the Schedules UI
// survives a write that came to change the cadence. The two the install may mean to move are
// absent when it means to leave them, and the case that matters is enabled:false: a false
// written as an empty value would be dropped by the encoder and leave a patrol the admin
// switched off running.
func TestUpdateCronTriggerSendsOnlyWhatMoved(t *testing.T) {
	h := newHubServer(t)
	h.reply(http.MethodPut, "/api/v1/cron-triggers/c-9", http.StatusOK, `{"id":"c-9"}`)

	c := h.client()
	schedule := "0 */2 * * *"

	// The cadence moved, the enablement did not.
	if err := c.UpdateCronTrigger(WithBackground(context.Background()), "c-9", CronTriggerUpdate{
		Name: "forgejo-developer-pr-patrol-apps", AgentRef: "a-developer",
		Prompt: "The hub's own prompt.", Schedule: &schedule,
	}); err != nil {
		t.Fatalf("UpdateCronTrigger: %v", err)
	}
	var moved map[string]any
	if err := json.Unmarshal([]byte(h.calls(http.MethodPut, "/api/v1/cron-triggers/c-9")[0].Body), &moved); err != nil {
		t.Fatalf("body is not JSON: %s", h.calls(http.MethodPut, "/api/v1/cron-triggers/c-9")[0].Body)
	}
	if moved["name"] != "forgejo-developer-pr-patrol-apps" || moved["agentRef"] != "a-developer" ||
		moved["prompt"] != "The hub's own prompt." || moved["schedule"] != "0 */2 * * *" {
		t.Errorf("body = %v, want the three required fields and the new schedule", moved)
	}
	if _, ok := moved["enabled"]; ok {
		t.Errorf("body carries enabled = %v, want it absent: an apply that retuned the cadence did not ask to switch the patrol", moved["enabled"])
	}

	// The enablement moved, the cadence did not.
	if err := c.UpdateCronTrigger(WithBackground(context.Background()), "c-9", CronTriggerUpdate{
		Name: "forgejo-developer-pr-patrol-apps", AgentRef: "a-developer",
		Prompt: "The hub's own prompt.", Enabled: new(false),
	}); err != nil {
		t.Fatalf("UpdateCronTrigger: %v", err)
	}
	var off map[string]any
	if err := json.Unmarshal([]byte(h.calls(http.MethodPut, "/api/v1/cron-triggers/c-9")[1].Body), &off); err != nil {
		t.Fatalf("body is not JSON: %s", h.calls(http.MethodPut, "/api/v1/cron-triggers/c-9")[1].Body)
	}
	v, ok := off["enabled"]
	if !ok || v != false {
		t.Errorf("body enabled = %v (present: %v), want an explicit false", v, ok)
	}
	if _, ok := off["schedule"]; ok {
		t.Errorf("body carries schedule = %v, want it absent: switching the patrol off is not retuning it", off["schedule"])
	}
}

// TestCreateAgentSendsTheAuthMethod is the sign-in's create wire: the openAI block
// names authMethod and no apiKey, because the hub refuses a block that carries both.
// A spec that names neither leaves the block absent, as it was.
func TestCreateAgentSendsTheAuthMethod(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/agents", http.StatusCreated, `{"id":"a-1","name":"a"}`)

	c := h.client()
	if _, err := c.CreateAgent(WithBackground(context.Background()), AgentSpec{
		Name: "a", GroupID: "g-1", ImageRef: "ai-1",
		LLM:              &LLMSpec{Provider: "openai", Model: "m"},
		OpenAIAuthMethod: "ChatGPTSignIn",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	var body map[string]any
	raw := h.calls(http.MethodPost, "/api/v1/agents")[0].Body
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("body is not JSON: %s", raw)
	}
	block, ok := body["openAI"].(map[string]any)
	if !ok {
		t.Fatalf("body has no openAI block: %s", raw)
	}
	if block["authMethod"] != "ChatGPTSignIn" {
		t.Errorf("openAI.authMethod = %v, want ChatGPTSignIn", block["authMethod"])
	}
	if _, ok := block["apiKey"]; ok {
		t.Errorf("openAI carries apiKey: %s — the hub refuses a sign-in block with a key", raw)
	}
}

// TestUpdateAgentSendsTheAuthMethodAlone: a method-only update is the move onto the
// sign-in. The block rides with the method and no apiKey, so the hub does not read
// the write as "set the key to nothing".
func TestUpdateAgentSendsTheAuthMethodAlone(t *testing.T) {
	h := newHubServer(t)
	h.reply(http.MethodPut, "/api/v1/agents/a-1", http.StatusOK, "")

	c := h.client()
	if err := c.UpdateAgent(WithBackground(context.Background()), "a-1", AgentUpdate{
		OpenAIAuthMethod: ptrTo("ChatGPTSignIn"),
	}); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}

	var body map[string]any
	raw := h.calls(http.MethodPut, "/api/v1/agents/a-1")[0].Body
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("body is not JSON: %s", raw)
	}
	block, ok := body["openAI"].(map[string]any)
	if !ok {
		t.Fatalf("body has no openAI block: %s", raw)
	}
	if block["authMethod"] != "ChatGPTSignIn" {
		t.Errorf("openAI.authMethod = %v, want ChatGPTSignIn", block["authMethod"])
	}
	if _, ok := block["apiKey"]; ok {
		t.Errorf("openAI carries apiKey: %s — an unnamed key has to stay absent", raw)
	}
}

// TestUpdateAgentNamesNoMethodLeavesItAlone is the keep-on-omit rule the whole
// update wire runs on: an update that says nothing about the method sends no block,
// so an agent that signed in keeps its sign-in through a key rotation.
func TestUpdateAgentNamesNoMethodLeavesItAlone(t *testing.T) {
	h := newHubServer(t)
	h.reply(http.MethodPut, "/api/v1/agents/a-1", http.StatusOK, "")

	c := h.client()
	if err := c.UpdateAgent(WithBackground(context.Background()), "a-1", AgentUpdate{
		OpenAIKey: ptrTo("k-rotated"),
	}); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}

	raw := h.calls(http.MethodPut, "/api/v1/agents/a-1")[0].Body
	if strings.Contains(raw, "authMethod") {
		t.Errorf("body names authMethod: %s — an unnamed method has to stay absent", raw)
	}
}

// TestGetAgentReadsTheAuthMethod is the read side the plan diffs against: the hub
// answers the method an agent holds, and defaults it for an agent that never named
// one — so an older hub's answer reads as the API key, not as a change to make.
func TestGetAgentReadsTheAuthMethod(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want string
	}{
		{"sign-in", `{"id":"a-1","name":"a","openAI":{"authMethod":"ChatGPTSignIn"}}`, "ChatGPTSignIn"},
		{"api key", `{"id":"a-1","name":"a","openAI":{"apiKey":"k"}}`, "APIKey"},
		{"no block at all", `{"id":"a-1","name":"a"}`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHubServer(t)
			h.get("/api/v1/agents/a-1", tt.body)

			agent, err := h.client().GetAgent(WithBackground(context.Background()), "a-1")
			if err != nil {
				t.Fatalf("GetAgent: %v", err)
			}
			if agent.OpenAIAuthMethod != tt.want {
				t.Errorf("OpenAIAuthMethod = %q, want %q", agent.OpenAIAuthMethod, tt.want)
			}
		})
	}
}

// TestCreateAgentSendsTheWindowItWasGiven pins the create body: a window goes out as the
// object the hub takes, and no window goes out as no key at all. The second half is the
// default the wizard relies on — an agent created without one is the hub's always-on, not
// a window the plugin invented.
func TestCreateAgentSendsTheWindowItWasGiven(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/agents", http.StatusCreated, `{"id":"a-9","name":"apps-developer"}`)

	c := h.client()
	if _, err := c.CreateAgent(WithBackground(context.Background()), AgentSpec{
		Name:     "apps-developer",
		GroupID:  "g-1",
		ImageRef: "ai-1",
		WorkingHours: &WorkingHours{
			Timezone: "Europe/Berlin", Days: []int{1, 2, 3, 4, 5}, Start: "09:00", End: "17:00",
		},
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := c.CreateAgent(WithBackground(context.Background()), AgentSpec{
		Name: "apps-other", GroupID: "g-1", ImageRef: "ai-1",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	calls := h.calls(http.MethodPost, "/api/v1/agents")
	if len(calls) != 2 {
		t.Fatalf("hub was called %d times, want 2", len(calls))
	}

	var set map[string]any
	if err := json.Unmarshal([]byte(calls[0].Body), &set); err != nil {
		t.Fatalf("body 1 is not JSON: %s", calls[0].Body)
	}
	want := map[string]any{
		"timezone": "Europe/Berlin",
		"days":     []any{float64(1), float64(2), float64(3), float64(4), float64(5)},
		"start":    "09:00",
		"end":      "17:00",
	}
	if !reflect.DeepEqual(set["workingHours"], want) {
		t.Errorf("workingHours = %v, want %v", set["workingHours"], want)
	}

	var unset map[string]any
	if err := json.Unmarshal([]byte(calls[1].Body), &unset); err != nil {
		t.Fatalf("body 2 is not JSON: %s", calls[1].Body)
	}
	if v, ok := unset["workingHours"]; ok {
		t.Errorf("body 2 carries workingHours %v, want no key at all: an agent the form set no window for"+
			" is the hub's always-on, not a window of the plugin's own", v)
	}
}

// TestUpdateAgentWorkingHoursFollowsTheHubsRule is the contract the wizard's clear is built
// on, and the reason a clear is the empty object rather than a null or an omitted key: the
// hub keeps an absent field, replaces a set one, and removes the window only for the empty
// object. Every body is asserted, because the difference between them is the whole
// difference between leaving a window an admin set in the console alone and erasing it.
func TestUpdateAgentWorkingHoursFollowsTheHubsRule(t *testing.T) {
	cases := []struct {
		name string
		spec AgentUpdate
		want string
	}{
		{
			name: "a write is the window",
			spec: AgentUpdate{WorkingHours: &WorkingHours{
				Timezone: "Europe/Berlin", Days: []int{1, 2, 3, 4, 5}, Start: "09:00", End: "17:00",
			}},
			want: `{"workingHours":{"timezone":"Europe/Berlin","days":[1,2,3,4,5],"start":"09:00","end":"17:00"}}`,
		},
		{
			name: "a clear is the empty object",
			spec: AgentUpdate{ClearWorkingHours: true},
			want: `{"workingHours":{}}`,
		},
		{
			// The re-apply's body. A plan with no op for the field reaches the hub with no
			// mention of it, which is what leaves whatever the console set sitting where it is.
			name: "neither is no key",
			spec: AgentUpdate{Name: ptrTo("The Refiner")},
			want: `{"name":"The Refiner"}`,
		},
		{
			// A clear wins over a write rather than sending both: an object alongside it would
			// read as a write and leave the window in place.
			name: "a clear beats a write",
			spec: AgentUpdate{WorkingHours: &WorkingHours{Timezone: "UTC"}, ClearWorkingHours: true},
			want: `{"workingHours":{}}`,
		},
	}

	h := newHubServer(t)
	h.reply(http.MethodPut, "/api/v1/agents/a-1", http.StatusOK, "")

	c := h.client()
	for _, tc := range cases {
		if err := c.UpdateAgent(WithBackground(context.Background()), "a-1", tc.spec); err != nil {
			t.Fatalf("UpdateAgent (%s): %v", tc.name, err)
		}
	}

	calls := h.calls(http.MethodPut, "/api/v1/agents/a-1")
	if len(calls) != len(cases) {
		t.Fatalf("hub was called %d times, want %d", len(calls), len(cases))
	}
	for i, tc := range cases {
		if calls[i].Body != tc.want {
			t.Errorf("%s: body = %s, want %s", tc.name, calls[i].Body, tc.want)
		}
	}
}

// TestGetAgentReadsTheWindowTheHubAnswers is the read half: the window the hub holds, and
// the open/closes-at/next-open-at it computes from it. The wizard does not read this to
// decide what to write — the install row is its baseline — but the console's screen is what
// an admin checks an apply against, so a window that reads back wrong reads as an apply that
// did not happen.
func TestGetAgentReadsTheWindowTheHubAnswers(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/agents/a-1", `{
		"id":"a-1","name":"architect","imageRef":{"name":"img-1"},
		"workingHours":{"timezone":"Europe/Berlin","days":[1,2,3,4,5],"start":"09:00","end":"17:00"},
		"hoursOpen":true,"hoursClosesAt":"2026-07-21T15:00:00Z","hoursNextOpenAt":"2026-07-22T07:00:00Z"
	}`)

	c := h.client()
	agent, err := c.GetAgent(WithBackground(context.Background()), "a-1")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if agent.WorkingHours == nil {
		t.Fatalf("workingHours = nil, want the window the hub answered")
	}
	if agent.WorkingHours.Timezone != "Europe/Berlin" || agent.WorkingHours.Start != "09:00" ||
		agent.WorkingHours.End != "17:00" {
		t.Errorf("window = %+v, want the hub's zone and bounds", *agent.WorkingHours)
	}
	if got, want := fmt.Sprint(agent.WorkingHours.Days), "[1 2 3 4 5]"; got != want {
		t.Errorf("days = %s, want %s", got, want)
	}
	if !agent.HoursOpen {
		t.Errorf("hoursOpen = false, want true")
	}
	// The two stamps stay as the hub answered them — RFC3339 text, empty when the agent
	// has no window — because nothing in the plugin compares them; the console's screen is
	// the only reader, and it prints them.
	if agent.HoursClosesAt != "2026-07-21T15:00:00Z" {
		t.Errorf("hoursClosesAt = %q, want the hub's stamp as it answered it", agent.HoursClosesAt)
	}
	if agent.HoursNextOpenAt != "2026-07-22T07:00:00Z" {
		t.Errorf("hoursNextOpenAt = %q, want the hub's stamp as it answered it", agent.HoursNextOpenAt)
	}
}

// TestGetAgentWithoutAWindowIsAlwaysOn is the same read on an agent that never got one: no
// window, and no claim about when it is open — the hub's answer, not a zero the client
// filled in.
func TestGetAgentWithoutAWindowIsAlwaysOn(t *testing.T) {
	h := newHubServer(t)
	h.get("/api/v1/agents/a-1", `{"id":"a-1","name":"architect","imageRef":{"name":"img-1"}}`)

	c := h.client()
	agent, err := c.GetAgent(WithBackground(context.Background()), "a-1")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if agent.WorkingHours != nil {
		t.Errorf("workingHours = %+v, want nil", *agent.WorkingHours)
	}
	if agent.HoursOpen {
		t.Errorf("hoursOpen = true, want false: nothing said this agent is open")
	}
	if agent.HoursClosesAt != "" || agent.HoursNextOpenAt != "" {
		t.Errorf("hours = %q/%q, want neither stamp", agent.HoursClosesAt, agent.HoursNextOpenAt)
	}
}

// -- managedBy stamp -------------------------------------------------------------------

// stampEnforcingCreate answers a create endpoint the way the hub answers a
// plugin-authenticated one: a managedBy that disagrees with the authenticated caller is
// a 400, and a non-plugin caller's managedBy is silently ignored. The body is read from
// the hubServer's record, because the shared handler has already drained the request.
// A test that lets the client's stamp drift away from the plugin id fails here, at the
// fake, rather than at a deployed hub.
func (h *hubServer) stampEnforcingCreate(path, reply string) func(http.ResponseWriter, *http.Request, int) {
	return func(w http.ResponseWriter, r *http.Request, _ int) {
		h.mu.Lock()
		var body string
		for i := len(h.seen) - 1; i >= 0; i-- {
			if h.seen[i].Method == http.MethodPost && h.seen[i].Path == path {
				body = h.seen[i].Body
				break
			}
		}
		h.mu.Unlock()

		var parsed map[string]any
		if err := json.Unmarshal([]byte(body), &parsed); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		stamp, _ := parsed["managedBy"].(string)
		asPlugin := r.Header.Get("Authorization") == "Bearer ainselp_bg"
		if asPlugin && stamp != managerStamp {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, `{"error":"managedBy must be %q for this caller"}`, managerStamp)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(reply))
	}
}

// TestWizardCreatesCarryTheManagerStamp is the wire half of #67: every resource kind
// the wizard creates — persona, agent image, agent, connector, MCP server — leaves on
// the plugin's own credential carrying "managedBy": "plugin-forgejo", and the
// stamp-enforcing fake hub accepts it. The credential is half the assertion, not
// decoration: the hub derives the stamp from the authenticated caller, so a create
// that carries the stamp but leaves as the console caller is silently left
// console-managed — the regression this test exists to catch.
func TestWizardCreatesCarryTheManagerStamp(t *testing.T) {
	h := newHubServer(t)
	h.route(http.MethodPost, "/api/v1/personas", h.stampEnforcingCreate("/api/v1/personas", `{"id":"p-9","name":"apps-refiner-persona"}`))
	h.route(http.MethodPost, "/api/v1/agent-images", h.stampEnforcingCreate("/api/v1/agent-images", `{"id":"img-9","displayName":"apps-refiner-image"}`))
	h.route(http.MethodPost, "/api/v1/agents", h.stampEnforcingCreate("/api/v1/agents", `{"id":"a-9","name":"apps-refiner"}`))
	h.route(http.MethodPost, "/api/v1/connectors", h.stampEnforcingCreate("/api/v1/connectors", `{"id":"c-9","name":"forgejo-test","webhookEndpoint":"https://hub.test/hooks/c-9","webhookSecretValue":"s"}`))
	h.route(http.MethodPost, "/api/v1/mcp-servers", h.stampEnforcingCreate("/api/v1/mcp-servers", `{"name":"forgejo-mcp-test","displayName":"Forgejo test","url":"https://plugin.test/mcp"}`))

	c := h.client()
	// The wizard's write context: the caller's deadline, no caller credential, marked
	// background — so the client mints and sends the plugin's own credential.
	ctx := WithBackground(WithoutCredential(context.Background()))

	calls := []struct {
		name, path string
		call       func(context.Context) error
	}{
		{"persona", "/api/v1/personas", func(ctx context.Context) error {
			_, err := c.CreatePersona(ctx, "g-1", "apps-refiner-persona", "d", "text")
			return err
		}},
		{"image", "/api/v1/agent-images", func(ctx context.Context) error {
			_, err := c.CreateAgentImage(ctx, ImageSpec{DisplayName: "apps-refiner-image", GroupID: "g-1", ImageURL: "img:1"})
			return err
		}},
		{"agent", "/api/v1/agents", func(ctx context.Context) error {
			_, err := c.CreateAgent(ctx, AgentSpec{Name: "apps-refiner", GroupID: "g-1", ImageRef: "img-9", PersonaID: "p-9"})
			return err
		}},
		{"connector", "/api/v1/connectors", func(ctx context.Context) error {
			_, err := c.CreateConnector(ctx, "g-1", "forgejo-test")
			return err
		}},
		{"mcp", "/api/v1/mcp-servers", func(ctx context.Context) error {
			_, err := c.RegisterMCPServer(ctx, MCPServerSpec{Name: "forgejo-mcp-test", GroupID: "g-1", DisplayName: "Forgejo test", URL: "https://plugin.test/mcp"})
			return err
		}},
	}
	for _, call := range calls {
		if err := call.call(ctx); err != nil {
			t.Fatalf("%s create: %v", call.name, err)
		}
		got := h.calls(http.MethodPost, call.path)
		last := got[len(got)-1]
		if !strings.Contains(last.Body, `"managedBy":"plugin-forgejo"`) {
			t.Errorf("%s create body = %s, want \"managedBy\":\"plugin-forgejo\"", call.name, last.Body)
		}
		if want := "Bearer ainselp_bg"; last.Auth != want {
			t.Errorf("%s create Authorization = %q, want the minted plugin credential %q: the hub stamps from the caller, not the body", call.name, last.Auth, want)
		}
	}
}

// TestCreateStampMismatchSurfacesHub400 proves the fake's rule bites: a create sent as
// the plugin whose stamp is not the plugin's stamp is the hub's 400, surfaced as a
// StatusError rather than retried or swallowed. This is what a stamp that drifted from
// the plugin id would meet on a real hub.
func TestCreateStampMismatchSurfacesHub400(t *testing.T) {
	h := newHubServer(t)
	h.route(http.MethodPost, "/api/v1/personas", h.stampEnforcingCreate("/api/v1/personas", `{"id":"p-9","name":"x"}`))

	c := h.client()
	// The client's own stamp is correct by construction, so the mismatch has to be
	// posted through the transport the client itself uses.
	cred, err := c.credential(WithBackground(WithoutCredential(context.Background())))
	if err != nil {
		t.Fatalf("credential: %v", err)
	}
	body := struct {
		Name      string `json:"name"`
		GroupID   string `json:"groupId"`
		ManagedBy string `json:"managedBy"`
	}{Name: "x", GroupID: "g-1", ManagedBy: "forgejo"}
	err = c.transport.PostJSON(context.Background(), cred, "/api/v1/personas", body, nil)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusBadRequest {
		t.Errorf("mismatched stamp = %v, want a 400 StatusError", err)
	}
}

// TestCreateAsCallerCarriesTheStampAnyway documents the harmless half: a create made
// on the caller's credential still carries the stamp — a user-kind caller's managedBy
// is silently ignored by the hub, so the field costs nothing and a later hub that
// honours it needs no client change.
func TestCreateAsCallerCarriesTheStampAnyway(t *testing.T) {
	h := newHubServer(t)
	h.post("/api/v1/personas", http.StatusCreated, `{"id":"p-9","name":"x"}`)

	c := h.client()
	if _, err := c.CreatePersona(WithCredential(context.Background(), "caller-cred"), "g-1", "x", "d", "t"); err != nil {
		t.Fatalf("CreatePersona: %v", err)
	}
	last := h.calls(http.MethodPost, "/api/v1/personas")[0]
	if !strings.Contains(last.Body, `"managedBy":"plugin-forgejo"`) {
		t.Errorf("body = %s, want the stamp present (the hub ignores it for a user caller)", last.Body)
	}
	if want := "Bearer caller-cred"; last.Auth != want {
		t.Errorf("Authorization = %q, want the caller's credential %q", last.Auth, want)
	}
}

// TestDeletesTolerateGoneResources: a removal that is retried — the row survives
// while anything is outstanding — re-attempts what an earlier pass already
// deleted. A hub delete of a gone resource reads as deleted, or the kept row
// could never clear. Any other non-success stays the error it is.
func TestDeletesTolerateGoneResources(t *testing.T) {
	ctx := WithBackground(context.Background())

	h := newHubServer(t)
	h.reply(http.MethodDelete, "/api/v1/agents/a-1", http.StatusNotFound, `{"error":"gone"}`)
	h.reply(http.MethodDelete, "/api/v1/connectors/c-1", http.StatusNotFound, `{"error":"gone"}`)
	h.reply(http.MethodDelete, "/api/v1/mcp-servers/m-1", http.StatusNotFound, `{"error":"gone"}`)
	h.reply(http.MethodDelete, "/api/v1/triggers/t-1", http.StatusNotFound, `{"error":"gone"}`)
	h.reply(http.MethodDelete, "/api/v1/cron-triggers/ct-1", http.StatusNotFound, `{"error":"gone"}`)
	h.reply(http.MethodDelete, "/api/v1/agent-images/img-1", http.StatusNotFound, `{"error":"gone"}`)
	h.reply(http.MethodDelete, "/api/v1/personas/p-1", http.StatusNotFound, `{"error":"gone"}`)

	c := h.client()
	for name, del := range map[string]func() error{
		"DeleteAgent":       func() error { return c.DeleteAgent(ctx, "a-1") },
		"DeleteConnector":   func() error { return c.DeleteConnector(ctx, "c-1") },
		"DeleteMCPServer":   func() error { return c.DeleteMCPServer(ctx, "m-1") },
		"DeleteTrigger":     func() error { return c.DeleteTrigger(ctx, "t-1") },
		"DeleteCronTrigger": func() error { return c.DeleteCronTrigger(ctx, "ct-1") },
		"DeleteAgentImage":  func() error { return c.DeleteAgentImage(ctx, "img-1") },
		"DeletePersona":     func() error { return c.DeletePersona(ctx, "p-1") },
	} {
		if err := del(); err != nil {
			t.Errorf("%s on a 404: %v, want nil", name, err)
		}
	}

	// A refusal is not "gone": only the 404 reads as deleted.
	h2 := newHubServer(t)
	h2.reply(http.MethodDelete, "/api/v1/agents/a-2", http.StatusConflict, `{"error":"stamped"}`)
	if err := h2.client().DeleteAgent(ctx, "a-2"); err == nil {
		t.Error("DeleteAgent on a 409: nil error, want the failure")
	}
}

// -- llm reachability check ----------------------------------------------------------

func TestCheckLLMAsksTheHubAndReadsOK(t *testing.T) {
	h := newHubServer(t)
	h.post(llmCheckPath, http.StatusOK, `{"ok":true}`)

	c := h.client()
	res, err := c.CheckLLM(WithCredential(context.Background(), "ainselp_user"), "ollama-cloud", "llama3.3")
	if err != nil {
		t.Fatalf("CheckLLM: %v", err)
	}
	if res.State != LLMCheckOK {
		t.Errorf("state = %q, want ok — the hub said the pair serves", res.State)
	}

	calls := h.calls(http.MethodPost, llmCheckPath)
	if len(calls) != 1 {
		t.Fatalf("the hub was asked %d times, want once", len(calls))
	}
	if got, want := calls[0].Body, `{"provider":"ollama-cloud","model":"llama3.3"}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
	if got, want := calls[0].Auth, "Bearer ainselp_user"; got != want {
		t.Errorf("auth = %q, want %q", got, want)
	}
}

func TestCheckLLMRefusedRouteIsUnknownNeverUnreachable(t *testing.T) {
	// A hub without the route (404/405) has not answered the question — the pair is
	// unknown, not broken. The whole point of the check is that a question nobody
	// answered never reads as health, and never reads as failure either.
	for _, code := range []int{
		http.StatusNotFound, http.StatusMethodNotAllowed,
	} {
		h := newHubServer(t)
		h.post(llmCheckPath, code, `{"error":"no such route"}`)

		res, err := h.client().CheckLLM(WithCredential(context.Background(), "ainselp_user"), "p", "m")
		if err != nil {
			t.Fatalf("CheckLLM(%d): %v", code, err)
		}
		if res.State != LLMCheckUnknown {
			t.Errorf("code %d: state = %q, want unknown", code, res.State)
		}
		if res.Reason == "" {
			t.Errorf("code %d: an unknown answer carries no reason", code)
		}
	}
}

func TestCheckLLMRefusedPermissionTellsTheAdminWhatToApprove(t *testing.T) {
	// 401/403 mean the route is there and the plugin's credential is not good for it:
	// the `llm:check` permission is not approved on the Plugin CR. That is still not an
	// answer about the provider — unknown, never unreachable — but the reason has to be
	// something an admin can act on, so it names the permission to approve and keeps
	// the hub's own sentence when the hub has one.
	for _, tc := range []struct {
		code int
		body string
		want string
	}{
		{http.StatusForbidden, `{"error":"plugin lacks permission llm:check"}`, "plugin lacks permission llm:check"},
		{http.StatusUnauthorized, `{"error":"plugin lacks permission llm:check"}`, "plugin lacks permission llm:check"},
		{http.StatusForbidden, ``, "permission not approved"},
		{http.StatusUnauthorized, ``, "permission not approved"},
	} {
		h := newHubServer(t)
		h.post(llmCheckPath, tc.code, tc.body)

		res, err := h.client().CheckLLM(WithCredential(context.Background(), "ainselp_user"), "p", "m")
		if err != nil {
			t.Fatalf("CheckLLM(%d): %v", tc.code, err)
		}
		if res.State != LLMCheckUnknown {
			t.Errorf("code %d: state = %q, want unknown — a refused check is not a provider that failed", tc.code, res.State)
		}
		if !strings.Contains(res.Reason, tc.want) {
			t.Errorf("code %d: reason = %q, want it to carry %q", tc.code, res.Reason, tc.want)
		}
		if !strings.Contains(res.Reason, "llm:check") || !strings.Contains(res.Reason, "approve") {
			t.Errorf("code %d: reason = %q, want it to name the permission to approve", tc.code, res.Reason)
		}
	}
}

func TestCheckLLMFailureCarriesTheHubsReason(t *testing.T) {
	// 502/503/504 are the statuses that mean "the hub asked and the pair could not
	// serve": unreachable, with the hub's own capped sentence — the sanitized text
	// the transport already read off the failure body, nothing else.
	h := newHubServer(t)
	h.post(llmCheckPath, http.StatusBadGateway, `{"error":"provider refused: connection reset"}`)

	res, err := h.client().CheckLLM(WithCredential(context.Background(), "ainselp_user"), "spurious", "gone-model")
	if err != nil {
		t.Fatalf("CheckLLM: %v", err)
	}
	if res.State != LLMCheckUnreachable {
		t.Errorf("state = %q, want unreachable", res.State)
	}
	if res.Reason != "provider refused: connection reset" {
		t.Errorf("reason = %q, want the hub's sentence", res.Reason)
	}
}

func TestCheckLLMHubWithoutWordsStillNamesTheStatus(t *testing.T) {
	h := newHubServer(t)
	h.post(llmCheckPath, http.StatusServiceUnavailable, ``)

	res, err := h.client().CheckLLM(WithCredential(context.Background(), "ainselp_user"), "p", "m")
	if err != nil {
		t.Fatalf("CheckLLM: %v", err)
	}
	if res.State != LLMCheckUnreachable {
		t.Errorf("state = %q, want unreachable", res.State)
	}
	if res.Reason != "the hub answered 503" {
		t.Errorf("reason = %q, want the status named", res.Reason)
	}
}

func TestCheckLLMHubSideFailuresAreUnknownNeverUnreachable(t *testing.T) {
	// A 400 (a body the hub would not accept — contract drift with the route, or a
	// custom provider the hub cannot probe from a pair), a 422, a 429 (the hub
	// rate-limiting the plugin) and a hub-side 500 are all the request failing, not
	// the pair. Reporting them as "this pair cannot serve" would blame the provider
	// for the hub's own problem — the misreport class this check exists to end,
	// aimed at the wrong party.
	for _, code := range []int{
		http.StatusBadRequest, http.StatusUnprocessableEntity,
		http.StatusTooManyRequests, http.StatusInternalServerError,
	} {
		h := newHubServer(t)
		h.post(llmCheckPath, code, `{"error":"hub-side trouble"}`)

		res, err := h.client().CheckLLM(WithCredential(context.Background(), "ainselp_user"), "p", "m")
		if err != nil {
			t.Fatalf("CheckLLM(%d): %v", code, err)
		}
		if res.State != LLMCheckUnknown {
			t.Errorf("code %d: state = %q, want unknown — the hub's own failure is not the provider's", code, res.State)
		}
		if res.Reason == "" {
			t.Errorf("code %d: an unknown answer carries no reason", code)
		}
	}
}

// TestCheckLLMCustomPairIsAskedAndComesBackUnknown is this plugin's custom rule: the
// client sends the pair like any other — no plugin-side special case — and the hub's
// 400 ("a custom provider cannot be checked from a provider/model pair") arrives as
// unknown. The hub stays the authority; a green light here would vouch for an endpoint
// nothing probed.
func TestCheckLLMCustomPairIsAskedAndComesBackUnknown(t *testing.T) {
	h := newHubServer(t)
	h.post(llmCheckPath, http.StatusBadRequest, `{"error":"custom provider cannot be checked from a provider/model pair"}`)

	res, err := h.client().CheckLLM(WithCredential(context.Background(), "ainselp_user"), "custom", "llama3.3")
	if err != nil {
		t.Fatalf("CheckLLM: %v", err)
	}
	if res.State != LLMCheckUnknown {
		t.Errorf("state = %q, want unknown — nothing probed this endpoint", res.State)
	}
	if len(h.calls(http.MethodPost, llmCheckPath)) != 1 {
		t.Errorf("the pair was not asked; the hub, not the client, answers the custom question")
	}
}

func TestCheckLLMUnreachableHubIsUnknown(t *testing.T) {
	// A hub that cannot be reached at all says nothing about the provider: unknown,
	// never ok and never the provider's fault.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close()

	c := NewHTTPClient(NewTransport(srv.URL))
	res, err := c.CheckLLM(WithCredential(context.Background(), "ainselp_user"), "p", "m")
	if err != nil {
		t.Fatalf("CheckLLM: %v", err)
	}
	if res.State != LLMCheckUnknown {
		t.Errorf("state = %q, want unknown — a hub that cannot be asked answers nothing", res.State)
	}
}

func TestCheckLLMTimeoutIsUnknownNotHang(t *testing.T) {
	// A provider that never answers must not hold "Check now" open: past the cap the
	// pair is unknown — the check could not be completed.
	h := newHubServer(t)
	h.route(http.MethodPost, llmCheckPath, func(w http.ResponseWriter, r *http.Request, _ int) {
		<-r.Context().Done()
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	c := h.client()
	c.checkTimeout = 20 * time.Millisecond
	res, err := c.CheckLLM(WithCredential(context.Background(), "ainselp_user"), "slow", "m")
	if err != nil {
		t.Fatalf("CheckLLM: %v", err)
	}
	if res.State != LLMCheckUnknown {
		t.Errorf("state = %q, want unknown — the cap expired before the hub answered", res.State)
	}
}

func TestCheckLLMCanceledCallerIsAnError(t *testing.T) {
	// A caller that went away is not an answer about the provider: the call comes
	// back as the error it is, and the caller stops caring.
	h := newHubServer(t)
	h.route(http.MethodPost, llmCheckPath, func(w http.ResponseWriter, r *http.Request, _ int) {
		<-r.Context().Done()
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := h.client().CheckLLM(WithCredential(ctx, "ainselp_user"), "p", "m")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CheckLLM = %v, want a context.Canceled error", err)
	}
}
