package ipos

import (
	"testing"
	"time"
)

// US4.6: downtime catch-up. FR-031: late steps still run, in order, once each.

func TestLifecycle_BR_IPO_03_CatchUpAfterDowntime(t *testing.T) {
	e := newSvcEnvJobs(t)
	e.setClock(10, 9, 0)
	i := e.create(acme())
	r := e.listen()

	e.tickAt(16, 11, 0) // no ticks at all between 10 Nov 09:00 and now
	wantStatus(t, e, i.ID, StatusAllotmentOut)

	sa := e.statusAudits(i.ID)
	if len(sa) != 3 {
		t.Fatalf("status audits = %d, want exactly 3 (US4.6)", len(sa))
	}
	steps := [][2]string{{"UPCOMING", "OPEN"}, {"OPEN", "CLOSED"}, {"CLOSED", "ALLOTMENT_OUT"}}
	instants := []time.Time{e.at(11, 10, 0), e.at(13, 17, 0), e.at(16, 10, 0)}
	for k, a := range sa {
		if a.Before["status"] != steps[k][0] || a.After["status"] != steps[k][1] || a.ActorKind != "system" {
			t.Fatalf("audit %d = %v -> %v, want %v", k, a.Before, a.After, steps[k])
		}
		sf, _ := a.After["scheduled_for"].(string)
		at, err := time.Parse(time.RFC3339, sf)
		if err != nil || !at.Equal(instants[k]) {
			t.Fatalf("audit %d scheduled_for = %q, want %s", k, sf, instants[k])
		}
	}
	if len(r.Status) != 3 || r.Status[0].To != StatusOpen || r.Status[1].To != StatusClosed || r.Status[2].To != StatusAllotmentOut {
		t.Fatalf("signals out of order: %+v", r.Status)
	}
	for _, k := range []string{jobOpen, jobClose, jobAllotment} {
		if st := e.h.Job(k, stepKey(i.ID, k)).Status; st != "done" {
			t.Fatalf("%s job = %s, want done exactly once", k, st)
		}
	}
	if st := e.h.Job(jobList, stepKey(i.ID, jobList)).Status; st != "pending" {
		t.Fatalf("listing step = %s, want still pending", st)
	}
	e.tickAt(16, 11, 5)
	if len(e.statusAudits(i.ID)) != 3 {
		t.Fatalf("a second tick must change nothing")
	}
}

func TestLifecycle_BR_IPO_03_CatchUpDoesNotExpireStateChanges(t *testing.T) {
	// Steps have no expiry: even weeks late they run (002 FR-014).
	e := newSvcEnvJobs(t)
	e.setClock(10, 9, 0)
	i := e.create(acme())
	e.tickAt(30, 12, 0)
	wantStatus(t, e, i.ID, StatusListed)
	if len(e.statusAudits(i.ID)) != 4 {
		t.Fatalf("status audits = %d, want 4", len(e.statusAudits(i.ID)))
	}
}
