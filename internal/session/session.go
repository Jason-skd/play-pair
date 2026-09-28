// Package session owns token issuance, expiry, authentication and revocation.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/Jason-skd/play-pair/internal/users"
)

var (
	// ErrNotFound is the repository result when no unexpired session matches.
	ErrNotFound = errors.New("session not found")
	// ErrUnauthenticated covers malformed, missing, expired and revoked tokens.
	ErrUnauthenticated = errors.New("unauthenticated")
)

// Duration is the lifetime of a newly issued session.
const Duration = 7 * 24 * time.Hour

// Pending contains the digest and lifetime of a token before it is bound to an
// account. It can be prepared before registration allocates a database ID.
type Pending struct {
	TokenHash [32]byte
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Record binds session data to a persisted account; it never contains the secret.
type Record struct {
	UserID users.ID
	Pending
}

// Issued pairs a client secret with the data to persist. Do not expose Token
// until the session write (or enclosing registration transaction) commits.
type Issued struct {
	Token   string
	Pending Pending
}

// Repository stores sessions and resolves unexpired sessions to account identity.
type Repository interface {
	CreateSession(context.Context, Record) error
	FindSessionUser(context.Context, [32]byte, time.Time) (users.Account, error)
	DeleteSession(context.Context, [32]byte) error
}

// Service manages session secrets, authentication and revocation.
type Service struct {
	repo Repository
	now  func() time.Time
}

// New creates a session service; a nil clock uses time.Now.
func New(repo Repository, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, now: now}
}

// Prepare creates a token without writing it or requiring an account ID.
// The caller must bind Pending to an account in its registration transaction.
func (s *Service) Prepare() (Issued, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Issued{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	now := s.now().UTC()
	return Issued{Token: token, Pending: Pending{TokenHash: sha256.Sum256(raw[:]), CreatedAt: now, ExpiresAt: now.Add(Duration)}}, nil
}

// Issue persists a session for an existing account before returning its secret.
func (s *Service) Issue(ctx context.Context, id users.ID) (Issued, error) {
	issued, err := s.Prepare()
	if err != nil {
		return Issued{}, err
	}
	if err := s.repo.CreateSession(ctx, Record{UserID: id, Pending: issued.Pending}); err != nil {
		return Issued{}, err
	}
	return issued, nil
}

// Authenticate resolves a valid, unexpired token without loading login credentials.
func (s *Service) Authenticate(ctx context.Context, token string) (users.Account, error) {
	digest, ok := tokenDigest(token)
	if !ok {
		return users.Account{}, ErrUnauthenticated
	}
	u, err := s.repo.FindSessionUser(ctx, digest, s.now().UTC())
	if errors.Is(err, ErrNotFound) {
		return users.Account{}, ErrUnauthenticated
	}
	return u, err
}

// Logout is idempotent, including absent and malformed tokens.
func (s *Service) Logout(ctx context.Context, token string) error {
	digest, ok := tokenDigest(token)
	if !ok {
		return nil
	}
	return s.repo.DeleteSession(ctx, digest)
}

func tokenDigest(token string) ([32]byte, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return [32]byte{}, false
	}
	return sha256.Sum256(raw), true
}
