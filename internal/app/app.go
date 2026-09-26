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
	"github.com/Jason-skd/play-pair/internal/config"
	"github.com/Jason-skd/play-pair/internal/db"
	"github.com/Jason-skd/play-pair/internal/errs"
)

type App struct {
	server   *http.Server
	listener net.Listener
	database *db.DB
	ready    atomic.Bool
	logger   *slog.Logger
}

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
	a.server = &http.Server{Handler: api.New(func(ctx context.Context) error {
		if !a.ready.Load() {
			return errs.DependencyUnavailable
		}
		return database.Check(ctx)
	}, c.ReadyTimeout, logger), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	a.ready.Store(true)
	go func() {
		if err := a.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server stopped", "category", "serve_failure")
			a.ready.Store(false)
		}
	}()
	return a, nil
}

func (a *App) Addr() net.Addr { return a.listener.Addr() }

func (a *App) Shutdown(ctx context.Context) error {
	a.ready.Store(false)
	err := a.server.Shutdown(ctx)
	if err != nil {
		_ = a.server.Close()
	}
	a.database.Close()
	return err
}
