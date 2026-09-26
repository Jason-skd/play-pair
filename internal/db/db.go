// Package db owns PostgreSQL connections and generated query adapters.
package db

import (
	"context"
	"fmt"

	"github.com/Jason-skd/play-pair/internal/db/generated"
	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB owns a PostgreSQL connection pool and its generated query adapter.
// Its methods may be called concurrently; the caller must eventually Close it.
type DB struct {
	pool    *pgxpool.Pool
	queries *generated.Queries
}

// Open constructs and checks a pool using ctx for startup. It closes the pool
// on failure; the caller owns the returned DB and must Close it on success.
func Open(ctx context.Context, url string, maxConns int32) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("database configuration: %w", err)
	}
	cfg.MaxConns = maxConns
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: initialize pool", errs.DependencyUnavailable)
	}
	db := &DB{pool: pool, queries: generated.New(pool)}
	if err := db.Check(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Check probes the database and returns errs.DependencyUnavailable on query failure.
func (db *DB) Check(ctx context.Context) error {
	if _, err := db.queries.HealthCheck(ctx); err != nil {
		return fmt.Errorf("%w: health query", errs.DependencyUnavailable)
	}
	return nil
}

// Close releases the connection pool after all database work has stopped.
func (db *DB) Close() { db.pool.Close() }
