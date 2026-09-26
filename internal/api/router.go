package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/Jason-skd/play-pair/internal/auth"
	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
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
	cop := http.NewCrossOriginProtection()
	generated.RegisterHandlersWithOptions(r, s, generated.GinServerOptions{Middlewares: []generated.MiddlewareFunc{func(c *gin.Context) {
		if err := cop.Check(c.Request); err != nil {
			writeError(c, http.StatusForbidden, generated.CsrfRejected, "Cross-origin request rejected")
		}
	}}, ErrorHandler: func(c *gin.Context, _ error, status int) {
		writeError(c, status, generated.InvalidInput, "Invalid request")
	}})
	return r
}

const sessionCookie = "playpair_session"

func (s *server) Register(c *gin.Context, params generated.RegisterParams) {
	if string(params.XPlayPairRequest) != "1" {
		writeError(c, 400, generated.CsrfRejected, "Invalid browser request")
		return
	}
	var body generated.RegisterRequest
	if !decodeJSON(c, &body) {
		return
	}
	if !s.allow(c, "register:ip:"+c.ClientIP(), time.Hour, 5) {
		return
	}
	u, token, expires, err := s.auth.Register(c.Request.Context(), body.Email, body.Password, body.DisplayName)
	if err != nil {
		s.authFailure(c, err)
		return
	}
	s.setSession(c, token, expires)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, userResponse(u))
}

func (s *server) Login(c *gin.Context, params generated.LoginParams) {
	if string(params.XPlayPairRequest) != "1" {
		writeError(c, 400, generated.CsrfRejected, "Invalid browser request")
		return
	}
	var body generated.LoginRequest
	if !decodeJSON(c, &body) {
		return
	}
	if !s.allow(c, "login:ip:"+c.ClientIP(), 15*time.Minute, 30) || !s.allow(c, "login:email:"+strings.ToLower(strings.TrimSpace(body.Email)), 15*time.Minute, 10) {
		return
	}
	u, token, expires, err := s.auth.Login(c.Request.Context(), body.Email, body.Password)
	if err != nil {
		s.authFailure(c, err)
		return
	}
	s.setSession(c, token, expires)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, userResponse(u))
}

func (s *server) Logout(c *gin.Context, params generated.LogoutParams) {
	if string(params.XPlayPairRequest) != "1" {
		writeError(c, 400, generated.CsrfRejected, "Invalid browser request")
		return
	}
	token, _ := c.Cookie(sessionCookie)
	if err := s.auth.Logout(c.Request.Context(), token); err != nil {
		s.authFailure(c, err)
		return
	}
	s.clearSession(c)
	c.Header("Cache-Control", "no-store")
	c.Status(http.StatusNoContent)
}

func (s *server) Me(c *gin.Context) {
	token, _ := c.Cookie(sessionCookie)
	u, err := s.auth.Authenticate(c.Request.Context(), token)
	if err != nil {
		s.authFailure(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, userResponse(u))
}

func decodeJSON(c *gin.Context, dst any) bool {
	if c.ContentType() != "application/json" {
		writeError(c, 400, generated.InvalidInput, "Expected application/json")
		return false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(c, 400, generated.InvalidInput, "Invalid request")
		return false
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		writeError(c, 400, generated.InvalidInput, "Invalid request")
		return false
	}
	return true
}

func (s *server) allow(c *gin.Context, key string, window time.Duration, limit int) bool {
	ok, retry := s.limits.allow(key, window, limit)
	if ok {
		return true
	}
	c.Header("Retry-After", strconv.Itoa(max(1, int(retry.Round(time.Second)/time.Second))))
	writeError(c, 429, generated.RateLimited, "Too many requests")
	return false
}

func (s *server) setSession(c *gin.Context, token string, expires time.Time) {
	http.SetCookie(c.Writer, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", Expires: expires, MaxAge: int(auth.SessionDuration.Seconds()), HttpOnly: true, Secure: s.secureCookie, SameSite: http.SameSiteLaxMode})
}
func (s *server) clearSession(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secureCookie, SameSite: http.SameSiteLaxMode})
}

func (s *server) authFailure(c *gin.Context, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidInput):
		writeError(c, 400, generated.InvalidInput, "Invalid input")
	case errors.Is(err, auth.ErrEmailConflict):
		writeError(c, 409, generated.EmailConflict, "Email already registered")
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(c, 401, generated.InvalidCredentials, "Invalid credentials")
	case errors.Is(err, auth.ErrUnauthenticated):
		writeError(c, 401, generated.Unauthenticated, "Authentication required")
	default:
		s.failure(c, err)
	}
}

func userResponse(u auth.User) generated.User {
	return generated.User{Id: openapi_types.UUID(uuid.MustParse(u.ID)), Email: openapi_types.Email(u.Email), DisplayName: u.DisplayName, CreatedAt: u.CreatedAt}
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
