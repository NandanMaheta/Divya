// Shared helpers for the migration 00003 schema tests (S06; specs/003-auth-core/data-model.md).
//
// The tests run against a real PostgreSQL 18 through dbtest.New. They assert exact SQLSTATE codes
// (23502 not_null, 23503 foreign_key, 23505 unique, 23514 check). Constraint names are asserted
// only where data-model.md names the index (the unique indexes); CHECK names are the migration's choice.
package auth_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/dbtest"
)

// SQLSTATE codes asserted by the schema tests.
const (
	notNull    = "23502"
	foreignKey = "23503"
	unique     = "23505"
	check      = "23514"
)

const validPHC = "$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g"

// omit removes a column from the INSERT so the column default (or NOT NULL) applies.
type omit struct{}

// row is a set of column values for one INSERT. A nil value inserts an explicit SQL NULL.
type row map[string]any

func (r row) with(over row) row {
	out := make(row, len(r)+len(over))
	for k, v := range r {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

// schemaEnv is a migrated database with one org.
type schemaEnv struct {
	pool *pgxpool.Pool
	org  uuid.UUID
}

func newSchemaEnv(t *testing.T) *schemaEnv {
	t.Helper()
	pool := dbtest.New(t)
	e := &schemaEnv{pool: pool, org: uuid.New()}
	e.mustExec(t, `INSERT INTO orgs (id, name) VALUES ($1, 'Swastik')`, e.org)
	return e
}

func (e *schemaEnv) mustExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("setup %q: %v", sql, err)
	}
}

// insert builds and runs INSERT INTO table (cols...) VALUES (...) and returns the error.
func (e *schemaEnv) insert(table string, r row) error {
	cols := make([]string, 0, len(r))
	for c, v := range r {
		if _, skip := v.(omit); skip {
			continue
		}
		cols = append(cols, c)
	}
	sort.Strings(cols)
	args := make([]any, len(cols))
	ph := make([]string, len(cols))
	for i, c := range cols {
		args[i] = r[c]
		ph[i] = fmt.Sprintf("$%d", i+1)
	}
	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ", "), strings.Join(ph, ", "))
	_, err := e.pool.Exec(context.Background(), sql, args...)
	return err
}

func (e *schemaEnv) mustInsert(t *testing.T, table string, r row) {
	t.Helper()
	if err := e.insert(table, r); err != nil {
		t.Fatalf("setup insert into %s: %v", table, err)
	}
}

func (e *schemaEnv) exec(sql string, args ...any) error {
	_, err := e.pool.Exec(context.Background(), sql, args...)
	return err
}

// sqlState returns the PostgreSQL error, or nil when err is not one.
func pgErr(err error) *pgconn.PgError {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe
	}
	return nil
}

// wantState fails unless err is a PostgreSQL error with exactly the given SQLSTATE.
func wantState(t *testing.T, err error, code string) *pgconn.PgError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected SQLSTATE %s, got success", code)
	}
	pe := pgErr(err)
	if pe == nil {
		t.Fatalf("expected SQLSTATE %s, got non-PostgreSQL error: %v", code, err)
	}
	if pe.Code != code {
		t.Fatalf("expected SQLSTATE %s, got %s (%s)", code, pe.Code, pe.Message)
	}
	return pe
}

// wantUnique expects 23505 on the named unique index.
func wantUnique(t *testing.T, err error, constraint string) {
	t.Helper()
	pe := wantState(t, err, unique)
	if pe.ConstraintName != constraint {
		t.Fatalf("unique violation on %q, want %q", pe.ConstraintName, constraint)
	}
}

func wantOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

var schemaT0 = time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

// ---- row builders: every default is valid, tests override one column at a time ----

var mobileSeq int

func (e *schemaEnv) userRow(over row) row {
	mobileSeq++
	id := uuid.New()
	base := row{
		"id":                  id,
		"org_id":              e.org,
		"role":                "client",
		"status":              "active",
		"name":                "Asha Mehta",
		"email":               fmt.Sprintf("u%s@example.com", id.String()[:8]),
		"mobile":              fmt.Sprintf("+9198%08d", mobileSeq),
		"password_hash":       validPHC,
		"password_changed_at": schemaT0,
		"created_at":          schemaT0,
		"updated_at":          schemaT0,
	}
	return base.with(over)
}

func (e *schemaEnv) newUser(t *testing.T, over row) uuid.UUID {
	t.Helper()
	r := e.userRow(over)
	e.mustInsert(t, "users", r)
	return r["id"].(uuid.UUID)
}

func (e *schemaEnv) newAdmin(t *testing.T) uuid.UUID {
	t.Helper()
	return e.newUser(t, row{"role": "admin", "mobile": nil})
}

func (e *schemaEnv) sessionRow(user uuid.UUID, over row) row {
	base := row{
		"id":                  uuid.New(),
		"org_id":              e.org,
		"user_id":             user,
		"device_id":           uuid.New(),
		"device_name":         "Pixel 9",
		"platform":            "android",
		"client_type":         "mobile",
		"created_at":          schemaT0,
		"last_active_at":      schemaT0,
		"idle_expires_at":     schemaT0.Add(30 * 24 * time.Hour),
		"absolute_expires_at": schemaT0.Add(90 * 24 * time.Hour),
	}
	return base.with(over)
}

func (e *schemaEnv) newSession(t *testing.T, user uuid.UUID, over row) uuid.UUID {
	t.Helper()
	r := e.sessionRow(user, over)
	e.mustInsert(t, "device_sessions", r)
	return r["id"].(uuid.UUID)
}

func digest(b byte) []byte {
	d := make([]byte, 32)
	for i := range d {
		d[i] = b
	}
	return d
}

func (e *schemaEnv) tokenRow(session uuid.UUID, hash byte, over row) row {
	base := row{
		"id":         uuid.New(),
		"org_id":     e.org,
		"session_id": session,
		"token_hash": digest(hash),
		"issued_at":  schemaT0,
	}
	return base.with(over)
}

func (e *schemaEnv) newToken(t *testing.T, session uuid.UUID, hash byte, over row) uuid.UUID {
	t.Helper()
	r := e.tokenRow(session, hash, over)
	e.mustInsert(t, "refresh_tokens", r)
	return r["id"].(uuid.UUID)
}

func (e *schemaEnv) mfaRow(user uuid.UUID, over row) row {
	base := row{
		"user_id":    user,
		"org_id":     e.org,
		"updated_at": schemaT0,
	}
	return base.with(over)
}

func (e *schemaEnv) recoveryRow(user uuid.UUID, hash byte, over row) row {
	base := row{
		"id":         uuid.New(),
		"org_id":     e.org,
		"user_id":    user,
		"batch_id":   uuid.New(),
		"code_hash":  digest(hash),
		"created_at": schemaT0,
	}
	return base.with(over)
}

func (e *schemaEnv) challengeRow(user uuid.UUID, hash byte, over row) row {
	base := row{
		"id":          uuid.New(),
		"org_id":      e.org,
		"user_id":     user,
		"token_hash":  digest(hash),
		"purpose":     "verify",
		"device_id":   uuid.New(),
		"device_name": "Chrome on Mac",
		"platform":    "web",
		"client_type": "web",
		"created_at":  schemaT0,
		"expires_at":  schemaT0.Add(5 * time.Minute),
	}
	return base.with(over)
}

func (e *schemaEnv) throttleRow(hash byte, over row) row {
	base := row{
		"org_id":     e.org,
		"scope":      "signin",
		"subject":    digest(hash),
		"updated_at": schemaT0,
	}
	return base.with(over)
}
