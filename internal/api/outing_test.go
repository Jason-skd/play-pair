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
)

// Only records protocol commands. Persistence and authorization integration use
// the real PostgreSQL repository in internal/db.
type outingProtocolStore struct {
	outing.Store
	o      outing.Outing
	member outing.Member
	input  *outing.MemberInput
	choice outing.Choice
	reset  bool
	saves  int
	err    error
}

func (q *outingProtocolStore) Read(_ context.Context, f func(outing.Store) error) error {
	if q.err != nil {
		return q.err
	}
	return f(q)
}
func (q *outingProtocolStore) Write(ctx context.Context, f func(outing.Store) error) error {
	return q.Read(ctx, f)
}
func (q *outingProtocolStore) LockOuting(context.Context, outing.ID) error { return nil }
func (q *outingProtocolStore) LoadOuting(context.Context, outing.ID) (outing.Outing, error) {
	return q.o, nil
}
func (q *outingProtocolStore) Membership(context.Context, outing.Outing, users.ID) (outing.Membership, error) {
	return outing.Membership{InGroup: true}, nil
}
func (q *outingProtocolStore) GroupMember(context.Context, int64, users.ID) (bool, error) {
	return true, nil
}
func (q *outingProtocolStore) InsertOuting(_ context.Context, actor users.ID, gid int64, zone string, p outing.Plan) (outing.Outing, error) {
	q.saves++
	q.o.InitiatorID = actor
	q.o.GroupID = gid
	q.o.Timezone = zone
	q.o.Plan = p
	return q.o, nil
}
func (q *outingProtocolStore) Member(context.Context, outing.ID, users.ID) (outing.Member, error) {
	return q.member, nil
}
func (q *outingProtocolStore) SaveDecision(_ context.Context, _ outing.ID, _ users.ID, c outing.Choice, in *outing.MemberInput) error {
	q.saves++
	q.choice = c
	q.input = in
	return nil
}
func (q *outingProtocolStore) SaveConfirmation(_ context.Context, _ outing.ID, _ users.ID, c outing.Choice) error {
	q.saves++
	q.choice = c
	return nil
}
func (q *outingProtocolStore) SavePlan(_ context.Context, _ outing.ID, p outing.Plan) error {
	q.saves++
	q.o.Plan = p
	return nil
}
func (q *outingProtocolStore) SaveArrangement(_ context.Context, _ outing.ID, a outing.Arrangement, _ bool, reset bool) error {
	q.saves++
	q.o.Arrangement = &a
	q.reset = reset
	return nil
}
func (q *outingProtocolStore) ListResponses(context.Context, outing.Outing, outing.ResponseFilter) (outing.ResponsePage, error) {
	return outing.ResponsePage{Responses: []outing.Summary{{MemberID: q.o.InitiatorID, Phase: outing.Decision, Status: outing.Accept}}, Next: &outing.ResponseCursor{MemberID: q.o.InitiatorID, Phase: outing.Decision}}, nil
}
func (q *outingProtocolStore) ListOutings(context.Context, users.ID, outing.ListFilter) (outing.OutingPage, error) {
	return outing.OutingPage{Outings: []outing.Outing{q.o}}, nil
}

func TestOutingHTTPProtocol(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) }
	services := newServices(newAuthRepo(), now)
	account, token, _, err := services.Credentials.Register(context.Background(), "outing@example.com", "correct horse battery", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	q := &outingProtocolStore{o: outing.Outing{ID: 41, GroupID: 1, InitiatorID: account.ID, Timezone: "UTC", Plan: outing.Plan{Dates: []outing.Date{"2026-10-10"}, DurationMinutes: 60}}, member: outing.Member{MemberID: account.ID}}
	services.Outings = outing.New(q, now)
	router := func() http.Handler {
		return New(nil, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), services, false)
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		return groupHTTPRequest(router(), method, path, body, token, "")
	}
	accept := `{"choice":"accept","input":{"maxBudgetFen":0,"requiresWheelchairAccess":false,"availability":[{"date":"2026-10-10","startTime":"10:00","endTime":"12:00"}]}}`
	w := request("POST", "/api/v1/groups/1/outings", `{"dates":["2026-10-10"],"durationMinutes":60,"timezone":"UTC"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("PUT", "/api/v1/outings/41/responses/decision", accept)
	if w.Code != 204 || q.choice != outing.Accept || q.input == nil || q.input.MaxBudgetFen != 0 || q.input.RequiresWheelchairAccess {
		t.Fatal(w.Code, w.Body.String(), q.input)
	}
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/api/v1/groups/1/outings", `null`},
		{"POST", "/api/v1/groups/1/outings", `{"dates":["2026-10-10"],"durationMinutes":60,"timezone":"UTC","timezone":"UTC"}`},
		{"PUT", "/api/v1/outings/41", `{"dates":["2026-10-10"],"durationMinutes":60,"timezone":"UTC"}`},
		{"PUT", "/api/v1/outings/41/responses/decision", `{"choice":"accept"}`},
		{"PUT", "/api/v1/outings/41/responses/decision", `{"choice":"deny","input":null}`},
		{"PUT", "/api/v1/outings/41/responses/decision", strings.Replace(accept, `"maxBudgetFen":0,`, "", 1)},
		{"PUT", "/api/v1/outings/41/responses/decision", strings.Replace(accept, `"requiresWheelchairAccess":false`, `"requiresWheelchairAccess":null`, 1)},
		{"PUT", "/api/v1/outings/41/responses/decision", strings.Replace(accept, `"requiresWheelchairAccess":false`, `"requiresWheelchairAccess":false,"requiresWheelchairAccess":true`, 1)},
		{"PUT", "/api/v1/outings/41/responses/decision", strings.Replace(accept, `"startTime":"10:00"`, `"StartTime":"10:00"`, 1)},
		{"PUT", "/api/v1/outings/41/responses/decision", strings.Replace(accept, `"date":"2026-10-10"`, `"date":"2026-10-11"`, 1)},
		{"PUT", "/api/v1/outings/41/responses/decision", strings.Replace(accept, `"10:00"`, `"10:15"`, 1)},
		{"GET", "/api/v1/outings?limit=0", ""},
		{"GET", "/api/v1/outings?groupId=0", ""},
		{"GET", "/api/v1/outings?limit=1&limit=2", ""},
		{"GET", "/api/v1/outings/41/responses?phase=unknown", ""},
		{"GET", "/api/v1/outings/41/responses?status=decline", ""},
		{"GET", "/api/v1/outings/41/responses?cursor=invalid", ""},
		{"GET", "/api/v1/outings/41/responses?actor=1", ""},
	} {
		before := q.saves
		w := request(tc.method, tc.path, tc.body)
		if w.Code != 400 || q.saves != before {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	arrangement := `{"startsAt":"2026-10-10T10:00:00Z","endsAt":"2026-10-10T11:00:00Z","place":"Park","activity":"Walk"}`
	w = request("POST", "/api/v1/outings/41/arrangement", arrangement)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("PUT", "/api/v1/outings/41/arrangement", arrangement)
	if w.Code != 400 {
		t.Fatal("omitted reset accepted", w.Code)
	}
	for _, reset := range []string{"false", "true"} {
		w = request("PUT", "/api/v1/outings/41/arrangement", strings.TrimSuffix(arrangement, "}")+`,"resetResponses":`+reset+`}`)
		if w.Code != 204 || q.reset != (reset == "true") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w = request("PUT", "/api/v1/outings/41/responses/confirmation", `{"choice":"deny"}`)
	if w.Code != 204 || q.choice != outing.Deny {
		t.Fatal(w.Code, w.Body.String())
	}
	c := outing.Accept
	q.member.Decision = &c
	q.member.Input = q.input
	w = request("GET", "/api/v1/outings/41", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"input":`) || !strings.Contains(w.Body.String(), `"arrangement":`) || !strings.Contains(w.Body.String(), `"warnings":[]`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("GET", "/api/v1/outings/41/responses", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "maxBudgetFen") || !strings.Contains(w.Body.String(), "nextCursor") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{outing.ErrForbidden, 403, "forbidden"}, {outing.ErrPhase, 409, "phase_conflict"}, {outing.ErrArchived, 409, "outing_archived"},
		{errs.NotFound, 404, "not_found"}, {errs.DependencyUnavailable, 503, "dependency_unavailable"}, {errors.New("private SQL"), 500, "internal_error"},
	} {
		q.err = tc.err
		w = request("GET", "/api/v1/outings/41", "")
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) || strings.Contains(w.Body.String(), "private SQL") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	q.err = nil
	for _, path := range []string{"/api/v1/outings", "/api/v1/outings/41", "/api/v1/outings/41/responses", "/api/v1/outings/41/members/1"} {
		w := groupHTTPRequest(router(), "GET", path, "", "", "")
		if w.Code != 401 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(path, w.Code)
		}
	}
	req := httptest.NewRequest("PUT", "/api/v1/outings/41/responses/confirmation", strings.NewReader(`{"choice":"accept"}`))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router().ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatal("missing browser header accepted", w.Code)
	}
}
