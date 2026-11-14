package db_test

// T021 - migration 00002_scheduler_outbox.sql (specs/002-scheduler-tick/data-model.md 1-4).
//
// Assumptions (the implementation must satisfy them, the tests do not bend):
//   - Table and column names are exactly those of data-model.md; no extra columns.
//   - Check violations are real CHECK constraints (SQLSTATE 23514), not triggers, so a missing
//     table (42P01) can never make a "must be rejected" case pass by accident.
//   - Down removes the four new tables only; 00001 (orgs, audit_log, forbid_mutation) stays.
//   - dbtest.New applies every migration; this file runs with 00002 as the newest migration, and
//     the Down test rolls back until scheduled_jobs is gone.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/dbtest"
	"github.com/NandanMaheta/swastik/apps/api/migrations"
)

const (
	pgCheckViolation  = "23514"
	pgUniqueViolation = "23505"
	pgForeignKey      = "23503"
)

var migT0 = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

// ---------------------------------------------------------------------------------------------
// helpers

func newOrg(ctx context.Context, t *testing.T, pool *pgxpool.Pool, name string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO orgs (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	return id
}

// insertRow inserts one row built from base overridden by over (a nil value in over removes the column).
func insertRow(ctx context.Context, pool *pgxpool.Pool, table string, base, over map[string]any) error {
	cols := map[string]any{}
	for k, v := range base {
		cols[k] = v
	}
	for k, v := range over {
		if v == nil {
			delete(cols, k)
			continue
		}
		cols[k] = v
	}
	names := make([]string, 0, len(cols))
	for k := range cols {
		names = append(names, k)
	}
	sort.Strings(names)
	args := make([]any, 0, len(names))
	ph := make([]string, 0, len(names))
	for i, n := range names {
		args = append(args, cols[n])
		ph = append(ph, fmt.Sprintf("$%d", i+1))
	}
	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(names, ", "), strings.Join(ph, ", "))
	_, err := pool.Exec(ctx, q, args...)
	return err
}

func jobBase(org string) map[string]any {
	return map[string]any{
		"org_id": org, "kind": "ipo.open", "dedupe_key": "ipo:1:open", "subject": "ipo:1", "run_at": migT0,
	}
}

func msgBase(org string) map[string]any {
	return map[string]any{
		"org_id": org, "topic": "task.reminder", "recipient_kind": "admin", "subject": "task:1",
		"dedupe_key": "task:1:slot:10", "available_at": migT0,
	}
}

func wantPgCode(t *testing.T, err error, code string) {
	t.Helper()
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		t.Fatalf("got error %v (%T), want PostgreSQL error %s", err, err, code)
	}
	if pe.Code != code {
		t.Fatalf("got SQLSTATE %s (%s), want %s", pe.Code, pe.Message, code)
	}
}

func uniq(prefix string, n int) string { return fmt.Sprintf("%s%d", prefix, n) }

// ---------------------------------------------------------------------------------------------
// columns

type colSpec struct {
	typ      string
	nullable bool
}

const (
	tstz = "timestamp with time zone"
	i32  = "integer"
	bl   = "boolean"
	txt  = "text"
	uid  = "uuid"
	jb   = "jsonb"
)

var expectedColumns = map[string]map[string]colSpec{
	"scheduled_jobs": {
		"id": {uid, false}, "org_id": {uid, false}, "kind": {txt, false}, "dedupe_key": {txt, false},
		"subject": {txt, false}, "payload": {jb, false}, "run_at": {tstz, false}, "expires_at": {tstz, true},
		"recurring": {bl, false}, "status": {txt, false}, "attempts": {i32, false}, "max_attempts": {i32, false},
		"first_attempt_at": {tstz, true}, "last_attempt_at": {tstz, true}, "last_error": {txt, true},
		"locked_until": {tstz, true}, "lock_token": {uid, true}, "locked_by": {txt, true},
		"rerun": {bl, false}, "cancel_requested": {bl, false}, "alerted_at": {tstz, true},
		"finished_at": {tstz, true}, "created_at": {tstz, false}, "updated_at": {tstz, false},
	},
	"outbox_messages": {
		"id": {uid, false}, "org_id": {uid, false}, "topic": {txt, false}, "recipient_kind": {txt, false},
		"recipient_id": {uid, true}, "subject": {txt, false}, "dedupe_key": {txt, false}, "payload": {jb, false},
		"respects_quiet_hours": {bl, false}, "available_at": {tstz, false}, "expires_at": {tstz, true},
		"status": {txt, false}, "attempts": {i32, false}, "max_attempts": {i32, false},
		"first_attempt_at": {tstz, true}, "last_attempt_at": {tstz, true}, "last_error": {txt, true},
		"locked_until": {tstz, true}, "lock_token": {uid, true}, "locked_by": {txt, true},
		"alerted_at": {tstz, true}, "sent_at": {tstz, true}, "finished_at": {tstz, true},
		"created_at": {tstz, false}, "updated_at": {tstz, false},
	},
	"quiet_hours": {
		"org_id": {uid, false}, "start_minute": {"smallint", false}, "end_minute": {"smallint", false},
		"updated_at": {tstz, false},
	},
	"tick_runs": {
		"id": {uid, false}, "started_at": {tstz, false}, "finished_at": {tstz, true}, "status": {txt, false},
		"instance": {txt, false}, "jobs_run": {i32, false}, "jobs_failed": {i32, false}, "jobs_dead": {i32, false},
		"jobs_expired": {i32, false}, "msgs_sent": {i32, false}, "msgs_failed": {i32, false},
		"msgs_dead": {i32, false}, "msgs_expired": {i32, false}, "budget_exhausted": {bl, false},
	},
}

func TestMigration00002_UpCreatesTablesWithExpectedColumns(t *testing.T) {
	pool := dbtest.New(t)
	ctx := t.Context()

	for table, want := range expectedColumns {
		t.Run(table, func(t *testing.T) {
			rows, err := pool.Query(ctx, `SELECT column_name, data_type, is_nullable FROM information_schema.columns
				WHERE table_schema = current_schema() AND table_name = $1`, table)
			if err != nil {
				t.Fatalf("query columns: %v", err)
			}
			defer rows.Close()
			got := map[string]colSpec{}
			for rows.Next() {
				var name, typ, nullable string
				if err := rows.Scan(&name, &typ, &nullable); err != nil {
					t.Fatalf("scan: %v", err)
				}
				got[name] = colSpec{typ, nullable == "YES"}
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("rows: %v", err)
			}
			if len(got) == 0 {
				t.Fatalf("table %s does not exist (migration 00002 not applied)", table)
			}
			for name, w := range want {
				g, ok := got[name]
				if !ok {
					t.Errorf("%s.%s is missing", table, name)
					continue
				}
				if g != w {
					t.Errorf("%s.%s = %+v, want %+v", table, name, g, w)
				}
			}
			for name := range got {
				if _, ok := want[name]; !ok {
					t.Errorf("%s.%s is not in data-model.md", table, name)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------------------------
// defaults

func TestMigration00002_DefaultsMatchDataModel(t *testing.T) {
	pool := dbtest.New(t)
	ctx := t.Context()
	org := newOrg(ctx, t, pool, "Swastik")

	t.Run("scheduled_jobs", func(t *testing.T) {
		if err := insertRow(ctx, pool, "scheduled_jobs", jobBase(org), nil); err != nil {
			t.Fatalf("minimal insert: %v", err)
		}
		var (
			id, status, payload                                                    string
			attempts, maxAttempts                                                  int
			recurring, rerun, cancelReq                                            bool
			createdSet, updatedSet                                                 bool
			expires, firstAt, lastAt, lockedUntil, alerted, finished, token, owner *string
		)
		err := pool.QueryRow(ctx, `SELECT id::text, status, payload::text, attempts, max_attempts, recurring, rerun,
			cancel_requested, created_at IS NOT NULL, updated_at IS NOT NULL, expires_at::text,
			first_attempt_at::text, last_attempt_at::text, locked_until::text, alerted_at::text, finished_at::text,
			lock_token::text, locked_by FROM scheduled_jobs`).
			Scan(&id, &status, &payload, &attempts, &maxAttempts, &recurring, &rerun, &cancelReq, &createdSet, &updatedSet,
				&expires, &firstAt, &lastAt, &lockedUntil, &alerted, &finished, &token, &owner)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if id == "" || status != "pending" || payload != "{}" || attempts != 0 || maxAttempts != 8 ||
			recurring || rerun || cancelReq || !createdSet || !updatedSet {
			t.Errorf("defaults wrong: id=%q status=%q payload=%q attempts=%d max=%d recurring=%v rerun=%v cancel=%v created=%v updated=%v",
				id, status, payload, attempts, maxAttempts, recurring, rerun, cancelReq, createdSet, updatedSet)
		}
		for name, p := range map[string]*string{"expires_at": expires, "first_attempt_at": firstAt, "last_attempt_at": lastAt,
			"locked_until": lockedUntil, "alerted_at": alerted, "finished_at": finished, "lock_token": token, "locked_by": owner} {
			if p != nil {
				t.Errorf("%s defaults to %q, want NULL", name, *p)
			}
		}
	})

	t.Run("outbox_messages", func(t *testing.T) {
		if err := insertRow(ctx, pool, "outbox_messages", msgBase(org), nil); err != nil {
			t.Fatalf("minimal insert: %v", err)
		}
		var (
			id, status, payload string
			attempts, max       int
			respects            bool
		)
		err := pool.QueryRow(ctx, `SELECT id::text, status, payload::text, attempts, max_attempts, respects_quiet_hours
			FROM outbox_messages`).Scan(&id, &status, &payload, &attempts, &max, &respects)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if id == "" || status != "pending" || payload != "{}" || attempts != 0 || max != 8 || !respects {
			t.Errorf("defaults wrong: id=%q status=%q payload=%q attempts=%d max=%d respects_quiet_hours=%v",
				id, status, payload, attempts, max, respects)
		}
	})

	t.Run("quiet_hours is 21:30-08:00 IST", func(t *testing.T) {
		if err := insertRow(ctx, pool, "quiet_hours", map[string]any{"org_id": org}, nil); err != nil {
			t.Fatalf("minimal insert: %v", err)
		}
		var start, end int
		if err := pool.QueryRow(ctx, `SELECT start_minute, end_minute FROM quiet_hours WHERE org_id = $1`, org).Scan(&start, &end); err != nil {
			t.Fatalf("read back: %v", err)
		}
		if start != 21*60+30 || end != 8*60 {
			t.Errorf("default window = %d..%d minutes, want 1290..480", start, end)
		}
	})

	t.Run("tick_runs", func(t *testing.T) {
		if err := insertRow(ctx, pool, "tick_runs", map[string]any{"started_at": migT0, "status": "running", "instance": "host-1"}, nil); err != nil {
			t.Fatalf("minimal insert: %v", err)
		}
		var sum int
		var exhausted bool
		err := pool.QueryRow(ctx, `SELECT jobs_run+jobs_failed+jobs_dead+jobs_expired+msgs_sent+msgs_failed+msgs_dead+msgs_expired, budget_exhausted
			FROM tick_runs`).Scan(&sum, &exhausted)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if sum != 0 || exhausted {
			t.Errorf("counters default to %d, budget_exhausted=%v; want all zero/false", sum, exhausted)
		}
	})
}

// ---------------------------------------------------------------------------------------------
// check constraints

func TestMigration00002_ScheduledJobsCheckConstraints(t *testing.T) {
	pool := dbtest.New(t)
	ctx := t.Context()
	org := newOrg(ctx, t, pool, "Swastik")
	longKind := "a." + strings.Repeat("b", 63) // 65 chars
	okKind := "a." + strings.Repeat("b", 62)   // 64 chars
	bigPayload := `{"x":"` + strings.Repeat("a", 8200) + `"}`

	bad := []struct {
		name string
		over map[string]any
	}{
		{"kind without dot", map[string]any{"kind": "ipo"}},
		{"kind upper case", map[string]any{"kind": "IPO.open"}},
		{"kind starts with digit", map[string]any{"kind": "1ipo.open"}},
		{"kind with dash", map[string]any{"kind": "ipo.open-now"}},
		{"kind over 64 chars", map[string]any{"kind": longKind}},
		{"kind empty", map[string]any{"kind": ""}},
		{"dedupe_key empty", map[string]any{"dedupe_key": ""}},
		{"dedupe_key over 200 chars", map[string]any{"dedupe_key": strings.Repeat("k", 201)}},
		{"subject empty", map[string]any{"subject": ""}},
		{"subject over 200 chars", map[string]any{"subject": strings.Repeat("s", 201)}},
		{"payload over 8 KB", map[string]any{"payload": bigPayload}},
		{"unknown status", map[string]any{"status": "bogus"}},
		{"negative attempts", map[string]any{"attempts": -1}},
		{"max_attempts 0", map[string]any{"max_attempts": 0}},
		{"max_attempts 51", map[string]any{"max_attempts": 51}},
		{"running without lease", map[string]any{"status": "running"}},
		{"running without token", map[string]any{"status": "running", "locked_until": migT0.Add(time.Minute)}},
		{"running without locked_until", map[string]any{"status": "running", "lock_token": "0197a000-0000-7000-8000-000000000001"}},
		{"done without finished_at", map[string]any{"status": "done"}},
		{"cancelled without finished_at", map[string]any{"status": "cancelled"}},
		{"expired without finished_at", map[string]any{"status": "expired"}},
		{"dead without finished_at", map[string]any{"status": "dead"}},
		{"rerun and cancel_requested together", map[string]any{"rerun": true, "cancel_requested": true}},
	}
	for i, tt := range bad {
		t.Run("rejects "+tt.name, func(t *testing.T) {
			over := map[string]any{"dedupe_key": uniq("bad-", i)}
			for k, v := range tt.over {
				over[k] = v
			}
			wantPgCode(t, insertRow(ctx, pool, "scheduled_jobs", jobBase(org), over), pgCheckViolation)
		})
	}

	good := []struct {
		name string
		over map[string]any
	}{
		{"kind with two dots and underscores", map[string]any{"kind": "a.b_c.d"}},
		{"kind of exactly 64 chars", map[string]any{"kind": okKind}},
		{"dedupe_key of exactly 200 chars", map[string]any{"dedupe_key": strings.Repeat("k", 200)}},
		{"max_attempts 1", map[string]any{"max_attempts": 1}},
		{"max_attempts 50", map[string]any{"max_attempts": 50}},
		{"running with lease and token", map[string]any{"status": "running", "locked_until": migT0.Add(time.Minute), "lock_token": "0197a000-0000-7000-8000-000000000001"}},
		{"done with finished_at", map[string]any{"status": "done", "finished_at": migT0}},
		{"dead with finished_at", map[string]any{"status": "dead", "finished_at": migT0}},
		{"rerun alone", map[string]any{"rerun": true}},
		{"cancel_requested alone", map[string]any{"cancel_requested": true}},
		{"recurring flag", map[string]any{"recurring": true}},
	}
	for i, tt := range good {
		t.Run("accepts "+tt.name, func(t *testing.T) {
			over := map[string]any{"dedupe_key": uniq("good-", i)}
			for k, v := range tt.over {
				over[k] = v
			}
			if err := insertRow(ctx, pool, "scheduled_jobs", jobBase(org), over); err != nil {
				t.Fatalf("valid row rejected: %v", err)
			}
		})
	}

	t.Run("accepts a payload of exactly 8192 octets", func(t *testing.T) {
		// jsonb renders {"x": "..."} with one space; 9 octets of framing.
		p := `{"x":"` + strings.Repeat("a", 8192-9) + `"}`
		over := map[string]any{"dedupe_key": "payload-edge", "payload": p}
		if err := insertRow(ctx, pool, "scheduled_jobs", jobBase(org), over); err != nil {
			t.Fatalf("8 KB payload rejected: %v", err)
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT octet_length(payload::text) FROM scheduled_jobs WHERE dedupe_key = 'payload-edge'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 8192 {
			t.Fatalf("test fixture is %d octets, want exactly 8192", n)
		}
	})

	t.Run("org must exist (FR-034)", func(t *testing.T) {
		over := map[string]any{"org_id": "0197a000-0000-7000-8000-0000000000ff", "dedupe_key": "fk"}
		wantPgCode(t, insertRow(ctx, pool, "scheduled_jobs", jobBase(org), over), pgForeignKey)
	})
	t.Run("org is required", func(t *testing.T) {
		err := insertRow(ctx, pool, "scheduled_jobs", jobBase(org), map[string]any{"org_id": nil, "dedupe_key": "no-org"})
		wantPgCode(t, err, "23502") // not_null_violation
	})
}

func TestMigration00002_OutboxMessagesCheckConstraints(t *testing.T) {
	pool := dbtest.New(t)
	ctx := t.Context()
	org := newOrg(ctx, t, pool, "Swastik")
	rid := "0197a000-0000-7000-8000-0000000000aa"
	bigPayload := `{"x":"` + strings.Repeat("a", 8200) + `"}`

	bad := []struct {
		name string
		over map[string]any
	}{
		{"topic without dot", map[string]any{"topic": "reminder"}},
		{"topic upper case", map[string]any{"topic": "Task.reminder"}},
		{"topic over 64 chars", map[string]any{"topic": "a." + strings.Repeat("b", 63)}},
		{"recipient_kind other", map[string]any{"recipient_kind": "staff"}},
		{"client without recipient_id", map[string]any{"recipient_kind": "client"}},
		{"dedupe_key empty", map[string]any{"dedupe_key": ""}},
		{"dedupe_key over 200 chars", map[string]any{"dedupe_key": strings.Repeat("k", 201)}},
		{"payload over 8 KB", map[string]any{"payload": bigPayload}},
		{"unknown status", map[string]any{"status": "queued"}},
		{"negative attempts", map[string]any{"attempts": -1}},
		{"max_attempts 0", map[string]any{"max_attempts": 0}},
		{"max_attempts 51", map[string]any{"max_attempts": 51}},
		{"sending without lease", map[string]any{"status": "sending"}},
		{"sending without token", map[string]any{"status": "sending", "locked_until": migT0.Add(time.Minute)}},
		{"sent without finished_at", map[string]any{"status": "sent"}},
		{"dead without finished_at", map[string]any{"status": "dead"}},
		{"withdrawn without finished_at", map[string]any{"status": "withdrawn"}},
		{"expired without finished_at", map[string]any{"status": "expired"}},
	}
	for i, tt := range bad {
		t.Run("rejects "+tt.name, func(t *testing.T) {
			over := map[string]any{"dedupe_key": uniq("bad-", i)}
			for k, v := range tt.over {
				over[k] = v
			}
			wantPgCode(t, insertRow(ctx, pool, "outbox_messages", msgBase(org), over), pgCheckViolation)
		})
	}

	good := []struct {
		name string
		over map[string]any
	}{
		{"admin broadcast with NULL recipient", map[string]any{"recipient_kind": "admin"}},
		{"client with recipient_id", map[string]any{"recipient_kind": "client", "recipient_id": rid}},
		{"exempt from quiet hours", map[string]any{"respects_quiet_hours": false}},
		{"expiry set", map[string]any{"expires_at": migT0.Add(time.Hour)}},
		{"sent with finished_at and sent_at", map[string]any{"status": "sent", "sent_at": migT0, "finished_at": migT0}},
		{"sending with lease and token", map[string]any{"status": "sending", "locked_until": migT0.Add(time.Minute), "lock_token": "0197a000-0000-7000-8000-000000000001"}},
	}
	for i, tt := range good {
		t.Run("accepts "+tt.name, func(t *testing.T) {
			over := map[string]any{"dedupe_key": uniq("good-", i)}
			for k, v := range tt.over {
				over[k] = v
			}
			if err := insertRow(ctx, pool, "outbox_messages", msgBase(org), over); err != nil {
				t.Fatalf("valid row rejected: %v", err)
			}
		})
	}

	t.Run("available_at is required", func(t *testing.T) {
		err := insertRow(ctx, pool, "outbox_messages", msgBase(org), map[string]any{"available_at": nil, "dedupe_key": "no-avail"})
		wantPgCode(t, err, "23502")
	})
	t.Run("org must exist", func(t *testing.T) {
		over := map[string]any{"org_id": "0197a000-0000-7000-8000-0000000000ff", "dedupe_key": "fk"}
		wantPgCode(t, insertRow(ctx, pool, "outbox_messages", msgBase(org), over), pgForeignKey)
	})
}

func TestMigration00002_QuietHoursAndTickRunsCheckConstraints(t *testing.T) {
	pool := dbtest.New(t)
	ctx := t.Context()
	org2 := newOrg(ctx, t, pool, "Second")

	qh := []struct {
		name       string
		start, end int
		ok         bool
	}{
		{"wrapping window 21:30-08:00", 1290, 480, true},
		{"non wrapping window 13:00-14:00", 780, 840, true},
		{"minute 0 and 1439 are valid", 0, 1439, true},
		{"start negative", -1, 480, false},
		{"start 1440", 1440, 480, false},
		{"end negative", 1290, -1, false},
		{"end 1440", 1290, 1440, false},
		{"start equals end", 600, 600, false},
	}
	for i, tt := range qh {
		t.Run("quiet_hours "+tt.name, func(t *testing.T) {
			o := newOrg(ctx, t, pool, uniq("qh-org-", i)) // org_id is the primary key: one org per case
			err := insertRow(ctx, pool, "quiet_hours", map[string]any{"org_id": o, "start_minute": tt.start, "end_minute": tt.end}, nil)
			if tt.ok {
				if err != nil {
					t.Fatalf("valid window rejected: %v", err)
				}
				return
			}
			wantPgCode(t, err, pgCheckViolation)
		})
	}
	t.Run("quiet_hours has one row per org", func(t *testing.T) {
		if err := insertRow(ctx, pool, "quiet_hours", map[string]any{"org_id": org2}, nil); err != nil {
			t.Fatalf("first row: %v", err)
		}
		wantPgCode(t, insertRow(ctx, pool, "quiet_hours", map[string]any{"org_id": org2}, nil), pgUniqueViolation)
	})
	t.Run("quiet_hours org must exist", func(t *testing.T) {
		wantPgCode(t, insertRow(ctx, pool, "quiet_hours", map[string]any{"org_id": "0197a000-0000-7000-8000-0000000000ff"}, nil), pgForeignKey)
	})

	tickBase := map[string]any{"started_at": migT0, "status": "running", "instance": "host-1"}
	t.Run("tick_runs accepts running, ok and error", func(t *testing.T) {
		for _, s := range []string{"running", "ok", "error"} {
			if err := insertRow(ctx, pool, "tick_runs", tickBase, map[string]any{"status": s}); err != nil {
				t.Errorf("status %q rejected: %v", s, err)
			}
		}
	})
	t.Run("tick_runs rejects unknown status", func(t *testing.T) {
		wantPgCode(t, insertRow(ctx, pool, "tick_runs", tickBase, map[string]any{"status": "done"}), pgCheckViolation)
	})
	for _, col := range []string{"jobs_run", "jobs_failed", "jobs_dead", "jobs_expired", "msgs_sent", "msgs_failed", "msgs_dead", "msgs_expired"} {
		t.Run("tick_runs rejects negative "+col, func(t *testing.T) {
			wantPgCode(t, insertRow(ctx, pool, "tick_runs", tickBase, map[string]any{col: -1}), pgCheckViolation)
		})
	}
}

// ---------------------------------------------------------------------------------------------
// uniqueness

func TestMigration00002_ScheduledJobsActiveKeyIsUnique_FR009(t *testing.T) {
	pool := dbtest.New(t)
	ctx := t.Context()
	org := newOrg(ctx, t, pool, "Swastik")
	other := newOrg(ctx, t, pool, "Other")
	running := map[string]any{"status": "running", "locked_until": migT0.Add(time.Minute), "lock_token": "0197a000-0000-7000-8000-000000000001"}
	finished := func(status string) map[string]any { return map[string]any{"status": status, "finished_at": migT0} }

	t.Run("second pending job with the same key is rejected", func(t *testing.T) {
		over := map[string]any{"dedupe_key": "k-pending"}
		if err := insertRow(ctx, pool, "scheduled_jobs", jobBase(org), over); err != nil {
			t.Fatal(err)
		}
		wantPgCode(t, insertRow(ctx, pool, "scheduled_jobs", jobBase(org), over), pgUniqueViolation)
	})
	t.Run("pending next to a running job with the same key is rejected", func(t *testing.T) {
		if err := insertRow(ctx, pool, "scheduled_jobs", jobBase(org), mergeMaps(running, map[string]any{"dedupe_key": "k-run"})); err != nil {
			t.Fatal(err)
		}
		wantPgCode(t, insertRow(ctx, pool, "scheduled_jobs", jobBase(org), map[string]any{"dedupe_key": "k-run"}), pgUniqueViolation)
	})
	for _, st := range []string{"done", "cancelled", "expired", "dead"} {
		t.Run("a "+st+" job does not block a new pending job with the same key (US5.6)", func(t *testing.T) {
			key := "k-hist-" + st
			if err := insertRow(ctx, pool, "scheduled_jobs", jobBase(org), mergeMaps(finished(st), map[string]any{"dedupe_key": key})); err != nil {
				t.Fatal(err)
			}
			if err := insertRow(ctx, pool, "scheduled_jobs", jobBase(org), map[string]any{"dedupe_key": key}); err != nil {
				t.Fatalf("new pending job rejected next to a %s one: %v", st, err)
			}
		})
	}
	t.Run("finished rows may repeat a key", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			if err := insertRow(ctx, pool, "scheduled_jobs", jobBase(org), mergeMaps(finished("done"), map[string]any{"dedupe_key": "k-twice-done"})); err != nil {
				t.Fatalf("done row %d: %v", i, err)
			}
		}
	})
	t.Run("cancelling frees the key", func(t *testing.T) {
		over := map[string]any{"dedupe_key": "k-cancel"}
		if err := insertRow(ctx, pool, "scheduled_jobs", jobBase(org), over); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE scheduled_jobs SET status='cancelled', finished_at=$1 WHERE dedupe_key='k-cancel'`, migT0); err != nil {
			t.Fatal(err)
		}
		if err := insertRow(ctx, pool, "scheduled_jobs", jobBase(org), over); err != nil {
			t.Fatalf("key not freed after cancel: %v", err)
		}
	})
	t.Run("same key in another org or another kind is independent", func(t *testing.T) {
		over := map[string]any{"dedupe_key": "k-scope"}
		if err := insertRow(ctx, pool, "scheduled_jobs", jobBase(org), over); err != nil {
			t.Fatal(err)
		}
		if err := insertRow(ctx, pool, "scheduled_jobs", jobBase(other), over); err != nil {
			t.Errorf("other org rejected: %v", err)
		}
		if err := insertRow(ctx, pool, "scheduled_jobs", jobBase(org), mergeMaps(over, map[string]any{"kind": "ipo.close"})); err != nil {
			t.Errorf("other kind rejected: %v", err)
		}
	})
}

func TestMigration00002_OutboxDedupeKeyIsUniqueAcrossAllStatuses_FR024(t *testing.T) {
	pool := dbtest.New(t)
	ctx := t.Context()
	org := newOrg(ctx, t, pool, "Swastik")
	other := newOrg(ctx, t, pool, "Other")

	for _, st := range []map[string]any{
		{"status": "pending"},
		{"status": "sent", "sent_at": migT0, "finished_at": migT0},
		{"status": "withdrawn", "finished_at": migT0},
		{"status": "expired", "finished_at": migT0},
		{"status": "dead", "finished_at": migT0},
	} {
		key := "k-" + st["status"].(string)
		t.Run("duplicate of a "+st["status"].(string)+" message is rejected", func(t *testing.T) {
			if err := insertRow(ctx, pool, "outbox_messages", msgBase(org), mergeMaps(st, map[string]any{"dedupe_key": key})); err != nil {
				t.Fatal(err)
			}
			wantPgCode(t, insertRow(ctx, pool, "outbox_messages", msgBase(org), map[string]any{"dedupe_key": key}), pgUniqueViolation)
		})
	}
	t.Run("same key under another topic or org is independent", func(t *testing.T) {
		over := map[string]any{"dedupe_key": "k-scope"}
		if err := insertRow(ctx, pool, "outbox_messages", msgBase(org), over); err != nil {
			t.Fatal(err)
		}
		if err := insertRow(ctx, pool, "outbox_messages", msgBase(org), mergeMaps(over, map[string]any{"topic": "task.other"})); err != nil {
			t.Errorf("other topic rejected: %v", err)
		}
		if err := insertRow(ctx, pool, "outbox_messages", msgBase(other), over); err != nil {
			t.Errorf("other org rejected: %v", err)
		}
	})
}

func mergeMaps(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// ---------------------------------------------------------------------------------------------
// indexes and mutability

func TestMigration00002_IndexesExist(t *testing.T) {
	pool := dbtest.New(t)
	ctx := t.Context()

	indexDef := func(name string) string {
		var def string
		err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() AND indexname = $1`, name).Scan(&def)
		if err != nil {
			return ""
		}
		return def
	}

	for _, name := range []string{
		"scheduled_jobs_active_key_uq", "scheduled_jobs_due_idx", "scheduled_jobs_lease_idx", "scheduled_jobs_expiry_idx",
		"scheduled_jobs_subject_idx", "scheduled_jobs_failed_idx", "scheduled_jobs_unalerted_idx", "scheduled_jobs_retention_idx",
	} {
		if indexDef(name) == "" {
			t.Errorf("index %s (data-model.md section 1) is missing", name)
		}
	}
	def := indexDef("scheduled_jobs_active_key_uq")
	for _, want := range []string{"UNIQUE", "org_id, kind, dedupe_key", "pending", "running"} {
		if !strings.Contains(def, want) {
			t.Errorf("scheduled_jobs_active_key_uq = %q, want it to contain %q", def, want)
		}
	}

	hasIndex := func(table string, parts ...string) bool {
		rows, err := pool.Query(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() AND tablename = $1`, table)
		if err != nil {
			t.Fatalf("pg_indexes: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var d string
			if err := rows.Scan(&d); err != nil {
				t.Fatal(err)
			}
			ok := true
			for _, p := range parts {
				ok = ok && strings.Contains(d, p)
			}
			if ok {
				return true
			}
		}
		return false
	}
	if !hasIndex("outbox_messages", "(available_at)", "pending") {
		t.Error("outbox_messages needs a partial index on (available_at) for status = 'pending' (relay claim)")
	}
	if !hasIndex("outbox_messages", "(locked_until)", "sending") {
		t.Error("outbox_messages needs a partial index on (locked_until) for status = 'sending'")
	}
	if !hasIndex("outbox_messages", "org_id, subject", "pending") {
		t.Error("outbox_messages needs a partial index on (org_id, subject) for pending (withdraw by item)")
	}
	if !hasIndex("outbox_messages", "UNIQUE", "org_id, topic, dedupe_key") {
		t.Error("outbox_messages needs UNIQUE (org_id, topic, dedupe_key)")
	}
	if !hasIndex("tick_runs", "started_at DESC") {
		t.Error("tick_runs needs an index on (started_at DESC)")
	}
}

// The four tables are operational queues, not ledgers (owner decision, data-model.md intro):
// they must stay updatable and deletable, i.e. no forbid_mutation() trigger.
func TestMigration00002_TablesAreMutableAndHaveNoAppendOnlyTrigger(t *testing.T) {
	pool := dbtest.New(t)
	ctx := t.Context()
	org := newOrg(ctx, t, pool, "Swastik")

	var n int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_trigger tg JOIN pg_class c ON c.oid = tg.tgrelid
		WHERE NOT tg.tgisinternal AND c.relname IN ('scheduled_jobs','outbox_messages','quiet_hours','tick_runs')`).Scan(&n)
	if err != nil {
		t.Fatalf("pg_trigger: %v", err)
	}
	// Zero rows would also come from missing tables, so insist the tables exist first.
	var tables int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname = current_schema()
		AND tablename IN ('scheduled_jobs','outbox_messages','quiet_hours','tick_runs')`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 4 {
		t.Fatalf("found %d of the 4 scheduler/outbox tables (migration 00002 not applied)", tables)
	}
	if n != 0 {
		t.Fatalf("%d triggers on the queue tables; they must not be append-only", n)
	}

	must := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := insertRow(ctx, pool, "scheduled_jobs", jobBase(org), nil); err != nil {
		t.Fatal(err)
	}
	if err := insertRow(ctx, pool, "outbox_messages", msgBase(org), nil); err != nil {
		t.Fatal(err)
	}
	if err := insertRow(ctx, pool, "quiet_hours", map[string]any{"org_id": org}, nil); err != nil {
		t.Fatal(err)
	}
	if err := insertRow(ctx, pool, "tick_runs", map[string]any{"started_at": migT0, "status": "running", "instance": "h"}, nil); err != nil {
		t.Fatal(err)
	}
	must(`UPDATE scheduled_jobs SET attempts = attempts + 1`)
	must(`UPDATE outbox_messages SET attempts = attempts + 1`)
	must(`UPDATE quiet_hours SET start_minute = 1200`)
	must(`UPDATE tick_runs SET status = 'ok', finished_at = $1`, migT0)
	must(`DELETE FROM scheduled_jobs`)
	must(`DELETE FROM outbox_messages`)
	must(`DELETE FROM quiet_hours`)
	must(`DELETE FROM tick_runs`)
}

// ---------------------------------------------------------------------------------------------
// down

func tableExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var ok bool
	if err := pool.QueryRow(t.Context(), `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&ok); err != nil {
		t.Fatalf("to_regclass(%s): %v", name, err)
	}
	return ok
}

func TestMigration00002_DownDropsTheFourTablesAndLeaves00001Intact(t *testing.T) {
	pool := dbtest.New(t)
	ctx := t.Context()
	org := newOrg(ctx, t, pool, "Swastik")
	var auditID string
	if err := pool.QueryRow(ctx, `INSERT INTO audit_log (org_id, actor_kind, entity, action) VALUES ($1, 'system', 'org', 'create') RETURNING id`, org).Scan(&auditID); err != nil {
		t.Fatalf("insert audit: %v", err)
	}
	for _, table := range []string{"scheduled_jobs", "outbox_messages", "quiet_hours", "tick_runs"} {
		if !tableExists(t, pool, table) {
			t.Fatalf("precondition: %s does not exist after Up (migration 00002 not applied)", table)
		}
	}

	// Roll back until the scheduler tables are gone (00002 is the newest migration today).
	for i := 0; i < 5 && tableExists(t, pool, "scheduled_jobs"); i++ {
		if err := db.Migrate(ctx, pool, migrations.FS, "down"); err != nil {
			t.Fatalf("down: %v", err)
		}
	}
	for _, table := range []string{"scheduled_jobs", "outbox_messages", "quiet_hours", "tick_runs"} {
		if tableExists(t, pool, table) {
			t.Errorf("%s still exists after Down", table)
		}
	}

	for _, table := range []string{"orgs", "audit_log"} {
		if !tableExists(t, pool, table) {
			t.Errorf("Down removed 00001 table %s", table)
		}
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE id = $1`, auditID).Scan(&rows); err != nil || rows != 1 {
		t.Errorf("audit row lost by Down: rows=%d err=%v", rows, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET action = 'x' WHERE id = $1`, auditID); err == nil {
		t.Error("audit_log is no longer append-only after Down (forbid_mutation or trigger removed)")
	}
	var applied int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM goose_db_version WHERE version_id = 1 AND is_applied`).Scan(&applied); err != nil || applied == 0 {
		t.Errorf("goose does not report 00001 as applied (count=%d err=%v)", applied, err)
	}

	// Reversible: Up again recreates the tables.
	if err := db.Migrate(ctx, pool, migrations.FS, "up"); err != nil {
		t.Fatalf("up after down: %v", err)
	}
	if !tableExists(t, pool, "scheduled_jobs") || !tableExists(t, pool, "outbox_messages") {
		t.Error("tables not recreated by Up after Down")
	}
}
