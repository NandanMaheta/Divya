package outbox_test

// Shared fixtures for the outbox tests (T032-T034, T038). All files of this package are one
// compile unit: they exercise the public contract in specs/002-scheduler-tick/contracts/go-module-api.md.
//
// Assumptions about names the contract leaves open (implementation must provide exactly these):
//   - outbox.New(txr db.TxRunner, now clock.Func, log *slog.Logger, opts outbox.Options) *outbox.Service
//     and outbox.DefaultOptions() outbox.Options (the plan's wiring line); lease 60 s, send timeout 5 s,
//     default max attempts 8 (data-model section 7). Tests only ever use DefaultOptions().
//   - outbox.RelayBudget{MaxMessages int, Deadline time.Time}: stop after MaxMessages claims or when the
//     injected clock reaches Deadline. outbox.RelaySummary{Sent, Failed, Dead, Expired int}
//     (the msgs_* columns of tick_runs). Both are built only in budgetOf() below.
//   - Sentinel errors outbox.ErrInvalid, outbox.ErrNotFound, outbox.ErrConflict.
//   - Payload "never PAN/tokens/secrets": Add validates input and returns outbox.ErrInvalid.
//
// Time: the fake clock starts at Saturday 14 Nov 2026 10:00 IST, outside quiet hours (21:30-08:00).

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NandanMaheta/swastik/apps/api/internal/outbox"
	"github.com/NandanMaheta/swastik/apps/api/internal/outbox/outboxtest"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/clock"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/dbtest"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
)

// ist returns the instant of the given IST wall-clock time on the given day of November 2026.
func ist(day, hour, minute int) time.Time {
	return istdate.Date{Year: 2026, Month: time.November, Day: day}.At(hour, minute)
}

// t0 is Saturday 14 Nov 2026, 10:00 IST.
var t0 = ist(14, 10, 0)

const testTopic = "task.reminder"

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	txr  db.TxRunner
	clk  *clock.Fake
	svc  *outbox.Service
	org  uuid.UUID
	snd  *outboxtest.Sender
}

// newEnv starts one PostgreSQL container, one org, a fake clock and a service with a recording
// sender registered for testTopic.
func newEnv(t *testing.T) *env {
	t.Helper()
	pool := dbtest.New(t)
	e := &env{t: t, pool: pool, txr: db.NewTxRunner(pool), clk: clock.NewFake(t0), snd: outboxtest.New()}
	e.svc = outbox.New(e.txr, e.clk.Now, slog.New(slog.NewTextHandler(io.Discard, nil)), outbox.DefaultOptions())
	e.org = e.newOrg("Swastik")
	e.svc.RegisterSender(testTopic, e.snd, 0)
	return e
}

// reset empties the outbox tables and restores the clock and the sender so subtests start clean.
func (e *env) reset() {
	e.t.Helper()
	if _, err := e.pool.Exec(e.t.Context(), `TRUNCATE outbox_messages, quiet_hours`); err != nil {
		e.t.Fatalf("reset: %v", err)
	}
	e.clk.Set(t0)
	e.snd.Reset()
}

func (e *env) newOrg(name string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(e.t.Context(), `INSERT INTO orgs (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		e.t.Fatalf("insert org: %v", err)
	}
	return id
}

// inTx runs fn in one transaction on the service's runner.
func (e *env) inTx(fn func(ctx context.Context) error) error {
	return e.txr.InTx(e.t.Context(), fn)
}

// msg builds an admin-broadcast message for the default org; callers adjust fields as needed.
func (e *env) msg(topic, key string) outbox.Message {
	return outbox.Message{
		OrgID:     e.org,
		Topic:     topic,
		Recipient: outbox.Recipient{Kind: "admin"},
		Subject:   "task:" + key,
		DedupeKey: key,
		Payload:   map[string]any{"template": "reminder", "task_id": key},
	}
}

// clientMsg builds a message for one client user.
func (e *env) clientMsg(topic, key string, client uuid.UUID) outbox.Message {
	m := e.msg(topic, key)
	m.Recipient = outbox.Recipient{Kind: "client", ID: &client}
	return m
}

// add records m inside its own committed transaction and returns whether it was inserted.
func (e *env) add(m outbox.Message) bool {
	e.t.Helper()
	var inserted bool
	err := e.inTx(func(ctx context.Context) error {
		var err error
		inserted, err = e.svc.Add(ctx, m)
		return err
	})
	if err != nil {
		e.t.Fatalf("Add(%s/%s): %v", m.Topic, m.DedupeKey, err)
	}
	return inserted
}

// budgetOf builds a relay budget: at most max messages, stopping when the injected clock reaches
// now+within (a negative value means the deadline has already passed).
func (e *env) budgetOf(max int, within time.Duration) outbox.RelayBudget {
	return outbox.RelayBudget{MaxMessages: max, Deadline: e.clk.Now().Add(within)}
}

// relay runs one Relay with the production-sized budget: 250 messages within 45 s.
func (e *env) relay() outbox.RelaySummary {
	e.t.Helper()
	return e.relayWith(e.budgetOf(250, 45*time.Second))
}

func (e *env) relayWith(b outbox.RelayBudget) outbox.RelaySummary {
	e.t.Helper()
	sum, err := e.svc.Relay(e.t.Context(), b)
	if err != nil {
		e.t.Fatalf("Relay: %v", err)
	}
	return sum
}

// msgRow is a raw view of an outbox_messages row.
type msgRow struct {
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
	MaxAttempts        int
	FirstAttemptAt     *time.Time
	LastAttemptAt      *time.Time
	LastError          *string
	LockedUntil        *time.Time
	AlertedAt          *time.Time
	SentAt             *time.Time
	FinishedAt         *time.Time
}

const rowColumns = `id, org_id, topic, subject, dedupe_key, recipient_kind, recipient_id, payload, respects_quiet_hours,
	available_at, expires_at, status, attempts, max_attempts, first_attempt_at, last_attempt_at, last_error,
	locked_until, alerted_at, sent_at, finished_at`

func scanRow(row interface{ Scan(...any) error }) (msgRow, error) {
	var r msgRow
	err := row.Scan(&r.ID, &r.Org, &r.Topic, &r.Subject, &r.DedupeKey, &r.RecipientKind, &r.RecipientID, &r.Payload,
		&r.RespectsQuietHours, &r.AvailableAt, &r.ExpiresAt, &r.Status, &r.Attempts, &r.MaxAttempts, &r.FirstAttemptAt,
		&r.LastAttemptAt, &r.LastError, &r.LockedUntil, &r.AlertedAt, &r.SentAt, &r.FinishedAt)
	return r, err
}

// row returns the single row for (topic, key) in the default org.
func (e *env) row(topic, key string) msgRow {
	e.t.Helper()
	return e.rowOf(e.org, topic, key)
}

func (e *env) rowOf(org uuid.UUID, topic, key string) msgRow {
	e.t.Helper()
	r, err := scanRow(e.pool.QueryRow(e.t.Context(),
		`SELECT `+rowColumns+` FROM outbox_messages WHERE org_id = $1 AND topic = $2 AND dedupe_key = $3`, org, topic, key))
	if err != nil {
		e.t.Fatalf("read row %s/%s: %v", topic, key, err)
	}
	return r
}

func (e *env) rowByID(id uuid.UUID) msgRow {
	e.t.Helper()
	r, err := scanRow(e.pool.QueryRow(e.t.Context(), `SELECT `+rowColumns+` FROM outbox_messages WHERE id = $1`, id))
	if err != nil {
		e.t.Fatalf("read row %s: %v", id, err)
	}
	return r
}

func (e *env) count(where string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.t.Context(), `SELECT count(*) FROM outbox_messages WHERE `+where, args...).Scan(&n); err != nil {
		e.t.Fatalf("count where %s: %v", where, err)
	}
	return n
}

// exec runs raw SQL to arrange a state the public API cannot produce directly (e.g. a message
// that was sent, or one whose lease has expired).
func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.t.Context(), sql, args...); err != nil {
		e.t.Fatalf("exec %q: %v", sql, err)
	}
}

// auditRow is a raw view of an audit_log row written by the outbox.
type auditRow struct {
	Org               uuid.UUID
	ActorID           *uuid.UUID
	ActorKind, Entity string
	EntityID          *uuid.UUID
	Action            string
	Before, After     map[string]any
}

func ptr[T any](v T) *T { return &v }
