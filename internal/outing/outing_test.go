package outing

import (
	"errors"
	"testing"
	"time"
)

func TestStatusBoundary(t *testing.T) {
	now := time.Date(2030, 1, 1, 9, 0, 0, 0, time.UTC)
	o := Outing{}
	if o.StatusAt(now) != StatusDecision {
		t.Fatal("missing arrangement")
	}
	o.Arrangement = &Arrangement{StartsAt: now}
	for _, c := range []struct {
		at   time.Time
		want Status
	}{{now.Add(-time.Nanosecond), StatusConfirmation}, {now, Archived}, {now.Add(time.Second), Archived}} {
		if got := o.StatusAt(c.at); got != c.want {
			t.Fatal(got, c.want)
		}
	}
}
func TestAvailabilityNormalization(t *testing.T) {
	date := Date("2030-10-03")
	in := MemberInput{Availability: []Availability{{date, 600, 720}, {date, 480, 600}, {date, 750, 780}, {date, 480, 540}, {date, 1410, 1440}}}
	got, err := Normalize(in, []Date{date}, "Asia/Shanghai")
	if err != nil || len(got.Availability) != 3 || got.Availability[0] != (Availability{date, 480, 720}) || got.Availability[1].StartMinute != 750 {
		t.Fatal(got, err)
	}
	if in.Availability[0].StartMinute != 600 {
		t.Fatal("mutated caller")
	}
	for _, a := range []Availability{{date, 480, 480}, {date, 600, 480}, {date, -30, 60}, {date, 0, 1470}, {date, 481, 600}, {"2030-10-04", 480, 600}} {
		_, err := Normalize(MemberInput{Availability: []Availability{a}}, []Date{date}, "Asia/Shanghai")
		if !errors.Is(err, ErrInvalidAvailability) {
			t.Fatal(a, err)
		}
	}
	if _, err := Normalize(MemberInput{}, []Date{date}, "Asia/Shanghai"); !errors.Is(err, ErrInvalidAvailability) {
		t.Fatal(err)
	}
	for _, c := range []struct {
		date       Date
		start, end int
		zone       string
		valid      bool
	}{
		{"2030-03-10", 90, 210, "America/New_York", false},
		{"2030-11-03", 60, 120, "America/New_York", false},
		{"2030-03-10", 210, 240, "America/New_York", true},
		{"2030-04-07", 90, 120, "Australia/Lord_Howe", false},
		{"2011-12-30", 480, 600, "Pacific/Apia", false},
	} {
		_, err := Normalize(MemberInput{Availability: []Availability{{c.date, c.start, c.end}}}, []Date{c.date}, c.zone)
		if (err == nil) != c.valid {
			t.Fatal(c, err)
		}
	}
}
func TestDateAndMinuteParsing(t *testing.T) {
	for _, v := range []string{"2030-2-01", "2030-02-29", "0000-01-01", "2030-01-01T00:00:00Z"} {
		if _, err := ParseDate(v); err == nil {
			t.Fatal(v)
		}
	}
	for _, v := range []string{"8:00", "08:15", "24:30", "25:00", "-1:00"} {
		if _, err := ParseMinute(v, true); err == nil {
			t.Fatal(v)
		}
	}
	if _, err := ParseMinute("24:00", false); err == nil {
		t.Fatal("start 24:00")
	}
	if n, err := ParseMinute("24:00", true); err != nil || n != 1440 {
		t.Fatal(n, err)
	}
	for _, zone := range []string{"", "Local", "bad/zone"} {
		if _, err := Location(zone); err == nil {
			t.Fatal(zone)
		}
	}
}

func TestAvailabilityLimits(t *testing.T) {
	date := Date("2030-10-03")
	valid := Availability{date, 480, 720}
	for _, input := range []MemberInput{{MaxBudgetFen: -1, Availability: []Availability{valid}}, {Availability: make([]Availability, 257)}} {
		if _, err := Normalize(input, []Date{date}, "UTC"); err == nil {
			t.Fatal("accepted invalid input")
		}
	}
	values := make([]Availability, 256)
	for i := range values {
		values[i] = valid
	}
	if got, err := Normalize(MemberInput{Availability: values}, []Date{date}, "UTC"); err != nil || len(got.Availability) != 1 {
		t.Fatal(got, err)
	}
}
