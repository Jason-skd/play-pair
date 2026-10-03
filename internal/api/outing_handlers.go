package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/Jason-skd/play-pair/internal/outing"
	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/gin-gonic/gin"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func (s *server) outingWrite(c *gin.Context, header, operation string) (users.ID, bool) {
	actor, ok := s.resourceActor(c)
	if !ok {
		return 0, false
	}
	if header != "1" {
		writeError(c, http.StatusBadRequest, generated.CsrfRejected, "Invalid browser request")
		return 0, false
	}
	return actor, s.allow(c, "outing:"+operation+":"+strconv.FormatInt(int64(actor), 10), 10*time.Minute, 20)
}
func planFromWire(dates []openapi_types.Date, duration int) outing.Plan {
	plan := outing.Plan{DurationMinutes: duration, Dates: make([]outing.Date, 0, len(dates))}
	for _, date := range dates {
		plan.Dates = append(plan.Dates, outing.Date(date.Time.Format(time.DateOnly)))
	}
	return plan
}
func (s *server) CreateOuting(c *gin.Context, groupID int64, params generated.CreateOutingParams) {
	actor, ok := s.outingWrite(c, string(params.XPlayPairRequest), "create")
	if !ok {
		return
	}
	var body generated.CreateOutingRequest
	if !decodeReplacementLimit(c, &body, []string{"dates", "durationMinutes", "timezone"}, nil, 64<<10) {
		return
	}
	current, err := s.services.Outings.Create(c.Request.Context(), actor, groupID, body.Timezone, planFromWire(body.Dates, body.DurationMinutes))
	if err != nil {
		s.outingFailure(c, err)
		return
	}
	c.JSON(http.StatusCreated, outingObject(current, s.services.Outings.Now()))
}
func (s *server) UpdateOuting(c *gin.Context, id int64, params generated.UpdateOutingParams) {
	actor, ok := s.outingWrite(c, string(params.XPlayPairRequest), "update")
	if !ok {
		return
	}
	var body generated.OutingPlan
	if !decodeReplacementLimit(c, &body, []string{"dates", "durationMinutes"}, nil, 64<<10) {
		return
	}
	if err := s.services.Outings.Update(c.Request.Context(), actor, outing.ID(id), planFromWire(body.Dates, body.DurationMinutes)); err != nil {
		s.outingFailure(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
func (s *server) GetOuting(c *gin.Context, id int64) {
	actor, ok := s.resourceActor(c)
	if !ok {
		return
	}
	detail, err := s.services.Outings.Get(c.Request.Context(), actor, outing.ID(id), actor)
	if err != nil {
		s.outingFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, generated.OutingDetail{
		Outing: outingObject(detail.Outing, s.services.Outings.Now()),
		Member: outingMember(detail),
	})
}
func (s *server) GetOutingMember(c *gin.Context, id, member int64) {
	actor, ok := s.resourceActor(c)
	if !ok {
		return
	}
	detail, err := s.services.Outings.Get(c.Request.Context(), actor, outing.ID(id), users.ID(member))
	if err != nil {
		s.outingFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, outingMember(detail))
}

// Presence pointers distinguish explicit zero/false consent from omitted fields.
// They are request-only; domain inputs always carry confirmed concrete values.
type decisionBody struct {
	Choice generated.OutingChoice `json:"choice"`
	Input  *struct {
		MaxBudgetFen             *int64                         `json:"maxBudgetFen"`
		RequiresWheelchairAccess *bool                          `json:"requiresWheelchairAccess"`
		Availability             []generated.OutingAvailability `json:"availability"`
	} `json:"input"`
}

func (s *server) RespondDecision(c *gin.Context, id int64, params generated.RespondDecisionParams) {
	actor, ok := s.outingWrite(c, string(params.XPlayPairRequest), "respond")
	if !ok {
		return
	}
	var body decisionBody
	var raw json.RawMessage
	if !decodeJSONLimit(c, &raw, 64<<10) {
		return
	}
	// Choice selects the request shape; the strict pass below validates the whole
	// document, including duplicate fields and explicit zero/false hard inputs.
	if err := json.Unmarshal(raw, &body); err != nil {
		s.outingFailure(c, outing.ErrInvalidInput)
		return
	}
	fields := []string{"choice"}
	if body.Choice == generated.OutingChoiceAccept {
		fields = append(fields,
			"input", "input.maxBudgetFen", "input.requiresWheelchairAccess",
			"input.availability", "input.availability[].date",
			"input.availability[].startTime", "input.availability[].endTime",
		)
	}
	if !decodeReplacementJSON(c, raw, &body, fields, nil) {
		return
	}
	var input *outing.MemberInput
	if submitted := body.Input; submitted != nil {
		if submitted.MaxBudgetFen == nil || submitted.RequiresWheelchairAccess == nil {
			s.outingFailure(c, outing.ErrInvalidInput)
			return
		}
		input = &outing.MemberInput{
			MaxBudgetFen:             *submitted.MaxBudgetFen,
			RequiresWheelchairAccess: *submitted.RequiresWheelchairAccess,
			Availability:             make([]outing.Availability, 0, len(submitted.Availability)),
		}
		for _, interval := range submitted.Availability {
			start, err := outing.ParseMinute(interval.StartTime, false)
			if err != nil {
				s.outingFailure(c, err)
				return
			}
			end, err := outing.ParseMinute(interval.EndTime, true)
			if err != nil {
				s.outingFailure(c, err)
				return
			}
			input.Availability = append(input.Availability, outing.Availability{
				Date:        outing.Date(interval.Date.Time.Format(time.DateOnly)),
				StartMinute: start,
				EndMinute:   end,
			})
		}
	}
	if err := s.services.Outings.Respond(c.Request.Context(), actor, outing.ID(id), outing.Decision, outing.Choice(body.Choice), input); err != nil {
		s.outingFailure(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
func (s *server) RespondConfirmation(c *gin.Context, id int64, params generated.RespondConfirmationParams) {
	actor, ok := s.outingWrite(c, string(params.XPlayPairRequest), "respond")
	if !ok {
		return
	}
	var body generated.ConfirmationRequest
	if !decodeReplacement(c, &body, []string{"choice"}, nil) {
		return
	}
	if err := s.services.Outings.Respond(c.Request.Context(), actor, outing.ID(id), outing.Confirmation, outing.Choice(body.Choice), nil); err != nil {
		s.outingFailure(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
func arrangementFromWire(arrangement generated.Arrangement) outing.Arrangement {
	return outing.Arrangement{StartsAt: arrangement.StartsAt, EndsAt: arrangement.EndsAt, Place: arrangement.Place, Activity: arrangement.Activity}
}
func (s *server) CreateArrangement(c *gin.Context, id int64, params generated.CreateArrangementParams) {
	actor, ok := s.outingWrite(c, string(params.XPlayPairRequest), "arrange")
	if !ok {
		return
	}
	var body generated.Arrangement
	if !decodeReplacement(c, &body, []string{"startsAt", "endsAt", "place", "activity"}, nil) {
		return
	}
	if err := s.services.Outings.Arrange(c.Request.Context(), actor, outing.ID(id), arrangementFromWire(body), false, false); err != nil {
		s.outingFailure(c, err)
		return
	}
	c.Status(http.StatusCreated)
}
func (s *server) UpdateArrangement(c *gin.Context, id int64, params generated.UpdateArrangementParams) {
	actor, ok := s.outingWrite(c, string(params.XPlayPairRequest), "arrange")
	if !ok {
		return
	}
	var body struct {
		generated.Arrangement
		ResetResponses *bool `json:"resetResponses"`
	}
	if !decodeReplacement(c, &body, []string{"startsAt", "endsAt", "place", "activity", "resetResponses"}, nil) {
		return
	}
	if body.ResetResponses == nil {
		s.outingFailure(c, outing.ErrInvalidInput)
		return
	}
	if err := s.services.Outings.Arrange(c.Request.Context(), actor, outing.ID(id), arrangementFromWire(body.Arrangement), true, *body.ResetResponses); err != nil {
		s.outingFailure(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
func outingQuery(c *gin.Context, allowed ...string) bool {
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		writeError(c, http.StatusBadRequest, generated.InvalidInput, "Invalid query")
		return false
	}
	for key, values := range query {
		if !slices.Contains(allowed, key) || len(values) != 1 || values[0] == "" {
			writeError(c, http.StatusBadRequest, generated.InvalidInput, "Invalid query")
			return false
		}
	}
	return true
}
func (s *server) ListOutings(c *gin.Context, params generated.ListOutingsParams) {
	actor, ok := s.resourceActor(c)
	if !ok || !outingQuery(c, "groupId", "afterId", "limit") {
		return
	}
	filter := outing.ListFilter{Limit: 50}
	if params.GroupId != nil {
		if *params.GroupId <= 0 {
			s.outingFailure(c, outing.ErrInvalidInput)
			return
		}
		filter.GroupID = *params.GroupId
	}
	if params.AfterId != nil {
		if *params.AfterId <= 0 {
			s.outingFailure(c, outing.ErrInvalidInput)
			return
		}
		filter.After = outing.ID(*params.AfterId)
	}
	if params.Limit != nil {
		filter.Limit = *params.Limit
	}
	page, err := s.services.Outings.List(c.Request.Context(), actor, filter)
	if err != nil {
		s.outingFailure(c, err)
		return
	}
	result := generated.OutingPage{Outings: make([]generated.Outing, 0, len(page.Outings))}
	now := s.services.Outings.Now()
	for _, current := range page.Outings {
		result.Outings = append(result.Outings, outingObject(current, now))
	}
	if page.Next != nil {
		nextID := int64(*page.Next)
		result.NextAfterId = &nextID
	}
	c.JSON(http.StatusOK, result)
}

// responseCursor encodes both ordering fields because each member can have two rows.
type responseCursor struct {
	MemberID int64        `json:"memberId"`
	Phase    outing.Phase `json:"phase"`
}

func (s *server) ListOutingResponses(c *gin.Context, id int64, params generated.ListOutingResponsesParams) {
	actor, ok := s.resourceActor(c)
	if !ok || !outingQuery(c, "phase", "status", "cursor", "limit") {
		return
	}
	filter := outing.ResponseFilter{Limit: 50}
	if params.Limit != nil {
		filter.Limit = *params.Limit
	}
	if params.Phase != nil {
		filter.Phase = outing.Phase(*params.Phase)
	}
	if params.Status != nil {
		filter.Status = outing.Choice(*params.Status)
	}
	if params.Cursor != nil {
		if len(*params.Cursor) > 128 {
			s.outingFailure(c, outing.ErrInvalidInput)
			return
		}
		cursorJSON, err := base64.RawURLEncoding.DecodeString(*params.Cursor)
		if err != nil {
			s.outingFailure(c, outing.ErrInvalidInput)
			return
		}
		var cursor responseCursor
		if err := json.Unmarshal(cursorJSON, &cursor); err != nil {
			s.outingFailure(c, outing.ErrInvalidInput)
			return
		}
		if cursor.MemberID <= 0 || (cursor.Phase != outing.Decision && cursor.Phase != outing.Confirmation) {
			s.outingFailure(c, outing.ErrInvalidInput)
			return
		}
		filter.After = outing.ResponseCursor{MemberID: users.ID(cursor.MemberID), Phase: cursor.Phase}
	}
	page, err := s.services.Outings.Responses(c.Request.Context(), actor, outing.ID(id), filter)
	if err != nil {
		s.outingFailure(c, err)
		return
	}
	result := generated.ResponsePage{Responses: make([]generated.ResponseSummary, 0, len(page.Responses))}
	for _, response := range page.Responses {
		result.Responses = append(result.Responses, generated.ResponseSummary{
			MemberId: int64(response.MemberID),
			Phase:    generated.OutingPhase(response.Phase),
			Status:   generated.ResponseStatus(response.Status),
		})
	}
	if page.Next != nil {
		// This fixed cursor contains only integer and string fields, so marshaling
		// cannot fail through unsupported values or custom MarshalJSON methods.
		cursorJSON, _ := json.Marshal(responseCursor{MemberID: int64(page.Next.MemberID), Phase: page.Next.Phase})
		cursor := base64.RawURLEncoding.EncodeToString(cursorJSON)
		result.NextCursor = &cursor
	}
	c.JSON(http.StatusOK, result)
}

// outingObject projects domain dates already validated on the write path.
func outingObject(current outing.Outing, now time.Time) generated.Outing {
	result := generated.Outing{
		Id:              int64(current.ID),
		GroupId:         current.GroupID,
		InitiatorId:     int64(current.InitiatorID),
		Timezone:        current.Timezone,
		DurationMinutes: current.DurationMinutes,
		CreatedAt:       current.CreatedAt,
		Status:          generated.OutingStatus(current.StatusAt(now)),
		Dates:           []openapi_types.Date{},
	}
	for _, date := range current.Dates {
		parsed, _ := time.Parse(time.DateOnly, string(date))
		result.Dates = append(result.Dates, openapi_types.Date{Time: parsed})
	}
	if arrangement := current.Arrangement; arrangement != nil {
		result.Arrangement = &generated.Arrangement{
			StartsAt: arrangement.StartsAt,
			EndsAt:   arrangement.EndsAt,
			Place:    arrangement.Place,
			Activity: arrangement.Activity,
		}
	}
	return result
}

// outingMember distinguishes an unanswered current round from a past round that
// the member never joined, while retaining first-round input during confirmation.
func outingMember(detail outing.Detail) generated.OutingMember {
	member := detail.Member
	result := generated.OutingMember{
		MemberId:     int64(member.MemberID),
		Decision:     "pending",
		Confirmation: "not_started",
		Warnings:     []generated.OutingWarning{},
	}
	if detail.Outing.Arrangement != nil {
		result.Decision = "not_participated"
		result.Confirmation = "pending"
	}
	if member.Decision != nil {
		result.Decision = generated.OutingMemberDecision(*member.Decision)
	}
	if member.Confirmation != nil {
		result.Confirmation = generated.OutingMemberConfirmation(*member.Confirmation)
	}
	if input := member.Input; input != nil {
		result.Input = &generated.OutingInput{
			MaxBudgetFen:             input.MaxBudgetFen,
			RequiresWheelchairAccess: input.RequiresWheelchairAccess,
			Availability:             []generated.OutingAvailability{},
		}
		for _, interval := range input.Availability {
			parsed, _ := time.Parse(time.DateOnly, string(interval.Date))
			result.Input.Availability = append(result.Input.Availability, generated.OutingAvailability{
				Date:      openapi_types.Date{Time: parsed},
				StartTime: fmt.Sprintf("%02d:%02d", interval.StartMinute/60, interval.StartMinute%60),
				EndTime:   fmt.Sprintf("%02d:%02d", interval.EndMinute/60, interval.EndMinute%60),
			})
		}
	}
	for _, warning := range detail.Warnings {
		result.Warnings = append(result.Warnings, generated.OutingWarning{Code: generated.OutingWarningCode(warning.Code), AvailabilityIndex: warning.AvailabilityIndex})
	}
	return result
}
func (s *server) outingFailure(c *gin.Context, err error) {
	switch {
	case errors.Is(err, outing.ErrInvalidInput):
		writeError(c, http.StatusBadRequest, generated.InvalidInput, "Invalid input")
	case errors.Is(err, outing.ErrInvalidAvailability):
		writeError(c, http.StatusBadRequest, generated.InvalidAvailability, "Invalid availability")
	case errors.Is(err, outing.ErrForbidden):
		writeError(c, http.StatusForbidden, generated.Forbidden, "Outing access denied")
	case errors.Is(err, outing.ErrPhase):
		writeError(c, http.StatusConflict, generated.PhaseConflict, "Operation is not available in this phase")
	case errors.Is(err, outing.ErrArchived):
		writeError(c, http.StatusConflict, generated.OutingArchived, "Outing has started")
	case errors.Is(err, errs.NotFound):
		writeError(c, http.StatusNotFound, generated.NotFound, "Not found")
	default:
		s.failure(c, err)
	}
}
