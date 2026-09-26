package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Jason-skd/play-pair/internal/app"
	"github.com/Jason-skd/play-pair/internal/config"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("service stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	c, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := app.Start(ctx, c, logger)
	if err != nil {
		return err
	}
	logger.Info("HTTP listening", "address", a.Addr().String())
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), c.ShutdownTimeout)
	defer cancel()
	err = a.Shutdown(shutdownCtx)
	if err != nil && errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return err
}
