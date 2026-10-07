package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/Jason-skd/play-pair/internal/outing"
)

func (q *outingProtocolStore) AcceptedInputs(context.Context, outing.ID) ([]outing.ParticipantInput, error) {
	if q.member.Input == nil {
		return []outing.ParticipantInput{}, nil
	}
	return []outing.ParticipantInput{{MemberID: q.member.MemberID, Input: *q.member.Input}}, nil
}

func TestDecisionInputsHTTP(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) }
	services := newServices(newAuthRepo(), now)
	account, token, _, err := services.Credentials.Register(context.Background(), "decision@example.com", "correct horse battery", "Organizer")
	if err != nil {
		t.Fatal(err)
	}
	q := &outingProtocolStore{o: outing.Outing{ID: 41, InitiatorID: account.ID, Timezone: "Asia/Shanghai", Plan: outing.Plan{Dates: []outing.Date{"2026-10-10"}, DurationMinutes: 60}}, member: outing.Member{MemberID: account.ID, Input: &outing.MemberInput{MaxBudgetFen: 0, RequiresWheelchairAccess: false, Availability: []outing.Availability{{Date: "2026-10-10", StartMinute: 600, EndMinute: 720}}}}}
	services.Outings = outing.New(q, now)
	router := New(nil, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), services, false)
	request := func(path string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if authenticated {
			r.Header.Set("Cookie", "playpair_session="+token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	if w := request("/api/v1/outings/41/decision-inputs", false); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("/api/v1/outings/41/decision-inputs?actor=1", true); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := request("/api/v1/outings/41/decision-inputs", true)
	var result generated.DecisionInputs
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.MaxBudgetFen != 0 || result.RequiresWheelchairAccess || len(result.Constraints) != 3 || len(result.CommonWindows) != 1 || q.saves != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	wantFields := []string{"outingId", "timezone", "durationMinutes", "maxBudgetFen", "requiresWheelchairAccess", "commonWindows", "constraints"}
	if len(fields) != len(wantFields) {
		t.Fatal("unexpected aggregation fields", w.Body.String())
	}
	for _, field := range wantFields {
		if _, ok := fields[field]; !ok {
			t.Fatal("missing aggregation field", field)
		}
	}
	if result.CommonWindows[0].StartsAt.Format(time.RFC3339) != "2026-10-10T10:00:00+08:00" || result.CommonWindows[0].EndsAt.Format(time.RFC3339) != "2026-10-10T12:00:00+08:00" || len(result.Constraints[0].Sources) != 1 || result.Constraints[0].Sources[0].MemberId != int64(account.ID) {
		t.Fatal(result)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Header())
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{outing.ErrNoParticipants, 409, "no_participants"}, {outing.ErrNoCommonTime, 409, "no_common_time"},
		{outing.ErrAggregationInput, 422, "aggregation_input_invalid"}, {outing.ErrAggregationCapacity, 422, "aggregation_capacity_exceeded"},
		{outing.ErrForbidden, 403, "forbidden"}, {outing.ErrPhase, 409, "phase_conflict"},
	} {
		q.err = tc.err
		w = request("/api/v1/outings/41/decision-inputs", true)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	q.err = nil
	q.o.Arrangement = &outing.Arrangement{}
	if w := request("/api/v1/outings/41/decision-inputs", true); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	q.o.Arrangement = nil
	q.o.InitiatorID++
	if w := request("/api/v1/outings/41/decision-inputs", true); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
}
