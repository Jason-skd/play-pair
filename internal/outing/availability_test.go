package outing

import (
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeAvailability(t *testing.T) {
	dates := []Date{"2026-10-10"}
	original := MemberInput{Availability: []Availability{
		{dates[0], 600, 660}, {dates[0], 540, 630}, {dates[0], 660, 690},
	}}
	got, err := Normalize(original, dates, "Asia/Shanghai")
	if err != nil || !reflect.DeepEqual(got.Availability, []Availability{{dates[0], 540, 690}}) {
		t.Fatal(got, err)
	}
	if original.Availability[0].StartMinute != 600 {
		t.Fatal("normalization mutated caller input")
	}
	for _, tc := range []struct {
		name  string
		input MemberInput
	}{
		{"empty", MemberInput{}},
		{"outside dates", MemberInput{Availability: []Availability{{"2026-10-11", 600, 660}}}},
		{"unaligned", MemberInput{Availability: []Availability{{dates[0], 601, 660}}}},
		{"reversed", MemberInput{Availability: []Availability{{dates[0], 660, 600}}}},
		{"overflow", MemberInput{Availability: []Availability{{dates[0], 0, 1470}}}},
		{"limit", MemberInput{Availability: make([]Availability, 257)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Normalize(tc.input, dates, "Asia/Shanghai"); !errors.Is(err, ErrInvalidAvailability) {
				t.Fatal(err)
			}
		})
	}
	if _, err := Normalize(MemberInput{MaxBudgetFen: -1}, dates, "UTC"); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
}

func TestLocalTimeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		date       Date
		start, end int
		valid      bool
	}{
		{"2026-03-08", 60, 90, true},
		{"2026-03-08", 90, 210, false},
		{"2026-11-01", 60, 120, false},
		{"2026-11-01", 180, 240, true},
		{"2026-10-10", 1380, 1440, true},
	} {
		_, err := Normalize(MemberInput{Availability: []Availability{{tc.date, tc.start, tc.end}}}, []Date{tc.date}, "America/New_York")
		if (err == nil) != tc.valid {
			t.Fatal(tc, err)
		}
	}
	for _, tc := range []struct {
		value  string
		end    bool
		minute int
	}{
		{"00:00", false, 0}, {"23:30", false, 1410}, {"24:00", true, 1440},
	} {
		v, err := ParseMinute(tc.value, tc.end)
		if err != nil || v != tc.minute {
			t.Fatal(tc, v, err)
		}
	}
	for _, value := range []string{"24:00", "9:00", "09:15", "25:00"} {
		if _, err := ParseMinute(value, false); err == nil {
			t.Fatal(value)
		}
	}
	for _, value := range []string{"2026-02-30", "0000-01-01", "2026-1-01"} {
		if _, err := ParseDate(value); err == nil {
			t.Fatal(value)
		}
	}
	for _, value := range []string{"", "Local", "/etc/localtime", "No/SuchZone"} {
		if _, err := Location(value); err == nil {
			t.Fatal(value)
		}
	}
}

func TestPlanValuePolicy(t *testing.T) {
	for _, duration := range []int{0, 15, 31, 1470} {
		if _, err := normalizePlan(Plan{Dates: []Date{"2026-10-10"}, DurationMinutes: duration}); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(duration, err)
		}
	}
	for _, duration := range []int{30, 1440} {
		if _, err := normalizePlan(Plan{Dates: []Date{"2026-10-10"}, DurationMinutes: duration}); err != nil {
			t.Fatal(duration, err)
		}
	}
	for _, date := range []Date{"0000-01-01", "10000-01-01", "2026-02-30"} {
		if _, err := normalizePlan(Plan{Dates: []Date{date}, DurationMinutes: 60}); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(date, err)
		}
	}
}

func TestPlanConflictsAreComputedWithoutChangingInput(t *testing.T) {
	input := MemberInput{Availability: []Availability{{"2026-10-10", 600, 660}, {"2026-10-11", 600, 720}}}
	plan := Plan{Dates: []Date{"2026-10-10"}, DurationMinutes: 90}
	valid, warnings := ValidAvailability(input, plan)
	if len(valid) != 1 || len(warnings) != 2 || warnings[0].Code != "date_outside_plan" || *warnings[0].AvailabilityIndex != 1 || warnings[1].Code != "no_sufficient_availability" {
		t.Fatal(valid, warnings)
	}
	if len(input.Availability) != 2 {
		t.Fatal("stored input was pruned")
	}
	plan.Dates = []Date{"2026-10-10", "2026-10-11"}
	_, warnings = ValidAvailability(input, plan)
	if len(warnings) != 0 {
		t.Fatal("restored plan retained stale warnings", warnings)
	}
}
