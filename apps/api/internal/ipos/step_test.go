package ipos

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
)

// The four lifecycle steps (data-model section 3). SETTLED is never a step target (FR-033).
var (
	stepOpen      = step{Kind: jobOpen, Target: StatusOpen}
	stepClose     = step{Kind: jobClose, Target: StatusClosed}
	stepAllotment = step{Kind: jobAllotment, Target: StatusAllotmentOut}
	stepList      = step{Kind: jobList, Target: StatusListed}
	allSteps      = []step{stepOpen, stepClose, stepAllotment, stepList}
)

func istAt(day, h, m int) time.Time {
	return istdate.Date{Year: 2026, Month: time.November, Day: day}.At(h, m)
}

func stepIPO(status Status, dates Dates) IPO {
	return IPO{ID: uuid.New(), OrgID: uuid.New(), Status: status, Dates: dates}
}

// setOverride sets IPO.LastOverrideAt (FR-036, data-model last_override_at). The S01 skeleton has
// no such field; reflection keeps this file compiling and fails with a clear reason until it exists.
func setOverride(t testing.TB, i *IPO, at time.Time) {
	t.Helper()
	f := reflect.ValueOf(i).Elem().FieldByName("LastOverrideAt")
	if !f.IsValid() {
		t.Fatalf("ipos.IPO has no LastOverrideAt *time.Time field (FR-036 needs it; data-model column last_override_at)")
	}
	f.Set(reflect.ValueOf(&at))
}

func TestStepInstant_BR_IPO_03(t *testing.T) {
	dates := Dates{Open: dt(18), Close: dt(20), Allotment: dt(23), Listing: dt(25), Refund: dt(24), DematCredit: dt(24)}
	i := stepIPO(StatusUpcoming, dates)
	tests := []struct {
		st   step
		want time.Time
	}{
		{stepOpen, istAt(18, 10, 0)},
		{stepClose, istAt(20, 17, 0)},
		{stepAllotment, istAt(23, 10, 0)},
		{stepList, istAt(25, 10, 0)},
	}
	for _, tc := range tests {
		t.Run(tc.st.Kind, func(t *testing.T) {
			got, ok := stepInstant(tc.st, i)
			if !ok {
				t.Fatalf("instant must exist when the date is set")
			}
			if !got.Equal(tc.want) {
				t.Fatalf("instant = %s, want %s", got, tc.want)
			}
		})
	}
	// 10:00 IST is 04:30 UTC and 17:00 IST is 11:30 UTC.
	if got, _ := stepInstant(stepOpen, i); got.UTC().Format(time.RFC3339) != "2026-11-18T04:30:00Z" {
		t.Fatalf("open instant UTC = %s", got.UTC().Format(time.RFC3339))
	}
	if got, _ := stepInstant(stepClose, i); got.UTC().Format(time.RFC3339) != "2026-11-20T11:30:00Z" {
		t.Fatalf("close instant UTC = %s", got.UTC().Format(time.RFC3339))
	}
}

func TestStepInstant_BR_IPO_03_UnsetDateHasNoInstant(t *testing.T) {
	for _, st := range allSteps {
		if _, ok := stepInstant(st, stepIPO(StatusUpcoming, Dates{Refund: dt(24), DematCredit: dt(24)})); ok {
			t.Fatalf("%s: no instant when its date is not set", st.Kind)
		}
	}
}

func TestStepDecision_BR_IPO_03(t *testing.T) {
	open, closeD, allot, list := dt(18), dt(20), dt(23), dt(25)
	full := Dates{Open: open, Close: closeD, Allotment: allot, Listing: list}
	armed := istAt(10, 9, 0)
	payload := stepPayload{IPOID: uuid.New(), ArmedAt: armed}

	tests := []struct {
		name   string
		st     step
		status Status
		dates  Dates
		now    time.Time
		want   Status
		fires  bool
	}{
		{"due exactly at the instant", stepOpen, StatusUpcoming, full, istAt(18, 10, 0), StatusOpen, true},
		{"one minute before the instant", stepOpen, StatusUpcoming, full, istAt(18, 9, 59), "", false},
		{"close fires at 17:00", stepClose, StatusOpen, full, istAt(20, 17, 0), StatusClosed, true},
		{"close not due at 16:59", stepClose, StatusOpen, full, istAt(20, 16, 59), "", false},
		{"allotment fires", stepAllotment, StatusClosed, full, istAt(23, 10, 0), StatusAllotmentOut, true},
		{"list fires", stepList, StatusAllotmentOut, full, istAt(25, 10, 0), StatusListed, true},
		{"late after downtime still fires", stepOpen, StatusUpcoming, full, istAt(19, 3, 0), StatusOpen, true},
		{"catch-up jumps several ranks", stepList, StatusUpcoming, full, istAt(26, 10, 0), StatusListed, true},
		{"already at target", stepOpen, StatusOpen, full, istAt(18, 10, 5), "", false},
		{"already past target", stepOpen, StatusClosed, full, istAt(18, 10, 5), "", false},
		{"listed never moves back to closed", stepClose, StatusListed, full, istAt(26, 10, 0), "", false},
		{"settled is never touched", stepList, StatusSettled, full, istAt(26, 10, 0), "", false},
		{"date cleared since arming", stepOpen, StatusUpcoming, Dates{Close: closeD}, istAt(18, 10, 5), "", false},
		{"date moved later since arming", stepOpen, StatusUpcoming, Dates{Open: dt(19)}, istAt(18, 10, 5), "", false},
		{"date moved earlier fires at the earlier instant", stepOpen, StatusUpcoming, Dates{Open: dt(17)}, istAt(17, 10, 0), StatusOpen, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, fires := stepDecision(tc.st, stepIPO(tc.status, tc.dates), tc.now, payload)
			if fires != tc.fires || (fires && got != tc.want) {
				t.Fatalf("stepDecision = (%s, %v), want (%s, %v)", got, fires, tc.want, tc.fires)
			}
		})
	}
}

// FR-036 / research R-02: after a manual override, a step armed before the override whose instant
// is at or before the override does not run again; a later instant, or a re-armed step, still does.
func TestStepDecision_BR_IPO_03_OverrideSuppression_FR036(t *testing.T) {
	dates := Dates{Open: dt(18), Close: dt(20), Allotment: dt(23), Listing: dt(25)}
	overrideAt := istAt(21, 12, 0) // admin set status back to OPEN after the close step had run
	p := func(armed time.Time) stepPayload { return stepPayload{IPOID: uuid.New(), ArmedAt: armed} }

	t.Run("old step at or before the override stays suppressed", func(t *testing.T) {
		i := stepIPO(StatusOpen, dates)
		setOverride(t, &i, overrideAt)
		if st, fires := stepDecision(stepClose, i, istAt(21, 13, 0), p(istAt(10, 9, 0))); fires {
			t.Fatalf("close (instant before override, armed before it) must not run again, got %s", st)
		}
	})
	t.Run("step instant exactly at the override is suppressed", func(t *testing.T) {
		i := stepIPO(StatusOpen, Dates{Close: dt(21)})
		setOverride(t, &i, istAt(21, 17, 0))
		if _, fires := stepDecision(stepClose, i, istAt(21, 17, 1), p(istAt(10, 9, 0))); fires {
			t.Fatalf("instant <= override and armed <= override must not run")
		}
	})
	t.Run("a step with a later instant still fires", func(t *testing.T) {
		i := stepIPO(StatusOpen, dates)
		setOverride(t, &i, overrideAt)
		if st, fires := stepDecision(stepAllotment, i, istAt(23, 10, 0), p(istAt(10, 9, 0))); !fires || st != StatusAllotmentOut {
			t.Fatalf("allotment (instant after override) must fire, got (%s, %v)", st, fires)
		}
	})
	t.Run("a step re-armed after the override fires", func(t *testing.T) {
		i := stepIPO(StatusOpen, dates)
		setOverride(t, &i, overrideAt)
		if st, fires := stepDecision(stepClose, i, istAt(21, 13, 0), p(istAt(21, 12, 30))); !fires || st != StatusClosed {
			t.Fatalf("close re-armed after the override (date changed) must fire, got (%s, %v)", st, fires)
		}
	})
}

func TestStepDiff_BR_IPO_03(t *testing.T) {
	kinds := func(ss []step) []string {
		out := []string{}
		for _, s := range ss {
			out = append(out, s.Kind)
		}
		return out
	}
	tests := []struct {
		name               string
		old, updated       Dates
		wantSched, wantCan []string
	}{
		{"nothing changes", Dates{Open: dt(18), Close: dt(20)}, Dates{Open: dt(18), Close: dt(20)}, []string{}, []string{}},
		{"create with all dates schedules four steps", Dates{}, Dates{Open: dt(18), Close: dt(20), Allotment: dt(23), Refund: dt(24), DematCredit: dt(24), Listing: dt(25)},
			[]string{jobOpen, jobClose, jobAllotment, jobList}, []string{}},
		{"only close moves", Dates{Open: dt(18), Close: dt(20), Allotment: dt(23)}, Dates{Open: dt(18), Close: dt(21), Allotment: dt(23)},
			[]string{jobClose}, []string{}},
		{"date set where there was none", Dates{Open: dt(18)}, Dates{Open: dt(18), Listing: dt(25)}, []string{jobList}, []string{}},
		{"date cleared cancels", Dates{Open: dt(18), Close: dt(20)}, Dates{Open: dt(18)}, []string{}, []string{jobClose}},
		{"one moved one cleared", Dates{Open: dt(18), Allotment: dt(23)}, Dates{Open: dt(19)}, []string{jobOpen}, []string{jobAllotment}},
		{"refund and demat credit have no step", Dates{Refund: dt(24), DematCredit: dt(24)}, Dates{Refund: dt(25), DematCredit: dt(26)}, []string{}, []string{}},
		{"refund cleared has no step", Dates{Refund: dt(24)}, Dates{}, []string{}, []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sched, cancel := stepDiff(tc.old, tc.updated)
			if !reflect.DeepEqual(kinds(sched), tc.wantSched) {
				t.Fatalf("schedule = %v, want %v", kinds(sched), tc.wantSched)
			}
			if !reflect.DeepEqual(kinds(cancel), tc.wantCan) {
				t.Fatalf("cancel = %v, want %v", kinds(cancel), tc.wantCan)
			}
			for _, s := range sched {
				want := map[string]Status{jobOpen: StatusOpen, jobClose: StatusClosed, jobAllotment: StatusAllotmentOut, jobList: StatusListed}[s.Kind]
				if s.Target != want {
					t.Fatalf("%s target = %s, want %s", s.Kind, s.Target, want)
				}
			}
		})
	}
}
