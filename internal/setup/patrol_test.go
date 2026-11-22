package setup

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// setFormPatrol and setRowPatrol are the patrol's pair of setFormHours and
// setRowHours: the form is what the admin chose, the row is what the wizard wrote last,
// and the pair of them is the whole diff. Both take a map's value, which Go will not let
// a test do to a struct field directly.
func setFormPatrol(in Input, r Role, p *PatrolInput) {
	ri := in.Roles[r]
	ri.Patrol = p
	in.Roles[r] = ri
}

func setRowPatrol(inst *Install, r Role, p *Patrol) {
	rs := inst.Roles[r]
	rs.Patrol = p
	inst.Roles[r] = rs
}

// TestPatrolScheduleRendersEveryOfferedInterval is the renderer and the validation
// reading from the same list: what is offered is what renders, and what is not offered is
// refused with the list in the message. The refused case is the one that matters — a step
// that does not divide its unit looks normal and is not.
func TestPatrolScheduleRendersEveryOfferedInterval(t *testing.T) {
	for _, tc := range []struct {
		interval string
		want     string
	}{
		{"1m", "* * * * *"},
		{"5m", "*/5 * * * *"},
		{"30m", "*/30 * * * *"},
		{"1h", "0 * * * *"},
		{"2h", "0 */2 * * *"},
		{"12h", "0 */12 * * *"},
	} {
		got, err := PatrolSchedule(tc.interval)
		if err != nil {
			t.Errorf("PatrolSchedule(%q): %v", tc.interval, err)
			continue
		}
		if got != tc.want {
			t.Errorf("PatrolSchedule(%q) = %q, want %q", tc.interval, got, tc.want)
		}
		// The reading back has to be the interval it came in as, or the wizard cannot
		// prefill the chip and the plan cannot name the cadence.
		if text := patrolCadenceText(got); text != "every "+tc.interval {
			t.Errorf("cadence text of %q = %q, want %q", got, text, "every "+tc.interval)
		}
		if !slices.Contains(PatrolIntervals, tc.interval) {
			t.Errorf("interval %q is rendered but not offered", tc.interval)
		}
	}

	// 45 does not divide 60: `*/45 * * * *` fires at :00 and :45 and resets, which is a
	// 45/15 loop wearing a normal-looking expression. It is not on the list, so it is
	// refused rather than rendered.
	for _, bad := range []string{"45m", "7h", "90m", "0m", "30", "", "sunny"} {
		if _, err := PatrolSchedule(bad); err == nil {
			t.Errorf("PatrolSchedule(%q) rendered, want it refused — it is not offered", bad)
		} else if !strings.Contains(err.Error(), "1m") {
			t.Errorf("PatrolSchedule(%q) error = %q, want it to name what is offered", bad, err)
		}
	}
}

// TestValidatePatrol is the plan-time guard. A cadence the plugin does not offer is
// refused; an expression is only checked for being shaped like one, because the hub owns
// cron's grammar and validates the real thing on write.
func TestValidatePatrol(t *testing.T) {
	for _, tc := range []struct {
		name  string
		in    *PatrolInput
		block string
	}{
		{"nothing said", nil, ""},
		{"an offered interval", &PatrolInput{Enabled: on(), Interval: "2h"}, ""},
		{"a hand-typed expression", &PatrolInput{Enabled: on(), Schedule: "*/45 9-17 * * 1-5"}, ""},
		{"neither spelling keeps the cadence", &PatrolInput{Enabled: off()}, ""},
		{"an interval that is not offered", &PatrolInput{Enabled: on(), Interval: "45m"}, "not one of"},
		{"an expression with four fields", &PatrolInput{Enabled: on(), Schedule: "*/30 * * *"}, "five fields"},
		{"a blank expression", &PatrolInput{Enabled: on(), Schedule: " "}, "five fields"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePatrol(tc.in)
			if tc.block == "" {
				if err != nil {
					t.Errorf("ValidatePatrol(%+v) = %v, want it accepted", tc.in, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.block) {
				t.Errorf("ValidatePatrol(%+v) = %v, want it to say %q", tc.in, err, tc.block)
			}
		})
	}
}

// TestPatrolForKeepsWhatTheFormDidNotSay is the keep-on-omit rule that lets a console
// retune survive: the form's answer rendered over the row's, and nothing else.
func TestPatrolForKeepsWhatTheFormDidNotSay(t *testing.T) {
	retuned := &Patrol{Enabled: true, Schedule: "0 2 * * *"} // the hub row was moved by hand
	for _, tc := range []struct {
		name string
		last *Patrol
		in   *PatrolInput
		want *Patrol
	}{
		{
			// The plan must not invent a default for an edit that never mentioned the
			// patrol: this is the row unchanged, so the diff below it says nothing.
			name: "the form said nothing", last: retuned, in: nil, want: retuned,
		},
		{
			// Toggling the patrol must not retune it, even though the form carries no
			// cadence of its own.
			name: "a toggle keeps the cadence the row holds",
			last: retuned,
			in:   &PatrolInput{Enabled: off()},
			want: &Patrol{Enabled: false, Schedule: "0 2 * * *"},
		},
		{
			// The MCP path's shape: an argument that names only a cadence means "retune it",
			// not "and switch it on". The enablement is the install's answer, not this one's.
			name: "a cadence alone keeps the enablement the row holds",
			last: &Patrol{Enabled: false, Schedule: "0 2 * * *"},
			in:   &PatrolInput{Interval: "2h"},
			want: &Patrol{Enabled: false, Interval: "2h", Schedule: "0 */2 * * *"},
		},
		{
			name: "an interval renders over the row",
			last: &Patrol{Enabled: true, Interval: "30m", Schedule: "*/30 * * * *"},
			in:   &PatrolInput{Enabled: on(), Interval: "6h"},
			want: &Patrol{Enabled: true, Interval: "6h", Schedule: "0 */6 * * *"},
		},
		{
			name: "a hand-typed expression wins over an interval",
			last: nil,
			in:   &PatrolInput{Enabled: on(), Interval: "30m", Schedule: "15 3 * * *"},
			want: &Patrol{Enabled: true, Interval: "30m", Schedule: "15 3 * * *"},
		},
		{
			// An install that never had a patrol and is switched off by name records
			// off with no cadence: PatrolWanted reads that as the shipped one.
			name: "the first answer, off", last: nil, in: &PatrolInput{Enabled: off()},
			want: &Patrol{Enabled: false},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := patrolFor(tc.last, tc.in)
			if got != nil && tc.want != nil && *got != *tc.want {
				t.Errorf("patrolFor = %+v, want %+v", *got, *tc.want)
			}
			if got == nil && tc.want != nil {
				t.Errorf("patrolFor = nil, want %+v", *tc.want)
			}
		})
	}
}

// TestCronPatrolDiffIsAgainstTheRow is the diff's contract with the apply: which half
// moved decides what the hub is told, and an install that never named a patrol is already
// at the shipped state — so the row written before this option existed produces no op.
func TestCronPatrolDiffIsAgainstTheRow(t *testing.T) {
	for _, tc := range []struct {
		name     string
		last     *Patrol
		in       *PatrolInput
		changed  bool
		schedule *string
		enabled  *bool
	}{
		{
			name:    "a row that never had one, and a form that says nothing",
			last:    nil,
			in:      nil,
			changed: false,
		},
		{
			name:    "the shipped cadence written out is the shipped cadence",
			last:    &Patrol{Enabled: true, Interval: "30m", Schedule: "*/30 * * * *"},
			in:      nil,
			changed: false,
		},
		{
			name:    "switched off",
			last:    nil,
			in:      &PatrolInput{Enabled: off()},
			changed: true,
			enabled: boolPtr(false),
		},
		{
			name:     "a new cadence",
			last:     nil,
			in:       &PatrolInput{Enabled: on(), Interval: "2h"},
			changed:  true,
			schedule: strPtr("0 */2 * * *"),
		},
		{
			name:    "the same cadence spelled by the chip",
			last:    &Patrol{Enabled: true, Interval: "30m", Schedule: "*/30 * * * *"},
			in:      &PatrolInput{Enabled: on(), Interval: "30m"},
			changed: false,
		},
		{
			// The same shape at the diff's level: retuning a patrol the install switched off
			// moves its schedule and says nothing about the enablement, so the row stays off.
			name:     "a retune of a patrol that is off leaves it off",
			last:     &Patrol{Enabled: false, Interval: "30m", Schedule: "*/30 * * * *"},
			in:       &PatrolInput{Interval: "2h"},
			changed:  true,
			schedule: strPtr("0 */2 * * *"),
		},
		{
			// Both halves at once: the admin moved the tick and the cadence.
			name:     "off and slower",
			last:     &Patrol{Enabled: true, Interval: "30m", Schedule: "*/30 * * * *"},
			in:       &PatrolInput{Enabled: off(), Interval: "12h"},
			changed:  true,
			schedule: strPtr("0 */12 * * *"),
			enabled:  boolPtr(false),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := CronPatrolDiff(tc.last, tc.in)
			if got.Changed != tc.changed {
				t.Fatalf("changed = %v, want %v (%+v -> %+v)", got.Changed, tc.changed, got.From, got.To)
			}
			if !tc.changed {
				if got.Schedule != nil || got.Enabled != nil {
					t.Errorf("diff carries writes %+v, want neither half", got)
				}
				return
			}
			if (got.Schedule == nil) != (tc.schedule == nil) || (got.Schedule != nil && *got.Schedule != *tc.schedule) {
				t.Errorf("schedule = %v, want %v", ptrText(got.Schedule), ptrText(tc.schedule))
			}
			if (got.Enabled == nil) != (tc.enabled == nil) || (got.Enabled != nil && *got.Enabled != *tc.enabled) {
				t.Errorf("enabled = %v, want %v", ptrText(got.Enabled), ptrText(tc.enabled))
			}
		})
	}
}

// on and off are the patrol's two answers, written short because a form field that means
// "the admin moved the tick box" is a pointer and every test case needs one.
func on() *bool  { return new(true) }
func off() *bool { return new(false) }

func boolPtr(b bool) *bool    { return &b }
func strPtr(s string) *string { return &s }

func ptrText[T any](p *T) string {
	if p == nil {
		return "<nil>"
	}
	return fmt.Sprint(*p)
}

// TestCronTriggersFollowsTheRow is what the plan's preview and the re-apply loop both
// read: the shipped set for an install that never chose, nothing at all for one switched
// off, and the row's own cadence for one that was retuned through the wizard.
func TestCronTriggersFollowsTheRow(t *testing.T) {
	base := Install{
		Slug:  "apps",
		Roles: map[Role]RoleState{RoleDeveloper: {AgentID: "a-developer"}},
	}

	t.Run("an install that never chose", func(t *testing.T) {
		got := CronTriggers(base)
		if len(got) != 1 {
			t.Fatalf("cron triggers = %d, want 1", len(got))
		}
		if got[0].Name != "forgejo-developer-pr-patrol-apps" || got[0].Schedule != CronPatrolSchedule || !got[0].Enabled {
			t.Errorf("cron trigger = %+v, want the shipped patrol", got[0])
		}
	})

	t.Run("one the wizard switched off", func(t *testing.T) {
		in := base
		in.Roles = map[Role]RoleState{RoleDeveloper: {AgentID: "a-developer", Patrol: &Patrol{Enabled: false, Schedule: CronPatrolSchedule}}}
		if got := CronTriggers(in); len(got) != 0 {
			t.Errorf("cron triggers = %+v, want none — a patrol the row says is off is not wanted", got)
		}
	})

	t.Run("one on a cadence the wizard set", func(t *testing.T) {
		in := base
		in.Roles = map[Role]RoleState{RoleDeveloper: {AgentID: "a-developer", Patrol: &Patrol{Enabled: true, Interval: "2h", Schedule: "0 */2 * * *"}}}
		got := CronTriggers(in)
		if len(got) != 1 || got[0].Schedule != "0 */2 * * *" {
			t.Errorf("cron triggers = %+v, want the patrol every 2 hours", got)
		}
	})
}

// TestPlanPatrolDiffIsAgainstTheRow mirrors the working-hours test two files over: the
// baseline is the row, not the hub, so a prefilled form writes nothing and a patrol the
// wizard never named is never written.
func TestPlanPatrolDiffIsAgainstTheRow(t *testing.T) {
	const field = FieldPatrol
	patrolOp := func(detail string) Op {
		return Op{Kind: KindCronTrigger, Name: CronPatrolName("apps"), Detail: detail, Role: RoleDeveloper, Field: field}
	}

	t.Run("prefilled and untouched changes nothing", func(t *testing.T) {
		in, snap := editBaseline()
		row := &Patrol{Enabled: true, Interval: "2h", Schedule: "0 */2 * * *"}
		setRowPatrol(snap.Existing, RoleDeveloper, row)
		setFormPatrol(in, RoleDeveloper, &PatrolInput{Enabled: on(), Interval: "2h"})
		got := Plan(in, snap)

		if len(got.Verdict.Blocking) != 0 {
			t.Fatalf("blocking = %q, want none", got.Verdict.Blocking)
		}
		for _, op := range got.Change {
			if op.Field == field {
				t.Errorf("change = %s, want no patrol op: the form says what the row records", opsString(got.Change))
			}
		}
	})

	t.Run("a patrol the wizard never named is never written", func(t *testing.T) {
		// The row records nothing, which is both "the wizard never set a patrol" and what
		// a row written before this field says. The hub may hold a cadence an admin set on
		// the Schedules UI; the plan cannot see it, so it cannot write over it.
		in, snap := editBaseline()
		got := Plan(in, snap)

		for _, op := range got.Change {
			if op.Field == field {
				t.Errorf("change = %s, want no patrol op: nothing was set in the wizard", opsString(got.Change))
			}
		}
	})

	t.Run("switching it off is an op", func(t *testing.T) {
		in, snap := editBaseline()
		setFormPatrol(in, RoleDeveloper, &PatrolInput{Enabled: off()})
		got := Plan(in, snap)

		want := patrolOp(`patrol: "on, every 30m" -> "off, every 30m"`)
		if !slices.Contains(got.Change, want) {
			t.Errorf("change = %s, want it to contain %s", opsString(got.Change), want)
		}
	})

	t.Run("a moved cadence is an op", func(t *testing.T) {
		in, snap := editBaseline()
		setFormPatrol(in, RoleDeveloper, &PatrolInput{Enabled: on(), Interval: "2h"})
		got := Plan(in, snap)

		want := patrolOp(`patrol: "on, every 30m" -> "on, every 2h"`)
		if !slices.Contains(got.Change, want) {
			t.Errorf("change = %s, want it to contain %s", opsString(got.Change), want)
		}
	})

	t.Run("a cadence the console moved is not fought", func(t *testing.T) {
		// The row says the wizard last wrote every 2 hours; the hub's row has since been
		// retuned to 02:00 by hand. The plan has no reading of the hub's row at all, which
		// is the whole of why this writes nothing.
		in, snap := editBaseline()
		setRowPatrol(snap.Existing, RoleDeveloper, &Patrol{Enabled: true, Interval: "2h", Schedule: "0 */2 * * *"})
		setFormPatrol(in, RoleDeveloper, &PatrolInput{Enabled: on(), Interval: "2h"})
		got := Plan(in, snap)

		for _, op := range got.Change {
			if op.Field == field {
				t.Errorf("change = %s, want no patrol op", opsString(got.Change))
			}
		}
	})
}

// TestPlanPatrolOffTakesThePatrolOutOfThePlan is the preview's half: an install created
// with the patrol switched off does not preview a patrol, and the plan's wanted set — which
// the apply creates from and the re-apply loop generates from — holds nothing.
func TestPlanPatrolOffTakesThePatrolOutOfThePlan(t *testing.T) {
	t.Run("a create that switched it off", func(t *testing.T) {
		in, snap := planInput(t)
		setFormPatrol(in, RoleDeveloper, &PatrolInput{Enabled: off()})
		got := Plan(in, snap)

		if len(got.Verdict.Blocking) != 0 {
			t.Fatalf("blocking = %q, want none", got.Verdict.Blocking)
		}
		if len(got.CronTriggers) != 0 {
			t.Errorf("cron triggers = %+v, want none", got.CronTriggers)
		}
		for _, op := range got.Create {
			if op.Kind == KindCronTrigger {
				t.Errorf("create = %s, want no cron trigger op", opsString(got.Create))
			}
		}
	})

	t.Run("a create that left it alone", func(t *testing.T) {
		in, snap := planInput(t)
		got := Plan(in, snap)

		if len(got.CronTriggers) != 1 || got.CronTriggers[0].Schedule != CronPatrolSchedule {
			t.Errorf("cron triggers = %+v, want the shipped patrol", got.CronTriggers)
		}
		var found bool
		for _, op := range got.Create {
			if op.Kind == KindCronTrigger && strings.Contains(op.Detail, "schedule "+CronPatrolSchedule) {
				found = true
			}
		}
		if !found {
			t.Errorf("create = %s, want a cron trigger op naming the schedule", opsString(got.Create))
		}
	})

	t.Run("an edit that switched it off", func(t *testing.T) {
		in, snap := editBaseline()
		setFormPatrol(in, RoleDeveloper, &PatrolInput{Enabled: off()})
		got := Plan(in, snap)

		if len(got.CronTriggers) != 0 {
			t.Errorf("cron triggers = %+v, want none — the wanted set follows the row", got.CronTriggers)
		}
	})
}

// TestPlanPatrolBlocksAndWarns: a cadence the plugin does not offer is the hub's 400
// arriving mid-apply, so it blocks; a cadence it does offer is merely expensive, and every
// fire is a model call, so the shortest ones come with the bill attached.
func TestPlanPatrolBlocksAndWarns(t *testing.T) {
	for _, tc := range []struct {
		name   string
		in     *PatrolInput
		block  string
		warn   string
		nowarn string
	}{
		{
			name:  "an interval that is not offered",
			in:    &PatrolInput{Enabled: on(), Interval: "45m"},
			block: `patrol interval "45m" is not one of`,
		},
		{
			name:  "an expression that is not shaped like one",
			in:    &PatrolInput{Enabled: on(), Schedule: "every 30 minutes"},
			block: "five fields",
		},
		{
			name: "the shortest cadence says what it costs",
			in:   &PatrolInput{Enabled: on(), Interval: "1m"},
			warn: "1440 model calls a day",
		},
		{
			name:   "an expensive cadence that is switched off says nothing",
			in:     &PatrolInput{Enabled: off(), Interval: "1m"},
			nowarn: "model calls",
		},
		{
			name:   "the shipped cadence says nothing",
			in:     &PatrolInput{Enabled: on(), Interval: "30m"},
			nowarn: "model calls",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, snap := editBaseline()
			setFormPatrol(in, RoleDeveloper, tc.in)
			got := Plan(in, snap)

			if tc.block != "" {
				var found bool
				for _, b := range got.Verdict.Blocking {
					if strings.Contains(b, tc.block) {
						found = true
					}
				}
				if !found {
					t.Errorf("blocking = %q, want one naming %q", got.Verdict.Blocking, tc.block)
				}
				return
			}
			if len(got.Verdict.Blocking) != 0 {
				t.Fatalf("blocking = %q, want none", got.Verdict.Blocking)
			}
			if tc.warn != "" {
				var found bool
				for _, w := range got.Verdict.Warnings {
					if strings.Contains(w, tc.warn) {
						found = true
					}
				}
				if !found {
					t.Errorf("warnings = %q, want one naming %q", got.Verdict.Warnings, tc.warn)
				}
			}
			if tc.nowarn != "" {
				for _, w := range got.Verdict.Warnings {
					if strings.Contains(w, tc.nowarn) {
						t.Errorf("warnings = %q, want none about %q", got.Verdict.Warnings, tc.nowarn)
					}
				}
			}
		})
	}
}

// TestPlanPatrolOnAnotherRoleIsIgnored: the wizard sends one shape of role card, and the
// two cards with no patrol must not fail a plan over a field they do not configure.
func TestPlanPatrolOnAnotherRoleIsIgnored(t *testing.T) {
	in, snap := editBaseline()
	setFormPatrol(in, RoleReviewer, &PatrolInput{Enabled: on(), Interval: "45m"})
	got := Plan(in, snap)

	for _, b := range got.Verdict.Blocking {
		if strings.Contains(b, "patrol") {
			t.Errorf("blocking = %q, want nothing about the patrol: the reviewer has none", got.Verdict.Blocking)
		}
	}
	for _, op := range got.Change {
		if op.Field == FieldPatrol {
			t.Errorf("change = %s, want no patrol op: only the developer's is read", opsString(got.Change))
		}
	}
}

// -- the apply's half -------------------------------------------------------------------
//
// The patrol tests share one install: the developer's agent exists, and the row carries
// whatever the test says the wizard last wrote. The plan handed to the apply is the one the
// plan itself produces for that pair, spelled out — the apply reads the plan's verdict and
// nothing else, which is the point of these tests.

func patrolHub(cron ...CronTrigger) *fakeHub {
	return &fakeHub{createdIDs: make(map[string]string), cronTriggers: cron}
}

func patrolForgejo() *fakeForgejo {
	return &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}
}

func patrolApply(h *fakeHub, f *fakeForgejo) Apply {
	return Apply{Hub: h, Forgejo: f, Now: time.Now, Rand: bytes.NewReader(make([]byte, 72))}
}

// installedPatrol is an install with a developer agent, on whatever cadence the row says
// the wizard last left it.
func installedPatrol(row *Patrol) *Install {
	prev := newCustomInstall()
	prev.Roles[RoleDeveloper] = RoleState{AgentID: "agent-2", BotLogin: "developer-bot"}
	setRowPatrol(prev, RoleDeveloper, row)
	return prev
}

// editPatrol is the edit the patrol tests share: the form's answer, and the plan's verdict
// that the patrol field moved.
func editPatrol(p *PatrolInput, row *Patrol) (Input, Result, *Install) {
	in := Input{
		Slug: "test", GroupID: "group-1", BaseURL: "http://forgejo.example.com",
		Org: "testorg", AdminToken: "admin-token",
		Roles: map[Role]RoleInput{RoleDeveloper: {Patrol: p}},
	}
	plan := Result{Change: []Op{{
		Kind: KindCronTrigger, Name: CronPatrolName("test"),
		Detail: "patrol", Role: RoleDeveloper, Field: FieldPatrol,
	}}}
	return in, plan, installedPatrol(row)
}

func patrolRowHub() []CronTrigger {
	return []CronTrigger{{
		ID: "cron-1", Name: CronPatrolName("test"), AgentRef: "agent-2",
		Schedule: "0 2 * * *", Prompt: "A prompt an admin edited in the console.", Enabled: true,
	}}
}

// TestEditInstallPatrolOffDisablesTheRow is the whole of the disable half: the hub's row is
// switched off in place, its schedule and its prompt are left where they are, and the
// install's own row records the switch.
func TestEditInstallPatrolOffDisablesTheRow(t *testing.T) {
	h := patrolHub(patrolRowHub()...)
	in, plan, prev := editPatrol(&PatrolInput{Enabled: off()}, &Patrol{Enabled: true, Interval: "2h", Schedule: "0 2 * * *"})

	got, _, err := patrolApply(h, patrolForgejo()).Run(context.Background(), in, plan, prev)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(h.cronTriggerUpdates) != 1 {
		t.Fatalf("UpdateCronTrigger called %d times, want 1", len(h.cronTriggerUpdates))
	}
	u := h.cronTriggerUpdates[0]
	if u.id != "cron-1" {
		t.Errorf("update addressed to %q, want the id the list carried", u.id)
	}
	if u.enabled == nil || *u.enabled {
		t.Errorf("enabled = %v, want it written false", u.enabled)
	}
	// The cadence is the one thing the admin did not move. Sending the wizard's default
	// here would quietly undo a schedule the console was retuned to.
	if u.schedule != nil {
		t.Errorf("schedule = %q, want nothing: only the enablement moved", *u.schedule)
	}
	if u.prompt != "A prompt an admin edited in the console." {
		t.Errorf("prompt = %q, want the hub's own — a patrol write is not a prompt write", u.prompt)
	}
	if len(h.cronTriggerCreates) != 0 {
		t.Errorf("created %d cron triggers, want none", len(h.cronTriggerCreates))
	}
	row := got.Roles[RoleDeveloper].Patrol
	if row == nil || row.Enabled {
		t.Fatalf("row patrol = %+v, want it to record the switch", row)
	}
	if row.Schedule != "0 2 * * *" {
		t.Errorf("row schedule = %q, want the cadence kept", row.Schedule)
	}
}

// TestEditInstallPatrolCadenceMovesOnlyTheSchedule is the other half: a moved cadence
// writes the schedule and leaves the enablement alone, so a patrol that was running keeps
// running and nothing else on the row moves.
func TestEditInstallPatrolCadenceMovesOnlyTheSchedule(t *testing.T) {
	h := patrolHub(CronTrigger{
		ID: "cron-1", Name: CronPatrolName("test"), AgentRef: "agent-2",
		Schedule: "*/30 * * * *", Prompt: CronPatrolPrompt, Enabled: true,
	})
	in, plan, prev := editPatrol(&PatrolInput{Enabled: on(), Interval: "6h"}, nil)

	got, _, err := patrolApply(h, patrolForgejo()).Run(context.Background(), in, plan, prev)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(h.cronTriggerUpdates) != 1 {
		t.Fatalf("UpdateCronTrigger called %d times, want 1", len(h.cronTriggerUpdates))
	}
	u := h.cronTriggerUpdates[0]
	if u.schedule == nil || *u.schedule != "0 */6 * * *" {
		t.Errorf("schedule = %v, want the rendered cadence", u.schedule)
	}
	if u.enabled != nil {
		t.Errorf("enabled = %v, want nothing: the patrol was already on", *u.enabled)
	}
	if row := got.Roles[RoleDeveloper].Patrol; row == nil || row.Schedule != "0 */6 * * *" || row.Interval != "6h" {
		t.Errorf("row patrol = %+v, want the cadence and the chip both recorded", row)
	}
}

// TestEditInstallSaysNothingAboutThePatrolWithoutAnOp is the rule the whole feature rests
// on, at the apply's end: a re-apply whose plan has no patrol op does not read the hub's
// patrol or write it. That is what leaves a schedule and a prompt an admin edited on the
// hub's Schedules UI alone through every other change.
func TestEditInstallSaysNothingAboutThePatrolWithoutAnOp(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  *Patrol
		form *PatrolInput
	}{
		{"the wizard never set one", nil, nil},
		{"the form still says what the row records", &Patrol{Enabled: true, Interval: "2h", Schedule: "0 */2 * * *"}, &PatrolInput{Enabled: on(), Interval: "2h"}},
		// A plan is what decides, not the form: a form that disagrees with the row and a
		// plan that never noticed says nothing, rather than writing on its own initiative.
		{"the plan says nothing, so the form is ignored", &Patrol{Enabled: true, Schedule: "*/30 * * * *"}, &PatrolInput{Enabled: off()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := patrolHub(patrolRowHub()...)
			in, _, prev := editPatrol(tc.form, tc.row)
			// The plan's only op is about the agent's name — nothing about the patrol.
			plan := Result{Change: []Op{{
				Kind: KindAgent, Name: AgentName(RoleDeveloper, "test"),
				Detail: `name: "forgejo-developer-test" -> "The Developer"`,
				Role:   RoleDeveloper, Field: FieldAgentName,
			}}}

			got, _, err := patrolApply(h, patrolForgejo()).Run(context.Background(), in, plan, prev)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			for _, call := range h.calls {
				if call == "ListCronTriggers" || call == "UpdateCronTrigger" || call == "CreateCronTrigger" {
					t.Errorf("apply called %s, want the patrol left entirely alone", call)
				}
			}
			if tc.row == nil {
				if row := got.Roles[RoleDeveloper].Patrol; row != nil {
					t.Errorf("row patrol = %+v, want the row left as it was", row)
				}
			}
		})
	}
}

// TestEditInstallPatrolMissingIsCreated covers the install whose patrol was deleted on the
// hub: an explicit change to the patrol puts the row back, on the cadence the admin asked
// for rather than the shipped one.
func TestEditInstallPatrolMissingIsCreated(t *testing.T) {
	h := patrolHub()
	in, plan, prev := editPatrol(&PatrolInput{Enabled: on(), Interval: "2h"}, nil)

	if _, _, err := patrolApply(h, patrolForgejo()).Run(context.Background(), in, plan, prev); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(h.cronTriggerCreates) != 1 {
		t.Fatalf("created %d cron triggers, want 1", len(h.cronTriggerCreates))
	}
	ct := h.cronTriggerCreates[0]
	if ct.name != CronPatrolName("test") || ct.schedule != "0 */2 * * *" {
		t.Errorf("created = %+v, want the install's patrol on the cadence asked for", ct)
	}
	if len(h.cronTriggerUpdates) != 0 {
		t.Errorf("updates = %d, want none — there was no row to write", len(h.cronTriggerUpdates))
	}
}

// TestEditInstallPatrolOffWithNoRowFilesNothing is the never-created case: an install that
// never had a patrol and is switched off records the answer and leaves the hub empty. This
// is the state in which re-filing a row the admin just refused would be the bug.
func TestEditInstallPatrolOffWithNoRowFilesNothing(t *testing.T) {
	h := patrolHub()
	in, plan, prev := editPatrol(&PatrolInput{Enabled: off()}, nil)

	got, _, err := patrolApply(h, patrolForgejo()).Run(context.Background(), in, plan, prev)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(h.cronTriggerCreates) != 0 {
		t.Errorf("created %d cron triggers, want none — the admin refused the patrol", len(h.cronTriggerCreates))
	}
	if len(h.cronTriggerUpdates) != 0 {
		t.Errorf("updates = %d, want none — there was no row to switch off", len(h.cronTriggerUpdates))
	}
	if row := got.Roles[RoleDeveloper].Patrol; row == nil || row.Enabled {
		t.Errorf("row patrol = %+v, want the refusal recorded", row)
	}
}

// TestApplyCreateFilesNoPatrolWhenTheFormSwitchedItOff: the create path reads the same
// wanted set the plan previewed, so a patrol switched off at creation is absent from the
// hub and recorded on the row.
func TestApplyCreateFilesNoPatrolWhenTheFormSwitchedItOff(t *testing.T) {
	for _, tc := range []struct {
		name     string
		patrol   *PatrolInput
		wantFile bool
		schedule string
	}{
		{"left alone, it is filed at the shipped cadence", nil, true, CronPatrolSchedule},
		{"switched off, it is not filed at all", &PatrolInput{Enabled: off()}, false, ""},
		{"asked for at 2 hours, it is filed at 2 hours", &PatrolInput{Enabled: on(), Interval: "2h"}, true, "0 */2 * * *"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakeHub{
				createdIDs: make(map[string]string),
				imageTools: []ImageTool{{Name: "tool1", Kind: "forgejo", Enabled: false}},
			}
			in := Input{
				Slug: "test", GroupID: "group-1", BaseURL: "http://forgejo.example.com",
				Org: "testorg", AdminToken: "admin-token",
				Roles: map[Role]RoleInput{
					RoleRefiner:   {Runtime: "image1", Tools: []string{"tool1"}},
					RoleDeveloper: {Runtime: "image1", Tools: []string{"tool1"}, Patrol: tc.patrol},
					RoleReviewer:  {Runtime: "image1", Tools: []string{"tool1"}},
				},
			}

			got, _, err := patrolApply(h, patrolForgejo()).Run(context.Background(), in, Result{}, nil)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !tc.wantFile {
				if len(h.cronTriggerCreates) != 0 {
					t.Fatalf("created %+v, want no patrol at all", h.cronTriggerCreates)
				}
			} else {
				if len(h.cronTriggerCreates) != 1 {
					t.Fatalf("created %d cron triggers, want 1", len(h.cronTriggerCreates))
				}
				if h.cronTriggerCreates[0].schedule != tc.schedule {
					t.Errorf("schedule = %q, want %q", h.cronTriggerCreates[0].schedule, tc.schedule)
				}
			}

			row := got.Roles[RoleDeveloper].Patrol
			if tc.patrol == nil {
				if row != nil {
					t.Errorf("row patrol = %+v, want nil: the wizard wrote nothing", row)
				}
				return
			}
			if row == nil || row.Enabled != *tc.patrol.Enabled {
				t.Errorf("row patrol = %+v, want it to record enabled=%v", row, tc.patrol.Enabled)
			}
		})
	}
}
