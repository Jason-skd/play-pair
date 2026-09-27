package api

import (
	"context"

	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/gin-gonic/gin"
)

type authenticatedUserKey struct{}

func (s *server) requireUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, _ := c.Cookie(sessionCookie)
		user, err := s.services.Sessions.Authenticate(c.Request.Context(), token)
		if err != nil {
			s.authFailure(c, err)
			return
		}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), authenticatedUserKey{}, user))
		c.Next()
	}
}

func authenticatedUser(ctx context.Context) (users.Account, bool) {
	u, ok := ctx.Value(authenticatedUserKey{}).(users.Account)
	return u, ok
}
