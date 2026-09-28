// Package users owns account identity, independently of login and business data.
package users

import "time"

// ID is a positive account identifier allocated by the database on insertion.
// Zero is not a persisted identity. IDs may have gaps after failed transactions.
type ID int64

// Account contains only the identity of an account.
type Account struct {
	ID        ID
	CreatedAt time.Time
}
