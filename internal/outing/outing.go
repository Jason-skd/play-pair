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
func (o Outing) StatusAt(now time.Time) string {
	if o.Arrangement == nil {
		return string(Decision)
	}
	if !now.Before(o.Arrangement.StartsAt) {
		return "archived"
	}
	return string(Confirmation)
}

// Membership contains persistent response existence separately from choice.
// A reset confirmation still grants access after leaving the source group.
type Membership struct{ InGroup, HasDecision, HasConfirmation bool }

// Visible retains organizer and respondent access after leaving the source group.
func (m Membership) Visible(o Outing, actor users.ID) bool {
	return actor == o.InitiatorID || m.InGroup || m.HasDecision || (o.Arrangement != nil && m.HasConfirmation)
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
	SavePlan(context.Context, ID, Plan) error
	SaveDecision(context.Context, ID, users.ID, Choice, *MemberInput) error
	SaveConfirmation(context.Context, ID, users.ID, Choice) error
	SaveArrangement(context.Context, ID, Arrangement, bool, bool) error
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

func normalizePlan(p Plan) (Plan, error) {
	if len(p.Dates) < 1 || len(p.Dates) > 31 || p.DurationMinutes < 30 || p.DurationMinutes > 1440 || p.DurationMinutes%30 != 0 {
		return p, ErrInvalidInput
	}
	p.Dates = slices.Clone(p.Dates)
	for _, d := range p.Dates {
		if _, e := ParseDate(string(d)); e != nil {
			return p, ErrInvalidInput
		}
	}
	slices.Sort(p.Dates)
	p.Dates = slices.Compact(p.Dates)
	return p, nil
}

// Create publishes a plan for a group member without creating invitation rows.
func (s *Service) Create(ctx context.Context, actor users.ID, gid int64, timezone string, p Plan) (o Outing, err error) {
	if actor <= 0 || gid <= 0 {
		return o, ErrInvalidInput
	}
	p, err = normalizePlan(p)
	if err != nil {
		return
	}
	if _, err = Location(timezone); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = s.repo.Write(ctx, func(q Store) error {
		ok, e := q.GroupMember(ctx, gid, actor)
		if e != nil {
			return e
		}
		if !ok {
			return ErrForbidden
		}
		o, e = q.InsertOuting(ctx, actor, gid, timezone, p)
		return e
	})
	return
}
func accessible(ctx context.Context, q Store, id ID, actor users.ID) (Outing, error) {
	o, e := q.LoadOuting(ctx, id)
	if e != nil {
		return o, e
	}
	m, e := q.Membership(ctx, o, actor)
	if e != nil {
		return o, e
	}
	if !m.Visible(o, actor) {
		return o, ErrForbidden
	}
	return o, nil
}

// Get returns member input only to that member or the organizer.
// First-round input remains readable in round two, but warnings are only computed
// while members can still amend that input during the decision round.
func (s *Service) Get(ctx context.Context, actor users.ID, id ID, member users.ID) (d Detail, err error) {
	if actor <= 0 || id <= 0 || member <= 0 {
		return d, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = s.repo.Read(ctx, func(q Store) error {
		o, e := accessible(ctx, q, id, actor)
		if e != nil {
			return e
		}
		if member != actor && actor != o.InitiatorID {
			return ErrForbidden
		}
		m, e := q.Membership(ctx, o, member)
		if e != nil {
			return e
		}
		if !m.Visible(o, member) {
			return ErrForbidden
		}
		d.Outing = o
		d.Member, e = q.Member(ctx, id, member)
		d.Warnings = []Warning{}
		if e == nil && o.Arrangement == nil && d.Member.Input != nil {
			_, d.Warnings = ValidAvailability(*d.Member.Input, o.Plan)
		}
		return e
	})
	return
}

// command locks before reading permissions and stage so concurrent outing commands
// cannot invalidate those checks before the callback commits its writes.
func (s *Service) command(ctx context.Context, actor users.ID, id ID, fn func(context.Context, Store, Outing, time.Time) error) error {
	if actor <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.repo.Write(ctx, func(q Store) error {
		if e := q.LockOuting(ctx, id); e != nil {
			return e
		}
		o, e := accessible(ctx, q, id, actor)
		if e != nil {
			return e
		}
		now := s.now()
		if o.StatusAt(now) == "archived" {
			return ErrArchived
		}
		return fn(ctx, q, o, now)
	})
}

// Update replaces dates and duration before the current arrangement starts.
// Existing input is preserved even when it conflicts with the replacement plan.
func (s *Service) Update(ctx context.Context, actor users.ID, id ID, p Plan) error {
	p, e := normalizePlan(p)
	if e != nil {
		return e
	}
	return s.command(ctx, actor, id, func(ctx context.Context, q Store, o Outing, _ time.Time) error {
		if actor != o.InitiatorID {
			return ErrForbidden
		}
		return q.SavePlan(ctx, id, p)
	})
}

// Respond replaces the actor's choice in the current round.
// Only first-round Accept carries input; first-round Deny clears previous input.
func (s *Service) Respond(ctx context.Context, actor users.ID, id ID, phase Phase, choice Choice, input *MemberInput) error {
	if (phase != Decision && phase != Confirmation) || (choice != Accept && choice != Deny) {
		return ErrInvalidInput
	}
	if (phase == Decision && choice == Accept) != (input != nil) {
		return ErrInvalidInput
	}
	return s.command(ctx, actor, id, func(ctx context.Context, q Store, o Outing, _ time.Time) error {
		if (o.Arrangement == nil) != (phase == Decision) {
			return ErrPhase
		}
		if phase == Confirmation {
			return q.SaveConfirmation(ctx, id, actor, choice)
		}
		if input != nil {
			v, e := Normalize(*input, o.Dates, o.Timezone)
			if e != nil {
				return e
			}
			input = &v
		}
		return q.SaveDecision(ctx, id, actor, choice, input)
	})
}

// Arrange creates or replaces the organizer's proposal before it starts.
// update selects replacement; reset clears second-round choices while retaining
// their rows and access. Keeping choices makes the organizer responsible for consent.
func (s *Service) Arrange(ctx context.Context, actor users.ID, id ID, a Arrangement, update, reset bool) error {
	a.Place = strings.TrimSpace(a.Place)
	a.Activity = strings.TrimSpace(a.Activity)
	for _, v := range []string{a.Place, a.Activity} {
		if !utf8.ValidString(v) || strings.ContainsRune(v, 0) || utf8.RuneCountInString(v) < 1 || utf8.RuneCountInString(v) > 500 {
			return ErrInvalidInput
		}
	}
	if a.StartsAt.Year() < 1 || a.EndsAt.Year() > 9999 || !a.StartsAt.Before(a.EndsAt) {
		return ErrInvalidInput
	}
	return s.command(ctx, actor, id, func(ctx context.Context, q Store, o Outing, now time.Time) error {
		if actor != o.InitiatorID {
			return ErrForbidden
		}
		if update != (o.Arrangement != nil) {
			return ErrPhase
		}
		if !a.StartsAt.After(now) {
			return ErrInvalidInput
		}
		return q.SaveArrangement(ctx, id, a, update, reset)
	})
}

// List returns visible outings, including retained access after leaving a group.
func (s *Service) List(ctx context.Context, actor users.ID, f ListFilter) (p OutingPage, err error) {
	if actor <= 0 || f.GroupID < 0 || f.After < 0 || f.Limit < 1 || f.Limit > 100 {
		return p, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = s.repo.Read(ctx, func(q Store) error { var e error; p, e = q.ListOutings(ctx, actor, f); return e })
	return
}

// Responses returns filtered summaries across both rounds without private input.
func (s *Service) Responses(ctx context.Context, actor users.ID, id ID, f ResponseFilter) (p ResponsePage, err error) {
	if actor <= 0 || id <= 0 || f.Limit < 1 || f.Limit > 100 || f.After.MemberID < 0 || (f.Phase != "" && f.Phase != Decision && f.Phase != Confirmation) || (f.Status != "" && f.Status != Accept && f.Status != Deny && f.Status != Pending) || (f.After.Phase != "" && f.After.Phase != Decision && f.After.Phase != Confirmation) {
		return p, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = s.repo.Read(ctx, func(q Store) error {
		o, e := accessible(ctx, q, id, actor)
		if e != nil {
			return e
		}
		p, e = q.ListResponses(ctx, o, f)
		return e
	})
	return
}
