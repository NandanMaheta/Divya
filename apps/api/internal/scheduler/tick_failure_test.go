package scheduler_test

// T043 - the tick when work fails: retries with growing waits then dead, panics recovered,
// expiry skips effects while state-change jobs always run, sanitised last_error, completion
// atomic with the handler's effects (US6.1-6.3, 7.1-7.2; FR-012, FR-014, FR-017, FR-018; BR-IPO-03).

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler/schedulertest"
)

// waitBounds are the allowed waits after the 1st..5th failed attempt: the base schedule of
// data-model section 7 (1 m, 5 m, 15 m, 1 h, 3 h) with +/-20 % jitter. From the 6th on the wait is the 6 h cap.
var waitBounds = [...][2]time.Duration{
	{48 * time.Second, 72 * time.Second},
	{4 * time.Minute, 6 * time.Minute},
	{12 * time.Minute, 18 * time.Minute},
	{48 * time.Minute, 72 * time.Minute},
	{144 * time.Minute, 216 * time.Minute},
}

const maxWait = 6 * time.Hour

func TestTickFailure_RetriesWithGrowingWaitsThenGoesDeadAndIsNeverRunAgain(t *testing.T) {
	h := schedulertest.NewHarness(t)

	// failUntilDead drives an always-failing job through every attempt: each tick runs at the
	// row's own run_at, so a retry can never happen before its time.
	failUntilDead := func(h *schedulertest.Harness, kind string, maxAttempts int) {
		t := h.T
		rec := failingKind(h, kind, fmt.Errorf("push gateway returned 502"))
		h.Schedule(h.Spec(kind, "k", start))
		var prev time.Duration
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			sum := h.Tick()
			row := h.Job(kind, "k")
			if rec.Count() != attempt || row.Attempts != attempt {
				t.Fatalf("attempt %d: handler ran %d times, row attempts=%d", attempt, rec.Count(), row.Attempts)
			}
			wantTime(t, "first_attempt_at", row.FirstAttemptAt, start)
			wantTime(t, "last_attempt_at", row.LastAttemptAt, h.Clock.Now())
			if row.LastError == nil || !strings.Contains(*row.LastError, "502") {
				t.Fatalf("attempt %d: last_error = %v, want the handler's error", attempt, row.LastError)
			}
			if attempt == maxAttempts {
				wantStatus(t, row, "dead") // US6.3
				if sum.Jobs.Dead != 1 || sum.Jobs.Failed != 0 || row.FinishedAt == nil {
					t.Fatalf("final attempt: summary %+v finished_at=%v, want one dead job", sum.Jobs, row.FinishedAt)
				}
				break
			}
			wantStatus(t, row, "pending") // US6.1: pending again with a later due time
			if sum.Jobs.Failed != 1 {
				t.Fatalf("attempt %d: summary %+v, want one failed job", attempt, sum.Jobs)
			}
			wait := row.RunAt.Sub(h.Clock.Now())
			if attempt <= len(waitBounds) {
				if b := waitBounds[attempt-1]; wait < b[0] || wait > b[1] {
					t.Fatalf("wait after attempt %d = %v, want within [%v, %v]", attempt, wait, b[0], b[1])
				}
			} else if wait != maxWait {
				t.Fatalf("wait after attempt %d = %v, want the %v cap", attempt, wait, maxWait)
			}
			if wait < prev { // US6.2: each wait is longer than the previous one, up to the cap
				t.Fatalf("wait after attempt %d = %v is shorter than the previous %v", attempt, wait, prev)
			}
			prev = wait

			h.Clock.Set(row.RunAt.Add(-time.Nanosecond))
			h.Tick()
			if rec.Count() != attempt {
				t.Fatalf("attempt %d was retried before its due time", attempt)
			}
			h.Clock.Set(row.RunAt)
		}
		h.Clock.Advance(14 * 24 * time.Hour)
		h.Tick()
		if rec.Count() != maxAttempts {
			t.Fatalf("a dead job ran again on its own (%d runs, want %d)", rec.Count(), maxAttempts)
		}
		wantStatus(t, h.Job(kind, "k"), "dead")
	}

	h.Run("kind_with_max_attempts_3", func(h *schedulertest.Harness) {
		h.HandleWith("sample.three", nil, scheduler.KindOptions{MaxAttempts: 3})
		failUntilDead(h, "sample.three", 3)
	})
	h.Run("default_8_attempts_with_the_cap_reached_from_the_6th_wait", func(h *schedulertest.Harness) {
		failUntilDead(h, "sample.default", 8)
	})
}

func TestTickFailure_RetriesAreStillSubjectToExpiry(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("expiry_passes_while_waiting_for_a_retry", func(h *schedulertest.Harness) {
		t := h.T
		rec := failingKind(h, "sample.reminder", errBoom)
		exp := after(10 * time.Minute)
		spec := h.Spec("sample.reminder", "slot-1", start)
		spec.ExpiresAt = &exp
		h.Schedule(spec)
		h.Tick() // fails once, retry in about a minute
		h.Clock.Set(after(11 * time.Minute))
		sum := h.Tick()
		if rec.Count() != 1 || sum.Jobs.Expired != 1 {
			t.Fatalf("handler ran %d times, summary %+v; want no second run and the job expired", rec.Count(), sum.Jobs)
		}
		wantStatus(t, h.Job("sample.reminder", "slot-1"), "expired")
	})
}

func TestTickFailure_APanicInAHandlerIsRecoveredAndTheTickContinues(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("panic", func(h *schedulertest.Harness) {
		t := h.T
		const token = "Bearer eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.c2lnbmF0dXJl"
		h.Handle("sample.explode", func(ctx context.Context, job scheduler.Job) error {
			_ = h.Effect(ctx, job.Key) // must be rolled back with the panic
			panic("kaboom " + token + " for PAN ABCDE1234F")
		})
		later := recordKind(h, "sample.step")
		h.Schedule(h.Spec("sample.explode", "boom", start.Add(-time.Hour)))
		h.Schedule(h.Spec("sample.step", "fine", start.Add(-time.Minute)))

		sum := h.Tick() // h.Tick fails the test if the panic escapes as a tick error
		if sum.Jobs.Failed != 1 || sum.Jobs.Run != 1 || later.Count() != 1 {
			t.Fatalf("summary %+v, later job runs %d; want the panicking job failed and the next job run", sum.Jobs, later.Count())
		}
		row := h.Job("sample.explode", "boom")
		wantStatus(t, row, "pending")
		if row.Attempts != 1 || row.LastError == nil {
			t.Fatalf("attempts=%d last_error=%v, want 1 and a recorded error", row.Attempts, row.LastError)
		}
		le := *row.LastError
		if !strings.Contains(strings.ToLower(le), "panic") {
			t.Errorf("last_error %q does not say the handler panicked", le)
		}
		for _, leak := range []string{"goroutine", ".go:", "eyJhbGci", "ABCDE1234F", "Bearer ey"} {
			if strings.Contains(le, leak) {
				t.Errorf("last_error %q leaks %q (stack traces stay in the logs, secrets never stored)", le, leak)
			}
		}
		if len(le) > 500 {
			t.Errorf("last_error is %d chars, want at most 500", len(le))
		}
		if h.Effects("boom") != 0 {
			t.Error("the effect of a panicking handler was committed")
		}
	})
}

func TestTickFailure_LastErrorIsTruncatedAndNeverCarriesSecrets(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("secrets", func(h *schedulertest.Harness) {
		t := h.T
		jwt := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk"
		failingKind(h, "sample.leaky", fmt.Errorf("call failed: Authorization: Bearer abc123.def456.ghi789 pan=ABCDE1234F jwt=%s %s", jwt, strings.Repeat("x", 900)))
		h.Schedule(h.Spec("sample.leaky", "k", start))
		h.Tick()
		row := h.Job("sample.leaky", "k")
		if row.LastError == nil {
			t.Fatal("no last_error recorded")
		}
		le := *row.LastError
		if len(le) > 500 || !strings.Contains(le, "call failed") {
			t.Errorf("last_error has %d chars and prefix %.20q; want <= 500 chars that still start with the real message", len(le), le)
		}
		for _, secret := range []string{"abc123.def456.ghi789", "ABCDE1234F", jwt, "dBjftJeZ4CVPmB92K27uhbUJU1p1r"} {
			if strings.Contains(le, secret) {
				t.Errorf("last_error leaks %q", secret)
			}
		}
	})
}

func TestTickFailure_BR_IPO_03_ExpiredWorkIsRecordedNotRunWhileStateChangesAlwaysRun(t *testing.T) {
	h := schedulertest.NewHarness(t)

	h.Run("hourly_reminder_slots_after_an_outage_only_the_current_slot_runs", func(h *schedulertest.Harness) {
		t := h.T
		reminders := recordKind(h, "sample.reminder")
		state := recordKind(h, "sample.open")
		for slot := range 5 { // slots 10:00..14:00, each valid until the next slot starts (US7.2)
			exp := after(time.Duration(slot+1) * time.Hour)
			spec := h.Spec("sample.reminder", "slot-"+strconv.Itoa(slot), after(time.Duration(slot)*time.Hour))
			spec.ExpiresAt = &exp
			h.Schedule(spec)
		}
		h.Schedule(h.Spec("sample.open", "ipo:42:open", after(30*time.Minute))) // state change, no expiry (US7.1)

		h.Clock.Set(after(4*time.Hour + 30*time.Minute)) // ticks stopped, resume at 14:30
		sum := h.Tick()
		if got := reminders.Keys(); len(got) != 1 || got[0] != "slot-4" {
			t.Fatalf("reminders that ran = %v, want only slot-4", got)
		}
		if state.Count() != 1 {
			t.Fatal("the state-change job without expiry did not run late") // FR-014
		}
		if sum.Jobs.Run != 2 || sum.Jobs.Expired != 4 {
			t.Fatalf("summary %+v, want 2 run and 4 expired", sum.Jobs)
		}
		for slot := range 4 {
			key := "slot-" + strconv.Itoa(slot)
			row := h.Job("sample.reminder", key)
			wantStatus(t, row, "expired")
			wantTime(t, key+" finished_at", row.FinishedAt, h.Clock.Now())
			if row.Attempts != 0 || h.Effects(key) != 0 {
				t.Errorf("%s: attempts=%d effects=%d, an expired job must not perform its effect", key, row.Attempts, h.Effects(key))
			}
		}
	})

	h.Run("expires_at_equal_to_now_is_expired_one_nanosecond_later_still_runs", func(h *schedulertest.Harness) {
		t := h.T
		rec := recordKind(h, "sample.reminder")
		atNow, later := start, start.Add(time.Nanosecond)
		a := h.Spec("sample.reminder", "at-now", after(-time.Hour))
		a.ExpiresAt = &atNow
		b := h.Spec("sample.reminder", "later", after(-time.Hour))
		b.ExpiresAt = &later
		h.Schedule(a)
		h.Schedule(b)
		h.Tick()
		if got := rec.Keys(); len(got) != 1 || got[0] != "later" {
			t.Fatalf("ran %v, want only the job whose expiry is still in the future", got)
		}
		wantStatus(t, h.Job("sample.reminder", "at-now"), "expired")
	})
}

func TestTickFailure_CompletionIsAtomicWithTheHandlersEffects(t *testing.T) {
	h := schedulertest.NewHarness(t)

	h.Run("effects_and_done_become_visible_together", func(h *schedulertest.Harness) {
		t := h.T
		var midEffects, midRunning int
		h.Handle("sample.step", func(ctx context.Context, job scheduler.Job) error {
			if err := h.Effect(ctx, job.Key); err != nil {
				return err
			}
			// Seen from another connection while the handler is still running.
			midEffects = h.Count("sample_effects", "key = $1", job.Key)                               //nolint:contextcheck // read from another connection on purpose
			midRunning = h.Count("scheduled_jobs", "dedupe_key = $1 AND status = 'running'", job.Key) //nolint:contextcheck // read from another connection on purpose
			return nil
		})
		h.Schedule(h.Spec("sample.step", "k", start))
		h.Tick()
		if midEffects != 0 || midRunning != 1 {
			t.Errorf("mid-run: committed effects=%d running rows=%d, want 0 and 1 (nothing visible before the job is marked done)", midEffects, midRunning)
		}
		wantStatus(t, h.Job("sample.step", "k"), "done")
		if h.Effects("k") != 1 {
			t.Errorf("effects after completion = %d, want 1", h.Effects("k"))
		}
	})

	h.Run("a_failing_handler_leaves_no_partial_effects_but_the_failure_is_recorded", func(h *schedulertest.Harness) {
		t := h.T
		h.Handle("sample.step", func(ctx context.Context, job scheduler.Job) error {
			if err := h.Effect(ctx, job.Key); err != nil {
				return err
			}
			if _, err := h.Outbox.Add(ctx, outboxMessage(h, "msg-"+job.Key)); err != nil {
				return err
			}
			return errBoom
		})
		h.Schedule(h.Spec("sample.step", "k", start))
		h.Tick()
		row := h.Job("sample.step", "k")
		wantStatus(t, row, "pending")
		if row.Attempts != 1 || row.LastError == nil || !strings.Contains(*row.LastError, "boom") {
			t.Errorf("attempts=%d last_error=%v, want the failure recorded", row.Attempts, row.LastError)
		}
		if h.Effects("k") != 0 || h.Count("outbox_messages", "true") != 0 {
			t.Errorf("effects=%d messages=%d, want both rolled back with the failed run", h.Effects("k"), h.Count("outbox_messages", "true"))
		}
	})

	h.Run("a_successful_job_commits_its_outbox_message_with_the_done_mark", func(h *schedulertest.Harness) {
		t := h.T
		h.Handle("sample.step", func(ctx context.Context, job scheduler.Job) error {
			_, err := h.Outbox.Add(ctx, outboxMessage(h, "msg-"+job.Key))
			return err
		})
		h.Schedule(h.Spec("sample.step", "k", start))
		h.Tick()
		wantStatus(t, h.Job("sample.step", "k"), "done")
		if h.Count("outbox_messages", "dedupe_key = 'msg-k'") != 1 {
			t.Fatal("the message recorded by the job was not committed with it")
		}
	})
}

func TestTickFailure_HandlerReceivesTheJobAndABoundedContext(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("job_fields_attempt_number_and_deadline", func(h *schedulertest.Harness) {
		t := h.T
		var seen []scheduler.Job
		var hasDeadline bool
		var remaining time.Duration
		h.Handle("sample.step", func(ctx context.Context, job scheduler.Job) error {
			seen = append(seen, job)
			dl, ok := ctx.Deadline()
			hasDeadline, remaining = ok, time.Until(dl)
			if len(seen) == 1 {
				return errBoom
			}
			return nil
		})
		exp := after(48 * time.Hour)
		spec := h.Spec("sample.step", "ipo:42:close", start)
		spec.Subject, spec.ExpiresAt, spec.Payload = "ipo:42", &exp, map[string]string{"ipo_id": "42"}
		h.Schedule(spec)
		h.Tick()
		h.Clock.Advance(2 * time.Minute)
		h.Tick()
		if len(seen) != 2 {
			t.Fatalf("handler ran %d times, want 2 (one failure, one retry)", len(seen))
		}
		j := seen[0]
		if j.OrgID != h.Org || j.Kind != "sample.step" || j.Key != "ipo:42:close" || j.Subject != "ipo:42" ||
			!j.RunAt.Equal(start) || j.ExpiresAt == nil || !j.ExpiresAt.Equal(exp) || payloadOf(t, j.Payload)["ipo_id"] != "42" {
			t.Errorf("job = %+v, want the scheduled values", j)
		}
		if j.Attempt != 1 || seen[1].Attempt != 2 || j.ID != seen[1].ID {
			t.Errorf("attempt numbers %d then %d, ids %v/%v; want 1 then 2 for the same job", j.Attempt, seen[1].Attempt, j.ID, seen[1].ID)
		}
		if !hasDeadline || remaining <= 0 || remaining > 10*time.Second {
			t.Errorf("handler context deadline present=%v remaining=%v, want a deadline within the 10 s handler timeout", hasDeadline, remaining)
		}
	})
}

func TestTickFailure_AFailingJobDoesNotBlockOthers(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("older_failing_job_and_younger_good_job", func(h *schedulertest.Harness) {
		t := h.T
		failingKind(h, "sample.flaky", errBoom)
		ok := recordKind(h, "sample.step")
		h.Schedule(h.Spec("sample.flaky", "bad", start.Add(-2*time.Hour)))
		h.Schedule(h.Spec("sample.step", "good", start.Add(-time.Hour)))
		sum := h.Tick()
		if sum.Jobs.Failed != 1 || sum.Jobs.Run != 1 || ok.Count() != 1 {
			t.Fatalf("summary %+v, good runs %d; want 1 failed and 1 run", sum.Jobs, ok.Count())
		}
	})
}
