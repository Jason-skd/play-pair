package api

import (
	"errors"
	"net/http"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/Jason-skd/play-pair/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func (s *server) Me(c *gin.Context) {
	u, ok := authenticatedUser(c.Request.Context())
	if !ok {
		s.failure(c, errors.New("authenticated user missing from request context"))
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, userResponse(u))
}

func userResponse(u auth.User) generated.User {
	return generated.User{Id: openapi_types.UUID(uuid.MustParse(u.ID)), Email: openapi_types.Email(u.Email), DisplayName: u.DisplayName, CreatedAt: u.CreatedAt}
}
