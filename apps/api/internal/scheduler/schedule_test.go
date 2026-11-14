package scheduler_test

// T041 - the schedule API owning features call inside their own transaction: Schedule (upsert),
// Reschedule, Cancel, CancelSubject (US1.9, 1.10, 5.1-5.7; FR-008, FR-009, FR-013; BR-IPO-03).
// Shared fixtures: helpers_test.go and schedulertest/harness.go.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler/schedulertest"
)

func TestSchedule_BR_IPO_03_ScheduleUpsertsOnePendingJobPerKey(t *testing.T) {
	h := schedulertest.NewHarness(t)

	h.Run("latest_due_time_details_and_expiry_win", func(h *schedulertest.Harness) {
		t := h.T
		recordKind(h, "sample.open")
		exp1, exp2 := after(2*time.Hour), after(5*time.Hour)
		first := h.Spec("sample.open", "ipo:42:open", after(time.Hour))
		first.ExpiresAt, first.Payload = &exp1, map[string]int{"v": 1}
		h.Schedule(first)
		second := h.Spec("sample.open", "ipo:42:open", after(3*time.Hour))
		second.Subject, second.ExpiresAt, second.Payload = "ipo:42", &exp2, map[string]int{"v": 2}
		h.Schedule(second)

		row := h.Job("sample.open", "ipo:42:open") // exactly one row (US1.9, FR-009)
		wantStatus(t, row, "pending")
		if !row.RunAt.Equal(after(3 * time.Hour)) {
			t.Errorf("run_at = %v, want the latest %v", row.RunAt, after(3*time.Hour))
		}
		wantTime(t, "expires_at", row.ExpiresAt, exp2)
		if row.Subject != "ipo:42" || payloadOf(t, row.Payload)["v"] != float64(2) {
			t.Errorf("subject=%q payload=%s, want the latest values", row.Subject, row.Payload)
		}
		if row.Org != h.Org || row.Attempts != 0 || row.MaxAttempts != 8 || row.Recurring {
			t.Errorf("row = %+v, want org %v, 0 attempts, default max 8, not recurring", row, h.Org)
		}
	})

	h.Run("a_kind_can_set_its_own_max_attempts", func(h *schedulertest.Harness) {
		t := h.T
		h.HandleWith("sample.fragile", func(context.Context, scheduler.Job) error { return nil }, scheduler.KindOptions{MaxAttempts: 3})
		h.Schedule(h.Spec("sample.fragile", "k", after(time.Hour)))
		if got := h.Job("sample.fragile", "k").MaxAttempts; got != 3 {
			t.Errorf("max_attempts = %d, want 3 from KindOptions", got)
		}
	})

	h.Run("the_same_key_in_two_orgs_is_two_jobs", func(h *schedulertest.Harness) {
		t := h.T
		recordKind(h, "sample.open")
		other := h.NewOrg("Other")
		h.Schedule(h.Spec("sample.open", "k", after(time.Hour)))
		spec := h.Spec("sample.open", "k", after(2*time.Hour))
		spec.OrgID = other
		h.Schedule(spec)
		if h.Job("sample.open", "k").Org != h.Org || h.JobOf(other, "sample.open", "k").Org != other {
			t.Fatal("each org must keep its own job for the same key")
		}
	})

	h.Run("a_done_key_creates_a_new_pending_row_and_keeps_the_old_one_as_history", func(h *schedulertest.Harness) {
		t := h.T
		rec := recordKind(h, "sample.open")
		h.Schedule(h.Spec("sample.open", "ipo:42:open", after(5*time.Minute)))
		h.Clock.Set(after(10 * time.Minute))
		h.Tick()
		if rec.Count() != 1 {
			t.Fatalf("job ran %d times, want 1", rec.Count())
		}
		wantStatus(t, h.Job("sample.open", "ipo:42:open"), "done")

		h.Schedule(h.Spec("sample.open", "ipo:42:open", after(24*time.Hour))) // US5.6: the IPO date changed again
		rows := h.Jobs("sample.open", "ipo:42:open")
		if len(rows) != 2 {
			t.Fatalf("rows = %d, want the done row plus a new pending row", len(rows))
		}
		wantStatus(t, rows[0], "done")
		wantStatus(t, rows[1], "pending")
		if rows[0].FinishedAt == nil || !rows[1].RunAt.Equal(after(24*time.Hour)) {
			t.Errorf("history row finished_at=%v, new run_at=%v", rows[0].FinishedAt, rows[1].RunAt)
		}
	})
}

func TestSchedule_BR_IPO_03_ScheduleJoinsTheCallersTransaction(t *testing.T) {
	h := schedulertest.NewHarness(t)

	h.Run("rollback_leaves_no_job_and_commit_leaves_one", func(h *schedulertest.Harness) {
		t := h.T
		recordKind(h, "sample.open")
		err := h.InTx(func(ctx context.Context) error {
			if err := h.Sched.Schedule(ctx, h.Spec("sample.open", "rolled-back", after(time.Hour))); err != nil {
				return err
			}
			return errBoom
		})
		if !errors.Is(err, errBoom) {
			t.Fatalf("InTx error = %v, want the callback's error", err)
		}
		if n := h.Count("scheduled_jobs", "true"); n != 0 { // US1.10
			t.Fatalf("%d jobs exist after the business change rolled back, want 0", n)
		}
		inTx(t, h, func(ctx context.Context) error {
			return h.Sched.Schedule(ctx, h.Spec("sample.open", "committed", after(time.Hour)))
		})
		if n := h.Count("scheduled_jobs", "dedupe_key = 'committed'"); n != 1 {
			t.Fatalf("committed job rows = %d, want 1", n)
		}
	})

	h.Run("without_a_transaction_it_returns_ErrNoTx_and_writes_nothing", func(h *schedulertest.Harness) {
		t := h.T
		recordKind(h, "sample.open")
		err := h.Sched.Schedule(t.Context(), h.Spec("sample.open", "k", after(time.Hour)))
		if !errors.Is(err, db.ErrNoTx) {
			t.Fatalf("Schedule without tx error = %v, want db.ErrNoTx", err)
		}
		if n := h.Count("scheduled_jobs", "true"); n != 0 {
			t.Fatalf("%d jobs written without a transaction", n)
		}
	})

	h.Run("an_unregistered_kind_is_ErrUnknownKind", func(h *schedulertest.Harness) {
		t := h.T
		err := h.InTx(func(ctx context.Context) error {
			return h.Sched.Schedule(ctx, h.Spec("nobody.handles_this", "k", after(time.Hour)))
		})
		if !errors.Is(err, scheduler.ErrUnknownKind) {
			t.Fatalf("error = %v, want ErrUnknownKind", err)
		}
		if n := h.Count("scheduled_jobs", "true"); n != 0 {
			t.Fatalf("%d jobs written for an unknown kind", n)
		}
	})

	h.Run("invalid_specs_are_ErrInvalid_before_any_SQL_so_the_transaction_stays_usable", func(h *schedulertest.Harness) {
		t := h.T
		recordKind(h, "sample.open")
		bad := map[string]func(*scheduler.JobSpec){
			"zero run_at":        func(s *scheduler.JobSpec) { s.RunAt = time.Time{} },
			"empty key":          func(s *scheduler.JobSpec) { s.Key = "" },
			"key over 200 chars": func(s *scheduler.JobSpec) { s.Key = strings.Repeat("k", 201) },
			"empty subject":      func(s *scheduler.JobSpec) { s.Subject = "" },
			"no org":             func(s *scheduler.JobSpec) { s.OrgID = [16]byte{} },
			"payload over 8 KB":  func(s *scheduler.JobSpec) { s.Payload = map[string]string{"blob": strings.Repeat("x", 9000)} },
			"payload not JSON":   func(s *scheduler.JobSpec) { s.Payload = make(chan int) },
		}
		inTx(t, h, func(ctx context.Context) error {
			for name, mutate := range bad {
				spec := h.Spec("sample.open", "bad", after(time.Hour))
				mutate(&spec)
				if err := h.Sched.Schedule(ctx, spec); !errors.Is(err, scheduler.ErrInvalid) {
					t.Errorf("%s: error = %v, want ErrInvalid", name, err)
				}
			}
			// A poisoned transaction (an SQL error was swallowed) would fail this call.
			return h.Sched.Schedule(ctx, h.Spec("sample.open", "good", after(time.Hour)))
		})
		if n := h.Count("scheduled_jobs", "true"); n != 1 {
			t.Fatalf("jobs = %d, want only the valid one", n)
		}
	})
}

func TestSchedule_BR_IPO_03_RescheduleMovesTheJobAndNeverCreatesOne(t *testing.T) {
	h := schedulertest.NewHarness(t)

	h.Run("commit_moves_the_due_time_so_the_old_time_no_longer_runs_it", func(h *schedulertest.Harness) {
		t := h.T
		rec := recordKind(h, "sample.open")
		h.Schedule(h.Spec("sample.open", "ipo:42:open", after(time.Hour)))
		var found bool
		inTx(t, h, func(ctx context.Context) error {
			var err error
			found, err = h.Sched.Reschedule(ctx, h.Org, "sample.open", "ipo:42:open", after(48*time.Hour))
			return err
		})
		if !found {
			t.Fatal("Reschedule reported no job for an existing key")
		}
		h.Clock.Set(after(time.Hour + 5*time.Minute)) // US5.1: the old due time has passed
		h.Tick()
		if rec.Count() != 0 {
			t.Fatalf("the job ran at its old due time after it was rescheduled")
		}
		h.Clock.Set(after(48 * time.Hour))
		h.Tick()
		if rec.Count() != 1 {
			t.Fatalf("job ran %d times at the new due time, want 1", rec.Count())
		}
	})

	h.Run("rollback_keeps_the_old_due_time", func(h *schedulertest.Harness) {
		t := h.T
		recordKind(h, "sample.open")
		h.Schedule(h.Spec("sample.open", "k", after(time.Hour)))
		_ = h.InTx(func(ctx context.Context) error {
			if _, err := h.Sched.Reschedule(ctx, h.Org, "sample.open", "k", after(48*time.Hour)); err != nil {
				return err
			}
			return errBoom
		})
		if got := h.Job("sample.open", "k").RunAt; !got.Equal(after(time.Hour)) { // US5.2
			t.Fatalf("run_at = %v after a rolled back change, want %v", got, after(time.Hour))
		}
	})

	h.Run("a_date_moved_into_the_past_runs_on_the_next_tick", func(h *schedulertest.Harness) {
		t := h.T
		rec := recordKind(h, "sample.open")
		h.Schedule(h.Spec("sample.open", "k", after(2*time.Hour)))
		inTx(t, h, func(ctx context.Context) error {
			_, err := h.Sched.Reschedule(ctx, h.Org, "sample.open", "k", after(-30*time.Minute))
			return err
		})
		if sum := h.Tick(); sum.Jobs.Run != 1 || rec.Count() != 1 { // US5.4
			t.Fatalf("summary %+v, runs %d; want the job to run on this tick", sum.Jobs, rec.Count())
		}
	})

	h.Run("a_missing_key_is_a_no_op_that_creates_nothing", func(h *schedulertest.Harness) {
		t := h.T
		recordKind(h, "sample.open")
		var found bool
		inTx(t, h, func(ctx context.Context) error {
			var err error
			found, err = h.Sched.Reschedule(ctx, h.Org, "sample.open", "no-such-key", after(time.Hour))
			return err
		})
		if found || h.Count("scheduled_jobs", "true") != 0 { // US5.7
			t.Fatalf("found=%v rows=%d, want false and no row", found, h.Count("scheduled_jobs", "true"))
		}
	})

	h.Run("a_done_job_is_not_rescheduled", func(h *schedulertest.Harness) {
		t := h.T
		recordKind(h, "sample.open")
		h.SeedJob(schedulertest.JobSeed{Kind: "sample.open", Key: "k", Status: "done"})
		var found bool
		inTx(t, h, func(ctx context.Context) error {
			var err error
			found, err = h.Sched.Reschedule(ctx, h.Org, "sample.open", "k", after(time.Hour))
			return err
		})
		if found {
			t.Fatal("Reschedule touched a finished job")
		}
		wantStatus(t, h.Job("sample.open", "k"), "done")
	})

	h.Run("without_a_transaction_it_returns_ErrNoTx", func(h *schedulertest.Harness) {
		t := h.T
		_, err := h.Sched.Reschedule(t.Context(), h.Org, "sample.open", "k", after(time.Hour))
		if !errors.Is(err, db.ErrNoTx) {
			t.Fatalf("error = %v, want db.ErrNoTx", err)
		}
	})
}

func TestSchedule_BR_IPO_03_CancelStopsPendingJobsByKeyAndBySubject(t *testing.T) {
	h := schedulertest.NewHarness(t)

	h.Run("cancel_by_key_records_cancelled_and_the_job_never_runs", func(h *schedulertest.Harness) {
		t := h.T
		rec := recordKind(h, "sample.open")
		h.Schedule(h.Spec("sample.open", "k", after(time.Hour)))
		var n int
		inTx(t, h, func(ctx context.Context) error {
			var err error
			n, err = h.Sched.Cancel(ctx, h.Org, "sample.open", "k")
			return err
		})
		if n != 1 {
			t.Fatalf("Cancel returned %d, want 1", n)
		}
		row := h.Job("sample.open", "k")
		wantStatus(t, row, "cancelled")
		wantTime(t, "finished_at", row.FinishedAt, start)
		h.Clock.Set(after(2 * time.Hour))
		h.Tick()
		if rec.Count() != 0 {
			t.Fatal("a cancelled job ran")
		}
	})

	h.Run("cancel_rolled_back_leaves_the_job_pending", func(h *schedulertest.Harness) {
		t := h.T
		recordKind(h, "sample.open")
		h.Schedule(h.Spec("sample.open", "k", after(time.Hour)))
		_ = h.InTx(func(ctx context.Context) error {
			if _, err := h.Sched.Cancel(ctx, h.Org, "sample.open", "k"); err != nil {
				return err
			}
			return errBoom
		})
		wantStatus(t, h.Job("sample.open", "k"), "pending")
	})

	h.Run("cancel_of_a_missing_key_is_zero_and_not_an_error", func(h *schedulertest.Harness) {
		t := h.T
		recordKind(h, "sample.open")
		var n int
		inTx(t, h, func(ctx context.Context) error {
			var err error
			n, err = h.Sched.Cancel(ctx, h.Org, "sample.open", "no-such-key")
			return err
		})
		if n != 0 { // US5.7
			t.Fatalf("Cancel on a missing key returned %d, want 0", n)
		}
	})

	h.Run("cancel_subject_stops_every_job_of_one_item_in_this_org_only", func(h *schedulertest.Harness) {
		t := h.T
		rec := recordKind(h, "sample.step")
		other := h.NewOrg("Other")
		for _, key := range []string{"ipo:42:open", "ipo:42:close", "ipo:42:remind"} {
			spec := h.Spec("sample.step", key, after(time.Hour))
			spec.Subject = "ipo:42"
			h.Schedule(spec)
		}
		keep := h.Spec("sample.step", "ipo:43:open", after(time.Hour))
		keep.Subject = "ipo:43"
		h.Schedule(keep)
		foreign := h.Spec("sample.step", "ipo:42:open", after(time.Hour))
		foreign.Subject, foreign.OrgID = "ipo:42", other
		h.Schedule(foreign)

		var n int
		inTx(t, h, func(ctx context.Context) error {
			var err error
			n, err = h.Sched.CancelSubject(ctx, h.Org, "ipo:42")
			return err
		})
		if n != 3 { // FR-013
			t.Fatalf("CancelSubject returned %d, want 3", n)
		}
		for _, key := range []string{"ipo:42:open", "ipo:42:close", "ipo:42:remind"} {
			wantStatus(t, h.Job("sample.step", key), "cancelled")
		}
		wantStatus(t, h.Job("sample.step", "ipo:43:open"), "pending")
		wantStatus(t, h.JobOf(other, "sample.step", "ipo:42:open"), "pending")

		h.Clock.Set(after(2 * time.Hour))
		h.Tick()
		if got := rec.Keys(); len(got) != 2 {
			t.Fatalf("ran %v, want only the untouched jobs of IPO 43 and of the other org", got)
		}
	})

	h.Run("cancel_subject_rolled_back_leaves_all_jobs_pending", func(h *schedulertest.Harness) {
		t := h.T
		recordKind(h, "sample.step")
		for _, key := range []string{"a", "b"} {
			spec := h.Spec("sample.step", key, after(time.Hour))
			spec.Subject = "ipo:42"
			h.Schedule(spec)
		}
		_ = h.InTx(func(ctx context.Context) error {
			if _, err := h.Sched.CancelSubject(ctx, h.Org, "ipo:42"); err != nil {
				return err
			}
			return errBoom
		})
		if n := h.Count("scheduled_jobs", "status = 'pending'"); n != 2 {
			t.Fatalf("pending jobs = %d after a rolled back CancelSubject, want 2", n)
		}
	})

	h.Run("cancel_and_cancel_subject_require_a_transaction", func(h *schedulertest.Harness) {
		t := h.T
		if _, err := h.Sched.Cancel(t.Context(), h.Org, "sample.step", "k"); !errors.Is(err, db.ErrNoTx) {
			t.Errorf("Cancel error = %v, want db.ErrNoTx", err)
		}
		if _, err := h.Sched.CancelSubject(t.Context(), h.Org, "ipo:42"); !errors.Is(err, db.ErrNoTx) {
			t.Errorf("CancelSubject error = %v, want db.ErrNoTx", err)
		}
	})
}

// US5.5 / FR-013: a change that arrives while the job is running is not lost. The handler makes
// the change from a separate transaction while it is running, as another request would.
func TestSchedule_BR_IPO_03_ChangesWhileRunningAreKept(t *testing.T) {
	h := schedulertest.NewHarness(t)

	// whileRunning registers kind with a handler that applies change (once) from another
	// transaction while the job is claimed, writes its effect, and returns result.
	whileRunning := func(h *schedulertest.Harness, kind string, change func(ctx context.Context) error, result error) *recorder {
		rec := &recorder{}
		h.Handle(kind, func(ctx context.Context, job scheduler.Job) error {
			rec.add(job.Key)
			if rec.Count() == 1 {
				if err := h.InTx(change); err != nil { //nolint:contextcheck // another request, its own transaction
					t.Errorf("concurrent change failed: %v", err)
				}
			}
			if err := h.Effect(ctx, job.Key); err != nil {
				return err
			}
			return result
		})
		return rec
	}

	h.Run("reschedule_while_running_ends_pending_at_the_new_time", func(h *schedulertest.Harness) {
		t := h.T
		newTime := after(3 * time.Hour)
		rec := whileRunning(h, "sample.open", func(ctx context.Context) error {
			found, err := h.Sched.Reschedule(ctx, h.Org, "sample.open", "k", newTime)
			if err == nil && !found {
				t.Error("Reschedule of a running job reported no job")
			}
			return err
		}, nil)
		h.Schedule(h.Spec("sample.open", "k", after(-time.Minute)))
		h.Tick()

		row := h.Job("sample.open", "k")
		wantStatus(t, row, "pending") // not done: the new schedule is not lost
		if !row.RunAt.Equal(newTime) || row.Attempts != 0 || row.Rerun || row.LockToken != nil {
			t.Fatalf("row = status %s run_at %v attempts %d rerun %v token %v, want pending at %v, fresh, no lease", row.Status, row.RunAt, row.Attempts, row.Rerun, row.LockToken, newTime)
		}
		if h.Effects("k") != 1 {
			t.Fatalf("effects = %d, want the in-flight run to have committed once", h.Effects("k"))
		}
		h.Tick() // still not due
		h.Clock.Set(newTime)
		h.Tick()
		if rec.Count() != 2 || h.Effects("k") != 2 {
			t.Fatalf("runs = %d, want a second run at the new time", rec.Count())
		}
		wantStatus(t, h.Job("sample.open", "k"), "done")
	})

	h.Run("schedule_upsert_while_running_ends_pending_with_the_new_values", func(h *schedulertest.Harness) {
		t := h.T
		newTime := after(4 * time.Hour)
		whileRunning(h, "sample.open", func(ctx context.Context) error {
			spec := h.Spec("sample.open", "k", newTime)
			spec.Payload = map[string]int{"v": 2}
			return h.Sched.Schedule(ctx, spec)
		}, nil)
		h.Schedule(h.Spec("sample.open", "k", after(-time.Minute)))
		h.Tick()
		rows := h.Jobs("sample.open", "k")
		if len(rows) != 1 {
			t.Fatalf("rows = %d, want the running row to be reused", len(rows))
		}
		wantStatus(t, rows[0], "pending")
		if !rows[0].RunAt.Equal(newTime) || payloadOf(t, rows[0].Payload)["v"] != float64(2) {
			t.Fatalf("run_at=%v payload=%s, want the values of the second Schedule", rows[0].RunAt, rows[0].Payload)
		}
	})

	h.Run("cancel_while_running_lets_a_failing_run_end_cancelled_instead_of_retrying", func(h *schedulertest.Harness) {
		t := h.T
		rec := whileRunning(h, "sample.open", func(ctx context.Context) error {
			n, err := h.Sched.Cancel(ctx, h.Org, "sample.open", "k")
			if err == nil && n != 1 {
				t.Errorf("Cancel of a running job returned %d, want 1", n)
			}
			return err
		}, errBoom)
		h.Schedule(h.Spec("sample.open", "k", after(-time.Minute)))
		h.Tick()
		wantStatus(t, h.Job("sample.open", "k"), "cancelled")
		h.Clock.Advance(48 * time.Hour)
		h.Tick()
		if rec.Count() != 1 {
			t.Fatalf("a job cancelled during its run ran %d times, want 1", rec.Count())
		}
	})
}
