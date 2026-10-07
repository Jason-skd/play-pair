package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/Jason-skd/play-pair/internal/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestOutingsApplication(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		if os.Getenv("PLAYPAIR_INTEGRATION") == "1" {
			t.Fatal("TEST_DATABASE_URL is required")
		}
		t.Skip("set TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	a, err := Start(ctx, config.Config{HTTPAddr: "127.0.0.1:0", DatabaseURL: databaseURL, DBMaxConns: 4, StartupTimeout: 5 * time.Second, ReadyTimeout: time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	var ids []int64
	var groupID int64
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, err := pgx.Connect(ctx, databaseURL)
		if err != nil {
			t.Error(err)
			return
		}
		defer c.Close(ctx)
		tx, err := c.Begin(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		defer tx.Rollback(ctx)
		for _, query := range []string{"DELETE FROM outings WHERE group_id=$1", "DELETE FROM group_members WHERE group_id=$1", "DELETE FROM groups WHERE id=$1"} {
			if _, err := tx.Exec(ctx, query, groupID); err != nil {
				t.Error(err)
				return
			}
		}
		if _, err := tx.Exec(ctx, "DELETE FROM users WHERE id=ANY($1::bigint[])", ids); err != nil {
			t.Error(err)
			return
		}
		if err := tx.Commit(ctx); err != nil {
			t.Error(err)
		}
	})
	clients := make([]*http.Client, 2)
	for i := range clients {
		jar, _ := cookiejar.New(nil)
		transport := &http.Transport{}
		t.Cleanup(transport.CloseIdleConnections)
		clients[i] = &http.Client{Jar: jar, Transport: transport, Timeout: 3 * time.Second}
	}
	request := func(actor int, method, path string, body any, want int) []byte {
		t.Helper()
		var encoded []byte
		if body != nil {
			var err error
			encoded, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, "http://"+a.Addr().String()+path, strings.NewReader(string(encoded)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-PlayPair-Request", "1")
		if path == "/api/v1/groups" && method == "POST" {
			req.Header.Set("Idempotency-Key", uuid.NewString())
		}
		res, err := clients[actor].Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, data)
		}
		return data
	}
	for i := range clients {
		var u generated.User
		data := request(i, "POST", "/api/v1/auth/register", map[string]string{"email": uuid.NewString() + "@example.com", "password": "correct horse battery", "displayName": "Member"}, 201)
		if err := json.Unmarshal(data, &u); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.Id)
	}
	var g generated.Group
	if err := json.Unmarshal(request(0, "POST", "/api/v1/groups", map[string]string{"name": "Outing group"}, 201), &g); err != nil {
		t.Fatal(err)
	}
	groupID = g.Id
	request(1, "POST", "/api/v1/groups/join", map[string]string{"joinCode": g.JoinCode}, 200)
	date := time.Now().UTC().AddDate(0, 0, 7).Format(time.DateOnly)
	var o generated.Outing
	if err := json.Unmarshal(request(0, "POST", fmt.Sprintf("/api/v1/groups/%d/outings", g.Id), map[string]any{"dates": []string{date}, "timezone": "UTC", "durationMinutes": 60}, 201), &o); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/outings/%d", o.Id)
	request(0, "GET", path+"/decision-inputs", nil, 409)
	request(1, "PUT", path+"/responses/decision", map[string]any{"choice": "accept", "input": map[string]any{"maxBudgetFen": 0, "requiresWheelchairAccess": false, "availability": []any{map[string]string{"date": date, "startTime": "10:00", "endTime": "12:00"}}}}, 204)
	request(1, "GET", fmt.Sprintf("%s/members/%d", path, ids[0]), nil, 403)
	request(0, "GET", fmt.Sprintf("%s/members/%d", path, ids[1]), nil, 200)
	request(1, "GET", path+"/decision-inputs", nil, 403)
	var aggregation generated.DecisionInputs
	if err := json.Unmarshal(request(0, "GET", path+"/decision-inputs", nil, 200), &aggregation); err != nil {
		t.Fatal(err)
	}
	if aggregation.MaxBudgetFen != 0 || len(aggregation.CommonWindows) != 1 || aggregation.CommonWindows[0].StartsAt.Format(time.DateOnly) != date || aggregation.CommonWindows[0].EndsAt.Sub(aggregation.CommonWindows[0].StartsAt) != 2*time.Hour || len(aggregation.Constraints) != 3 || len(aggregation.Constraints[0].Sources) != 1 || aggregation.Constraints[0].Sources[0].MemberId != ids[1] {
		t.Fatal(aggregation)
	}
	request(0, "PUT", path, map[string]any{"dates": []string{time.Now().UTC().AddDate(0, 0, 8).Format(time.DateOnly)}, "durationMinutes": 60}, 204)
	request(0, "GET", path+"/decision-inputs", nil, 409)
	data := request(1, "GET", path, nil, 200)
	if !strings.Contains(string(data), "date_outside_plan") {
		t.Fatal(string(data))
	}
	start := time.Now().UTC().AddDate(0, 0, 7).Truncate(time.Hour)
	arrangement := map[string]any{"startsAt": start, "endsAt": start.Add(time.Hour), "place": "Park", "activity": "Walk"}
	request(0, "POST", path+"/arrangement", arrangement, 201)
	request(0, "GET", path+"/decision-inputs", nil, 409)
	request(1, "PUT", path+"/responses/confirmation", map[string]string{"choice": "accept"}, 204)
	arrangement["resetResponses"] = true
	request(0, "PUT", path+"/arrangement", arrangement, 204)
	var detail generated.OutingDetail
	if err := json.Unmarshal(request(1, "GET", path, nil, 200), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Member.Input == nil || detail.Member.Confirmation != "pending" || len(detail.Member.Warnings) != 0 {
		t.Fatal(detail)
	}
	request(1, "PUT", path+"/responses/decision", map[string]string{"choice": "deny"}, 409)
	request(1, "GET", "/api/v1/outings?groupId="+fmt.Sprint(g.Id), nil, 200)
	data = request(1, "GET", path+"/responses?phase=confirmation&status=pending", nil, 200)
	if strings.Contains(string(data), "maxBudgetFen") {
		t.Fatal("private response input leaked", string(data))
	}
	request(1, "POST", "/api/v1/auth/logout", nil, 204)
	request(1, "GET", path, nil, 401)
}
