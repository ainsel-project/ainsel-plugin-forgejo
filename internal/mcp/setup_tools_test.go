package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"

	assets "github.com/ainsel-project/ainsel-plugin-forgejo"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/api"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/forgejo"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/manifest"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/settings"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
)

// -- the fixtures the setup tool tests are read under ------------------------------

// setupStore is the install store the setup service reads: an empty one for a new
// install, with the same Save/List/Delete the real store answers.
type setupStore struct {
	installs map[string]setup.Install
}

func (s *setupStore) Save(_ context.Context, in setup.Install) error {
	s.installs[in.Slug] = in
	return nil
}

func (s *setupStore) List(_ context.Context) ([]setup.Install, error) {
	out := make([]setup.Install, 0, len(s.installs))
	for _, in := range s.installs {
		out = append(out, in)
	}
	return out, nil
}

func (s *setupStore) Delete(_ context.Context, slug string) error {
	delete(s.installs, slug)
	return nil
}

// setupSettings is the settings store the wizard reads: every role on the same
// provider and model, the forge MCP server wired, and — for the sentinel test —
// whatever key values the test plants. The values are read raw, the way
// wizardSettings reads them; the masking that keeps keys out of a console answer
// belongs to the settings routes, not here.
type setupSettings struct {
	values map[string]string // field -> value, shared by every role
}

func (s setupSettings) All(_ context.Context) ([]settings.Setting, error) {
	var out []settings.Setting
	for _, role := range settings.Roles {
		for field, value := range s.values {
			out = append(out, settings.Setting{Role: string(role), Field: field, Value: value})
		}
	}
	return out, nil
}

// ListInstances answers the one instance the setup tools' tests plan and apply
// against — the baseUrl every request names (testBase). The instance list is a
// settings-store read like the role values, so it belongs to this fake.
func (s setupSettings) ListInstances(_ context.Context) ([]settings.Instance, error) {
	return []settings.Instance{{Name: "test", BaseURL: testBase}}, nil
}

// defaultSettings is what a plugin with no key set reads: a plain provider, the
// model, and the forge MCP wiring.
func defaultSettings() setupSettings {
	return setupSettings{values: map[string]string{
		settings.FieldLLMProvider:     "ollama-cloud",
		settings.FieldLLMModel:        "glm-4.7",
		settings.FieldMCPUrl:          "https://mcp.test/forgejo/mcp",
		settings.FieldMCPTokenFromEnv: setup.TokenEnvName,
	}}
}

// forgejoStub is the Forgejo the setup service points at: enough of the admin API
// for a snapshot (who am I, does the org exist, are the logins and teams taken)
// and for an apply's first step (create user), which the failure test fails on
// purpose.
type forgejoStub struct {
	orgExists      bool
	adminIsAdmin   bool
	failCreateUser bool
	teams          map[string]int64 // answered by GET orgs/{org}/teams
	orgHooks       []map[string]any // answered by GET orgs/{org}/hooks
	loginExists    map[string]bool  // answered by GET /api/v1/users/{login}
	srv            *httptest.Server
}

func (f *forgejoStub) client(t *testing.T) *forgejo.Client {
	t.Helper()

	if f.srv == nil {
		f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/v1/user" && r.Method == http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"login": "admin", "is_admin": f.adminIsAdmin})
			case r.URL.Path == "/api/v1/admin/users" && r.Method == http.MethodPost:
				if f.failCreateUser {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(map[string]any{"message": "username is invalid"})
					return
				}
				w.WriteHeader(http.StatusCreated)
			case strings.HasPrefix(r.URL.Path, "/api/v1/orgs/") && strings.HasSuffix(r.URL.Path, "/teams") && r.Method == http.MethodGet:
				if p := r.URL.Query().Get("page"); p != "" && p != "1" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte("[]"))
					return
				}
				out := []map[string]any{}
				for name, id := range f.teams {
					out = append(out, map[string]any{"id": id, "name": name})
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(out)
			case strings.HasPrefix(r.URL.Path, "/api/v1/orgs/") && strings.HasSuffix(r.URL.Path, "/hooks") && r.Method == http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(f.orgHooks)
			case strings.HasPrefix(r.URL.Path, "/api/v1/users/") && r.Method == http.MethodGet:
				login := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")
				if !f.loginExists[login] {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"login": login})
			case strings.HasPrefix(r.URL.Path, "/api/v1/orgs/") && strings.HasSuffix(r.URL.Path, "/teams"):
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("[]"))
			case strings.HasPrefix(r.URL.Path, "/api/v1/orgs/") && r.Method == http.MethodGet:
				if !f.orgExists {
					w.WriteHeader(http.StatusNotFound)
				}
			default:
				// The bot logins are not taken, and nothing else is answered to.
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		t.Cleanup(f.srv.Close)
	}
	return forgejo.New(f.srv.URL, nil)
}

// setupFixture is the whole dependency set one setup test reads: the hub, the
// Forgejo, the store and the settings the service is built on.
type setupFixture struct {
	hub      *hub.Fake
	forgejo  *forgejoStub
	store    *setupStore
	settings setupSettings
}

func newSetupFixture() *setupFixture {
	return &setupFixture{
		hub:      &hub.Fake{PluginGroupsSeed: []string{"g-1"}},
		forgejo:  &forgejoStub{orgExists: true, adminIsAdmin: true},
		store:    &setupStore{installs: map[string]setup.Install{}},
		settings: defaultSettings(),
	}
}

func (f *setupFixture) service(t *testing.T) *api.SetupService {
	t.Helper()
	return api.NewSetupService(f.hub, f.forgejo.client(t), f.store, f.settings, api.SetupConfig{
		ForgejoBaseURL: testBase,
		HubBaseURL:     "https://hub.test",
	})
}

// handler is the same service behind the same routes the console calls — the
// equivalence tests diff the tool's answer against this.
func (f *setupFixture) handler(t *testing.T) http.Handler {
	t.Helper()
	return f.service(t).Handler()
}

// testSetupService is the fixture service every NewHandler call carries: the
// setup tools are registered on every endpoint, and the work-tool tests never
// call them.
func testSetupService(t *testing.T) *api.SetupService {
	t.Helper()
	return newSetupFixture().service(t)
}

// -- the caller the setup tools answer to ------------------------------------------

// setupCallerHeader builds the headers a hub-proxied setup call arrives with: the
// credential, and a caller context of the test's choosing.
func setupCallerHeader(t *testing.T, isAdmin bool, groupsJSON string) http.Header {
	t.Helper()

	body := `{"pluginId":"forgejo","kind":"user","sub":"u-op","username":"op",` +
		`"isAdmin":` + boolLiteral(isAdmin) + `,"permissions":[],"groups":[` + groupsJSON + `],"invocationId":""}`
	if !json.Valid([]byte(body)) {
		t.Fatalf("test caller context %q is not valid JSON", body)
	}
	return http.Header{
		"X-Ainsel-Credential": {credential},
		"X-Ainsel-Context":    {base64.RawURLEncoding.EncodeToString([]byte(body))},
	}
}

func boolLiteral(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// adminCaller is the operator the setup is for: a user, an admin, a writer on g-1.
func adminCaller(t *testing.T) http.Header {
	t.Helper()
	return setupCallerHeader(t, true, `{"id":"g-1","name":"g-1","role":"owner"},{"id":"g-2","name":"g-2","role":"writer"}`)
}

// callSetupOK calls a setup tool as the admin operator it is for, and fails the
// test if the tool said it failed.
func callSetupOK(t *testing.T, url, name string, args map[string]any) string {
	t.Helper()

	got := callTool(t, url, adminCaller(t), name, args)
	if got.IsError {
		t.Fatalf("tools/call %s: tool failed: %s", name, got.Text)
	}
	return got.Text
}

// httpSetupCall calls one setup route over HTTP with the headers the console's
// proxied request carries, and returns the status and body.
func httpSetupCall(t *testing.T, h http.Handler, method, path string, body any, header http.Header) (int, string) {
	t.Helper()

	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	for name, values := range header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code, strings.TrimSpace(w.Body.String())
}

// planArgs are the typed arguments of a plan whose verdict is clear: a new
// install, a writable group, a live org, and a runtime for every role.
func planArgs() map[string]any {
	return map[string]any{
		"slug":       "demo",
		"groupId":    "g-1",
		"baseUrl":    testBase,
		"org":        "demo-org",
		"adminToken": "ADMINTOKEN-xyz",
		"roles": map[string]any{
			"refiner":   map[string]any{"runtime": "python:3.11"},
			"developer": map[string]any{"runtime": "python:3.11"},
			"reviewer":  map[string]any{"runtime": "python:3.11"},
		},
	}
}

// -- the equivalence with the HTTP routes -------------------------------------------

// TestMCPSetupOptionsMatchesHTTPRoute: the tool and the route answer the same
// question from the same service, so they answer it with the same bytes.
func TestMCPSetupOptionsMatchesHTTPRoute(t *testing.T) {
	f := newSetupFixture()
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), f.service(t)))
	t.Cleanup(srv.Close)

	code, httpBody := httpSetupCall(t, f.handler(t), http.MethodGet, "/api/setup/options", nil, adminCaller(t))
	if code != http.StatusOK {
		t.Fatalf("route: status = %d, want 200 (body %s)", code, httpBody)
	}

	got := callSetupOK(t, srv.URL, "setup_options", nil)
	if got != httpBody {
		t.Errorf("tool answer != route body\n tool: %s\n route: %s", got, httpBody)
	}

	// And the answer is the options shape: the writable group, the plugin group,
	// the base URL and the tool rails.
	var opts api.SetupOptions
	decodeTool(t, got, &opts)
	if len(opts.Groups) != 2 || opts.Groups[0].ID != "g-1" {
		t.Errorf("groups = %+v, want the caller's two writable groups", opts.Groups)
	}
	if opts.ForgejoBaseURL != testBase {
		t.Errorf("forgejoBaseURL = %q, want %q", opts.ForgejoBaseURL, testBase)
	}
	if len(opts.Tools.Defaults["refiner"]) == 0 {
		t.Error("tools.defaults.refiner is empty, want the shipped defaults")
	}
}

// TestMCPSetupPlanMatchesHTTPRoute checks the preview: same input, same caller,
// same bytes — including the verdict, which is part of a 200 here rather than an
// error.
func TestMCPSetupPlanMatchesHTTPRoute(t *testing.T) {
	f := newSetupFixture()
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), f.service(t)))
	t.Cleanup(srv.Close)

	code, httpBody := httpSetupCall(t, f.handler(t), http.MethodPost, "/api/setup/plan", planArgs(), adminCaller(t))
	if code != http.StatusOK {
		t.Fatalf("route: status = %d, want 200 (body %s)", code, httpBody)
	}

	got := callSetupOK(t, srv.URL, "setup_plan", planArgs())
	if got != httpBody {
		t.Errorf("tool answer != route body\n tool: %s\n route: %s", got, httpBody)
	}

	// The plan is a real preview: it names the resources and blocks on nothing.
	var plan setup.Result
	decodeTool(t, got, &plan)
	if len(plan.Verdict.Blocking) != 0 {
		t.Errorf("blocking = %q, want a clear verdict", plan.Verdict.Blocking)
	}
	if len(plan.Create) == 0 {
		t.Error("create is empty, want the install's resources")
	}
}

// TestMCPSetupApplyBlockedRefusesWithThePlan is the blocked-apply rule on the
// tool path: the refusal text is the plan JSON — the same body the route answers
// the 409 with — because the block only reads against the thing it blocks.
func TestMCPSetupApplyBlockedRefusesWithThePlan(t *testing.T) {
	f := newSetupFixture()
	// g-1 is not a plugin group as far as this hub says: the plan blocks.
	f.hub = &hub.Fake{PluginGroupsSeed: []string{"g-2"}}
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), f.service(t)))
	t.Cleanup(srv.Close)

	code, httpBody := httpSetupCall(t, f.handler(t), http.MethodPost, "/api/setup/apply", planArgs(), adminCaller(t))
	if code != http.StatusConflict {
		t.Fatalf("route: status = %d, want 409 (body %s)", code, httpBody)
	}

	got := callTool(t, srv.URL, adminCaller(t), "setup_apply", planArgs())
	if !got.IsError {
		t.Fatalf("setup_apply on a blocked plan: isError = false, want a refusal (text %s)", got.Text)
	}
	if got.Text != httpBody {
		t.Errorf("refusal text != 409 body\n tool: %s\n route: %s", got.Text, httpBody)
	}

	// The refusal is the whole plan, verdict included — not just the messages.
	var plan setup.Result
	if err := json.Unmarshal([]byte(got.Text), &plan); err != nil {
		t.Fatalf("refusal is not the plan JSON: %v", err)
	}
	if len(plan.Verdict.Blocking) == 0 {
		t.Error("refusal plan carries no blocking verdict")
	}
}

// TestMCPSetupApplyFailureMatchesHTTPRoute checks the failed-apply rendering: the
// tool's refusal names the failing step and service, the same text the route's
// 502 body carries in its error field.
func TestMCPSetupApplyFailureMatchesHTTPRoute(t *testing.T) {
	f := newSetupFixture()
	f.forgejo.failCreateUser = true
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), f.service(t)))
	t.Cleanup(srv.Close)

	code, httpBody := httpSetupCall(t, f.handler(t), http.MethodPost, "/api/setup/apply", planArgs(), adminCaller(t))
	if code != http.StatusBadGateway {
		t.Fatalf("route: status = %d, want 502 (body %s)", code, httpBody)
	}
	var failure struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(httpBody), &failure); err != nil {
		t.Fatalf("decode 502 body: %v", err)
	}
	if failure.Error == "" {
		t.Fatalf("502 body carries no error text: %s", httpBody)
	}

	got := callTool(t, srv.URL, adminCaller(t), "setup_apply", planArgs())
	if !got.IsError {
		t.Fatalf("setup_apply on a failed apply: isError = false, want a refusal (text %s)", got.Text)
	}
	if got.Text != failure.Error {
		t.Errorf("refusal text = %q, want the route's error text %q", got.Text, failure.Error)
	}
}

// -- the refusals -------------------------------------------------------------------

// TestMCPSetupToolsRefuseWhatTheGateRefuses is the same-refusal contract: a call
// with no credential, from a non-admin, or on a group the caller cannot write is
// refused by the tool with the very name the route answers with.
func TestMCPSetupToolsRefuseWhatTheGateRefuses(t *testing.T) {
	f := newSetupFixture()
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), f.service(t)))
	t.Cleanup(srv.Close)
	route := f.handler(t)

	tests := []struct {
		name       string
		header     http.Header
		routeCode  int
		routeError string
		toolText   string
	}{
		{
			name:       "no credential",
			header:     http.Header{},
			routeCode:  http.StatusUnauthorized,
			routeError: "missing credential",
			toolText:   "missing credential",
		},
		{
			name:       "not an admin",
			header:     setupCallerHeader(t, false, `{"id":"g-1","name":"g-1","role":"owner"}`),
			routeCode:  http.StatusForbidden,
			routeError: "admin only",
			toolText:   "admin only",
		},
		{
			name:       "not a caller at all",
			header:     http.Header{"X-Ainsel-Credential": {credential}},
			routeCode:  http.StatusForbidden,
			routeError: "admin only",
			toolText:   "admin only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The gate refuses before any group check, on every route and tool.
			code, body := httpSetupCall(t, route, http.MethodPost, "/api/setup/plan", planArgs(), tt.header)
			if code != tt.routeCode {
				t.Fatalf("route: status = %d, want %d (body %s)", code, tt.routeCode, body)
			}
			var e struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal([]byte(body), &e); err != nil || e.Error != tt.routeError {
				t.Fatalf("route body = %s, want error %q", body, tt.routeError)
			}

			for _, tool := range []string{"setup_options", "setup_plan", "setup_apply"} {
				got := callTool(t, srv.URL, tt.header, tool, planArgs())
				if !got.IsError || got.Text != tt.toolText {
					t.Errorf("%s: refusal = (isError %v, text %q), want %q", tool, got.IsError, got.Text, tt.toolText)
				}
			}
		})
	}
}

// TestMCPSetupPlanAndApplyRefuseAGroupTheCallerCannotWrite pins the group rule.
// The contract note from the issue calls it a 403; the route has always answered
// it as a bad request naming groupId — and the tool answers with the same name,
// which is what "the same refusal" means.
func TestMCPSetupPlanAndApplyRefuseAGroupTheCallerCannotWrite(t *testing.T) {
	f := newSetupFixture()
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), f.service(t)))
	t.Cleanup(srv.Close)
	route := f.handler(t)

	// An admin, but only a member on g-1: no write, so no plan and no apply.
	header := setupCallerHeader(t, true, `{"id":"g-1","name":"g-1","role":"member"}`)

	code, body := httpSetupCall(t, route, http.MethodPost, "/api/setup/plan", planArgs(), header)
	if code != http.StatusBadRequest {
		t.Fatalf("route plan: status = %d, want 400 (body %s)", code, body)
	}
	if !strings.Contains(body, `"groupId"`) {
		t.Fatalf("route plan body = %s, want the groupId refusal", body)
	}

	for _, tool := range []string{"setup_plan", "setup_apply"} {
		got := callTool(t, srv.URL, header, tool, planArgs())
		if !got.IsError || got.Text != "groupId" {
			t.Errorf("%s: refusal = (isError %v, text %q), want %q", tool, got.IsError, got.Text, "groupId")
		}
	}
}

// TestMCPSetupToolsWithoutACredential checks the tools against a caller the hub
// never authenticated: the work tools refuse with "missing credential", and so
// does every setup tool — the setup is the last place to answer from the
// plugin's own identity.
func TestMCPSetupToolsWithoutACredential(t *testing.T) {
	f := newSetupFixture()
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), f.service(t)))
	t.Cleanup(srv.Close)

	for _, tool := range []string{"setup_options", "setup_plan", "setup_apply"} {
		got := callTool(t, srv.URL, http.Header{}, tool, planArgs())
		if !got.IsError || got.Text != "missing credential" {
			t.Errorf("%s: refusal = (isError %v, text %q), want %q", tool, got.IsError, got.Text, "missing credential")
		}
	}
}

// -- the typed arguments -------------------------------------------------------------

// TestMCPSetupToolSchemas checks what the tools declare: plan and apply take the
// route's request fields, required ones named, roles an object; and the
// annotations say what each tool does before anyone calls it.
func TestMCPSetupToolSchemas(t *testing.T) {
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), testSetupService(t)))
	t.Cleanup(srv.Close)

	var listed struct {
		Tools []struct {
			Name        string          `json:"name"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	callResult(t, srv.URL, 3, "tools/list", map[string]any{}, &listed)

	byName := map[string]map[string]any{}
	for _, tool := range listed.Tools {
		var raw map[string]any
		if err := json.Unmarshal(tool.InputSchema, &raw); err != nil {
			t.Fatalf("tool %s: decode inputSchema: %v", tool.Name, err)
		}
		byName[tool.Name] = raw
	}

	for _, name := range []string{"setup_plan", "setup_apply"} {
		raw, ok := byName[name]
		if !ok {
			t.Fatalf("tool %s is not listed", name)
		}
		required, _ := raw["required"].([]any)
		var wantRequired []string
		for _, r := range required {
			if s, ok := r.(string); ok {
				wantRequired = append(wantRequired, s)
			}
		}
		for _, want := range []string{"slug", "groupId", "baseUrl", "org", "adminToken"} {
			if !slices.Contains(wantRequired, want) {
				t.Errorf("%s: required = %v, want %q among them", name, required, want)
			}
		}
		props, _ := raw["properties"].(map[string]any)
		if props == nil {
			t.Fatalf("%s: no properties", name)
		}
		if _, ok := props["roles"]; !ok {
			t.Errorf("%s: properties have no roles argument", name)
		}
	}

	// setup_options takes nothing.
	if raw, ok := byName["setup_options"]; ok {
		if props := raw["properties"]; props != nil {
			if m, _ := props.(map[string]any); len(m) != 0 {
				t.Errorf("setup_options takes %v, want no arguments", props)
			}
		}
	}
}

// TestMCPSetupToolAnnotations pins the hints: the reads read, the write warns.
func TestMCPSetupToolAnnotations(t *testing.T) {
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), testSetupService(t)))
	t.Cleanup(srv.Close)

	var listed struct {
		Tools []struct {
			Name        string           `json:"name"`
			Annotations *map[string]bool `json:"annotations"`
		} `json:"tools"`
	}
	callResult(t, srv.URL, 3, "tools/list", map[string]any{}, &listed)

	want := map[string]map[string]bool{
		"setup_options":        {"readOnlyHint": true, "destructiveHint": false},
		"setup_plan":           {"readOnlyHint": true, "destructiveHint": false},
		"setup_apply":          {"readOnlyHint": false, "destructiveHint": true, "openWorldHint": true},
		"setup_orphans_plan":   {"readOnlyHint": true, "destructiveHint": false},
		"setup_orphans_remove": {"readOnlyHint": false, "destructiveHint": true, "openWorldHint": true},
	}
	for _, tool := range listed.Tools {
		checks, ok := want[tool.Name]
		if !ok {
			continue
		}
		if tool.Annotations == nil {
			t.Fatalf("%s: no annotations, want them stated", tool.Name)
		}
		for hint, value := range checks {
			if got := (*tool.Annotations)[hint]; got != value {
				t.Errorf("%s: annotation %s = %v, want %v", tool.Name, hint, got, value)
			}
		}
	}
}

// TestMCPSetupPlanRefusesAnUnknownRole: a caller that typed "devloper" has not
// configured the developer, and silently shipping the defaults would say
// otherwise.
func TestMCPSetupPlanRefusesAnUnknownRole(t *testing.T) {
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), testSetupService(t)))
	t.Cleanup(srv.Close)

	args := planArgs()
	args["roles"] = map[string]any{"devloper": map[string]any{"runtime": "python:3.11"}}
	got := callTool(t, srv.URL, adminCaller(t), "setup_plan", args)
	if !got.IsError || !strings.Contains(got.Text, "unknown role") {
		t.Fatalf("setup_plan with an unknown role: (isError %v, text %q), want a refusal naming the role", got.IsError, got.Text)
	}
}

// TestMCPSetupPlanRefusesAMissingRequiredArgument: the refusal names the argument
// that is missing, so a caller that gave four of five learns what to add.
func TestMCPSetupPlanRefusesAMissingRequiredArgument(t *testing.T) {
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), testSetupService(t)))
	t.Cleanup(srv.Close)

	args := planArgs()
	delete(args, "slug")
	got := callTool(t, srv.URL, adminCaller(t), "setup_plan", args)
	if !got.IsError || !strings.Contains(got.Text, "slug") {
		t.Fatalf("setup_plan without slug: (isError %v, text %q), want a refusal naming slug", got.IsError, got.Text)
	}
}

// TestMCPSetupPlanOmittedRolesReadsAsTheConsoleDefault: no roles argument is the
// wizard's default form — every role on its defaults, which for a new install
// means no runtime, which blocks. The plan still answers, verdict and all.
func TestMCPSetupPlanOmittedRolesReadsAsTheConsoleDefault(t *testing.T) {
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), testSetupService(t)))
	t.Cleanup(srv.Close)

	args := planArgs()
	delete(args, "roles")
	got := callSetupOK(t, srv.URL, "setup_plan", args)

	var plan setup.Result
	decodeTool(t, got, &plan)
	if len(plan.Verdict.Blocking) == 0 {
		t.Error("a new install with no runtimes planned clear, want it blocked")
	}
}

// -- the manifest --------------------------------------------------------------------

// TestMCPSetupManifestCoversRegisteredTools is the coupling the hub reads: every
// tool the endpoint lists is declared in mcp.audience, and nothing the manifest
// declares is missing from the endpoint. The setup tools are operator-facing —
// that is what keeps them out of an agent's tool set.
func TestMCPSetupManifestCoversRegisteredTools(t *testing.T) {
	m, err := manifest.Parse(assets.ManifestJSON)
	if err != nil {
		t.Fatalf("parse shipped manifest: %v", err)
	}

	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), testSetupService(t)))
	t.Cleanup(srv.Close)

	var listed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	callResult(t, srv.URL, 4, "tools/list", map[string]any{}, &listed)

	registered := map[string]bool{}
	for _, tool := range listed.Tools {
		registered[tool.Name] = true
		if _, declared := m.MCP.Audience[tool.Name]; !declared {
			t.Errorf("tool %s is registered but has no mcp.audience entry", tool.Name)
		}
	}
	for name := range m.MCP.Audience {
		if !registered[name] {
			t.Errorf("manifest declares audience for %q, but the endpoint does not list it", name)
		}
	}

	for _, tool := range []string{"setup_options", "setup_plan", "setup_apply"} {
		if got := m.ToolAudience(tool); got != manifest.AudienceOperator {
			t.Errorf("ToolAudience(%q) = %q, want %q", tool, got, manifest.AudienceOperator)
		}
	}
}

// -- no secrets in the answers ---------------------------------------------------------

// TestMCPSetupToolsCarryNoSecrets is the sentinel lock on R4 for the tool path.
// The admin token and the four settings keys flow into plan and apply; none of
// them may come back out — not in a plan, not in the blocked-apply payload, and
// not in the rendered error of a failed apply, the two places that render foreign
// error text being the apply failure and nothing else. The assertions read the
// raw tool text, so an echo inside a wrapped string still fails.
func TestMCPSetupToolsCarryNoSecrets(t *testing.T) {
	sentinels := []string{
		"SENTINEL-ADMIN-TOKEN-0123",
		"SENTINEL-CUSTOM-KEY-8f2a",
		"SENTINEL-OPENAI-KEY-1b3c",
		"SENTINEL-ANTHROPIC-KEY-4d5e",
		"SENTINEL-GOOGLE-KEY-7f9a",
	}

	newFixture := func() *setupFixture {
		f := newSetupFixture()
		// Every key the settings store can hold, planted, on the provider that
		// reads them all: the custom one, with its endpoint.
		f.settings = setupSettings{values: map[string]string{
			settings.FieldLLMProvider:     "custom",
			settings.FieldLLMModel:        "some-model",
			settings.FieldLLMCustomUrl:    "https://llm.test/v1",
			settings.FieldLLMCustomKey:    sentinels[1],
			settings.FieldLLMOpenAIKey:    sentinels[2],
			settings.FieldLLMAnthropicKey: sentinels[3],
			settings.FieldLLMGoogleKey:    sentinels[4],
			settings.FieldMCPUrl:          "https://mcp.test/forgejo/mcp",
			settings.FieldMCPTokenFromEnv: setup.TokenEnvName,
		}}
		return f
	}

	// The clear plan, the blocked apply and the failed apply: every answer the
	// setup tools give with these arguments is read for a sentinel.
	cases := []struct {
		name    string
		fixture func() *setupFixture
		tool    string
		ok      bool
	}{
		{name: "plan", fixture: newFixture, tool: "setup_plan", ok: true},
		{
			name: "blocked apply",
			fixture: func() *setupFixture {
				f := newFixture()
				f.hub = &hub.Fake{PluginGroupsSeed: []string{"g-2"}}
				return f
			},
			tool: "setup_apply",
		},
		{
			name: "failed apply",
			fixture: func() *setupFixture {
				f := newFixture()
				f.forgejo.failCreateUser = true
				return f
			},
			tool: "setup_apply",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.fixture()
			srv := httptest.NewServer(NewHandler("0.1.0", testService(t), f.service(t)))
			t.Cleanup(srv.Close)

			args := planArgs()
			args["adminToken"] = sentinels[0]

			got := callTool(t, srv.URL, adminCaller(t), tt.tool, args)
			if tt.ok && got.IsError {
				t.Fatalf("%s failed: %s", tt.tool, got.Text)
			}
			for _, sentinel := range sentinels {
				if strings.Contains(got.Text, sentinel) {
					t.Errorf("%s: answer leaks %s: %s", tt.tool, sentinel, got.Text)
				}
			}

			// The same arguments through the route: the contract is the route's,
			// and the tool must not be the one that leaks.
			code, body := httpSetupCall(t, f.handler(t), http.MethodPost, "/api/setup/"+tt.tool, args, adminCaller(t))
			_ = code
			for _, sentinel := range sentinels {
				if strings.Contains(body, sentinel) {
					t.Errorf("route %s: body leaks %s: %s", tt.tool, sentinel, body)
				}
			}
		})
	}
}

// -- the working-hours window ---------------------------------------------------------

// TestMCPSetupPlanCarriesTheWindowTheCallerNamed: the roles argument takes the same window the
// console's form writes, and the plan answers with the op that names it — byte for byte what the
// route answers for the same body, because both read one Input and one Plan.
func TestMCPSetupPlanCarriesTheWindowTheCallerNamed(t *testing.T) {
	f := newSetupFixture()
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), f.service(t)))
	t.Cleanup(srv.Close)

	args := planArgs()
	args["roles"].(map[string]any)["developer"] = map[string]any{
		"runtime": "python:3.11",
		"workingHours": map[string]any{
			"timezone": "Europe/Berlin",
			"days":     []any{float64(1), float64(2), float64(3), float64(4), float64(5)},
			"start":    "09:00",
			"end":      "17:00",
		},
	}

	code, httpBody := httpSetupCall(t, f.handler(t), http.MethodPost, "/api/setup/plan", args, adminCaller(t))
	if code != http.StatusOK {
		t.Fatalf("route: status = %d, want 200 (body %s)", code, httpBody)
	}
	got := callSetupOK(t, srv.URL, "setup_plan", args)
	if got != httpBody {
		t.Errorf("tool answer != route body\n tool: %s\n route: %s", got, httpBody)
	}

	var plan setup.Result
	decodeTool(t, got, &plan)
	if len(plan.Verdict.Blocking) != 0 {
		t.Fatalf("blocking = %q, want a clear verdict", plan.Verdict.Blocking)
	}
	var named int
	for _, op := range plan.Create {
		if op.Kind == setup.KindAgent && strings.Contains(op.Detail, "working hours") {
			named++
			if !strings.Contains(op.Detail, "Mon–Fri 09:00–17:00 Europe/Berlin") {
				t.Errorf("agent op %q, want the window the caller named", op.Detail)
			}
		}
	}
	// One role named a window, so one agent says so — and the two that said nothing are the
	// plan's promise that it writes nothing for them.
	if named != 1 {
		t.Errorf("%d agent creates name a window, want 1; create = %v", named, plan.Create)
	}
}

// TestMCPSetupPlanOmittedWindowClearsTheRow is the tool path's half of the contract the schema
// states: an omitted `workingHours` is "any hour", and on an edit against a row that records a
// window the plan answers with the clear, not with silence. The console form has always pinned
// this ("an explicit clear is an op"); the tool path is the case an MCP caller can get wrong, so
// it is a test here rather than a line in the docs.
func TestMCPSetupPlanOmittedWindowClearsTheRow(t *testing.T) {
	f := newSetupFixture()

	const slug = "demo"
	w := &setup.WorkingHours{Timezone: "Europe/Berlin", Start: "09:00", End: "17:00", Days: []int{1, 2, 3, 4, 5}}
	row := setup.Install{
		Slug:    slug,
		GroupID: "g-1",
		Org:     "demo-org",
		Roles:   map[setup.Role]setup.RoleState{},
	}
	for _, r := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
		row.Roles[r] = setup.RoleState{
			AgentID:  "ag-" + string(r),
			ImageID:  "img-" + string(r),
			BotLogin: setup.BotLogin(r, slug),
		}
	}
	// The window lives on the developer's row alone: the wizard wrote it there and nowhere else.
	rs := row.Roles[setup.RoleDeveloper]
	rs.WorkingHours = setup.CloneWorkingHours(w)
	row.Roles[setup.RoleDeveloper] = rs
	f.store.installs[slug] = row
	f.hub.SeedAgents = map[string]hub.Agent{}
	f.hub.SeedImages = map[string]hub.Image{}
	for _, r := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
		f.hub.SeedAgents["ag-"+string(r)] = hub.Agent{
			ID:   "ag-" + string(r),
			Name: setup.AgentName(r, slug),
			LLM:  &hub.LLMSpec{Provider: "ollama-cloud", Model: "glm-4.7"},
		}
		f.hub.SeedImages["img-"+string(r)] = hub.Image{
			ID:       "img-" + string(r),
			ImageURL: "python:3.11",
		}
	}

	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), f.service(t)))
	t.Cleanup(srv.Close)

	// The same request that wrote the window, minus the window: every role names a runtime and
	// none of them says anything about hours.
	args := planArgs()
	got := callSetupOK(t, srv.URL, "setup_plan", args)

	var plan setup.Result
	decodeTool(t, got, &plan)
	if len(plan.Verdict.Blocking) != 0 {
		t.Fatalf("blocking = %q, want a clear verdict (answer %s)", plan.Verdict.Blocking, got)
	}

	var cleared, silent int
	for _, op := range plan.Change {
		if op.Kind != setup.KindAgent || !strings.Contains(op.Detail, "working hours") {
			continue
		}
		if op.Role == setup.RoleDeveloper {
			cleared++
			if !strings.Contains(op.Detail, `"Mon–Fri 09:00–17:00 Europe/Berlin" -> "any hours"`) {
				t.Errorf("developer op %q, want the clear against the row's window", op.Detail)
			}
		} else {
			silent++
		}
	}
	if cleared != 1 {
		t.Errorf("%d working-hours changes clear the row, want 1; change = %s", cleared, plan.Change)
	}
	// The two roles whose row never carried a window are the plan's promise that an omission
	// writes nothing where there was nothing to clear.
	if silent != 0 {
		t.Errorf("%d working-hours changes for roles whose row carried no window, want 0", silent)
	}
}

// TestMCPSetupPlanRefusesAWindowThatIsNotAnObject: a window has to be the object the schema
// describes, and the refusal names the argument so a caller that sent a string or a list of days
// knows what to fix. A window that is an object but says something the hub cannot use — a zone
// that does not exist, a bound that is not HH:MM — is not the argument's problem: the plan blocks
// on it, the way it blocks on the console's form.
func TestMCPSetupPlanRefusesAWindowThatIsNotAnObject(t *testing.T) {
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), testSetupService(t)))
	t.Cleanup(srv.Close)

	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"a string", "09:00-17:00", "workingHours must be an object"},
		{"a list", []any{"Mon"}, "workingHours must be an object"},
		{"days that are not numbers", map[string]any{"days": []any{"Mon"}}, "days must be a list of weekday numbers"},
		{"a bound that is not a string", map[string]any{"start": 900}, "start must be an HH:MM time"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := planArgs()
			args["roles"].(map[string]any)["developer"] = map[string]any{
				"runtime":      "python:3.11",
				"workingHours": tc.value,
			}
			got := callTool(t, srv.URL, adminCaller(t), "setup_plan", args)
			if !got.IsError || !strings.Contains(got.Text, tc.want) {
				t.Fatalf("setup_plan with a bad window: (isError %v, text %q), want a refusal saying %q",
					got.IsError, got.Text, tc.want)
			}
		})
	}
}

// TestMCPSetupPlanBlocksAWindowTheHubWouldRefuse is the plan's guard on the tool path: the
// argument is well-formed, so it reaches the plan — and the plan refuses it there, with the same
// sentence the console's form gets, rather than letting an apply half-build an install and fail on
// a 400.
func TestMCPSetupPlanBlocksAWindowTheHubWouldRefuse(t *testing.T) {
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), testSetupService(t)))
	t.Cleanup(srv.Close)

	args := planArgs()
	args["roles"].(map[string]any)["developer"] = map[string]any{
		"runtime":      "python:3.11",
		"workingHours": map[string]any{"timezone": "Mars/Olympus_Mons", "start": "09:00", "end": "17:00"},
	}
	got := callSetupOK(t, srv.URL, "setup_plan", args)

	var plan setup.Result
	decodeTool(t, got, &plan)
	var found string
	for _, b := range plan.Verdict.Blocking {
		if strings.Contains(b, "working hours") {
			found = b
		}
	}
	if found == "" {
		t.Fatalf("blocking = %q, want the plan to refuse the zone", plan.Verdict.Blocking)
	}
	if !strings.Contains(found, "developer") {
		t.Errorf("block %q, want it to name the role", found)
	}
}

// TestMCPSetupRoleSchemaOffersTheWindow: the window is on the tool's schema, because a caller that
// cannot see it cannot set it — and the description is where the difference between omitting it and
// sending an empty object is stated.
func TestMCPSetupRoleSchemaOffersTheWindow(t *testing.T) {
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), testSetupService(t)))
	t.Cleanup(srv.Close)

	var listed struct {
		Tools []struct {
			Name        string         `json:"name"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	callResult(t, srv.URL, 3, "tools/list", map[string]any{}, &listed)

	for _, tool := range listed.Tools {
		if tool.Name != "setup_plan" && tool.Name != "setup_apply" {
			continue
		}
		props, _ := tool.InputSchema["properties"].(map[string]any)
		roles, _ := props["roles"].(map[string]any)
		rolesProps, _ := roles["properties"].(map[string]any)
		// The role is an object schema, so its fields sit under its own "properties".
		developer, _ := rolesProps["developer"].(map[string]any)
		developerProps, _ := developer["properties"].(map[string]any)
		hours, ok := developerProps["workingHours"].(map[string]any)
		if !ok {
			t.Fatalf("%s: the role schema has no workingHours; role fields = %v", tool.Name, keysOf(developerProps))
		}
		hoursProps, _ := hours["properties"].(map[string]any)
		for _, field := range []string{"timezone", "days", "start", "end"} {
			if _, ok := hoursProps[field]; !ok {
				t.Errorf("%s: workingHours has no %s", tool.Name, field)
			}
		}
		if desc, _ := hours["description"].(string); !strings.Contains(desc, "Omit it for any hour") {
			t.Errorf("%s: workingHours description = %q, want it to state what omitting means",
				tool.Name, desc)
		}
	}
}

// -- the developer's PR patrol ---------------------------------------------------------

// TestMCPSetupRoleSchemaHasNoPatrol: the patrol is one plugin-wide setting now, so the tool's role
// schema offers no per-install patrol — a caller that could set one here would be setting a value
// nothing reads.
func TestMCPSetupRoleSchemaHasNoPatrol(t *testing.T) {
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), testSetupService(t)))
	t.Cleanup(srv.Close)

	var listed struct {
		Tools []struct {
			Name        string         `json:"name"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	callResult(t, srv.URL, 3, "tools/list", map[string]any{}, &listed)

	for _, tool := range listed.Tools {
		if tool.Name != "setup_plan" && tool.Name != "setup_apply" {
			continue
		}
		props, _ := tool.InputSchema["properties"].(map[string]any)
		roles, _ := props["roles"].(map[string]any)
		rolesProps, _ := roles["properties"].(map[string]any)
		developer, _ := rolesProps["developer"].(map[string]any)
		developerProps, _ := developer["properties"].(map[string]any)
		if _, ok := developerProps["patrol"]; ok {
			t.Errorf("%s: the role schema still offers a per-install patrol", tool.Name)
		}
	}
}

// keysOf names a schema object's fields, sorted, so a failure to find one says what is there.
func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// -- the orphan sweep tools -----------------------------------------------------------

// orphanFixture: the same service, on a Forgejo that holds some of what the slug
// "ai" left behind — two of its three bots, two teams, and two org hooks of which
// one points at the hub.
func orphanFixture() *setupFixture {
	f := newSetupFixture()
	f.forgejo.loginExists = map[string]bool{"refiner-ai": true, "reviewer-ai": true}
	f.forgejo.teams = map[string]int64{"ainsel-refiner-ai": 11, "ainsel-developer-ai": 12}
	f.forgejo.orgHooks = []map[string]any{
		{"id": 5, "type": "forgejo", "active": true,
			"config": map[string]any{"url": "https://hub.test/api/v1/connectors/c-9/webhook"}},
		{"id": 6, "type": "gitea", "active": true,
			"config": map[string]any{"url": "https://human.example/webhook"}},
	}
	return f
}

func orphanArgs() map[string]any {
	return map[string]any{
		"slug":       "ai",
		"baseUrl":    testBase,
		"org":        "demo-org",
		"adminToken": "ADMINTOKEN-xyz",
	}
}

// TestMCPOrphanToolsRefuseWhatTheGateRefuses: the sweep tools stand behind the
// same gate as the wizard's — no credential, no caller, no admin: refused, on the
// tool exactly as on the route.
func TestMCPOrphanToolsRefuseWhatTheGateRefuses(t *testing.T) {
	f := orphanFixture()
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), f.service(t)))
	t.Cleanup(srv.Close)

	for _, tt := range []struct {
		header   http.Header
		toolText string
	}{
		{http.Header{}, "missing credential"},
		{setupCallerHeader(t, false, `{"id":"g-1","name":"g-1","role":"owner"}`), "admin only"},
	} {
		for _, tool := range []string{"setup_orphans_plan", "setup_orphans_remove"} {
			got := callTool(t, srv.URL, tt.header, tool, orphanArgs())
			if !got.IsError || got.Text != tt.toolText {
				t.Errorf("%s: refusal = (isError %v, text %q), want %q", tool, got.IsError, got.Text, tt.toolText)
			}
		}
	}
}

// TestMCPOrphanPlanMatchesHTTPRoute: the tool and the route read the same service,
// so they answer with the same bytes — the same five candidates, the human's hook
// left out of both.
func TestMCPOrphanPlanMatchesHTTPRoute(t *testing.T) {
	f := orphanFixture()
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), f.service(t)))
	t.Cleanup(srv.Close)
	route := f.handler(t)

	toolText := callSetupOK(t, srv.URL, "setup_orphans_plan", orphanArgs())
	code, routeBody := httpSetupCall(t, route, http.MethodPost, "/api/setup/orphans/ai/plan", orphanArgs(), adminCaller(t))
	if code != http.StatusOK {
		t.Fatalf("route: %d %s", code, routeBody)
	}

	var toolJSON, routeJSON any
	if err := json.Unmarshal([]byte(toolText), &toolJSON); err != nil {
		t.Fatalf("tool text is not json: %v (%s)", err, toolText)
	}
	if err := json.Unmarshal([]byte(routeBody), &routeJSON); err != nil {
		t.Fatalf("route body is not json: %v (%s)", err, routeBody)
	}
	if a, b := toolJSON.(map[string]any)["items"], routeJSON.(map[string]any)["items"]; !slices.Equal(
		stringsIn(a), stringsIn(b)) {
		t.Errorf("tool items = %v, route items = %v, want the same", a, b)
	}
	if n := len(stringsIn(routeJSON.(map[string]any)["items"])); n != 5 {
		t.Errorf("items = %d, want 5 (two users, two teams, the hub-pointing hook): %s", n, routeBody)
	}
}

// stringsIn renders a plan's items by kind:name, the way the tests compare them.
func stringsIn(v any) []string {
	list, _ := v.([]any)
	out := []string{}
	for _, it := range list {
		m, _ := it.(map[string]any)
		out = append(out, fmt.Sprintf("%v:%v", m["kind"], m["name"]))
	}
	slices.Sort(out)
	return out
}

// TestMCPOrphanRemoveDeletesThePlannedSet: the destructive tool deletes what the
// plan showed and reports it by kind and name.
func TestMCPOrphanRemoveDeletesThePlannedSet(t *testing.T) {
	f := orphanFixture()
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), f.service(t)))
	t.Cleanup(srv.Close)

	text := callSetupOK(t, srv.URL, "setup_orphans_remove", orphanArgs())
	var report struct {
		Deleted []string `json:"deleted"`
		Failed  []string `json:"failed"`
	}
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		t.Fatalf("report is not json: %v (%s)", err, text)
	}
	if len(report.Failed) != 0 || len(report.Deleted) != 5 {
		t.Errorf("report = %+v, want five deleted and none failed", report)
	}
}

// TestMCPOrphanRemoveRefusesAMissingRequiredArgument: the refusal names the
// argument, the way the wizard tools do.
func TestMCPOrphanRemoveRefusesAMissingRequiredArgument(t *testing.T) {
	srv := httptest.NewServer(NewHandler("0.1.0", testService(t), testSetupService(t)))
	t.Cleanup(srv.Close)

	args := orphanArgs()
	delete(args, "slug")
	got := callTool(t, srv.URL, adminCaller(t), "setup_orphans_remove", args)
	if !got.IsError || !strings.Contains(got.Text, "slug") {
		t.Fatalf("without slug: (isError %v, text %q), want a refusal naming slug", got.IsError, got.Text)
	}
}
