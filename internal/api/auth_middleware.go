package api

import (
	"context"

	"github.com/Jason-skd/play-pair/internal/auth"
	"github.com/gin-gonic/gin"
)

type authenticatedUserKey struct{}

func (s *server) requireUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, _ := c.Cookie(sessionCookie)
		user, err := s.auth.Authenticate(c.Request.Context(), token)
		if err != nil {
			s.authFailure(c, err)
			return
		}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), authenticatedUserKey{}, user))
		c.Next()
	}
}

func authenticatedUser(ctx context.Context) (auth.User, bool) {
	u, ok := ctx.Value(authenticatedUserKey{}).(auth.User)
	return u, ok
}
