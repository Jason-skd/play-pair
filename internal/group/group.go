// Package group owns persistent friend groups and code-authorized membership.
package group

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/google/uuid"
)

type ID int64

var (
	ErrInvalidInput        = errors.New("invalid group input")
	ErrForbidden           = errors.New("group membership required")
	ErrIdempotencyConflict = errors.New("group creation key conflict")
)

type Group struct {
	ID                   ID
	Name, JoinCode       string
	InitiatorID          users.ID
	CreatedAt, UpdatedAt time.Time
}
type Member struct {
	MemberID    users.ID
	Role        string
	JoinedAt    time.Time
	DisplayName string
	Nickname    *string
}
type Page struct {
	Members           []Member
	NextAfterMemberID *users.ID
}

// Role derives authority from groups.initiator_id, never from membership metadata.
func Role(member, initiator users.ID) string {
	if member == initiator {
		return "initiator"
	}
	return "member"
}

// Repository requires actor on every read. Create and Join commit atomically;
// retries preserve timestamps and Create reports whether it inserted a group.
type Repository interface {
	CreateGroup(context.Context, users.ID, uuid.UUID, string) (Group, bool, error)
	JoinGroup(context.Context, users.ID, string) (Group, error)
	GetGroup(context.Context, users.ID, ID) (Group, error)
	GetGroupMembers(context.Context, users.ID, ID, users.ID, int) (Page, error)
}
type Service struct{ repo Repository }

func New(repo Repository) *Service { return &Service{repo: repo} }

func NormalizeName(name string) (string, error) {
	if !utf8.ValidString(name) || strings.ContainsRune(name, 0) {
		return "", ErrInvalidInput
	}
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 80 {
		return "", ErrInvalidInput
	}
	return name, nil
}
func NormalizeCode(code string) (string, error) {
	code = strings.TrimSpace(code)
	if len(code) != 16 {
		return "", ErrInvalidInput
	}
	b := []byte(code)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
			b[i] = c
		}
		if !(c >= 'A' && c <= 'Z' || c >= '2' && c <= '7') {
			return "", ErrInvalidInput
		}
	}
	return string(b), nil
}
func (s *Service) Create(ctx context.Context, actor users.ID, key uuid.UUID, name string) (Group, bool, error) {
	name, err := NormalizeName(name)
	if err != nil || actor <= 0 {
		return Group{}, false, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.repo.CreateGroup(ctx, actor, key, name)
}
func (s *Service) Join(ctx context.Context, actor users.ID, code string) (Group, error) {
	code, err := NormalizeCode(code)
	if err != nil || actor <= 0 {
		return Group{}, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.repo.JoinGroup(ctx, actor, code)
}
func (s *Service) Get(ctx context.Context, actor users.ID, id ID) (Group, error) {
	if id <= 0 || actor <= 0 {
		return Group{}, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.repo.GetGroup(ctx, actor, id)
}
func (s *Service) Members(ctx context.Context, actor users.ID, id ID, after users.ID, limit int) (Page, error) {
	if actor <= 0 || id <= 0 || after < 0 || limit < 1 || limit > 100 {
		return Page{}, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.repo.GetGroupMembers(ctx, actor, id, after, limit)
}
