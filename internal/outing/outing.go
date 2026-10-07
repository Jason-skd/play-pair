// Package outing owns outing access, response rounds and editable plans.
package outing

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode/utf8"

	"github.com/Jason-skd/play-pair/internal/users"
)

// ID identifies a persisted outing.
type ID int64

// Phase identifies the planning or arrangement-confirmation response round.
type Phase string

// Choice represents a response or its read-side pending projection.
type Choice string

const (
	Decision     Phase  = "decision"
	Confirmation Phase  = "confirmation"
	Accept       Choice = "accept"
	Deny         Choice = "deny"
	// Pending is never a submitted choice; it represents an absent or reset response.
	Pending Choice = "pending"
)

var (
	// ErrInvalidInput indicates invalid plan, arrangement or response values.
	ErrInvalidInput = errors.New("invalid outing input")
	// ErrInvalidAvailability indicates invalid dates or local-time intervals.
	ErrInvalidAvailability = errors.New("invalid outing availability")
	// ErrForbidden indicates insufficient access or organizer authority.
	ErrForbidden = errors.New("outing access denied")
	// ErrPhase indicates that the command does not match the current response round.
	ErrPhase = errors.New("outing phase conflict")
	// ErrArchived indicates that the current arrangement has already started.
	ErrArchived = errors.New("outing has started")
)

// Date is a canonical YYYY-MM-DD calendar date in the outing's immutable timezone.
type Date string

// Availability is a local-time interval with an inclusive start and exclusive end.
// Minutes are measured from midnight; an end of 1440 denotes the next midnight.
type Availability struct {
	Date                   Date
	StartMinute, EndMinute int
}

// MemberInput holds explicitly confirmed first-round constraints for this outing.
// Zero budget and false accessibility are supplied values, not missing defaults.
type MemberInput struct {
	MaxBudgetFen             int64
	RequiresWheelchairAccess bool
	Availability             []Availability
}

// Arrangement is the organizer's concrete proposal for second-round responses.
type Arrangement struct {
	StartsAt, EndsAt time.Time
	Place, Activity  string
}

// Plan contains editable candidate dates and the required activity duration.
type Plan struct {
	Dates           []Date
	DurationMinutes int
}

// Outing combines the persisted plan and its optional arrangement.
// GroupID identifies the discovery source, not an ongoing membership requirement.
type Outing struct {
	ID          ID
	GroupID     int64
	InitiatorID users.ID
	Timezone    string
	Plan
	CreatedAt   time.Time
	Arrangement *Arrangement
}

// StatusAt derives the current stage from arrangement existence and start time.
func (current Outing) StatusAt(now time.Time) string {
	if current.Arrangement == nil {
		return string(Decision)
	}
	if !now.Before(current.Arrangement.StartsAt) {
		return "archived"
	}
	return string(Confirmation)
}

// Membership contains persistent response existence separately from choice.
// A reset confirmation still grants access after leaving the source group.
type Membership struct{ InGroup, HasDecision, HasConfirmation bool }

// Visible retains organizer and respondent access after leaving the source group.
func (m Membership) Visible(current Outing, actor users.ID) bool {
	return actor == current.InitiatorID || m.InGroup || m.HasDecision || (current.Arrangement != nil && m.HasConfirmation)
}

// Member retains both response rounds and first-round input after confirmation opens.
// Nil choices mean no row; a reset confirmation row instead contains Pending.
type Member struct {
	MemberID     users.ID
	Decision     *Choice
	Confirmation *Choice
	Input        *MemberInput
}

// Warning describes a current-plan conflict without changing stored member input.
// AvailabilityIndex is nil when the warning applies to the whole submission.
type Warning struct {
	Code              string
	AvailabilityIndex *int
}

// Detail combines an outing with private member input and first-round warnings.
type Detail struct {
	Outing   Outing
	Member   Member
	Warnings []Warning
}

// Summary exposes a member's round and status without their private input.
type Summary struct {
	MemberID users.ID
	Phase    Phase
	Status   Choice
}

// ResponseCursor identifies the last returned row in member-then-phase order.
type ResponseCursor struct {
	MemberID users.ID
	Phase    Phase
}

// ResponseFilter optionally restricts round and status; empty values leave them unfiltered.
// A zero After starts pagination, and Limit bounds the number of summaries.
type ResponseFilter struct {
	Phase  Phase
	Status Choice
	After  ResponseCursor
	Limit  int
}

// ResponsePage contains summaries and a cursor when another page is available.
type ResponsePage struct {
	Responses []Summary
	Next      *ResponseCursor
}

// ListFilter optionally restricts the discovery group; zero GroupID includes all.
// After is an exclusive outing-ID cursor, with zero selecting the first page.
type ListFilter struct {
	GroupID int64
	After   ID
	Limit   int
}

// OutingPage contains visible outings and an optional continuation cursor.
type OutingPage struct {
	Outings []Outing
	Next    *ID
}

// Store exposes persistence facts and writes within a repository-owned snapshot.
// LockOuting serializes commands for one outing; Go decides what is permitted.
type Store interface {
	GroupMember(context.Context, int64, users.ID) (bool, error)
	InsertOuting(context.Context, users.ID, int64, string, Plan) (Outing, error)
	LoadOuting(context.Context, ID) (Outing, error)
	LockOuting(context.Context, ID) error
	Membership(context.Context, Outing, users.ID) (Membership, error)
	Member(context.Context, ID, users.ID) (Member, error)
	AcceptedInputs(context.Context, ID) ([]ParticipantInput, error)
	SavePlan(context.Context, ID, Plan) error
	SaveDecision(context.Context, ID, users.ID, Choice, *MemberInput) error
	SaveConfirmation(context.Context, ID, users.ID, Choice) error
	SaveArrangement(ctx context.Context, id ID, arrangement Arrangement, updateExisting, resetResponses bool) error
	ListOutings(context.Context, users.ID, ListFilter) (OutingPage, error)
	ListResponses(context.Context, Outing, ResponseFilter) (ResponsePage, error)
}

// Read uses one consistent snapshot; Write commits only if the callback succeeds.
type Repository interface {
	Read(context.Context, func(Store) error) error
	Write(context.Context, func(Store) error) error
}

// Service owns outing value validation, permissions and stage transitions.
type Service struct {
	repo Repository
	now  func() time.Time
}

// New constructs a service with repository transactions and an injected clock.
func New(repo Repository, now func() time.Time) *Service { return &Service{repo: repo, now: now} }

// Now returns the service clock used to project time-dependent outing status.
func (s *Service) Now() time.Time { return s.now() }

func normalizePlan(plan Plan) (Plan, error) {
	if len(plan.Dates) < 1 || len(plan.Dates) > 31 {
		return plan, ErrInvalidInput
	}
	if plan.DurationMinutes < 30 || plan.DurationMinutes > 1440 || plan.DurationMinutes%30 != 0 {
		return plan, ErrInvalidInput
	}
	plan.Dates = slices.Clone(plan.Dates)
	for _, date := range plan.Dates {
		if _, err := ParseDate(string(date)); err != nil {
			return plan, ErrInvalidInput
		}
	}
	slices.Sort(plan.Dates)
	plan.Dates = slices.Compact(plan.Dates)
	return plan, nil
}

// Create publishes a plan for a group member without creating invitation rows.
func (s *Service) Create(ctx context.Context, actor users.ID, groupID int64, timezone string, plan Plan) (Outing, error) {
	if actor <= 0 || groupID <= 0 {
		return Outing{}, ErrInvalidInput
	}
	plan, err := normalizePlan(plan)
	if err != nil {
		return Outing{}, err
	}
	if _, err := Location(timezone); err != nil {
		return Outing{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var current Outing
	err = s.repo.Write(ctx, func(store Store) error {
		inGroup, err := store.GroupMember(ctx, groupID, actor)
		if err != nil {
			return err
		}
		if !inGroup {
			return ErrForbidden
		}
		current, err = store.InsertOuting(ctx, actor, groupID, timezone, plan)
		return err
	})
	return current, err
}

func accessible(ctx context.Context, store Store, id ID, actor users.ID) (Outing, error) {
	current, err := store.LoadOuting(ctx, id)
	if err != nil {
		return current, err
	}
	membership, err := store.Membership(ctx, current, actor)
	if err != nil {
		return current, err
	}
	if !membership.Visible(current, actor) {
		return current, ErrForbidden
	}
	return current, nil
}

// Get returns member input only to that member or the organizer.
// First-round input remains readable in round two, but warnings are only computed
// while members can still amend that input during the decision round.
func (s *Service) Get(ctx context.Context, actor users.ID, id ID, memberID users.ID) (Detail, error) {
	if actor <= 0 || id <= 0 || memberID <= 0 {
		return Detail{}, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var detail Detail
	err := s.repo.Read(ctx, func(store Store) error {
		current, err := accessible(ctx, store, id, actor)
		if err != nil {
			return err
		}
		if memberID != actor && actor != current.InitiatorID {
			return ErrForbidden
		}
		membership, err := store.Membership(ctx, current, memberID)
		if err != nil {
			return err
		}
		if !membership.Visible(current, memberID) {
			return ErrForbidden
		}
		member, err := store.Member(ctx, id, memberID)
		if err != nil {
			return err
		}
		detail = Detail{Outing: current, Member: member, Warnings: []Warning{}}
		if current.Arrangement == nil && member.Input != nil {
			_, detail.Warnings = ValidAvailability(*detail.Member.Input, current.Plan)
		}
		return nil
	})
	return detail, err
}

// command locks before reading permissions and stage so concurrent outing commands
// cannot invalidate those checks before the callback commits its writes.
func (s *Service) command(ctx context.Context, actor users.ID, id ID, fn func(context.Context, Store, Outing, time.Time) error) error {
	if actor <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.repo.Write(ctx, func(store Store) error {
		if err := store.LockOuting(ctx, id); err != nil {
			return err
		}
		current, err := accessible(ctx, store, id, actor)
		if err != nil {
			return err
		}
		now := s.now()
		if current.StatusAt(now) == "archived" {
			return ErrArchived
		}
		return fn(ctx, store, current, now)
	})
}

// Update replaces dates and duration before the current arrangement starts.
// Existing input is preserved even when it conflicts with the replacement plan.
func (s *Service) Update(ctx context.Context, actor users.ID, id ID, plan Plan) error {
	plan, err := normalizePlan(plan)
	if err != nil {
		return err
	}
	return s.command(ctx, actor, id, func(ctx context.Context, store Store, current Outing, _ time.Time) error {
		if actor != current.InitiatorID {
			return ErrForbidden
		}
		return store.SavePlan(ctx, id, plan)
	})
}

// Respond replaces the actor's choice in the current round.
// Only first-round Accept carries input; first-round Deny clears previous input.
func (s *Service) Respond(ctx context.Context, actor users.ID, id ID, phase Phase, choice Choice, input *MemberInput) error {
	if (phase != Decision && phase != Confirmation) || (choice != Accept && choice != Deny) {
		return ErrInvalidInput
	}
	requiresInput := phase == Decision && choice == Accept
	if requiresInput != (input != nil) {
		return ErrInvalidInput
	}
	return s.command(ctx, actor, id, func(ctx context.Context, store Store, current Outing, _ time.Time) error {
		decisionOpen := current.Arrangement == nil
		if decisionOpen != (phase == Decision) {
			return ErrPhase
		}
		if phase == Confirmation {
			return store.SaveConfirmation(ctx, id, actor, choice)
		}
		if input != nil {
			normalized, err := Normalize(*input, current.Dates, current.Timezone)
			if err != nil {
				return err
			}
			input = &normalized
		}
		return store.SaveDecision(ctx, id, actor, choice, input)
	})
}

// Arrange creates or replaces the organizer's proposal before it starts.
// updateExisting selects replacement; resetResponses clears second-round choices while retaining
// their rows and access. Keeping choices makes the organizer responsible for consent.
func (s *Service) Arrange(ctx context.Context, actor users.ID, id ID, arrangement Arrangement, updateExisting, resetResponses bool) error {
	arrangement.Place = strings.TrimSpace(arrangement.Place)
	arrangement.Activity = strings.TrimSpace(arrangement.Activity)
	for _, text := range []string{arrangement.Place, arrangement.Activity} {
		if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
			return ErrInvalidInput
		}
		if length := utf8.RuneCountInString(text); length < 1 || length > 500 {
			return ErrInvalidInput
		}
	}
	if arrangement.StartsAt.Year() < 1 || arrangement.EndsAt.Year() > 9999 || !arrangement.StartsAt.Before(arrangement.EndsAt) {
		return ErrInvalidInput
	}
	return s.command(ctx, actor, id, func(ctx context.Context, store Store, current Outing, now time.Time) error {
		if actor != current.InitiatorID {
			return ErrForbidden
		}
		if updateExisting != (current.Arrangement != nil) {
			return ErrPhase
		}
		if !arrangement.StartsAt.After(now) {
			return ErrInvalidInput
		}
		return store.SaveArrangement(ctx, id, arrangement, updateExisting, resetResponses)
	})
}

// List returns visible outings, including retained access after leaving a group.
func (s *Service) List(ctx context.Context, actor users.ID, filter ListFilter) (OutingPage, error) {
	if actor <= 0 || filter.GroupID < 0 || filter.After < 0 || filter.Limit < 1 || filter.Limit > 100 {
		return OutingPage{}, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var page OutingPage
	err := s.repo.Read(ctx, func(store Store) error {
		var err error
		page, err = store.ListOutings(ctx, actor, filter)
		return err
	})
	return page, err
}

// Responses returns filtered summaries across both rounds without private input.
func (s *Service) Responses(ctx context.Context, actor users.ID, id ID, filter ResponseFilter) (ResponsePage, error) {
	if actor <= 0 || id <= 0 || filter.Limit < 1 || filter.Limit > 100 || filter.After.MemberID < 0 {
		return ResponsePage{}, ErrInvalidInput
	}
	if filter.Phase != "" && filter.Phase != Decision && filter.Phase != Confirmation {
		return ResponsePage{}, ErrInvalidInput
	}
	if filter.Status != "" && filter.Status != Accept && filter.Status != Deny && filter.Status != Pending {
		return ResponsePage{}, ErrInvalidInput
	}
	if filter.After.Phase != "" && filter.After.Phase != Decision && filter.After.Phase != Confirmation {
		return ResponsePage{}, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var page ResponsePage
	err := s.repo.Read(ctx, func(store Store) error {
		current, err := accessible(ctx, store, id, actor)
		if err != nil {
			return err
		}
		page, err = store.ListResponses(ctx, current, filter)
		return err
	})
	return page, err
}
