package ipos

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"pgregory.net/rapid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
)

// BR-IPO-03 time travel: fake clock, real scheduler Tick, real handlers (RegisterJobs).

func newLifecycle(t *testing.T) (*svcEnv, IPO) {
	t.Helper()
	e := newSvcEnvJobs(t)
	e.setClock(10, 9, 0)
	return e, e.create(acme())
}

func TestLifecycle_BR_IPO_03_OpenBoundary(t *testing.T) {
	t.Run("09:59 stays, 10:00 opens", func(t *testing.T) {
		e, i := newLifecycle(t)
		e.tickAt(11, 9, 59)
		wantStatus(t, e, i.ID, StatusUpcoming)
		if e.auditCount(i.ID) != 1 {
			t.Fatalf("no audit expected before the instant")
		}
		e.tickAt(11, 10, 0)
		wantStatus(t, e, i.ID, StatusOpen)
		sa := e.statusAudits(i.ID)
		if len(sa) != 1 {
			t.Fatalf("status audits = %d", len(sa))
		}
		a := sa[0]
		if a.ActorKind != "system" || a.ActorID != nil || a.Before["status"] != "UPCOMING" || a.After["status"] != "OPEN" || a.After["status_source"] != "schedule" {
			t.Fatalf("audit = %+v / %+v", a.Before, a.After)
		}
		sf, _ := a.After["scheduled_for"].(string)
		at, err := time.Parse(time.RFC3339, sf)
		if err != nil || !at.Equal(e.at(11, 10, 0)) {
			t.Fatalf("scheduled_for = %q, want 11 Nov 10:00 IST (US4.2)", sf)
		}
		if row := e.mustRow(i.ID); row.StatusSource != "schedule" {
			t.Fatalf("status_source = %s", row.StatusSource)
		}
	})
	t.Run("missed 10:00 tick, 10:04 opens", func(t *testing.T) {
		e, i := newLifecycle(t)
		e.tickAt(11, 10, 4)
		wantStatus(t, e, i.ID, StatusOpen)
	})
	t.Run("IST not UTC: 04:29 UTC stays, 04:30 UTC opens", func(t *testing.T) {
		e, i := newLifecycle(t)
		e.h.Clock.Set(time.Date(2026, 11, 11, 4, 29, 0, 0, time.UTC))
		e.h.Tick()
		wantStatus(t, e, i.ID, StatusUpcoming)
		e.h.Clock.Set(time.Date(2026, 11, 11, 4, 30, 0, 0, time.UTC))
		e.h.Tick()
		wantStatus(t, e, i.ID, StatusOpen)
	})
}

func TestLifecycle_BR_IPO_03_CloseBoundary(t *testing.T) {
	e, i := newLifecycle(t)
	e.tickAt(11, 10, 0)
	e.tickAt(13, 16, 59)
	wantStatus(t, e, i.ID, StatusOpen)
	e.tickAt(13, 17, 0)
	wantStatus(t, e, i.ID, StatusClosed)
}

func TestLifecycle_BR_IPO_03_AllotmentAndListing(t *testing.T) {
	e, i := newLifecycle(t)
	e.tickAt(11, 10, 0)
	e.tickAt(13, 17, 0)
	e.tickAt(16, 9, 59)
	wantStatus(t, e, i.ID, StatusClosed)
	e.tickAt(16, 10, 0)
	wantStatus(t, e, i.ID, StatusAllotmentOut)
	e.tickAt(18, 9, 59)
	wantStatus(t, e, i.ID, StatusAllotmentOut)
	e.tickAt(18, 10, 0)
	wantStatus(t, e, i.ID, StatusListed)
	if n := len(e.statusAudits(i.ID)); n != 4 {
		t.Fatalf("status audits = %d, want 4", n)
	}
}

func TestLifecycle_BR_IPO_03_NeverSettledByTime(t *testing.T) {
	e, i := newLifecycle(t)
	e.tickAt(18, 10, 0)
	wantStatus(t, e, i.ID, StatusListed)
	n := e.auditCount(i.ID)
	e.h.RunTicks(e.at(18, 12, 0), e.at(18, 12, 0).Add(30*24*time.Hour), 12*time.Hour)
	wantStatus(t, e, i.ID, StatusListed)
	if e.auditCount(i.ID) != n {
		t.Fatalf("time must write nothing after LISTED (US4.5, US6.8)")
	}
}

func TestLifecycle_BR_IPO_03_MissingAllotmentWaitsInClosed(t *testing.T) {
	e := newSvcEnvJobs(t)
	e.setClock(10, 9, 0)
	d := acme()
	d.Dates = Dates{Open: dt(11), Close: dt(13)}
	i := e.create(d)
	e.tickAt(20, 12, 0)
	wantStatus(t, e, i.ID, StatusClosed)
	d2 := draftOf(i)
	d2.Dates.Allotment = dt(25)
	e.update(e.currentID(i.ID), d2)
	e.tickAt(25, 10, 0)
	wantStatus(t, e, i.ID, StatusAllotmentOut)
}

func TestLifecycle_BR_IPO_03_DraftsAdvanceAndStayHidden(t *testing.T) {
	e, i := newLifecycle(t)
	e.tickAt(13, 17, 0)
	wantStatus(t, e, i.ID, StatusClosed)
	if _, err := e.svc.GetPublished(e.ctx(), e.org, i.ID); err == nil {
		t.Fatalf("a draft must stay invisible to clients (D-07)")
	}
	if p, err := e.svc.ListPublished(e.ctx(), e.org, ClientFilter{}); err != nil || len(p.Items) != 0 {
		t.Fatalf("client list = %v err=%v", p.Items, err)
	}
}

func TestLifecycle_BR_IPO_03_PublishedAdvancesToo(t *testing.T) {
	e := newSvcEnvJobs(t)
	e.setClock(10, 9, 0)
	i := e.published(acme(), FlagApply)
	e.tickAt(11, 10, 0)
	wantStatus(t, e, i.ID, StatusOpen)
	if got := e.currentID(i.ID); !got.Published || got.Version != i.Version+1 {
		t.Fatalf("publication must be untouched and version bumped: %+v", got)
	}
}

func TestLifecycle_BR_IPO_03_DuplicateRunIsNoop(t *testing.T) {
	t.Run("handler run twice", func(t *testing.T) {
		e := newSvcEnv(t)
		e.setClock(10, 9, 0)
		i := e.create(acme())
		c := e.captureHandlers()
		e.setClock(11, 10, 5)
		for k := range 2 {
			if err := e.runStep(c, i.ID, jobOpen); err != nil {
				t.Fatalf("run %d: %v", k, err)
			}
		}
		wantStatus(t, e, i.ID, StatusOpen)
		if n := len(e.statusAudits(i.ID)); n != 1 {
			t.Fatalf("status audits = %d, want 1 (US4.9)", n)
		}
	})
	t.Run("concurrent runs", func(t *testing.T) {
		e := newSvcEnv(t)
		e.setClock(10, 9, 0)
		i := e.create(acme())
		c := e.captureHandlers()
		e.setClock(11, 10, 5)
		var wg sync.WaitGroup
		errs := make([]error, 4)
		for k := range errs {
			wg.Add(1)
			go func() { defer wg.Done(); errs[k] = e.runStep(c, i.ID, jobOpen) }()
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatalf("run: %v", err)
			}
		}
		if n := len(e.statusAudits(i.ID)); n != 1 || e.mustRow(i.ID).Version != 2 {
			t.Fatalf("audits=%d version=%d, want exactly one change", n, e.mustRow(i.ID).Version)
		}
	})
}

func TestLifecycle_BR_IPO_03_StepReadsCurrentDates(t *testing.T) {
	// FR-032: a stale job whose date has since moved later does nothing.
	e := newSvcEnv(t)
	e.setClock(10, 9, 0)
	i := e.create(acme())
	c := e.captureHandlers()
	stale := e.h.Job(jobOpen, stepKey(i.ID, jobOpen))
	d := draftOf(i)
	d.Dates.Open = dt(12)
	e.update(i, d)
	e.setClock(11, 10, 5)
	job := scheduler.Job{ID: stale.ID, OrgID: stale.Org, Kind: stale.Kind, Key: stale.Key, Subject: stale.Subject, Payload: stale.Payload, RunAt: stale.RunAt}
	if err := e.h.Txr.InTx(e.ctx(), func(ctx context.Context) error { return c.handlers[jobOpen](ctx, job) }); err != nil {
		t.Fatalf("handler: %v", err)
	}
	wantStatus(t, e, i.ID, StatusUpcoming)
}

func TestLifecycle_BR_IPO_03_PastDatesOnCreate(t *testing.T) {
	e := newSvcEnvJobs(t)
	e.setClock(14, 10, 0) // open and close are already past, allotment is not
	i := e.create(acme())
	e.h.Tick()
	wantStatus(t, e, i.ID, StatusClosed)
	sa := e.statusAudits(i.ID)
	if len(sa) != 2 || sa[0].After["status"] != "OPEN" || sa[1].After["status"] != "CLOSED" {
		t.Fatalf("status audits = %d, want OPEN then CLOSED (US4.10)", len(sa))
	}
	e.tickAt(19, 10, 0)
	wantStatus(t, e, i.ID, StatusListed)
}

func TestRegisterJobs_BR_IPO_03_KindsAndOptions(t *testing.T) {
	e := newSvcEnv(t)
	c := e.captureHandlers()
	for _, k := range stepKinds {
		if c.handlers[k] == nil {
			t.Fatalf("kind %s not registered", k)
		}
		if o := c.opts[k]; o.MaxAttempts != 0 || o.Recurring != nil {
			t.Fatalf("%s options = %+v, want zero KindOptions{} (8 attempts, not recurring)", k, o)
		}
	}
	if len(c.handlers) != 4 {
		t.Fatalf("registered %d kinds, want exactly 4", len(c.handlers))
	}
}

func TestRegisterJobs_BR_IPO_03_MissingIPOIsNoop(t *testing.T) {
	e := newSvcEnv(t)
	c := e.captureHandlers()
	job := scheduler.Job{ID: uuid.New(), OrgID: e.org, Kind: jobOpen, Key: "ipo:x:open", Subject: "ipo:x",
		Payload: []byte(`{"ipo_id":"` + uuid.NewString() + `","armed_at":"2026-11-10T00:00:00Z"}`)}
	e.setClock(11, 10, 5)
	if err := e.h.Txr.InTx(e.ctx(), func(ctx context.Context) error { return c.handlers[jobOpen](ctx, job) }); err != nil {
		t.Fatalf("a deleted IPO's job must be a quiet no-op: %v", err)
	}
}

// Property: after a single tick, the status equals the highest step whose IST instant has
// passed (status-from-dates oracle), and is never SETTLED.
func TestLifecycle_BR_IPO_03_Properties(t *testing.T) {
	e := newSvcEnvJobs(t)
	rapid.Check(t, func(rt *rapid.T) {
		openD := istdate.Date{Year: 2026, Month: time.November, Day: rapid.IntRange(2, 12).Draw(rt, "open")}
		closeD := openD.AddDays(rapid.IntRange(0, 5).Draw(rt, "closeGap"))
		allotD := closeD.AddDays(rapid.IntRange(1, 5).Draw(rt, "allotGap"))
		listD := allotD.AddDays(rapid.IntRange(1, 5).Draw(rt, "listGap"))
		now := schedulerNov(1).Add(time.Duration(rapid.IntRange(0, 40*24*60).Draw(rt, "minutes")) * time.Minute)

		e.h.Clock.Set(schedulerNov(1).Add(-48 * time.Hour))
		d := minimalDraft("Prop")
		d.Dates = Dates{Open: &openD, Close: &closeD, Allotment: &allotD, Listing: &listD}
		i := e.create(d)
		e.h.Clock.Set(now)
		e.h.Tick()

		want := StatusUpcoming
		for _, st := range lifecycleSteps {
			if at, ok := stepInstant(st, IPO{Dates: d.Dates}); ok && !at.After(now) {
				want = st.Target
			}
		}
		got := e.mustRow(i.ID).Status
		if got != string(want) || got == string(StatusSettled) {
			rt.Fatalf("status = %s, want %s at %s", got, want, now)
		}
	})
}

func schedulerNov(day int) time.Time {
	return istdate.Date{Year: 2026, Month: time.November, Day: day}.At(0, 0)
}
