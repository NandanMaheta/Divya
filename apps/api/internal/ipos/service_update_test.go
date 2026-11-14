package ipos

import (
	"errors"
	"strings"
	"testing"
)

// US1.2, US1.9, US2.4, US3.7 (FR-021), US5.9: Update (PUT) behaviour.

func TestUpdate_FR006_StaleVersion(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(acme())
	d := acme()
	d.Name = "Acme Limited"
	if _, err := e.svc.Update(e.ctx(), e.admin, e.org, i.ID, i.Version, d, e.key()); err != nil {
		t.Fatalf("first save: %v", err)
	}
	d2 := acme()
	d2.Name = "Other Name"
	_, err := e.svc.Update(e.ctx(), e.admin, e.org, i.ID, i.Version, d2, e.key())
	if !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("err = %v, want ErrStaleVersion (US1.9)", err)
	}
	if r := e.mustRow(i.ID); r.Name != "Acme Limited" || r.Version != 2 || e.auditCount(i.ID) != 2 {
		t.Fatalf("first save must remain: %+v audits=%d", r, e.auditCount(i.ID))
	}
}

func TestUpdate_FR003_StoresExactValuesAndAuditsDiff(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(minimalDraft("Acme Ltd"))
	d := draftOf(i)
	d.PriceBandFloorPaise, d.PriceBandCapPaise, d.LotSize = p64(100_000), p64(105_000), p32(14)
	res, err := e.svc.Update(e.ctx(), e.admin, e.org, i.ID, i.Version, d, e.key())
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res.IPO.Version != 2 || len(res.Warnings) != 0 {
		t.Fatalf("version=%d warnings=%v", res.IPO.Version, res.Warnings)
	}
	if v, ok := res.IPO.MinApplicationPaise(); !ok || v != 1_470_000 {
		t.Fatalf("min application = %d,%v", v, ok)
	}
	a := e.audits(i.ID)
	if len(a) != 2 || a[1].Action != "ipo.updated" || a[1].ActorKind != "admin" {
		t.Fatalf("audit = %v, want created then updated", e.actions(i.ID))
	}
	if !strings.Contains(string(a[1].AfterRaw), "105000") || strings.Contains(string(a[1].AfterRaw), "Acme Ltd") {
		t.Fatalf("after must hold the changed fields only: %s", a[1].AfterRaw)
	}
}

func TestUpdate_FR005_WarningReturnedAndSaved(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(minimalDraft("Big Lot"))
	d := draftOf(i)
	d.PriceBandFloorPaise, d.PriceBandCapPaise, d.LotSize = p64(48_000), p64(50_000), p32(50)
	res, err := e.svc.Update(e.ctx(), e.admin, e.org, i.ID, i.Version, d, e.key())
	if err != nil || len(res.Warnings) != 1 || res.Warnings[0].Code != "ipo.min_application_out_of_range" {
		t.Fatalf("err=%v warnings=%+v (US1.3)", err, res.Warnings)
	}
	if !strings.Contains(res.Warnings[0].Detail, "₹25,000") || !strings.Contains(res.Warnings[0].Detail, "₹10,000") {
		t.Fatalf("detail = %q", res.Warnings[0].Detail)
	}
}

func TestUpdate_BR_IPO_01_EditToNonMainboardRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		segs []Exchange
		code string
	}{
		{"emerge", []Exchange{"NSE_EMERGE"}, "ipo.sme_not_allowed"},
		{"mixed", []Exchange{ExchangeNSE, "BSE_SME"}, "ipo.sme_not_allowed"},
		{"unknown", []Exchange{"XYZ"}, "ipo.invalid_segment"},
		{"empty", nil, "ipo.invalid_segment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newSvcEnv(t)
			i := e.create(acme())
			d := draftOf(i)
			d.Exchanges = tc.segs
			_, err := e.svc.Update(e.ctx(), e.admin, e.org, i.ID, i.Version, d, e.key())
			wantValidation2(t, err, tc.code)
			if r := e.mustRow(i.ID); r.Version != 1 || e.auditCount(i.ID) != 1 {
				t.Fatalf("IPO must be unchanged: %+v", r)
			}
			var ex []string
			if err := e.h.Pool.QueryRow(e.ctx(), `SELECT exchanges FROM ipos WHERE id=$1`, i.ID).Scan(&ex); err != nil || len(ex) != 2 {
				t.Fatalf("exchanges = %v err=%v", ex, err)
			}
		})
	}
}

func TestUpdate_FR003_InvalidValuesChangeNothing(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(acme())
	for _, mut := range []func(*Draft){
		func(d *Draft) { d.LotSize = p32(0) },
		func(d *Draft) { d.PriceBandFloorPaise, d.PriceBandCapPaise = p64(105_000), p64(100_000) },
		func(d *Draft) { d.PriceBandFloorPaise = nil },
	} {
		d := draftOf(i)
		mut(&d)
		_, err := e.svc.Update(e.ctx(), e.admin, e.org, i.ID, i.Version, d, e.key())
		wantValidation2(t, err, "ipo.invalid_value")
	}
	if r := e.mustRow(i.ID); r.Version != 1 || e.auditCount(i.ID) != 1 {
		t.Fatalf("nothing may change: %+v", r)
	}
}

func TestUpdate_BR_IPO_03_DateOrderRejected(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*Dates)
		ok   bool
	}{
		{"open after close", func(d *Dates) { d.Open = dt(14) }, false},
		{"allotment on close day", func(d *Dates) { d.Allotment = dt(13) }, false},
		{"listing before allotment", func(d *Dates) { d.Listing = dt(15) }, false},
		{"refund before allotment", func(d *Dates) { d.Refund = dt(15) }, false},
		{"open same day as close", func(d *Dates) { d.Open = dt(13) }, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newSvcEnv(t)
			i := e.create(acme())
			d := draftOf(i)
			tc.mut(&d.Dates)
			_, err := e.svc.Update(e.ctx(), e.admin, e.org, i.ID, i.Version, d, e.key())
			if tc.ok {
				if err != nil {
					t.Fatalf("err = %v, want accepted", err)
				}
				return
			}
			wantValidation2(t, err, "ipo.date_order")
			if e.mustRow(i.ID).Version != 1 {
				t.Fatalf("nothing may change")
			}
		})
	}
}

func TestUpdate_BR_IPO_02_PublishedMustKeepRequirements(t *testing.T) {
	tests := []struct {
		name  string
		mut   func(*Draft)
		field string
	}{
		{"clear open", func(d *Draft) { d.Dates.Open = nil }, "open_date"},
		{"clear close", func(d *Draft) { d.Dates.Close = nil }, "close_date"},
		{"clear lot size", func(d *Draft) { d.LotSize = nil }, "lot_size"},
		{"clear both prices", func(d *Draft) { d.PriceBandFloorPaise, d.PriceBandCapPaise, d.IssuePricePaise = nil, nil, nil }, "price"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newSvcEnv(t)
			i := e.published(acme(), FlagApply)
			d := draftOf(i)
			tc.mut(&d)
			_, err := e.svc.Update(e.ctx(), e.admin, e.org, i.ID, i.Version, d, e.key())
			ve := wantValidation2(t, err, "ipo.publish_incomplete")
			if f := issueFields(ve); !contains(f, tc.field) {
				t.Fatalf("issues %v must name %s", f, tc.field)
			}
			if r := e.mustRow(i.ID); r.Version != i.Version || !r.Published {
				t.Fatalf("published IPO must be unchanged: %+v", r)
			}
		})
	}
}

func TestUpdate_BR_IPO_02_PublishedEditSendsNoEligibilitySignal(t *testing.T) {
	e := newSvcEnv(t)
	i := e.published(acme(), FlagApply)
	r := e.listen()
	d := draftOf(i)
	d.Registrar = ptrStr("KFin")
	if _, err := e.svc.Update(e.ctx(), e.admin, e.org, i.ID, i.Version, d, e.key()); err != nil {
		t.Fatal(err)
	}
	r.wantCounts(t, 0, 0, 0)
}

func TestUpdate_FR006_NoOpWritesNothing(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(acme())
	jobsBefore := e.stepJobs(i.ID)
	res, err := e.svc.Update(e.ctx(), e.admin, e.org, i.ID, i.Version, draftOf(i), e.key())
	if err != nil {
		t.Fatalf("a PUT that changes nothing is a 200: %v", err)
	}
	if res.IPO.Version != 1 || e.mustRow(i.ID).Version != 1 || e.auditCount(i.ID) != 1 {
		t.Fatalf("no version bump and no audit expected (R-06)")
	}
	if got := e.stepJobs(i.ID); len(got) != len(jobsBefore) {
		t.Fatalf("steps changed on a no-op")
	}
}

func TestUpdate_BR_IPO_01_NotFoundAndOrgIsolation(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(acme())
	other := e.h.NewOrg("Other")
	if _, err := e.svc.Update(e.ctx(), e.admin, other, i.ID, i.Version, acme(), e.key()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other org: err = %v, want ErrNotFound", err)
	}
	if _, err := e.svc.Update(e.ctx(), e.admin, e.org, newID(), 1, acme(), e.key()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: err = %v, want ErrNotFound", err)
	}
}

func TestUpdate_US1_11_SourceStaysManual(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(acme())
	d := draftOf(i)
	d.Name = "Acme Limited"
	res, err := e.svc.Update(e.ctx(), e.admin, e.org, i.ID, i.Version, d, e.key())
	if err != nil || res.IPO.Source != "manual" || res.IPO.LastSyncedAt != nil {
		t.Fatalf("err=%v source=%q", err, res.IPO.Source)
	}
}
