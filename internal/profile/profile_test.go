package profile

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Jason-skd/play-pair/internal/users"
)

type captureRepo struct {
	calls   int
	command Update
}

func (r *captureRepo) GetProfile(context.Context, users.ID) (Profile, error) { return Profile{}, nil }
func (r *captureRepo) UpdateProfile(_ context.Context, id users.ID, c Update) (Profile, error) {
	r.calls++
	r.command = c
	return Profile{UserID: id, DisplayName: c.DisplayName, Nickname: c.Nickname, Version: c.ExpectedVersion + 1}, nil
}
func TestReplaceProfile(t *testing.T) {
	nick := "  昵称  "
	r := &captureRepo{}
	s := New(r)
	p, err := s.Replace(context.Background(), "user", Update{DisplayName: "  显示名 ", Nickname: &nick, ExpectedVersion: 1})
	if err != nil || p.Name() != "昵称" || p.DisplayName != "显示名" || p.Version != 2 {
		t.Fatal(p, err)
	}
	nick = "  "
	p, err = s.Replace(context.Background(), "user", Update{DisplayName: "显示名", Nickname: &nick, ExpectedVersion: 2})
	if err != nil || p.Nickname != nil || p.Name() != "显示名" {
		t.Fatal(p, err)
	}
	for _, command := range []Update{{DisplayName: " ", ExpectedVersion: 1}, {DisplayName: strings.Repeat("中", 81), ExpectedVersion: 1}, {DisplayName: "Name"}, {DisplayName: "\x00", ExpectedVersion: 1}} {
		before := r.calls
		if _, err := s.Replace(context.Background(), "user", command); !errors.Is(err, ErrInvalidInput) || r.calls != before {
			t.Fatal("invalid data reached repository", err)
		}
	}
}
