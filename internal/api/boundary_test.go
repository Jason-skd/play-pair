package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/auth"
	"github.com/gin-gonic/gin"
)

func TestAuthenticationAbortsHandler(t *testing.T) {
	s := &server{auth: auth.New(newAuthRepo(), nil)}
	r := gin.New()
	called := false
	r.GET("/protected", s.requireUser(), func(c *gin.Context) { called = true })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/protected", nil))
	if called || w.Code != 401 {
		t.Fatalf("called=%v status=%d", called, w.Code)
	}
}

func TestPublicRoutesAndIdempotentLogout(t *testing.T) {
	r := New(func(context.Context) error { return nil }, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), auth.New(newAuthRepo(), nil), true)
	for _, path := range []string{"/healthz", "/readyz"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != 200 {
			t.Fatal(path, w.Code)
		}
	}
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
		req.Header.Set("X-PlayPair-Request", "1")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		cookies := w.Result().Cookies()
		if w.Code != 204 || len(cookies) != 1 {
			t.Fatalf("status=%d cookies=%v", w.Code, cookies)
		}
		c := cookies[0]
		if c.Name != sessionCookie || c.Path != "/" || c.MaxAge != -1 || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
			t.Fatal(c)
		}
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	expiry := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	for _, secure := range []bool{false, true} {
		s := &server{secureCookie: secure}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		s.setSession(c, "token", expiry)
		cookies := w.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatal(cookies)
		}
		cookie := cookies[0]
		if cookie.Name != sessionCookie || cookie.Value != "token" || cookie.Path != "/" || !cookie.Expires.Equal(expiry) || cookie.MaxAge != 604800 || cookie.Secure != secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
			t.Fatal(cookie)
		}
	}
}

func TestRateLimitPoliciesAndKeyIsolation(t *testing.T) {
	now := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	l := newLimiter(10)
	l.now = func() time.Time { return now }
	for _, tc := range []struct {
		key    string
		window time.Duration
		limit  int
	}{
		{"register:ip:a", registerWindow, registerIPLimit},
		{"login:ip:a", loginWindow, loginIPLimit},
		{"login:email:a", loginWindow, loginEmailLimit},
		{"login:ip:b", loginWindow, loginIPLimit},
	} {
		for i := 0; i < tc.limit; i++ {
			if ok, _ := l.allow(tc.key, tc.window, tc.limit); !ok {
				t.Fatal(tc.key, i)
			}
		}
		if ok, retry := l.allow(tc.key, tc.window, tc.limit); ok || retry != tc.window {
			t.Fatal(tc.key, ok, retry)
		}
	}
}
