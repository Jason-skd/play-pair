package api

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWriteProtections(t *testing.T) {
	for _, path := range []string{"/api/v1/auth/register", "/api/v1/auth/login", "/api/v1/auth/logout"} {
		for _, tc := range []struct {
			name, header, origin, site string
			want                       int
			code                       string
		}{
			{"missing header", "", "", "", 400, "invalid_input"},
			{"wrong header", "2", "", "", 400, "csrf_rejected"},
			{"cross origin", "1", "https://evil.example", "", 403, "csrf_rejected"},
			{"cross site", "1", "", "cross-site", 403, "csrf_rejected"},
			{"binding before origin", "", "https://evil.example", "", 400, "invalid_input"},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				var logs bytes.Buffer
				// Nil auth turns accidental fallthrough into a 500, making abort ordering observable.
				r := New(nil, time.Second, slog.New(slog.NewTextHandler(&logs, nil)), nil, true)
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
				req.Header.Set("Content-Type", "application/json")
				if tc.header != "" {
					req.Header.Set("X-PlayPair-Request", tc.header)
				}
				if tc.origin != "" {
					req.Header.Set("Origin", tc.origin)
				}
				if tc.site != "" {
					req.Header.Set("Sec-Fetch-Site", tc.site)
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if w.Code != tc.want || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) || logs.Len() != 0 {
					t.Fatalf("status=%d body=%s logs=%s", w.Code, w.Body.String(), logs.String())
				}
			})
		}
	}
}

func TestJSONProtections(t *testing.T) {
	for _, path := range []string{"/api/v1/auth/register", "/api/v1/auth/login"} {
		for _, tc := range []struct{ name, contentType, body string }{
			{"content type", "text/plain", `{}`},
			{"empty", "application/json", ""},
			{"malformed", "application/json", `{`},
			{"unknown field", "application/json", `{"unknown":true}`},
			{"multiple objects", "application/json", `{} {}`},
			{"trailing junk", "application/json", `{} x`},
			{"too large", "application/json", `{"email":"` + strings.Repeat("x", 8<<10) + `"}`},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				var logs bytes.Buffer
				r := New(nil, time.Second, slog.New(slog.NewTextHandler(&logs, nil)), nil, true)
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(tc.body))
				req.Header.Set("Content-Type", tc.contentType)
				req.Header.Set("X-PlayPair-Request", "1")
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"invalid_input"`) || logs.Len() != 0 {
					t.Fatalf("status=%d body=%s logs=%s", w.Code, w.Body.String(), logs.String())
				}
			})
		}
	}
}

func TestFailureLogsRedactDetails(t *testing.T) {
	for _, panicFailure := range []bool{false, true} {
		var logs bytes.Buffer
		secret := "password=secret session=secret-token"
		r := New(func(context.Context) error {
			if panicFailure {
				panic(secret)
			}
			return &secretError{secret}
		}, time.Second, slog.New(slog.NewTextHandler(&logs, nil)), nil, true)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if w.Code != 500 || strings.Contains(w.Body.String(), secret) || strings.Contains(logs.String(), secret) {
			t.Fatalf("status=%d body=%s logs=%s", w.Code, w.Body.String(), logs.String())
		}
		if !strings.Contains(logs.String(), w.Header().Get("X-Request-ID")) {
			t.Fatal("missing log correlation")
		}
	}
}

type secretError struct{ message string }

func (e *secretError) Error() string { return e.message }
