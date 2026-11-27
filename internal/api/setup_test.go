package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel-plugin-forgejo/content"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/forgejo"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/settings"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
)

// fakeSettings stands in for the settings store on the setup routes: every role
// runs on the same model and the forge MCP server has its endpoint. The wizard
// reads these instead of collecting them; a test that wants them empty builds
// its own reader.
type fakeSettings struct{}

func (fakeSettings) All(ctx context.Context) ([]settings.Setting, error) {
	var out []settings.Setting
	for _, role := range settings.Roles {
		out = append(out,
			settings.Setting{Role: string(role), Field: settings.FieldLLMProvider, Value: "ollama-cloud"},
			settings.Setting{Role: string(role), Field: settings.FieldLLMModel, Value: "glm-4.7"},
			settings.Setting{Role: string(role), Field: settings.FieldMCPUrl, Value: "https://mcp.test/forgejo/mcp"},
			settings.Setting{Role: string(role), Field: settings.FieldMCPTokenFromEnv, Value: setup.TokenEnvName},
		)
	}
	return out, nil
}

// ListInstances answers the one instance the tests plan and apply against —
// the baseUrl every setup request in this file names. A test that wants the
// list empty or different builds its own reader.
func (fakeSettings) ListInstances(ctx context.Context) ([]settings.Instance, error) {
	return []settings.Instance{{Name: "test", BaseURL: "https://git.test"}}, nil
}

// fakeStore implements a minimal store for testing
type fakeStore struct {
	installs map[string]setup.Install
}

func (s *fakeStore) Save(ctx context.Context, install setup.Install) error {
	s.installs[install.Slug] = install
	return nil
}

func (s *fakeStore) List(ctx context.Context) ([]setup.Install, error) {
	installs := make([]setup.Install, 0, len(s.installs))
	for _, install := range s.installs {
		installs = append(installs, install)
	}
	return installs, nil
}

func (s *fakeStore) Delete(ctx context.Context, slug string) error {
	delete(s.installs, slug)
	return nil
}

func (s *fakeStore) ConnectorIDs(ctx context.Context) ([]string, error) {
	ids := make([]string, 0)
	for _, install := range s.installs {
		if install.ConnectorID != "" {
			ids = append(ids, install.ConnectorID)
		}
	}
	return ids, nil
}

func (s *fakeStore) Close() error {
	return nil
}

// Helper to create a caller context header
func callerContextHeader(t *testing.T, isAdmin bool) string {
	caller := hub.Caller{
		Kind:    "user",
		Sub:     "u-admin",
		IsAdmin: isAdmin,
		Groups: []hub.Group{
			{ID: "g-1", Name: "g-1", Role: "owner"},
			{ID: "g-2", Name: "g-2", Role: "writer"},
		},
	}
	data, _ := json.Marshal(caller)
	return base64.RawURLEncoding.EncodeToString(data)
}

// TestSetupGateRequiresCredential checks that all /api/setup/* routes return 401 without a credential.
func TestSetupGateRequiresCredential(t *testing.T) {
	h := &hub.Fake{}
	s := &fakeStore{installs: make(map[string]setup.Install)}
	api := NewSetup(h, nil, s, fakeSettings{}, SetupConfig{})

	routes := []string{
		"GET /api/setup/installs",
		"GET /api/setup/options",
		"POST /api/setup/plan",
		"POST /api/setup/apply",
		"GET /api/setup/installs/test/deliveries",
		"POST /api/setup/installs/test/repair-connection",
		"POST /api/setup/installs/test/remove",
	}

	for _, route := range routes {
		parts := strings.Split(route, " ")
		method, path := parts[0], parts[1]

		var body io.Reader
		if method == "POST" {
			body = strings.NewReader("{}")
		}
		req := httptest.NewRequest(method, path, body)
		// No credential header
		w := httptest.NewRecorder()
		api.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: got %d, want 401", route, w.Code)
		}
	}
}

// TestSetupGateRequiresAdmin checks that all /api/setup/* routes return 403 for non-admin users.
func TestSetupGateRequiresAdmin(t *testing.T) {
	h := &hub.Fake{}
	s := &fakeStore{installs: make(map[string]setup.Install)}
	api := NewSetup(h, nil, s, fakeSettings{}, SetupConfig{})

	routes := []string{
		"GET /api/setup/installs",
		"GET /api/setup/options",
		"POST /api/setup/plan",
		"POST /api/setup/apply",
		"GET /api/setup/installs/test/deliveries",
		"POST /api/setup/installs/test/repair-connection",
		"POST /api/setup/installs/test/remove",
	}

	for _, route := range routes {
		parts := strings.Split(route, " ")
		method, path := parts[0], parts[1]

		var body io.Reader
		if method == "POST" {
			body = strings.NewReader("{}")
		}
		req := httptest.NewRequest(method, path, body)
		req.Header.Set("X-Ainsel-Credential", "test-cred")

		// Add non-admin caller
		nonAdminCaller := hub.Caller{
			Kind:    "user",
			IsAdmin: false,
			Groups:  []hub.Group{{ID: "g-1"}},
		}
		data, _ := json.Marshal(nonAdminCaller)
		req.Header.Set("X-Ainsel-Context", base64.RawURLEncoding.EncodeToString(data))

		w := httptest.NewRecorder()
		api.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("%s: got %d, want 403", route, w.Code)
		}
	}
}

// TestSetupPlanMakesNoWrites checks that POST /api/setup/plan does not write to hub or Forgejo.
func TestSetupPlanMakesNoWrites(t *testing.T) {
	h := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		Personas:         []hub.Ref{},
		Agents:           []hub.Ref{},
		Images:           []hub.Ref{{ID: "img-1", Name: "img-1"}},
		Connectors:       []hub.Ref{},
		MCPServers:       []hub.MCPServer{},
	}

	fg := &forgejoStub{
		orgExists:    true,
		adminIsAdmin: true,
		loginExists:  map[string]bool{},
	}

	s := &fakeStore{installs: make(map[string]setup.Install)}
	api := NewSetup(h, fg.client(), s, fakeSettings{}, SetupConfig{ForgejoBaseURL: "https://git.test"})

	planReq := struct {
		Slug       string                         `json:"slug"`
		GroupID    string                         `json:"groupId"`
		BaseURL    string                         `json:"baseUrl"`
		Org        string                         `json:"org"`
		AdminToken string                         `json:"adminToken"`
		Roles      map[setup.Role]setup.RoleInput `json:"roles"`
	}{
		Slug:    "test",
		GroupID: "g-1",
		BaseURL: "https://git.test",
		Org:     "test-org",
		// Note: no AdminToken for plan phase - it's just a preview
		Roles: map[setup.Role]setup.RoleInput{
			setup.RoleRefiner:   {Runtime: "python:3.11"},
			setup.RoleDeveloper: {Runtime: "python:3.11"},
			setup.RoleReviewer:  {Runtime: "python:3.11"},
		},
	}

	body, _ := json.Marshal(planReq)
	req := httptest.NewRequest("POST", "/api/setup/plan", bytes.NewReader(body))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", w.Code, w.Body.String())
	}

	if len(h.CreatedPersonas) > 0 || len(h.CreatedAgents) > 0 || len(h.CreatedConnectors) > 0 {
		t.Errorf("hub.Fake recorded writes during plan: personas=%d, agents=%d, connectors=%d", len(h.CreatedPersonas), len(h.CreatedAgents), len(h.CreatedConnectors))
	}

	if fg.methodCount > 0 {
		t.Errorf("Forgejo stub received %d calls during plan, want 0", fg.methodCount)
	}
}

// TestSetupPlanIgnoresLegacyFields pins the upgrade path: a wizard page that was
// still open when the plugin updated posts the old body — mcpUrl, and an llm
// under each role. Those fields are simply not read any more: unknown JSON is
// dropped, and the plan draws the llm and the MCP endpoint from the settings
// store. A request in the old shape plans exactly like one in the new.
func TestSetupPlanIgnoresLegacyFields(t *testing.T) {
	h := &hub.Fake{PluginGroupsSeed: []string{"g-1"}}
	fg := &forgejoStub{orgExists: true, adminIsAdmin: true, loginExists: map[string]bool{}}
	s := &fakeStore{installs: make(map[string]setup.Install)}
	api := NewSetup(h, fg.client(), s, fakeSettings{}, SetupConfig{ForgejoBaseURL: "https://git.test"})

	body := strings.NewReader(`{"slug":"test","groupId":"g-1","baseUrl":"https://git.test","org":"test-org",` +
		`"mcpUrl":"http://old-mcp:3000","adminToken":"ADMINTOKEN-xyz",` +
		`"roles":{"refiner":{"runtime":"python:3.11","llm":{"provider":"bogus-provider","model":"gpt-old","key":"LLMKEY-abc"}},` +
		`"developer":{"runtime":"python:3.11"},"reviewer":{"runtime":"python:3.11"}}}`)
	req := httptest.NewRequest("POST", "/api/setup/plan", body)
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", w.Code, w.Body.String())
	}

	var plan setup.Result
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// The bogus provider in the body is never read, so it cannot block anything.
	if len(plan.Verdict.Blocking) != 0 {
		t.Fatalf("blocking = %q, want none: the request's llm fields are not read", plan.Verdict.Blocking)
	}

	seen := false
	for _, op := range plan.Create {
		if op.Kind == setup.KindMCPServer {
			seen = true
			if !strings.Contains(op.Detail, "https://mcp.test/forgejo/mcp") {
				t.Errorf("mcp op = %q, want the settings' URL, not the request's", op.Detail)
			}
		}
	}
	if !seen {
		t.Errorf("no MCP server op in the plan: %+v", plan.Create)
	}
}

// TestSetupApplyBlockingVerdictReturns409 checks that a blocking verdict returns 409 with no writes.
func TestSetupApplyBlockingVerdictReturns409(t *testing.T) {
	h := &hub.Fake{
		// Group g-1 is NOT in PluginGroups, which will block
		PluginGroupsSeed: []string{"g-2"},
	}

	fg := &forgejoStub{
		orgExists:    true,
		adminIsAdmin: true,
		loginExists:  map[string]bool{},
	}

	s := &fakeStore{installs: make(map[string]setup.Install)}
	api := NewSetup(h, fg.client(), s, fakeSettings{}, SetupConfig{ForgejoBaseURL: "https://git.test"})

	planReq := struct {
		Slug       string                         `json:"slug"`
		GroupID    string                         `json:"groupId"`
		BaseURL    string                         `json:"baseUrl"`
		Org        string                         `json:"org"`
		AdminToken string                         `json:"adminToken"`
		Roles      map[setup.Role]setup.RoleInput `json:"roles"`
	}{
		Slug:       "test",
		GroupID:    "g-1",
		BaseURL:    "https://git.test",
		Org:        "test-org",
		AdminToken: "ADMINTOKEN-xyz",
		Roles: map[setup.Role]setup.RoleInput{
			setup.RoleRefiner:   {Runtime: "python:3.11"},
			setup.RoleDeveloper: {Runtime: "python:3.11"},
			setup.RoleReviewer:  {Runtime: "python:3.11"},
		},
	}

	body, _ := json.Marshal(planReq)
	req := httptest.NewRequest("POST", "/api/setup/apply", bytes.NewReader(body))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("got %d, want 409: %s", w.Code, w.Body.String())
	}

	var verdict struct {
		Verdict setup.Verdict `json:"verdict"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &verdict); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(verdict.Verdict.Blocking) == 0 {
		t.Errorf("expected blocking verdict, got none")
	}

	// The body is the plan the preview would have shown, so the wizard can put the block
	// against the resource it blocks instead of making the admin run the preview again.
	var plan setup.Result
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
		t.Fatalf("unmarshal plan: %v", err)
	}
	if len(plan.Create) == 0 {
		t.Errorf("create = %v, want the resources the blocked install would have created", plan.Create)
	}

	if len(h.CreatedPersonas) > 0 {
		t.Errorf("hub.Fake recorded writes on blocked apply: personas=%d", len(h.CreatedPersonas))
	}
}

// TestSetupApplyHappyPath checks that a successful apply saves the install and returns the report with no secrets.
func TestSetupApplyHappyPath(t *testing.T) {
	// Build default tools for the fake hub to return when RefreshImageTools is called
	// Collect all tools from all default profiles
	allTools := make(map[string]bool)
	for _, role := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
		for _, tool := range content.DefaultTools(role) {
			allTools[tool] = true
		}
	}

	// Convert to ImageTool objects (mcp tools are prefixed by the caller, so we use as-is)
	defaultImageTools := make([]hub.ImageTool, 0, len(allTools))
	for tool := range allTools {
		defaultImageTools = append(defaultImageTools, hub.ImageTool{
			Name:    tool,
			Kind:    "mcp",
			Enabled: false, // Will be enabled by SetImageTools
		})
	}

	h := &hub.Fake{
		PluginGroupsSeed:         []string{"g-1"},
		Personas:                 []hub.Ref{},
		Agents:                   []hub.Ref{},
		Images:                   []hub.Ref{},
		Connectors:               []hub.Ref{},
		MCPServers:               []hub.MCPServer{},
		SeedImages:               map[string]hub.Image{},
		DefaultRefreshImageTools: defaultImageTools,
		NextConnector: hub.Connector{
			Ref:                hub.Ref{ID: "c-1", Name: "c-1"},
			WebhookEndpoint:    "https://hub.test/webhook",
			WebhookSecretValue: "NEWSECRET-xyz",
		},
	}

	fg := &forgejoStub{
		orgExists:     true,
		adminIsAdmin:  true,
		loginExists:   map[string]bool{},
		tokenToReturn: "BOTTOKEN-123",
	}

	s := &fakeStore{installs: make(map[string]setup.Install)}

	// Capture logs to scan for secrets
	logBuf := new(bytes.Buffer)
	oldStderr := log.Writer()
	log.SetOutput(logBuf)
	defer log.SetOutput(oldStderr)

	api := NewSetup(h, fg.client(), s, fakeSettings{}, SetupConfig{ForgejoBaseURL: "https://git.test"})

	planReq := struct {
		Slug       string                         `json:"slug"`
		GroupID    string                         `json:"groupId"`
		BaseURL    string                         `json:"baseUrl"`
		Org        string                         `json:"org"`
		AdminToken string                         `json:"adminToken"`
		Roles      map[setup.Role]setup.RoleInput `json:"roles"`
	}{
		Slug:       "test",
		GroupID:    "g-1",
		BaseURL:    "https://git.test",
		Org:        "test-org",
		AdminToken: "ADMINTOKEN-xyz",
		Roles: map[setup.Role]setup.RoleInput{
			setup.RoleRefiner:   {Runtime: "python:3.11"},
			setup.RoleDeveloper: {Runtime: "python:3.11"},
			setup.RoleReviewer:  {Runtime: "python:3.11"},
		},
	}

	body, _ := json.Marshal(planReq)
	req := httptest.NewRequest("POST", "/api/setup/apply", bytes.NewReader(body))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Install setup.Install     `json:"install"`
		Report  setup.ApplyReport `json:"report"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Install.Slug != "test" {
		t.Errorf("install slug: got %q, want test", resp.Install.Slug)
	}

	// Check that the install was saved
	saved := s.installs["test"]
	if saved.Slug != "test" {
		t.Errorf("saved install slug: got %q, want test", saved.Slug)
	}

	// Check roles are filled
	for _, role := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
		state := saved.Roles[role]
		if state.AgentID == "" {
			t.Errorf("role %q: agent ID not set", role)
		}
		if state.BotLogin == "" {
			t.Errorf("role %q: bot login not set", role)
		}
	}

	// The agent's llm is the settings' answer: the wizard never handled a key
	// or a URL (R4), and neither does the apply.
	if len(h.CreatedAgents) != 3 {
		t.Fatalf("expected 3 agents created, got %d", len(h.CreatedAgents))
	}
	for _, agent := range h.CreatedAgents {
		if agent.LLM == nil || agent.LLM.Provider != "ollama-cloud" || agent.LLM.Model != "glm-4.7" {
			t.Errorf("created agent LLM = %+v, want the settings' ollama-cloud/glm-4.7", agent.LLM)
		}
	}

	// Secret scan: check response body
	secrets := []string{"ADMINTOKEN-xyz", "BOTTOKEN-123", "LLMKEY-abc"}
	responseBody := w.Body.String()
	for _, secret := range secrets {
		if strings.Contains(responseBody, secret) {
			t.Errorf("response contains secret: %s", secret)
		}
	}

	// Secret scan: check saved install
	savedJSON, _ := json.Marshal(saved)
	for _, secret := range secrets {
		if strings.Contains(string(savedJSON), secret) {
			t.Errorf("saved install contains secret: %s", secret)
		}
	}

	// Secret scan: check logs
	logOutput := logBuf.String()
	for _, secret := range secrets {
		if strings.Contains(logOutput, secret) {
			t.Errorf("logs contain secret: %s", secret)
		}
	}
}

// TestSetupApplyNamesTheFailingService checks that Apply errors show the step name
// and the service's message, with the rollback report.
func TestSetupApplyNamesTheFailingService(t *testing.T) {
	h := &hub.Fake{
		PluginGroupsSeed:         []string{"g-1"},
		Personas:                 []hub.Ref{},
		Agents:                   []hub.Ref{},
		Images:                   []hub.Ref{},
		Connectors:               []hub.Ref{},
		MCPServers:               []hub.MCPServer{},
		SeedImages:               map[string]hub.Image{},
		DefaultRefreshImageTools: []hub.ImageTool{},
		NextConnector: hub.Connector{
			Ref:                hub.Ref{ID: "c-1", Name: "c-1"},
			WebhookEndpoint:    "https://hub.test/webhook",
			WebhookSecretValue: "NEWSECRET-xyz",
		},
	}

	fg := &forgejoStub{
		orgExists:    true,
		adminIsAdmin: true,
		loginExists:  map[string]bool{},
		// Will return 422 on CreateUser to simulate failure
		failCreateUserWith: &struct {
			code    int
			message string
		}{422, "user already exists"},
	}

	s := &fakeStore{installs: make(map[string]setup.Install)}
	api := NewSetup(h, fg.client(), s, fakeSettings{}, SetupConfig{ForgejoBaseURL: "https://git.test"})

	planReq := struct {
		Slug       string                         `json:"slug"`
		GroupID    string                         `json:"groupId"`
		BaseURL    string                         `json:"baseUrl"`
		Org        string                         `json:"org"`
		AdminToken string                         `json:"adminToken"`
		Roles      map[setup.Role]setup.RoleInput `json:"roles"`
	}{
		Slug:       "test",
		GroupID:    "g-1",
		BaseURL:    "https://git.test",
		Org:        "test-org",
		AdminToken: "ADMINTOKEN-xyz",
		Roles: map[setup.Role]setup.RoleInput{
			setup.RoleRefiner:   {Runtime: "python:3.11"},
			setup.RoleDeveloper: {Runtime: "python:3.11"},
			setup.RoleReviewer:  {Runtime: "python:3.11"},
		},
	}

	body, _ := json.Marshal(planReq)
	req := httptest.NewRequest("POST", "/api/setup/apply", bytes.NewReader(body))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("got %d, want 502", w.Code)
	}

	var resp struct {
		Error  string            `json:"error"`
		Report setup.ApplyReport `json:"report"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Check error contains the step name and the service message
	if !strings.Contains(resp.Error, "CreateUser") {
		t.Errorf("error missing step name: %s", resp.Error)
	}
	if !strings.Contains(resp.Error, "user already exists") {
		t.Errorf("error missing service message: %s", resp.Error)
	}

	// Check report is present
	if resp.Report.Created == nil {
		t.Errorf("report.Created is nil, want empty slice")
	}
	if resp.Report.RolledBack == nil {
		t.Errorf("report.RolledBack is nil, want empty slice")
	}

	// Check no hub creates were recorded (users are first step)
	if len(h.CreatedPersonas) > 0 || len(h.CreatedAgents) > 0 || len(h.CreatedConnectors) > 0 {
		t.Errorf("hub.Fake recorded unexpected creates: personas=%d, agents=%d, connectors=%d",
			len(h.CreatedPersonas), len(h.CreatedAgents), len(h.CreatedConnectors))
	}

	// Secret scan: verify no secrets in response
	responseBody := w.Body.String()
	if strings.Contains(responseBody, "ADMINTOKEN-xyz") {
		t.Errorf("response contains ADMINTOKEN")
	}
	if strings.Contains(responseBody, "BOTTOKEN-") {
		t.Errorf("response contains BOTTOKEN")
	}
}

// TestSetupSnapshotBlocksOnMissingDefaultTools checks that missing DefaultTools/CheckTools blocks.
func TestSetupSnapshotBlocksOnMissingDefaultTools(t *testing.T) {
	h := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
	}

	// Create a custom setup API that doesn't inject content functions
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/setup/plan", func(w http.ResponseWriter, r *http.Request) {
		gate(w, r, h, func(ctx context.Context, caller *hub.Caller) error {
			var req struct {
				Slug    string `json:"slug"`
				GroupID string `json:"groupId"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				return badRequest("invalid json")
			}

			// Build snapshot WITHOUT DefaultTools and CheckTools (they remain nil)
			snap := setup.Snapshot{
				CallerGroups: []string{"g-1"},
				PluginGroups: []string{"g-1"},
				// DefaultTools and CheckTools are nil!
			}

			input := setup.Input{
				Slug:    req.Slug,
				GroupID: req.GroupID,
				Org:     "test-org",
				Roles:   map[setup.Role]setup.RoleInput{},
			}

			result := setup.Plan(input, snap)
			writeJSON(w, http.StatusOK, result)
			return nil
		})
	})

	planReq := struct {
		Slug    string `json:"slug"`
		GroupID string `json:"groupId"`
	}{
		Slug:    "test",
		GroupID: "g-1",
	}

	body, _ := json.Marshal(planReq)
	req := httptest.NewRequest("POST", "/api/setup/plan", bytes.NewReader(body))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var result struct {
		Verdict setup.Verdict `json:"verdict"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &result)

	if len(result.Verdict.Blocking) == 0 {
		t.Errorf("expected blocking verdict for missing DefaultTools/CheckTools, got none")
	}
}

// TestSetupSnapshotBlocksOnMissingAdmin checks that missing admin check blocks in the verdict.
func TestSetupSnapshotBlocksOnMissingAdmin(t *testing.T) {
	h := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		Personas:         []hub.Ref{},
		Agents:           []hub.Ref{},
		Images:           []hub.Ref{{ID: "img-1", Name: "img-1"}},
		Connectors:       []hub.Ref{},
		MCPServers:       []hub.MCPServer{},
	}

	// Forgejo stub that is not an admin
	fg := &forgejoStub{
		orgExists:    true,
		adminIsAdmin: false, // NOT admin
		loginExists:  map[string]bool{},
	}

	s := &fakeStore{installs: make(map[string]setup.Install)}
	api := NewSetup(h, fg.client(), s, fakeSettings{}, SetupConfig{ForgejoBaseURL: "https://git.test"})

	planReq := struct {
		Slug       string                         `json:"slug"`
		GroupID    string                         `json:"groupId"`
		BaseURL    string                         `json:"baseUrl"`
		Org        string                         `json:"org"`
		AdminToken string                         `json:"adminToken"`
		Roles      map[setup.Role]setup.RoleInput `json:"roles"`
	}{
		Slug:       "test",
		GroupID:    "g-1",
		BaseURL:    "https://git.test",
		Org:        "test-org",
		AdminToken: "ADMINTOKEN-xyz",
		Roles: map[setup.Role]setup.RoleInput{
			setup.RoleRefiner:   {Runtime: "python:3.11"},
			setup.RoleDeveloper: {Runtime: "python:3.11"},
			setup.RoleReviewer:  {Runtime: "python:3.11"},
		},
	}

	body, _ := json.Marshal(planReq)
	req := httptest.NewRequest("POST", "/api/setup/plan", bytes.NewReader(body))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	var result struct {
		Verdict setup.Verdict `json:"verdict"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &result)

	if len(result.Verdict.Blocking) == 0 {
		t.Errorf("expected blocking verdict when token is not admin, got none")
	}
}

// TestSetupDeleteWithoutAdminToken deletes only hub resources and lists Forgejo items in checklist.
func TestSetupDeleteWithoutAdminToken(t *testing.T) {
	h := &hub.Fake{}

	s := &fakeStore{
		installs: map[string]setup.Install{
			"test": {
				Slug:        "test",
				GroupID:     "g-1",
				BaseURL:     "https://git.test",
				Org:         "test-org",
				ConnectorID: "c-1",
				HookID:      42,
				Roles: map[setup.Role]setup.RoleState{
					setup.RoleRefiner:   {AgentID: "a-1", TeamID: 1, BotLogin: "refiner-test"},
					setup.RoleDeveloper: {AgentID: "a-2", TeamID: 2, BotLogin: "developer-test"},
					setup.RoleReviewer:  {AgentID: "a-3", TeamID: 3, BotLogin: "reviewer-test"},
				},
			},
		},
	}

	api := NewSetup(h, nil, s, fakeSettings{}, SetupConfig{})

	req := httptest.NewRequest("POST", "/api/setup/installs/test/remove", nil)
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", w.Code)
	}

	var resp struct {
		Deleted   []string `json:"deleted"`
		Checklist []string `json:"checklist"`
		Kept      bool     `json:"kept"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	// The row survives: without the token the hook, teams and users are exactly
	// what is outstanding, and the row is the only record of what they were.
	if !resp.Kept {
		t.Errorf("kept = false, want true: the Forgejo items were not deleted")
	}
	if _, still := s.installs["test"]; !still {
		t.Error("the install row was deleted while its Forgejo items are outstanding")
	}

	// Should have deleted connector, agents
	if !contains(resp.Deleted, "connector:c-1") {
		t.Errorf("deleted: got %v, want connector:c-1 in list", resp.Deleted)
	}

	// Should list Forgejo items in checklist
	if !contains(resp.Checklist, "hook:42") {
		t.Errorf("checklist: got %v, want hook:42", resp.Checklist)
	}
	if !contains(resp.Checklist, "user:refiner-test") {
		t.Errorf("checklist: got %v, want user:refiner-test", resp.Checklist)
	}
}

// TestSetupDeleteWithAdminToken also deletes Forgejo resources.
func TestSetupDeleteWithAdminToken(t *testing.T) {
	h := &hub.Fake{
		Triggers: map[string][]setup.Trigger{
			"c-1": {
				{ID: "trigger-1", Name: "forgejo-refiner-issue-opened-test", AgentRef: "a-1", ConnectorRef: "c-1"},
				{ID: "trigger-2", Name: "forgejo-refiner-issue-reopened-test", AgentRef: "a-1", ConnectorRef: "c-1"},
				{ID: "trigger-3", Name: "forgejo-refiner-mention-test", AgentRef: "a-1", ConnectorRef: "c-1"},
				{ID: "trigger-4", Name: "forgejo-developer-mention-test", AgentRef: "a-2", ConnectorRef: "c-1"},
				{ID: "trigger-5", Name: "forgejo-reviewer-pr-opened-test", AgentRef: "a-3", ConnectorRef: "c-1"},
				{ID: "trigger-6", Name: "forgejo-reviewer-mention-test", AgentRef: "a-3", ConnectorRef: "c-1"},
			},
		},
	}

	s := &fakeStore{
		installs: map[string]setup.Install{
			"test": {
				Slug:        "test",
				GroupID:     "g-1",
				BaseURL:     "https://git.test",
				Org:         "test-org",
				ConnectorID: "c-1",
				HookID:      42,
				Roles: map[setup.Role]setup.RoleState{
					setup.RoleRefiner:   {AgentID: "a-1", ImageID: "img-1", PersonaID: "p-1", TeamID: 1, BotLogin: "refiner-test"},
					setup.RoleDeveloper: {AgentID: "a-2", ImageID: "img-2", PersonaID: "p-2", TeamID: 2, BotLogin: "developer-test"},
					setup.RoleReviewer:  {AgentID: "a-3", ImageID: "img-3", PersonaID: "p-3", TeamID: 3, BotLogin: "reviewer-test"},
				},
			},
		},
	}

	fg := &forgejoStub{
		orgExists: true,
	}

	api := NewSetup(h, fg.client(), s, fakeSettings{}, SetupConfig{ForgejoBaseURL: "https://git.test"})

	deleteReq := struct {
		AdminToken string `json:"adminToken"`
	}{
		AdminToken: "ADMINTOKEN-xyz",
	}

	body, _ := json.Marshal(deleteReq)
	req := httptest.NewRequest("POST", "/api/setup/installs/test/remove", bytes.NewReader(body))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", w.Code)
	}

	var resp struct {
		Deleted   []string `json:"deleted"`
		Checklist []string `json:"checklist"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	// With admin token, should try to delete Forgejo resources
	if fg.deleteCalls == 0 {
		t.Errorf("expected Forgejo delete calls with admin token, got 0")
	}

	// Verify all six triggers are deleted
	expectedTriggers := []string{"trigger:trigger-1", "trigger:trigger-2", "trigger:trigger-3", "trigger:trigger-4", "trigger:trigger-5", "trigger:trigger-6"}
	for _, expectedTrigger := range expectedTriggers {
		if !contains(resp.Deleted, expectedTrigger) {
			t.Errorf("trigger %s not found in deleted list", expectedTrigger)
		}
	}

	// Verify the basic order: hook before triggers, triggers before connector
	hookIdx := -1
	connectorIdx := -1
	triggerIdx := -1
	for i, item := range resp.Deleted {
		if strings.HasPrefix(item, "hook:") && hookIdx == -1 {
			hookIdx = i
		}
		if strings.HasPrefix(item, "trigger:") && triggerIdx == -1 {
			triggerIdx = i
		}
		if strings.HasPrefix(item, "connector:") && connectorIdx == -1 {
			connectorIdx = i
		}
	}

	if hookIdx >= 0 && triggerIdx >= 0 && hookIdx >= triggerIdx {
		t.Errorf("hook should be deleted before triggers: hook at %d, triggers at %d", hookIdx, triggerIdx)
	}
	if triggerIdx >= 0 && connectorIdx >= 0 && triggerIdx >= connectorIdx {
		t.Errorf("triggers should be deleted before connector: triggers at %d, connector at %d", triggerIdx, connectorIdx)
	}
}

// TestSetupDeliveries checks that GET deliveries marks verified on first event.
func TestSetupDeliveries(t *testing.T) {
	h := &hub.Fake{
		SeedEvents: []hub.Event{
			{
				ID:   "e-1",
				Type: "issues",
				At:   time.Now(),
			},
		},
	}

	s := &fakeStore{
		installs: map[string]setup.Install{
			"test": {
				Slug:        "test",
				ConnectorID: "c-1",
			},
		},
	}

	api := NewSetup(h, nil, s, fakeSettings{}, SetupConfig{})

	req := httptest.NewRequest("GET", "/api/setup/installs/test/deliveries?since=2026-01-01T00:00:00Z", nil)
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", w.Code)
	}

	var resp struct {
		Verified bool       `json:"verified"`
		At       *time.Time `json:"at,omitempty"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	if !resp.Verified {
		t.Errorf("verified: got false, want true")
	}

	// Check that the install was updated
	updated := s.installs["test"]
	if updated.WebhookVerifiedAt == nil {
		t.Errorf("WebhookVerifiedAt not set")
	}
}

// TestSetupRepairConnection rotates secret and patches hook.
func TestSetupRepairConnection(t *testing.T) {
	h := &hub.Fake{
		NextConnector: hub.Connector{
			Ref:                hub.Ref{ID: "c-1", Name: "c-1"},
			WebhookEndpoint:    "https://hub.test/webhook",
			WebhookSecretValue: "NEWSECRET-xyz",
		},
	}

	s := &fakeStore{
		installs: map[string]setup.Install{
			"test": {
				Slug:        "test",
				ConnectorID: "c-1",
				Org:         "test-org",
				HookID:      42,
			},
		},
	}

	fg := &forgejoStub{
		orgExists: true,
	}

	api := NewSetup(h, fg.client(), s, fakeSettings{}, SetupConfig{})

	repairReq := struct {
		AdminToken string `json:"adminToken"`
	}{
		AdminToken: "ADMINTOKEN-xyz",
	}

	body, _ := json.Marshal(repairReq)
	req := httptest.NewRequest("POST", "/api/setup/installs/test/repair-connection", bytes.NewReader(body))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", w.Code, w.Body.String())
	}

	// Check that the response doesn't contain the new secret
	if strings.Contains(w.Body.String(), "NEWSECRET-xyz") {
		t.Errorf("response contains new secret")
	}

	// Check that hub rotated the secret
	if len(h.RotatedConnectors) == 0 {
		t.Errorf("hub.RotateConnectorSecret not called")
	}
}

// Helper functions
func contains(list []string, item string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}

// forgejoStub is an httptest-compatible Forgejo server for testing
type forgejoStub struct {
	orgExists          bool
	adminIsAdmin       bool
	loginExists        map[string]bool
	tokenToReturn      string
	methodCount        int
	deleteCalls        int
	teamCounter        int64
	hookCounter        int64
	botPasswords       map[string]string // record bot passwords for basic auth
	teams              map[string]int64  // team name -> id, answered by GET orgs/{org}/teams
	orgHooks           []map[string]any  // answered by GET orgs/{org}/hooks
	failDeleteWith     int               // answer every DELETE with this status (0 = 204)
	failCreateUserWith *struct {
		code    int
		message string
	} // fail POST /api/v1/admin/users with this code and message
	srv *httptest.Server
}

func (f *forgejoStub) client() *forgejo.Client {
	if f.srv == nil {
		if f.botPasswords == nil {
			f.botPasswords = make(map[string]string)
		}
		f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f.methodCount++

			// Check for specific endpoints in order of specificity
			if r.URL.Path == "/api/v1/user" && r.Method == "GET" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"login":    "admin",
					"is_admin": f.adminIsAdmin,
				})
				return
			}

			// GET /api/v1/orgs/{org}/teams - the team list FindTeam reads. One page
			// of everything, and an empty second page so the paging stops.
			if strings.HasPrefix(r.URL.Path, "/api/v1/orgs/") && strings.HasSuffix(r.URL.Path, "/teams") && r.Method == "GET" {
				if p := r.URL.Query().Get("page"); p != "" && p != "1" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte("[]"))
					return
				}
				out := []map[string]interface{}{}
				for name, id := range f.teams {
					out = append(out, map[string]interface{}{"id": id, "name": name})
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(out)
				return
			}

			// GET /api/v1/orgs/{org}/hooks - the hook list the orphan sweep reads.
			if strings.HasPrefix(r.URL.Path, "/api/v1/orgs/") && strings.HasSuffix(r.URL.Path, "/hooks") && r.Method == "GET" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(f.orgHooks)
				return
			}

			if strings.HasPrefix(r.URL.Path, "/api/v1/orgs/") && r.Method == "GET" {
				if !f.orgExists {
					w.WriteHeader(404)
				}
				return
			}

			if strings.HasPrefix(r.URL.Path, "/api/v1/users/") && r.Method == "GET" {
				login := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/users/"), "/")
				if !f.loginExists[login] {
					w.WriteHeader(404)
					return
				}
				// User exists: return user object
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"login": login,
				})
				return
			}

			// POST /api/v1/admin/users - create user
			if r.URL.Path == "/api/v1/admin/users" && r.Method == "POST" {
				var req struct {
					Username string `json:"username"`
					Email    string `json:"email"`
					Password string `json:"password"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)

				// Check if we should fail this request
				if f.failCreateUserWith != nil {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(f.failCreateUserWith.code)
					_ = json.NewEncoder(w).Encode(map[string]interface{}{
						"message": f.failCreateUserWith.message,
					})
					return
				}

				f.loginExists[req.Username] = true
				f.botPasswords[req.Username] = req.Password
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"login": req.Username,
					"email": req.Email,
				})
				return
			}

			// POST /api/v1/users/{bot}/tokens - mint token with basic auth
			if strings.HasPrefix(r.URL.Path, "/api/v1/users/") && strings.HasSuffix(r.URL.Path, "/tokens") && r.Method == "POST" {
				// Extract login from path
				parts := strings.Split(r.URL.Path, "/")
				login := parts[4]

				// Verify basic auth
				username, password, ok := r.BasicAuth()
				if ok && username == login && f.botPasswords[login] == password {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(map[string]interface{}{
						"sha1": f.tokenToReturn,
					})
					return
				}
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			// POST /api/v1/orgs/{org}/teams - create team
			if strings.HasPrefix(r.URL.Path, "/api/v1/orgs/") && strings.HasSuffix(r.URL.Path, "/teams") && r.Method == "POST" {
				var req struct {
					Name string `json:"name"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				f.teamCounter++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id":   f.teamCounter,
					"name": req.Name,
				})
				return
			}

			// PUT /api/v1/teams/{id}/members/{login} - add team member
			if strings.HasPrefix(r.URL.Path, "/api/v1/teams/") && strings.Contains(r.URL.Path, "/members/") && r.Method == "PUT" {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			// POST /api/v1/orgs/{org}/hooks - create hook
			if strings.HasPrefix(r.URL.Path, "/api/v1/orgs/") && strings.HasSuffix(r.URL.Path, "/hooks") && r.Method == "POST" {
				var req struct {
					URL    string `json:"url"`
					Secret string `json:"secret"`
				}
				// The actual schema has it nested under Config, but decode what we need
				_ = json.NewDecoder(r.Body).Decode(&req)
				f.hookCounter++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id": f.hookCounter,
				})
				return
			}

			// PATCH /api/v1/orgs/{org}/hooks/{id} - update hook
			if strings.HasPrefix(r.URL.Path, "/api/v1/orgs/") && strings.Contains(r.URL.Path, "/hooks/") && r.Method == "PATCH" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id": 1,
				})
				return
			}

			if r.Method == "DELETE" {
				f.deleteCalls++
				if f.failDeleteWith != 0 {
					w.WriteHeader(f.failDeleteWith)
					return
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}

			w.WriteHeader(200)
		}))
	}
	return forgejo.New(f.srv.URL, nil)
}

// TestSetupPlanEditWithBlankRuntimeKeepsStored tests that Prior is populated on an edit
// and blank runtime keeps what the install has.
func TestSetupPlanEditWithBlankRuntimeKeepsStored(t *testing.T) {
	// Existing install with stored resources
	existing := setup.Install{
		Slug:    "test",
		GroupID: "g-1",
		Org:     "test-org",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleRefiner: {
				ImageID: "img-refiner",
				AgentID: "ag-refiner",
			},
		},
	}

	// Fake hub returns the stored image and agent
	h := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		SeedImages: map[string]hub.Image{
			"img-refiner": {
				ID:       "img-refiner",
				ImageURL: "python:3.11",
			},
		},
		SeedAgents: map[string]hub.Agent{
			"ag-refiner": {
				ID:   "ag-refiner",
				Name: "refiner-agent",
				LLM:  &hub.LLMSpec{Provider: "opencode", Model: "model-x"},
			},
		},
	}

	fg := &forgejoStub{
		orgExists:    true,
		adminIsAdmin: true,
		loginExists:  map[string]bool{},
	}

	s := &fakeStore{installs: map[string]setup.Install{"test": existing}}
	api := NewSetup(h, fg.client(), s, fakeSettings{}, SetupConfig{})

	// Plan request with blank runtime (should keep what the install has)
	planReq := struct {
		Slug       string                         `json:"slug"`
		GroupID    string                         `json:"groupId"`
		BaseURL    string                         `json:"baseUrl"`
		Org        string                         `json:"org"`
		AdminToken string                         `json:"adminToken"`
		Roles      map[setup.Role]setup.RoleInput `json:"roles"`
	}{
		Slug:       "test",
		GroupID:    "g-1",
		BaseURL:    "https://git.test",
		Org:        "test-org",
		AdminToken: "",
		Roles: map[setup.Role]setup.RoleInput{
			setup.RoleRefiner:   {Runtime: ""}, // Blank runtime should be filled from Prior
			setup.RoleDeveloper: {Runtime: "python:3.11"},
			setup.RoleReviewer:  {Runtime: "python:3.11"},
		},
	}

	body, _ := json.Marshal(planReq)
	req := httptest.NewRequest("POST", "/api/setup/plan", bytes.NewReader(body))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", w.Code, w.Body.String())
	}

	var result setup.Result
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Should have no blocking verdict because Prior provided the runtime
	if len(result.Verdict.Blocking) > 0 {
		t.Errorf("expected no blocking verdict, got: %v", result.Verdict.Blocking)
	}
}

// TestSetupRequestToInputIsTheCleanedForm pins the one place the HTTP request becomes
// the setup's Input (issue #85): the address, the slug, the org and the exclude label
// arrive trimmed, so the configured-instance check, the store lookup and the plan all
// read what the install goes on to store. The admin token and the group id pass through
// untouched — a token's bytes are the answer, and a group id is matched verbatim.
func TestSetupRequestToInputIsTheCleanedForm(t *testing.T) {
	req := SetupRequest{
		Slug:         "\ttest ",
		GroupID:      " g-1 ",
		BaseURL:      " https://git.test\n",
		Org:          " test-org ",
		AdminToken:   " tok en ",
		ExcludeLabel: "do-not-refine\t",
		Roles: map[setup.Role]setup.RoleInput{
			setup.RoleDeveloper: {Runtime: "python:3.11\t", AgentName: " dev ", TurnTimeoutMs: " 3600000 "},
		},
	}

	got := req.toInput()

	if got.Slug != "test" || got.Org != "test-org" || got.BaseURL != "https://git.test" || got.ExcludeLabel != "do-not-refine" {
		t.Errorf("input = %+v, want the slug, org, base URL and label trimmed", got)
	}
	if got.GroupID != " g-1 " || got.AdminToken != " tok en " {
		t.Errorf("input = %+v, want the group id and the admin token left alone", got)
	}
	ri := got.Roles[setup.RoleDeveloper]
	if ri.Runtime != "python:3.11" || ri.AgentName != "dev" || ri.TurnTimeoutMs != "3600000" {
		t.Errorf("role = %+v, want the runtime, agent name and turn timeout trimmed", ri)
	}
}

// TestSetupPlanTrimsTheSlugBeforeTheStoreLookup is the same trimming seen from the
// route: a request whose slug carries a tab is the same install, not a second one.
// Before the trim it missed the row, read as a new install, and blocked for an admin
// token an edit never needs.
func TestSetupPlanTrimsTheSlugBeforeTheStoreLookup(t *testing.T) {
	existing := setup.Install{
		Slug:    "test",
		GroupID: "g-1",
		Org:     "test-org",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleRefiner:   {ImageID: "img-refiner", AgentID: "ag-refiner"},
			setup.RoleDeveloper: {ImageID: "img-developer", AgentID: "ag-developer"},
			setup.RoleReviewer:  {ImageID: "img-reviewer", AgentID: "ag-reviewer"},
		},
	}
	h := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		SeedImages: map[string]hub.Image{
			"img-refiner":   {ID: "img-refiner", ImageURL: "python:3.11"},
			"img-developer": {ID: "img-developer", ImageURL: "python:3.11"},
			"img-reviewer":  {ID: "img-reviewer", ImageURL: "python:3.11"},
		},
		SeedAgents: map[string]hub.Agent{
			"ag-refiner":   {ID: "ag-refiner", Name: "refiner-agent", LLM: &hub.LLMSpec{Provider: "ollama-cloud", Model: "glm-4.7"}},
			"ag-developer": {ID: "ag-developer", Name: "developer-agent", LLM: &hub.LLMSpec{Provider: "ollama-cloud", Model: "glm-4.7"}},
			"ag-reviewer":  {ID: "ag-reviewer", Name: "reviewer-agent", LLM: &hub.LLMSpec{Provider: "ollama-cloud", Model: "glm-4.7"}},
		},
	}
	fg := &forgejoStub{orgExists: true, adminIsAdmin: true, loginExists: map[string]bool{}}
	s := &fakeStore{installs: map[string]setup.Install{"test": existing}}
	api := NewSetup(h, fg.client(), s, fakeSettings{}, SetupConfig{})

	body, _ := json.Marshal(SetupRequest{
		Slug:    "\ttest ",
		GroupID: "g-1",
		// The address carries the copy-paste whitespace too: the instance check reads
		// it trimmed, so this is still the configured https://git.test.
		BaseURL: " https://git.test\t",
		Org:     "test-org",
		Roles: map[setup.Role]setup.RoleInput{
			setup.RoleRefiner:   {Runtime: "python:3.11\t"},
			setup.RoleDeveloper: {Runtime: "python:3.11"},
			setup.RoleReviewer:  {Runtime: "python:3.11"},
		},
	})
	req := httptest.NewRequest("POST", "/api/setup/plan", bytes.NewReader(body))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", w.Code, w.Body.String())
	}
	var result setup.Result
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, b := range result.Verdict.Blocking {
		if strings.Contains(b, "admin token") {
			t.Errorf("blocking = %q, want the edit path: the trimmed slug finds the row",
				result.Verdict.Blocking)
		}
	}
}

// TestSetupPlanBlocksOnTakenBotLogin tests that a taken bot login is detected and blocks the plan.
func TestSetupPlanBlocksOnTakenBotLogin(t *testing.T) {
	h := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
	}

	fg := &forgejoStub{
		orgExists:    true,
		adminIsAdmin: true,
		loginExists:  map[string]bool{"refiner-test": true}, // Bot login already taken
	}

	s := &fakeStore{installs: make(map[string]setup.Install)}
	api := NewSetup(h, fg.client(), s, fakeSettings{}, SetupConfig{ForgejoBaseURL: "https://git.test"})

	planReq := struct {
		Slug       string                         `json:"slug"`
		GroupID    string                         `json:"groupId"`
		BaseURL    string                         `json:"baseUrl"`
		Org        string                         `json:"org"`
		AdminToken string                         `json:"adminToken"`
		Roles      map[setup.Role]setup.RoleInput `json:"roles"`
	}{
		Slug:       "test",
		GroupID:    "g-1",
		BaseURL:    "https://git.test",
		Org:        "test-org",
		AdminToken: "ADMINTOKEN-xyz",
		Roles: map[setup.Role]setup.RoleInput{
			setup.RoleRefiner:   {Runtime: "python:3.11"},
			setup.RoleDeveloper: {Runtime: "python:3.11"},
			setup.RoleReviewer:  {Runtime: "python:3.11"},
		},
	}

	body, _ := json.Marshal(planReq)
	req := httptest.NewRequest("POST", "/api/setup/plan", bytes.NewReader(body))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", w.Code, w.Body.String())
	}

	var result setup.Result
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Should block because the bot login is taken
	if len(result.Verdict.Blocking) == 0 {
		t.Errorf("expected blocking verdict for taken bot login")
	}
	blocked := false
	for _, msg := range result.Verdict.Blocking {
		if strings.Contains(msg, "refiner-test") || strings.Contains(msg, "already taken") {
			blocked = true
			break
		}
	}
	if !blocked {
		t.Errorf("expected blocking message about taken login, got: %v", result.Verdict.Blocking)
	}
}

// TestSetupDeleteRemovesMCPServer tests that DELETE handler removes the MCP server.
func TestSetupDeleteRemovesMCPServer(t *testing.T) {
	h := &hub.Fake{
		MCPServers: []hub.MCPServer{
			{Name: "forgejo-mcp-test", URL: "http://forgejo.example.com/api/mcp"},
		},
	}

	s := &fakeStore{
		installs: map[string]setup.Install{
			"test": {
				Slug:      "test",
				MCPServer: "forgejo-mcp-test",
				Org:       "test-org",
			},
		},
	}

	fg := &forgejoStub{
		orgExists: true,
	}

	api := NewSetup(h, fg.client(), s, fakeSettings{}, SetupConfig{})

	deleteReq := struct {
		AdminToken string `json:"adminToken"`
	}{
		AdminToken: "ADMINTOKEN-xyz",
	}

	body, _ := json.Marshal(deleteReq)
	req := httptest.NewRequest("POST", "/api/setup/installs/test/remove", bytes.NewReader(body))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", w.Code, w.Body.String())
	}

	var result struct {
		Deleted   []string `json:"deleted"`
		Checklist []string `json:"checklist"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Check that MCP server was deleted
	found := false
	for _, d := range result.Deleted {
		if d == "mcp:forgejo-mcp-test" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("MCP server not in deleted list: %v", result.Deleted)
	}
}

// TestSetupDeleteKeepsRowWhenHubKeepsAResource is the reason a row outlives a removal that
// did not finish: the row is the only record of which resources belong to the install (R5).
// Delete it after a half-finished removal and whatever survived has no owner left to name it
// — the checklist cannot point at what it does not know about, and no later removal can find
// it either. A Forgejo leftover is different and does not hold the row: it is named, and it
// is the admin's to remove by hand.
func TestSetupDeleteKeepsRowWhenHubKeepsAResource(t *testing.T) {
	h := &hub.Fake{DeleteErr: errors.New("hub is keeping the agent")}

	s := &fakeStore{
		installs: map[string]setup.Install{
			"test": {
				Slug:        "test",
				ConnectorID: "connector-1",
				MCPServer:   "forgejo-mcp-test",
				Roles: map[setup.Role]setup.RoleState{
					setup.RoleRefiner: {
						AgentID:   "agent-1",
						ImageID:   "image-1",
						PersonaID: "persona-1",
						BotLogin:  "refiner-bot",
						TeamID:    7,
					},
				},
			},
		},
	}

	fg := &forgejoStub{orgExists: true}
	api := NewSetup(h, fg.client(), s, fakeSettings{}, SetupConfig{})

	body, _ := json.Marshal(struct {
		AdminToken string `json:"adminToken"`
	}{AdminToken: "ADMINTOKEN-xyz"})
	req := httptest.NewRequest("POST", "/api/setup/installs/test/remove", bytes.NewReader(body))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", w.Code, w.Body.String())
	}

	var result struct {
		Deleted   []string `json:"deleted"`
		Checklist []string `json:"checklist"`
		Kept      bool     `json:"kept"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !result.Kept {
		t.Errorf("kept = false, want true: the hub removed none of the install")
	}
	if _, still := s.installs["test"]; !still {
		t.Error("the row was deleted while the hub still holds the install's resources")
	}
	if len(result.Checklist) == 0 {
		t.Error("checklist is empty, want the resources that survived named")
	}
	// What the hub kept is not reported as removed. The Forgejo side is: the stub deleted
	// the team and the bot user, and that happened whatever the row ends up doing.
	for _, d := range result.Deleted {
		for _, prefix := range []string{"agent:", "image:", "persona:", "connector:", "mcp:"} {
			if strings.HasPrefix(d, prefix) {
				t.Errorf("deleted = %v, want %q reported as a leftover, not as removed", result.Deleted, d)
			}
		}
	}
}

// TestSetupOptionsSaysWhatTheChecklistMayOffer is the tool step's data: the set each role
// starts with and the tools its rails forbid. Without the first two the wizard can only
// hardcode them and drift from what the plan and the R7 rails enforce. The old pick lists
// are gone: a created-only install makes its own agents, images, personas and MCP
// server, so there is nothing to offer to pick — and no llm or MCP URL either, since
// those live in the settings store now.
func TestSetupOptionsSaysWhatTheChecklistMayOffer(t *testing.T) {
	h := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		Images: []hub.Ref{
			{ID: "img-1", Name: "test-image"},
		},
		MCPServers: []hub.MCPServer{
			{Name: "forgejo-mcp-test", URL: "http://forgejo.example.com/api/mcp", TokenFromEnv: "FORGEJO_TOKEN"},
		},
	}

	s := &fakeStore{installs: make(map[string]setup.Install)}
	api := NewSetup(h, nil, s, fakeSettings{}, SetupConfig{})

	req := httptest.NewRequest("GET", "/api/setup/options", nil)
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", w.Code, w.Body.String())
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// No pick lists: not a running instance's agents, images, personas or MCP servers.
	for _, gone := range []string{"images", "agents", "personas", "mcpServers"} {
		if _, ok := result[gone]; ok {
			t.Errorf("options still offers %q — a created-only install has nothing to pick", gone)
		}
	}

	var tools struct {
		Defaults  map[string][]string `json:"defaults"`
		Forbidden map[string][]string `json:"forbidden"`
	}
	if err := json.Unmarshal(result["tools"], &tools); err != nil {
		t.Fatalf("tools: %v", err)
	}

	if !slices.Contains(tools.Defaults["developer"], "bash") {
		t.Errorf("developer defaults = %v, want the shipped developer profile", tools.Defaults["developer"])
	}
	if slices.Contains(tools.Defaults["reviewer"], "bash") {
		t.Errorf("reviewer defaults = %v, want no bash: only the developer runs it", tools.Defaults["reviewer"])
	}
	if !slices.Contains(tools.Forbidden["refiner"], "bash") {
		t.Errorf("refiner forbidden = %v, want bash among the tools it may never have", tools.Forbidden["refiner"])
	}
	if !slices.Contains(tools.Forbidden["developer"], "submit_pull_review") {
		t.Errorf("developer forbidden = %v, want review writes: only the reviewer casts a review", tools.Forbidden["developer"])
	}
	if slices.Contains(tools.Forbidden["reviewer"], "submit_pull_review") {
		t.Errorf("reviewer forbidden = %v, want review writes allowed for the reviewer", tools.Forbidden["reviewer"])
	}
}

// TestSetupDeleteRemovesPrPatrol: the install's cron trigger goes with it — by the
// id the list carried — and another install's patrol of the same shape is left
// alone.
func TestSetupDeleteRemovesPrPatrol(t *testing.T) {
	h := &hub.Fake{
		Triggers: map[string][]setup.Trigger{
			"c-1": {{ID: "trigger-1", Name: "forgejo-developer-mention-test", ConnectorRef: "c-1"}},
		},
		CronTriggers: []setup.CronTrigger{
			{ID: "cron-own", Name: "forgejo-developer-pr-patrol-test", Schedule: "*/30 * * * *", Enabled: true},
			{ID: "cron-other", Name: "forgejo-developer-pr-patrol-other", Schedule: "*/30 * * * *", Enabled: true},
		},
	}

	s := &fakeStore{
		installs: map[string]setup.Install{
			"test": {
				Slug:        "test",
				GroupID:     "g-1",
				BaseURL:     "https://git.test",
				Org:         "test-org",
				ConnectorID: "c-1",
				Roles: map[setup.Role]setup.RoleState{
					setup.RoleDeveloper: {AgentID: "a-2", TeamID: 2, BotLogin: "developer-test"},
				},
			},
		},
	}

	api := NewSetup(h, nil, s, fakeSettings{}, SetupConfig{})

	req := httptest.NewRequest("POST", "/api/setup/installs/test/remove", nil)
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", w.Code)
	}

	var resp struct {
		Deleted   []string `json:"deleted"`
		Checklist []string `json:"checklist"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	if !contains(resp.Deleted, "cronTrigger:cron-own") {
		t.Errorf("deleted = %v, want cronTrigger:cron-own", resp.Deleted)
	}
	for _, d := range h.DeletedCronTriggers {
		if d == "cron-other" {
			t.Errorf("deleted another install's patrol: %v", h.DeletedCronTriggers)
		}
	}
}

// TestSetupInstallsHidesRetiringRows: the wizard's "existing installs" screen is a list of
// installs the admin can edit. A row the cutover keeps for a pending cleanup is none of
// that — its resources are being deleted — so the route hides it. The store still answers
// with it, because the cutover and the remove route read the unfiltered list.
func TestSetupInstallsHidesRetiringRows(t *testing.T) {
	s := &fakeStore{installs: map[string]setup.Install{
		"apps":   {Slug: "apps", ConnectorID: "c-1"},
		"legacy": {Slug: "legacy", ConnectorID: "c-2", Retiring: true},
	}}
	api := NewSetup(&hub.Fake{}, nil, s, fakeSettings{}, SetupConfig{})

	req := httptest.NewRequest("GET", "/api/setup/installs", nil)
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", w.Code)
	}
	var resp struct {
		Installs []setup.Install `json:"installs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body)
	}
	if len(resp.Installs) != 1 || resp.Installs[0].Slug != "apps" {
		t.Errorf("installs = %+v, want only [apps]", resp.Installs)
	}
	if got, err := getInstallBySlug(context.Background(), s, "legacy"); err != nil || got == nil {
		t.Errorf("the retiring row is not in the store any more: %+v (%v)", got, err)
	}
}

// TestSetupRetiringRowIsStillFoundBySlug is the other half of hiding it: the slug stays
// taken, so a new install cannot be applied over a row that is the only pointer to
// resources still standing, and the remove route still reaches the row — which is how an
// admin gives up on a resource the hub will not delete.
func TestSetupRetiringRowIsStillFoundBySlug(t *testing.T) {
	s := &fakeStore{installs: map[string]setup.Install{
		"legacy": {Slug: "legacy", ConnectorID: "c-2", Retiring: true},
	}}

	got, err := getInstallBySlug(context.Background(), s, "legacy")
	if err != nil {
		t.Fatalf("getInstallBySlug: %v", err)
	}
	if !got.Retiring {
		t.Error("the plan reads the unfiltered list, so a retiring slug still reads as taken")
	}

	api := NewSetup(&hub.Fake{}, nil, s, fakeSettings{}, SetupConfig{})
	req := httptest.NewRequest("POST", "/api/setup/installs/legacy/remove", nil)
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("remove a retiring install: got %d, want 200 (%s)", w.Code, w.Body)
	}
	if got, err := getInstallBySlug(context.Background(), s, "legacy"); err != nil || got != nil {
		t.Errorf("the retiring row was not removable: got %+v (%v)", got, err)
	}
}

// applyWithManagerGuard runs a full apply against a fake hub that enforces the hub's
// named-manager rule, and returns the recorder. It is the shape every managedBy test
// here needs: if any wizard write slipped back onto the caller's credential, the fake
// stamps that resource "user" — and the guard then refuses the wizard's own later
// write of it, so the apply fails instead of quietly shipping a console-editable
// install.
func applyWithManagerGuard(t *testing.T) (*hub.Fake, *httptest.ResponseRecorder) {
	t.Helper()

	allTools := make(map[string]bool)
	for _, role := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
		for _, tool := range content.DefaultTools(role) {
			allTools[tool] = true
		}
	}
	defaultImageTools := make([]hub.ImageTool, 0, len(allTools))
	for tool := range allTools {
		defaultImageTools = append(defaultImageTools, hub.ImageTool{Name: tool, Kind: "mcp"})
	}

	h := &hub.Fake{
		EnforceManaged:           true,
		PluginGroupsSeed:         []string{"g-1"},
		DefaultRefreshImageTools: defaultImageTools,
		NextConnector: hub.Connector{
			Ref:                hub.Ref{ID: "c-1", Name: "c-1"},
			WebhookEndpoint:    "https://hub.test/webhook",
			WebhookSecretValue: "NEWSECRET-xyz",
		},
	}
	fg := &forgejoStub{orgExists: true, adminIsAdmin: true, loginExists: map[string]bool{}, tokenToReturn: "BOTTOKEN-123"}
	s := &fakeStore{installs: make(map[string]setup.Install)}
	api := NewSetup(h, fg.client(), s, fakeSettings{}, SetupConfig{ForgejoBaseURL: "https://git.test"})

	reqBody, _ := json.Marshal(struct {
		Slug       string                         `json:"slug"`
		GroupID    string                         `json:"groupId"`
		BaseURL    string                         `json:"baseUrl"`
		Org        string                         `json:"org"`
		AdminToken string                         `json:"adminToken"`
		Roles      map[setup.Role]setup.RoleInput `json:"roles"`
	}{
		Slug:       "test",
		GroupID:    "g-1",
		BaseURL:    "https://git.test",
		Org:        "test-org",
		AdminToken: "ADMINTOKEN-xyz",
		Roles: map[setup.Role]setup.RoleInput{
			setup.RoleRefiner:   {Runtime: "python:3.11"},
			setup.RoleDeveloper: {Runtime: "python:3.11"},
			setup.RoleReviewer:  {Runtime: "python:3.11"},
		},
	})
	req := httptest.NewRequest("POST", "/api/setup/apply", bytes.NewReader(reqBody))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)
	return h, w
}

// TestSetupApplyStampsEveryResourceAsThePlugin is the acceptance criterion for #67 at
// the level the rule is enforced: the hub derives a create's managedBy from the
// authenticated caller, so what matters is that every wizard create arrives as the
// plugin. The guarded fake stamps each resource accordingly and refuses any mutation
// of a stamped resource that does not — so a passing apply here means all five
// stampable kinds (personas, images, agents, connector, MCP server) were created as
// the plugin, and the wizard's own tool writes on those images passed the guard.
func TestSetupApplyStampsEveryResourceAsThePlugin(t *testing.T) {
	h, w := applyWithManagerGuard(t)
	if w.Code != http.StatusOK {
		t.Fatalf("apply under the manager guard = %d, want 200: %s", w.Code, w.Body.String())
	}

	want := "plugin-forgejo"
	// Every resource kind the hub stamps, by the ids the apply recorded on the row:
	// three personas, three images, three agents, one connector, one MCP server.
	var resp struct {
		Install setup.Install `json:"install"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	keys := []string{"connector:" + resp.Install.ConnectorID, "mcp:" + resp.Install.MCPServer}
	for _, role := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
		st := resp.Install.Roles[role]
		keys = append(keys, "persona:"+st.PersonaID, "image:"+st.ImageID, "agent:"+st.AgentID)
	}
	for _, key := range keys {
		if got := h.ManagedBy[key]; got != want {
			t.Errorf("ManagedBy[%s] = %q, want %q", key, got, want)
		}
	}
}

// TestSetupRemoveWritesAsThePlugin removes an install whose resources the fake has
// stamped as the plugin's, under the guard: the console could not remove these at
// all, and the wizard still can. If the remove path ran on the caller's credential,
// every stamped delete would answer 409 and the hub-side checklist would fill up.
func TestSetupRemoveWritesAsThePlugin(t *testing.T) {
	stamped := map[string]string{
		"agent:a-1":            "plugin-forgejo",
		"image:img-1":          "plugin-forgejo",
		"persona:p-1":          "plugin-forgejo",
		"connector:c-1":        "plugin-forgejo",
		"mcp:forgejo-mcp-test": "plugin-forgejo",
	}
	h := &hub.Fake{
		EnforceManaged: true,
		ManagedBy:      stamped,
		Triggers: map[string][]setup.Trigger{
			"c-1": {{Name: "forgejo-refiner-issue-opened-test"}},
		},
	}
	s := &fakeStore{
		installs: map[string]setup.Install{
			"test": {
				Slug:        "test",
				ConnectorID: "c-1",
				MCPServer:   "forgejo-mcp-test",
				Org:         "test-org",
				HookID:      42,
				Roles: map[setup.Role]setup.RoleState{
					setup.RoleRefiner: {AgentID: "a-1", ImageID: "img-1", PersonaID: "p-1", BotLogin: "refiner-bot", TeamID: 7},
				},
			},
		},
	}
	fg := &forgejoStub{orgExists: true}
	api := NewSetup(h, fg.client(), s, fakeSettings{}, SetupConfig{})

	reqBody, _ := json.Marshal(struct {
		AdminToken string `json:"adminToken"`
	}{AdminToken: "ADMINTOKEN-xyz"})
	req := httptest.NewRequest("POST", "/api/setup/installs/test/remove", bytes.NewReader(reqBody))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("remove under the manager guard = %d, want 200: %s", w.Code, w.Body.String())
	}
	var result struct {
		Deleted   []string `json:"deleted"`
		Checklist []string `json:"checklist"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, want := range []string{"agent:a-1", "image:img-1", "persona:p-1", "connector:c-1", "mcp:forgejo-mcp-test"} {
		if !contains(result.Deleted, want) {
			t.Errorf("deleted = %v, want %q removed: the wizard writes what it owns as the named manager", result.Deleted, want)
		}
	}
	for _, c := range result.Checklist {
		if strings.HasPrefix(c, "agent:") || strings.HasPrefix(c, "image:") ||
			strings.HasPrefix(c, "persona:") || strings.HasPrefix(c, "connector:") ||
			strings.HasPrefix(c, "mcp:") || strings.HasPrefix(c, "trigger:") {
			t.Errorf("checklist = %v, want no hub leftovers: a refused delete reads as the wizard losing its own resource", result.Checklist)
		}
	}
}

// TestSetupRepairConnectionRotatesAsThePlugin: the connector is stamped as the
// plugin's, so the console cannot rotate its secret — only the wizard can, and only
// writing as the plugin. Under the guard, a rotate that kept the caller's credential
// would answer 409 and fail the route.
func TestSetupRepairConnectionRotatesAsThePlugin(t *testing.T) {
	h := &hub.Fake{
		EnforceManaged: true,
		ManagedBy:      map[string]string{"connector:c-1": "plugin-forgejo"},
		NextConnector: hub.Connector{
			Ref:                hub.Ref{ID: "c-1", Name: "c-1"},
			WebhookEndpoint:    "https://hub.test/webhook",
			WebhookSecretValue: "NEWSECRET-xyz",
		},
	}
	s := &fakeStore{
		installs: map[string]setup.Install{
			"test": {Slug: "test", ConnectorID: "c-1", Org: "test-org", HookID: 42},
		},
	}
	fg := &forgejoStub{orgExists: true}
	api := NewSetup(h, fg.client(), s, fakeSettings{}, SetupConfig{})

	reqBody, _ := json.Marshal(struct {
		AdminToken string `json:"adminToken"`
	}{AdminToken: "ADMINTOKEN-xyz"})
	req := httptest.NewRequest("POST", "/api/setup/installs/test/repair-connection", bytes.NewReader(reqBody))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("repair under the manager guard = %d, want 200: %s", w.Code, w.Body.String())
	}
	if len(h.RotatedConnectors) != 1 || h.RotatedConnectors[0] != "c-1" {
		t.Errorf("rotated = %v, want c-1", h.RotatedConnectors)
	}
}

// -- the remove converging, and the orphan sweep --------------------------------------

// TestSetupDeleteForgejoFailureKeepsTheRow: a Forgejo delete that fails is an
// outstanding item like any hub one — the row survives with the checklist naming
// what is left, so the next remove can finish the job.
func TestSetupDeleteForgejoFailureKeepsTheRow(t *testing.T) {
	h := &hub.Fake{}
	s := &fakeStore{
		installs: map[string]setup.Install{
			"test": {
				Slug: "test", GroupID: "g-1", BaseURL: "https://git.test", Org: "test-org",
				ConnectorID: "c-1", HookID: 42,
				Roles: map[setup.Role]setup.RoleState{
					setup.RoleRefiner:   {AgentID: "a-1", TeamID: 1, BotLogin: "refiner-test"},
					setup.RoleDeveloper: {AgentID: "a-2", TeamID: 2, BotLogin: "developer-test"},
					setup.RoleReviewer:  {AgentID: "a-3", TeamID: 3, BotLogin: "reviewer-test"},
				},
			},
		},
	}
	fg := &forgejoStub{orgExists: true, failDeleteWith: http.StatusInternalServerError}

	api := NewSetup(h, fg.client(), s, fakeSettings{}, SetupConfig{ForgejoBaseURL: "https://git.test"})

	body, _ := json.Marshal(struct {
		AdminToken string `json:"adminToken"`
	}{"ADMINTOKEN-xyz"})
	req := httptest.NewRequest("POST", "/api/setup/installs/test/remove", bytes.NewReader(body))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var resp struct {
		Deleted   []string `json:"deleted"`
		Checklist []string `json:"checklist"`
		Kept      bool     `json:"kept"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	if !resp.Kept {
		t.Errorf("kept = false, want true: every Forgejo delete failed")
	}
	if _, still := s.installs["test"]; !still {
		t.Error("the install row was deleted while its Forgejo items are outstanding")
	}
	for _, want := range []string{"hook:42", "team:1", "team:2", "team:3",
		"user:refiner-test", "user:developer-test", "user:reviewer-test"} {
		if !contains(resp.Checklist, want) {
			t.Errorf("checklist: got %v, want %s", resp.Checklist, want)
		}
	}
}

// TestSetupDeleteRetriedConverges: the whole point of keeping the row. The first
// remove ran without the token and kept the row; the second, with the token,
// deletes everything and the row goes.
func TestSetupDeleteRetriedConverges(t *testing.T) {
	h := &hub.Fake{}
	s := &fakeStore{
		installs: map[string]setup.Install{
			"test": {
				Slug: "test", GroupID: "g-1", BaseURL: "https://git.test", Org: "test-org",
				ConnectorID: "c-1", HookID: 42,
				Roles: map[setup.Role]setup.RoleState{
					setup.RoleRefiner:   {AgentID: "a-1", TeamID: 1, BotLogin: "refiner-test"},
					setup.RoleDeveloper: {AgentID: "a-2", TeamID: 2, BotLogin: "developer-test"},
					setup.RoleReviewer:  {AgentID: "a-3", TeamID: 3, BotLogin: "reviewer-test"},
				},
			},
		},
	}
	fg := &forgejoStub{orgExists: true}
	api := NewSetup(h, fg.client(), s, fakeSettings{}, SetupConfig{ForgejoBaseURL: "https://git.test"})

	remove := func(adminToken string) (int, string) {
		var reader *bytes.Reader
		if adminToken != "" {
			body, _ := json.Marshal(struct {
				AdminToken string `json:"adminToken"`
			}{adminToken})
			reader = bytes.NewReader(body)
		} else {
			reader = bytes.NewReader(nil)
		}
		req := httptest.NewRequest("POST", "/api/setup/installs/test/remove", reader)
		req.Header.Set("X-Ainsel-Credential", "test-cred")
		req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}

	// First pass, no token: the row is kept with the Forgejo items outstanding.
	if code, body := remove(""); code != http.StatusOK || !strings.Contains(body, `"kept":true`) {
		t.Fatalf("first remove: %d %s, want 200 with kept", code, body)
	}
	if _, still := s.installs["test"]; !still {
		t.Fatal("the row was deleted by the first remove")
	}

	// Second pass, with the token: the same targets, deleted for real, row gone.
	if code, body := remove("ADMINTOKEN-xyz"); code != http.StatusOK || strings.Contains(body, `"kept":true`) {
		t.Fatalf("second remove: %d %s, want 200 without kept", code, body)
	}
	if _, still := s.installs["test"]; still {
		t.Error("the row survived a remove that deleted everything")
	}
}

// -- the orphan sweep -----------------------------------------------------------------

// orphanFixture: a slug whose row is gone, and a Forgejo holding some of what the
// wizard made for it — two of the three bots, one team, and two org hooks, one of
// which points at the hub and one of which a human made.
func orphanStub() *forgejoStub {
	return &forgejoStub{
		orgExists:    true,
		adminIsAdmin: true,
		loginExists:  map[string]bool{"refiner-ai": true, "reviewer-ai": true},
		teams:        map[string]int64{"ainsel-refiner-ai": 11, "ainsel-developer-ai": 12},
		orgHooks: []map[string]any{
			{"id": 5, "type": "forgejo", "active": true,
				"config": map[string]any{"url": "https://hub.test/api/v1/connectors/c-9/webhook"}},
			{"id": 6, "type": "gitea", "active": true,
				"config": map[string]any{"url": "https://human.example/webhook"}},
		},
	}
}

func orphanAPI(fg *forgejoStub, s *fakeStore) http.Handler {
	h := &hub.Fake{}
	return NewSetupService(h, fg.client(), s, fakeSettings{}, SetupConfig{
		ForgejoBaseURL: "https://git.test",
		HubBaseURL:     "https://hub.test",
	}).Handler()
}

func orphanCall(t *testing.T, h http.Handler, slug, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/setup/orphans/"+slug+"/plan", strings.NewReader(body))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

// TestSetupOrphanPlanNamesTheLeftovers: the plan finds exactly the convention-
// named resources and the hook that points at the hub — and not the human's hook,
// not the team that is gone, not the login that is free.
func TestSetupOrphanPlanNamesTheLeftovers(t *testing.T) {
	h := orphanAPI(orphanStub(), &fakeStore{installs: map[string]setup.Install{}})

	code, body := orphanCall(t, h, "ai", `{"baseUrl":"https://git.test","org":"test-org","adminToken":"ADMINTOKEN-xyz"}`)
	if code != http.StatusOK {
		t.Fatalf("plan: %d %s", code, body)
	}
	var plan struct {
		Items []struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &plan); err != nil {
		t.Fatalf("decode: %v (body %s)", err, body)
	}

	got := []string{}
	for _, it := range plan.Items {
		got = append(got, it.Kind+":"+it.Name)
	}
	want := []string{
		"user:refiner-ai", "user:reviewer-ai",
		"team:ainsel-refiner-ai", "team:ainsel-developer-ai",
		"hook:https://hub.test/api/v1/connectors/c-9/webhook",
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("items = %v, want %v", got, want)
	}
}

// TestSetupOrphanDeleteRemovesThePlannedSet: the delete re-derives the same set
// and removes it; the report names every item by kind and name.
func TestSetupOrphanDeleteRemovesThePlannedSet(t *testing.T) {
	fg := orphanStub()
	h := orphanAPI(fg, &fakeStore{installs: map[string]setup.Install{}})

	req := httptest.NewRequest("POST", "/api/setup/orphans/ai/delete", strings.NewReader(
		`{"baseUrl":"https://git.test","org":"test-org","adminToken":"ADMINTOKEN-xyz"}`))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	var report struct {
		Deleted []string `json:"deleted"`
		Failed  []string `json:"failed"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &report)
	if len(report.Failed) != 0 {
		t.Errorf("failed = %v, want none", report.Failed)
	}
	if len(report.Deleted) != 5 {
		t.Errorf("deleted = %v, want the five leftovers", report.Deleted)
	}
	if fg.deleteCalls != 5 {
		t.Errorf("forgejo deletes = %d, want 5", fg.deleteCalls)
	}
}

// TestSetupOrphanSweepRefusals: the sweep is admin work with an admin token —
// a non-admin caller, a missing token, an unlisted instance and a missing slug
// are all refused before anything is read or deleted.
func TestSetupOrphanSweepRefusals(t *testing.T) {
	h := orphanAPI(orphanStub(), &fakeStore{installs: map[string]setup.Install{}})

	t.Run("non-admin caller", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/setup/orphans/ai/plan", strings.NewReader(
			`{"baseUrl":"https://git.test","org":"test-org","adminToken":"ADMINTOKEN-xyz"}`))
		req.Header.Set("X-Ainsel-Credential", "test-cred")
		req.Header.Set("X-Ainsel-Context", callerContextHeader(t, false))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", w.Code)
		}
	})

	t.Run("no admin token", func(t *testing.T) {
		code, body := orphanCall(t, h, "ai", `{"baseUrl":"https://git.test","org":"test-org"}`)
		if code != http.StatusBadRequest || !strings.Contains(body, "adminToken") {
			t.Errorf("status = %d body = %s, want 400 naming adminToken", code, body)
		}
	})

	t.Run("instance outside the configured list", func(t *testing.T) {
		code, body := orphanCall(t, h, "ai", `{"baseUrl":"https://elsewhere.test","org":"test-org","adminToken":"ADMINTOKEN-xyz"}`)
		if code != http.StatusBadRequest {
			t.Errorf("status = %d body = %s, want 400", code, body)
		}
	})

	t.Run("no slug", func(t *testing.T) {
		// A blank slug reaches the service; a missing path segment is a 404 from
		// the router before the sweep is ever asked.
		code, body := orphanCall(t, h, "%20", `{"baseUrl":"https://git.test","org":"test-org","adminToken":"ADMINTOKEN-xyz"}`)
		if code != http.StatusBadRequest || !strings.Contains(body, "slug") {
			t.Errorf("status = %d body = %s, want 400 naming slug", code, body)
		}
	})

	t.Run("token that is not a site admin", func(t *testing.T) {
		fg := orphanStub()
		fg.adminIsAdmin = false
		hh := orphanAPI(fg, &fakeStore{installs: map[string]setup.Install{}})
		code, body := orphanCall(t, hh, "ai", `{"baseUrl":"https://git.test","org":"test-org","adminToken":"ADMINTOKEN-xyz"}`)
		if code != http.StatusBadRequest || !strings.Contains(body, "site administrator") {
			t.Errorf("status = %d body = %s, want 400 naming the token", code, body)
		}
	})
}

func orphanDeleteCall(t *testing.T, h http.Handler, slug, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/setup/orphans/"+slug+"/delete", strings.NewReader(body))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

// TestSetupOrphanSweepRefusesALiveSlug: the sweep is for a slug whose row is gone.
// Where a row is still there, the slug is an install and the sweep is refused before
// Forgejo is asked for anything — the typo'd slug that names a live install is the
// case that would otherwise purge a working install's accounts and leave its row
// pointing at nothing.
func TestSetupOrphanSweepRefusesALiveSlug(t *testing.T) {
	store := &fakeStore{installs: map[string]setup.Install{
		"ai": {Slug: "ai", BaseURL: "https://git.test", Org: "test-org", HookID: 5},
	}}
	h := orphanAPI(orphanStub(), store)

	code, body := orphanCall(t, h, "ai", `{"baseUrl":"https://git.test","org":"test-org","adminToken":"ADMINTOKEN-xyz"}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "still installed") {
		t.Errorf("plan: %d %s, want 400 naming the live install", code, body)
	}

	fg := orphanStub()
	hh := orphanAPI(fg, store)
	code, body = orphanDeleteCall(t, hh, "ai", `{"baseUrl":"https://git.test","org":"test-org","adminToken":"ADMINTOKEN-xyz"}`)
	if code != http.StatusBadRequest {
		t.Errorf("delete: %d %s, want 400", code, body)
	}
	if fg.deleteCalls != 0 {
		t.Errorf("delete calls = %d, want none for a slug that is still installed", fg.deleteCalls)
	}
}

// TestSetupOrphanSweepSparesWhatARowStillReferences: two installs in one org. One
// lost its row and is being swept; the other is still listed, and its hook, team and
// bot login are the ones the sweep's convention and its hub-URL match would also
// name. The plan must not list them and the delete must not remove them — the plan
// is only a control if it can tell a live install's resources from an orphan's.
func TestSetupOrphanSweepSparesWhatARowStillReferences(t *testing.T) {
	fg := orphanStub()
	fg.orgHooks = append(fg.orgHooks, map[string]any{
		"id": 7, "type": "forgejo", "active": true,
		"config": map[string]any{"url": "https://hub.test/api/v1/connectors/c-77/webhook"},
	})
	store := &fakeStore{installs: map[string]setup.Install{
		"live": {
			Slug: "live", BaseURL: "https://git.test", Org: "test-org", HookID: 5,
			Roles: map[setup.Role]setup.RoleState{
				setup.RoleRefiner: {BotLogin: "refiner-ai", TeamID: 11},
			},
		},
	}}
	h := orphanAPI(fg, store)

	code, body := orphanCall(t, h, "ai", `{"baseUrl":"https://git.test","org":"test-org","adminToken":"ADMINTOKEN-xyz"}`)
	if code != http.StatusOK {
		t.Fatalf("plan: %d %s", code, body)
	}
	for _, gone := range []string{`"refiner-ai"`, `"ainsel-refiner-ai"`, `"id":"5"`} {
		if strings.Contains(body, gone) {
			t.Errorf("plan lists %s, which the live install's row still references: %s", gone, body)
		}
	}
	if !strings.Contains(body, "reviewer-ai") || !strings.Contains(body, "ainsel-developer-ai") ||
		!strings.Contains(body, "c-77/webhook") {
		t.Errorf("plan lost the real leftovers: %s", body)
	}

	code, body = orphanDeleteCall(t, h, "ai", `{"baseUrl":"https://git.test","org":"test-org","adminToken":"ADMINTOKEN-xyz"}`)
	if code != http.StatusOK {
		t.Fatalf("delete: %d %s", code, body)
	}
	var report struct {
		Deleted []string `json:"deleted"`
		Failed  []string `json:"failed"`
	}
	_ = json.Unmarshal([]byte(body), &report)
	if len(report.Failed) != 0 {
		t.Errorf("failed = %v, want none", report.Failed)
	}
	for _, d := range report.Deleted {
		if d == "user:refiner-ai" || d == "team:ainsel-refiner-ai" {
			t.Errorf("deleted %s, which the live install still needs", d)
		}
	}
	// The live install's hook (id 5) survives: three items deleted, not four.
	if len(report.Deleted) != 3 {
		t.Errorf("deleted = %v, want the three true leftovers", report.Deleted)
	}
}

// TestSetupOrphanPlanWithoutHubURLClaimsNoHooks: the hub address is the only
// hook tell a lost row leaves. Without it the sweep names users and teams and
// leaves every hook alone — guessing here is exactly what the two steps exist
// to prevent.
func TestSetupOrphanPlanWithoutHubURLClaimsNoHooks(t *testing.T) {
	h := NewSetupService(&hub.Fake{}, orphanStub().client(), &fakeStore{installs: map[string]setup.Install{}},
		fakeSettings{}, SetupConfig{ForgejoBaseURL: "https://git.test"}).Handler()

	code, body := orphanCall(t, h, "ai", `{"baseUrl":"https://git.test","org":"test-org","adminToken":"ADMINTOKEN-xyz"}`)
	if code != http.StatusOK {
		t.Fatalf("plan: %d %s", code, body)
	}
	if strings.Contains(body, `"kind":"hook"`) {
		t.Errorf("body = %s, want no hook without a hub address to match", body)
	}
	if !strings.Contains(body, `"kind":"user"`) {
		t.Errorf("body = %s, want the users still named", body)
	}
}

// wizardKeySettings is the settings reading the wizard builds its plan on, with
// one role moved onto a provider that has a key: the wizard's own key box only
// has somewhere to go once the role runs on a provider that holds one.
type wizardKeySettings struct {
	fakeSettings
	role     setup.Role
	provider string
}

func (w wizardKeySettings) All(ctx context.Context) ([]settings.Setting, error) {
	out, err := w.fakeSettings.All(ctx)
	if err != nil {
		return nil, err
	}
	for i, s := range out {
		if s.Role == string(w.role) && s.Field == settings.FieldLLMProvider {
			out[i].Value = w.provider
		}
	}
	return out, nil
}

// setupApplyRequest is the wizard's apply body: the shape the console posts, in
// the shape the route decodes it.
type setupApplyRequest struct {
	Slug       string                         `json:"slug"`
	GroupID    string                         `json:"groupId"`
	BaseURL    string                         `json:"baseUrl"`
	Org        string                         `json:"org"`
	AdminToken string                         `json:"adminToken"`
	Roles      map[setup.Role]setup.RoleInput `json:"roles"`
}

// defaultImageToolSet is what the fake hub hands back when an image's tools are
// refreshed: every tool the shipped profiles name, disabled, as a fresh catalog
// arrives.
func defaultImageToolSet() []hub.ImageTool {
	all := map[string]bool{}
	for _, role := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
		for _, tool := range content.DefaultTools(role) {
			all[tool] = true
		}
	}
	out := make([]hub.ImageTool, 0, len(all))
	for tool := range all {
		out = append(out, hub.ImageTool{Name: tool, Kind: "mcp"})
	}
	return out
}

// TestSetupApplyWritesTheWizardKeyOntoTheAgentAndKeepsNoCopy is the wizard's
// half of the key contract: a key typed in the wizard reaches the agent the
// wizard creates, as the hub's own per-agent secret — and nothing else. It is
// not written to the install row (R4 names the install row as no place for a
// key), and the settings store is never asked to hold it: the plugin keeps no
// copy, which is the whole point of the move.
func TestSetupApplyWritesTheWizardKeyOntoTheAgentAndKeepsNoCopy(t *testing.T) {
	h := &hub.Fake{
		PluginGroupsSeed:         []string{"g-1"},
		Personas:                 []hub.Ref{},
		Agents:                   []hub.Ref{},
		Images:                   []hub.Ref{},
		Connectors:               []hub.Ref{},
		MCPServers:               []hub.MCPServer{},
		SeedImages:               map[string]hub.Image{},
		DefaultRefreshImageTools: defaultImageToolSet(),
		NextConnector: hub.Connector{
			Ref:                hub.Ref{ID: "c-1", Name: "c-1"},
			WebhookEndpoint:    "https://hub.test/webhook",
			WebhookSecretValue: "NEWSECRET-xyz",
		},
	}
	fg := &forgejoStub{orgExists: true, adminIsAdmin: true, loginExists: map[string]bool{}, tokenToReturn: "BOTTOKEN-123"}
	s := &fakeStore{installs: make(map[string]setup.Install)}

	logBuf := new(bytes.Buffer)
	old := log.Writer()
	log.SetOutput(logBuf)
	defer log.SetOutput(old)

	api := NewSetup(h, fg.client(), s,
		wizardKeySettings{role: setup.RoleDeveloper, provider: setup.LLMProviderAnthropic},
		SetupConfig{ForgejoBaseURL: "https://git.test"})

	body, _ := json.Marshal(setupApplyRequest{
		Slug: "test", GroupID: "g-1", BaseURL: "https://git.test",
		Org: "test-org", AdminToken: "ADMINTOKEN-xyz",
		Roles: map[setup.Role]setup.RoleInput{
			setup.RoleRefiner:   {Runtime: "python:3.11"},
			setup.RoleDeveloper: {Runtime: "python:3.11", LLMKey: "sk-wizard"},
			setup.RoleReviewer:  {Runtime: "python:3.11"},
		},
	})
	req := httptest.NewRequest("POST", "/api/setup/apply", bytes.NewReader(body))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", w.Code, w.Body.String())
	}

	var created *hub.AgentSpec
	for i, a := range h.CreatedAgents {
		if strings.Contains(a.Name, "developer") {
			created = &h.CreatedAgents[i]
		}
	}
	if created == nil {
		t.Fatalf("no developer agent was created: %+v", h.CreatedAgents)
	}
	if created.AnthropicKey != "sk-wizard" {
		t.Errorf("created agent anthropic key = %q, want the wizard's key", created.AnthropicKey)
	}
	for _, a := range h.CreatedAgents {
		if a.Name == created.Name {
			continue
		}
		if a.OpenAIKey != "" || a.AnthropicKey != "" || a.GoogleKey != "" || a.CustomKey != "" {
			t.Errorf("%s: a role that typed no key was created with one: %+v", a.Name, a)
		}
	}

	// The install row carries no key — only, at most, the fingerprint of what
	// reached the agent.
	row := s.installs["test"]
	for role, st := range row.Roles {
		if st.LLMKeyHashOf(setup.LLMProviderAnthropic) != "" && role != setup.RoleDeveloper {
			t.Errorf("%s: the row records a key fingerprint for a role that typed none", role)
		}
	}
	if strings.Contains(w.Body.String(), "sk-wizard") {
		t.Errorf("the apply answer echoes the key")
	}
	if strings.Contains(logBuf.String(), "sk-wizard") {
		t.Errorf("the log carries the key")
	}
}

// TestSetupApplyDropsAWizardKeyForAProviderThatHoldsNone is the wizard key's
// drop rule: a role on a keyless provider has no block to hold a key, and a key
// saved for it would be a key with no agent — so the apply creates the agent
// without one and says nothing, rather than storing a secret nothing will read.
func TestSetupApplyDropsAWizardKeyForAProviderThatHoldsNone(t *testing.T) {
	h := &hub.Fake{
		PluginGroupsSeed:         []string{"g-1"},
		Personas:                 []hub.Ref{},
		Agents:                   []hub.Ref{},
		Images:                   []hub.Ref{},
		Connectors:               []hub.Ref{},
		MCPServers:               []hub.MCPServer{},
		SeedImages:               map[string]hub.Image{},
		DefaultRefreshImageTools: defaultImageToolSet(),
		NextConnector: hub.Connector{
			Ref:                hub.Ref{ID: "c-1", Name: "c-1"},
			WebhookEndpoint:    "https://hub.test/webhook",
			WebhookSecretValue: "NEWSECRET-xyz",
		},
	}
	fg := &forgejoStub{orgExists: true, adminIsAdmin: true, loginExists: map[string]bool{}, tokenToReturn: "BOTTOKEN-123"}
	s := &fakeStore{installs: make(map[string]setup.Install)}

	api := NewSetup(h, fg.client(), s, fakeSettings{}, SetupConfig{ForgejoBaseURL: "https://git.test"})

	body, _ := json.Marshal(setupApplyRequest{
		Slug: "test", GroupID: "g-1", BaseURL: "https://git.test",
		Org: "test-org", AdminToken: "ADMINTOKEN-xyz",
		Roles: map[setup.Role]setup.RoleInput{
			setup.RoleRefiner:   {Runtime: "python:3.11"},
			setup.RoleDeveloper: {Runtime: "python:3.11", LLMKey: "sk-nowhere"},
			setup.RoleReviewer:  {Runtime: "python:3.11"},
		},
	})
	req := httptest.NewRequest("POST", "/api/setup/apply", bytes.NewReader(body))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", w.Code, w.Body.String())
	}
	for _, a := range h.CreatedAgents {
		if a.OpenAIKey != "" || a.AnthropicKey != "" || a.GoogleKey != "" || a.CustomKey != "" {
			t.Errorf("%s: created with a key the provider cannot hold: %+v", a.Name, a)
		}
	}
}
