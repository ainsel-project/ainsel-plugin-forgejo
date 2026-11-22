package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ainsel-project/ainsel-plugin-forgejo/content"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/reconcile"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/settings"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
)

// newSettingsAPI serves the settings routes over a real settings store in a
// temporary directory: the routes are thin, so the tests read and write the
// same rows the plugin would.
func newSettingsAPI(t *testing.T) (http.Handler, *settings.Store) {
	t.Helper()
	s, err := settings.Open(t.TempDir())
	if err != nil {
		t.Fatalf("settings.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return NewSettings(&hub.Fake{}, s, nil, nil), s
}

// settingsRequest builds a settings request as the hub proxy sends it: a
// credential, an admin caller, and (for PUT) a JSON body.
func settingsRequest(t *testing.T, method, body string) *http.Request {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, "/api/settings", nil)
	} else {
		req = httptest.NewRequest(method, "/api/settings", strings.NewReader(body))
	}
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	return req
}

// decodedSettings reads a settingsResponse out of an answer.
func decodedSettings(t *testing.T, body string) []settings.Setting {
	t.Helper()
	var out struct {
		Settings []settings.Setting `json:"settings"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode settings response: %v\nbody: %s", err, body)
	}
	return out.Settings
}

func settingIn(list []settings.Setting, role, field string) (settings.Setting, bool) {
	for _, s := range list {
		if s.Role == role && s.Field == field {
			return s, true
		}
	}
	return settings.Setting{}, false
}

// TestSettingsRoutesGateTheCaller checks both routes refuse a request with no
// credential (401) and a caller who is not an admin (403): these values apply
// to every install, so they are admin-only reads as much as admin-only writes.
func TestSettingsRoutesGateTheCaller(t *testing.T) {
	api, _ := newSettingsAPI(t)

	tests := []struct {
		method string
		body   string
	}{
		{http.MethodGet, ""},
		{http.MethodPut, `{"settings":[{"role":"refiner","field":"llmModel","value":"x"}]}`},
	}

	for _, tt := range tests {
		// No credential at all.
		var req *http.Request
		if tt.body == "" {
			req = httptest.NewRequest(tt.method, "/api/settings", nil)
		} else {
			req = httptest.NewRequest(tt.method, "/api/settings", strings.NewReader(tt.body))
		}
		w := httptest.NewRecorder()
		api.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s without credential: %d, want 401", tt.method, w.Code)
		}

		// Credential, but a caller who is not an admin.
		req = httptest.NewRequest(tt.method, "/api/settings", strings.NewReader(tt.body))
		req.Header.Set("X-Ainsel-Credential", "test-cred")
		req.Header.Set("X-Ainsel-Context", callerContextHeader(t, false))
		w = httptest.NewRecorder()
		api.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s as a non-admin: %d, want 403", tt.method, w.Code)
		}
	}
}

// TestSettingsGETAnswersDefaults checks the empty state: the answer is the
// whole registry, and the content-backed fields answer with the shipped texts.
func TestSettingsGETAnswersDefaults(t *testing.T) {
	api, _ := newSettingsAPI(t)

	w := httptest.NewRecorder()
	api.ServeHTTP(w, settingsRequest(t, http.MethodGet, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body)
	}

	list := decodedSettings(t, w.Body.String())
	if len(list) != 38 {
		t.Fatalf("got %d settings, want 3 roles × 12 fields and the developer's 2 patrol fields", len(list))
	}
	for _, role := range []string{"refiner", "developer", "reviewer"} {
		persona, ok := settingIn(list, role, settings.FieldPersonaText)
		if !ok || persona.Value != content.Persona(setup.Role(role)) {
			t.Errorf("%s personaText is not the shipped persona", role)
		}
		if persona.Dirty {
			t.Errorf("%s personaText: dirty with no edit", role)
		}
	}
	skill, _ := settingIn(list, "refiner", settings.FieldSkillText)
	if skill.Value != content.SkillSection(setup.Role("refiner")) {
		t.Errorf("skillText is not the shipped skill section for the role")
	}
	if skill.DefaultVersion == "" {
		t.Errorf("defaultVersion is empty; every default carries the version that shipped it")
	}
}

// TestSettingsPUTMarksDirtyAndReadsBack: an edit answers 200 with the full
// set, the field dirty and edited; a later GET still says so.
func TestSettingsPUTMarksDirtyAndReadsBack(t *testing.T) {
	api, _ := newSettingsAPI(t)

	w := httptest.NewRecorder()
	api.ServeHTTP(w, settingsRequest(t, http.MethodPut,
		`{"settings":[{"role":"refiner","field":"llmModel","value":"gpt-x"}]}`))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200 (body %s)", w.Code, w.Body)
	}
	set, ok := settingIn(decodedSettings(t, w.Body.String()), "refiner", settings.FieldLLMModel)
	if !ok || !set.Dirty || set.Value != "gpt-x" {
		t.Fatalf("PUT response does not show the edit dirty: %+v", set)
	}
	if set.Default != settings.Default(setup.Role("refiner"), settings.FieldLLMModel) {
		t.Errorf("PUT response dropped the shipped default")
	}

	w = httptest.NewRecorder()
	api.ServeHTTP(w, settingsRequest(t, http.MethodGet, ""))
	set, _ = settingIn(decodedSettings(t, w.Body.String()), "refiner", settings.FieldLLMModel)
	if !set.Dirty || set.Value != "gpt-x" {
		t.Errorf("GET after PUT: dirty = %v, value = %q", set.Dirty, set.Value)
	}
}

// TestSettingsPUTReset clears the flag and re-adopts the default. A reset is
// offered per field, so reset: true ignores the value it arrives beside.
func TestSettingsPUTReset(t *testing.T) {
	api, _ := newSettingsAPI(t)

	w := httptest.NewRecorder()
	api.ServeHTTP(w, settingsRequest(t, http.MethodPut,
		`{"settings":[{"role":"developer","field":"llmProvider","value":"opencode"}]}`))
	if w.Code != http.StatusOK {
		t.Fatalf("edit: %s", w.Body)
	}

	w = httptest.NewRecorder()
	api.ServeHTTP(w, settingsRequest(t, http.MethodPut,
		`{"settings":[{"role":"developer","field":"llmProvider","value":"ignored","reset":true}]}`))
	if w.Code != http.StatusOK {
		t.Fatalf("reset status = %d, want 200 (body %s)", w.Code, w.Body)
	}
	set, _ := settingIn(decodedSettings(t, w.Body.String()), "developer", settings.FieldLLMProvider)
	if set.Dirty {
		t.Errorf("after reset: dirty = true, want false")
	}
	if want := settings.Default(setup.Role("developer"), settings.FieldLLMProvider); set.Value != want {
		t.Errorf("after reset: value = %q, want the default %q", set.Value, want)
	}
}

// TestSettingsPUTRefusesBadEditsWithoutWriting is the no-partial-writes rule
// at the route: one bad field in the batch answers 400 with a field-level
// error for each bad one, and the good field in the same batch is not written.
func TestSettingsPUTRefusesBadEditsWithoutWriting(t *testing.T) {
	api, _ := newSettingsAPI(t)

	w := httptest.NewRecorder()
	api.ServeHTTP(w, settingsRequest(t, http.MethodPut, `{"settings":[
		{"role":"refiner","field":"llmModel","value":"fine"},
		{"role":"janitor","field":"llmModel","value":"who"},
		{"role":"refiner","field":"soulText","value":"x"},
		{"role":"refiner","field":"skillText","value":"no frontmatter"}
	]}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", w.Code, w.Body)
	}

	var out struct {
		Error  string `json:"error"`
		Fields []struct {
			Role  string `json:"role"`
			Field string `json:"field"`
			Error string `json:"error"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, w.Body)
	}
	if len(out.Fields) != 3 {
		t.Fatalf("field errors = %d, want 3 (body %s)", len(out.Fields), w.Body)
	}
	want := map[string]string{"janitor.llmModel": "unknown role", "refiner.soulText": "unknown field", "refiner.skillText": "frontmatter"}
	for _, f := range out.Fields {
		sub, ok := want[f.Role+"."+f.Field]
		if !ok {
			t.Errorf("unexpected field error for %s.%s", f.Role, f.Field)
			continue
		}
		if !strings.Contains(f.Error, sub) {
			t.Errorf("%s.%s error = %q, want it to mention %q", f.Role, f.Field, f.Error, sub)
		}
	}

	// The good edit in the refused batch wrote nothing.
	w = httptest.NewRecorder()
	api.ServeHTTP(w, settingsRequest(t, http.MethodGet, ""))
	set, _ := settingIn(decodedSettings(t, w.Body.String()), "refiner", settings.FieldLLMModel)
	if set.Dirty {
		t.Errorf("the refused batch wrote refiner.llmModel anyway")
	}
}

// TestSettingsPUTRejectsAnEmptyBody and malformed JSON: both are the caller's
// mistake, answered as one without touching the store.
func TestSettingsPUTRejectsAnEmptyBody(t *testing.T) {
	api, _ := newSettingsAPI(t)

	w := httptest.NewRecorder()
	api.ServeHTTP(w, settingsRequest(t, http.MethodPut, `{"settings":[]}`))
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty batch: %d, want 400", w.Code)
	}

	w = httptest.NewRecorder()
	api.ServeHTTP(w, settingsRequest(t, http.MethodPut, `{not json`))
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad json: %d, want 400", w.Code)
	}
}

// --- reconcile triggers (PR 3) ---

// recordingPass is the reconcile engine as a settings-route test drives it: it
// remembers the scope of every pass it was asked to run and answers with what
// the test hands it — including a failure, which a settings save must survive.
// CheckLLMs answers the same way: the "Check now" route runs it after the pass,
// so the stub carries its own scripted checks and failure.
type recordingPass struct {
	scopes   []reconcile.Scope
	sum      reconcile.Summary
	err      error
	checks   []reconcile.LLMCheck
	checkErr error

	// pushes records the keys the route handed to the engine, in the order it
	// asked — the route's half of the key contract is "a typed key is a write
	// onto the role's agents, and only that". pushErr is what that write answers.
	pushes  []string
	pushErr error
}

func (r *recordingPass) Pass(ctx context.Context, scope reconcile.Scope) (reconcile.Summary, error) {
	r.scopes = append(r.scopes, scope)
	return r.sum, r.err
}

func (r *recordingPass) CheckLLMs(ctx context.Context) ([]reconcile.LLMCheck, error) {
	return r.checks, r.checkErr
}

func (r *recordingPass) PushLLMKey(ctx context.Context, role, provider, key string) error {
	r.pushes = append(r.pushes, role+"/"+provider+"/"+key)
	return r.pushErr
}

// fakeInstalls is the install store as the settings route reads it: which roles
// have an agent the hub holds a secret for. A key typed for a role outside that
// set has nowhere to go, and the route says so per field.
type fakeInstalls struct {
	list []setup.Install
	err  error
}

func (f fakeInstalls) List(context.Context) ([]setup.Install, error) {
	return f.list, f.err
}

// installsOf is the one-line shape the tests write: one install row holding an
// agent for each named role.
func installsOf(roles ...string) installsReader {
	in := setup.Install{Slug: "acme", LayoutVersion: setup.LayoutManagedAgents, Roles: map[setup.Role]setup.RoleState{}}
	for _, r := range roles {
		in.Roles[setup.Role(r)] = setup.RoleState{AgentID: "agent-" + r}
	}
	return fakeInstalls{list: []setup.Install{in}}
}

// decodedFields reads the refused-field list out of an answer, GET or PUT.
func decodedFields(t *testing.T, body string) []settings.FieldError {
	t.Helper()
	var out struct {
		Fields []settings.FieldError `json:"fields"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode fields: %v\nbody: %s", err, body)
	}
	return out.Fields
}

// newSettingsAPIWithPass serves the settings routes over a real settings store
// and a recording pass.
func newSettingsAPIWithPass(t *testing.T, rec passRunner) (http.Handler, *recordingPass) {
	t.Helper()
	s, err := settings.Open(t.TempDir())
	if err != nil {
		t.Fatalf("settings.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	r, ok := rec.(*recordingPass)
	if !ok {
		t.Fatalf("test bug: not a recordingPass")
	}
	return NewSettings(&hub.Fake{}, s, rec, installsOf(string(setup.RoleDeveloper), string(setup.RoleRefiner), string(setup.RoleReviewer))), r
}

// putSettings saves one batch and answers the status code.
func putSettings(t *testing.T, api http.Handler, body string) int {
	t.Helper()
	w := httptest.NewRecorder()
	api.ServeHTTP(w, settingsRequest(t, http.MethodPut, body))
	return w.Code
}

// TestSettingsKeyIsNeverServedAndGoesStraightToTheAgents is the route's half of
// the key's contract now: a typed key is written onto the role's agents and the
// plugin keeps no copy — the PUT's echo and the GET both answer the row empty,
// and the row never reads as edited, because nothing here is set.
func TestSettingsKeyIsNeverServedAndGoesStraightToTheAgents(t *testing.T) {
	rec := &recordingPass{}
	api, _ := newSettingsAPIWithPass(t, rec)

	w := httptest.NewRecorder()
	// The role runs on the custom provider, so the key has a block to land in —
	// a key for a provider the role does not run on is its own test.
	api.ServeHTTP(w, settingsRequest(t, http.MethodPut, `{"settings":[
		{"role":"developer","field":"llmProvider","value":"custom"},
		{"role":"developer","field":"llmCustomUrl","value":"http://gw.local/v1"},
		{"role":"developer","field":"llmCustomKey","value":"sk-secret"}]}`))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200 (body %s)", w.Code, w.Body)
	}
	if len(rec.pushes) != 1 || rec.pushes[0] != "developer/custom/sk-secret" {
		t.Errorf("pushes = %v, want the key written onto developer's agents", rec.pushes)
	}
	set, _ := settingIn(decodedSettings(t, w.Body.String()), "developer", settings.FieldLLMCustomKey)
	if set.Value != "" || set.Dirty {
		t.Errorf("PUT echo: value = %q, dirty = %v, want empty and unedited", set.Value, set.Dirty)
	}
	if strings.Contains(w.Body.String(), "sk-secret") {
		t.Errorf("PUT body carries the key it was handed")
	}

	w = httptest.NewRecorder()
	api.ServeHTTP(w, settingsRequest(t, http.MethodGet, ""))
	if strings.Contains(w.Body.String(), "sk-secret") {
		t.Errorf("GET body carries a key the plugin was told to keep no copy of")
	}
	set, _ = settingIn(decodedSettings(t, w.Body.String()), "developer", settings.FieldLLMCustomKey)
	if set.Value != "" || set.Dirty {
		t.Errorf("GET: value = %q, dirty = %v, want empty and unedited", set.Value, set.Dirty)
	}
}

// TestSettingsPUTRefusesAKeyForARoleWithNoAgent is the per-field refusal: the
// agent is the key's only home, so a key for a role nobody installed has
// nowhere to go — but that is one field's problem, not the batch's. The rest of
// the batch saves and reconciles as it always did; the key comes back named,
// with the wizard as the way out. (Refusing the whole batch is what made a
// Settings page unusable before the first install.)
func TestSettingsPUTRefusesAKeyForARoleWithNoAgent(t *testing.T) {
	s, err := settings.Open(t.TempDir())
	if err != nil {
		t.Fatalf("settings.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	rec := &recordingPass{}
	api := NewSettings(&hub.Fake{}, s, rec, installsOf(string(setup.RoleDeveloper)))

	// The refused role runs on the provider the key belongs to, so the only
	// reason to refuse it is the one this test is about: no agent installed.
	if err := s.Save(context.Background(), []settings.Edit{
		{Role: "reviewer", Field: settings.FieldLLMProvider, Value: setup.LLMProviderCustom},
		{Role: "reviewer", Field: settings.FieldLLMCustomUrl, Value: "http://gw.local/v1"},
	}); err != nil {
		t.Fatalf("provider Save: %v", err)
	}

	w := httptest.NewRecorder()
	api.ServeHTTP(w, settingsRequest(t, http.MethodPut, `{"settings":[
		{"role":"reviewer","field":"llmCustomKey","value":"sk-secret"},
		{"role":"reviewer","field":"llmModel","value":"gpt-x"}]}`))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200 — the batch saved, the key did not (body %s)", w.Code, w.Body)
	}
	fields := decodedFields(t, w.Body.String())
	if len(fields) != 1 || fields[0].Field != settings.FieldLLMCustomKey || fields[0].Role != "reviewer" {
		t.Fatalf("refused fields = %+v, want one naming reviewer.llmCustomKey", fields)
	}
	if !strings.Contains(fields[0].Error, "install") {
		t.Errorf("refusal = %q, want it to name installing the role as the way out", fields[0].Error)
	}
	if strings.Contains(w.Body.String(), "sk-secret") {
		t.Errorf("the refusal echoes the key")
	}
	if len(rec.pushes) != 0 {
		t.Errorf("pushes = %v, want none — the key was refused", rec.pushes)
	}

	// The batch's other field saved: the refusal is the key's, not the PUT's.
	if got, err := s.Effective(context.Background(), setup.RoleReviewer, settings.FieldLLMModel); err != nil || got != "gpt-x" {
		t.Errorf("reviewer.llmModel = %q (%v), want the batch's other edit to have saved", got, err)
	}
}

// TestSettingsPUTRefusesAKeyForAProviderTheRoleDoesNotRun keeps a key from
// being saved into nothing: an agent on ollama-cloud has no custom-provider
// block to hold a custom key, and a save that quietly dropped the key would
// read as a save that stored one. Naming the provider the role runs on is the
// fix the admin can act on; moving the role onto that provider in the same
// batch is accepted, because the check reads the settings as they now are.
func TestSettingsPUTRefusesAKeyForAProviderTheRoleDoesNotRun(t *testing.T) {
	rec := &recordingPass{}
	api, _ := newSettingsAPIWithPass(t, rec)

	w := httptest.NewRecorder()
	api.ServeHTTP(w, settingsRequest(t, http.MethodPut,
		`{"settings":[{"role":"developer","field":"llmCustomKey","value":"sk-secret"}]}`))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200 — the refusal is a field answer, not a bad batch (body %s)", w.Code, w.Body)
	}
	fields := decodedFields(t, w.Body.String())
	if len(fields) != 1 || fields[0].Field != settings.FieldLLMCustomKey {
		t.Fatalf("fields = %+v, want one naming llmCustomKey", fields)
	}
	if !strings.Contains(fields[0].Error, settings.DefaultLLMProvider) {
		t.Errorf("refusal = %q, want it to name the provider the role runs on", fields[0].Error)
	}
	if len(rec.pushes) != 0 {
		t.Errorf("pushes = %v, want none", rec.pushes)
	}

	// Same batch, provider moved too: the key now has an agent to hold it.
	rec2 := &recordingPass{}
	api2, _ := newSettingsAPIWithPass(t, rec2)
	w = httptest.NewRecorder()
	api2.ServeHTTP(w, settingsRequest(t, http.MethodPut, `{"settings":[
		{"role":"developer","field":"llmProvider","value":"custom"},
		{"role":"developer","field":"llmCustomUrl","value":"http://gw.local/v1"},
		{"role":"developer","field":"llmCustomKey","value":"sk-secret"}]}`))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT with the provider: status = %d, want 200 (body %s)", w.Code, w.Body)
	}
	if len(rec2.pushes) != 1 || rec2.pushes[0] != "developer/custom/sk-secret" {
		t.Errorf("pushes = %v, want the key written onto developer's agents", rec2.pushes)
	}
}

// TestSettingsPUTReportsAKeyTheHubRefused is the difference between a failing
// pass and a failing key: a pass that fails is logged, because the settings are
// the PUT's promise and the next pass retries. A key that did not land has no
// retry — the plugin kept no copy to retry from — so the field answers.
func TestSettingsPUTReportsAKeyTheHubRefused(t *testing.T) {
	rec := &recordingPass{pushErr: errors.New("hub refused the write")}
	api, _ := newSettingsAPIWithPass(t, rec)

	w := httptest.NewRecorder()
	api.ServeHTTP(w, settingsRequest(t, http.MethodPut, `{"settings":[
		{"role":"developer","field":"llmProvider","value":"custom"},
		{"role":"developer","field":"llmCustomUrl","value":"http://gw.local/v1"},
		{"role":"developer","field":"llmCustomKey","value":"sk-secret"}]}`))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200 — the settings did save (body %s)", w.Code, w.Body)
	}
	fields := decodedFields(t, w.Body.String())
	if len(fields) != 1 || fields[0].Field != settings.FieldLLMCustomKey {
		t.Fatalf("fields = %+v, want one naming llmCustomKey", fields)
	}
	if !strings.Contains(fields[0].Error, "hub refused") {
		t.Errorf("field error = %q, want the hub's reason", fields[0].Error)
	}
}

// TestSettingsPUTRefusesCustomProviderWithoutAURL names the refused field at
// the route, so the console can put the message on the row to fill in.
func TestSettingsPUTRefusesCustomProviderWithoutAURL(t *testing.T) {
	api, _ := newSettingsAPI(t)

	w := httptest.NewRecorder()
	api.ServeHTTP(w, settingsRequest(t, http.MethodPut,
		`{"settings":[{"role":"developer","field":"llmProvider","value":"custom"}]}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PUT status = %d, want 400 (body %s)", w.Code, w.Body)
	}
	var body struct {
		Fields []struct {
			Role  string `json:"role"`
			Field string `json:"field"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body %s)", err, w.Body)
	}
	if len(body.Fields) != 1 || body.Fields[0].Field != settings.FieldLLMProvider {
		t.Errorf("refused fields = %+v, want one naming llmProvider", body.Fields)
	}
}

// TestSettingsPUTReconcilesOnlyWhatTheFieldsTouch is the targeted trigger:
// each saved field asks for a pass over exactly the resources it owns, a
// field that owns no hub resource asks for no pass, and a batch asks for one
// pass over the union.
func TestSettingsPUTReconcilesOnlyWhatTheFieldsTouch(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []reconcile.Scope
	}{
		{"persona text reconciles the personas",
			`{"settings":[{"role":"refiner","field":"personaText","value":"x"}]}`,
			[]reconcile.Scope{reconcile.ScopePersona}},
		{"the llm pair reconciles the agents",
			`{"settings":[{"role":"refiner","field":"llmModel","value":"x"}]}`,
			[]reconcile.Scope{reconcile.ScopeAgent}},
		{"the llm pair reconciles the agents, provider too",
			`{"settings":[{"role":"developer","field":"llmProvider","value":"opencode"}]}`,
			[]reconcile.Scope{reconcile.ScopeAgent}},
		{"the OpenAI auth method reconciles the agents",
			`{"settings":[{"role":"developer","field":"llmOpenAIAuthMethod","value":"ChatGPTSignIn"}]}`,
			[]reconcile.Scope{reconcile.ScopeAgent}},
		{"the MCP pair reconciles the MCP row",
			`{"settings":[{"role":"refiner","field":"mcpUrl","value":"http://x/mcp"}]}`,
			[]reconcile.Scope{reconcile.ScopeMCPServer}},
		{"the token env reconciles the MCP row",
			`{"settings":[{"role":"reviewer","field":"mcpTokenFromEnv","value":"X"}]}`,
			[]reconcile.Scope{reconcile.ScopeMCPServer}},
		{"the skill text reconciles nothing here",
			`{"settings":[{"role":"refiner","field":"skillText","value":` + fmt.Sprintf("%q", editedSkillForTest()) + `}]}`,
			nil},
		{"a batch is one pass over the union",
			`{"settings":[{"role":"refiner","field":"personaText","value":"x"},{"role":"refiner","field":"llmModel","value":"y"}]}`,
			[]reconcile.Scope{reconcile.ScopePersona | reconcile.ScopeAgent}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recordingPass{}
			api, _ := newSettingsAPIWithPass(t, rec)
			if code := putSettings(t, api, tt.body); code != http.StatusOK {
				t.Fatalf("PUT: status %d, want 200", code)
			}
			if len(rec.scopes) != len(tt.want) {
				t.Fatalf("passes: got %v, want %v", rec.scopes, tt.want)
			}
			for i, want := range tt.want {
				if rec.scopes[i] != want {
					t.Errorf("pass %d: scope %d, want %d", i, rec.scopes[i], want)
				}
			}
		})
	}
}

// TestSettingsPUTReconcilesASavedKeyOfEveryProvider keeps the targeted trigger
// honest for the one field that reaches an agent on its own: saving a key of any
// of the four providers runs the agent pass (the custom URL and the rest of the
// agent's fields still ride it) and asks for the key to be written onto the
// role's agents. A batch with no agent installed for the role is refused in the
// key's own field before any of that — see
// TestSettingsPUTRefusesAKeyForARoleWithNoAgent.
func TestSettingsPUTReconcilesASavedKeyOfEveryProvider(t *testing.T) {
	for _, tt := range []struct{ field, provider string }{
		{settings.FieldLLMCustomKey, setup.LLMProviderCustom},
		{settings.FieldLLMOpenAIKey, setup.LLMProviderOpenAI},
		{settings.FieldLLMAnthropicKey, setup.LLMProviderAnthropic},
		{settings.FieldLLMGoogleKey, setup.LLMProviderGoogle},
	} {
		field, provider := tt.field, tt.provider
		t.Run(field, func(t *testing.T) {
			s, err := settings.Open(t.TempDir())
			if err != nil {
				t.Fatalf("settings.Open: %v", err)
			}
			t.Cleanup(func() { s.Close() })
			rec := &recordingPass{}
			api := NewSettings(&hub.Fake{}, s, rec, installsOf(string(setup.RoleDeveloper)))

			// The role runs on the provider whose key is saved, or the route
			// refuses the key as one with no block to hold it — that rule is its
			// own test.
			body := fmt.Sprintf(`{"settings":[{"role":"developer","field":"llmProvider","value":%q},{"role":"developer","field":%q,"value":"sk-test"}]}`, provider, field)
			if provider == setup.LLMProviderCustom {
				body = fmt.Sprintf(`{"settings":[{"role":"developer","field":"llmProvider","value":"custom"},{"role":"developer","field":"llmCustomUrl","value":"http://gw.local/v1"},{"role":"developer","field":%q,"value":"sk-test"}]}`, field)
			}
			if code := putSettings(t, api, body); code != http.StatusOK {
				t.Fatalf("PUT %s: status %d, want 200", field, code)
			}
			if len(rec.scopes) == 0 || rec.scopes[0]&reconcile.ScopeAgent == 0 {
				t.Errorf("saving %s: passes = %v, want an agent pass", field, rec.scopes)
			}
			if len(rec.pushes) != 1 || rec.pushes[0] != "developer/"+provider+"/sk-test" {
				t.Fatalf("saving %s: pushes = %v, want the key written onto the agents", field, rec.pushes)
			}
		})
	}
}

// editedSkillForTest is a skill edit the store accepts: shipped frontmatter,
// changed body.
func editedSkillForTest() string {
	return content.SkillSection(setup.RoleRefiner) + "\n### Edited for the test\n"
}

// TestSettingsPUTSurvivesAFailingPass: the PUT's promise is the save. A pass
// the hub will not serve is logged and the next trigger tries again — it is
// not a reason to tell the admin their save failed.
func TestSettingsPUTSurvivesAFailingPass(t *testing.T) {
	rec := &recordingPass{err: errors.New("hub offline")}
	api, _ := newSettingsAPIWithPass(t, rec)

	if code := putSettings(t, api, `{"settings":[{"role":"refiner","field":"llmModel","value":"gpt-x"}]}`); code != http.StatusOK {
		t.Fatalf("PUT with a failing pass: status %d, want 200", code)
	}

	// The setting really saved.
	w := httptest.NewRecorder()
	api.ServeHTTP(w, settingsRequest(t, http.MethodGet, ""))
	set, _ := settingIn(decodedSettings(t, w.Body.String()), "refiner", settings.FieldLLMModel)
	if !set.Dirty || set.Value != "gpt-x" {
		t.Errorf("save did not land behind the failing pass: %+v", set)
	}
}

// TestSettingsCheckNowRunsAFullPass: the manual trigger is gated like
// everything else here, runs one pass over everything, and answers with the
// summary — both its lists, in the shape the console reads.
func TestSettingsCheckNowRunsAFullPass(t *testing.T) {
	rec := &recordingPass{sum: reconcile.Summary{
		Updated: []reconcile.Change{{Install: "app", Role: "refiner", Kind: "agent", Field: "llm", From: "opencode/gpt-4", To: "custom/model-x"}},
		Refused: []reconcile.Refusal{{Install: "app", Kind: "mcpServer", Field: "forgejo-mcp-app", Error: "hub offline"}},
	}}
	api, r := newSettingsAPIWithPass(t, rec)

	// Gated: no credential is 401, a non-admin is 403.
	req := httptest.NewRequest(http.MethodPost, "/api/settings/reconcile", nil)
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("without credential: %d, want 401", w.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/settings/reconcile", nil)
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, false))
	w = httptest.NewRecorder()
	api.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("as a non-admin: %d, want 403", w.Code)
	}

	// The admin's request runs the full scope and answers with the summary.
	req = settingsRequest(t, http.MethodPost, "")
	req.URL.Path = "/api/settings/reconcile"
	w = httptest.NewRecorder()
	api.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body)
	}
	if len(r.scopes) != 1 || r.scopes[0] != reconcile.ScopeAll {
		t.Fatalf("scopes: got %v, want one ScopeAll", r.scopes)
	}
	var out struct {
		Updated   []reconcile.Change    `json:"updated"`
		Refused   []reconcile.Refusal   `json:"refused"`
		LLMChecks *[]reconcile.LLMCheck `json:"llmChecks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, w.Body)
	}
	if len(out.Updated) != 1 || out.Updated[0].Field != "llm" {
		t.Errorf("updated: %+v", out.Updated)
	}
	if len(out.Refused) != 1 || out.Refused[0].Kind != "mcpServer" {
		t.Errorf("refused: %+v", out.Refused)
	}
	// The check list rides along even when nothing was checked: the console
	// reads one shape, [] rather than null.
	if out.LLMChecks == nil {
		t.Errorf("llmChecks: null, want [] (body %s)", w.Body)
	} else if len(*out.LLMChecks) != 0 {
		t.Errorf("llmChecks: %+v, want empty", *out.LLMChecks)
	}
}

// TestSettingsCheckNowCarriesLLMChecks: the reachability half answers beside the
// drift half. A clean pass over settings that name a provider which cannot serve
// must not read as "nothing to fix": updated/refused keep their meaning, and the
// failing pair arrives in llmChecks with its state and reason.
func TestSettingsCheckNowCarriesLLMChecks(t *testing.T) {
	rec := &recordingPass{checks: []reconcile.LLMCheck{
		{Role: "refiner", Provider: "ollama-cloud", Model: "llama3.3", State: hub.LLMCheckUnreachable, Reason: "provider refused the request"},
		{Role: "developer", Provider: "ollama-cloud", Model: "llama3.3", State: hub.LLMCheckUnreachable, Reason: "provider refused the request"},
		{Role: "reviewer", Provider: "ollama-cloud", Model: "llama3.3", State: hub.LLMCheckOK},
	}}
	api, _ := newSettingsAPIWithPass(t, rec)

	req := settingsRequest(t, http.MethodPost, "")
	req.URL.Path = "/api/settings/reconcile"
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body)
	}
	var out struct {
		Updated   []reconcile.Change   `json:"updated"`
		Refused   []reconcile.Refusal  `json:"refused"`
		LLMChecks []reconcile.LLMCheck `json:"llmChecks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, w.Body)
	}
	if len(out.Updated) != 0 || len(out.Refused) != 0 {
		t.Errorf("drift lists changed: updated %+v, refused %+v", out.Updated, out.Refused)
	}
	if len(out.LLMChecks) != 3 {
		t.Fatalf("llmChecks: %+v, want one per role", out.LLMChecks)
	}
	if out.LLMChecks[0].State != hub.LLMCheckUnreachable || out.LLMChecks[0].Reason != "provider refused the request" {
		t.Errorf("failing pair: %+v", out.LLMChecks[0])
	}
	if out.LLMChecks[2].State != hub.LLMCheckOK {
		t.Errorf("ok pair: %+v", out.LLMChecks[2])
	}
}

// TestSettingsCheckNowFailsWhenTheCheckCannotRun: a summary that silently
// omitted the reachability half would let "nothing to fix" stand for a check
// that was never made — the exact misreport the route exists to end. So a
// CheckLLMs that cannot run fails the answer rather than riding along as empty.
func TestSettingsCheckNowFailsWhenTheCheckCannotRun(t *testing.T) {
	rec := &recordingPass{checkErr: errors.New("settings unreadable")}
	api, _ := newSettingsAPIWithPass(t, rec)

	req := settingsRequest(t, http.MethodPost, "")
	req.URL.Path = "/api/settings/reconcile"
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("status = 200, want a failure (body %s)", w.Body)
	}
}

// TestSettingsCheckNowWithoutAHub: a plugin run without HUB_URL has no engine;
// the route says so rather than answering with an empty success.
func TestSettingsCheckNowWithoutAHub(t *testing.T) {
	api, _ := newSettingsAPI(t)
	req := settingsRequest(t, http.MethodPost, "")
	req.URL.Path = "/api/settings/reconcile"
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

// TestCombinedAPIRoutesSettingsSubpaths: the combined mux matches by pattern,
// and an exact "/api/settings" registration would quietly hand
// /api/settings/reconcile to the work API — a "Check now" that answers 404.
// The route the browser hits is the route that must reach the settings handler.
func TestCombinedAPIRoutesSettingsSubpaths(t *testing.T) {
	hit := func(name string, seen *[]string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*seen = append(*seen, name+":"+r.URL.Path)
		})
	}
	var workSeen, setupSeen, settingsSeen []string
	api := NewAPI(hit("work", &workSeen), hit("setup", &setupSeen), hit("settings", &settingsSeen))

	for _, req := range []struct{ method, path string }{
		{http.MethodGet, "/api/settings"},
		{http.MethodPut, "/api/settings"},
		{http.MethodPost, "/api/settings/reconcile"},
	} {
		w := httptest.NewRecorder()
		api.ServeHTTP(w, httptest.NewRequest(req.method, req.path, nil))
	}
	w := httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/work/anything", nil))

	if len(settingsSeen) != 3 {
		t.Fatalf("settings handler saw %v, want all three settings paths", settingsSeen)
	}
	if len(workSeen) != 1 || workSeen[0] != "work:/api/work/anything" {
		t.Errorf("work handler saw %v", workSeen)
	}
}
