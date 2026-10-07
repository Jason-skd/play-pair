package db

import (
	"context"
	"errors"
	"time"

	"github.com/Jason-skd/play-pair/internal/db/generated"
	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/Jason-skd/play-pair/internal/outing"
	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type outingStore struct{ q *generated.Queries }

func outingError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return errs.NotFound
	}
	for _, knownErr := range []error{errs.NotFound, outing.ErrForbidden, outing.ErrInvalidInput, outing.ErrInvalidAvailability, outing.ErrPhase, outing.ErrArchived, outing.ErrNoParticipants, outing.ErrNoCommonTime, outing.ErrAggregationCapacity, outing.ErrAggregationInput} {
		if errors.Is(err, knownErr) {
			return err
		}
	}
	return dependency("outing operation", err)
}

// Read keeps the outing, access facts and member input in one read-only snapshot.
func (db *DB) Read(ctx context.Context, fn func(outing.Store) error) error {
	return db.outingTransaction(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, fn)
}

// Write commits the callback atomically, including deferred input-completeness checks.
// The domain service acquires the outing row lock before checking mutable state.
func (db *DB) Write(ctx context.Context, fn func(outing.Store) error) error {
	return db.outingTransaction(ctx, pgx.TxOptions{}, fn)
}
func (db *DB) outingTransaction(ctx context.Context, opts pgx.TxOptions, fn func(outing.Store) error) (err error) {
	defer func() { err = outingError(err) }()
	tx, err := db.pool.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	defer rollbackGroup(tx)
	if err = fn(&outingStore{q: db.queries.WithTx(tx)}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *outingStore) GroupMember(ctx context.Context, groupID int64, actor users.ID) (bool, error) {
	return s.q.OutingGroupMember(ctx, generated.OutingGroupMemberParams{GroupID: groupID, MemberID: int64(actor)})
}

// outingDate converts dates already validated by the domain service.
func outingDate(date outing.Date) pgtype.Date {
	parsed, _ := time.Parse(time.DateOnly, string(date))
	return pgtype.Date{Time: parsed, Valid: true}
}
func (s *outingStore) InsertOuting(ctx context.Context, actor users.ID, groupID int64, timezone string, plan outing.Plan) (outing.Outing, error) {
	row, err := s.q.InsertOuting(ctx, generated.InsertOutingParams{GroupID: groupID, InitiatorID: int64(actor), Timezone: timezone, DurationMinutes: int32(plan.DurationMinutes)})
	if err != nil {
		return outing.Outing{}, err
	}
	for _, date := range plan.Dates {
		if err = s.q.AddOutingDate(ctx, generated.AddOutingDateParams{OutingID: row.ID, Date: outingDate(date)}); err != nil {
			return outing.Outing{}, err
		}
	}
	return s.project(ctx, row)
}
func (s *outingStore) LockOuting(ctx context.Context, id outing.ID) error {
	_, err := s.q.LockOuting(ctx, int64(id))
	return err
}
func (s *outingStore) LoadOuting(ctx context.Context, id outing.ID) (outing.Outing, error) {
	row, err := s.q.ReadOuting(ctx, int64(id))
	if err != nil {
		return outing.Outing{}, err
	}
	return s.project(ctx, row)
}
func (s *outingStore) project(ctx context.Context, row generated.Outing) (outing.Outing, error) {
	result := outing.Outing{
		ID:          outing.ID(row.ID),
		GroupID:     row.GroupID,
		InitiatorID: users.ID(row.InitiatorID),
		Timezone:    row.Timezone,
		CreatedAt:   row.CreatedAt.Time,
		Plan: outing.Plan{
			DurationMinutes: int(row.DurationMinutes),
			Dates:           []outing.Date{},
		},
	}
	dates, err := s.q.ReadOutingDates(ctx, row.ID)
	if err != nil {
		return result, err
	}
	for _, date := range dates {
		result.Dates = append(result.Dates, outing.Date(date.Time.Format(time.DateOnly)))
	}
	arrangement, err := s.q.ReadArrangement(ctx, row.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.Arrangement = &outing.Arrangement{
		StartsAt: arrangement.StartsAt.Time,
		EndsAt:   arrangement.EndsAt.Time,
		Place:    arrangement.Place,
		Activity: arrangement.Activity,
	}
	return result, nil
}
func (s *outingStore) Membership(ctx context.Context, current outing.Outing, actor users.ID) (outing.Membership, error) {
	membership, err := s.q.OutingMembership(ctx, generated.OutingMembershipParams{GroupID: current.GroupID, OutingID: int64(current.ID), MemberID: int64(actor)})
	return outing.Membership{InGroup: membership.InGroup, HasDecision: membership.HasDecision, HasConfirmation: membership.HasConfirmation}, err
}
func (s *outingStore) Member(ctx context.Context, id outing.ID, actor users.ID) (outing.Member, error) {
	member := outing.Member{MemberID: actor}
	decision, err := s.q.ReadDecision(ctx, generated.ReadDecisionParams{OutingID: int64(id), MemberID: int64(actor)})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return member, err
	}
	if err == nil {
		choice := outing.Choice(decision.Choice)
		member.Decision = &choice
		if choice == outing.Accept {
			member.Input = &outing.MemberInput{
				MaxBudgetFen:             decision.MaxBudgetFen.Int64,
				RequiresWheelchairAccess: decision.RequiresWheelchairAccess.Bool,
				Availability:             []outing.Availability{},
			}
			availability, err := s.q.ReadAvailability(ctx, generated.ReadAvailabilityParams{OutingID: int64(id), MemberID: int64(actor)})
			if err != nil {
				return member, err
			}
			for _, interval := range availability {
				member.Input.Availability = append(member.Input.Availability, outing.Availability{
					Date:        outing.Date(interval.Date.Time.Format(time.DateOnly)),
					StartMinute: int(interval.StartMinute),
					EndMinute:   int(interval.EndMinute),
				})
			}
		}
	}
	confirmation, err := s.q.ReadConfirmation(ctx, generated.ReadConfirmationParams{OutingID: int64(id), MemberID: int64(actor)})
	if errors.Is(err, pgx.ErrNoRows) {
		return member, nil
	}
	if err != nil {
		return member, err
	}
	choice := outing.Pending
	if confirmation.Choice.Valid {
		choice = outing.Choice(confirmation.Choice.String)
	}
	member.Confirmation = &choice
	return member, nil
}

// AcceptedInputs selects respondents, independent of current group membership.
// Queries read at most one extra member/interval beyond the domain limits, so
// exceeding capacity is reported instead of truncating input or unbounded reads.
func (s *outingStore) AcceptedInputs(ctx context.Context, id outing.ID) ([]outing.ParticipantInput, error) {
	decisions, err := s.q.ReadAcceptedDecisions(ctx, int64(id))
	if err != nil {
		return nil, err
	}
	if len(decisions) > outing.MaxAggregationMembers {
		return nil, outing.ErrAggregationCapacity
	}
	result := make([]outing.ParticipantInput, 0, len(decisions))
	for _, decision := range decisions {
		if !decision.MaxBudgetFen.Valid || !decision.RequiresWheelchairAccess.Valid {
			return nil, outing.ErrAggregationInput
		}
		intervals, err := s.q.ReadDecisionAvailability(ctx, generated.ReadDecisionAvailabilityParams{OutingID: int64(id), MemberID: decision.MemberID})
		if err != nil {
			return nil, err
		}
		if len(intervals) > 256 {
			return nil, outing.ErrAggregationCapacity
		}
		input := outing.MemberInput{MaxBudgetFen: decision.MaxBudgetFen.Int64, RequiresWheelchairAccess: decision.RequiresWheelchairAccess.Bool, Availability: make([]outing.Availability, 0, len(intervals))}
		for _, interval := range intervals {
			if !interval.Date.Valid || interval.Date.InfinityModifier != pgtype.Finite {
				return nil, outing.ErrAggregationInput
			}
			input.Availability = append(input.Availability, outing.Availability{Date: outing.Date(interval.Date.Time.Format(time.DateOnly)), StartMinute: int(interval.StartMinute), EndMinute: int(interval.EndMinute)})
		}
		result = append(result, outing.ParticipantInput{MemberID: users.ID(decision.MemberID), Input: input})
	}
	return result, nil
}

// SavePlan replaces candidate dates without deleting stale member availability.
func (s *outingStore) SavePlan(ctx context.Context, id outing.ID, plan outing.Plan) error {
	if err := s.q.UpdateOutingPlan(ctx, generated.UpdateOutingPlanParams{OutingID: int64(id), DurationMinutes: int32(plan.DurationMinutes)}); err != nil {
		return err
	}
	if err := s.q.DeleteOutingDates(ctx, int64(id)); err != nil {
		return err
	}
	for _, date := range plan.Dates {
		if err := s.q.AddOutingDate(ctx, generated.AddOutingDateParams{OutingID: int64(id), Date: outingDate(date)}); err != nil {
			return err
		}
	}
	return nil
}

// SaveDecision replaces choice and input together. Deferred completeness checks
// run at commit, allowing the old availability to be removed before replacement.
func (s *outingStore) SaveDecision(ctx context.Context, id outing.ID, actor users.ID, choice outing.Choice, input *outing.MemberInput) error {
	params := generated.SaveDecisionParams{OutingID: int64(id), MemberID: int64(actor), Choice: string(choice)}
	if input != nil {
		params.MaxBudgetFen = pgtype.Int8{Int64: input.MaxBudgetFen, Valid: true}
		params.RequiresWheelchairAccess = pgtype.Bool{Bool: input.RequiresWheelchairAccess, Valid: true}
	}
	if err := s.q.SaveDecision(ctx, params); err != nil {
		return err
	}
	if err := s.q.DeleteAvailability(ctx, generated.DeleteAvailabilityParams{OutingID: int64(id), MemberID: int64(actor)}); err != nil {
		return err
	}
	if input != nil {
		for _, interval := range input.Availability {
			if err := s.q.AddAvailability(ctx, generated.AddAvailabilityParams{
				OutingID:    int64(id),
				MemberID:    int64(actor),
				Date:        outingDate(interval.Date),
				StartMinute: int16(interval.StartMinute),
				EndMinute:   int16(interval.EndMinute),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *outingStore) SaveConfirmation(ctx context.Context, id outing.ID, actor users.ID, choice outing.Choice) error {
	return s.q.SaveConfirmation(ctx, generated.SaveConfirmationParams{OutingID: int64(id), MemberID: int64(actor), Choice: pgtype.Text{String: string(choice), Valid: true}})
}
func (s *outingStore) SaveArrangement(ctx context.Context, id outing.ID, arrangement outing.Arrangement, updateExisting, resetResponses bool) error {
	var err error
	if updateExisting {
		err = s.q.UpdateArrangement(ctx, generated.UpdateArrangementParams{
			OutingID: int64(id),
			StartsAt: timestamp(arrangement.StartsAt),
			EndsAt:   timestamp(arrangement.EndsAt),
			Place:    arrangement.Place,
			Activity: arrangement.Activity,
		})
	} else {
		err = s.q.InsertArrangement(ctx, generated.InsertArrangementParams{
			OutingID: int64(id),
			StartsAt: timestamp(arrangement.StartsAt),
			EndsAt:   timestamp(arrangement.EndsAt),
			Place:    arrangement.Place,
			Activity: arrangement.Activity,
		})
	}
	if err != nil {
		return err
	}
	if resetResponses {
		// Clear choices rather than deleting rows: respondents retain access even
		// if they have left the group and never answered the first round.
		return s.q.ResetConfirmations(ctx, int64(id))
	}
	return nil
}
func (s *outingStore) ListOutings(ctx context.Context, actor users.ID, filter outing.ListFilter) (outing.OutingPage, error) {
	page := outing.OutingPage{Outings: []outing.Outing{}}
	rows, err := s.q.ListVisibleOutings(ctx, generated.ListVisibleOutingsParams{
		Actor:       int64(actor),
		GroupFilter: filter.GroupID,
		AfterID:     int64(filter.After),
		PageLimit:   int32(filter.Limit + 1),
	})
	if err != nil {
		return page, err
	}
	if len(rows) > filter.Limit {
		nextID := outing.ID(rows[filter.Limit-1].ID)
		page.Next = &nextID
		rows = rows[:filter.Limit]
	}
	for _, row := range rows {
		current, err := s.project(ctx, row)
		if err != nil {
			return page, err
		}
		page.Outings = append(page.Outings, current)
	}
	return page, nil
}
func (s *outingStore) ListResponses(ctx context.Context, current outing.Outing, filter outing.ResponseFilter) (outing.ResponsePage, error) {
	page := outing.ResponsePage{Responses: []outing.Summary{}}
	rows, err := s.q.ListOutingResponses(ctx, generated.ListOutingResponsesParams{
		OutingID:     int64(current.ID),
		GroupID:      current.GroupID,
		InitiatorID:  int64(current.InitiatorID),
		DecisionOpen: current.Arrangement == nil,
		PhaseFilter:  string(filter.Phase),
		StatusFilter: string(filter.Status),
		AfterMember:  int64(filter.After.MemberID),
		AfterPhase:   string(filter.After.Phase),
		PageLimit:    int32(filter.Limit + 1),
	})
	if err != nil {
		return page, err
	}
	if len(rows) > filter.Limit {
		row := rows[filter.Limit-1]
		page.Next = &outing.ResponseCursor{MemberID: users.ID(row.MemberID), Phase: outing.Phase(row.Phase)}
		rows = rows[:filter.Limit]
	}
	for _, row := range rows {
		page.Responses = append(page.Responses, outing.Summary{MemberID: users.ID(row.MemberID), Phase: outing.Phase(row.Phase), Status: outing.Choice(row.Status)})
	}
	return page, nil
}

var _ outing.Repository = (*DB)(nil)
