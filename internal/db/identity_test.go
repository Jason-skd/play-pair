package db

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestIdentityMigration(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(fmt.Sprintf("populated=%t", populated), func(t *testing.T) {
			conn, ctx := migrationConnection(t)
			for _, name := range []string{"00002_auth.sql", "00003_account_modules.sql", "00004_preferences.sql"} {
				if err := migrateFixture(ctx, conn, name); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			if populated {
				for i := 1; i <= 2; i++ {
					oldID := uuid.NewString()
					if _, err := conn.Exec(ctx, "INSERT INTO users(id,created_at) VALUES($1,$2)", oldID, now); err != nil {
						t.Fatal(err)
					}
					if _, err := conn.Exec(ctx, "INSERT INTO password_credentials(user_id,email,password_hash,updated_at) VALUES($1,$2,$3,$4)", oldID, fmt.Sprintf("old%d@example.com", i), fmt.Sprintf("hash%d", i), now); err != nil {
						t.Fatal(err)
					}
					if _, err := conn.Exec(ctx, "INSERT INTO profiles(user_id,display_name,nickname,version) VALUES($1,$2,$3,$4)", oldID, fmt.Sprintf("Name%d", i), fmt.Sprintf("Nick%d", i), i+1); err != nil {
						t.Fatal(err)
					}
					var budget *int64
					var access *bool
					if i == 1 {
						zero, no := int64(0), false
						budget, access = &zero, &no
					}
					if _, err := conn.Exec(ctx, "INSERT INTO preferences(user_id,likes,neutral,dislikes,max_budget_fen,requires_wheelchair_access,version) VALUES($1,$2,'一般','不喜欢',$3,$4,$5)", oldID, fmt.Sprintf("喜欢%d", i), budget, access, i+3); err != nil {
						t.Fatal(err)
					}
					digest := make([]byte, 32)
					digest[0] = byte(i)
					if _, err := conn.Exec(ctx, "INSERT INTO sessions(token_hash,user_id,created_at,expires_at) VALUES($1,$2,$3,$4)", digest, oldID, now, now.Add(time.Hour)); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := migrateFixture(ctx, conn, "00005_user_identity.sql"); err != nil {
				t.Fatal(err)
			}
			var maximum int64
			if populated {
				seen := map[int64]bool{}
				for i := 1; i <= 2; i++ {
					var id, profileVersion, preferenceVersion int64
					var hash, name, nickname, likes, neutral, dislikes string
					var budget *int64
					var access *bool
					var created, updated, sessionCreated, expires time.Time
					digest := make([]byte, 32)
					digest[0] = byte(i)
					err := conn.QueryRow(ctx, `SELECT u.id,u.created_at,c.password_hash,c.updated_at,
						p.display_name,p.nickname,p.version,f.likes,f.neutral,f.dislikes,
						f.max_budget_fen,f.requires_wheelchair_access,f.version,s.created_at,s.expires_at
						FROM users u JOIN password_credentials c ON c.user_id=u.id
						JOIN profiles p ON p.user_id=u.id JOIN preferences f ON f.user_id=u.id
						JOIN sessions s ON s.user_id=u.id WHERE c.email=$1 AND s.token_hash=$2`,
						fmt.Sprintf("old%d@example.com", i), digest).Scan(&id, &created, &hash, &updated, &name, &nickname, &profileVersion, &likes, &neutral, &dislikes, &budget, &access, &preferenceVersion, &sessionCreated, &expires)
					if err != nil {
						t.Fatal(err)
					}
					if id <= 0 || seen[id] || hash != fmt.Sprintf("hash%d", i) || name != fmt.Sprintf("Name%d", i) || nickname != fmt.Sprintf("Nick%d", i) || likes != fmt.Sprintf("喜欢%d", i) || neutral != "一般" || dislikes != "不喜欢" || profileVersion != int64(i+1) || preferenceVersion != int64(i+3) {
						t.Fatal("migration lost or mixed account data")
					}
					if !created.Equal(now) || updated.IsZero() || !sessionCreated.Equal(now) || !expires.Equal(now.Add(time.Hour)) {
						t.Fatal("migration changed account or session timestamps")
					}
					if i == 1 && (budget == nil || *budget != 0 || access == nil || *access) || i == 2 && (budget != nil || access != nil) {
						t.Fatal("migration lost nullable preference semantics")
					}
					seen[id] = true
					maximum = max(maximum, id)
					// Verify the current generated authentication query still resolves
					// a token issued before the UUID-to-integer migration.
					got, err := generated.New(conn).FindSessionUser(ctx, generated.FindSessionUserParams{TokenHash: digest, Now: timestamp(now)})
					if err != nil || got.ID != id {
						t.Fatalf("old session no longer authenticates: %#v %v", got, err)
					}
				}
			}

			q := generated.New(conn)
			account, err := q.CreateUser(ctx)
			if err != nil || account.ID <= maximum || account.CreatedAt.Time.IsZero() {
				t.Fatalf("INSERT RETURNING did not allocate a fresh identity: %#v %v", account, err)
			}
			// GENERATED ALWAYS must reject caller-chosen IDs, even valid integers.
			_, err = conn.Exec(ctx, "INSERT INTO users(id,created_at) VALUES(999999,now())")
			var identityErr *pgconn.PgError
			if !errors.As(err, &identityErr) || identityErr.Code != "428C9" {
				t.Fatalf("database did not enforce generated identity: %v", err)
			}
			for _, table := range []string{"password_credentials", "profiles", "preferences", "sessions"} {
				if populated {
					_, err := conn.Exec(ctx, "UPDATE "+table+" SET user_id=-1 WHERE user_id=$1", maximum)
					var pgErr *pgconn.PgError
					if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
						t.Fatalf("%s foreign key missing: %v", table, err)
					}
					_, err = conn.Exec(ctx, "INSERT INTO "+table+" SELECT * FROM "+table+" WHERE user_id=$1", maximum)
					if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
						t.Fatalf("%s primary key missing: %v", table, err)
					}
				}
			}
			// Identity sequences are not transactional: rollback leaves a gap,
			// and the next successful insert must not reuse the abandoned ID.
			tx, err := conn.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			abandoned, err := q.WithTx(tx).CreateUser(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			next, err := q.CreateUser(ctx)
			if err != nil || next.ID <= abandoned.ID || abandoned.ID <= account.ID {
				t.Fatalf("identity sequence reused an ID: %d %d %#v %v", account.ID, abandoned.ID, next, err)
			}
			if _, err := conn.Exec(ctx, "DELETE FROM users"); err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{"password_credentials", "profiles", "preferences", "sessions"} {
				var count int
				if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("%s did not cascade: %d %v", table, count, err)
				}
			}
		})
	}
}
