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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/auth"
)

type authRepo struct {
	mu       sync.Mutex
	users    map[string]auth.PasswordCredential
	sessions map[[32]byte]auth.User
	expires  map[[32]byte]time.Time
}

func newAuthRepo() *authRepo {
	return &authRepo{users: map[string]auth.PasswordCredential{}, sessions: map[[32]byte]auth.User{}, expires: map[[32]byte]time.Time{}}
}
func (r *authRepo) Register(_ context.Context, u auth.PasswordCredential, h [32]byte, exp time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.users[u.Email]; ok {
		return auth.ErrEmailConflict
	}
	r.users[u.Email] = u
	r.sessions[h] = u.User
	r.expires[h] = exp
	return nil
}
func (r *authRepo) FindPasswordCredentialByEmail(_ context.Context, email string) (auth.PasswordCredential, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[email]
	if !ok {
		return auth.PasswordCredential{}, auth.ErrNotFound
	}
	return u, nil
}
func (r *authRepo) CreateSession(_ context.Context, id string, h [32]byte, _ time.Time, exp time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, u := range r.users {
		if u.ID == id {
			r.sessions[h] = u.User
			r.expires[h] = exp
			return nil
		}
	}
	return auth.ErrNotFound
}
func (r *authRepo) FindSessionUser(_ context.Context, h [32]byte, now time.Time) (auth.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.sessions[h]
	if !ok || !r.expires[h].After(now) {
		return auth.User{}, auth.ErrNotFound
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
	router := New(func(context.Context) error { return nil }, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), auth.New(repo, time.Now), false)
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
	router := New(func(context.Context) error { return nil }, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), auth.New(newAuthRepo(), time.Now), false)
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
	router := New(func(context.Context) error { return nil }, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), auth.New(newAuthRepo(), time.Now), false)
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

var _ auth.Repository = (*authRepo)(nil)
var _ = errors.Is
