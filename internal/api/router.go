package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/gin-gonic/gin"
)

type HealthCheck func(context.Context) error
type server struct {
	check   HealthCheck
	timeout time.Duration
	log     *slog.Logger
}

func New(check HealthCheck, timeout time.Duration, logger *slog.Logger) *gin.Engine {
	s := &server{check, timeout, logger}
	r := gin.New()
	r.HandleMethodNotAllowed = true
	r.RedirectTrailingSlash = false
	r.RedirectFixedPath = false
	r.Use(func(c *gin.Context) {
		var id [16]byte
		_, _ = rand.Read(id[:])
		c.Header("X-Request-ID", hex.EncodeToString(id[:]))
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("request panic", "request_id", c.Writer.Header().Get("X-Request-ID"))
				if !c.Writer.Written() {
					writeError(c, http.StatusInternalServerError, generated.InternalError, "Internal server error")
				}
				c.Abort()
			}
		}()
		c.Next()
	})
	r.NoRoute(func(c *gin.Context) { writeError(c, 404, generated.NotFound, "Not found") })
	r.NoMethod(func(c *gin.Context) { writeError(c, 405, generated.MethodNotAllowed, "Method not allowed") })
	generated.RegisterHandlers(r, s)
	return r
}

func (s *server) Health(c *gin.Context) { c.JSON(200, generated.Health{Status: generated.Ok}) }
func (s *server) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), s.timeout)
	defer cancel()
	if err := s.check(ctx); err != nil {
		s.failure(c, err)
		return
	}
	c.JSON(200, generated.Health{Status: generated.Ok})
}
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
