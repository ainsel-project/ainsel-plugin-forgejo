package server

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/manifest"
)

// The paths New mounts, written down a second time for the startup cross-check
// below. ServeMux patterns are read as literals, so they stay
// literal in New; these are the values a manifest has to agree with, and
// TestServerRoutes walks the same paths against the real mux.
const (
	// apiBasePath is the prefix the API routes live under. The routes themselves
	// come with the backend; this is the prefix they and the manifest agree on.
	apiBasePath = "/api"

	// mcpPath is where the MCP endpoint is mounted (any method).
	mcpPath = "/mcp"

	// uiPrefix is the prefix the UI bundle is served under (GET /ui/), so the
	// prefix a manifest's ui.entry has to sit under.
	uiPrefix = "/ui/"
)

// CheckManifestRoutes reports whether a parsed manifest declares the paths New
// actually serves. Call it at startup, before the server starts, with the same skill
// documents New is built with (the Config.Skills value; nil when the plugin serves
// none).
//
// The hub proxies a plugin by what its manifest says, not by what its binary
// serves. Rename api.basePath, or mcp.path, or move ui.entry out from under
// /ui/ without moving the matching route, and the plugin still starts, still
// goes Ready and still answers on the old path — which nothing asks for any
// more. That is a dead API behind a healthy-looking pod, or a skill row whose body is
// whatever was fetched last; this check turns it
// into a startup error that names both sides of the mismatch.
//
// A plugin that genuinely wants a different path changes both sides: the
// manifest and the patterns in New. All problems are joined into one error, so
// a copier who renamed three paths fixes them in one edit instead of one per
// restart.
func CheckManifestRoutes(m *manifest.Manifest, skills map[string]string) error {
	var errs []error

	switch {
	case m.API == nil:
		errs = append(errs, fmt.Errorf(
			"manifest declares no api.basePath, but the server serves its API under %q: declare api.basePath %q in ainsel-plugin.json, or remove the API routes from internal/server/server.go",
			apiBasePath, apiBasePath))
	case m.API.BasePath != apiBasePath:
		errs = append(errs, fmt.Errorf(
			"manifest api.basePath is %q, but the server serves its API under %q: set api.basePath to %q in ainsel-plugin.json, or move the API routes in internal/server/server.go",
			m.API.BasePath, apiBasePath, apiBasePath))
	}

	switch {
	case m.MCP == nil:
		errs = append(errs, fmt.Errorf(
			"manifest declares no mcp.path, but the server mounts the MCP endpoint at %q: declare mcp.path %q in ainsel-plugin.json, or stop mounting it in internal/server/server.go and cmd/plugin/main.go",
			mcpPath, mcpPath))
	case m.MCP.Path != mcpPath:
		errs = append(errs, fmt.Errorf(
			"manifest mcp.path is %q, but the server mounts the MCP endpoint at %q: set mcp.path to %q in ainsel-plugin.json, or move the mount in internal/server/server.go",
			m.MCP.Path, mcpPath, mcpPath))
	}

	switch {
	case m.UI == nil:
		errs = append(errs, fmt.Errorf(
			"manifest declares no ui.entry, but the server serves the embedded UI bundle under %q: declare ui.entry \"/ui/index.js\" in ainsel-plugin.json, or serve no bundle at all",
			uiPrefix))
	case !strings.HasPrefix(m.UI.Entry, uiPrefix):
		errs = append(errs, fmt.Errorf(
			"manifest ui.entry is %q, but the server serves the UI bundle under %q: set ui.entry to a path under %q (e.g. \"/ui/index.js\"), or move the mount in internal/server/server.go",
			m.UI.Entry, uiPrefix, uiPrefix))
	}

	// A declared skill is a document the hub comes to read and stores as a platform
	// skill, so it has to be a document this binary serves, not merely a path that
	// looks like one.
	for i, sk := range m.Skills {
		field := fmt.Sprintf("skills[%d].path", i)
		if skills == nil {
			errs = append(errs, fmt.Errorf(
				"manifest declares %s %q, but the server is built with no skill documents: pass the shipped skills as Config.Skills (see Skills in assets.go), or remove skills from ainsel-plugin.json",
				field, sk.Path))
			continue
		}
		if skills[sk.Path] == "" {
			errs = append(errs, fmt.Errorf(
				"manifest %s is %q, but no document is served there: serve that exact path in the map passed as Config.Skills (see Skills in assets.go), or point %s at a document the plugin serves",
				field, sk.Path, field))
		}
	}

	return errors.Join(errs...)
}
