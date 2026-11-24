package settings

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ainsel-project/ainsel-plugin-forgejo/content"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/store"
)

// openStore opens a settings store in a fresh directory and closes it with the
// test.
func openStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// editedSkill is the shipped skill section of a role with a body appended and its
// frontmatter kept — the shape of a legitimate admin edit.
func editedSkill(role setup.Role) string {
	return content.SkillSection(role) + "\n### Edited for the test\n\nAn admin's line.\n"
}

// TestAllResolvesDefaultsFromContent checks the no-rows state: every (role,
// field) answers, and each answer is the shipped default — the persona and
// skill come from content/, the wiring from the consts — with no row ever
// written.
func TestAllResolvesDefaultsFromContent(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	out, err := s.All(ctx)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(out) != gridSize() {
		t.Fatalf("All returned %d settings, want %d", len(out), gridSize())
	}

	i := 0
	for _, role := range Roles {
		for _, field := range Fields {
			if !Applies(role, field) {
				continue
			}
			set := out[i]
			i++
			if set.Role != string(role) || set.Field != field {
				t.Fatalf("setting %d is %s.%s, want %s.%s", i-1, set.Role, set.Field, role, field)
			}
			want := Default(role, field)
			if set.Value != want {
				t.Errorf("%s.%s: value = %q…, want the shipped default", role, field, truncate(set.Value))
			}
			if set.Default != want {
				t.Errorf("%s.%s: default != shipped default", role, field)
			}
			if set.Dirty {
				t.Errorf("%s.%s: dirty = true with no row", role, field)
			}
			if set.DefaultVersion != PluginVersion() {
				t.Errorf("%s.%s: defaultVersion = %q, want the running version %q", role, field, set.DefaultVersion, PluginVersion())
			}
		}
	}

	// The content-backed fields specifically: personaText is the role's
	// template and skillText is the role's section of the shipped SKILL.md,
	// frontmatter included.
	for _, role := range Roles {
		if got := Default(role, FieldPersonaText); got != content.Persona(role) || got == "" {
			t.Errorf("personaText default for %s is not the shipped persona", role)
		}
		if got := Default(role, FieldSkillText); got != content.SkillSection(role) || !strings.HasPrefix(got, "---") {
			t.Errorf("skillText default for %s is not the shipped skill section", role)
		}
	}
	if got := Default(Roles[0], FieldMCPTokenFromEnv); got != setup.TokenEnvName {
		t.Errorf("mcpTokenFromEnv default = %q, want %q", got, setup.TokenEnvName)
	}
	if got := Default(Roles[0], FieldMCPUrl); got != "" {
		t.Errorf("mcpUrl default = %q, want empty", got)
	}
}

// TestSaveRoundTripAcrossReopen edits every field of every role, closes the
// database, and reopens it: what went in must come back — value, dirty flag,
// and the version that produced the default the edit replaced. This is also
// the restart test: the row survives because it lives in the same file the
// wizard's installs do.
func TestSaveRoundTripAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	want := map[string]string{}
	edits := make([]Edit, 0, gridSize())
	for _, role := range Roles {
		for _, field := range Fields {
			if !Applies(role, field) {
				continue
			}
			value := "edited-" + string(role) + "-" + field
			if field == FieldPatrolEnabled {
				value = PatrolOff
			}
			if field == FieldPatrolInterval {
				value = "6h"
			}
			if field == FieldSkillText {
				value = editedSkill(role)
			}
			if field == FieldLLMProvider {
				// The dropdown's list is the write path's rule: the round trip
				// has to use a value the list accepts.
				value = "opencode"
			}
			if field == FieldLLMOpenAIAuthMethod {
				// The same rule for the other dropdown: only a listed method saves.
				value = "ChatGPTSignIn"
			}
			if isLLMKeyField(field) {
				// The key fields are a pass-through: a save carrying one writes
				// no row, so the round trip has nothing to bring back. The
				// pass-through's own contract is TestLLMKeysAreNotStored.
				value = ""
			}
			if field == FieldLLMOpenAIKey {
				// The one pair a batch cannot carry: the sign-in above sends no key,
				// and Save refuses the two together (TestSaveRefusesSignInWithAKeyInThe
				// SameBatch).
				value = ""
			}
			edits = append(edits, Edit{Role: string(role), Field: field, Value: value})
			want[string(role)+"."+field] = value
		}
	}
	if err := s.Save(ctx, edits); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s2.Close() }()

	out, err := s2.All(ctx)
	if err != nil {
		t.Fatalf("All after reopen: %v", err)
	}
	for _, set := range out {
		k := set.Role + "." + set.Field
		if set.Value != want[k] {
			t.Errorf("%s: value = %q…, want the edited value", k, truncate(set.Value))
		}
		if isLLMKeyField(set.Field) {
			// A pass-through never reads back and never reads as edited.
			if set.Value != "" {
				t.Errorf("%s: value = %q after a save that named a key, want empty", k, set.Value)
			}
			if set.Dirty {
				t.Errorf("%s: dirty = true after a save that stores no key", k)
			}
			continue
		}
		if !set.Dirty {
			t.Errorf("%s: dirty = false after an edit", k)
		}
		if set.Default != Default(setup.Role(set.Role), set.Field) {
			t.Errorf("%s: default is no longer the shipped default", k)
		}
		if set.DefaultVersion != PluginVersion() {
			t.Errorf("%s: defaultVersion = %q, want %q", k, set.DefaultVersion, PluginVersion())
		}
	}
}

// TestSettingsShareTheInstallDatabase checks the design's one-file promise:
// the settings table lives in the same SQLite file the store owns, and rows
// written through either handle survive both being reopened.
func TestSettingsShareTheInstallDatabase(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	installs, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	setStore, err := Open(dir)
	if err != nil {
		_ = installs.Close()
		t.Fatalf("settings.Open: %v", err)
	}

	in := setup.Install{Slug: "apps", Org: "acme"}
	if err := installs.Save(ctx, in); err != nil {
		t.Fatalf("store.Save: %v", err)
	}
	edit := Edit{Role: string(setup.RoleRefiner), Field: FieldLLMModel, Value: "gpt-x"}
	if err := setStore.Save(ctx, []Edit{edit}); err != nil {
		t.Fatalf("settings.Save: %v", err)
	}

	_ = installs.Close()
	_ = setStore.Close()

	installs, err = store.Open(dir)
	if err != nil {
		t.Fatalf("store reopen: %v", err)
	}
	defer func() { _ = installs.Close() }()
	setStore, err = Open(dir)
	if err != nil {
		t.Fatalf("settings reopen: %v", err)
	}
	defer func() { _ = setStore.Close() }()

	list, err := installs.List(ctx)
	if err != nil || len(list) != 1 || list[0].Slug != "apps" {
		t.Fatalf("installs after reopen = %v, %v", list, err)
	}
	for _, set := range mustAll(t, setStore) {
		if set.Field == FieldLLMModel && set.Role == string(setup.RoleRefiner) && set.Value != "gpt-x" {
			t.Errorf("refiner llmModel = %q after reopen, want %q", set.Value, "gpt-x")
		}
	}
}

// TestResetReAdoptsTheDefault: an edit marks the field dirty, a reset clears
// the flag and puts the shipped default back in force.
func TestResetReAdoptsTheDefault(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	edit := Edit{Role: string(setup.RoleDeveloper), Field: FieldPersonaText, Value: "mine"}

	if err := s.Save(ctx, []Edit{edit}); err != nil {
		t.Fatalf("Save edit: %v", err)
	}
	set := find(t, s, setup.RoleDeveloper, FieldPersonaText)
	if !set.Dirty || set.Value != "mine" {
		t.Fatalf("after edit: dirty = %v, value = %q", set.Dirty, set.Value)
	}

	if err := s.Save(ctx, []Edit{{Role: edit.Role, Field: edit.Field, Reset: true}}); err != nil {
		t.Fatalf("Save reset: %v", err)
	}
	set = find(t, s, setup.RoleDeveloper, FieldPersonaText)
	if set.Dirty {
		t.Errorf("after reset: dirty = true, want false")
	}
	if want := content.Persona(setup.RoleDeveloper); set.Value != want {
		t.Errorf("after reset: value = %q…, want the shipped default", truncate(set.Value))
	}
	if set.DefaultVersion != PluginVersion() {
		t.Errorf("after reset: defaultVersion = %q, want the running version", set.DefaultVersion)
	}
}

// TestSaveRefusesBadEdits is the validation table. Every refusal names its
// field, and a mixed batch writes nothing at all — the good edit in a refused
// batch must not be there afterwards.
func TestSaveRefusesBadEdits(t *testing.T) {
	ctx := context.Background()
	good := Edit{Role: string(setup.RoleRefiner), Field: FieldLLMModel, Value: "gpt-x"}

	tests := []struct {
		name      string
		bad       Edit
		wantField string // the FieldError's message substring
	}{
		{
			name:      "unknown role",
			bad:       Edit{Role: "janitor", Field: FieldLLMModel, Value: "x"},
			wantField: "unknown role",
		},
		{
			name:      "empty role",
			bad:       Edit{Role: "", Field: FieldLLMModel, Value: "x"},
			wantField: "unknown role",
		},
		{
			name:      "unknown field",
			bad:       Edit{Role: string(setup.RoleRefiner), Field: "soulText", Value: "x"},
			wantField: "unknown field",
		},
		{
			name:      "skill text without frontmatter",
			bad:       Edit{Role: string(setup.RoleRefiner), Field: FieldSkillText, Value: "# no frontmatter"},
			wantField: "frontmatter",
		},
		{
			name: "skill text frontmatter without description",
			bad: Edit{Role: string(setup.RoleRefiner), Field: FieldSkillText,
				Value: "---\nname: forgejo\n---\n# body"},
			wantField: "description",
		},
		{
			name: "skill text frontmatter without name",
			bad: Edit{Role: string(setup.RoleRefiner), Field: FieldSkillText,
				Value: "---\ndescription: d\n---\n# body"},
			wantField: "name",
		},
		{
			name:      "skill text with an unterminated frontmatter",
			bad:       Edit{Role: string(setup.RoleRefiner), Field: FieldSkillText, Value: "---\nname: n\ndescription: d\n# never closed"},
			wantField: "frontmatter",
		},
		{
			name:      "llm provider off the list",
			bad:       Edit{Role: string(setup.RoleRefiner), Field: FieldLLMProvider, Value: "ollama"},
			wantField: "unknown LLM provider",
		},
		{
			name:      "llm provider empty",
			bad:       Edit{Role: string(setup.RoleRefiner), Field: FieldLLMProvider, Value: ""},
			wantField: "unknown LLM provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openStore(t)
			err := s.Save(ctx, []Edit{good, tt.bad})

			var invalid *ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("Save err = %v, want a ValidationError", err)
			}
			if len(invalid.Fields) != 1 {
				t.Fatalf("refused %d fields, want 1: %v", len(invalid.Fields), invalid.Fields)
			}
			f := invalid.Fields[0]
			if f.Role != tt.bad.Role || f.Field != tt.bad.Field {
				t.Errorf("refusal names %s.%s, want %s.%s", f.Role, f.Field, tt.bad.Role, tt.bad.Field)
			}
			if !strings.Contains(f.Error, tt.wantField) {
				t.Errorf("refusal says %q, want it to mention %q", f.Error, tt.wantField)
			}
			// No partial write: the good edit in the same batch is not there.
			set := find(t, s, setup.RoleRefiner, FieldLLMModel)
			if set.Dirty || set.Value != Default(setup.RoleRefiner, FieldLLMModel) {
				t.Errorf("the refused batch still wrote the good edit: dirty = %v, value = %q", set.Dirty, set.Value)
			}
		})
	}
}

// TestSaveRefusesADuplicatedField catches a PUT that names the same field
// twice with different values: which one wins is the admin's to say, not the
// database's to guess.
func TestSaveRefusesADuplicatedField(t *testing.T) {
	s := openStore(t)
	err := s.Save(context.Background(), []Edit{
		{Role: string(setup.RoleRefiner), Field: FieldLLMModel, Value: "a"},
		{Role: string(setup.RoleRefiner), Field: FieldLLMModel, Value: "b"},
	})
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("Save err = %v, want a ValidationError", err)
	}
	if len(invalid.Fields) != 1 || !strings.Contains(invalid.Fields[0].Error, "twice") {
		t.Errorf("refused fields = %v, want one naming the duplicate", invalid.Fields)
	}
}

// TestSaveAcceptsAnEditedSkillText is the other side of the frontmatter rule:
// an edit that keeps it lands.
func TestSaveAcceptsAnEditedSkillText(t *testing.T) {
	s := openStore(t)
	edit := Edit{Role: string(setup.RoleReviewer), Field: FieldSkillText, Value: editedSkill(setup.RoleReviewer)}
	if err := s.Save(context.Background(), []Edit{edit}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	set := find(t, s, setup.RoleReviewer, FieldSkillText)
	if !set.Dirty || set.Value != editedSkill(setup.RoleReviewer) {
		t.Errorf("edited skill did not land: dirty = %v", set.Dirty)
	}
}

// TestSaveAcceptsEveryListedProvider: the dropdown's list is the write path's
// rule, so every listed provider must save — the list and the check can never
// disagree, because they are the same list. `custom` saves only with its URL
// named in the same batch: the batch is the unit, and a custom provider with
// no URL is what the batch rule refuses.
func TestSaveAcceptsEveryListedProvider(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	for _, p := range LLMProviders {
		edits := []Edit{{Role: string(setup.RoleDeveloper), Field: FieldLLMProvider, Value: p.Value}}
		if p.Value == setup.LLMProviderCustom {
			edits = append(edits, Edit{
				Role: string(setup.RoleDeveloper), Field: FieldLLMCustomUrl, Value: "https://llm.example.com/v1",
			})
		}
		if err := s.Save(ctx, edits); err != nil {
			t.Fatalf("Save %q: %v", p.Value, err)
		}
	}
	set := find(t, s, setup.RoleDeveloper, FieldLLMProvider)
	if !set.Dirty || set.Value != LLMProviders[len(LLMProviders)-1].Value {
		t.Errorf("after saving every listed provider: dirty = %v, value = %q", set.Dirty, set.Value)
	}
}

// TestAllServesProviderOptionsOnTheProviderRows: the registry answer carries the
// dropdown only where a dropdown belongs. The llmProvider rows and the OpenAI
// auth-method rows have options, and they carry the list itself when the row's
// value is on it; every
// other row is unchanged — the 3×6 shape the console renders without knowing
// the registry stays exactly as it was.
func TestAllServesProviderOptionsOnTheProviderRows(t *testing.T) {
	s := openStore(t)
	rows := mustAll(t, s)
	if len(rows) != gridSize() {
		t.Fatalf("All answered with %d rows, want %d", len(rows), gridSize())
	}
	for _, set := range rows {
		if set.Field == FieldLLMOpenAIAuthMethod {
			// The other dropdown the registry serves: the OpenAI auth method, with
			// its two listed choices.
			if len(set.Options) != len(LLMAuthMethods) {
				t.Errorf("%s.%s offers %d options, want the %d listed methods",
					set.Role, set.Field, len(set.Options), len(LLMAuthMethods))
			}
			continue
		}
		if set.Field == FieldPatrolEnabled || set.Field == FieldPatrolInterval {
			// The patrol's two dropdowns: on/off, and the plugin's offered cadences.
			want := 2
			if set.Field == FieldPatrolInterval {
				want = len(setup.PatrolIntervals)
			}
			if len(set.Options) != want {
				t.Errorf("%s.%s offers %d options, want %d", set.Role, set.Field, len(set.Options), want)
			}
			continue
		}
		if set.Field != FieldLLMProvider {
			if set.Options != nil {
				t.Errorf("%s.%s carries %d options, want none", set.Role, set.Field, len(set.Options))
			}
			continue
		}
		if len(set.Options) != len(LLMProviders) {
			t.Errorf("%s.llmProvider offers %d options, want the %d listed providers", set.Role, len(set.Options), len(LLMProviders))
			continue
		}
		for i, o := range set.Options {
			if o != LLMProviders[i] {
				t.Errorf("option %d = %+v, want %+v", i, o, LLMProviders[i])
			}
		}
	}
}

// TestAllKeepsAnOffListProviderSelectable covers the row a Save would refuse:
// a store upgraded from an older plugin can hold a provider the list does not
// name. It stays the row's value, and the dropdown carries it as an extra
// option labeled as the current off-list value — visible, selectable, and one
// deliberate choice away of a listed provider.
func TestAllKeepsAnOffListProviderSelectable(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	// Written straight to the table: this is the state a Save refuses to
	// create today, which is exactly why the read path has to survive it.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (role, field, value, dirty, default_version) VALUES (?, ?, ?, 1, ?);`,
		string(setup.RoleRefiner), FieldLLMProvider, "legacy-cloud", PluginVersion()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	set := find(t, s, setup.RoleRefiner, FieldLLMProvider)
	if set.Value != "legacy-cloud" {
		t.Fatalf("off-list row reads back %q, want the stored value", set.Value)
	}
	if len(set.Options) != len(LLMProviders)+1 {
		t.Fatalf("off-list row offers %d options, want the list plus the current value",
			len(set.Options))
	}
	last := set.Options[len(set.Options)-1]
	if last.Value != "legacy-cloud" || !strings.Contains(last.Label, "legacy-cloud") {
		t.Errorf("the extra option is %+v, want the current value labeled as such", last)
	}

	// The admin can still move the row onto the list.
	if err := s.Save(ctx, []Edit{{Role: string(setup.RoleRefiner), Field: FieldLLMProvider, Value: "opencode"}}); err != nil {
		t.Fatalf("moving onto a listed provider: %v", err)
	}
	if set := find(t, s, setup.RoleRefiner, FieldLLMProvider); set.Value != "opencode" || len(set.Options) != len(LLMProviders) {
		t.Errorf("after moving onto the list: value = %q, options = %d", set.Value, len(set.Options))
	}
}

// TestUnknownDefaultAnswersEmpty pins the Default contract for names the
// registry does not have — the checkSave path trusts it not to guess.
func TestUnknownDefaultAnswersEmpty(t *testing.T) {
	if got := Default(setup.Role("janitor"), FieldPersonaText); got != "" {
		t.Errorf("unknown role default = %q, want empty", got)
	}
	if got := Default(setup.RoleRefiner, "soulText"); got != "" {
		t.Errorf("unknown field default = %q, want empty", got)
	}
}

// --- custom provider (issue #54) ---

// TestSaveRefusesCustomProviderWithoutAURL: the dropdown offers `custom`, but
// a role moved onto it with no URL would point its agents at no usable
// endpoint — the save is refused, naming the field to fix.
func TestSaveRefusesCustomProviderWithoutAURL(t *testing.T) {
	s := openStore(t)
	err := s.Save(context.Background(), []Edit{
		{Role: string(setup.RoleDeveloper), Field: FieldLLMProvider, Value: setup.LLMProviderCustom},
	})
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("Save err = %v, want a ValidationError", err)
	}
	if len(invalid.Fields) != 1 || invalid.Fields[0].Field != FieldLLMProvider {
		t.Fatalf("refused fields = %v, want one naming %s", invalid.Fields, FieldLLMProvider)
	}
	if !strings.Contains(invalid.Fields[0].Error, FieldLLMCustomUrl) {
		t.Errorf("refusal says %q, want it to name %s", invalid.Fields[0].Error, FieldLLMCustomUrl)
	}
	// Nothing landed: the provider row still answers the shipped default.
	set := find(t, s, setup.RoleDeveloper, FieldLLMProvider)
	if set.Dirty || set.Value != Default(setup.RoleDeveloper, FieldLLMProvider) {
		t.Errorf("refused save still wrote the provider: dirty = %v, value = %q", set.Dirty, set.Value)
	}
}

// TestSaveAcceptsCustomProviderWithURLInTheSameBatch is the batch contract:
// edits land whole or not at all, so a save that moves the role onto `custom`
// while naming the URL in the same batch is valid — the check reads the
// post-batch effective value, not each edit in isolation.
func TestSaveAcceptsCustomProviderWithURLInTheSameBatch(t *testing.T) {
	s := openStore(t)
	err := s.Save(context.Background(), []Edit{
		{Role: string(setup.RoleDeveloper), Field: FieldLLMProvider, Value: setup.LLMProviderCustom},
		{Role: string(setup.RoleDeveloper), Field: FieldLLMCustomUrl, Value: "https://llm.example.com/v1"},
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if set := find(t, s, setup.RoleDeveloper, FieldLLMProvider); set.Value != setup.LLMProviderCustom {
		t.Errorf("provider = %q, want %s", set.Value, setup.LLMProviderCustom)
	}
	if set := find(t, s, setup.RoleDeveloper, FieldLLMCustomUrl); set.Value != "https://llm.example.com/v1" {
		t.Errorf("URL = %q, want the batch's URL", set.Value)
	}
}

// TestSaveAcceptsCustomProviderWithAStoredURL: the URL may have been saved
// long before the provider moved — the effective value is what counts.
func TestSaveAcceptsCustomProviderWithAStoredURL(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	if err := s.Save(ctx, []Edit{
		{Role: string(setup.RoleDeveloper), Field: FieldLLMCustomUrl, Value: "https://llm.example.com/v1"},
	}); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	if err := s.Save(ctx, []Edit{
		{Role: string(setup.RoleDeveloper), Field: FieldLLMProvider, Value: setup.LLMProviderCustom},
	}); err != nil {
		t.Fatalf("Save with a stored URL: %v", err)
	}
}

// TestSaveRefusesEmptyingTheURLUnderACustomProvider is the rule's other edge:
// a role already on `custom` cannot have its URL emptied — as an edit or as a
// reset, since the reset's effective value is the empty default.
func TestSaveRefusesEmptyingTheURLUnderACustomProvider(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	if err := s.Save(ctx, []Edit{
		{Role: string(setup.RoleDeveloper), Field: FieldLLMCustomUrl, Value: "https://llm.example.com/v1"},
		{Role: string(setup.RoleDeveloper), Field: FieldLLMProvider, Value: setup.LLMProviderCustom},
	}); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	err := s.Save(ctx, []Edit{
		{Role: string(setup.RoleDeveloper), Field: FieldLLMCustomUrl, Value: "  "},
	})
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("emptying Save err = %v, want a ValidationError", err)
	}
	if len(invalid.Fields) != 1 || invalid.Fields[0].Field != FieldLLMCustomUrl {
		t.Fatalf("refused fields = %v, want one naming %s", invalid.Fields, FieldLLMCustomUrl)
	}

	// A reset empties it too — same refusal.
	err = s.Save(ctx, []Edit{
		{Role: string(setup.RoleDeveloper), Field: FieldLLMCustomUrl, Reset: true},
	})
	if !errors.As(err, &invalid) {
		t.Fatalf("resetting Save err = %v, want a ValidationError", err)
	}

	// The stored URL is still there.
	if set := find(t, s, setup.RoleDeveloper, FieldLLMCustomUrl); set.Value != "https://llm.example.com/v1" {
		t.Errorf("URL = %q after refusals, want the stored URL", set.Value)
	}
}

// TestLLMKeysAreNotStored is the key fields' contract now: a save carrying a
// key stores nothing, reads back nothing, and leaves no row — the key's home is
// the hub's per-agent secret, and the plugin keeps no copy. An empty save and a
// reset are equally inert: there is nothing here to keep and nothing to clear.
func TestLLMKeysAreNotStored(t *testing.T) {
	ctx := context.Background()
	for _, field := range []string{FieldLLMCustomKey, FieldLLMOpenAIKey, FieldLLMAnthropicKey, FieldLLMGoogleKey} {
		s := openStore(t)

		if err := s.Save(ctx, []Edit{
			{Role: string(setup.RoleDeveloper), Field: field, Value: "sk-secret"},
		}); err != nil {
			t.Fatalf("%s: Save key: %v", field, err)
		}
		if got, err := s.Effective(ctx, setup.RoleDeveloper, field); err != nil || got != "" {
			t.Errorf("%s: Effective = %q (%v), want empty — the store keeps no key", field, got, err)
		}
		for _, r := range mustAll(t, s) {
			if r.Role == string(setup.RoleDeveloper) && r.Field == field {
				if r.Value != "" || r.Dirty {
					t.Errorf("%s: served row = %q dirty=%v, want an empty unedited row", field, r.Value, r.Dirty)
				}
			}
		}
		if pending, err := s.PendingLLMKeys(ctx); err != nil || len(pending) != 0 {
			t.Errorf("%s: PendingLLMKeys = %v (%v), want none — a save stores no key", field, pending, err)
		}

		// An empty save and a reset store nothing either — and neither errors.
		if err := s.Save(ctx, []Edit{
			{Role: string(setup.RoleDeveloper), Field: field, Value: ""},
		}); err != nil {
			t.Fatalf("%s: empty Save: %v", field, err)
		}
		if err := s.Save(ctx, []Edit{
			{Role: string(setup.RoleDeveloper), Field: field, Reset: true},
		}); err != nil {
			t.Fatalf("%s: reset Save: %v", field, err)
		}
		if got, _ := s.Effective(ctx, setup.RoleDeveloper, field); got != "" {
			t.Errorf("%s: Effective = %q after an empty save and a reset, want empty", field, got)
		}
	}
}

// TestLegacyKeyRowsAreNeitherServedNorEffective is the upgrade's first half: a
// table an older plugin wrote still holds key rows, and no read of the settings
// answers with one — not the console's registry (All), not the apply's or the
// reconciler's reading (Effective). The value is only ever reachable through
// the migration's own raw read.
func TestLegacyKeyRowsAreNeitherServedNorEffective(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()
	if _, err := s.db.Exec(`INSERT INTO settings (role, field, value, dirty, default_version)
		VALUES ('developer', 'llmCustomKey', 'sk-legacy', 1, 'v0.0.0')`); err != nil {
		t.Fatalf("inserting a legacy key row: %v", err)
	}
	_ = s.Close()

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s2.Close() }()

	if got, err := s2.Effective(ctx, setup.RoleDeveloper, FieldLLMCustomKey); err != nil || got != "" {
		t.Errorf("Effective over a legacy key row = %q (%v), want empty", got, err)
	}
	for _, r := range mustAll(t, s2) {
		if r.Field == FieldLLMCustomKey && r.Role == string(setup.RoleDeveloper) && r.Value != "" {
			t.Errorf("All served the legacy key %q", r.Value)
		}
	}
	MaskSecrets(mustAll(t, s2))

	// The migration's read is the one place the stored value is reachable —
	// and the delete behind it is what ends that.
	pending, err := s2.PendingLLMKeys(ctx)
	if err != nil {
		t.Fatalf("PendingLLMKeys: %v", err)
	}
	if len(pending) != 1 || pending[0].Value != "sk-legacy" || pending[0].Role != "developer" {
		t.Fatalf("PendingLLMKeys = %+v, want the one legacy row", pending)
	}
	if err := s2.DeleteLLMKeys(ctx, pending); err != nil {
		t.Fatalf("DeleteLLMKeys: %v", err)
	}
	if pending, err := s2.PendingLLMKeys(ctx); err != nil || len(pending) != 0 {
		t.Errorf("PendingLLMKeys after the delete = %v (%v), want none", pending, err)
	}
}

// TestDeleteLLMKeysRemovesOnlyWhatTheMigrationForwarded is the delete's
// per-field-per-role rule: a key the hub refused keeps its row and is retried
// on the next start, and a row for another role or field is never touched.
func TestDeleteLLMKeysRemovesOnlyWhatTheMigrationForwarded(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()
	if _, err := s.db.Exec(`INSERT INTO settings (role, field, value, dirty, default_version) VALUES
		('developer', 'llmCustomKey', 'sk-dev', 1, 'v0.0.0'),
		('reviewer', 'llmCustomKey', 'sk-rev', 1, 'v0.0.0'),
		('developer', 'llmOpenAIKey', 'sk-oai', 1, 'v0.0.0')`); err != nil {
		t.Fatalf("inserting legacy key rows: %v", err)
	}
	_ = s.Close()

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s2.Close() }()

	if err := s2.DeleteLLMKeys(ctx, []PendingLLMKey{
		{Role: "developer", Field: FieldLLMCustomKey},
		{Role: "developer", Field: FieldLLMOpenAIKey},
	}); err != nil {
		t.Fatalf("DeleteLLMKeys: %v", err)
	}
	pending, err := s2.PendingLLMKeys(ctx)
	if err != nil {
		t.Fatalf("PendingLLMKeys: %v", err)
	}
	if len(pending) != 1 || pending[0].Role != "reviewer" || pending[0].Value != "sk-rev" {
		t.Errorf("PendingLLMKeys = %+v, want only the reviewer's row kept", pending)
	}
}

// TestAdoptDefaultsLeavesKeyRowsForTheMigration is the migration's second
// half: the startup adoption re-adopts stale rows toward the shipped default,
// and a stored key row is not its to move — the migration reads the row as the
// upgrade stored it, and adoption must not blank the value before it forwards.
func TestAdoptDefaultsLeavesKeyRowsForTheMigration(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()
	if _, err := s.db.Exec(`INSERT INTO settings (role, field, value, dirty, default_version)
		VALUES ('developer', 'llmAnthropicKey', 'sk-legacy', 1, 'v0.0.0')`); err != nil {
		t.Fatalf("inserting a legacy key row: %v", err)
	}
	_ = s.Close()

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s2.Close() }()

	if _, err := s2.AdoptDefaults(ctx); err != nil {
		t.Fatalf("AdoptDefaults: %v", err)
	}
	pending, err := s2.PendingLLMKeys(ctx)
	if err != nil {
		t.Fatalf("PendingLLMKeys: %v", err)
	}
	if len(pending) != 1 || pending[0].Value != "sk-legacy" {
		t.Errorf("PendingLLMKeys = %+v, want the key row untouched by adoption", pending)
	}
}

// TestSaveRefusesTwoCustomRolesInRoleOrder: the refusal list walks the shipped
// role order, not the map's — two roles failing the URL rule arrive the same
// way every time, so the console's refusal list and any golden answer on it
// are stable.
func TestSaveRefusesTwoCustomRolesInRoleOrder(t *testing.T) {
	s := openStore(t)
	err := s.Save(context.Background(), []Edit{
		{Role: string(setup.RoleReviewer), Field: FieldLLMProvider, Value: setup.LLMProviderCustom},
		{Role: string(setup.RoleRefiner), Field: FieldLLMProvider, Value: setup.LLMProviderCustom},
	})
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("Save err = %v, want a ValidationError", err)
	}
	if len(invalid.Fields) != 2 {
		t.Fatalf("refused fields = %v, want two", invalid.Fields)
	}
	if invalid.Fields[0].Role != string(setup.RoleRefiner) || invalid.Fields[1].Role != string(setup.RoleReviewer) {
		t.Errorf("refusals = %v, want refiner then reviewer (the shipped role order)", invalid.Fields)
	}
}

// TestCustomFieldsDefaultToEmpty: nothing ships a custom endpoint — the rows
// start empty, which is what lets the URL rule tell "nobody said" from an edit.
func TestCustomFieldsDefaultToEmpty(t *testing.T) {
	s := openStore(t)
	for _, field := range []string{FieldLLMCustomUrl, FieldLLMCustomKey} {
		set := find(t, s, setup.RoleRefiner, field)
		if set.Value != "" || set.Dirty {
			t.Errorf("%s: value = %q, dirty = %v, want empty and untouched", field, set.Value, set.Dirty)
		}
	}
}

// -- helpers -----------------------------------------------------------------

// mustAll reads the whole set, failing the test rather than returning an
// error to ignore.
func mustAll(t *testing.T, s *Store) []Setting {
	t.Helper()
	out, err := s.All(context.Background())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	return out
}

// find reads one (role, field) out of the whole set.
func find(t *testing.T, s *Store, role setup.Role, field string) Setting {
	t.Helper()
	for _, set := range mustAll(t, s) {
		if set.Role == string(role) && set.Field == field {
			return set
		}
	}
	t.Fatalf("All did not include %s.%s", role, field)
	return Setting{}
}

// truncate keeps a long text out of a failure message.
func truncate(s string) string {
	if len(s) > 40 {
		return s[:40]
	}
	return s
}

// --- upgrade auto-adoption ---

// staleEveryRow rewinds every stored row's default_version to an old plugin
// version — the state a running store is in the moment an upgrade ships new
// defaults. Rows are seeded first (un-dirty, at the running version), since
// Open writes nothing until a Save does; tests add their own edits before
// calling, and the rewind moves everything, edited rows included.
// gridSize is how many rows the registry answers with: every role × every field the role has.
func gridSize() int {
	n := 0
	for _, role := range Roles {
		for _, field := range Fields {
			if Applies(role, field) {
				n++
			}
		}
	}
	return n
}

func staleEveryRow(t *testing.T, s *Store, version string) {
	t.Helper()
	ctx := context.Background()
	var edits []Edit
	for _, role := range Roles {
		for _, field := range Fields {
			if !Applies(role, field) {
				continue
			}
			edits = append(edits, Edit{Role: string(role), Field: field, Reset: true})
		}
	}
	if err := s.Save(ctx, edits); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE settings SET default_version = ?;`, version); err != nil {
		t.Fatalf("rewind: %v", err)
	}
}

// TestAdoptDefaultsMovesUntouchedStaleRows is the upgrade's winner rule for
// fields nobody edited: value and version both move to what this plugin ships,
// and the answer names what moved. A value the old version shipped, stored
// un-dirty, is no one's opinion — it goes.
func TestAdoptDefaultsMovesUntouchedStaleRows(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	// One field an admin did edit, so it can be seen refusing to move. Done
	// before the rewind, so its row carries the old version like the rest.
	if err := s.Save(ctx, []Edit{{Role: "refiner", Field: FieldLLMModel, Value: "mine"}}); err != nil {
		t.Fatalf("edit Save: %v", err)
	}
	staleEveryRow(t, s, "v0.0.1")
	// The rewind moved the edited row's version too; its value stays the admin's.
	if err := s.Save(ctx, []Edit{{Role: "refiner", Field: FieldLLMModel, Value: "mine"}}); err != nil {
		t.Fatalf("edit Save again: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE settings SET default_version = ? WHERE dirty = 1;`, "v0.0.1"); err != nil {
		t.Fatalf("rewind edited: %v", err)
	}

	// A value the old version shipped, sitting un-dirty in a row: what an
	// upgrade replaces, whatever it says.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE settings SET value = ? WHERE role = ? AND field = ? AND dirty = 0;`,
		"old shipped text", "developer", FieldSkillText); err != nil {
		t.Fatalf("stale value: %v", err)
	}

	adopted, err := s.AdoptDefaults(ctx)
	if err != nil {
		t.Fatalf("AdoptDefaults: %v", err)
	}

	total := gridSize() - 4*len(Roles)
	if len(adopted) != total-1 {
		t.Fatalf("adopted %d rows, want %d (everything but the edited one, and but the key rows — those are the migration's, not adoption's)", len(adopted), total-1)
	}
	// The registry's order: the first entry is the first field of the first role.
	if adopted[0].Role != string(Roles[0]) || adopted[0].Field != Fields[0] {
		t.Errorf("first adopted = %s.%s, want %s.%s", adopted[0].Role, adopted[0].Field, Roles[0], Fields[0])
	}
	for _, a := range adopted {
		if a.Role == "refiner" && a.Field == FieldLLMModel {
			t.Errorf("the edited field was adopted")
		}
	}

	// The moved rows now say the shipped default at the running version —
	// in the database, not only in the read.
	for _, role := range Roles {
		for _, field := range Fields {
			if !Applies(role, field) {
				continue
			}
			if role == "refiner" && field == FieldLLMModel {
				continue
			}
			if isLLMKeyField(field) {
				// A stored key row is the migration's to read and delete, never
				// adoption's to blank out before the forward.
				continue
			}
			var value, version string
			var dirty int
			err := s.db.QueryRowContext(ctx,
				`SELECT value, default_version, dirty FROM settings WHERE role = ? AND field = ?;`,
				string(role), field).Scan(&value, &version, &dirty)
			if err != nil {
				t.Fatalf("read %s.%s: %v", role, field, err)
			}
			if version != PluginVersion() {
				t.Errorf("%s.%s default_version = %q, want %q", role, field, version, PluginVersion())
			}
			if field == FieldSkillText && role == "developer" {
				if value != Default(role, field) {
					t.Errorf("developer skillText value stayed %q", value)
				}
			}
		}
	}

	// Reading after the adoption still says the edited field is the admin's.
	all, err := s.All(ctx)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	for _, st := range all {
		if st.Role == "refiner" && st.Field == FieldLLMModel {
			if !st.Dirty || st.Value != "mine" {
				t.Errorf("edited field after adoption: %+v", st)
			}
		}
	}
}

// TestAdoptDefaultsPinsTheEditedField is the second half of the winner rule,
// and the reason a dirty row's version is not bookkeeping: an admin's edit
// keeps its value AND its old default_version, because that version is what
// the console diffs the shipped default against to say "the new version has
// new text for a field you edited". Moving it would erase the very signal the
// diff exists to show.
func TestAdoptDefaultsPinsTheEditedField(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if err := s.Save(ctx, []Edit{{Role: "developer", Field: FieldPersonaText, Value: "the admin's persona"}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	staleEveryRow(t, s, "v0.9.0")
	// staleEveryRow seeded un-dirty rows over the edit; re-apply it, pinned to
	// the old version, the way it stood before the upgrade.
	if err := s.Save(ctx, []Edit{{Role: "developer", Field: FieldPersonaText, Value: "the admin's persona"}}); err != nil {
		t.Fatalf("Save again: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE settings SET default_version = ? WHERE dirty = 1;`, "v0.9.0"); err != nil {
		t.Fatalf("pin edited row: %v", err)
	}

	if _, err := s.AdoptDefaults(ctx); err != nil {
		t.Fatalf("AdoptDefaults: %v", err)
	}

	var value, version string
	if err := s.db.QueryRowContext(ctx,
		`SELECT value, default_version FROM settings WHERE role = ? AND field = ?;`,
		"developer", FieldPersonaText).Scan(&value, &version); err != nil {
		t.Fatalf("read: %v", err)
	}
	if value != "the admin's persona" {
		t.Errorf("edited value moved: %q", value)
	}
	if version != "v0.9.0" {
		t.Errorf("edited row's default_version moved to %q; it pins the version the edit was made against", version)
	}

	// And the read still shows the diff the console draws: the admin's value,
	// the new default, and the version gap between them.
	all, _ := s.All(ctx)
	for _, st := range all {
		if st.Role == "developer" && st.Field == FieldPersonaText {
			if st.Value != "the admin's persona" {
				t.Errorf("All: value = %q", st.Value)
			}
			if st.Default != Default(setup.Role("developer"), FieldPersonaText) {
				t.Errorf("All: default is not the shipped one")
			}
			if st.DefaultVersion != "v0.9.0" {
				t.Errorf("All: defaultVersion = %q, want the pinned v0.9.0", st.DefaultVersion)
			}
		}
	}
}

// TestAdoptDefaultsIsANoOpWhenNothingStale: the common case — a store already
// at the running version, or never written — moves nothing and says nothing.
func TestAdoptDefaultsIsANoOpWhenNothingStale(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	adopted, err := s.AdoptDefaults(ctx)
	if err != nil {
		t.Fatalf("AdoptDefaults: %v", err)
	}
	if len(adopted) != 0 {
		t.Fatalf("adopted %d rows from an empty store", len(adopted))
	}

	if err := s.Save(ctx, []Edit{{Role: "refiner", Field: FieldLLMModel, Value: "x"}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	adopted, err = s.AdoptDefaults(ctx)
	if err != nil {
		t.Fatalf("AdoptDefaults: %v", err)
	}
	if len(adopted) != 0 {
		t.Fatalf("adopted %d rows at the current version: %+v", len(adopted), adopted)
	}
}

// --- OpenAI auth method (issue #65) ---

// TestSaveRefusesSignInWithAKeyInTheSameBatch is the hub's rule, enforced where
// the admin can still fix it: an agent cannot be given an API key and the ChatGPT
// sign-in at once, so a batch that names both is refused, naming the method row.
func TestSaveRefusesSignInWithAKeyInTheSameBatch(t *testing.T) {
	s := openStore(t)
	err := s.Save(context.Background(), []Edit{
		{Role: string(setup.RoleDeveloper), Field: FieldLLMOpenAIKey, Value: "sk-both"},
		{Role: string(setup.RoleDeveloper), Field: FieldLLMOpenAIAuthMethod, Value: setup.LLMAuthMethodChatGPTSignIn},
	})
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("Save err = %v, want a ValidationError", err)
	}
	if len(invalid.Fields) != 1 || invalid.Fields[0].Field != FieldLLMOpenAIAuthMethod {
		t.Fatalf("refused fields = %v, want one naming %s", invalid.Fields, FieldLLMOpenAIAuthMethod)
	}
	if !strings.Contains(invalid.Fields[0].Error, FieldLLMOpenAIKey) {
		t.Errorf("refusal says %q, want it to name %s", invalid.Fields[0].Error, FieldLLMOpenAIKey)
	}
	// Nothing landed — a refused batch is a whole batch that never happened.
	for _, field := range []string{FieldLLMOpenAIKey, FieldLLMOpenAIAuthMethod} {
		set := find(t, s, setup.RoleDeveloper, field)
		if set.Dirty || set.Value != Default(setup.RoleDeveloper, field) {
			t.Errorf("refused save still wrote %s: dirty = %v, value = %q", field, set.Dirty, set.Value)
		}
	}
}

// TestSaveAcceptsSignInWithAnEmptyKeyRow: an empty key row is "keep what is
// stored", not a key the batch carries — so it does not collide with the sign-in.
// A key stored under a role that has long since signed in is inert, not refused:
// clearing it is what a Reset does.
func TestSaveAcceptsSignInWithAnEmptyKeyRow(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	if err := s.Save(ctx, []Edit{
		{Role: string(setup.RoleDeveloper), Field: FieldLLMOpenAIKey, Value: "sk-stored"},
	}); err != nil {
		t.Fatalf("storing a key: %v", err)
	}
	if err := s.Save(ctx, []Edit{
		{Role: string(setup.RoleDeveloper), Field: FieldLLMOpenAIAuthMethod, Value: setup.LLMAuthMethodChatGPTSignIn},
		{Role: string(setup.RoleDeveloper), Field: FieldLLMOpenAIKey, Value: ""},
	}); err != nil {
		t.Fatalf("Save: %v (an empty key row is no key in the batch)", err)
	}
	set := find(t, s, setup.RoleDeveloper, FieldLLMOpenAIAuthMethod)
	if set.Value != setup.LLMAuthMethodChatGPTSignIn {
		t.Errorf("auth method = %q, want the sign-in", set.Value)
	}
}

// TestSaveRefusesTwoSignInRolesInRoleOrder: the refusal list is deterministic,
// the same way the custom provider's is.
func TestSaveRefusesTwoSignInRolesInRoleOrder(t *testing.T) {
	s := openStore(t)
	err := s.Save(context.Background(), []Edit{
		{Role: string(setup.RoleReviewer), Field: FieldLLMOpenAIKey, Value: "sk-r"},
		{Role: string(setup.RoleReviewer), Field: FieldLLMOpenAIAuthMethod, Value: setup.LLMAuthMethodChatGPTSignIn},
		{Role: string(setup.RoleRefiner), Field: FieldLLMOpenAIKey, Value: "sk-f"},
		{Role: string(setup.RoleRefiner), Field: FieldLLMOpenAIAuthMethod, Value: setup.LLMAuthMethodChatGPTSignIn},
	})
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("Save err = %v, want a ValidationError", err)
	}
	if len(invalid.Fields) != 2 {
		t.Fatalf("refused fields = %v, want two", invalid.Fields)
	}
	if invalid.Fields[0].Role != string(setup.RoleRefiner) || invalid.Fields[1].Role != string(setup.RoleReviewer) {
		t.Errorf("refused roles = %q, %q, want refiner then reviewer",
			invalid.Fields[0].Role, invalid.Fields[1].Role)
	}
}

// TestSaveRefusesAnUnknownAuthMethod: the dropdown's list is the write path's rule
// too — the value goes to the hub as openAI.authMethod, and a string the hub does
// not know would surface as a refused agent write instead.
func TestSaveRefusesAnUnknownAuthMethod(t *testing.T) {
	s := openStore(t)
	err := s.Save(context.Background(), []Edit{
		{Role: string(setup.RoleDeveloper), Field: FieldLLMOpenAIAuthMethod, Value: "ChatGTP"},
	})
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("Save err = %v, want a ValidationError", err)
	}
	if len(invalid.Fields) != 1 || !strings.Contains(invalid.Fields[0].Error, setup.LLMAuthMethodChatGPTSignIn) {
		t.Fatalf("refused fields = %v, want one naming the two listed methods", invalid.Fields)
	}
}

// TestAuthMethodDefaultIsTheAPIKey: an untouched row answers the method the hub
// holds when nobody chose one, so a fresh install writes exactly what it wrote
// before the dropdown existed.
func TestAuthMethodDefaultIsTheAPIKey(t *testing.T) {
	if got := Default(setup.RoleDeveloper, FieldLLMOpenAIAuthMethod); got != setup.LLMAuthMethodAPIKey {
		t.Errorf("default = %q, want %q", got, setup.LLMAuthMethodAPIKey)
	}
	s := openStore(t)
	set := find(t, s, setup.RoleDeveloper, FieldLLMOpenAIAuthMethod)
	if set.Dirty || set.Value != setup.LLMAuthMethodAPIKey {
		t.Errorf("untouched row = %q (dirty %v), want the API key, un-dirty", set.Value, set.Dirty)
	}
	if len(set.Options) != len(LLMAuthMethods) {
		t.Errorf("row offers %d options, want the %d listed methods", len(set.Options), len(LLMAuthMethods))
	}
	if set.Options[0].Value != setup.LLMAuthMethodAPIKey {
		t.Errorf("first option = %q, want the default to lead the list", set.Options[0].Value)
	}
}

// TestPatrolFieldsExistForTheDeveloperAlone: the patrol is one plugin-wide setting held on the
// developer role, so the registry answers it for that role only, with the shipped defaults.
func TestPatrolFieldsExistForTheDeveloperAlone(t *testing.T) {
	s := openStore(t)
	for _, set := range mustAll(t, s) {
		if set.Field != FieldPatrolEnabled && set.Field != FieldPatrolInterval {
			continue
		}
		if set.Role != string(setup.RoleDeveloper) {
			t.Errorf("%s.%s is answered; the patrol is the developer's alone", set.Role, set.Field)
		}
	}
	ctx := context.Background()
	if v, err := s.Effective(ctx, setup.RoleDeveloper, FieldPatrolEnabled); err != nil || v != PatrolOn {
		t.Errorf("patrolEnabled default = %q, %v, want %q", v, err, PatrolOn)
	}
	if v, err := s.Effective(ctx, setup.RoleDeveloper, FieldPatrolInterval); err != nil || v != "30m" {
		t.Errorf("patrolInterval default = %q, %v, want 30m", v, err)
	}
}

// TestSaveRefusesAPatrolOutsideTenMinutesToADay: the write path holds the list the dropdown
// offers — nothing under ten minutes, nothing over a day, nothing off the even beats — and
// refuses the patrol on a role that has none.
func TestSaveRefusesAPatrolOutsideTenMinutesToADay(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	for _, bad := range []string{"1m", "5m", "9m", "45m", "5h", "25h", "48h", "", "often"} {
		err := s.Save(ctx, []Edit{{Role: "developer", Field: FieldPatrolInterval, Value: bad}})
		var verr *ValidationError
		if !errors.As(err, &verr) {
			t.Errorf("Save(patrolInterval=%q) = %v, want a refusal", bad, err)
		}
	}
	for _, good := range setup.PatrolIntervals {
		if err := s.Save(ctx, []Edit{{Role: "developer", Field: FieldPatrolInterval, Value: good}}); err != nil {
			t.Errorf("Save(patrolInterval=%q): %v", good, err)
		}
	}
	if err := s.Save(ctx, []Edit{{Role: "developer", Field: FieldPatrolEnabled, Value: "maybe"}}); err == nil {
		t.Error("Save(patrolEnabled=maybe) = nil, want a refusal")
	}
	for _, role := range []string{"refiner", "reviewer"} {
		if err := s.Save(ctx, []Edit{{Role: role, Field: FieldPatrolInterval, Value: "1h"}}); err == nil {
			t.Errorf("Save(%s.patrolInterval) = nil, want a refusal: only the developer has a patrol", role)
		}
	}
}
