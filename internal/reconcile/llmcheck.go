package reconcile

import (
	"context"
	"fmt"
	"sync"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/settings"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
)

// LLMCheck is one role's LLM pair as the on-demand reachability check found it: the
// pair the settings name for the role, the state the hub reported, and the sanitized
// reason for anything that is not ok.
//
// Entries are per role — the admin reads the answer beside the role's fields — but the
// question is asked per distinct (provider, model) pair: two roles sharing a pair are
// one check whose result is reported twice, not two pings to the same provider.
type LLMCheck struct {
	Role     string            `json:"role"`
	Provider string            `json:"provider"`
	Model    string            `json:"model"`
	State    hub.LLMCheckState `json:"state"`
	Reason   string            `json:"reason,omitempty"`
}

// CheckLLMs is the reachability half of "Check now": it asks the hub, once per
// distinct (provider, model) pair the settings name, whether that pair can serve a
// request, and reports the answer per role.
//
// It is on-demand only. The periodic loop does not run it: reachability is the
// operational question an admin asks, and a loop pinging a third-party LLM API every
// ten minutes adds cost, rate-limit exposure and flake for a failure the plugin cannot
// repair — an unreachable provider is not drift a pass can write away.
//
// Like the pass, every hub call is made as the plugin (WithBackground): the question is
// about the plugin's configuration, whatever browser happened to press the button. A
// role whose settings hold no model is skipped — nobody has chosen one yet, the same
// rule reconcileAgent applies to writes — and a check that could not be asked reports
// unknown, never ok. The error is reserved for the check itself not running: settings
// that cannot be read, or a caller that went away.
func (e Engine) CheckLLMs(ctx context.Context) ([]LLMCheck, error) {
	ctx = hub.WithBackground(ctx)

	want, err := e.desiredState(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: %w", err)
	}

	type rolePair struct {
		role setup.Role
		pair hub.LLMCheckPair
	}
	roles := make([]rolePair, 0, len(settings.Roles))
	pairs := make(map[hub.LLMCheckPair]bool, len(settings.Roles))
	for _, role := range settings.Roles {
		spec := want.llm[role]
		if spec.Model == "" {
			continue
		}
		p := hub.LLMCheckPair{Provider: spec.Provider, Model: spec.Model}
		roles = append(roles, rolePair{role: role, pair: p})
		pairs[p] = true
	}

	// One check per distinct pair, in parallel: each goes out to a provider that may
	// be down, and a check that waits its turn behind two slow ones is a "Check now"
	// that takes three times as long as it needs to. The hub client caps each check
	// on its own deadline, and every one honors the caller's context. No retries: a
	// pass asks each provider once.
	results := make(map[hub.LLMCheckPair]hub.LLMCheckResult, len(pairs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for p := range pairs {
		wg.Add(1)
		go func(p hub.LLMCheckPair) {
			defer wg.Done()
			res, err := e.Hub.CheckLLM(ctx, p.Provider, p.Model)
			if err != nil {
				// The call did not complete. That is the hub not answering,
				// not the provider failing: "cannot check", never ok.
				e.log("", fmt.Sprintf("llm check %s @%s: %v", p.Model, p.Provider, err))
				res = hub.LLMCheckResult{
					State:  hub.LLMCheckUnknown,
					Reason: "cannot check: the hub did not answer",
				}
			}
			// A flagged result is not logged here: the caller logs the whole summary
			// once per role through logPass, which names the install and the pass.
			mu.Lock()
			results[p] = res
			mu.Unlock()
		}(p)
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Fan the answers back out per role, in the settings' order: the pair was asked
	// once, but each role names it, and each role's admin-facing line is its own.
	out := make([]LLMCheck, 0, len(roles))
	for _, rp := range roles {
		res := results[rp.pair]
		out = append(out, LLMCheck{
			Role:     string(rp.role),
			Provider: rp.pair.Provider,
			Model:    rp.pair.Model,
			State:    res.State,
			Reason:   res.Reason,
		})
	}
	return out, nil
}
