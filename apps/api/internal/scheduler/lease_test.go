package scheduler_test

// T044 - leases, crashes and fencing (US1.5, 1.6; FR-011, FR-012). A "crash" is a claim made with
// the tick's own query (schedulertest.ClaimOnly, 60 s lease) that is never completed or failed.
//
// Assumption (data-model section 1, research "release the claim transaction before running
// handlers"): the claim commits before the handler runs, and the handler's transaction does not
// lock the job row until it completes it, so another tick can reclaim a row whose lease ran out
// while a slow handler is still busy. The fenced completion (lock_token) then decides who wins.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"pgregory.net/rapid"

	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler/schedulertest"
)

func TestLease_AClaimThatNeverFinishesIsSkippedUntilTheLeaseEndsThenRunsAgain(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("crash_after_claim", func(h *schedulertest.Harness) {
		rec := recordKind(h, "sample.step")
		h.Schedule(h.Spec("sample.step", "k", after(-time.Minute)))
		claimed, ok := h.ClaimOnly() // the instance crashes right after claiming (US1.5)
		if !ok {
			t.Fatal("nothing to claim")
		}
		row := h.Job("sample.step", "k")
		wantStatus(t, row, "running")
		wantTime(t, "locked_until", row.LockedUntil, start.Add(lease))

		for _, at := range []time.Duration{0, 30 * time.Second, lease - time.Nanosecond} {
			h.Clock.Set(after(at))
			if sum := h.Tick(); sum.Jobs.Run != 0 || rec.Count() != 0 {
				t.Fatalf("%v after the claim the job ran (summary %+v) although its lease still holds", at, sum.Jobs)
			}
			wantStatus(t, h.Job("sample.step", "k"), "running")
		}

		h.Clock.Set(after(lease)) // locked_until <= now: the lease is over
		sum := h.Tick()
		if sum.Jobs.Run != 1 || rec.Count() != 1 || h.Effects("k") != 1 {
			t.Fatalf("summary %+v runs %d effects %d after the lease ended, want exactly one run", sum.Jobs, rec.Count(), h.Effects("k"))
		}
		row = h.Job("sample.step", "k")
		wantStatus(t, row, "done")
		if row.ID != claimed.ID || row.Attempts != 2 || row.LockToken != nil {
			t.Fatalf("row id=%v attempts=%d token=%v, want the same row, 2 attempts (the crashed one counts) and no token", row.ID, row.Attempts, row.LockToken)
		}
		wantTime(t, "first_attempt_at", row.FirstAttemptAt, start)
		wantTime(t, "last_attempt_at", row.LastAttemptAt, after(lease))
	})
}

// US1.6: the crashed run already produced its effect through a separate committed transaction
// (a message recorded before the crash). The rerun records the same message again; dedupe keeps
// one, and it is delivered once.
func TestLease_AnEffectCommittedBeforeTheCrashIsNotDuplicatedByTheRerun(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("dedupe", func(h *schedulertest.Harness) {
		var inserted []bool
		h.Handle("sample.notify", func(ctx context.Context, job scheduler.Job) error {
			ok, err := h.Outbox.Add(ctx, outboxMessage(h, "notice:"+job.Key))
			inserted = append(inserted, ok)
			return err
		})
		h.Schedule(h.Spec("sample.notify", "k", after(-time.Minute)))
		if _, ok := h.ClaimOnly(); !ok {
			t.Fatal("nothing to claim")
		}
		h.AddMessage(schedulertest.Topic, "notice:k") // what the crashed instance managed to commit

		h.Clock.Set(after(lease + 5*time.Minute))
		sum := h.Tick()
		if sum.Jobs.Run != 1 || len(inserted) != 1 || inserted[0] {
			t.Fatalf("summary %+v, Add results %v; want one rerun whose Add reports a duplicate", sum.Jobs, inserted)
		}
		wantStatus(t, h.Job("sample.notify", "k"), "done")
		if n := h.Count("outbox_messages", "dedupe_key = 'notice:k'"); n != 1 {
			t.Fatalf("messages for the job = %d, want 1", n)
		}
		h.Tick()
		if d := h.Sender.Deliveries("notice:k"); d != 1 {
			t.Fatalf("deliveries = %d, want exactly 1", d)
		}
	})
}

// stealDuringRun registers kind with a handler that writes its effect and a message through the
// job's transaction, then lets the lease run out and has another instance reclaim the row before
// returning result. It returns the recorder and a pointer to the thief's claim.
func stealDuringRun(t *testing.T, h *schedulertest.Harness, kind string, result error) (*recorder, *uuid.UUID) {
	rec := &recorder{}
	thief := new(uuid.UUID)
	h.Handle(kind, func(ctx context.Context, job scheduler.Job) error {
		rec.add(job.Key)
		if rec.Count() == 1 {
			if err := h.Effect(ctx, job.Key); err != nil {
				return err
			}
			if _, err := h.Outbox.Add(ctx, outboxMessage(h, "msg:"+job.Key)); err != nil {
				return err
			}
			h.Clock.Advance(lease) // this run took too long ...
			stolen, ok := h.ClaimOnly()
			if !ok {
				t.Error("the expired lease could not be reclaimed: the handler's transaction must not lock the job row while it runs")
				return result
			}
			*thief = stolen.LockToken.UUID
			return result // ... and only now returns, with a stale lock_token
		}
		return h.Effect(ctx, job.Key)
	})
	return rec, thief
}

func TestLease_ALostLeaseRollsBackTheHandlersEffects(t *testing.T) {
	h := schedulertest.NewHarness(t)

	h.Run("successful_handler_with_a_stale_token_commits_nothing", func(h *schedulertest.Harness) {
		rec, thief := stealDuringRun(t, h, "sample.step", nil)
		h.Schedule(h.Spec("sample.step", "k", after(-time.Minute)))
		sum := h.Tick() // a lost lease is not a tick error (h.Tick fails the test on error)
		if sum.Jobs.Run != 0 {
			t.Errorf("summary %+v counts a run whose lease was lost", sum.Jobs)
		}
		if h.Effects("k") != 0 || h.Count("outbox_messages", "dedupe_key = 'msg:k'") != 0 {
			t.Fatalf("effects=%d messages=%d after the lease was lost, want both rolled back (US1.5)", h.Effects("k"), h.Count("outbox_messages", "dedupe_key = 'msg:k'"))
		}
		row := h.Job("sample.step", "k")
		wantStatus(t, row, "running") // still owned by the thief
		if row.LockToken == nil || *row.LockToken != *thief {
			t.Fatalf("lock_token = %v, want the thief's %v: the stale run must not touch the row", row.LockToken, *thief)
		}

		h.Clock.Advance(lease) // the thief crashes too; its lease ends
		h.Tick()
		if rec.Count() != 2 || h.Effects("k") != 1 {
			t.Fatalf("runs=%d effects=%d, want a second run that commits exactly one effect", rec.Count(), h.Effects("k"))
		}
		wantStatus(t, h.Job("sample.step", "k"), "done")
	})

	h.Run("failing_handler_with_a_stale_token_records_no_failure", func(h *schedulertest.Harness) {
		_, thief := stealDuringRun(t, h, "sample.step", errBoom)
		h.Schedule(h.Spec("sample.step", "k", after(-time.Minute)))
		sum := h.Tick()
		row := h.Job("sample.step", "k")
		wantStatus(t, row, "running")
		if row.LockToken == nil || *row.LockToken != *thief || row.LastError != nil || row.Attempts != 2 {
			t.Fatalf("token=%v last_error=%v attempts=%d; want the thief's token, no error and 2 claims: a stale run cannot fail the job",
				row.LockToken, row.LastError, row.Attempts)
		}
		if sum.Jobs.Failed != 0 || sum.Jobs.Dead != 0 {
			t.Errorf("summary %+v counts the stale run's failure", sum.Jobs)
		}
	})
}

// A job whose lease keeps running out (the process dies every time) must not loop forever:
// a claim beyond max_attempts makes it dead without running the handler (data-model section 1).
func TestLease_RepeatedLeaseExpiryEndsDead(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("lease_expired_too_often", func(h *schedulertest.Harness) {
		rec := &recorder{}
		h.HandleWith("sample.crashy", func(_ context.Context, job scheduler.Job) error { rec.add(job.Key); return nil }, scheduler.KindOptions{MaxAttempts: 2})
		h.Schedule(h.Spec("sample.crashy", "k", after(-time.Minute)))
		for range 2 {
			if _, ok := h.ClaimOnly(); !ok {
				t.Fatal("nothing to claim")
			}
			h.Clock.Advance(lease)
		}
		sum := h.Tick()
		row := h.Job("sample.crashy", "k")
		wantStatus(t, row, "dead")
		if rec.Count() != 0 || sum.Jobs.Dead != 1 {
			t.Fatalf("handler runs=%d summary %+v, want no run and one dead job", rec.Count(), sum.Jobs)
		}
		if row.LastError == nil || !strings.Contains(strings.ToLower(*row.LastError), "lease") || row.FinishedAt == nil || row.LockToken != nil {
			t.Fatalf("last_error=%v finished_at=%v token=%v, want a 'lease expired' error, a finish time and no token", row.LastError, row.FinishedAt, row.LockToken)
		}
	})
}

// Property (FR-010, FR-011, FR-012, BR-IPO-03): whatever mix of ticks, clock moves and crashed
// claims happens, a job never runs before its due time or while another claim's lease holds, every
// state the rows pass through is valid, and once ticks resume every job's effect is committed
// exactly once.
func TestLease_BR_IPO_03_ClaimAndLeaseProperties(t *testing.T) {
	h := schedulertest.NewHarness(t)
	rapid.Check(t, func(rt *rapid.T) {
		h.Reset()
		type run struct {
			key string
			at  time.Time
		}
		var runs []run
		h.HandleWith("sample.prop", func(ctx context.Context, job scheduler.Job) error {
			runs = append(runs, run{job.Key, h.Clock.Now()})
			return h.Effect(ctx, job.Key)
		}, scheduler.KindOptions{MaxAttempts: 50})

		n := rapid.IntRange(1, 4).Draw(rt, "jobs")
		due := map[string]time.Time{}
		leaseEnd := map[string]time.Time{} // end of the latest crashed claim per key
		for i := range n {
			key := "p" + string(rune('a'+i))
			due[key] = after(time.Duration(rapid.IntRange(-10, 30).Draw(rt, "dueMinutes")) * time.Minute)
			h.Schedule(h.Spec("sample.prop", key, due[key]))
		}

		check := func() {
			for _, r := range runs {
				if r.at.Before(due[r.key]) {
					rt.Fatalf("%s ran at %v before its due time %v", r.key, r.at, due[r.key])
				}
				if end, ok := leaseEnd[r.key]; ok && r.at.Before(end) && r.at.After(end.Add(-lease)) {
					rt.Fatalf("%s ran at %v while a crashed claim's lease held until %v", r.key, r.at, end)
				}
			}
			if bad := h.Count("scheduled_jobs", "status = 'running' AND (locked_until IS NULL OR lock_token IS NULL)"); bad != 0 {
				rt.Fatalf("%d running rows without lease or token", bad)
			}
			if bad := h.Count("(SELECT dedupe_key FROM scheduled_jobs WHERE status IN ('pending','running') GROUP BY org_id, kind, dedupe_key HAVING count(*) > 1) d", "true"); bad != 0 {
				rt.Fatalf("%d keys with more than one active row", bad)
			}
			for key := range due {
				if e := h.Effects(key); e > 1 {
					rt.Fatalf("%s effect committed %d times", key, e)
				}
			}
		}

		steps := rapid.IntRange(1, 12).Draw(rt, "steps")
		for range steps {
			switch rapid.SampledFrom([]string{"advance", "tick", "crash"}).Draw(rt, "action") {
			case "advance":
				h.Clock.Advance(time.Duration(rapid.IntRange(0, 300).Draw(rt, "seconds")) * time.Second)
			case "tick":
				// Runs that start in this tick happen at the current instant; a job claimed by a
				// crash must not be among them while its lease holds.
				before := len(runs)
				h.Tick()
				for _, r := range runs[before:] {
					if !r.at.Equal(h.Clock.Now()) {
						rt.Fatalf("run recorded at %v during a tick at %v", r.at, h.Clock.Now())
					}
				}
			case "crash":
				if job, ok := h.ClaimOnly(); ok {
					if h.Clock.Now().Before(due[job.DedupeKey]) {
						rt.Fatalf("claim returned %s before its due time", job.DedupeKey)
					}
					leaseEnd[job.DedupeKey] = h.Clock.Now().Add(lease)
				}
			}
			check()
		}

		h.Clock.Advance(time.Hour) // ticks resume: every lease is over, every job is due
		h.Tick()
		check()
		for key := range due {
			if e := h.Effects(key); e != 1 {
				rt.Fatalf("%s effect committed %d times after recovery, want exactly 1", key, e)
			}
			if r := h.Job("sample.prop", key); r.Status != "done" {
				rt.Fatalf("%s status %s after recovery, want done", key, r.Status)
			}
		}
	})
}
