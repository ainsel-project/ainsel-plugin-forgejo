package setup

import (
	"strings"
	"testing"
)

// TestPatrolIntervalsAreTenMinutesToADay: nothing below ten minutes and nothing above a day is
// offered — and the ends of the list are the two the admin asked for.
func TestPatrolIntervalsAreTenMinutesToADay(t *testing.T) {
	if got := PatrolIntervals[0]; got != "10m" {
		t.Errorf("shortest interval = %q, want 10m", got)
	}
	if got := PatrolIntervals[len(PatrolIntervals)-1]; got != "24h" {
		t.Errorf("longest interval = %q, want 24h", got)
	}
	for _, iv := range PatrolIntervals {
		n, unit, err := splitInterval(iv)
		if err != nil {
			t.Errorf("%s: %v", iv, err)
			continue
		}
		minutes := n
		if unit == "h" {
			minutes = n * 60
		}
		if minutes < 10 || minutes > 24*60 {
			t.Errorf("%s is %d minutes, outside 10 minutes to 24 hours", iv, minutes)
		}
	}
}

// TestPatrolScheduleRendersEveryOfferedInterval pins the cron expression each interval means,
// and that every one fires on an even beat: a minute step divides 60 and an hour step divides 24.
func TestPatrolScheduleRendersEveryOfferedInterval(t *testing.T) {
	want := map[string]string{
		"10m": "*/10 * * * *",
		"12m": "*/12 * * * *",
		"15m": "*/15 * * * *",
		"20m": "*/20 * * * *",
		"30m": "*/30 * * * *",
		"1h":  "0 * * * *",
		"2h":  "0 */2 * * *",
		"3h":  "0 */3 * * *",
		"4h":  "0 */4 * * *",
		"6h":  "0 */6 * * *",
		"8h":  "0 */8 * * *",
		"12h": "0 */12 * * *",
		"24h": "0 0 * * *",
	}
	if len(want) != len(PatrolIntervals) {
		t.Fatalf("test covers %d intervals, the list offers %d", len(want), len(PatrolIntervals))
	}
	for _, iv := range PatrolIntervals {
		got, err := PatrolSchedule(iv)
		if err != nil {
			t.Errorf("PatrolSchedule(%q): %v", iv, err)
			continue
		}
		if got != want[iv] {
			t.Errorf("PatrolSchedule(%q) = %q, want %q", iv, got, want[iv])
		}
		n, unit, _ := splitInterval(iv)
		if unit == "m" && 60%n != 0 || unit == "h" && 24%n != 0 {
			t.Errorf("%s does not divide its unit evenly: the cadence would be irregular", iv)
		}
	}
}

// TestPatrolScheduleRefusesWhatIsNotOffered: everything under ten minutes, over a day, off the
// even beats, and malformed is refused with the list in the message.
func TestPatrolScheduleRefusesWhatIsNotOffered(t *testing.T) {
	for _, iv := range []string{"", "1m", "5m", "9m", "45m", "5h", "7h", "25h", "48h", "2d", "30", "m", "-10m", "ten"} {
		_, err := PatrolSchedule(iv)
		if err == nil {
			t.Errorf("PatrolSchedule(%q) = nil error, want a refusal", iv)
			continue
		}
		if !strings.Contains(err.Error(), "10m") || !strings.Contains(err.Error(), "24h") {
			t.Errorf("PatrolSchedule(%q) error %q, want it to name the offered list", iv, err)
		}
	}
}

// TestDefaultPatrolSpecIsTheShippedPatrol: on, every 30 minutes, and that is also the
// default interval the settings carry.
func TestDefaultPatrolSpecIsTheShippedPatrol(t *testing.T) {
	spec := DefaultPatrolSpec()
	if !spec.Enabled || spec.Schedule != CronPatrolSchedule {
		t.Errorf("DefaultPatrolSpec = %+v, want on at %q", spec, CronPatrolSchedule)
	}
	got, err := PatrolSchedule(DefaultPatrolInterval)
	if err != nil || got != CronPatrolSchedule {
		t.Errorf("PatrolSchedule(DefaultPatrolInterval) = %q, %v, want %q", got, err, CronPatrolSchedule)
	}
}
