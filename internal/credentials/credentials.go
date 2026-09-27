// Package credentials owns password credentials and registration/login use cases.
package credentials

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Jason-skd/play-pair/internal/profile"
	"github.com/Jason-skd/play-pair/internal/session"
	"github.com/Jason-skd/play-pair/internal/users"
	"golang.org/x/crypto/argon2"
)

var (
	ErrInvalidInput       = errors.New("invalid registration")
	ErrEmailConflict      = errors.New("email conflict")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrNotFound           = errors.New("credential not found")
)

type PasswordCredential struct {
	users.Account
	Email        string
	PasswordHash string
}

// Registration contains initial account, credential and display data for one atomic write.
type Registration struct {
	PasswordCredential
	DisplayName string
}
type Repository interface {
	Register(context.Context, Registration, session.Record) error
	FindPasswordCredentialByEmail(context.Context, string) (PasswordCredential, error)
}
type SessionIssuer interface {
	Prepare(users.ID) (session.Issued, error)
	Issue(context.Context, users.ID) (session.Issued, error)
}
type Service struct {
	repo      Repository
	sessions  SessionIssuer
	now       func() time.Time
	gate      chan struct{}
	dummyOnce sync.Once
	dummyHash string
}

func New(repo Repository, sessions SessionIssuer, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, sessions: sessions, now: now, gate: make(chan struct{}, 2)}
}
func (s *Service) Register(ctx context.Context, email, password, displayName string) (users.Account, string, time.Time, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	name, err := profile.NormalizeName(displayName)
	if err != nil || !validEmail(email) || !validPassword(password) {
		return users.Account{}, "", time.Time{}, ErrInvalidInput
	}
	hash, err := s.hash(ctx, password)
	if err != nil {
		return users.Account{}, "", time.Time{}, err
	}
	account, err := users.New(s.now())
	if err != nil {
		return users.Account{}, "", time.Time{}, err
	}
	issued, err := s.sessions.Prepare(account.ID)
	if err != nil {
		return users.Account{}, "", time.Time{}, err
	}
	command := Registration{PasswordCredential: PasswordCredential{Account: account, Email: email, PasswordHash: hash}, DisplayName: name}
	if err := s.repo.Register(ctx, command, issued.Record); err != nil {
		return users.Account{}, "", time.Time{}, err
	}
	return account, issued.Token, issued.Record.ExpiresAt, nil
}
func (s *Service) Login(ctx context.Context, email, password string) (users.Account, string, time.Time, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !validEmail(email) || !validPassword(password) {
		return users.Account{}, "", time.Time{}, ErrInvalidCredentials
	}
	credential, err := s.repo.FindPasswordCredentialByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		_ = s.verify(ctx, password, s.dummy())
		return users.Account{}, "", time.Time{}, ErrInvalidCredentials
	}
	if err != nil {
		return users.Account{}, "", time.Time{}, err
	}
	if !s.verify(ctx, password, credential.PasswordHash) {
		return users.Account{}, "", time.Time{}, ErrInvalidCredentials
	}
	issued, err := s.sessions.Issue(ctx, credential.ID)
	if err != nil {
		return users.Account{}, "", time.Time{}, err
	}
	return credential.Account, issued.Token, issued.Record.ExpiresAt, nil
}
func validEmail(v string) bool {
	if len(v) == 0 || len(v) > 254 {
		return false
	}
	a, err := mail.ParseAddress(v)
	return err == nil && a.Address == v && isASCII(v)
}

func validPassword(v string) bool {
	n := utf8.RuneCountInString(v)
	return utf8.ValidString(v) && n >= 15 && n <= 128
}
func isASCII(v string) bool {
	for _, r := range v {
		if r > 127 {
			return false
		}
	}
	return true
}

func (s *Service) hash(ctx context.Context, password string) (string, error) {
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 1, 32)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func (s *Service) verify(ctx context.Context, password, encoded string) bool {
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	var memory, iterations uint32
	var parallel uint8
	// Accept only the supported cost profile so stored data cannot request unbounded work.
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallel); err != nil || memory != 64*1024 || iterations != 3 || parallel != 1 {
		return false
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, e2 := base64.RawStdEncoding.DecodeString(parts[5])
	if e1 != nil || e2 != nil || len(salt) != 16 || len(want) != 32 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallel, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func (s *Service) dummy() string {
	s.dummyOnce.Do(func() {
		salt := []byte("0123456789abcdef")
		key := argon2.IDKey([]byte("fixed invalid credential"), salt, 3, 64*1024, 1, 32)
		s.dummyHash = fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
	})
	return s.dummyHash
}
