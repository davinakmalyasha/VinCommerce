package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vincommerce/backend/internal/config"
)

// Pool wraps pgxpool.Pool for PostgreSQL access.
type Pool struct {
	*pgxpool.Pool
}

// Connect creates and verifies a connection pool.
func Connect(ctx context.Context, cfg config.DatabaseConfig) (*Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	poolCfg.MaxConns = int32(cfg.MaxConns)
	poolCfg.MinConns = int32(cfg.MinConns)
	poolCfg.MaxConnIdleTime = cfg.MaxIdle
	poolCfg.MaxConnLifetime = cfg.MaxLife

	// Server-side guards so a runaway query or stuck transaction can never
	// pin a pooled connection indefinitely and starve checkout traffic.
	poolCfg.ConnConfig.RuntimeParams["statement_timeout"] = "30s"
	poolCfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "30s"
	poolCfg.ConnConfig.RuntimeParams["lock_timeout"] = "10s"

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &Pool{Pool: pool}, nil
}
