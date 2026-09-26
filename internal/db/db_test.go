package db

import (
	"context"
	"os"
	"testing"
	"time"

	"errors"
	"github.com/Jason-skd/play-pair/internal/errs"
)

func TestRealDatabase(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("PLAYPAIR_INTEGRATION") == "1" {
			t.Fatal("TEST_DATABASE_URL is required")
		}
		t.Skip("set TEST_DATABASE_URL for integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	database, err := Open(ctx, url, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Check(ctx); err != nil {
		t.Fatal(err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if err := database.Check(canceled); !errors.Is(err, errs.DependencyUnavailable) {
		t.Fatalf("canceled query: %v", err)
	}
}
