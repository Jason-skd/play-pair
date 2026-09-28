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
	if u.ID <= 0 || !u.CreatedAt.Equal(now) {
		t.Fatalf("registration did not return its persisted identity: %#v", u)
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
	var accounts int
	if err := database.pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE created_at=$1", now).Scan(&accounts); err != nil || accounts != 1 {
		t.Fatalf("duplicate email left an allocated account: count=%d err=%v", accounts, err)
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

	t.Run("session failure rolls back allocated account and dependents", func(t *testing.T) {
		// Use a distinct timestamp to detect a leaked account without knowing the
		// allocated ID. Foreign keys prevent child rows surviving its rollback.
		createdAt := now.Add(time.Hour)
		rollbackEmail := "rollback-" + email
		defer func() {
			cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if _, err := database.pool.Exec(cleanup, "DELETE FROM users WHERE created_at = $1", createdAt); err != nil {
				t.Errorf("cleanup rollback fixture: %v", err)
			}
		}()
		credential := credentials.Registration{CreatedAt: createdAt, Email: rollbackEmail, PasswordHash: hash, DisplayName: rollbackEmail}
		var digest [32]byte
		if _, err := rand.Read(digest[:]); err != nil {
			t.Fatal(err)
		}
		// Equal creation and expiry fails after all four other inserts.
		account, err := database.Register(ctx, credential, session.Pending{TokenHash: digest, CreatedAt: now, ExpiresAt: now})
		if !errors.Is(err, errs.DependencyUnavailable) || account != (users.Account{}) {
			t.Fatalf("invalid session expiry returned account=%#v err=%v", account, err)
		}
		var users, credentials, sessions, profiles int
		if err := database.pool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM users WHERE created_at = $1),
			(SELECT count(*) FROM password_credentials WHERE email = $2),
			(SELECT count(*) FROM sessions WHERE token_hash = $3),
			(SELECT count(*) FROM profiles WHERE display_name = $2)`, createdAt, rollbackEmail, digest[:]).Scan(&users, &credentials, &sessions, &profiles); err != nil {
			t.Fatal(err)
		}
		if users != 0 || credentials != 0 || sessions != 0 || profiles != 0 {
			t.Fatalf("partial registration persisted: users=%d credentials=%d sessions=%d profiles=%d", users, credentials, sessions, profiles)
		}
	})
}
