package ipos

import (
	"errors"
	"fmt"
	"testing"
)

// ADR 0008 signals: DatesChanged (FR-042), EligibilityChanged (FR-024), listener failure rolls
// back everything (US3.10, US5.5), RejectedError, registration order.

func TestSignals_BR_IPO_03_DatesChangedOncePerEdit(t *testing.T) {
	e := newSvcEnv(t)
	r := e.listen()
	i := e.create(acme())
	r.wantCounts(t, 0, 0, 0) // creating sends no dates signal

	d := draftOf(i)
	d.Dates = Dates{Open: dt(12), Close: dt(17), Allotment: dt(18), Refund: dt(19), DematCredit: dt(19), Listing: dt(20)}
	res, err := e.svc.Update(e.ctx(), e.admin, e.org, i.ID, i.Version, d, e.key())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Dates) != 1 {
		t.Fatalf("dates signals = %d, want exactly 1 for an edit changing five dates", len(r.Dates))
	}
	ev := r.Dates[0]
	if ev.IPOID != i.ID || ev.OrgID != e.org || ev.Actor != e.admin || !ev.At.Equal(e.h.Clock.Now()) {
		t.Fatalf("envelope wrong: %+v", ev)
	}
	if *ev.Old.Open != *dt(11) || *ev.Old.Close != *dt(13) || *ev.Old.Listing != *dt(18) || *ev.New.Open != *dt(12) || *ev.New.Close != *dt(17) || *ev.New.Listing != *dt(20) {
		t.Fatalf("old/new dates wrong: %+v", ev)
	}
	if *ev.Old.Refund != *dt(17) || *ev.New.Refund != *dt(19) || *ev.Old.DematCredit != *dt(17) || *ev.New.DematCredit != *dt(19) || *ev.Old.Allotment != *dt(16) || *ev.New.Allotment != *dt(18) {
		t.Fatalf("all six dates must be reported: %+v", ev)
	}

	// non-date edit and no-op edit: nothing
	d2 := draftOf(res.IPO)
	d2.Registrar = ptrStr("KFin")
	res2, err := e.svc.Update(e.ctx(), e.admin, e.org, i.ID, res.IPO.Version, d2, e.key())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Update(e.ctx(), e.admin, e.org, i.ID, res2.IPO.Version, draftOf(res2.IPO), e.key()); err != nil {
		t.Fatal(err)
	}
	r.wantCounts(t, 1, 0, 0)

	// a refund-only change still signals (no step moves, dependents still need to know)
	d3 := draftOf(res2.IPO)
	d3.Dates.Refund = dt(20)
	if _, err := e.svc.Update(e.ctx(), e.admin, e.org, i.ID, res2.IPO.Version, d3, e.key()); err != nil {
		t.Fatal(err)
	}
	r.wantCounts(t, 2, 0, 0)
}

func TestSignals_BR_IPO_03_DatesChangedFailureRollsBack(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(acme())
	r := e.listen()
	r.errDates = errors.New("dependent exploded")
	stepsBefore, auditsBefore := fmt.Sprint(e.stepJobs(i.ID)), e.auditCount(i.ID)
	d := draftOf(i)
	d.Dates.Open = dt(12)
	d.Name = "Renamed"
	_, err := e.svc.Update(e.ctx(), e.admin, e.org, i.ID, i.Version, d, e.key())
	if err == nil || !errors.Is(err, r.errDates) {
		t.Fatalf("err = %v, want the listener error (US5.5)", err)
	}
	row := e.mustRow(i.ID)
	if row.Name != "Acme Ltd" || *row.Open != "2026-11-11" || row.Version != 1 {
		t.Fatalf("row must be unchanged: %+v", row)
	}
	if fmt.Sprint(e.stepJobs(i.ID)) != stepsBefore || e.auditCount(i.ID) != auditsBefore {
		t.Fatalf("steps or audit changed despite rollback")
	}
}

func TestSignals_BR_IPO_02_EligibilityChanged(t *testing.T) {
	e := newSvcEnv(t)
	r := e.listen()
	i := e.create(acme())
	i = e.setFlag(i, FlagApply, nil) // draft
	r.wantCounts(t, 0, 0, 0)
	i = e.publish(i) // -> eligible
	if len(r.Elig) != 1 || !r.Elig[0].Eligible || r.Elig[0].Cause != "publish" {
		t.Fatalf("publish APPLY: %+v", r.Elig)
	}
	i = e.setFlag(i, FlagDoNotApply, nil) // flips off
	if len(r.Elig) != 2 || r.Elig[1].Eligible || r.Elig[1].Cause != "flag" {
		t.Fatalf("flag flip: %+v", r.Elig)
	}
	i = e.setFlag(i, FlagDoNotApply, ptrStr("note only")) // no flip
	if len(r.Elig) != 2 {
		t.Fatalf("note-only must not signal")
	}
	i = e.setFlag(i, FlagApply, nil) // flips on
	if len(r.Elig) != 3 || !r.Elig[2].Eligible || r.Elig[2].Cause != "flag" {
		t.Fatalf("flag back to APPLY: %+v", r.Elig)
	}
	if _, err := e.svc.Unpublish(e.ctx(), e.admin, e.org, i.ID, i.Version, e.key()); err != nil {
		t.Fatal(err)
	}
	if len(r.Elig) != 4 || r.Elig[3].Eligible || r.Elig[3].Cause != "unpublish" {
		t.Fatalf("unpublish: %+v", r.Elig)
	}
	if len(r.EligRows) != 4 || !r.EligRows[0].Published || r.EligRows[3].Published {
		t.Fatalf("listener must see the new state inside the transaction: %+v", r.EligRows)
	}
}

func TestSignals_BR_IPO_02_FailureRollsBackPublish(t *testing.T) {
	for _, mode := range []string{"error", "panic"} {
		t.Run(mode, func(t *testing.T) {
			e := newSvcEnv(t)
			i := e.setFlag(e.create(acme()), FlagApply, nil)
			r := e.listen()
			if mode == "error" {
				r.errElig = errors.New("deals module failed")
			} else {
				r.panicElig = true
			}
			n := e.auditCount(i.ID)
			var err error
			func() {
				defer func() {
					if p := recover(); p != nil && mode == "error" {
						t.Fatalf("unexpected panic %v", p)
					}
				}()
				_, err = e.svc.Publish(e.ctx(), e.admin, e.org, i.ID, i.Version, e.key())
			}()
			if mode == "error" && err == nil {
				t.Fatalf("Publish must fail when a listener fails (US3.10)")
			}
			row := e.mustRow(i.ID)
			if row.Published || row.FirstPublished != nil || row.Version != i.Version || e.auditCount(i.ID) != n {
				t.Fatalf("whole publish must be undone: %+v audits %d->%d", row, n, e.auditCount(i.ID))
			}
		})
	}
}

func TestSignals_BR_IPO_02_RejectedErrorSurfacesAndRollsBack(t *testing.T) {
	e := newSvcEnv(t)
	i := e.setFlag(e.create(acme()), FlagApply, nil)
	r := e.listen()
	r.errElig = &RejectedError{Code: "deals.open_deals", Detail: "2 open deals"}
	_, err := e.svc.Publish(e.ctx(), e.admin, e.org, i.ID, i.Version, e.key())
	var rej *RejectedError
	if !errors.As(err, &rej) || rej.Code != "deals.open_deals" {
		t.Fatalf("err = %v, want *RejectedError with the listener's code", err)
	}
	if e.mustRow(i.ID).Published {
		t.Fatalf("must be rolled back")
	}
}

func TestSignals_BR_IPO_02_ListenersCalledInRegistrationOrder(t *testing.T) {
	e := newSvcEnv(t)
	var order []string
	a := &recorder{svc: e.svc, name: "first", log: &order}
	b := &recorder{svc: e.svc, name: "second", log: &order}
	e.attach(a)
	e.attach(b)
	i := e.setFlag(e.create(acme()), FlagApply, nil)
	e.publish(i)
	if fmt.Sprint(order) != "[first:eligibility second:eligibility]" {
		t.Fatalf("order = %v", order)
	}
}

func TestSignals_BR_IPO_03_SignalTimeIsClock(t *testing.T) {
	e := newSvcEnv(t)
	r := e.listen()
	e.setClock(12, 9, 30)
	i := e.setFlag(e.create(acme()), FlagApply, nil)
	e.publish(i)
	if !r.Elig[0].At.Equal(e.at(12, 9, 30)) {
		t.Fatalf("At = %s", r.Elig[0].At)
	}
}
