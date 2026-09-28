package group

import (
	"context"
	"errors"
	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

type captureRepo struct {
	name, code string
	calls      int
	deadline   time.Time
}

func (r *captureRepo) capture(ctx context.Context) { r.calls++; r.deadline, _ = ctx.Deadline() }
func (r *captureRepo) CreateGroup(ctx context.Context, _ users.ID, _ uuid.UUID, name string) (Group, bool, error) {
	r.capture(ctx)
	r.name = name
	return Group{}, true, nil
}
func (r *captureRepo) JoinGroup(ctx context.Context, _ users.ID, code string) (Group, error) {
	r.capture(ctx)
	r.code = code
	return Group{}, nil
}
func (r *captureRepo) GetGroup(ctx context.Context, _ users.ID, _ ID) (Group, error) {
	r.capture(ctx)
	return Group{}, nil
}
func (r *captureRepo) GetGroupMembers(ctx context.Context, _ users.ID, _ ID, _ users.ID, _ int) (Page, error) {
	r.capture(ctx)
	return Page{}, nil
}
func TestValidation(t *testing.T) {
	for _, name := range []string{"", " \n", strings.Repeat("中", 81), "bad\x00", string([]byte{255})} {
		if _, err := NormalizeName(name); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("accepted name", name)
		}
	}
	for _, name := range []string{"中", strings.Repeat("中", 80), " \u3000周末搭子\u0085"} {
		if _, err := NormalizeName(name); err != nil {
			t.Fatal(err)
		}
	}
	for _, code := range []string{"", strings.Repeat("a", 15), strings.Repeat("a", 17), "AAAAAAAAAAAAAAA1", "AAAAAAAAAAAAAAA8", "AAAAAAAAAAAAAAA ", "AAAAAAAAAAAAAAAſ"} {
		if _, err := NormalizeCode(code); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("accepted code", code)
		}
	}
	if got, err := NormalizeCode(" \naBcDeFgHiJ234567\u3000"); err != nil || got != "ABCDEFGHIJ234567" {
		t.Fatal(got, err)
	}
	if Role(1, 1) != "initiator" || Role(2, 1) != "member" {
		t.Fatal("roles")
	}
}
func TestServiceBoundaries(t *testing.T) {
	r := &captureRepo{}
	s := New(r)
	ctx := context.Background()
	if _, _, err := s.Create(ctx, 1, uuid.New(), " 周末 "); err != nil || r.name != "周末" {
		t.Fatal(err, r.name)
	}
	if time.Until(r.deadline) > 5*time.Second || r.deadline.IsZero() {
		t.Fatal("unbounded operation")
	}
	if _, err := s.Join(ctx, 1, "abcdefghijklmnop"); err != nil || r.code != "ABCDEFGHIJKLMNOP" {
		t.Fatal(err, r.code)
	}
	before := r.calls
	for _, id := range []ID{0, -1} {
		if _, err := s.Get(ctx, 1, id); err == nil {
			t.Fatal("invalid id")
		}
	}
	for _, limit := range []int{0, -1, 101} {
		if _, err := s.Members(ctx, 1, 1, 0, limit); err == nil {
			t.Fatal("invalid limit")
		}
	}
	if _, err := s.Members(ctx, 1, 1, -1, 50); err == nil {
		t.Fatal("invalid cursor")
	}
	if _, _, err := s.Create(ctx, 0, uuid.New(), "name"); err == nil {
		t.Fatal("invalid actor")
	}
	if r.calls != before {
		t.Fatal("invalid input reached repository")
	}
	early, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if _, err := s.Get(early, 1, 1); err != nil {
		t.Fatal(err)
	}
	if d, _ := early.Deadline(); !r.deadline.Equal(d) {
		t.Fatal("parent deadline lost")
	}
}
