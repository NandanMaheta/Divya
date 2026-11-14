package ipos

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"pgregory.net/rapid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
)

var allStatuses = []Status{StatusUpcoming, StatusOpen, StatusClosed, StatusAllotmentOut, StatusListed, StatusSettled}

func drawDate(t *rapid.T, label string) *istdate.Date {
	if !rapid.Bool().Draw(t, label+"_set") {
		return nil
	}
	return dt(rapid.IntRange(1, 28).Draw(t, label))
}

func drawStepIPO(t *rapid.T) (IPO, time.Time) {
	i := IPO{
		ID: uuid.New(), Status: rapid.SampledFrom(allStatuses).Draw(t, "status"),
		Dates: Dates{Open: drawDate(t, "open"), Close: drawDate(t, "close"), Allotment: drawDate(t, "allot"), Listing: drawDate(t, "list")},
	}
	now := istAt(1, 0, 0).Add(time.Duration(rapid.IntRange(0, 40*24*60).Draw(t, "minutes")) * time.Minute)
	return i, now
}

// Forward-only: whatever a step decides, the new status ranks strictly above the current one and
// is the step's own target. Time never produces SETTLED (FR-032, FR-033).
func TestStepDecision_BR_IPO_03_ForwardOnlyNeverSettled(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		i, now := drawStepIPO(t)
		st := rapid.SampledFrom(allSteps).Draw(t, "step")
		got, fires := stepDecision(st, i, now, stepPayload{IPOID: i.ID, ArmedAt: istAt(1, 0, 0)})
		if !fires {
			return
		}
		if got != st.Target {
			t.Fatalf("fired to %s, step target is %s", got, st.Target)
		}
		if got == StatusSettled {
			t.Fatalf("a step must never set SETTLED")
		}
		if got.Rank() <= i.Status.Rank() {
			t.Fatalf("moved %s -> %s is not forward", i.Status, got)
		}
		inst, ok := stepInstant(st, i)
		if !ok || inst.After(now) {
			t.Fatalf("fired without a due instant (ok=%v inst=%s now=%s)", ok, inst, now)
		}
	})
}

// Idempotent: applying every due step in any order, any number of times, reaches the same final
// status as applying them once in lifecycle order, and a second pass changes nothing (US4.9).
func TestStepDecision_BR_IPO_03_IdempotentAndOrderIndependent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		i, now := drawStepIPO(t)
		start := i.Status
		order := rapid.SliceOfN(rapid.SampledFrom(allSteps), 4, 12).Draw(t, "order")
		p := stepPayload{IPOID: i.ID, ArmedAt: istAt(1, 0, 0)}

		cur := i
		for _, st := range order {
			if to, fires := stepDecision(st, cur, now, p); fires {
				if to.Rank() <= cur.Status.Rank() {
					t.Fatalf("backwards %s -> %s", cur.Status, to)
				}
				cur.Status = to
			}
		}
		// A second full pass in lifecycle order must change nothing once the first pass ran every step.
		after := cur
		for _, st := range allSteps {
			if to, fires := stepDecision(st, after, now, p); fires {
				after.Status = to
			}
		}
		// Reference: one pass in lifecycle order.
		ref := i
		for _, st := range allSteps {
			if to, fires := stepDecision(st, ref, now, p); fires {
				ref.Status = to
			}
		}
		if after.Status != ref.Status {
			t.Fatalf("final status %s depends on order; lifecycle-order pass gives %s", after.Status, ref.Status)
		}
		if after.Status.Rank() < start.Rank() {
			t.Fatalf("status went backwards %s -> %s", start, after.Status)
		}
		for _, st := range allSteps {
			if _, fires := stepDecision(st, ref, now, p); fires && ref.Status.Rank() < st.Target.Rank() {
				// a still-due step must not remain after a full pass
				t.Fatalf("step %s still due after a full pass", st.Kind)
			}
		}
	})
}

// A step never fires before its instant, whatever the status.
func TestStepDecision_BR_IPO_03_NeverBeforeInstant(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		i, _ := drawStepIPO(t)
		st := rapid.SampledFrom(allSteps).Draw(t, "step")
		inst, ok := stepInstant(st, i)
		if !ok {
			if _, fires := stepDecision(st, i, istAt(28, 23, 59), stepPayload{ArmedAt: istAt(1, 0, 0)}); fires {
				t.Fatalf("a step without a date fired")
			}
			return
		}
		before := inst.Add(-time.Duration(rapid.IntRange(1, 5000).Draw(t, "early")) * time.Minute)
		if _, fires := stepDecision(st, i, before, stepPayload{ArmedAt: istAt(1, 0, 0)}); fires {
			t.Fatalf("%s fired %s before its instant", st.Kind, inst.Sub(before))
		}
	})
}

// stepDiff: every scheduled step has a set date that differs from the old one, every cancelled
// step had a date that is now cleared, and the two sets are disjoint.
func TestStepDiff_BR_IPO_03_Properties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		old := Dates{Open: drawDate(t, "o_open"), Close: drawDate(t, "o_close"), Allotment: drawDate(t, "o_allot"), Listing: drawDate(t, "o_list")}
		upd := Dates{Open: drawDate(t, "n_open"), Close: drawDate(t, "n_close"), Allotment: drawDate(t, "n_allot"), Listing: drawDate(t, "n_list")}
		sched, cancel := stepDiff(old, upd)
		seen := map[string]bool{}
		for _, s := range sched {
			seen[s.Kind] = true
			a, aok := stepInstant(s, IPO{Dates: old})
			b, bok := stepInstant(s, IPO{Dates: upd})
			if !bok || (aok && a.Equal(b)) {
				t.Fatalf("%s scheduled although its date did not change to a set value", s.Kind)
			}
		}
		for _, s := range cancel {
			if seen[s.Kind] {
				t.Fatalf("%s is both scheduled and cancelled", s.Kind)
			}
			if _, aok := stepInstant(s, IPO{Dates: old}); !aok {
				t.Fatalf("%s cancelled but had no date before", s.Kind)
			}
			if _, bok := stepInstant(s, IPO{Dates: upd}); bok {
				t.Fatalf("%s cancelled but its date is still set", s.Kind)
			}
		}
		// Completeness: a change always yields exactly one action for that step.
		for _, st := range allSteps {
			a, aok := stepInstant(st, IPO{Dates: old})
			b, bok := stepInstant(st, IPO{Dates: upd})
			changed := aok != bok || (aok && !a.Equal(b))
			acted := seen[st.Kind]
			for _, c := range cancel {
				if c.Kind == st.Kind {
					acted = true
				}
			}
			if changed != acted {
				t.Fatalf("%s changed=%v but acted=%v", st.Kind, changed, acted)
			}
		}
	})
}
