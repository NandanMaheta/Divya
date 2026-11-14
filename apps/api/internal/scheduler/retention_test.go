package scheduler_test

// T051 - the built-in scheduler.retention kind (FR-035, FR-015; data-model section 7): recurring,
// daily at 03:00 IST, one row per org; purges done/cancelled/expired jobs and sent/withdrawn/expired
// messages finished more than 30 days ago, and tick_runs older than 30 days; dead items stay until
// an admin acts; a long outage runs it once.

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler/schedulertest"
)

const retentionDays = 30 * 24 * time.Hour

func TestRetention_IsARecurringDailyJobAt0300ISTPerOrg(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("rows", func(h *schedulertest.Harness) {
		t := h.T
		if scheduler.RetentionKind != "scheduler.retention" {
			t.Fatalf("RetentionKind = %q, want scheduler.retention", scheduler.RetentionKind)
		}
		opts := scheduler.RetentionOptions()
		if opts.Recurring == nil {
			t.Fatal("RetentionOptions().Recurring is nil: the kind must be recurring")
		}
		for _, tc := range []struct{ now, want time.Time }{
			{start, schedulertest.Nov(15, 3, 0)},                        // Sat 10:00 -> Sun 03:00
			{schedulertest.Nov(15, 2, 59), schedulertest.Nov(15, 3, 0)}, // just before
			{schedulertest.Nov(15, 3, 0), schedulertest.Nov(16, 3, 0)},  // exactly at: the next day
			{schedulertest.Nov(30, 23, 0), istdate.Date{Year: 2026, Month: time.December, Day: 1}.At(3, 0)},
		} {
			if got := opts.Recurring(tc.now); !got.Equal(tc.want) {
				t.Errorf("Recurring(%v) = %v, want %v", tc.now, got, tc.want)
			}
		}

		h.RegisterRetention()
		other := h.NewOrg("Other")
		h.Tick()
		for _, org := range []uuid.UUID{h.Org, other} {
			row := h.JobOf(org, scheduler.RetentionKind, "recurring")
			wantStatus(t, row, "pending")
			if !row.Recurring || !row.RunAt.Equal(schedulertest.Nov(15, 3, 0)) {
				t.Errorf("org %v retention row recurring=%v run_at=%v, want recurring at Sun 15 Nov 03:00 IST", org, row.Recurring, row.RunAt)
			}
		}
	})
}

func TestRetention_PurgesFinishedWorkOlderThan30DaysAndKeepsDeadItems(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("purge", func(h *schedulertest.Harness) {
		t := h.T
		h.RegisterRetention()
		other := h.NewOrg("Other")
		h.Tick() // arms the retention rows for Sun 15 Nov 03:00 IST

		runAt := schedulertest.Nov(15, 3, 0)
		old, recent := runAt.Add(-retentionDays-time.Minute), runAt.Add(-retentionDays+time.Hour)
		var gone, kept []uuid.UUID
		for _, org := range []uuid.UUID{h.Org, other} {
			for _, st := range []string{"done", "cancelled", "expired"} {
				gone = append(gone, h.SeedJob(schedulertest.JobSeed{Org: org, Kind: "sample.step", Key: "old-" + st, Status: st, FinishedAt: &old}))
				kept = append(kept, h.SeedJob(schedulertest.JobSeed{Org: org, Kind: "sample.step", Key: "recent-" + st, Status: st, FinishedAt: &recent}))
			}
			kept = append(kept,
				h.SeedJob(schedulertest.JobSeed{Org: org, Kind: "sample.step", Key: "old-dead", Status: "dead", Attempts: 8, FinishedAt: &old}),
				h.SeedJob(schedulertest.JobSeed{Org: org, Kind: "sample.step", Key: "old-pending", RunAt: runAt.Add(24 * time.Hour)}))
		}
		var goneMsgs, keptMsgs []uuid.UUID
		for _, org := range []uuid.UUID{h.Org, other} {
			for _, st := range []string{"sent", "withdrawn", "expired"} {
				goneMsgs = append(goneMsgs, h.SeedMessage(schedulertest.MessageSeed{Org: org, Key: "old-" + st, Status: st, FinishedAt: &old}))
				keptMsgs = append(keptMsgs, h.SeedMessage(schedulertest.MessageSeed{Org: org, Key: "recent-" + st, Status: st, FinishedAt: &recent}))
			}
			keptMsgs = append(keptMsgs, h.SeedMessage(schedulertest.MessageSeed{Org: org, Key: "old-dead", Status: "dead", Attempts: 8, FinishedAt: &old}))
		}
		h.SeedTickRun(old)
		h.SeedTickRun(recent)

		h.Clock.Set(runAt)
		sum := h.Tick()
		if sum.Jobs.Run != 2 {
			t.Fatalf("summary %+v, want the two retention jobs (one per org) run", sum.Jobs)
		}
		for _, id := range gone {
			if n := h.Count("scheduled_jobs", "id = $1", id); n != 0 {
				t.Errorf("finished job %v older than 30 days was kept", id)
			}
		}
		for _, id := range kept {
			if n := h.Count("scheduled_jobs", "id = $1", id); n != 1 {
				t.Errorf("job %v (recent, dead or active) was purged", id)
			}
		}
		for _, id := range goneMsgs {
			if n := h.Count("outbox_messages", "id = $1", id); n != 0 {
				t.Errorf("finished message %v older than 30 days was kept", id)
			}
		}
		for _, id := range keptMsgs {
			if n := h.Count("outbox_messages", "id = $1", id); n != 1 {
				t.Errorf("message %v (recent or dead) was purged", id)
			}
		}
		if n := h.Count("tick_runs", "started_at < $1", runAt.Add(-retentionDays)); n != 0 {
			t.Errorf("%d tick_runs older than 30 days kept", n)
		}
		if n := h.Count("tick_runs", "started_at = $1", recent); n != 1 {
			t.Error("a tick_runs row younger than 30 days was purged")
		}
		for _, org := range []uuid.UUID{h.Org, other} {
			row := h.JobOf(org, scheduler.RetentionKind, "recurring")
			wantStatus(t, row, "pending")
			if !row.RunAt.Equal(schedulertest.Nov(16, 3, 0)) {
				t.Errorf("retention re-armed for %v, want Mon 16 Nov 03:00 IST", row.RunAt)
			}
		}
	})
}

func TestRetention_RunsOnceAfterAnOutage(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("catch_up", func(h *schedulertest.Harness) {
		t := h.T
		h.RegisterRetention()
		h.Tick()
		h.Clock.Set(schedulertest.Nov(19, 14, 0)) // four 03:00 occurrences missed
		if sum := h.Tick(); sum.Jobs.Run != 1 {
			t.Fatalf("summary %+v, want retention to run once", sum.Jobs)
		}
		if sum := h.Tick(); sum.Jobs.Run != 0 {
			t.Fatalf("second tick at the same instant ran %d jobs, want 0", sum.Jobs.Run)
		}
		row := h.Job(scheduler.RetentionKind, "recurring")
		if !row.RunAt.Equal(schedulertest.Nov(20, 3, 0)) || row.Attempts != 0 {
			t.Fatalf("after catch-up run_at=%v attempts=%d, want Fri 20 Nov 03:00 IST and a fresh count", row.RunAt, row.Attempts)
		}
	})
}
