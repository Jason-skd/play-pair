package outing

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// ConstraintStatus distinguishes confirmed facts from external conditions that
// still need confirmation. Unknown never overrides a determinable violation.
type ConstraintStatus string

const (
	Satisfied ConstraintStatus = "satisfied"
	Violated  ConstraintStatus = "violated"
	Unknown   ConstraintStatus = "unknown"
)

// ConstraintAnswer is supplied by the recommendation provider, once per ID.
type ConstraintAnswer struct {
	ConstraintID string
	Status       ConstraintStatus
	Evidence     string
	Reason       string
}

// Proposal contains the provider's structured facts, separately from its ticks.
// Nil external facts mean unconfirmed, never zero cost or confirmed access.
// Cost is CNY fen per person for the whole outing, matching ConstraintBudget.
type Proposal struct {
	Schedule         TimeWindow
	CostPerPersonFen *int64
	WheelchairAccess *bool
	Answers          []ConstraintAnswer
}

type ConstraintResult struct {
	ConstraintID string
	Status       ConstraintStatus
	Evidence     string
	Reason       string
}

type ValidationFailure struct {
	ConstraintID string
	Code         string
}

// Validation is suitable for #27's immutable recommendation record. Accepted
// means no identified conflict; Pending must still be shown to the organizer.
type Validation struct {
	Accepted bool
	Results  []ConstraintResult
	Failures []ValidationFailure
	Pending  []ConstraintResult
}

// ValidateProposal rejects malformed answer coverage as a contract error and
// reports factual or provider-reported conflicts as a rejected candidate. It is
// pure: #27 can replay the exact constraint set and facts without database I/O.
func ValidateProposal(constraints []Constraint, proposal Proposal) (Validation, error) {
	if !validConstraintSet(constraints) || len(proposal.Answers) != len(constraints) {
		return Validation{}, ErrConstraintContract
	}
	if !validWindow(proposal.Schedule) || (proposal.CostPerPersonFen != nil && *proposal.CostPerPersonFen < 0) {
		return Validation{}, ErrConstraintContract
	}
	answers := make(map[string]ConstraintAnswer, len(proposal.Answers))
	for _, answer := range proposal.Answers {
		if answer.Status != Satisfied && answer.Status != Violated && answer.Status != Unknown {
			return Validation{}, ErrConstraintContract
		}
		if !validExplanation(answer.Evidence) || !validExplanation(answer.Reason) || (strings.TrimSpace(answer.Evidence) == "" && strings.TrimSpace(answer.Reason) == "") {
			return Validation{}, ErrConstraintContract
		}
		if _, exists := answers[answer.ConstraintID]; exists {
			return Validation{}, ErrConstraintContract
		}
		if !slices.ContainsFunc(constraints, func(c Constraint) bool { return c.ID == answer.ConstraintID }) {
			return Validation{}, ErrConstraintContract
		}
		answers[answer.ConstraintID] = answer
	}
	result := Validation{Accepted: true, Results: []ConstraintResult{}, Failures: []ValidationFailure{}, Pending: []ConstraintResult{}}
	for _, constraint := range constraints {
		answer, exists := answers[constraint.ID]
		if !exists {
			return Validation{}, ErrConstraintContract
		}
		status, reason := checkConstraint(constraint, proposal)
		item := ConstraintResult{ConstraintID: constraint.ID, Status: status, Evidence: answer.Evidence, Reason: reason}
		if status == Violated || answer.Status == Violated {
			item.Status = Violated
			code := "constraint_violated"
			if status == Satisfied && answer.Status == Violated {
				code = "fact_answer_conflict"
			}
			if answer.Status == Violated && status != Violated {
				item.Reason = answer.Reason
			}
			result.Failures = append(result.Failures, ValidationFailure{ConstraintID: constraint.ID, Code: code})
			result.Accepted = false
		}
		// A provider cannot confirm an absent structured fact with a tick. Retain
		// it as unknown and preserve the provider's evidence for later review.
		if item.Status == Unknown {
			result.Pending = append(result.Pending, item)
		}
		result.Results = append(result.Results, item)
	}
	return result, nil
}

func validExplanation(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0) && utf8.RuneCountInString(value) <= 2000
}

func validWindow(window TimeWindow) bool {
	return window.StartsAt.Year() >= 1 && window.EndsAt.Year() <= 9999 && window.StartsAt.Before(window.EndsAt)
}

func validConstraintSet(constraints []Constraint) bool {
	if len(constraints) < 3 || len(constraints) > 4 {
		return false
	}
	ids, types := map[string]bool{}, map[ConstraintType]bool{}
	for _, constraint := range constraints {
		if constraint.ID == "" || len(constraint.ID) > 128 || ids[constraint.ID] || types[constraint.Type] || len(constraint.Sources) == 0 {
			return false
		}
		ids[constraint.ID], types[constraint.Type] = true, true
		for _, source := range constraint.Sources {
			if source.MemberID < 0 || source.Field == "" {
				return false
			}
		}
		switch constraint.Type {
		case ConstraintBudget:
			if constraint.Scope != ScopePerPerson || constraint.MaxBudgetFen == nil || *constraint.MaxBudgetFen < 0 || constraint.DurationMinutes != nil || len(constraint.Windows) != 0 {
				return false
			}
		case ConstraintTime:
			if constraint.Scope != ScopeSchedule || len(constraint.Windows) == 0 || len(constraint.Windows) > MaxAggregationMembers*256 || constraint.MaxBudgetFen != nil || constraint.DurationMinutes != nil {
				return false
			}
			for _, window := range constraint.Windows {
				if !validWindow(window) {
					return false
				}
			}
		case ConstraintDuration:
			if constraint.Scope != ScopeSchedule || constraint.DurationMinutes == nil || *constraint.DurationMinutes < 30 || *constraint.DurationMinutes > 1440 || *constraint.DurationMinutes%30 != 0 || constraint.MaxBudgetFen != nil || len(constraint.Windows) != 0 {
				return false
			}
		case ConstraintAccessibility:
			if constraint.Scope != ScopeVenue || constraint.MaxBudgetFen != nil || constraint.DurationMinutes != nil || len(constraint.Windows) != 0 {
				return false
			}
		default:
			return false
		}
	}
	return types[ConstraintBudget] && types[ConstraintTime] && types[ConstraintDuration]
}

func checkConstraint(constraint Constraint, proposal Proposal) (ConstraintStatus, string) {
	switch constraint.Type {
	case ConstraintBudget:
		if proposal.CostPerPersonFen == nil {
			return Unknown, "cost_unconfirmed"
		}
		if *proposal.CostPerPersonFen > *constraint.MaxBudgetFen {
			return Violated, "budget_exceeded"
		}
		return Satisfied, "within_budget"
	case ConstraintAccessibility:
		if proposal.WheelchairAccess == nil {
			return Unknown, "wheelchair_access_unconfirmed"
		}
		if !*proposal.WheelchairAccess {
			return Violated, "wheelchair_access_unavailable"
		}
		return Satisfied, "wheelchair_access_confirmed"
	case ConstraintDuration:
		if proposal.Schedule.EndsAt.Sub(proposal.Schedule.StartsAt) != time.Duration(*constraint.DurationMinutes)*time.Minute {
			return Violated, "duration_mismatch"
		}
		return Satisfied, "duration_matches"
	case ConstraintTime:
		start := proposal.Schedule.StartsAt
		if start.Second() != 0 || start.Nanosecond() != 0 {
			return Violated, "start_not_half_hour"
		}
		for _, window := range constraint.Windows {
			// Compare against the locally aligned window's grid, without assuming
			// that UTC minute zero is a half-hour boundary in every timezone.
			if !start.Before(window.StartsAt) && !proposal.Schedule.EndsAt.After(window.EndsAt) {
				if start.Sub(window.StartsAt)%(30*time.Minute) != 0 {
					return Violated, "start_not_half_hour"
				}
				return Satisfied, "within_common_window"
			}
		}
		return Violated, "outside_common_window"
	}
	return Unknown, "unsupported_constraint"
}
