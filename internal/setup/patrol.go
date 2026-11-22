package setup

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// The developer's scheduled PR patrol is one plugin-wide setting, not a choice each
// install makes: its cadence and whether it runs live in the Settings page (the developer
// role's patrolInterval and patrolEnabled), and the reconcile pass writes them onto every
// install's patrol trigger. This file holds what those two settings are made of — the
// cadences on offer and the cron expression each one means — and PatrolSpec, the pair the
// pass hands to CronTriggers.

// PatrolIntervals are the cadences the plugin offers, smallest first, and they are the
// validation: a value outside this list is refused rather than rendered. Nothing is
// offered below ten minutes — every fire is a model call, and a patrol that wakes the
// developer more often than that is noise — and a day is the longest: a patrol that
// sleeps longer than that is not patrolling.
//
// Every minute value divides 60 and every hour value divides 24, and that is not a
// matter of taste. A step field in a cron expression counts from zero *within its unit*
// and resets at the top of it, so `*/45 * * * *` fires at :00 and :45 and then starts
// over — a 45-minute cadence that is really a 45, 15, 45, 15 pattern, an irregular
// loop wearing a normal-looking expression. The same trap sits at the top of the day
// for hours, which is why 5h and 7h are not offered either.
var PatrolIntervals = []string{"10m", "12m", "15m", "20m", "30m", "1h", "2h", "3h", "4h", "6h", "8h", "12h", "24h"}

// DefaultPatrolInterval is the cadence the patrol runs at until an admin picks another:
// the same "*/30 * * * *" the plugin has always shipped.
const DefaultPatrolInterval = "30m"

// PatrolSpec is the patrol the plugin wants on an install: whether the trigger is
// allowed to fire, and the cron expression it fires on. It is read from the settings by
// the reconcile pass and never stored on the install row.
type PatrolSpec struct {
	Enabled  bool
	Schedule string
}

// DefaultPatrolSpec is the patrol an untouched plugin runs: on, every 30 minutes. A new
// install files its trigger with it; the first reconcile pass moves the trigger onto
// whatever the settings say.
func DefaultPatrolSpec() PatrolSpec {
	return PatrolSpec{Enabled: true, Schedule: CronPatrolSchedule}
}

// PatrolSchedule renders one offered interval as the cron expression it means:
// `*/N * * * *` for minutes, `0 */N * * *` for hours, and `0 0 * * *` for a day. An
// interval outside PatrolIntervals is refused with the list in the message, because the
// refusal and the offer are the same rule and cannot be allowed to drift apart.
func PatrolSchedule(interval string) (string, error) {
	if !slices.Contains(PatrolIntervals, interval) {
		return "", fmt.Errorf("patrol interval %q is not one of %s", interval, strings.Join(PatrolIntervals, ", "))
	}
	n, unit, err := splitInterval(interval)
	if err != nil {
		return "", err
	}
	if unit == "h" {
		// A step of 24 hours is a step that never repeats inside the day, and a step of one
		// is no step: both are what a reader recognises on the hub's Schedules UI as the
		// plain expression.
		switch n {
		case 1:
			return "0 * * * *", nil
		case 24:
			return "0 0 * * *", nil
		}
		return fmt.Sprintf("0 */%d * * *", n), nil
	}
	return fmt.Sprintf("*/%d * * * *", n), nil
}

// splitInterval reads an interval into its number and its unit.
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
