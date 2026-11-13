// Package db owns the PostgreSQL connection pool and schema migrations.
package db

import (
	"context"
	"embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Open creates a connection pool and verifies connectivity.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("db: parse url: %w", err)
	}
	// Small pool: Cloud Run instances are tiny and Supabase free has limited connections.
	cfg.MaxConns = 5
	cfg.MaxConnIdleTime = 2 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: connect: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}

// Migrate applies migrations from the embedded filesystem. Direction is "up", "down" or "status".
func Migrate(ctx context.Context, pool *pgxpool.Pool, migrations embed.FS, direction string) (err error) {
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("db: goose dialect: %w", err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() {
		if cerr := sqlDB.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("db: close migration handle: %w", cerr)
		}
	}()
	switch direction {
	case "up":
		err = goose.UpContext(ctx, sqlDB, ".")
	case "down":
		err = goose.DownContext(ctx, sqlDB, ".")
	case "status":
		err = goose.StatusContext(ctx, sqlDB, ".")
	default:
		return fmt.Errorf("db: unknown migrate direction %q", direction)
	}
	if err != nil {
		return fmt.Errorf("db: migrate %s: %w", direction, err)
	}
	return nil
}
