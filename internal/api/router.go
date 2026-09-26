package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/Jason-skd/play-pair/internal/auth"
	"github.com/gin-gonic/gin"
)

type HealthCheck func(context.Context) error
type server struct {
	check        HealthCheck
	timeout      time.Duration
	log          *slog.Logger
	auth         *auth.Service
	secureCookie bool
	limits       *limiter
}

func New(check HealthCheck, timeout time.Duration, logger *slog.Logger, authService *auth.Service, secureCookie bool) *gin.Engine {
	s := &server{check: check, timeout: timeout, log: logger, auth: authService, secureCookie: secureCookie, limits: newLimiter(10000)}
	r := gin.New()
	_ = r.SetTrustedProxies(nil)
	r.HandleMethodNotAllowed = true
	r.RedirectTrailingSlash = false
	r.RedirectFixedPath = false
	r.Use(requestMiddleware(logger))
	r.NoRoute(func(c *gin.Context) { writeError(c, 404, generated.NotFound, "Not found") })
	r.NoMethod(func(c *gin.Context) { writeError(c, 405, generated.MethodNotAllowed, "Method not allowed") })
	generated.RegisterHandlersWithOptions(r, s, generated.GinServerOptions{Middlewares: []generated.MiddlewareFunc{crossOriginProtection()}, ErrorHandler: func(c *gin.Context, _ error, status int) {
		writeError(c, status, generated.InvalidInput, "Invalid request")
	}})
	return r
}
