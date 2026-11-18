package setup

import "testing"

// TestNormalize is issue #85's first half: the copy-paste whitespace comes off before
// anything reads the input, so the runtime that reaches the hub is one a task pod can
// start from. The credential and the hub id are the two fields it must not touch — a
// token's bytes are the answer, and a group id is matched verbatim against the
// caller's own groups.
func TestNormalize(t *testing.T) {
	in := Input{
		Slug:         "  apps\t",
		GroupID:      " g-apps ",
		BaseURL:      "\nhttps://forgejo.example/\n",
		Org:          " acme ",
		AdminToken:   " tok en ",
		ExcludeLabel: " do-not-refine\t",
		Roles: map[Role]RoleInput{
			RoleDeveloper: {
				Runtime:       "dpinsel/ainsel-pi-go:1.24\t",
				AgentName:     " forgejo-developer-apps ",
				TurnTimeoutMs: " 3600000\n",
				Tools:         []string{"tool1"},
			},
		},
	}

	got := Normalize(in)

	if got.Slug != "apps" {
		t.Errorf("Slug = %q, want %q", got.Slug, "apps")
	}
	if got.Org != "acme" {
		t.Errorf("Org = %q, want %q", got.Org, "acme")
	}
	if got.BaseURL != "https://forgejo.example/" {
		t.Errorf("BaseURL = %q, want the address without the newlines", got.BaseURL)
	}
	if got.ExcludeLabel != "do-not-refine" {
		t.Errorf("ExcludeLabel = %q, want %q", got.ExcludeLabel, "do-not-refine")
	}
	// Left alone, on purpose.
	if got.GroupID != " g-apps " {
		t.Errorf("GroupID = %q, want it untouched", got.GroupID)
	}
	if got.AdminToken != " tok en " {
		t.Errorf("AdminToken = %q, want it untouched", got.AdminToken)
	}

	ri := got.Roles[RoleDeveloper]
	if ri.Runtime != "dpinsel/ainsel-pi-go:1.24" {
		t.Errorf("Runtime = %q, want the ref without the tab", ri.Runtime)
	}
	if ri.AgentName != "forgejo-developer-apps" {
		t.Errorf("AgentName = %q, want it trimmed", ri.AgentName)
	}
	if ri.TurnTimeoutMs != "3600000" {
		t.Errorf("TurnTimeoutMs = %q, want it trimmed", ri.TurnTimeoutMs)
	}
	if len(ri.Tools) != 1 || ri.Tools[0] != "tool1" {
		t.Errorf("Tools = %v, want the set as it was", ri.Tools)
	}

	// The caller's own map is not written into: a caller that plans and then applies
	// with the same Input sees no surprise either way.
	if raw := in.Roles[RoleDeveloper]; raw.Runtime != "dpinsel/ainsel-pi-go:1.24\t" {
		t.Errorf("caller's Runtime = %q, want it unchanged", raw.Runtime)
	}

	// Idempotent, so Plan and Apply.Run can each call it without agreeing on who did.
	again := Normalize(got)
	if again.Slug != got.Slug || again.BaseURL != got.BaseURL || again.Org != got.Org ||
		again.ExcludeLabel != got.ExcludeLabel || again.AdminToken != got.AdminToken ||
		again.GroupID != got.GroupID {
		t.Errorf("Normalize is not idempotent: %+v, then %+v", got, again)
	}
	if r2 := again.Roles[RoleDeveloper]; r2.Runtime != ri.Runtime || r2.AgentName != ri.AgentName || r2.TurnTimeoutMs != ri.TurnTimeoutMs {
		t.Errorf("Normalize is not idempotent on the role: %+v, then %+v", ri, r2)
	}
}

// TestNormalizeWhitespaceOnlyIsBlank is the other half of the rule: trimming turns a
// field that holds only whitespace into the field left blank, which every reader here
// already means as "the default" or "keep what the install has".
func TestNormalizeWhitespaceOnlyIsBlank(t *testing.T) {
	in := Input{
		Slug:    "apps",
		GroupID: "g",
		Roles: map[Role]RoleInput{
			RoleRefiner: {Runtime: "\t\n", AgentName: " ", TurnTimeoutMs: " "},
		},
	}

	got := Normalize(in)

	if ri := got.Roles[RoleRefiner]; ri.Runtime != "" || ri.AgentName != "" || ri.TurnTimeoutMs != "" {
		t.Errorf("roles[refiner] = %+v, want the three fields blank", ri)
	}
}

// TestRuntimeHasWhitespace is the syntax rule the block is built on: after trimming,
// only whitespace inside the ref is left to find.
func TestRuntimeHasWhitespace(t *testing.T) {
	for _, tc := range []struct {
		ref  string
		want bool
	}{
		{"dpinsel/ainsel-pi-go:1.24", false},
		{"registry:5000/img@sha256:0000", false},
		{"dpinsel/ainsel pi-go:1.24", true},
		{"img:8\t0", true},
		{"img:8\n0", true},
		{"", false},
	} {
		if got := runtimeHasWhitespace(tc.ref); got != tc.want {
			t.Errorf("runtimeHasWhitespace(%q) = %v, want %v", tc.ref, got, tc.want)
		}
	}
}
