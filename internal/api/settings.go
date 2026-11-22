package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/reconcile"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/settings"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
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
// over the resources a scope names, the on-demand LLM reachability check
// behind "Check now", and the write that puts an entered provider key onto a
// role's agents. A nil rec is a plugin built without a hub client: settings
// still save, and nothing there is to reconcile, to ask, or to hand a key to.
type passRunner interface {
	Pass(ctx context.Context, scope reconcile.Scope) (reconcile.Summary, error)
	CheckLLMs(ctx context.Context) ([]reconcile.LLMCheck, error)
	PushLLMKey(ctx context.Context, role, provider, key string) error
}

// installsReader is what the settings routes need of the install store: which
// roles have an agent installed. It answers the one question an entered key
// has to be asked before it is accepted — is there an agent to hold it,
// because the agent is the key's only home (the settings table keeps no copy).
type installsReader interface {
	List(context.Context) ([]setup.Install, error)
}

// settingsResponse is both GET and PUT answer: the full set, edited included.
// Never null — the set is always the whole registry, so the console renders
// the page without knowing which fields exist.
type settingsResponse struct {
	Settings []settings.Setting `json:"settings"`

	// Fields names a field that saved but did not reach the hub — today only a
	// provider key, whose storage is a hub write onto the role's agents rather
	// than a row here. It is the shape a refused PUT already answers with,
	// carrying the opposite news: the batch landed, this half of it did not.
	// Empty on every save that reached everything, so the console that ignores
	// it is correct rather than merely quiet.
	Fields []settings.FieldError `json:"fields,omitempty"`
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
//
// installs is what says whether a role has an agent at all. One kind of field
// needs it: a provider key is not stored here (settings.isLLMKeyField), it is
// written onto the role's agents, so a key typed for a role nobody installed
// has nowhere to go — it is refused in its own field, per field, while the rest
// of the batch saves (the whole-batch refusal is what made a Settings page
// unusable before the first install).
func NewSettings(h hub.Client, s settingsOps, rec passRunner, installs installsReader) http.Handler {
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
			// A served answer never carries a stored secret back: a provider key
			// row reads empty, and the plugin holds no key to answer with — the
			// key's home is the hub, on the role's agents.
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

			// The provider keys are the saved fields with no row behind them:
			// they are written onto the role's agents, so a key for a role with
			// no agent is refused here rather than accepted and dropped. The
			// refusal is per field — the batch's other edits save exactly as they
			// always did, and the answer names the key that did not go — in the
			// field shape the console already renders, because the fix is a wizard
			// action: install the role, then come back to this box (or type the
			// key in the wizard itself). Refusing the whole batch is what made a
			// Settings page unusable before the first install.
			accepted, keyErrs := acceptedEdits(ctx, edits, rec, installs)

			if err := s.Save(ctx, accepted); err != nil {
				// A refused edit is the caller's answer, not a failure to
				// hide: each bad field, named, so the form can mark it.
				var invalid *settings.ValidationError
				if errors.As(err, &invalid) {
					writeJSON(w, http.StatusBadRequest, struct {
						Error  string                `json:"error"`
						Fields []settings.FieldError `json:"fields"`
					}{
						Error:  "invalid settings",
						Fields: append(invalid.Fields, keyErrs...),
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
			for _, e := range accepted {
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

			// The keys go out last, after the pass above: a role that just moved
			// onto a key-based provider has its other agent fields written by
			// that pass, and the key lands on an agent that exists and is wired
			// as the settings now say. Unlike a failing pass — which is logged,
			// because the settings are the PUT's promise and the next pass tries
			// again — a key that did not land is answered: there is no later pass
			// that will retry it, because the plugin kept no copy to retry from.
			writeJSON(w, http.StatusOK, settingsResponse{Settings: out, Fields: append(pushLLMKeys(ctx, rec, accepted, out), keyErrs...)})
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

	// POST /api/settings/reconcile — "Check now": one drift pass over everything,
	// answered with the per-resource summary, plus the LLM reachability check over
	// the pairs the settings name. This is where drift shows itself — and where a
	// provider that cannot serve shows itself too, because a button named "check"
	// that never asks the question is how a broken configuration reads as healthy.
	// The periodic loop stays drift-only: pinging a third-party provider on a timer
	// is cost and flake the plugin cannot repair away.
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
			checks, err := rec.CheckLLMs(ctx)
			if err != nil {
				// The reachability half not running fails the whole answer: a summary
				// that silently omits it would let "nothing to fix" stand for a check
				// that was never made — the exact misreport this route exists to end.
				// The drift pass already wrote what it found, so a retry loses nothing.
				log.Printf("api: settings: reconcile llm check: %v", err)
				return fmt.Errorf("llm check: %w", err)
			}
			sum.LLMChecks = checks
			if sum.LLMChecks == nil {
				// One shape for the console: the list is empty when nothing was
				// flagged, never null.
				sum.LLMChecks = []reconcile.LLMCheck{}
			}
			logPass("check now", sum)
			writeJSON(w, http.StatusOK, sum)
			return nil
		})
	})

	return mux
}

// acceptedEdits is the pre-save rule for the fields whose storage is not a row:
// a provider key entered for a role with no installed agent has nowhere honest
// to go, and accepting it would be a save that silently stores nothing. It
// answers the batch without those keys, plus one field error each — the refusal
// is the key's own, never the batch's, because a whole-batch refusal is what
// made a Settings page unusable before the first install (github #83's trap).
// An unknown role is left for Save to name: this check is about installs, and
// "install the role first" would be a wrong answer for a role that does not
// exist.
func acceptedEdits(ctx context.Context, edits []settings.Edit, rec passRunner, installs installsReader) ([]settings.Edit, []settings.FieldError) {
	var pending []settings.Edit
	for _, e := range edits {
		if !settings.IsLLMKeyField(e.Field) || e.Reset || strings.TrimSpace(e.Value) == "" {
			continue
		}
		if !knownRole(e.Role) {
			continue
		}
		pending = append(pending, e)
	}
	if len(pending) == 0 {
		return edits, nil
	}
	if rec == nil {
		// No hub means nowhere to store the key, and a plugin that cannot reach
		// the hub is not a plugin that can promise the key arrived.
		return dropKeys(edits, pending), refuseKeys(pending, "this plugin has no hub to store the provider key on")
	}
	if installs == nil {
		return dropKeys(edits, pending), refuseKeys(pending, "the plugin could not check whether this role is installed")
	}

	list, err := installs.List(ctx)
	if err != nil {
		// Not being able to ask is not a reason to write the key into nothing,
		// and it is not the admin's mistake either — so the refusal says the
		// store could not be read.
		log.Printf("api: settings: install store for a provider key: %v", err)
		return dropKeys(edits, pending), refuseKeys(pending, "the plugin could not check whether this role is installed")
	}
	installed := map[string]bool{}
	for _, in := range list {
		if in.LayoutVersion != setup.LayoutManagedAgents {
			// A legacy row awaits the cutover; its agents are not this plugin's
			// to write, so they are not a home for the key either.
			continue
		}
		for role, st := range in.Roles {
			if st.AgentID != "" {
				installed[string(role)] = true
			}
		}
	}

	var bad []settings.FieldError
	var dropped []settings.Edit
	for _, e := range pending {
		if !installed[e.Role] {
			dropped = append(dropped, e)
			bad = append(bad, settings.FieldError{
				Role:  e.Role,
				Field: e.Field,
				Error: "the key is stored on the role's agents by the hub — install the role first, or enter the key during Setup",
			})
		}
	}
	return dropKeys(edits, dropped), bad
}

// dropKeys is the batch without the named edits in it: a refused key leaves no
// row behind, so what saves is exactly what the answer does not refuse.
func dropKeys(edits, refused []settings.Edit) []settings.Edit {
	if len(refused) == 0 {
		return edits
	}
	type key struct{ role, field string }
	bad := make(map[key]bool, len(refused))
	for _, e := range refused {
		bad[key{e.Role, e.Field}] = true
	}
	out := make([]settings.Edit, 0, len(edits))
	for _, e := range edits {
		if bad[key{e.Role, e.Field}] {
			continue
		}
		out = append(out, e)
	}
	return out
}

// refuseKeys is the answer for a batch where every entered key fails for the
// same reason.
func refuseKeys(edits []settings.Edit, msg string) []settings.FieldError {
	bad := make([]settings.FieldError, 0, len(edits))
	for _, e := range edits {
		bad = append(bad, settings.FieldError{Role: e.Role, Field: e.Field, Error: msg})
	}
	return bad
}

// pushLLMKeys writes each key the batch entered onto the role's agents, and
// answers with the ones that did not land. It runs after the save and after
// the pass: the settings are the PUT's promise, the key is the extra write on
// top.
//
// A key whose provider is not the provider the role now runs on is refused
// rather than sent: the agent has no block for a provider it does not use, and
// quietly dropping the key would read as a save that stored one. The key itself
// never appears in an error — the hub builds its failure text from its own
// response, which does not echo what it was handed.
func pushLLMKeys(ctx context.Context, rec passRunner, edits []settings.Edit, all []settings.Setting) []settings.FieldError {
	var bad []settings.FieldError
	for _, e := range edits {
		if !settings.IsLLMKeyField(e.Field) || e.Reset {
			continue
		}
		key := strings.TrimSpace(e.Value)
		if key == "" {
			// "Keep what the agents hold" — the shape every batch that never
			// touched the box sends, and nothing to write.
			continue
		}
		if rec == nil || !knownRole(e.Role) {
			// A missing hub or an unknown role was already refused before the
			// save; nothing to push.
			continue
		}
		provider := llmProviderOfKeyField(e.Field)
		if running := effectiveOf(all, e.Role, settings.FieldLLMProvider); running != provider {
			bad = append(bad, settings.FieldError{
				Role:  e.Role,
				Field: e.Field,
				Error: fmt.Sprintf("the role runs on %s, so a %s key has no agent to hold it (pick %s as the provider, or clear this box)", running, provider, provider),
			})
			continue
		}
		if err := rec.PushLLMKey(ctx, e.Role, provider, key); err != nil {
			bad = append(bad, settings.FieldError{Role: e.Role, Field: e.Field, Error: err.Error()})
		}
	}
	return bad
}

// llmProviderOfKeyField is the provider a key row belongs to — the inverse of
// setup.LLMKeyFieldOf, and the reason the route can say which provider a typed
// key was meant for without the admin naming it twice.
func llmProviderOfKeyField(field string) string {
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

// effectiveOf reads one field's effective value out of a registry read, ""
// when the read holds no such row.
func effectiveOf(all []settings.Setting, role, field string) string {
	for _, s := range all {
		if s.Role == role && s.Field == field {
			return s.Value
		}
	}
	return ""
}

// knownRole says whether a settings edit names one of the three installed
// roles — the check the key refusal makes before telling anyone to install it.
func knownRole(role string) bool {
	for _, r := range settings.Roles {
		if string(r) == role {
			return true
		}
	}
	return false
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
	case settings.FieldPatrolEnabled, settings.FieldPatrolInterval:
		return reconcile.ScopeTrigger
	default:
		return reconcile.ScopeNone
	}
}

// logPass writes a pass's answer to the log as it is answered: the summary is
// for whoever asked, the log is for whoever asks later — including the refused
// writes, which are warnings and not halts. A flagged LLM check is a warning of
// the same kind: not a write the hub refused, but not a check that passed either.
func logPass(cause string, sum reconcile.Summary) {
	flagged := 0
	for _, c := range sum.LLMChecks {
		if c.State != hub.LLMCheckOK {
			flagged++
		}
	}
	if len(sum.Updated) == 0 && len(sum.Refused) == 0 && flagged == 0 {
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
	for _, c := range sum.LLMChecks {
		if c.State == hub.LLMCheckOK {
			continue
		}
		log.Printf("reconcile (%s): WARNING llm check %s: %s @%s is %s: %s", cause, c.Role, c.Model, c.Provider, c.State, c.Reason)
	}
	log.Printf("reconcile (%s): %d updated, %d refused", cause, len(sum.Updated), len(sum.Refused))
}
