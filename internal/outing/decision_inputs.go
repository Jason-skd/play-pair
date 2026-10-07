package outing

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/Jason-skd/play-pair/internal/users"
)

// Limits bound both database reads and the provider input size. Capacity failure
// never truncates the participating members or the feasible start times.
const (
	MaxAggregationMembers = 128
	MaxStartTimes         = 1024
)

var (
	ErrNoParticipants      = errors.New("outing has no accepted inputs")
	ErrNoCommonTime        = errors.New("outing has no common time")
	ErrAggregationCapacity = errors.New("outing aggregation capacity exceeded")
	ErrAggregationInput    = errors.New("outing input cannot be aggregated")
	ErrConstraintContract  = errors.New("invalid constraint contract")
)

// ParticipantInput retains explicit input and attribution for the recommendation
// consumer. It does not include defaults or convert soft preferences into rules.
type ParticipantInput struct {
	MemberID users.ID
	Input    MemberInput
}

// TimeWindow uses instants for half-open intervals, including across midnight.
type TimeWindow struct {
	StartsAt, EndsAt time.Time
}

type ConstraintType string

const (
	ConstraintBudget        ConstraintType = "budget"
	ConstraintAccessibility ConstraintType = "wheelchair_access"
	ConstraintTime          ConstraintType = "common_time"
	ConstraintDuration      ConstraintType = "duration"
)

type ConstraintScope string

const (
	ScopePerPerson ConstraintScope = "per_person_whole_outing"
	ScopeVenue     ConstraintScope = "venue"
	ScopeSchedule  ConstraintScope = "schedule"
)

// ConstraintSource points to the current outing plan or explicit member input.
type ConstraintSource struct {
	MemberID users.ID // Zero identifies the outing plan.
	Field    string
}

// Constraint carries only the parameter for its Type. Aggregate constructs the
// set; providers answer it and ValidateProposal independently checks the facts.
// Budget and cost use CNY fen per person for the entire outing.
type Constraint struct {
	ID              string
	Type            ConstraintType
	Scope           ConstraintScope
	MaxBudgetFen    *int64
	DurationMinutes *int
	Windows         []TimeWindow
	Sources         []ConstraintSource
}

// Aggregation is an ephemeral current-input result. #27 owns any immutable
// history saved with a recommendation; this service never persists results.
type Aggregation struct {
	OutingID                 ID
	Timezone                 string
	DurationMinutes          int
	Participants             []ParticipantInput
	MaxBudgetFen             int64
	RequiresWheelchairAccess bool
	CommonWindows            []TimeWindow
	StartTimes               []time.Time
	Constraints              []Constraint
}

// Aggregate reads the plan and accepted inputs in the repository's consistent
// transaction. An arrangement closes this organizer-only computation phase.
func (s *Service) Aggregate(ctx context.Context, actor users.ID, id ID) (Aggregation, error) {
	if actor <= 0 || id <= 0 {
		return Aggregation{}, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var result Aggregation
	err := s.repo.Read(ctx, func(store Store) error {
		current, err := store.LoadOuting(ctx, id)
		if err != nil {
			return err
		}
		if actor != current.InitiatorID {
			return ErrForbidden
		}
		if current.Arrangement != nil {
			return ErrPhase
		}
		participants, err := store.AcceptedInputs(ctx, id)
		if err != nil {
			return err
		}
		result, err = aggregateInputs(current, participants)
		return err
	})
	return result, err
}

func aggregateInputs(current Outing, participants []ParticipantInput) (Aggregation, error) {
	if len(participants) == 0 {
		return Aggregation{}, ErrNoParticipants
	}
	if len(participants) > MaxAggregationMembers {
		return Aggregation{}, ErrAggregationCapacity
	}
	plan, err := normalizePlan(current.Plan)
	if err != nil {
		return Aggregation{}, ErrAggregationInput
	}
	current.Plan = plan
	loc, err := Location(current.Timezone)
	if err != nil {
		return Aggregation{}, ErrAggregationInput
	}
	result := Aggregation{
		OutingID: current.ID, Timezone: current.Timezone, DurationMinutes: current.DurationMinutes,
		Participants: slices.Clone(participants), MaxBudgetFen: participants[0].Input.MaxBudgetFen,
		CommonWindows: []TimeWindow{}, StartTimes: []time.Time{}, Constraints: []Constraint{},
	}
	slices.SortFunc(result.Participants, func(a, b ParticipantInput) int {
		if a.MemberID < b.MemberID {
			return -1
		}
		if a.MemberID > b.MemberID {
			return 1
		}
		return 0
	})
	budgetSources, timeSources, accessSources := []ConstraintSource{}, []ConstraintSource{{Field: "dates"}}, []ConstraintSource{}
	// Validate every participating input before intersecting. An empty early
	// intersection must not hide a later corrupt input or silently drop a member.
	memberWindows := make([][]TimeWindow, 0, len(participants))
	for i := range result.Participants {
		participant := &result.Participants[i]
		if participant.MemberID <= 0 || (i > 0 && result.Participants[i-1].MemberID == participant.MemberID) {
			return Aggregation{}, ErrAggregationInput
		}
		input, windows, err := currentAvailability(participant.Input, current, loc)
		if err != nil {
			return Aggregation{}, err
		}
		participant.Input = input
		result.MaxBudgetFen = min(result.MaxBudgetFen, input.MaxBudgetFen)
		result.RequiresWheelchairAccess = result.RequiresWheelchairAccess || input.RequiresWheelchairAccess
		budgetSources = append(budgetSources, ConstraintSource{MemberID: participant.MemberID, Field: "maxBudgetFen"})
		timeSources = append(timeSources, ConstraintSource{MemberID: participant.MemberID, Field: "availability"})
		if input.RequiresWheelchairAccess {
			accessSources = append(accessSources, ConstraintSource{MemberID: participant.MemberID, Field: "requiresWheelchairAccess"})
		}
		memberWindows = append(memberWindows, windows)
	}
	result.CommonWindows = memberWindows[0]
	for _, windows := range memberWindows[1:] {
		result.CommonWindows = intersectWindows(result.CommonWindows, windows)
	}
	for _, window := range result.CommonWindows {
		for start := window.StartsAt; !start.Add(time.Duration(current.DurationMinutes) * time.Minute).After(window.EndsAt); start = start.Add(30 * time.Minute) {
			if len(result.StartTimes) == MaxStartTimes {
				return Aggregation{}, ErrAggregationCapacity
			}
			result.StartTimes = append(result.StartTimes, start)
		}
	}
	if len(result.StartTimes) == 0 {
		return Aggregation{}, ErrNoCommonTime
	}
	result.Constraints = append(result.Constraints,
		Constraint{Type: ConstraintBudget, Scope: ScopePerPerson, MaxBudgetFen: &result.MaxBudgetFen, Sources: budgetSources},
		Constraint{Type: ConstraintTime, Scope: ScopeSchedule, Windows: slices.Clone(result.CommonWindows), Sources: timeSources},
		Constraint{Type: ConstraintDuration, Scope: ScopeSchedule, DurationMinutes: &result.DurationMinutes, Sources: []ConstraintSource{{Field: "durationMinutes"}}},
	)
	if result.RequiresWheelchairAccess {
		result.Constraints = append(result.Constraints, Constraint{Type: ConstraintAccessibility, Scope: ScopeVenue, Sources: accessSources})
	}
	// Hash only supported concrete values. Canonical participant/window ordering
	// makes IDs stable across query order and sensitive to changed input/config.
	encoded, err := json.Marshal(struct {
		Outing Outing
		Inputs []ParticipantInput
	}{Outing: Outing{ID: current.ID, Timezone: current.Timezone, Plan: current.Plan}, Inputs: result.Participants})
	if err != nil {
		return Aggregation{}, ErrAggregationInput
	}
	digest := sha256.Sum256(encoded)
	for i := range result.Constraints {
		result.Constraints[i].ID = fmt.Sprintf("%s:%x", result.Constraints[i].Type, digest[:])
	}
	return result, nil
}

func currentAvailability(input MemberInput, current Outing, loc *time.Location) (MemberInput, []TimeWindow, error) {
	if len(input.Availability) > 256 {
		return MemberInput{}, nil, ErrAggregationCapacity
	}
	// Validate stale dates too, then exclude them using the current plan. The
	// stored submission remains intact in the returned participant input.
	dates := make([]Date, 0, len(input.Availability))
	for _, interval := range input.Availability {
		if _, err := ParseDate(string(interval.Date)); err != nil {
			return MemberInput{}, nil, ErrAggregationInput
		}
		if !slices.Contains(dates, interval.Date) {
			dates = append(dates, interval.Date)
		}
	}
	normalized, err := Normalize(input, dates, current.Timezone)
	if err != nil {
		return MemberInput{}, nil, ErrAggregationInput
	}
	valid, _ := ValidAvailability(normalized, current.Plan)
	windows := make([]TimeWindow, 0, len(valid))
	for _, interval := range valid {
		date, _ := time.Parse(time.DateOnly, string(interval.Date))
		start := time.Date(date.Year(), date.Month(), date.Day(), interval.StartMinute/60, interval.StartMinute%60, 0, 0, loc)
		end := time.Date(date.Year(), date.Month(), date.Day(), interval.EndMinute/60, interval.EndMinute%60, 0, 0, loc)
		if end.Year() > 9999 {
			return MemberInput{}, nil, ErrAggregationInput
		}
		windows = append(windows, TimeWindow{StartsAt: start, EndsAt: end})
	}
	return normalized, mergeWindows(windows), nil
}

func mergeWindows(windows []TimeWindow) []TimeWindow {
	slices.SortFunc(windows, func(a, b TimeWindow) int { return a.StartsAt.Compare(b.StartsAt) })
	result := make([]TimeWindow, 0, len(windows))
	for _, window := range windows {
		if len(result) > 0 && !window.StartsAt.After(result[len(result)-1].EndsAt) {
			previous := &result[len(result)-1]
			if window.EndsAt.After(previous.EndsAt) {
				previous.EndsAt = window.EndsAt
			}
		} else {
			result = append(result, window)
		}
	}
	return result
}

func intersectWindows(left, right []TimeWindow) []TimeWindow {
	result := []TimeWindow{}
	for i, j := 0, 0; i < len(left) && j < len(right); {
		start, end := left[i].StartsAt, left[i].EndsAt
		if right[j].StartsAt.After(start) {
			start = right[j].StartsAt
		}
		if right[j].EndsAt.Before(end) {
			end = right[j].EndsAt
		}
		if start.Before(end) {
			result = append(result, TimeWindow{StartsAt: start, EndsAt: end})
		}
		if left[i].EndsAt.Before(right[j].EndsAt) {
			i++
		} else {
			j++
		}
	}
	return result
}
