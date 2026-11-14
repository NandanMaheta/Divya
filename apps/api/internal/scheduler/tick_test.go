package scheduler_test

// T042 - the tick, happy path: due jobs run once, oldest first, never early; per-tick caps and
// budgets; every tick is recorded; recurring work catches up once
// (US1.1-1.3, 1.7, 1.8, 7.6; FR-005, FR-006, FR-010, FR-015, FR-016; BR-IPO-03).
// Failure behaviour is in tick_failure_test.go, crash and lease behaviour in lease_test.go; the
// overdue status (US6.8, FR-021) is in admin_test.go because Status arrives with T054.

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler/schedulertest"
)

func TestTick_BR_IPO_03_DueJobsRunOnceOldestFirstAndNeverBeforeTheirTime(t *testing.T) {
	h := schedulertest.NewHarness(t)

	h.Run("oldest_due_time_first_each_once_and_future_jobs_wait", func(h *schedulertest.Harness) {
		t := h.T
		rec := recordKind(h, "sample.step")
		h.Schedule(h.Spec("sample.step", "late", after(-30*time.Minute)))
		h.Schedule(h.Spec("sample.step", "early", after(-2*time.Hour)))
		h.Schedule(h.Spec("sample.step", "mid", after(-time.Hour)))
		h.Schedule(h.Spec("sample.step", "future", after(10*time.Minute)))

		sum := h.Tick()
		if want := []string{"early", "mid", "late"}; !slices.Equal(rec.Keys(), want) { // FR-016
			t.Fatalf("ran %v, want %v (oldest due time first, the future job not at all)", rec.Keys(), want)
		}
		if sum.Jobs.Run != 3 || sum.Jobs.Failed != 0 || sum.Jobs.Dead != 0 || sum.Jobs.Expired != 0 || sum.BudgetExhausted {
			t.Fatalf("summary jobs=%+v budget_exhausted=%v, want 3 run and nothing else", sum.Jobs, sum.BudgetExhausted)
		}
		for _, key := range []string{"early", "mid", "late"} {
			row := h.Job("sample.step", key)
			wantStatus(t, row, "done") // US1.1
			wantTime(t, key+" finished_at", row.FinishedAt, start)
			wantTime(t, key+" first_attempt_at", row.FirstAttemptAt, start)
			wantTime(t, key+" last_attempt_at", row.LastAttemptAt, start)
			if row.Attempts != 1 || row.LockToken != nil || row.LockedUntil != nil || h.Effects(key) != 1 {
				t.Errorf("%s: attempts=%d token=%v locked_until=%v effects=%d, want 1, none, none, 1", key, row.Attempts, row.LockToken, row.LockedUntil, h.Effects(key))
			}
		}
		wantStatus(t, h.Job("sample.step", "future"), "pending") // US1.2

		h.Tick() // a second tick at the same instant changes nothing
		if rec.Count() != 3 {
			t.Fatalf("a second tick re-ran work: %v", rec.Keys())
		}

		h.Clock.Set(after(10*time.Minute - time.Nanosecond))
		h.Tick()
		if rec.CountKey("future") != 0 {
			t.Fatal("the job ran before its due time")
		}
		h.Clock.Set(after(10 * time.Minute)) // due exactly at the tick instant runs on that tick (spec edge case)
		h.Tick()
		if rec.CountKey("future") != 1 {
			t.Fatal("the job did not run at exactly its due time")
		}
	})
}

// US1.3, US7.1: a job is due once its time has passed, not only within the last 5 minutes.
func TestTick_BR_IPO_03_LateNotSkipped(t *testing.T) {
	h := schedulertest.NewHarness(t)

	h.Run("a_skipped_tick_and_a_long_outage_never_lose_state_change_work", func(h *schedulertest.Harness) {
		t := h.T
		rec := recordKind(h, "sample.step")
		h.Schedule(h.Spec("sample.step", "ipo:42:close", after(30*time.Minute))) // due 10:30
		h.Schedule(h.Spec("sample.step", "ipo:43:close", after(24*time.Hour+time.Minute)))
		h.Clock.Set(after(29 * time.Minute))
		h.Tick() // 10:29 tick: nothing due yet
		// The 10:30 tick is skipped entirely; the next tick is at 10:35 (US1.3) ...
		h.Clock.Set(after(35 * time.Minute))
		h.Tick()
		if rec.CountKey("ipo:42:close") != 1 {
			t.Fatal("a job whose tick was skipped did not run on the next tick: due means 'due time has passed'")
		}
		// ... and a long outage never loses work either: this one is more than two days late.
		h.Clock.Set(after(72 * time.Hour))
		h.Tick()
		if rec.CountKey("ipo:43:close") != 1 {
			t.Fatal("a state-change job two days late did not run")
		}
	})
}

func TestTick_IdleTickIsRecordedAndTouchesTheDatabase(t *testing.T) {
	h := schedulertest.NewHarness(t)

	h.Run("an_idle_tick_succeeds_with_zero_counts_and_writes_one_tick_run", func(h *schedulertest.Harness) {
		t := h.T
		h.Clock.Set(after(time.Minute))
		sum := h.Tick()
		if sum.Jobs.Run+sum.Jobs.Failed+sum.Jobs.Dead+sum.Jobs.Expired != 0 || sum.Messages.Sent+sum.Messages.Failed+sum.Messages.Dead+sum.Messages.Expired != 0 || sum.BudgetExhausted {
			t.Fatalf("idle summary = %+v", sum)
		}
		if sum.TickID == uuid.Nil || !sum.StartedAt.Equal(after(time.Minute)) || sum.FinishedAt.Before(sum.StartedAt) {
			t.Errorf("summary id=%v started=%v finished=%v", sum.TickID, sum.StartedAt, sum.FinishedAt)
		}
		if n := h.Count("tick_runs", "true"); n != 1 { // US1.7, FR-006
			t.Fatalf("tick_runs rows = %d, want 1", n)
		}
		tr := h.LastTickRun()
		if tr.ID != sum.TickID || tr.Status != "ok" || tr.Instance != schedulertest.Instance || !tr.FinishedAt.Valid ||
			!db.Time(tr.StartedAt).Equal(after(time.Minute)) {
			t.Errorf("tick_runs row = %+v, want id %v status ok instance %s started %v and a finish time", tr, sum.TickID, schedulertest.Instance, after(time.Minute))
		}
		if tr.JobsRun != 0 || tr.MsgsSent != 0 || tr.BudgetExhausted {
			t.Errorf("idle tick_runs counts = %+v, want zeros", tr)
		}
		h.Clock.Advance(5 * time.Minute)
		h.Tick()
		if n := h.Count("tick_runs", "true"); n != 2 {
			t.Fatalf("tick_runs rows = %d after two ticks, want 2", n)
		}
	})

	h.Run("the_tick_run_row_carries_the_same_counts_as_the_summary", func(h *schedulertest.Harness) {
		t := h.T
		recordKind(h, "sample.step")
		failingKind(h, "sample.flaky", errBoom)
		h.Schedule(h.Spec("sample.step", "a", after(-time.Hour)))
		h.Schedule(h.Spec("sample.step", "b", after(-time.Minute)))
		h.Schedule(h.Spec("sample.flaky", "f", after(-time.Minute)))
		exp := after(-time.Minute)
		stale := h.Spec("sample.step", "stale", after(-time.Hour))
		stale.ExpiresAt = &exp
		h.Schedule(stale)
		h.AddMessage(schedulertest.Topic, "m1")

		sum := h.Tick()
		if sum.Jobs.Run != 2 || sum.Jobs.Failed != 1 || sum.Jobs.Expired != 1 || sum.Messages.Sent != 1 {
			t.Fatalf("summary = jobs %+v messages %+v, want run 2, failed 1, expired 1, sent 1", sum.Jobs, sum.Messages)
		}
		tr := h.LastTickRun()
		if int(tr.JobsRun) != sum.Jobs.Run || int(tr.JobsFailed) != sum.Jobs.Failed || int(tr.JobsDead) != sum.Jobs.Dead ||
			int(tr.JobsExpired) != sum.Jobs.Expired || int(tr.MsgsSent) != sum.Messages.Sent || int(tr.MsgsFailed) != sum.Messages.Failed ||
			int(tr.MsgsDead) != sum.Messages.Dead || int(tr.MsgsExpired) != sum.Messages.Expired || tr.BudgetExhausted != sum.BudgetExhausted {
			t.Fatalf("tick_runs %+v does not match summary %+v", tr, sum)
		}
	})

	h.Run("an_unreachable_database_makes_the_tick_return_an_error", func(h *schedulertest.Harness) {
		t := h.T
		pool, err := pgxpool.NewWithConfig(t.Context(), h.Pool.Config())
		if err != nil {
			t.Fatal(err)
		}
		pool.Close() // every later query fails, as if the database were down (FR-007, US1.7)
		txr := db.NewTxRunner(pool)
		sch := scheduler.New(txr, h.Outbox, h.Clock.Now, slog.New(slog.NewTextHandler(io.Discard, nil)), schedulertest.Instance, scheduler.DefaultOptions())
		if _, err := sch.Tick(t.Context()); err == nil {
			t.Fatal("Tick returned nil although the database is unreachable; the handler needs the error to answer 503")
		}
	})
}

func TestTick_RunsJobsFirstThenRelaysAvailableMessages(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("a_message_recorded_by_a_job_is_relayed_in_the_same_tick", func(h *schedulertest.Harness) {
		t := h.T
		h.Handle("sample.notify", func(ctx context.Context, job scheduler.Job) error {
			_, err := h.Outbox.Add(ctx, outboxMessage(h, "from-job"))
			return err
		})
		h.AddMessage(schedulertest.Topic, "already-waiting")
		h.Schedule(h.Spec("sample.notify", "n", after(-time.Minute)))

		sum := h.Tick()
		if sum.Jobs.Run != 1 || sum.Messages.Sent != 2 {
			t.Fatalf("summary jobs=%+v messages=%+v, want 1 job run and both messages sent", sum.Jobs, sum.Messages)
		}
		if h.Sender.Deliveries("from-job") != 1 || h.Sender.Deliveries("already-waiting") != 1 {
			t.Fatalf("deliveries: from-job=%d already-waiting=%d, want 1 each", h.Sender.Deliveries("from-job"), h.Sender.Deliveries("already-waiting"))
		}
	})
}

func TestTick_PerTickCapsAndBudgetsLeaveTheRestForTheNextTick(t *testing.T) {
	h := schedulertest.NewHarness(t)

	h.Run("at_most_250_jobs_per_tick_oldest_first_then_the_rest_without_repeats", func(h *schedulertest.Harness) {
		t := h.T
		rec := &recorder{}
		h.Handle("sample.bulk", func(_ context.Context, job scheduler.Job) error { rec.add(job.Key); return nil })
		want := make([]string, 0, 260)
		for i := range 260 {
			key := "job-" + strconv.Itoa(1000+i)
			want = append(want, key)
			h.SeedJob(schedulertest.JobSeed{Kind: "sample.bulk", Key: key, RunAt: after(-time.Duration(260-i) * time.Minute)})
		}
		first := h.Tick()
		if first.Jobs.Run != 250 || !first.BudgetExhausted { // FR-005, US1.8
			t.Fatalf("first tick ran %d jobs, budget_exhausted=%v; want 250 and true", first.Jobs.Run, first.BudgetExhausted)
		}
		if !slices.Equal(rec.Keys(), want[:250]) {
			t.Fatal("the first 250 jobs were not the 250 oldest, in order")
		}
		second := h.Tick()
		if second.Jobs.Run != 10 || second.BudgetExhausted {
			t.Fatalf("second tick ran %d jobs, budget_exhausted=%v; want the remaining 10 and false", second.Jobs.Run, second.BudgetExhausted)
		}
		if !slices.Equal(rec.Keys(), want) {
			t.Fatalf("jobs ran %d times in total or out of order, want each of the 260 exactly once oldest first", rec.Count())
		}
	})

	h.Run("the_jobs_phase_stops_taking_new_work_after_30_seconds", func(h *schedulertest.Harness) {
		t := h.T
		rec := &recorder{}
		h.Handle("sample.slow", func(_ context.Context, job scheduler.Job) error {
			rec.add(job.Key)
			h.Clock.Advance(11 * time.Second) // each job "takes" 11 s on the fake clock
			return nil
		})
		for i := range 10 {
			h.SeedJob(schedulertest.JobSeed{Kind: "sample.slow", Key: "s-" + strconv.Itoa(i), RunAt: after(-time.Duration(10-i) * time.Minute)})
		}
		sum := h.Tick()
		if sum.Jobs.Run != 3 || !sum.BudgetExhausted { // 11 s, 22 s, 33 s >= 30 s
			t.Fatalf("tick ran %d jobs, budget_exhausted=%v; want 3 and true", sum.Jobs.Run, sum.BudgetExhausted)
		}
		if n := h.Count("scheduled_jobs", "status = 'pending'"); n != 7 {
			t.Fatalf("pending jobs = %d, want the 7 others left for the next tick", n)
		}
	})

	h.Run("at_most_250_messages_per_tick", func(h *schedulertest.Harness) {
		t := h.T
		for i := range 255 {
			h.SeedMessage(schedulertest.MessageSeed{Key: "m-" + strconv.Itoa(i)})
		}
		first := h.Tick()
		if first.Messages.Sent != 250 || !first.BudgetExhausted {
			t.Fatalf("first tick sent %d messages, budget_exhausted=%v; want 250 and true", first.Messages.Sent, first.BudgetExhausted)
		}
		if second := h.Tick(); second.Messages.Sent != 5 || second.BudgetExhausted {
			t.Fatalf("second tick sent %d, budget_exhausted=%v; want 5 and false", second.Messages.Sent, second.BudgetExhausted)
		}
		if h.Sender.Count() != 255 {
			t.Fatalf("sender received %d messages, want 255 (each exactly once)", h.Sender.Count())
		}
	})
}

func TestTick_RecurringKindsRunOnceOnCatchUp(t *testing.T) {
	h := schedulertest.NewHarness(t)
	nextHour := func(now time.Time) time.Time { return now.Truncate(time.Hour).Add(time.Hour) }

	h.Run("an_active_row_per_org_is_ensured_and_missed_runs_collapse_into_one", func(h *schedulertest.Harness) {
		t := h.T
		rec := &recorder{}
		h.HandleWith("sample.recurring", func(_ context.Context, job scheduler.Job) error {
			rec.add(job.Key)
			return nil
		}, scheduler.KindOptions{Recurring: nextHour})

		h.Tick() // creates the row; its first run is the next occurrence, not "now"
		row := h.Job("sample.recurring", "recurring")
		wantStatus(t, row, "pending")
		if !row.Recurring || !row.RunAt.Equal(nextHour(start)) || rec.Count() != 0 {
			t.Fatalf("recurring=%v run_at=%v runs=%d, want a recurring row due %v and no run yet", row.Recurring, row.RunAt, rec.Count(), nextHour(start))
		}

		h.Clock.Set(after(5 * time.Hour)) // several occurrences missed (US7.6, FR-015)
		sum := h.Tick()
		if rec.Count() != 1 || sum.Jobs.Run != 1 {
			t.Fatalf("recurring job ran %d times (summary %d) after 5 missed hours, want once", rec.Count(), sum.Jobs.Run)
		}
		row = h.Job("sample.recurring", "recurring")
		wantStatus(t, row, "pending")
		if !row.RunAt.Equal(nextHour(h.Clock.Now())) || row.Attempts != 0 || row.FinishedAt != nil {
			t.Fatalf("after the run: run_at=%v attempts=%d finished_at=%v, want re-armed for %v", row.RunAt, row.Attempts, row.FinishedAt, nextHour(h.Clock.Now()))
		}
		h.Tick()
		if rec.Count() != 1 {
			t.Fatal("the recurring job ran again at the same instant")
		}
		h.Clock.Set(row.RunAt)
		h.Tick()
		if rec.Count() != 2 {
			t.Fatalf("the recurring job did not run at its next occurrence (runs=%d)", rec.Count())
		}
		if n := h.Count("scheduled_jobs", "kind = 'sample.recurring'"); n != 1 {
			t.Fatalf("recurring rows = %d, want the same single row re-armed", n)
		}
	})

	h.Run("every_org_gets_its_own_row_and_a_dead_row_is_replaced", func(h *schedulertest.Harness) {
		t := h.T
		h.HandleWith("sample.recurring", func(context.Context, scheduler.Job) error { return nil }, scheduler.KindOptions{Recurring: nextHour})
		h.SeedJob(schedulertest.JobSeed{Kind: "sample.recurring", Key: "recurring", Subject: "recurring", Status: "dead", Recurring: true, LastError: "gave up"})
		other := h.NewOrg("Other")
		h.Tick()
		for _, org := range []uuid.UUID{h.Org, other} {
			if n := h.Count("scheduled_jobs", "org_id = $1 AND kind = 'sample.recurring' AND status = 'pending' AND dedupe_key = 'recurring'", org); n != 1 {
				t.Errorf("org %v has %d pending recurring rows, want 1 (self-healing after a dead row)", org, n)
			}
		}
		h.Tick()
		if n := h.Count("scheduled_jobs", "kind = 'sample.recurring' AND status IN ('pending','running')"); n != 2 {
			t.Fatalf("active recurring rows = %d after a second tick, want still one per org", n)
		}
	})
}

func TestTick_UnknownKindFailsThatJobAndTheTickContinues(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("unknown_kind", func(h *schedulertest.Harness) {
		t := h.T
		rec := recordKind(h, "sample.step")
		h.SeedJob(schedulertest.JobSeed{Kind: "ghost.kind", Key: "g", RunAt: after(-2 * time.Hour)})
		h.Schedule(h.Spec("sample.step", "ok", after(-time.Hour)))

		sum := h.Tick()
		if sum.Jobs.Failed != 1 || sum.Jobs.Run != 1 || rec.Count() != 1 {
			t.Fatalf("summary jobs=%+v runs=%d, want the ghost job failed and the other job run", sum.Jobs, rec.Count())
		}
		row := h.Job("ghost.kind", "g")
		wantStatus(t, row, "pending") // retried later like any failure
		if row.Attempts != 1 || row.LastError == nil || !strings.Contains(strings.ToLower(*row.LastError), "unknown") || !row.RunAt.After(start) {
			t.Fatalf("row attempts=%d last_error=%v run_at=%v, want 1 attempt, an 'unknown kind' error and a later run_at", row.Attempts, row.LastError, row.RunAt)
		}
	})
}
