package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/botspace/backend/internal/shared/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool creates a configured pgx pool and verifies connectivity.
func NewPool(ctx context.Context, cfg config.DBConfig) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("parse pgx config: %w", err)
	}

	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConnLifetime = cfg.MaxConnLife

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return pool, nil
}

// ErrNotFound is returned when a query expects a single row but none was found.
var ErrNotFound = errors.New("entity not found")
