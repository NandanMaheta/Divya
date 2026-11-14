// Package scheduler runs the durable, tick-driven scheduled jobs of the platform: owning features
// schedule work in their own transaction, the tick (called by Cloud Scheduler) claims due jobs
// under a lease, runs each handler atomically with its "done" mark, retries failures with backoff,
// expires stale work, relays the outbox and alerts the admin about dead items. Time always comes
// from the injected clock (constitution III).
package scheduler

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/outbox"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/clock"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
)

// Sentinel errors; callers match them with errors.Is.
var (
	ErrUnknownKind = errors.New("scheduler: unknown job kind")
	ErrNotFound    = errors.New("scheduler: not found")
	ErrConflict    = errors.New("scheduler: conflict")
	ErrInvalid     = errors.New("scheduler: invalid input")
)

const (
	maxKindLen       = 64
	maxKeyLen        = 200
	maxAttemptsLimit = 50
	overdueAfter     = 15 * time.Minute
)

var kindRE = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Options tunes the service; DefaultOptions holds the production values (data-model section 7).
type Options struct {
	Lease              time.Duration // how long a claim is valid
	HandlerTimeout     time.Duration // bound on one handler run
	MaxJobs            int           // jobs claimed per tick
	MaxMessages        int           // messages relayed per tick
	JobsPhase          time.Duration // no new job is claimed after this long
	TickBudget         time.Duration // no new message is claimed after this long
	DefaultMaxAttempts int
}

// DefaultOptions returns lease 60 s, handler timeout 10 s, 250 jobs and 250 messages per tick,
// a 30 s jobs phase, a 45 s tick and 8 attempts.
func DefaultOptions() Options {
	return Options{
		Lease: 60 * time.Second, HandlerTimeout: 10 * time.Second, MaxJobs: 250, MaxMessages: 250,
		JobsPhase: 30 * time.Second, TickBudget: 45 * time.Second, DefaultMaxAttempts: 8,
	}
}

// Service schedules, claims and runs jobs.
type Service struct {
	txr      db.TxRunner
	ob       *outbox.Service
	now      clock.Func
	log      *slog.Logger
	instance string
	opts     Options

	mu    sync.RWMutex
	kinds map[string]registration
}

// New builds the service. now is the only source of time; instance is diagnostics only.
func New(txr db.TxRunner, ob *outbox.Service, now clock.Func, log *slog.Logger, instance string, opts Options) *Service {
	return &Service{txr: txr, ob: ob, now: now, log: log, instance: instance, opts: opts, kinds: map[string]registration{}}
}

// JobCounts are the job outcomes of one tick (tick_runs.jobs_*).
type JobCounts struct {
	Run     int `json:"run" doc:"completed successfully"`
	Failed  int `json:"failed" doc:"failed and scheduled for retry"`
	Dead    int `json:"dead"`
	Expired int `json:"expired"`
}

// MessageCounts are the outbox outcomes of one tick (tick_runs.msgs_*).
type MessageCounts struct {
	Sent    int `json:"sent"`
	Failed  int `json:"failed"`
	Dead    int `json:"dead"`
	Expired int `json:"expired"`
}

// TickSummary is the response of the tick (contracts/internal-tick.openapi.yaml).
type TickSummary struct {
	TickID          uuid.UUID     `json:"tick_id"`
	StartedAt       time.Time     `json:"started_at"`
	FinishedAt      time.Time     `json:"finished_at"`
	Jobs            JobCounts     `json:"jobs"`
	Messages        MessageCounts `json:"messages"`
	BudgetExhausted bool          `json:"budget_exhausted" doc:"caps or the time budget stopped the tick early"`
}
