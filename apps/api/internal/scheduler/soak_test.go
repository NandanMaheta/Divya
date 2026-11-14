package scheduler_test

// T065 - soak (SC-001; BR-IPO-03, BR-TASK-03): many IPOs and clients walk through simulated days
// of 5-minute ticks with random duplicate, overlapping and skipped ticks, handler failures and
// crashes after a claim. Afterwards: no job took effect twice, no message reached the sender
// twice (except after a simulated crash between sending and recording), no due job is left unrun,
// nothing expired was sent, and nothing became available inside quiet hours.
//
// Pull requests run a 3-day slice; SWASTIK_SOAK=full runs 30 days (nightly, see
// .github/workflows/nightly.yml). SWASTIK_SOAK_SEED=<n> replays or varies the random choices.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/NandanMaheta/swastik/apps/api/internal/outbox"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db/dbgen"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler/schedulertest"
)

type soakConfig struct {
	days, clients, ipos, slots int
}

func soakSettings() soakConfig {
	if os.Getenv("SWASTIK_SOAK") == "full" {
		return soakConfig{days: 30, clients: 100, ipos: 10, slots: 3}
	}
	return soakConfig{days: 3, clients: 100, ipos: 10, slots: 1}
}

func soakSeed(t *testing.T) uint64 {
	t.Helper()
	seed := uint64(20261114)
	if v, ok := os.LookupEnv("SWASTIK_SOAK_SEED"); ok {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			t.Fatalf("SWASTIK_SOAK_SEED=%q: %v", v, err)
		}
		seed = n
	}
	return seed
}

// lockedRand is a random source shared by handlers running in overlapping ticks.
type lockedRand struct {
	mu sync.Mutex
	r  *rand.Rand
}

func (l *lockedRand) chance(p float64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.r.Float64() < p
}

func (l *lockedRand) intn(n int) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.r.IntN(n)
}

func TestSoak_SC_001(t *testing.T) {
	if testing.Short() {
		t.Skip("soak test: skipped in -short mode")
	}
	cfg := soakSettings()
	seed := soakSeed(t)
	t.Logf("soak: %+v seed=%d (SWASTIK_SOAK_SEED replays it)", cfg, seed)
	h := schedulertest.NewHarness(t)

	h.Run("random_ticks_and_crashes_lose_and_duplicate_nothing", func(h *schedulertest.Harness) {
		t := h.T
		rng := &lockedRand{r: rand.New(rand.NewPCG(seed, 1))}  // the walk
		hrng := &lockedRand{r: rand.New(rand.NewPCG(seed, 2))} // handler failures
		begin := h.Clock.Now()
		end := begin.Add(time.Duration(cfg.days) * 24 * time.Hour)

		clients := make([]uuid.UUID, cfg.clients)
		for i := range clients {
			clients[i] = uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("soak-client-%d", i)))
		}

		// Handlers: a 2 % chance of failing, otherwise one effect (and one message for a reminder).
		var runsBeforeDue sync.Map
		guard := func(job scheduler.Job) {
			if h.Clock.Now().Before(job.RunAt) {
				runsBeforeDue.Store(job.Key, true)
			}
		}
		h.HandleWith("sample.soak_step", func(ctx context.Context, job scheduler.Job) error {
			guard(job)
			if hrng.chance(0.02) {
				return errBoom
			}
			return h.Effect(ctx, job.Key)
		}, scheduler.KindOptions{MaxAttempts: 12})
		h.HandleWith("sample.soak_remind", func(ctx context.Context, job scheduler.Job) error {
			guard(job)
			if hrng.chance(0.02) {
				return errBoom
			}
			if err := h.Effect(ctx, job.Key); err != nil {
				return err
			}
			var client uuid.UUID
			var p struct{ Client string }
			if err := json.Unmarshal(job.Payload, &p); err != nil {
				return err
			}
			client = uuid.MustParse(p.Client)
			expires := job.RunAt.Add(6 * time.Hour)
			_, err := h.Outbox.Add(ctx, outbox.Message{
				OrgID: h.Org, Topic: schedulertest.Topic, Recipient: outbox.Recipient{Kind: "client", ID: &client},
				Subject: job.Subject, DedupeKey: "msg:" + job.Key, Payload: map[string]string{"k": job.Key}, ExpiresAt: &expires,
			})
			return err
		}, scheduler.KindOptions{MaxAttempts: 12})

		var dmu sync.Mutex
		deliveries := map[string][]time.Time{}
		h.Sender.OnSend(func(_ context.Context, e outbox.Envelope) {
			dmu.Lock()
			defer dmu.Unlock()
			deliveries[e.DedupeKey] = append(deliveries[e.DedupeKey], h.Clock.Now())
		})

		// Workload: each IPO has four state-change steps and a payment-due job; each client gets
		// time-sensitive reminders per IPO (useless after 30 minutes).
		var stepKeys []string
		inTx(t, h, func(ctx context.Context) error {
			schedule := func(spec scheduler.JobSpec) {
				if err := h.Sched.Schedule(ctx, spec); err != nil {
					t.Fatalf("schedule %s: %v", spec.Key, err)
				}
			}
			spread := time.Duration(cfg.days) * 24 * time.Hour / time.Duration(cfg.ipos)
			for i := range cfg.ipos {
				open := begin.Add(time.Duration(i)*spread + time.Duration((i*5)%24+1)*time.Hour) // around the clock, so some reminders fall in quiet hours
				ipo := fmt.Sprintf("ipo%02d", i)
				for n, step := range []string{"open", "close", "allotment", "listing"} {
					key := ipo + ":" + step
					stepKeys = append(stepKeys, key)
					s := h.Spec("sample.soak_step", key, open.Add(time.Duration(n)*8*time.Hour))
					s.Subject = ipo
					schedule(s)
				}
				pay := h.Spec("sample.soak_step", ipo+":payment_due", istdate.PaymentDue(istdate.DateOf(open.Add(24*time.Hour))))
				pay.Subject = ipo
				schedule(pay)
				for c := range cfg.clients {
					for s := range cfg.slots {
						at := open.Add(time.Duration(s)*3*time.Hour + time.Duration(c%24)*5*time.Minute)
						exp := at.Add(30 * time.Minute)
						spec := h.Spec("sample.soak_remind", fmt.Sprintf("%s:c%03d:s%d", ipo, c, s), at)
						spec.Subject = fmt.Sprintf("task:%s:c%03d", ipo, c)
						spec.ExpiresAt = &exp
						spec.Payload = map[string]string{"client": clients[c].String()}
						schedule(spec)
					}
				}
			}
			return nil
		})
		totalJobs := h.Count("scheduled_jobs", "true")

		// The walk.
		crashedMsgs := map[string]bool{}
		skip := 0
		var ticks, skipped, duplicated, overlapped, jobCrashes int
		for at := begin; at.Before(end); at = at.Add(5 * time.Minute) {
			h.Clock.Set(at)
			if skip > 0 {
				skip--
				skipped++
				continue
			}
			switch {
			case rng.chance(0.03): // an outage of up to 2.5 hours
				skip = rng.intn(30)
				skipped++
				continue
			case rng.chance(0.07):
				skipped++
				continue
			}
			if rng.chance(0.004) { // an IPO date change: BR-IPO-03
				key := stepKeys[rng.intn(len(stepKeys))]
				inTx(t, h, func(ctx context.Context) error {
					_, err := h.Sched.Reschedule(ctx, h.Org, "sample.soak_step", key, at.Add(time.Duration(1+rng.intn(36))*time.Hour))
					return err
				})
			}
			if rng.chance(0.05) { // a process dies right after claiming jobs
				for range 1 + rng.intn(3) {
					if _, ok := h.ClaimOnly(); ok {
						jobCrashes++
					}
				}
			}
			// A relay dies after the sender accepted a message and before it was marked sent.
			if h.Count("outbox_messages", "status = 'pending' AND available_at <= $1", at) > 0 && rng.chance(0.9) {
				for range 1 + rng.intn(5) {
					now := h.Clock.Now()
					msg, err := dbgen.New(h.Pool).ClaimMessage(t.Context(), dbgen.ClaimMessageParams{
						Now: db.Timestamptz(now), LockedUntil: db.Timestamptz(now.Add(lease)), LockedBy: db.Text("crashed-host"),
					})
					if errors.Is(err, pgx.ErrNoRows) {
						break
					}
					if err != nil {
						t.Fatalf("claim message: %v", err)
					}
					env := outbox.Envelope{ID: msg.ID, OrgID: msg.OrgID, Topic: msg.Topic, DedupeKey: msg.DedupeKey, Attempt: int(msg.Attempts)}
					if err := h.Sender.Send(t.Context(), env); err != nil {
						t.Fatalf("send: %v", err)
					}
					crashedMsgs[msg.DedupeKey] = true
				}
			}

			concurrent := 1
			switch {
			case rng.chance(0.05):
				concurrent = 2 + rng.intn(3)
				overlapped++
			case rng.chance(0.05):
				duplicated++
				h.Tick() // the same tick delivered twice, one after the other
			}
			var wg sync.WaitGroup
			for range concurrent {
				wg.Go(func() {
					if _, err := h.Sched.Tick(t.Context()); err != nil {
						t.Errorf("tick at %v: %v", at, err)
					}
				})
			}
			wg.Wait()
			ticks++
		}

		// Ticks resume normally: leases and backoffs run out and the backlog drains.
		h.Clock.Set(end)
		for range 60 {
			h.Clock.Advance(30 * time.Minute)
			h.Tick()
		}
		final := h.Clock.Now()
		t.Logf("soak: %d ticks, %d skipped, %d duplicated, %d overlapped, %d job crashes, %d message crashes, %d jobs",
			ticks, skipped, duplicated, overlapped, jobCrashes, len(crashedMsgs), totalJobs)
		if ticks == 0 || overlapped == 0 || duplicated == 0 || skipped == 0 {
			t.Errorf("the walk did not exercise every disturbance: ticks=%d overlapped=%d duplicated=%d skipped=%d", ticks, overlapped, duplicated, skipped)
		}

		// Jobs: no effect twice, no due job left unrun, no run before the due time.
		runsBeforeDue.Range(func(k, _ any) bool { t.Errorf("%v ran before its run_at", k); return true })
		if n := h.Count("(SELECT key FROM sample_effects GROUP BY key HAVING count(*) > 1) d", "true"); n != 0 {
			t.Errorf("%d jobs took effect more than once", n)
		}
		if n := h.Count("scheduled_jobs", "status IN ('pending','running') AND run_at <= $1 AND (expires_at IS NULL OR expires_at > $1)", final); n != 0 {
			t.Errorf("%d due jobs were left unrun", n)
		}
		if n := h.Count("scheduled_jobs", "kind = 'sample.soak_step' AND run_at <= $1 AND status <> 'done'", final); n != 0 {
			t.Errorf("%d state-change jobs that are due did not run: they must always run, however late", n)
		}
		if n := h.Count("scheduled_jobs", "status = 'dead'"); n != 0 {
			t.Errorf("%d dead jobs", n)
		}
		if n := h.Count("scheduled_jobs", "status = 'done' AND NOT EXISTS (SELECT 1 FROM sample_effects e WHERE e.key = dedupe_key)"); n != 0 {
			t.Errorf("%d jobs are done without their effect (the mark and the effect must commit together)", n)
		}
		if n := h.Count("sample_effects", "NOT EXISTS (SELECT 1 FROM scheduled_jobs j WHERE j.dedupe_key = key AND j.status = 'done')"); n != 0 {
			t.Errorf("%d effects exist for jobs that are not done", n)
		}
		if n := h.Count("scheduled_jobs", "kind = 'sample.soak_remind' AND status NOT IN ('done','expired','pending')"); n != 0 {
			t.Errorf("%d reminder jobs ended in a state other than done or expired", n)
		}
		if n := h.Count("scheduled_jobs", "kind = 'sample.soak_remind' AND status = 'expired' AND EXISTS (SELECT 1 FROM sample_effects e WHERE e.key = dedupe_key)"); n != 0 {
			t.Errorf("%d expired reminders left an effect", n)
		}
		if n := h.Count("scheduled_jobs", "kind = 'sample.soak_remind' AND status = 'expired'"); n == 0 && cfg.days >= 3 {
			t.Log("no reminder expired in this run: outages were short")
		}

		// Messages: sent once (twice after a crash between sending and recording), never when
		// expired, never made available inside quiet hours, never before they were available.
		if n := h.Count("outbox_messages", "status IN ('pending','sending') AND available_at <= $1", final); n != 0 {
			t.Errorf("%d available messages were left unsent", n)
		}
		if n := h.Count("outbox_messages", "status = 'dead'"); n != 0 {
			t.Errorf("%d dead messages", n)
		}
		rows, err := h.Pool.Query(t.Context(), `SELECT dedupe_key, status, available_at FROM outbox_messages`)
		if err != nil {
			t.Fatalf("messages: %v", err)
		}
		defer rows.Close()
		var sent, expired int
		for rows.Next() {
			var key, status string
			var available time.Time
			if err := rows.Scan(&key, &status, &available); err != nil {
				t.Fatal(err)
			}
			dmu.Lock()
			got := deliveries[key]
			dmu.Unlock()
			if istdate.DefaultQuietHours.Contains(available) {
				t.Errorf("%s became available at %v, inside quiet hours (SC-008)", key, available.In(istdate.IST))
			}
			for _, at := range got {
				if at.Before(available) {
					t.Errorf("%s reached the sender at %v before it was available at %v", key, at, available)
				}
			}
			maxDeliveries := 1
			if crashedMsgs[key] {
				maxDeliveries = 2
			}
			if len(got) > maxDeliveries {
				t.Errorf("%s reached the sender %d times (crashed between send and mark: %v)", key, len(got), crashedMsgs[key])
			}
			switch status {
			case "sent":
				sent++
				if want := maxDeliveries; len(got) != want {
					t.Errorf("%s is sent with %d deliveries, want %d", key, len(got), want)
				}
			case "expired":
				expired++
				if len(got) != 0 {
					t.Errorf("%s expired but reached the sender %d times", key, len(got))
				}
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		t.Logf("soak: %d messages sent, %d expired", sent, expired)
		if sent == 0 {
			t.Error("no message was sent: the soak did not exercise the outbox")
		}
	})
}
