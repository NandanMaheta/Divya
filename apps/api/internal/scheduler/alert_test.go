package scheduler_test

// T049 - the grouped dead-item alert (contracts/outbox-events.md, K8, FR-019): after the jobs and
// messages phases, one transaction selects every unalerted dead job and message, records one
// admin.jobs_dead message per organisation and marks the rows alerted. A failed sweep marks
// nothing and the next tick retries; admin retry clears alerted_at so a second death alerts again.

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db/dbgen"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler/schedulertest"
)

const alertTopic = "admin.jobs_dead"

// alertPayload is the admin.jobs_dead payload schema.
type alertPayload struct {
	TickID       string   `json:"tick_id"`
	JobsDead     int      `json:"jobs_dead"`
	MessagesDead int      `json:"messages_dead"`
	Kinds        []string `json:"kinds"`
	Topics       []string `json:"topics"`
}

// alerts returns the admin.jobs_dead messages of one org, oldest first.
func alerts(h *schedulertest.Harness, org uuid.UUID) []schedulertest.MsgRow {
	return h.Messages("org_id = $1 AND topic = $2", org, alertTopic)
}

// decodeAlert decodes the payload strictly: the schema allows no other properties.
func decodeAlert(t *testing.T, raw []byte) alertPayload {
	t.Helper()
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("alert payload %s: %v", raw, err)
	}
	for k := range keys {
		if !slices.Contains([]string{"tick_id", "jobs_dead", "messages_dead", "kinds", "topics"}, k) {
			t.Errorf("alert payload has extra property %q (no error texts, ids or secrets allowed): %s", k, raw)
		}
	}
	for _, k := range []string{"tick_id", "jobs_dead", "messages_dead", "kinds", "topics"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("alert payload misses required %q: %s", k, raw)
		}
	}
	var p alertPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func deadJob(h *schedulertest.Harness, org uuid.UUID, kind, key string) uuid.UUID {
	return h.SeedJob(schedulertest.JobSeed{Org: org, Kind: kind, Key: key, Status: "dead", Attempts: 8, LastError: "secret-ish failure text"})
}

func deadMessage(h *schedulertest.Harness, org uuid.UUID, topic, key string) uuid.UUID {
	return h.SeedMessage(schedulertest.MessageSeed{Org: org, Topic: topic, Key: key, Status: "dead", Attempts: 8})
}

func TestAlert_OneGroupedAlertPerOrgPerTickWithTheContractPayload(t *testing.T) {
	h := schedulertest.NewHarness(t)

	h.Run("payload_fields_and_marking", func(h *schedulertest.Harness) {
		j1 := deadJob(h, h.Org, "sample.open", "a")
		j2 := deadJob(h, h.Org, "ipo.close", "b")
		j3 := deadJob(h, h.Org, "sample.open", "c")
		m1 := deadMessage(h, h.Org, "task.reminder", "m1")
		h.Clock.Set(after(time.Minute))
		sum := h.Tick()

		got := alerts(h, h.Org)
		if len(got) != 1 {
			t.Fatalf("admin.jobs_dead messages = %d, want exactly 1 for the org", len(got))
		}
		a := got[0]
		if a.Subject != "scheduler:dead" || a.DedupeKey != "jobs_dead:"+sum.TickID.String() || a.RecipientKind != "admin" || a.RecipientID != nil {
			t.Errorf("alert subject=%q key=%q recipient=%s/%v, want scheduler:dead, jobs_dead:<tick id>, every admin", a.Subject, a.DedupeKey, a.RecipientKind, a.RecipientID)
		}
		if !a.RespectsQuietHours || a.ExpiresAt != nil {
			t.Errorf("respects_quiet_hours=%v expires_at=%v, want true and none", a.RespectsQuietHours, a.ExpiresAt)
		}
		p := decodeAlert(t, a.Payload)
		if p.TickID != sum.TickID.String() || p.JobsDead != 3 || p.MessagesDead != 1 {
			t.Errorf("payload = %+v, want tick %v, 3 dead jobs, 1 dead message", p, sum.TickID)
		}
		if !slices.Equal(p.Kinds, []string{"ipo.close", "sample.open"}) || !slices.Equal(p.Topics, []string{"task.reminder"}) {
			t.Errorf("kinds=%v topics=%v, want distinct sorted values", p.Kinds, p.Topics)
		}
		for _, id := range []uuid.UUID{j1, j2, j3} {
			wantTime(t, "job alerted_at", h.JobByID(id).AlertedAt, after(time.Minute))
		}
		if m := h.Messages("id = $1", m1); len(m) != 1 || m[0].AlertedAt == nil || !m[0].AlertedAt.Equal(after(time.Minute)) {
			t.Errorf("dead message alerted_at not set to the tick instant: %+v", m)
		}
	})

	h.Run("an_alert_is_not_repeated_on_the_next_tick", func(h *schedulertest.Harness) {
		deadJob(h, h.Org, "sample.open", "a")
		h.Tick()
		h.Clock.Advance(5 * time.Minute)
		h.Tick()
		h.Clock.Advance(5 * time.Minute)
		h.Tick()
		if n := len(alerts(h, h.Org)); n != 1 {
			t.Fatalf("admin.jobs_dead messages = %d after three ticks, want 1", n)
		}
	})

	h.Run("no_dead_items_no_alert", func(h *schedulertest.Harness) {
		h.SeedJob(schedulertest.JobSeed{Kind: "sample.open", Key: "done", Status: "done"})
		h.SeedJob(schedulertest.JobSeed{Kind: "sample.open", Key: "retrying", Attempts: 3, LastError: "x", RunAt: after(time.Hour)})
		h.Tick()
		if n := h.Count("outbox_messages", "topic = $1", alertTopic); n != 0 {
			t.Fatalf("alerts = %d without dead items, want 0", n)
		}
	})

	h.Run("each_org_gets_its_own_alert_with_its_own_counts", func(h *schedulertest.Harness) {
		other := h.NewOrg("Other")
		deadJob(h, h.Org, "sample.open", "a")
		deadJob(h, other, "sample.close", "b")
		deadJob(h, other, "sample.close", "c")
		deadMessage(h, other, "task.reminder", "m")
		h.Tick()
		mine, theirs := alerts(h, h.Org), alerts(h, other)
		if len(mine) != 1 || len(theirs) != 1 {
			t.Fatalf("alerts: harness org %d, other org %d; want 1 each", len(mine), len(theirs))
		}
		pm, pt := decodeAlert(t, mine[0].Payload), decodeAlert(t, theirs[0].Payload)
		if pm.JobsDead != 1 || pm.MessagesDead != 0 || !slices.Equal(pm.Kinds, []string{"sample.open"}) || len(pm.Topics) != 0 {
			t.Errorf("harness org payload = %+v", pm)
		}
		if pt.JobsDead != 2 || pt.MessagesDead != 1 || !slices.Equal(pt.Kinds, []string{"sample.close"}) || !slices.Equal(pt.Topics, []string{"task.reminder"}) {
			t.Errorf("other org payload = %+v", pt)
		}
		if mine[0].DedupeKey != theirs[0].DedupeKey {
			t.Errorf("dedupe keys %q and %q, want the same jobs_dead:<tick id> in each org", mine[0].DedupeKey, theirs[0].DedupeKey)
		}
	})

	h.Run("kinds_list_is_capped_at_10_while_counts_stay_exact", func(h *schedulertest.Harness) {
		for i := range 12 {
			deadJob(h, h.Org, "sample.k"+strconv.Itoa(10+i), "x")
		}
		h.Tick()
		got := alerts(h, h.Org)
		if len(got) != 1 {
			t.Fatalf("alerts = %d, want 1", len(got))
		}
		p := decodeAlert(t, got[0].Payload)
		if p.JobsDead != 12 || len(p.Kinds) > 10 || !slices.IsSorted(p.Kinds) {
			t.Fatalf("payload = %+v, want 12 dead jobs and at most 10 sorted kinds", p)
		}
	})
}

func TestAlert_AdminTopicsNeverTriggerAnAlert(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("dead_alert_message", func(h *schedulertest.Harness) {
		id := deadMessage(h, h.Org, alertTopic, "jobs_dead:old")
		h.Tick()
		if n := h.Count("outbox_messages", "topic = $1", alertTopic); n != 1 {
			t.Fatalf("admin.jobs_dead messages = %d, want only the old dead one (an undeliverable alert must not alert)", n)
		}
		if m := h.Messages("id = $1", id); m[0].AlertedAt == nil {
			t.Error("the dead admin.* message was not marked alerted, so every tick would look at it again")
		}
	})

	h.Run("undeliverable_alerts_die_without_a_cascade", func(h *schedulertest.Harness) {
		deadJob(h, h.Org, "sample.open", "a")
		h.Tick()       // records one alert; no sender is registered for admin.jobs_dead until 014
		for range 80 { // 10 days of 3-hourly ticks: enough for every attempt outside quiet hours
			h.Clock.Advance(3 * time.Hour)
			h.Tick()
			if a := alerts(h, h.Org); len(a) == 1 && a[0].Status == "dead" {
				break
			}
		}
		h.Clock.Advance(3 * time.Hour)
		h.Tick()
		got := alerts(h, h.Org)
		if len(got) != 1 || got[0].Status != "dead" {
			t.Fatalf("alerts = %+v, want the single alert to end dead and no new alert about it", got)
		}
	})
}

func TestAlert_AFailedSweepMarksNothingAndIsRetriedNextTick(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("failing_insert", func(h *schedulertest.Harness) {
		// Make recording the alert fail inside the sweep transaction, as a database error would.
		h.Exec(`CREATE FUNCTION fail_alert() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN IF NEW.topic = 'admin.jobs_dead' THEN RAISE EXCEPTION 'injected alert failure'; END IF; RETURN NEW; END $$`)
		h.Exec(`CREATE TRIGGER fail_alert BEFORE INSERT ON outbox_messages FOR EACH ROW EXECUTE FUNCTION fail_alert()`)
		dropped := false
		drop := func() {
			if !dropped {
				dropped = true
				h.Exec(`DROP TRIGGER fail_alert ON outbox_messages`)
				h.Exec(`DROP FUNCTION fail_alert()`)
			}
		}
		defer drop()

		job := deadJob(h, h.Org, "sample.open", "a")
		msg := deadMessage(h, h.Org, "task.reminder", "m")
		ran := recordKind(h, "sample.step")
		h.Schedule(h.Spec("sample.step", "ok", after(-time.Minute)))

		_, _ = h.Sched.Tick(t.Context()) // the tick may report the failure; the jobs phase still counts
		if ran.Count() != 1 {
			t.Fatalf("the jobs phase did not run (runs=%d)", ran.Count())
		}
		if a := h.JobByID(job).AlertedAt; a != nil {
			t.Fatalf("dead job marked alerted at %v although the alert was never recorded", a)
		}
		if m := h.Messages("id = $1", msg); m[0].AlertedAt != nil {
			t.Fatal("dead message marked alerted although the alert was never recorded")
		}

		drop()
		h.Clock.Advance(5 * time.Minute)
		h.Tick()
		got := alerts(h, h.Org)
		if len(got) != 1 {
			t.Fatalf("alerts after recovery = %d, want 1", len(got))
		}
		if p := decodeAlert(t, got[0].Payload); p.JobsDead != 1 || p.MessagesDead != 1 {
			t.Fatalf("payload = %+v, want the items of the failed sweep", p)
		}
		if h.JobByID(job).AlertedAt == nil {
			t.Fatal("dead job not marked alerted after the successful sweep")
		}
	})
}

func TestAlert_AnItemRetriedByTheAdminThatDiesAgainAlertsAgain(t *testing.T) {
	h := schedulertest.NewHarness(t)
	h.Run("retry_then_die", func(h *schedulertest.Harness) {
		h.HandleWith("sample.once", func(context.Context, scheduler.Job) error { return errBoom }, scheduler.KindOptions{MaxAttempts: 1})
		alertedAt := after(-time.Hour)
		id := h.SeedJob(schedulertest.JobSeed{Kind: "sample.once", Key: "k", Status: "dead", Attempts: 1, MaxAttempts: 1, AlertedAt: &alertedAt})
		h.Tick()
		if n := len(alerts(h, h.Org)); n != 0 {
			t.Fatalf("alerts = %d for an already alerted dead job, want 0", n)
		}

		// The admin retry (same query the service uses) clears alerted_at and requeues the job.
		h.Clock.Advance(5 * time.Minute)
		if _, err := dbgen.New(h.Pool).AdminRetryJob(t.Context(), dbgen.AdminRetryJobParams{Now: db.Timestamptz(h.Clock.Now()), ID: id, OrgID: h.Org}); err != nil {
			t.Fatal(err)
		}
		sum := h.Tick() // runs, fails, dies again, and is swept in the same tick
		if sum.Jobs.Dead != 1 {
			t.Fatalf("summary %+v, want the retried job dead again", sum.Jobs)
		}
		got := alerts(h, h.Org)
		if len(got) != 1 {
			t.Fatalf("alerts = %d after the job died again, want 1", len(got))
		}
		if p := decodeAlert(t, got[0].Payload); p.JobsDead != 1 || p.TickID != sum.TickID.String() {
			t.Fatalf("payload = %+v, want this tick and one dead job", p)
		}
		wantTime(t, "alerted_at", h.JobByID(id).AlertedAt, h.Clock.Now())
	})
}
