package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/Jason-skd/play-pair/internal/group"
	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/google/uuid"
)

// This stub only captures protocol inputs and returns explicit outcomes. Real
// membership authorization, idempotency and pagination are tested with PostgreSQL.
type groupStub struct {
	g          group.Group
	page       group.Page
	err        error
	created    bool
	calls      int
	actor      users.ID
	id         group.ID
	key        uuid.UUID
	name, code string
	after      users.ID
	limit      int
}

func (r *groupStub) CreateGroup(_ context.Context, a users.ID, k uuid.UUID, n string) (group.Group, bool, error) {
	r.calls++
	r.actor = a
	r.key = k
	r.name = n
	return r.g, r.created, r.err
}
func (r *groupStub) JoinGroup(_ context.Context, a users.ID, c string) (group.Group, error) {
	r.calls++
	r.actor = a
	r.code = c
	return r.g, r.err
}
func (r *groupStub) GetGroup(_ context.Context, a users.ID, id group.ID) (group.Group, error) {
	r.calls++
	r.actor = a
	r.id = id
	return r.g, r.err
}
func (r *groupStub) GetGroupMembers(_ context.Context, a users.ID, id group.ID, after users.ID, limit int) (group.Page, error) {
	r.calls++
	r.actor = a
	r.id = id
	r.after = after
	r.limit = limit
	return r.page, r.err
}
func groupHTTPRequest(handler http.Handler, method, path, body, token, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PlayPair-Request", "1")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if token != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}
func TestGroupHTTPProtocol(t *testing.T) {
	auth := newAuthRepo()
	services := newServices(auth, time.Now)
	account, token, _, err := services.Credentials.Register(context.Background(), "group@example.com", "correct horse battery", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	repo := &groupStub{g: group.Group{ID: 41, Name: "Weekend", JoinCode: "ABCDEFGHIJKLMNOP", InitiatorID: account.ID}, created: true}
	services.Groups = group.New(repo)
	router := func() http.Handler {
		return New(nil, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), services, false)
	}
	key := uuid.NewString()
	r := router()
	w := groupHTTPRequest(r, "POST", "/api/v1/groups", `{"name":" Weekend "}`, token, key)
	if w.Code != 201 || repo.actor != account.ID || repo.name != "Weekend" || repo.key.String() != key || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "creationKey") || strings.Contains(w.Body.String(), key) {
		t.Fatal("private key exposed")
	}
	repo.created = false
	w = groupHTTPRequest(r, "POST", "/api/v1/groups", `{"name":"Weekend"}`, token, key)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = groupHTTPRequest(r, "POST", "/api/v1/groups/join", `{"joinCode":" abcdefghijklmnop "}`, token, "")
	if w.Code != 200 || repo.code != "ABCDEFGHIJKLMNOP" || repo.actor != account.ID {
		t.Fatal(w.Code, w.Body.String())
	}
	w = groupHTTPRequest(r, "GET", "/api/v1/groups/41", "", token, "")
	if w.Code != 200 || repo.id != 41 {
		t.Fatal(w.Code, w.Body.String())
	}
	nick := "Nick"
	next := users.ID(8)
	repo.page = group.Page{Members: []group.Member{{MemberID: 8, Role: "member", DisplayName: "Name", Nickname: &nick}}, NextAfterMemberID: &next}
	w = groupHTTPRequest(r, "GET", "/api/v1/groups/41/members?afterMemberId=7&limit=1", "", token, "")
	if w.Code != 200 || repo.after != 7 || repo.limit != 1 || !strings.Contains(w.Body.String(), `"nextAfterMemberId":8`) || strings.Contains(w.Body.String(), "email") || strings.Contains(w.Body.String(), "preference") {
		t.Fatal(w.Code, w.Body.String())
	}
	repo.page = group.Page{}
	w = groupHTTPRequest(r, "GET", "/api/v1/groups/41/members", "", token, "")
	if w.Code != 200 || repo.after != 0 || repo.limit != 50 || !strings.Contains(w.Body.String(), `"members":[]`) || !strings.Contains(w.Body.String(), `"nextAfterMemberId":null`) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/api/v1/groups", `{}`}, {"POST", "/api/v1/groups", `null`}, {"POST", "/api/v1/groups", `{"name":null}`},
		{"POST", "/api/v1/groups", `{"name":"x","name":"y"}`}, {"POST", "/api/v1/groups", `{"Name":"x"}`},
		{"POST", "/api/v1/groups", `{"name":"x","actor":9}`}, {"POST", "/api/v1/groups", `{"name":"x","role":"initiator"}`},
		{"POST", "/api/v1/groups", `{"name":"x"} {}`}, {"POST", "/api/v1/groups", `{"name":"\u0000"}`},
		{"POST", "/api/v1/groups", `{"name":"` + string([]byte{255}) + `"}`}, {"POST", "/api/v1/groups", `{"name":"` + strings.Repeat("中", 81) + `"}`},
		{"POST", "/api/v1/groups", strings.Repeat(" ", 8<<10) + `{"name":"x"}`},
		{"POST", "/api/v1/groups/join", `{}`}, {"POST", "/api/v1/groups/join", `{"joinCode":null}`}, {"POST", "/api/v1/groups/join", `{"joinCode":"invalid"}`},
		{"POST", "/api/v1/groups/join", `{"joinCode":"ABCDEFGHIJKLMNOP","joinCode":"ABCDEFGHIJKLMNOP"}`},
		{"POST", "/api/v1/groups/join", `{"joinCode":"ABCDEFGHIJKLMNOP","memberId":8}`},
		{"POST", "/api/v1/groups/join", strings.Repeat(" ", 8<<10) + `{}`},
		{"GET", "/api/v1/groups/0", ""}, {"GET", "/api/v1/groups/-1", ""}, {"GET", "/api/v1/groups/abc", ""}, {"GET", "/api/v1/groups/9223372036854775808", ""},
		{"GET", "/api/v1/groups/41/members?limit=0", ""}, {"GET", "/api/v1/groups/41/members?limit=101", ""},
		{"GET", "/api/v1/groups/41/members?limit=x", ""}, {"GET", "/api/v1/groups/41/members?limit=1&limit=2", ""},
		{"GET", "/api/v1/groups/41/members?afterMemberId=-1", ""}, {"GET", "/api/v1/groups/41/members?afterMemberId=0", ""},
		{"GET", "/api/v1/groups/41/members?afterMemberId=9223372036854775808", ""}, {"GET", "/api/v1/groups/41/members?limit=", ""},
		{"GET", "/api/v1/groups/41/members?actor=8", ""},
		{"GET", "/api/v1/groups/41/members?limit=%zz", ""},
		{"GET", "/api/v1/groups/41/members?limit=1;limit=2", ""},
	} {
		before := repo.calls
		w := groupHTTPRequest(router(), tc.method, tc.path, tc.body, token, key)
		if w.Code != 400 || repo.calls != before || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s: %d %s calls=%d", tc.method, tc.path, w.Code, w.Body.String(), repo.calls-before)
		}
	}
	for _, key := range []string{"", "not-a-uuid"} {
		before := repo.calls
		w := groupHTTPRequest(router(), "POST", "/api/v1/groups", `{"name":"x"}`, token, key)
		if w.Code != 400 || repo.calls != before {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
func TestGroupHTTPAuthErrorsProtectionAndLimits(t *testing.T) {
	auth := newAuthRepo()
	services := newServices(auth, time.Now)
	_, token, _, err := services.Credentials.Register(context.Background(), "group@example.com", "correct horse battery", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	_, other, _, err := services.Credentials.Register(context.Background(), "other@example.com", "correct horse battery", "Other")
	if err != nil {
		t.Fatal(err)
	}
	repo := &groupStub{g: group.Group{JoinCode: "ABCDEFGHIJKLMNOP"}, created: true}
	services.Groups = group.New(repo)
	var logs bytes.Buffer
	r := New(nil, time.Second, slog.New(slog.NewTextHandler(&logs, nil)), services, false)
	key := uuid.NewString()
	routes := []struct{ method, path, body string }{{"POST", "/api/v1/groups", `{"name":"x"}`}, {"POST", "/api/v1/groups/join", `{"joinCode":"ABCDEFGHIJKLMNOP"}`}, {"GET", "/api/v1/groups/1", ""}, {"GET", "/api/v1/groups/1/members", ""}}
	for _, rt := range routes {
		for _, tok := range []string{"", "invalid"} {
			before := repo.calls
			w := groupHTTPRequest(r, rt.method, rt.path, rt.body, tok, key)
			if w.Code != 401 || repo.calls != before || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(w.Code, w.Body.String())
			}
		}
	}
	for _, rt := range routes[:2] {
		for _, mode := range []string{"origin", "fetch-site", "header", "content-type"} {
			req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(rt.body))
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
			req.Header.Set("Idempotency-Key", key)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-PlayPair-Request", "1")
			switch mode {
			case "origin":
				req.Header.Set("Origin", "https://evil.example")
			case "fetch-site":
				req.Header.Set("Sec-Fetch-Site", "cross-site")
			case "header":
				req.Header.Del("X-PlayPair-Request")
			case "content-type":
				req.Header.Set("Content-Type", "text/plain")
			}
			before := repo.calls
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if (w.Code != 400 && w.Code != 403) || repo.calls != before {
				t.Fatal(mode, w.Code, w.Body.String())
			}
		}
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{group.ErrForbidden, 403, "forbidden"}, {errs.NotFound, 404, "not_found"}, {group.ErrIdempotencyConflict, 409, "idempotency_conflict"}, {context.DeadlineExceeded, 503, "dependency_unavailable"}, {context.Canceled, 503, "dependency_unavailable"}, {fmt.Errorf("%w: SQL ABCDEFGHIJKLMNOP session-secret", errs.DependencyUnavailable), 503, "dependency_unavailable"}, {errors.New("SQL secret"), 500, "internal_error"}} {
		repo.err = tc.err
		w := groupHTTPRequest(r, "GET", "/api/v1/groups/1", "", token, "")
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) || strings.Contains(w.Body.String(), "ABCDEFGHIJKLMNOP") || strings.Contains(w.Body.String(), "SQL") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if strings.Contains(logs.String(), "ABCDEFGHIJKLMNOP") || strings.Contains(logs.String(), "session-secret") || strings.Contains(logs.String(), "SQL") {
		t.Fatal("leaked secrets", logs.String())
	}
	repo.err = nil
	// Fresh limiter makes the exact independent per-actor/per-operation quota observable.
	r = New(nil, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), services, false)
	for _, rt := range routes[:2] {
		for i := range 20 {
			w := groupHTTPRequest(r, rt.method, rt.path, rt.body, token, key)
			if w.Code >= 400 {
				t.Fatal(i, w.Code, w.Body.String())
			}
		}
		w := groupHTTPRequest(r, rt.method, rt.path, rt.body, token, key)
		if w.Code != 429 || w.Header().Get("Retry-After") == "" {
			t.Fatal(w.Code, w.Body.String())
		}
		w = groupHTTPRequest(r, rt.method, rt.path, rt.body, other, key)
		if w.Code >= 400 {
			t.Fatal("actor limits coupled", w.Code)
		}
	}
	if err := services.Sessions.Logout(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	for _, rt := range routes {
		w := groupHTTPRequest(r, rt.method, rt.path, rt.body, other, key)
		if w.Code != 401 {
			t.Fatal("revoked session allowed", w.Code)
		}
	}
}
