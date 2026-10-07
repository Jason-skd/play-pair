package outing

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/users"
)

func decisionOuting() Outing {
	return Outing{ID: 10, Timezone: "Asia/Shanghai", Plan: Plan{Dates: []Date{"2026-10-10", "2026-10-11"}, DurationMinutes: 60}}
}

func participant(id users.ID, budget int64, access bool, intervals ...Availability) ParticipantInput {
	return ParticipantInput{MemberID: id, Input: MemberInput{MaxBudgetFen: budget, RequiresWheelchairAccess: access, Availability: intervals}}
}

func TestAggregateExplicitInputsAndStableConstraints(t *testing.T) {
	inputs := []ParticipantInput{
		participant(2, 12000, true, Availability{"2026-10-10", 630, 720}, Availability{"2026-10-10", 600, 660}),
		participant(1, 0, false, Availability{"2026-10-10", 660, 780}),
	}
	original := slices.Clone(inputs[0].Input.Availability)
	result, err := aggregateInputs(decisionOuting(), inputs)
	if err != nil || result.MaxBudgetFen != 0 || !result.RequiresWheelchairAccess || len(result.StartTimes) != 1 || len(result.Constraints) != 4 {
		t.Fatal(result, err)
	}
	if !reflect.DeepEqual(inputs[0].Input.Availability, original) {
		t.Fatal("mutated caller input")
	}
	if result.Participants[0].MemberID != 1 || result.StartTimes[0].Format(time.RFC3339) != "2026-10-10T11:00:00+08:00" {
		t.Fatal(result)
	}
	for _, constraint := range result.Constraints {
		if constraint.ID == "" || len(constraint.Sources) == 0 {
			t.Fatal(constraint)
		}
	}
	reordered := []ParticipantInput{inputs[1], inputs[0]}
	second, err := aggregateInputs(decisionOuting(), reordered)
	if err != nil || !reflect.DeepEqual(result, second) {
		t.Fatal("unstable across query order", second, err)
	}
	changed := decisionOuting()
	changed.DurationMinutes = 30
	third, err := aggregateInputs(changed, inputs)
	if err != nil || result.Constraints[0].ID == third.Constraints[0].ID {
		t.Fatal("ID did not bind plan", err)
	}
	inputs[1].Input.MaxBudgetFen = 100
	fourth, err := aggregateInputs(decisionOuting(), inputs)
	if err != nil || result.Constraints[0].ID == fourth.Constraints[0].ID {
		t.Fatal("ID did not bind member input", err)
	}
}

func TestAggregateWindows(t *testing.T) {
	for _, tc := range []struct {
		name        string
		zone        string
		duration    int
		left, right []Availability
		starts      []string
		err         error
	}{
		{"half-open touching", "UTC", 30, []Availability{{"2026-10-10", 600, 660}}, []Availability{{"2026-10-10", 660, 720}}, nil, ErrNoCommonTime},
		{"adjacent member intervals", "UTC", 90, []Availability{{"2026-10-10", 600, 660}, {"2026-10-10", 660, 720}}, []Availability{{"2026-10-10", 600, 720}}, []string{"2026-10-10T10:00:00Z", "2026-10-10T10:30:00Z"}, nil},
		{"across midnight", "Asia/Shanghai", 90, []Availability{{"2026-10-10", 1380, 1440}, {"2026-10-11", 0, 90}}, []Availability{{"2026-10-10", 1410, 1440}, {"2026-10-11", 0, 60}}, []string{"2026-10-10T23:30:00+08:00"}, nil},
		{"stale member not dropped", "UTC", 60, []Availability{{"2026-10-09", 600, 720}}, []Availability{{"2026-10-10", 600, 720}}, nil, ErrNoCommonTime},
		{"stale interval excluded", "UTC", 60, []Availability{{"2026-10-09", 600, 720}, {"2026-10-10", 600, 660}}, []Availability{{"2026-10-10", 600, 720}}, []string{"2026-10-10T10:00:00Z"}, nil},
		{"duration too long", "UTC", 90, []Availability{{"2026-10-10", 600, 660}}, []Availability{{"2026-10-10", 600, 660}}, nil, ErrNoCommonTime},
		{"quarter-hour timezone offset", "Asia/Kathmandu", 60, []Availability{{"2026-10-10", 600, 690}}, []Availability{{"2026-10-10", 600, 690}}, []string{"2026-10-10T10:00:00+05:45", "2026-10-10T10:30:00+05:45"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := decisionOuting()
			current.Timezone = tc.zone
			current.DurationMinutes = tc.duration
			result, err := aggregateInputs(current, []ParticipantInput{participant(1, 10000, false, tc.left...), participant(2, 10000, false, tc.right...)})
			if !errors.Is(err, tc.err) {
				t.Fatal(result, err)
			}
			if err != nil {
				return
			}
			got := []string{}
			for _, start := range result.StartTimes {
				got = append(got, start.Format(time.RFC3339))
			}
			if !reflect.DeepEqual(got, tc.starts) {
				t.Fatal(got, tc.starts)
			}
		})
	}
}

func TestAggregateInvalidInputAndLimits(t *testing.T) {
	current := decisionOuting()
	valid := participant(1, 0, false, Availability{"2026-10-10", 600, 720})
	for _, tc := range []struct {
		name   string
		inputs []ParticipantInput
		err    error
	}{
		{"none", nil, ErrNoParticipants},
		{"too many members", make([]ParticipantInput, MaxAggregationMembers+1), ErrAggregationCapacity},
		{"negative budget", []ParticipantInput{participant(1, -1, false, Availability{"2026-10-10", 600, 720})}, ErrAggregationInput},
		{"missing availability", []ParticipantInput{participant(1, 0, false)}, ErrAggregationInput},
		{"bad stale date", []ParticipantInput{participant(1, 0, false, Availability{"2026-02-30", 600, 720})}, ErrAggregationInput},
		{"bad interval", []ParticipantInput{participant(1, 0, false, Availability{"2026-10-10", 601, 720})}, ErrAggregationInput},
		{"duplicate member", []ParticipantInput{valid, valid}, ErrAggregationInput},
		{"later corrupt input after empty intersection", []ParticipantInput{participant(1, 0, false, Availability{"2026-10-09", 600, 720}), participant(2, 0, false)}, ErrAggregationInput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := aggregateInputs(current, tc.inputs)
			if !errors.Is(err, tc.err) {
				t.Fatal(err)
			}
		})
	}
	for _, date := range []Date{"2026-03-08", "2026-11-01"} {
		current := Outing{Timezone: "America/New_York", Plan: Plan{Dates: []Date{date}, DurationMinutes: 30}}
		_, err := aggregateInputs(current, []ParticipantInput{participant(1, 0, false, Availability{date, 60, 240})})
		if !errors.Is(err, ErrAggregationInput) {
			t.Fatal(date, err)
		}
	}
	current.Dates = nil
	current.DurationMinutes = 30
	fullDays := []Availability{}
	for date := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC); len(current.Dates) < 31; date = date.AddDate(0, 0, 1) {
		d := Date(date.Format(time.DateOnly))
		current.Dates = append(current.Dates, d)
		fullDays = append(fullDays, Availability{d, 0, 1440})
	}
	if _, err := aggregateInputs(current, []ParticipantInput{participant(1, 0, false, fullDays...)}); !errors.Is(err, ErrAggregationCapacity) {
		t.Fatal(err)
	}
}
