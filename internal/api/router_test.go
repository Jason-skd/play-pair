package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

func TestRouter(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		name, method, path string
		check              HealthCheck
		want               int
		code               string
	}{
		{"live", "GET", "/healthz", func(context.Context) error { return errors.New("unused") }, 200, ""},
		{"ready", "GET", "/readyz", func(context.Context) error { return nil }, 200, ""},
		{"dependency", "GET", "/readyz", func(context.Context) error { return errs.DependencyUnavailable }, 503, "dependency_unavailable"},
		{"timeout", "GET", "/readyz", func(context.Context) error { return context.DeadlineExceeded }, 503, "dependency_unavailable"},
		{"internal", "GET", "/readyz", func(context.Context) error { return errors.New("secret") }, 500, "internal_error"},
		{"missing", "GET", "/missing", func(context.Context) error { return nil }, 404, "not_found"},
		{"method", "POST", "/healthz", func(context.Context) error { return nil }, 405, "method_not_allowed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := New(tc.check, time.Second, logger, nil, false)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.want {
				t.Fatalf("status %d, body %s", w.Code, w.Body.String())
			}
			id := w.Header().Get("X-Request-ID")
			if id == "" {
				t.Fatal("missing request id")
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if tc.code == "" {
				if body["status"] != "ok" {
					t.Fatal(body)
				}
				return
			}
			e := body["error"].(map[string]any)
			if e["code"] != tc.code || e["requestId"] != id || e["message"] == "secret" {
				t.Fatal(body)
			}
			if tc.want == 405 && w.Header().Get("Allow") != "GET" {
				t.Fatalf("Allow=%q", w.Header().Get("Allow"))
			}
		})
	}
}

func TestRecovery(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := New(func(context.Context) error { panic("secret") }, time.Second, logger, nil, false)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != 500 || json.Valid(w.Body.Bytes()) == false || w.Body.String() == "secret" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestReadyCancellation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := New(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, time.Millisecond, logger, nil, false)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != 503 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

var _ *gin.Engine = New(func(context.Context) error { return nil }, time.Second, slog.Default(), nil, false)

func TestRoutesMatchOpenAPI(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	var want []string
	for path, operations := range spec.Paths {
		for method := range operations {
			want = append(want, method+" "+path)
		}
	}
	r := New(nil, time.Second, slog.Default(), nil, true)
	var got []string
	for _, route := range r.Routes() {
		got = append(got, strings.ToLower(route.Method)+" "+route.Path)
	}
	sort.Strings(want)
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Fatalf("routes=%v\nOpenAPI=%v", got, want)
	}
}
