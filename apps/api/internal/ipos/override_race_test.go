package ipos

import (
	"context"
	"testing"
	"time"
)

// K-04: an override that commits while a step handler already waits on the row lock must make the
// step a no-op (the step re-reads last_override_at after acquiring the lock).
func TestOverride_BR_IPO_03_RaceWithRunningStep(t *testing.T) {
	e := newSvcEnv(t)
	e.setClock(10, 9, 0)
	i := e.create(acme())
	c := e.captureHandlers()
	e.setClock(11, 10, 0)
	if err := e.runStep(c, i.ID, jobOpen); err != nil {
		t.Fatalf("open step: %v", err)
	}
	wantStatus(t, e, i.ID, StatusOpen)

	e.setClock(13, 17, 30) // the close step (17:00) is due
	cur := e.currentID(i.ID)
	locked, release := make(chan struct{}), make(chan struct{})
	overrideDone := make(chan error, 1)
	go func() {
		overrideDone <- e.h.Txr.InTx(e.ctx(), func(ctx context.Context) error {
			if _, err := e.svc.OverrideStatus(ctx, e.admin, e.org, i.ID, cur.Version, StatusUpcoming, "back to start", e.key()); err != nil {
				close(locked)
				return err
			}
			close(locked) // the row lock is now held by this uncommitted transaction
			<-release
			return nil
		})
	}()
	<-locked

	stepDone := make(chan error, 1)
	go func() { stepDone <- e.runStep(c, i.ID, jobClose) }()
	select {
	case err := <-stepDone:
		t.Fatalf("the step finished while the override holds the row lock: %v", err)
	case <-time.After(400 * time.Millisecond):
	}
	close(release)
	if err := <-overrideDone; err != nil {
		t.Fatalf("override: %v", err)
	}
	if err := <-stepDone; err != nil {
		t.Fatalf("step: %v", err)
	}
	row := e.mustRow(i.ID)
	if row.Status != "UPCOMING" || row.StatusSource != "admin" {
		t.Fatalf("row = %+v: the step must be a no-op after the override committed", row)
	}
	if n := len(e.statusAudits(i.ID)); n != 2 {
		t.Fatalf("status audits = %d, want 2 (open step, override)", n)
	}
}
