// Package schedulertest is the shared harness for the scheduler tests (and for later features
// that prove their schedules against a fake clock, plan "Fixtures"). It lives in a non-test
// package so other modules' tests can import it.
//
// One Harness = one dbtest container, one org, a fake clock, an outbox and a scheduler service
// wired exactly like cmd/swastik will wire them, and an outboxtest.Sender registered for Topic.
// Tests start one Harness per top-level test and use Run for scenarios; Run resets the tables,
// the clock, the sender and the handlers first.
//
// Assumptions about the scheduler contract that go-module-api.md leaves open (the implementation
// must provide exactly these; they are used by the harness and by the scheduler tests):
//   - scheduler.New(txr db.TxRunner, ob *outbox.Service, now clock.Func, log *slog.Logger,
//     instance string, opts scheduler.Options) *scheduler.Service (the plan's wiring line) and
//     scheduler.DefaultOptions(): lease 60 s, handler timeout 10 s, 250 jobs and 250 messages per
//     tick, jobs phase 30 s and whole tick 45 s (data-model section 7). Tests only use DefaultOptions().
//   - scheduler.TickSummary has TickID uuid.UUID, StartedAt/FinishedAt time.Time,
//     Jobs{Run, Failed, Dead, Expired int}, Messages{Sent, Failed, Dead, Expired int},
//     BudgetExhausted bool, and marshals to the TickSummary schema of contracts/internal-tick.openapi.yaml.
//   - scheduler.RetentionKind ("scheduler.retention"), (*Service).RetentionHandler() Handler and
//     scheduler.RetentionOptions() KindOptions (recurring, daily 03:00 IST) are what main registers.
//   - scheduler.ErrUnknownKind, ErrNotFound, ErrConflict, ErrInvalid sentinels.
package schedulertest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NandanMaheta/swastik/apps/api/internal/outbox"
	"github.com/NandanMaheta/swastik/apps/api/internal/outbox/outboxtest"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/clock"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db/dbgen"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/dbtest"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
)

// Topic is the outbox topic the harness registers Sender for.
const Topic = "sample.notice"

// Lease is the allowed run time of a claim (data-model section 7).
const Lease = 60 * time.Second

// Instance is the instance name the harness gives the scheduler service.
const Instance = "test-host"

// Nov returns the instant of the given IST wall-clock time on a day of November 2026.
func Nov(day, hour, minute int) time.Time {
	return istdate.Date{Year: 2026, Month: time.November, Day: day}.At(hour, minute)
}

// Start is Saturday 14 Nov 2026, 10:00 IST: outside quiet hours, a weekday-independent fixture.
var Start = Nov(14, 10, 0)

// Harness is one test environment. Copies made by Run share the pool, services and handlers.
type Harness struct {
	T      *testing.T
	Pool   *pgxpool.Pool
	Txr    db.TxRunner
	Clock  *clock.Fake
	Outbox *outbox.Service
	Sched  *scheduler.Service
	Sender *outboxtest.Sender
	Org    uuid.UUID

	st *state
}

type state struct {
	mu  sync.Mutex
	fns map[string]scheduler.Handler
}

// NewHarness starts a PostgreSQL container, one org and the services (skipped under -short).
func NewHarness(t *testing.T) *Harness {
	t.Helper()
	pool := dbtest.New(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	txr := db.NewTxRunner(pool)
	clk := clock.NewFake(Start)
	ob := outbox.New(txr, clk.Now, log, outbox.DefaultOptions())
	h := &Harness{
		T: t, Pool: pool, Txr: txr, Clock: clk, Outbox: ob, Sender: outboxtest.New(),
		Sched: scheduler.New(txr, ob, clk.Now, log, Instance, scheduler.DefaultOptions()),
		st:    &state{fns: map[string]scheduler.Handler{}},
	}
	ob.RegisterSender(Topic, h.Sender, 0)
	h.Exec(`CREATE TABLE sample_effects (id bigserial PRIMARY KEY, key text NOT NULL, at timestamptz NOT NULL DEFAULT now())`)
	h.Org = h.NewOrg("Swastik")
	return h
}

// Run runs fn as a subtest with a clean slate: tables emptied, other orgs removed, clock at Start,
// sender and handlers reset. fn receives a Harness bound to the subtest's *testing.T.
func (h *Harness) Run(name string, fn func(h *Harness)) {
	h.T.Helper()
	h.T.Run(name, func(t *testing.T) {
		sub := *h
		sub.T = t
		sub.Reset()
		fn(&sub)
	})
}

// Reset empties scheduled_jobs, outbox_messages, quiet_hours, tick_runs, audit_log and the sample
// effects, drops every org but the harness org, and restores the clock, the sender and the handlers.
func (h *Harness) Reset() {
	h.T.Helper()
	h.Exec(`TRUNCATE scheduled_jobs, outbox_messages, quiet_hours, tick_runs, audit_log, sample_effects`)
	h.Exec(`DELETE FROM orgs WHERE id <> $1`, h.Org)
	h.Clock.Set(Start)
	h.Sender.Reset()
	h.st.mu.Lock()
	for k := range h.st.fns {
		h.st.fns[k] = nil
	}
	h.st.mu.Unlock()
}

// Exec runs raw SQL, failing the test on error.
func (h *Harness) Exec(sql string, args ...any) {
	h.T.Helper()
	if _, err := h.Pool.Exec(h.T.Context(), sql, args...); err != nil {
		h.T.Fatalf("exec %q: %v", sql, err)
	}
}

// Count returns count(*) of a table with a WHERE clause body (use "true" for all rows).
func (h *Harness) Count(table, where string, args ...any) int {
	h.T.Helper()
	var n int
	if err := h.Pool.QueryRow(h.T.Context(), `SELECT count(*) FROM `+table+` WHERE `+where, args...).Scan(&n); err != nil {
		h.T.Fatalf("count %s where %s: %v", table, where, err)
	}
	return n
}

// NewOrg inserts another organisation.
func (h *Harness) NewOrg(name string) uuid.UUID {
	h.T.Helper()
	var id uuid.UUID
	if err := h.Pool.QueryRow(h.T.Context(), `INSERT INTO orgs (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		h.T.Fatalf("insert org: %v", err)
	}
	return id
}

// InTx runs fn in one transaction on the service's runner.
func (h *Harness) InTx(fn func(ctx context.Context) error) error {
	return h.Txr.InTx(h.T.Context(), fn)
}

// ---------------------------------------------------------------------------------------------
// Handlers

// Handle registers kind with default KindOptions on first use and (re)sets its behaviour. Handlers
// can be swapped between subtests because the harness registers a trampoline once; KindOptions
// of the first call for a kind win, so use a distinct kind for a different MaxAttempts/Recurring.
func (h *Harness) Handle(kind string, fn scheduler.Handler) {
	h.HandleWith(kind, fn, scheduler.KindOptions{})
}

// HandleWith is Handle with explicit KindOptions.
func (h *Harness) HandleWith(kind string, fn scheduler.Handler, opts scheduler.KindOptions) {
	h.T.Helper()
	h.st.mu.Lock()
	_, known := h.st.fns[kind]
	h.st.fns[kind] = fn
	h.st.mu.Unlock()
	if known {
		return
	}
	h.Sched.Register(kind, func(ctx context.Context, job scheduler.Job) error {
		h.st.mu.Lock()
		f := h.st.fns[kind]
		h.st.mu.Unlock()
		if f == nil {
			return errors.New("schedulertest: no handler set for " + kind)
		}
		return f(ctx, job)
	}, opts)
}

// RegisterRetention registers the built-in scheduler.retention kind like cmd/swastik does. It is
// not part of NewHarness so that tests counting jobs are not disturbed by the recurring row.
func (h *Harness) RegisterRetention() {
	h.T.Helper()
	h.Sched.Register(scheduler.RetentionKind, h.Sched.RetentionHandler(), scheduler.RetentionOptions())
}

// Effect records one business effect for key through the transaction in ctx, so it commits and
// rolls back together with the job's completion mark.
func (h *Harness) Effect(ctx context.Context, key string) error {
	tx, err := db.MustTx(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO sample_effects (key) VALUES ($1)`, key)
	return err
}

// Effects returns how many committed effects exist for key.
func (h *Harness) Effects(key string) int {
	h.T.Helper()
	return h.Count("sample_effects", "key = $1", key)
}

// ---------------------------------------------------------------------------------------------
// Scheduling and ticking

// Spec builds a JobSpec for the harness org: subject "sample:<key>", payload {"key": key}.
func (h *Harness) Spec(kind, key string, runAt time.Time) scheduler.JobSpec {
	return scheduler.JobSpec{OrgID: h.Org, Kind: kind, Key: key, Subject: "sample:" + key, RunAt: runAt, Payload: map[string]string{"key": key}}
}

// Schedule commits spec in its own transaction.
func (h *Harness) Schedule(spec scheduler.JobSpec) {
	h.T.Helper()
	if err := h.InTx(func(ctx context.Context) error { return h.Sched.Schedule(ctx, spec) }); err != nil {
		h.T.Fatalf("Schedule(%s/%s): %v", spec.Kind, spec.Key, err)
	}
}

// Tick runs one tick at the current fake instant.
func (h *Harness) Tick() scheduler.TickSummary {
	h.T.Helper()
	sum, err := h.Sched.Tick(h.T.Context())
	if err != nil {
		h.T.Fatalf("Tick: %v", err)
	}
	return sum
}

// RunTicks sets the clock to from, then ticks at from, from+step, ... while the instant is not
// after to. It returns every summary in order. The clock is left at the last tick instant.
func (h *Harness) RunTicks(from, to time.Time, step time.Duration) []scheduler.TickSummary {
	h.T.Helper()
	var out []scheduler.TickSummary
	for at := from; !at.After(to); at = at.Add(step) {
		h.Clock.Set(at)
		out = append(out, h.Tick())
	}
	return out
}

// ClaimOnly claims the oldest due job exactly like a tick does (same query, 60 s lease) and then
// "crashes": it never completes or fails it. ok is false when nothing is due.
func (h *Harness) ClaimOnly() (job dbgen.ScheduledJob, ok bool) {
	h.T.Helper()
	now := h.Clock.Now()
	job, err := dbgen.New(h.Pool).ClaimJob(h.T.Context(), dbgen.ClaimJobParams{
		Now: db.Timestamptz(now), LockedUntil: db.Timestamptz(now.Add(Lease)), LockedBy: db.Text("crashed-host"),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.ScheduledJob{}, false
	}
	if err != nil {
		h.T.Fatalf("claim: %v", err)
	}
	return job, true
}

// ---------------------------------------------------------------------------------------------
// Rows

// JobRow is a raw view of a scheduled_jobs row.
type JobRow struct {
	ID              uuid.UUID
	Org             uuid.UUID
	Kind, Key       string
	Subject         string
	Payload         []byte
	RunAt           time.Time
	ExpiresAt       *time.Time
	Recurring       bool
	Status          string
	Attempts        int
	MaxAttempts     int
	FirstAttemptAt  *time.Time
	LastAttemptAt   *time.Time
	LastError       *string
	LockedUntil     *time.Time
	LockToken       *uuid.UUID
	Rerun           bool
	CancelRequested bool
	AlertedAt       *time.Time
	FinishedAt      *time.Time
}

const jobColumns = `id, org_id, kind, dedupe_key, subject, payload, run_at, expires_at, recurring, status, attempts, max_attempts,
	first_attempt_at, last_attempt_at, last_error, locked_until, lock_token, rerun, cancel_requested, alerted_at, finished_at`

func scanJob(row pgx.Row) (JobRow, error) {
	var r JobRow
	err := row.Scan(&r.ID, &r.Org, &r.Kind, &r.Key, &r.Subject, &r.Payload, &r.RunAt, &r.ExpiresAt, &r.Recurring, &r.Status,
		&r.Attempts, &r.MaxAttempts, &r.FirstAttemptAt, &r.LastAttemptAt, &r.LastError, &r.LockedUntil, &r.LockToken,
		&r.Rerun, &r.CancelRequested, &r.AlertedAt, &r.FinishedAt)
	if err != nil {
		return r, fmt.Errorf("scan job: %w", err)
	}
	return r, nil
}

// Jobs returns every row for (harness org, kind, key), oldest first (done rows stay as history).
func (h *Harness) Jobs(kind, key string) []JobRow { return h.JobsOf(h.Org, kind, key) }

// JobsOf is Jobs for another org.
func (h *Harness) JobsOf(org uuid.UUID, kind, key string) []JobRow {
	h.T.Helper()
	rows, err := h.Pool.Query(h.T.Context(), `SELECT `+jobColumns+` FROM scheduled_jobs WHERE org_id = $1 AND kind = $2 AND dedupe_key = $3 ORDER BY created_at, id`, org, kind, key)
	if err != nil {
		h.T.Fatalf("read jobs: %v", err)
	}
	defer rows.Close()
	var out []JobRow
	for rows.Next() {
		r, err := scanJob(rows)
		if err != nil {
			h.T.Fatalf("scan job: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		h.T.Fatal(err)
	}
	return out
}

// Job returns the single row for (harness org, kind, key) and fails unless there is exactly one.
func (h *Harness) Job(kind, key string) JobRow { return h.JobOf(h.Org, kind, key) }

// JobOf is Job for another org.
func (h *Harness) JobOf(org uuid.UUID, kind, key string) JobRow {
	h.T.Helper()
	rows := h.JobsOf(org, kind, key)
	if len(rows) != 1 {
		h.T.Fatalf("job %s/%s: %d rows, want exactly 1", kind, key, len(rows))
	}
	return rows[0]
}

// JobByID returns the row with this id.
func (h *Harness) JobByID(id uuid.UUID) JobRow {
	h.T.Helper()
	r, err := scanJob(h.Pool.QueryRow(h.T.Context(), `SELECT `+jobColumns+` FROM scheduled_jobs WHERE id = $1`, id))
	if err != nil {
		h.T.Fatalf("read job %s: %v", id, err)
	}
	return r
}

// JobSeed describes a row to insert directly, in any status. Zero fields get sensible defaults:
// Org = harness org, Subject = "sample:<Key>", Status = pending, RunAt = now, MaxAttempts = 8;
// running rows get a lease of Lease from now and a token; finished statuses get FinishedAt = now.
type JobSeed struct {
	Org                           uuid.UUID
	Kind, Key, Subject, Status    string
	RunAt                         time.Time
	ExpiresAt                     *time.Time
	Recurring                     bool
	Attempts, MaxAttempts         int
	FirstAttemptAt, LastAttemptAt *time.Time
	LastError                     string
	LockedUntil                   *time.Time
	AlertedAt, FinishedAt         *time.Time
	Payload                       string // JSON, default {}
}

// SeedJob inserts the row and returns its id.
func (h *Harness) SeedJob(s JobSeed) uuid.UUID {
	h.T.Helper()
	now := h.Clock.Now()
	if s.Org == uuid.Nil {
		s.Org = h.Org
	}
	if s.Subject == "" {
		s.Subject = "sample:" + s.Key
	}
	if s.Status == "" {
		s.Status = "pending"
	}
	if s.RunAt.IsZero() {
		s.RunAt = now
	}
	if s.MaxAttempts == 0 {
		s.MaxAttempts = 8
	}
	if s.Payload == "" {
		s.Payload = "{}"
	}
	var lockedUntil *time.Time
	var token *uuid.UUID
	if s.Status == "running" {
		lu := now.Add(Lease)
		if s.LockedUntil != nil {
			lu = *s.LockedUntil
		}
		lockedUntil = &lu
		tok := uuid.New()
		token = &tok
	}
	finished := s.FinishedAt
	switch s.Status {
	case "done", "dead", "cancelled", "expired":
		if finished == nil {
			finished = &now
		}
	}
	var id uuid.UUID
	err := h.Pool.QueryRow(h.T.Context(), `INSERT INTO scheduled_jobs
		(org_id, kind, dedupe_key, subject, payload, run_at, expires_at, recurring, status, attempts, max_attempts,
		 first_attempt_at, last_attempt_at, last_error, locked_until, lock_token, alerted_at, finished_at)
		VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7,$8,$9,$10,$11,$12,$13,NULLIF($14,''),$15,$16,$17,$18) RETURNING id`,
		s.Org, s.Kind, s.Key, s.Subject, s.Payload, s.RunAt, s.ExpiresAt, s.Recurring, s.Status, s.Attempts, s.MaxAttempts,
		s.FirstAttemptAt, s.LastAttemptAt, s.LastError, lockedUntil, token, s.AlertedAt, finished).Scan(&id)
	if err != nil {
		h.T.Fatalf("seed job %s/%s: %v", s.Kind, s.Key, err)
	}
	return id
}

// MsgRow is a raw view of an outbox_messages row.
type MsgRow struct {
	ID                 uuid.UUID
	Org                uuid.UUID
	Topic, Subject     string
	DedupeKey          string
	RecipientKind      string
	RecipientID        *uuid.UUID
	Payload            []byte
	RespectsQuietHours bool
	AvailableAt        time.Time
	ExpiresAt          *time.Time
	Status             string
	Attempts           int
	LastError          *string
	AlertedAt          *time.Time
	SentAt             *time.Time
	FinishedAt         *time.Time
}

// Messages returns the outbox rows matching a WHERE clause body, oldest first.
func (h *Harness) Messages(where string, args ...any) []MsgRow {
	h.T.Helper()
	rows, err := h.Pool.Query(h.T.Context(), `SELECT id, org_id, topic, subject, dedupe_key, recipient_kind, recipient_id, payload,
		respects_quiet_hours, available_at, expires_at, status, attempts, last_error, alerted_at, sent_at, finished_at
		FROM outbox_messages WHERE `+where+` ORDER BY id`, args...)
	if err != nil {
		h.T.Fatalf("read messages: %v", err)
	}
	defer rows.Close()
	var out []MsgRow
	for rows.Next() {
		var r MsgRow
		if err := rows.Scan(&r.ID, &r.Org, &r.Topic, &r.Subject, &r.DedupeKey, &r.RecipientKind, &r.RecipientID, &r.Payload,
			&r.RespectsQuietHours, &r.AvailableAt, &r.ExpiresAt, &r.Status, &r.Attempts, &r.LastError, &r.AlertedAt, &r.SentAt, &r.FinishedAt); err != nil {
			h.T.Fatalf("scan message: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		h.T.Fatal(err)
	}
	return out
}

// AddMessage records m in its own committed transaction (Org defaults to the harness org,
// Recipient to every admin, Subject to "sample:<key>", Payload to {"k": key}).
func (h *Harness) AddMessage(topic, key string) bool {
	h.T.Helper()
	var inserted bool
	err := h.InTx(func(ctx context.Context) error {
		var err error
		inserted, err = h.Outbox.Add(ctx, outbox.Message{
			OrgID: h.Org, Topic: topic, Recipient: outbox.Recipient{Kind: "admin"}, Subject: "sample:" + key,
			DedupeKey: key, Payload: map[string]string{"k": key},
		})
		return err
	})
	if err != nil {
		h.T.Fatalf("Add(%s/%s): %v", topic, key, err)
	}
	return inserted
}

// MessageSeed describes an outbox row to insert directly. Defaults: harness org, topic Topic,
// status pending, available now; sent rows get sent_at, finished statuses get finished_at.
type MessageSeed struct {
	Org                   uuid.UUID
	Topic, Key, Status    string
	AvailableAt           *time.Time
	Attempts              int
	AlertedAt, FinishedAt *time.Time
}

// SeedMessage inserts the row and returns its id.
func (h *Harness) SeedMessage(s MessageSeed) uuid.UUID {
	h.T.Helper()
	now := h.Clock.Now()
	if s.Org == uuid.Nil {
		s.Org = h.Org
	}
	if s.Topic == "" {
		s.Topic = Topic
	}
	if s.Status == "" {
		s.Status = "pending"
	}
	avail := now
	if s.AvailableAt != nil {
		avail = *s.AvailableAt
	}
	finished := s.FinishedAt
	var sentAt *time.Time
	switch s.Status {
	case "sent", "dead", "withdrawn", "expired":
		if finished == nil {
			finished = &now
		}
		if s.Status == "sent" {
			sentAt = finished
		}
	}
	var id uuid.UUID
	err := h.Pool.QueryRow(h.T.Context(), `INSERT INTO outbox_messages
		(org_id, topic, recipient_kind, subject, dedupe_key, available_at, status, attempts, alerted_at, sent_at, finished_at)
		VALUES ($1,$2,'admin',$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		s.Org, s.Topic, "sample:"+s.Key, s.Key, avail, s.Status, s.Attempts, s.AlertedAt, sentAt, finished).Scan(&id)
	if err != nil {
		h.T.Fatalf("seed message %s/%s: %v", s.Topic, s.Key, err)
	}
	return id
}

// LastTickRun returns the most recent tick_runs row (the test fails when there is none).
func (h *Harness) LastTickRun() dbgen.TickRun {
	h.T.Helper()
	tr, err := dbgen.New(h.Pool).LastTickRun(h.T.Context())
	if err != nil {
		h.T.Fatalf("last tick run: %v", err)
	}
	return tr
}

// AuditRow is a raw view of an audit_log row.
type AuditRow struct {
	Org               uuid.UUID
	ActorID           *uuid.UUID
	ActorKind, Entity string
	EntityID          *uuid.UUID
	Action            string
	Before, After     []byte
}

// Audit returns the audit rows of one entity id, oldest first.
func (h *Harness) Audit(entityID uuid.UUID) []AuditRow {
	h.T.Helper()
	rows, err := h.Pool.Query(h.T.Context(), `SELECT org_id, actor_id, actor_kind, entity, entity_id, action, before, after
		FROM audit_log WHERE entity_id = $1 ORDER BY created_at, id`, entityID)
	if err != nil {
		h.T.Fatalf("read audit: %v", err)
	}
	defer rows.Close()
	var out []AuditRow
	for rows.Next() {
		var r AuditRow
		if err := rows.Scan(&r.Org, &r.ActorID, &r.ActorKind, &r.Entity, &r.EntityID, &r.Action, &r.Before, &r.After); err != nil {
			h.T.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		h.T.Fatal(err)
	}
	return out
}

// SeedTickRun inserts a finished tick_runs row started at the given instant.
func (h *Harness) SeedTickRun(startedAt time.Time) {
	h.T.Helper()
	h.Exec(`INSERT INTO tick_runs (started_at, finished_at, status, instance) VALUES ($1, $1, 'ok', 'seed')`, startedAt)
}
