// Package profile owns personalized display information.
package profile

import (
	"context"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/Jason-skd/play-pair/internal/users"
)

// ErrInvalidInput identifies invalid names or an unusable expected version.
var ErrInvalidInput = errors.New("invalid profile")

// Profile is a user's display information, versioned independently of preferences.
// A nil Nickname falls back to DisplayName when choosing a display label.
type Profile struct {
	UserID      users.ID
	DisplayName string
	Nickname    *string
	Version     int64
}

// Update fully replaces display information at ExpectedVersion.
// A nil or blank Nickname clears the previously saved nickname.
type Update struct {
	DisplayName     string
	Nickname        *string
	ExpectedVersion int64
}

// Repository persists profiles. UpdateProfile must compare and increment the
// version atomically, returning errs.VersionConflict when it does not match.
type Repository interface {
	GetProfile(context.Context, users.ID) (Profile, error)
	UpdateProfile(context.Context, users.ID, Update) (Profile, error)
}

// Service validates and normalizes display information before persistence.
type Service struct{ repo Repository }

// New creates a profile service backed by repo.
func New(repo Repository) *Service { return &Service{repo: repo} }

// Get returns an existing profile without changing its version.
func (s *Service) Get(ctx context.Context, id users.ID) (Profile, error) {
	return s.repo.GetProfile(ctx, id)
}

// Replace normalizes names and replaces the profile at the expected version.
// It never changes the user's preferences or their version.
func (s *Service) Replace(ctx context.Context, id users.ID, command Update) (Profile, error) {
	name, err := NormalizeName(command.DisplayName)
	if err != nil || command.ExpectedVersion < 1 || command.ExpectedVersion == math.MaxInt64 {
		return Profile{}, ErrInvalidInput
	}
	command.DisplayName = name
	if command.Nickname != nil {
		nickname := strings.TrimSpace(*command.Nickname)
		if !utf8.ValidString(nickname) || strings.ContainsRune(nickname, 0) || utf8.RuneCountInString(nickname) > 80 {
			return Profile{}, ErrInvalidInput
		}
		command.Nickname = nil
		if nickname != "" {
			command.Nickname = &nickname
		}
	}
	return s.repo.UpdateProfile(ctx, id, command)
}

// Name is the display label used when the application permits identifying a member.
func (p Profile) Name() string {
	if p.Nickname != nil {
		return *p.Nickname
	}
	return p.DisplayName
}

// NormalizeName validates a display name at registration and profile update.
func NormalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || strings.ContainsRune(name, 0) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 80 {
		return "", ErrInvalidInput
	}
	return name, nil
}
