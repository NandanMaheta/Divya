package scheduler_test

// T062 - catch-up after downtime (US7.1-7.6, SC-003; BR-IPO-03, BR-TASK-03): after hours without
// ticks, state-change work runs late but runs, stale time-sensitive work is recorded as expired
// and never sent, and recurring work runs once however many runs were missed.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/NandanMaheta/swastik/apps/api/internal/outbox"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler/schedulertest"
)

func TestCatchUp_BR_IPO_03_BR_TASK_03_SixHourOutageWith500Jobs(t *testing.T) {
	h := schedulertest.NewHarness(t)

	h.Run("backlog_drains_in_three_ticks_and_stale_work_is_never_sent", func(h *schedulertest.Harness) {
		t := h.T
		outageStart := after(-time.Hour) // 09:00, the last tick before the outage
		resume := after(5 * time.Hour)   // 15:00, six hours later
		h.Clock.Set(outageStart)

		states := &recorder{}
		reminders := &recorder{}
		dueAt := map[string]time.Time{}
		h.Handle("sample.state", func(ctx context.Context, job scheduler.Job) error {
			states.add(job.Key)
			return h.Effect(ctx, job.Key)
		})
		h.Handle("sample.remind", func(ctx context.Context, job scheduler.Job) error {
			reminders.add(job.Key)
			if err := h.Effect(ctx, job.Key); err != nil {
				return err
			}
			_, err := h.Outbox.Add(ctx, outboxMessage(h, "msg:"+job.Key))
			return err
		})

		inTx(t, h, func(ctx context.Context) error {
			schedule := func(spec scheduler.JobSpec) {
				if err := h.Sched.Schedule(ctx, spec); err != nil {
					t.Fatalf("schedule %s: %v", spec.Key, err)
				}
				dueAt[spec.Key] = spec.RunAt
			}
			// 500 state-change jobs, each at its own instant between 09:05 and 14:38, no expiry (US7.5).
			for i := range 500 {
				schedule(h.Spec("sample.state", fmt.Sprintf("state-%03d", i), outageStart.Add(5*time.Minute+time.Duration(i)*40*time.Second)))
			}
			// US7.1: "IPO closes" was due at 13:00:20, inside the outage.
			schedule(h.Spec("sample.state", "ipo-close", schedulertest.Nov(14, 13, 0).Add(20*time.Second)))
			// 50 reminders that were useful only until 14:00 (US7.2) ...
			staleUntil := schedulertest.Nov(14, 14, 0)
			for i := range 50 {
				spec := h.Spec("sample.remind", fmt.Sprintf("stale-%02d", i), outageStart.Add(time.Hour+time.Duration(i)*time.Minute))
				spec.ExpiresAt = &staleUntil
				schedule(spec)
			}
			// ... and 5 that are still useful when the ticks resume.
			freshUntil := schedulertest.Nov(14, 15, 30)
			for i := range 5 {
				spec := h.Spec("sample.remind", fmt.Sprintf("fresh-%d", i), schedulertest.Nov(14, 14, 30).Add(time.Duration(i)*time.Minute))
				spec.ExpiresAt = &freshUntil
				schedule(spec)
			}
			// Messages recorded before the outage: 10 expired by 12:00, 10 informational, 5 valid until 18:00.
			add := func(key string, expires *time.Time) {
				m := outboxMessage(h, key)
				m.ExpiresAt = expires
				if _, err := h.Outbox.Add(ctx, m); err != nil {
					t.Fatalf("add %s: %v", key, err)
				}
			}
			noon, evening := schedulertest.Nov(14, 12, 0), schedulertest.Nov(14, 18, 0)
			for i := range 10 {
				add(fmt.Sprintf("old-%d", i), &noon)
				add(fmt.Sprintf("info-%d", i), nil) // US7.4: no expiry, so it goes out late
			}
			for i := range 5 {
				add(fmt.Sprintf("valid-%d", i), &evening)
			}
			return nil
		})

		// Nothing runs while the ticks are down: the clock moves, no tick is made.
		h.Clock.Set(resume)

		var sums []scheduler.TickSummary
		for i := range 3 { // SC-003: within 3 ticks (15 minutes) of ticks resuming
			h.Clock.Set(resume.Add(time.Duration(i) * 5 * time.Minute))
			sums = append(sums, h.Tick())
		}
		if !sums[0].BudgetExhausted {
			t.Errorf("the first tick drained a 500-job backlog within its caps: %+v", sums[0])
		}
		if first := sums[0].Jobs.Run; first != 250 {
			t.Errorf("the first tick ran %d jobs, want the per-tick cap of 250", first)
		}
		if sums[0].Jobs.Expired != 50 {
			t.Errorf("the first tick expired %d jobs, want the 50 stale reminders", sums[0].Jobs.Expired)
		}

		// State-change work: every job ran, once, late, oldest due time first (US7.1, US7.5).
		if got := states.Count(); got != 501 {
			t.Fatalf("%d state-change runs, want 501", got)
		}
		var prev time.Time
		for _, key := range states.Keys() {
			if dueAt[key].Before(prev) {
				t.Fatalf("%s (due %v) ran after a job due later (%v): the backlog must drain oldest first", key, dueAt[key], prev)
			}
			prev = dueAt[key]
		}
		for _, key := range append(states.Keys(), "ipo-close") {
			if h.Effects(key) != 1 {
				t.Fatalf("%s has %d effects, want 1", key, h.Effects(key))
			}
		}
		wantStatus(t, h.Job("sample.state", "ipo-close"), "done")
		if n := h.Count("scheduled_jobs", "kind = 'sample.state' AND status <> 'done'"); n != 0 {
			t.Errorf("%d state-change jobs are not done after 3 ticks", n)
		}

		// Stale reminders: recorded as expired, never run, no message (US7.2).
		for i := range 50 {
			key := fmt.Sprintf("stale-%02d", i)
			wantStatus(t, h.Job("sample.remind", key), "expired")
			if h.Effects(key) != 0 || h.Count("outbox_messages", "dedupe_key = $1", "msg:"+key) != 0 {
				t.Errorf("%s left an effect or a message although it had expired", key)
			}
		}
		if reminders.Count() != 5 {
			t.Errorf("reminders ran %d times (%v), want only the 5 still valid", reminders.Count(), reminders.Keys())
		}

		// Messages: expired ones never reach the sender (US7.3); the rest go out late, once (US7.4).
		for i := range 10 {
			if n := len(callsFor(h, fmt.Sprintf("old-%d", i))); n != 0 {
				t.Errorf("old-%d reached the sender %d times after its expiry", i, n)
			}
			if h.Sender.Deliveries(fmt.Sprintf("info-%d", i)) != 1 {
				t.Errorf("info-%d deliveries = %d, want 1", i, h.Sender.Deliveries(fmt.Sprintf("info-%d", i)))
			}
		}
		for i := range 5 {
			if h.Sender.Deliveries(fmt.Sprintf("valid-%d", i)) != 1 || h.Sender.Deliveries(fmt.Sprintf("msg:fresh-%d", i)) != 1 {
				t.Errorf("valid-%d / fresh-%d were not each delivered once", i, i)
			}
		}
		if h.Sender.Count() != 20 {
			t.Errorf("sender got %d messages, want 20 (10 info, 5 valid, 5 fresh reminders)", h.Sender.Count())
		}
		if n := h.Count("outbox_messages", "status = 'expired'"); n != 10 {
			t.Errorf("%d expired messages, want 10", n)
		}
		if n := h.Count("outbox_messages", "status <> 'sent' AND status <> 'expired'"); n != 0 {
			t.Errorf("%d messages are neither sent nor expired", n)
		}
	})

	h.Run("hourly_slots_only_the_latest_still_valid_one_runs", func(h *schedulertest.Harness) {
		t := h.T
		h.Clock.Set(schedulertest.Nov(14, 9, 0))
		rec := &recorder{}
		h.Handle("sample.hourly_slot", func(ctx context.Context, job scheduler.Job) error {
			rec.add(job.Key)
			_, err := h.Outbox.Add(ctx, outbox.Message{
				OrgID: h.Org, Topic: schedulertest.Topic, Recipient: outbox.Recipient{Kind: "admin"}, Subject: "sample:" + job.Key,
				DedupeKey: "msg:" + job.Key, Payload: map[string]string{"k": job.Key}, ExpiresAt: job.ExpiresAt,
			})
			return err
		})
		inTx(t, h, func(ctx context.Context) error {
			for hour := 10; hour <= 14; hour++ {
				spec := h.Spec("sample.hourly_slot", fmt.Sprintf("slot-%02d", hour), schedulertest.Nov(14, hour, 0))
				exp := schedulertest.Nov(14, hour+1, 0) // valid until the next slot starts
				spec.ExpiresAt = &exp
				if err := h.Sched.Schedule(ctx, spec); err != nil {
					return err
				}
			}
			return nil
		})

		h.Clock.Set(schedulertest.Nov(14, 14, 30))
		sum := h.Tick()
		if want := []string{"slot-14"}; fmt.Sprint(rec.Keys()) != fmt.Sprint(want) {
			t.Fatalf("ran %v, want only %v", rec.Keys(), want)
		}
		if sum.Jobs.Run != 1 || sum.Jobs.Expired != 4 {
			t.Errorf("summary %+v, want 1 run and 4 expired", sum.Jobs)
		}
		for hour := 10; hour <= 13; hour++ {
			key := fmt.Sprintf("slot-%02d", hour)
			wantStatus(t, h.Job("sample.hourly_slot", key), "expired")
			if h.Count("outbox_messages", "dedupe_key = $1", "msg:"+key) != 0 {
				t.Errorf("%s produced a message", key)
			}
		}
		if h.Sender.Count() != 1 || h.Sender.Deliveries("msg:slot-14") != 1 {
			t.Errorf("sender got %d messages, want only msg:slot-14", h.Sender.Count())
		}
	})
}

func TestCatchUp_BR_IPO_03_RecurringRunsOnceAfterMissedRuns(t *testing.T) {
	h := schedulertest.NewHarness(t)

	h.Run("six_missed_runs_make_one_run", func(h *schedulertest.Harness) {
		t := h.T
		const kind = "sample.hourly"
		rec := &recorder{}
		h.HandleWith(kind, func(ctx context.Context, job scheduler.Job) error {
			rec.add(job.Key)
			return h.Effect(ctx, fmt.Sprintf("run@%s", h.Clock.Now().Format(time.RFC3339)))
		}, scheduler.KindOptions{Recurring: hourlyAfter})

		h.Tick() // 10:00: the recurring row is created for the next occurrence, 11:00
		first := h.Job(kind, "recurring")
		wantTime(t, "first run_at", &first.RunAt, hourlyAfter(start))
		if rec.Count() != 0 {
			t.Fatalf("ran %d times before its first occurrence", rec.Count())
		}
		h.Clock.Set(hourlyAfter(start))
		h.Tick()
		if rec.Count() != 1 {
			t.Fatalf("runs = %d at 11:00, want 1", rec.Count())
		}

		// The ticks stop; runs of 12:00 to 16:00 are missed. They resume at 17:00.
		h.Clock.Set(after(7 * time.Hour))
		h.Tick()
		if rec.Count() != 2 {
			t.Fatalf("runs = %d after the outage, want exactly 1 more (once, not once per missed run)", rec.Count())
		}
		row := h.Job(kind, "recurring")
		wantStatus(t, row, "pending")
		wantTime(t, "next run_at", &row.RunAt, after(8*time.Hour))

		h.Clock.Set(after(7*time.Hour + 5*time.Minute))
		h.Tick()
		if rec.Count() != 2 {
			t.Fatalf("runs = %d at 17:05, want no extra run", rec.Count())
		}
		h.Clock.Set(after(8 * time.Hour))
		h.Tick()
		if rec.Count() != 3 {
			t.Fatalf("runs = %d at 18:00, want 3", rec.Count())
		}
		if n := h.Count("scheduled_jobs", "kind = $1 AND status IN ('pending','running')", kind); n != 1 {
			t.Errorf("%d active rows for the recurring kind, want 1", n)
		}
	})
}

// hourlyAfter is the next full IST hour strictly after t.
func hourlyAfter(t time.Time) time.Time {
	midnight := istdate.DateOf(t).At(0, 0)
	return midnight.Add((t.Sub(midnight)/time.Hour + 1) * time.Hour)
}

// callsFor returns the envelopes the sender was called with for one dedupe key, failed calls included.
func callsFor(h *schedulertest.Harness, key string) []outbox.Envelope {
	var out []outbox.Envelope
	for _, e := range h.Sender.Calls() {
		if e.DedupeKey == key {
			out = append(out, e)
		}
	}
	return out
}
