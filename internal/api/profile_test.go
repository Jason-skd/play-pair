package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/Jason-skd/play-pair/internal/profile"
	"github.com/Jason-skd/play-pair/internal/users"
)

func (r *authRepo) GetProfile(_ context.Context, id users.ID) (profile.Profile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.profiles[id]
	if !ok {
		return profile.Profile{}, errs.NotFound
	}
	return p, nil
}
func (r *authRepo) UpdateProfile(_ context.Context, id users.ID, c profile.Update) (profile.Profile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.profiles[id]
	if p.Version != c.ExpectedVersion {
		return profile.Profile{}, errs.VersionConflict
	}
	p.DisplayName = c.DisplayName
	p.Nickname = c.Nickname
	p.Version++
	r.profiles[id] = p
	return p, nil
}

func resourceRequest(handler http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-PlayPair-Request", "1")
	if token != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestProfileHTTP(t *testing.T) {
	repo := newAuthRepo()
	services := newServices(repo, time.Now)
	_, token, _, err := services.Credentials.Register(context.Background(), "one@example.com", "correct horse battery", "One")
	if err != nil {
		t.Fatal(err)
	}
	_, other, _, err := services.Credentials.Register(context.Background(), "two@example.com", "correct horse battery", "Two")
	if err != nil {
		t.Fatal(err)
	}
	r := New(nil, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), services, false)
	for _, method := range []string{"GET", "PUT"} {
		w := resourceRequest(r, method, "/api/v1/profile", "{}", "")
		if w.Code != 401 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := resourceRequest(r, "GET", "/api/v1/profile", "", token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"nickname":null`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = resourceRequest(r, "PUT", "/api/v1/profile", `{"displayName":" New ","nickname":" Nick ","expectedVersion":1}`, token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"version":2`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = resourceRequest(r, "GET", "/api/v1/me", "", token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"displayName":"New"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = resourceRequest(r, "GET", "/api/v1/profile", "", other)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"displayName":"Two"`) || !strings.Contains(w.Body.String(), `"version":1`) {
		t.Fatal("cross-user mutation", w.Body.String())
	}
	for _, body := range []string{
		`{}`, `null`,
		`{"displayName":"New","expectedVersion":2}`,
		`{"displayName":null,"nickname":null,"expectedVersion":2}`,
		`{"displayName":"New","nickname":null,"expectedVersion":null}`,
		`{"displayName":"New","nickname":null,"expectedVersion":2,"userId":"other"}`,
		`{"displayName":"New","nickname":null,"expectedVersion":2,"Exclude":[]}`,
		`{"displayName":"New","nickname":null,"expectedVersion":0}`,
		`{"displayName":"New","nickname":null,"expectedVersion":2} {}`,
		`{"displayName":"New","nickname":"` + strings.Repeat("中", 81) + `","expectedVersion":2}`,
		strings.Repeat(" ", 33<<10) + `{}`,
	} {
		w = resourceRequest(r, "PUT", "/api/v1/profile", body, token)
		if w.Code != 400 {
			t.Fatalf("expected invalid input, got %d", w.Code)
		}
	}
	w = resourceRequest(r, "PUT", "/api/v1/profile", `{"displayName":"Stale","nickname":null,"expectedVersion":1}`, token)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "version_conflict") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = resourceRequest(r, "PUT", "/api/v1/profile", `{"displayName":"New","nickname":null,"expectedVersion":2}`, token)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var p map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || p["nickname"] != nil || p["version"] != float64(3) {
		t.Fatal(p, err)
	}
	for _, origin := range []string{"", "https://evil.example"} {
		req := httptest.NewRequest("PUT", "/api/v1/profile", strings.NewReader(`{}`))
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		if origin != "" {
			req.Header.Set("Origin", origin)
			req.Header.Set("X-PlayPair-Request", "1")
		}
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 400 && w.Code != 403 {
			t.Fatal("write protection", w.Code)
		}
	}
}
