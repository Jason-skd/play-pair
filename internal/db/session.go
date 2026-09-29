package db

import (
	"context"
	"errors"
	"time"

	"github.com/Jason-skd/play-pair/internal/db/generated"
	"github.com/Jason-skd/play-pair/internal/session"
	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// CreateSession persists a token digest for an existing account.
func (db *DB) CreateSession(ctx context.Context, r session.Record) error {
	if err := db.queries.CreateSession(ctx, generated.CreateSessionParams{UserID: int64(r.UserID), TokenHash: r.TokenHash[:], ExpiresAt: timestamp(r.ExpiresAt)}); err != nil {
		return dependency("create session", err)
	}
	return nil
}

// FindSessionUser resolves only unexpired sessions, without consulting credentials.
func (db *DB) FindSessionUser(ctx context.Context, h [32]byte, now time.Time) (users.Account, error) {
	v, err := db.queries.FindSessionUser(ctx, generated.FindSessionUserParams{TokenHash: h[:], Now: timestamp(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return users.Account{}, session.ErrNotFound
	}
	if err != nil {
		return users.Account{}, dependency("find session", err)
	}
	return users.Account{ID: users.ID(v.ID), CreatedAt: v.CreatedAt.Time}, nil
}

// DeleteSession is idempotent for a digest that is no longer stored.
func (db *DB) DeleteSession(ctx context.Context, h [32]byte) error {
	if _, err := db.queries.DeleteSession(ctx, h[:]); err != nil {
		return dependency("delete session", err)
	}
	return nil
}
func timestamp(v time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: v, Valid: true} }

var _ session.Repository = (*DB)(nil)
