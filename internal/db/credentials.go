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

// Register uses INSERT RETURNING to allocate the account ID, then uses that ID
// for every dependent row in the same transaction. No identity escapes on failure.
func (db *DB) Register(ctx context.Context, command credentials.Registration, pending session.Pending) (users.Account, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return users.Account{}, dependency("begin registration", err)
	}
	defer tx.Rollback(ctx)
	q := db.queries.WithTx(tx)
	account, err := q.CreateUser(ctx)
	if err != nil {
		return users.Account{}, dependency("create account", err)
	}
	id := account.ID
	if err := q.CreatePasswordCredential(ctx, generated.CreatePasswordCredentialParams{UserID: id, Email: command.Email, PasswordHash: command.PasswordHash}); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "password_credentials_email_key" {
			return users.Account{}, credentials.ErrEmailConflict
		}
		return users.Account{}, dependency("create credential", err)
	}
	if err := q.CreateProfile(ctx, generated.CreateProfileParams{UserID: id, DisplayName: command.DisplayName}); err != nil {
		return users.Account{}, dependency("create profile", err)
	}
	if err := q.CreatePreference(ctx, id); err != nil {
		return users.Account{}, dependency("create preference", err)
	}
	if err := q.CreateSession(ctx, generated.CreateSessionParams{UserID: id, TokenHash: pending.TokenHash[:], ExpiresAt: timestamp(pending.ExpiresAt)}); err != nil {
		return users.Account{}, dependency("create registration session", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return users.Account{}, dependency("commit registration", err)
	}
	return users.Account{ID: users.ID(account.ID), CreatedAt: account.CreatedAt.Time}, nil
}

// FindPasswordCredentialByEmail reads the private projection used for password verification.
func (db *DB) FindPasswordCredentialByEmail(ctx context.Context, email string) (credentials.PasswordCredential, error) {
	v, err := db.queries.FindPasswordCredentialByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return credentials.PasswordCredential{}, credentials.ErrNotFound
	}
	if err != nil {
		return credentials.PasswordCredential{}, dependency("find credential", err)
	}
	return credentials.PasswordCredential{Account: users.Account{ID: users.ID(v.ID), CreatedAt: v.CreatedAt.Time}, Email: v.Email, PasswordHash: v.PasswordHash}, nil
}

// UserDetails is the read projection used by the existing public identity response.
// It does not participate in session authentication and never loads password hashes.
func (db *DB) UserDetails(ctx context.Context, userID users.ID) (string, string, error) {
	v, err := db.queries.UserDetails(ctx, int64(userID))
	if err != nil {
		return "", "", dependency("read user details", err)
	}
	return v.Email, v.DisplayName, nil
}
func dependency(operation string, err error) error {
	return fmt.Errorf("%w: %s: %w", errs.DependencyUnavailable, operation, err)
}

var _ credentials.Repository = (*DB)(nil)
