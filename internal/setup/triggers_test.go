package setup

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// The golden file is the trigger set the dev instance actually runs for the
// "apps" install (connector c-724d9d68), exported in AInsel's trigger API JSON
// shape. Everything the generator produces is checked against it, so a change
// to the generated set that would silently diverge from what already works in
// the field has to fail here first.
//
// The file is kept byte-identical to that export — it records what the dev
// instance actually ran before this branch, legacy field name and the dropped
// CI-sender trigger included — so the comparison tests normalise it first
// (goldenNormalized) instead of editing the fixture into what the generator
// emits.
const goldenFile = "testdata/dev-apps-triggers.json"

// ciFailureGoldenName is the export's seventh trigger: the developer's CI
// failure trigger. The CI sender is gone from the model (the spec drops it), so
// it is the one entry the golden set has to lose before it is compared with
// what Triggers returns.
const ciFailureGoldenName = "trigger-developer-ci-failure-apps"

// devInstall is the golden install: slug "apps" on connector c-724d9d68, with
// the three dev bots and their agents in the role states. The agent ids are the
// dev names, which happen to look like ids for the developer.
func devInstall() Install {
	return Install{
		Slug:        "apps",
		ConnectorID: "c-724d9d68",
		Roles: map[Role]RoleState{
			RoleRefiner: {
				AgentID:  "a-refiner",
				BotLogin: "refine-agent",
			},
			RoleDeveloper: {
				AgentID:  "a-14b9fb30",
				BotLogin: "dev-agent",
			},
			RoleReviewer: {
				AgentID:  "a-reviewer",
				BotLogin: "review-agent",
			},
		},
		ExcludeLabel: "do-not-refine",
	}
}

// goldenTriggers reads the exported dev set as it was written. It failing to
// load as []Trigger is itself a failure: it means the file is not in the shape
// the JSON tags on Trigger and Filter promise (ruling R4).
func goldenTriggers(t *testing.T) []Trigger {
	t.Helper()
	raw, err := os.ReadFile(goldenFile)
	if err != nil {
		t.Fatalf("read golden file: %v", err)
	}
	var out []Trigger
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse golden file: %v", err)
	}
	return out
}

// goldenNormalized is the export turned into what the generator is supposed to
// produce, the three steps the live set predates:
//
//   - the CI-sender trigger is dropped — the model has no CI sender any more,
//     so the wanted set is the six of the spec, not the seven that were filed;
//   - every name loses its old "trigger-" prefix for the "forgejo-" one, which
//     is how AInsel-side names are derived from the slug now (R3);
//   - the legacy issue.labels spelling is mapped onto issue.labels.name, the
//     field the generator emits.
//
// Doing this here rather than in the fixture is what keeps the fixture honest
// about what the dev instance actually ran.
func goldenNormalized(t *testing.T) []Trigger {
	t.Helper()

	out := make([]Trigger, 0, len(goldenTriggers(t)))
	for _, tr := range goldenTriggers(t) {
		if tr.Name == ciFailureGoldenName {
			continue
		}
		tr.Name = "forgejo-" + strings.TrimPrefix(tr.Name, "trigger-")
		for j := range tr.Filters {
			if tr.Filters[j].Field == legacyLabelField {
				tr.Filters[j].Field = labelNameField
			}
		}
		out = append(out, tr)
	}
	if len(out) != 6 {
		t.Fatalf("golden set normalises to %d triggers, want 6", len(out))
	}
	return out
}

// copyTriggers deep-copies a trigger list. A Trigger holds a filter slice, so a
// plain copy shares the filters and mutating one copy's filter silently mutates
// the original list as well.
func copyTriggers(ts []Trigger) []Trigger {
	out := make([]Trigger, len(ts))
	for i, tr := range ts {
		out[i] = tr
		out[i].Filters = append([]Filter(nil), tr.Filters...)
	}
	return out
}

// labelFilter picks the label exclusion out of a trigger's filters, or nil when
// it has none.
func labelFilter(tr Trigger) *Filter {
	for i := range tr.Filters {
		if tr.Filters[i].Op == "not-contains" {
			return &tr.Filters[i]
		}
	}
	return nil
}

// TestGoldenHasDevTriggers pins the fixture itself: the export is the seven
// triggers the dev instance ran, under the names the export actually carries,
// two of them excluding by label on the legacy field. It is what makes
// goldenNormalized's three steps meaningful.
func TestGoldenHasDevTriggers(t *testing.T) {
	golden := goldenTriggers(t)
	if len(golden) != 7 {
		t.Fatalf("golden file has %d triggers, want 7", len(golden))
	}
	want := []string{
		"trigger-refiner-issue-opened-apps",
		"trigger-refiner-issue-reopened-apps",
		"trigger-refiner-mention-apps",
		"trigger-reviewer-mention-apps",
		"trigger-reviewer-pr-opened-apps",
		"trigger-developer-mention-apps",
		ciFailureGoldenName,
	}
	got := make(map[string]bool, len(golden))
	for _, tr := range golden {
		got[tr.Name] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("golden file is missing %q", name)
		}
	}

	// Two of them — the refiner's opened and reopened triggers — exclude by
	// label, and the export carries that on the legacy issue.labels field. That
	// is the defect the generator fixes; the file must keep recording it.
	legacy := 0
	for _, tr := range golden {
		if f := labelFilter(tr); f != nil && f.Field == legacyLabelField {
			legacy++
		}
	}
	if legacy != 2 {
		t.Errorf("golden has %d not-contains filters on %q, want 2", legacy, legacyLabelField)
	}
}

// TestTriggersReturnsSix is the set of the spec: exactly six triggers, named
// forgejo-<role>-<what>-<slug>, each pointing at the agent its role has. There
// is no seventh trigger for a CI sender any more — the field is gone from the
// model, so nothing can add one.
func TestTriggersReturnsSix(t *testing.T) {
	in := devInstall()
	got := Triggers(in)

	want := []string{
		"forgejo-refiner-issue-opened-apps",
		"forgejo-refiner-issue-reopened-apps",
		"forgejo-refiner-mention-apps",
		"forgejo-reviewer-pr-opened-apps",
		"forgejo-reviewer-mention-apps",
		"forgejo-developer-mention-apps",
	}
	if len(got) != len(want) {
		t.Fatalf("Triggers returned %d triggers, want %d: %s", len(got), len(want), names(got))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("trigger %d is %q, want %q: %s", i, got[i].Name, name, names(got))
			continue
		}
		// The name says which role it is for, and the agentRef has to agree: a
		// refiner's trigger pointing at the developer's agent would send every
		// new issue to the code-writing agent.
		role := Role(strings.SplitN(name, "-", 3)[1])
		if want := in.Roles[role].AgentID; got[i].AgentRef != want {
			t.Errorf("%s agentRef = %q, want the %s agent %q", name, got[i].AgentRef, role, want)
		}
		if got[i].ConnectorRef != in.ConnectorID {
			t.Errorf("%s connectorRef = %q, want %q", name, got[i].ConnectorRef, in.ConnectorID)
		}
	}
}

// TestLabelFilterMatchesLabelNames pins what the generator emits: the refiner's
// exclusion is on issue.labels.name. Forgejo sends issue.labels as an array of
// label objects and AInsel's matcher stringifies each element, so a not-contains
// on issue.labels compares "map[...]" strings against a label name and excludes
// nothing; the dotted path resolves through the array to every label's name.
func TestLabelFilterMatchesLabelNames(t *testing.T) {
	wantTriggers := []string{
		"forgejo-refiner-issue-opened-apps",
		"forgejo-refiner-issue-reopened-apps",
	}
	seen := 0
	for _, tr := range Triggers(devInstall()) {
		f := labelFilter(tr)
		if f == nil {
			continue
		}
		seen++
		if !slices.Contains(wantTriggers, tr.Name) {
			t.Errorf("%s excludes by label, want only the refiner's issue triggers to", tr.Name)
		}
		if f.Field != labelNameField {
			t.Errorf("%s excludes on field %q, want %q", tr.Name, f.Field, labelNameField)
		}
		if f.Value != "do-not-refine" {
			t.Errorf("%s excludes %q, want %q", tr.Name, f.Value, "do-not-refine")
		}
	}
	if seen != 2 {
		t.Fatalf("generated %d label exclusions, want 2", seen)
	}
}

// TestMissingTreatsLegacyLabelFieldAsCovered is why Missing is forgiving about
// that one field: the live dev triggers filter on issue.labels, and setup run
// against them must recognise them as the trigger it wants rather than add a
// near-duplicate next to a trigger the admin already has.
func TestMissingTreatsLegacyLabelFieldAsCovered(t *testing.T) {
	live := copyTriggers(Triggers(devInstall()))
	rewritten := 0
	for i := range live {
		if f := labelFilter(live[i]); f != nil && f.Field == labelNameField {
			f.Field = legacyLabelField
			rewritten++
		}
	}
	if rewritten != 2 {
		t.Fatalf("rewrote %d label filters to %q, want 2", rewritten, legacyLabelField)
	}

	// All six are there in the legacy spelling, so nothing is missing —
	// including the two whose field name differs from what the generator emits.
	if got := Missing(Triggers(devInstall()), live); len(got) != 0 {
		t.Errorf("Missing vs live legacy-shaped set = %s, want empty", names(got))
	}

	// And it is the field that is forgiven, not the whole filter: a legacy
	// trigger excluding a different label still leaves the wanted one missing.
	other := copyTriggers(live)
	for i := range other {
		if f := labelFilter(other[i]); f != nil {
			f.Value = "wont-fix"
		}
	}
	missing := Missing(Triggers(devInstall()), other)
	if len(missing) != 2 {
		t.Fatalf("Missing vs legacy set excluding %q = %s, want the 2 issue triggers",
			"wont-fix", names(missing))
	}
	for _, tr := range missing {
		if f := labelFilter(tr); f == nil || f.Value != "do-not-refine" {
			t.Errorf("Missing returned %s (%s), want the label exclusions", tr.Name, describeFilters(tr.Filters))
		}
	}
}

// TestTriggersMatchGolden is the whole pattern at once: generated from the dev
// install, the set has to equal the normalised export trigger for trigger and
// filter for filter — order-insensitive on the filters, because the order
// filters were added to a live trigger carries no meaning.
func TestTriggersMatchGolden(t *testing.T) {
	got := Triggers(devInstall())
	want := goldenNormalized(t)

	if len(got) != len(want) {
		t.Fatalf("Triggers returned %d triggers, want %d: %s", len(got), len(want), names(got))
	}
	byName := make(map[string]Trigger, len(got))
	for _, tr := range got {
		if _, dup := byName[tr.Name]; dup {
			t.Fatalf("duplicate trigger name %q in %s", tr.Name, names(got))
		}
		byName[tr.Name] = tr
	}
	for _, w := range want {
		g, ok := byName[w.Name]
		if !ok {
			t.Errorf("missing trigger %q; got %s", w.Name, names(got))
			continue
		}
		if g.AgentRef != w.AgentRef {
			t.Errorf("%s agentRef = %q, want %q", w.Name, g.AgentRef, w.AgentRef)
		}
		if g.ConnectorRef != w.ConnectorRef {
			t.Errorf("%s connectorRef = %q, want %q", w.Name, g.ConnectorRef, w.ConnectorRef)
		}
		if !sameFilters(g.Filters, w.Filters) {
			t.Errorf("%s filters = %s, want %s", w.Name, describeFilters(g.Filters), describeFilters(w.Filters))
		}
	}
}

// TestMissingEmptyWhenInstallMatchesGolden is the idempotence the setup flow
// leans on: an install whose live triggers are still the legacy dev export —
// names, CI-sender trigger and old label field included — has nothing
// missing, so re-running setup creates nothing.
func TestMissingEmptyWhenInstallMatchesGolden(t *testing.T) {
	if got := Missing(Triggers(devInstall()), goldenTriggers(t)); len(got) != 0 {
		t.Errorf("Missing = %s, want empty", names(got))
	}
}

// TestMissingReturnsRemovedTrigger removes one wanted trigger at a time — the
// live instance will drift trigger by trigger, not all at once — and checks
// Missing hands back exactly that one. Names are ignored by Missing, so the
// match is on the (agentRef, connectorRef, filter set) identity: a same-named
// trigger pointing somewhere else still counts as missing.
func TestMissingReturnsRemovedTrigger(t *testing.T) {
	golden := goldenNormalized(t)
	for i, removed := range golden {
		existing := make([]Trigger, 0, len(golden)-1)
		existing = append(existing, golden[:i]...)
		existing = append(existing, golden[i+1:]...)

		missing := Missing(Triggers(devInstall()), existing)
		if len(missing) != 1 {
			t.Fatalf("with %q removed, Missing returned %d triggers, want 1: %s", removed.Name, len(missing), names(missing))
		}
		if missing[0].Name != removed.Name {
			t.Errorf("with %q removed, Missing returned %q", removed.Name, missing[0].Name)
		}
	}
}

// TestMissingIgnoresNames is the other half of the same ruling: an existing
// trigger with the right filters under a different name is not missing, while
// one that merely shares a name but not the filters is.
func TestMissingIgnoresNames(t *testing.T) {
	golden := goldenNormalized(t)
	renamed := copyTriggers(golden)
	renamed[0].Name = "some-older-name"
	if got := Missing(Triggers(devInstall()), renamed); len(got) != 0 {
		t.Errorf("Missing with renamed trigger = %s, want empty", names(got))
	}

	// The same name with one filter changed does not match: the filters decide.
	changed := copyTriggers(golden)
	changed[0].Filters[2].Value = "wont-fix"
	if v := golden[0].Filters[2].Value; v != "do-not-refine" {
		t.Fatalf("mutating the copy changed the original list too: golden filter value is %q", v)
	}
	missing := Missing(Triggers(devInstall()), changed)
	if len(missing) != 1 || missing[0].Name != golden[0].Name {
		t.Errorf("Missing with changed filter = %s, want %q", names(missing), golden[0].Name)
	}
}

// TestExcludeLabelDefaults: an install that never names a label is the install
// that names the default — the refiner is never left watching issues a human
// tried to exclude by leaving the field empty.
func TestExcludeLabelDefaults(t *testing.T) {
	inst := devInstall()
	inst.ExcludeLabel = ""
	if got := Missing(Triggers(inst), goldenNormalized(t)); len(got) != 0 {
		t.Errorf("Triggers with empty ExcludeLabel = %s missing from golden, want none", names(got))
	}
}

// TestTriggersFromAnUnfinishedInstallNamesNothing: an install half way through
// the wizard has no agents and no bots yet. The set is still the six — so the
// plan shows what is coming — but with empty refs, and nothing here panics on
// the missing role states.
func TestTriggersFromAnUnfinishedInstallNamesNothing(t *testing.T) {
	got := Triggers(Install{Slug: "apps", ConnectorID: "c-1"})
	if len(got) != 6 {
		t.Fatalf("Triggers on an empty install returned %d triggers, want 6: %s", len(got), names(got))
	}
	for _, tr := range got {
		if tr.AgentRef != "" {
			t.Errorf("%s agentRef = %q, want empty", tr.Name, tr.AgentRef)
		}
	}
}

// TestJSONShape pins the tags: what the generator marshals must be what
// POST /api/v1/triggers takes — name, agentRef, connectorRef, filters, and on a
// filter field, op, value with values present only when set (ruling R4).
func TestJSONShape(t *testing.T) {
	raw, err := json.Marshal(Trigger{
		Name:         "n",
		AgentRef:     "a",
		ConnectorRef: "c",
		Filters: []Filter{
			{Field: "type", Op: "eq", Value: "issues"},
			{Field: "action", Op: "in", Values: []string{"opened", "reopened"}},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var doc struct {
		Name         string                       `json:"name"`
		AgentRef     string                       `json:"agentRef"`
		ConnectorRef string                       `json:"connectorRef"`
		Filters      []map[string]json.RawMessage `json:"filters"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.Name != "n" || doc.AgentRef != "a" || doc.ConnectorRef != "c" {
		t.Errorf("trigger keys came back as %+v, want name/agentRef/connectorRef", doc)
	}
	if len(doc.Filters) != 2 {
		t.Fatalf("filters came back as %d, want 2: %s", len(doc.Filters), raw)
	}
	for _, key := range []string{"field", "op", "value"} {
		if _, ok := doc.Filters[0][key]; !ok {
			t.Errorf("filter 1 has no %q key: %s", key, raw)
		}
	}
	if _, ok := doc.Filters[0]["values"]; ok {
		t.Errorf("empty values should be omitted, got: %s", raw)
	}
	if _, ok := doc.Filters[1]["values"]; !ok {
		t.Errorf("set filter should carry values, got: %s", raw)
	}
}

// names spells a trigger list out for failure messages.
func names(ts []Trigger) string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name)
	}
	return "[" + joinComma(out) + "]"
}

func joinComma(ss []string) string {
	joined := ""
	for i, s := range ss {
		if i > 0 {
			joined += ", "
		}
		joined += s
	}
	return joined
}

// describeFilters spells a filter set out for failure messages, in the same
// sorted order sameFilters compares it in.
func describeFilters(fs []Filter) string {
	sorted := append([]Filter(nil), fs...)
	sortFilters(sorted)
	out := make([]string, 0, len(sorted))
	for _, f := range sorted {
		out = append(out, f.Field+" "+f.Op+" "+f.Value)
	}
	return "{" + joinComma(out) + "}"
}
