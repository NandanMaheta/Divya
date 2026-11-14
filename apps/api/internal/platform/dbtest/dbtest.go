// Package dbtest gives integration tests a fresh, fully migrated PostgreSQL 18 database.
//
// One container is started per test binary (package) and migrations run once into a template
// database. Each call to New clones that template into a brand-new database (CREATE DATABASE …
// TEMPLATE …), so every test is fully isolated while paying only ~100 ms instead of a container
// start. Tests are skipped with `go test -short`.
package dbtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/migrations"
)

// Image is the PostgreSQL version used in tests; keep it equal to production.
const Image = "postgres:18-alpine"

const templateDB = "swastik_template"

var (
	once    sync.Once
	baseURL *url.URL      // connection URL of the container (database part replaced per use)
	admin   *pgxpool.Pool // connected to the "postgres" maintenance database
	initErr error
	seq     atomic.Int64
)

// New returns a pool to a fresh database with all migrations applied. The database is dropped
// when the test ends.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: skipped in -short mode")
	}
	once.Do(start)
	if initErr != nil {
		t.Fatalf("dbtest: %v", initErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := fmt.Sprintf("t_%d_%d", os.Getpid(), seq.Add(1))
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q TEMPLATE %q`, name, templateDB)); err != nil {
		t.Fatalf("dbtest: create database: %v", err)
	}
	pool, err := db.Open(ctx, withDatabase(name))
	if err != nil {
		t.Fatalf("dbtest: open: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dropCancel()
		_, _ = admin.Exec(dropCtx, fmt.Sprintf(`DROP DATABASE IF EXISTS %q WITH (FORCE)`, name))
	})
	return pool
}

// start runs once per test binary: container, template database, migrations.
// The container is reaped by testcontainers when the test process exits.
func start() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	ctr, err := tcpostgres.Run(ctx, Image,
		tcpostgres.WithDatabase("postgres"),
		tcpostgres.WithUsername("swastik"),
		tcpostgres.WithPassword("swastik"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		initErr = fmt.Errorf("start postgres: %w", err)
		return
	}
	raw, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		initErr = fmt.Errorf("connection string: %w", err)
		return
	}
	if baseURL, err = url.Parse(raw); err != nil {
		initErr = fmt.Errorf("parse connection string: %w", err)
		return
	}
	if admin, err = db.Open(ctx, withDatabase("postgres")); err != nil {
		initErr = fmt.Errorf("open admin: %w", err)
		return
	}
	if _, err = admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q`, templateDB)); err != nil {
		initErr = fmt.Errorf("create template: %w", err)
		return
	}
	tmpl, err := db.Open(ctx, withDatabase(templateDB))
	if err != nil {
		initErr = fmt.Errorf("open template: %w", err)
		return
	}
	err = db.Migrate(ctx, tmpl, migrations.FS, "up")
	tmpl.Close() // a template must have no open connections before it is cloned
	if err != nil {
		initErr = fmt.Errorf("migrate template: %w", err)
	}
}

func withDatabase(name string) string {
	u := *baseURL
	u.Path = "/" + name
	return u.String()
}
