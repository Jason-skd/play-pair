package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
		writeError(c, 400, generated.CsrfRejected, "Invalid browser request")
		return 0, false
	}
	return actor, s.allow(c, "outing:"+operation+":"+strconv.FormatInt(int64(actor), 10), 10*time.Minute, 20)
}
func planFromWire(ds []openapi_types.Date, duration int) outing.Plan {
	p := outing.Plan{DurationMinutes: duration, Dates: make([]outing.Date, 0, len(ds))}
	for _, d := range ds {
		p.Dates = append(p.Dates, outing.Date(d.Time.Format(time.DateOnly)))
	}
	return p
}
func (s *server) CreateOuting(c *gin.Context, gid int64, params generated.CreateOutingParams) {
	actor, ok := s.outingWrite(c, string(params.XPlayPairRequest), "create")
	if !ok {
		return
	}
	var body generated.CreateOutingRequest
	if !decodeReplacementLimit(c, &body, []string{"dates", "durationMinutes", "timezone"}, nil, 64<<10) {
		return
	}
	o, e := s.services.Outings.Create(c.Request.Context(), actor, gid, body.Timezone, planFromWire(body.Dates, body.DurationMinutes))
	if e != nil {
		s.outingFailure(c, e)
		return
	}
	c.JSON(201, outingObject(o, s.services.Outings.Now()))
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
	if e := s.services.Outings.Update(c.Request.Context(), actor, outing.ID(id), planFromWire(body.Dates, body.DurationMinutes)); e != nil {
		s.outingFailure(c, e)
		return
	}
	c.Status(204)
}
func (s *server) GetOuting(c *gin.Context, id int64) {
	actor, ok := s.resourceActor(c)
	if !ok {
		return
	}
	d, e := s.services.Outings.Get(c.Request.Context(), actor, outing.ID(id), actor)
	if e != nil {
		s.outingFailure(c, e)
		return
	}
	c.JSON(200, generated.OutingDetail{Outing: outingObject(d.Outing, s.services.Outings.Now()), Member: outingMember(d)})
}
func (s *server) GetOutingMember(c *gin.Context, id, member int64) {
	actor, ok := s.resourceActor(c)
	if !ok {
		return
	}
	d, e := s.services.Outings.Get(c.Request.Context(), actor, outing.ID(id), users.ID(member))
	if e != nil {
		s.outingFailure(c, e)
		return
	}
	c.JSON(200, outingMember(d))
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
		fields = append(fields, "input", "input.maxBudgetFen", "input.requiresWheelchairAccess", "input.availability", "input.availability[].date", "input.availability[].startTime", "input.availability[].endTime")
	}
	if !decodeReplacementJSON(c, raw, &body, fields, nil) {
		return
	}
	var input *outing.MemberInput
	if v := body.Input; v != nil {
		if v.MaxBudgetFen == nil || v.RequiresWheelchairAccess == nil {
			s.outingFailure(c, outing.ErrInvalidInput)
			return
		}
		input = &outing.MemberInput{MaxBudgetFen: *v.MaxBudgetFen, RequiresWheelchairAccess: *v.RequiresWheelchairAccess, Availability: make([]outing.Availability, 0, len(v.Availability))}
		for _, a := range v.Availability {
			start, e := outing.ParseMinute(a.StartTime, false)
			if e != nil {
				s.outingFailure(c, e)
				return
			}
			end, e := outing.ParseMinute(a.EndTime, true)
			if e != nil {
				s.outingFailure(c, e)
				return
			}
			input.Availability = append(input.Availability, outing.Availability{Date: outing.Date(a.Date.Time.Format(time.DateOnly)), StartMinute: start, EndMinute: end})
		}
	}
	if e := s.services.Outings.Respond(c.Request.Context(), actor, outing.ID(id), outing.Decision, outing.Choice(body.Choice), input); e != nil {
		s.outingFailure(c, e)
		return
	}
	c.Status(204)
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
	if e := s.services.Outings.Respond(c.Request.Context(), actor, outing.ID(id), outing.Confirmation, outing.Choice(body.Choice), nil); e != nil {
		s.outingFailure(c, e)
		return
	}
	c.Status(204)
}
func arrangementFromWire(a generated.Arrangement) outing.Arrangement {
	return outing.Arrangement{StartsAt: a.StartsAt, EndsAt: a.EndsAt, Place: a.Place, Activity: a.Activity}
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
	if e := s.services.Outings.Arrange(c.Request.Context(), actor, outing.ID(id), arrangementFromWire(body), false, false); e != nil {
		s.outingFailure(c, e)
		return
	}
	c.Status(201)
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
	if e := s.services.Outings.Arrange(c.Request.Context(), actor, outing.ID(id), arrangementFromWire(body.Arrangement), true, *body.ResetResponses); e != nil {
		s.outingFailure(c, e)
		return
	}
	c.Status(204)
}
func outingQuery(c *gin.Context, allowed ...string) bool {
	q, e := url.ParseQuery(c.Request.URL.RawQuery)
	if e != nil {
		writeError(c, 400, generated.InvalidInput, "Invalid query")
		return false
	}
	for k, vs := range q {
		if !slices.Contains(allowed, k) || len(vs) != 1 || vs[0] == "" {
			writeError(c, 400, generated.InvalidInput, "Invalid query")
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
	f := outing.ListFilter{Limit: 50}
	if params.GroupId != nil {
		if *params.GroupId <= 0 {
			s.outingFailure(c, outing.ErrInvalidInput)
			return
		}
		f.GroupID = *params.GroupId
	}
	if params.AfterId != nil {
		if *params.AfterId <= 0 {
			s.outingFailure(c, outing.ErrInvalidInput)
			return
		}
		f.After = outing.ID(*params.AfterId)
	}
	if params.Limit != nil {
		f.Limit = *params.Limit
	}
	p, e := s.services.Outings.List(c.Request.Context(), actor, f)
	if e != nil {
		s.outingFailure(c, e)
		return
	}
	result := generated.OutingPage{Outings: make([]generated.Outing, 0, len(p.Outings))}
	now := s.services.Outings.Now()
	for _, o := range p.Outings {
		result.Outings = append(result.Outings, outingObject(o, now))
	}
	if p.Next != nil {
		n := int64(*p.Next)
		result.NextAfterId = &n
	}
	c.JSON(200, result)
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
	f := outing.ResponseFilter{Limit: 50}
	if params.Limit != nil {
		f.Limit = *params.Limit
	}
	if params.Phase != nil {
		f.Phase = outing.Phase(*params.Phase)
	}
	if params.Status != nil {
		f.Status = outing.Choice(*params.Status)
	}
	if params.Cursor != nil {
		if len(*params.Cursor) > 128 {
			s.outingFailure(c, outing.ErrInvalidInput)
			return
		}
		b, e := base64.RawURLEncoding.DecodeString(*params.Cursor)
		var v responseCursor
		if e != nil || json.Unmarshal(b, &v) != nil || v.MemberID <= 0 || (v.Phase != outing.Decision && v.Phase != outing.Confirmation) {
			s.outingFailure(c, outing.ErrInvalidInput)
			return
		}
		f.After = outing.ResponseCursor{MemberID: users.ID(v.MemberID), Phase: v.Phase}
	}
	p, e := s.services.Outings.Responses(c.Request.Context(), actor, outing.ID(id), f)
	if e != nil {
		s.outingFailure(c, e)
		return
	}
	result := generated.ResponsePage{Responses: make([]generated.ResponseSummary, 0, len(p.Responses))}
	for _, r := range p.Responses {
		result.Responses = append(result.Responses, generated.ResponseSummary{MemberId: int64(r.MemberID), Phase: generated.OutingPhase(r.Phase), Status: generated.ResponseStatus(r.Status)})
	}
	if p.Next != nil {
		b, _ := json.Marshal(responseCursor{MemberID: int64(p.Next.MemberID), Phase: p.Next.Phase})
		v := base64.RawURLEncoding.EncodeToString(b)
		result.NextCursor = &v
	}
	c.JSON(200, result)
}
func outingObject(o outing.Outing, now time.Time) generated.Outing {
	v := generated.Outing{Id: int64(o.ID), GroupId: o.GroupID, InitiatorId: int64(o.InitiatorID), Timezone: o.Timezone, DurationMinutes: o.DurationMinutes, CreatedAt: o.CreatedAt, Status: generated.OutingStatus(o.StatusAt(now)), Dates: []openapi_types.Date{}}
	for _, d := range o.Dates {
		t, _ := time.Parse(time.DateOnly, string(d))
		v.Dates = append(v.Dates, openapi_types.Date{Time: t})
	}
	if a := o.Arrangement; a != nil {
		v.Arrangement = &generated.Arrangement{StartsAt: a.StartsAt, EndsAt: a.EndsAt, Place: a.Place, Activity: a.Activity}
	}
	return v
}

// outingMember distinguishes an unanswered current round from a past round that
// the member never joined, while retaining first-round input during confirmation.
func outingMember(d outing.Detail) generated.OutingMember {
	m := d.Member
	v := generated.OutingMember{MemberId: int64(m.MemberID), Decision: "pending", Confirmation: "not_started", Warnings: []generated.OutingWarning{}}
	if d.Outing.Arrangement != nil {
		v.Decision = "not_participated"
		v.Confirmation = "pending"
	}
	if m.Decision != nil {
		v.Decision = generated.OutingMemberDecision(*m.Decision)
	}
	if m.Confirmation != nil {
		v.Confirmation = generated.OutingMemberConfirmation(*m.Confirmation)
	}
	if in := m.Input; in != nil {
		v.Input = &generated.OutingInput{MaxBudgetFen: in.MaxBudgetFen, RequiresWheelchairAccess: in.RequiresWheelchairAccess, Availability: []generated.OutingAvailability{}}
		for _, a := range in.Availability {
			t, _ := time.Parse(time.DateOnly, string(a.Date))
			v.Input.Availability = append(v.Input.Availability, generated.OutingAvailability{Date: openapi_types.Date{Time: t}, StartTime: fmt.Sprintf("%02d:%02d", a.StartMinute/60, a.StartMinute%60), EndTime: fmt.Sprintf("%02d:%02d", a.EndMinute/60, a.EndMinute%60)})
		}
	}
	for _, w := range d.Warnings {
		v.Warnings = append(v.Warnings, generated.OutingWarning{Code: generated.OutingWarningCode(w.Code), AvailabilityIndex: w.AvailabilityIndex})
	}
	return v
}
func (s *server) outingFailure(c *gin.Context, e error) {
	switch {
	case errors.Is(e, outing.ErrInvalidInput):
		writeError(c, 400, generated.InvalidInput, "Invalid input")
	case errors.Is(e, outing.ErrInvalidAvailability):
		writeError(c, 400, generated.InvalidAvailability, "Invalid availability")
	case errors.Is(e, outing.ErrForbidden):
		writeError(c, 403, generated.Forbidden, "Outing access denied")
	case errors.Is(e, outing.ErrPhase):
		writeError(c, 409, generated.PhaseConflict, "Operation is not available in this phase")
	case errors.Is(e, outing.ErrArchived):
		writeError(c, 409, generated.OutingArchived, "Outing has started")
	case errors.Is(e, errs.NotFound):
		writeError(c, 404, generated.NotFound, "Not found")
	default:
		s.failure(c, e)
	}
}
