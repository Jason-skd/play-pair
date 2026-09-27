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

var ErrInvalidInput = errors.New("invalid preference")

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

type Preference struct {
	UserID       users.ID
	Soft         Soft
	HardDefaults HardDefaults
	Version      int64
}

type Update struct {
	Soft            Soft
	HardDefaults    HardDefaults
	ExpectedVersion int64
}

type Repository interface {
	GetPreference(context.Context, users.ID) (Preference, error)
	UpdatePreference(context.Context, users.ID, Update) (Preference, error)
}

type Service struct{ repo Repository }

func New(repo Repository) *Service { return &Service{repo: repo} }
func (s *Service) Get(ctx context.Context, id users.ID) (Preference, error) {
	return s.repo.GetPreference(ctx, id)
}

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
