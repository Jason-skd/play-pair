package db

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/Jason-skd/play-pair/internal/group"
	"github.com/Jason-skd/play-pair/internal/outing"
	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/google/uuid"
)

func outingFixture(t *testing.T) (*DB, context.Context, []users.ID) {
	t.Helper()
	db, ctx, ids := groupFixture(t)
	data, err := os.ReadFile("../../db/migrations/00007_outings.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.pool.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	// Remove the new schema before the group's existing cleanup runs.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := db.pool.Exec(ctx, strings.Split(string(data), "-- +goose Down")[1]); err != nil {
			t.Error(err)
		}
	})
	return db, ctx, ids
}
func newOuting(t *testing.T, db *DB, ctx context.Context, actor users.ID, g group.ID) outing.Outing {
	t.Helper()
	o, created, err := outing.New(db, time.Now).Create(ctx, actor, outing.Create{GroupID: g, Key: uuid.New(), Dates: []outing.Date{"2030-10-03", "2030-10-05"}, Timezone: "Asia/Shanghai", DurationMinutes: 120})
	if err != nil || !created {
		t.Fatal(o, err)
	}
	return o
}
func outingInput() *outing.MemberInput {
	return &outing.MemberInput{MaxBudgetFen: 0, RequiresWheelchairAccess: false, Availability: []outing.Availability{{Date: "2030-10-03", StartMinute: 480, EndMinute: 600}, {Date: "2030-10-03", StartMinute: 570, EndMinute: 720}}}
}
func arrange(t *testing.T, db *DB, ctx context.Context, id outing.ID) {
	t.Helper()
	if _, err := db.pool.Exec(ctx, "INSERT INTO outing_arrangements(outing_id,starts_at,ends_at,place,activity) VALUES($1,clock_timestamp()+interval '1 day',clock_timestamp()+interval '1 day 2 hours','SCNU','Board games')", id); err != nil {
		t.Fatal(err)
	}
}
func archiveFixture(t *testing.T, db *DB, ctx context.Context, id outing.ID) {
	t.Helper()
	if _, err := db.pool.Exec(ctx, "ALTER TABLE outing_arrangements DISABLE TRIGGER outing_arrangement_guard; UPDATE outing_arrangements SET starts_at=clock_timestamp()-interval '1 second'; ALTER TABLE outing_arrangements ENABLE TRIGGER outing_arrangement_guard"); err != nil {
		t.Fatal(err)
	}
}
func TestOutingLifecycle(t *testing.T) {
	db, ctx, ids := outingFixture(t)
	g := mustCreateGroup(t, db, ctx, ids[0])
	s := outing.New(db, time.Now)
	if _, err := db.JoinGroup(ctx, ids[1], g.JoinCode); err != nil {
		t.Fatal(err)
	}
	// An ordinary member publishes; neither publisher nor group initiator autoaccepts.
	o := newOuting(t, db, ctx, ids[1], g.ID)
	if len(o.Invitations) != 1 || o.Invitations[0].Status != outing.Pending {
		t.Fatal(o)
	}
	p, err := s.Invitations(ctx, ids[0], o.ID, outing.Cursor{}, 50)
	if err != nil || len(p.Invitations) != 2 {
		t.Fatal(p, err)
	}
	if _, err = s.Get(ctx, ids[2], o.ID); !errors.Is(err, outing.ErrForbidden) {
		t.Fatal(err)
	}
	cmd := outing.Respond{OutingID: o.ID, Phase: outing.Decision, Status: outing.Accepted, ExpectedVersion: 1, Input: outingInput()}
	accepted, err := s.Respond(ctx, ids[1], cmd)
	if err != nil || accepted.AcceptedAt == nil || len(accepted.Input.Availability) != 1 {
		t.Fatal(accepted, err)
	}
	replay, err := s.Respond(ctx, ids[1], cmd)
	if err != nil || !reflect.DeepEqual(accepted, replay) {
		t.Fatal("replay", replay, err)
	}
	cmd.Input.MaxBudgetFen = 100
	if _, err = s.Respond(ctx, ids[1], cmd); !errors.Is(err, errs.VersionConflict) {
		t.Fatal(err)
	}
	cmd.ExpectedVersion = accepted.Version
	changed, err := s.Respond(ctx, ids[1], cmd)
	if err != nil || !changed.AcceptedAt.Equal(*accepted.AcceptedAt) || changed.Version <= accepted.Version {
		t.Fatal(changed, err)
	}
	declined, err := s.Respond(ctx, ids[1], outing.Respond{OutingID: o.ID, Phase: outing.Decision, Status: outing.Declined, ExpectedVersion: changed.Version})
	if err != nil || declined.Input != nil || declined.AcceptedAt != nil {
		t.Fatal(declined, err)
	}
	cmd.ExpectedVersion = declined.Version
	accepted, err = s.Respond(ctx, ids[1], cmd)
	if err != nil {
		t.Fatal(err)
	}
	// A first-round rejection must not carry into confirmation.
	if _, err = s.Respond(ctx, ids[0], outing.Respond{OutingID: o.ID, Phase: outing.Decision, Status: outing.Rejected, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	arrange(t, db, ctx, o.ID)
	got, err := s.Get(ctx, ids[1], o.ID)
	if err != nil || got.StatusAt(time.Now()) != outing.StatusConfirmation || len(got.Invitations) != 2 || got.Invitations[0].Input == nil || got.Invitations[1].Status != outing.Pending {
		t.Fatal(got, err)
	}
	cmd.ExpectedVersion = accepted.Version
	if _, err = s.Respond(ctx, ids[1], cmd); !errors.Is(err, outing.ErrPhaseConflict) {
		t.Fatal(err)
	}
	if _, err = db.JoinGroup(ctx, ids[2], g.JoinCode); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(ctx, ids[2], o.ID)
	if err != nil || len(got.Invitations) != 1 || got.Invitations[0].Phase != outing.Confirmation {
		t.Fatal(got, err)
	}
	confirm := outing.Respond{OutingID: o.ID, Phase: outing.Confirmation, Status: outing.Accepted, ExpectedVersion: 1}
	if _, err = s.Respond(ctx, ids[0], confirm); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Respond(ctx, ids[2], confirm); err != nil {
		t.Fatal(err)
	}
	p, err = s.Invitations(ctx, ids[2], o.ID, outing.Cursor{}, 1)
	if err != nil || p.Next == nil || len(p.Invitations) != 1 {
		t.Fatal(p, err)
	}
	p, err = s.Invitations(ctx, ids[2], o.ID, *p.Next, 100)
	if err != nil || len(p.Invitations) != 4 {
		t.Fatal(p, err)
	}
	for _, i := range p.Invitations {
		if i.Input != nil {
			t.Fatal("leaked input")
		}
	}
	archiveFixture(t, db, ctx, o.ID)
	got, err = s.Get(ctx, ids[2], o.ID)
	if err != nil || got.StatusAt(time.Now()) != outing.Archived {
		t.Fatal(got, err)
	}
	if _, err = s.Respond(ctx, ids[2], confirm); !errors.Is(err, outing.ErrArchived) {
		t.Fatal(err)
	}
}
func TestOutingCreationReplayAndJoin(t *testing.T) {
	db, ctx, ids := outingFixture(t)
	g := mustCreateGroup(t, db, ctx, ids[0])
	s := outing.New(db, time.Now)
	c := outing.Create{GroupID: g.ID, Key: uuid.New(), Dates: []outing.Date{"2030-10-05", "2030-10-03", "2030-10-03"}, Timezone: "Asia/Shanghai", DurationMinutes: 120}
	o, created, err := s.Create(ctx, ids[0], c)
	if err != nil || !created {
		t.Fatal(err)
	}
	replay, created, err := s.Create(ctx, ids[0], c)
	if err != nil || created || !reflect.DeepEqual(o, replay) {
		t.Fatal(replay, err)
	}
	c.DurationMinutes = 150
	if _, _, err = s.Create(ctx, ids[0], c); !errors.Is(err, outing.ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	if _, err = db.JoinGroup(ctx, ids[1], g.JoinCode); err != nil {
		t.Fatal(err)
	}
	p, err := s.Invitations(ctx, ids[1], 0, outing.Cursor{}, 50)
	if err != nil || len(p.Invitations) != 1 || p.Invitations[0].Phase != outing.Decision {
		t.Fatal(p, err)
	}
	if _, err = db.JoinGroup(ctx, ids[1], g.JoinCode); err != nil {
		t.Fatal(err)
	}
	again, err := s.Invitations(ctx, ids[1], 0, outing.Cursor{}, 50)
	if err != nil || !reflect.DeepEqual(p, again) {
		t.Fatal(again, err)
	}
	arrange(t, db, ctx, o.ID)
	archiveFixture(t, db, ctx, o.ID)
	if _, err = db.JoinGroup(ctx, ids[2], g.JoinCode); err != nil {
		t.Fatal(err)
	}
	p, err = s.Invitations(ctx, ids[2], 0, outing.Cursor{}, 50)
	if err != nil || len(p.Invitations) != 0 {
		t.Fatal(p, err)
	}
}
func TestOutingDatabaseIntegrity(t *testing.T) {
	db, ctx, ids := outingFixture(t)
	g := mustCreateGroup(t, db, ctx, ids[0])
	o := newOuting(t, db, ctx, ids[0], g.ID)
	checks := []string{
		"UPDATE outing_invitations SET status='accepted' WHERE outing_id=$1",
		"INSERT INTO outing_member_inputs(outing_id,member_id,max_budget_fen,requires_wheelchair_access) SELECT outing_id,member_id,0,false FROM outing_invitations WHERE outing_id=$1",
		"INSERT INTO outing_invitations(outing_id,member_id,phase) VALUES($1,999999,'decision')",
		"INSERT INTO outing_invitations(outing_id,member_id,phase) SELECT outing_id,member_id,'confirmation' FROM outing_invitations WHERE outing_id=$1",
		"DELETE FROM outing_dates WHERE outing_id=$1",
		"UPDATE outings SET timezone='UTC' WHERE id=$1",
	}
	for _, query := range checks {
		if _, err := db.pool.Exec(ctx, query, o.ID); err == nil {
			t.Fatal("accepted invalid SQL", query)
		}
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackGroup(tx)
	if _, err = tx.Exec(ctx, "INSERT INTO outings(group_id,initiator_id,timezone,duration_minutes,creation_key) VALUES($1,$2,'UTC',30,$3)", g.ID, ids[0], uuid.New()); err != nil {
		t.Fatal(err)
	}
	requirePGConstraint(t, tx.Commit(ctx), "23514", "outing_dates_complete")
	accepted, err := outing.New(db, time.Now).Respond(ctx, ids[0], outing.Respond{OutingID: o.ID, Phase: outing.Decision, Status: outing.Accepted, ExpectedVersion: 1, Input: outingInput()})
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"DELETE FROM outing_availability WHERE outing_id=$1", "UPDATE outing_availability SET end_minute=481 WHERE outing_id=$1", "UPDATE outing_availability SET date='2030-10-04' WHERE outing_id=$1", "UPDATE outing_member_inputs SET phase='confirmation' WHERE outing_id=$1"} {
		if _, err := db.pool.Exec(ctx, query, o.ID); err == nil {
			t.Fatal(query)
		}
	}
	got, err := outing.New(db, time.Now).Get(ctx, ids[0], o.ID)
	if err != nil || !reflect.DeepEqual(got.Invitations[0], accepted) {
		t.Fatal("failed write changed input", got, err)
	}
}

func TestOutingConcurrentCreationAndResponses(t *testing.T) {
	db, ctx, ids := outingFixture(t)
	g := mustCreateGroup(t, db, ctx, ids[0])
	s := outing.New(db, time.Now)
	c := outing.Create{GroupID: g.ID, Key: uuid.New(), Dates: []outing.Date{"2030-10-03"}, Timezone: "Asia/Shanghai", DurationMinutes: 120}
	type result struct {
		o       outing.Outing
		created bool
		err     error
	}
	results := make(chan result, 4)
	start := make(chan struct{})
	for range 4 {
		go func() { <-start; o, created, err := s.Create(ctx, ids[0], c); results <- result{o, created, err} }()
	}
	close(start)
	var o outing.Outing
	created := 0
	for range 4 {
		select {
		case r := <-results:
			if r.err != nil {
				t.Fatal(r.err)
			}
			if r.created {
				created++
			}
			if o.ID != 0 && o.ID != r.o.ID {
				t.Fatal("duplicate outing")
			}
			o = r.o
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if created != 1 {
		t.Fatal(created)
	}
	responseResults := make(chan error, 2)
	start = make(chan struct{})
	for _, budget := range []int64{100, 200} {
		go func() {
			<-start
			input := outingInput()
			input.MaxBudgetFen = budget
			_, err := s.Respond(ctx, ids[0], outing.Respond{OutingID: o.ID, Phase: outing.Decision, Status: outing.Accepted, ExpectedVersion: 1, Input: input})
			responseResults <- err
		}()
	}
	close(start)
	wins, conflicts := 0, 0
	for range 2 {
		select {
		case err := <-responseResults:
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
}
func TestOutingConcurrentJoinAndStageChange(t *testing.T) {
	db, ctx, ids := outingFixture(t)
	g := mustCreateGroup(t, db, ctx, ids[0])
	s := outing.New(db, time.Now)
	type result struct {
		o   outing.Outing
		err error
	}
	created := make(chan result, 1)
	joined := make(chan error, 1)
	start := make(chan struct{})
	go func() {
		<-start
		o, _, err := s.Create(ctx, ids[0], outing.Create{GroupID: g.ID, Key: uuid.New(), Dates: []outing.Date{"2030-10-03"}, Timezone: "UTC", DurationMinutes: 60})
		created <- result{o, err}
	}()
	go func() { <-start; _, err := db.JoinGroup(ctx, ids[1], g.JoinCode); joined <- err }()
	close(start)
	var o outing.Outing
	select {
	case r := <-created:
		if r.err != nil {
			t.Fatal(r.err)
		}
		o = r.o
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-joined:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	page, err := s.Invitations(ctx, ids[0], o.ID, outing.Cursor{}, 100)
	if err != nil || len(page.Invitations) != 2 {
		t.Fatal(page, err)
	}
	arranged := make(chan error, 1)
	start = make(chan struct{})
	go func() {
		<-start
		_, err := db.pool.Exec(ctx, "INSERT INTO outing_arrangements(outing_id,starts_at,ends_at,place,activity) VALUES($1,clock_timestamp()+interval '1 day',clock_timestamp()+interval '2 days','SCNU','Games')", o.ID)
		arranged <- err
	}()
	go func() { <-start; _, err := db.JoinGroup(ctx, ids[2], g.JoinCode); joined <- err }()
	close(start)
	for _, ch := range []chan error{arranged, joined} {
		select {
		case err := <-ch:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	got, err := s.Get(ctx, ids[2], o.ID)
	if err != nil {
		t.Fatal(err)
	}
	confirmation := 0
	for _, i := range got.Invitations {
		if i.Phase == outing.Confirmation {
			confirmation++
		}
	}
	if confirmation != 1 {
		t.Fatal(got)
	}
}

// Poll database-observable conditions with a deadline instead of guessing when
// a worker has started or when the database's clock has reached a boundary.
func awaitOutingCondition(t *testing.T, ctx context.Context, db *DB, query string, args ...any) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		var ready bool
		if err := db.pool.QueryRow(ctx, query, args...).Scan(&ready); err != nil {
			t.Fatal(err)
		}
		if ready {
			return
		}
		select {
		case <-tick.C:
		case <-timer.C:
			t.Fatal("condition deadline", query)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}
func TestOutingCutoffAfterLockAndCancellation(t *testing.T) {
	db, ctx, ids := outingFixture(t)
	g := mustCreateGroup(t, db, ctx, ids[0])
	o := newOuting(t, db, ctx, ids[0], g.ID)
	arrange(t, db, ctx, o.ID)
	// Schedule this isolated fixture near the boundary, without changing production clocks.
	if _, err := db.pool.Exec(ctx, "ALTER TABLE outing_arrangements DISABLE TRIGGER outing_arrangement_guard; UPDATE outing_arrangements SET starts_at=clock_timestamp()+interval '500 milliseconds'; ALTER TABLE outing_arrangements ENABLE TRIGGER outing_arrangement_guard"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackGroup(tx)
	if _, err = tx.Exec(ctx, "SELECT id FROM groups WHERE id=$1 FOR UPDATE", g.ID); err != nil {
		t.Fatal(err)
	}
	s := outing.New(db, time.Now)
	command := outing.Respond{OutingID: o.ID, Phase: outing.Confirmation, Status: outing.Accepted, ExpectedVersion: 1}
	done := make(chan error, 1)
	go func() { _, err := s.Respond(ctx, ids[0], command); done <- err }()
	awaitOutingCondition(t, ctx, db, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%LockOutingGroup%')")
	awaitOutingCondition(t, ctx, db, "SELECT clock_timestamp() >= starts_at FROM outing_arrangements WHERE outing_id=$1", o.ID)
	rollbackGroup(tx)
	select {
	case err := <-done:
		if !errors.Is(err, outing.ErrArchived) {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	got, err := s.Get(ctx, ids[0], o.ID)
	if err != nil || got.Invitations[1].Status != outing.Pending {
		t.Fatal(got, err)
	}
	o = newOuting(t, db, ctx, ids[0], g.ID)
	tx, err = db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackGroup(tx)
	if _, err = tx.Exec(ctx, "SELECT id FROM groups WHERE id=$1 FOR UPDATE", g.ID); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err = s.Respond(short, ids[0], outing.Respond{OutingID: o.ID, Phase: outing.Decision, Status: outing.Rejected, ExpectedVersion: 1}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	rollbackGroup(tx)
	got, err = s.Get(ctx, ids[0], o.ID)
	if err != nil || got.Invitations[0].Status != outing.Pending {
		t.Fatal(got, err)
	}
}
func TestOutingAtomicFailures(t *testing.T) {
	db, ctx, ids := outingFixture(t)
	g := mustCreateGroup(t, db, ctx, ids[0])
	s := outing.New(db, time.Now)
	if _, err := db.pool.Exec(ctx, `CREATE FUNCTION fail_outing_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected failure'; END $$;
 CREATE TRIGGER fail_outing_write AFTER INSERT ON outing_invitations FOR EACH ROW EXECUTE FUNCTION fail_outing_write()`); err != nil {
		t.Fatal(err)
	}
	key := uuid.New()
	if _, _, err := s.Create(ctx, ids[0], outing.Create{GroupID: g.ID, Key: key, Dates: []outing.Date{"2030-10-03"}, Timezone: "UTC", DurationMinutes: 60}); err == nil {
		t.Fatal("creation succeeded")
	}
	var n int
	if err := db.pool.QueryRow(ctx, "SELECT count(*) FROM outings WHERE creation_key=$1", key).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if _, err := db.pool.Exec(ctx, "DROP TRIGGER fail_outing_write ON outing_invitations"); err != nil {
		t.Fatal(err)
	}
	o := newOuting(t, db, ctx, ids[0], g.ID)
	if _, err := db.pool.Exec(ctx, "CREATE TRIGGER fail_outing_write AFTER INSERT ON outing_invitations FOR EACH ROW EXECUTE FUNCTION fail_outing_write()"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.JoinGroup(ctx, ids[1], g.JoinCode); err == nil {
		t.Fatal("join succeeded")
	}
	if err := db.pool.QueryRow(ctx, "SELECT count(*) FROM group_members WHERE group_id=$1 AND member_id=$2", g.ID, ids[1]).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if _, err := db.pool.Exec(ctx, "DROP TRIGGER fail_outing_write ON outing_invitations; CREATE CONSTRAINT TRIGGER fail_outing_write AFTER INSERT ON outing_availability DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_outing_write()"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Respond(ctx, ids[0], outing.Respond{OutingID: o.ID, Phase: outing.Decision, Status: outing.Accepted, ExpectedVersion: 1, Input: outingInput()}); err == nil {
		t.Fatal("commit failure succeeded")
	}
	got, err := s.Get(ctx, ids[0], o.ID)
	if err != nil || got.Invitations[0].Status != outing.Pending || got.Invitations[0].Version != 1 {
		t.Fatal(got, err)
	}
}
func TestOutingMigrationUpDownUp(t *testing.T) {
	db, ctx, ids := outingFixture(t)
	g := mustCreateGroup(t, db, ctx, ids[0])
	newOuting(t, db, ctx, ids[0], g.ID)
	data, err := os.ReadFile("../../db/migrations/00007_outings.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "-- +goose Down")
	if _, err = db.pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal(err)
	}
	got, err := group.New(db).Get(ctx, ids[0], g.ID)
	if err != nil || got.ID != g.ID {
		t.Fatal(got, err)
	}
	if _, err = db.pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	newOuting(t, db, ctx, ids[0], g.ID)
}
