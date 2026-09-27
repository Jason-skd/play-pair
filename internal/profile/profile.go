// Package profile owns personalized display information.
package profile

import (
	"errors"
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

// NormalizeName validates a display name at registration and profile update.
func NormalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || strings.ContainsRune(name, 0) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 80 {
		return "", ErrInvalidInput
	}
	return name, nil
}
