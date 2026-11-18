package reconcile

import (
	"context"
	"errors"
	"testing"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/settings"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
)

// --- the on-demand LLM reachability check ---

// pairSettings is a settings source naming one (provider, model) pair for every role —
// the shape of an admin who set one model and meant it everywhere.
func pairSettings(provider, model string) fakeSettings {
	var rows []settings.Setting
	for _, role := range settings.Roles {
		rows = append(rows,
			settings.Setting{Role: string(role), Field: settings.FieldLLMProvider, Value: provider},
			settings.Setting{Role: string(role), Field: settings.FieldLLMModel, Value: model},
		)
	}
	return fakeSettings{rows: rows}
}

// brokenSettings is a store that cannot answer: the check has no pairs to ask about
// and says so rather than guessing.
type brokenSettings struct{}

func (brokenSettings) All(context.Context) ([]settings.Setting, error) {
	return nil, errors.New("store offline")
}

// checkEngine is an engine with nothing but a hub and settings: the check reads the
// settings and asks the hub, and touches no install.
func checkEngine(f hub.Client, s settingsReader) Engine {
	return Engine{Hub: f, Settings: s}
}

func TestCheckLLMsReachableAnswersOKPerRole(t *testing.T) {
	f := &hub.Fake{
		LLMChecks: map[hub.LLMCheckPair]hub.LLMCheckResult{
			{Provider: "ollama-cloud", Model: "llama3.3"}: {State: hub.LLMCheckOK},
		},
	}
	engine := checkEngine(f, pairSettings("ollama-cloud", "llama3.3"))

	checks, err := engine.CheckLLMs(context.Background())
	if err != nil {
		t.Fatalf("CheckLLMs: %v", err)
	}
	if len(checks) != len(settings.Roles) {
		t.Fatalf("checks = %+v, want one per role", checks)
	}
	for i, role := range settings.Roles {
		c := checks[i]
		if c.Role != string(role) || c.Provider != "ollama-cloud" || c.Model != "llama3.3" {
			t.Errorf("check %d = %+v, want %s @ollama-cloud/llama3.3", i, c, role)
		}
		if c.State != hub.LLMCheckOK {
			t.Errorf("check %d state = %q, want ok", i, c.State)
		}
	}
}

func TestCheckLLMsUnreachableNamesRolePairAndReason(t *testing.T) {
	f := &hub.Fake{
		LLMChecks: map[hub.LLMCheckPair]hub.LLMCheckResult{
			{Provider: "spurious", Model: "gone-model"}: {
				State:  hub.LLMCheckUnreachable,
				Reason: "provider refused: connection reset",
			},
		},
	}
	engine := checkEngine(f, pairSettings("spurious", "gone-model"))

	checks, err := engine.CheckLLMs(context.Background())
	if err != nil {
		t.Fatalf("CheckLLMs: %v", err)
	}
	for _, c := range checks {
		if c.State != hub.LLMCheckUnreachable {
			t.Errorf("check %+v: state = %q, want unreachable", c, c.State)
		}
		if c.Reason != "provider refused: connection reset" {
			t.Errorf("check %+v: reason = %q, want the hub's sentence", c, c.Reason)
		}
	}
}

func TestCheckLLMsHubThatWillNotAnswerIsUnknownNeverOK(t *testing.T) {
	// The old-hub path and the hub-refused path alike: a question nobody answered is
	// "cannot check" — never ok (the misreport this check exists to end) and never a
	// claim about the provider.
	f := &hub.Fake{LLMCheckErr: errors.New("connection refused")}
	engine := checkEngine(f, pairSettings("ollama-cloud", "llama3.3"))

	checks, err := engine.CheckLLMs(context.Background())
	if err != nil {
		t.Fatalf("CheckLLMs: %v", err)
	}
	if len(checks) != len(settings.Roles) {
		t.Fatalf("checks = %+v, want one per role", checks)
	}
	for _, c := range checks {
		if c.State != hub.LLMCheckUnknown {
			t.Errorf("check %+v: state = %q, want unknown", c, c.State)
		}
		if c.Reason == "" {
			t.Errorf("check %+v: an unknown answer carries no reason", c)
		}
	}
}

func TestCheckLLMsSkipsARoleWithNoModel(t *testing.T) {
	// An empty model is "nobody has chosen one yet" — the same rule reconcileAgent
	// refuses to write under. There is nothing to ask, so nothing is asked and
	// nothing is reported for that role.
	f := &hub.Fake{
		LLMChecks: map[hub.LLMCheckPair]hub.LLMCheckResult{
			{Provider: "ollama-cloud", Model: "llama3.3"}: {State: hub.LLMCheckOK},
		},
	}
	rows := pairSettings("ollama-cloud", "llama3.3").rows
	for i, r := range rows {
		if r.Role == string(setup.RoleDeveloper) && r.Field == settings.FieldLLMModel {
			rows[i].Value = ""
		}
	}
	engine := checkEngine(f, fakeSettings{rows: rows})

	checks, err := engine.CheckLLMs(context.Background())
	if err != nil {
		t.Fatalf("CheckLLMs: %v", err)
	}
	if len(checks) != 2 {
		t.Fatalf("checks = %+v, want the two roles that name a model", checks)
	}
	for _, c := range checks {
		if c.Role == string(setup.RoleDeveloper) {
			t.Errorf("the role with no model was reported: %+v", c)
		}
	}
	if len(f.LLMChecksAsked) != 1 {
		t.Errorf("the hub was asked %d times, want once — the skipped role asks nothing", len(f.LLMChecksAsked))
	}
}

func TestCheckLLMsAsksASharedPairOnce(t *testing.T) {
	// The shipped defaults name one pair for all three roles. The provider is
	// pinged once, and the one answer is reported under each role — the admin reads
	// the check beside each role's fields, the network sees one request.
	f := &hub.Fake{
		LLMChecks: map[hub.LLMCheckPair]hub.LLMCheckResult{
			{Provider: "ollama-cloud", Model: "llama3.3"}: {State: hub.LLMCheckOK},
		},
	}
	// No settings source at all: the shipped defaults, which name one pair.
	engine := checkEngine(f, nil)

	checks, err := engine.CheckLLMs(context.Background())
	if err != nil {
		t.Fatalf("CheckLLMs: %v", err)
	}
	if len(checks) != len(settings.Roles) {
		t.Fatalf("checks = %+v, want one per role", checks)
	}
	if len(f.LLMChecksAsked) != 1 {
		t.Errorf("the hub was asked %d times (%+v), want once for the shared pair",
			len(f.LLMChecksAsked), f.LLMChecksAsked)
	}
}

func TestCheckLLMsAsksDistinctPairsSeparately(t *testing.T) {
	// Two different pairs are two questions — dedupe only merges identical ones.
	f := &hub.Fake{
		LLMChecks: map[hub.LLMCheckPair]hub.LLMCheckResult{
			{Provider: "ollama-cloud", Model: "llama3.3"}:   {State: hub.LLMCheckOK},
			{Provider: "opencode", Model: "deepseek-coder"}: {State: hub.LLMCheckUnreachable, Reason: "no key"},
		},
	}
	rows := pairSettings("ollama-cloud", "llama3.3").rows
	for i, r := range rows {
		if r.Role == string(setup.RoleReviewer) {
			if r.Field == settings.FieldLLMProvider {
				rows[i].Value = "opencode"
			}
			if r.Field == settings.FieldLLMModel {
				rows[i].Value = "deepseek-coder"
			}
		}
	}
	engine := checkEngine(f, fakeSettings{rows: rows})

	checks, err := engine.CheckLLMs(context.Background())
	if err != nil {
		t.Fatalf("CheckLLMs: %v", err)
	}
	if len(f.LLMChecksAsked) != 2 {
		t.Errorf("the hub was asked %d times, want twice — two distinct pairs", len(f.LLMChecksAsked))
	}
	for _, c := range checks {
		if c.Role == string(setup.RoleReviewer) && c.State != hub.LLMCheckUnreachable {
			t.Errorf("reviewer check = %+v, want the unreachable answer of its own pair", c)
		}
	}
}

func TestCheckLLMsReportsUnreadableSettingsRatherThanGuessing(t *testing.T) {
	// No settings, no pairs, no honest answer: the check says it could not run
	// rather than answering an empty list that reads as "nothing to flag".
	engine := checkEngine(&hub.Fake{}, brokenSettings{})

	if _, err := engine.CheckLLMs(context.Background()); err == nil {
		t.Fatalf("CheckLLMs with unreadable settings answered without an error")
	}
}

func TestCheckLLMsHonorsCallerCancellation(t *testing.T) {
	// A caller that went away stops the check: the answer is the cancellation, not
	// a summary nobody will read.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	engine := checkEngine(&hub.Fake{}, pairSettings("ollama-cloud", "llama3.3"))
	if _, err := engine.CheckLLMs(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("CheckLLMs = %v, want the cancelled context to come back as the error", err)
	}
}

func TestCheckLLMsMarksHubCallsBackground(t *testing.T) {
	// Like the pass: the question is about the plugin's configuration, asked as the
	// plugin, whatever browser pressed the button. The real client refuses a call
	// that carries neither a caller nor the mark, so reaching the method is the proof.
	f := &hub.Fake{
		LLMChecks: map[hub.LLMCheckPair]hub.LLMCheckResult{
			{Provider: "ollama-cloud", Model: "llama3.3"}: {State: hub.LLMCheckOK},
		},
	}
	engine := checkEngine(f, pairSettings("ollama-cloud", "llama3.3"))

	if _, err := engine.CheckLLMs(context.Background()); err != nil {
		t.Fatalf("CheckLLMs: %v", err)
	}
	calls := false
	for _, c := range f.Calls {
		if c == "CheckLLM" {
			calls = true
		}
	}
	if !calls {
		t.Errorf("the check never reached the hub: %v", f.Calls)
	}
}

func TestCheckLLMsNothingToAskAnswersEmptyNotNull(t *testing.T) {
	// Every role empty is no question asked — and the answer is still a list, so the
	// console reads one shape.
	var rows []settings.Setting
	for _, role := range settings.Roles {
		rows = append(rows,
			settings.Setting{Role: string(role), Field: settings.FieldLLMProvider, Value: "ollama-cloud"},
			settings.Setting{Role: string(role), Field: settings.FieldLLMModel, Value: ""},
		)
	}
	engine := checkEngine(&hub.Fake{}, fakeSettings{rows: rows})

	checks, err := engine.CheckLLMs(context.Background())
	if err != nil {
		t.Fatalf("CheckLLMs: %v", err)
	}
	if checks == nil {
		t.Errorf("checks is nil; the console reads one shape")
	}
	if len(checks) != 0 {
		t.Errorf("checks = %+v, want empty", checks)
	}
}

// TestCheckLLMsAsksACustomPairRatherThanSkippingIt is the pass's half of the custom rule.
// A role on the custom provider is asked like any other — the console has to show the pair
// the admin set, not a blank — and the state is whatever the client answered. The pass has
// no business deciding a pair it cannot probe is fine, and no business hiding a row the
// admin is looking for: the honest "unknown" is the client's to give, and this is the
// proof the pass lets it through.
func TestCheckLLMsAsksACustomPairRatherThanSkippingIt(t *testing.T) {
	f := &hub.Fake{
		LLMChecks: map[hub.LLMCheckPair]hub.LLMCheckResult{
			{Provider: "custom", Model: "llama3.3"}: {State: hub.LLMCheckUnknown, Reason: "not one the hub can probe"},
		},
	}
	engine := checkEngine(f, pairSettings("custom", "llama3.3"))

	checks, err := engine.CheckLLMs(context.Background())
	if err != nil {
		t.Fatalf("CheckLLMs: %v", err)
	}
	if len(checks) != len(settings.Roles) {
		t.Fatalf("checks = %+v, want one per role — a custom role is not a role to skip", checks)
	}
	for _, c := range checks {
		if c.Provider != "custom" || c.Model != "llama3.3" {
			t.Errorf("check %+v, want the custom pair the settings name", c)
		}
		if c.State == hub.LLMCheckOK {
			t.Errorf("%s reads ok, want the client's unknown: nothing probed this endpoint", c.Role)
		}
	}
}
