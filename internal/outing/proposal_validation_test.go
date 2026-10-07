package outing

import (
	"errors"
	"slices"
	"testing"
	"time"
)

func validationFixture(t *testing.T) ([]Constraint, Proposal) {
	t.Helper()
	result, err := aggregateInputs(decisionOuting(), []ParticipantInput{participant(1, 10000, true, Availability{"2026-10-10", 600, 720})})
	if err != nil {
		t.Fatal(err)
	}
	cost, access := int64(10000), true
	proposal := Proposal{Schedule: TimeWindow{StartsAt: result.StartTimes[0], EndsAt: result.StartTimes[0].Add(time.Hour)}, CostPerPersonFen: &cost, WheelchairAccess: &access}
	for _, constraint := range result.Constraints {
		proposal.Answers = append(proposal.Answers, ConstraintAnswer{ConstraintID: constraint.ID, Status: Satisfied, Evidence: "provider source"})
	}
	return result.Constraints, proposal
}

func TestProposalDeterministicRulesOverrideTicks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Proposal)
		reason string
	}{
		{"over budget", func(p *Proposal) { v := int64(12000); p.CostPerPersonFen = &v }, "budget_exceeded"},
		{"outside window", func(p *Proposal) {
			p.Schedule.StartsAt = p.Schedule.StartsAt.Add(-30 * time.Minute)
			p.Schedule.EndsAt = p.Schedule.EndsAt.Add(-30 * time.Minute)
		}, "outside_common_window"},
		{"wrong duration", func(p *Proposal) { p.Schedule.EndsAt = p.Schedule.EndsAt.Add(30 * time.Minute) }, "duration_mismatch"},
		{"start grid", func(p *Proposal) {
			p.Schedule.StartsAt = p.Schedule.StartsAt.Add(15 * time.Minute)
			p.Schedule.EndsAt = p.Schedule.EndsAt.Add(15 * time.Minute)
		}, "start_not_half_hour"},
		{"access unavailable", func(p *Proposal) { v := false; p.WheelchairAccess = &v }, "wheelchair_access_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, status := range []ConstraintStatus{Satisfied, Unknown} {
				constraints, p := validationFixture(t)
				tc.change(&p)
				for i := range p.Answers {
					p.Answers[i].Status = status
				}
				result, err := ValidateProposal(constraints, p)
				if err != nil || result.Accepted || len(result.Failures) == 0 || !slices.ContainsFunc(result.Results, func(r ConstraintResult) bool { return r.Status == Violated && r.Reason == tc.reason }) {
					t.Fatal(status, result, err)
				}
			}
		})
	}
	constraints, p := validationFixture(t)
	result, err := ValidateProposal(constraints, p)
	if err != nil || !result.Accepted || len(result.Pending) != 0 || len(result.Failures) != 0 {
		t.Fatal(result, err)
	}
	p.Schedule.EndsAt = p.Schedule.StartsAt.Add(2 * time.Hour)
	// Starts and ends exactly at the common-window bounds satisfy time coverage,
	// while the separate duration rule still rejects the longer proposal.
	result, err = ValidateProposal(constraints, p)
	if err != nil || result.Results[1].Status != Satisfied || result.Results[2].Status != Violated {
		t.Fatal(result, err)
	}
}

func TestProposalUnknownExternalFacts(t *testing.T) {
	constraints, p := validationFixture(t)
	p.CostPerPersonFen = nil
	p.WheelchairAccess = nil
	for _, status := range []ConstraintStatus{Unknown, Satisfied} {
		for i := range p.Answers {
			p.Answers[i].Status = status
			p.Answers[i].Reason = "not independently confirmed"
		}
		result, err := ValidateProposal(constraints, p)
		if err != nil || !result.Accepted || len(result.Pending) != 2 || result.Results[0].Reason != "cost_unconfirmed" {
			t.Fatal(result, err)
		}
	}
	p.Answers[0].Status = Violated
	result, err := ValidateProposal(constraints, p)
	if err != nil || result.Accepted || len(result.Failures) != 1 {
		t.Fatal(result, err)
	}
	constraints, p = validationFixture(t)
	p.Answers[0].Status = Violated
	p.Answers[0].Reason = "conflicting source"
	result, err = ValidateProposal(constraints, p)
	if err != nil || result.Accepted || result.Failures[0].Code != "fact_answer_conflict" {
		t.Fatal(result, err)
	}
}

func TestProposalContractErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Proposal)
	}{
		{"missing", func(p *Proposal) { p.Answers = p.Answers[:len(p.Answers)-1] }},
		{"duplicate", func(p *Proposal) { p.Answers[1] = p.Answers[0] }},
		{"unknown id", func(p *Proposal) { p.Answers[0].ConstraintID = "not-an-input" }},
		{"invalid status", func(p *Proposal) { p.Answers[0].Status = "maybe" }},
		{"no reason or evidence", func(p *Proposal) { p.Answers[0].Evidence = "" }},
		{"bad schedule", func(p *Proposal) { p.Schedule.EndsAt = p.Schedule.StartsAt }},
		{"negative cost", func(p *Proposal) { v := int64(-1); p.CostPerPersonFen = &v }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			constraints, p := validationFixture(t)
			tc.change(&p)
			if _, err := ValidateProposal(constraints, p); !errors.Is(err, ErrConstraintContract) {
				t.Fatal(err)
			}
		})
	}
	constraints, p := validationFixture(t)
	constraints[1] = constraints[0]
	if _, err := ValidateProposal(constraints, p); !errors.Is(err, ErrConstraintContract) {
		t.Fatal(err)
	}
}
