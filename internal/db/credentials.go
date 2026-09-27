package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/Jason-skd/play-pair/internal/credentials"
	"github.com/Jason-skd/play-pair/internal/db/generated"
	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/Jason-skd/play-pair/internal/session"
	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Register atomically persists the account, credential, profile, empty preference and initial session.
func (db *DB) Register(ctx context.Context, command credentials.Registration, record session.Record) error {
	if command.ID != record.UserID {
		return errors.New("registration session identity mismatch")
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return dependency("begin registration", err)
	}
	defer tx.Rollback(ctx)
	q := db.queries.WithTx(tx)
	id, err := parseUUID(string(command.ID))
	if err != nil {
		return err
	}
	if err := q.CreateUser(ctx, generated.CreateUserParams{ID: id, CreatedAt: timestamp(command.CreatedAt)}); err != nil {
		return dependency("create account", err)
	}
	if err := q.CreatePasswordCredential(ctx, generated.CreatePasswordCredentialParams{UserID: id, Email: command.Email, PasswordHash: command.PasswordHash, UpdatedAt: timestamp(command.CreatedAt)}); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "password_credentials_email_key" {
			return credentials.ErrEmailConflict
		}
		return dependency("create credential", err)
	}
	if err := q.CreateProfile(ctx, generated.CreateProfileParams{UserID: id, DisplayName: command.DisplayName}); err != nil {
		return dependency("create profile", err)
	}
	if err := q.CreatePreference(ctx, id); err != nil {
		return dependency("create preference", err)
	}
	if err := q.CreateSession(ctx, generated.CreateSessionParams{UserID: id, TokenHash: record.TokenHash[:], CreatedAt: timestamp(record.CreatedAt), ExpiresAt: timestamp(record.ExpiresAt)}); err != nil {
		return dependency("create registration session", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return dependency("commit registration", err)
	}
	return nil
}
func (db *DB) FindPasswordCredentialByEmail(ctx context.Context, email string) (credentials.PasswordCredential, error) {
	v, err := db.queries.FindPasswordCredentialByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return credentials.PasswordCredential{}, credentials.ErrNotFound
	}
	if err != nil {
		return credentials.PasswordCredential{}, dependency("find credential", err)
	}
	return credentials.PasswordCredential{Account: users.Account{ID: users.ID(uuidString(v.ID)), CreatedAt: v.CreatedAt.Time}, Email: v.Email, PasswordHash: v.PasswordHash}, nil
}

// UserDetails is the read projection used by the existing public identity response.
// It does not participate in session authentication and never loads password hashes.
func (db *DB) UserDetails(ctx context.Context, userID users.ID) (string, string, error) {
	id, err := parseUUID(string(userID))
	if err != nil {
		return "", "", err
	}
	v, err := db.queries.UserDetails(ctx, id)
	if err != nil {
		return "", "", dependency("read user details", err)
	}
	return v.Email, v.DisplayName, nil
}
func dependency(operation string, err error) error {
	return fmt.Errorf("%w: %s: %w", errs.DependencyUnavailable, operation, err)
}

var _ credentials.Repository = (*DB)(nil)
