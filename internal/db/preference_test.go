package db

import (
	"errors"
	"strings"
	"testing"

	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/Jason-skd/play-pair/internal/preference"
	"github.com/Jason-skd/play-pair/internal/profile"
	"github.com/google/uuid"
)

func TestPreferenceDatabase(t *testing.T) {
	db, ctx, id := resourceFixture(t)
	s := preference.New(db)
	p, err := s.Get(ctx, id)
	if err != nil || p.Version != 1 || p.Soft != (preference.Soft{}) || p.HardDefaults.MaxBudgetFen != nil || p.HardDefaults.RequiresWheelchairAccess != nil {
		t.Fatal(p, err)
	}
	zero := int64(0)
	no, yes := false, true
	for _, hard := range []preference.HardDefaults{{MaxBudgetFen: &zero, RequiresWheelchairAccess: &no}, {RequiresWheelchairAccess: &yes}, {}} {
		p, err = s.Replace(ctx, id, preference.Update{Soft: preference.Soft{Likes: "桌游", Neutral: "电影", Dislikes: "太吵"}, HardDefaults: hard, ExpectedVersion: p.Version})
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if (got.HardDefaults.MaxBudgetFen == nil) != (hard.MaxBudgetFen == nil) || (got.HardDefaults.RequiresWheelchairAccess == nil) != (hard.RequiresWheelchairAccess == nil) {
			t.Fatal("null lost", got)
		}
		if hard.MaxBudgetFen != nil && *got.HardDefaults.MaxBudgetFen != *hard.MaxBudgetFen {
			t.Fatal("budget lost")
		}
		if hard.RequiresWheelchairAccess != nil && *got.HardDefaults.RequiresWheelchairAccess != *hard.RequiresWheelchairAccess {
			t.Fatal("boolean lost")
		}
	}
	old := p
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, likes := range []string{"户外", "室内"} {
		go func() {
			<-start
			_, err := s.Replace(ctx, id, preference.Update{Soft: preference.Soft{Likes: likes}, ExpectedVersion: old.Version})
			results <- err
		}()
	}
	close(start)
	wins, conflicts := 0, 0
	for range 2 {
		select {
		case err := <-results:
			if err == nil {
				wins++
			} else if errors.Is(err, errs.VersionConflict) {
				conflicts++
			} else {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal(wins, conflicts)
	}
	p, err = s.Get(ctx, id)
	if err != nil || p.Version != old.Version+1 {
		t.Fatal(p, err)
	}
	if old.Soft.Likes != "桌游" || old.Version != 4 {
		t.Fatal("previously read snapshot changed", old)
	}
	profiles := profile.New(db)
	pr, err := profiles.Get(ctx, id)
	if err != nil || pr.Version != 1 {
		t.Fatal("preference changed profile", pr, err)
	}
	if _, err := profiles.Replace(ctx, id, profile.Update{DisplayName: "New", ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, id)
	if err != nil || got.Version != p.Version {
		t.Fatal("profile changed preference", got, err)
	}
	for _, query := range []string{
		`UPDATE preferences SET max_budget_fen=-1 WHERE user_id=$1`,
		`UPDATE preferences SET likes=repeat('中',501) WHERE user_id=$1`,
		`UPDATE preferences SET version=0 WHERE user_id=$1`,
	} {
		if _, err := db.pool.Exec(ctx, query, id); err == nil {
			t.Fatal("database accepted invalid value")
		}
	}
	got, err = s.Get(ctx, id)
	if err != nil || got.Version != p.Version || got.Soft != p.Soft {
		t.Fatal("failed write mutated preference", got, err)
	}
	if _, err := db.pool.Exec(ctx, `INSERT INTO preferences(user_id) VALUES($1)`, id); err == nil {
		t.Fatal("duplicate preference accepted")
	}
	if _, err := db.pool.Exec(ctx, `INSERT INTO preferences(user_id) VALUES($1)`, int64(-1)); err == nil {
		t.Fatal("orphan preference accepted")
	}
	if _, err := db.pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, id); !errors.Is(err, errs.NotFound) {
		t.Fatal("preference did not cascade", err)
	}
}

func TestPreferenceMigrationBackfillsEmptyDefaults(t *testing.T) {
	c, ctx := migrationConnection(t)
	for _, name := range []string{"00002_auth.sql", "00003_account_modules.sql"} {
		if err := migrateFixture(ctx, c, name); err != nil {
			t.Fatal(err)
		}
	}
	id := uuid.NewString()
	if _, err := c.Exec(ctx, `INSERT INTO users(id,created_at) VALUES($1,now())`, id); err != nil {
		t.Fatal(err)
	}
	if err := migrateFixture(ctx, c, "00004_preferences.sql"); err != nil {
		t.Fatal(err)
	}
	var version int64
	var budget *int64
	var access *bool
	var likes, neutral, dislikes string
	if err := c.QueryRow(ctx, `SELECT version,max_budget_fen,requires_wheelchair_access,likes,neutral,dislikes FROM preferences WHERE user_id=$1`, id).Scan(&version, &budget, &access, &likes, &neutral, &dislikes); err != nil {
		t.Fatal(err)
	}
	if version != 1 || budget != nil || access != nil || strings.Join([]string{likes, neutral, dislikes}, "") != "" {
		t.Fatal("backfill imposed constraints")
	}
}
