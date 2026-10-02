package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/api/generated"
	"github.com/Jason-skd/play-pair/internal/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The full HTTP chain owns an isolated schema, including immutable arrangement
// fixtures. No test cleanup bypass is exposed as an application capability.
func TestOutingsApplication(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		if os.Getenv("PLAYPAIR_INTEGRATION") == "1" {
			t.Fatal("TEST_DATABASE_URL is required")
		}
		t.Skip("set TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := "outings_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
		if err := conn.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	if _, err = conn.Exec(ctx, "SET search_path TO "+quoted); err != nil {
		t.Fatal(err)
	}
	migrations, err := filepath.Glob("../../db/migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range migrations {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0]); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(file, err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	u, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	app, err := Start(ctx, config.Config{HTTPAddr: "127.0.0.1:0", DatabaseURL: u.String(), DBMaxConns: 4, StartupTimeout: 5 * time.Second, ReadyTimeout: time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := app.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	clients := make([]*http.Client, 4)
	for i := range clients {
		jar, _ := cookiejar.New(nil)
		transport := &http.Transport{}
		t.Cleanup(transport.CloseIdleConnections)
		clients[i] = &http.Client{Jar: jar, Transport: transport, Timeout: 3 * time.Second}
	}
	request := func(actor int, method, path, body, key string, want int) []byte {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, "http://"+app.Addr().String()+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-PlayPair-Request", "1")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
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
			t.Fatalf("%d %s %s: %d %s", actor, method, path, res.StatusCode, data)
		}
		return data
	}
	for i := range clients {
		request(i, "POST", "/api/v1/auth/register", fmt.Sprintf(`{"email":"%s@example.com","password":"correct horse battery","displayName":"Person %d"}`, uuid.NewString(), i), "", 201)
	}
	var g generated.Group
	if err = json.Unmarshal(request(0, "POST", "/api/v1/groups", `{"name":"Weekend"}`, uuid.NewString(), 201), &g); err != nil {
		t.Fatal(err)
	}
	join, _ := json.Marshal(map[string]string{"joinCode": g.JoinCode})
	request(1, "POST", "/api/v1/groups/join", string(join), "", 200)
	key := uuid.NewString()
	create := `{"dates":["2030-10-03","2030-10-05"],"timezone":"Asia/Shanghai","durationMinutes":120}`
	var o generated.Outing
	if err = json.Unmarshal(request(1, "POST", fmt.Sprintf("/api/v1/groups/%d/outings", g.Id), create, key, 201), &o); err != nil {
		t.Fatal(err)
	}
	if o.Status != "decision" || o.Arrangement != nil || len(o.Invitations) != 1 || o.Invitations[0].Status != "pending" {
		t.Fatal(o)
	}
	request(1, "POST", fmt.Sprintf("/api/v1/groups/%d/outings", g.Id), create, key, 200)
	path := fmt.Sprintf("/api/v1/outings/%d", o.Id)
	request(2, "GET", path, "", "", 403)
	request(2, "POST", "/api/v1/groups/join", string(join), "", 200)
	var page generated.InvitationPage
	if err = json.Unmarshal(request(2, "GET", "/api/v1/me/invitations", "", "", 200), &page); err != nil || len(page.Invitations) != 1 {
		t.Fatal(page, err)
	}
	accepted := `{"status":"accepted","expectedVersion":1,"input":{"maxBudgetFen":0,"requiresWheelchairAccess":false,"availability":[{"date":"2030-10-03","startTime":"08:00","endTime":"10:00"},{"date":"2030-10-03","startTime":"09:30","endTime":"12:00"}]}}`
	var invitation generated.Invitation
	if err = json.Unmarshal(request(1, "PUT", path+"/invitations/decision", accepted, "", 200), &invitation); err != nil || invitation.Input == nil || len(invitation.Input.Availability) != 1 || invitation.Input.Availability[0].EndTime != "12:00" {
		t.Fatal(invitation, err)
	}
	request(1, "PUT", path+"/invitations/decision", accepted, "", 200)
	request(1, "PUT", path+"/invitations/decision", strings.Replace(accepted, `"maxBudgetFen":0`, `"maxBudgetFen":1`, 1), "", 409)
	invalid := fmt.Sprintf(`{"status":"accepted","expectedVersion":%d,"input":{"maxBudgetFen":0,"requiresWheelchairAccess":false,"availability":[{"date":"2030-10-03","startTime":"23:00","endTime":"01:00"}]}}`, invitation.Version)
	data := request(1, "PUT", path+"/invitations/decision", invalid, "", 400)
	if !strings.Contains(string(data), "invalid_availability") {
		t.Fatal(string(data))
	}
	if data := request(0, "GET", path+"/invitations", "", "", 200); strings.Contains(string(data), "maxBudgetFen") {
		t.Fatal("summary leaked constraints")
	}
	if _, err = conn.Exec(ctx, "INSERT INTO outing_arrangements(outing_id,starts_at,ends_at,place,activity) VALUES($1,clock_timestamp()+interval '1 day',clock_timestamp()+interval '1 day 2 hours','SCNU','Board games')", o.Id); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(request(1, "GET", path, "", "", 200), &o); err != nil || o.Status != "confirmation" || o.Timezone != "Asia/Shanghai" || o.Arrangement == nil || len(o.Invitations) != 2 {
		t.Fatal(o, err)
	}
	request(1, "PUT", path+"/invitations/decision", accepted, "", 409)
	request(1, "PUT", path+"/invitations/confirmation", `{"status":"accepted","expectedVersion":1}`, "", 200)
	// Backdating is a fixture-only operation; production arrangements are immutable.
	if _, err = conn.Exec(ctx, "ALTER TABLE outing_arrangements DISABLE TRIGGER outing_arrangement_guard; UPDATE outing_arrangements SET starts_at=clock_timestamp()-interval '1 second'; ALTER TABLE outing_arrangements ENABLE TRIGGER outing_arrangement_guard"); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(request(1, "GET", path, "", "", 200), &o); err != nil || o.Status != "archived" {
		t.Fatal(o, err)
	}
	request(2, "PUT", path+"/invitations/confirmation", `{"status":"accepted","expectedVersion":1}`, "", 409)
	request(3, "POST", "/api/v1/groups/join", string(join), "", 200)
	if err = json.Unmarshal(request(3, "GET", "/api/v1/me/invitations", "", "", 200), &page); err != nil || len(page.Invitations) != 0 {
		t.Fatal(page, err)
	}
}
