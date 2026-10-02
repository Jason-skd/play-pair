package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/Jason-skd/play-pair/internal/outing"
	"github.com/Jason-skd/play-pair/internal/users"
	"github.com/google/uuid"
)

// The stub captures protocol commands only; PostgreSQL tests own business semantics.
type outingStub struct {
	calls   int
	actor   users.ID
	create  outing.Create
	respond outing.Respond
	err     error
}

func (r *outingStub) CreateOuting(_ context.Context, a users.ID, c outing.Create) (outing.Outing, bool, error) {
	r.calls++
	r.actor = a
	r.create = c
	return outing.Outing{ID: 9, Dates: c.Dates, Timezone: c.Timezone}, true, r.err
}
func (r *outingStub) GetOuting(_ context.Context, a users.ID, id outing.ID) (outing.Outing, error) {
	r.calls++
	r.actor = a
	return outing.Outing{ID: id, Timezone: "Asia/Shanghai"}, r.err
}
func (r *outingStub) RespondOuting(_ context.Context, a users.ID, c outing.Respond) (outing.Invitation, error) {
	r.calls++
	r.actor = a
	r.respond = c
	return outing.Invitation{OutingID: c.OutingID, MemberID: a, Phase: c.Phase, Status: c.Status, Version: 2, Input: c.Input}, r.err
}
func (r *outingStub) ListOutingInvitations(_ context.Context, a users.ID, _ outing.ID, _ outing.Cursor, _ int) (outing.Page, error) {
	r.calls++
	r.actor = a
	return outing.Page{}, r.err
}
func outingHTTPFixture(t *testing.T) (func() http.Handler, *outingStub, string) {
	t.Helper()
	auth := newAuthRepo()
	services := newServices(auth, time.Now)
	_, token, _, err := services.Credentials.Register(context.Background(), "outing@example.com", "correct horse battery", "Member")
	if err != nil {
		t.Fatal(err)
	}
	repo := &outingStub{}
	services.Outings = outing.New(repo, time.Now)
	return func() http.Handler {
		return New(nil, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), services, false)
	}, repo, token
}
func TestOutingHTTPProtocol(t *testing.T) {
	router, repo, token := outingHTTPFixture(t)
	r := router()
	body := `{"dates":["2030-10-05","2030-10-03"],"timezone":"Asia/Shanghai","durationMinutes":120}`
	w := groupHTTPRequest(r, "POST", "/api/v1/groups/7/outings", body, token, uuid.NewString())
	if w.Code != 201 || repo.create.GroupID != 7 || repo.create.Dates[0] != "2030-10-03" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = groupHTTPRequest(r, "GET", "/api/v1/outings/9", "", token, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"decision"`) || !strings.Contains(w.Body.String(), `"arrangement":null`) {
		t.Fatal(w.Code, w.Body.String())
	}
	accepted := `{"status":"accepted","expectedVersion":1,"input":{"maxBudgetFen":0,"requiresWheelchairAccess":false,"availability":[{"date":"2030-10-03","startTime":"08:00","endTime":"24:00"}]}}`
	w = groupHTTPRequest(r, "PUT", "/api/v1/outings/9/invitations/decision", accepted, token, "")
	if w.Code != 200 || repo.respond.Input == nil || repo.respond.Input.Availability[0].EndMinute != 1440 || repo.respond.Input.MaxBudgetFen != 0 || repo.respond.Input.RequiresWheelchairAccess {
		t.Fatal(w.Code, w.Body.String())
	}
	w = groupHTTPRequest(r, "PUT", "/api/v1/outings/9/invitations/confirmation", `{"status":"accepted","expectedVersion":1}`, token, "")
	if w.Code != 200 || repo.respond.Input != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/v1/me/invitations", "/api/v1/outings/9/invitations"} {
		w = groupHTTPRequest(r, "GET", path, "", token, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"invitations":[]`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/v1/groups/7/outings", strings.Replace(body, `"timezone":"Asia/Shanghai",`, "", 1)},
		{"POST", "/api/v1/groups/7/outings", strings.Replace(body, `"dates":`, `"dates":[],"dates":`, 1)},
		{"PUT", "/api/v1/outings/9/invitations/decision", strings.Replace(accepted, `"maxBudgetFen":0,`, "", 1)},
		{"PUT", "/api/v1/outings/9/invitations/decision", strings.Replace(accepted, `"requiresWheelchairAccess":false`, `"requiresWheelchairAccess":null`, 1)},
		{"PUT", "/api/v1/outings/9/invitations/decision", strings.Replace(accepted, `"startTime":"08:00"`, `"startTime":"08:00","startTime":"09:00"`, 1)},
		{"PUT", "/api/v1/outings/9/invitations/decision", strings.Replace(accepted, "08:00", "08:15", 1)},
		{"PUT", "/api/v1/outings/9/invitations/decision", strings.Replace(accepted, `"date":`, `"Date":`, 1)},
		{"PUT", "/api/v1/outings/9/invitations/decision", `{"status":"accepted","expectedVersion":1}`},
		{"PUT", "/api/v1/outings/9/invitations/confirmation", accepted},
		{"PUT", "/api/v1/outings/9/invitations/decision", strings.Replace(accepted, `"accepted"`, `"rejected"`, 1)},
		{"PUT", "/api/v1/outings/9/invitations/decision", `{"status":"rejected","expectedVersion":1,"input":null}`},
		{"PUT", "/api/v1/outings/9/invitations/invalid", `{"status":"rejected","expectedVersion":1}`},
		{"GET", "/api/v1/me/invitations?limit=0", ""},
		{"GET", "/api/v1/me/invitations?limit=1&limit=2", ""},
		{"GET", "/api/v1/me/invitations?cursor=garbage", ""},
		{"GET", "/api/v1/outings/0/invitations", ""},
	} {
		before := repo.calls
		w = groupHTTPRequest(router(), c.method, c.path, c.body, token, uuid.NewString())
		if w.Code != 400 || repo.calls != before {
			t.Fatal(c, w.Code, w.Body.String(), repo.calls-before)
		}
	}
	for _, path := range []string{"/api/v1/outings/9", "/api/v1/outings/9/invitations", "/api/v1/me/invitations"} {
		w = groupHTTPRequest(r, "GET", path, "", "", "")
		if w.Code != 401 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
func TestOutingHTTPErrorsAndProtection(t *testing.T) {
	router, repo, token := outingHTTPFixture(t)
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{outing.ErrInvalidInput, 400, "invalid_input"}, {outing.ErrInvalidAvailability, 400, "invalid_availability"}, {outing.ErrForbidden, 403, "forbidden"}, {errs.NotFound, 404, "not_found"}, {errs.VersionConflict, 409, "version_conflict"}, {outing.ErrPhaseConflict, 409, "phase_conflict"}, {outing.ErrArchived, 409, "outing_archived"}, {outing.ErrIdempotencyConflict, 409, "idempotency_conflict"}, {context.DeadlineExceeded, 503, "dependency_unavailable"}, {errors.New("SECRET SQL"), 500, "internal_error"},
	} {
		repo.err = c.err
		w := groupHTTPRequest(router(), "GET", "/api/v1/outings/9", "", token, "")
		if w.Code != c.status || !strings.Contains(w.Body.String(), c.code) || strings.Contains(w.Body.String(), "SECRET") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	repo.err = nil
	r := router()
	for i := 0; i < 21; i++ {
		w := groupHTTPRequest(r, "PUT", "/api/v1/outings/9/invitations/confirmation", `{"status":"accepted","expectedVersion":1}`, token, "")
		want := 200
		if i == 20 {
			want = 429
		}
		if w.Code != want {
			t.Fatal(i, w.Code, w.Body.String())
		}
	}
}

func TestOutingRequestLimits(t *testing.T) {
	router, repo, token := outingHTTPFixture(t)
	for _, body := range []string{
		`{"dates":[null],"timezone":"UTC","durationMinutes":30}`,
		`{"dates":["2030-10-03"],"timezone":"UTC","durationMinutes":31}`,
		`{"dates":["2030-10-03"],"timezone":"` + strings.Repeat("x", 64<<10) + `","durationMinutes":30}`,
		`{"dates":["2030-10-03"],"timezone":"Local","durationMinutes":30}`,
	} {
		w := groupHTTPRequest(router(), "POST", "/api/v1/groups/7/outings", body, token, uuid.NewString())
		if w.Code != 400 || repo.calls != 0 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestOutingBrowserProtection(t *testing.T) {
	router, repo, token := outingHTTPFixture(t)
	for _, c := range []struct{ header, origin string }{{"", ""}, {"1", "https://untrusted.example"}} {
		req := httptest.NewRequest("PUT", "http://example.com/api/v1/outings/9/invitations/confirmation", strings.NewReader(`{"status":"accepted","expectedVersion":1}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-PlayPair-Request", c.header)
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		w := httptest.NewRecorder()
		router().ServeHTTP(w, req)
		if (w.Code != 400 && w.Code != 403) || repo.calls != 0 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
