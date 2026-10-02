package db

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"time"

	"github.com/Jason-skd/play-pair/internal/db/generated"
	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/Jason-skd/play-pair/internal/group"
	"github.com/Jason-skd/play-pair/internal/outing"
	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func outingError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return errs.NotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.ConstraintName {
		case "outing_archived":
			return outing.ErrArchived
		case "outing_phase":
			return outing.ErrPhaseConflict
		}
	}
	for _, known := range []error{errs.NotFound, errs.VersionConflict, outing.ErrForbidden, outing.ErrInvalidInput, outing.ErrInvalidAvailability, outing.ErrIdempotencyConflict, outing.ErrPhaseConflict, outing.ErrArchived} {
		if errors.Is(err, known) {
			return err
		}
	}
	return dependency("outing operation", err)
}
func outingAccess(ctx context.Context, q *generated.Queries, actor users.ID, gid int64) error {
	allowed, err := q.GroupAccess(ctx, generated.GroupAccessParams{GroupID: gid, Actor: int64(actor)})
	if err != nil {
		return err
	}
	if !allowed {
		return outing.ErrForbidden
	}
	return nil
}
func outingDate(d outing.Date) pgtype.Date {
	t, _ := time.Parse(time.DateOnly, string(d))
	return pgtype.Date{Time: t, Valid: true}
}
func invitationResult(v generated.OutingInvitation) outing.Invitation {
	i := outing.Invitation{OutingID: outing.ID(v.OutingID), MemberID: users.ID(v.MemberID), Phase: outing.Phase(v.Phase), Status: outing.Response(v.Status), Version: v.Version, UpdatedAt: v.UpdatedAt.Time}
	if v.AcceptedAt.Valid {
		t := v.AcceptedAt.Time
		i.AcceptedAt = &t
	}
	return i
}
func readInvitationInput(ctx context.Context, q *generated.Queries, i *outing.Invitation) error {
	if i.Phase != outing.Decision || i.Status != outing.Accepted {
		return nil
	}
	v, err := q.ReadMemberInput(ctx, generated.ReadMemberInputParams{OutingID: int64(i.OutingID), MemberID: int64(i.MemberID)})
	if err != nil {
		return err
	}
	a, err := q.ReadAvailability(ctx, generated.ReadAvailabilityParams{OutingID: int64(i.OutingID), MemberID: int64(i.MemberID)})
	if err != nil {
		return err
	}
	i.Input = &outing.MemberInput{MaxBudgetFen: v.MaxBudgetFen, RequiresWheelchairAccess: v.RequiresWheelchairAccess, Availability: make([]outing.Availability, 0, len(a))}
	for _, r := range a {
		i.Input.Availability = append(i.Input.Availability, outing.Availability{Date: outing.Date(r.Date.Time.Format(time.DateOnly)), StartMinute: int(r.StartMinute), EndMinute: int(r.EndMinute)})
	}
	return nil
}
func readOutingResult(ctx context.Context, q *generated.Queries, v generated.Outing, actor users.ID) (outing.Outing, error) {
	o := outing.Outing{ID: outing.ID(v.ID), GroupID: group.ID(v.GroupID), InitiatorID: users.ID(v.InitiatorID), Timezone: v.Timezone, DurationMinutes: int(v.DurationMinutes), CreatedAt: v.CreatedAt.Time, Dates: []outing.Date{}, Invitations: []outing.Invitation{}}
	dates, err := q.ReadOutingDates(ctx, v.ID)
	if err != nil {
		return o, err
	}
	for _, d := range dates {
		o.Dates = append(o.Dates, outing.Date(d.Time.Format(time.DateOnly)))
	}
	a, err := q.ReadArrangement(ctx, v.ID)
	if err == nil {
		o.Arrangement = &outing.Arrangement{StartsAt: a.StartsAt.Time, EndsAt: a.EndsAt.Time, Place: a.Place, Activity: a.Activity}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return o, err
	}
	for _, phase := range []outing.Phase{outing.Decision, outing.Confirmation} {
		v, err := q.ReadInvitation(ctx, generated.ReadInvitationParams{OutingID: v.ID, MemberID: int64(actor), Phase: string(phase)})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return o, err
		}
		i := invitationResult(v)
		if err := readInvitationInput(ctx, q, &i); err != nil {
			return o, err
		}
		o.Invitations = append(o.Invitations, i)
	}
	return o, nil
}
func (db *DB) CreateOuting(ctx context.Context, actor users.ID, c outing.Create) (result outing.Outing, created bool, err error) {
	defer func() { err = outingError(err) }()
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return result, false, err
	}
	defer rollbackGroup(tx)
	q := db.queries.WithTx(tx)
	if _, err = q.LockOutingGroup(ctx, int64(c.GroupID)); err != nil {
		return result, false, err
	}
	if err = outingAccess(ctx, q, actor, int64(c.GroupID)); err != nil {
		return result, false, err
	}
	key := pgtype.UUID{Bytes: c.Key, Valid: true}
	v, err := q.CreateOuting(ctx, generated.CreateOutingParams{GroupID: int64(c.GroupID), Actor: int64(actor), Timezone: c.Timezone, DurationMinutes: int32(c.DurationMinutes), CreationKey: key})
	if errors.Is(err, pgx.ErrNoRows) {
		v, err = q.FindOutingCreation(ctx, generated.FindOutingCreationParams{Actor: int64(actor), CreationKey: key})
		if err != nil {
			return result, false, err
		}
		result, err = readOutingResult(ctx, q, v, actor)
		if err != nil {
			return result, false, err
		}
		if result.GroupID != c.GroupID || result.Timezone != c.Timezone || result.DurationMinutes != c.DurationMinutes || !slices.Equal(result.Dates, c.Dates) {
			return outing.Outing{}, false, outing.ErrIdempotencyConflict
		}
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	for _, d := range c.Dates {
		if err = q.AddOutingDate(ctx, generated.AddOutingDateParams{OutingID: v.ID, Date: outingDate(d)}); err != nil {
			return result, false, err
		}
	}
	if err = q.InviteOutingGroup(ctx, v.ID); err != nil {
		return result, false, err
	}
	result, err = readOutingResult(ctx, q, v, actor)
	if err != nil {
		return result, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return outing.Outing{}, false, err
	}
	return result, true, nil
}

// Repeatable read keeps the arrangement, phase invitations and inputs from one
// committed snapshot; a concurrent response cannot produce a torn projection.
func (db *DB) GetOuting(ctx context.Context, actor users.ID, id outing.ID) (result outing.Outing, err error) {
	defer func() { err = outingError(err) }()
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, err
	}
	defer rollbackGroup(tx)
	q := db.queries.WithTx(tx)
	v, err := q.ReadOuting(ctx, int64(id))
	if err != nil {
		return result, err
	}
	if err = outingAccess(ctx, q, actor, v.GroupID); err != nil {
		return result, err
	}
	return readOutingResult(ctx, q, v, actor)
}
func (db *DB) RespondOuting(ctx context.Context, actor users.ID, c outing.Respond) (result outing.Invitation, err error) {
	defer func() { err = outingError(err) }()
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer rollbackGroup(tx)
	q := db.queries.WithTx(tx)
	o, err := q.ReadOuting(ctx, int64(c.OutingID))
	if err != nil {
		return result, err
	}
	if _, err = q.LockOutingGroup(ctx, o.GroupID); err != nil {
		return result, err
	}
	if err = outingAccess(ctx, q, actor, o.GroupID); err != nil {
		return result, err
	}
	if err = q.CheckOutingPhase(ctx, generated.CheckOutingPhaseParams{OutingID: o.ID, Phase: string(c.Phase)}); err != nil {
		return result, err
	}
	lookup := generated.ReadInvitationParams{OutingID: o.ID, MemberID: int64(actor), Phase: string(c.Phase)}
	v, err := q.ReadInvitation(ctx, lookup)
	if err != nil {
		return result, err
	}
	result = invitationResult(v)
	if err = readInvitationInput(ctx, q, &result); err != nil {
		return result, err
	}
	if c.Input != nil {
		dates, e := q.ReadOutingDates(ctx, o.ID)
		if e != nil {
			return result, e
		}
		ds := make([]outing.Date, 0, len(dates))
		for _, d := range dates {
			ds = append(ds, outing.Date(d.Time.Format(time.DateOnly)))
		}
		input, e := outing.Normalize(*c.Input, ds, o.Timezone)
		if e != nil {
			return result, e
		}
		c.Input = &input
	}
	if result.Status == c.Status && reflect.DeepEqual(result.Input, c.Input) {
		return result, nil
	}
	if result.Version != c.ExpectedVersion {
		return result, errs.VersionConflict
	}
	if (c.Status == outing.Declined && result.Status != outing.Accepted && result.Status != outing.Declined) || (c.Status == outing.Rejected && (result.Status == outing.Accepted || result.Status == outing.Declined)) {
		return result, outing.ErrPhaseConflict
	}
	if err = q.SetInvitationResponse(ctx, generated.SetInvitationResponseParams{OutingID: o.ID, MemberID: int64(actor), Phase: string(c.Phase), Status: string(c.Status)}); err != nil {
		return result, err
	}
	if c.Phase == outing.Decision {
		if err = q.DeleteAvailability(ctx, generated.DeleteAvailabilityParams{OutingID: o.ID, MemberID: int64(actor)}); err != nil {
			return result, err
		}
		if err = q.DeleteMemberInput(ctx, generated.DeleteMemberInputParams{OutingID: o.ID, MemberID: int64(actor)}); err != nil {
			return result, err
		}
		if c.Input != nil {
			if err = q.AddMemberInput(ctx, generated.AddMemberInputParams{OutingID: o.ID, MemberID: int64(actor), MaxBudgetFen: c.Input.MaxBudgetFen, RequiresWheelchairAccess: c.Input.RequiresWheelchairAccess}); err != nil {
				return result, err
			}
			for _, a := range c.Input.Availability {
				if err = q.AddAvailability(ctx, generated.AddAvailabilityParams{OutingID: o.ID, MemberID: int64(actor), Date: outingDate(a.Date), StartMinute: int16(a.StartMinute), EndMinute: int16(a.EndMinute)}); err != nil {
					return result, err
				}
			}
		}
	}
	v, err = q.ReadInvitation(ctx, lookup)
	if err != nil {
		return result, err
	}
	result = invitationResult(v)
	if err = readInvitationInput(ctx, q, &result); err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return outing.Invitation{}, err
	}
	return result, nil
}
func (db *DB) ListOutingInvitations(ctx context.Context, actor users.ID, id outing.ID, c outing.Cursor, limit int) (page outing.Page, err error) {
	defer func() { err = outingError(err) }()
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return page, err
	}
	defer rollbackGroup(tx)
	q := db.queries.WithTx(tx)
	if id != 0 {
		o, e := q.ReadOuting(ctx, int64(id))
		if e != nil {
			return page, e
		}
		if err = outingAccess(ctx, q, actor, o.GroupID); err != nil {
			return page, err
		}
	}
	rows, err := q.ListInvitations(ctx, generated.ListInvitationsParams{OutingID: int64(id), Actor: int64(actor), AfterOuting: int64(c.OutingID), AfterMember: int64(c.MemberID), AfterPhase: string(c.Phase), PageLimit: int32(limit + 1)})
	if err != nil {
		return page, err
	}
	page.Invitations = make([]outing.Invitation, 0, min(limit, len(rows)))
	if len(rows) > limit {
		last := rows[limit-1]
		page.Next = &outing.Cursor{OutingID: outing.ID(last.OutingID), MemberID: users.ID(last.MemberID), Phase: outing.Phase(last.Phase)}
		rows = rows[:limit]
	}
	for _, v := range rows {
		page.Invitations = append(page.Invitations, invitationResult(v))
	}
	return page, nil
}

var _ outing.Repository = (*DB)(nil)
