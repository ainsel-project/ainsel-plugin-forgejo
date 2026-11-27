// Command plugin runs the forgejo plugin: it serves the embedded manifest,
// a health check, the embedded UI bundle, the backend API and the MCP endpoint
// from one port.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	forgejo "github.com/ainsel-project/ainsel-plugin-forgejo"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/api"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/cutover"
	forgejoapi "github.com/ainsel-project/ainsel-plugin-forgejo/internal/forgejo"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/manifest"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/mcp"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/reconcile"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/server"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/settings"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/skill"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/store"
)

// shutdownTimeout bounds the graceful shutdown, so a stuck request cannot hold
// the pod past the point Kubernetes wants it gone.
const shutdownTimeout = 10 * time.Second

// Request timeouts for the server. Without them a connection that opens and
// never finishes its request (Slowloris) holds a goroutine and a fd forever,
// and a plugin has no other client than the hub to negotiate that with.
//
// WriteTimeout is the ceiling on one response, an open SSE stream included, so
// a plugin whose tool call streams for longer raises it (or moves the long
// stream to its own handler) rather than losing the call at 30s.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("plugin: %v", err)
	}
}

func run() error {
	// The manifest the plugin describes itself with, parsed from the bytes it
	// serves, so the two can never disagree. The same values go to the check and to
	// the server: the document the hub reads is the document being checked, and the
	// skill map the check looks at is the tree the server mounts.
	skills := forgejo.Skills()
	m, err := manifest.Parse(forgejo.ManifestJSON)
	if err != nil {
		return fmt.Errorf("embedded manifest is invalid: %w", err)
	}

	// The manifest is what the hub proxies by and the mounts are what this
	// binary serves; if a rename moved one and not the other, the plugin would
	// start Ready and answer nothing. Refuse to start instead.
	if err := server.CheckManifestRoutes(m, skills); err != nil {
		return fmt.Errorf("manifest paths do not match the routes the server mounts: %w", err)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Where the plugin reaches the hub to ask about the caller. In the cluster the
	// chart sets it to the hub's Service; unset — a plugin run on its own — there is
	// no hub to reach, and the work routes say so rather than reporting no work.
	hubURL := os.Getenv("HUB_URL")
	hubTransport := hub.NewTransport(hubURL)
	var hubClient hub.Client
	if c := hub.NewHTTPClient(hubTransport); c != nil {
		hubClient = c
	}

	// The installs the wizard saved: which forges this plugin works on, and which
	// role each agent plays. The work API reads nothing outside them.
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}
	installStore, err := store.Open(dataDir)
	if err != nil {
		return fmt.Errorf("install store: %w", err)
	}
	defer func() {
		if err := installStore.Close(); err != nil {
			log.Printf("install store: %v", err)
		}
	}()

	// The per-role settings the admin owns, in the same database file as the
	// installs: two handles on one SQLite file, serialised by SQLite itself.
	settingsStore, err := settings.Open(dataDir)
	if err != nil {
		return fmt.Errorf("settings store: %w", err)
	}
	defer func() {
		if err := settingsStore.Close(); err != nil {
			log.Printf("settings store: %v", err)
		}
	}()

	// The work read path the API routes and the MCP tools both serve: the hub to ask,
	// the installs to ask about, and the Forgejo every link is built from.
	forgejoBaseURL := os.Getenv("FORGEJO_BASE_URL")
	forgejoClient := forgejoapi.New(forgejoBaseURL, nil)

	// The configured Forgejo instances seed once from FORGEJO_BASE_URL: an
	// existing deployment keeps its single address as the one instance the wizard
	// pre-selects, with no admin step. A list the admin has edited (or emptied)
	// is never re-seeded — the env var is the migration, not the owner.
	if seeded, err := settingsStore.SeedInstanceFromEnv(context.Background(), forgejoBaseURL); err != nil {
		log.Printf("settings: seeding the Forgejo instance from FORGEJO_BASE_URL: %v", err)
	} else if seeded {
		log.Printf("settings: seeded the Forgejo instance list from FORGEJO_BASE_URL (%s)", forgejoBaseURL)
	}

	// Fresh-start cutover (design §11): the first start under the created-only
	// layout retires whatever the legacy wizard left behind — every install row
	// not stamped with the new layout has the resources it still tracks deleted,
	// with the pass logged loudly. The row drops once nothing of it is left to
	// retry; a delete that failed keeps the row, stamped retiring, so the next
	// start retries it — the row is the only record of what the plugin created,
	// and dropping it over a transient hub failure orphans the resource for
	// good. It runs here, at start, before the startup reconcile or the API can
	// act on an old row; a pass that crashes retries on the next start, because
	// re-deleting what is already gone is one logged no-op more. Without an
	// admin token the Forgejo side cannot be reached, and those resources are
	// logged as left standing by choice.
	if hubClient != nil {
		cut := cutover.Cutover{
			Hub:   hubClient,
			Store: installStore,
			Log:   func(msg string) { log.Printf("cutover: %s", msg) },
		}
		if tok := os.Getenv("FORGEJO_ADMIN_TOKEN"); tok != "" {
			cut.Forgejo = forgejoClient.Admin(tok)
		}
		if err := cut.Run(context.Background()); err != nil {
			log.Printf("cutover: %v", err)
		}
	}

	// The reconcile engine (design decision 8): the plugin owns the resources it
	// creates, so a pass reads them back, computes the state the settings say
	// they should be in, and writes the difference. It is triggered, never
	// scheduled: a settings save reconciles the resources that field touches
	// (the API drives that), this start reconciles everything once — the
	// upgrade's auto-adoption — and "Check now" reconciles everything on
	// request. There is no timer; the pass runs on the server's lifetime
	// context only when the hub client is available (HUB_URL is set).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var reconciler *reconcile.Engine
	if hubClient != nil {
		reconciler = &reconcile.Engine{
			Hub:      hubClient,
			Store:    installStore,
			Settings: settingsStore,
			Defaults: settingsStore,
			Log: func(slug, msg string) {
				if slug != "" {
					log.Printf("reconcile %s: %s", slug, msg)
				} else {
					log.Printf("reconcile: %s", msg)
				}
			},
		}
	}

	// The work read path: the installs to ask about, and the same Forgejo the
	// routes were built on.
	workService := api.NewWorkService(hubClient, installStore, forgejoBaseURL)
	workAPI := api.NewWork(workService)

	// The setup service: the wizard's options, plan and apply, behind one code
	// path the console's routes and the MCP operator tools both serve from.
	// The wizard reads the settings store: its created-only installs are built on
	// the settings' llm and MCP values.
	setupService := api.NewSetupService(hubClient, forgejoClient, installStore, settingsStore, api.SetupConfig{
		ForgejoBaseURL: forgejoBaseURL,
		// The hub's own address: how the orphan sweep recognises a leftover org
		// webhook — its URL points at the hub's connector webhook space.
		HubBaseURL: hubURL,
	})
	// The setup API for the wizard. Combine work and setup into a single API
	// handler by routing by prefix.
	setupAPI := setupService.Handler()
	// The role settings API: the admin's values for every install, read and
	// edited here; a saved PUT reconciles the resources the saved fields touch.
	settingsAPI := api.NewSettings(hubClient, settingsStore, reconciler, installStore)
	combinedAPI := api.NewAPI(workAPI, setupAPI, settingsAPI)

	srv := &http.Server{
		Addr:              ":" + port,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		Handler: server.New(server.Config{
			ManifestJSON: forgejo.ManifestJSON,
			UI:           forgejo.UI(),
			Skills:       skills,
			// The skill document is read from the settings on every fetch, so the
			// edit an admin just saved is the body the hub's next pass stores — no
			// restart, nothing pushed (design decision 5). A read that fails is
			// logged and answered as a failure, which keeps the body the hub stored
			// rather than reverting it to the shipped text.
			SkillText: func(ctx context.Context, path string) (string, error) {
				return skill.Text(ctx, settingsStore, path)
			},
			MCP:     mcp.NewHandler(m.Version, workService, setupService),
			Version: m.Version,
			API:     combinedAPI,
		}),
	}

	serveErr := make(chan error, 1)
	// The listener is opened here rather than inside ListenAndServe so the
	// startup reconcile can be scheduled once the plugin is verifiably up:
	// the pass talks to the hub for as long as the hub takes, and must not
	// delay the health endpoint or the manifest route.
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	go func() {
		log.Printf("plugin %s listening on %s", m.ID, srv.Addr)
		// ErrServerClosed is the expected result of Shutdown, not a failure.
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	// The provider-key migration (the upgrade's other half): an older plugin
	// stored the four LLM provider keys in the settings table. Their home is the
	// hub's per-agent secret now, so each stored key is forwarded onto the
	// role's agents once — while the row is still the authoritative copy — and
	// the row is deleted behind it. A key whose forward the hub refused keeps
	// its row and is retried on the next start: until it lands, the row is the
	// only copy there is. A key with no agent to hold it is dropped with a
	// loud line — the admin enters it on the next install, or on Settings once
	// the role is installed. It runs before the startup reconcile below, so no
	// pass ever sees a settings table that still holds a secret.
	if reconciler != nil {
		forwardStoredLLMKeys(ctx, settingsStore, reconciler)
	} else if pending, err := settingsStore.PendingLLMKeys(ctx); err != nil {
		log.Printf("settings: reading stored provider keys: %v", err)
	} else if len(pending) > 0 {
		// No hub to forward to: the rows stay, and nothing serves them — every
		// read of the settings table answers a key field empty.
		log.Printf("settings: %d stored provider key(s) cannot be forwarded without a hub", len(pending))
	}

	// The startup reconcile, in a goroutine of its own: the plugin is serving
	// — the hub can reach it — before the pass starts talking back. It adopts
	// the new shipped defaults for fields nobody edited (the upgrade's
	// auto-adoption, decision 4) and reconciles every install, so an upgrade's
	// new persona text reaches the agents without anyone asking. ctx is
	// cancelled at shutdown, and a pass in flight goes with it.
	if reconciler != nil {
		go func() {
			sum, err := reconciler.Startup(ctx)
			if err != nil {
				log.Printf("reconcile: startup pass: %v", err)
			}
			if n := len(sum.Updated); n > 0 || len(sum.Refused) > 0 {
				log.Printf("reconcile: startup pass: %d updated, %d refused", n, len(sum.Refused))
			}
		}()
	}

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		log.Printf("signal received, shutting down (up to %s for in-flight requests)", shutdownTimeout)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// keyPusher is what the key migration needs of the reconcile engine: the one
// write that puts an entered provider key onto a role's agents.
type keyPusher interface {
	PushLLMKey(ctx context.Context, role, provider, key string) error
}

// forwardStoredLLMKeys moves the provider keys an older plugin stored in the
// settings table onto the role's agents, and deletes the rows behind them.
//
// The order is the whole point: the row is forwarded while it still holds the
// authoritative value, and only a key that reached the hub (or one there was
// nowhere to put — a role with no installed agent) is deleted. A key the hub
// refused keeps its row and is retried on the next start, because until it
// lands, the row is the only copy there is. An empty row is a key nobody ever
// typed: deleted with the rest.
func forwardStoredLLMKeys(ctx context.Context, st *settings.Store, rec keyPusher) {
	pending, err := st.PendingLLMKeys(ctx)
	if err != nil {
		log.Printf("settings: reading stored provider keys: %v", err)
		return
	}
	if len(pending) == 0 {
		return
	}

	var done []settings.PendingLLMKey
	for _, k := range pending {
		if strings.TrimSpace(k.Value) == "" {
			done = append(done, k)
			continue
		}
		provider := llmProviderForKeyField(k.Field)
		if provider == "" {
			// Not a provider this plugin holds a key for: nothing to forward.
			done = append(done, k)
			continue
		}
		if err := rec.PushLLMKey(ctx, k.Role, provider, k.Value); err != nil {
			if strings.Contains(err.Error(), "no installed agent") {
				// Nowhere honest to put it, and holding the row forever would
				// keep the plaintext on the volume for a key no agent will use.
				log.Printf("settings: stored %s key for role %s has no installed agent to hold it: dropped, enter it again on the next install or on Settings once the role is installed", k.Field, k.Role)
				done = append(done, k)
				continue
			}
			log.Printf("settings: forwarding the stored %s key for role %s: %v: kept for the next start", k.Field, k.Role, err)
			continue
		}
		log.Printf("settings: forwarded the stored %s key for role %s onto the role's agents", k.Field, k.Role)
		done = append(done, k)
	}

	if err := st.DeleteLLMKeys(ctx, done); err != nil {
		log.Printf("settings: deleting forwarded provider keys: %v", err)
	}
}

// llmProviderForKeyField is the provider a stored key row belongs to — the
// inverse of setup.LLMKeyFieldOf.
func llmProviderForKeyField(field string) string {
	switch field {
	case settings.FieldLLMCustomKey:
		return setup.LLMProviderCustom
	case settings.FieldLLMOpenAIKey:
		return setup.LLMProviderOpenAI
	case settings.FieldLLMAnthropicKey:
		return setup.LLMProviderAnthropic
	case settings.FieldLLMGoogleKey:
		return setup.LLMProviderGoogle
	}
	return ""
}
