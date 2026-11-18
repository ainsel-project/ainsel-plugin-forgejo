package setup

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	// The window's zone is validated with time.LoadLocation, and the plugin's image is
	// distroless — no /usr/share/zoneinfo to read. The embedded database is what makes
	// that call answer the same in a container as it does on a dev machine.
	_ "time/tzdata"
)

// WorkingHours is the weekly window one role's agent takes queued work in. It is the
// wizard's shape for the hub's `spec.workingHours`, and the hub's shape on the wire:
// Timezone is an IANA zone name (empty reads as UTC), Days are cron weekday numbers where
// 0 and 7 are both Sunday (empty is every day), and Start and End are HH:MM wall-clock
// bounds in that zone — an End before its Start is one window crossing midnight, so a
// night shift is a single window.
//
// What it is not is a stop. Outside the window the hub publishes no due task, so no task
// job and no pod is created; ingestion and routing keep running and nothing is lost. A
// message that arrives at 23:00 for a 09:00–17:00 agent is answered when the window opens,
// and a task already running when it shuts runs to its end. The wizard's wording keeps to
// that: work waits, it does not get interrupted.
type WorkingHours struct {
	Timezone string `json:"timezone,omitempty"`
	Days     []int  `json:"days,omitempty"`
	Start    string `json:"start,omitempty"`
	End      string `json:"end,omitempty"`
}

// CloneWorkingHours is a copy a row can hold. The install row keeps the window the wizard
// last wrote, and a row must not share a slice with the form that is still being edited.
func CloneWorkingHours(w *WorkingHours) *WorkingHours {
	if w == nil {
		return nil
	}
	out := *w
	out.Days = slices.Clone(w.Days)
	return &out
}

// WorkingHoursEqual says whether two windows are the same window. Two nils are equal —
// "no window" is a value here, not the absence of one — and a nil against a set is not.
// Day lists compare as sets, so [0 1] and [7 1 0] are the same two days.
func WorkingHoursEqual(a, b *WorkingHours) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.Timezone != b.Timezone || a.Start != b.Start || a.End != b.End {
		return false
	}
	return slices.Equal(normalizeDays(a.Days), normalizeDays(b.Days))
}

// hhmmPattern is an HH:MM wall-clock time, the only form the hub's bounds take.
var hhmmPattern = regexp.MustCompile(`^([01]?\d|2[0-3]):[0-5]\d$`)

// ValidateWorkingHours is the plan's check on a window, made before anything is written.
// The hub refuses a window it cannot use with a 400 — an un-loadable zone, a bound that is
// not HH:MM, bounds that leave no window — and a refusal that arrives mid-apply leaves the
// admin with a half-written install. Naming the role at plan time is the cheaper answer.
//
// A nil window is valid: it is the install's "any hour", which is what nothing set means.
func ValidateWorkingHours(w *WorkingHours) error {
	if w == nil {
		return nil
	}
	if w.Timezone != "" {
		if _, err := time.LoadLocation(w.Timezone); err != nil {
			return fmt.Errorf("working hours: timezone %q is not a zone that can be loaded — name an IANA zone like Europe/Berlin, or leave it empty for UTC", w.Timezone)
		}
	}
	if (w.Start == "") != (w.End == "") {
		return fmt.Errorf("working hours: %q and %q are a pair — set both bounds or neither", w.Start, w.End)
	}
	for _, bound := range []string{w.Start, w.End} {
		if bound != "" && !hhmmPattern.MatchString(bound) {
			return fmt.Errorf("working hours: %q is not an HH:MM time", bound)
		}
	}
	if w.Start != "" && w.Start == w.End {
		return fmt.Errorf("working hours: both bounds are %s, which leaves no window at all", w.Start)
	}
	for _, d := range w.Days {
		if d < 0 || d > 7 {
			return fmt.Errorf("working hours: day %d is not a weekday — 0 or 7 is Sunday, 6 is Saturday", d)
		}
	}
	return nil
}

// WorkingHoursText renders a window the way the preview names it: "Mon–Fri 09:00–17:00
// Europe/Berlin", with "every day" for a window that names none and "UTC" for a zone left
// empty. Nil has no text: the caller says "any hours" in its own words.
func WorkingHoursText(w *WorkingHours) string {
	if w == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(daysText(w.Days))
	b.WriteString(" ")
	switch {
	case w.Start != "" && w.End != "":
		b.WriteString(w.Start + "–" + w.End)
	case w.Start != "":
		b.WriteString("from " + w.Start)
	case w.End != "":
		b.WriteString("until " + w.End)
	default:
		b.WriteString("all day")
	}
	zone := w.Timezone
	if zone == "" {
		zone = "UTC"
	}
	b.WriteString(" " + zone)
	return b.String()
}

// dayNames are the weekdays in the cron numbering the window's Days carry: 0 is Sunday.
var dayNames = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

// normalizeDays puts a day list into the form a comparison reads: each day once, 7 folded
// onto Sunday's 0, ascending.
func normalizeDays(days []int) []int {
	out := make([]int, 0, len(days))
	seen := make(map[int]bool, len(days))
	for _, d := range days {
		if d < 0 || d > 7 {
			// A day the hub would refuse is compared as it came in rather than
			// folded into a weekday it is not; the validator is what refuses it.
			out = append(out, d)
			continue
		}
		if d == 7 {
			d = 0
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	sort.Ints(out)
	return out
}

// daysText names a day list: "every day" when it names none or the whole week, "Mon–Fri"
// when it is one run of days, and the names themselves otherwise.
func daysText(days []int) string {
	d := normalizeDays(days)
	if len(d) == 0 || len(d) == 7 {
		return "every day"
	}
	if len(d) > 2 {
		// normalizeDays sorted them, so one step up the list is all a run needs.
		run := true
		for i := 1; i < len(d); i++ {
			if d[i] != d[i-1]+1 {
				run = false
				break
			}
		}
		if run {
			return dayNames[d[0]] + "–" + dayNames[d[len(d)-1]]
		}
	}
	names := make([]string, 0, len(d))
	for _, x := range d {
		if x < 0 || x > 6 {
			names = append(names, fmt.Sprintf("%d", x))
			continue
		}
		names = append(names, dayNames[x])
	}
	return strings.Join(names, ", ")
}
