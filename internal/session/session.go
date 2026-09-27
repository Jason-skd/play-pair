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
	ErrNotFound        = errors.New("session not found")
	ErrUnauthenticated = errors.New("unauthenticated")
)

const Duration = 7 * 24 * time.Hour

// Record contains only the token digest, never the client secret.
type Record struct {
	UserID    users.ID
	TokenHash [32]byte
	CreatedAt time.Time
	ExpiresAt time.Time
}

type Issued struct {
	Token  string
	Record Record
}

type Repository interface {
	CreateSession(context.Context, Record) error
	FindSessionUser(context.Context, [32]byte, time.Time) (users.Account, error)
	DeleteSession(context.Context, [32]byte) error
}

type Service struct {
	repo Repository
	now  func() time.Time
}

func New(repo Repository, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, now: now}
}

// Prepare produces a session for an enclosing registration transaction.
// It becomes usable only after the caller commits Record.
func (s *Service) Prepare(id users.ID) (Issued, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Issued{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	now := s.now().UTC()
	return Issued{Token: token, Record: Record{UserID: id, TokenHash: sha256.Sum256(raw[:]), CreatedAt: now, ExpiresAt: now.Add(Duration)}}, nil
}

func (s *Service) Issue(ctx context.Context, id users.ID) (Issued, error) {
	issued, err := s.Prepare(id)
	if err != nil {
		return Issued{}, err
	}
	if err := s.repo.CreateSession(ctx, issued.Record); err != nil {
		return Issued{}, err
	}
	return issued, nil
}

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
