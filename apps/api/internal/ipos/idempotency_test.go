package ipos

import (
	"sync"
	"testing"
)

// FR-007 (replay of every mutation) and SC-005 (one audit row per mutation).

func TestIdempotency_FR007_ReplayEveryMutation(t *testing.T) {
	e := newSvcEnv(t)
	r := e.listen()
	i := e.create(acme())
	id := i.ID

	type step struct {
		name  string
		do    func(v int32, key string) (Result, error)
		audit string
	}
	note := "n"
	steps := []step{
		{"SetFlag", func(v int32, k string) (Result, error) {
			return e.svc.SetFlag(e.ctx(), e.admin, e.org, id, v, flagPtr(FlagApply), &note, k)
		}, "ipo.flag_set"},
		{"Update", func(v int32, k string) (Result, error) {
			d := draftOf(i)
			d.Name = "Acme Limited"
			return e.svc.Update(e.ctx(), e.admin, e.org, id, v, d, k)
		}, "ipo.updated"},
		{"Publish", func(v int32, k string) (Result, error) { return e.svc.Publish(e.ctx(), e.admin, e.org, id, v, k) }, "ipo.published"},
		{"OverrideStatus", func(v int32, k string) (Result, error) {
			return e.svc.OverrideStatus(e.ctx(), e.admin, e.org, id, v, StatusOpen, "why", k)
		}, "ipo.status_changed"},
		{"Unpublish", func(v int32, k string) (Result, error) { return e.svc.Unpublish(e.ctx(), e.admin, e.org, id, v, k) }, "ipo.unpublished"},
	}
	v := i.Version
	for k, s := range steps {
		key := e.key()
		first, err := s.do(v, key)
		if err != nil || first.Replayed {
			t.Fatalf("%s first: err=%v replayed=%v", s.name, err, first.Replayed)
		}
		audits := e.auditCount(id)
		d0, el0, st0 := r.counts()
		// replay with the same (now stale) version and the same key
		again, err := s.do(v, key)
		if err != nil || !again.Replayed {
			t.Fatalf("%s replay: err=%v replayed=%v, want success flagged Replayed", s.name, err, again.Replayed)
		}
		if again.IPO.Version != first.IPO.Version {
			t.Fatalf("%s replay returned version %d, want current %d", s.name, again.IPO.Version, first.IPO.Version)
		}
		if e.auditCount(id) != audits {
			t.Fatalf("%s replay wrote an audit row", s.name)
		}
		if d1, el1, st1 := r.counts(); d1 != d0 || el1 != el0 || st1 != st0 {
			t.Fatalf("%s replay re-sent signals", s.name)
		}
		if got := e.actions(id); got[len(got)-1] != s.audit || len(got) != k+2 {
			t.Fatalf("%s: audit = %v", s.name, got)
		}
		v = first.IPO.Version
	}
}

func TestIdempotency_FR007_DeleteReplays(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(acme())
	replayed, err := e.svc.Delete(e.ctx(), e.admin, e.org, i.ID, i.Version, "delete-key-1")
	if err != nil || replayed {
		t.Fatalf("first: %v %v", replayed, err)
	}
	replayed, err = e.svc.Delete(e.ctx(), e.admin, e.org, i.ID, i.Version, "delete-key-1")
	if err != nil || !replayed {
		t.Fatalf("replay: replayed=%v err=%v, want true,nil (204)", replayed, err)
	}
	if n := e.h.Count("audit_log", "entity = 'ipo' AND action = 'ipo.deleted'"); n != 1 {
		t.Fatalf("delete audits = %d", n)
	}
}

func TestIdempotency_FR007_KeyIsScopedPerAction(t *testing.T) {
	e := newSvcEnv(t)
	i := e.create(acme())
	flagged, err := e.svc.SetFlag(e.ctx(), e.admin, e.org, i.ID, i.Version, flagPtr(FlagApply), nil, "shared-key-9")
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.Publish(e.ctx(), e.admin, e.org, i.ID, flagged.IPO.Version, "shared-key-9")
	if err != nil || res.Replayed || !res.IPO.Published {
		t.Fatalf("a key used for another action must not replay: %+v err=%v", res, err)
	}
}

func TestIdempotency_FR007_ConcurrentPublishSameKey(t *testing.T) {
	e := newSvcEnv(t)
	r := e.listen()
	i := e.setFlag(e.create(acme()), FlagApply, nil)
	const n = 6
	var wg sync.WaitGroup
	res := make([]Result, n)
	errs := make([]error, n)
	for k := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res[k], errs[k] = e.svc.Publish(e.ctx(), e.admin, e.org, i.ID, i.Version, "publish-key-1")
		}()
	}
	wg.Wait()
	fresh := 0
	for k := range n {
		if errs[k] != nil {
			t.Fatalf("call %d: %v", k, errs[k])
		}
		if !res[k].Replayed {
			fresh++
		}
	}
	if fresh != 1 || len(r.Elig) != 1 || e.h.Count("audit_log", "entity_id = $1 AND action = 'ipo.published'", i.ID) != 1 {
		t.Fatalf("fresh=%d eligibility signals=%d, want exactly one effect", fresh, len(r.Elig))
	}
}

func TestAudit_SC005_OneEntryPerMutation(t *testing.T) {
	e := newSvcEnvJobs(t)
	e.setClock(10, 9, 0)
	i := e.create(acme())                                                                                  // ipo.created
	i = e.update(i, func() Draft { d := draftOf(i); d.Name = "Acme Limited"; return d }())                 // ipo.updated
	if _, err := e.svc.Update(e.ctx(), e.admin, e.org, i.ID, i.Version, draftOf(i), e.key()); err != nil { // no-op: none
		t.Fatal(err)
	}
	i = e.setFlag(i, FlagApply, nil)                                                                                     // ipo.flag_set
	if _, err := e.svc.SetFlag(e.ctx(), e.admin, e.org, i.ID, i.Version, flagPtr(FlagApply), nil, e.key()); err != nil { // no-op: none
		t.Fatal(err)
	}
	if _, err := e.svc.OverrideStatus(e.ctx(), e.admin, e.org, i.ID, i.Version, StatusUpcoming, "same", e.key()); err == nil { // rejected: none
		t.Fatal("same-status override must fail")
	}
	i = e.publish(i)                        // ipo.published
	i = e.override(i, StatusClosed, "seed") // ipo.status_changed
	e.tickAt(11, 10, 0)                     // open step: rank below CLOSED, no-op: none
	e.tickAt(16, 10, 0)                     // allotment step: ipo.status_changed
	i = e.currentID(i.ID)
	if _, err := e.svc.Unpublish(e.ctx(), e.admin, e.org, i.ID, i.Version, e.key()); err != nil { // ipo.unpublished
		t.Fatal(err)
	}
	e.wantActions(i.ID, "ipo.created", "ipo.updated", "ipo.flag_set", "ipo.published", "ipo.status_changed", "ipo.status_changed", "ipo.unpublished")

	del := e.create(minimalDraft("Doomed"))
	if _, err := e.svc.Delete(e.ctx(), e.admin, e.org, del.ID, del.Version, e.key()); err != nil {
		t.Fatal(err)
	}
	e.wantActions(del.ID, "ipo.created", "ipo.deleted")
}
