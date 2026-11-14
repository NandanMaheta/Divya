package scheduler_test

// T061 - the time-travel lifecycle (US9.1-9.4, SC-006; BR-IPO-03, BR-TASK-03, BR-SET-08, GS-04).
//
// A sample IPO walks from UPCOMING to SETTLED between Sun 8 Nov and Thu 19 Nov 2026 in 5-minute
// ticks driven by the fake clock. Sample step jobs stand for the IPO module (008), the reminder
// series for 013 and the payment-due job for 018/019; the scheduler only sees opaque kinds.
//
// Calendar (all IST): open Mon 9 Nov 10:00, close Wed 11 Nov 17:00, allotment Thu 12 Nov 10:00,
// listing Fri 13 Nov 10:00, payment due Tue 17 Nov 23:59 (listing + 2 weekdays, GS-04).

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/outbox"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler/schedulertest"
)

const (
	lifeStep      = 5 * time.Minute
	lifeKindStep  = "sample.ipo_step"
	lifeKindPay   = "sample.payment_due"
	lifeKindNudge = "sample.reminder"
	lifeIPO       = "ipo:42"
	lifeTask      = "task:42-1"
)

var (
	lifeFrom = schedulertest.Nov(8, 0, 0)    // Sunday 00:00
	lifeTo   = schedulertest.Nov(19, 23, 55) // last tick of Thursday 19 Nov

	lifeOpen    = schedulertest.Nov(9, 10, 0)
	lifeClose   = schedulertest.Nov(11, 17, 0)
	lifeAllot   = schedulertest.Nov(12, 10, 0)
	lifeListing = schedulertest.Nov(13, 10, 0)
	lifeMoved   = schedulertest.Nov(16, 10, 0) // listing moved to Monday on Thursday 12 Nov
	lifeMoveAt  = schedulertest.Nov(12, 10, 0)

	// The client completes the task in quiet hours, while its 22:00 reminder is deferred to 08:00.
	lifeTaskDone = schedulertest.Nov(10, 23, 15)

	lifeClient = uuid.MustParse("0197a000-0000-7000-8000-0000000000c1")
)

func istDay(d int) istdate.Date { return istdate.Date{Year: 2026, Month: time.November, Day: d} }

// firstTickAtOrAfter is the first 5-minute tick instant that is not before t.
func firstTickAtOrAfter(t time.Time) time.Time {
	n := (t.Sub(lifeFrom) + lifeStep - 1) / lifeStep
	return lifeFrom.Add(n * lifeStep)
}

// lifeRuns remembers the instants (read from the fake clock) at which handlers ran.
type lifeRuns struct {
	mu sync.Mutex
	at map[string][]time.Time
}

func (r *lifeRuns) add(key string, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.at == nil {
		r.at = map[string][]time.Time{}
	}
	r.at[key] = append(r.at[key], at)
}

func (r *lifeRuns) get(key string) []time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Time(nil), r.at[key]...)
}

// reminderSlots are the 2-hourly (standard intensity, BR-TASK-03) reminder instants from the
// opening to the closing of the IPO.
func reminderSlots() []time.Time {
	var out []time.Time
	for at := lifeOpen; !at.After(lifeClose); at = at.Add(2 * time.Hour) {
		out = append(out, at)
	}
	return out
}

func slotKey(at time.Time) string { return "reminder:" + at.In(istdate.IST).Format("0102T1504") }

// lifecycleResult is what a walk through the calendar observed.
type lifecycleResult struct {
	runs  *lifeRuns
	sends map[string]time.Time // delivery instant per dedupe key (every key is delivered once)
	ticks int
}

// walkLifecycle schedules the sample IPO and ticks every 5 minutes from lifeFrom to lifeTo. When
// moveListing is set, the owner moves the listing to Monday 16 Nov at lifeMoveAt, in one
// transaction, rescheduling the dependent payment-due job with the new due date.
func walkLifecycle(h *schedulertest.Harness, moveListing bool) lifecycleResult {
	t := h.T
	res := lifecycleResult{runs: &lifeRuns{}, sends: map[string]time.Time{}}

	step := func(ctx context.Context, job scheduler.Job) error {
		res.runs.add(job.Key, h.Clock.Now())
		return h.Effect(ctx, job.Key)
	}
	h.Handle(lifeKindStep, step)
	h.Handle(lifeKindPay, step)
	h.Handle(lifeKindNudge, func(ctx context.Context, job scheduler.Job) error {
		res.runs.add(job.Key, h.Clock.Now())
		if err := h.Effect(ctx, job.Key); err != nil {
			return err
		}
		_, err := h.Outbox.Add(ctx, outbox.Message{
			OrgID: h.Org, Topic: schedulertest.Topic, Recipient: outbox.Recipient{Kind: "client", ID: &lifeClient},
			Subject: lifeTask, DedupeKey: "msg:" + job.Key, Payload: map[string]string{"task": lifeTask},
		})
		return err
	})
	h.Sender.OnSend(func(_ context.Context, e outbox.Envelope) {
		if _, dup := res.sends[e.DedupeKey]; dup {
			t.Errorf("message %s reached the sender twice", e.DedupeKey)
		}
		res.sends[e.DedupeKey] = h.Clock.Now()
	})

	inTx(t, h, func(ctx context.Context) error {
		for _, s := range []struct {
			key string
			at  time.Time
		}{{"open", lifeOpen}, {"close", lifeClose}, {"allotment", lifeAllot}, {"listing", lifeListing}} {
			spec := h.Spec(lifeKindStep, lifeIPO+":"+s.key, s.at)
			spec.Subject = lifeIPO
			if err := h.Sched.Schedule(ctx, spec); err != nil {
				return err
			}
		}
		pay := h.Spec(lifeKindPay, lifeIPO+":payment_due", istdate.PaymentDue(istDay(13)))
		pay.Subject = lifeIPO
		if err := h.Sched.Schedule(ctx, pay); err != nil {
			return err
		}
		for _, slot := range reminderSlots() { // time-sensitive: useless once the next slot is due
			spec := h.Spec(lifeKindNudge, slotKey(slot), slot)
			spec.Subject = lifeTask
			exp := slot.Add(2 * time.Hour)
			spec.ExpiresAt = &exp
			if err := h.Sched.Schedule(ctx, spec); err != nil {
				return err
			}
		}
		return nil
	})

	// 60 s of real time for the whole walk (SC-006); the ticks themselves never wait.
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	for at := lifeFrom; !at.After(lifeTo); at = at.Add(lifeStep) {
		h.Clock.Set(at)
		if moveListing && at.Equal(lifeMoveAt) {
			inTx(t, h, func(ctx context.Context) error {
				if ok, err := h.Sched.Reschedule(ctx, h.Org, lifeKindStep, lifeIPO+":listing", lifeMoved); err != nil || !ok {
					t.Fatalf("reschedule listing: ok=%v err=%v", ok, err)
				}
				if ok, err := h.Sched.Reschedule(ctx, h.Org, lifeKindPay, lifeIPO+":payment_due", istdate.PaymentDue(istDay(16))); err != nil || !ok {
					t.Fatalf("reschedule payment due: ok=%v err=%v", ok, err)
				}
				return nil
			})
		}
		if at.Equal(lifeTaskDone) { // BR-TASK-03: reminders stop as soon as the task completes
			inTx(t, h, func(ctx context.Context) error {
				if _, err := h.Sched.CancelSubject(ctx, h.Org, lifeTask); err != nil {
					return err
				}
				_, err := h.Outbox.WithdrawSubject(ctx, h.Org, lifeTask)
				return err
			})
		}
		if _, err := h.Sched.Tick(ctx); err != nil {
			t.Fatalf("tick at %v: %v", at, err)
		}
		res.ticks++
	}
	return res
}

func TestLifecycle_BR_IPO_03_BR_SET_08_GS_04_TwoWeeks(t *testing.T) {
	h := schedulertest.NewHarness(t)

	// GS-04 and BR-SET-08 pin the dates the scenarios below depend on.
	for _, c := range []struct {
		listing int
		due     time.Time
	}{{13, schedulertest.Nov(17, 23, 59)}, {12, schedulertest.Nov(16, 23, 59)}, {16, schedulertest.Nov(18, 23, 59)}} {
		if got := istdate.PaymentDue(istDay(c.listing)); !got.Equal(c.due) {
			t.Fatalf("PaymentDue(%d Nov) = %v, want %v (listing + 2 weekdays at 23:59 IST)", c.listing, got, c.due)
		}
	}

	h.Run("us9_1_every_step_runs_once_at_the_first_tick_and_payment_due_follows_listing", func(h *schedulertest.Harness) {
		t := h.T
		res := walkLifecycle(h, false)

		if want := int((lifeTo.Sub(lifeFrom))/lifeStep) + 1; res.ticks != want {
			t.Fatalf("ticked %d times, want %d", res.ticks, want)
		}
		expect := map[string]time.Time{
			lifeIPO + ":open":      lifeOpen,
			lifeIPO + ":close":     lifeClose,
			lifeIPO + ":allotment": lifeAllot,
			lifeIPO + ":listing":   lifeListing,
		}
		for key, due := range expect {
			assertRanOnceAt(t, h, lifeKindStep, key, res.runs, firstTickAtOrAfter(due))
		}
		// Due Tuesday 17 Nov 23:59 is between two ticks: the first tick after it is Wednesday 00:00.
		assertRanOnceAt(t, h, lifeKindPay, lifeIPO+":payment_due", res.runs, schedulertest.Nov(18, 0, 0))

		assertReminderSeries(t, h, res)

		// US9.3: every timestamp the scheduler and outbox wrote came from the fake clock.
		if n := h.Count("tick_runs", "started_at < $1 OR started_at > $2 OR finished_at < $1 OR finished_at > $2", lifeFrom, lifeTo); n != 0 {
			t.Errorf("%d tick records carry an instant outside the simulated calendar: something read the machine or database clock", n)
		}
		if n := h.Count("tick_runs", "true"); n != res.ticks {
			t.Errorf("%d tick records for %d ticks", n, res.ticks)
		}
		if n := h.Count("outbox_messages", "status = 'sent' AND (sent_at < $1 OR sent_at > $2)", lifeFrom, lifeTo); n != 0 {
			t.Errorf("%d messages were marked sent at a real-clock instant", n)
		}
	})

	h.Run("us9_2_listing_moved_to_monday_moves_the_payment_due_job_to_wednesday", func(h *schedulertest.Harness) {
		t := h.T
		res := walkLifecycle(h, true)

		for key, due := range map[string]time.Time{
			lifeIPO + ":open":      lifeOpen,
			lifeIPO + ":close":     lifeClose,
			lifeIPO + ":allotment": lifeAllot,
		} {
			assertRanOnceAt(t, h, lifeKindStep, key, res.runs, firstTickAtOrAfter(due))
		}
		// BR-IPO-03: the date change moved the same job; it did not run on Friday 13 Nov.
		assertRanOnceAt(t, h, lifeKindStep, lifeIPO+":listing", res.runs, lifeMoved)
		if rows := h.Jobs(lifeKindStep, lifeIPO+":listing"); len(rows) != 1 {
			t.Errorf("%d listing rows, want the single moved job", len(rows))
		}
		// BR-SET-08, GS-04: listing Monday 16 Nov gives Wednesday 18 Nov 23:59, so the first tick
		// after it is Thursday 19 Nov 00:00, and the job did not run on the old due date.
		assertRanOnceAt(t, h, lifeKindPay, lifeIPO+":payment_due", res.runs, schedulertest.Nov(19, 0, 0))
		assertReminderSeries(t, h, res)
	})
}

// assertRanOnceAt checks that the handler ran exactly once, at want, and that the job row and its
// single effect agree with it.
func assertRanOnceAt(t *testing.T, h *schedulertest.Harness, kind, key string, runs *lifeRuns, want time.Time) {
	t.Helper()
	got := runs.get(key)
	if len(got) != 1 || !got[0].Equal(want) {
		t.Errorf("%s ran at %v, want exactly once at %v", key, got, want)
		return
	}
	row := h.Job(kind, key)
	wantStatus(t, row, "done")
	wantTime(t, key+" finished_at", row.FinishedAt, want)
	if row.RunAt.After(want) {
		t.Errorf("%s ran at %v before its due time %v", key, want, row.RunAt)
	}
	if e := h.Effects(key); e != 1 {
		t.Errorf("%s has %d effects, want 1", key, e)
	}
}

// assertReminderSeries checks BR-TASK-03: reminders run at their slots until the task is done,
// no message is delivered in quiet hours, and what the client completed is never sent.
func assertReminderSeries(t *testing.T, h *schedulertest.Harness, res lifecycleResult) {
	t.Helper()
	wantSends := 0
	for _, slot := range reminderSlots() {
		key := slotKey(slot)
		row := h.Job(lifeKindNudge, key)
		if slot.After(lifeTaskDone) {
			// stop on completion: the later reminders were cancelled and never ran
			wantStatus(t, row, "cancelled")
			if runs := res.runs.get(key); len(runs) != 0 || h.Effects(key) != 0 {
				t.Errorf("%s ran after the task was done: runs=%v effects=%d", key, runs, h.Effects(key))
			}
			continue
		}
		wantStatus(t, row, "done")
		if runs := res.runs.get(key); len(runs) != 1 || !runs[0].Equal(slot) {
			t.Errorf("%s ran at %v, want once at %v (state work is never late on a 5-minute grid)", key, runs, slot)
		}

		// The message respects quiet hours: it is available at the slot, or at 08:00 when the slot is quiet.
		available := istdate.DefaultQuietHours.Defer(slot)
		sent, wasSent := res.sends["msg:"+key]
		switch {
		case available.After(lifeTaskDone) && !slot.After(lifeTaskDone):
			if wasSent { // withdrawn at 23:15 while deferred to 08:00
				t.Errorf("%s was sent at %v although its task was completed at %v", key, sent, lifeTaskDone)
			}
		case !wasSent || !sent.Equal(available):
			t.Errorf("%s sent at %v (sent=%v), want exactly at %v", key, sent, wasSent, available)
		default:
			wantSends++
		}
	}
	if len(res.sends) != wantSends {
		t.Errorf("%d messages reached the sender, want %d", len(res.sends), wantSends)
	}
	for key, at := range res.sends {
		if istdate.DefaultQuietHours.Contains(at) {
			t.Errorf("message %s was delivered at %v, inside quiet hours (SC-008)", key, at.In(istdate.IST))
		}
	}
	// Concrete anchors, so the expectations above cannot silently drift together.
	if at, ok := res.sends["msg:"+slotKey(schedulertest.Nov(9, 22, 0))]; !ok || !at.Equal(schedulertest.Nov(10, 8, 0)) {
		t.Errorf("the 22:00 reminder of Mon 9 Nov was sent at %v (sent=%v), want Tue 10 Nov 08:00", at, ok)
	}
	if _, ok := res.sends["msg:"+slotKey(schedulertest.Nov(10, 22, 0))]; ok {
		t.Error("the 22:00 reminder of Tue 10 Nov was sent although the task was completed at 23:15")
	}
	if n := h.Count("outbox_messages", "status = 'withdrawn'"); n != 1 {
		t.Errorf("%d withdrawn messages, want exactly the deferred 22:00 reminder", n)
	}
}
