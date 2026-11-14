package ipos

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// US1, US2 (create side): BR-IPO-01 (mainboard only), BR-IPO-03 (steps at creation), FR-007.

func TestCreate_BR_IPO_03_DraftDefaultsAndAudit(t *testing.T) {
	e := newSvcEnv(t)
	res, err := e.svc.Create(e.ctx(), e.admin, e.org, minimalDraft("Acme Ltd"), e.key())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	i := res.IPO
	if i.ID == uuid.Nil || i.OrgID != e.org || i.Name != "Acme Ltd" {
		t.Fatalf("identity wrong: %+v", i)
	}
	if i.Status != StatusUpcoming || i.Published || i.Flag != nil || i.Version != 1 {
		t.Fatalf("defaults wrong: status=%s published=%v flag=%v version=%d", i.Status, i.Published, i.Flag, i.Version)
	}
	if i.Source != "manual" || i.LastSyncedAt != nil || i.SourceRef != nil {
		t.Fatalf("provenance wrong: source=%q ref=%v synced=%v (US1.11)", i.Source, i.SourceRef, i.LastSyncedAt)
	}
	if res.Replayed || len(res.Warnings) != 0 {
		t.Fatalf("replayed=%v warnings=%v", res.Replayed, res.Warnings)
	}
	if got := e.iposCount(); got != 1 {
		t.Fatalf("rows = %d, want 1", got)
	}
	a := e.audits(i.ID)
	if len(a) != 1 || a[0].Action != "ipo.created" || a[0].ActorKind != "admin" || a[0].ActorID == nil || *a[0].ActorID != e.admin.ID {
		t.Fatalf("audit = %+v, want one ipo.created by the admin", a)
	}
	if a[0].BeforeRaw != nil && string(a[0].BeforeRaw) != "null" {
		t.Fatalf("created audit before = %s, want null", a[0].BeforeRaw)
	}
	if !strings.Contains(string(a[0].AfterRaw), "Acme Ltd") {
		t.Fatalf("created audit after lacks the snapshot: %s", a[0].AfterRaw)
	}
	if len(e.stepJobs(i.ID)) != 0 {
		t.Fatalf("a draft without dates must schedule no steps")
	}
}

func TestCreate_FR004_ExactPaiseAndMinApplication(t *testing.T) {
	e := newSvcEnv(t)
	res, err := e.svc.Create(e.ctx(), e.admin, e.org, acme(), e.key())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	i := res.IPO
	if i.PriceBandFloorPaise == nil || *i.PriceBandFloorPaise != 100_000 || *i.PriceBandCapPaise != 105_000 || *i.LotSize != 14 {
		t.Fatalf("exact values lost: %+v", i)
	}
	if v, ok := i.MinApplicationPaise(); !ok || v != 1_470_000 {
		t.Fatalf("min application = %d,%v want 1470000 (Rs 14,700)", v, ok)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("Rs 14,700 is inside the range, got warnings %v", res.Warnings)
	}
	var cap, floor int64
	if err := e.h.Pool.QueryRow(e.ctx(), `SELECT price_band_cap_paise, price_band_floor_paise FROM ipos WHERE id=$1`, i.ID).Scan(&cap, &floor); err != nil || cap != 105_000 || floor != 100_000 {
		t.Fatalf("stored paise = %d/%d err=%v", floor, cap, err)
	}
}

func TestCreate_FR005_WarningBounds(t *testing.T) {
	tests := []struct {
		name    string
		lot     int32
		cap     int64
		warn    bool
		wantVal int64
	}{
		{"9,999 warns", 1, 999_900, true, 999_900},
		{"10,000 exact no warning", 1, 1_000_000, false, 0},
		{"20,000 exact no warning", 1, 2_000_000, false, 0},
		{"20,001 warns", 1, 2_000_100, true, 2_000_100},
		{"25,000 warns with text", 50, 50_000, true, 2_500_000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newSvcEnv(t)
			d := minimalDraft("W")
			d.LotSize, d.PriceBandFloorPaise, d.PriceBandCapPaise = &tc.lot, &tc.cap, &tc.cap
			res, err := e.svc.Create(e.ctx(), e.admin, e.org, d, e.key())
			if err != nil {
				t.Fatalf("a warning must never block the save: %v", err)
			}
			if !tc.warn {
				if len(res.Warnings) != 0 {
					t.Fatalf("warnings = %v, want none", res.Warnings)
				}
				return
			}
			if len(res.Warnings) != 1 || res.Warnings[0].Code != "ipo.min_application_out_of_range" {
				t.Fatalf("warnings = %+v", res.Warnings)
			}
			if res.Warnings[0].Params["value_paise"] != tc.wantVal {
				t.Fatalf("params = %v", res.Warnings[0].Params)
			}
			if tc.wantVal == 2_500_000 && !strings.Contains(res.Warnings[0].Detail, "₹25,000") {
				t.Fatalf("detail = %q must show ₹25,000", res.Warnings[0].Detail)
			}
			if e.iposCount() != 1 {
				t.Fatalf("IPO must be saved despite the warning")
			}
		})
	}
}

func TestCreate_BR_IPO_01_SMEStoresNothingAndSchedulesNothing(t *testing.T) {
	tests := []struct {
		name string
		segs []Exchange
		code string
	}{
		{"NSE Emerge", []Exchange{"NSE_EMERGE"}, "ipo.sme_not_allowed"},
		{"BSE SME", []Exchange{"BSE_SME"}, "ipo.sme_not_allowed"},
		{"mainboard plus SME", []Exchange{ExchangeNSE, "BSE_SME"}, "ipo.sme_not_allowed"},
		{"unknown", []Exchange{"MCX"}, "ipo.invalid_segment"},
		{"missing", nil, "ipo.invalid_segment"},
		{"duplicate", []Exchange{ExchangeNSE, ExchangeNSE}, "ipo.invalid_segment"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newSvcEnv(t)
			d := acme()
			d.Exchanges = tc.segs
			_, err := e.svc.Create(e.ctx(), e.admin, e.org, d, e.key())
			_ = wantValidation2(t, err, tc.code)
			if e.iposCount() != 0 || e.h.Count("scheduled_jobs", "true") != 0 || e.h.Count("audit_log", "entity = 'ipo'") != 0 {
				t.Fatalf("nothing may be stored, scheduled or audited (US2.1)")
			}
		})
	}
}

func TestCreate_BR_IPO_01_AcceptsMainboardAndSMEInName(t *testing.T) {
	for _, tc := range []struct {
		name string
		segs []Exchange
	}{
		{"NSE", []Exchange{ExchangeNSE}},
		{"BSE", []Exchange{ExchangeBSE}},
		{"both", []Exchange{ExchangeNSE, ExchangeBSE}},
		{"SME Solutions Ltd", []Exchange{ExchangeNSE}}, // US2.7: recognition is by segment only
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newSvcEnv(t)
			d := minimalDraft("SME Solutions Ltd")
			d.Exchanges = tc.segs
			if _, err := e.svc.Create(e.ctx(), e.admin, e.org, d, e.key()); err != nil {
				t.Fatalf("Create: %v", err)
			}
		})
	}
}

func TestCreate_FR003_InvalidValuesStoreNothing(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*Draft)
		code string
	}{
		{"floor above cap", func(d *Draft) { d.PriceBandFloorPaise, d.PriceBandCapPaise = p64(105_000), p64(100_000) }, "ipo.invalid_value"},
		{"only floor", func(d *Draft) { d.PriceBandFloorPaise, d.PriceBandCapPaise = p64(100_000), nil }, "ipo.invalid_value"},
		{"only cap", func(d *Draft) { d.PriceBandFloorPaise, d.PriceBandCapPaise = nil, p64(100_000) }, "ipo.invalid_value"},
		{"lot zero", func(d *Draft) { d.LotSize = p32(0) }, "ipo.invalid_value"},
		{"negative price", func(d *Draft) { d.IssuePricePaise = p64(-1) }, "ipo.invalid_value"},
		{"blank name", func(d *Draft) { d.Name = "   " }, "ipo.invalid_value"},
		{"open after close", func(d *Draft) { d.Dates.Open, d.Dates.Close = dt(14), dt(13) }, "ipo.date_order"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newSvcEnv(t)
			d := acme()
			tc.mut(&d)
			_, err := e.svc.Create(e.ctx(), e.admin, e.org, d, e.key())
			_ = wantValidation2(t, err, tc.code)
			if e.iposCount() != 0 || e.h.Count("scheduled_jobs", "true") != 0 {
				t.Fatalf("nothing may change on a rejected create")
			}
		})
	}
}

func TestCreate_FR003_FixedPriceBandAccepted(t *testing.T) {
	e := newSvcEnv(t)
	d := minimalDraft("Fixed")
	d.PriceBandFloorPaise, d.PriceBandCapPaise = p64(50_000), p64(50_000)
	if _, err := e.svc.Create(e.ctx(), e.admin, e.org, d, e.key()); err != nil {
		t.Fatalf("floor = cap must be accepted (US1.7): %v", err)
	}
}

func TestCreate_US1_10_DuplicateNamesAllowed(t *testing.T) {
	e := newSvcEnv(t)
	a, b := e.create(minimalDraft("Twin Ltd")), e.create(minimalDraft("Twin Ltd"))
	if a.ID == b.ID || e.iposCount() != 2 {
		t.Fatalf("two IPOs with one name must both exist")
	}
}

func TestCreate_BR_IPO_03_SchedulesStepsForSetDates(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(acme())
	jobs := e.stepJobs(i.ID)
	want := map[string]time.Time{
		jobOpen: e.at(11, 10, 0), jobClose: e.at(13, 17, 0), jobAllotment: e.at(16, 10, 0), jobList: e.at(18, 10, 0),
	}
	if len(jobs) != 4 {
		t.Fatalf("pending steps = %v, want exactly open, close, allotment, list (no refund/credit step)", jobs)
	}
	for k, at := range want {
		if !jobs[k].Equal(at) {
			t.Fatalf("%s run_at = %s, want %s (IST)", k, jobs[k], at)
		}
		row := e.h.Job(k, stepKey(i.ID, k))
		if row.Subject != "ipo:"+i.ID.String() || row.ExpiresAt != nil {
			t.Fatalf("%s subject=%q expires=%v", k, row.Subject, row.ExpiresAt)
		}
		if !strings.Contains(string(row.Payload), i.ID.String()) || !strings.Contains(string(row.Payload), "armed_at") {
			t.Fatalf("%s payload = %s, want ipo_id and armed_at", k, row.Payload)
		}
	}
}

func TestCreate_FR007_IdempotentSequential(t *testing.T) {
	e := newSvcEnv(t)
	first, err := e.svc.Create(e.ctx(), e.admin, e.org, acme(), "create-key-1")
	if err != nil || first.Replayed {
		t.Fatalf("first: %v replayed=%v", err, first.Replayed)
	}
	second, err := e.svc.Create(e.ctx(), e.admin, e.org, acme(), "create-key-1")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !second.Replayed || second.IPO.ID != first.IPO.ID {
		t.Fatalf("replay must return the same IPO flagged Replayed: %+v", second)
	}
	if e.iposCount() != 1 || e.auditCount(first.IPO.ID) != 1 || e.jobRows(first.IPO.ID) != 4 {
		t.Fatalf("rows=%d audits=%d jobs=%d, want 1/1/4", e.iposCount(), e.auditCount(first.IPO.ID), e.jobRows(first.IPO.ID))
	}
}

func TestCreate_FR007_IdempotentConcurrent(t *testing.T) {
	e := newSvcEnv(t)
	const n = 8
	var wg sync.WaitGroup
	results := make([]Result, n)
	errs := make([]error, n)
	for k := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[k], errs[k] = e.svc.Create(e.ctx(), e.admin, e.org, acme(), "concurrent-key")
		}()
	}
	wg.Wait()
	fresh := 0
	for k := range n {
		if errs[k] != nil {
			t.Fatalf("call %d: %v", k, errs[k])
		}
		if results[k].IPO.ID != results[0].IPO.ID {
			t.Fatalf("calls returned different IPOs")
		}
		if !results[k].Replayed {
			fresh++
		}
	}
	if fresh != 1 || e.iposCount() != 1 || e.auditCount(results[0].IPO.ID) != 1 {
		t.Fatalf("fresh=%d rows=%d audits=%d, want 1/1/1", fresh, e.iposCount(), e.auditCount(results[0].IPO.ID))
	}
}

func TestCreate_FR007_KeyIsScopedPerOrg(t *testing.T) {
	e := newSvcEnv(t)
	other := e.h.NewOrg("Other")
	a, err := e.svc.Create(e.ctx(), e.admin, e.org, acme(), "shared-key-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.svc.Create(e.ctx(), e.admin, other, acme(), "shared-key-1")
	if err != nil || b.Replayed || b.IPO.ID == a.IPO.ID || b.IPO.OrgID != other {
		t.Fatalf("same key in another org must create a second IPO: %+v err=%v", b, err)
	}
	_ = fmt.Sprint()
}
