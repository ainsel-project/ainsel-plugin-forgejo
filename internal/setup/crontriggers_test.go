package setup

import (
	"strings"
	"testing"
)

func TestCronTriggersShape(t *testing.T) {
	in := Install{
		Slug: "apps",
		Roles: map[Role]RoleState{
			RoleRefiner:   {AgentID: "a-refiner"},
			RoleDeveloper: {AgentID: "a-developer"},
			RoleReviewer:  {AgentID: "a-reviewer"},
		},
	}
	got := CronTriggers(in, DefaultPatrolSpec())
	if len(got) != 1 {
		t.Fatalf("got %d cron triggers, want exactly 1", len(got))
	}
	ct := got[0]
	if ct.Name != "forgejo-developer-pr-patrol-apps" {
		t.Errorf("name = %q, want forgejo-developer-pr-patrol-apps", ct.Name)
	}
	if ct.AgentRef != "a-developer" {
		t.Errorf("agentRef = %q, want the developer agent", ct.AgentRef)
	}
	if ct.Schedule != "*/30 * * * *" {
		t.Errorf("schedule = %q, want */30 * * * *", ct.Schedule)
	}
	if !ct.Enabled {
		t.Error("enabled = false, want the patrol on")
	}
}

// TestCronPatrolPromptIsSelfContainedAndIdempotent: the hub fires the prompt
// verbatim with no event context, so it must name the whole job — and the dedupe
// rule is what keeps a 48-fires-a-day loop from becoming comment spam.
func TestCronPatrolPromptIsSelfContainedAndIdempotent(t *testing.T) {
	p := CronPatrolPrompt
	for _, want := range []string{
		"open pull requests",
		"at most two repositories per run",
		"review",
		"workflow",
		"merge conflict",
		"read the pull's existing comments",
		"post nothing",
		"end the turn silently",
	} {
		if !strings.Contains(strings.ToLower(p), strings.ToLower(want)) {
			t.Errorf("prompt does not mention %q: %s", want, p)
		}
	}
}

func TestCronPatrolName(t *testing.T) {
	if got := CronPatrolName("apps"); got != "forgejo-developer-pr-patrol-apps" {
		t.Errorf("CronPatrolName(apps) = %q", got)
	}
}

func TestCronMissingMatchesByName(t *testing.T) {
	want := CronTriggers(Install{
		Slug:  "apps",
		Roles: map[Role]RoleState{RoleDeveloper: {AgentID: "a-developer"}},
	}, DefaultPatrolSpec())

	// Nothing existing: everything is missing.
	if got := CronMissing(want, nil); len(got) != 1 {
		t.Errorf("CronMissing(want, nil) = %d, want 1", len(got))
	}

	// Same name existing: nothing is missing — even with a different schedule and
	// prompt, which is an admin's retune, not drift.
	existing := []CronTrigger{{
		ID: "c-9", Name: "forgejo-developer-pr-patrol-apps",
		AgentRef: "a-developer", Schedule: "0 2 * * *", Prompt: "Something else.",
		Enabled: true,
	}}
	if got := CronMissing(want, existing); len(got) != 0 {
		t.Errorf("CronMissing over a retuned patrol = %d, want 0 (name match, retune is not drift)", len(got))
	}

	// A different name is a different trigger: still missing.
	other := []CronTrigger{{ID: "c-1", Name: "forgejo-developer-pr-patrol-other", Enabled: true}}
	if got := CronMissing(want, other); len(got) != 1 {
		t.Errorf("CronMissing over another install's patrol = %d, want 1", len(got))
	}

	// Idempotent on re-run: the same inputs answer the same way twice.
	if got := CronMissing(want, existing); len(got) != 0 {
		t.Errorf("second CronMissing = %d, want 0", len(got))
	}
}

// TestCronTriggersCarriesTheSpec: the patrol is the plugin-wide setting's, so the trigger is
// built from the spec it is handed — its cadence, and whether it may fire. A spec that is off
// still files the trigger, disabled, so it stays on the hub's Schedules UI.
func TestCronTriggersCarriesTheSpec(t *testing.T) {
	in := Install{Slug: "apps", Roles: map[Role]RoleState{RoleDeveloper: {AgentID: "a-developer"}}}

	got := CronTriggers(in, PatrolSpec{Enabled: false, Schedule: "0 */6 * * *"})
	if len(got) != 1 {
		t.Fatalf("got %d cron triggers, want 1", len(got))
	}
	if got[0].Schedule != "0 */6 * * *" || got[0].Enabled {
		t.Errorf("trigger = %+v, want a disabled patrol on 0 */6 * * *", got[0])
	}

	// An empty schedule is the shipped one, never an empty string the hub would refuse.
	if got := CronTriggers(in, PatrolSpec{Enabled: true}); got[0].Schedule != CronPatrolSchedule {
		t.Errorf("schedule = %q, want the shipped %q", got[0].Schedule, CronPatrolSchedule)
	}
}
