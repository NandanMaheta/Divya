package ipos

import (
	"errors"
	"fmt"
	"testing"
)

// US3 (BR-IPO-02): publish, flag, unpublish.

func completeNoFlag() Draft { return acme() }

func TestPublish_BR_IPO_02_RequiresFlag(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(completeNoFlag())
	_, err := e.svc.Publish(e.ctx(), e.admin, e.org, i.ID, i.Version, e.key())
	ve := wantValidation2(t, err, "ipo.publish_incomplete")
	if f := issueFields(ve); fmt.Sprint(f) != "[apply_flag]" {
		t.Fatalf("issues = %v, want only apply_flag (US3.1)", f)
	}
	if r := e.mustRow(i.ID); r.Published || r.Version != 1 || e.auditCount(i.ID) != 1 {
		t.Fatalf("must stay a draft: %+v", r)
	}
}

func TestPublish_BR_IPO_02_ListsEveryMissingItem(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(minimalDraft("Bare"))
	i = e.setFlag(i, FlagApply, nil)
	_, err := e.svc.Publish(e.ctx(), e.admin, e.org, i.ID, i.Version, e.key())
	ve := wantValidation2(t, err, "ipo.publish_incomplete")
	got := issueFields(ve)
	for _, want := range []string{"open_date", "close_date", "price", "lot_size"} {
		if !contains(got, want) {
			t.Fatalf("issues %v must list %s (US3.2)", got, want)
		}
	}
	if len(got) != 4 || e.mustRow(i.ID).Published {
		t.Fatalf("issues = %v, published=%v", got, e.mustRow(i.ID).Published)
	}
}

func TestPublish_BR_IPO_02_ApplyWithFinalPriceIsEligible(t *testing.T) {
	e := newSvcEnv(t)
	r := e.listen()
	d := Draft{Name: "Acme Ltd", Exchanges: []Exchange{ExchangeNSE}, IssuePricePaise: p64(105_000), LotSize: p32(14),
		Dates: Dates{Open: dt(11), Close: dt(13)}}
	i := e.create(d)
	note := "strong anchor book"
	i = e.setFlag(i, FlagApply, &note)
	res, err := e.svc.Publish(e.ctx(), e.admin, e.org, i.ID, i.Version, e.key())
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !res.IPO.Published || !res.IPO.Eligible() || res.IPO.FirstPublishedAt == nil || res.IPO.Version != i.Version+1 {
		t.Fatalf("published state wrong: %+v", res.IPO)
	}
	if got := e.actions(i.ID); fmt.Sprint(got) != "[ipo.created ipo.flag_set ipo.published]" {
		t.Fatalf("audit = %v", got)
	}
	a := e.audits(i.ID)[2]
	if a.ActorKind != "admin" || a.After["published"] != true || a.After["eligible"] != true {
		t.Fatalf("published audit = %+v", a.After)
	}
	if len(r.Elig) != 1 || !r.Elig[0].Eligible || r.Elig[0].Cause != "publish" || r.Elig[0].IPOID != i.ID || r.Elig[0].Actor != e.admin {
		t.Fatalf("eligibility signals = %+v (US3.3)", r.Elig)
	}
}

func TestPublish_BR_IPO_02_DoNotApplyVisibleNotEligible(t *testing.T) {
	e := newSvcEnv(t)
	r := e.listen()
	i := e.published(acme(), FlagDoNotApply)
	if i.Eligible() || !i.Published {
		t.Fatalf("DNA must be published but not eligible: %+v", i)
	}
	r.wantNoSignals(t)
	p, err := e.svc.ListPublished(e.ctx(), e.org, ClientFilter{})
	if err != nil || len(p.Items) != 1 || p.Items[0].Flag == nil || *p.Items[0].Flag != FlagDoNotApply {
		t.Fatalf("client must see the DNA IPO: %+v err=%v", p.Items, err)
	}
}

func TestPublish_BR_IPO_02_SoftWarningReturned(t *testing.T) {
	e := newSvcEnv(t)
	d := acme()
	d.LotSize, d.PriceBandFloorPaise, d.PriceBandCapPaise = p32(50), p64(48_000), p64(50_000)
	i := e.setFlag(e.create(d), FlagApply, nil)
	res, err := e.svc.Publish(e.ctx(), e.admin, e.org, i.ID, i.Version, e.key())
	if err != nil || len(res.Warnings) != 1 || res.Warnings[0].Code != "ipo.min_application_out_of_range" {
		t.Fatalf("err=%v warnings=%v (US3.9)", err, res.Warnings)
	}
}

func TestPublish_BR_IPO_02_AlreadyPublishedIsNoChange(t *testing.T) {
	e := newSvcEnv(t)
	i := e.published(acme(), FlagApply)
	n := e.auditCount(i.ID)
	_, err := e.svc.Publish(e.ctx(), e.admin, e.org, i.ID, i.Version, e.key())
	if !errors.Is(err, ErrNoChange) || e.auditCount(i.ID) != n {
		t.Fatalf("err = %v audits %d->%d", err, n, e.auditCount(i.ID))
	}
}

func TestPublish_FR006_StaleVersion(t *testing.T) {
	e := newSvcEnv(t)
	i := e.setFlag(e.create(acme()), FlagApply, nil)
	_, err := e.svc.Publish(e.ctx(), e.admin, e.org, i.ID, i.Version-1, e.key())
	if !errors.Is(err, ErrStaleVersion) || e.mustRow(i.ID).Published {
		t.Fatalf("err = %v", err)
	}
}

func TestPublish_BR_IPO_03_DoesNotTouchSteps(t *testing.T) {
	e := newSvcEnv(t)
	i := e.setFlag(e.create(acme()), FlagApply, nil)
	before := e.stepJobs(i.ID)
	rows := e.jobRows(i.ID)
	e.publish(i)
	after := e.stepJobs(i.ID)
	if e.jobRows(i.ID) != rows || fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("publish must not touch lifecycle steps (K-11): %v -> %v", before, after)
	}
}

func TestPublish_BR_IPO_02_FirstPublishedAtKept(t *testing.T) {
	e := newSvcEnv(t)
	i := e.published(acme(), FlagApply)
	first := *e.mustRow(i.ID).FirstPublished
	un, err := e.svc.Unpublish(e.ctx(), e.admin, e.org, i.ID, i.Version, e.key())
	if err != nil {
		t.Fatal(err)
	}
	e.h.Clock.Advance(3_600_000_000_000)
	e.publish(un.IPO)
	if got := *e.mustRow(i.ID).FirstPublished; !got.Equal(first) {
		t.Fatalf("first_published_at moved: %s -> %s", first, got)
	}
}

func TestSetFlag_BR_IPO_02_FlipToDoNotApplySignalsNotEligible(t *testing.T) {
	e := newSvcEnv(t)
	i := e.published(acme(), FlagApply)
	r := e.listen()
	note := "GMP collapsed"
	res, err := e.svc.SetFlag(e.ctx(), e.admin, e.org, i.ID, i.Version, flagPtr(FlagDoNotApply), &note, e.key())
	if err != nil {
		t.Fatalf("SetFlag: %v", err)
	}
	if res.IPO.Eligible() || *res.IPO.Flag != FlagDoNotApply || res.IPO.FlagNote == nil || *res.IPO.FlagNote != note {
		t.Fatalf("flag state wrong: %+v", res.IPO)
	}
	if len(r.Elig) != 1 || r.Elig[0].Eligible || r.Elig[0].Cause != "flag" {
		t.Fatalf("signals = %+v (US3.5)", r.Elig)
	}
	a := e.audits(i.ID)
	last := a[len(a)-1]
	if last.Action != "ipo.flag_set" || last.Before["apply_flag"] != "APPLY" || last.After["apply_flag"] != "DO_NOT_APPLY" || last.After["flag_note"] != note {
		t.Fatalf("flag audit before=%v after=%v", last.Before, last.After)
	}
}

func TestSetFlag_BR_IPO_02_NoteOnlyNoEligibilitySignal(t *testing.T) {
	e := newSvcEnv(t)
	i := e.published(acme(), FlagApply)
	r := e.listen()
	n := e.auditCount(i.ID)
	note := "updated note"
	if _, err := e.svc.SetFlag(e.ctx(), e.admin, e.org, i.ID, i.Version, flagPtr(FlagApply), &note, e.key()); err != nil {
		t.Fatal(err)
	}
	r.wantNoSignals(t)
	if e.auditCount(i.ID) != n+1 || e.mustRow(i.ID).Note == nil || *e.mustRow(i.ID).Note != note {
		t.Fatalf("note must change with an audit entry (US3.6)")
	}
}

func TestSetFlag_BR_IPO_02_DraftFlagNeverSignals(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(acme())
	r := e.listen()
	e.setFlag(i, FlagApply, nil)
	r.wantNoSignals(t)
}

func TestSetFlag_BR_IPO_02_NoOpWritesNothing(t *testing.T) {
	e := newSvcEnv(t)
	i := e.published(acme(), FlagApply)
	n := e.auditCount(i.ID)
	res, err := e.svc.SetFlag(e.ctx(), e.admin, e.org, i.ID, i.Version, flagPtr(FlagApply), nil, e.key())
	if err != nil || res.IPO.Version != i.Version || e.auditCount(i.ID) != n {
		t.Fatalf("err=%v version=%d audits=%d->%d", err, res.IPO.Version, n, e.auditCount(i.ID))
	}
}

func TestSetFlag_BR_IPO_02_PublishedCannotClearFlag(t *testing.T) {
	e := newSvcEnv(t)
	i := e.published(acme(), FlagApply)
	_, err := e.svc.SetFlag(e.ctx(), e.admin, e.org, i.ID, i.Version, nil, nil, e.key())
	_ = wantValidation2(t, err, "ipo.publish_incomplete")
	if r := e.mustRow(i.ID); r.Flag == nil || r.Version != i.Version {
		t.Fatalf("flag must stay (US3.7): %+v", r)
	}
}

func TestSetFlag_BR_IPO_02_NoteWithoutFlagRejected(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(acme())
	note := "orphan note"
	_, err := e.svc.SetFlag(e.ctx(), e.admin, e.org, i.ID, i.Version, nil, &note, e.key())
	_ = wantValidation2(t, err, "ipo.invalid_value")
}

func TestSetFlag_FR006_StaleAndNotFound(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(acme())
	if _, err := e.svc.SetFlag(e.ctx(), e.admin, e.org, i.ID, i.Version+3, flagPtr(FlagApply), nil, e.key()); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("err = %v", err)
	}
	if _, err := e.svc.SetFlag(e.ctx(), e.admin, e.org, newID(), 1, flagPtr(FlagApply), nil, e.key()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestUnpublish_BR_IPO_02_HiddenStatusAndStepsUnaffected(t *testing.T) {
	e := newSvcEnv(t)
	i := e.published(acme(), FlagApply)
	i = e.override(i, StatusOpen, "seed")
	r := e.listen()
	steps := fmt.Sprint(e.stepJobs(i.ID))
	res, err := e.svc.Unpublish(e.ctx(), e.admin, e.org, i.ID, i.Version, e.key())
	if err != nil {
		t.Fatalf("Unpublish: %v", err)
	}
	if res.IPO.Published || res.IPO.Eligible() || res.IPO.Status != StatusOpen {
		t.Fatalf("state wrong: %+v", res.IPO)
	}
	if fmt.Sprint(e.stepJobs(i.ID)) != steps {
		t.Fatalf("steps must be unaffected (US3.8)")
	}
	if len(r.Elig) != 1 || r.Elig[0].Eligible || r.Elig[0].Cause != "unpublish" {
		t.Fatalf("signals = %+v", r.Elig)
	}
	if _, err := e.svc.GetPublished(e.ctx(), e.org, i.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("client Get after unpublish: %v (US7.5)", err)
	}
	if p, _ := e.svc.ListPublished(e.ctx(), e.org, ClientFilter{}); len(p.Items) != 0 {
		t.Fatalf("client list must be empty")
	}
	a := e.audits(i.ID)
	if a[len(a)-1].Action != "ipo.unpublished" {
		t.Fatalf("audit = %v", e.actions(i.ID))
	}
}

func TestUnpublish_BR_IPO_02_DraftIsNoChange(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(acme())
	if _, err := e.svc.Unpublish(e.ctx(), e.admin, e.org, i.ID, i.Version, e.key()); !errors.Is(err, ErrNoChange) {
		t.Fatalf("err = %v", err)
	}
}

func TestUnpublish_BR_IPO_02_DoNotApplySendsNoSignal(t *testing.T) {
	e := newSvcEnv(t)
	i := e.published(acme(), FlagDoNotApply)
	r := e.listen()
	if _, err := e.svc.Unpublish(e.ctx(), e.admin, e.org, i.ID, i.Version, e.key()); err != nil {
		t.Fatal(err)
	}
	r.wantNoSignals(t)
}
