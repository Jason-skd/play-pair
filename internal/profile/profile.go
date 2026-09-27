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

var ErrInvalidInput = errors.New("invalid profile")

type Profile struct {
	UserID      users.ID
	DisplayName string
	Nickname    *string
	Version     int64
}

type Update struct {
	DisplayName     string
	Nickname        *string
	ExpectedVersion int64
}

type Repository interface {
	GetProfile(context.Context, users.ID) (Profile, error)
	UpdateProfile(context.Context, users.ID, Update) (Profile, error)
}

type Service struct{ repo Repository }

func New(repo Repository) *Service { return &Service{repo: repo} }

func (s *Service) Get(ctx context.Context, id users.ID) (Profile, error) {
	return s.repo.GetProfile(ctx, id)
}

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
