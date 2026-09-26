package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Jason-skd/play-pair/internal/config"
)

func TestLifecycle(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL for integration tests")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{HTTPAddr: "127.0.0.1:0", DatabaseURL: url, DBMaxConns: 2, StartupTimeout: 5 * time.Second, ReadyTimeout: time.Second, ShutdownTimeout: time.Second}
	a, err := Start(context.Background(), cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Second}
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := client.Get("http://" + a.Addr().String() + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := net.DialTimeout("tcp", a.Addr().String(), time.Second); err == nil {
		t.Fatal("listener still open")
	}
}

func TestStartupFailures(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{HTTPAddr: "127.0.0.1:0", DatabaseURL: "postgres://user:secret@127.0.0.1:1/missing", DBMaxConns: 1, StartupTimeout: 100 * time.Millisecond, ReadyTimeout: time.Second}
	if a, err := Start(context.Background(), cfg, logger); err == nil {
		a.Shutdown(context.Background())
		t.Fatal("expected database failure")
	}
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		return
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	cfg.DatabaseURL = url
	cfg.HTTPAddr = listener.Addr().String()
	cfg.StartupTimeout = 5 * time.Second
	if a, err := Start(context.Background(), cfg, logger); err == nil {
		a.Shutdown(context.Background())
		t.Fatal("expected listen failure")
	}
}
