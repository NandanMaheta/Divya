package ipos

import (
	"context"
	"time"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
)

// statusChange describes one real status change to apply.
type statusChange struct {
	To           Status
	Source       StatusSource
	By           audit.Actor
	Version      int32
	ScheduledFor *time.Time // step instant (schedule only)
	Step         string     // job kind (schedule only)
	Reason       string     // override only
	IdemKey      string
	Override     bool // admin override: stamps last_override_at (FR-036)
}

// applyStatus writes the new status, one audit row and the StatusChanged signal, in the caller's
// transaction. cur must be the row locked by the caller.
func (s *Service) applyStatus(ctx context.Context, cur IPO, c statusChange) (IPO, error) {
	now := s.d.Now()
	u := StatusUpdate{Status: c.To, Source: c.Source, Version: c.Version, Now: now}
	if c.Override {
		u.OverrideAt = &now
	}
	upd, err := s.store.SetIPOStatus(ctx, cur.OrgID, cur.ID, u)
	if err != nil {
		return IPO{}, err
	}
	before := fields{"status": string(cur.Status), "status_source": string(cur.StatusSource)}
	after := fields{"status": string(upd.Status), "status_source": string(upd.StatusSource)}
	if c.Step != "" {
		after["step"] = c.Step
	}
	if c.ScheduledFor != nil {
		after["scheduled_for"] = c.ScheduledFor.Format(time.RFC3339)
	}
	if c.Reason != "" {
		after["reason"] = c.Reason
	}
	if err := s.writeAudit(ctx, c.By, cur.OrgID, cur.ID, actStatus, before, withKey(after, c.IdemKey)); err != nil {
		return IPO{}, err
	}
	err = s.emitStatus(ctx, StatusChanged{
		OrgID: cur.OrgID, IPOID: cur.ID, From: cur.Status, To: upd.Status, Source: c.Source,
		Actor: c.By, At: now, ScheduledFor: c.ScheduledFor,
	})
	if err != nil {
		return IPO{}, err
	}
	return upd, nil
}
