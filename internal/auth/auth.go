// Package auth owns credentials and server-side session lifecycle.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

var (
	ErrInvalidInput       = errors.New("invalid input")
	ErrEmailConflict      = errors.New("email conflict")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthenticated    = errors.New("unauthenticated")
)

type User struct {
	ID          string
	Email       string
	DisplayName string
	CreatedAt   time.Time
}

type StoredUser struct {
	User
	PasswordHash string
}

type Repository interface {
	Register(context.Context, StoredUser, [32]byte, time.Time) error
	FindByEmail(context.Context, string) (StoredUser, error)
	CreateSession(context.Context, string, [32]byte, time.Time, time.Time) error
	FindSessionUser(context.Context, [32]byte, time.Time) (StoredUser, error)
	DeleteSession(context.Context, [32]byte) error
}

type Service struct {
	repo      Repository
	now       func() time.Time
	gate      chan struct{}
	dummyOnce sync.Once
	dummyHash string
}

const SessionDuration = 7 * 24 * time.Hour

func New(repo Repository, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, now: now, gate: make(chan struct{}, 2)}
}

func (s *Service) Register(ctx context.Context, email, password, displayName string) (User, string, time.Time, error) {
	email, displayName, err := validate(email, password, displayName)
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	hash, err := s.hash(ctx, password)
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	id, err := randomUUID()
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	token, digest, err := newToken()
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	now := s.now().UTC()
	user := User{ID: id, Email: email, DisplayName: displayName, CreatedAt: now}
	if err := s.repo.Register(ctx, StoredUser{User: user, PasswordHash: hash}, digest, now.Add(SessionDuration)); err != nil {
		return User{}, "", time.Time{}, err
	}
	return user, token, now.Add(SessionDuration), nil
}

func (s *Service) Login(ctx context.Context, email, password string) (User, string, time.Time, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !validEmail(email) || !validPassword(password) {
		return User{}, "", time.Time{}, ErrInvalidCredentials
	}
	u, err := s.repo.FindByEmail(ctx, email)
	if err != nil {
		_ = s.verify(ctx, password, s.dummy())
		return User{}, "", time.Time{}, ErrInvalidCredentials
	}
	if !s.verify(ctx, password, u.PasswordHash) {
		return User{}, "", time.Time{}, ErrInvalidCredentials
	}
	token, digest, err := newToken()
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	now := s.now().UTC()
	if err := s.repo.CreateSession(ctx, u.ID, digest, now, now.Add(SessionDuration)); err != nil {
		return User{}, "", time.Time{}, err
	}
	return u.User, token, now.Add(SessionDuration), nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (User, error) {
	digest, ok := tokenDigest(token)
	if !ok {
		return User{}, ErrUnauthenticated
	}
	u, err := s.repo.FindSessionUser(ctx, digest, s.now().UTC())
	if err != nil {
		return User{}, ErrUnauthenticated
	}
	return u.User, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	digest, ok := tokenDigest(token)
	if !ok {
		return nil
	}
	return s.repo.DeleteSession(ctx, digest)
}

func validate(email, password, name string) (string, string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	name = strings.TrimSpace(name)
	if !validEmail(email) || !validPassword(password) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 80 {
		return "", "", ErrInvalidInput
	}
	return email, name, nil
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

func newToken() (string, [32]byte, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", [32]byte{}, err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), sha256.Sum256(raw[:]), nil
}

func tokenDigest(token string) ([32]byte, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return [32]byte{}, false
	}
	return sha256.Sum256(raw), true
}

func randomUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
