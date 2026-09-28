package credentials

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/session"
	"github.com/Jason-skd/play-pair/internal/users"
)

type registeredUser struct {
	PasswordCredential
	DisplayName string
}

type memoryRepo struct {
	users    map[string]registeredUser
	sessions map[[32]byte]users.Account
	expires  map[[32]byte]time.Time
}

func newMemory() *memoryRepo {
	return &memoryRepo{map[string]registeredUser{}, map[[32]byte]users.Account{}, map[[32]byte]time.Time{}}
}
func (m *memoryRepo) Register(_ context.Context, command Registration, record session.Pending) (users.Account, error) {
	h, exp := record.TokenHash, record.ExpiresAt
	if _, ok := m.users[command.Email]; ok {
		return users.Account{}, ErrEmailConflict
	}
	u := registeredUser{
		PasswordCredential: PasswordCredential{Account: users.Account{ID: users.ID(len(m.users) + 1), CreatedAt: command.CreatedAt}, Email: command.Email, PasswordHash: command.PasswordHash},
		DisplayName:        command.DisplayName,
	}
	m.users[u.Email] = u
	m.sessions[h] = u.Account
	m.expires[h] = exp
	return u.Account, nil
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

type registrationResultRepo struct {
	*memoryRepo
	account users.Account
	err     error
	command Registration
	pending session.Pending
}

func (r *registrationResultRepo) Register(_ context.Context, c Registration, p session.Pending) (users.Account, error) {
	r.command, r.pending = c, p
	return r.account, r.err
}

func TestRegisterUsesPersistedIdentityAndWithholdsFailedResults(t *testing.T) {
	now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "returned identity"},
		{name: "failed transaction", err: errors.New("commit failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := users.Account{ID: 812345678901234567, CreatedAt: now}
			repo := &registrationResultRepo{memoryRepo: newMemory(), account: account, err: tc.err}
			clock := func() time.Time { return now }
			s := New(repo, session.New(repo, clock), clock)
			got, token, expires, err := s.Register(context.Background(), " User@Example.com ", "correct horse battery", " Name ")
			if tc.err != nil {
				if !errors.Is(err, repo.err) || got != (users.Account{}) || token != "" || !expires.IsZero() {
					t.Fatalf("failed write exposed identity or session: %#v %q %v %v", got, token, expires, err)
				}
			} else if err != nil || got != account || token == "" || !expires.Equal(now.Add(session.Duration)) {
				t.Fatalf("repository identity not returned: %#v %v", got, err)
			}
			if repo.command.Email != "user@example.com" || repo.command.DisplayName != "Name" || !repo.command.CreatedAt.Equal(now) || !repo.pending.CreatedAt.Equal(now) || !repo.pending.ExpiresAt.Equal(now.Add(session.Duration)) {
				t.Fatal("registration data or pending session lost")
			}
		})
	}
}
