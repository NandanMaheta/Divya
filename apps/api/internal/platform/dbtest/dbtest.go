// Package dbtest starts a throwaway PostgreSQL 18 container with all migrations applied.
// Integration tests call New(t); they are skipped with `go test -short`.
package dbtest

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/NandanMaheta/divya/apps/api/internal/platform/db"
	"github.com/NandanMaheta/divya/apps/api/migrations"
)

// Image is the PostgreSQL version used in tests; keep it equal to production.
const Image = "postgres:18-alpine"

// New returns a pool to a fresh, fully migrated database that is removed when the test ends.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: skipped in -short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	ctr, err := tcpostgres.Run(ctx, Image,
		tcpostgres.WithDatabase("divya"),
		tcpostgres.WithUsername("divya"),
		tcpostgres.WithPassword("divya"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		t.Fatalf("dbtest: start postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(ctr) })

	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dbtest: connection string: %v", err)
	}
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("dbtest: open: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := db.Migrate(ctx, pool, migrations.FS, "up"); err != nil {
		t.Fatalf("dbtest: migrate: %v", err)
	}
	return pool
}
