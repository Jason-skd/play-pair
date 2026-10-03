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
)

func outingFixture(t *testing.T) (*DB, context.Context, []users.ID, *outing.Service, group.Group, outing.Outing) {
	t.Helper()
	db, ctx, ids := groupFixture(t)
	data, err := os.ReadFile("../../db/migrations/00007_outings.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := db.pool.Exec(cleanup, "DELETE FROM outings"); err != nil {
			t.Error(err)
		}
	})
	g := mustCreateGroup(t, db, ctx, ids[0])
	for _, id := range ids[1:] {
		if _, err := group.New(db).Join(ctx, id, g.JoinCode); err != nil {
			t.Fatal(err)
		}
	}
	s := outing.New(db, func() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) })
	// The outing organizer is deliberately not the group's initiator.
	o, err := s.Create(ctx, ids[1], int64(g.ID), "Asia/Shanghai", outing.Plan{Dates: []outing.Date{"2026-10-10"}, DurationMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	return db, ctx, ids, s, g, o
}

func outingInput() *outing.MemberInput {
	return &outing.MemberInput{MaxBudgetFen: 0, RequiresWheelchairAccess: false, Availability: []outing.Availability{{Date: "2026-10-10", StartMinute: 600, EndMinute: 720}}}
}

func outingArrangement() outing.Arrangement {
	start := time.Date(2026, 10, 10, 2, 0, 0, 0, time.UTC)
	return outing.Arrangement{StartsAt: start, EndsAt: start.Add(time.Hour), Place: "Park", Activity: "Walk"}
}

func TestOutingResponsesWarningsAndAccess(t *testing.T) {
	db, ctx, ids, s, g, o := outingFixture(t)
	if err := s.Respond(ctx, ids[2], o.ID, outing.Decision, outing.Accept, outingInput()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, ids[0], o.ID, ids[2]); !errors.Is(err, outing.ErrForbidden) {
		t.Fatal("private input exposed", err)
	}
	d, err := s.Get(ctx, ids[1], o.ID, ids[2])
	if err != nil || d.Member.Input == nil || len(d.Warnings) != 0 {
		t.Fatal(d, err)
	}
	plan := outing.Plan{Dates: []outing.Date{"2026-10-11"}, DurationMinutes: 150}
	if err := s.Update(ctx, ids[2], o.ID, plan); !errors.Is(err, outing.ErrForbidden) {
		t.Fatal(err)
	}
	if err := s.Update(ctx, ids[1], o.ID, plan); err != nil {
		t.Fatal(err)
	}
	d, err = s.Get(ctx, ids[2], o.ID, ids[2])
	if err != nil || len(d.Warnings) != 2 || len(d.Member.Input.Availability) != 1 || d.Member.Input.Availability[0].Date != "2026-10-10" {
		t.Fatal(d, err)
	}
	if err := s.Respond(ctx, ids[2], o.ID, outing.Decision, outing.Accept, outingInput()); !errors.Is(err, outing.ErrInvalidAvailability) {
		t.Fatal(err)
	}
	// Removing an unresponded member loses access; decision respondents and the
	// organizer retain access and management independent of group membership.
	if _, err := db.pool.Exec(ctx, "DELETE FROM group_members WHERE group_id=$1 AND member_id IN ($2,$3)", g.ID, ids[1], ids[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, ids[2], o.ID, ids[2]); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(ctx, ids[1], o.ID, o.Plan); err != nil {
		t.Fatal("organizer lost management", err)
	}
	if err := s.Respond(ctx, ids[2], o.ID, outing.Decision, outing.Deny, nil); err != nil {
		t.Fatal(err)
	}
	d, err = s.Get(ctx, ids[2], o.ID, ids[2])
	if err != nil || d.Member.Input != nil || *d.Member.Decision != outing.Deny || len(d.Warnings) != 0 {
		t.Fatal(d, err)
	}
	if err := s.Respond(ctx, ids[2], o.ID, outing.Decision, outing.Accept, outingInput()); err != nil {
		t.Fatal(err)
	}
	// Another group supplies a member who has never responded to this outing.
	other := mustCreateGroup(t, db, ctx, ids[2])
	otherOuting, err := s.Create(ctx, ids[2], int64(other.ID), "UTC", o.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, ids[0], otherOuting.ID, ids[0]); !errors.Is(err, outing.ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, ids[0], int64(other.ID), "UTC", o.Plan); !errors.Is(err, outing.ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, ids[0], 999999, ids[0]); !errors.Is(err, errs.NotFound) {
		t.Fatal(err)
	}
}

func TestArrangementResetKeepsConfirmationOnlyMember(t *testing.T) {
	db, ctx, ids, s, g, o := outingFixture(t)
	if err := s.Respond(ctx, ids[0], o.ID, outing.Decision, outing.Accept, outingInput()); err != nil {
		t.Fatal(err)
	}
	a := outingArrangement()
	if err := s.Arrange(ctx, ids[1], o.ID, a, false, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Respond(ctx, ids[0], o.ID, outing.Decision, outing.Deny, nil); !errors.Is(err, outing.ErrPhase) {
		t.Fatal(err)
	}
	if err := s.Respond(ctx, ids[2], o.ID, outing.Confirmation, outing.Accept, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, "DELETE FROM group_members WHERE group_id=$1 AND member_id=$2", g.ID, ids[2]); err != nil {
		t.Fatal(err)
	}
	a.Place = "Another park"
	if err := s.Arrange(ctx, ids[1], o.ID, a, true, false); err != nil {
		t.Fatal(err)
	}
	d, err := s.Get(ctx, ids[2], o.ID, ids[2])
	if err != nil || *d.Member.Confirmation != outing.Accept {
		t.Fatal(d, err)
	}
	if err := s.Arrange(ctx, ids[1], o.ID, a, true, true); err != nil {
		t.Fatal(err)
	}
	d, err = s.Get(ctx, ids[2], o.ID, ids[2])
	if err != nil || d.Member.Confirmation == nil || *d.Member.Confirmation != outing.Pending {
		t.Fatal(d, err)
	}
	page, err := s.List(ctx, ids[2], outing.ListFilter{Limit: 1})
	if err != nil || len(page.Outings) != 1 || page.Outings[0].ID != o.ID {
		t.Fatal(page, err)
	}
	if err := s.Respond(ctx, ids[2], o.ID, outing.Confirmation, outing.Deny, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(ctx, ids[1], o.ID, outing.Plan{Dates: []outing.Date{"2026-10-11"}, DurationMinutes: 150}); err != nil {
		t.Fatal(err)
	}
	d, err = s.Get(ctx, ids[0], o.ID, ids[0])
	if err != nil || d.Member.Input == nil || len(d.Warnings) != 0 {
		t.Fatal("confirmation hid first-round input or reported warning", d, err)
	}
	started := outing.New(db, func() time.Time { return a.StartsAt })
	for _, err := range []error{
		started.Update(ctx, ids[1], o.ID, o.Plan),
		started.Arrange(ctx, ids[1], o.ID, outingArrangement(), true, false),
		started.Respond(ctx, ids[2], o.ID, outing.Confirmation, outing.Accept, nil),
	} {
		if !errors.Is(err, outing.ErrArchived) {
			t.Fatal("start restriction bypassed", err)
		}
	}
	if _, err := started.Get(ctx, ids[2], o.ID, ids[2]); err != nil {
		t.Fatal("archive unreadable", err)
	}
}

func TestOutingPendingFilteringAndPagination(t *testing.T) {
	db, ctx, ids, s, g, o := outingFixture(t)
	page, err := s.Responses(ctx, ids[0], o.ID, outing.ResponseFilter{Status: outing.Pending, Limit: 1})
	if err != nil || len(page.Responses) != 1 || page.Next == nil || page.Responses[0].MemberID != ids[0] {
		t.Fatal(page, err)
	}
	page, err = s.Responses(ctx, ids[0], o.ID, outing.ResponseFilter{Status: outing.Pending, After: *page.Next, Limit: 10})
	if err != nil || len(page.Responses) != 2 || page.Next != nil {
		t.Fatal(page, err)
	}
	if err := s.Respond(ctx, ids[1], o.ID, outing.Decision, outing.Deny, nil); err != nil {
		t.Fatal(err)
	}
	// A never-responded member leaves; no persistent pending row is needed.
	if _, err := db.pool.Exec(ctx, "DELETE FROM group_members WHERE group_id=$1 AND member_id=$2", g.ID, ids[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, ids[2], o.ID, ids[2]); !errors.Is(err, outing.ErrForbidden) {
		t.Fatal(err)
	}
	page, err = s.Responses(ctx, ids[0], o.ID, outing.ResponseFilter{Status: outing.Pending, Limit: 10})
	if err != nil || len(page.Responses) != 1 || page.Responses[0].MemberID != ids[0] {
		t.Fatal(page, err)
	}
	if err := s.Arrange(ctx, ids[1], o.ID, outingArrangement(), false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := group.New(db).Join(ctx, ids[2], g.JoinCode); err != nil {
		t.Fatal(err)
	}
	want := []outing.Summary{{MemberID: ids[0], Phase: outing.Confirmation, Status: outing.Pending}, {MemberID: ids[1], Phase: outing.Confirmation, Status: outing.Pending}, {MemberID: ids[1], Phase: outing.Decision, Status: outing.Deny}, {MemberID: ids[2], Phase: outing.Confirmation, Status: outing.Pending}}
	got := []outing.Summary{}
	var cursor outing.ResponseCursor
	for range 6 {
		page, err = s.Responses(ctx, ids[0], o.ID, outing.ResponseFilter{After: cursor, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, page.Responses...)
		if page.Next == nil {
			break
		}
		cursor = *page.Next
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
	page, err = s.Responses(ctx, ids[0], o.ID, outing.ResponseFilter{Phase: outing.Decision, Status: outing.Pending, Limit: 10})
	if err != nil || len(page.Responses) != 0 {
		t.Fatal("past round synthesized pending", page, err)
	}
	second, err := s.Create(ctx, ids[1], int64(g.ID), "UTC", o.Plan)
	if err != nil {
		t.Fatal(err)
	}
	outings, err := s.List(ctx, ids[0], outing.ListFilter{GroupID: int64(g.ID), Limit: 1})
	if err != nil || outings.Next == nil || outings.Outings[0].ID != o.ID {
		t.Fatal(outings, err)
	}
	outings, err = s.List(ctx, ids[0], outing.ListFilter{GroupID: int64(g.ID), After: *outings.Next, Limit: 1})
	if err != nil || outings.Next != nil || outings.Outings[0].ID != second.ID {
		t.Fatal(outings, err)
	}
}

func TestOutingDatabaseCompletenessAndOwnership(t *testing.T) {
	db, ctx, ids, s, _, o := outingFixture(t)
	_, err := db.pool.Exec(ctx, "INSERT INTO outing_decisions(outing_id,member_id,choice,max_budget_fen,requires_wheelchair_access) VALUES($1,$2,'accept',0,false)", o.ID, ids[2])
	requirePGConstraint(t, err, "23514", "outing_input_complete")
	if err := s.Respond(ctx, ids[1], o.ID, outing.Decision, outing.Deny, nil); err != nil {
		t.Fatal(err)
	}
	_, err = db.pool.Exec(ctx, "INSERT INTO outing_availability(outing_id,member_id,date,start_minute,end_minute) VALUES($1,$2,'2026-10-10',600,660)", o.ID, ids[1])
	requirePGConstraint(t, err, "23514", "outing_input_complete")
	if err := s.Respond(ctx, ids[2], o.ID, outing.Decision, outing.Accept, outingInput()); err != nil {
		t.Fatal(err)
	}
	_, err = db.pool.Exec(ctx, "DELETE FROM outing_availability WHERE outing_id=$1 AND member_id=$2", o.ID, ids[2])
	requirePGConstraint(t, err, "23514", "outing_input_complete")
	_, err = db.pool.Exec(ctx, "INSERT INTO outing_availability SELECT $1,$2,DATE '2026-10-11'+i,0,30 FROM generate_series(1,256) i", o.ID, ids[2])
	requirePGConstraint(t, err, "23514", "outing_input_complete")
	// Moving the last interval must validate its old owner, too.
	if err := s.Respond(ctx, ids[0], o.ID, outing.Decision, outing.Accept, outingInput()); err != nil {
		t.Fatal(err)
	}
	_, err = db.pool.Exec(ctx, "UPDATE outing_availability SET member_id=$2,start_minute=750,end_minute=810 WHERE outing_id=$1 AND member_id=$3", o.ID, ids[0], ids[2])
	requirePGConstraint(t, err, "23514", "outing_input_complete")
	_, err = db.pool.Exec(ctx, "INSERT INTO outing_availability SELECT outing_id,member_id,date+1,0,30 FROM outing_availability WHERE outing_id=$1 AND member_id=$2", o.ID, ids[2])
	if err != nil {
		t.Fatal("stale candidate dates rejected", err)
	}
	// No stage trigger: direct SQL may correct input after arrangement creation.
	if err := s.Arrange(ctx, ids[1], o.ID, outingArrangement(), false, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Respond(ctx, ids[2], o.ID, outing.Confirmation, outing.Accept, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, "UPDATE outing_decisions SET max_budget_fen=10 WHERE outing_id=$1 AND member_id=$2", o.ID, ids[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, "DELETE FROM outing_arrangements WHERE outing_id=$1", o.ID); err != nil {
		t.Fatal(err)
	}
	d, err := s.Get(ctx, ids[2], o.ID, ids[2])
	if err != nil || d.Outing.Arrangement != nil || d.Member.Confirmation != nil || d.Member.Input == nil {
		t.Fatal(d, err)
	}
	if err := s.Arrange(ctx, ids[1], o.ID, outingArrangement(), false, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Respond(ctx, ids[2], o.ID, outing.Confirmation, outing.Deny, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, "DELETE FROM outings WHERE id=$1", o.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"outing_dates", "outing_decisions", "outing_availability", "outing_arrangements", "outing_confirmations"} {
		var n int
		if err := db.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
}

func TestOutingConcurrentResponsesAndMigration(t *testing.T) {
	db, ctx, ids, s, _, o := outingFixture(t)
	start := make(chan struct{})
	results := make(chan error, 8)
	for i := range 8 {
		go func() {
			<-start
			choice, input := outing.Accept, outingInput()
			if i%2 == 0 {
				choice, input = outing.Deny, nil
			}
			results <- s.Respond(ctx, ids[2], o.ID, outing.Decision, choice, input)
		}()
	}
	close(start)
	for range 8 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	d, err := s.Get(ctx, ids[2], o.ID, ids[2])
	if err != nil || d.Member.Decision == nil || ((*d.Member.Decision == outing.Accept) != (d.Member.Input != nil)) {
		t.Fatal(d, err)
	}
	data, err := os.ReadFile("../../db/migrations/00007_outings.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "-- +goose Down")
	if _, err := db.pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&n); err != nil || n != len(ids) {
		t.Fatal("migration damaged accounts", n, err)
	}
}

func TestOutingWriteRollbackAndCancellation(t *testing.T) {
	db, ctx, ids, s, _, o := outingFixture(t)
	if err := s.Respond(ctx, ids[2], o.ID, outing.Decision, outing.Accept, outingInput()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `CREATE FUNCTION fail_availability() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected failure'; END $$;
CREATE TRIGGER fail_availability BEFORE INSERT ON outing_availability FOR EACH ROW EXECUTE FUNCTION fail_availability()`); err != nil {
		t.Fatal(err)
	}
	input := outingInput()
	input.MaxBudgetFen = 100
	if err := s.Respond(ctx, ids[2], o.ID, outing.Decision, outing.Accept, input); err == nil {
		t.Fatal("failed input write succeeded")
	}
	d, err := s.Get(ctx, ids[2], o.ID, ids[2])
	if err != nil || d.Member.Input.MaxBudgetFen != 0 || len(d.Member.Input.Availability) != 1 {
		t.Fatal("partial response persisted", d, err)
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackGroup(tx)
	if _, err := tx.Exec(ctx, "SELECT id FROM outings WHERE id=$1 FOR UPDATE", o.ID); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := s.Respond(short, ids[2], o.ID, outing.Decision, outing.Deny, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	rollbackGroup(tx)
	canceled, stop := context.WithCancel(ctx)
	stop()
	if err := s.Update(canceled, ids[1], o.ID, o.Plan); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
