package api

import (
	"context"
	"errors"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/gin-gonic/gin"
)

func (s *server) failure(c *gin.Context, err error) {
	// Do not log raw dependency errors: they can contain DSNs or credentials.
	status, code, message := 500, generated.InternalError, "Internal server error"
	if errors.Is(err, errs.DependencyUnavailable) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		status, code, message = 503, generated.DependencyUnavailable, "Service temporarily unavailable"
	}
	s.log.Error("request failed", "request_id", c.Writer.Header().Get("X-Request-ID"), "category", code)
	writeError(c, status, code, message)
}
func writeError(c *gin.Context, status int, code generated.ErrorResponseErrorCode, message string) {
	var body generated.ErrorResponse
	body.Error.Code = code
	body.Error.Message = message
	body.Error.RequestId = c.Writer.Header().Get("X-Request-ID")
	c.AbortWithStatusJSON(status, body)
}
