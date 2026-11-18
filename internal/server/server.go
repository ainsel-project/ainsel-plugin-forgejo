// Package server wires the plugin's HTTP surface: the manifest it describes
// itself with, the health check the deployment probes, the embedded UI bundle
// the console loads, the backend API, and the MCP endpoint. Everything is
// served from one mux on one port, which is what a plugin container is.
package server

import (
	"context"
	"io/fs"
	"log"
	"net/http"
	"path"
	"slices"
	"strings"
)

// Config is what the server needs to serve. ManifestJSON, UI and Skills are the
// embedded assets from the root package; MCP is the streamable-HTTP handler,
// nil until the plugin has one, in which case /mcp is simply not there.
type Config struct {
	// ManifestJSON is the manifest body served at GET /ainsel-plugin.json,
	// byte for byte.
	ManifestJSON []byte

	// UI is the built UI bundle, served under GET /ui/. Nil serves no UI.
	UI fs.FS

	// Skills are the plugin's skill documents, each served at the path its
	// manifest entry names it by. Nil serves none, which is how a plugin with no
	// skill is built.
	Skills map[string]string

	// SkillText, when set, is asked for the body of a skill path on every request,
	// so a document whose text lives in the plugin's settings is re-read per fetch
	// instead of served from the map above: the hub re-fetches every skill it knows
	// on every sync pass, so an edit saved to the settings is in the body its next
	// pass reads, with no restart and nothing pushed. An error is a read that
	// failed, and the route answers as a failure — a hub fetch that fails keeps the
	// body it stored, where serving the shipped text here would overwrite what the
	// admin saved.
	SkillText func(ctx context.Context, path string) (string, error)

	// MCP is the MCP endpoint, mounted at /mcp for any method. Nil leaves the
	// path a 404.
	MCP http.Handler

	// Version is the plugin's version, reported by the API. It comes from the
	// manifest, so the two cannot disagree.
	Version string

	// API is the plugin's backend handlers, mounted under apiBasePath at the full
	// paths they declare. Nil leaves /api/* a 404, which is how a plugin with no
	// API wired answers.
	API http.Handler
}

// New builds the handler for every route the plugin serves. Unknown paths are
// 404 — a plugin owns its namespace and has nothing to fall back on.
func New(cfg Config) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /ainsel-plugin.json", func(w http.ResponseWriter, r *http.Request) {
		// The hub compares and stores this body, so it is written as-is
		// rather than re-encoded from a parsed manifest.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(cfg.ManifestJSON)
	})

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})

	if cfg.UI != nil {
		mux.Handle("GET /ui/", uiHandler(cfg.UI))
	}

	// Each skill is mounted at its own exact path rather than under a /skills/
	// subtree: nothing above a document is then listable, and the manifest's
	// skills[].path is the only thing that has to agree with the mount — which is
	// what CheckManifestRoutes checks.
	for at, body := range cfg.Skills {
		mux.HandleFunc("GET "+at, func(w http.ResponseWriter, r *http.Request) {
			text := body
			if cfg.SkillText != nil {
				served, err := cfg.SkillText(r.Context(), at)
				if err != nil {
					log.Printf("GET %s: %v", at, err)
					http.Error(w, "skill document is unavailable", http.StatusServiceUnavailable)
					return
				}
				text = served
			}
			// The hub stores what it fetches here as a markdown document, and a body
			// served as sniffed text/plain reads as the wrong file.
			w.Header().Set("Content-Type", skillContentType)
			_, _ = w.Write([]byte(text))
		})
	}

	// The plugin's API routes mount here, under apiBasePath — the prefix the hub
	// proxies this plugin's API to. The handler declares the full "/api/…" paths,
	// so they arrive at it unchanged; /api/* with no handler is a 404.
	if cfg.API != nil {
		mux.Handle("/api/", cfg.API)
	}

	if cfg.MCP != nil {
		// No method pattern: the streamable-HTTP transport uses GET, POST and
		// DELETE on the same path.
		mux.Handle("/mcp", cfg.MCP)
	}

	// ServeMux answers a request whose path is not clean with a redirect to the
	// cleaned path, which turns "/ui/../ainsel-plugin.json" into a redirect that
	// does serve the manifest. Such requests are rejected here instead, before
	// any route gets to rewrite them.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if slices.Contains(strings.Split(r.URL.Path, "/"), "..") {
			http.NotFound(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// skillContentType is what a skill document is served as.
const skillContentType = "text/markdown; charset=utf-8"

// uiHandler serves the UI bundle under /ui/ and never lists a directory. The
// bundle has no index.html, so the stock file server would answer a directory
// with a listing advertising the bundle's contents; a directory is treated like
// any other missing file and is a 404.
func uiHandler(bundle fs.FS) http.Handler {
	files := http.StripPrefix("/ui", http.FileServerFS(bundle))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The dot-dot guard in New has already run, so this path has no ".."
		// segment to outsmart the lookup below. Cleaning it puts the name in
		// the form fs.FS wants: "/ui/assets/" and "/ui/assets" both become
		// "assets", which is the directory that must not be listed.
		name := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/ui"))
		if name == "/" {
			http.NotFound(w, r)
			return
		}
		if info, err := fs.Stat(bundle, strings.TrimPrefix(name, "/")); err == nil && info.IsDir() {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
}
