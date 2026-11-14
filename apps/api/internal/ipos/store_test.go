package ipos

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/dbtest"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
)

// These tests exercise the data-model section 6 queries through Store, inside a transaction
// carried by the context (db.TxRunner). Assumptions the contracts leave open:
//   - a missing row, or one of another org, gives ErrNotFound;
//   - a version-guarded update that matches no row gives ErrStaleVersion or ErrNotFound (the store
//     cannot tell the two apart without a second query) and changes nothing;
//   - InsertIPO on an existing (org, idempotency key) returns created=false and writes nothing;
//   - ListIPOs* return up to Limit+1 rows (the extra row tells the service there is a next page);
//   - month bounds MonthStart and MonthEnd are inclusive IST civil dates on open_date.

type storeEnv struct {
	t     *testing.T
	pool  *pgxpool.Pool
	tx    db.TxRunner
	store *Store
	org   uuid.UUID
	now   time.Time
}

func newStoreEnv(t *testing.T) *storeEnv {
	t.Helper()
	pool := dbtest.New(t)
	e := &storeEnv{t: t, pool: pool, tx: db.NewTxRunner(pool), store: &Store{}, now: time.Date(2026, 11, 14, 4, 30, 0, 0, time.UTC)}
	e.org = e.newOrg("Operator")
	return e
}

func (e *storeEnv) newOrg(name string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(e.t.Context(), `INSERT INTO orgs (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		e.t.Fatalf("insert org: %v", err)
	}
	return id
}

func (e *storeEnv) in(fn func(ctx context.Context)) {
	e.t.Helper()
	if err := e.tx.InTx(e.t.Context(), func(ctx context.Context) error { fn(ctx); return nil }); err != nil {
		e.t.Fatalf("tx: %v", err)
	}
}

func sdraft(name string, open *istdate.Date) Draft {
	return Draft{Name: name, Exchanges: []Exchange{ExchangeNSE}, Dates: Dates{Open: open}}
}

func (e *storeEnv) insert(org uuid.UUID, d Draft) IPO {
	e.t.Helper()
	var out IPO
	e.in(func(ctx context.Context) {
		got, created, err := e.store.InsertIPO(ctx, org, d, nil, e.now)
		if err != nil || !created {
			e.t.Fatalf("InsertIPO(%q): created=%v err=%v", d.Name, created, err)
		}
		out = got
	})
	return out
}

func (e *storeEnv) get(org, id uuid.UUID) (IPO, error) {
	var out IPO
	var err error
	e.in(func(ctx context.Context) { out, err = e.store.GetIPO(ctx, org, id) })
	return out, err
}

func names(ipos []IPO) []string {
	out := make([]string, len(ipos))
	for i, x := range ipos {
		out[i] = x.Name
	}
	return out
}

func wantNames(t *testing.T, got []IPO, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if !reflect.DeepEqual(names(got), want) {
		t.Fatalf("names = %v, want %v", names(got), want)
	}
}

func isStaleOrMissing(err error) bool {
	return errors.Is(err, ErrStaleVersion) || errors.Is(err, ErrNotFound)
}

func TestQueries_InsertAndGet_RoundTripsEveryFact(t *testing.T) {
	e := newStoreEnv(t)
	d := Draft{
		Name: "Acme Industries", Exchanges: []Exchange{ExchangeBSE, ExchangeNSE}, NSESymbol: ptrStr("ACME"),
		IssueSizePaise: p64(120_000_000_000), PriceBandFloorPaise: p64(14000), PriceBandCapPaise: p64(14700), IssuePricePaise: p64(14500),
		LotSize:   p32(100),
		Dates:     Dates{Open: dt(18), Close: dt(20), Allotment: dt(23), Refund: dt(24), DematCredit: dt(24), Listing: dt(25)},
		Registrar: ptrStr("KFin Technologies"), LeadManagers: []string{"Axis Capital", "ICICI Securities"},
		OfferDocumentURL: ptrStr("https://example.com/rhp.pdf"),
	}
	created := e.insert(e.org, d)

	if created.ID == uuid.Nil || created.OrgID != e.org {
		t.Fatalf("id/org not set: %+v", created)
	}
	if created.Status != StatusUpcoming || created.StatusSource != SourceSchedule || created.Version != 1 ||
		created.Published || created.Flag != nil || created.FlagNote != nil || created.FirstPublishedAt != nil || created.Source != "manual" {
		t.Fatalf("draft defaults wrong: %+v", created)
	}
	if !created.CreatedAt.Equal(e.now) || !created.UpdatedAt.Equal(e.now) || !created.StatusChangedAt.Equal(e.now) {
		t.Fatalf("instants must come from the injected clock: %+v", created)
	}
	got, err := e.get(e.org, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != d.Name || !reflect.DeepEqual(got.Exchanges, d.Exchanges) || !reflect.DeepEqual(got.NSESymbol, d.NSESymbol) ||
		!reflect.DeepEqual(got.IssueSizePaise, d.IssueSizePaise) || !reflect.DeepEqual(got.PriceBandFloorPaise, d.PriceBandFloorPaise) ||
		!reflect.DeepEqual(got.PriceBandCapPaise, d.PriceBandCapPaise) || !reflect.DeepEqual(got.IssuePricePaise, d.IssuePricePaise) ||
		!reflect.DeepEqual(got.LotSize, d.LotSize) || !reflect.DeepEqual(got.Dates, d.Dates) ||
		!reflect.DeepEqual(got.Registrar, d.Registrar) || !reflect.DeepEqual(got.LeadManagers, d.LeadManagers) ||
		!reflect.DeepEqual(got.OfferDocumentURL, d.OfferDocumentURL) {
		t.Fatalf("round trip lost data:\n got %+v\nwant %+v", got, d)
	}
}

func TestQueries_InsertAllowsDuplicateNames(t *testing.T) {
	e := newStoreEnv(t)
	a := e.insert(e.org, sdraft("Same Name Ltd", nil))
	b := e.insert(e.org, sdraft("Same Name Ltd", nil))
	if a.ID == b.ID {
		t.Fatal("two drafts with one name are two rows (US1.10)")
	}
}

func TestQueries_Get_OrgIsolationAndUnknownID(t *testing.T) {
	e := newStoreEnv(t)
	other := e.newOrg("Other")
	mine := e.insert(e.org, sdraft("Mine", nil))

	if _, err := e.get(other, mine.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another org must see ErrNotFound, got %v", err)
	}
	if _, err := e.get(e.org, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id must be ErrNotFound, got %v", err)
	}
	e.in(func(ctx context.Context) {
		if _, err := e.store.GetIPOForUpdate(ctx, other, mine.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetIPOForUpdate other org: %v", err)
		}
		if _, err := e.store.GetIPOForShare(ctx, other, mine.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetIPOForShare other org: %v", err)
		}
		if got, err := e.store.GetIPOForUpdate(ctx, e.org, mine.ID); err != nil || got.ID != mine.ID {
			t.Fatalf("GetIPOForUpdate own org: %v %v", got.ID, err)
		}
		if got, err := e.store.GetIPOForShare(ctx, e.org, mine.ID); err != nil || got.ID != mine.ID {
			t.Fatalf("GetIPOForShare own org: %v %v", got.ID, err)
		}
	})
}

func TestQueries_InsertIdempotencyKey(t *testing.T) {
	e := newStoreEnv(t)
	other := e.newOrg("Other")
	key := "create-key-0001"
	var first IPO
	e.in(func(ctx context.Context) {
		got, created, err := e.store.InsertIPO(ctx, e.org, sdraft("Keyed", nil), &key, e.now)
		if err != nil || !created {
			t.Fatalf("first insert: created=%v err=%v", created, err)
		}
		first = got
	})
	e.in(func(ctx context.Context) {
		if _, created, err := e.store.InsertIPO(ctx, e.org, sdraft("Keyed again", nil), &key, e.now); err != nil || created {
			t.Fatalf("replayed key: created=%v err=%v, want created=false and no error", created, err)
		}
		found, err := e.store.GetIPOByCreateKey(ctx, e.org, key)
		if err != nil || found.ID != first.ID || found.Name != "Keyed" {
			t.Fatalf("GetIPOByCreateKey = %+v, %v; want the first row", found, err)
		}
		if _, err := e.store.GetIPOByCreateKey(ctx, e.org, "never-used-key"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown key: %v", err)
		}
		if _, err := e.store.GetIPOByCreateKey(ctx, other, key); !errors.Is(err, ErrNotFound) {
			t.Fatalf("key of another org must not resolve: %v", err)
		}
		if _, created, err := e.store.InsertIPO(ctx, other, sdraft("Other org keyed", nil), &key, e.now); err != nil || !created {
			t.Fatalf("same key in another org is a different create: created=%v err=%v", created, err)
		}
	})
	var page []IPO
	e.in(func(ctx context.Context) {
		var err error
		page, err = e.store.ListIPOsAdmin(ctx, e.org, AdminFilter{Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
	})
	wantNames(t, page, "Keyed")
}

func TestQueries_UpdateFacts_VersionGuard(t *testing.T) {
	e := newStoreEnv(t)
	row := e.insert(e.org, sdraft("Before", nil))
	later := e.now.Add(time.Hour)

	e.in(func(ctx context.Context) {
		changed := sdraft("After", dt(18))
		changed.LotSize = p32(100)
		got, err := e.store.UpdateIPOFacts(ctx, e.org, row.ID, FactsUpdate{Draft: changed, Version: 1, Now: later})
		if err != nil {
			t.Fatalf("update with the current version: %v", err)
		}
		if got.Name != "After" || got.Version != 2 || !got.UpdatedAt.Equal(later) || got.LotSize == nil || *got.LotSize != 100 || got.Dates.Open == nil {
			t.Fatalf("update result wrong: %+v", got)
		}
	})
	e.in(func(ctx context.Context) {
		_, err := e.store.UpdateIPOFacts(ctx, e.org, row.ID, FactsUpdate{Draft: sdraft("Stale", nil), Version: 1, Now: later})
		if !isStaleOrMissing(err) {
			t.Fatalf("stale version must be rejected, got %v", err)
		}
		_, err = e.store.UpdateIPOFacts(ctx, e.newOrg("Other"), row.ID, FactsUpdate{Draft: sdraft("Hijack", nil), Version: 2, Now: later})
		if !isStaleOrMissing(err) {
			t.Fatalf("another org must not update, got %v", err)
		}
	})
	if got, _ := e.get(e.org, row.ID); got.Name != "After" || got.Version != 2 {
		t.Fatalf("rejected updates changed the row: %+v", got)
	}
}

func TestQueries_SetFlag_VersionGuard(t *testing.T) {
	e := newStoreEnv(t)
	row := e.insert(e.org, sdraft("Flagged", nil))
	apply := FlagApply
	note := "Strong GMP"
	e.in(func(ctx context.Context) {
		got, err := e.store.SetIPOFlag(ctx, e.org, row.ID, 1, &apply, &note, e.now.Add(time.Minute))
		if err != nil || got.Flag == nil || *got.Flag != FlagApply || got.FlagNote == nil || *got.FlagNote != note || got.Version != 2 {
			t.Fatalf("SetIPOFlag = %+v, %v", got, err)
		}
		if _, err := e.store.SetIPOFlag(ctx, e.org, row.ID, 1, &apply, nil, e.now); !isStaleOrMissing(err) {
			t.Fatalf("stale flag change: %v", err)
		}
		cleared, err := e.store.SetIPOFlag(ctx, e.org, row.ID, 2, nil, nil, e.now.Add(2*time.Minute))
		if err != nil || cleared.Flag != nil || cleared.FlagNote != nil || cleared.Version != 3 {
			t.Fatalf("clearing the flag on a draft: %+v, %v", cleared, err)
		}
	})
}

func TestQueries_SetPublished_KeepsFirstPublishedAt(t *testing.T) {
	e := newStoreEnv(t)
	d := sdraft("Publishable", dt(18))
	d.Dates.Close, d.LotSize, d.PriceBandFloorPaise, d.PriceBandCapPaise = dt(20), p32(100), p64(14000), p64(14700)
	row := e.insert(e.org, d)
	apply := FlagApply
	t1, t2 := e.now.Add(time.Hour), e.now.Add(2*time.Hour)

	e.in(func(ctx context.Context) {
		if _, err := e.store.SetIPOFlag(ctx, e.org, row.ID, 1, &apply, nil, e.now); err != nil {
			t.Fatal(err)
		}
		pub, err := e.store.SetIPOPublished(ctx, e.org, row.ID, 2, true, t1)
		if err != nil || !pub.Published || pub.FirstPublishedAt == nil || !pub.FirstPublishedAt.Equal(t1) || pub.Version != 3 {
			t.Fatalf("publish: %+v, %v", pub, err)
		}
		unpub, err := e.store.SetIPOPublished(ctx, e.org, row.ID, 3, false, t2)
		if err != nil || unpub.Published || unpub.FirstPublishedAt == nil || !unpub.FirstPublishedAt.Equal(t1) {
			t.Fatalf("unpublish must keep first_published_at: %+v, %v", unpub, err)
		}
		repub, err := e.store.SetIPOPublished(ctx, e.org, row.ID, 4, true, t2.Add(time.Hour))
		if err != nil || repub.FirstPublishedAt == nil || !repub.FirstPublishedAt.Equal(t1) {
			t.Fatalf("republish must keep the first publish moment: %+v, %v", repub, err)
		}
		if _, err := e.store.SetIPOPublished(ctx, e.org, row.ID, 1, false, t2); !isStaleOrMissing(err) {
			t.Fatalf("stale publish change: %v", err)
		}
	})
}

// lastOverrideAt reads IPO.LastOverrideAt (data-model last_override_at). The S01 skeleton lacks the
// field; reflection keeps this file compiling and fails with a clear reason until it exists.
func lastOverrideAt(t *testing.T, i IPO) *time.Time {
	t.Helper()
	f := reflect.ValueOf(i).FieldByName("LastOverrideAt")
	if !f.IsValid() {
		t.Fatalf("ipos.IPO has no LastOverrideAt *time.Time field (FR-036; data-model column last_override_at)")
	}
	v, _ := f.Interface().(*time.Time)
	return v
}

func TestQueries_SetStatus_SourceAndOverrideMoment(t *testing.T) {
	e := newStoreEnv(t)
	row := e.insert(e.org, sdraft("Moving", nil))
	t1, t2 := e.now.Add(time.Hour), e.now.Add(2*time.Hour)

	e.in(func(ctx context.Context) {
		sched, err := e.store.SetIPOStatus(ctx, e.org, row.ID, StatusUpdate{Status: StatusOpen, Source: SourceSchedule, Version: 1, Now: t1})
		if err != nil || sched.Status != StatusOpen || sched.StatusSource != SourceSchedule || !sched.StatusChangedAt.Equal(t1) || sched.Version != 2 {
			t.Fatalf("schedule step: %+v, %v", sched, err)
		}
		if lastOverrideAt(t, sched) != nil {
			t.Fatalf("a scheduled change must not set last_override_at")
		}
		ov, err := e.store.SetIPOStatus(ctx, e.org, row.ID, StatusUpdate{Status: StatusUpcoming, Source: SourceAdmin, Version: 2, Now: t2, OverrideAt: &t2})
		if err != nil || ov.Status != StatusUpcoming || ov.StatusSource != SourceAdmin || ov.Version != 3 {
			t.Fatalf("override backwards: %+v, %v", ov, err)
		}
		if at := lastOverrideAt(t, ov); at == nil || !at.Equal(t2) {
			t.Fatalf("override must record last_override_at = %v, got %v", t2, at)
		}
		later, err := e.store.SetIPOStatus(ctx, e.org, row.ID, StatusUpdate{Status: StatusOpen, Source: SourceSchedule, Version: 3, Now: t2.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if at := lastOverrideAt(t, later); at == nil || !at.Equal(t2) {
			t.Fatalf("last_override_at is never cleared, got %v", at)
		}
		if _, err := e.store.SetIPOStatus(ctx, e.org, row.ID, StatusUpdate{Status: StatusSettled, Source: SourceSettlement, Version: 1, Now: t2}); !isStaleOrMissing(err) {
			t.Fatalf("stale status change: %v", err)
		}
		settled, err := e.store.SetIPOStatus(ctx, e.org, row.ID, StatusUpdate{Status: StatusSettled, Source: SourceSettlement, Version: 4, Now: t2.Add(2 * time.Hour)})
		if err != nil || settled.Status != StatusSettled || settled.StatusSource != SourceSettlement {
			t.Fatalf("settlement: %+v, %v", settled, err)
		}
	})
}

func TestQueries_Delete_Guards(t *testing.T) {
	e := newStoreEnv(t)
	other := e.newOrg("Other")
	draft := e.insert(e.org, sdraft("Never published", nil))

	e.in(func(ctx context.Context) {
		if n, err := e.store.DeleteIPO(ctx, e.org, draft.ID, 99); err != nil || n != 0 {
			t.Fatalf("wrong version: rows=%d err=%v", n, err)
		}
		if n, err := e.store.DeleteIPO(ctx, other, draft.ID, 1); err != nil || n != 0 {
			t.Fatalf("another org: rows=%d err=%v", n, err)
		}
		if n, err := e.store.DeleteIPO(ctx, e.org, draft.ID, 1); err != nil || n != 1 {
			t.Fatalf("delete draft: rows=%d err=%v", n, err)
		}
	})
	if _, err := e.get(e.org, draft.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted row must be gone: %v", err)
	}
}

func TestQueries_Delete_EverPublishedIsNotDeleted(t *testing.T) {
	e := newStoreEnv(t)
	d := sdraft("Was published", dt(18))
	d.Dates.Close, d.LotSize, d.IssuePricePaise = dt(20), p32(100), p64(14700)
	row := e.insert(e.org, d)
	apply := FlagApply
	e.in(func(ctx context.Context) {
		if _, err := e.store.SetIPOFlag(ctx, e.org, row.ID, 1, &apply, nil, e.now); err != nil {
			t.Fatal(err)
		}
		if _, err := e.store.SetIPOPublished(ctx, e.org, row.ID, 2, true, e.now); err != nil {
			t.Fatal(err)
		}
		if _, err := e.store.SetIPOPublished(ctx, e.org, row.ID, 3, false, e.now); err != nil {
			t.Fatal(err)
		}
		n, err := e.store.DeleteIPO(ctx, e.org, row.ID, 4)
		if err != nil || n != 0 {
			t.Fatalf("an ever-published IPO is not deletable: rows=%d err=%v (the query guards on first_published_at)", n, err)
		}
	})
	if _, err := e.get(e.org, row.ID); err != nil {
		t.Fatalf("row must survive: %v", err)
	}
}

// seedList creates, in insertion order: n1 and n2 undated, a Nov-20, b and c Nov-18, then edge
// months (31 Oct, 1 Nov, 30 Nov, 1 Dec). Admin order is open_date DESC NULLS FIRST, id DESC.
func seedList(e *storeEnv) {
	rows := []struct {
		name string
		open *istdate.Date
	}{
		{"n1", nil}, {"n2", nil},
		{"a-nov20", dt(20)}, {"b-nov18", dt(18)}, {"c-nov18", dt(18)},
		{"oct31", &istdate.Date{Year: 2026, Month: time.October, Day: 31}},
		{"nov01", dt(1)}, {"nov30", dt(30)},
		{"dec01", &istdate.Date{Year: 2026, Month: time.December, Day: 1}},
	}
	for _, r := range rows {
		e.insert(e.org, sdraft(r.name, r.open))
	}
}

func TestQueries_ListAdmin_OrderNullsFirstThenNewestOpenThenIDDesc(t *testing.T) {
	e := newStoreEnv(t)
	seedList(e)
	var got []IPO
	e.in(func(ctx context.Context) {
		var err error
		got, err = e.store.ListIPOsAdmin(ctx, e.org, AdminFilter{Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
	})
	wantNames(t, got, "n2", "n1", "dec01", "nov30", "a-nov20", "c-nov18", "b-nov18", "nov01", "oct31")
}

func TestQueries_ListAdmin_LimitFetchesOneExtraRow(t *testing.T) {
	e := newStoreEnv(t)
	seedList(e)
	e.in(func(ctx context.Context) {
		got, err := e.store.ListIPOsAdmin(ctx, e.org, AdminFilter{Limit: 3})
		if err != nil {
			t.Fatal(err)
		}
		wantNames(t, got, "n2", "n1", "dec01", "nov30")
		all, err := e.store.ListIPOsAdmin(ctx, e.org, AdminFilter{Limit: 20})
		if err != nil || len(all) != 9 {
			t.Fatalf("limit above the row count returns every row: %d, %v", len(all), err)
		}
	})
}

func TestQueries_ListAdmin_MonthFilterUsesISTCivilBoundaries(t *testing.T) {
	e := newStoreEnv(t)
	seedList(e)
	start, end := nov(1), nov(30)
	e.in(func(ctx context.Context) {
		got, err := e.store.ListIPOsAdmin(ctx, e.org, AdminFilter{MonthStart: &start, MonthEnd: &end, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		wantNames(t, got, "nov30", "a-nov20", "c-nov18", "b-nov18", "nov01") // bounds inclusive, undated rows excluded
	})
}

func TestQueries_ListAdmin_StatusPublishedAndFlagFilters(t *testing.T) {
	e := newStoreEnv(t)
	apply, dont := FlagApply, FlagDoNotApply
	mk := func(name string, open *istdate.Date) IPO {
		d := sdraft(name, open)
		d.Dates.Close, d.LotSize, d.IssuePricePaise = dt(28), p32(100), p64(14700)
		if open == nil {
			d.Dates.Open = dt(10)
		}
		return e.insert(e.org, d)
	}
	pub := mk("pub-apply", dt(11))
	pubNo := mk("pub-dont", dt(12))
	draftApply := mk("draft-apply", dt(13))
	_ = mk("draft-noflag", dt(14))
	e.in(func(ctx context.Context) {
		set := func(r IPO, f ApplyFlag) IPO {
			got, err := e.store.SetIPOFlag(ctx, e.org, r.ID, 1, &f, nil, e.now)
			if err != nil {
				t.Fatal(err)
			}
			return got
		}
		a := set(pub, apply)
		b := set(pubNo, dont)
		set(draftApply, apply)
		if _, err := e.store.SetIPOPublished(ctx, e.org, a.ID, a.Version, true, e.now); err != nil {
			t.Fatal(err)
		}
		if _, err := e.store.SetIPOPublished(ctx, e.org, b.ID, b.Version, true, e.now); err != nil {
			t.Fatal(err)
		}
		if _, err := e.store.SetIPOStatus(ctx, e.org, a.ID, StatusUpdate{Status: StatusOpen, Source: SourceSchedule, Version: a.Version + 1, Now: e.now}); err != nil {
			t.Fatal(err)
		}

		list := func(f AdminFilter) []IPO {
			f.Limit = 50
			got, err := e.store.ListIPOsAdmin(ctx, e.org, f)
			if err != nil {
				t.Fatal(err)
			}
			return got
		}
		yes, no := true, false
		open := StatusOpen
		flagApply, flagDont, flagNone := "APPLY", "DO_NOT_APPLY", "NONE"

		wantNames(t, list(AdminFilter{}), "draft-noflag", "draft-apply", "pub-dont", "pub-apply")
		wantNames(t, list(AdminFilter{Published: &yes}), "pub-dont", "pub-apply")
		wantNames(t, list(AdminFilter{Published: &no}), "draft-noflag", "draft-apply")
		wantNames(t, list(AdminFilter{Status: &open}), "pub-apply")
		wantNames(t, list(AdminFilter{Flag: &flagApply}), "draft-apply", "pub-apply")
		wantNames(t, list(AdminFilter{Flag: &flagDont}), "pub-dont")
		wantNames(t, list(AdminFilter{Flag: &flagNone}), "draft-noflag")
		wantNames(t, list(AdminFilter{Published: &no, Flag: &flagApply}), "draft-apply")
	})
}

func TestQueries_ListPublished_OnlyPublishedAndOrgScoped(t *testing.T) {
	e := newStoreEnv(t)
	other := e.newOrg("Other")
	apply := FlagApply
	mk := func(org uuid.UUID, name string, open *istdate.Date, publish bool) IPO {
		d := sdraft(name, open)
		d.Dates.Close, d.LotSize, d.IssuePricePaise = dt(28), p32(100), p64(14700)
		row := e.insert(org, d)
		if publish {
			e.in(func(ctx context.Context) {
				f, err := e.store.SetIPOFlag(ctx, org, row.ID, 1, &apply, nil, e.now)
				if err != nil {
					t.Fatal(err)
				}
				row, err = e.store.SetIPOPublished(ctx, org, row.ID, f.Version, true, e.now)
				if err != nil {
					t.Fatal(err)
				}
			})
		}
		return row
	}
	mk(e.org, "draft", dt(15), false)
	older := mk(e.org, "pub-older", dt(10), true)
	mk(e.org, "pub-newer", dt(16), true)
	mk(other, "other-org-published", dt(17), true)

	e.in(func(ctx context.Context) {
		got, err := e.store.ListPublishedIPOs(ctx, e.org, ClientFilter{Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		wantNames(t, got, "pub-newer", "pub-older")

		closed := StatusClosed
		got, err = e.store.ListPublishedIPOs(ctx, e.org, ClientFilter{Status: &closed, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		wantNames(t, got)

		if _, err := e.store.SetIPOStatus(ctx, e.org, older.ID, StatusUpdate{Status: StatusClosed, Source: SourceAdmin, Version: older.Version, Now: e.now, OverrideAt: &e.now}); err != nil {
			t.Fatal(err)
		}
		got, err = e.store.ListPublishedIPOs(ctx, e.org, ClientFilter{Status: &closed, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		wantNames(t, got, "pub-older")

		if _, err := e.store.GetPublishedIPO(ctx, e.org, older.ID); err != nil {
			t.Fatalf("published IPO is fetchable by clients: %v", err)
		}
	})
	if drafts, _ := func() ([]IPO, error) {
		var out []IPO
		var err error
		e.in(func(ctx context.Context) { out, err = e.store.ListIPOsAdmin(ctx, e.org, AdminFilter{Limit: 50}) })
		return out, err
	}(); len(drafts) != 3 {
		t.Fatalf("admin sees drafts too: %v", names(drafts))
	}
}

func TestQueries_GetPublished_DraftAndOtherOrgAreNotFound(t *testing.T) {
	e := newStoreEnv(t)
	other := e.newOrg("Other")
	draft := e.insert(e.org, sdraft("Draft", nil))
	e.in(func(ctx context.Context) {
		if _, err := e.store.GetPublishedIPO(ctx, e.org, draft.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("a draft looks unknown to clients: %v", err)
		}
		if _, err := e.store.GetPublishedIPO(ctx, other, draft.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("another org: %v", err)
		}
		if _, err := e.store.GetPublishedIPO(ctx, e.org, uuid.New()); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown id: %v", err)
		}
	})
}

func TestQueries_FindAuditByIdempotencyKey(t *testing.T) {
	e := newStoreEnv(t)
	other := e.newOrg("Other")
	row := e.insert(e.org, sdraft("Audited", nil))
	e.in(func(ctx context.Context) {
		err := audit.Write(ctx, audit.Entry{
			OrgID: e.org, Actor: audit.Actor{ID: uuid.New(), Kind: audit.KindAdmin}, Entity: "ipo", EntityID: row.ID,
			Action: "ipo.published", Before: map[string]any{"published": false}, After: map[string]any{"published": true, "idempotency_key": "publish-key-1"},
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	e.in(func(ctx context.Context) {
		id, found, err := e.store.FindIPOAuditByIdempotencyKey(ctx, e.org, row.ID, "ipo.published", "publish-key-1")
		if err != nil || !found || id == uuid.Nil {
			t.Fatalf("existing key: id=%v found=%v err=%v", id, found, err)
		}
		for name, c := range map[string]struct {
			org, ipo    uuid.UUID
			action, key string
		}{
			"other key":    {e.org, row.ID, "ipo.published", "publish-key-2"},
			"other action": {e.org, row.ID, "ipo.unpublished", "publish-key-1"},
			"other ipo":    {e.org, uuid.New(), "ipo.published", "publish-key-1"},
			"other org":    {other, row.ID, "ipo.published", "publish-key-1"},
		} {
			if _, found, err := e.store.FindIPOAuditByIdempotencyKey(ctx, c.org, c.ipo, c.action, c.key); err != nil || found {
				t.Fatalf("%s: found=%v err=%v, want not found and no error", name, found, err)
			}
		}
	})
}
