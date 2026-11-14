package ipos

import (
	"context"
	"errors"
	"testing"
)

// US6 (BR-IPO-03, FR-033, FR-035, FR-036): override, MarkSettled.

func lifecycleIPO(t *testing.T) (*svcEnv, IPO) {
	t.Helper()
	e := newSvcEnvJobs(t)
	e.setClock(10, 9, 0)
	return e, e.create(acme())
}

func TestOverride_BR_IPO_03_ForwardMakesLaterStepNoop(t *testing.T) {
	e, i := lifecycleIPO(t)
	e.tickAt(11, 10, 0)
	e.tickAt(13, 19, 0)
	wantStatus(t, e, i.ID, StatusClosed)
	cur := e.currentID(i.ID)
	res, err := e.svc.OverrideStatus(e.ctx(), e.admin, e.org, i.ID, cur.Version, StatusAllotmentOut, "registrar published early", e.key())
	if err != nil {
		t.Fatalf("override: %v", err)
	}
	if res.IPO.Status != StatusAllotmentOut || res.IPO.StatusSource != SourceAdmin || res.IPO.LastOverrideAt == nil {
		t.Fatalf("result = %+v (US6.1, US6.9)", res.IPO)
	}
	sa := e.statusAudits(i.ID)
	last := sa[len(sa)-1]
	if last.ActorKind != "admin" || last.After["reason"] != "registrar published early" || last.Before["status"] != "CLOSED" || last.After["status"] != "ALLOTMENT_OUT" || last.After["status_source"] != "admin" {
		t.Fatalf("override audit = %v -> %v", last.Before, last.After)
	}
	n := e.auditCount(i.ID)
	e.tickAt(16, 10, 0)
	if e.auditCount(i.ID) != n || e.mustRow(i.ID).StatusSource != "admin" {
		t.Fatalf("the 16 Nov step must make no change and write nothing")
	}
}

func TestOverride_BR_IPO_03_BackwardDoesNotRefirePastSteps(t *testing.T) {
	e, i := lifecycleIPO(t)
	e.tickAt(11, 10, 0)
	e.setClock(12, 12, 0)
	e.override(e.currentID(i.ID), StatusUpcoming, "opened by mistake")
	wantStatus(t, e, i.ID, StatusUpcoming)
	e.tickAt(12, 13, 0)
	e.tickAt(12, 23, 0)
	wantStatus(t, e, i.ID, StatusUpcoming)
	e.tickAt(13, 17, 0)
	wantStatus(t, e, i.ID, StatusClosed)
}

func TestOverride_BR_IPO_03_BackwardSuppressesPendingPastStep(t *testing.T) {
	// No tick ran during downtime: the open step is still pending but its instant is before the
	// override, so it must not fire (FR-036, K-04). The close step is later and still fires.
	e, i := lifecycleIPO(t)
	e.setClock(12, 12, 0)
	cur := e.override(i, StatusOpen, "seed")
	e.setClock(12, 12, 5)
	e.override(cur, StatusUpcoming, "back again")
	e.tickAt(12, 12, 10)
	wantStatus(t, e, i.ID, StatusUpcoming)
	e.tickAt(13, 17, 0)
	wantStatus(t, e, i.ID, StatusClosed)
}

func TestOverride_BR_IPO_03_DateChangeReArmsStep(t *testing.T) {
	e, i := lifecycleIPO(t)
	e.tickAt(11, 10, 0)
	e.setClock(12, 12, 0)
	e.override(e.currentID(i.ID), StatusUpcoming, "opened by mistake")
	cur := e.currentID(i.ID)
	d := draftOf(cur)
	d.Dates.Open = dt(13)
	e.update(cur, d)
	e.tickAt(13, 10, 0)
	wantStatus(t, e, i.ID, StatusOpen)
	e.tickAt(13, 17, 0)
	wantStatus(t, e, i.ID, StatusClosed)
}

func TestOverride_BR_IPO_03_ReasonRequired(t *testing.T) {
	for _, reason := range []string{"", "   ", "\t\n"} {
		e := newSvcEnv(t)
		i := e.create(acme())
		_, err := e.svc.OverrideStatus(e.ctx(), e.admin, e.org, i.ID, i.Version, StatusOpen, reason, e.key())
		_ = wantValidation2(t, err, "ipo.invalid_value")
		if e.mustRow(i.ID).Status != "UPCOMING" || e.auditCount(i.ID) != 1 {
			t.Fatalf("nothing may change without a reason (US6.4)")
		}
	}
}

func TestOverride_BR_IPO_03_SameStatusIsNoChange(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(acme())
	_, err := e.svc.OverrideStatus(e.ctx(), e.admin, e.org, i.ID, i.Version, StatusUpcoming, "x", e.key())
	if !errors.Is(err, ErrNoChange) || e.auditCount(i.ID) != 1 || e.mustRow(i.ID).Version != 1 {
		t.Fatalf("err = %v (US6.5)", err)
	}
}

func TestOverride_BR_IPO_03_AnyStatusToAnyStatus(t *testing.T) {
	for _, to := range allStatuses[1:] {
		t.Run(string(to), func(t *testing.T) {
			e := newSvcEnv(t)
			i := e.create(acme())
			res, err := e.svc.OverrideStatus(e.ctx(), e.admin, e.org, i.ID, i.Version, to, "admin decision", e.key())
			if err != nil || res.IPO.Status != to || res.IPO.StatusSource != SourceAdmin || res.IPO.Version != 2 {
				t.Fatalf("err=%v result=%+v", err, res.IPO)
			}
		})
	}
}

func TestOverride_BR_IPO_03_AdminSettledWithReason(t *testing.T) {
	e, i := lifecycleIPO(t)
	e.tickAt(18, 10, 0)
	res, err := e.svc.OverrideStatus(e.ctx(), e.admin, e.org, i.ID, e.currentID(i.ID).Version, StatusSettled, "all payments received", e.key())
	if err != nil || res.IPO.Status != StatusSettled {
		t.Fatalf("err=%v status=%s (US6.6)", err, res.IPO.Status)
	}
	sa := e.statusAudits(i.ID)
	if sa[len(sa)-1].After["reason"] != "all payments received" {
		t.Fatalf("reason missing from audit: %v", sa[len(sa)-1].After)
	}
}

func TestOverride_BR_IPO_03_StaleAndNotFound(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(acme())
	if _, err := e.svc.OverrideStatus(e.ctx(), e.admin, e.org, i.ID, 9, StatusOpen, "x", e.key()); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("err = %v", err)
	}
	if _, err := e.svc.OverrideStatus(e.ctx(), e.admin, e.org, newID(), 1, StatusOpen, "x", e.key()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestMarkSettled_BR_IPO_03_FromListed(t *testing.T) {
	e, i := lifecycleIPO(t)
	e.tickAt(18, 10, 0)
	n := len(e.statusAudits(i.ID))
	if err := e.svc.MarkSettled(e.ctx(), e.org, i.ID); err != nil {
		t.Fatalf("MarkSettled: %v", err)
	}
	row := e.mustRow(i.ID)
	if row.Status != "SETTLED" || row.StatusSource != "settlement" {
		t.Fatalf("row = %+v", row)
	}
	sa := e.statusAudits(i.ID)
	last := sa[len(sa)-1]
	if len(sa) != n+1 || last.ActorKind != "system" || last.ActorID != nil || last.After["status"] != "SETTLED" || last.After["status_source"] != "settlement" {
		t.Fatalf("audit = %+v / %+v", last.Before, last.After)
	}
	if _, has := last.After["reason"]; has {
		t.Fatalf("settlement carries no reason")
	}
	// already SETTLED: nil, nothing written
	v := row.Version
	if err := e.svc.MarkSettled(e.ctx(), e.org, i.ID); err != nil || e.mustRow(i.ID).Version != v || len(e.statusAudits(i.ID)) != n+1 {
		t.Fatalf("second call must be a silent no-op: %v", err)
	}
}

func TestMarkSettled_BR_IPO_03_NotListedChangesNothing(t *testing.T) {
	e, i := lifecycleIPO(t)
	e.tickAt(13, 17, 0)
	err := e.svc.MarkSettled(e.ctx(), e.org, i.ID)
	var nl *NotListedError
	if !errors.Is(err, ErrNotListed) || !errors.As(err, &nl) || nl.Current != StatusClosed {
		t.Fatalf("err = %v, want NotListedError{CLOSED} (US6.7)", err)
	}
	wantStatus(t, e, i.ID, StatusClosed)
	if err := e.svc.MarkSettled(e.ctx(), e.org, newID()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id err = %v", err)
	}
}

func TestMarkSettled_BR_IPO_03_JoinsCallerTransaction(t *testing.T) {
	e, i := lifecycleIPO(t)
	e.tickAt(18, 10, 0)
	boom := errors.New("caller rolls back")
	err := e.h.Txr.InTx(e.ctx(), func(ctx context.Context) error {
		if err := e.svc.MarkSettled(ctx, e.org, i.ID); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	wantStatus(t, e, i.ID, StatusListed)
}

func TestOverride_BR_IPO_03_ListedWithoutApplicationsStaysListed(t *testing.T) {
	e, i := lifecycleIPO(t)
	e.tickAt(18, 10, 0)
	e.tickAt(25, 10, 0)
	wantStatus(t, e, i.ID, StatusListed)
}
