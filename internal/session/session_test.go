package session

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/users"
)

type repository struct {
	record Record
	err    error
	calls  int
}

func (r *repository) CreateSession(_ context.Context, v Record) error { r.record = v; return r.err }
func (r *repository) FindSessionUser(_ context.Context, h [32]byte, now time.Time) (users.Account, error) {
	r.calls++
	if r.err != nil {
		return users.Account{}, r.err
	}
	if h != r.record.TokenHash || !now.Before(r.record.ExpiresAt) {
		return users.Account{}, ErrNotFound
	}
	return users.Account{ID: r.record.UserID}, nil
}
func (r *repository) DeleteSession(_ context.Context, h [32]byte) error {
	if h == r.record.TokenHash {
		r.record = Record{}
	}
	return r.err
}

func TestSessionLifecycleAndFailure(t *testing.T) {
	now := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	r := &repository{}
	s := New(r, func() time.Time { return now })
	issued, err := s.Issue(context.Background(), "account")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(issued.Token)
	if err != nil || len(raw) != 32 || sha256.Sum256(raw) != r.record.TokenHash || r.record.ExpiresAt != now.Add(Duration) {
		t.Fatal("invalid token or expiry")
	}
	if _, err := s.Authenticate(context.Background(), issued.Token); err != nil {
		t.Fatal(err)
	}
	now = now.Add(Duration)
	if _, err := s.Authenticate(context.Background(), issued.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal(err)
	}
	before := r.calls
	if _, err := s.Authenticate(context.Background(), "malformed"); !errors.Is(err, ErrUnauthenticated) || r.calls != before {
		t.Fatal("malformed token queried repository")
	}
	r.err = errors.New("write failed")
	if got, err := s.Issue(context.Background(), "account"); err == nil || got.Token != "" {
		t.Fatal("failed write exposed session")
	}
}
