package db

import (
	"errors"
	"testing"

	"github.com/Jason-skd/play-pair/internal/outing"
)

func TestDecisionAggregationCurrentInputs(t *testing.T) {
	db, ctx, ids, service, sourceGroup, current := outingFixture(t)
	if _, err := service.Aggregate(ctx, ids[1], current.ID); !errors.Is(err, outing.ErrNoParticipants) {
		t.Fatal(err)
	}
	if _, err := service.Aggregate(ctx, ids[0], current.ID); !errors.Is(err, outing.ErrForbidden) {
		t.Fatal(err)
	}
	if err := service.Respond(ctx, ids[2], current.ID, outing.Decision, outing.Accept, outingInput()); err != nil {
		t.Fatal(err)
	}
	if err := service.Respond(ctx, ids[0], current.ID, outing.Decision, outing.Deny, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, "DELETE FROM group_members WHERE group_id=$1 AND member_id=$2", sourceGroup.ID, ids[2]); err != nil {
		t.Fatal(err)
	}
	result, err := service.Aggregate(ctx, ids[1], current.ID)
	if err != nil || len(result.Constraints[0].Sources) != 1 || result.Constraints[0].Sources[0].MemberID != ids[2] || result.MaxBudgetFen != 0 || len(result.CommonWindows) != 1 {
		t.Fatal(result, err)
	}
	input := outingInput()
	input.MaxBudgetFen = 10000
	input.RequiresWheelchairAccess = true
	if err := service.Respond(ctx, ids[1], current.ID, outing.Decision, outing.Accept, input); err != nil {
		t.Fatal(err)
	}
	result, err = service.Aggregate(ctx, ids[1], current.ID)
	if err != nil || len(result.Constraints[0].Sources) != 2 || result.MaxBudgetFen != 0 || !result.RequiresWheelchairAccess {
		t.Fatal(result, err)
	}
	if err := service.Update(ctx, ids[1], current.ID, outing.Plan{Dates: []outing.Date{"2026-10-11"}, DurationMinutes: 60}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Aggregate(ctx, ids[1], current.ID); !errors.Is(err, outing.ErrNoCommonTime) {
		t.Fatal("stale member silently dropped", err)
	}
	if err := service.Update(ctx, ids[1], current.ID, current.Plan); err != nil {
		t.Fatal(err)
	}
	// Stored value ranges are domain-owned; direct SQL can expose corrupt input,
	// which must be distinguished from absent participants or no common time.
	if _, err := db.pool.Exec(ctx, "UPDATE outing_decisions SET max_budget_fen=-1 WHERE outing_id=$1 AND member_id=$2", current.ID, ids[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Aggregate(ctx, ids[1], current.ID); !errors.Is(err, outing.ErrAggregationInput) {
		t.Fatal(err)
	}
	if err := service.Respond(ctx, ids[2], current.ID, outing.Decision, outing.Deny, nil); err != nil {
		t.Fatal(err)
	}
	result, err = service.Aggregate(ctx, ids[1], current.ID)
	if err != nil || len(result.Constraints[0].Sources) != 1 || result.Constraints[0].Sources[0].MemberID != ids[1] {
		t.Fatal(result, err)
	}
	if err := service.Arrange(ctx, ids[1], current.ID, outingArrangement(), false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Aggregate(ctx, ids[1], current.ID); !errors.Is(err, outing.ErrPhase) {
		t.Fatal(err)
	}
}

func TestDecisionInputRepeatableReadSnapshot(t *testing.T) {
	db, ctx, ids, service, _, current := outingFixture(t)
	if err := service.Respond(ctx, ids[2], current.ID, outing.Decision, outing.Accept, outingInput()); err != nil {
		t.Fatal(err)
	}
	err := db.Read(ctx, func(store outing.Store) error {
		before, err := store.LoadOuting(ctx, current.ID)
		if err != nil {
			return err
		}
		// Commit a new date and participant input between the two snapshot reads.
		plan := outing.Plan{Dates: []outing.Date{"2026-10-11"}, DurationMinutes: 90}
		if err := service.Update(ctx, ids[1], current.ID, plan); err != nil {
			return err
		}
		input := outingInput()
		input.MaxBudgetFen = 12000
		input.Availability[0].Date = "2026-10-11"
		if err := service.Respond(ctx, ids[2], current.ID, outing.Decision, outing.Accept, input); err != nil {
			return err
		}
		participants, err := store.AcceptedInputs(ctx, current.ID)
		if err != nil {
			return err
		}
		if before.Dates[0] != "2026-10-10" || len(participants) != 1 || participants[0].Input.MaxBudgetFen != 0 || participants[0].Input.Availability[0].Date != before.Dates[0] {
			t.Fatal("mixed snapshots", before, participants)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Aggregate(ctx, ids[1], current.ID)
	if err != nil || result.DurationMinutes != 90 || result.MaxBudgetFen != 12000 || len(result.CommonWindows) != 1 || result.CommonWindows[0].StartsAt.Format("2006-01-02") != "2026-10-11" {
		t.Fatal(result, err)
	}
}

func TestDecisionAvailabilityCapacityRead(t *testing.T) {
	db, ctx, ids, service, _, current := outingFixture(t)
	if err := service.Respond(ctx, ids[2], current.ID, outing.Decision, outing.Accept, outingInput()); err != nil {
		t.Fatal(err)
	}
	// A direct writer may bypass Go value limits; the adapter reads one extra
	// interval and rejects capacity rather than presenting a partial input.
	if _, err := db.pool.Exec(ctx, "INSERT INTO outing_availability(outing_id,member_id,date,start_minute,end_minute) SELECT $1,$2,'2026-10-12'::date,n,n+30 FROM generate_series(0,256) n", current.ID, ids[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Aggregate(ctx, ids[1], current.ID); !errors.Is(err, outing.ErrAggregationCapacity) {
		t.Fatal(err)
	}
}

func TestDecisionMemberCapacityRead(t *testing.T) {
	db, ctx, ids, service, _, current := outingFixture(t)
	// Seed independent respondents at the supported boundary, without creating
	// group memberships; accepted input is the source of participation.
	if _, err := db.pool.Exec(ctx, `WITH members AS (
		INSERT INTO users(created_at) SELECT clock_timestamp() FROM generate_series(1,$2::integer) RETURNING id
	), decisions AS (
		INSERT INTO outing_decisions(outing_id,member_id,choice,max_budget_fen,requires_wheelchair_access)
		SELECT $1,id,'accept',0,false FROM members RETURNING member_id
	)
	INSERT INTO outing_availability(outing_id,member_id,date,start_minute,end_minute)
	SELECT $1,member_id,'2026-10-10'::date,600,720 FROM decisions`, current.ID, outing.MaxAggregationMembers); err != nil {
		t.Fatal(err)
	}
	result, err := service.Aggregate(ctx, ids[1], current.ID)
	if err != nil || len(result.Constraints[0].Sources) != outing.MaxAggregationMembers {
		t.Fatal(result, err)
	}
	if err := service.Respond(ctx, ids[1], current.ID, outing.Decision, outing.Accept, outingInput()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Aggregate(ctx, ids[1], current.ID); !errors.Is(err, outing.ErrAggregationCapacity) {
		t.Fatal("members truncated instead of rejected", err)
	}
}
