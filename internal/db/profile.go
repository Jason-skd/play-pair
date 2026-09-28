package db

import (
	"context"
	"errors"

	"github.com/Jason-skd/play-pair/internal/db/generated"
	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/Jason-skd/play-pair/internal/profile"
	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// GetProfile reads display data without creating a missing profile implicitly.
func (db *DB) GetProfile(ctx context.Context, userID users.ID) (profile.Profile, error) {
	v, err := db.queries.GetProfile(ctx, int64(userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return profile.Profile{}, errs.NotFound
	}
	if err != nil {
		return profile.Profile{}, dependency("read profile", err)
	}
	return profileResult(v), nil
}

// UpdateProfile compares and increments the version in one SQL statement.
func (db *DB) UpdateProfile(ctx context.Context, userID users.ID, command profile.Update) (profile.Profile, error) {
	nickname := pgtype.Text{}
	if command.Nickname != nil {
		nickname = pgtype.Text{String: *command.Nickname, Valid: true}
	}
	v, err := db.queries.UpdateProfile(ctx, generated.UpdateProfileParams{UserID: int64(userID), DisplayName: command.DisplayName, Nickname: nickname, Version: command.ExpectedVersion})
	if errors.Is(err, pgx.ErrNoRows) {
		return profile.Profile{}, errs.VersionConflict
	}
	if err != nil {
		return profile.Profile{}, dependency("update profile", err)
	}
	return profileResult(v), nil
}

func profileResult(v generated.Profile) profile.Profile {
	p := profile.Profile{UserID: users.ID(v.UserID), DisplayName: v.DisplayName, Version: v.Version}
	if v.Nickname.Valid {
		name := v.Nickname.String
		p.Nickname = &name
	}
	return p
}

var _ profile.Repository = (*DB)(nil)
