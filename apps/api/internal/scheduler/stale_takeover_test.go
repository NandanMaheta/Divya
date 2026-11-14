package scheduler_test

// Stale takeover must not claim rows that need recovery (BR-IPO-03, FR-014). A lease that ends
// after the tick's recovery but during its claim loop is seen by ClaimJob at a later "now" with no
// recovery in between (modelled by ClaimOnly at a time past the lease end). ClaimJob may take over
// only stale running rows that are not cancel_requested, not rerun and not expired; those wait for
// the next tick's recovery.

import (
	"context"
	"testing"
	"time"

	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler/schedulertest"
)

func TestStaleTakeover_BR_IPO_03_FR_014_CancelRequestedIsNotClaimed(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("cancel", func(h *schedulertest.Harness) {
		t := h.T
		rec := recordKind(h, "sample.step")
		h.Schedule(h.Spec("sample.step", "k", after(-time.Minute)))
		if _, ok := h.ClaimOnly(); !ok {
			t.Fatal("nothing to claim")
		}
		inTx(t, h, func(ctx context.Context) error {
			if n, err := h.Sched.Cancel(ctx, h.Org, "sample.step", "k"); err != nil || n != 1 {
				t.Fatalf("Cancel = %d, %v", n, err)
			}
			return nil
		})
		h.Clock.Set(after(lease))
		if job, ok := h.ClaimOnly(); ok {
			t.Fatalf("ClaimJob took over a cancel_requested row (status %s, attempts %d), want no claim", job.Status, job.Attempts)
		}
		h.Tick()
		wantStatus(t, h.Job("sample.step", "k"), "cancelled")
		if rec.Count() != 0 {
			t.Fatalf("runs = %d, want 0 for a cancelled job", rec.Count())
		}
	})
}

func TestStaleTakeover_BR_IPO_03_FR_014_RerunOnLastAttemptIsNotClaimed(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("rerun", func(h *schedulertest.Harness) {
		t := h.T
		rec := &recorder{}
		h.HandleWith("sample.step", func(ctx context.Context, job scheduler.Job) error {
			rec.add(job.Key)
			return h.Effect(ctx, job.Key)
		}, scheduler.KindOptions{MaxAttempts: 1})
		h.Schedule(h.Spec("sample.step", "k", after(-time.Minute)))
		if _, ok := h.ClaimOnly(); !ok {
			t.Fatal("nothing to claim")
		}
		newRunAt := after(lease) // due at the claim time, so only the rerun flag can exclude it
		inTx(t, h, func(ctx context.Context) error {
			if moved, err := h.Sched.Reschedule(ctx, h.Org, "sample.step", "k", newRunAt); err != nil || !moved {
				t.Fatalf("Reschedule = %v, %v", moved, err)
			}
			return nil
		})
		h.Clock.Set(after(lease))
		if job, ok := h.ClaimOnly(); ok {
			t.Fatalf("ClaimJob took over a rerun row (attempts %d), want no claim", job.Attempts)
		}
		h.Tick()
		row := h.Job("sample.step", "k")
		if row.Status == "dead" {
			t.Fatalf("rescheduled job on its last attempt was marked dead, want it to run again")
		}
		wantStatus(t, row, "done")
		if rec.CountKey("k") != 1 {
			t.Fatalf("runs = %d, want exactly 1 at the rescheduled run_at", rec.CountKey("k"))
		}
	})
}

func TestStaleTakeover_BR_IPO_03_FR_014_ExpiredIsNotClaimed(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("expired", func(h *schedulertest.Harness) {
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
		if job, ok := h.ClaimOnly(); ok {
			t.Fatalf("ClaimJob took over an expired row (attempts %d), want no claim", job.Attempts)
		}
		h.Tick()
		wantStatus(t, h.Job("sample.step", "k"), "expired")
		if rec.Count() != 0 {
			t.Fatalf("runs = %d, want 0 for an expired job", rec.Count())
		}
	})
}
