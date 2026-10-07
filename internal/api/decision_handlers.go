package api

import (
	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/Jason-skd/play-pair/internal/outing"
	"github.com/gin-gonic/gin"
)

func (s *server) GetDecisionInputs(c *gin.Context, id int64) {
	actor, ok := s.resourceActor(c)
	if !ok || !outingQuery(c) {
		return
	}
	result, err := s.services.Outings.Aggregate(c.Request.Context(), actor, outing.ID(id))
	if err != nil {
		s.outingFailure(c, err)
		return
	}
	c.JSON(200, decisionInputsObject(result))
}

func timeWindowObject(window outing.TimeWindow) generated.TimeWindow {
	return generated.TimeWindow{StartsAt: window.StartsAt, EndsAt: window.EndsAt}
}

func decisionInputsObject(result outing.Aggregation) generated.DecisionInputs {
	response := generated.DecisionInputs{
		OutingId: int64(result.OutingID), Timezone: result.Timezone, DurationMinutes: result.DurationMinutes,
		MaxBudgetFen: result.MaxBudgetFen, RequiresWheelchairAccess: result.RequiresWheelchairAccess,
		Participants: []generated.DecisionParticipant{}, CommonWindows: []generated.TimeWindow{},
		StartTimes: result.StartTimes, Constraints: []generated.HardConstraint{},
	}
	for _, participant := range result.Participants {
		// Reuse the existing private-input projection; no defaults are read.
		member := outingMember(outing.Detail{Member: outing.Member{Input: &participant.Input}})
		response.Participants = append(response.Participants, generated.DecisionParticipant{MemberId: int64(participant.MemberID), Input: *member.Input})
	}
	for _, window := range result.CommonWindows {
		response.CommonWindows = append(response.CommonWindows, timeWindowObject(window))
	}
	for _, constraint := range result.Constraints {
		item := generated.HardConstraint{
			ConstraintId: constraint.ID, Type: generated.HardConstraintType(constraint.Type), Scope: generated.HardConstraintScope(constraint.Scope),
			MaxBudgetFen: constraint.MaxBudgetFen, DurationMinutes: constraint.DurationMinutes, Sources: []generated.ConstraintSource{},
		}
		if constraint.Type == outing.ConstraintTime {
			windows := make([]generated.TimeWindow, 0, len(constraint.Windows))
			for _, window := range constraint.Windows {
				windows = append(windows, timeWindowObject(window))
			}
			item.Windows = &windows
		}
		for _, source := range constraint.Sources {
			item.Sources = append(item.Sources, generated.ConstraintSource{MemberId: int64(source.MemberID), Field: generated.ConstraintSourceField(source.Field)})
		}
		response.Constraints = append(response.Constraints, item)
	}
	return response
}
