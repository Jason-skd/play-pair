package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/Jason-skd/play-pair/internal/group"
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

// outingObject checks exact field names, duplicate keys and explicit non-null
// presence before the generated decoder handles types. Arrays are checked at
// their owning schema boundary rather than changing profile replacement rules.
func outingObject(raw json.RawMessage, required, optional []string) (map[string]json.RawMessage, bool) {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, false
		}
		key, ok := token.(string)
		if !ok {
			return nil, false
		}
		if _, exists := fields[key]; exists || (!slices.Contains(required, key) && !slices.Contains(optional, key)) {
			return nil, false
		}
		var v json.RawMessage
		if err := d.Decode(&v); err != nil || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return nil, false
		}
		fields[key] = v
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') {
		return nil, false
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return nil, false
		}
	}
	return fields, true
}
func outingBody(c *gin.Context) (json.RawMessage, bool) {
	var raw json.RawMessage
	if !decodeJSONLimit(c, &raw, 64<<10) {
		return nil, false
	}
	if !utf8.Valid(raw) {
		writeError(c, 400, generated.InvalidInput, "Invalid UTF-8")
		return nil, false
	}
	return raw, true
}
func (s *server) CreateOuting(c *gin.Context, gid generated.GroupId, params generated.CreateOutingParams) {
	actor, ok := s.outingWrite(c, string(params.XPlayPairRequest), "create")
	if !ok {
		return
	}
	raw, ok := outingBody(c)
	if !ok {
		return
	}
	fields, valid := outingObject(raw, []string{"dates", "timezone", "durationMinutes"}, nil)
	if !valid {
		s.outingFailure(c, outing.ErrInvalidInput)
		return
	}
	var dates []string
	if json.Unmarshal(fields["dates"], &dates) != nil {
		s.outingFailure(c, outing.ErrInvalidInput)
		return
	}
	for _, d := range dates {
		if _, err := outing.ParseDate(d); err != nil {
			s.outingFailure(c, outing.ErrInvalidInput)
			return
		}
	}
	var body generated.CreateOutingRequest

	if json.Unmarshal(raw, &body) != nil {
		s.outingFailure(c, outing.ErrInvalidInput)
		return
	}
	command := outing.Create{GroupID: group.ID(gid), Key: params.IdempotencyKey, Timezone: body.Timezone, DurationMinutes: body.DurationMinutes, Dates: make([]outing.Date, 0, len(body.Dates))}
	for _, d := range body.Dates {
		command.Dates = append(command.Dates, outing.Date(d.Time.Format(time.DateOnly)))
	}
	o, created, err := s.services.Outings.Create(c.Request.Context(), actor, command)
	if err != nil {
		s.outingFailure(c, err)
		return
	}
	status := 200
	if created {
		status = 201
	}
	c.JSON(status, outingResponse(o, s.services.Outings.Now()))
}
func (s *server) GetOuting(c *gin.Context, id generated.OutingId) {
	actor, ok := s.resourceActor(c)
	if !ok {
		return
	}
	o, err := s.services.Outings.Get(c.Request.Context(), actor, outing.ID(id))
	if err != nil {
		s.outingFailure(c, err)
		return
	}
	c.JSON(200, outingResponse(o, s.services.Outings.Now()))
}
func (s *server) RespondOuting(c *gin.Context, id generated.OutingId, phase generated.InvitationPhase, params generated.RespondOutingParams) {
	actor, ok := s.outingWrite(c, string(params.XPlayPairRequest), "respond")
	if !ok {
		return
	}
	raw, ok := outingBody(c)
	if !ok {
		return
	}
	fields, ok := outingObject(raw, []string{"status", "expectedVersion"}, []string{"input"})
	if !ok {
		s.outingFailure(c, outing.ErrInvalidInput)
		return
	}
	if input, exists := fields["input"]; exists {
		f, ok := outingObject(input, []string{"maxBudgetFen", "requiresWheelchairAccess", "availability"}, nil)
		if !ok {
			s.outingFailure(c, outing.ErrInvalidInput)
			return
		}
		var intervals []json.RawMessage
		if json.Unmarshal(f["availability"], &intervals) != nil || len(intervals) == 0 || len(intervals) > 256 {
			s.outingFailure(c, outing.ErrInvalidAvailability)
			return
		}
		for _, v := range intervals {
			interval, valid := outingObject(v, []string{"date", "startTime", "endTime"}, nil)
			if !valid {
				s.outingFailure(c, outing.ErrInvalidAvailability)
				return
			}
			var date string
			if json.Unmarshal(interval["date"], &date) != nil {
				s.outingFailure(c, outing.ErrInvalidAvailability)
				return
			}
			if _, err := outing.ParseDate(date); err != nil {
				s.outingFailure(c, err)
				return
			}
		}
	}
	var body generated.RespondOutingRequest
	if json.Unmarshal(raw, &body) != nil {
		s.outingFailure(c, outing.ErrInvalidInput)
		return
	}
	command := outing.Respond{OutingID: outing.ID(id), Phase: outing.Phase(phase), Status: outing.Response(body.Status), ExpectedVersion: body.ExpectedVersion}
	if body.Input != nil {
		command.Input = &outing.MemberInput{MaxBudgetFen: body.Input.MaxBudgetFen, RequiresWheelchairAccess: body.Input.RequiresWheelchairAccess, Availability: make([]outing.Availability, 0, len(body.Input.Availability))}
		for _, v := range body.Input.Availability {
			start, err := outing.ParseMinute(v.StartTime, false)
			if err != nil {
				s.outingFailure(c, err)
				return
			}
			end, err := outing.ParseMinute(v.EndTime, true)
			if err != nil {
				s.outingFailure(c, err)
				return
			}
			command.Input.Availability = append(command.Input.Availability, outing.Availability{Date: outing.Date(v.Date.Time.Format(time.DateOnly)), StartMinute: start, EndMinute: end})
		}
	}
	result, err := s.services.Outings.Respond(c.Request.Context(), actor, command)
	if err != nil {
		s.outingFailure(c, err)
		return
	}
	c.JSON(200, invitationResponse(result))
}

// Cursors are structural tuples encoded as URL-safe JSON, never SQL fragments.
type invitationCursor struct {
	OutingID int64        `json:"outingId"`
	MemberID int64        `json:"memberId"`
	Phase    outing.Phase `json:"phase"`
}

func encodeInvitationCursor(c outing.Cursor) string {
	b, _ := json.Marshal(invitationCursor{int64(c.OutingID), int64(c.MemberID), c.Phase})
	return base64.RawURLEncoding.EncodeToString(b)
}
func parseInvitationQuery(c *gin.Context) (outing.Cursor, int, error) {
	cursor, limit := outing.Cursor{}, 50
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		return cursor, limit, outing.ErrInvalidInput
	}
	for key, values := range query {
		if (key != "cursor" && key != "limit") || len(values) != 1 || values[0] == "" {
			return cursor, limit, outing.ErrInvalidInput
		}
	}
	if v := query.Get("limit"); v != "" {
		limit, err = strconv.Atoi(v)
		if err != nil {
			return cursor, limit, outing.ErrInvalidInput
		}
	}
	if v := query.Get("cursor"); v != "" {
		if len(v) > 128 {
			return cursor, limit, outing.ErrInvalidInput
		}
		raw, err := base64.RawURLEncoding.DecodeString(v)
		if err != nil {
			return cursor, limit, outing.ErrInvalidInput
		}
		if _, ok := outingObject(raw, []string{"outingId", "memberId", "phase"}, nil); !ok {
			return cursor, limit, outing.ErrInvalidInput
		}
		var wire invitationCursor
		if json.Unmarshal(raw, &wire) != nil || wire.OutingID <= 0 || wire.MemberID <= 0 || (wire.Phase != outing.Decision && wire.Phase != outing.Confirmation) {
			return cursor, limit, outing.ErrInvalidInput
		}
		cursor = outing.Cursor{OutingID: outing.ID(wire.OutingID), MemberID: users.ID(wire.MemberID), Phase: wire.Phase}
	}
	return cursor, limit, nil
}
func (s *server) GetOutingInvitations(c *gin.Context, id generated.OutingId, _ generated.GetOutingInvitationsParams) {
	if id <= 0 {
		s.outingFailure(c, outing.ErrInvalidInput)
		return
	}
	s.listInvitations(c, outing.ID(id))
}
func (s *server) GetMyInvitations(c *gin.Context, _ generated.GetMyInvitationsParams) {
	s.listInvitations(c, 0)
}
func (s *server) listInvitations(c *gin.Context, id outing.ID) {
	actor, ok := s.resourceActor(c)
	if !ok {
		return
	}
	cursor, limit, err := parseInvitationQuery(c)
	if err != nil {
		s.outingFailure(c, err)
		return
	}
	page, err := s.services.Outings.Invitations(c.Request.Context(), actor, id, cursor, limit)
	if err != nil {
		s.outingFailure(c, err)
		return
	}
	result := generated.InvitationPage{Invitations: make([]generated.Invitation, 0, len(page.Invitations))}
	for _, i := range page.Invitations {
		result.Invitations = append(result.Invitations, invitationResponse(i))
	}
	if page.Next != nil {
		v := encodeInvitationCursor(*page.Next)
		result.NextCursor = &v
	}
	c.JSON(200, result)
}
func outingResponse(o outing.Outing, now time.Time) generated.Outing {
	result := generated.Outing{Id: int64(o.ID), GroupId: int64(o.GroupID), InitiatorId: int64(o.InitiatorID), Timezone: o.Timezone, DurationMinutes: o.DurationMinutes, CreatedAt: o.CreatedAt, Status: generated.OutingStatus(o.StatusAt(now)), Dates: make([]openapi_types.Date, 0, len(o.Dates)), Invitations: make([]generated.Invitation, 0, len(o.Invitations))}
	for _, d := range o.Dates {
		t, _ := time.Parse(time.DateOnly, string(d))
		result.Dates = append(result.Dates, openapi_types.Date{Time: t})
	}
	if a := o.Arrangement; a != nil {
		result.Arrangement = &generated.Arrangement{StartsAt: a.StartsAt, EndsAt: a.EndsAt, Place: a.Place, Activity: a.Activity}
	}
	for _, i := range o.Invitations {
		result.Invitations = append(result.Invitations, invitationResponse(i))
	}
	return result
}
func invitationResponse(i outing.Invitation) generated.Invitation {
	result := generated.Invitation{OutingId: int64(i.OutingID), MemberId: int64(i.MemberID), Phase: generated.InvitationPhase(i.Phase), Status: generated.InvitationStatus(i.Status), Version: i.Version, AcceptedAt: i.AcceptedAt, UpdatedAt: i.UpdatedAt}
	if input := i.Input; input != nil {
		result.Input = &generated.MemberInput{MaxBudgetFen: input.MaxBudgetFen, RequiresWheelchairAccess: input.RequiresWheelchairAccess, Availability: make([]generated.Availability, 0, len(input.Availability))}
		for _, v := range input.Availability {
			d, _ := time.Parse(time.DateOnly, string(v.Date))
			result.Input.Availability = append(result.Input.Availability, generated.Availability{Date: openapi_types.Date{Time: d}, StartTime: fmt.Sprintf("%02d:%02d", v.StartMinute/60, v.StartMinute%60), EndTime: fmt.Sprintf("%02d:%02d", v.EndMinute/60, v.EndMinute%60)})
		}
	}
	return result
}
func (s *server) outingFailure(c *gin.Context, err error) {
	switch {
	case errors.Is(err, outing.ErrInvalidAvailability):
		writeError(c, 400, generated.InvalidAvailability, "Invalid availability")
	case errors.Is(err, outing.ErrInvalidInput):
		writeError(c, 400, generated.InvalidInput, "Invalid input")
	case errors.Is(err, outing.ErrForbidden):
		writeError(c, 403, generated.Forbidden, "Group membership required")
	case errors.Is(err, outing.ErrIdempotencyConflict):
		writeError(c, 409, generated.IdempotencyConflict, "Creation key already used for a different request")
	case errors.Is(err, outing.ErrPhaseConflict):
		writeError(c, 409, generated.PhaseConflict, "Invitation phase or response has changed")
	case errors.Is(err, outing.ErrArchived):
		writeError(c, 409, generated.OutingArchived, "Outing has started")
	case errors.Is(err, errs.VersionConflict):
		writeError(c, 409, generated.VersionConflict, "Resource has changed; reload before saving")
	case errors.Is(err, errs.NotFound):
		writeError(c, 404, generated.NotFound, "Not found")
	default:
		s.failure(c, err)
	}
}
