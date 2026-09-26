// Package db owns PostgreSQL connections and generated query adapters.
package db

import (
	"context"
	"fmt"

	"github.com/Jason-skd/play-pair/internal/db/generated"
	"github.com/Jason-skd/play-pair/internal/errs"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	pool    *pgxpool.Pool
	queries *generated.Queries
}

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

func (db *DB) Check(ctx context.Context) error {
	if _, err := db.queries.HealthCheck(ctx); err != nil {
		return fmt.Errorf("%w: health query", errs.DependencyUnavailable)
	}
	return nil
}
func (db *DB) Close() { db.pool.Close() }
