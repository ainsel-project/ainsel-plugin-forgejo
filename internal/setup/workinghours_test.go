package setup

import "testing"

// The window's text is what the preview shows and what the plan's detail compares, so
// these are the words the admin reads: days named as a run, an unnamed set as every day,
// and an empty zone named as the UTC it means.
func TestWorkingHoursText(t *testing.T) {
	for _, tc := range []struct {
		name string
		w    *WorkingHours
		want string
	}{
		{"none", nil, ""},
		{
			"a working week",
			&WorkingHours{Timezone: "Europe/Berlin", Days: []int{1, 2, 3, 4, 5}, Start: "09:00", End: "17:00"},
			"Mon–Fri 09:00–17:00 Europe/Berlin",
		},
		{
			"no days named is every day",
			&WorkingHours{Start: "09:00", End: "17:00"},
			"every day 09:00–17:00 UTC",
		},
		{
			"the whole week named is every day",
			&WorkingHours{Days: []int{0, 1, 2, 3, 4, 5, 6}, Start: "09:00", End: "17:00"},
			"every day 09:00–17:00 UTC",
		},
		{
			// 7 is Sunday, the same day 0 is — and the run it completes reads as a run.
			"a week written with a 7",
			&WorkingHours{Days: []int{7, 1, 2, 3, 4, 5}, Start: "09:00", End: "17:00"},
			"Sun–Fri 09:00–17:00 UTC",
		},
		{
			"days apart are named one by one",
			&WorkingHours{Days: []int{0, 3, 6}, Start: "09:00", End: "17:00"},
			"Sun, Wed, Sat 09:00–17:00 UTC",
		},
		{
			// A night shift is one window, not two, and the text says it as one.
			"across midnight",
			&WorkingHours{Days: []int{1}, Start: "22:00", End: "06:00"},
			"Mon 22:00–06:00 UTC",
		},
		{
			"one bound is half a window",
			&WorkingHours{Start: "09:00"},
			"every day from 09:00 UTC",
		},
		{
			"no bounds is the whole day",
			&WorkingHours{Days: []int{6}},
			"Sat all day UTC",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := WorkingHoursText(tc.w); got != tc.want {
				t.Errorf("text = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestWorkingHoursEqual is the diff's equality: two nils are the same window, a nil and a
// set are not, and a day list compares as a set so the same week written two ways is one
// week.
func TestWorkingHoursEqual(t *testing.T) {
	week := func(days ...int) *WorkingHours {
		return &WorkingHours{Timezone: "UTC", Days: days, Start: "09:00", End: "17:00"}
	}

	for _, tc := range []struct {
		name string
		a, b *WorkingHours
		want bool
	}{
		{"neither set one", nil, nil, true},
		{"one set it", week(1), nil, false},
		{"the other set it", nil, week(1), false},
		{"same week, same order", week(1, 2), week(1, 2), true},
		{"same week, other order", week(1, 2), week(2, 1), true},
		{"sunday written as 7", week(0, 1), week(7, 1), true},
		{"a repeated day is one day", week(1, 1), week(1), true},
		{"a moved start", &WorkingHours{Start: "09:00", End: "17:00"}, &WorkingHours{Start: "10:00", End: "17:00"}, false},
		{"a moved zone", week(1), &WorkingHours{Timezone: "Europe/Berlin", Days: []int{1}, Start: "09:00", End: "17:00"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := WorkingHoursEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("equal(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
			if got := WorkingHoursEqual(tc.b, tc.a); got != tc.want {
				t.Errorf("equal the other way round = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestValidateWorkingHoursAccepts is what the guard must not refuse: the shapes the hub
// takes, including the ones an admin would otherwise be told to fix.
func TestValidateWorkingHoursAccepts(t *testing.T) {
	for _, tc := range []struct {
		name string
		w    *WorkingHours
	}{
		{"no window", nil},
		{"an empty window is every hour of every day", &WorkingHours{}},
		{"a night shift", &WorkingHours{Start: "22:00", End: "06:00"}},
		{"midnight as a bound", &WorkingHours{Start: "00:00", End: "23:59"}},
		{"sunday as 7", &WorkingHours{Days: []int{7}}},
		{"a named zone", &WorkingHours{Timezone: "America/Argentina/Buenos_Aires", Start: "08:00", End: "16:00"}},
		{"days alone", &WorkingHours{Days: []int{1, 3}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateWorkingHours(tc.w); err != nil {
				t.Errorf("refused a window the hub takes: %v", err)
			}
		})
	}
}

// TestCloneWorkingHoursCopiesWhatTheRowHolds: the row keeps the window the wizard wrote,
// and it keeps it as its own — a form edited afterwards must not move a saved row.
func TestCloneWorkingHoursCopiesWhatTheRowHolds(t *testing.T) {
	if CloneWorkingHours(nil) != nil {
		t.Error("cloning nil answered non-nil")
	}

	week := &WorkingHours{Timezone: "UTC", Days: []int{1, 2}, Start: "09:00", End: "17:00"}
	clone := CloneWorkingHours(week)
	clone.Days[0] = 5
	if week.Days[0] != 1 {
		t.Errorf("the original's day list moved with the clone: %v", week.Days)
	}
	if clone.Timezone != "UTC" || clone.Start != "09:00" || clone.End != "17:00" {
		t.Errorf("clone = %+v, want the original's bounds and zone", *clone)
	}
}
