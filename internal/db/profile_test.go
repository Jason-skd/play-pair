package db

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/credentials"
	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/Jason-skd/play-pair/internal/profile"
	"github.com/Jason-skd/play-pair/internal/session"
	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/google/uuid"
)

func resourceFixture(t *testing.T) (*DB, context.Context, users.ID) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("PLAYPAIR_INTEGRATION") == "1" {
			t.Fatal("TEST_DATABASE_URL is required")
		}
		t.Skip("set TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	db, err := Open(ctx, url, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	s := credentials.New(db, session.New(db, time.Now), time.Now)
	u, _, _, err := s.Register(ctx, uuid.NewString()+"@example.com", "correct horse battery", "Initial")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := db.pool.Exec(ctx, "DELETE FROM users WHERE id=$1", u.ID); err != nil {
			t.Error(err)
		}
	})
	return db, ctx, u.ID
}

func TestProfileDatabase(t *testing.T) {
	db, ctx, id := resourceFixture(t)
	s := profile.New(db)
	p, err := s.Get(ctx, id)
	if err != nil || p.Version != 1 || p.Nickname != nil {
		t.Fatal(p, err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, name := range []string{"First", "Second"} {
		go func() {
			<-start
			_, err := s.Replace(ctx, id, profile.Update{DisplayName: name, ExpectedVersion: 1})
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
	if err != nil || p.Version != 2 {
		t.Fatal(p, err)
	}
	_, name, err := db.UserDetails(ctx, id)
	if err != nil || name != p.DisplayName {
		t.Fatal("identity projection stale", err)
	}
	nick := "Friend"
	p, err = s.Replace(ctx, id, profile.Update{DisplayName: "Final", Nickname: &nick, ExpectedVersion: 2})
	if err != nil || p.Name() != "Friend" {
		t.Fatal(p, err)
	}
	p, err = s.Replace(ctx, id, profile.Update{DisplayName: "Final", ExpectedVersion: 3})
	if err != nil || p.Name() != "Final" || p.Nickname != nil {
		t.Fatal(p, err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE profiles SET display_name='' WHERE user_id=$1`, id); err == nil {
		t.Fatal("database accepted empty name")
	}
	p, err = s.Get(ctx, id)
	if err != nil || p.Version != 4 || p.DisplayName != "Final" {
		t.Fatal("failed write mutated profile", p, err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE profiles SET version=0 WHERE user_id=$1`, id); err == nil {
		t.Fatal("database accepted zero version")
	}
}
