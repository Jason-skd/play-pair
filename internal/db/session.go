package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Jason-skd/play-pair/internal/db/generated"
	"github.com/Jason-skd/play-pair/internal/session"
	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (db *DB) CreateSession(ctx context.Context, r session.Record) error {
	id, err := parseUUID(string(r.UserID))
	if err != nil {
		return err
	}
	if err := db.queries.CreateSession(ctx, generated.CreateSessionParams{UserID: id, TokenHash: r.TokenHash[:], CreatedAt: timestamp(r.CreatedAt), ExpiresAt: timestamp(r.ExpiresAt)}); err != nil {
		return dependency("create session", err)
	}
	return nil
}
func (db *DB) FindSessionUser(ctx context.Context, h [32]byte, now time.Time) (users.Account, error) {
	v, err := db.queries.FindSessionUser(ctx, generated.FindSessionUserParams{TokenHash: h[:], ExpiresAt: timestamp(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return users.Account{}, session.ErrNotFound
	}
	if err != nil {
		return users.Account{}, dependency("find session", err)
	}
	return users.Account{ID: users.ID(uuidString(v.ID)), CreatedAt: v.CreatedAt.Time}, nil
}
func (db *DB) DeleteSession(ctx context.Context, h [32]byte) error {
	if _, err := db.queries.DeleteSession(ctx, h[:]); err != nil {
		return dependency("delete session", err)
	}
	return nil
}
func parseUUID(v string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(v); err != nil {
		return id, fmt.Errorf("parse user id: %w", err)
	}
	return id, nil
}

func timestamp(v time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: v, Valid: true} }

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", id.Bytes[0:4], id.Bytes[4:6], id.Bytes[6:8], id.Bytes[8:10], id.Bytes[10:16])
}

var _ session.Repository = (*DB)(nil)
