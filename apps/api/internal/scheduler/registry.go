package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
)

// Handler performs one job. ctx carries the job's transaction: every database write made through
// db.MustTx(ctx) commits atomically with the job's "done" mark and is rolled back when the lease
// was lost. Handlers MUST be idempotent and MUST read current business data (the payload holds
// ids only). Returning an error schedules a retry (FR-012, FR-017).
type Handler func(ctx context.Context, job Job) error

// KindOptions tunes one registered kind.
type KindOptions struct {
	MaxAttempts int                           // 0 = the default (8)
	Recurring   func(now time.Time) time.Time // non-nil = recurring kind; returns the next run_at (FR-015)
}

// JobSpec is what an owning feature schedules inside its own transaction.
type JobSpec struct {
	OrgID     uuid.UUID
	Kind      string     // must be registered, else ErrUnknownKind
	Key       string     // dedupe key, stable across reschedules, e.g. "ipo:<id>:open"
	Subject   string     // business item, e.g. "ipo:<id>"
	RunAt     time.Time  // required
	ExpiresAt *time.Time // nil = state-change work that always runs, however late (FR-014)
	Payload   any        // JSON-encoded; ids only; at most 8 KB
}

// Job is what a Handler receives.
type Job struct {
	ID        uuid.UUID
	OrgID     uuid.UUID
	Kind      string
	Key       string
	Subject   string
	Payload   json.RawMessage
	RunAt     time.Time
	ExpiresAt *time.Time
	Attempt   int
}

type registration struct {
	handler Handler
	opts    KindOptions
}

// Register sets the handler of a kind. It is called at wiring time and panics on a duplicate or
// malformed kind, a nil handler or an attempt limit outside 0..50.
func (s *Service) Register(kind string, h Handler, opts KindOptions) {
	if h == nil || !kindRE.MatchString(kind) || len(kind) > maxKindLen || opts.MaxAttempts < 0 || opts.MaxAttempts > maxAttemptsLimit {
		panic(fmt.Sprintf("scheduler: invalid registration of kind %q", kind))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.kinds[kind]; dup {
		panic(fmt.Sprintf("scheduler: kind %q registered twice", kind))
	}
	s.kinds[kind] = registration{handler: h, opts: opts}
}

func (s *Service) registrationFor(kind string) (registration, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.kinds[kind]
	return r, ok
}

// maxAttemptsFor resolves the attempt limit of a kind.
func (s *Service) maxAttemptsFor(r registration) int32 {
	if r.opts.MaxAttempts > 0 {
		return int32(r.opts.MaxAttempts) //nolint:gosec // validated to 1..50 in Register
	}
	return int32(s.opts.DefaultMaxAttempts) //nolint:gosec // small constant from Options
}

type recurringKind struct {
	kind string
	reg  registration
}

// recurringKinds lists the registered recurring kinds in a stable order.
func (s *Service) recurringKinds() []recurringKind {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []recurringKind
	for k, r := range s.kinds {
		if r.opts.Recurring != nil {
			out = append(out, recurringKind{kind: k, reg: r})
		}
	}
	slices.SortFunc(out, func(a, b recurringKind) int {
		switch {
		case a.kind < b.kind:
			return -1
		case a.kind > b.kind:
			return 1
		}
		return 0
	})
	return out
}
