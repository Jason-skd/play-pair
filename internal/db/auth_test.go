package db

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/credentials"
	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/Jason-skd/play-pair/internal/session"
	"github.com/Jason-skd/play-pair/internal/users"
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
	sessions := session.New(database, func() time.Time { return now })
	service := credentials.New(database, sessions, func() time.Time { return now })
	email := "auth-" + now.Format("20060102150405.000000") + "@example.com"
	u, token, _, err := service.Register(ctx, email, "correct horse battery", "Database User")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		// Tests remove the user so its cascaded credential and sessions do not accumulate.
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := database.pool.Exec(cleanup, "DELETE FROM users WHERE id = $1", u.ID); err != nil {
			t.Errorf("cleanup user: %v", err)
		}
	}()
	var hash string
	var updatedAt time.Time
	if err := database.pool.QueryRow(ctx, "SELECT password_hash, updated_at FROM password_credentials WHERE user_id = $1", u.ID).Scan(&hash, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if hash == "" || hash == "correct horse battery" || !updatedAt.Equal(now) {
		t.Fatal("credential must contain a hash and the registration timestamp")
	}
	if _, _, _, err := service.Login(ctx, email, "correct horse battery"); err != nil {
		t.Fatalf("password login: %v", err)
	}
	if got, err := sessions.Authenticate(ctx, token); err != nil || got.ID != u.ID {
		t.Fatalf("authenticate: %#v %v", got, err)
	}
	if _, _, _, err := service.Register(ctx, email, "different valid password", "Duplicate"); !errors.Is(err, credentials.ErrEmailConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := database.pool.Exec(ctx, "DELETE FROM password_credentials WHERE user_id = $1", u.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := sessions.Authenticate(ctx, token); err != nil || got.ID != u.ID || !got.CreatedAt.Equal(u.CreatedAt) {
		t.Fatalf("session without password credential: %#v %v", got, err)
	}
	if _, _, _, err := service.Login(ctx, email, "correct horse battery"); !errors.Is(err, credentials.ErrInvalidCredentials) {
		t.Fatalf("login without password credential: %v", err)
	}
	if err := sessions.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Authenticate(ctx, token); !errors.Is(err, session.ErrUnauthenticated) {
		t.Fatalf("revoked: %v", err)
	}

	t.Run("session failure rolls back identity and credential", func(t *testing.T) {
		var id string
		// Let PostgreSQL supply a valid UUID without coupling this test to auth's generator.
		if err := database.pool.QueryRow(ctx, "SELECT gen_random_uuid()::text").Scan(&id); err != nil {
			t.Fatal(err)
		}
		defer func() {
			cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if _, err := database.pool.Exec(cleanup, "DELETE FROM users WHERE id = $1", id); err != nil {
				t.Errorf("cleanup rollback fixture: %v", err)
			}
		}()
		credential := credentials.Registration{PasswordCredential: credentials.PasswordCredential{Account: users.Account{ID: users.ID(id), CreatedAt: now}, Email: id + "@example.com", PasswordHash: hash}, DisplayName: "Rollback"}
		var digest [32]byte
		if _, err := rand.Read(digest[:]); err != nil {
			t.Fatal(err)
		}
		// Equal creation and expiry violates the session CHECK after both earlier inserts.
		if err := database.Register(ctx, credential, session.Record{UserID: credential.ID, TokenHash: digest, CreatedAt: now, ExpiresAt: now}); !errors.Is(err, errs.DependencyUnavailable) {
			t.Fatalf("invalid session expiry: %v", err)
		}
		var users, credentials, sessions, profiles int
		if err := database.pool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM users WHERE id = $1),
			(SELECT count(*) FROM password_credentials WHERE user_id = $1),
			(SELECT count(*) FROM sessions WHERE user_id = $1),
 (SELECT count(*) FROM profiles WHERE user_id = $1)`, id).Scan(&users, &credentials, &sessions, &profiles); err != nil {
			t.Fatal(err)
		}
		if users != 0 || credentials != 0 || sessions != 0 || profiles != 0 {
			t.Fatalf("partial registration persisted: users=%d credentials=%d sessions=%d", users, credentials, sessions)
		}
	})
}
