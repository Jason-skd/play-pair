package db

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/auth"
)

func TestAuthRepositoryRealDatabase(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("PLAYPAIR_INTEGRATION") == "1" {
			t.Fatal("TEST_DATABASE_URL is required")
		}
		t.Skip("set TEST_DATABASE_URL for integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	database, err := Open(ctx, url, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Microsecond)
	service := auth.New(database, func() time.Time { return now })
	email := "auth-" + now.Format("20060102150405.000000") + "@example.com"
	u, token, _, err := service.Register(ctx, email, "correct horse battery", "Database User")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := service.Authenticate(ctx, token); err != nil || got.ID != u.ID {
		t.Fatalf("authenticate: %#v %v", got, err)
	}
	if _, _, _, err := service.Register(ctx, email, "different valid password", "Duplicate"); !errors.Is(err, auth.ErrEmailConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := service.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("revoked: %v", err)
	}
}
