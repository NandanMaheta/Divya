package ipos

import (
	"errors"
	"testing"
)

// US5 (BR-IPO-03, FR-041): date edits reschedule this feature's steps in the same change.

func openIPO(t *testing.T) (*svcEnv, IPO) {
	t.Helper()
	e := newSvcEnvJobs(t)
	e.setClock(10, 9, 0)
	i := e.create(acme())
	return e, i
}

func TestReschedule_BR_IPO_03_OpenMovedLater(t *testing.T) {
	e, i := openIPO(t)
	d := draftOf(i)
	d.Dates.Open = dt(12)
	e.update(i, d)
	if at := e.stepJobs(i.ID)[jobOpen]; !at.Equal(e.at(12, 10, 0)) {
		t.Fatalf("open step run_at = %s, want 12 Nov 10:00 IST", at)
	}
	if n := e.h.Count("scheduled_jobs", "subject = $1 AND kind = $2 AND status = 'pending'", "ipo:"+i.ID.String(), jobOpen); n != 1 {
		t.Fatalf("pending open steps = %d, want 1", n)
	}
	e.tickAt(11, 10, 5)
	wantStatus(t, e, i.ID, StatusUpcoming)
	e.tickAt(12, 10, 0)
	wantStatus(t, e, i.ID, StatusOpen)
}

func TestReschedule_BR_IPO_03_CloseExtendedWhileOpen(t *testing.T) {
	e, i := openIPO(t)
	e.tickAt(13, 12, 0)
	cur := e.currentID(i.ID)
	wantStatus(t, e, i.ID, StatusOpen)
	d := draftOf(cur)
	d.Dates = Dates{Open: dt(11), Close: dt(17), Allotment: dt(18), Refund: dt(19), DematCredit: dt(19), Listing: dt(20)}
	e.update(cur, d)
	e.tickAt(13, 17, 5)
	wantStatus(t, e, i.ID, StatusOpen)
	e.tickAt(17, 17, 0)
	wantStatus(t, e, i.ID, StatusClosed)
}

func TestReschedule_BR_IPO_03_CloseMovedIntoPast(t *testing.T) {
	e, i := openIPO(t)
	e.tickAt(13, 12, 0)
	cur := e.currentID(i.ID)
	d := draftOf(cur)
	d.Dates.Close = dt(12)
	e.update(cur, d)
	e.h.Tick()
	wantStatus(t, e, i.ID, StatusClosed)
}

func TestReschedule_BR_IPO_03_FailedSaveKeepsOldSteps(t *testing.T) {
	e, i := openIPO(t)
	before := e.stepJobs(i.ID)
	// rejected by validation
	d := draftOf(i)
	d.Dates.Open = dt(14)
	if _, err := e.svc.Update(e.ctx(), e.admin, e.org, i.ID, i.Version, d, e.key()); err == nil {
		t.Fatalf("date order violation must be rejected")
	}
	// rejected by a listener after the steps were rewritten
	r := e.listen()
	r.errDates = errors.New("boom")
	d2 := draftOf(i)
	d2.Dates.Open = dt(12)
	if _, err := e.svc.Update(e.ctx(), e.admin, e.org, i.ID, i.Version, d2, e.key()); err == nil {
		t.Fatalf("listener failure must fail the edit")
	}
	after := e.stepJobs(i.ID)
	for k, at := range before {
		if !after[k].Equal(at) {
			t.Fatalf("%s run_at moved %s -> %s despite the rollback (US5.6)", k, at, after[k])
		}
	}
	if e.jobRows(i.ID) != 4 {
		t.Fatalf("job rows = %d, want 4", e.jobRows(i.ID))
	}
}

func TestReschedule_BR_IPO_03_OpenMovedLaterWhileOpenStaysOpen(t *testing.T) {
	e, i := openIPO(t)
	e.tickAt(12, 12, 0)
	wantStatus(t, e, i.ID, StatusOpen)
	cur := e.currentID(i.ID)
	d := draftOf(cur)
	d.Dates.Open = dt(13)
	e.update(cur, d)
	n := e.auditCount(i.ID)
	e.tickAt(13, 11, 0)
	wantStatus(t, e, i.ID, StatusOpen)
	if e.auditCount(i.ID) != n {
		t.Fatalf("automatic changes never move backwards and the re-fired step is a silent no-op (US5.7)")
	}
}

func TestReschedule_BR_IPO_03_ClearedAllotmentCancelsStep(t *testing.T) {
	e, i := openIPO(t)
	d := draftOf(i)
	d.Dates.Allotment = nil
	e.update(i, d)
	if _, ok := e.stepJobs(i.ID)[jobAllotment]; ok {
		t.Fatalf("allotment step must be cancelled (US5.8)")
	}
	if st := e.h.Job(jobAllotment, stepKey(i.ID, jobAllotment)).Status; st != "cancelled" {
		t.Fatalf("job status = %s", st)
	}
	e.tickAt(16, 10, 0)
	wantStatus(t, e, i.ID, StatusClosed)
}

func TestReschedule_BR_IPO_03_OneAuditWithOldAndNewDates(t *testing.T) {
	e, i := openIPO(t)
	d := draftOf(i)
	d.Dates = Dates{Open: dt(11), Close: dt(17), Allotment: dt(18), Refund: dt(19), DematCredit: dt(19), Listing: dt(20)}
	e.update(i, d)
	a := e.audits(i.ID)
	if len(a) != 2 || a[1].Action != "ipo.updated" {
		t.Fatalf("audit = %v, want one ipo.updated (US5.10)", e.actions(i.ID))
	}
	if !contains2(string(a[1].BeforeRaw), "2026-11-13") || !contains2(string(a[1].AfterRaw), "2026-11-17") || !contains2(string(a[1].AfterRaw), "2026-11-20") {
		t.Fatalf("before=%s after=%s", a[1].BeforeRaw, a[1].AfterRaw)
	}
	if contains2(string(a[1].AfterRaw), "2026-11-11") {
		t.Fatalf("unchanged open date must not be in the diff: %s", a[1].AfterRaw)
	}
}

func TestReschedule_BR_IPO_03_OnlyChangedStepsTouchScheduler(t *testing.T) {
	e := newSvcEnvWith(t, svcOpts{jobs: true, spy: true})
	e.setClock(10, 9, 0)
	i := e.create(acme())
	base := e.spy.calls()
	if base != 4 {
		t.Fatalf("create made %d scheduler calls, want 4", base)
	}
	// non-date edit: no scheduler call (US5.11)
	d := draftOf(i)
	d.Registrar = ptrStr("KFin")
	i = e.update(i, d)
	if e.spy.calls() != base {
		t.Fatalf("non-date edit touched the scheduler")
	}
	// refund-only change: no step exists for it
	d = draftOf(i)
	d.Dates.Refund = dt(18)
	i = e.update(i, d)
	if e.spy.calls() != base {
		t.Fatalf("refund date has no lifecycle step")
	}
	// close only: exactly one Schedule
	d = draftOf(i)
	d.Dates.Close = dt(14)
	i = e.update(i, d)
	if len(e.spy.schedules) != 5 || e.spy.schedules[4].Kind != jobClose || len(e.spy.cancels) != 0 {
		t.Fatalf("schedules=%d cancels=%v, want one extra close Schedule", len(e.spy.schedules), e.spy.cancels)
	}
	// clear allotment (listing stays valid): exactly one Cancel
	d = draftOf(i)
	d.Dates.Allotment = nil
	e.update(i, d)
	if len(e.spy.cancels) != 1 || e.spy.cancels[0] != jobAllotment {
		t.Fatalf("cancels = %v, want [ipo.allotment]", e.spy.cancels)
	}
}

func TestReschedule_BR_IPO_03_DeleteCancelsAllSteps(t *testing.T) {
	e, i := openIPO(t)
	if _, err := e.svc.Delete(e.ctx(), e.admin, e.org, i.ID, i.Version, e.key()); err != nil {
		t.Fatal(err)
	}
	e.tickAt(30, 12, 0)
	if e.h.Count("scheduled_jobs", "subject = $1 AND status = 'done'", "ipo:"+i.ID.String()) != 0 {
		t.Fatalf("no step may run for a deleted IPO")
	}
}
