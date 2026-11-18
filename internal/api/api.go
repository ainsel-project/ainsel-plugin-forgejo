package api

import "net/http"

// NewAPI combines the work, setup and settings handlers by routing by prefix:
// requests to /api/setup/* go to the setup handler, requests to /api/settings
// go to the settings handler, requests to /api/instances go to the settings
// handler too (the configured Forgejo instances are settings-store data, and
// the admin-only gate the settings routes serve behind is the right gate for
// them), and all other /api/* requests go to the work handler.
func NewAPI(work, setup, settings http.Handler) http.Handler {
	mux := http.NewServeMux()
	// Register more specific patterns first, so /api/setup/* routes to setup
	// and /api/settings (and /api/settings/reconcile) routes to settings
	// rather than to the work prefix.
	mux.Handle("/api/setup/", setup)
	mux.Handle("/api/settings", settings)
	// The trailing-slash form too: "Check now" lives under the collection, at
	// /api/settings/reconcile, and without this it would fall through to work.
	mux.Handle("/api/settings/", settings)
	// The configured Forgejo instances: served by the settings handler, at a
	// path of its own.
	mux.Handle("/api/instances", settings)
	// Then register the less specific pattern for all other /api/* requests
	mux.Handle("/api/", work)
	return mux
}
