//nolint:unparam,revive,unused // S01 compile skeleton; removed as slices land
package ipos

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
)

type DatesChangedListener interface {
	OnIPODatesChanged(ctx context.Context, e DatesChanged) error
}
type EligibilityChangedListener interface {
	OnIPOEligibilityChanged(ctx context.Context, e EligibilityChanged) error
}
type StatusChangedListener interface {
	OnIPOStatusChanged(ctx context.Context, e StatusChanged) error
}

type DatesChanged struct {
	OrgID, IPOID uuid.UUID
	Old, New     Dates
	Actor        audit.Actor
	At           time.Time
}

type EligibilityChanged struct {
	OrgID, IPOID uuid.UUID
	Eligible     bool
	Cause        string // "publish" | "unpublish" | "flag"
	Actor        audit.Actor
	At           time.Time
}

type StatusChanged struct {
	OrgID, IPOID uuid.UUID
	From, To     Status
	Source       StatusSource
	Actor        audit.Actor
	At           time.Time
	ScheduledFor *time.Time
}

func (s *Service) OnDatesChanged(l DatesChangedListener)             { panic(errNotImplemented) }
func (s *Service) OnEligibilityChanged(l EligibilityChangedListener) { panic(errNotImplemented) }
func (s *Service) OnStatusChanged(l StatusChangedListener)           { panic(errNotImplemented) }
