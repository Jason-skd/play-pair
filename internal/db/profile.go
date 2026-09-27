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

func (db *DB) GetProfile(ctx context.Context, userID users.ID) (profile.Profile, error) {
	id, err := parseUUID(string(userID))
	if err != nil {
		return profile.Profile{}, err
	}
	v, err := db.queries.GetProfile(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return profile.Profile{}, errs.NotFound
	}
	if err != nil {
		return profile.Profile{}, dependency("read profile", err)
	}
	return profileResult(v), nil
}

func (db *DB) UpdateProfile(ctx context.Context, userID users.ID, command profile.Update) (profile.Profile, error) {
	id, err := parseUUID(string(userID))
	if err != nil {
		return profile.Profile{}, err
	}
	nickname := pgtype.Text{}
	if command.Nickname != nil {
		nickname = pgtype.Text{String: *command.Nickname, Valid: true}
	}
	v, err := db.queries.UpdateProfile(ctx, generated.UpdateProfileParams{UserID: id, DisplayName: command.DisplayName, Nickname: nickname, Version: command.ExpectedVersion})
	if errors.Is(err, pgx.ErrNoRows) {
		return profile.Profile{}, errs.VersionConflict
	}
	if err != nil {
		return profile.Profile{}, dependency("update profile", err)
	}
	return profileResult(v), nil
}

func profileResult(v generated.Profile) profile.Profile {
	p := profile.Profile{UserID: users.ID(uuidString(v.UserID)), DisplayName: v.DisplayName, Version: v.Version}
	if v.Nickname.Valid {
		name := v.Nickname.String
		p.Nickname = &name
	}
	return p
}

var _ profile.Repository = (*DB)(nil)
