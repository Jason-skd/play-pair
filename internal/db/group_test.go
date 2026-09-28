package db

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/credentials"
	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/Jason-skd/play-pair/internal/group"
	"github.com/Jason-skd/play-pair/internal/preference"
	"github.com/Jason-skd/play-pair/internal/profile"
	"github.com/Jason-skd/play-pair/internal/session"
	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Every test owns a schema in the explicitly configured independent test database.
// This also isolates fault-injection triggers and clock fixtures from other tests.
func groupFixture(t *testing.T) (*DB, context.Context, []users.ID) {
	t.Helper()
	c, ctx := migrationConnection(t)
	for _, file := range []string{"00002_auth.sql", "00003_account_modules.sql", "00004_preferences.sql", "00005_user_identity.sql", "00006_groups.sql"} {
		if err := migrateFixture(ctx, c, file); err != nil {
			t.Fatal(err)
		}
	}
	var schema string
	if err := c.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := Open(ctx, u.String(), 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	auth := credentials.New(db, session.New(db, time.Now), time.Now)
	ids := make([]users.ID, 3)
	for i := range ids {
		a, _, _, err := auth.Register(ctx, uuid.NewString()+"@example.com", "correct horse battery", "Initial")
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = a.ID
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tx, err := db.pool.Begin(cleanup)
		if err != nil {
			t.Error(err)
			return
		}
		defer rollbackGroup(tx)
		for _, sql := range []string{"DELETE FROM group_members", "DELETE FROM groups", "DELETE FROM users"} {
			if _, err := tx.Exec(cleanup, sql); err != nil {
				t.Error(err)
				return
			}
		}
		if err := tx.Commit(cleanup); err != nil {
			t.Error(err)
		}
	})
	return db, ctx, ids
}
func mustCreateGroup(t *testing.T, db *DB, ctx context.Context, id users.ID) group.Group {
	t.Helper()
	g, created, err := group.New(db).Create(ctx, id, uuid.New(), "周末")
	if err != nil || !created {
		t.Fatal(g, created, err)
	}
	return g
}
func requirePGConstraint(t *testing.T, err error, code, constraint string) {
	t.Helper()
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != code || pg.ConstraintName != constraint {
		t.Fatalf("expected %s %s, got %v", code, constraint, err)
	}
}
func TestGroupPersistenceAndConcurrentRequests(t *testing.T) {
	db, ctx, ids := groupFixture(t)
	s := group.New(db)
	key := uuid.New()
	type result struct {
		g       group.Group
		created bool
		err     error
	}
	results := make(chan result, 8)
	start := make(chan struct{})
	for range 8 {
		go func() {
			<-start
			g, created, err := s.Create(ctx, ids[0], key, " 周末 ")
			results <- result{g, created, err}
		}()
	}
	close(start)
	var g group.Group
	created := 0
	for range 8 {
		select {
		case r := <-results:
			if r.err != nil {
				t.Fatal(r.err)
			}
			if r.created {
				created++
			}
			if g.ID != 0 && g.ID != r.g.ID {
				t.Fatal("duplicate group")
			}
			g = r.g
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if created != 1 {
		t.Fatal("create count", created)
	}
	replay, isNew, err := s.Create(ctx, ids[0], key, "周末")
	if err != nil || isNew || replay != g {
		t.Fatal("replay changed group", replay, err)
	}
	if _, _, err = s.Create(ctx, ids[0], key, "different"); !errors.Is(err, group.ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	other, isNew, err := s.Create(ctx, ids[1], key, "周末")
	if err != nil || !isNew || other.ID == g.ID {
		t.Fatal("keys not actor scoped", err)
	}
	another := mustCreateGroup(t, db, ctx, ids[0])
	if another.ID == g.ID {
		t.Fatal("name treated as unique")
	}
	for _, actor := range ids[1:] {
		if _, err := s.Get(ctx, actor, g.ID); !errors.Is(err, group.ErrForbidden) {
			t.Fatal(err)
		}
		if _, err := s.Members(ctx, actor, g.ID, 0, 50); !errors.Is(err, group.ErrForbidden) {
			t.Fatal(err)
		}
	}
	if _, err := s.Get(ctx, ids[0], 999999); !errors.Is(err, errs.NotFound) {
		t.Fatal(err)
	}
	if _, err := s.Join(ctx, ids[1], strings.Repeat("Z", 16)); !errors.Is(err, errs.NotFound) {
		t.Fatal(err)
	}
	start = make(chan struct{})
	joins := make(chan error, 8)
	for i := range 8 {
		actor := ids[1+i%2]
		go func() { <-start; _, err := s.Join(ctx, actor, strings.ToLower(g.JoinCode)); joins <- err }()
	}
	close(start)
	for range 8 {
		select {
		case err := <-joins:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	full, err := s.Members(ctx, ids[1], g.ID, 0, 50)
	if err != nil || len(full.Members) != 3 || full.NextAfterMemberID != nil {
		t.Fatal(full, err)
	}
	for i, m := range full.Members {
		if m.MemberID != ids[i] || m.Role != group.Role(m.MemberID, ids[0]) {
			t.Fatal(m)
		}
	}
	latest, err := s.Get(ctx, ids[1], g.ID)
	if err != nil || latest.JoinCode != g.JoinCode {
		t.Fatal(latest, err)
	}
	for _, id := range ids {
		again, err := s.Join(ctx, id, g.JoinCode)
		if err != nil || again != latest {
			t.Fatal("duplicate touched group", again, err)
		}
	}
	again, err := s.Members(ctx, ids[0], g.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range again.Members {
		if !m.JoinedAt.Equal(full.Members[i].JoinedAt) {
			t.Fatal("duplicate changed joined_at")
		}
	}
	p, err := s.Members(ctx, ids[0], g.ID, 0, 1)
	if err != nil || len(p.Members) != 1 || p.NextAfterMemberID == nil || *p.NextAfterMemberID != ids[0] {
		t.Fatal(p, err)
	}
	p, err = s.Members(ctx, ids[0], g.ID, *p.NextAfterMemberID, 2)
	if err != nil || len(p.Members) != 2 || p.NextAfterMemberID != nil || p.Members[0].MemberID != ids[1] {
		t.Fatal(p, err)
	}
	p, err = s.Members(ctx, ids[0], g.ID, ids[2], 50)
	if err != nil || len(p.Members) != 0 || p.NextAfterMemberID != nil {
		t.Fatal(p, err)
	}
	nick := "昵称"
	if _, err := profile.New(db).Replace(ctx, ids[1], profile.Update{DisplayName: "Changed", Nickname: &nick, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	p, err = s.Members(ctx, ids[0], g.ID, ids[0], 1)
	if err != nil || p.Members[0].DisplayName != "Changed" || p.Members[0].Nickname == nil || *p.Members[0].Nickname != nick {
		t.Fatal(p, err)
	}
	if _, err := preference.New(db).Replace(ctx, ids[1], preference.Update{Soft: preference.Soft{Likes: "Changed"}, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	after, err := s.Get(ctx, ids[0], g.ID)
	if err != nil || after != latest {
		t.Fatal("profile touched group", err)
	}
}
func TestGroupCodeCollisionsAndAtomicFailures(t *testing.T) {
	db, ctx, ids := groupFixture(t)
	fixed := bytes.Repeat([]byte{0}, 10)
	g, _, err := db.createGroup(ctx, ids[0], uuid.New(), "First", bytes.NewReader(fixed))
	if err != nil {
		t.Fatal(err)
	}
	random := bytes.NewReader(append(append([]byte{}, fixed...), bytes.Repeat([]byte{1}, 10)...))
	second, created, err := db.createGroup(ctx, ids[0], uuid.New(), "Second", random)
	if err != nil || !created || second.JoinCode == g.JoinCode || random.Len() != 0 {
		t.Fatal(second, err, random.Len())
	}
	exhausted := bytes.NewReader(bytes.Repeat([]byte{0}, 60))
	if _, _, err := db.createGroup(ctx, ids[0], uuid.New(), "Exhausted", exhausted); !errors.Is(err, errs.DependencyUnavailable) || exhausted.Len() != 10 {
		t.Fatal("retry bound", err, exhausted.Len())
	}
	// An invalid name is not a join-code collision and must consume only one draw.
	invalid := bytes.NewReader(bytes.Repeat([]byte{2}, 50))
	_, _, err = db.createGroup(ctx, ids[0], uuid.New(), "", invalid)
	requirePGConstraint(t, err, "23514", "groups_name_check")
	if invalid.Len() != 40 {
		t.Fatal("noncollision retried")
	}
	if _, _, err := db.createGroup(ctx, ids[0], uuid.New(), "No entropy", bytes.NewReader(nil)); !errors.Is(err, errs.DependencyUnavailable) {
		t.Fatal(err)
	}
	// Failure at member insertion rolls back the group; commit failure does too.
	for _, deferred := range []bool{false, true} {
		trigger := "CREATE TRIGGER fail_member BEFORE INSERT ON group_members FOR EACH ROW EXECUTE FUNCTION fail_member()"
		if deferred {
			trigger = "CREATE CONSTRAINT TRIGGER fail_member AFTER INSERT ON group_members DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_member()"
		}
		if _, err := db.pool.Exec(ctx, `CREATE FUNCTION fail_member() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected failure'; END $$;`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.pool.Exec(ctx, trigger); err != nil {
			t.Fatal(err)
		}
		key := uuid.New()
		if _, _, err := db.CreateGroup(ctx, ids[0], key, "Failure"); err == nil {
			t.Fatal("failed transaction reported success")
		}
		var count int
		if err := db.pool.QueryRow(ctx, "SELECT count(*) FROM groups WHERE creation_key=$1", key).Scan(&count); err != nil || count != 0 {
			t.Fatal("partial group", count, err)
		}
		if _, err := db.pool.Exec(ctx, "DROP TRIGGER fail_member ON group_members; DROP FUNCTION fail_member()"); err != nil {
			t.Fatal(err)
		}
	}
}
func TestGroupDatabaseConstraints(t *testing.T) {
	db, ctx, ids := groupFixture(t)
	g := mustCreateGroup(t, db, ctx, ids[0])
	other := mustCreateGroup(t, db, ctx, ids[1])
	checks := []struct {
		sql, code, name string
		args            []any
	}{
		{"INSERT INTO group_members(group_id,member_id) VALUES($1,$2)", "23505", "group_members_pkey", []any{g.ID, ids[0]}},
		{"INSERT INTO group_members(group_id,member_id) VALUES($1,999999)", "23503", "group_members_user_fkey", []any{g.ID}},
		{"INSERT INTO group_members(group_id,member_id) VALUES(999999,$1)", "23503", "group_members_group_fkey", []any{ids[0]}},
		{"UPDATE groups SET join_code=$1 WHERE id=$2", "23505", "groups_join_code_key", []any{g.JoinCode, other.ID}},
		{"UPDATE groups SET join_code='bad' WHERE id=$1", "23514", "groups_join_code_check", []any{g.ID}},
		{"UPDATE groups SET name='' WHERE id=$1", "23514", "groups_name_check", []any{g.ID}},
		{"UPDATE groups SET name=$1 WHERE id=$2", "23514", "groups_name_check", []any{strings.Repeat("中", 81), g.ID}},
		{"UPDATE groups SET name=$1 WHERE id=$2", "23514", "groups_name_check", []any{"\u3000space", g.ID}},
		{"INSERT INTO groups(id,name,join_code,initiator_id,creation_key) OVERRIDING SYSTEM VALUE VALUES(-1,'bad','AAAAAAAAAAAAAAAA',$1,$2)", "23514", "groups_id_positive", []any{ids[0], uuid.New()}},
	}
	for _, c := range checks {
		_, err := db.pool.Exec(ctx, c.sql, c.args...)
		requirePGConstraint(t, err, c.code, c.name)
	}
	// The reverse composite FK rejects a missing initiator even if that user belongs to another group.
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackGroup(tx)
	if _, err := tx.Exec(ctx, "INSERT INTO groups(name,join_code,initiator_id,creation_key) VALUES('Incomplete','AAAAAAAAAAAAAAAA',$1,$2)", ids[0], uuid.New()); err != nil {
		t.Fatal("reverse FK was not deferred", err)
	}
	requirePGConstraint(t, tx.Commit(ctx), "23503", "groups_initiator_member_fkey")
	for _, sql := range []string{"DELETE FROM group_members WHERE group_id=$1 AND member_id=$2", "UPDATE group_members SET member_id=$3 WHERE group_id=$1 AND member_id=$2", "UPDATE group_members SET group_id=$3 WHERE group_id=$1 AND member_id=$2"} {
		tx, err := db.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		args := []any{g.ID, ids[0]}
		if strings.Contains(sql, "$3") {
			if strings.Contains(sql, "SET group_id") {
				args = append(args, other.ID)
			} else {
				args = append(args, ids[2])
			}
		}
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			rollbackGroup(tx)
			t.Fatal(err)
		}
		requirePGConstraint(t, tx.Commit(ctx), "23503", "groups_initiator_member_fkey")
	}
}
func TestGroupCancellationAndLockTimeout(t *testing.T) {
	db, ctx, ids := groupFixture(t)
	g := mustCreateGroup(t, db, ctx, ids[0])
	s := group.New(db)
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackGroup(tx)
	if _, err := tx.Exec(ctx, "SELECT id FROM groups WHERE id=$1 FOR UPDATE", g.ID); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := s.Join(short, ids[1], g.JoinCode); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("lock wait succeeded", err)
	}
	rollbackGroup(tx)
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, _, err := s.Create(canceled, ids[0], uuid.New(), "Canceled"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.Join(canceled, ids[1], g.JoinCode); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	p, err := s.Members(ctx, ids[0], g.ID, 0, 50)
	if err != nil || len(p.Members) != 1 {
		t.Fatal("canceled join persisted", p, err)
	}
}

// controlledGroupTimes modifies only this test's isolated schema. Production
// stamps cannot be used to backdate rows; disabling the trigger is fixture setup.
func controlledGroupTimes(t *testing.T, db *DB, ctx context.Context) {
	t.Helper()
	_, err := db.pool.Exec(ctx, "ALTER TABLE groups DISABLE TRIGGER groups_stamp; UPDATE groups SET updated_at='2000-01-01'; ALTER TABLE groups ENABLE TRIGGER groups_stamp")
	if err != nil {
		t.Fatal(err)
	}
}
func groupTime(t *testing.T, db *DB, ctx context.Context, id group.ID) time.Time {
	t.Helper()
	var v time.Time
	if err := db.pool.QueryRow(ctx, "SELECT updated_at FROM groups WHERE id=$1", id).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}
func TestGroupTimestamps(t *testing.T) {
	db, ctx, ids := groupFixture(t)
	g := mustCreateGroup(t, db, ctx, ids[0])
	other := mustCreateGroup(t, db, ctx, ids[1])
	if g.UpdatedAt.Before(g.CreatedAt) {
		t.Fatal("time moved backwards")
	}
	controlledGroupTimes(t, db, ctx)
	old := groupTime(t, db, ctx, g.ID)
	if _, err := db.JoinGroup(ctx, ids[2], g.JoinCode); err != nil {
		t.Fatal(err)
	}
	if !groupTime(t, db, ctx, g.ID).After(old) {
		t.Fatal("insert did not touch")
	}
	controlledGroupTimes(t, db, ctx)
	for _, sql := range []string{"UPDATE groups SET name=name WHERE id=$1", "UPDATE group_members SET joined_at=joined_at WHERE group_id=$1"} {
		if _, err := db.pool.Exec(ctx, sql, g.ID); err != nil {
			t.Fatal(err)
		}
	}
	if !groupTime(t, db, ctx, g.ID).Equal(old) {
		t.Fatal("no-op touched group")
	}
	if _, err := db.JoinGroup(ctx, ids[2], g.JoinCode); err != nil {
		t.Fatal(err)
	}
	if !groupTime(t, db, ctx, g.ID).Equal(old) {
		t.Fatal("duplicate join touched group")
	}
	if _, err := db.pool.Exec(ctx, "UPDATE group_members SET joined_at=joined_at + interval '1 second' WHERE group_id=$1 AND member_id=$2", g.ID, ids[2]); err != nil {
		t.Fatal(err)
	}
	if !groupTime(t, db, ctx, g.ID).After(old) {
		t.Fatal("update did not touch")
	}
	controlledGroupTimes(t, db, ctx)
	if _, err := db.pool.Exec(ctx, "UPDATE group_members SET group_id=$1 WHERE group_id=$2 AND member_id=$3", other.ID, g.ID, ids[2]); err != nil {
		t.Fatal(err)
	}
	if !groupTime(t, db, ctx, g.ID).After(old) || !groupTime(t, db, ctx, other.ID).After(old) {
		t.Fatal("move must touch both groups")
	}
	controlledGroupTimes(t, db, ctx)
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM group_members WHERE group_id=$1 AND member_id=$2", other.ID, ids[2]); err != nil {
		rollbackGroup(tx)
		t.Fatal(err)
	}
	rollbackGroup(tx)
	if !groupTime(t, db, ctx, other.ID).Equal(old) {
		t.Fatal("rollback retained time change")
	}
	if _, err := db.pool.Exec(ctx, "DELETE FROM group_members WHERE group_id=$1 AND member_id=$2", other.ID, ids[2]); err != nil {
		t.Fatal(err)
	}
	if !groupTime(t, db, ctx, other.ID).After(old) {
		t.Fatal("delete did not touch")
	}
	controlledGroupTimes(t, db, ctx)
	if _, err := db.pool.Exec(ctx, "UPDATE groups SET name='New' WHERE id=$1", g.ID); err != nil {
		t.Fatal(err)
	}
	if !groupTime(t, db, ctx, g.ID).After(old) {
		t.Fatal("group change did not touch")
	}
	// Clock regressions cannot decrease a previously stored value.
	if _, err := db.pool.Exec(ctx, "ALTER TABLE groups DISABLE TRIGGER groups_stamp; UPDATE groups SET updated_at='2100-01-01'; ALTER TABLE groups ENABLE TRIGGER groups_stamp"); err != nil {
		t.Fatal(err)
	}
	future := groupTime(t, db, ctx, g.ID)
	if _, err := db.JoinGroup(ctx, ids[2], g.JoinCode); err != nil {
		t.Fatal(err)
	}
	if !groupTime(t, db, ctx, g.ID).Equal(future) {
		t.Fatal("time moved backwards")
	}
}

func TestGroupMigrationUpDownUpPreservesAccounts(t *testing.T) {
	c, ctx := migrationConnection(t)
	for _, file := range []string{"00002_auth.sql", "00003_account_modules.sql", "00004_preferences.sql", "00005_user_identity.sql"} {
		if err := migrateFixture(ctx, c, file); err != nil {
			t.Fatal(err)
		}
	}
	_, err := c.Exec(ctx, `INSERT INTO users(created_at) VALUES('2000-01-01');
 INSERT INTO password_credentials(user_id,email,password_hash,updated_at) VALUES(1,'old@example.com','retained-hash','2000-01-01');
 INSERT INTO profiles(user_id,display_name,nickname,version) VALUES(1,'Name','Nick',3);
 INSERT INTO preferences(user_id) VALUES(1);
 INSERT INTO sessions(token_hash,user_id,created_at,expires_at) VALUES(decode(repeat('ab',32),'hex'),1,'2000-01-01','2100-01-01');`)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var v string
		if err := c.QueryRow(ctx, `SELECT jsonb_build_array((SELECT jsonb_agg(u) FROM users u),(SELECT jsonb_agg(c) FROM password_credentials c),(SELECT jsonb_agg(p) FROM profiles p),(SELECT jsonb_agg(p) FROM preferences p),(SELECT jsonb_agg(s) FROM sessions s))::text`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := snapshot()
	if err := migrateFixture(ctx, c, "00006_groups.sql"); err != nil {
		t.Fatal(err)
	}
	tx, err := c.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO groups(name,join_code,initiator_id,creation_key) VALUES('Group','AAAAAAAAAAAAAAAA',1,'00000000-0000-0000-0000-000000000001'); INSERT INTO group_members(group_id,member_id) VALUES(1,1)`); err != nil {
		rollbackGroup(tx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../db/migrations/00006_groups.sql")
	if err != nil {
		t.Fatal(err)
	}
	down := strings.Split(string(data), "-- +goose Down")[1]
	if _, err := c.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if snapshot() != before {
		t.Fatal("down changed account modules")
	}
	if err := migrateFixture(ctx, c, "00006_groups.sql"); err != nil {
		t.Fatal(err)
	}
	if snapshot() != before {
		t.Fatal("up changed account modules")
	}
	var count int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM groups").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestGroupConcurrentConflictingCreation(t *testing.T) {
	db, ctx, ids := groupFixture(t)
	key := uuid.New()
	s := group.New(db)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, name := range []string{"First", "Second"} {
		go func() { <-start; _, _, err := s.Create(ctx, ids[0], key, name); results <- err }()
	}
	close(start)
	wins, conflicts := 0, 0
	for range 2 {
		select {
		case err := <-results:
			switch {
			case err == nil:
				wins++
			case errors.Is(err, group.ErrIdempotencyConflict):
				conflicts++
			default:
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal(wins, conflicts)
	}
}

type callbackReader func([]byte) (int, error)

func (f callbackReader) Read(b []byte) (int, error) { return f(b) }

func TestGroupCollisionRechecksIdempotency(t *testing.T) {
	db, ctx, ids := groupFixture(t)
	// Seed the colliding code, then let an idempotent winner commit after the
	// loser's initial lookup but before its INSERT, using a request-local reader.
	fixed := bytes.Repeat([]byte{0}, 10)
	if _, _, err := db.createGroup(ctx, ids[0], uuid.New(), "Seed", bytes.NewReader(fixed)); err != nil {
		t.Fatal(err)
	}
	key := uuid.New()
	draws := 0
	var winner group.Group
	reader := callbackReader(func(b []byte) (int, error) {
		draws++
		g, _, err := db.createGroup(ctx, ids[0], key, "Same", bytes.NewReader(bytes.Repeat([]byte{1}, 10)))
		winner = g
		if err != nil {
			return 0, err
		}
		return copy(b, fixed), nil
	})
	g, created, err := db.createGroup(ctx, ids[0], key, "Same", reader)
	if err != nil || created || draws != 1 || g != winner {
		t.Fatal("lost idempotent winner", g, created, draws, err)
	}
}
