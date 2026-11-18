package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
)

// The credential the hub would have forwarded. It is a token: TestWorkRoutesCallTheHubWithThe
// CallersCredential asserts it is the credential the hub is called with, and the failure cases
// below assert it never comes back out in an answer.
const testCredential = "ainselp_testtoken"

// decodedRun is one run as the API answers with it. durationMs is an integer number of
// milliseconds rather than a nanosecond count, and costUsd is a pointer because an unknown
// cost has to arrive as null and not as a free run.
type decodedRun struct {
	ID         string   `json:"id"`
	Agent      string   `json:"agent"`
	Role       string   `json:"role"`
	Status     string   `json:"status"`
	DurationMs *int64   `json:"durationMs"`
	CostUSD    *float64 `json:"costUsd"`
	CostKnown  bool     `json:"costKnown"`
	URL        string   `json:"url"`
	IssueURL   string   `json:"issueUrl"`
}

type decodedItem struct {
	Subject struct {
		Repo   string `json:"repo"`
		Number int    `json:"number"`
		Kind   string `json:"kind"`
		Title  string `json:"title"`
	} `json:"subject"`
	URL        string        `json:"url"`
	Runs       []decodedRun  `json:"runs"`
	DurationMs int64         `json:"durationMs"`
	CostUSD    *float64      `json:"costUsd"`
	CostKnown  bool          `json:"costKnown"`
	PRs        []decodedItem `json:"prs"`
}

type decodedGroup struct {
	Repo      string        `json:"repo"`
	URL       string        `json:"url"`
	Items     []decodedItem `json:"items"`
	CostUSD   *float64      `json:"costUsd"`
	CostKnown bool          `json:"costKnown"`
}

// testHandler builds the routes over the given hub client and installs.
func testHandler(t *testing.T, client hub.Client, installs ...setup.Install) http.Handler {
	return NewWork(NewWorkService(client, testStore(t, installs...), testBase))
}

// doGet issues a GET the way the hub proxy would: with the credential it forwarded on the
// request, when it forwarded one.
func doGet(t *testing.T, h http.Handler, target, credential string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, target, nil)
	if credential != "" {
		req.Header.Set("X-Ainsel-Credential", credential)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// decode reads a route's answer into T, failing the test over anything that is not the JSON
// the route promises.
func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
	}
	return out
}

// wantError asserts the shape every failure on these routes has: a status, and a body whose
// one field is what went wrong.
func wantError(t *testing.T, rec *httptest.ResponseRecorder, status int, msg string) {
	t.Helper()

	if rec.Code != status {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, status, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body is not JSON: %v (%q)", err, rec.Body.String())
	}
	if body.Error != msg {
		t.Errorf("error = %q, want %q", body.Error, msg)
	}
}

// TestWorkRoutesRefuseARequestWithNoCaller is ruling R21: these routes answer for a caller,
// and a request that arrived without a credential to call the hub with is refused rather than
// answered from the plugin's own identity — which would show a caller everything a plugin
// credential can reach.
func TestWorkRoutesRefuseARequestWithNoCaller(t *testing.T) {
	targets := []string{
		"/api/now",
		"/api/work",
		"/api/work/dpinsel/apps/12",
		"/api/agents/forgejo-developer/work",
		"/api/invocations/inv-1/work",
	}

	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			spy := newSpy()
			h := testHandler(t, spy, appsInstall())

			rec := doGet(t, h, target, "")
			wantError(t, rec, http.StatusUnauthorized, "missing credential")
			if len(spy.Calls) != 0 {
				t.Errorf("the hub was called with %+v, want no call at all", spy.Calls)
			}
		})
	}
}

// TestWorkRoutesCallTheHubWithTheCallersCredential is the half of ruling R21 that the 401 case
// cannot show: the credential is not only read off the request and kept out of the answer, it
// is staged on the context and handed to the hub, which is what makes the hub answer with what
// this caller may see instead of with everything the plugin's own identity can reach. Drop the
// hub.WithCredential from callerContext and these routes keep answering happily while every
// real hub call they make is refused for want of a caller.
func TestWorkRoutesCallTheHubWithTheCallersCredential(t *testing.T) {
	targets := []string{
		"/api/now",
		"/api/work",
		"/api/work/dpinsel/apps/12",
		"/api/agents/forgejo-developer/work",
		"/api/invocations/inv-1/work",
	}

	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			spy := newSpy()
			h := testHandler(t, spy, appsInstall())

			rec := doGet(t, h, target, testCredential)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
			}
			if len(spy.Calls) == 0 {
				t.Fatal("the route answered without asking the hub anything")
			}
			if spy.credential != testCredential {
				t.Errorf("the hub was called with credential %q, want %q", spy.credential, testCredential)
			}
		})
	}
}

// TestWorkRoutesAreGetOnly keeps 404 and 405 meaning different things: the paths are mounted,
// the method is not.
func TestWorkRoutesAreGetOnly(t *testing.T) {
	h := testHandler(t, newSpy(), appsInstall())

	req := httptest.NewRequest(http.MethodPost, "/api/work", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); !strings.Contains(got, "GET") {
		t.Errorf("Allow = %q, want it to name GET", got)
	}
}

// TestNoHubIsNotReportedAsNoWork is the standalone answer: a plugin with no hub says so,
// instead of serving an empty work list that reads like a quiet month.
func TestNoHubIsNotReportedAsNoWork(t *testing.T) {
	h := testHandler(t, nil, appsInstall())

	rec := doGet(t, h, "/api/work", testCredential)
	wantError(t, rec, http.StatusServiceUnavailable, "hub not configured")
}

// TestWorkRouteServesGroupedWork is the console's work page: the repositories, every item
// with the page it links to, durations in milliseconds, and a cost that is null rather than
// zero whenever the money was never measured.
func TestWorkRouteServesGroupedWork(t *testing.T) {
	h := testHandler(t, newSpy(), appsInstall())

	rec := doGet(t, h, "/api/work", testCredential)
	res := decode[struct {
		Repos []decodedGroup `json:"repos"`
	}](t, rec)

	if len(res.Repos) != 2 {
		t.Fatalf("repos = %d, want 2: %+v", len(res.Repos), res.Repos)
	}
	if got, want := res.Repos[1].URL, testBase+"/"+appsRepo; got != want {
		t.Errorf("repo URL = %q, want %q", got, want)
	}
	if res.Repos[0].Repo == otherForge {
		t.Errorf("repo %q belongs to another connector and is not this plugin's work", otherForge)
	}

	apps := res.Repos[1]
	if len(apps.Items) != 1 {
		t.Fatalf("items = %+v, want issue 12 alone", apps.Items)
	}
	issue := apps.Items[0]
	if issue.Subject.Number != 12 || issue.Subject.Kind != "issue" {
		t.Errorf("subject = %+v, want issue 12", issue.Subject)
	}
	if want := testBase + "/" + appsRepo + "/issues/12"; issue.URL != want {
		t.Errorf("item URL = %q, want %q", issue.URL, want)
	}
	// Milliseconds, written out rather than recomputed: a duration that arrives in
	// nanoseconds is a number no console renders as a duration.
	if want := int64(40 * 60 * 1000); issue.DurationMs != want {
		t.Errorf("durationMs = %d, want %d", issue.DurationMs, want)
	}
	if issue.CostUSD == nil || *issue.CostUSD != 0.75 {
		t.Errorf("costUsd = %v, want 0.75 (both runs priced)", issue.CostUSD)
	}
	if !issue.CostKnown {
		t.Errorf("costKnown = false, want true")
	}
	if len(issue.Runs) != 2 {
		t.Fatalf("runs = %+v, want the issue's two", issue.Runs)
	}
	if got := issue.Runs[0].Role; got != "developer" {
		t.Errorf("run role = %q, want developer", got)
	}
	// The run's own page is its item's page: they are the same thread by construction.
	if want := testBase + "/" + appsRepo + "/issues/12"; issue.Runs[0].URL != want {
		t.Errorf("run URL = %q, want %q", issue.Runs[0].URL, want)
	}
	if want := int64(10 * 60 * 1000); issue.Runs[0].DurationMs == nil || *issue.Runs[0].DurationMs != want {
		t.Errorf("run durationMs = %v, want %d", issue.Runs[0].DurationMs, want)
	}
	// A run that has not finished has no finished duration to report — and not a
	// zero, which a console would read as a run that ended the moment it started.
	if len(issue.PRs) != 1 {
		t.Fatalf("prs = %+v, want pull 34", issue.PRs)
	}
	pr := issue.PRs[0]
	if want := testBase + "/" + appsRepo + "/pulls/34"; pr.URL != want {
		t.Errorf("nested pull URL = %q, want %q", pr.URL, want)
	}
	// One of the pull's runs is unpriced, so the pull's total is null.
	if pr.CostKnown {
		t.Errorf("pull costKnown = true, want false")
	}
	if pr.CostUSD != nil {
		t.Errorf("pull costUsd = %v, want null: an unknown cost is never a number", *pr.CostUSD)
	}
	if apps.CostUSD != nil {
		t.Errorf("repo costUsd = %v, want null (a run in it is unpriced)", *apps.CostUSD)
	}
	// The same rule, stated on the bytes rather than through a decoder: a decoder
	// cannot tell a null from a missing field.
	if !strings.Contains(rec.Body.String(), `"costUsd":null`) {
		t.Errorf("body has no explicit null cost: %s", rec.Body.String())
	}
}

// TestNowRouteServesRunsWithTheirPages checks the Now page's body: the runs that have not
// finished, each with its page, and the issue page too for a run on a pull request.
func TestNowRouteServesRunsWithTheirPages(t *testing.T) {
	h := testHandler(t, newSpy(), appsInstall())

	rec := doGet(t, h, "/api/now", testCredential)
	res := decode[struct {
		Runs []decodedRun `json:"runs"`
	}](t, rec)

	if len(res.Runs) != 2 {
		t.Fatalf("runs = %d, want 2: %+v", len(res.Runs), res.Runs)
	}
	if got, want := res.Runs[0].ID, "inv-5"; got != want {
		t.Errorf("first run = %q, want %q", got, want)
	}
	if got, want := res.Runs[0].URL, testBase+"/"+otherRepo+"/issues/3"; got != want {
		t.Errorf("run URL = %q, want %q", got, want)
	}
	if res.Runs[0].IssueURL != "" {
		t.Errorf("issue run issueUrl = %q, want none", res.Runs[0].IssueURL)
	}
	if got, want := res.Runs[1].IssueURL, testBase+"/"+appsRepo+"/issues/12"; got != want {
		t.Errorf("pull run issueUrl = %q, want %q", got, want)
	}
	// A run that is still going has no finished duration, and its cost has not been
	// measured: neither arrives as a zero.
	if res.Runs[1].DurationMs != nil {
		t.Errorf("running run durationMs = %v, want none", *res.Runs[1].DurationMs)
	}
	if res.Runs[1].CostUSD != nil {
		t.Errorf("running run costUsd = %v, want null", *res.Runs[1].CostUSD)
	}
}

// TestItemRouteServesOneThread covers the drill-down: the thread, a number nobody ran on, and
// a number that is not a number.
func TestItemRouteServesOneThread(t *testing.T) {
	h := testHandler(t, newSpy(), appsInstall())

	t.Run("issue", func(t *testing.T) {
		rec := doGet(t, h, "/api/work/dpinsel/apps/12", testCredential)
		res := decode[struct {
			Item decodedItem `json:"item"`
		}](t, rec)

		if res.Item.Subject.Number != 12 {
			t.Fatalf("item = %+v, want issue 12", res.Item.Subject)
		}
		if want := testBase + "/" + appsRepo + "/issues/12"; res.Item.URL != want {
			t.Errorf("URL = %q, want %q", res.Item.URL, want)
		}
		if len(res.Item.PRs) != 1 {
			t.Errorf("prs = %+v, want the nested pull", res.Item.PRs)
		}
	})

	t.Run("a pull request nested under it", func(t *testing.T) {
		rec := doGet(t, h, "/api/work/dpinsel/apps/34", testCredential)
		res := decode[struct {
			Item decodedItem `json:"item"`
		}](t, rec)
		if res.Item.Subject.Kind != "pull" {
			t.Errorf("item = %+v, want pull 34", res.Item.Subject)
		}
	})

	t.Run("no such thread", func(t *testing.T) {
		rec := doGet(t, h, "/api/work/dpinsel/apps/99", testCredential)
		wantError(t, rec, http.StatusNotFound, "no such work item")
	})

	t.Run("not a number", func(t *testing.T) {
		rec := doGet(t, h, "/api/work/dpinsel/apps/all", testCredential)
		wantError(t, rec, http.StatusBadRequest, "invalid item number")
	})

	t.Run("not a number that names a page", func(t *testing.T) {
		rec := doGet(t, h, "/api/work/dpinsel/apps/0", testCredential)
		wantError(t, rec, http.StatusBadRequest, "invalid item number")
	})
}

// TestAgentWorkRouteServesTheAgentsThreads is the agent slot's route.
func TestAgentWorkRouteServesTheAgentsThreads(t *testing.T) {
	h := testHandler(t, newSpy(), appsInstall())

	rec := doGet(t, h, "/api/agents/forgejo-developer/work", testCredential)
	res := decode[struct {
		Items []decodedItem `json:"items"`
	}](t, rec)

	if len(res.Items) != 1 {
		t.Fatalf("items = %+v, want issue 12", res.Items)
	}
	if res.Items[0].Subject.Number != 12 {
		t.Errorf("item = %+v, want issue 12", res.Items[0].Subject)
	}
	if res.Items[0].URL == "" {
		t.Errorf("item has no URL: %+v", res.Items[0])
	}

	t.Run("an agent with no work", func(t *testing.T) {
		rec := doGet(t, h, "/api/agents/nobody/work", testCredential)
		res := decode[struct {
			Items []decodedItem `json:"items"`
		}](t, rec)
		// An empty list, not a null: the console maps over it either way.
		if res.Items == nil || len(res.Items) != 0 {
			t.Errorf("items = %v, want an empty list", res.Items)
		}
	})
}

// TestInvocationWorkRouteServesTheItemAndItsSiblings is the invocation slot's route: the
// thread a run was about and what its siblings have cost.
func TestInvocationWorkRouteServesTheItemAndItsSiblings(t *testing.T) {
	h := testHandler(t, newSpy(), appsInstall())

	rec := doGet(t, h, "/api/invocations/inv-1/work", testCredential)
	res := decode[struct {
		Item              decodedItem `json:"item"`
		SiblingsCostUSD   *float64    `json:"siblingsCostUsd"`
		SiblingsCostKnown bool        `json:"siblingsCostKnown"`
	}](t, rec)

	if res.Item.Subject.Number != 12 {
		t.Fatalf("item = %+v, want issue 12", res.Item.Subject)
	}
	if res.SiblingsCostUSD == nil || *res.SiblingsCostUSD != 0.5 {
		t.Errorf("siblingsCostUsd = %v, want the other run's 0.5", res.SiblingsCostUSD)
	}
	if !res.SiblingsCostKnown {
		t.Errorf("siblingsCostKnown = false, want true")
	}

	t.Run("an unpriced sibling", func(t *testing.T) {
		rec := doGet(t, h, "/api/invocations/inv-2/work", testCredential)
		res := decode[struct {
			SiblingsCostUSD   *float64 `json:"siblingsCostUsd"`
			SiblingsCostKnown bool     `json:"siblingsCostKnown"`
		}](t, rec)
		if res.SiblingsCostKnown {
			t.Errorf("siblingsCostKnown = true, want false")
		}
		if res.SiblingsCostUSD != nil {
			t.Errorf("siblingsCostUsd = %v, want null", *res.SiblingsCostUSD)
		}
	})

	t.Run("no such invocation", func(t *testing.T) {
		rec := doGet(t, h, "/api/invocations/inv-nope/work", testCredential)
		wantError(t, rec, http.StatusNotFound, "no such invocation")
	})
}

// TestWorkRouteRejectsABadSince keeps a mistyped window from silently becoming the default:
// a console that asks for "week" and gets a month has been told a lie about its own question.
func TestWorkRouteRejectsABadSince(t *testing.T) {
	spy := newSpy()
	h := testHandler(t, spy, appsInstall())

	for _, bad := range []string{"week", "30", "-7d", "7x", ""} {
		rec := doGet(t, h, "/api/work?since="+bad, testCredential)
		// The empty value is the one exception: it is no value, which is the default.
		if bad == "" {
			if rec.Code != http.StatusOK {
				t.Errorf("since= served %d, want 200 (no value is the default)", rec.Code)
			}
			continue
		}
		wantError(t, rec, http.StatusBadRequest, "invalid since")
	}

	// A window that does parse reaches the hub as a moment.
	rec := doGet(t, h, "/api/work?since=7d", testCredential)
	if rec.Code != http.StatusOK {
		t.Fatalf("since=7d served %d (body %q)", rec.Code, rec.Body.String())
	}
	want := time.Now().Add(-7 * 24 * time.Hour)
	if d := spy.since.Sub(want); d > time.Minute || d < -time.Minute {
		t.Errorf("since = %v, want about %v", spy.since, want)
	}

	// So does a moment given outright.
	rec = doGet(t, h, "/api/work?since="+day.Format(time.RFC3339), testCredential)
	if rec.Code != http.StatusOK {
		t.Fatalf("since=<RFC3339> served %d (body %q)", rec.Code, rec.Body.String())
	}
	if !spy.since.Equal(day) {
		t.Errorf("since = %v, want %v", spy.since, day)
	}
}

// TestWorkRoutePassesTheHubsAnswersThrough checks the two hub answers the console has to
// receive intact: "you may not see this" stays a 403, and anything else the hub did not
// answer with stays a 502 — and neither carries the credential to the client.
func TestWorkRoutePassesTheHubsAnswersThrough(t *testing.T) {
	t.Run("forbidden", func(t *testing.T) {
		spy := newSpy()
		spy.err = &hub.StatusError{Code: http.StatusForbidden}
		h := testHandler(t, spy, appsInstall())

		rec := doGet(t, h, "/api/work", testCredential)
		wantError(t, rec, http.StatusForbidden, "forbidden")
		if strings.Contains(rec.Body.String(), testCredential) {
			t.Errorf("the body carries the credential: %s", rec.Body.String())
		}
	})

	t.Run("a hub that did not answer", func(t *testing.T) {
		spy := newSpy()
		spy.err = &hub.StatusError{Code: http.StatusInternalServerError}
		h := testHandler(t, spy, appsInstall())

		rec := doGet(t, h, "/api/work", testCredential)
		wantError(t, rec, http.StatusBadGateway, "hub unavailable")
	})

	t.Run("a hub that could not be reached", func(t *testing.T) {
		spy := newSpy()
		spy.err = errors.New("hub is not answering")
		h := testHandler(t, spy, appsInstall())

		rec := doGet(t, h, "/api/now", testCredential)
		wantError(t, rec, http.StatusBadGateway, "hub unavailable")
		if strings.Contains(rec.Body.String(), testCredential) {
			t.Errorf("the body carries the credential: %s", rec.Body.String())
		}
	})
}
