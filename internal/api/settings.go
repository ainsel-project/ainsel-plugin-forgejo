package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/reconcile"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/settings"
)

// settingsOps is what the settings routes need of the settings store: read
// every field with its defaults folded in, and apply a batch of edits whole or
// not at all. It also carries the list of configured Forgejo instances: read
// it, and replace it whole.
type settingsOps interface {
	All(context.Context) ([]settings.Setting, error)
	Save(context.Context, []settings.Edit) error
	ListInstances(context.Context) ([]settings.Instance, error)
	SaveInstances(context.Context, []settings.Instance) error
}

// passRunner is the reconcile engine as the settings routes use it: one pass
// over the resources a scope names. A nil rec is a plugin built without a hub
// client: settings still save, and nothing there is to reconcile.
type passRunner interface {
	Pass(ctx context.Context, scope reconcile.Scope) (reconcile.Summary, error)
}

// settingsResponse is both GET and PUT answer: the full set, edited included.
// Never null — the set is always the whole registry, so the console renders
// the page without knowing which fields exist.
type settingsResponse struct {
	Settings []settings.Setting `json:"settings"`
}

// instancesResponse is both GET and PUT /api/instances answer: the stored list
// in its stored order. Never null — an empty list is the honest "nothing is
// configured", and the console renders the wizard's link-to-Settings state off
// it.
type instancesResponse struct {
	Instances []settings.Instance `json:"instances"`
}

// NewSettings serves the role settings routes: GET reads every field of every
// role with the shipped default behind it, PUT edits fields (marking them
// dirty) and resets them (re-adopting the default), and POST
// /api/settings/reconcile is "Check now" — one pass over everything, answered
// with what it updated and what the hub refused.
//
// Admin-only, like the wizard's routes: these values apply to every install
// the plugin has, so they are the admin's to set and nobody else's to read.
// A saved PUT is a reconcile trigger: the pass over exactly the resources the
// saved fields touch runs here, with the plugin's own credential — the plugin
// is the sole writer of those resources (design decision 1), whatever browser
// noticed the drift.
func NewSettings(h hub.Client, s settingsOps, rec passRunner) http.Handler {
	mux := http.NewServeMux()

	// GET /api/settings — every (role, field) with its effective value, its
	// dirty flag, and the shipped default with the version that produced it.
	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		gate(w, r, h, func(ctx context.Context, caller *hub.Caller) error {
			out, err := s.All(ctx)
			if err != nil {
				log.Printf("api: settings: store.All: %v", err)
				return errSettings
			}
			// A served answer never carries a stored secret back: the custom
			// provider key row reads empty, its dirty flag telling the admin a
			// key is set (an empty save keeps it, a reset clears it).
			settings.MaskSecrets(out)
			writeJSON(w, http.StatusOK, settingsResponse{Settings: out})
			return nil
		})
	})

	// PUT /api/settings — update one or more fields, each either an edit
	// (marks the field dirty) or a reset (clears it and re-adopts the
	// default). Refused edits answer with a field-level error for each bad
	// one and write nothing; on success the answer is the full set, so the
	// console sees the new dirty flags without a second read — and the
	// resources those fields own have already been reconciled to them.
	mux.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		gate(w, r, h, func(ctx context.Context, caller *hub.Caller) error {
			var req struct {
				Settings []struct {
					Role  string `json:"role"`
					Field string `json:"field"`
					Value string `json:"value"`
					Reset bool   `json:"reset"`
				} `json:"settings"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				return badRequest("invalid json")
			}
			if len(req.Settings) == 0 {
				return badRequest("no settings to update")
			}

			edits := make([]settings.Edit, 0, len(req.Settings))
			for _, e := range req.Settings {
				edits = append(edits, settings.Edit{
					Role:  e.Role,
					Field: e.Field,
					Value: e.Value,
					Reset: e.Reset,
				})
			}

			if err := s.Save(ctx, edits); err != nil {
				// A refused edit is the caller's answer, not a failure to
				// hide: each bad field, named, so the form can mark it.
				var invalid *settings.ValidationError
				if errors.As(err, &invalid) {
					writeJSON(w, http.StatusBadRequest, struct {
						Error  string                `json:"error"`
						Fields []settings.FieldError `json:"fields"`
					}{
						Error:  "invalid settings",
						Fields: invalid.Fields,
					})
					return nil
				}
				log.Printf("api: settings: store.Save: %v", err)
				return errSettings
			}

			// Saved. Now the pass over exactly the resources those fields
			// touch (design decision 8's targeted trigger): an edited persona
			// text has nothing to say about an agent's llm, and a pass that
			// swept everything anyway is the periodic loop with its timer
			// removed. A failing pass is logged, not answered: the settings
			// are saved — that is the PUT's promise — and "Check now" or the
			// next save tries the resources again.
			scope := reconcile.ScopeNone
			for _, e := range edits {
				scope |= reconcileScopeFor(e.Field)
			}
			if scope != reconcile.ScopeNone && rec != nil {
				if sum, err := rec.Pass(ctx, scope); err != nil {
					log.Printf("api: settings: reconcile after save: %v", err)
				} else {
					logPass("settings save", sum)
				}
			}

			out, err := s.All(ctx)
			if err != nil {
				log.Printf("api: settings: store.All: %v", err)
				return errSettings
			}
			settings.MaskSecrets(out)
			writeJSON(w, http.StatusOK, settingsResponse{Settings: out})
			return nil
		})
	})

	// GET /api/instances — the configured Forgejo instances, in the order the
	// Settings page lists them. The wizard's Instance step draws its select
	// from this list; an empty list is what tells it to link the admin to
	// Settings instead of offering an empty select.
	mux.HandleFunc("GET /api/instances", func(w http.ResponseWriter, r *http.Request) {
		gate(w, r, h, func(ctx context.Context, caller *hub.Caller) error {
			out, err := s.ListInstances(ctx)
			if err != nil {
				log.Printf("api: settings: store.ListInstances: %v", err)
				return errSettings
			}
			writeJSON(w, http.StatusOK, instancesResponse{Instances: out})
			return nil
		})
	})

	// PUT /api/instances — replace the whole list. Like the settings PUT, a
	// refused write names each bad row and changes nothing; on success the
	// answer is the stored list, so the console sees the normalized addresses
	// without a second read.
	mux.HandleFunc("PUT /api/instances", func(w http.ResponseWriter, r *http.Request) {
		gate(w, r, h, func(ctx context.Context, caller *hub.Caller) error {
			var req struct {
				Instances *[]settings.Instance `json:"instances"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				return badRequest("invalid json")
			}
			// A body without the "instances" key is a truncated request, not the
			// empty list: treating it as one would wipe the registry and mark it
			// seeded, so the env seed would never return. An explicit [] still means
			// "clear the list".
			if req.Instances == nil {
				return badRequest("instances")
			}

			if err := s.SaveInstances(ctx, *req.Instances); err != nil {
				var invalid *settings.ValidationError
				if errors.As(err, &invalid) {
					writeJSON(w, http.StatusBadRequest, struct {
						Error  string                `json:"error"`
						Fields []settings.FieldError `json:"fields"`
					}{
						Error:  "invalid instances",
						Fields: invalid.Fields,
					})
					return nil
				}
				log.Printf("api: settings: store.SaveInstances: %v", err)
				return errSettings
			}

			out, err := s.ListInstances(ctx)
			if err != nil {
				log.Printf("api: settings: store.ListInstances: %v", err)
				return errSettings
			}
			writeJSON(w, http.StatusOK, instancesResponse{Instances: out})
			return nil
		})
	})

	// POST /api/settings/reconcile — "Check now": one pass over everything,
	// answered with the per-resource summary. This is where drift shows
	// itself: the console keeps no drift badge of its own, because a badge
	// that noticed drift on its own would be a timer by another name.
	mux.HandleFunc("POST /api/settings/reconcile", func(w http.ResponseWriter, r *http.Request) {
		if rec == nil {
			writeError(w, http.StatusServiceUnavailable, "this plugin has no hub to reconcile against")
			return
		}
		gate(w, r, h, func(ctx context.Context, caller *hub.Caller) error {
			sum, err := rec.Pass(ctx, reconcile.ScopeAll)
			if err != nil {
				log.Printf("api: settings: reconcile: %v", err)
				return err
			}
			logPass("check now", sum)
			writeJSON(w, http.StatusOK, sum)
			return nil
		})
	})

	return mux
}

// reconcileScopeFor is the settings-field-to-resources mapping, explicit and
// small (design decision 8): the persona text reaches the personas, the llm
// fields (provider, model, and every provider key row) reach the agents, the
// MCP pair reaches the forge MCP row. The skill text reaches nothing here — PR 4
// makes it the served skill's text, and an agent's skill *selection* is the
// manifest's fixed set, which the agent scope carries on its own. Triggers and
// coverage belong to no setting: they ride a full pass only.
//
// Every key row belongs to the agent scope: a key is the agent's credential, so
// saving one alone has to trigger the pass that writes it back — the same rule
// for the custom pair's key and for the three vendor keys, whatever provider the
// role happens to run on (away from it the write is a no-op the pass makes
// anyway, and the key is there the moment the role moves onto it). The OpenAI
// auth-method row belongs to it for the same reason: it is the other half of how
// an agent authenticates, and a saved field that owned no scope would never
// reach the agent it was chosen for.
func reconcileScopeFor(field string) reconcile.Scope {
	switch field {
	case settings.FieldPersonaText:
		return reconcile.ScopePersona
	case settings.FieldLLMProvider, settings.FieldLLMModel,
		settings.FieldLLMCustomUrl, settings.FieldLLMCustomKey,
		settings.FieldLLMOpenAIKey, settings.FieldLLMAnthropicKey, settings.FieldLLMGoogleKey,
		settings.FieldLLMOpenAIAuthMethod:
		return reconcile.ScopeAgent
	case settings.FieldMCPUrl, settings.FieldMCPTokenFromEnv:
		return reconcile.ScopeMCPServer
	default:
		return reconcile.ScopeNone
	}
}

// logPass writes a pass's answer to the log as it is answered: the summary is
// for whoever asked, the log is for whoever asks later — including the refused
// writes, which are warnings and not halts.
func logPass(cause string, sum reconcile.Summary) {
	if len(sum.Updated) == 0 && len(sum.Refused) == 0 {
		log.Printf("reconcile (%s): nothing to fix", cause)
		return
	}
	for _, c := range sum.Updated {
		at := c.Install
		if c.Role != "" {
			at += "/" + c.Role
		}
		log.Printf("reconcile (%s): fixed %s %s.%s: %s -> %s", cause, at, c.Kind, c.Field, c.From, c.To)
	}
	for _, rf := range sum.Refused {
		at := rf.Install
		if rf.Role != "" {
			at += "/" + rf.Role
		}
		log.Printf("reconcile (%s): WARNING hub refused %s %s.%s: %v", cause, at, rf.Kind, rf.Field, rf.Error)
	}
	log.Printf("reconcile (%s): %d updated, %d refused", cause, len(sum.Updated), len(sum.Refused))
}
