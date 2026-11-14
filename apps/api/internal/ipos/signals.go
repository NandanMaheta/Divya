package ipos

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
)

// DatesChangedListener receives DatesChanged inside the emitting transaction.
type DatesChangedListener interface {
	OnIPODatesChanged(ctx context.Context, e DatesChanged) error
}

// EligibilityChangedListener receives EligibilityChanged inside the emitting transaction.
type EligibilityChangedListener interface {
	OnIPOEligibilityChanged(ctx context.Context, e EligibilityChanged) error
}

// StatusChangedListener receives StatusChanged inside the emitting transaction.
type StatusChangedListener interface {
	OnIPOStatusChanged(ctx context.Context, e StatusChanged) error
}

// DatesChanged is sent when at least one of the six dates changed (never on create).
type DatesChanged struct {
	OrgID, IPOID uuid.UUID
	Old, New     Dates
	Actor        audit.Actor
	At           time.Time
}

// EligibilityChanged is sent when Eligible() flips.
type EligibilityChanged struct {
	OrgID, IPOID uuid.UUID
	Eligible     bool
	Cause        string // "publish" | "unpublish" | "flag"
	Actor        audit.Actor
	At           time.Time
}

// StatusChanged is sent on every real status change.
type StatusChanged struct {
	OrgID, IPOID uuid.UUID
	From, To     Status
	Source       StatusSource
	Actor        audit.Actor
	At           time.Time
	ScheduledFor *time.Time
}

// listeners are the registered signal receivers, called in registration order (ADR 0008).
type listeners struct {
	dates  []DatesChangedListener
	elig   []EligibilityChangedListener
	status []StatusChangedListener
}

// OnDatesChanged registers a listener; call it at wiring time only.
func (s *Service) OnDatesChanged(l DatesChangedListener) { s.sig.dates = append(s.sig.dates, l) }

// OnEligibilityChanged registers a listener; call it at wiring time only.
func (s *Service) OnEligibilityChanged(l EligibilityChangedListener) {
	s.sig.elig = append(s.sig.elig, l)
}

// OnStatusChanged registers a listener; call it at wiring time only.
func (s *Service) OnStatusChanged(l StatusChangedListener) { s.sig.status = append(s.sig.status, l) }

// safely turns a listener panic into an error so the transaction rolls back.
func safely(signal string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("ipos: %s listener panicked: %v", signal, r)
		}
	}()
	return fn()
}

// The first listener error stops the fan-out and is returned unchanged, so a *RejectedError
// reaches the caller.

func (s *Service) emitDates(ctx context.Context, e DatesChanged) error {
	for _, l := range s.sig.dates {
		if err := safely("dates", func() error { return l.OnIPODatesChanged(ctx, e) }); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) emitEligibility(ctx context.Context, e EligibilityChanged) error {
	for _, l := range s.sig.elig {
		if err := safely("eligibility", func() error { return l.OnIPOEligibilityChanged(ctx, e) }); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) emitStatus(ctx context.Context, e StatusChanged) error {
	for _, l := range s.sig.status {
		if err := safely("status", func() error { return l.OnIPOStatusChanged(ctx, e) }); err != nil {
			return err
		}
	}
	return nil
}
