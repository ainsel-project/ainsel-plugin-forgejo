package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/api"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/forge"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/store"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/work"
)

// rpcError is the JSON-RPC error object, reported instead of a result when the
// server rejects a request.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// rpcMessage is one JSON-RPC 2.0 reply, of the kind both the plain-JSON and the
// SSE form of a streamable-HTTP response carry.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      float64         `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

// call posts one JSON-RPC request to the endpoint and returns the reply whose
// id matches. The streamable-HTTP transport answers with either a bare JSON
// body or an SSE stream whose data lines carry the messages, so both shapes are
// unwrapped here rather than assumed.
func call(t *testing.T, url string, id int, method string, params any) rpcMessage {
	return callWith(t, nil, url, id, method, params)
}

// callWith is call with headers on the request — which is how a caller that
// reached the plugin through the hub is simulated: the hub leaves the caller's
// context on the request the transport builds its tool context from.
func callWith(t *testing.T, header http.Header, url string, id int, method string, params any) rpcMessage {
	t.Helper()

	payload := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		payload["params"] = params
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s: %v", method, err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for name, values := range header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	// The transport negotiates on these two: it answers with JSON or with an
	// event stream, and a client that does not advertise both gets a 406.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", method, err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("post %s: status = %d, want 200 (body %q)", method, res.StatusCode, b)
	}

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read reply to %s: %v", method, err)
	}

	var messages []rpcMessage
	if strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		// An event stream carries the messages one per "data:" line; event names,
		// ids, comments and blank separators carry no payload.
		scanner := bufio.NewScanner(bytes.NewReader(raw))
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			var msg rpcMessage
			if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &msg); err != nil {
				t.Fatalf("decode reply line %q: %v", line, err)
			}
			messages = append(messages, msg)
		}
		if err := scanner.Err(); err != nil {
			t.Fatalf("read reply to %s: %v", method, err)
		}
	} else if trimmed := strings.TrimSpace(string(raw)); strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &messages); err != nil {
			t.Fatalf("decode reply batch %q: %v", raw, err)
		}
	} else {
		var msg rpcMessage
		if err := json.Unmarshal([]byte(trimmed), &msg); err != nil {
			t.Fatalf("decode reply %q: %v", raw, err)
		}
		messages = append(messages, msg)
	}
	if len(messages) == 0 {
		t.Fatalf("post %s: reply carried no JSON-RPC message (body %q)", method, raw)
	}

	// A stream may carry more than one message (a notification reply alongside
	// the response), so the one asked for is picked by id.
	msg := messages[0]
	for _, m := range messages {
		if int(m.ID) == id {
			msg = m
			break
		}
	}
	if msg.Error != nil {
		t.Fatalf("%s: jsonrpc error %d: %s", method, msg.Error.Code, msg.Error.Message)
	}
	return msg
}

// callResult decodes the result of a successful request into v.
func callResult(t *testing.T, url string, id int, method string, params any, v any) {
	t.Helper()

	msg := call(t, url, id, method, params)
	if len(msg.Result) == 0 {
		t.Fatalf("%s: reply has no result (id %v)", method, msg.ID)
	}
	if err := json.Unmarshal(msg.Result, v); err != nil {
		t.Fatalf("%s: decode result: %v", method, err)
	}
}

// initParams is what a client sends to open the connection; the version asked for
// is the newest the platform speaks.
func initParams() map[string]any {
	return map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "ainsel-plugin-forgejo-test", "version": "0.1.0"},
	}
}

// TestMCPEndpoint walks the endpoint the way a client does: it opens the connection
// and lists what the plugin offers. It runs over real HTTP rather than against the
// library's in-process transport, because what the plugin ships is the HTTP handler —
// mounting and all.
//
// Nothing here sends a credential: the hub lists a plugin's tools with its own
// background call, and a tool set that only appeared for a caller would look to the
// hub like a plugin with no tools at all.
func TestMCPEndpoint(t *testing.T) {
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), testSetupService(t)))
	t.Cleanup(srv.Close)
	url := srv.URL

	t.Run("initialize", func(t *testing.T) {
		var init struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
		}
		callResult(t, url, 1, "initialize", initParams(), &init)

		// The name is the plugin id: AInsel prefixes the tool names it lists with
		// it, so an id here that drifted from the manifest would name tools that
		// belong to no plugin.
		if init.ServerInfo.Name != "forgejo" {
			t.Errorf("serverInfo.name = %q, want %q", init.ServerInfo.Name, "forgejo")
		}
		if init.ServerInfo.Version != "0.1.0" {
			t.Errorf("serverInfo.version = %q, want %q", init.ServerInfo.Version, "0.1.0")
		}
		if init.ProtocolVersion == "" {
			t.Error("protocolVersion is empty, want a negotiated version")
		}
	})

	t.Run("tools/list names the plugin's tools", func(t *testing.T) {
		var listed struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		}
		callResult(t, url, 2, "tools/list", map[string]any{}, &listed)

		names := make([]string, 0, len(listed.Tools))
		for _, tool := range listed.Tools {
			names = append(names, tool.Name)
			// A description that spelled out the qualified name would be the wrong
			// name for an agent, which types the bare one; naming neither is what
			// lets one description serve both callers.
			if strings.Contains(tool.Description, pluginName+"_") {
				t.Errorf("tool %q: description names a qualified tool name: %q", tool.Name, tool.Description)
			}
		}
		want := []string{"list_work", "get_work_item", "setup_options", "setup_plan", "setup_apply", "setup_orphans_plan", "setup_orphans_remove"}
		if !sameNames(names, want) {
			t.Errorf("tools = %v, want %v", names, want)
		}
	})
}

// callerHeader builds the header the hub leaves for an authenticated caller: the
// context JSON in base64url without padding.
func callerHeader(t *testing.T, body string) http.Header {
	t.Helper()

	if !json.Valid([]byte(body)) {
		t.Fatalf("test context %q is not valid JSON", body)
	}
	return http.Header{"X-Ainsel-Context": {base64.RawURLEncoding.EncodeToString([]byte(body))}}
}

// TestMCPEndpointCallerRequest checks the endpoint against a request that came
// through the hub: the forwarded context is read onto the request context rather
// than answered about, so it changes nothing the client sees. A context the plugin
// cannot read is treated the same way — the call is not failed over a header the
// hub wrote.
func TestMCPEndpointCallerRequest(t *testing.T) {
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), testSetupService(t)))
	t.Cleanup(srv.Close)
	url := srv.URL

	const alice = `{"pluginId":"forgejo","kind":"user","sub":"00u1","username":"alice",` +
		`"isAdmin":false,"permissions":[],"groups":[{"id":"g1","name":"Platform","role":"member"}],"invocationId":""}`

	tests := []struct {
		name   string
		header http.Header
	}{
		{name: "caller context", header: callerHeader(t, alice)},
		{name: "context that is not base64url", header: http.Header{"X-Ainsel-Context": {"not base64 at all!"}}},
		{name: "context that is not JSON", header: http.Header{"X-Ainsel-Context": {base64.RawURLEncoding.EncodeToString([]byte("greeting"))}}},
		{name: "no context header"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var init struct {
				ServerInfo struct {
					Name string `json:"name"`
				} `json:"serverInfo"`
			}
			msg := callWith(t, tt.header, url, 1, "initialize", initParams())
			if err := json.Unmarshal(msg.Result, &init); err != nil {
				t.Fatalf("initialize: decode result: %v", err)
			}
			if init.ServerInfo.Name != "forgejo" {
				t.Errorf("serverInfo.name = %q, want %q", init.ServerInfo.Name, "forgejo")
			}
		})
	}
}

// -- the fixtures the tool tests are read under ---------------------------------------

// The Forgejo the service points at, the two repositories its install has worked on, and
// the credential the hub is imagined to have forwarded. Expected URLs are written out in
// full from these rather than rebuilt with the helpers that produce them, so a change to
// the URL shape fails here.
const (
	testBase    = "https://git.example.test"
	appsRepo    = "acme/apps"
	otherRepo   = "acme/other"
	connectorID = "c-1"
	credential  = "ainselp_testcaller"
)

// day is the date the fixture runs hang off. It is a fixed distance back from now rather
// than a calendar date: the fake hub filters by since, and a fixed date stops being
// answerable for once the default 30-day window passes it. Eight days is the distance
// the since-filtered assertions read against: a six-day window stops short of every
// fixture run, and a finished one is past the seven days AInsel's token metrics reach.
var day = time.Now().UTC().Add(-8 * 24 * time.Hour).Truncate(24 * time.Hour)

// at is a run time on the fixture day: at(9, 30) is 09:30 UTC.
func at(hour, minute int) time.Time {
	return day.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

// ending spells a finished run's end time as the *time.Time the type carries.
func ending(t time.Time) *time.Time { return &t }

// issueOf is the subject of a run about an issue in the named repository.
func issueOf(repo string, number int, title string) forge.Subject {
	return forge.Subject{Repo: repo, Number: number, Kind: forge.KindIssue, Title: title}
}

// pullOf is the subject of a run about a pull request, carrying the title and branch the
// linking rule reads: this one names issue 12 in its scope and in its branch.
func pullOf(repo string, number int, title, branch string) forge.Subject {
	return forge.Subject{Repo: repo, Number: number, Kind: forge.KindPull, Title: title, Branch: branch}
}

// appsInstall is the install the fixtures are read under: one connector and the role
// agents the fixture runs are named after.
func appsInstall() setup.Install {
	return setup.Install{
		Slug:        "apps",
		GroupID:     "g-1",
		BaseURL:     testBase,
		ConnectorID: connectorID,
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleRefiner:   {AgentID: "forgejo-refiner"},
			setup.RoleDeveloper: {AgentID: "forgejo-developer"},
			setup.RoleReviewer:  {AgentID: "forgejo-reviewer"},
		},
	}
}

// fixtureRuns are the runs the hub answers with: two on issue #12 and two on the pull
// request that closes it — one of those still running and unpriced — one queued on
// another repository, and one on a connector no install points at, which is the run no
// answer below may contain.
func fixtureRuns() []work.Run {
	return []work.Run{
		{
			ID: "inv-1", Agent: "forgejo-refiner", Status: "success", Connector: connectorID,
			Start: at(9, 0), End: ending(at(9, 30)), CostUSD: 0.25, CostKnown: true,
			Subject: issueOf(appsRepo, 12, "Search the word list"),
		},
		{
			ID: "inv-2", Agent: "forgejo-developer", Status: "success", Connector: connectorID,
			Start: at(10, 0), End: ending(at(11, 0)), CostUSD: 1.5, CostKnown: true,
			Subject: pullOf(appsRepo, 34, "feat(#12): search the word list", "feat/12-word-list"),
		},
		{
			// Running, and older than the seven days AInsel's token metrics reach
			// back, so its cost is unknown rather than zero.
			ID: "inv-3", Agent: "forgejo-reviewer", Status: "running", Connector: connectorID,
			Start:   at(12, 0),
			Subject: pullOf(appsRepo, 34, "feat(#12): search the word list", "feat/12-word-list"),
		},
		{
			ID: "inv-4", Agent: "forgejo-refiner", Status: "queued", Connector: connectorID,
			Start:   at(14, 0),
			Subject: issueOf(otherRepo, 3, "Ship it"),
		},
		{
			// Another forge's connector: not this install's work.
			ID: "inv-5", Agent: "forgejo-developer", Status: "success", Connector: "c-2",
			Start: at(15, 0), End: ending(at(15, 5)), CostUSD: 9, CostKnown: true,
			Subject: issueOf("someone/else", 7, "Not ours"),
		},
	}
}

// spyClient is the fake hub with the two answers a hub.Fake cannot give: the credential a
// Runs call was made with, and a failure to hand back.
type spyClient struct {
	*hub.Fake

	// credential is the caller's credential the Runs call carried, "" when it carried
	// none. A hub call without one is a call the real hub would refuse, so the tools
	// assert that the credential they were handed is the one they pass on.
	credential string

	// err is what Runs returns instead of the seed — the hub saying no.
	err error
}

var _ hub.Client = (*spyClient)(nil)

func (c *spyClient) Runs(ctx context.Context, q hub.RunsQuery) ([]work.Run, error) {
	c.credential, _ = hub.CredentialFromContext(ctx)
	if c.err != nil {
		return nil, c.err
	}
	return c.Fake.Runs(ctx, q)
}

// testStore opens a real store in a temporary directory, holding the installs given.
func testStore(t *testing.T, installs ...setup.Install) *store.Store {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})
	for _, in := range installs {
		if err := st.Save(context.Background(), in); err != nil {
			t.Fatalf("store.Save(%q): %v", in.Slug, err)
		}
	}
	return st
}

// testService builds the read path the tools are mounted on: the fixture hub, one
// install, the fixture Forgejo. The spy is returned too, for the assertions about what the
// tools asked the hub.
func testService(t *testing.T) *api.WorkService {
	t.Helper()
	return api.NewWorkService(&spyClient{Fake: &hub.Fake{SeedRuns: fixtureRuns()}}, testStore(t, appsInstall()), testBase)
}

// serviceOver builds the same thing over a hub of the test's own — the one that answers a
// failure, or the one there is no hub behind at all.
func serviceOver(t *testing.T, client hub.Client) *api.WorkService {
	t.Helper()
	return api.NewWorkService(client, testStore(t, appsInstall()), testBase)
}

// callerRequest is the header a hub-proxied tool call arrives with: the credential the hub
// minted for the caller behind it, and the invocation the call belongs to.
func callerRequest() http.Header {
	return http.Header{
		"X-Ainsel-Credential":    {credential},
		"X-Ainsel-Invocation-Id": {"inv-tool-1"},
	}
}

// -- the tools, over JSON-RPC ---------------------------------------------------------

// toolText is the text a tools/call answered with, and whether the answer says the tool
// failed. A tool-level failure is not a call failure: it arrives as a result whose
// isError is set, and the caller reads the text to find out what went wrong.
type toolText struct {
	Text    string
	IsError bool
}

// callTool calls one tool over JSON-RPC with the headers a caller arrives with. The
// arguments are passed as given, so a test can leave a required one out.
func callTool(t *testing.T, url string, header http.Header, name string, args map[string]any) toolText {
	t.Helper()

	params := map[string]any{"name": name}
	if args != nil {
		params["arguments"] = args
	}
	msg := callWith(t, header, url, 7, "tools/call", params)

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		t.Fatalf("tools/call %s: decode result: %v", name, err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("tools/call %s: %d content parts, want one text part (%+v)", name, len(result.Content), result.Content)
	}
	return toolText{Text: result.Content[0].Text, IsError: result.IsError}
}

// callToolOK calls a tool and fails the test if the tool said it failed.
func callToolOK(t *testing.T, url, name string, args map[string]any) string {
	t.Helper()

	got := callTool(t, url, callerRequest(), name, args)
	if got.IsError {
		t.Fatalf("tools/call %s: tool failed: %s", name, got.Text)
	}
	return got.Text
}

// served starts the endpoint over the fixture service and hands back its URL.
func served(t *testing.T) string {
	t.Helper()

	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), testSetupService(t)))
	t.Cleanup(srv.Close)
	return srv.URL
}

// decodeTool answers a tool's JSON body as the shape given.
func decodeTool[T any](t *testing.T, text string, v *T) {
	t.Helper()

	if err := json.Unmarshal([]byte(text), v); err != nil {
		t.Fatalf("decode tool answer %q: %v", text, err)
	}
}

// sameNames reports whether two lists hold the same names, in either order.
func sameNames(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]bool, len(got))
	for _, n := range got {
		seen[n] = true
	}
	for _, n := range want {
		if !seen[n] {
			return false
		}
	}
	return true
}

// listedItem is one entry of list_work's answer, and of the pulls nested in it.
type listedItem struct {
	Repo         string       `json:"repo"`
	Number       int          `json:"number"`
	Kind         string       `json:"kind"`
	Title        string       `json:"title"`
	URL          string       `json:"url"`
	CostUSD      *float64     `json:"costUsd"`
	DurationMs   int64        `json:"durationMs"`
	Runs         int          `json:"runs"`
	LastActivity string       `json:"lastActivity"`
	PRs          []listedItem `json:"prs"`
}

// workList is list_work's whole answer.
type workList struct {
	Items []listedItem `json:"items"`
}

// found returns the entry for one repository's number, or nil.
func (l workList) found(repo string, number int) *listedItem {
	for i, it := range l.Items {
		if it.Repo == repo && it.Number == number {
			return &l.Items[i]
		}
		for j, pr := range it.PRs {
			if pr.Repo == repo && pr.Number == number {
				return &l.Items[i].PRs[j]
			}
		}
	}
	return nil
}

// TestMCPToolsListedWithoutACaller is the hub's side of the contract: it lists a plugin's
// tools with its own background request, with no headers on it at all, and the tool set it
// gets back is every tool the plugin has — the hub is the one that filters by audience.
func TestMCPToolsListedWithoutACaller(t *testing.T) {
	spy := &spyClient{Fake: &hub.Fake{SeedRuns: fixtureRuns()}}
	srv := httptest.NewServer(NewHandler("0.1.0", serviceOver(t, spy), testSetupService(t)))
	t.Cleanup(srv.Close)

	var listed struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			InputSchema struct {
				Type       string         `json:"type"`
				Required   []string       `json:"required"`
				Properties map[string]any `json:"properties"`
			} `json:"inputSchema"`
			Annotations struct {
				ReadOnlyHint    *bool `json:"readOnlyHint"`
				DestructiveHint *bool `json:"destructiveHint"`
				IdempotentHint  *bool `json:"idempotentHint"`
				OpenWorldHint   *bool `json:"openWorldHint"`
			} `json:"annotations"`
		} `json:"tools"`
	}
	callResult(t, srv.URL, 2, "tools/list", map[string]any{}, &listed)

	if len(listed.Tools) != 7 {
		t.Fatalf("tools = %d, want 7: %+v", len(listed.Tools), listed.Tools)
	}
	for _, tool := range listed.Tools {
		// apply and the orphan remove are the tools that write; their hints are
		// asserted where the destructive shape of them is the point.
		if tool.Name == "setup_apply" || tool.Name == "setup_orphans_remove" {
			continue
		}
		// The hints are stated rather than left to the defaults, which a client
		// reads as the worst case: an absent hint is destructive.
		if tool.Annotations.ReadOnlyHint == nil || !*tool.Annotations.ReadOnlyHint {
			t.Errorf("tool %q: readOnlyHint is not true", tool.Name)
		}
		if tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
			t.Errorf("tool %q: destructiveHint is not false", tool.Name)
		}
		if tool.Annotations.IdempotentHint == nil || !*tool.Annotations.IdempotentHint {
			t.Errorf("tool %q: idempotentHint is not true", tool.Name)
		}
		if tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Errorf("tool %q: openWorldHint is not false", tool.Name)
		}
	}

	byName := map[string]int{}
	for i, tool := range listed.Tools {
		byName[tool.Name] = i
	}
	if _, ok := byName["list_work"]; !ok {
		t.Fatalf("no list_work in %+v", listed.Tools)
	}
	if _, ok := byName["get_work_item"]; !ok {
		t.Fatalf("no get_work_item in %+v", listed.Tools)
	}
	// A repository and a number are the two the tool cannot do without; a caller who
	// gives neither gets a tool error rather than every item the plugin can see.
	if got := listed.Tools[byName["get_work_item"]].InputSchema.Required; !sameNames(got, []string{"repo", "number"}) {
		t.Errorf("get_work_item required = %v, want [repo number]", got)
	}
	for _, name := range []string{"repo", "agent", "since"} {
		if _, ok := listed.Tools[byName["list_work"]].InputSchema.Properties[name]; !ok {
			t.Errorf("list_work has no %q argument", name)
		}
	}

	// Listing what the plugin offers is not a reason to ask the hub anything: the tools
	// are known without it.
	for _, call := range spy.Calls {
		if call == "Runs" {
			t.Error("tools/list asked the hub for runs")
		}
	}
}

// TestMCPListWorkAnswersWithForgejoURLs is the brief's promise: an agent that asks what
// AInsel has been working on gets the work, and the page on the forge to read it at.
func TestMCPListWorkAnswersWithForgejoURLs(t *testing.T) {
	url := served(t)

	var got workList
	decodeTool(t, callToolOK(t, url, "list_work", nil), &got)

	if len(got.Items) != 2 {
		t.Fatalf("items = %+v, want the two threads with runs", got.Items)
	}

	issue := got.found(appsRepo, 12)
	if issue == nil {
		t.Fatalf("no item for issue 12: %+v", got.Items)
	}
	if want := testBase + "/" + appsRepo + "/issues/12"; issue.URL != want {
		t.Errorf("item URL = %q, want %q", issue.URL, want)
	}
	if issue.Kind != "issue" {
		t.Errorf("kind = %q, want issue", issue.Kind)
	}
	if issue.Title != "Search the word list" {
		t.Errorf("title = %q, want the issue's", issue.Title)
	}
	// The issue's own finished run: 30 minutes. The pull request's hour is the pull's
	// own, summed there and not here.
	if want := int64(30 * 60 * 1000); issue.DurationMs != want {
		t.Errorf("durationMs = %d, want %d", issue.DurationMs, want)
	}
	if issue.Runs != 1 {
		t.Errorf("runs = %d, want 1 (the pull request's runs are the pull's own)", issue.Runs)
	}
	if issue.CostUSD == nil || *issue.CostUSD != 0.25 {
		t.Errorf("costUsd = %v, want 0.25", issue.CostUSD)
	}
	if issue.LastActivity == "" {
		t.Error("lastActivity is empty, and a caller cannot tell when this was worked on")
	}

	// The pull request that names the issue sits under it, with its own page: the
	// thread and the code are two pages on the forge.
	if len(issue.PRs) != 1 {
		t.Fatalf("prs = %+v, want pull 34", issue.PRs)
	}
	pull := issue.PRs[0]
	if want := testBase + "/" + appsRepo + "/pulls/34"; pull.URL != want {
		t.Errorf("nested pull URL = %q, want %q", pull.URL, want)
	}
	if pull.Kind != "pull" {
		t.Errorf("nested kind = %q, want pull", pull.Kind)
	}

	// A run nobody priced makes the total unknown, not zero: the pull's running run has
	// no cost, so its costUsd is null in the bytes rather than a number nobody measured.
	if pull.CostUSD != nil {
		t.Errorf("nested pull costUsd = %v, want null", *pull.CostUSD)
	}
	if !strings.Contains(callToolOK(t, url, "list_work", nil), `"costUsd":null`) {
		t.Error("no explicit null cost in the answer: an unknown cost must not read as a zero")
	}

	// Another repository's thread is in the same answer, since no repository was named.
	if got.found(otherRepo, 3) == nil {
		t.Errorf("no item for %s#3: %+v", otherRepo, got.Items)
	}

	// A connector no install points at is not this plugin's work.
	if strings.Contains(callToolOK(t, url, "list_work", nil), "someone/else") {
		t.Error("the answer contains a repository on a connector this plugin was never installed on")
	}
}

// TestMCPListWorkNarrowsToWhatWasNamed checks the three filters: a repository, an agent and
// how far back. Each narrows the work rather than the answer, so what is left is still a
// thread rather than a run log.
func TestMCPListWorkNarrowsToWhatWasNamed(t *testing.T) {
	url := served(t)

	t.Run("repo", func(t *testing.T) {
		var got workList
		decodeTool(t, callToolOK(t, url, "list_work", map[string]any{"repo": appsRepo}), &got)
		if len(got.Items) != 1 || got.Items[0].Repo != appsRepo {
			t.Fatalf("items = %+v, want only %s", got.Items, appsRepo)
		}
		if len(got.Items[0].PRs) != 1 {
			t.Errorf("nested pulls = %+v, want the pull the issue still holds", got.Items[0].PRs)
		}
	})

	t.Run("agent", func(t *testing.T) {
		var got workList
		decodeTool(t, callToolOK(t, url, "list_work", map[string]any{"agent": "forgejo-developer"}), &got)
		for _, it := range got.Items {
			// The developer ran the pull request, which is nested under an issue it
			// ran nothing on: what may not appear is a thread of another agent's.
			if it.Repo == otherRepo {
				t.Errorf("item %+v belongs to another agent", it)
			}
		}
		if got.found(appsRepo, 34) == nil {
			t.Error("the developer's pull request is missing from its own agent's work")
		}
	})

	t.Run("since", func(t *testing.T) {
		// Every fixture run is more than a week old, so a six-day window leaves
		// nothing — and the answer is an empty list, not an error.
		var got workList
		decodeTool(t, callToolOK(t, url, "list_work", map[string]any{"since": "6d"}), &got)
		if len(got.Items) != 0 {
			t.Errorf("items = %+v, want none inside a 6-day window", got.Items)
		}

		// The same window spelled as a moment, answering for everything since.
		var all workList
		decodeTool(t, callToolOK(t, url, "list_work", map[string]any{
			"since": day.Add(-time.Hour).Format(time.RFC3339),
		}), &all)
		if len(all.Items) != 2 {
			t.Errorf("items = %+v, want both threads inside a window that covers the fixture day", all.Items)
		}
	})

	t.Run("since that is no window", func(t *testing.T) {
		got := callTool(t, url, callerRequest(), "list_work", map[string]any{"since": "last tuesday"})
		if !got.IsError || !strings.Contains(got.Text, "since") {
			t.Errorf("answer = %+v, want a tool error naming since", got)
		}
	})

	t.Run("repo that has no work", func(t *testing.T) {
		var got workList
		decodeTool(t, callToolOK(t, url, "list_work", map[string]any{"repo": "nobody/nothing"}), &got)
		if len(got.Items) != 0 {
			t.Errorf("items = %+v, want none", got.Items)
		}
	})
}

// TestMCPGetWorkItemAnswersWithItsRuns is the second tool's promise: one thread, the runs
// that made it, and the page to read it at.
func TestMCPGetWorkItemAnswersWithItsRuns(t *testing.T) {
	url := served(t)

	text := callToolOK(t, url, "get_work_item", map[string]any{"repo": appsRepo, "number": 12})
	if !strings.Contains(text, testBase+"/"+appsRepo+"/issues/12") {
		t.Errorf("answer has no Forgejo URL: %s", text)
	}

	var got struct {
		Repo   string `json:"repo"`
		Number int    `json:"number"`
		Kind   string `json:"kind"`
		Title  string `json:"title"`
		URL    string `json:"url"`
		Runs   []struct {
			ID      string   `json:"id"`
			Agent   string   `json:"agent"`
			Role    string   `json:"role"`
			Status  string   `json:"status"`
			Start   string   `json:"start"`
			End     *string  `json:"end"`
			CostUSD *float64 `json:"costUsd"`
		} `json:"runs"`
		PRs []listedItem `json:"prs"`
	}
	decodeTool(t, text, &got)

	if got.Repo != appsRepo || got.Number != 12 || got.Kind != "issue" {
		t.Errorf("subject = %s #%d (%s), want issue %s 12", got.Repo, got.Number, got.Kind, appsRepo)
	}
	if len(got.Runs) != 1 {
		t.Fatalf("runs = %+v, want the issue's one", got.Runs)
	}
	run := got.Runs[0]
	if run.ID != "inv-1" || run.Agent != "forgejo-refiner" || run.Role != "refiner" || run.Status != "success" {
		t.Errorf("run = %+v, want the refiner's successful inv-1", run)
	}
	if run.Start == "" || run.End == nil {
		t.Errorf("run times = start %q end %v, want both", run.Start, run.End)
	}
	if run.CostUSD == nil || *run.CostUSD != 0.25 {
		t.Errorf("run costUsd = %v, want 0.25", run.CostUSD)
	}
	if len(got.PRs) != 1 || got.PRs[0].Number != 34 {
		t.Errorf("prs = %+v, want pull 34", got.PRs)
	}

	// A pull request the list nests under its issue is still one number away: a caller
	// that only knows 34 reads it directly.
	var pull struct {
		Number int    `json:"number"`
		Kind   string `json:"kind"`
		URL    string `json:"url"`
		Runs   []struct {
			ID      string   `json:"id"`
			CostUSD *float64 `json:"costUsd"`
		} `json:"runs"`
	}
	decodeTool(t, callToolOK(t, url, "get_work_item", map[string]any{"repo": appsRepo, "number": 34}), &pull)
	if pull.Number != 34 || pull.Kind != "pull" {
		t.Errorf("subject = %s #%d, want pull 34", pull.Kind, pull.Number)
	}
	if want := testBase + "/" + appsRepo + "/pulls/34"; pull.URL != want {
		t.Errorf("URL = %q, want %q", pull.URL, want)
	}
	if len(pull.Runs) != 2 {
		t.Fatalf("runs = %+v, want the pull's two", pull.Runs)
	}
	// The running one was never priced: null, not 0.
	for _, r := range pull.Runs {
		if r.ID == "inv-3" && r.CostUSD != nil {
			t.Errorf("unpriced run %q costUsd = %v, want null", r.ID, *r.CostUSD)
		}
	}
}

// TestMCPGetWorkItemRefusesWhatItCannotLookUp covers the two ways the tool is asked for
// something it cannot answer: an argument missing, and a number nothing was run under.
func TestMCPGetWorkItemRefusesWhatItCannotLookUp(t *testing.T) {
	url := served(t)

	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{name: "no number", args: map[string]any{"repo": appsRepo}, want: "number"},
		{name: "number is not a number", args: map[string]any{"repo": appsRepo, "number": "twelve"}, want: "number"},
		{name: "no repo", args: map[string]any{"number": 12}, want: "repo"},
		{name: "repo is empty", args: map[string]any{"repo": " ", "number": 12}, want: "repo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := callTool(t, url, callerRequest(), "get_work_item", tt.args)
			if !got.IsError || !strings.Contains(got.Text, tt.want) {
				t.Errorf("answer = %+v, want a tool error naming %q", got, tt.want)
			}
		})
	}

	// A number with no runs behind it is not an error of the same kind: it is the
	// honest answer that there is no work there.
	got := callTool(t, url, callerRequest(), "get_work_item", map[string]any{"repo": appsRepo, "number": 999})
	if !got.IsError || got.Text != "not found" {
		t.Errorf("answer = %+v, want the tool error \"not found\"", got)
	}
	// Nor is a repository the plugin was never installed on.
	got = callTool(t, url, callerRequest(), "get_work_item", map[string]any{"repo": "nobody/nothing", "number": 1})
	if !got.IsError || got.Text != "not found" {
		t.Errorf("answer = %+v, want the tool error \"not found\"", got)
	}
}

// TestMCPToolCallWithoutACredential is ruling R21 stated as a test: a tool call the hub
// forwarded with no credential is refused rather than answered from the plugin's own
// identity, which sees every group the chart grants it.
func TestMCPToolCallWithoutACredential(t *testing.T) {
	spy := &spyClient{Fake: &hub.Fake{SeedRuns: fixtureRuns()}}
	srv := httptest.NewServer(NewHandler("0.1.0", serviceOver(t, spy), testSetupService(t)))
	t.Cleanup(srv.Close)

	for _, name := range []string{"list_work", "get_work_item"} {
		args := map[string]any{"repo": appsRepo, "number": 12}
		got := callTool(t, srv.URL, http.Header{}, name, args)
		if !got.IsError || got.Text != "missing credential" {
			t.Errorf("%s: answer = %+v, want the tool error \"missing credential\"", name, got)
		}
	}

	// And nothing was asked of the hub on the way: the refusal is the whole call.
	for _, call := range spy.Calls {
		if call == "Runs" {
			t.Error("a call with no credential still read the hub")
		}
	}
}

// TestMCPToolCallCarriesTheCallersCredential checks the other half of R21: the credential
// the hub forwarded is the one the hub is called back with, so what comes back is what that
// caller may see and no more.
func TestMCPToolCallCarriesTheCallersCredential(t *testing.T) {
	spy := &spyClient{Fake: &hub.Fake{SeedRuns: fixtureRuns()}}
	srv := httptest.NewServer(NewHandler("0.1.0", serviceOver(t, spy), testSetupService(t)))
	t.Cleanup(srv.Close)

	callToolOK(t, srv.URL, "list_work", map[string]any{"repo": appsRepo})
	if spy.credential != credential {
		t.Errorf("hub was called with credential %q, want the forwarded one", spy.credential)
	}

	callToolOK(t, srv.URL, "get_work_item", map[string]any{"repo": appsRepo, "number": 12})
	if spy.credential != credential {
		t.Errorf("hub was called with credential %q, want the forwarded one", spy.credential)
	}
}

// TestMCPToolFailureSaysWhatFailed is the difference between a hub that said no and a hub
// that did not answer: a caller who is not allowed to see the work is told so, and a caller
// whose work is unreachable is not told that about themselves.
func TestMCPToolFailureSaysWhatFailed(t *testing.T) {
	tests := []struct {
		name   string
		client hub.Client
		want   string
	}{
		{name: "hub says no", client: &spyClient{Fake: &hub.Fake{}, err: &hub.StatusError{Code: http.StatusForbidden}}, want: "forbidden"},
		{name: "hub is down", client: &spyClient{Fake: &hub.Fake{}, err: errors.New("connection refused")}, want: "hub unavailable"},
		{name: "no hub at all", client: nil, want: "hub not configured"},
	}

	for _, tool := range []string{"list_work", "get_work_item"} {
		for _, tt := range tests {
			t.Run(tool+"/"+tt.name, func(t *testing.T) {
				srv := httptest.NewServer(NewHandler("0.1.0", serviceOver(t, tt.client), testSetupService(t)))
				t.Cleanup(srv.Close)

				args := map[string]any{"repo": appsRepo}
				if tool == "get_work_item" {
					args["number"] = 12
				}
				got := callTool(t, srv.URL, callerRequest(), tool, args)
				if !got.IsError || got.Text != tt.want {
					t.Errorf("answer = %+v, want the tool error %q", got, tt.want)
				}
			})
		}
	}
}

// TestMCPRequestContext checks what the transport puts on a tool call's context: the
// credential the hub minted and the invocation the call belongs to, and neither when the
// hub sent none. A header the plugin cannot use leaves the context as it came rather than
// failing a call over a header the hub wrote.
func TestMCPRequestContext(t *testing.T) {
	tests := []struct {
		name       string
		header     http.Header
		wantCred   string
		wantInvoke string
	}{
		{
			name:       "an agent's call",
			header:     callerRequest(),
			wantCred:   credential,
			wantInvoke: "inv-tool-1",
		},
		{
			// A user's call through the hub: a credential, and no invocation.
			name:       "no invocation header",
			header:     http.Header{"X-Ainsel-Credential": {credential}},
			wantCred:   credential,
			wantInvoke: "",
		},
		{
			// A value that is not the shape the hub sends is no invocation: it must
			// not reach a log line or a filename downstream.
			name:       "invocation id the hub would never send",
			header:     http.Header{"X-Ainsel-Credential": {credential}, "X-Ainsel-Invocation-Id": {"../../etc/passwd"}},
			wantCred:   credential,
			wantInvoke: "",
		},
		{
			// The hub's own listing request: neither.
			name:       "no hub headers at all",
			header:     http.Header{},
			wantCred:   "",
			wantInvoke: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			for name, values := range tt.header {
				for _, v := range values {
					req.Header.Set(name, v)
				}
			}

			ctx := requestContext(context.Background(), req)
			if got, _ := hub.CredentialFromContext(ctx); got != tt.wantCred {
				t.Errorf("credential = %q, want %q", got, tt.wantCred)
			}
			if got := hub.InvocationIDFromContext(ctx); got != tt.wantInvoke {
				t.Errorf("invocation id = %q, want %q", got, tt.wantInvoke)
			}
		})
	}
}
