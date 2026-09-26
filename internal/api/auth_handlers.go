package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/Jason-skd/play-pair/internal/auth"
	"github.com/gin-gonic/gin"
)

const sessionCookie = "playpair_session"

const (
	registerIPLimit = 5
	registerWindow  = time.Hour
	loginIPLimit    = 30
	loginEmailLimit = 10
	loginWindow     = 15 * time.Minute
)

// Register creates a user and initial session after browser and JSON checks.
func (s *server) Register(c *gin.Context, params generated.RegisterParams) {
	if string(params.XPlayPairRequest) != "1" {
		writeError(c, 400, generated.CsrfRejected, "Invalid browser request")
		return
	}
	var body generated.RegisterRequest
	if !decodeJSON(c, &body) {
		return
	}
	if !s.allow(c, "register:ip:"+c.ClientIP(), registerWindow, registerIPLimit) {
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

// Login verifies credentials and issues an independent session cookie.
func (s *server) Login(c *gin.Context, params generated.LoginParams) {
	if string(params.XPlayPairRequest) != "1" {
		writeError(c, 400, generated.CsrfRejected, "Invalid browser request")
		return
	}
	var body generated.LoginRequest
	if !decodeJSON(c, &body) {
		return
	}
	if !s.allow(c, "login:ip:"+c.ClientIP(), loginWindow, loginIPLimit) || !s.allow(c, "login:email:"+strings.ToLower(strings.TrimSpace(body.Email)), loginWindow, loginEmailLimit) {
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

// Logout revokes the presented session; missing and malformed cookies succeed.
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

func (s *server) allow(c *gin.Context, key string, window time.Duration, limit int) bool {
	ok, retry := s.limits.allow(key, window, limit)
	if ok {
		return true
	}
	c.Header("Retry-After", strconv.Itoa(max(1, int((retry+time.Second-1)/time.Second))))
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
