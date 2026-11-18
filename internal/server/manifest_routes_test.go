package server

import (
	"slices"
	"strings"
	"testing"

	"github.com/ainsel-project/ainsel-plugin-forgejo"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/manifest"
)

// servedSkills is the skill tree these manifests are checked against: one
// document, at the path the shipped manifest declares it at.
func servedSkills() map[string]string {
	return map[string]string{"/skills/forgejo/SKILL.md": "# forgejo\n"}
}

// routes builds a manifest that declares the paths New mounts, with one field
// left for a test to break.
func routes(apiBase, mcpPath, uiEntry string) *manifest.Manifest {
	m := &manifest.Manifest{ID: "forgejo", DisplayName: "Forgejo", Version: "0.1.0", PluginAPI: "1.1"}
	if apiBase != "" {
		m.API = &manifest.APISpec{BasePath: apiBase}
	}
	if mcpPath != "" {
		m.MCP = &manifest.MCPSpec{Path: mcpPath}
	}
	if uiEntry != "" {
		m.UI = &manifest.UISpec{Entry: uiEntry}
	}
	return m
}

// TestCheckManifestRoutes pins the startup cross-check: the manifest is what the
// hub proxies by, the mounts are what the binary serves, and a manifest that
// renames a path without the route moving produces a plugin that looks healthy
// and answers nothing. Every mismatch has to say which side to fix.
func TestCheckManifestRoutes(t *testing.T) {
	tests := []struct {
		name       string
		m          *manifest.Manifest
		wantErr    string // substring of the joined error; "" means no error wanted
		wantAbsent string // substring that must NOT appear in the error
	}{
		{
			name: "all three match the mounts",
			m:    routes("/api", "/mcp", "/ui/index.js"),
		},
		{
			name:       "api.basePath renamed",
			m:          routes("/apis", "/mcp", "/ui/index.js"),
			wantErr:    `api.basePath is "/apis"`,
			wantAbsent: "mcp.path",
		},
		{
			name:    "api section missing",
			m:       routes("", "/mcp", "/ui/index.js"),
			wantErr: `api.basePath`,
		},
		{
			name:    "mcp.path renamed",
			m:       routes("/api", "/mcp/v2", "/ui/index.js"),
			wantErr: `mcp.path is "/mcp/v2"`,
		},
		{
			name:    "mcp section missing",
			m:       routes("/api", "", "/ui/index.js"),
			wantErr: `mcp.path`,
		},
		{
			name:    "ui.entry outside the served prefix",
			m:       routes("/api", "/mcp", "/plugins/forgejo/index.js"),
			wantErr: `ui.entry is "/plugins/forgejo/index.js"`,
		},
		{
			name:    "ui.entry is the bare prefix",
			m:       routes("/api", "/mcp", "/ui"),
			wantErr: `ui.entry is "/ui"`,
		},
		{
			name:    "ui section missing",
			m:       routes("/api", "/mcp", ""),
			wantErr: `ui.entry`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckManifestRoutes(tt.m, servedSkills())
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckManifestRoutes = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckManifestRoutes = nil, want an error mentioning %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
			// A mismatch must not drag the other checks' wording along, or the
			// message stops telling the author which line to edit.
			if tt.wantAbsent != "" && strings.Contains(err.Error(), tt.wantAbsent) {
				t.Errorf("error = %v, should not mention %q", err, tt.wantAbsent)
			}
		})
	}

	t.Run("two mismatches are both reported", func(t *testing.T) {
		// Both problems at once, so a copier who renamed two paths fixes both on
		// the first run instead of one per restart.
		err := CheckManifestRoutes(routes("/apis", "/mcp/v2", "/ui/index.js"), servedSkills())
		if err == nil {
			t.Fatal("CheckManifestRoutes = nil, want both mismatches")
		}
		for _, want := range []string{`api.basePath is "/apis"`, `mcp.path is "/mcp/v2"`} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %v, want it to mention %q", err, want)
			}
		}
	})
}

// TestCheckManifestRoutesShippedManifest runs the check against the manifest this
// plugin ships and embeds: it is the manifest the server serves, so it has to pass
// the check that same server applies to a plugin's manifest at register time.
func TestCheckManifestRoutesShippedManifest(t *testing.T) {
	m, err := manifest.Parse(forgejo.ManifestJSON)
	if err != nil {
		t.Fatalf("parse embedded manifest: %v", err)
	}
	if err := CheckManifestRoutes(m, forgejo.Skills()); err != nil {
		t.Errorf("the shipped manifest fails its own route check: %v", err)
	}
}

// skill declares one skill the way a manifest does, for the cases below.
func skill(path string) []manifest.SkillSpec {
	return []manifest.SkillSpec{{Name: "forgejo", Description: "A skill", Path: path}}
}

// TestCheckManifestRoutesSkills pins the other half of "the manifest is what the
// hub comes to read": a declared skill is a document the hub fetches off the pod
// and stores as a platform skill, so a manifest that names a path the binary
// serves nothing at leaves a skill row holding nothing, silently. The check has to
// say which side to fix.
func TestCheckManifestRoutesSkills(t *testing.T) {
	tests := []struct {
		name    string
		skills  []manifest.SkillSpec
		served  map[string]string
		wantErr string // substring of the joined error; "" means no error wanted
	}{
		{
			// A plugin with no skill is a plugin with nothing to fetch.
			name: "no skill declared, none served",
		},
		{
			name:   "declared skill is served",
			skills: skill("/skills/forgejo/SKILL.md"),
			served: servedSkills(),
		},
		{
			name:    "declared skill is not served",
			skills:  skill("/skills/forgejo/GUIDE.md"),
			served:  servedSkills(),
			wantErr: `skills[0].path is "/skills/forgejo/GUIDE.md", but no document is served there`,
		},
		{
			name:    "declared skill is served an empty body",
			skills:  skill("/skills/forgejo/SKILL.md"),
			served:  map[string]string{"/skills/forgejo/SKILL.md": ""},
			wantErr: `skills[0].path is "/skills/forgejo/SKILL.md", but no document is served there`,
		},
		{
			name:    "skill declared but the server is built with none",
			skills:  skill("/skills/forgejo/SKILL.md"),
			wantErr: "no skill documents",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := routes("/api", "/mcp", "/ui/index.js")
			m.Skills = tt.skills

			err := CheckManifestRoutes(m, tt.served)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckManifestRoutes = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckManifestRoutes = nil, want an error mentioning %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// TestShippedManifestDeclaresTheForgejoSkill pins what the hub registers from the
// shipped manifest: both tools read-only and for everyone, and the one skill
// document the plugin ships, named the way the hub names a plugin skill row.
func TestShippedManifestDeclaresTheForgejoSkill(t *testing.T) {
	m, err := manifest.Parse(forgejo.ManifestJSON)
	if err != nil {
		t.Fatalf("parse embedded manifest: %v", err)
	}

	// Both tools are reads a caller in front of a browser and an agent run both
	// need, so both are declared "both" rather than left to the default: a tool
	// that means "everyone" and a tool nobody thought about should not look the
	// same in the manifest.
	for _, tool := range []string{"list_work", "get_work_item"} {
		if got := m.ToolAudience(tool); got != manifest.AudienceBoth {
			t.Errorf("ToolAudience(%q) = %q, want %q", tool, got, manifest.AudienceBoth)
		}
	}

	// The setup trio is operator-facing: it is the console's wizard reached
	// without the browser, and an agent run has no business planning or applying
	// an install. "operator" is what keeps them out of an agent's tool set.
	for _, tool := range []string{"setup_options", "setup_plan", "setup_apply"} {
		if got := m.ToolAudience(tool); got != manifest.AudienceOperator {
			t.Errorf("ToolAudience(%q) = %q, want %q", tool, got, manifest.AudienceOperator)
		}
	}

	if len(m.Skills) != 1 {
		t.Fatalf("manifest declares %d skills, want the one forgejo skill", len(m.Skills))
	}
	want := manifest.SkillSpec{
		Name:        "forgejo",
		Description: "How Forgejo agents hand off work and read what AInsel did",
		Path:        "/skills/forgejo/SKILL.md",
	}
	if m.Skills[0] != want {
		t.Errorf("manifest skills[0] = %+v, want %+v", m.Skills[0], want)
	}
}

// TestShippedManifestIsValid checks the manifest the plugin ships with against
// the platform rules: it must parse, identify itself as the forgejo plugin,
// target a plugin API the host accepts, name the component each of its pages
// renders, fill known slots, and ask for the permissions it uses.
func TestShippedManifestIsValid(t *testing.T) {
	m, err := manifest.Parse(forgejo.ManifestJSON)
	if err != nil {
		t.Fatalf("Parse(shipped manifest) = error, want none: %v", err)
	}
	if m.ID != "forgejo" {
		t.Errorf("manifest ID = %q, want %q", m.ID, "forgejo")
	}
	ok, err := manifest.Compatible(m.PluginAPI, manifest.HostAPIVersion)
	if err != nil {
		t.Fatalf("Compatible(%q, %q) = error, want none: %v", m.PluginAPI, manifest.HostAPIVersion, err)
	}
	if !ok {
		t.Errorf("Compatible(%q, %q) = false, want true", m.PluginAPI, manifest.HostAPIVersion)
	}
	if m.UI == nil {
		t.Fatal("manifest has no ui spec, want pages and slots to be set")
	}

	// The console imports the bundle once and resolves each page by the name in
	// ui.pages[].component, so every page has to name an export the bundle has.
	// Setup is the admin's page: it is the one that reaches other people's
	// credentials, so it is the one the console keeps from everyone else.
	wantPages := []struct {
		path       string
		component  string
		visibility string
	}{
		{"/now", "LivePage", ""},
		{"/work", "WorkPage", ""},
		{"/setup", "SetupPage", manifest.PageVisibilityAdmin},
	}
	byPath := make(map[string]manifest.Page, len(m.UI.Pages))
	for _, page := range m.UI.Pages {
		byPath[page.Path] = page
	}
	for _, want := range wantPages {
		page, ok := byPath[want.path]
		if !ok {
			t.Fatalf("manifest has no page at path %q: %+v", want.path, m.UI.Pages)
		}
		if page.Component != want.component {
			t.Errorf("page %q component = %q, want %q", want.path, page.Component, want.component)
		}
		if page.Visibility != want.visibility {
			t.Errorf("page %q visibility = %q, want %q", want.path, page.Visibility, want.visibility)
		}
	}

	// Only the four slot ids AInsel accepts, and this plugin fills two of them.
	for slot, component := range map[string]string{
		"invocation.panel": "ForgejoInvocationPanel",
		"agent.panel":      "ForgejoAgentPanel",
	} {
		if got := m.UI.Slots[slot]; got != component {
			t.Errorf(`ui.slots[%q] = %q, want %q`, slot, got, component)
		}
	}
	if len(m.UI.Slots) != 2 {
		t.Errorf("manifest fills %d slots, want the invocation and the agent panel", len(m.UI.Slots))
	}

	// What the plugin asks for is the whole ask: an entry the platform does not
	// know is refused by Parse, and one it knows but this plugin never calls is a
	// grant no admin needs to weigh.
	wantPermissions := []string{
		"invocations:read",
		"observability:read",
		"agents:read",
		"agents:write",
		"agent-images:read",
		"agent-images:write",
		"personas:read",
		"personas:write",
		"connectors:read",
		"connectors:write",
		"triggers:read",
		"triggers:write",
		"mcp-servers:read",
		"mcp-servers:write",
	}
	got := append([]string(nil), m.Permissions...)
	slices.Sort(got)
	slices.Sort(wantPermissions)
	if !slices.Equal(got, wantPermissions) {
		t.Errorf("manifest permissions = %v, want %v", got, wantPermissions)
	}
}

// TestShippedManifestRejectsUnknownField checks that the shipped manifest is
// still parsed strictly: a key the schema does not know about must be rejected,
// not silently dropped.
func TestShippedManifestRejectsUnknownField(t *testing.T) {
	// Take the shipped manifest and append an "extra" member before the
	// closing brace.
	body := strings.TrimSpace(string(forgejo.ManifestJSON))
	if !strings.HasSuffix(body, "}") {
		t.Fatalf("shipped manifest does not end with '}': %q", body)
	}
	data := strings.TrimSuffix(body, "}") + `,"extra":1}`

	_, err := manifest.Parse([]byte(data))
	if err == nil {
		t.Fatal("Parse(manifest with unknown field) = nil error, want rejection")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("Parse error = %v, want it to contain %q", err, "unknown field")
	}
}
