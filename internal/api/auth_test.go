package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/credentials"
	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/Jason-skd/play-pair/internal/profile"
	"github.com/Jason-skd/play-pair/internal/session"
	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/gin-gonic/gin"
)

type authRepo struct {
	profiles map[users.ID]profile.Profile
	mu       sync.Mutex
	users    map[string]credentials.Registration
	sessions map[[32]byte]users.Account
	expires  map[[32]byte]time.Time
	lookups  int
	findErr  error
}

func newAuthRepo() *authRepo {
	return &authRepo{profiles: map[users.ID]profile.Profile{}, users: map[string]credentials.Registration{}, sessions: map[[32]byte]users.Account{}, expires: map[[32]byte]time.Time{}}
}
func (r *authRepo) Register(_ context.Context, u credentials.Registration, record session.Record) error {
	h, exp := record.TokenHash, record.ExpiresAt
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.users[u.Email]; ok {
		return credentials.ErrEmailConflict
	}
	r.users[u.Email] = u
	r.profiles[u.ID] = profile.Profile{UserID: u.ID, DisplayName: u.DisplayName, Version: 1}
	r.sessions[h] = u.Account
	r.expires[h] = exp
	return nil
}
func (r *authRepo) FindPasswordCredentialByEmail(_ context.Context, email string) (credentials.PasswordCredential, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[email]
	if !ok {
		return credentials.PasswordCredential{}, credentials.ErrNotFound
	}
	return u.PasswordCredential, nil
}
func (r *authRepo) CreateSession(_ context.Context, record session.Record) error {
	id, h, exp := record.UserID, record.TokenHash, record.ExpiresAt
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, u := range r.users {
		if u.ID == id {
			r.sessions[h] = u.Account
			r.expires[h] = exp
			return nil
		}
	}
	return credentials.ErrNotFound
}
func (r *authRepo) FindSessionUser(_ context.Context, h [32]byte, now time.Time) (users.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lookups++
	if r.findErr != nil {
		return users.Account{}, r.findErr
	}
	u, ok := r.sessions[h]
	if !ok || !r.expires[h].After(now) {
		return users.Account{}, session.ErrNotFound
	}
	return u, nil
}
func (r *authRepo) DeleteSession(_ context.Context, h [32]byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, h)
	delete(r.expires, h)
	return nil
}

func TestAuthenticationHTTPFlow(t *testing.T) {
	repo := newAuthRepo()
	router := New(func(context.Context) error { return nil }, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), newServices(repo, time.Now), false)
	ts := httptest.NewServer(router)
	defer ts.Close()
	jar1, _ := cookiejar.New(nil)
	client1 := &http.Client{Jar: jar1}
	register := doJSON(t, client1, ts.URL+"/api/v1/auth/register", `{"email":"USER@example.com","password":"correct horse battery","displayName":" User "}`, true)
	if register.StatusCode != http.StatusCreated {
		t.Fatalf("register: %d %s", register.StatusCode, readBody(register))
	}
	cookies := register.Cookies()
	register.Body.Close()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].Secure {
		t.Fatalf("cookie: %#v", cookies)
	}
	me := do(t, client1, http.MethodGet, ts.URL+"/api/v1/me", "", false)
	if me.StatusCode != http.StatusOK {
		t.Fatalf("me: %d %s", me.StatusCode, readBody(me))
	}
	var user map[string]any
	if err := json.NewDecoder(me.Body).Decode(&user); err != nil {
		t.Fatal(err)
	}
	me.Body.Close()
	if user["email"] != "user@example.com" || user["displayName"] != "User" {
		t.Fatalf("user: %#v", user)
	}

	jar2, _ := cookiejar.New(nil)
	client2 := &http.Client{Jar: jar2}
	login := doJSON(t, client2, ts.URL+"/api/v1/auth/login", `{"email":"user@example.com","password":"correct horse battery"}`, true)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login: %d %s", login.StatusCode, readBody(login))
	}
	login.Body.Close()
	logout := do(t, client1, http.MethodPost, ts.URL+"/api/v1/auth/logout", "", true)
	if logout.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: %d %s", logout.StatusCode, readBody(logout))
	}
	logout.Body.Close()
	if resp := do(t, client1, http.MethodGet, ts.URL+"/api/v1/me", "", false); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked: %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	if resp := do(t, client2, http.MethodGet, ts.URL+"/api/v1/me", "", false); resp.StatusCode != http.StatusOK {
		t.Fatalf("second session: %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
}

func TestAuthenticationHTTPRejections(t *testing.T) {
	router := New(func(context.Context) error { return nil }, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), newServices(newAuthRepo(), time.Now), false)
	ts := httptest.NewServer(router)
	defer ts.Close()
	client := ts.Client()
	if resp := doJSON(t, client, ts.URL+"/api/v1/auth/register", `{"email":"a@example.com","password":"correct horse battery","displayName":"A"}`, false); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing header: %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/auth/register", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PlayPair-Request", "1")
	req.Header.Set("Origin", "https://evil.example")
	if resp, err := client.Do(req); err != nil {
		t.Fatal(err)
	} else if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross origin: %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	if resp := doJSON(t, client, ts.URL+"/api/v1/auth/login", `{"email":"missing@example.com","password":"correct horse battery"}`, true); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("credentials: %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	if resp := do(t, client, http.MethodGet, ts.URL+"/api/v1/me", "", false); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
}

func TestLoginRateLimit(t *testing.T) {
	router := New(func(context.Context) error { return nil }, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), newServices(newAuthRepo(), time.Now), false)
	ts := httptest.NewServer(router)
	defer ts.Close()
	for i := 0; i < 11; i++ {
		resp := doJSON(t, ts.Client(), ts.URL+"/api/v1/auth/login", `{"email":"same@example.com","password":"correct horse battery"}`, true)
		if i < 10 && resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i, resp.StatusCode)
		}
		if i == 10 && (resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "") {
			t.Fatalf("rate limit: %d %q", resp.StatusCode, resp.Header.Get("Retry-After"))
		}
		resp.Body.Close()
	}
}

func TestLoginRateLimitNormalizesEmail(t *testing.T) {
	r := New(nil, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), newServices(newAuthRepo(), time.Now), false)
	for i := 0; i <= loginEmailLimit; i++ {
		body := `{"email":"same@example.com","password":"correct horse battery"}`
		if i%2 == 0 {
			body = `{"email":" SAME@EXAMPLE.COM ","password":"correct horse battery"}`
		}
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-PlayPair-Request", "1")
		r.ServeHTTP(w, req)
		if i < loginEmailLimit && w.Code != 401 {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
		if i == loginEmailLimit {
			retry, err := strconv.Atoi(w.Header().Get("Retry-After"))
			if w.Code != 429 || err != nil || retry < 1 || retry > 900 {
				t.Fatalf("limit: %d retry=%q", w.Code, w.Header().Get("Retry-After"))
			}
		}
	}
}

func TestMeAuthenticationBoundary(t *testing.T) {
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, cookie            string
		expires                 time.Duration
		revoked                 bool
		findErr                 error
		wantStatus, wantLookups int
		wantCode                string
	}{
		{"valid", "valid", time.Hour, false, nil, 200, 1, ""},
		{"missing", "", time.Hour, false, nil, 401, 0, "unauthenticated"},
		{"malformed", "malformed", time.Hour, false, nil, 401, 0, "unauthenticated"},
		{"expired", "valid", 0, false, nil, 401, 1, "unauthenticated"},
		{"revoked", "valid", time.Hour, true, nil, 401, 1, "unauthenticated"},
		{"dependency", "valid", time.Hour, false, errs.DependencyUnavailable, 503, 1, "dependency_unavailable"},
		{"internal", "valid", time.Hour, false, errors.New("secret"), 500, 1, "internal_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newAuthRepo()
			service := newServices(repo, func() time.Time { return now })
			user, token, _, err := service.Credentials.Register(context.Background(), "user@example.com", "correct horse battery", "User")
			if err != nil {
				t.Fatal(err)
			}
			digest := [32]byte{}
			for hash := range repo.sessions {
				digest = hash
			}
			repo.expires[digest] = now.Add(tc.expires)
			if tc.revoked {
				delete(repo.sessions, digest)
			}
			repo.findErr = tc.findErr
			var logs bytes.Buffer
			router := New(func(context.Context) error { return nil }, time.Second, slog.New(slog.NewTextHandler(&logs, nil)), service, true)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
			if tc.cookie != "" {
				if tc.cookie != "malformed" {
					tc.cookie = token
				}
				req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tc.cookie})
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.wantStatus || repo.lookups != tc.wantLookups {
				t.Fatalf("status=%d lookups=%d body=%s", w.Code, repo.lookups, w.Body.String())
			}
			if tc.wantCode != "" && !strings.Contains(w.Body.String(), `"code":"`+tc.wantCode+`"`) {
				t.Fatal(w.Body.String())
			}
			if tc.wantCode == "" && !strings.Contains(w.Body.String(), string(user.ID)) {
				t.Fatal(w.Body.String())
			}
			if strings.Contains(w.Body.String(), token) || strings.Contains(logs.String(), token) || strings.Contains(logs.String(), "secret") {
				t.Fatal("secret leaked")
			}
		})
	}
}

func TestMeMissingMiddlewareIdentity(t *testing.T) {
	s := &server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := gin.New()
	r.GET("/me", s.Me)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/me", nil))
	if w.Code != 500 || !strings.Contains(w.Body.String(), `"code":"internal_error"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func doJSON(t *testing.T, client *http.Client, url, body string, header bool) *http.Response {
	t.Helper()
	return do(t, client, http.MethodPost, url, body, header)
}
func do(t *testing.T, client *http.Client, method, url, body string, header bool) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if header {
		req.Header.Set("X-PlayPair-Request", "1")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
func readBody(resp *http.Response) string {
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return string(b)
}

var _ credentials.Repository = (*authRepo)(nil)
var _ = errors.Is

func newServices(repo *authRepo, now func() time.Time) Services {
	sessions := session.New(repo, now)
	return Services{Credentials: credentials.New(repo, sessions, now), Sessions: sessions, Users: repo, Profiles: profile.New(repo)}
}
func (r *authRepo) UserDetails(_ context.Context, id users.ID) (string, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, u := range r.users {
		if u.ID == id {
			return u.Email, r.profiles[id].DisplayName, nil
		}
	}
	return "", "", credentials.ErrNotFound
}
