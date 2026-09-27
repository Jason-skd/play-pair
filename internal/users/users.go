// Package users owns account identity, independently of login and business data.
package users

import (
	"time"

	"github.com/google/uuid"
)

// ID is a stable account identifier shared by business resources.
type ID string

// Account contains only the identity of an account.
type Account struct {
	ID        ID
	CreatedAt time.Time
}

// New creates an identity without persisting it.
func New(now time.Time) (Account, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return Account{}, err
	}
	return Account{ID: ID(id.String()), CreatedAt: now.UTC()}, nil
}
