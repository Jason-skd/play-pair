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

// Register inserts the identity, password credential, and first session in one transaction.
// A later insert failure rolls back all earlier writes. The caller supplies a
// token digest and expiry; a duplicate email returns auth.ErrEmailConflict.
func (db *DB) Register(ctx context.Context, credential auth.PasswordCredential, tokenHash [32]byte, expiresAt time.Time) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%w: begin registration", errs.DependencyUnavailable)
	}
	defer tx.Rollback(ctx)
	q := db.queries.WithTx(tx)
	id, err := parseUUID(credential.ID)
	if err != nil {
		return err
	}
	if err := q.CreateUser(ctx, generated.CreateUserParams{ID: id, Email: credential.Email, DisplayName: credential.DisplayName, CreatedAt: timestamp(credential.CreatedAt)}); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return auth.ErrEmailConflict
		}
		return fmt.Errorf("%w: create user", errs.DependencyUnavailable)
	}
	if err := q.CreatePasswordCredential(ctx, generated.CreatePasswordCredentialParams{UserID: id, PasswordHash: credential.PasswordHash, UpdatedAt: timestamp(credential.CreatedAt)}); err != nil {
		return fmt.Errorf("%w: create password credential", errs.DependencyUnavailable)
	}
	if err := q.CreateSession(ctx, generated.CreateSessionParams{TokenHash: tokenHash[:], UserID: id, CreatedAt: timestamp(credential.CreatedAt), ExpiresAt: timestamp(expiresAt)}); err != nil {
		return fmt.Errorf("%w: create registration session", errs.DependencyUnavailable)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: commit registration", errs.DependencyUnavailable)
	}
	return nil
}

// FindPasswordCredentialByEmail loads password-login data for a normalized email.
// A user without a password credential is reported as auth.ErrNotFound.
func (db *DB) FindPasswordCredentialByEmail(ctx context.Context, email string) (auth.PasswordCredential, error) {
	u, err := db.queries.FindPasswordCredentialByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.PasswordCredential{}, auth.ErrNotFound
	}
	if err != nil {
		return auth.PasswordCredential{}, fmt.Errorf("%w: find password credential", errs.DependencyUnavailable)
	}
	return passwordCredential(u.ID, u.Email, u.PasswordHash, u.DisplayName, u.CreatedAt.Time), nil
}

// CreateSession persists a token digest for an existing identity. The caller
// supplies creation and fixed expiry times; database failure wraps errs.DependencyUnavailable.
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

// FindSessionUser resolves an unexpired session without reading password
// credentials. Missing or expired sessions return auth.ErrNotFound.
func (db *DB) FindSessionUser(ctx context.Context, tokenHash [32]byte, now time.Time) (auth.User, error) {
	u, err := db.queries.FindSessionUser(ctx, generated.FindSessionUserParams{TokenHash: tokenHash[:], ExpiresAt: timestamp(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.User{}, auth.ErrNotFound
	}
	if err != nil {
		return auth.User{}, fmt.Errorf("%w: find session", errs.DependencyUnavailable)
	}
	return user(u.ID, u.Email, u.DisplayName, u.CreatedAt.Time), nil
}

// DeleteSession revokes a session; an absent digest is already revoked.
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

func user(id pgtype.UUID, email, name string, created time.Time) auth.User {
	return auth.User{ID: uuidString(id), Email: email, DisplayName: name, CreatedAt: created}
}

func passwordCredential(id pgtype.UUID, email, hash, name string, created time.Time) auth.PasswordCredential {
	return auth.PasswordCredential{User: user(id, email, name, created), PasswordHash: hash}
}

func timestamp(v time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: v, Valid: true} }

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", id.Bytes[0:4], id.Bytes[4:6], id.Bytes[6:8], id.Bytes[8:10], id.Bytes[10:16])
}

var _ auth.Repository = (*DB)(nil)
