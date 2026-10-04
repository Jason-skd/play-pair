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
	db, ctx, memberIDs := groupFixture(t)
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
	sourceGroup := mustCreateGroup(t, db, ctx, memberIDs[0])
	for _, id := range memberIDs[1:] {
		if _, err := group.New(db).Join(ctx, id, sourceGroup.JoinCode); err != nil {
			t.Fatal(err)
		}
	}
	service := outing.New(db, func() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) })
	// The outing organizer is deliberately not the group's initiator.
	current, err := service.Create(ctx, memberIDs[1], int64(sourceGroup.ID), "Asia/Shanghai", outing.Plan{Dates: []outing.Date{"2026-10-10"}, DurationMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	return db, ctx, memberIDs, service, sourceGroup, current
}

func outingInput() *outing.MemberInput {
	return &outing.MemberInput{MaxBudgetFen: 0, RequiresWheelchairAccess: false, Availability: []outing.Availability{{Date: "2026-10-10", StartMinute: 600, EndMinute: 720}}}
}

func outingArrangement() outing.Arrangement {
	start := time.Date(2026, 10, 10, 2, 0, 0, 0, time.UTC)
	return outing.Arrangement{StartsAt: start, EndsAt: start.Add(time.Hour), Place: "Park", Activity: "Walk"}
}

func TestOutingResponsesWarningsAndAccess(t *testing.T) {
	db, ctx, memberIDs, service, sourceGroup, current := outingFixture(t)
	if err := service.Respond(ctx, memberIDs[2], current.ID, outing.Decision, outing.Accept, outingInput()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(ctx, memberIDs[0], current.ID, memberIDs[2]); !errors.Is(err, outing.ErrForbidden) {
		t.Fatal("private input exposed", err)
	}
	detail, err := service.Get(ctx, memberIDs[1], current.ID, memberIDs[2])
	if err != nil || detail.Member.Input == nil || len(detail.Warnings) != 0 {
		t.Fatal(detail, err)
	}
	plan := outing.Plan{Dates: []outing.Date{"2026-10-11"}, DurationMinutes: 150}
	if err := service.Update(ctx, memberIDs[2], current.ID, plan); !errors.Is(err, outing.ErrForbidden) {
		t.Fatal(err)
	}
	if err := service.Update(ctx, memberIDs[1], current.ID, plan); err != nil {
		t.Fatal(err)
	}
	detail, err = service.Get(ctx, memberIDs[2], current.ID, memberIDs[2])
	if err != nil || len(detail.Warnings) != 2 || len(detail.Member.Input.Availability) != 1 || detail.Member.Input.Availability[0].Date != "2026-10-10" {
		t.Fatal(detail, err)
	}
	if err := service.Respond(ctx, memberIDs[2], current.ID, outing.Decision, outing.Accept, outingInput()); !errors.Is(err, outing.ErrInvalidAvailability) {
		t.Fatal(err)
	}
	// Removing an unresponded member loses access; decision respondents and the
	// organizer retain access and management independent of group membership.
	if _, err := db.pool.Exec(ctx, "DELETE FROM group_members WHERE group_id=$1 AND member_id IN ($2,$3)", sourceGroup.ID, memberIDs[1], memberIDs[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(ctx, memberIDs[2], current.ID, memberIDs[2]); err != nil {
		t.Fatal(err)
	}
	if err := service.Update(ctx, memberIDs[1], current.ID, current.Plan); err != nil {
		t.Fatal("organizer lost management", err)
	}
	if err := service.Respond(ctx, memberIDs[2], current.ID, outing.Decision, outing.Deny, nil); err != nil {
		t.Fatal(err)
	}
	detail, err = service.Get(ctx, memberIDs[2], current.ID, memberIDs[2])
	if err != nil || detail.Member.Input != nil || *detail.Member.Decision != outing.Deny || len(detail.Warnings) != 0 {
		t.Fatal(detail, err)
	}
	if err := service.Respond(ctx, memberIDs[2], current.ID, outing.Decision, outing.Accept, outingInput()); err != nil {
		t.Fatal(err)
	}
	// Another group supplies a member who has never responded to this outing.
	other := mustCreateGroup(t, db, ctx, memberIDs[2])
	otherOuting, err := service.Create(ctx, memberIDs[2], int64(other.ID), "UTC", current.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(ctx, memberIDs[0], otherOuting.ID, memberIDs[0]); !errors.Is(err, outing.ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := service.Create(ctx, memberIDs[0], int64(other.ID), "UTC", current.Plan); !errors.Is(err, outing.ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := service.Get(ctx, memberIDs[0], 999999, memberIDs[0]); !errors.Is(err, errs.NotFound) {
		t.Fatal(err)
	}
}

func TestArrangementResetKeepsConfirmationOnlyMember(t *testing.T) {
	db, ctx, memberIDs, service, sourceGroup, current := outingFixture(t)
	if err := service.Respond(ctx, memberIDs[0], current.ID, outing.Decision, outing.Accept, outingInput()); err != nil {
		t.Fatal(err)
	}
	arrangement := outingArrangement()
	if err := service.Arrange(ctx, memberIDs[1], current.ID, arrangement, false, false); err != nil {
		t.Fatal(err)
	}
	if err := service.Respond(ctx, memberIDs[0], current.ID, outing.Decision, outing.Deny, nil); !errors.Is(err, outing.ErrPhase) {
		t.Fatal(err)
	}
	if err := service.Respond(ctx, memberIDs[2], current.ID, outing.Confirmation, outing.Accept, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, "DELETE FROM group_members WHERE group_id=$1 AND member_id=$2", sourceGroup.ID, memberIDs[2]); err != nil {
		t.Fatal(err)
	}
	arrangement.Place = "Another park"
	if err := service.Arrange(ctx, memberIDs[1], current.ID, arrangement, true, false); err != nil {
		t.Fatal(err)
	}
	detail, err := service.Get(ctx, memberIDs[2], current.ID, memberIDs[2])
	if err != nil || *detail.Member.Confirmation != outing.Accept {
		t.Fatal(detail, err)
	}
	if err := service.Arrange(ctx, memberIDs[1], current.ID, arrangement, true, true); err != nil {
		t.Fatal(err)
	}
	detail, err = service.Get(ctx, memberIDs[2], current.ID, memberIDs[2])
	if err != nil || detail.Member.Confirmation == nil || *detail.Member.Confirmation != outing.Pending {
		t.Fatal(detail, err)
	}
	page, err := service.List(ctx, memberIDs[2], outing.ListFilter{Limit: 1})
	if err != nil || len(page.Outings) != 1 || page.Outings[0].ID != current.ID {
		t.Fatal(page, err)
	}
	if err := service.Respond(ctx, memberIDs[2], current.ID, outing.Confirmation, outing.Deny, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.Update(ctx, memberIDs[1], current.ID, outing.Plan{Dates: []outing.Date{"2026-10-11"}, DurationMinutes: 150}); err != nil {
		t.Fatal(err)
	}
	detail, err = service.Get(ctx, memberIDs[0], current.ID, memberIDs[0])
	if err != nil || detail.Member.Input == nil || len(detail.Warnings) != 0 {
		t.Fatal("confirmation hid first-round input or reported warning", detail, err)
	}
	started := outing.New(db, func() time.Time { return arrangement.StartsAt })
	for _, err := range []error{
		started.Update(ctx, memberIDs[1], current.ID, current.Plan),
		started.Arrange(ctx, memberIDs[1], current.ID, outingArrangement(), true, false),
		started.Respond(ctx, memberIDs[2], current.ID, outing.Confirmation, outing.Accept, nil),
	} {
		if !errors.Is(err, outing.ErrArchived) {
			t.Fatal("start restriction bypassed", err)
		}
	}
	if _, err := started.Get(ctx, memberIDs[2], current.ID, memberIDs[2]); err != nil {
		t.Fatal("archive unreadable", err)
	}
}

func TestOutingPendingFilteringAndPagination(t *testing.T) {
	db, ctx, memberIDs, service, sourceGroup, current := outingFixture(t)
	page, err := service.Responses(ctx, memberIDs[0], current.ID, outing.ResponseFilter{Status: outing.Pending, Limit: 1})
	if err != nil || len(page.Responses) != 1 || page.Next == nil || page.Responses[0].MemberID != memberIDs[0] {
		t.Fatal(page, err)
	}
	page, err = service.Responses(ctx, memberIDs[0], current.ID, outing.ResponseFilter{Status: outing.Pending, After: *page.Next, Limit: 10})
	if err != nil || len(page.Responses) != 2 || page.Next != nil {
		t.Fatal(page, err)
	}
	if err := service.Respond(ctx, memberIDs[1], current.ID, outing.Decision, outing.Deny, nil); err != nil {
		t.Fatal(err)
	}
	// A never-responded member leaves; no persistent pending row is needed.
	if _, err := db.pool.Exec(ctx, "DELETE FROM group_members WHERE group_id=$1 AND member_id=$2", sourceGroup.ID, memberIDs[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(ctx, memberIDs[2], current.ID, memberIDs[2]); !errors.Is(err, outing.ErrForbidden) {
		t.Fatal(err)
	}
	page, err = service.Responses(ctx, memberIDs[0], current.ID, outing.ResponseFilter{Status: outing.Pending, Limit: 10})
	if err != nil || len(page.Responses) != 1 || page.Responses[0].MemberID != memberIDs[0] {
		t.Fatal(page, err)
	}
	if err := service.Arrange(ctx, memberIDs[1], current.ID, outingArrangement(), false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := group.New(db).Join(ctx, memberIDs[2], sourceGroup.JoinCode); err != nil {
		t.Fatal(err)
	}
	want := []outing.Summary{{MemberID: memberIDs[0], Phase: outing.Confirmation, Status: outing.Pending}, {MemberID: memberIDs[1], Phase: outing.Confirmation, Status: outing.Pending}, {MemberID: memberIDs[1], Phase: outing.Decision, Status: outing.Deny}, {MemberID: memberIDs[2], Phase: outing.Confirmation, Status: outing.Pending}}
	got := []outing.Summary{}
	var cursor outing.ResponseCursor
	for range 6 {
		page, err = service.Responses(ctx, memberIDs[0], current.ID, outing.ResponseFilter{After: cursor, Limit: 1})
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
	page, err = service.Responses(ctx, memberIDs[0], current.ID, outing.ResponseFilter{Phase: outing.Decision, Status: outing.Pending, Limit: 10})
	if err != nil || len(page.Responses) != 0 {
		t.Fatal("past round synthesized pending", page, err)
	}
	second, err := service.Create(ctx, memberIDs[1], int64(sourceGroup.ID), "UTC", current.Plan)
	if err != nil {
		t.Fatal(err)
	}
	outings, err := service.List(ctx, memberIDs[0], outing.ListFilter{GroupID: int64(sourceGroup.ID), Limit: 1})
	if err != nil || outings.Next == nil || outings.Outings[0].ID != current.ID {
		t.Fatal(outings, err)
	}
	outings, err = service.List(ctx, memberIDs[0], outing.ListFilter{GroupID: int64(sourceGroup.ID), After: *outings.Next, Limit: 1})
	if err != nil || outings.Next != nil || outings.Outings[0].ID != second.ID {
		t.Fatal(outings, err)
	}
}

func TestOutingDatabaseCompletenessAndOwnership(t *testing.T) {
	db, ctx, memberIDs, service, _, current := outingFixture(t)
	_, err := db.pool.Exec(ctx, "INSERT INTO outing_decisions(outing_id,member_id,choice,max_budget_fen,requires_wheelchair_access) VALUES($1,$2,'accept',0,false)", current.ID, memberIDs[2])
	requirePGConstraint(t, err, "23514", "outing_input_complete")
	if err := service.Respond(ctx, memberIDs[1], current.ID, outing.Decision, outing.Deny, nil); err != nil {
		t.Fatal(err)
	}
	_, err = db.pool.Exec(ctx, "INSERT INTO outing_availability(outing_id,member_id,date,start_minute,end_minute) VALUES($1,$2,'2026-10-10',600,660)", current.ID, memberIDs[1])
	requirePGConstraint(t, err, "23514", "outing_input_complete")
	if err := service.Respond(ctx, memberIDs[2], current.ID, outing.Decision, outing.Accept, outingInput()); err != nil {
		t.Fatal(err)
	}
	_, err = db.pool.Exec(ctx, "DELETE FROM outing_availability WHERE outing_id=$1 AND member_id=$2", current.ID, memberIDs[2])
	requirePGConstraint(t, err, "23514", "outing_input_complete")
	// Moving the last interval must validate its old owner, too.
	if err := service.Respond(ctx, memberIDs[0], current.ID, outing.Decision, outing.Accept, outingInput()); err != nil {
		t.Fatal(err)
	}
	_, err = db.pool.Exec(ctx, "UPDATE outing_availability SET member_id=$2,start_minute=750,end_minute=810 WHERE outing_id=$1 AND member_id=$3", current.ID, memberIDs[0], memberIDs[2])
	requirePGConstraint(t, err, "23514", "outing_input_complete")
	_, err = db.pool.Exec(ctx, "INSERT INTO outing_availability SELECT outing_id,member_id,date+1,0,30 FROM outing_availability WHERE outing_id=$1 AND member_id=$2", current.ID, memberIDs[2])
	if err != nil {
		t.Fatal("stale candidate dates rejected", err)
	}
	// No stage trigger: direct SQL may correct input after arrangement creation.
	if err := service.Arrange(ctx, memberIDs[1], current.ID, outingArrangement(), false, false); err != nil {
		t.Fatal(err)
	}
	if err := service.Respond(ctx, memberIDs[2], current.ID, outing.Confirmation, outing.Accept, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, "UPDATE outing_decisions SET max_budget_fen=10 WHERE outing_id=$1 AND member_id=$2", current.ID, memberIDs[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, "DELETE FROM outing_arrangements WHERE outing_id=$1", current.ID); err != nil {
		t.Fatal(err)
	}
	detail, err := service.Get(ctx, memberIDs[2], current.ID, memberIDs[2])
	if err != nil || detail.Outing.Arrangement != nil || detail.Member.Confirmation != nil || detail.Member.Input == nil {
		t.Fatal(detail, err)
	}
	if err := service.Arrange(ctx, memberIDs[1], current.ID, outingArrangement(), false, false); err != nil {
		t.Fatal(err)
	}
	if err := service.Respond(ctx, memberIDs[2], current.ID, outing.Confirmation, outing.Deny, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, "DELETE FROM outings WHERE id=$1", current.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"outing_dates", "outing_decisions", "outing_availability", "outing_arrangements", "outing_confirmations"} {
		var n int
		if err := db.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
}

func TestOutingDatabaseLeavesValuePolicyToGo(t *testing.T) {
	db, ctx, memberIDs, service, _, current := outingFixture(t)
	if err := service.Respond(ctx, memberIDs[2], current.ID, outing.Decision, outing.Accept, outingInput()); err != nil {
		t.Fatal(err)
	}
	// Direct writes bypass Go's value policy but still satisfy input completeness.
	for _, sql := range []string{
		"UPDATE outings SET duration_minutes=15 WHERE id=$1",
		"INSERT INTO outing_dates VALUES($1, DATE '10000-01-01')",
		"UPDATE outing_decisions SET max_budget_fen=-1 WHERE outing_id=$1 AND member_id=$2",
		"UPDATE outing_availability SET date=DATE '10000-01-01',start_minute=601,end_minute=-1 WHERE outing_id=$1 AND member_id=$2",
		"INSERT INTO outing_availability SELECT $1,$2,DATE '2026-10-11'+i,0,30 FROM generate_series(1,256) i",
		"INSERT INTO outing_arrangements VALUES($1,'2026-10-11','2026-10-10','','')",
	} {
		args := []any{current.ID}
		if strings.Contains(sql, "$2") {
			args = append(args, memberIDs[2])
		}
		if _, err := db.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(sql, err)
		}
	}
}

func TestOutingConcurrentResponsesAndMigration(t *testing.T) {
	db, ctx, memberIDs, service, _, current := outingFixture(t)
	start := make(chan struct{})
	results := make(chan error, 8)
	for i := range 8 {
		go func() {
			<-start
			choice, input := outing.Accept, outingInput()
			if i%2 == 0 {
				choice, input = outing.Deny, nil
			}
			results <- service.Respond(ctx, memberIDs[2], current.ID, outing.Decision, choice, input)
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
	detail, err := service.Get(ctx, memberIDs[2], current.ID, memberIDs[2])
	if err != nil || detail.Member.Decision == nil || ((*detail.Member.Decision == outing.Accept) != (detail.Member.Input != nil)) {
		t.Fatal(detail, err)
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
	if err := db.pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&n); err != nil || n != len(memberIDs) {
		t.Fatal("migration damaged accounts", n, err)
	}
}

func TestOutingWriteRollbackAndCancellation(t *testing.T) {
	db, ctx, memberIDs, service, _, current := outingFixture(t)
	if err := service.Respond(ctx, memberIDs[2], current.ID, outing.Decision, outing.Accept, outingInput()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `CREATE FUNCTION fail_availability() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected failure'; END $$;
CREATE TRIGGER fail_availability BEFORE INSERT ON outing_availability FOR EACH ROW EXECUTE FUNCTION fail_availability()`); err != nil {
		t.Fatal(err)
	}
	input := outingInput()
	input.MaxBudgetFen = 100
	if err := service.Respond(ctx, memberIDs[2], current.ID, outing.Decision, outing.Accept, input); err == nil {
		t.Fatal("failed input write succeeded")
	}
	detail, err := service.Get(ctx, memberIDs[2], current.ID, memberIDs[2])
	if err != nil || detail.Member.Input.MaxBudgetFen != 0 || len(detail.Member.Input.Availability) != 1 {
		t.Fatal("partial response persisted", detail, err)
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackGroup(tx)
	if _, err := tx.Exec(ctx, "SELECT id FROM outings WHERE id=$1 FOR UPDATE", current.ID); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := service.Respond(short, memberIDs[2], current.ID, outing.Decision, outing.Deny, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	rollbackGroup(tx)
	canceled, stop := context.WithCancel(ctx)
	stop()
	if err := service.Update(canceled, memberIDs[1], current.ID, current.Plan); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
