package api

import (
	"context"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/gin-gonic/gin"
)

// Health reports process liveness without checking dependencies.
func (s *server) Health(c *gin.Context) { c.JSON(200, generated.Health{Status: generated.Ok}) }

// Ready checks dependency readiness with the configured timeout.
func (s *server) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), s.timeout)
	defer cancel()
	if err := s.check(ctx); err != nil {
		s.failure(c, err)
		return
	}
	c.JSON(200, generated.Health{Status: generated.Ok})
}
