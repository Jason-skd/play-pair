package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/Jason-skd/play-pair/internal/preference"
	"github.com/Jason-skd/play-pair/internal/users"
)

func (r *authRepo) GetPreference(_ context.Context, id users.ID) (preference.Preference, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.resourceErr != nil {
		return preference.Preference{}, r.resourceErr
	}
	p, ok := r.preferences[id]
	if !ok {
		return preference.Preference{}, errs.NotFound
	}
	return p, nil
}
func (r *authRepo) UpdatePreference(_ context.Context, id users.ID, c preference.Update) (preference.Preference, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.resourceErr != nil {
		return preference.Preference{}, r.resourceErr
	}
	p := r.preferences[id]
	if p.Version != c.ExpectedVersion {
		return preference.Preference{}, errs.VersionConflict
	}
	p.Soft = c.Soft
	p.HardDefaults = c.HardDefaults
	p.Version++
	r.preferences[id] = p
	return p, nil
}

func TestPreferenceHTTP(t *testing.T) {
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
	var logs strings.Builder
	r := New(nil, time.Second, slog.New(slog.NewTextHandler(&logs, nil)), services, false)
	for _, method := range []string{"GET", "PUT"} {
		if w := resourceRequest(r, method, "/api/v1/preferences", "{}", ""); w.Code != 401 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := resourceRequest(r, "GET", "/api/v1/preferences", "", token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"maxBudgetFen":null`) || !strings.Contains(w.Body.String(), `"requiresWheelchairAccess":null`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	// Round-trip false, true and null independently of the budget; all empty is valid.
	for i, values := range []struct{ budget, access string }{{"0", "false"}, {"10000", "true"}, {"null", "null"}} {
		body := fmt.Sprintf(`{"soft":{"likes":"","neutral":"","dislikes":""},"hardDefaults":{"maxBudgetFen":%s,"requiresWheelchairAccess":%s},"expectedVersion":%d}`, values.budget, values.access, i+1)
		w = resourceRequest(r, "PUT", "/api/v1/preferences", body, token)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"maxBudgetFen":`+values.budget) || !strings.Contains(w.Body.String(), `"requiresWheelchairAccess":`+values.access) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	valid := `{"soft":{"likes":"SECRET_PREFERENCE","neutral":"","dislikes":"太吵"},"hardDefaults":{"maxBudgetFen":null,"requiresWheelchairAccess":null},"expectedVersion":4}`
	for _, body := range []string{
		`{}`, `null`,
		strings.Replace(valid, `"likes":"SECRET_PREFERENCE"`, `"likes":"SECRET_PREFERENCE","Likes":"override"`, 1),
		strings.Replace(valid, `"likes":"SECRET_PREFERENCE"`, `"likes":"SECRET_PREFERENCE","likes":"override"`, 1),
		strings.Replace(valid, `"maxBudgetFen":null,`, "", 1),
		strings.Replace(valid, `"requiresWheelchairAccess":null`, `"requiresWheelchairAccess":"false"`, 1),
		strings.Replace(valid, `"likes":"SECRET_PREFERENCE"`, `"likes":null`, 1),
		strings.Replace(valid, `"neutral":"",`, "", 1),
		strings.Replace(valid, `"maxBudgetFen":null`, `"maxBudgetFen":-1`, 1),
		strings.Replace(valid, `"maxBudgetFen":null`, `"maxBudgetFen":0.5`, 1),
		strings.Replace(valid, `"maxBudgetFen":null`, `"maxBudgetFen":9223372036854775808`, 1),
		strings.Replace(valid, `"hardDefaults":{`, `"hardDefaults":{"exclude":[],`, 1),
		strings.Replace(valid, `"soft":{`, `"soft":{"weight":5,`, 1),
		strings.Replace(valid, `"expectedVersion":4`, `"expectedVersion":0`, 1),
		strings.Replace(valid, `"expectedVersion":4`, `"expectedVersion":null`, 1),
		strings.Replace(valid, `"expectedVersion":4`, `"expectedVersion":4,"userId":"other"`, 1),
		strings.Replace(valid, `"expectedVersion":4`, `"expectedVersion":4,"Exclude":[]`, 1),
		strings.Replace(valid, "SECRET_PREFERENCE", strings.Repeat("中", 501), 1),
		`{"soft":null,"hardDefaults":{"maxBudgetFen":null,"requiresWheelchairAccess":null},"expectedVersion":4}`,
		valid + ` {}`,
	} {
		w = resourceRequest(r, "PUT", "/api/v1/preferences", body, token)
		if w.Code != 400 {
			t.Fatalf("invalid payload accepted: status=%d body=%s", w.Code, w.Body.String())
		}
	}
	for _, body := range []string{strings.Replace(valid, `"expectedVersion":4`, `"expectedVersion":1`, 1)} {
		w = resourceRequest(r, "PUT", "/api/v1/preferences", body, token)
		if w.Code != 409 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	// A fully escaped 500-rune string in every bucket must fit the body limit.
	escaped := strings.Repeat(`\ud83d\ude00`, 500)
	large := fmt.Sprintf(`{"soft":{"likes":"%s","neutral":"%s","dislikes":"%s"},"hardDefaults":{"maxBudgetFen":null,"requiresWheelchairAccess":null},"expectedVersion":4}`, escaped, escaped, escaped)
	w = resourceRequest(r, "PUT", "/api/v1/preferences", large, token)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = resourceRequest(r, "GET", "/api/v1/preferences", "", other)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"version":1`) {
		t.Fatal("cross-user mutation", w.Body.String())
	}
	w = resourceRequest(r, "GET", "/api/v1/profile", "", token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"version":1`) {
		t.Fatal("preference changed profile", w.Body.String())
	}
	w = resourceRequest(r, "PUT", "/api/v1/profile", `{"displayName":"New","nickname":null,"expectedVersion":1}`, token)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = resourceRequest(r, "GET", "/api/v1/preferences", "", token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"version":5`) {
		t.Fatal("profile changed preference", w.Body.String())
	}
	repo.resourceErr = fmt.Errorf("%w: SECRET_PREFERENCE", errs.DependencyUnavailable)
	w = resourceRequest(r, "PUT", "/api/v1/preferences", strings.Replace(valid, `"expectedVersion":4`, `"expectedVersion":5`, 1), token)
	if w.Code != 503 || strings.Contains(w.Body.String(), "SECRET_PREFERENCE") || strings.Contains(logs.String(), "SECRET_PREFERENCE") {
		t.Fatal("dependency mapping or privacy failure")
	}
	repo.resourceErr = nil
	w = resourceRequest(r, "GET", "/api/v1/preferences", "", token)
	var p struct {
		Version int64 `json:"version"`
	}
	if json.Unmarshal(w.Body.Bytes(), &p) != nil || p.Version != 5 {
		t.Fatal("failed update changed resource")
	}
	for _, origin := range []string{"", "https://evil.example"} {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/preferences", strings.NewReader(valid))
		req.Header.Set("Content-Type", "application/json")
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
	w = resourceRequest(r, "PUT", "/api/v1/preferences", strings.Repeat(" ", 33<<10)+valid, token)
	if w.Code != 400 {
		t.Fatal("unbounded body")
	}
}
