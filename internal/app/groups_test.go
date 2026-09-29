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

// Full HTTP/session/service/SQL path, including membership surviving a new session.
func TestGroupsApplication(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		if os.Getenv("PLAYPAIR_INTEGRATION") == "1" {
			t.Fatal("TEST_DATABASE_URL is required")
		}
		t.Skip("set TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	app, err := Start(ctx, config.Config{HTTPAddr: "127.0.0.1:0", DatabaseURL: databaseURL, DBMaxConns: 4, StartupTimeout: 5 * time.Second, ReadyTimeout: time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	clients := make([]*http.Client, 3)
	ids := make([]int64, 0, 3)
	emails := make([]string, 3)
	groupIDs := make([]int64, 0, 2)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, databaseURL)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close(ctx)
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		defer tx.Rollback(ctx)
		for _, sql := range []string{"DELETE FROM group_members WHERE group_id=ANY($1::bigint[])", "DELETE FROM groups WHERE id=ANY($1::bigint[])"} {
			if _, err := tx.Exec(ctx, sql, groupIDs); err != nil {
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
	request := func(actor int, method, path string, body any, key string, want int) []byte {
		t.Helper()
		var encoded []byte
		if body != nil {
			var err error
			encoded, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, "http://"+app.Addr().String()+path, strings.NewReader(string(encoded)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-PlayPair-Request", "1")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		resp, err := clients[actor].Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			t.Fatalf("actor %d %s %s: %d %s", actor, method, path, resp.StatusCode, data)
		}
		if strings.HasPrefix(path, "/api/v1/groups") && resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("group response cacheable")
		}
		return data
	}
	for i := range clients {
		jar, _ := cookiejar.New(nil)
		transport := &http.Transport{}
		t.Cleanup(transport.CloseIdleConnections)
		clients[i] = &http.Client{Jar: jar, Transport: transport, Timeout: 3 * time.Second}
		emails[i] = uuid.NewString() + "@example.com"
		data := request(i, "POST", "/api/v1/auth/register", map[string]string{"email": emails[i], "password": "correct horse battery", "displayName": fmt.Sprintf("Person %d", i)}, "", 201)
		var u generated.User
		if err := json.Unmarshal(data, &u); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, u.Id)
	}
	key := uuid.NewString()
	body := map[string]string{"name": " 周末搭子 "}
	data := request(0, "POST", "/api/v1/groups", body, key, 201)
	var g generated.Group
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	groupIDs = append(groupIDs, g.Id)
	if g.InitiatorId != ids[0] || g.Name != "周末搭子" || len(g.JoinCode) != 16 {
		t.Fatal(g)
	}
	path := fmt.Sprintf("/api/v1/groups/%d", g.Id)
	decodeMembers := func(data []byte) generated.GroupMemberPage {
		t.Helper()
		var p generated.GroupMemberPage
		if err := json.Unmarshal(data, &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	page := decodeMembers(request(0, "GET", path+"/members", nil, "", 200))
	if len(page.Members) != 1 || page.Members[0].MemberId != ids[0] || page.Members[0].Role != generated.Initiator {
		t.Fatal(page)
	}
	for _, who := range []int{1, 2} {
		request(who, "GET", path, nil, "", 403)
		request(who, "GET", path+"/members", nil, "", 403)
	}
	// C's membership in a separate group confers no access to A's group.
	var cg generated.Group
	if err := json.Unmarshal(request(2, "POST", "/api/v1/groups", map[string]string{"name": "Other"}, key, 201), &cg); err != nil {
		t.Fatal(err)
	}
	groupIDs = append(groupIDs, cg.Id)
	request(2, "GET", path, nil, "", 403)
	request(2, "GET", path+"/members", nil, "", 403)
	joined := request(1, "POST", "/api/v1/groups/join", map[string]string{"joinCode": " " + strings.ToLower(g.JoinCode) + " "}, "", 200)
	read := request(1, "GET", path, nil, "", 200)
	if string(read) != string(joined) {
		t.Fatal("member cannot read joined group")
	}
	page = decodeMembers(request(1, "GET", path+"/members?limit=1", nil, "", 200))
	if len(page.Members) != 1 || page.NextAfterMemberId == nil || *page.NextAfterMemberId != ids[0] {
		t.Fatal(page)
	}
	page = decodeMembers(request(1, "GET", fmt.Sprintf("%s/members?afterMemberId=%d&limit=1", path, *page.NextAfterMemberId), nil, "", 200))
	if len(page.Members) != 1 || page.Members[0].MemberId != ids[1] || page.Members[0].Role != generated.Member || page.NextAfterMemberId != nil {
		t.Fatal(page)
	}
	before := request(1, "GET", path+"/members", nil, "", 200)
	repeated := request(1, "POST", "/api/v1/groups/join", map[string]string{"joinCode": g.JoinCode}, "", 200)
	if string(repeated) != string(joined) || string(before) != string(request(0, "GET", path+"/members", nil, "", 200)) {
		t.Fatal("repeat changed membership/time")
	}
	repeated = request(0, "POST", "/api/v1/groups/join", map[string]string{"joinCode": g.JoinCode}, "", 200)
	if string(repeated) != string(joined) {
		t.Fatal("initiator join changed group")
	}
	replay := request(0, "POST", "/api/v1/groups", body, key, 200)
	if string(replay) != string(joined) {
		t.Fatal("creation replay changed group")
	}
	request(0, "POST", "/api/v1/groups", map[string]string{"name": "different"}, key, 409)
	request(1, "PUT", "/api/v1/profile", map[string]any{"displayName": "Updated", "nickname": "昵称", "expectedVersion": 1}, "", 200)
	projected := request(0, "GET", path+"/members", nil, "", 200)
	if !strings.Contains(string(projected), "昵称") || strings.Contains(string(projected), "email") || strings.Contains(string(projected), "preference") {
		t.Fatal("bad member projection", string(projected))
	}
	if string(request(0, "GET", path, nil, "", 200)) != string(joined) {
		t.Fatal("profile changed group timestamp")
	}
	request(1, "POST", "/api/v1/auth/logout", nil, "", 204)
	request(1, "GET", path, nil, "", 401)
	request(1, "GET", path+"/members", nil, "", 401)
	request(1, "POST", "/api/v1/auth/login", map[string]string{"email": emails[1], "password": "correct horse battery"}, "", 200)
	if string(request(1, "GET", path, nil, "", 200)) != string(joined) {
		t.Fatal("membership lost on new session")
	}
	request(1, "GET", "/api/v1/groups/9223372036854775807", nil, "", 404)
	request(1, "POST", "/api/v1/groups/join", map[string]string{"joinCode": "ZZZZZZZZZZZZZZZZ"}, "", 404)
}
