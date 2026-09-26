package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Jason-skd/play-pair/internal/auth"
	"github.com/Jason-skd/play-pair/internal/db/generated"
	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func (db *DB) Register(ctx context.Context, user auth.StoredUser, tokenHash [32]byte, expiresAt time.Time) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%w: begin registration", errs.DependencyUnavailable)
	}
	defer tx.Rollback(ctx)
	q := db.queries.WithTx(tx)
	id, err := parseUUID(user.ID)
	if err != nil {
		return err
	}
	if err := q.CreateUser(ctx, generated.CreateUserParams{ID: id, Email: user.Email, PasswordHash: user.PasswordHash, DisplayName: user.DisplayName, CreatedAt: timestamp(user.CreatedAt)}); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return auth.ErrEmailConflict
		}
		return fmt.Errorf("%w: create user", errs.DependencyUnavailable)
	}
	if err := q.CreateSession(ctx, generated.CreateSessionParams{TokenHash: tokenHash[:], UserID: id, CreatedAt: timestamp(user.CreatedAt), ExpiresAt: timestamp(expiresAt)}); err != nil {
		return fmt.Errorf("%w: create registration session", errs.DependencyUnavailable)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: commit registration", errs.DependencyUnavailable)
	}
	return nil
}

func (db *DB) FindByEmail(ctx context.Context, email string) (auth.StoredUser, error) {
	u, err := db.queries.FindUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.StoredUser{}, auth.ErrNotFound
	}
	if err != nil {
		return auth.StoredUser{}, fmt.Errorf("%w: find user", errs.DependencyUnavailable)
	}
	return storedUser(u.ID, u.Email, u.PasswordHash, u.DisplayName, u.CreatedAt.Time), nil
}

func (db *DB) CreateSession(ctx context.Context, userID string, tokenHash [32]byte, createdAt, expiresAt time.Time) error {
	id, err := parseUUID(userID)
	if err != nil {
		return err
	}
	if err := db.queries.CreateSession(ctx, generated.CreateSessionParams{TokenHash: tokenHash[:], UserID: id, CreatedAt: timestamp(createdAt), ExpiresAt: timestamp(expiresAt)}); err != nil {
		return fmt.Errorf("%w: create session", errs.DependencyUnavailable)
	}
	return nil
}

func (db *DB) FindSessionUser(ctx context.Context, tokenHash [32]byte, now time.Time) (auth.StoredUser, error) {
	u, err := db.queries.FindSessionUser(ctx, generated.FindSessionUserParams{TokenHash: tokenHash[:], ExpiresAt: timestamp(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.StoredUser{}, auth.ErrNotFound
	}
	if err != nil {
		return auth.StoredUser{}, fmt.Errorf("%w: find session", errs.DependencyUnavailable)
	}
	return storedUser(u.ID, u.Email, u.PasswordHash, u.DisplayName, u.CreatedAt.Time), nil
}

func (db *DB) DeleteSession(ctx context.Context, tokenHash [32]byte) error {
	_, err := db.queries.DeleteSession(ctx, tokenHash[:])
	if err != nil {
		return fmt.Errorf("%w: delete session", errs.DependencyUnavailable)
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

func storedUser(id pgtype.UUID, email, hash, name string, created time.Time) auth.StoredUser {
	return auth.StoredUser{User: auth.User{ID: uuidString(id), Email: email, DisplayName: name, CreatedAt: created}, PasswordHash: hash}
}

func timestamp(v time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: v, Valid: true} }

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", id.Bytes[0:4], id.Bytes[4:6], id.Bytes[6:8], id.Bytes[8:10], id.Bytes[10:16])
}

var _ auth.Repository = (*DB)(nil)
