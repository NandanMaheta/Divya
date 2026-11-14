package ipos

import (
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
)

// Job kinds scheduled per IPO step.
const (
	jobOpen      = "ipo.open"
	jobClose     = "ipo.close"
	jobAllotment = "ipo.allotment"
	jobList      = "ipo.list"
)

// stepPayload is the job payload (ids and timestamps only).
type stepPayload struct {
	IPOID   uuid.UUID `json:"ipo_id"`
	ArmedAt time.Time `json:"armed_at"`
}

type step struct {
	Kind   string
	Target Status
}

// lifecycleSteps is the step table in lifecycle order. SETTLED is never a step target (FR-033).
var lifecycleSteps = []step{
	{Kind: jobOpen, Target: StatusOpen},
	{Kind: jobClose, Target: StatusClosed},
	{Kind: jobAllotment, Target: StatusAllotmentOut},
	{Kind: jobList, Target: StatusListed},
}

// stepDate returns the IPO date that drives a step.
func stepDate(st step, d Dates) *istdate.Date {
	switch st.Kind {
	case jobOpen:
		return d.Open
	case jobClose:
		return d.Close
	case jobAllotment:
		return d.Allotment
	case jobList:
		return d.Listing
	}
	return nil
}

// stepInstant is the IST instant a step fires (10:00 / 17:00 / 10:00 / 10:00).
func stepInstant(st step, i IPO) (time.Time, bool) {
	d := stepDate(st, i.Dates)
	if d == nil {
		return time.Time{}, false
	}
	hour := 10
	if st.Kind == jobClose {
		hour = 17
	}
	return d.At(hour, 0), true
}

// stepDecision decides whether a due step moves the status; forward only (research R-01, R-02).
func stepDecision(st step, i IPO, now time.Time, p stepPayload) (Status, bool) {
	at, ok := stepInstant(st, i)
	if !ok || at.After(now) {
		return "", false
	}
	if o := i.LastOverrideAt; o != nil && !p.ArmedAt.After(*o) && !at.After(*o) {
		return "", false
	}
	if i.Status.Rank() >= st.Target.Rank() {
		return "", false
	}
	return st.Target, true
}

// stepDiff lists the schedule and cancel actions for a date change; unchanged dates give none.
func stepDiff(old, updated Dates) (schedule, cancel []step) {
	for _, st := range lifecycleSteps {
		was, now := stepDate(st, old), stepDate(st, updated)
		switch {
		case now == nil && was != nil:
			cancel = append(cancel, st)
		case now != nil && (was == nil || *was != *now):
			schedule = append(schedule, st)
		}
	}
	return schedule, cancel
}
