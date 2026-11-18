package server

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ainsel-project/ainsel-plugin-forgejo"
	"github.com/ainsel-project/ainsel-plugin-forgejo/content"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/api"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/manifest"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/settings"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
	pluginskill "github.com/ainsel-project/ainsel-plugin-forgejo/internal/skill"
)

// testUI is a UI bundle with two assets — one at the root and one in a
// subdirectory — so the /ui/ routes can be checked without a build step.
func testUI() fs.FS {
	return fstest.MapFS{
		"index.js":         &fstest.MapFile{Data: []byte("export const NowPage = 1")},
		"assets/index.css": &fstest.MapFile{Data: []byte(":root{}")},
	}
}

// TestServerRoutes walks every route the plugin serves, including the ones a
// stranger will probe: a directory must not be listed, and a request aimed at
// the manifest through /ui/ must not end up serving it.
func TestServerRoutes(t *testing.T) {
	h := New(Config{
		ManifestJSON: forgejo.ManifestJSON,
		UI:           testUI(),
		Version:      "0.1.0",
	})

	tests := []struct {
		name string
		// Fields with a want* of "" leave that part unchecked.
		method     string
		target     string
		wantStatus int
		wantBody   string // exact body, for the bytes served as-is
		wantType   string // Content-Type prefix
	}{
		{
			name:       "manifest is served verbatim",
			method:     http.MethodGet,
			target:     "/ainsel-plugin.json",
			wantStatus: http.StatusOK,
			wantBody:   string(forgejo.ManifestJSON),
			wantType:   "application/json",
		},
		{
			name:       "manifest is not served for other methods",
			method:     http.MethodPost,
			target:     "/ainsel-plugin.json",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "health check",
			method:     http.MethodGet,
			target:     "/healthz",
			wantStatus: http.StatusOK,
			wantBody:   "ok",
		},
		{
			name:       "api path with no route yet",
			method:     http.MethodGet,
			target:     "/api/work",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "ui asset",
			method:     http.MethodGet,
			target:     "/ui/index.js",
			wantStatus: http.StatusOK,
			wantBody:   "export const NowPage = 1",
			wantType:   "text/javascript",
		},
		{
			name:       "nested ui asset",
			method:     http.MethodGet,
			target:     "/ui/assets/index.css",
			wantStatus: http.StatusOK,
			wantBody:   ":root{}",
			wantType:   "text/css",
		},
		{
			name:       "missing ui asset",
			method:     http.MethodGet,
			target:     "/ui/missing.js",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "ui root is not listed",
			method:     http.MethodGet,
			target:     "/ui/",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "ui subdirectory is not listed",
			method:     http.MethodGet,
			target:     "/ui/assets/",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "ui cannot serve the manifest by traversal",
			method:     http.MethodGet,
			target:     "/ui/../ainsel-plugin.json",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "unknown path",
			method:     http.MethodGet,
			target:     "/nope",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "mcp without a handler",
			method:     http.MethodPost,
			target:     "/mcp",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "skill with none served",
			method:     http.MethodGet,
			target:     "/skills/forgejo/SKILL.md",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.target, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			res := rec.Result()
			if res.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %q, location %q)",
					res.StatusCode, tt.wantStatus, rec.Body.String(), res.Header.Get("Location"))
			}
			if tt.wantBody != "" && rec.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.wantBody)
			}
			if tt.wantType != "" && !strings.HasPrefix(res.Header.Get("Content-Type"), tt.wantType) {
				t.Errorf("Content-Type = %q, want prefix %q", res.Header.Get("Content-Type"), tt.wantType)
			}
		})
	}
}

// TestServerMCPRoutesToHandler checks the other side of the /mcp route: once a
// handler is configured it receives the request, whatever the method — the MCP
// streamable-HTTP transport uses GET, POST and DELETE on one path.
func TestServerMCPRoutesToHandler(t *testing.T) {
	reached := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	h := New(Config{
		ManifestJSON: forgejo.ManifestJSON,
		UI:           testUI(),
		MCP:          reached,
		Version:      "0.1.0",
	})

	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/mcp", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Result().StatusCode != http.StatusAccepted {
				t.Fatalf("status = %d, want %d (body %q)", rec.Result().StatusCode, http.StatusAccepted, rec.Body.String())
			}
		})
	}
}

// TestServerRoutesToAPI checks the other side of the /api mount: once an API handler is
// configured it receives the request at the path it was asked for — the API's own mux
// declares full "/api/…" paths, so the mount must pass the path through, not strip it.
func TestServerRoutesToAPI(t *testing.T) {
	reached := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/work" {
			t.Errorf("handler saw path %q, want the full path %q", r.URL.Path, "/api/work")
		}
		w.WriteHeader(http.StatusAccepted)
	})
	h := New(Config{
		ManifestJSON: forgejo.ManifestJSON,
		Version:      "0.1.0",
		API:          reached,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/work", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Result().StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want %d (body %q)", rec.Result().StatusCode, http.StatusAccepted, rec.Body.String())
	}
}

// TestServerServesShippedSkill checks the skill document this plugin ships from the
// path the shipped manifest names, over the server the binary actually runs: the hub
// fetches that path off the pod and stores the body as a platform skill, so a route
// that answers 404 leaves a skill row holding nothing and a route that answers as
// sniffed text/plain stores the wrong kind of document.
func TestServerServesShippedSkill(t *testing.T) {
	skills := forgejo.Skills()
	h := New(Config{
		ManifestJSON: forgejo.ManifestJSON,
		Skills:       skills,
		Version:      "0.1.0",
	})

	m, err := manifest.Parse(forgejo.ManifestJSON)
	if err != nil {
		t.Fatalf("parse embedded manifest: %v", err)
	}
	if len(m.Skills) != 1 {
		t.Fatalf("manifest declares %d skills, want the one forgejo skill", len(m.Skills))
	}
	path := m.Skills[0].Path
	if skills[path] == "" {
		t.Fatalf("no document is served at the declared path %q", path)
	}

	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	res := rec.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status = %d, want %d", path, res.StatusCode, http.StatusOK)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type = %q, want text/markdown", ct)
	}
	body := rec.Body.String()
	if body != skills[path] {
		t.Errorf("body is %d bytes, want the %d bytes of the shipped skill", len(body), len(skills[path]))
	}
	// The hub reads at most 64 KiB of a plugin's skill document and refuses the rest,
	// so a longer body is a skill that never lands at all.
	if len(body) > 64<<10 {
		t.Errorf("skill body is %d bytes, over the hub's 64 KiB cap", len(body))
	}

	// Nothing above the document is listable: the plugin ships one skill, and a
	// directory that answers with a listing advertises what the bundle holds.
	for _, target := range []string{"/skills/", "/skills/forgejo/", "/skills/forgejo/other.md"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Result().StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want %d", target, rec.Result().StatusCode, http.StatusNotFound)
		}
	}
}

// TestServerWithoutUI checks that a server built without a UI bundle still
// serves the manifest and health check instead of panicking on a nil fs.FS.
func TestServerWithoutUI(t *testing.T) {
	h := New(Config{ManifestJSON: forgejo.ManifestJSON, Version: "0.1.0"})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Result().StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Result().StatusCode, http.StatusOK)
	}

	req = httptest.NewRequest(http.MethodGet, "/ui/index.js", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Result().StatusCode != http.StatusNotFound {
		t.Errorf("status for /ui/index.js = %d, want %d", rec.Result().StatusCode, http.StatusNotFound)
	}
}

// TestServerSetupAPIRequiresCredential checks that the setup API endpoint
// returns 401 when accessed without the X-Ainsel-Credential header.
func TestServerSetupAPIRequiresCredential(t *testing.T) {
	setupAPI := api.NewSetup(nil, nil, nil, nil, api.SetupConfig{
		ForgejoBaseURL: "https://git.example.test",
	})
	h := New(Config{
		ManifestJSON: forgejo.ManifestJSON,
		Version:      "0.1.0",
		API:          setupAPI,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/setup/installs", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Result().StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (body %q)", rec.Result().StatusCode, http.StatusUnauthorized, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "missing credential") {
		t.Errorf("body does not mention missing credential: %q", rec.Body.String())
	}
}

// TestAPIRoutingByPrefix checks that the combined API handler correctly routes
// /api/setup/* requests to the setup handler, /api/settings to the settings
// handler, and other /api/* requests to the work handler.
func TestAPIRoutingByPrefix(t *testing.T) {
	// Create fake work and setup handlers that just echo which one handled the request
	workAPI := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/setup/") {
			t.Errorf("work handler received /api/setup/* request: %s", r.URL.Path)
			http.Error(w, "work handler should not handle setup routes", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"work":"ok"}`))
	})

	setupAPI := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/setup/") {
			t.Errorf("setup handler received non-setup request: %s", r.URL.Path)
			http.Error(w, "setup handler should only handle /api/setup/* routes", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"setup":"ok"}`))
	})

	settingsAPI := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/settings" {
			t.Errorf("settings handler received non-settings request: %s", r.URL.Path)
			http.Error(w, "settings handler should only handle /api/settings", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"settings":"ok"}`))
	})

	combinedAPI := api.NewAPI(workAPI, setupAPI, settingsAPI)
	h := New(Config{
		ManifestJSON: forgejo.ManifestJSON,
		Version:      "0.1.0",
		API:          combinedAPI,
	})

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{
			name:       "setup route goes to setup handler",
			path:       "/api/setup/installs",
			wantStatus: http.StatusOK,
			wantBody:   `{"setup":"ok"}`,
		},
		{
			name:       "setup options goes to setup handler",
			path:       "/api/setup/options",
			wantStatus: http.StatusOK,
			wantBody:   `{"setup":"ok"}`,
		},
		{
			name:       "settings route goes to settings handler",
			path:       "/api/settings",
			wantStatus: http.StatusOK,
			wantBody:   `{"settings":"ok"}`,
		},
		{
			name:       "work route goes to work handler",
			path:       "/api/work",
			wantStatus: http.StatusOK,
			wantBody:   `{"work":"ok"}`,
		},
		{
			name:       "now route goes to work handler",
			path:       "/api/now",
			wantStatus: http.StatusOK,
			wantBody:   `{"work":"ok"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Result().StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Result().StatusCode, tt.wantStatus)
			}
			if rec.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

// openSettings opens a settings store in a fresh directory and closes it with the test:
// a plugin pod that has started and never been configured.
func openSettings(t *testing.T) *settings.Store {
	t.Helper()
	s, err := settings.Open(t.TempDir())
	if err != nil {
		t.Fatalf("settings.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// servingSkill builds the server the way a plugin with settings serves its skill: the
// shipped documents mounted at the manifest's paths, each read through the settings on
// the way out.
func servingSkill(t *testing.T) (http.Handler, *settings.Store, string) {
	t.Helper()
	m, err := manifest.Parse([]byte(forgejo.ManifestJSON))
	if err != nil {
		t.Fatalf("manifest.Parse: %v", err)
	}
	if len(m.Skills) != 1 {
		t.Fatalf("manifest declares %d skills, want the one", len(m.Skills))
	}
	s := openSettings(t)
	h := New(Config{
		ManifestJSON: forgejo.ManifestJSON,
		Skills:       forgejo.Skills(),
		SkillText: func(ctx context.Context, at string) (string, error) {
			return pluginskill.Text(ctx, s, at)
		},
		Version: "0.1.0",
	})
	return h, s, m.Skills[0].Path
}

// getSkillBody fetches a served skill body the way the hub's sync pass does.
func getSkillBody(t *testing.T, h http.Handler, at string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, at, nil))
	return rec.Code, rec.Body.String()
}

// skillSection is the shipped skill section of a role with its frontmatter removed — the
// text that has to survive in a document edited somewhere else.
func skillSection(t *testing.T, role setup.Role) string {
	t.Helper()
	_, body, ok := content.SkillFrontmatter(content.SkillSection(role))
	if !ok {
		t.Fatalf("SkillSection(%q) is not a SKILL.md-shaped text", role)
	}
	return body
}

// TestServerServesSkillTextSavedAfterStart is the endpoint half of the fetch model: the
// hub re-fetches every skill it knows on every pass, so the body has to come out of the
// settings per request. A save that lands while the pod runs is in the body the next
// request reads — no restart, no reload, nothing pushed. A body assembled once at start
// would answer the next fetch with what the admin had just replaced.
func TestServerServesSkillTextSavedAfterStart(t *testing.T) {
	h, s, at := servingSkill(t)
	ctx := context.Background()

	// Cold start: nothing in the table, and the route answers with the shipped text —
	// the same body the map alone would have served.
	if code, body := getSkillBody(t, h, at); code != http.StatusOK || body != content.Skill() {
		t.Fatalf("GET %s cold = %d, %d bytes; want 200 and the shipped document", at, code, len(body))
	}

	edit := []settings.Edit{{
		Role:  string(setup.RoleReviewer),
		Field: settings.FieldSkillText,
		Value: content.SkillSection(setup.RoleReviewer) + "\n### Edited while running\n",
	}}
	if err := s.Save(ctx, edit); err != nil {
		t.Fatalf("Save: %v", err)
	}

	code, body := getSkillBody(t, h, at)
	if code != http.StatusOK {
		t.Fatalf("GET %s after a save = %d, want 200", at, code)
	}
	if !strings.Contains(body, "### Edited while running") {
		t.Error("the saved text is not in the body the next fetch reads")
	}
	if !strings.Contains(body, strings.TrimSpace(skillSection(t, setup.RoleRefiner))) {
		t.Error("a save to one role took another role's shipped section out of the document")
	}

	// A reset clears the dirty flag, and the route answers with the shipped section of
	// that role again — the same fallback a cold start answers with.
	resets := []settings.Edit{{Role: string(setup.RoleReviewer), Field: settings.FieldSkillText, Reset: true}}
	if err := s.Save(ctx, resets); err != nil {
		t.Fatalf("reset Save: %v", err)
	}
	if _, body := getSkillBody(t, h, at); body != content.Skill() {
		t.Error("after a reset the route does not serve the shipped document again")
	}

	// Paths outside the manifest's declaration are not served with the resolver in
	// place either: the route set is the map's keys, and these are not among them.
	for _, other := range []string{"/skills/other/SKILL.md", "/skills/forgejo/OTHER.md"} {
		if code, _ := getSkillBody(t, h, other); code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", other, code)
		}
	}
}

// TestServerSkillReadFailureIsNotTheShippedText: a hub fetch that fails keeps the body it
// stored, so a settings read that fails answers as a failure. Serving the shipped document
// here would overwrite what the admin saved with the text they replaced, for as long as
// the read stayed broken.
func TestServerSkillReadFailureIsNotTheShippedText(t *testing.T) {
	h := New(Config{
		ManifestJSON: forgejo.ManifestJSON,
		Skills:       forgejo.Skills(),
		SkillText: func(context.Context, string) (string, error) {
			return "", errors.New("the settings store is gone")
		},
		Version: "0.1.0",
	})

	at := "/skills/forgejo/SKILL.md"
	code, body := getSkillBody(t, h, at)
	if code != http.StatusServiceUnavailable {
		t.Errorf("GET %s with a broken settings read = %d, want 503", at, code)
	}
	if strings.Contains(body, content.Skill()) {
		t.Error("a failed read served the shipped document, which the hub would then store")
	}
}
