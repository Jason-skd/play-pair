package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

type memoryRepo struct {
	users    map[string]StoredUser
	sessions map[[32]byte]StoredUser
	expires  map[[32]byte]time.Time
}

func newMemory() *memoryRepo {
	return &memoryRepo{map[string]StoredUser{}, map[[32]byte]StoredUser{}, map[[32]byte]time.Time{}}
}
func (m *memoryRepo) Register(_ context.Context, u StoredUser, h [32]byte, exp time.Time) error {
	if _, ok := m.users[u.Email]; ok {
		return ErrEmailConflict
	}
	m.users[u.Email] = u
	m.sessions[h] = u
	m.expires[h] = exp
	return nil
}
func (m *memoryRepo) FindByEmail(_ context.Context, e string) (StoredUser, error) {
	u, ok := m.users[e]
	if !ok {
		return StoredUser{}, errors.New("missing")
	}
	return u, nil
}
func (m *memoryRepo) CreateSession(_ context.Context, id string, h [32]byte, _ time.Time, exp time.Time) error {
	for _, u := range m.users {
		if u.ID == id {
			m.sessions[h] = u
			m.expires[h] = exp
			return nil
		}
	}
	return errors.New("missing")
}
func (m *memoryRepo) FindSessionUser(_ context.Context, h [32]byte, now time.Time) (StoredUser, error) {
	u, ok := m.sessions[h]
	if !ok || !m.expires[h].After(now) {
		return StoredUser{}, errors.New("missing")
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
	s := New(newMemory(), func() time.Time { return now })
	u, token, exp, err := s.Register(context.Background(), " User@Example.COM ", "correct horse battery", " Fu Shi ")
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "user@example.com" || u.DisplayName != "Fu Shi" || exp != now.Add(SessionDuration) {
		t.Fatalf("unexpected registration: %#v %v", u, exp)
	}
	if got, err := s.Authenticate(context.Background(), token); err != nil || got.ID != u.ID {
		t.Fatalf("authenticate: %#v %v", got, err)
	}
	_, second, _, err := s.Login(context.Background(), "USER@example.com", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Logout(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(context.Background(), token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("old session: %v", err)
	}
	if _, err := s.Authenticate(context.Background(), second); err != nil {
		t.Fatalf("second session: %v", err)
	}
}

func TestValidationAndCredentials(t *testing.T) {
	s := New(newMemory(), time.Now)
	if _, _, _, err := s.Register(context.Background(), "bad", "short", " "); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("validation: %v", err)
	}
	if _, _, _, err := s.Login(context.Background(), "missing@example.com", "correct horse battery"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("credentials: %v", err)
	}
}
