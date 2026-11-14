package ipos

import (
	"errors"
	"testing"
	"time"
)

// StatusChanged (FR-037): exactly once per real change, from override, step and MarkSettled.

func TestSignals_BR_IPO_03_StatusChangedExactlyOnce(t *testing.T) {
	e := newSvcEnvJobs(t)
	e.setClock(10, 9, 0)
	r := e.listen()
	i := e.create(acme())

	// step: UPCOMING -> OPEN at 11 Nov 10:00
	e.tickAt(11, 10, 0)
	if len(r.Status) != 1 {
		t.Fatalf("signals after the open step = %d, want 1", len(r.Status))
	}
	ev := r.Status[0]
	if ev.From != StatusUpcoming || ev.To != StatusOpen || ev.Source != SourceSchedule || ev.Actor.Kind != "system" || ev.Actor.ID.String() != "00000000-0000-0000-0000-000000000000" {
		t.Fatalf("step signal wrong: %+v", ev)
	}
	if ev.ScheduledFor == nil || !ev.ScheduledFor.Equal(e.at(11, 10, 0)) || ev.IPOID != i.ID || ev.OrgID != e.org {
		t.Fatalf("scheduled_for / ids wrong: %+v", ev)
	}
	if len(r.StatusRows) != 1 || r.StatusRows[0].Status != StatusOpen {
		t.Fatalf("listener must see the new row inside the transaction: %+v", r.StatusRows)
	}

	// override: OPEN -> ALLOTMENT_OUT (non-adjacent, admin)
	cur := e.currentID(i.ID)
	e.override(cur, StatusAllotmentOut, "registrar early")
	if len(r.Status) != 2 {
		t.Fatalf("signals after override = %d", len(r.Status))
	}
	ev = r.Status[1]
	if ev.From != StatusOpen || ev.To != StatusAllotmentOut || ev.Source != SourceAdmin || ev.Actor != e.admin || ev.ScheduledFor != nil {
		t.Fatalf("override signal wrong: %+v", ev)
	}

	// later steps are no-ops: no signal
	e.tickAt(18, 10, 0) // listing step would move to LISTED: a real change
	if len(r.Status) != 3 || r.Status[2].To != StatusListed {
		t.Fatalf("listing step: %+v", r.Status)
	}
	e.tickAt(19, 10, 0)
	if len(r.Status) != 3 {
		t.Fatalf("a tick with nothing due must send nothing")
	}

	// MarkSettled
	if err := e.svc.MarkSettled(e.ctx(), e.org, i.ID); err != nil {
		t.Fatalf("MarkSettled: %v", err)
	}
	ev = r.Status[3]
	if ev.From != StatusListed || ev.To != StatusSettled || ev.Source != SourceSettlement || ev.Actor.Kind != "system" {
		t.Fatalf("settle signal wrong: %+v", ev)
	}
	if err := e.svc.MarkSettled(e.ctx(), e.org, i.ID); err != nil || len(r.Status) != 4 {
		t.Fatalf("second MarkSettled must be a silent no-op: err=%v signals=%d", err, len(r.Status))
	}
}

func TestSignals_BR_IPO_03_ForwardOverrideMakesStepSilent(t *testing.T) {
	e := newSvcEnvJobs(t)
	e.setClock(13, 19, 0)
	i := e.create(acme())
	r := e.listen()
	e.tickAt(13, 19, 0) // open and close steps catch up
	n := len(r.Status)
	e.override(e.currentID(i.ID), StatusAllotmentOut, "early")
	e.tickAt(16, 10, 0) // allotment step: already there, no change
	if len(r.Status) != n+1 {
		t.Fatalf("signals = %d, want %d (override only; the 16 Nov step is a no-op, US6.1)", len(r.Status), n+1)
	}
}

func TestSignals_BR_IPO_03_StatusFailureRollsBackOverride(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(acme())
	r := e.listen()
	r.failStatus(errors.New("tasks failed"))
	n := e.auditCount(i.ID)
	_, err := e.svc.OverrideStatus(e.ctx(), e.admin, e.org, i.ID, i.Version, StatusClosed, "why", e.key())
	if err == nil {
		t.Fatalf("override must return an error")
	}
	if row := e.mustRow(i.ID); row.Status != "UPCOMING" || row.StatusSource != "schedule" || row.Version != 1 || row.LastOverride != nil || e.auditCount(i.ID) != n {
		t.Fatalf("override must be undone: %+v", row)
	}
}

func TestSignals_BR_IPO_03_StepListenerFailureRetries(t *testing.T) {
	e := newSvcEnvJobs(t)
	e.setClock(10, 9, 0)
	i := e.create(acme())
	r := e.listen()
	r.failStatus(errors.New("tasks failed"))
	e.tickAt(11, 10, 0)
	if row := e.mustRow(i.ID); row.Status != "UPCOMING" || e.auditCount(i.ID) != 1 {
		t.Fatalf("a failing listener must undo the step: %+v audits=%d", row, e.auditCount(i.ID))
	}
	job := e.h.Job(jobOpen, stepKey(i.ID, jobOpen))
	if job.Status != "pending" || job.Attempts != 1 {
		t.Fatalf("job = %s attempts=%d, want pending/1 (scheduler retry)", job.Status, job.Attempts)
	}
	r.failStatus(nil)
	e.h.Clock.Set(e.at(11, 10, 0).Add(3 * time.Minute))
	e.h.Tick()
	if row := e.mustRow(i.ID); row.Status != "OPEN" || e.auditCount(i.ID) != 2 {
		t.Fatalf("retry must succeed once: %+v audits=%d", row, e.auditCount(i.ID))
	}
	if st := e.h.Job(jobOpen, stepKey(i.ID, jobOpen)).Status; st != "done" {
		t.Fatalf("job status = %s", st)
	}
}
