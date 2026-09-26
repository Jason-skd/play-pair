// Package app assembles concrete dependencies and controls their lifetime.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/Jason-skd/play-pair/internal/api"
	"github.com/Jason-skd/play-pair/internal/auth"
	"github.com/Jason-skd/play-pair/internal/config"
	"github.com/Jason-skd/play-pair/internal/db"
	"github.com/Jason-skd/play-pair/internal/errs"
)

// App owns the HTTP listener, server, and database pool for one process.
type App struct {
	server   *http.Server
	listener net.Listener
	database *db.DB
	ready    atomic.Bool
	logger   *slog.Logger
}

// Start opens the database and listener, then begins serving in the background.
// ctx bounds startup; the caller must call Shutdown on a successful result.
func Start(ctx context.Context, c config.Config, logger *slog.Logger) (*App, error) {
	startCtx, cancel := context.WithTimeout(ctx, c.StartupTimeout)
	defer cancel()
	database, err := db.Open(startCtx, c.DatabaseURL, c.DBMaxConns)
	if err != nil {
		return nil, fmt.Errorf("start database: %w", err)
	}
	listener, err := net.Listen("tcp", c.HTTPAddr)
	if err != nil {
		database.Close()
		return nil, fmt.Errorf("listen HTTP: %w", err)
	}
	a := &App{listener: listener, database: database, logger: logger}
	authService := auth.New(database, time.Now)
	a.server = &http.Server{Handler: api.New(func(ctx context.Context) error {
		if !a.ready.Load() {
			return errs.DependencyUnavailable
		}
		return database.Check(ctx)
	}, c.ReadyTimeout, logger, authService, c.AuthCookieSecure), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	a.ready.Store(true)
	go func() {
		if err := a.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server stopped", "category", "serve_failure")
			a.ready.Store(false)
		}
	}()
	return a, nil
}

// Addr returns the bound listener address, including an allocated ephemeral port.
func (a *App) Addr() net.Addr { return a.listener.Addr() }

// Shutdown stops readiness, drains HTTP using ctx, and closes the database pool.
// If draining fails, it force-closes the server and returns the drain error.
func (a *App) Shutdown(ctx context.Context) error {
	a.ready.Store(false)
	err := a.server.Shutdown(ctx)
	if err != nil {
		_ = a.server.Close()
	}
	a.database.Close()
	return err
}
