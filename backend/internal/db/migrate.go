package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/vincommerce/backend/internal/config"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies pending SQL migrations using goose.
func Migrate(ctx context.Context, cfg config.DatabaseConfig) error {
	sqlDB, err := sql.Open("pgx", cfg.DSN())
	if err != nil {
		return fmt.Errorf("open sql db: %w", err)
	}
	defer sqlDB.Close()

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}
	if err := goose.UpContext(ctx, sqlDB, "migrations"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

var _ = stdlib.GetDefaultDriver
