package api

import (
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/Jason-skd/play-pair/internal/group"
	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/gin-gonic/gin"
)

func (s *server) groupWrite(c *gin.Context, header, operation string) (users.ID, bool) {
	actor, ok := s.resourceActor(c)
	if !ok {
		return 0, false
	}
	if header != "1" {
		writeError(c, 400, generated.CsrfRejected, "Invalid browser request")
		return 0, false
	}
	return actor, s.allow(c, "group:"+operation+":"+strconv.FormatInt(int64(actor), 10), 10*time.Minute, 20)
}
func (s *server) CreateGroup(c *gin.Context, params generated.CreateGroupParams) {
	actor, ok := s.groupWrite(c, string(params.XPlayPairRequest), "create")
	if !ok {
		return
	}
	var body generated.CreateGroupRequest
	if !decodeReplacementLimit(c, &body, []string{"name"}, nil, 8<<10) {
		return
	}
	g, created, err := s.services.Groups.Create(c.Request.Context(), actor, params.IdempotencyKey, body.Name)
	if err != nil {
		s.groupFailure(c, err)
		return
	}
	status := 200
	if created {
		status = 201
	}
	c.JSON(status, groupResponse(g))
}
func (s *server) JoinGroup(c *gin.Context, params generated.JoinGroupParams) {
	actor, ok := s.groupWrite(c, string(params.XPlayPairRequest), "join")
	if !ok {
		return
	}
	var body generated.JoinGroupRequest
	if !decodeReplacementLimit(c, &body, []string{"joinCode"}, nil, 8<<10) {
		return
	}
	g, err := s.services.Groups.Join(c.Request.Context(), actor, body.JoinCode)
	if err != nil {
		s.groupFailure(c, err)
		return
	}
	c.JSON(200, groupResponse(g))
}
func (s *server) GetGroup(c *gin.Context, id generated.GroupId) {
	actor, ok := s.resourceActor(c)
	if !ok {
		return
	}
	g, err := s.services.Groups.Get(c.Request.Context(), actor, group.ID(id))
	if err != nil {
		s.groupFailure(c, err)
		return
	}
	c.JSON(200, groupResponse(g))
}
func (s *server) GetGroupMembers(c *gin.Context, id generated.GroupId, params generated.GetGroupMembersParams) {
	actor, ok := s.resourceActor(c)
	if !ok {
		return
	}
	after, limit := users.ID(0), 50
	// Reject ambiguous repeated, empty, or unknown query parameters as invalid input.
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		s.groupFailure(c, group.ErrInvalidInput)
		return
	}
	for key, values := range query {
		if (key != "afterMemberId" && key != "limit") || len(values) != 1 || values[0] == "" {
			s.groupFailure(c, group.ErrInvalidInput)
			return
		}
	}
	if params.AfterMemberId != nil {
		after = users.ID(*params.AfterMemberId)
		if after <= 0 {
			s.groupFailure(c, group.ErrInvalidInput)
			return
		}
	}
	if params.Limit != nil {
		limit = *params.Limit
	}
	page, err := s.services.Groups.Members(c.Request.Context(), actor, group.ID(id), after, limit)
	if err != nil {
		s.groupFailure(c, err)
		return
	}
	result := generated.GroupMemberPage{Members: make([]generated.GroupMember, 0, len(page.Members))}
	if page.NextAfterMemberID != nil {
		next := int64(*page.NextAfterMemberID)
		result.NextAfterMemberId = &next
	}
	for _, m := range page.Members {
		result.Members = append(result.Members, generated.GroupMember{MemberId: int64(m.MemberID), Role: generated.GroupMemberRole(m.Role), JoinedAt: m.JoinedAt, DisplayName: m.DisplayName, Nickname: m.Nickname})
	}
	c.JSON(200, result)
}
func groupResponse(g group.Group) generated.Group {
	return generated.Group{Id: int64(g.ID), Name: g.Name, JoinCode: g.JoinCode, InitiatorId: int64(g.InitiatorID), CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt}
}
func (s *server) groupFailure(c *gin.Context, err error) {
	switch {
	case errors.Is(err, group.ErrInvalidInput):
		writeError(c, 400, generated.InvalidInput, "Invalid input")
	case errors.Is(err, group.ErrForbidden):
		writeError(c, 403, generated.Forbidden, "Group membership required")
	case errors.Is(err, group.ErrIdempotencyConflict):
		writeError(c, 409, generated.IdempotencyConflict, "Creation key already used for a different request")
	case errors.Is(err, errs.NotFound):
		writeError(c, 404, generated.NotFound, "Not found")
	default:
		s.failure(c, err)
	}
}
