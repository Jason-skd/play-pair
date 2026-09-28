// Package preference owns soft preference text and optional hard-constraint defaults.
// Defaults never represent authorization for a particular outing.
package preference

import (
	"context"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/Jason-skd/play-pair/internal/users"
)

// ErrInvalidInput identifies invalid text, budget or expected version values.
var ErrInvalidInput = errors.New("invalid preference")

// Soft holds optional free text for later AI scoring, at most 500 Unicode code
// points per field. Dislikes are not interpreted as mandatory exclusions.
type Soft struct {
	Likes    string
	Neutral  string
	Dislikes string
}

// HardDefaults are optional. Nil means not filled in, zero budget is a real
// zero-cost limit, and false wheelchair access explicitly makes no such request.
type HardDefaults struct {
	MaxBudgetFen             *int64
	RequiresWheelchairAccess *bool
}

// Preference is a user's saved defaults, versioned independently of their profile.
// Its values are not an authorization or a constraint snapshot for any outing.
type Preference struct {
	UserID       users.ID
	Soft         Soft
	HardDefaults HardDefaults
	Version      int64
}

// Update fully replaces saved defaults at ExpectedVersion. Empty text and nil
// hard defaults clear old values; they do not mean "leave the old value unchanged".
type Update struct {
	Soft            Soft
	HardDefaults    HardDefaults
	ExpectedVersion int64
}

// Repository persists preferences. UpdatePreference must compare and increment
// the version atomically, returning errs.VersionConflict on a mismatch.
type Repository interface {
	GetPreference(context.Context, users.ID) (Preference, error)
	UpdatePreference(context.Context, users.ID, Update) (Preference, error)
}

// Service validates soft text and optional hard defaults before persistence.
type Service struct{ repo Repository }

// New creates a preference service backed by repo.
func New(repo Repository) *Service { return &Service{repo: repo} }

// Get returns existing defaults without confirming participation in an outing.
func (s *Service) Get(ctx context.Context, id users.ID) (Preference, error) {
	return s.repo.GetPreference(ctx, id)
}

// Replace validates and fully replaces defaults at the expected version.
// Nil, zero budget and an explicit false requirement retain distinct meanings.
func (s *Service) Replace(ctx context.Context, id users.ID, c Update) (Preference, error) {
	if c.ExpectedVersion < 1 || c.ExpectedVersion == math.MaxInt64 {
		return Preference{}, ErrInvalidInput
	}
	for _, v := range []*string{&c.Soft.Likes, &c.Soft.Neutral, &c.Soft.Dislikes} {
		// Only trim surrounding whitespace; preserve the user's text and meaning.
		*v = strings.TrimSpace(*v)
		if !utf8.ValidString(*v) || strings.ContainsRune(*v, 0) || utf8.RuneCountInString(*v) > 500 {
			return Preference{}, ErrInvalidInput
		}
	}
	if c.HardDefaults.MaxBudgetFen != nil {
		v := *c.HardDefaults.MaxBudgetFen
		if v < 0 {
			return Preference{}, ErrInvalidInput
		}
		c.HardDefaults.MaxBudgetFen = &v
	}
	if c.HardDefaults.RequiresWheelchairAccess != nil {
		v := *c.HardDefaults.RequiresWheelchairAccess
		c.HardDefaults.RequiresWheelchairAccess = &v
	}
	return s.repo.UpdatePreference(ctx, id, c)
}
