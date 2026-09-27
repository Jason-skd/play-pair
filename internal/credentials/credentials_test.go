package credentials

import (
	"context"
	"errors"
	"github.com/Jason-skd/play-pair/internal/session"
	"github.com/Jason-skd/play-pair/internal/users"
	"testing"
	"time"
)

type memoryRepo struct {
	users    map[string]Registration
	sessions map[[32]byte]users.Account
	expires  map[[32]byte]time.Time
}

func newMemory() *memoryRepo {
	return &memoryRepo{map[string]Registration{}, map[[32]byte]users.Account{}, map[[32]byte]time.Time{}}
}
func (m *memoryRepo) Register(_ context.Context, u Registration, record session.Record) error {
	h, exp := record.TokenHash, record.ExpiresAt
	if _, ok := m.users[u.Email]; ok {
		return ErrEmailConflict
	}
	m.users[u.Email] = u
	m.sessions[h] = u.Account
	m.expires[h] = exp
	return nil
}
func (m *memoryRepo) FindPasswordCredentialByEmail(_ context.Context, e string) (PasswordCredential, error) {
	u, ok := m.users[e]
	if !ok {
		return PasswordCredential{}, ErrNotFound
	}
	return u.PasswordCredential, nil
}
func (m *memoryRepo) CreateSession(_ context.Context, record session.Record) error {
	id, h, exp := record.UserID, record.TokenHash, record.ExpiresAt
	for _, u := range m.users {
		if u.ID == id {
			m.sessions[h] = u.Account
			m.expires[h] = exp
			return nil
		}
	}
	return errors.New("missing")
}
func (m *memoryRepo) FindSessionUser(_ context.Context, h [32]byte, now time.Time) (users.Account, error) {
	u, ok := m.sessions[h]
	if !ok || !m.expires[h].After(now) {
		return users.Account{}, session.ErrNotFound
	}
	return u, nil
}
func (m *memoryRepo) DeleteSession(_ context.Context, h [32]byte) error {
	delete(m.sessions, h)
	delete(m.expires, h)
	return nil
}

func TestRegisterLoginAuthenticateLogout(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	repo := newMemory()
	sessions := session.New(repo, func() time.Time { return now })
	s := New(repo, sessions, func() time.Time { return now })
	u, token, exp, err := s.Register(context.Background(), " User@Example.COM ", "correct horse battery", " Fu Shi ")
	if err != nil {
		t.Fatal(err)
	}
	if repo.users["user@example.com"].Email != "user@example.com" || repo.users["user@example.com"].DisplayName != "Fu Shi" || exp != now.Add(session.Duration) {
		t.Fatalf("unexpected registration: %#v %v", u, exp)
	}
	if got, err := sessions.Authenticate(context.Background(), token); err != nil || got.ID != u.ID {
		t.Fatalf("authenticate: %#v %v", got, err)
	}
	_, second, _, err := s.Login(context.Background(), "USER@example.com", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.Logout(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Authenticate(context.Background(), token); !errors.Is(err, session.ErrUnauthenticated) {
		t.Fatalf("old session: %v", err)
	}
	if _, err := sessions.Authenticate(context.Background(), second); err != nil {
		t.Fatalf("second session: %v", err)
	}
}

func TestValidationAndCredentials(t *testing.T) {
	repo := newMemory()
	s := New(repo, session.New(repo, time.Now), time.Now)
	if _, _, _, err := s.Register(context.Background(), "bad", "short", " "); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("validation: %v", err)
	}
	if _, _, _, err := s.Login(context.Background(), "missing@example.com", "correct horse battery"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("credentials: %v", err)
	}
}
