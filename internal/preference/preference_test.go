package preference

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Jason-skd/play-pair/internal/users"
)

type captureRepo struct{ calls int }

func (r *captureRepo) GetPreference(context.Context, users.ID) (Preference, error) {
	return Preference{}, nil
}
func (r *captureRepo) UpdatePreference(_ context.Context, id users.ID, c Update) (Preference, error) {
	r.calls++
	return Preference{UserID: id, Soft: c.Soft, HardDefaults: c.HardDefaults, Version: c.ExpectedVersion + 1}, nil
}
func TestPreferenceValidationAndNulls(t *testing.T) {
	r := &captureRepo{}
	s := New(r)
	zero := int64(0)
	yes, no := true, false
	for _, hard := range []HardDefaults{{}, {MaxBudgetFen: &zero, RequiresWheelchairAccess: &no}, {RequiresWheelchairAccess: &yes}} {
		p, err := s.Replace(context.Background(), 1, Update{Soft: Soft{Likes: " 喜欢桌游\n可以聊天 ", Dislikes: "太吵"}, HardDefaults: hard, ExpectedVersion: 1})
		if err != nil || p.Version != 2 || p.Soft.Likes != "喜欢桌游\n可以聊天" || p.Soft.Neutral != "" {
			t.Fatal(p, err)
		}
		if (p.HardDefaults.MaxBudgetFen == nil) != (hard.MaxBudgetFen == nil) || (p.HardDefaults.RequiresWheelchairAccess == nil) != (hard.RequiresWheelchairAccess == nil) {
			t.Fatal("lost null")
		}
		if hard.RequiresWheelchairAccess != nil && *p.HardDefaults.RequiresWheelchairAccess != *hard.RequiresWheelchairAccess {
			t.Fatal("lost boolean")
		}
	}
	negative := int64(-1)
	for _, c := range []Update{{ExpectedVersion: 0}, {ExpectedVersion: 1, HardDefaults: HardDefaults{MaxBudgetFen: &negative}}, {ExpectedVersion: 1, Soft: Soft{Likes: strings.Repeat("中", 501)}}, {ExpectedVersion: 1, Soft: Soft{Neutral: "\x00"}}, {ExpectedVersion: 1, Soft: Soft{Dislikes: string([]byte{0xff})}}} {
		before := r.calls
		if _, err := s.Replace(context.Background(), 1, c); !errors.Is(err, ErrInvalidInput) || r.calls != before {
			t.Fatal("invalid input persisted", err)
		}
	}
	if _, err := s.Replace(context.Background(), 1, Update{ExpectedVersion: 1, Soft: Soft{Likes: strings.Repeat("中", 500)}}); err != nil {
		t.Fatal(err)
	}
}
