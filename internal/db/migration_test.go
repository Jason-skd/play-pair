package db

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Run migrations in a unique schema so upgrade fixtures never touch application data.
func migrationConnection(t *testing.T) (*pgx.Conn, context.Context) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("PLAYPAIR_INTEGRATION") == "1" {
			t.Fatal("TEST_DATABASE_URL is required")
		}
		t.Skip("set TEST_DATABASE_URL for integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{"migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := c.Exec(cleanup, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
		if err := c.Close(cleanup); err != nil {
			t.Error(err)
		}
	})
	if _, err := c.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	return c, ctx
}

func migrateFixture(ctx context.Context, c *pgx.Conn, name string) error {
	data, err := os.ReadFile("../../db/migrations/" + name)
	if err != nil {
		return err
	}
	sql := strings.Split(string(data), "-- +goose Down")[0]
	tx, err := c.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, sql); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func TestAccountMigrationPreservesData(t *testing.T) {
	c, ctx := migrationConnection(t)
	if err := migrateFixture(ctx, c, "00002_auth.sql"); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err := c.Exec(ctx, `INSERT INTO users VALUES ($1,'old@example.com','Old Name',now());`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exec(ctx, `INSERT INTO password_credentials VALUES ($1,'retained-hash',now())`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exec(ctx, `INSERT INTO sessions VALUES (decode(repeat('ab',32),'hex'),$1,now(),now()+interval '1 day')`, id); err != nil {
		t.Fatal(err)
	}
	if err := migrateFixture(ctx, c, "00003_account_modules.sql"); err != nil {
		t.Fatal(err)
	}
	var email, hash, name string
	var version int64
	var count int
	if err := c.QueryRow(ctx, `SELECT c.email,c.password_hash,p.display_name,p.version,(SELECT count(*) FROM sessions WHERE user_id=u.id)
FROM users u JOIN password_credentials c ON c.user_id=u.id JOIN profiles p ON p.user_id=u.id WHERE u.id=$1`, id).Scan(&email, &hash, &name, &version, &count); err != nil {
		t.Fatal(err)
	}
	if email != "old@example.com" || hash != "retained-hash" || name != "Old Name" || version != 1 || count != 1 {
		t.Fatalf("migration lost data: %q %q %d %d", email, name, version, count)
	}
}

func TestAccountMigrationRejectsOrphansAtomically(t *testing.T) {
	c, ctx := migrationConnection(t)
	if err := migrateFixture(ctx, c, "00002_auth.sql"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exec(ctx, `INSERT INTO users VALUES ($1,'orphan@example.com','Orphan',now())`, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := migrateFixture(ctx, c, "00003_account_modules.sql"); err == nil {
		t.Fatal("expected orphan check")
	}
	var email, name string
	if err := c.QueryRow(ctx, `SELECT email,display_name FROM users`).Scan(&email, &name); err != nil {
		t.Fatal(err)
	}
	if email != "orphan@example.com" || name != "Orphan" {
		t.Fatal("orphan changed")
	}
}
