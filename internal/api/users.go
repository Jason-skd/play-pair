package api

import (
	"errors"
	"net/http"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/gin-gonic/gin"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Me returns the identity installed by authentication middleware.
// Missing middleware identity is an internal assembly error.
func (s *server) Me(c *gin.Context) {
	u, ok := authenticatedUser(c.Request.Context())
	if !ok {
		s.failure(c, errors.New("authenticated user missing from request context"))
		return
	}
	c.Header("Cache-Control", "no-store")
	s.respondUser(c, http.StatusOK, u)
}

func (s *server) respondUser(c *gin.Context, status int, u users.Account) {
	email, name, err := s.services.Users.UserDetails(c.Request.Context(), u.ID)
	if err != nil {
		s.failure(c, err)
		return
	}
	c.JSON(status, userResponse(u, email, name))
}
func userResponse(u users.Account, email, name string) generated.User {
	return generated.User{Id: int64(u.ID), Email: openapi_types.Email(email), DisplayName: name, CreatedAt: u.CreatedAt}
}
