package scheduler_test

// T063 - overlapping ticks (US1.4, US3.8; FR-011, FR-026): Cloud Scheduler may deliver a tick
// twice, or a slow tick may still be running when the next one starts. Whatever the overlap,
// every job takes effect once and every message reaches the sender once.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler/schedulertest"
)

func TestTick_ConcurrentOverlappingTicksTakeEffectOnce(t *testing.T) {
	h := schedulertest.NewHarness(t)

	for _, tc := range []struct{ ticks, work int }{{2, 10}, {2, 100}, {5, 10}, {5, 100}} {
		h.Run(fmt.Sprintf("%d_concurrent_ticks_over_%d_jobs_and_messages", tc.ticks, tc.work), func(h *schedulertest.Harness) {
			t := h.T
			rec := recordKind(h, "sample.step")
			inTx(t, h, func(ctx context.Context) error {
				for i := range tc.work {
					key := fmt.Sprintf("k%03d", i)
					if err := h.Sched.Schedule(ctx, h.Spec("sample.step", key, after(-time.Duration(tc.work-i)*time.Second))); err != nil {
						return err
					}
					if _, err := h.Outbox.Add(ctx, outboxMessage(h, "m"+key)); err != nil {
						return err
					}
				}
				return nil
			})

			// All ticks are released together at the same fake instant.
			var (
				start   = make(chan struct{})
				wg      sync.WaitGroup
				mu      sync.Mutex
				sums    []scheduler.TickSummary
				failure []error
			)
			for range tc.ticks {
				wg.Go(func() {
					<-start
					sum, err := h.Sched.Tick(t.Context())
					mu.Lock()
					defer mu.Unlock()
					if err != nil {
						failure = append(failure, err)
						return
					}
					sums = append(sums, sum)
				})
			}
			close(start)
			wg.Wait()
			if len(failure) != 0 {
				t.Fatalf("overlapping ticks failed: %v", failure)
			}

			// The summed counts equal the totals: nothing was run or sent twice, nothing was lost.
			var run, sent, failed, dead int
			for _, s := range sums {
				run += s.Jobs.Run
				sent += s.Messages.Sent
				failed += s.Jobs.Failed + s.Messages.Failed
				dead += s.Jobs.Dead + s.Messages.Dead
			}
			if run != tc.work || sent != tc.work || failed != 0 || dead != 0 {
				t.Errorf("summed counts: jobs run %d, messages sent %d, failures %d, dead %d; want %d, %d, 0, 0", run, sent, failed, dead, tc.work, tc.work)
			}
			if rec.Count() != tc.work {
				t.Errorf("handler ran %d times for %d jobs", rec.Count(), tc.work)
			}
			for i := range tc.work {
				key := fmt.Sprintf("k%03d", i)
				if rec.CountKey(key) != 1 || h.Effects(key) != 1 {
					t.Errorf("%s: handler runs %d, effects %d, want 1 and 1", key, rec.CountKey(key), h.Effects(key))
				}
				if h.Sender.Deliveries("m"+key) != 1 || len(callsFor(h, "m"+key)) != 1 {
					t.Errorf("m%s: deliveries %d, sender calls %d, want 1 and 1", key, h.Sender.Deliveries("m"+key), len(callsFor(h, "m"+key)))
				}
			}
			if n := h.Count("scheduled_jobs", "status = 'done'"); n != tc.work {
				t.Errorf("%d done jobs, want %d", n, tc.work)
			}
			if n := h.Count("outbox_messages", "status = 'sent'"); n != tc.work {
				t.Errorf("%d sent messages, want %d", n, tc.work)
			}
			if n := h.Count("tick_runs", "true"); n != tc.ticks {
				t.Errorf("%d tick records for %d ticks", n, tc.ticks)
			}
		})
	}
}
