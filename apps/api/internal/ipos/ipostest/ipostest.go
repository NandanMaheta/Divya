// Package ipostest holds test helpers for features that depend on internal/ipos.
package ipostest //nolint:unparam,revive,unused // S01 compile skeleton; removed as slices land

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/ipos"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/clock"
)

// NewService builds a real ipos.Service over the test database and a fake scheduler.
func NewService(t testing.TB, now clock.Func) *ipos.Service { panic("ipostest: not implemented") }

// RecordingListener records every signal it receives.
type RecordingListener struct {
	Dates       []ipos.DatesChanged
	Eligibility []ipos.EligibilityChanged
	Status      []ipos.StatusChanged
}

// OnIPODatesChanged records the signal.
func (r *RecordingListener) OnIPODatesChanged(ctx context.Context, e ipos.DatesChanged) error {
	panic("ipostest: not implemented")
}

// OnIPOEligibilityChanged records the signal.
func (r *RecordingListener) OnIPOEligibilityChanged(ctx context.Context, e ipos.EligibilityChanged) error {
	panic("ipostest: not implemented")
}

// OnIPOStatusChanged records the signal.
func (r *RecordingListener) OnIPOStatusChanged(ctx context.Context, e ipos.StatusChanged) error {
	panic("ipostest: not implemented")
}

// FailingListener returns Err on every signal.
type FailingListener struct{ Err error }

// OnIPODatesChanged returns Err.
func (f *FailingListener) OnIPODatesChanged(ctx context.Context, e ipos.DatesChanged) error {
	return f.Err
}

// OnIPOEligibilityChanged returns Err.
func (f *FailingListener) OnIPOEligibilityChanged(ctx context.Context, e ipos.EligibilityChanged) error {
	return f.Err
}

// OnIPOStatusChanged returns Err.
func (f *FailingListener) OnIPOStatusChanged(ctx context.Context, e ipos.StatusChanged) error {
	return f.Err
}

// ExampleIPO returns a valid draft-ready IPO for org.
func ExampleIPO(org uuid.UUID, at time.Time) ipos.Draft { panic("ipostest: not implemented") }
