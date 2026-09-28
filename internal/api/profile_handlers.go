package api

import (
	"errors"
	"net/http"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/Jason-skd/play-pair/internal/preference"
	"github.com/Jason-skd/play-pair/internal/profile"
	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/gin-gonic/gin"
)

// resourceActor requires the identity installed by authentication middleware.
func (s *server) resourceActor(c *gin.Context) (users.ID, bool) {
	u, ok := authenticatedUser(c.Request.Context())
	if !ok {
		s.failure(c, errors.New("authenticated user missing from request context"))
		return 0, false
	}
	c.Header("Cache-Control", "no-store")
	return u.ID, true
}

// GetProfile returns display data for the authenticated account only.
func (s *server) GetProfile(c *gin.Context) {
	id, ok := s.resourceActor(c)
	if !ok {
		return
	}
	p, err := s.services.Profiles.Get(c.Request.Context(), id)
	if err != nil {
		s.resourceFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, profileResponse(p))
}

// PutProfile requires a complete replacement and an explicit expected version.
func (s *server) PutProfile(c *gin.Context, params generated.PutProfileParams) {
	id, ok := s.resourceActor(c)
	if !ok {
		return
	}
	if string(params.XPlayPairRequest) != "1" {
		writeError(c, 400, generated.CsrfRejected, "Invalid browser request")
		return
	}
	var body generated.UpdateProfile
	if !decodeReplacement(c, &body, []string{"displayName", "nickname", "expectedVersion"}, []string{"nickname"}) {
		return
	}
	p, err := s.services.Profiles.Replace(c.Request.Context(), id, profile.Update{DisplayName: body.DisplayName, Nickname: body.Nickname, ExpectedVersion: body.ExpectedVersion})
	if err != nil {
		s.resourceFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, profileResponse(p))
}

func profileResponse(p profile.Profile) generated.Profile {
	return generated.Profile{DisplayName: p.DisplayName, Nickname: p.Nickname, Version: p.Version}
}

func (s *server) resourceFailure(c *gin.Context, err error) {
	switch {
	case errors.Is(err, profile.ErrInvalidInput), errors.Is(err, preference.ErrInvalidInput):
		writeError(c, 400, generated.InvalidInput, "Invalid input")
	case errors.Is(err, errs.VersionConflict):
		writeError(c, 409, generated.VersionConflict, "Resource has changed; reload before saving")
	case errors.Is(err, errs.NotFound):
		writeError(c, 404, generated.NotFound, "Not found")
	default:
		s.failure(c, err)
	}
}
