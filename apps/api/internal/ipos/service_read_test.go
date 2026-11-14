package ipos

import (
	"errors"
	"testing"
	"time"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
)

// US7 (service side), US9: reads, filters, pages, delete rules, org isolation.

func (e *svcEnv) seedCatalogue() {
	e.t.Helper()
	mk := func(name string, open *istdate.Date) Draft {
		d := acme()
		d.Name = name
		d.Dates = Dates{Open: open}
		return d
	}
	// A: published APPLY OPEN, open 11 Nov
	a := e.published(mk("A", dt(11)), FlagApply)
	_ = e.override(a, StatusOpen, "seed")
	// B: published DO NOT APPLY UPCOMING, open 20 Nov
	_ = e.published(mk("B", dt(20)), FlagDoNotApply)
	// C: draft, no flag, no dates
	_ = e.create(minimalDraft("C"))
	// D: draft APPLY CLOSED, open 2 Dec
	d := e.create(Draft{Name: "D", Exchanges: []Exchange{ExchangeNSE}, Dates: Dates{Open: &istdate.Date{Year: 2026, Month: time.December, Day: 2}}})
	d = e.setFlag(d, FlagApply, nil)
	_ = e.override(d, StatusClosed, "seed")
	// E: published APPLY LISTED, open 5 Oct
	oct := &istdate.Date{Year: 2026, Month: time.October, Day: 5}
	ee := e.published(mk("E", oct), FlagApply)
	_ = e.override(ee, StatusListed, "seed")
}

func listNames(t *testing.T, p Page[IPO], err error) []string {
	t.Helper()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return names(p.Items)
}

func sameSet(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	m := map[string]int{}
	for _, g := range got {
		m[g]++
	}
	for _, w := range want {
		m[w]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}

func TestList_FR009_Filters(t *testing.T) {
	e := newSvcEnv(t)
	e.seedCatalogue()
	status := func(s Status) *Status { return &s }
	yes, no := true, false
	nov1, nov30 := istdate.Date{Year: 2026, Month: time.November, Day: 1}, istdate.Date{Year: 2026, Month: time.November, Day: 30}
	flag := func(s string) *string { return &s }
	tests := []struct {
		name string
		f    AdminFilter
		want []string
	}{
		{"status CLOSED", AdminFilter{Status: status(StatusClosed)}, []string{"D"}},
		{"published", AdminFilter{Published: &yes}, []string{"A", "B", "E"}},
		{"draft", AdminFilter{Published: &no}, []string{"C", "D"}},
		{"flag APPLY", AdminFilter{Flag: flag("APPLY")}, []string{"A", "D", "E"}},
		{"flag DO_NOT_APPLY", AdminFilter{Flag: flag("DO_NOT_APPLY")}, []string{"B"}},
		{"flag NONE", AdminFilter{Flag: flag("NONE")}, []string{"C"}},
		{"November IST month", AdminFilter{MonthStart: &nov1, MonthEnd: &nov30}, []string{"A", "B"}},
		{"published and APPLY", AdminFilter{Published: &yes, Flag: flag("APPLY")}, []string{"A", "E"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := e.svc.List(e.ctx(), e.org, tc.f)
			if got := listNames(t, p, err); !sameSet(got, tc.want...) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
	p, err := e.svc.List(e.ctx(), e.org, AdminFilter{})
	got := listNames(t, p, err)
	want := []string{"C", "D", "B", "A", "E"} // open date newest first, undated first (US9.1)
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("default order = %v, want %v", got, want)
		}
	}
}

func TestList_FR009_KeysetPagesCoverEverythingOnce(t *testing.T) {
	e := newSvcEnv(t)
	e.seedCatalogue()
	seen := map[string]bool{}
	cursor, pages := "", 0
	for {
		p, err := e.svc.List(e.ctx(), e.org, AdminFilter{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		pages++
		if len(p.Items) > 2 {
			t.Fatalf("page %d has %d items, limit 2", pages, len(p.Items))
		}
		for _, i := range p.Items {
			if seen[i.Name] {
				t.Fatalf("%s repeated across pages", i.Name)
			}
			seen[i.Name] = true
		}
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
		if pages > 10 {
			t.Fatal("pagination does not terminate")
		}
	}
	if len(seen) != 5 || pages != 3 {
		t.Fatalf("seen=%d pages=%d, want 5 over 3 pages", len(seen), pages)
	}
}

func TestList_FR009_BR_IPO_02_ClientSeesPublishedOnly(t *testing.T) {
	e := newSvcEnv(t)
	e.seedCatalogue()
	other := e.h.NewOrg("Other")
	if _, err := e.svc.Create(e.ctx(), e.admin, other, minimalDraft("Z"), e.key()); err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.ListPublished(e.ctx(), e.org, ClientFilter{})
	if got := listNames(t, p, err); !sameSet(got, "A", "B", "E") {
		t.Fatalf("client list = %v, want A B E only (US7.1, SC-004)", got)
	}
	open := StatusOpen
	p, err = e.svc.ListPublished(e.ctx(), e.org, ClientFilter{Status: &open})
	if got := listNames(t, p, err); !sameSet(got, "A") {
		t.Fatalf("OPEN filter = %v, want A (US7.2)", got)
	}
}

func TestGet_BR_IPO_02_AnyIPOAndOrgIsolation(t *testing.T) {
	e := newSvcEnv(t)
	draft := e.create(acme())
	got, err := e.svc.Get(e.ctx(), e.org, draft.ID)
	if err != nil || got.ID != draft.ID || got.Published {
		t.Fatalf("Get draft: %+v err=%v", got, err)
	}
	other := e.h.NewOrg("Other")
	if _, err := e.svc.Get(e.ctx(), other, draft.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org Get err = %v, want ErrNotFound", err)
	}
	if _, err := e.svc.Get(e.ctx(), e.org, newID()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown Get err = %v", err)
	}
}

func TestGet_BR_IPO_02_DraftLooksUnknownToClients(t *testing.T) {
	e := newSvcEnv(t)
	draft := e.create(acme())
	pub := e.published(acme(), FlagApply)
	_, errDraft := e.svc.GetPublished(e.ctx(), e.org, draft.ID)
	_, errUnknown := e.svc.GetPublished(e.ctx(), e.org, newID())
	if !errors.Is(errDraft, ErrNotFound) || !errors.Is(errUnknown, ErrNotFound) {
		t.Fatalf("draft err=%v unknown err=%v, both must be ErrNotFound (US7.3)", errDraft, errUnknown)
	}
	if errDraft.Error() != errUnknown.Error() {
		t.Fatalf("errors must be indistinguishable: %q vs %q", errDraft, errUnknown)
	}
	if got, err := e.svc.GetPublished(e.ctx(), e.org, pub.ID); err != nil || got.ID != pub.ID {
		t.Fatalf("published GetPublished: %v", err)
	}
}

func TestDelete_FR008_NeverPublishedDraft(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(acme())
	if len(e.stepJobs(i.ID)) != 4 {
		t.Fatalf("precondition: 4 steps")
	}
	replayed, err := e.svc.Delete(e.ctx(), e.admin, e.org, i.ID, i.Version, e.key())
	if err != nil || replayed {
		t.Fatalf("Delete: replayed=%v err=%v", replayed, err)
	}
	if _, ok := e.row(i.ID); ok {
		t.Fatalf("row must be gone")
	}
	if len(e.stepJobs(i.ID)) != 0 {
		t.Fatalf("all lifecycle steps must be cancelled (US9.2)")
	}
	a := e.audits(i.ID)
	if len(a) != 2 || a[1].Action != "ipo.deleted" || a[1].ActorKind != "admin" {
		t.Fatalf("audit = %v, want created, deleted", e.actions(i.ID))
	}
	if !contains2(string(a[1].BeforeRaw), "Acme Ltd") || (a[1].AfterRaw != nil && string(a[1].AfterRaw) != "null") {
		t.Fatalf("deleted audit before=%s after=%s: snapshot expected", a[1].BeforeRaw, a[1].AfterRaw)
	}
	if _, err := e.svc.Get(e.ctx(), e.org, i.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete: %v", err)
	}
}

func contains2(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestDelete_FR008_EverPublishedRejected(t *testing.T) {
	e := newSvcEnv(t)
	pub := e.published(acme(), FlagApply)
	if _, err := e.svc.Delete(e.ctx(), e.admin, e.org, pub.ID, pub.Version, e.key()); !errors.Is(err, ErrPublishedNotDeletable) {
		t.Fatalf("published delete err = %v (US9.3)", err)
	}
	un, err := e.svc.Unpublish(e.ctx(), e.admin, e.org, pub.ID, pub.Version, e.key())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Delete(e.ctx(), e.admin, e.org, un.IPO.ID, un.IPO.Version, e.key()); !errors.Is(err, ErrPublishedNotDeletable) {
		t.Fatalf("ever-published delete err = %v, want ErrPublishedNotDeletable (D-08)", err)
	}
	if _, ok := e.row(pub.ID); !ok || len(e.stepJobs(pub.ID)) == 0 {
		t.Fatalf("IPO and its steps must remain")
	}
}

func TestDelete_FR006_StaleNotFoundAndOrg(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(acme())
	if _, err := e.svc.Delete(e.ctx(), e.admin, e.org, i.ID, i.Version+5, e.key()); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("stale delete err = %v", err)
	}
	other := e.h.NewOrg("Other")
	if _, err := e.svc.Delete(e.ctx(), e.admin, other, i.ID, i.Version, e.key()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org delete err = %v", err)
	}
	if _, ok := e.row(i.ID); !ok {
		t.Fatalf("row must survive")
	}
}
