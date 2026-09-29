package db

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"io"
	"time"

	"github.com/Jason-skd/play-pair/internal/db/generated"
	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/Jason-skd/play-pair/internal/group"
	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// rollbackGroup gets its own bounded context, even when the operation was canceled.
func rollbackGroup(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
func groupResult(v generated.Group) group.Group {
	return group.Group{ID: group.ID(v.ID), Name: v.Name, JoinCode: v.JoinCode, InitiatorID: users.ID(v.InitiatorID), CreatedAt: v.CreatedAt.Time, UpdatedAt: v.UpdatedAt.Time}
}
func replayGroup(v generated.Group, name string) (group.Group, bool, error) {
	if v.Name != name {
		return group.Group{}, false, group.ErrIdempotencyConflict
	}
	return groupResult(v), false, nil
}

func (db *DB) CreateGroup(ctx context.Context, actor users.ID, key uuid.UUID, name string) (group.Group, bool, error) {
	return db.createGroup(ctx, actor, key, name, rand.Reader)
}

// The random reader is scoped to this operation, allowing deterministic collision
// tests without changing shared state or the production source of entropy.
func (db *DB) createGroup(ctx context.Context, actor users.ID, key uuid.UUID, name string, random io.Reader) (group.Group, bool, error) {
	lookup := generated.FindGroupCreationParams{InitiatorID: int64(actor), CreationKey: pgtype.UUID{Bytes: key, Valid: true}}
	for attempt := 0; attempt < 5; attempt++ {
		// Also check after a code collision: a concurrent idempotent request may win.
		v, err := db.queries.FindGroupCreation(ctx, lookup)
		if err == nil {
			return replayGroup(v, name)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return group.Group{}, false, dependency("find group creation", err)
		}
		var entropy [10]byte
		if _, err = io.ReadFull(random, entropy[:]); err != nil {
			return group.Group{}, false, dependency("generate group code", err)
		}
		code := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(entropy[:])
		v, created, err := db.createGroupAttempt(ctx, lookup, name, code)
		if err == nil {
			if !created {
				return replayGroup(v, name)
			}
			return groupResult(v), true, nil
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23505" || pgErr.ConstraintName != "groups_join_code_key" {
			return group.Group{}, false, dependency("create group", err)
		}
	}
	// A concurrent creation can win during the last collision too.
	v, err := db.queries.FindGroupCreation(ctx, lookup)
	if err == nil {
		return replayGroup(v, name)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return group.Group{}, false, dependency("find group creation", err)
	}
	return group.Group{}, false, dependency("generate unique group code", errors.New("collision attempts exhausted"))
}
func (db *DB) createGroupAttempt(ctx context.Context, lookup generated.FindGroupCreationParams, name, code string) (generated.Group, bool, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return generated.Group{}, false, err
	}
	defer rollbackGroup(tx)
	q := db.queries.WithTx(tx)
	v, err := q.CreateGroup(ctx, generated.CreateGroupParams{Name: name, JoinCode: code, InitiatorID: lookup.InitiatorID, CreationKey: lookup.CreationKey})
	if errors.Is(err, pgx.ErrNoRows) {
		v, err = q.FindGroupCreation(ctx, lookup)
		return v, false, err // read-only replay; deferred rollback releases transaction
	}
	if err != nil {
		return generated.Group{}, false, err
	}
	// INSERT owns the new group row before writing any member.
	if err = q.AddGroupMember(ctx, generated.AddGroupMemberParams{GroupID: v.ID, MemberID: lookup.InitiatorID}); err != nil {
		return generated.Group{}, false, err
	}
	v, err = q.ReadGroup(ctx, v.ID)
	if err != nil {
		return generated.Group{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return generated.Group{}, false, err
	}
	return v, true, nil
}
func (db *DB) JoinGroup(ctx context.Context, actor users.ID, code string) (group.Group, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return group.Group{}, dependency("begin group join", err)
	}
	defer rollbackGroup(tx)
	q := db.queries.WithTx(tx)
	v, err := q.LockGroupByCode(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return group.Group{}, errs.NotFound
	}
	if err != nil {
		return group.Group{}, dependency("lock group", err)
	}
	if err = q.AddGroupMember(ctx, generated.AddGroupMemberParams{GroupID: v.ID, MemberID: int64(actor)}); err != nil {
		return group.Group{}, dependency("join group", err)
	}
	v, err = q.ReadGroup(ctx, v.ID)
	if err != nil {
		return group.Group{}, dependency("read joined group", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return group.Group{}, dependency("commit group join", err)
	}
	return groupResult(v), nil
}
func (db *DB) groupAccess(ctx context.Context, actor users.ID, id group.ID) error {
	allowed, err := db.queries.GroupAccess(ctx, generated.GroupAccessParams{ID: int64(id), Actor: int64(actor)})
	if errors.Is(err, pgx.ErrNoRows) {
		return errs.NotFound
	}
	if err != nil {
		return dependency("check group access", err)
	}
	if !allowed {
		return group.ErrForbidden
	}
	return nil
}
func (db *DB) GetGroup(ctx context.Context, actor users.ID, id group.ID) (group.Group, error) {
	if err := db.groupAccess(ctx, actor, id); err != nil {
		return group.Group{}, err
	}
	v, err := db.queries.ReadAuthorizedGroup(ctx, generated.ReadAuthorizedGroupParams{ID: int64(id), Actor: int64(actor)})
	if errors.Is(err, pgx.ErrNoRows) {
		return group.Group{}, group.ErrForbidden
	}
	if err != nil {
		return group.Group{}, dependency("read group", err)
	}
	return groupResult(v), nil
}
func (db *DB) GetGroupMembers(ctx context.Context, actor users.ID, id group.ID, after users.ID, limit int) (group.Page, error) {
	if err := db.groupAccess(ctx, actor, id); err != nil {
		return group.Page{}, err
	}
	rows, err := db.queries.ListGroupMembers(ctx, generated.ListGroupMembersParams{GroupID: int64(id), Actor: int64(actor), AfterMemberID: int64(after), PageLimit: int32(limit + 1)})
	if err != nil {
		return group.Page{}, dependency("read group members", err)
	}
	page := group.Page{Members: make([]group.Member, 0, min(limit, len(rows)))}
	if len(rows) > limit {
		next := users.ID(rows[limit-1].MemberID)
		page.NextAfterMemberID = &next
		rows = rows[:limit]
	}
	for _, v := range rows {
		m := group.Member{MemberID: users.ID(v.MemberID), Role: group.Role(users.ID(v.MemberID), users.ID(v.InitiatorID)), JoinedAt: v.JoinedAt.Time, DisplayName: v.DisplayName}
		if v.Nickname.Valid {
			name := v.Nickname.String
			m.Nickname = &name
		}
		page.Members = append(page.Members, m)
	}
	return page, nil
}

var _ group.Repository = (*DB)(nil)
