// Package outing owns invitations, confirmed inputs and runtime outing phases.
package outing

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/Jason-skd/play-pair/internal/group"
	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/google/uuid"
)

// ID identifies a persisted outing.
type ID int64

// Phase identifies an independent invitation round.
type Phase string

// Status is derived from the arrangement and a caller-supplied clock.
type Status string

// Response distinguishes an initial refusal from withdrawal after acceptance.
type Response string

const (
	Decision           Phase    = "decision"
	Confirmation       Phase    = "confirmation"
	StatusDecision     Status   = "decision"
	StatusConfirmation Status   = "confirmation"
	Archived           Status   = "archived"
	Pending            Response = "pending"
	Accepted           Response = "accepted"
	Rejected           Response = "rejected"
	Declined           Response = "declined"
)

var (
	ErrInvalidInput        = errors.New("invalid outing input")
	ErrInvalidAvailability = errors.New("invalid availability")
	ErrForbidden           = errors.New("outing membership required")
	ErrIdempotencyConflict = errors.New("outing creation key conflict")
	ErrPhaseConflict       = errors.New("invitation phase conflict")
	ErrArchived            = errors.New("outing archived")
)

// Date is a validated local calendar date, independent of an instant or timezone.
type Date string

// ParseDate accepts an exact Gregorian YYYY-MM-DD date in years 1 through 9999.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil || t.Year() < 1 || t.Format(time.DateOnly) != s {
		return "", ErrInvalidAvailability
	}
	return Date(s), nil
}

// Availability is a closed local interval. EndMinute may be 1440 (24:00).
type Availability struct {
	Date                   Date
	StartMinute, EndMinute int
}

// MemberInput is explicit consent for one outing, not a saved preference default.
type MemberInput struct {
	MaxBudgetFen             int64
	RequiresWheelchairAccess bool
	Availability             []Availability
}

// Arrangement records immutable, confirmed facts; it is not a ranked candidate.
type Arrangement struct {
	StartsAt, EndsAt time.Time
	Place, Activity  string
}

// Outing combines persisted facts with the requesting member's invitations.
type Outing struct {
	ID              ID
	GroupID         group.ID
	InitiatorID     users.ID
	Timezone        string
	Dates           []Date
	DurationMinutes int
	CreatedAt       time.Time
	Arrangement     *Arrangement
	Invitations     []Invitation
}

// StatusAt archives at the exact start instant without changing persistent data.
func (o Outing) StatusAt(now time.Time) Status {
	if o.Arrangement == nil {
		return StatusDecision
	}
	if !now.Before(o.Arrangement.StartsAt) {
		return Archived
	}
	return StatusConfirmation
}

// Invitation owns the revision and timestamps of one member's response and input.
type Invitation struct {
	OutingID   ID
	MemberID   users.ID
	Phase      Phase
	Status     Response
	Version    int64
	AcceptedAt *time.Time
	UpdatedAt  time.Time
	Input      *MemberInput
}

// Cursor orders invitation pages by outing, member, then phase.
type Cursor struct {
	OutingID ID
	MemberID users.ID
	Phase    Phase
}

// Page contains summaries without private inputs, plus an optional next cursor.
type Page struct {
	Invitations []Invitation
	Next        *Cursor
}

// Create publishes one group outing with an actor-scoped permanent creation key.
type Create struct {
	GroupID         group.ID
	Key             uuid.UUID
	Dates           []Date
	Timezone        string
	DurationMinutes int
}

// Respond replaces a round's response; only decision acceptance carries input.
type Respond struct {
	OutingID        ID
	Phase           Phase
	Status          Response
	ExpectedVersion int64
	Input           *MemberInput
}

// Repository implements atomic commands and rechecks phase and cutoff under lock.
type Repository interface {
	CreateOuting(context.Context, users.ID, Create) (Outing, bool, error)
	GetOuting(context.Context, users.ID, ID) (Outing, error)
	RespondOuting(context.Context, users.ID, Respond) (Invitation, error)
	ListOutingInvitations(context.Context, users.ID, ID, Cursor, int) (Page, error)
}

// Service validates commands and bounds repository calls; callers own its clock.
type Service struct {
	repo Repository
	now  func() time.Time
}

// New constructs a service with explicit persistence and response-clock dependencies.
func New(repo Repository, now func() time.Time) *Service { return &Service{repo: repo, now: now} }

// Now supplies one clock reading for a complete response projection.
func (s *Service) Now() time.Time { return s.now() }

// Create canonicalizes dates before atomic publication and idempotency comparison.
func (s *Service) Create(ctx context.Context, actor users.ID, c Create) (Outing, bool, error) {
	if actor <= 0 || c.GroupID <= 0 || len(c.Dates) == 0 || len(c.Dates) > 31 || c.DurationMinutes < 30 || c.DurationMinutes > 1440 || c.DurationMinutes%30 != 0 {
		return Outing{}, false, ErrInvalidInput
	}
	if _, err := Location(c.Timezone); err != nil {
		return Outing{}, false, err
	}
	c.Dates = slices.Clone(c.Dates)
	for _, d := range c.Dates {
		if _, err := ParseDate(string(d)); err != nil {
			return Outing{}, false, ErrInvalidInput
		}
	}
	slices.Sort(c.Dates)
	c.Dates = slices.Compact(c.Dates)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.repo.CreateOuting(ctx, actor, c)
}

// Get returns an outing visible to actor, including only actor's private inputs.
func (s *Service) Get(ctx context.Context, actor users.ID, id ID) (Outing, error) {
	if actor <= 0 || id <= 0 {
		return Outing{}, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.repo.GetOuting(ctx, actor, id)
}

// Respond validates the command shape; the repository validates inputs against
// locked outing facts before committing the response and input together.
func (s *Service) Respond(ctx context.Context, actor users.ID, c Respond) (Invitation, error) {
	if actor <= 0 || c.OutingID <= 0 || (c.Phase != Decision && c.Phase != Confirmation) || c.ExpectedVersion < 1 || c.ExpectedVersion == math.MaxInt64 || (c.Status != Accepted && c.Status != Rejected && c.Status != Declined) {
		return Invitation{}, ErrInvalidInput
	}
	if c.Status == Accepted && c.Phase == Decision {
		if c.Input == nil || c.Input.MaxBudgetFen < 0 {
			return Invitation{}, ErrInvalidInput
		}
	} else if c.Input != nil {
		return Invitation{}, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.repo.RespondOuting(ctx, actor, c)
}

// Invitations with id zero lists the actor's own inbox; other IDs list group summaries.
func (s *Service) Invitations(ctx context.Context, actor users.ID, id ID, c Cursor, limit int) (Page, error) {
	if actor <= 0 || id < 0 || limit < 1 || limit > 100 || c.OutingID < 0 || c.MemberID < 0 || (c.Phase != "" && c.Phase != Decision && c.Phase != Confirmation) {
		return Page{}, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.repo.ListOutingInvitations(ctx, actor, id, c, limit)
}

// Location rejects implicit server-local interpretation.
func Location(name string) (*time.Location, error) {
	if name == "" || name == "Local" || strings.HasPrefix(name, "/") {
		return nil, ErrInvalidInput
	}
	l, err := time.LoadLocation(name)
	if err != nil {
		return nil, ErrInvalidInput
	}
	return l, nil
}

// ParseMinute accepts half-hour boundaries; only an end may be 24:00.
func ParseMinute(s string, end bool) (int, error) {
	if s == "24:00" && end {
		return 1440, nil
	}
	t, err := time.Parse("15:04", s)
	if err != nil || t.Format("15:04") != s || t.Minute()%30 != 0 {
		return 0, ErrInvalidAvailability
	}
	return t.Hour()*60 + t.Minute(), nil
}

// Normalize validates every half-hour boundary, including DST gaps and folds,
// before merging overlapping closed intervals. It never splits invalid input.
func Normalize(input MemberInput, dates []Date, timezone string) (MemberInput, error) {
	if input.MaxBudgetFen < 0 {
		return MemberInput{}, ErrInvalidInput
	}
	if len(input.Availability) == 0 || len(input.Availability) > 256 {
		return MemberInput{}, ErrInvalidAvailability
	}
	loc, err := Location(timezone)
	if err != nil {
		return MemberInput{}, err
	}
	values := slices.Clone(input.Availability)
	for _, v := range values {
		if !slices.Contains(dates, v.Date) || v.StartMinute < 0 || v.EndMinute > 1440 || v.StartMinute >= v.EndMinute || v.StartMinute%30 != 0 || v.EndMinute%30 != 0 {
			return MemberInput{}, ErrInvalidAvailability
		}
		d, err := time.Parse(time.DateOnly, string(v.Date))
		if err != nil {
			return MemberInput{}, ErrInvalidAvailability
		}
		for m := v.StartMinute; m <= v.EndMinute; m += 30 {
			if !uniqueLocal(d.Add(time.Duration(m)*time.Minute), loc) {
				return MemberInput{}, ErrInvalidAvailability
			}
		}
	}
	slices.SortFunc(values, func(a, b Availability) int {
		if a.Date < b.Date {
			return -1
		}
		if a.Date > b.Date {
			return 1
		}
		return a.StartMinute - b.StartMinute
	})
	merged := make([]Availability, 0, len(values))
	for _, v := range values {
		if len(merged) > 0 {
			p := &merged[len(merged)-1]
			if p.Date == v.Date && v.StartMinute <= p.EndMinute {
				p.EndMinute = max(p.EndMinute, v.EndMinute)
				continue
			}
		}
		merged = append(merged, v)
	}
	input.Availability = merged
	return input, nil
}

// Enumerate offsets on nearby zone segments rather than relying on time.Date's
// unspecified choice for repeated or nonexistent wall times.
func uniqueLocal(wall time.Time, loc *time.Location) bool {
	offsets := map[int]bool{}
	start, end := wall.Add(-72*time.Hour), wall.Add(72*time.Hour)
	for t := start; t.Before(end); {
		local := t.In(loc)
		_, offset := local.Zone()
		offsets[offset] = true
		_, next := local.ZoneBounds()
		if next.IsZero() || !next.Before(end) {
			break
		}
		t = next
	}
	matches := 0
	for offset := range offsets {
		t := wall.Add(-time.Duration(offset) * time.Second).In(loc)
		if t.Year() == wall.Year() && t.Month() == wall.Month() && t.Day() == wall.Day() && t.Hour() == wall.Hour() && t.Minute() == wall.Minute() && t.Second() == 0 {
			matches++
		}
	}
	return matches == 1
}
