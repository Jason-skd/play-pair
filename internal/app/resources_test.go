package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Exercise the real router, services and database together; transport-only
// tests use doubles and cannot establish transaction or nullable SQL behavior.
func TestPersonalResourcesApplication(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("PLAYPAIR_INTEGRATION") == "1" {
			t.Fatal("TEST_DATABASE_URL is required")
		}
		t.Skip("set TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	app, err := Start(ctx, config.Config{HTTPAddr: "127.0.0.1:0", DatabaseURL: url, DBMaxConns: 4, StartupTimeout: 5 * time.Second, ReadyTimeout: time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	jar, _ := cookiejar.New(nil)
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Jar: jar, Timeout: 3 * time.Second, Transport: transport}
	request := func(method, path, body string, want int) map[string]json.RawMessage {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, "http://"+app.Addr().String()+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-PlayPair-Request", "1")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, data)
		}
		var result map[string]json.RawMessage
		if len(data) > 0 {
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	email := uuid.NewString() + "@example.com"
	registered := request("POST", "/api/v1/auth/register", `{"email":"`+email+`","password":"correct horse battery","displayName":"Initial"}`, 201)
	var id int64
	if err := json.Unmarshal(registered["id"], &id); err != nil {
		t.Fatal(err)
	}
	if id <= 0 {
		t.Fatalf("registration did not return a positive database identity: %d", id)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, url)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, "DELETE FROM users WHERE id=$1", id); err != nil {
			t.Error(err)
		}
	})
	p := request("GET", "/api/v1/profile", "", 200)
	if string(p["version"]) != "1" || string(p["nickname"]) != "null" {
		t.Fatal("initial profile", p)
	}
	request("PUT", "/api/v1/profile", `{"displayName":"Updated","nickname":"Nick","expectedVersion":1}`, 200)
	me := request("GET", "/api/v1/me", "", 200)
	if string(me["displayName"]) != `"Updated"` || string(me["id"]) != string(registered["id"]) {
		t.Fatal("stale display or mismatched account identity", me)
	}
	for _, c := range []struct{ budget, access, version string }{{"0", "false", "1"}, {"10000", "true", "2"}, {"null", "null", "3"}} {
		body := `{"soft":{"likes":"桌游","neutral":"","dislikes":"太吵"},"hardDefaults":{"maxBudgetFen":` + c.budget + `,"requiresWheelchairAccess":` + c.access + `},"expectedVersion":` + c.version + `}`
		request("PUT", "/api/v1/preferences", body, 200)
		got := request("GET", "/api/v1/preferences", "", 200)
		var hard map[string]json.RawMessage
		if err := json.Unmarshal(got["hardDefaults"], &hard); err != nil {
			t.Fatal(err)
		}
		if string(hard["maxBudgetFen"]) != c.budget || string(hard["requiresWheelchairAccess"]) != c.access {
			t.Fatal("SQL round trip lost null/zero/false", hard)
		}
	}
	request("PUT", "/api/v1/preferences", `{"soft":{"likes":"","neutral":"","dislikes":""},"hardDefaults":{"maxBudgetFen":null,"requiresWheelchairAccess":null},"expectedVersion":1}`, 409)
	p = request("GET", "/api/v1/profile", "", 200)
	if string(p["version"]) != "2" {
		t.Fatal("coupled versions", p)
	}
	request("POST", "/api/v1/auth/logout", "", 204)
	request("GET", "/api/v1/preferences", "", 401)
	request("GET", "/api/v1/profile", "", 401)
	loggedIn := request("POST", "/api/v1/auth/login", `{"email":"`+email+`","password":"correct horse battery"}`, 200)
	if string(loggedIn["id"]) != string(registered["id"]) {
		t.Fatal("login changed account identity", loggedIn)
	}
	got := request("GET", "/api/v1/preferences", "", 200)
	if string(got["version"]) != "4" {
		t.Fatal("preferences did not survive new session", got)
	}
}
