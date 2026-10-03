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
	for _, e := range []error{errs.NotFound, outing.ErrForbidden, outing.ErrInvalidInput, outing.ErrInvalidAvailability, outing.ErrPhase, outing.ErrArchived} {
		if errors.Is(err, e) {
			return err
		}
	}
	return dependency("outing operation", err)
}
func (db *DB) Read(ctx context.Context, fn func(outing.Store) error) error {
	return db.outingTransaction(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, fn)
}
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
func (s *outingStore) GroupMember(ctx context.Context, gid int64, actor users.ID) (bool, error) {
	return s.q.OutingGroupMember(ctx, generated.OutingGroupMemberParams{GroupID: gid, MemberID: int64(actor)})
}
func outingDate(d outing.Date) pgtype.Date {
	t, _ := time.Parse(time.DateOnly, string(d))
	return pgtype.Date{Time: t, Valid: true}
}
func (s *outingStore) InsertOuting(ctx context.Context, actor users.ID, gid int64, timezone string, p outing.Plan) (outing.Outing, error) {
	v, e := s.q.InsertOuting(ctx, generated.InsertOutingParams{GroupID: gid, InitiatorID: int64(actor), Timezone: timezone, DurationMinutes: int32(p.DurationMinutes)})
	if e != nil {
		return outing.Outing{}, e
	}
	for _, d := range p.Dates {
		if e = s.q.AddOutingDate(ctx, generated.AddOutingDateParams{OutingID: v.ID, Date: outingDate(d)}); e != nil {
			return outing.Outing{}, e
		}
	}
	return s.project(ctx, v)
}
func (s *outingStore) LockOuting(ctx context.Context, id outing.ID) error {
	_, e := s.q.LockOuting(ctx, int64(id))
	return e
}
func (s *outingStore) LoadOuting(ctx context.Context, id outing.ID) (outing.Outing, error) {
	v, e := s.q.ReadOuting(ctx, int64(id))
	if e != nil {
		return outing.Outing{}, e
	}
	return s.project(ctx, v)
}
func (s *outingStore) project(ctx context.Context, v generated.Outing) (o outing.Outing, err error) {
	o = outing.Outing{ID: outing.ID(v.ID), GroupID: v.GroupID, InitiatorID: users.ID(v.InitiatorID), Timezone: v.Timezone, CreatedAt: v.CreatedAt.Time, Plan: outing.Plan{DurationMinutes: int(v.DurationMinutes), Dates: []outing.Date{}}}
	ds, e := s.q.ReadOutingDates(ctx, v.ID)
	if e != nil {
		return o, e
	}
	for _, d := range ds {
		o.Dates = append(o.Dates, outing.Date(d.Time.Format(time.DateOnly)))
	}
	a, e := s.q.ReadArrangement(ctx, v.ID)
	if e == nil {
		o.Arrangement = &outing.Arrangement{StartsAt: a.StartsAt.Time, EndsAt: a.EndsAt.Time, Place: a.Place, Activity: a.Activity}
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return o, e
	}
	return o, nil
}
func (s *outingStore) Membership(ctx context.Context, o outing.Outing, actor users.ID) (outing.Membership, error) {
	m, e := s.q.OutingMembership(ctx, generated.OutingMembershipParams{GroupID: o.GroupID, OutingID: int64(o.ID), MemberID: int64(actor)})
	return outing.Membership{InGroup: m.InGroup, HasDecision: m.HasDecision, HasConfirmation: m.HasConfirmation}, e
}
func (s *outingStore) Member(ctx context.Context, id outing.ID, actor users.ID) (m outing.Member, err error) {
	m.MemberID = actor
	d, e := s.q.ReadDecision(ctx, generated.ReadDecisionParams{OutingID: int64(id), MemberID: int64(actor)})
	if e == nil {
		c := outing.Choice(d.Choice)
		m.Decision = &c
		if c == outing.Accept {
			m.Input = &outing.MemberInput{MaxBudgetFen: d.MaxBudgetFen.Int64, RequiresWheelchairAccess: d.RequiresWheelchairAccess.Bool, Availability: []outing.Availability{}}
			as, e := s.q.ReadAvailability(ctx, generated.ReadAvailabilityParams{OutingID: int64(id), MemberID: int64(actor)})
			if e != nil {
				return m, e
			}
			for _, a := range as {
				m.Input.Availability = append(m.Input.Availability, outing.Availability{Date: outing.Date(a.Date.Time.Format(time.DateOnly)), StartMinute: int(a.StartMinute), EndMinute: int(a.EndMinute)})
			}
		}
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return m, e
	}
	c, e := s.q.ReadConfirmation(ctx, generated.ReadConfirmationParams{OutingID: int64(id), MemberID: int64(actor)})
	if e == nil {
		choice := outing.Pending
		if c.Choice.Valid {
			choice = outing.Choice(c.Choice.String)
		}
		m.Confirmation = &choice
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return m, e
	}
	return m, nil
}
func (s *outingStore) SavePlan(ctx context.Context, id outing.ID, p outing.Plan) error {
	if e := s.q.UpdateOutingPlan(ctx, generated.UpdateOutingPlanParams{OutingID: int64(id), DurationMinutes: int32(p.DurationMinutes)}); e != nil {
		return e
	}
	if e := s.q.DeleteOutingDates(ctx, int64(id)); e != nil {
		return e
	}
	for _, d := range p.Dates {
		if e := s.q.AddOutingDate(ctx, generated.AddOutingDateParams{OutingID: int64(id), Date: outingDate(d)}); e != nil {
			return e
		}
	}
	return nil
}
func (s *outingStore) SaveDecision(ctx context.Context, id outing.ID, actor users.ID, choice outing.Choice, input *outing.MemberInput) error {
	p := generated.SaveDecisionParams{OutingID: int64(id), MemberID: int64(actor), Choice: string(choice)}
	if input != nil {
		p.MaxBudgetFen = pgtype.Int8{Int64: input.MaxBudgetFen, Valid: true}
		p.RequiresWheelchairAccess = pgtype.Bool{Bool: input.RequiresWheelchairAccess, Valid: true}
	}
	if e := s.q.SaveDecision(ctx, p); e != nil {
		return e
	}
	if e := s.q.DeleteAvailability(ctx, generated.DeleteAvailabilityParams{OutingID: int64(id), MemberID: int64(actor)}); e != nil {
		return e
	}
	if input != nil {
		for _, a := range input.Availability {
			if e := s.q.AddAvailability(ctx, generated.AddAvailabilityParams{OutingID: int64(id), MemberID: int64(actor), Date: outingDate(a.Date), StartMinute: int16(a.StartMinute), EndMinute: int16(a.EndMinute)}); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *outingStore) SaveConfirmation(ctx context.Context, id outing.ID, actor users.ID, choice outing.Choice) error {
	return s.q.SaveConfirmation(ctx, generated.SaveConfirmationParams{OutingID: int64(id), MemberID: int64(actor), Choice: pgtype.Text{String: string(choice), Valid: true}})
}
func (s *outingStore) SaveArrangement(ctx context.Context, id outing.ID, a outing.Arrangement, update, reset bool) error {
	var e error
	if update {
		e = s.q.UpdateArrangement(ctx, generated.UpdateArrangementParams{OutingID: int64(id), StartsAt: timestamp(a.StartsAt), EndsAt: timestamp(a.EndsAt), Place: a.Place, Activity: a.Activity})
	} else {
		e = s.q.InsertArrangement(ctx, generated.InsertArrangementParams{OutingID: int64(id), StartsAt: timestamp(a.StartsAt), EndsAt: timestamp(a.EndsAt), Place: a.Place, Activity: a.Activity})
	}
	if e != nil {
		return e
	}
	if reset {
		return s.q.ResetConfirmations(ctx, int64(id))
	}
	return nil
}
func (s *outingStore) ListOutings(ctx context.Context, actor users.ID, f outing.ListFilter) (p outing.OutingPage, err error) {
	p.Outings = []outing.Outing{}
	rows, e := s.q.ListVisibleOutings(ctx, generated.ListVisibleOutingsParams{Actor: int64(actor), GroupFilter: f.GroupID, AfterID: int64(f.After), PageLimit: int32(f.Limit + 1)})
	if e != nil {
		return p, e
	}
	if len(rows) > f.Limit {
		n := outing.ID(rows[f.Limit-1].ID)
		p.Next = &n
		rows = rows[:f.Limit]
	}
	for _, r := range rows {
		o, e := s.project(ctx, r)
		if e != nil {
			return p, e
		}
		p.Outings = append(p.Outings, o)
	}
	return p, nil
}
func (s *outingStore) ListResponses(ctx context.Context, o outing.Outing, f outing.ResponseFilter) (p outing.ResponsePage, err error) {
	p.Responses = []outing.Summary{}
	rows, e := s.q.ListOutingResponses(ctx, generated.ListOutingResponsesParams{OutingID: int64(o.ID), GroupID: o.GroupID, InitiatorID: int64(o.InitiatorID), DecisionOpen: o.Arrangement == nil, PhaseFilter: string(f.Phase), StatusFilter: string(f.Status), AfterMember: int64(f.After.MemberID), AfterPhase: string(f.After.Phase), PageLimit: int32(f.Limit + 1)})
	if e != nil {
		return p, e
	}
	if len(rows) > f.Limit {
		r := rows[f.Limit-1]
		p.Next = &outing.ResponseCursor{MemberID: users.ID(r.MemberID), Phase: outing.Phase(r.Phase)}
		rows = rows[:f.Limit]
	}
	for _, r := range rows {
		p.Responses = append(p.Responses, outing.Summary{MemberID: users.ID(r.MemberID), Phase: outing.Phase(r.Phase), Status: outing.Choice(r.Status)})
	}
	return p, nil
}

var _ outing.Repository = (*DB)(nil)
