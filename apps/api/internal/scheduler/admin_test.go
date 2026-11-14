package scheduler_test

// T053 - scheduler admin capabilities and status at service level (US6.4-6.9; FR-019-FR-021;
// SC-007). The HTTP layer is Phase B (after 003); these tests pin what it will call.
//
// Assumptions (contract: go-module-api.md and admin-scheduler.openapi.yaml):
//   - FailedFilter{State "retrying"|"dead"|"" , Kind string, Limit int, Cursor string}; an unknown
//     State is ErrInvalid.
//   - FailedJob, Page[FailedJob] and Status marshal to JSON exactly as the FailedJob /
//     FailedJobPage / SchedulerStatus schemas (snake_case keys, "items"), so the tests read them
//     through JSON and never depend on Go field types.
//   - RetryJob / CancelJob open their own transaction, write audit_log (actions
//     scheduled_job.retry / scheduled_job.cancel, entity scheduled_job) in it, store a non-empty
//     idemKey as after.idempotency_key, answer ErrNotFound for ids outside the org and
//     ErrConflict for jobs that are not dead, including a retry that would create a second active
//     row for the same key (the (org, kind, dedupe_key) unique index).

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler/schedulertest"
)

// failedJobKeys are the properties of the FailedJob schema; nothing else may be exposed.
var failedJobKeys = []string{"id", "kind", "subject", "dedupe_key", "state", "attempts", "max_attempts", "first_attempt_at",
	"last_attempt_at", "next_attempt_at", "expires_at", "last_error", "created_at"}

func toJSONMap(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s is not a JSON object: %v", raw, err)
	}
	return m
}

func listJobs(t *testing.T, h *schedulertest.Harness, org uuid.UUID, f scheduler.FailedFilter) []map[string]any {
	t.Helper()
	page, err := h.Sched.ListFailedJobs(t.Context(), org, f)
	if err != nil {
		t.Fatalf("ListFailedJobs(%+v): %v", f, err)
	}
	m := toJSONMap(t, page)
	raw, _ := json.Marshal(m["items"])
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil || (items == nil && m["items"] == nil) {
		t.Fatalf("page %v has no items array", m)
	}
	return items
}

func jsonTimeOf(t *testing.T, item map[string]any, key string) *time.Time {
	t.Helper()
	v, ok := item[key]
	if !ok || v == nil {
		return nil
	}
	s, isStr := v.(string)
	if !isStr {
		t.Fatalf("%s = %v, want an RFC 3339 string or null", key, v)
	}
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("%s = %q: %v", key, s, err)
	}
	return &ts
}

func auditJSON(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("audit json %s: %v", raw, err)
	}
	return m
}

// seedDead inserts a dead job with realistic attempt data, a payload holding a "secret" and a worker name.
func seedDead(h *schedulertest.Harness, org uuid.UUID, kind, key string) uuid.UUID {
	first, last := after(-2*time.Hour), after(-time.Hour)
	id := h.SeedJob(schedulertest.JobSeed{Org: org, Kind: kind, Key: key, Subject: "ipo:42", Status: "dead", Attempts: 8,
		FirstAttemptAt: &first, LastAttemptAt: &last, FinishedAt: &last, LastError: "push gateway timeout",
		Payload: `{"ipo_id": "42", "secret": "hunter2"}`})
	h.Exec(`UPDATE scheduled_jobs SET locked_by = 'host-1' WHERE id = $1`, id)
	return id
}

// seedRetrying inserts a pending job that already failed 3 times and waits for its next attempt.
func seedRetrying(h *schedulertest.Harness, kind, key string) uuid.UUID {
	first, last := after(-3*time.Hour), after(-30*time.Minute)
	return h.SeedJob(schedulertest.JobSeed{Kind: kind, Key: key, Attempts: 3, FirstAttemptAt: &first, LastAttemptAt: &last,
		LastError: "boom", RunAt: after(20 * time.Minute)})
}

func TestAdmin_ListShowsRetryingAndDeadJobsWithTheRequiredFieldsOnly(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("list", func(h *schedulertest.Harness) {
		t := h.T
		other := h.NewOrg("Other")
		deadID := seedDead(h, h.Org, "ipo.open", "ipo:42:open")
		seedRetrying(h, "ipo.close", "ipo:42:close")
		h.SeedJob(schedulertest.JobSeed{Kind: "ipo.open", Key: "fresh", RunAt: after(time.Hour)})
		for _, st := range []string{"done", "cancelled", "expired", "running"} {
			h.SeedJob(schedulertest.JobSeed{Kind: "ipo.open", Key: st, Status: st, Attempts: 1})
		}
		seedDead(h, other, "ipo.open", "foreign")

		items := listJobs(t, h, h.Org, scheduler.FailedFilter{})
		if len(items) != 2 {
			t.Fatalf("list returned %d items, want the retrying and the dead job only: %v", len(items), items)
		}
		retrying, dead := items[0], items[1] // newest last attempt first
		if retrying["dedupe_key"] != "ipo:42:close" || dead["dedupe_key"] != "ipo:42:open" {
			t.Fatalf("order = %v, %v; want the retrying job (30 min ago) before the dead one (1 h ago)", retrying["dedupe_key"], dead["dedupe_key"])
		}
		for _, it := range items {
			for _, req := range []string{"id", "kind", "subject", "dedupe_key", "state", "attempts", "max_attempts", "created_at"} {
				if v, ok := it[req]; !ok || v == nil {
					t.Errorf("%v: required field %q missing", it["dedupe_key"], req)
				}
			}
			for k := range it {
				if !slices.Contains(failedJobKeys, k) {
					t.Errorf("%v exposes %q, which is not in the FailedJob schema", it["dedupe_key"], k)
				}
			}
		}
		if dead["id"] != deadID.String() || dead["state"] != "dead" || dead["kind"] != "ipo.open" || dead["subject"] != "ipo:42" ||
			dead["attempts"] != float64(8) || dead["max_attempts"] != float64(8) || dead["last_error"] != "push gateway timeout" {
			t.Errorf("dead item = %v", dead)
		}
		if f, l := jsonTimeOf(t, dead, "first_attempt_at"), jsonTimeOf(t, dead, "last_attempt_at"); f == nil || !f.Equal(after(-2*time.Hour)) || l == nil || !l.Equal(after(-time.Hour)) {
			t.Errorf("dead attempt times = %v / %v", f, l)
		}
		if n := jsonTimeOf(t, dead, "next_attempt_at"); n != nil {
			t.Errorf("a dead job has no next attempt, got %v", n)
		}
		if retrying["state"] != "retrying" || retrying["attempts"] != float64(3) || retrying["last_error"] != "boom" {
			t.Errorf("retrying item = %v", retrying)
		}
		if n := jsonTimeOf(t, retrying, "next_attempt_at"); n == nil || !n.Equal(after(20*time.Minute)) {
			t.Errorf("retrying next_attempt_at = %v, want %v", n, after(20*time.Minute))
		}
		raw, _ := json.Marshal(items)
		for _, secret := range []string{"hunter2", "host-1", "lock_token", "payload"} {
			if strings.Contains(strings.ToLower(string(raw)), secret) {
				t.Errorf("the list exposes %q: %s", secret, raw)
			}
		}
		if foreign := listJobs(t, h, other, scheduler.FailedFilter{}); len(foreign) != 1 || foreign[0]["dedupe_key"] != "foreign" {
			t.Errorf("the other org sees %v, want only its own dead job (US6.9)", foreign)
		}
	})

	h.Run("filters_and_paging", func(h *schedulertest.Harness) {
		t := h.T
		seedDead(h, h.Org, "ipo.open", "d1")
		seedDead(h, h.Org, "ipo.close", "d2")
		seedRetrying(h, "ipo.open", "r1")
		keys := func(f scheduler.FailedFilter) []string {
			items := listJobs(t, h, h.Org, f)
			out := make([]string, 0, len(items))
			for _, it := range items {
				out = append(out, it["dedupe_key"].(string))
			}
			slices.Sort(out)
			return out
		}
		for _, tc := range []struct {
			name string
			f    scheduler.FailedFilter
			want []string
		}{
			{"both states", scheduler.FailedFilter{}, []string{"d1", "d2", "r1"}},
			{"dead", scheduler.FailedFilter{State: "dead"}, []string{"d1", "d2"}},
			{"retrying", scheduler.FailedFilter{State: "retrying"}, []string{"r1"}},
			{"kind", scheduler.FailedFilter{Kind: "ipo.close"}, []string{"d2"}},
			{"kind and state", scheduler.FailedFilter{Kind: "ipo.open", State: "dead"}, []string{"d1"}},
			{"no match", scheduler.FailedFilter{Kind: "ipo.unknown"}, nil},
		} {
			if got := keys(tc.f); !slices.Equal(got, tc.want) {
				t.Errorf("%s: keys = %v, want %v", tc.name, got, tc.want)
			}
		}
		if _, err := h.Sched.ListFailedJobs(t.Context(), h.Org, scheduler.FailedFilter{State: "bogus"}); !errors.Is(err, scheduler.ErrInvalid) {
			t.Errorf("unknown state: err = %v, want ErrInvalid", err)
		}

		// Keyset paging: limit 2 then the cursor gives the rest, no item twice.
		page1, err := h.Sched.ListFailedJobs(t.Context(), h.Org, scheduler.FailedFilter{Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		p1 := toJSONMap(t, page1)
		cursor, ok := p1["next_cursor"].(string)
		if !ok || cursor == "" || len(p1["items"].([]any)) != 2 {
			t.Fatalf("first page = %v, want 2 items and a next_cursor", p1)
		}
		page2, err := h.Sched.ListFailedJobs(t.Context(), h.Org, scheduler.FailedFilter{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		p2 := toJSONMap(t, page2)
		if items := p2["items"].([]any); len(items) != 1 || p2["next_cursor"] != nil {
			t.Fatalf("second page = %v, want the last item and no next_cursor", p2)
		}
	})
}

func TestAdmin_RetryRequeuesADeadJobWithFreshAttemptsAndAnAuditEntry(t *testing.T) {
	h := schedulertest.NewHarness(t)

	h.Run("retry_runs_on_the_next_tick", func(h *schedulertest.Harness) {
		t := h.T
		rec := recordKind(h, "ipo.open")
		id := seedDead(h, h.Org, "ipo.open", "ipo:42:open")
		alerted := after(-time.Hour)
		h.Exec(`UPDATE scheduled_jobs SET alerted_at = $1 WHERE id = $2`, alerted, id)

		fj, err := h.Sched.RetryJob(t.Context(), adminAct, h.Org, id, "idem-key-0001")
		if err != nil {
			t.Fatalf("RetryJob: %v", err)
		}
		item := toJSONMap(t, fj)
		if item["id"] != id.String() || item["state"] != "pending" || item["attempts"] != float64(0) {
			t.Errorf("returned item = %v, want the pending job with 0 attempts", item)
		}
		row := h.JobByID(id)
		wantStatus(t, row, "pending")
		if row.Attempts != 0 || !row.RunAt.Equal(start) || row.AlertedAt != nil || row.FinishedAt != nil || row.FirstAttemptAt != nil {
			t.Errorf("row attempts=%d run_at=%v alerted=%v finished=%v first=%v; want 0/now/nil/nil/nil", row.Attempts, row.RunAt, row.AlertedAt, row.FinishedAt, row.FirstAttemptAt)
		}

		audits := h.Audit(id)
		if len(audits) != 1 {
			t.Fatalf("audit rows = %d, want 1 (SC-007)", len(audits))
		}
		a := audits[0]
		if a.Org != h.Org || a.Entity != "scheduled_job" || a.Action != "scheduled_job.retry" || a.ActorKind != "admin" ||
			a.ActorID == nil || *a.ActorID != adminAct.ID || a.EntityID == nil || *a.EntityID != id {
			t.Errorf("audit identity = %+v", a)
		}
		before, aft := auditJSON(t, a.Before), auditJSON(t, a.After)
		if before["status"] != "dead" || before["attempts"] != float64(8) || aft["status"] != "pending" || aft["attempts"] != float64(0) {
			t.Errorf("audit before/after = %v / %v", before, aft)
		}
		if aft["idempotency_key"] != "idem-key-0001" {
			t.Errorf("audit after = %v, want the idempotency key", aft)
		}
		if strings.Contains(string(a.Before)+string(a.After), "hunter2") {
			t.Error("the audit entry copies the job payload")
		}

		h.Tick()
		if rec.Count() != 1 {
			t.Fatalf("the retried job ran %d times on the next tick, want 1", rec.Count())
		}
		row = h.JobByID(id)
		wantStatus(t, row, "done")
		if row.Attempts != 1 {
			t.Errorf("attempts after the retried run = %d, want 1 (fresh count)", row.Attempts)
		}
	})

	h.Run("without_an_idempotency_key_none_is_stored", func(h *schedulertest.Harness) {
		t := h.T
		recordKind(h, "ipo.open")
		id := seedDead(h, h.Org, "ipo.open", "k")
		if _, err := h.Sched.RetryJob(t.Context(), adminAct, h.Org, id, ""); err != nil {
			t.Fatal(err)
		}
		if aft := auditJSON(t, h.Audit(id)[0].After); aft["idempotency_key"] != nil {
			t.Errorf("audit after = %v, want no idempotency_key", aft)
		}
	})

	h.Run("a_retried_job_whose_expiry_passed_still_expires", func(h *schedulertest.Harness) {
		t := h.T
		rec := recordKind(h, "ipo.remind")
		id := seedDead(h, h.Org, "ipo.remind", "slot-1")
		h.Exec(`UPDATE scheduled_jobs SET expires_at = $1 WHERE id = $2`, after(-time.Minute), id)
		if _, err := h.Sched.RetryJob(t.Context(), adminAct, h.Org, id, ""); err != nil {
			t.Fatal(err)
		}
		sum := h.Tick()
		if rec.Count() != 0 || sum.Jobs.Expired != 1 {
			t.Fatalf("runs=%d summary %+v, want no run and the job expired (US6.6)", rec.Count(), sum.Jobs)
		}
		wantStatus(t, h.JobByID(id), "expired")
	})
}

func TestAdmin_CancelEndsADeadJobForGoodWithAnAuditEntry(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("cancel", func(h *schedulertest.Harness) {
		t := h.T
		rec := recordKind(h, "ipo.open")
		id := seedDead(h, h.Org, "ipo.open", "k")
		fj, err := h.Sched.CancelJob(t.Context(), adminAct, h.Org, id, "idem-key-0002")
		if err != nil {
			t.Fatalf("CancelJob: %v", err)
		}
		if item := toJSONMap(t, fj); item["state"] != "cancelled" || item["id"] != id.String() {
			t.Errorf("returned item = %v, want the cancelled job", item)
		}
		row := h.JobByID(id)
		wantStatus(t, row, "cancelled")
		wantTime(t, "finished_at", row.FinishedAt, start)
		audits := h.Audit(id)
		if len(audits) != 1 || audits[0].Action != "scheduled_job.cancel" || audits[0].Entity != "scheduled_job" || audits[0].Org != h.Org {
			t.Fatalf("audit = %+v, want one scheduled_job.cancel entry", audits)
		}
		if before, aft := auditJSON(t, audits[0].Before), auditJSON(t, audits[0].After); before["status"] != "dead" || aft["status"] != "cancelled" || aft["idempotency_key"] != "idem-key-0002" {
			t.Errorf("audit before/after = %v / %v", before, aft)
		}
		h.Clock.Advance(48 * time.Hour)
		h.Tick()
		if rec.Count() != 0 {
			t.Fatal("a cancelled job ran")
		}
		if items := listJobs(t, h, h.Org, scheduler.FailedFilter{}); len(items) != 0 {
			t.Errorf("the cancelled job is still listed as failed work: %v", items)
		}
	})
}

func TestAdmin_RetryAndCancelRejectOtherOrgsAndJobsThatAreNotDead(t *testing.T) {
	h := schedulertest.NewHarness(t)
	type action struct {
		name string
		call func(h *schedulertest.Harness, org, id uuid.UUID) error
	}
	actions := []action{
		{"retry", func(h *schedulertest.Harness, org, id uuid.UUID) error {
			_, err := h.Sched.RetryJob(h.T.Context(), adminAct, org, id, "")
			return err
		}},
		{"cancel", func(h *schedulertest.Harness, org, id uuid.UUID) error {
			_, err := h.Sched.CancelJob(h.T.Context(), adminAct, org, id, "")
			return err
		}},
	}

	h.Run("other_org_and_unknown_ids_are_not_found", func(h *schedulertest.Harness) {
		t := h.T
		other := h.NewOrg("Other")
		foreign := seedDead(h, other, "ipo.open", "k")
		for _, a := range actions {
			if err := a.call(h, h.Org, foreign); !errors.Is(err, scheduler.ErrNotFound) {
				t.Errorf("%s of another org's job: err = %v, want ErrNotFound (US6.9)", a.name, err)
			}
			if err := a.call(h, h.Org, uuid.New()); !errors.Is(err, scheduler.ErrNotFound) {
				t.Errorf("%s of an unknown id: err = %v, want ErrNotFound", a.name, err)
			}
		}
		wantStatus(t, h.JobByID(foreign), "dead")
		if n := h.Count("audit_log", "true"); n != 0 {
			t.Errorf("audit rows = %d after rejected actions, want 0", n)
		}
	})

	h.Run("jobs_that_are_not_dead_are_a_conflict", func(h *schedulertest.Harness) {
		t := h.T
		ids := map[string]uuid.UUID{
			"retrying": seedRetrying(h, "ipo.open", "r"),
			"pending":  h.SeedJob(schedulertest.JobSeed{Kind: "ipo.open", Key: "p", RunAt: after(time.Hour)}),
			"running":  h.SeedJob(schedulertest.JobSeed{Kind: "ipo.open", Key: "run", Status: "running", Attempts: 1}),
		}
		for _, st := range []string{"done", "cancelled", "expired"} {
			ids[st] = h.SeedJob(schedulertest.JobSeed{Kind: "ipo.open", Key: st, Status: st})
		}
		for st, id := range ids {
			for _, a := range actions {
				if err := a.call(h, h.Org, id); !errors.Is(err, scheduler.ErrConflict) {
					t.Errorf("%s of a %s job: err = %v, want ErrConflict", a.name, st, err)
				}
			}
			if got := h.JobByID(id).Status; got != map[string]string{"retrying": "pending"}[st] && got != st {
				t.Errorf("%s job changed to %s by a rejected action", st, got)
			}
		}
		if n := h.Count("audit_log", "true"); n != 0 {
			t.Errorf("audit rows = %d after rejected actions, want 0", n)
		}
	})

	h.Run("retry_while_a_newer_job_for_the_same_key_is_active_is_a_conflict", func(h *schedulertest.Harness) {
		t := h.T
		recordKind(h, "ipo.open")
		dead := seedDead(h, h.Org, "ipo.open", "ipo:42:open")
		h.Schedule(h.Spec("ipo.open", "ipo:42:open", after(time.Hour))) // the owning feature rescheduled meanwhile
		_, err := h.Sched.RetryJob(t.Context(), adminAct, h.Org, dead, "")
		if !errors.Is(err, scheduler.ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict (one active job per key)", err)
		}
		wantStatus(t, h.JobByID(dead), "dead")
		if n := h.Count("audit_log", "true"); n != 0 {
			t.Errorf("audit rows = %d, want 0", n)
		}
		if _, err := h.Sched.CancelJob(t.Context(), adminAct, h.Org, dead, ""); err != nil {
			t.Fatalf("cancelling the old dead job must still work: %v", err)
		}
	})
}

func TestAdmin_StatusCountsFailedWorkOfTheOrg(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("counts", func(h *schedulertest.Harness) {
		t := h.T
		other := h.NewOrg("Other")
		seedDead(h, h.Org, "ipo.open", "d1")
		seedDead(h, h.Org, "ipo.open", "d2")
		seedRetrying(h, "ipo.open", "r1")
		seedDead(h, other, "ipo.open", "foreign")
		h.SeedMessage(schedulertest.MessageSeed{Key: "md", Status: "dead", Attempts: 8})
		h.SeedMessage(schedulertest.MessageSeed{Key: "mr", Attempts: 2})
		h.SeedMessage(schedulertest.MessageSeed{Key: "mr2", Attempts: 1})
		h.SeedMessage(schedulertest.MessageSeed{Org: other, Key: "foreign", Status: "dead", Attempts: 8})

		st, err := h.Sched.Status(t.Context(), h.Org)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		m := toJSONMap(t, st)
		for k, want := range map[string]float64{"dead_jobs": 2, "retrying_jobs": 1, "dead_messages": 1, "retrying_messages": 2} {
			if m[k] != want {
				t.Errorf("%s = %v, want %v (status %v)", k, m[k], want, m)
			}
		}
	})
}

// US6.8, FR-021: the admin sees when the last tick started and whether the scheduler is overdue
// (more than 15 minutes since the last tick started, or no tick at all).
func TestAdmin_StatusIsOverdueMoreThan15MinutesAfterTheLastTick(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("status", func(h *schedulertest.Harness) {
		t := h.T
		never := status(t, h)
		if never["overdue"] != true || never["last_tick_started_at"] != nil || never["overdue_after_seconds"] != float64(900) {
			t.Fatalf("before any tick: %v, want overdue, no last tick, threshold 900 s", never)
		}

		h.Tick()
		for _, tc := range []struct {
			after   time.Duration
			overdue bool
		}{
			{0, false},
			{14*time.Minute + 59*time.Second, false},
			{15 * time.Minute, false}, // "more than 15 minutes"
			{15*time.Minute + time.Second, true},
			{3 * time.Hour, true},
		} {
			h.Clock.Set(after(tc.after))
			st := status(t, h)
			if st["overdue"] != tc.overdue {
				t.Errorf("%v after the last tick: overdue = %v, want %v", tc.after, st["overdue"], tc.overdue)
			}
			if ts := jsonTimeOf(t, st, "last_tick_started_at"); ts == nil || !ts.Equal(start) || st["last_tick_status"] != "ok" {
				t.Errorf("last_tick_started_at=%v last_tick_status=%v, want %v and ok", st["last_tick_started_at"], st["last_tick_status"], start)
			}
			if ts := jsonTimeOf(t, st, "last_tick_finished_at"); ts == nil {
				t.Errorf("last_tick_finished_at missing for a finished tick: %v", st)
			}
		}
		h.Tick() // a new tick clears the warning
		if st := status(t, h); st["overdue"] != false {
			t.Errorf("overdue = %v right after a tick, want false", st["overdue"])
		}
	})

	h.Run("only_the_most_recent_tick_counts", func(h *schedulertest.Harness) {
		t := h.T
		h.SeedTickRun(after(-2 * time.Hour))
		h.SeedTickRun(after(-10 * time.Minute))
		h.SeedTickRun(after(-time.Hour))
		st := status(t, h)
		if st["overdue"] != false {
			t.Errorf("status %v, want not overdue: the latest tick started 10 minutes ago", st)
		}
	})
}

// status returns Status of the harness org as SchedulerStatus JSON.
func status(t *testing.T, h *schedulertest.Harness) map[string]any {
	t.Helper()
	st, err := h.Sched.Status(t.Context(), h.Org)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	return toJSONMap(t, st)
}
