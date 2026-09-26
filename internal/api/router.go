// Package api adapts HTTP requests to health and authentication services.
package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/Jason-skd/play-pair/internal/auth"
	"github.com/gin-gonic/gin"
)

// HealthCheck checks dependency readiness within the caller's context.
type HealthCheck func(context.Context) error
type server struct {
	check        HealthCheck
	timeout      time.Duration
	log          *slog.Logger
	auth         *auth.Service
	secureCookie bool
	limits       *limiter
}

// New assembles HTTP routes and request middleware. The caller owns the check,
// logger, and authentication service and must keep them valid while serving.
// A true secureCookie requires HTTPS for browsers to return session cookies.
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
	wrapper := generated.ServerInterfaceWrapper{Handler: s, HandlerMiddlewares: []generated.MiddlewareFunc{crossOriginProtection()}, ErrorHandler: func(c *gin.Context, _ error, status int) {
		writeError(c, status, generated.InvalidInput, "Invalid request")
	}}
	r.POST("/api/v1/auth/register", wrapper.Register)
	r.POST("/api/v1/auth/login", wrapper.Login)
	r.POST("/api/v1/auth/logout", wrapper.Logout)
	r.GET("/api/v1/me", s.requireUser(), wrapper.Me)
	r.GET("/healthz", wrapper.Health)
	r.GET("/readyz", wrapper.Ready)
	return r
}
