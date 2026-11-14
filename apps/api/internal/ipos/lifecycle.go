package ipos

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
)

// RegisterJobs registers ipo.open, ipo.close, ipo.allotment and ipo.list with default options
// (8 attempts, not recurring). The steps have no expiry: a late step still runs (FR-031).
func RegisterJobs(r JobRegistrar, s *Service) {
	for _, st := range lifecycleSteps {
		r.Register(st.Kind, s.stepHandler(st), scheduler.KindOptions{})
	}
}

// stepHandler moves the status forward when the step is due. It re-reads the row under lock, so a
// duplicate run, a moved date or a racing override make it a silent no-op (FR-032, FR-036).
func (s *Service) stepHandler(st step) scheduler.Handler {
	return func(ctx context.Context, job scheduler.Job) error {
		var p stepPayload
		if err := json.Unmarshal(job.Payload, &p); err != nil {
			return fmt.Errorf("ipos: step %s payload: %w", st.Kind, err)
		}
		return s.inTx(ctx, func(ctx context.Context) error {
			cur, err := s.store.GetIPOForUpdate(ctx, job.OrgID, p.IPOID)
			if err != nil {
				if isNotFound(err) {
					return nil // deleted meanwhile
				}
				return err
			}
			to, ok := stepDecision(st, cur, s.d.Now(), p)
			if !ok {
				return nil
			}
			at, _ := stepInstant(st, cur)
			_, err = s.applyStatus(ctx, cur, statusChange{
				To: to, Source: SourceSchedule, By: systemActor, Version: cur.Version,
				ScheduledFor: &at, Step: st.Kind,
			})
			return err
		})
	}
}
