//nolint:unparam,revive,unused // S01 compile skeleton; removed as slices land
package ipos

import (
	"time"

	"github.com/google/uuid"
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

// stepInstant is the IST instant a step fires (10:00 / 17:00 / 10:00 / 10:00).
func stepInstant(st step, i IPO) (time.Time, bool) { panic(errNotImplemented) }

// stepDecision decides whether a due step moves the status; forward only.
func stepDecision(st step, i IPO, now time.Time, p stepPayload) (Status, bool) {
	panic(errNotImplemented)
}

// stepDiff lists the schedule and cancel actions for a date change.
func stepDiff(old, updated Dates) (schedule, cancel []step) { panic(errNotImplemented) }
