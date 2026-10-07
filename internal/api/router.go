// Package api adapts HTTP requests to health, authentication and personal resources.
package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/Jason-skd/play-pair/internal/credentials"
	"github.com/Jason-skd/play-pair/internal/group"
	"github.com/Jason-skd/play-pair/internal/outing"
	"github.com/Jason-skd/play-pair/internal/preference"
	"github.com/Jason-skd/play-pair/internal/profile"
	"github.com/Jason-skd/play-pair/internal/session"
	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/gin-gonic/gin"
)

// HealthCheck checks dependency readiness within the caller's context.
type HealthCheck func(context.Context) error

// UserReader composes display and login data for the existing HTTP identity view.
type UserReader interface {
	UserDetails(context.Context, users.ID) (email, displayName string, err error)
}

// Services supplies the router's explicit dependencies, assembled by the caller.
// Account authentication stays separate from display and preference operations.
type Services struct {
	Outings     *outing.Service
	Groups      *group.Service
	Preferences *preference.Service
	Profiles    *profile.Service
	Credentials *credentials.Service
	Sessions    *session.Service
	Users       UserReader
}
type server struct {
	check        HealthCheck
	timeout      time.Duration
	log          *slog.Logger
	services     Services
	secureCookie bool
	limits       *limiter
}

// New assembles HTTP routes and request middleware. The caller owns the check,
// logger, and services and must keep them valid while serving.
// A true secureCookie requires HTTPS for browsers to return session cookies.
func New(check HealthCheck, timeout time.Duration, logger *slog.Logger, services Services, secureCookie bool) *gin.Engine {
	s := &server{check: check, timeout: timeout, log: logger, services: services, secureCookie: secureCookie, limits: newLimiter(10000)}
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
	groups := r.Group("/api/v1/groups", func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() }, s.requireUser())
	groups.POST("", wrapper.CreateGroup)
	groups.POST("/:groupId/outings", wrapper.CreateOuting)
	outings := r.Group("/api/v1/outings", func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() }, s.requireUser())
	outings.GET("", wrapper.ListOutings)
	outings.GET("/:outingId", wrapper.GetOuting)
	outings.PUT("/:outingId", wrapper.UpdateOuting)
	outings.GET("/:outingId/responses", wrapper.ListOutingResponses)
	outings.GET("/:outingId/members/:memberId", wrapper.GetOutingMember)
	outings.GET("/:outingId/decision-inputs", wrapper.GetDecisionInputs)
	outings.PUT("/:outingId/responses/decision", wrapper.RespondDecision)
	outings.PUT("/:outingId/responses/confirmation", wrapper.RespondConfirmation)
	outings.POST("/:outingId/arrangement", wrapper.CreateArrangement)
	outings.PUT("/:outingId/arrangement", wrapper.UpdateArrangement)
	groups.POST("/join", wrapper.JoinGroup)
	groups.GET("/:groupId", wrapper.GetGroup)
	groups.GET("/:groupId/members", wrapper.GetGroupMembers)
	r.POST("/api/v1/auth/register", wrapper.Register)
	r.POST("/api/v1/auth/login", wrapper.Login)
	r.POST("/api/v1/auth/logout", wrapper.Logout)
	r.GET("/api/v1/preferences", s.requireUser(), wrapper.GetPreferences)
	r.PUT("/api/v1/preferences", s.requireUser(), wrapper.PutPreferences)
	r.GET("/api/v1/profile", s.requireUser(), wrapper.GetProfile)
	r.PUT("/api/v1/profile", s.requireUser(), wrapper.PutProfile)
	r.GET("/api/v1/me", s.requireUser(), wrapper.Me)
	r.GET("/healthz", wrapper.Health)
	r.GET("/readyz", wrapper.Ready)
	return r
}
