package scheduler_test

// Stale running job recovery (conductor decision, BR-IPO-03, FR-010, FR-014). At the start of every
// tick, before any claim, a row with status 'running' and locked_until <= now is recovered, by
// precedence: cancel_requested -> cancelled; expires_at <= now -> expired; rerun -> pending with
// rerun=false, attempts=0, first_attempt_at=NULL and run_at unchanged. Lock fields are cleared.
// A crash is a claim that is never finished (schedulertest.ClaimOnly, 60 s lease).

import (
	"context"
	"testing"
	"time"

	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler/schedulertest"
)

func wantNoLock(t *testing.T, row schedulertest.JobRow) {
	t.Helper()
	if row.LockedUntil != nil || row.LockToken != nil {
		t.Fatalf("locked_until=%v lock_token=%v, want both cleared after recovery", row.LockedUntil, row.LockToken)
	}
}

func TestRecovery_BR_IPO_03_FR_010_CancelRequestedCrashedRunBecomesCancelled(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("cancel", func(h *schedulertest.Harness) {
		t := h.T
		rec := recordKind(h, "sample.step")
		h.Schedule(h.Spec("sample.step", "k", after(-time.Minute)))
		if _, ok := h.ClaimOnly(); !ok {
			t.Fatal("nothing to claim")
		}
		inTx(t, h, func(ctx context.Context) error {
			n, err := h.Sched.Cancel(ctx, h.Org, "sample.step", "k")
			if err != nil || n != 1 {
				t.Fatalf("Cancel = %d, %v", n, err)
			}
			return nil
		})
		h.Clock.Set(after(lease))
		sum := h.Tick()
		row := h.Job("sample.step", "k")
		wantStatus(t, row, "cancelled")
		if rec.Count() != 0 || sum.Jobs.Run != 0 || row.CancelRequested {
			t.Fatalf("runs=%d summary %+v cancel_requested=%v, want no run and cancel_requested cleared", rec.Count(), sum.Jobs, row.CancelRequested)
		}
		if row.FinishedAt == nil {
			t.Fatal("finished_at is nil on a cancelled row")
		}
		wantNoLock(t, row)
	})
}

func TestRecovery_BR_IPO_03_FR_014_ExpiredCrashedRunBecomesExpired(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("expire", func(h *schedulertest.Harness) {
		t := h.T
		rec := recordKind(h, "sample.step")
		spec := h.Spec("sample.step", "k", after(-time.Minute))
		exp := after(30 * time.Second)
		spec.ExpiresAt = &exp
		h.Schedule(spec)
		if _, ok := h.ClaimOnly(); !ok {
			t.Fatal("nothing to claim")
		}
		h.Clock.Set(after(lease))
		h.Tick()
		row := h.Job("sample.step", "k")
		wantStatus(t, row, "expired")
		if rec.Count() != 0 || row.FinishedAt == nil {
			t.Fatalf("runs=%d finished_at=%v, want no run and finished_at set", rec.Count(), row.FinishedAt)
		}
		wantNoLock(t, row)
	})
}

func TestRecovery_BR_IPO_03_FR_010_ExpiryBeatsRerun(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("precedence", func(h *schedulertest.Harness) {
		t := h.T
		recordKind(h, "sample.step")
		h.Schedule(h.Spec("sample.step", "a", after(-time.Minute)))
		exp := after(30 * time.Second)
		spec := h.Spec("sample.step", "b", after(-time.Minute))
		spec.ExpiresAt = &exp
		h.Schedule(spec)
		h.ClaimOnly()
		h.ClaimOnly()
		inTx(t, h, func(ctx context.Context) error {
			// b: rerun requested AND expired -> expired beats rerun.
			if moved, err := h.Sched.Reschedule(ctx, h.Org, "sample.step", "b", after(10*time.Minute)); err != nil || !moved {
				t.Fatalf("Reschedule b = %v, %v", moved, err)
			}
			return nil
		})
		h.Clock.Set(after(lease))
		h.Tick()
		wantStatus(t, h.Job("sample.step", "b"), "expired")
	})
}

func TestRecovery_BR_IPO_03_FR_010_RescheduledCrashedRunGoesBackToPendingAndRunsOnceAtNewRunAt(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("rerun", func(h *schedulertest.Harness) {
		t := h.T
		rec := &recorder{}
		// One allowed attempt: the crashed attempt was the last one.
		h.HandleWith("sample.step", func(ctx context.Context, job scheduler.Job) error {
			rec.add(job.Key)
			return h.Effect(ctx, job.Key)
		}, scheduler.KindOptions{MaxAttempts: 1})
		h.Schedule(h.Spec("sample.step", "k", after(-time.Minute)))
		if _, ok := h.ClaimOnly(); !ok {
			t.Fatal("nothing to claim")
		}
		newRunAt := after(3 * 24 * time.Hour)
		inTx(t, h, func(ctx context.Context) error {
			if moved, err := h.Sched.Reschedule(ctx, h.Org, "sample.step", "k", newRunAt); err != nil || !moved {
				t.Fatalf("Reschedule = %v, %v", moved, err)
			}
			return nil
		})
		if row := h.Job("sample.step", "k"); !row.Rerun {
			t.Fatal("Reschedule on a running row did not set rerun")
		}

		h.Clock.Set(after(lease))
		if sum := h.Tick(); sum.Jobs.Run != 0 || rec.Count() != 0 {
			t.Fatalf("summary %+v runs %d, want no run before the new run_at", sum.Jobs, rec.Count())
		}
		row := h.Job("sample.step", "k")
		wantStatus(t, row, "pending") // not 'running' for days, not dead
		if row.Rerun || row.Attempts != 0 || row.FirstAttemptAt != nil || !row.RunAt.Equal(newRunAt) {
			t.Fatalf("row rerun=%v attempts=%d first_attempt_at=%v run_at=%v, want false, 0, nil, %v", row.Rerun, row.Attempts, row.FirstAttemptAt, row.RunAt, newRunAt)
		}
		wantNoLock(t, row)

		h.Clock.Set(newRunAt.Add(-time.Second))
		h.Tick()
		if rec.Count() != 0 {
			t.Fatalf("ran %d times before the new run_at", rec.Count())
		}
		h.Clock.Set(newRunAt)
		h.Tick()
		h.Clock.Set(newRunAt.Add(time.Hour))
		h.Tick()
		if rec.Count() != 1 || h.Effects("k") != 1 {
			t.Fatalf("runs %d effects %d, want exactly one", rec.Count(), h.Effects("k"))
		}
		wantStatus(t, h.Job("sample.step", "k"), "done")
	})
}
