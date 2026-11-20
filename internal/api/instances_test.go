package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/settings"
)

// instancesRequest builds an instances request as the console sends it: a
// credential, an admin caller, and (for PUT) a JSON body.
func instancesRequest(t *testing.T, method, body string) *http.Request {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, "/api/instances", nil)
	} else {
		req = httptest.NewRequest(method, "/api/instances", strings.NewReader(body))
	}
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	return req
}

// decodedInstances reads an instancesResponse out of an answer.
func decodedInstances(t *testing.T, body string) []settings.Instance {
	t.Helper()
	var out instancesResponse
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode instances response: %v\nbody: %s", err, body)
	}
	return out.Instances
}

// TestInstancesRoutesGateTheCaller checks both instance routes refuse a request
// with no credential (401) and a caller who is not an admin (403): the list
// says which hosts this plugin may be installed against, so it is admin-only.
func TestInstancesRoutesGateTheCaller(t *testing.T) {
	api, _ := newSettingsAPI(t)

	for _, tt := range []struct{ method, body string }{
		{http.MethodGet, ""},
		{http.MethodPut, `{"instances":[{"name":"A","baseUrl":"https://a.example.com"}]}`},
	} {
		newReq := func() *http.Request {
			if tt.body == "" {
				return httptest.NewRequest(tt.method, "/api/instances", nil)
			}
			return httptest.NewRequest(tt.method, "/api/instances", strings.NewReader(tt.body))
		}

		req := newReq()
		w := httptest.NewRecorder()
		api.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s without credential: %d, want 401", tt.method, w.Code)
		}

		req = newReq()
		req.Header.Set("X-Ainsel-Credential", "test-cred")
		req.Header.Set("X-Ainsel-Context", callerContextHeader(t, false))
		w = httptest.NewRecorder()
		api.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s as a non-admin: %d, want 403", tt.method, w.Code)
		}
	}
}

// TestInstancesGETAnswersEmptyList checks the nothing-configured state: 200 with
// an empty, non-null list — the shape the wizard reads as "link the admin to
// Settings".
func TestInstancesGETAnswersEmptyList(t *testing.T) {
	api, _ := newSettingsAPI(t)

	w := httptest.NewRecorder()
	api.ServeHTTP(w, instancesRequest(t, http.MethodGet, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/instances: %d, want 200", w.Code)
	}
	if body := strings.TrimSpace(w.Body.String()); !strings.Contains(body, `"instances":[]`) {
		t.Fatalf("body = %s, want an empty instances array, not null", body)
	}
}

// TestInstancesPUTReplacesAndAnswersTheStoredList checks the write path: the
// answer is the stored list with the addresses normalized, and a second PUT is
// the whole list rather than an append.
func TestInstancesPUTReplacesAndAnswersTheStoredList(t *testing.T) {
	api, _ := newSettingsAPI(t)

	w := httptest.NewRecorder()
	api.ServeHTTP(w, instancesRequest(t, http.MethodPut,
		`{"instances":[{"name":"Primary","baseUrl":"https://a.example.com/"},{"name":"Lab","baseUrl":"https://b.example.com"}]}`))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT: %d, want 200: %s", w.Code, w.Body.String())
	}
	out := decodedInstances(t, w.Body.String())
	if len(out) != 2 {
		t.Fatalf("got %d instances, want 2", len(out))
	}
	if out[0].BaseURL != "https://a.example.com" {
		t.Errorf("first baseUrl = %q, want the trailing slash stored off", out[0].BaseURL)
	}

	w = httptest.NewRecorder()
	api.ServeHTTP(w, instancesRequest(t, http.MethodPut,
		`{"instances":[{"name":"Only","baseUrl":"https://c.example.com"}]}`))
	if w.Code != http.StatusOK {
		t.Fatalf("second PUT: %d, want 200", w.Code)
	}
	out = decodedInstances(t, w.Body.String())
	if len(out) != 1 || out[0].Name != "Only" {
		t.Fatalf("got %+v, want the list replaced by the one new instance", out)
	}

	// The read agrees with the write's answer.
	w = httptest.NewRecorder()
	api.ServeHTTP(w, instancesRequest(t, http.MethodGet, ""))
	if got := decodedInstances(t, w.Body.String()); len(got) != 1 || got[0].Name != "Only" {
		t.Fatalf("GET after PUT = %+v", got)
	}
}

// TestInstancesPUTRefusesAMissingInstancesKey checks the truncated-request case:
// a body without the "instances" key is not the empty list. Treating it as one
// would wipe the registry and mark it seeded, so the env seed would never return
// — the refusal changes nothing, while an explicit [] still clears the list.
func TestInstancesPUTRefusesAMissingInstancesKey(t *testing.T) {
	api, _ := newSettingsAPI(t)

	w := httptest.NewRecorder()
	api.ServeHTTP(w, instancesRequest(t, http.MethodPut,
		`{"instances":[{"name":"Primary","baseUrl":"https://a.example.com"}]}`))
	if w.Code != http.StatusOK {
		t.Fatalf("seed PUT: %d, want 200: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	api.ServeHTTP(w, instancesRequest(t, http.MethodPut, `{}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PUT without the instances key: %d, want 400: %s", w.Code, w.Body.String())
	}

	// The stored list survives the refused request.
	w = httptest.NewRecorder()
	api.ServeHTTP(w, instancesRequest(t, http.MethodGet, ""))
	if got := decodedInstances(t, w.Body.String()); len(got) != 1 || got[0].Name != "Primary" {
		t.Fatalf("GET after the refused PUT = %+v, want the list untouched", got)
	}

	// An explicit empty list is still the legal way to clear it.
	w = httptest.NewRecorder()
	api.ServeHTTP(w, instancesRequest(t, http.MethodPut, `{"instances":[]}`))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT with an explicit empty list: %d, want 200: %s", w.Code, w.Body.String())
	}
	if got := decodedInstances(t, w.Body.String()); len(got) != 0 {
		t.Fatalf("explicit clear = %+v, want the empty list", got)
	}
}

// TestInstancesPUTRefusesBadRows checks the field-level answer: a bad row comes
// back as 400 naming the row and field, and the stored list is untouched.
func TestInstancesPUTRefusesBadRows(t *testing.T) {
	api, store := newSettingsAPI(t)

	if err := store.SaveInstances(context.Background(), []settings.Instance{
		{Name: "Kept", BaseURL: "https://kept.example.com"},
	}); err != nil {
		t.Fatalf("SaveInstances: %v", err)
	}

	w := httptest.NewRecorder()
	api.ServeHTTP(w, instancesRequest(t, http.MethodPut,
		`{"instances":[{"name":"","baseUrl":"https://a.example.com"},{"name":"B","baseUrl":"not-a-url"}]}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PUT: %d, want 400: %s", w.Code, w.Body.String())
	}

	var bad struct {
		Error  string `json:"error"`
		Fields []struct {
			Field string `json:"field"`
			Error string `json:"error"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &bad); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(bad.Fields) != 2 {
		t.Fatalf("fields = %+v, want two named rows", bad.Fields)
	}
	if bad.Fields[0].Field != "instances[0].name" || bad.Fields[1].Field != "instances[1].baseUrl" {
		t.Errorf("fields = %+v, want instances[0].name and instances[1].baseUrl", bad.Fields)
	}

	out, err := store.ListInstances(context.Background())
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(out) != 1 || out[0].Name != "Kept" {
		t.Fatalf("a refused write changed the list to %+v", out)
	}
}

// TestInstancesPUTRejectsMalformedJSON checks the cheap refusal: a body that is
// not JSON is a 400, not a 500.
func TestInstancesPUTRejectsMalformedJSON(t *testing.T) {
	api, _ := newSettingsAPI(t)

	w := httptest.NewRecorder()
	api.ServeHTTP(w, instancesRequest(t, http.MethodPut, `{not json`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PUT with a broken body: %d, want 400", w.Code)
	}
}

// TestNewAPIRoutesInstancesToSettings checks the mount: /api/instances reaches
// the settings handler, and the neighbouring paths still reach their own.
func TestNewAPIRoutesInstancesToSettings(t *testing.T) {
	s, err := settings.Open(t.TempDir())
	if err != nil {
		t.Fatalf("settings.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.SaveInstances(context.Background(), []settings.Instance{
		{Name: "Mounted", BaseURL: "https://mounted.example.com"},
	}); err != nil {
		t.Fatalf("SaveInstances: %v", err)
	}

	api := NewAPI(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "work", http.StatusTeapot) }),
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "setup", http.StatusTeapot) }),
		NewSettings(&hub.Fake{}, s, nil, nil),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/instances", nil)
	req.Header.Set("X-Ainsel-Credential", "test-cred")
	req.Header.Set("X-Ainsel-Context", callerContextHeader(t, true))
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/instances through NewAPI: %d, want 200 (body %q)", w.Code, w.Body.String())
	}
	if got := decodedInstances(t, w.Body.String()); len(got) != 1 || got[0].Name != "Mounted" {
		t.Fatalf("got %+v, want the settings handler's list", got)
	}

	// A path that belongs to work still reaches work, so the new mount did not
	// swallow the catch-all.
	w = httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/work", nil))
	if w.Code != http.StatusTeapot {
		t.Fatalf("GET /api/work: %d, want the work handler", w.Code)
	}
}
