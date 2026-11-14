package scheduler_test

// Shared fixtures for the scheduler tests (T041-T044, T049, T051, T053). All files of this package
// are one compile unit and run against the harness in schedulertest, which lists the assumptions
// about names the contract leaves open (scheduler.New, DefaultOptions, TickSummary, RetentionKind ...).
//
// Time: the fake clock starts at schedulertest.Start, Saturday 14 Nov 2026 10:00 IST, outside quiet hours.

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/outbox"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler/schedulertest"
)

var (
	start    = schedulertest.Start
	errBoom  = errors.New("boom")
	adminAct = audit.Actor{ID: uuid.MustParse("0197a000-0000-7000-8000-00000000ad01"), Kind: "admin"}
)

// lease is the allowed run time of a claim (data-model section 7).
const lease = schedulertest.Lease

// after is start plus d.
func after(d time.Duration) time.Time { return start.Add(d) }

// recorder remembers which jobs a handler ran for, in order.
type recorder struct {
	mu   sync.Mutex
	keys []string
}

func (r *recorder) add(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys = append(r.keys, key)
}

// Keys returns the keys of the runs so far, oldest first.
func (r *recorder) Keys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.keys...)
}

// Count is the number of runs so far.
func (r *recorder) Count() int { return len(r.Keys()) }

// CountKey is the number of runs for one key.
func (r *recorder) CountKey(key string) int {
	n := 0
	for _, k := range r.Keys() {
		if k == key {
			n++
		}
	}
	return n
}

// recordKind registers kind with a handler that records the key and writes one effect row for it
// through the job's transaction, then succeeds.
func recordKind(h *schedulertest.Harness, kind string) *recorder {
	r := &recorder{}
	h.Handle(kind, func(ctx context.Context, job scheduler.Job) error {
		r.add(job.Key)
		return h.Effect(ctx, job.Key)
	})
	return r
}

// failingKind registers kind with a handler that records the key and always returns err.
func failingKind(h *schedulertest.Harness, kind string, err error) *recorder {
	r := &recorder{}
	h.Handle(kind, func(_ context.Context, job scheduler.Job) error {
		r.add(job.Key)
		return err
	})
	return r
}

// outboxMessage is an admin-broadcast message of the harness topic for the harness org.
func outboxMessage(h *schedulertest.Harness, key string) outbox.Message {
	return outbox.Message{
		OrgID: h.Org, Topic: schedulertest.Topic, Recipient: outbox.Recipient{Kind: "admin"}, Subject: "sample:" + key,
		DedupeKey: key, Payload: map[string]string{"k": key},
	}
}

// payloadOf decodes a job payload into a map.
func payloadOf(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("payload %q is not a JSON object: %v", raw, err)
	}
	return m
}

func wantStatus(t *testing.T, row schedulertest.JobRow, status string) {
	t.Helper()
	if row.Status != status {
		t.Fatalf("job %s/%s status = %s, want %s (attempts=%d last_error=%v)", row.Kind, row.Key, row.Status, status, row.Attempts, row.LastError)
	}
}

func wantTime(t *testing.T, name string, got *time.Time, want time.Time) {
	t.Helper()
	if got == nil || !got.Equal(want) {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

// inTx runs fn in a committed transaction and fails the test on error.
func inTx(t *testing.T, h *schedulertest.Harness, fn func(ctx context.Context) error) {
	t.Helper()
	if err := h.InTx(fn); err != nil {
		t.Fatalf("transaction failed: %v", err)
	}
}
