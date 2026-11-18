package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/settings"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
)

// instanceSetup builds the setup route over one configured instance: the
// address fakeSettings lists, standing in for the stub Forgejo. The plan and
// apply paths only reach the forge once the address is on the list, so the
// config names it too and the stub client is the one reused.
func instanceSetup(t *testing.T, fg *forgejoStub, s *fakeStore) (http.Handler, *forgejoStub) {
	t.Helper()
	if fg == nil {
		fg = &forgejoStub{orgExists: true, adminIsAdmin: true, loginExists: map[string]bool{}}
	}
	h := &hub.Fake{PluginGroupsSeed: []string{"g-1"}}
	return NewSetup(h, fg.client(), s, fakeSettings{}, SetupConfig{ForgejoBaseURL: "https://git.test"}), fg
}

// planRequest posts a setup plan as the console does and returns the recorder.
func planRequest(t *testing.T, api http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/setup/plan", strings.NewReader(body))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)
	return w
}

const planBodyTemplate = `{"slug":"test","groupId":"g-1","baseUrl":%q,"org":"test-org","adminToken":"ADMINTOKEN-xyz",` +
	`"roles":{"refiner":{"runtime":"python:3.11"},"developer":{"runtime":"python:3.11"},"reviewer":{"runtime":"python:3.11"}}}`

// TestSetupPlanRefusesAnUnconfiguredBaseURL is the issue's core refusal: an
// address outside the configured list is refused before any Forgejo is
// contacted, so a typo cannot produce a preview of an install pointed at the
// wrong host.
func TestSetupPlanRefusesAnUnconfiguredBaseURL(t *testing.T) {
	fg := &forgejoStub{orgExists: true, adminIsAdmin: true, loginExists: map[string]bool{}}
	api, _ := instanceSetup(t, fg, &fakeStore{installs: map[string]setup.Install{}})

	w := planRequest(t, api, fmt.Sprintf(planBodyTemplate, "https://typo.example.com"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("plan with an unconfigured baseUrl: %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "https://typo.example.com") {
		t.Errorf("refusal does not name the address: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Settings") {
		t.Errorf("refusal does not point the admin at Settings: %s", w.Body.String())
	}
}

// TestSetupPlanAcceptsTheConfiguredBaseURL checks the other side: the listed
// address plans as it always did.
func TestSetupPlanAcceptsTheConfiguredBaseURL(t *testing.T) {
	api, _ := instanceSetup(t, nil, &fakeStore{installs: map[string]setup.Install{}})

	w := planRequest(t, api, fmt.Sprintf(planBodyTemplate, "https://git.test"))
	if w.Code != http.StatusOK {
		t.Fatalf("plan with the configured baseUrl: %d, want 200: %s", w.Code, w.Body.String())
	}
}

// TestSetupPlanRefusesWhenNoInstanceIsConfigured checks the empty-list case:
// with nothing configured, no address is a configured one, and the refusal says
// to add one rather than pretending the typed address is fine.
func TestSetupPlanRefusesWhenNoInstanceIsConfigured(t *testing.T) {
	h := &hub.Fake{PluginGroupsSeed: []string{"g-1"}}
	fg := &forgejoStub{orgExists: true, adminIsAdmin: true, loginExists: map[string]bool{}}
	api := NewSetup(h, fg.client(), &fakeStore{installs: map[string]setup.Install{}}, emptySettings{},
		SetupConfig{ForgejoBaseURL: "https://git.test"})

	w := planRequest(t, api, fmt.Sprintf(planBodyTemplate, "https://git.test"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("plan with no instance configured: %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no Forgejo instance is configured") {
		t.Errorf("refusal does not say the list is empty: %s", w.Body.String())
	}
}

// TestSetupPlanAllowsEmptyBaseURLOnEdit checks the rule the edit form depends
// on: a blank baseUrl means "keep the install's own address", and that stays
// legal even though the address it keeps is not typed into the form.
func TestSetupPlanAllowsEmptyBaseURLOnEdit(t *testing.T) {
	existing := setup.Install{
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
	}
	api, _ := instanceSetup(t, nil, &fakeStore{installs: map[string]setup.Install{"test": existing}})

	w := planRequest(t, api, fmt.Sprintf(planBodyTemplate, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("edit with a blank baseUrl: %d, want 200: %s", w.Code, w.Body.String())
	}
}

// TestSetupPlanRefusesBlankBaseURLOnCreate is the create-path mirror of the
// edit rule above: with a non-empty instance list and no install to keep, a blank
// baseUrl has nothing to mean — letting it through would store an install with no
// address at all and silently drop it onto the FORGEJO_BASE_URL fallback, possibly
// a host the admin removed from the list. The refusal names the choice to make.
func TestSetupPlanRefusesBlankBaseURLOnCreate(t *testing.T) {
	api, _ := instanceSetup(t, nil, &fakeStore{installs: map[string]setup.Install{}})

	w := planRequest(t, api, fmt.Sprintf(planBodyTemplate, ""))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with a blank baseUrl: %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "instance") {
		t.Errorf("refusal does not name the choice to make: %s", w.Body.String())
	}
}

// TestSetupApplyRefusesBlankBaseURLOnCreate checks the same refusal on the write
// path, where it matters most: an apply must not store an address-less install on
// the env fallback while instances are configured.
func TestSetupApplyRefusesBlankBaseURLOnCreate(t *testing.T) {
	fg := &forgejoStub{orgExists: true, adminIsAdmin: true, loginExists: map[string]bool{}}
	api, _ := instanceSetup(t, fg, &fakeStore{installs: map[string]setup.Install{}})

	req := httptest.NewRequest(http.MethodPost, "/api/setup/apply", strings.NewReader(fmt.Sprintf(planBodyTemplate, "")))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("apply with a blank baseUrl on a create: %d, want 400: %s", w.Code, w.Body.String())
	}
	if fg.methodCount != 0 {
		t.Errorf("refused apply reached a Forgejo anyway: %d calls", fg.methodCount)
	}
}

// TestSetupPlanKeepsAnUnlistedInstallBaseURLOnEdit is the path the wizard's
// "Keep <url>" state submits, and the one the step's notice promises works: the
// install's stored address is not on the configured list (fakeSettings answers
// with https://git.test, the install lives on https://legacy.test), the form
// leaves the choice untouched and therefore submits a blank baseUrl, and the plan
// keeps the row's own address instead of refusing it. The plugin's own address is
// the legacy one, so the stub Forgejo is the client the kept address resolves to.
func TestSetupPlanKeepsAnUnlistedInstallBaseURLOnEdit(t *testing.T) {
	existing := setup.Install{
		Slug:        "test",
		GroupID:     "g-1",
		BaseURL:     "https://legacy.test",
		Org:         "test-org",
		ConnectorID: "c-1",
		HookID:      42,
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleRefiner:   {AgentID: "a-1", ImageID: "img-1", PersonaID: "p-1", TeamID: 1, BotLogin: "refiner-test"},
			setup.RoleDeveloper: {AgentID: "a-2", ImageID: "img-2", PersonaID: "p-2", TeamID: 2, BotLogin: "developer-test"},
			setup.RoleReviewer:  {AgentID: "a-3", ImageID: "img-3", PersonaID: "p-3", TeamID: 3, BotLogin: "reviewer-test"},
		},
	}
	h := &hub.Fake{PluginGroupsSeed: []string{"g-1"}}
	fg := &forgejoStub{orgExists: true, adminIsAdmin: true, loginExists: map[string]bool{}}
	api := NewSetup(h, fg.client(), &fakeStore{installs: map[string]setup.Install{"test": existing}}, fakeSettings{},
		SetupConfig{ForgejoBaseURL: "https://legacy.test"})

	w := planRequest(t, api, fmt.Sprintf(planBodyTemplate, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("edit of an install on an unlisted address: %d, want 200: %s", w.Code, w.Body.String())
	}
	// Blank means keep, not move: the plan must not report a base URL change.
	if strings.Contains(w.Body.String(), "base URL") {
		t.Errorf("plan reports a base URL change for a kept address: %s", w.Body.String())
	}
}

// TestSetupApplyRefusesAnUnconfiguredBaseURL checks the same refusal on the
// write path: an apply cannot land an install on an address nobody configured.
func TestSetupApplyRefusesAnUnconfiguredBaseURL(t *testing.T) {
	api, _ := instanceSetup(t, nil, &fakeStore{installs: map[string]setup.Install{}})

	req := httptest.NewRequest(http.MethodPost, "/api/setup/apply", strings.NewReader(fmt.Sprintf(planBodyTemplate, "https://typo.example.com")))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("apply with an unconfigured baseUrl: %d, want 400: %s", w.Code, w.Body.String())
	}
}

// TestSetupOptionsAnswersTheInstances checks the wizard's read: the options
// answer carries the configured list, in order, beside the address the console
// used to pre-fill.
func TestSetupOptionsAnswersTheInstances(t *testing.T) {
	h := &hub.Fake{PluginGroupsSeed: []string{"g-1"}}
	svc := NewSetupService(h, nil, &fakeStore{installs: map[string]setup.Install{}}, fakeSettings{},
		SetupConfig{ForgejoBaseURL: "https://git.test"})

	opts, err := svc.Options(context.Background(), &hub.Caller{Kind: "user", IsAdmin: true})
	if err != nil {
		t.Fatalf("Options: %v", err)
	}
	if len(opts.Instances) != 1 || opts.Instances[0].BaseURL != "https://git.test" {
		t.Fatalf("instances = %+v, want the one configured instance", opts.Instances)
	}
	if opts.ForgejoBaseURL != "https://git.test" {
		t.Errorf("forgejoBaseURL = %q, want the configured address kept", opts.ForgejoBaseURL)
	}

	// And the answer is a JSON array, not null, when the list is empty: the
	// console renders the link-to-Settings state off an empty array.
	svcEmpty := NewSetupService(h, nil, &fakeStore{installs: map[string]setup.Install{}}, emptySettings{}, SetupConfig{})
	body, err := json.Marshal(mustOptions(t, svcEmpty))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `"instances":[]`) {
		t.Errorf("empty options answer = %s, want an empty instances array", body)
	}
}

func mustOptions(t *testing.T, svc *SetupService) SetupOptions {
	t.Helper()
	opts, err := svc.Options(context.Background(), &hub.Caller{Kind: "user", IsAdmin: true})
	if err != nil {
		t.Fatalf("Options: %v", err)
	}
	return opts
}

// emptySettings is a settings store with the role values the wizard needs and no
// instances at all — the state a fresh checkout is in before the admin adds one.
type emptySettings struct{}

func (emptySettings) All(context.Context) ([]settings.Setting, error) {
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

func (emptySettings) ListInstances(context.Context) ([]settings.Instance, error) {
	return []settings.Instance{}, nil
}

// TestSetupApplyUsesTheInstallOwnForgejo checks the multi-instance rule on the
// write path: an install whose address is a second configured instance is
// created against that address, not the one the environment named.
func TestSetupApplyUsesTheInstallOwnForgejo(t *testing.T) {
	// The stub stands for the second instance: its URL is the address the
	// request names, and the env address is a different host entirely.
	fg := &forgejoStub{orgExists: true, adminIsAdmin: true, loginExists: map[string]bool{}}
	fg.client() // start the server so its URL is known
	stubURL := fg.srv.URL

	h := &hub.Fake{PluginGroupsSeed: []string{"g-1"}}
	svc := NewSetupService(h, nil, &fakeStore{installs: map[string]setup.Install{}},
		stubSettings{base: stubURL}, SetupConfig{ForgejoBaseURL: "https://elsewhere.example.com"})
	api := svc.Handler()

	body := strings.Replace(fmt.Sprintf(planBodyTemplate, "https://elsewhere.example.com"),
		"https://elsewhere.example.com", stubURL, 1)
	req := httptest.NewRequest(http.MethodPost, "/api/setup/apply", strings.NewReader(body))
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	// The apply reaches the stub: the method counter moved. Whatever the apply
	// decides about the rest, an apply that had used the env address could not
	// have reached it at all.
	if fg.methodCount == 0 {
		t.Fatalf("apply against the install's own instance reached no Forgejo; code %d body %s", w.Code, w.Body.String())
	}
}

// stubSettings lists exactly one instance: the stub server's own address.
type stubSettings struct{ base string }

func (stubSettings) All(context.Context) ([]settings.Setting, error) {
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

func (s stubSettings) ListInstances(context.Context) ([]settings.Instance, error) {
	return []settings.Instance{{Name: "stub", BaseURL: s.base}}, nil
}
