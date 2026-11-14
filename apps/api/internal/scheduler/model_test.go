package scheduler_test

// T064 - the scheduler against an in-memory model (FR-009 to FR-016; BR-IPO-03).
//
// Random sequences of schedule, reschedule, cancel, clock advance, tick, crash-after-claim and
// "fail the next run" are applied to the real scheduler (PostgreSQL) and to a small model that
// restates the rules of the spec. After every step the two must agree, and these hold:
//   - no effect takes place twice, and every successful run takes place once;
//   - a handler never runs before the job's run_at;
//   - at most one active (pending or running) row per key;
//   - a running row has a lease and a token, any other row has neither;
//   - a dead row has used all its attempts, a pending row has not;
//   - after a tick no due, unexpired job is left unclaimed.

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler/schedulertest"
)

const (
	modelKind        = "sample.model"
	modelMaxAttempts = 3
	// backoff after a failed attempt is 1 m .. 3 h with +/-20 % jitter, then 6 h (platform/retry).
	modelMinBackoff = 48 * time.Second
	modelMaxBackoff = 6 * time.Hour
)

// mjob is the model of the active row of one key.
type mjob struct {
	running   bool
	runAt     time.Time
	expiresAt *time.Time
	attempts  int
	leaseEnd  time.Time
	rerun     bool // changed while running: run again at the new time after this run
	cancelReq bool // cancelled while running
	retry     bool // runAt is a placeholder for a backoff the model cannot compute; read it back
}

func (j *mjob) dueAt(now time.Time) bool {
	if j.running {
		return !j.leaseEnd.After(now) // the lease ran out: another claim may take over
	}
	return !j.runAt.After(now) && (j.expiresAt == nil || j.expiresAt.After(now))
}

type model struct {
	active   map[string]*mjob
	effects  map[string]int
	failures map[string]int // injected failures still to be consumed by the handler
	calls    []call         // what the real handler saw, in order; the model replays it
}

// call is one invocation of the real handler and whether it failed.
type call struct {
	key    string
	failed bool
}

// next is the key a claim takes now: the due job with the oldest run_at. ambiguous reports a tie.
func (m *model) next(now time.Time) (key string, ambiguous bool) {
	var best *mjob
	for k, j := range m.active {
		if !j.dueAt(now) {
			continue
		}
		switch {
		case best == nil || j.runAt.Before(best.runAt):
			best, key, ambiguous = j, k, false
		case j.runAt.Equal(best.runAt):
			ambiguous = true
		}
	}
	return key, ambiguous
}

func (m *model) claim(key string, now time.Time) *mjob {
	j := m.active[key]
	j.attempts++
	j.running = true
	j.leaseEnd = now.Add(lease)
	return j
}

// settle applies the outcome of a run or failure of a claimed job (data-model section 1).
func (m *model) settle(key string, now time.Time, failed bool) {
	j := m.active[key]
	j.running, j.leaseEnd = false, time.Time{}
	switch {
	case !failed:
		m.effects[key]++
		if j.rerun {
			j.attempts, j.rerun, j.cancelReq = 0, false, false
			return
		}
		delete(m.active, key) // done
	case j.cancelReq:
		delete(m.active, key) // cancelled
	case j.rerun:
		j.attempts, j.rerun = 0, false
	case j.attempts >= modelMaxAttempts:
		delete(m.active, key) // dead
	default:
		j.runAt, j.retry = now.Add(modelMinBackoff), true
	}
}

// tick mirrors Tick: expire, then claim and run every due job oldest first. The model must claim
// the same keys in the same order as the real handler was called; a failure is whatever the
// handler returned.
func (m *model) tick(rt *rapid.T, now time.Time) {
	for k, j := range m.active {
		if !j.running && j.expiresAt != nil && !j.expiresAt.After(now) {
			delete(m.active, k) // expired
		}
	}
	for {
		key, _ := m.next(now)
		if key == "" {
			return
		}
		j := m.claim(key, now)
		switch {
		case j.attempts > modelMaxAttempts: // the lease ran out too often: dead without a run
			delete(m.active, key)
		default:
			if len(m.calls) == 0 || m.calls[0].key != key {
				rt.Fatalf("model runs %s next, the scheduler's handler calls were %v (claim order oldest run_at first)", key, m.calls)
			}
			c := m.calls[0]
			m.calls = m.calls[1:]
			m.settle(key, now, c.failed)
		}
	}
}

func TestScheduler_BR_IPO_03_ModelProperties(t *testing.T) {
	h := schedulertest.NewHarness(t)
	keys := []string{"a", "b", "c"}

	rapid.Check(t, func(rt *rapid.T) {
		h.Reset()
		m := &model{active: map[string]*mjob{}, effects: map[string]int{}, failures: map[string]int{}}
		h.HandleWith(modelKind, func(ctx context.Context, job scheduler.Job) error {
			if h.Clock.Now().Before(job.RunAt) {
				rt.Fatalf("%s ran at %v before its run_at %v (FR-010)", job.Key, h.Clock.Now(), job.RunAt)
			}
			if m.failures[job.Key] > 0 {
				m.failures[job.Key]--
				m.calls = append(m.calls, call{job.Key, true})
				return errBoom
			}
			m.calls = append(m.calls, call{job.Key, false})
			return h.Effect(ctx, job.Key)
		}, scheduler.KindOptions{MaxAttempts: modelMaxAttempts})

		// runAt gives every key its own second, so the claim order is never a tie between keys.
		runAtOf := func(rt *rapid.T, key string) time.Time {
			off := time.Duration(rapid.IntRange(-10, 30).Draw(rt, "offsetMinutes")) * time.Minute
			return h.Clock.Now().Add(off + time.Duration(slices.Index(keys, key))*time.Second)
		}

		checkAgainstModel := func() {
			effects := map[string]int{}
			rows, err := h.Pool.Query(t.Context(), `SELECT key, count(*) FROM sample_effects GROUP BY key`)
			if err != nil {
				rt.Fatalf("effects: %v", err)
			}
			for rows.Next() {
				var k string
				var n int
				if err := rows.Scan(&k, &n); err != nil {
					rt.Fatalf("scan: %v", err)
				}
				effects[k] = n
			}
			rows.Close()

			for _, key := range keys {
				if effects[key] != m.effects[key] {
					rt.Fatalf("%s: %d effects, model has %d (no effect twice, none lost)", key, effects[key], m.effects[key])
				}
				var active []schedulertest.JobRow
				for _, r := range h.Jobs(modelKind, key) {
					switch r.Status {
					case "pending", "running":
						active = append(active, r)
						if r.Status == "pending" && r.Attempts >= r.MaxAttempts {
							rt.Fatalf("%s: pending with %d of %d attempts used; it should be dead", key, r.Attempts, r.MaxAttempts)
						}
					case "dead":
						if r.Attempts < r.MaxAttempts {
							rt.Fatalf("%s: dead after %d of %d attempts", key, r.Attempts, r.MaxAttempts)
						}
					case "done", "cancelled", "expired":
					default:
						rt.Fatalf("%s: unknown status %q", key, r.Status)
					}
					if (r.Status == "running") != (r.LockedUntil != nil && r.LockToken != nil) || (r.Status != "running") != (r.LockedUntil == nil && r.LockToken == nil) {
						rt.Fatalf("%s: status %s with locked_until=%v token=%v; a running row has both, any other has neither", key, r.Status, r.LockedUntil, r.LockToken)
					}
				}
				if len(active) > 1 {
					rt.Fatalf("%s: %d active rows", key, len(active))
				}
				want, ok := m.active[key]
				switch {
				case !ok && len(active) == 0:
					continue
				case ok != (len(active) == 1):
					rt.Fatalf("%s: model active=%v, database active rows=%d", key, ok, len(active))
				}
				r := active[0]
				if want.retry { // adopt the backoff the scheduler chose after checking its bounds
					wait := r.RunAt.Sub(h.Clock.Now())
					if wait < modelMinBackoff || wait > modelMaxBackoff {
						rt.Fatalf("%s: retry %v after the failure, want between %v and %v", key, wait, modelMinBackoff, modelMaxBackoff)
					}
					want.runAt, want.retry = r.RunAt, false
				}
				if (r.Status == "running") != want.running || !r.RunAt.Equal(want.runAt) || r.Attempts != want.attempts ||
					r.Rerun != want.rerun || r.CancelRequested != want.cancelReq || (want.expiresAt == nil) != (r.ExpiresAt == nil) ||
					(want.expiresAt != nil && !r.ExpiresAt.Equal(*want.expiresAt)) {
					rt.Fatalf("%s: database row {status=%s run_at=%v attempts=%d rerun=%v cancel=%v expires=%v}, model %+v", key,
						r.Status, r.RunAt, r.Attempts, r.Rerun, r.CancelRequested, r.ExpiresAt, *want)
				}
			}
		}

		tick := func() {
			now := h.Clock.Now()
			h.Tick()
			m.tick(rt, now)
			if len(m.calls) != 0 {
				rt.Fatalf("the handler ran for %v, the model did not", m.calls)
			}
			// FR-006: nothing due and unexpired is left behind by a tick.
			if n := h.Count("scheduled_jobs", `(status = 'pending' AND run_at <= $1 AND (expires_at IS NULL OR expires_at > $1)) OR (status = 'running' AND locked_until <= $1)`, now); n != 0 {
				rt.Fatalf("%d due jobs left after a tick", n)
			}
		}

		steps := rapid.IntRange(1, 22).Draw(rt, "steps")
		for range steps {
			key := rapid.SampledFrom(keys).Draw(rt, "key")
			switch rapid.SampledFrom([]string{"schedule", "reschedule", "cancel", "advance", "tick", "crash", "fail"}).Draw(rt, "action") {
			case "schedule":
				runAt := runAtOf(rt, key)
				var expires *time.Time
				if rapid.Bool().Draw(rt, "expires") {
					e := runAt.Add(time.Duration(rapid.IntRange(1, 90).Draw(rt, "expiryMinutes")) * time.Minute)
					expires = &e
				}
				spec := h.Spec(modelKind, key, runAt)
				spec.ExpiresAt = expires
				h.Schedule(spec)
				if j, ok := m.active[key]; ok {
					j.runAt, j.expiresAt, j.rerun, j.cancelReq = runAt, expires, j.running, false
				} else {
					m.active[key] = &mjob{runAt: runAt, expiresAt: expires}
				}
			case "reschedule":
				runAt := runAtOf(rt, key)
				var moved bool
				inTx(t, h, func(ctx context.Context) (err error) {
					moved, err = h.Sched.Reschedule(ctx, h.Org, modelKind, key, runAt)
					return err
				})
				j, ok := m.active[key]
				if moved != ok {
					rt.Fatalf("Reschedule(%s) = %v, model has an active job: %v", key, moved, ok)
				}
				if ok {
					j.runAt, j.rerun, j.cancelReq = runAt, j.running, false
				}
			case "cancel":
				var n int
				inTx(t, h, func(ctx context.Context) (err error) {
					n, err = h.Sched.Cancel(ctx, h.Org, modelKind, key)
					return err
				})
				j, ok := m.active[key]
				if n != b2i(ok) {
					rt.Fatalf("Cancel(%s) affected %d rows, model has an active job: %v", key, n, ok)
				}
				switch {
				case ok && j.running:
					j.cancelReq, j.rerun = true, false
				case ok:
					delete(m.active, key)
				}
			case "advance":
				h.Clock.Advance(time.Duration(rapid.IntRange(0, 7200).Draw(rt, "seconds")) * time.Second)
			case "tick":
				tick()
			case "crash":
				now := h.Clock.Now()
				want, ambiguous := m.next(now)
				if ambiguous {
					break // two jobs share a run_at: the claim order is the database's choice
				}
				job, claimed := h.ClaimOnly()
				if claimed != (want != "") || (claimed && job.DedupeKey != want) {
					rt.Fatalf("claim took %q (claimed=%v), model expected %q", job.DedupeKey, claimed, want)
				}
				if claimed {
					m.claim(want, now)
				}
			case "fail":
				m.failures[key] = rapid.IntRange(1, 4).Draw(rt, "failures")
			}
			checkAgainstModel()
		}

		// Ticks resume for good: every lease and backoff ends, and every job reaches a final state.
		for range 8 {
			h.Clock.Advance(7 * time.Hour)
			tick()
			checkAgainstModel()
		}
		if len(m.active) != 0 {
			rt.Fatalf("model still has active jobs after recovery: %v", fmt.Sprint(len(m.active)))
		}
		if n := h.Count("scheduled_jobs", "status IN ('pending','running')"); n != 0 {
			rt.Fatalf("%d active rows after recovery", n)
		}
	})
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
