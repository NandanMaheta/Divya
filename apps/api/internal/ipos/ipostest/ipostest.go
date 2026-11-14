// Package ipostest holds test helpers for features that depend on internal/ipos.
package ipostest

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/ipos"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler/schedulertest"
)

// NewService builds a real ipos.Service over the harness database, fake clock and real scheduler,
// with the four lifecycle step handlers registered.
func NewService(h *schedulertest.Harness) *ipos.Service {
	svc := ipos.NewService(ipos.Deps{
		Tx: h.Txr, Scheduler: h.Sched, Now: h.Clock.Now,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ipos.RegisterJobs(h.Sched, svc)
	return svc
}

// RecordingListener records every signal it receives.
type RecordingListener struct {
	mu          sync.Mutex
	Dates       []ipos.DatesChanged
	Eligibility []ipos.EligibilityChanged
	Status      []ipos.StatusChanged
}

// OnIPODatesChanged records the signal.
func (r *RecordingListener) OnIPODatesChanged(_ context.Context, e ipos.DatesChanged) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Dates = append(r.Dates, e)
	return nil
}

// OnIPOEligibilityChanged records the signal.
func (r *RecordingListener) OnIPOEligibilityChanged(_ context.Context, e ipos.EligibilityChanged) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Eligibility = append(r.Eligibility, e)
	return nil
}

// OnIPOStatusChanged records the signal.
func (r *RecordingListener) OnIPOStatusChanged(_ context.Context, e ipos.StatusChanged) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Status = append(r.Status, e)
	return nil
}

// FailingListener returns Err on every signal.
type FailingListener struct{ Err error }

// OnIPODatesChanged returns Err.
func (f *FailingListener) OnIPODatesChanged(context.Context, ipos.DatesChanged) error {
	return f.Err
}

// OnIPOEligibilityChanged returns Err.
func (f *FailingListener) OnIPOEligibilityChanged(context.Context, ipos.EligibilityChanged) error {
	return f.Err
}

// OnIPOStatusChanged returns Err.
func (f *FailingListener) OnIPOStatusChanged(context.Context, ipos.StatusChanged) error {
	return f.Err
}

// ExampleIPO is the spec's Acme Ltd: open Wed 11 Nov 2026, close Fri 13 Nov, allotment Mon 16 Nov,
// refund and credit Tue 17 Nov, listing Wed 18 Nov; band 1,000 to 1,050 rupees, lot 14.
// The org and instant are accepted for call-site symmetry with other builders and are not used.
func ExampleIPO(_ uuid.UUID, _ time.Time) ipos.Draft {
	day := func(d int) *istdate.Date { return &istdate.Date{Year: 2026, Month: time.November, Day: d} }
	floor, capP, issue, lot := int64(100_000), int64(105_000), int64(50_000_000_000), int32(14)
	sym, reg := "ACME", "Link Intime"
	return ipos.Draft{
		Name: "Acme Ltd", Exchanges: []ipos.Exchange{ipos.ExchangeNSE, ipos.ExchangeBSE}, NSESymbol: &sym,
		IssueSizePaise: &issue, PriceBandFloorPaise: &floor, PriceBandCapPaise: &capP, LotSize: &lot,
		Dates:     ipos.Dates{Open: day(11), Close: day(13), Allotment: day(16), Refund: day(17), DematCredit: day(17), Listing: day(18)},
		Registrar: &reg, LeadManagers: []string{"Axis Capital"},
	}
}
