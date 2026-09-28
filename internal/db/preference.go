package db

import (
	"context"
	"errors"

	"github.com/Jason-skd/play-pair/internal/db/generated"
	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/Jason-skd/play-pair/internal/preference"
	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// GetPreference reads saved defaults without authorizing an outing.
func (db *DB) GetPreference(ctx context.Context, userID users.ID) (preference.Preference, error) {
	v, err := db.queries.GetPreference(ctx, int64(userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return preference.Preference{}, errs.NotFound
	}
	if err != nil {
		return preference.Preference{}, dependency("read preference", err)
	}
	return preferenceResult(v), nil
}

// UpdatePreference atomically replaces defaults only at the expected version.
// Explicit pgtype validity preserves the difference between NULL, zero and false.
func (db *DB) UpdatePreference(ctx context.Context, userID users.ID, c preference.Update) (preference.Preference, error) {
	budget := pgtype.Int8{}
	access := pgtype.Bool{}
	if c.HardDefaults.MaxBudgetFen != nil {
		budget = pgtype.Int8{Int64: *c.HardDefaults.MaxBudgetFen, Valid: true}
	}
	if c.HardDefaults.RequiresWheelchairAccess != nil {
		access = pgtype.Bool{Bool: *c.HardDefaults.RequiresWheelchairAccess, Valid: true}
	}
	v, err := db.queries.UpdatePreference(ctx, generated.UpdatePreferenceParams{UserID: int64(userID), Likes: c.Soft.Likes, Neutral: c.Soft.Neutral, Dislikes: c.Soft.Dislikes, MaxBudgetFen: budget, RequiresWheelchairAccess: access, Version: c.ExpectedVersion})
	if errors.Is(err, pgx.ErrNoRows) {
		return preference.Preference{}, errs.VersionConflict
	}
	if err != nil {
		return preference.Preference{}, dependency("update preference", err)
	}
	return preferenceResult(v), nil
}
func preferenceResult(v generated.Preference) preference.Preference {
	p := preference.Preference{UserID: users.ID(v.UserID), Soft: preference.Soft{Likes: v.Likes, Neutral: v.Neutral, Dislikes: v.Dislikes}, Version: v.Version}
	if v.MaxBudgetFen.Valid {
		budget := v.MaxBudgetFen.Int64
		p.HardDefaults.MaxBudgetFen = &budget
	}
	if v.RequiresWheelchairAccess.Valid {
		access := v.RequiresWheelchairAccess.Bool
		p.HardDefaults.RequiresWheelchairAccess = &access
	}
	return p
}

var _ preference.Repository = (*DB)(nil)
