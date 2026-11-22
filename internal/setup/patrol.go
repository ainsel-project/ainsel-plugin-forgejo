package setup

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Patrol is the developer's scheduled PR patrol as the install row remembers it:
// whether the wizard last left it running, and at what cadence. It is the same
// kind of value as WorkingHours and TurnTimeoutMs — the record of what the wizard
// wrote, not a reading of the hub — because the plan diffs the form against *this*
// and never against the live cron trigger. An admin who retunes the patrol on the
// hub's Schedules UI keeps that retune through every re-apply this plugin makes for
// any other reason; only a form the admin actually moved on the patrol writes back.
//
// The cadence is kept twice on purpose. Schedule is the canonical cron expression —
// what the hub was last told, and the only half the plan compares. Interval is the
// wizard's own spelling of the choice ("30m", "2h"), kept so the form can prefill the
// chip the admin picked; an empty Interval with a Schedule set reads as a hand-typed
// expression, which prefills the advanced box instead.
//
// Nil on the row means "the wizard never wrote one", which is also what a row written
// before this option existed says, and both read as today's behaviour: the patrol runs,
// every 30 minutes. PatrolWanted is what turns a nil into that answer.
type Patrol struct {
	// Enabled is whether the patrol is meant to fire at all. False is the hub's
	// `enabled: false` — the row stays on the hub, visible on the Schedules UI with
	// its last and next run, and the hub's emitter skips it. It is never a delete:
	// a deleted patrol has nothing left to re-enable, so turning it back on would
	// re-file the shipped prompt and silently throw away any prompt an admin edited
	// in the console.
	Enabled bool `json:"enabled"`

	// Interval is the wizard's spelling of the cadence, "" for a hand-typed
	// expression. It is display state for the form, never the basis of a diff.
	Interval string `json:"interval,omitempty"`

	// Schedule is the cron expression the install was last told to run the patrol
	// at. It is the canonical value: an empty one reads as CronPatrolSchedule.
	Schedule string `json:"schedule,omitempty"`
}

// PatrolInput is the wizard's answer about the patrol, on the developer's role. Nil is
// "the admin said nothing about the patrol": the plan emits no op for it and the apply
// writes nothing, which is what lets a console retune survive an unrelated re-apply and
// what keeps every existing install's plan byte-identical.
//
// Interval and Schedule are the two spellings of the same choice and the wizard sends
// one of them: Interval is one of PatrolIntervals, Schedule is a hand-written
// expression for anything the list cannot offer. A Schedule that arrives beside an
// Interval wins, because it is the more specific of the two.
// Enabled is a pointer because the caller can mean three things by it. A wizard's tick box
// sends true or false. A caller that only wants the cadence — the MCP path, where
// {"interval": "5m"} reads as "retune it", not as "and switch it off" — leaves it out, and
// the patrol keeps whatever enablement the install row already records.
type PatrolInput struct {
	Enabled  *bool  `json:"enabled,omitempty"`
	Interval string `json:"interval,omitempty"`
	Schedule string `json:"schedule,omitempty"`
}

// PatrolIntervals are the cadences the wizard offers, smallest first, and they are the
// validation: a value outside this list is refused rather than rendered.
//
// Every minute value divides 60 and every hour value divides 24, and that is not a
// matter of taste. A step field in a cron expression counts from zero *within its unit*
// and resets at the top of it, so `*/45 * * * *` fires at :00 and :45 and then starts
// over — a 45-minute cadence that is really a 45, 15, 45, 15 pattern, an irregular
// loop wearing a normal-looking expression. The same trap sits at the top of the day
// for hours, which is why 5h and 7h are not offered either.
var PatrolIntervals = []string{"1m", "2m", "3m", "4m", "5m", "6m", "10m", "12m", "15m", "20m", "30m", "1h", "2h", "3h", "4h", "6h", "8h", "12h"}

// PatrolSchedule renders one offered interval as the cron expression it means:
// `*/N * * * *` for minutes, `0 */N * * *` for hours. An interval outside
// PatrolIntervals is refused with the list in the message, because the refusal and the
// offer are the same rule and cannot be allowed to drift apart.
func PatrolSchedule(interval string) (string, error) {
	if !slices.Contains(PatrolIntervals, interval) {
		return "", fmt.Errorf("patrol interval %q is not one of %s", interval, strings.Join(PatrolIntervals, ", "))
	}
	n, unit, err := splitInterval(interval)
	if err != nil {
		return "", err
	}
	// A step of one is not a step: `*/1` and `*` are the same schedule and the second is
	// what a reader recognises on the hub's Schedules UI.
	if n == 1 {
		if unit == "h" {
			return "0 * * * *", nil
		}
		return "* * * * *", nil
	}
	if unit == "h" {
		return fmt.Sprintf("0 */%d * * *", n), nil
	}
	return fmt.Sprintf("*/%d * * * *", n), nil
}

// splitInterval reads an interval the wizard's way into its number and its unit.
func splitInterval(interval string) (int, string, error) {
	if len(interval) < 2 {
		return 0, "", fmt.Errorf("patrol interval %q is not a number with a unit — use one of %s", interval, strings.Join(PatrolIntervals, ", "))
	}
	unit := interval[len(interval)-1:]
	n, err := strconv.Atoi(interval[:len(interval)-1])
	if err != nil || n <= 0 {
		return 0, "", fmt.Errorf("patrol interval %q is not a number with a unit — use one of %s", interval, strings.Join(PatrolIntervals, ", "))
	}
	return n, unit, nil
}

// ValidatePatrol checks the wizard's answer before anything is written. An interval the
// plugin does not offer is refused here, in the plan, because the MCP path has no wizard
// in front of it and a nonsense cadence is not a preference. A hand-typed expression is
// deliberately not re-parsed: the hub owns cron, validates it on create and on update
// with a 400, and a second grammar in this plugin would only ever disagree with it. What
// is checked here is that the string could be an expression at all — five fields — so a
// blank or half-typed one fails in the preview instead of at the hub mid-apply.
func ValidatePatrol(in *PatrolInput) error {
	if in == nil {
		return nil
	}
	if in.Schedule != "" {
		if fields := strings.Fields(in.Schedule); len(fields) != 5 {
			return fmt.Errorf("patrol schedule %q is not a cron expression — it needs five fields (minute hour day-of-month month day-of-week), or pick an interval instead", in.Schedule)
		}
		return nil
	}
	if in.Interval == "" {
		// Neither spelling: the cadence the install already has, which is what an
		// edit that only touched the tick means.
		return nil
	}
	_, err := PatrolSchedule(in.Interval)
	return err
}

// patrolFor is the row value the wizard's answer leaves behind: the form's answer
// rendered over what the row recorded, or the row's own value when the form said nothing
// at all — the same keep-on-omit rule the turn timeout runs on, and the reason flipping
// the patrol off and back on does not silently retune a cadence.
//
// An empty Interval and an empty Schedule keep the cadence the install already ran at, and
// an absent Enabled keeps whatever the install was running — which is what an edit that moved
// only one of the two means. It is total: an interval the plugin does not offer leaves no
// schedule behind, and the plan that renders this has already blocked on it.
func patrolFor(last *Patrol, in *PatrolInput) *Patrol {
	if in == nil {
		return last
	}
	p := &Patrol{Enabled: last == nil || last.Enabled, Interval: in.Interval}
	if in.Enabled != nil {
		p.Enabled = *in.Enabled
	}
	switch {
	case in.Schedule != "":
		p.Schedule = in.Schedule
	case in.Interval != "":
		if s, err := PatrolSchedule(in.Interval); err == nil {
			p.Schedule = s
		}
	case last != nil && last.Schedule != "":
		p.Schedule = last.Schedule
	}
	return p
}

// PatrolWanted is the effective state of one install row's patrol: a nil row answers
// "on, at the shipped cadence", which is what every install written before this option
// is running and what the wizard prefills for one that never chose.
func PatrolWanted(p *Patrol) Patrol {
	want := Patrol{Enabled: true, Schedule: CronPatrolSchedule}
	if p == nil {
		return want
	}
	want.Enabled = p.Enabled
	want.Interval = p.Interval
	if p.Schedule != "" {
		want.Schedule = p.Schedule
	}
	return want
}

// CronPatrolDiff compares the patrol the wizard now asks for against what the install row
// recorded as last applied, and answers with only the halves that moved. A nil answer says
// nothing at all and moves nothing — it is not "back to the default". It is the
// plan/apply half of the rule the re-apply loop deliberately does not have: the loop
// never compares a live schedule against a desired one, because that is how a console
// retune gets "corrected" away.
//
// It answers the halves rather than one boolean so the apply can PUT only what moved:
// an admin who flipped the patrol off has not asked for its cadence to change, and a
// write that carried both would rewrite a schedule the console set.
func CronPatrolDiff(last *Patrol, in *PatrolInput) PatrolChange {
	from, to := PatrolWanted(last), PatrolWanted(patrolFor(last, in))
	c := PatrolChange{From: from, To: to}
	// Only the two halves the hub is told about. Interval is the wizard's spelling of the
	// same cadence, so a form that says 30m over a row that says nothing and holds */30 has
	// moved nothing, and an op here would preview a write that writes the same schedule.
	if from.Enabled == to.Enabled && from.Schedule == to.Schedule {
		return c
	}
	c.Changed = true
	if from.Schedule != to.Schedule {
		s := to.Schedule
		c.Schedule = &s
	}
	if from.Enabled != to.Enabled {
		e := to.Enabled
		c.Enabled = &e
	}
	return c
}

// PatrolChange is one patrol diff: whether anything moved, and the new value of each
// half that did. Each pointer is nil for the half that stayed, so the apply PUTs only what
// the admin moved.
type PatrolChange struct {
	Changed  bool
	Schedule *string
	Enabled  *bool
	From     Patrol
	To       Patrol
}

// PatrolSummary is the plan's one-line reading of a patrol state: "every 30 minutes",
// or the expression itself when no offered interval says it — which is the honest answer
// for a hand-typed cron, and for the `0 */7 * * *` an admin may have written by hand.
func PatrolSummary(p Patrol) string {
	state := "on"
	if !p.Enabled {
		state = "off"
	}
	return state + ", " + patrolCadenceText(p.Schedule)
}

// patrolCadenceText names a cadence the way an admin would: the wizard's interval when
// one of the offered ones means it, the expression when none does, and the shipped
// default when nothing was ever set.
func patrolCadenceText(schedule string) string {
	if schedule == "" {
		schedule = CronPatrolSchedule
	}
	for _, iv := range PatrolIntervals {
		if s, err := PatrolSchedule(iv); err == nil && s == schedule {
			return "every " + iv
		}
	}
	return "cron " + schedule
}

// patrolIntervalMinutes is how often a cadence fires, in minutes, for the warning the
// plan gives about a very short one. It reads the cadence back through the offered list
// rather than parsing the expression, so a hand-typed one answers false: this plugin does
// not own cron's grammar and will not guess at it.
func patrolIntervalMinutes(schedule string) (int, bool) {
	for _, iv := range PatrolIntervals {
		s, err := PatrolSchedule(iv)
		if err != nil || s != schedule {
			continue
		}
		n, unit, err := splitInterval(iv)
		if err != nil {
			return 0, false
		}
		if unit == "h" {
			return n * 60, true
		}
		return n, true
	}
	return 0, false
}

// minWarnedPatrolMinutes is the cadence below which the plan says something about the
// cost. Every fire is a model call against the install's model, so the shortest thing on
// offer — 1m — is 1 440 wakes a day for one install, and the admin who chose it should
// have read that number somewhere before applying it.
const minWarnedPatrolMinutes = 10
