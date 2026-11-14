package ipos

import (
	"context"
	"reflect"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
)

// Update replaces the facts of an IPO (PUT). A save that changes nothing writes nothing.
// Order: lock, replay, version, validate, write, steps, audit, DatesChanged.
func (s *Service) Update(ctx context.Context, by audit.Actor, org, id uuid.UUID, version int32, in Draft, idemKey string) (Result, error) {
	var res Result
	err := s.inTx(ctx, func(ctx context.Context) error {
		cur, err := s.store.GetIPOForUpdate(ctx, org, id)
		if err != nil {
			return err
		}
		if again, err := s.replayed(ctx, org, id, actUpdated, idemKey); err != nil || again {
			res = Result{IPO: cur, Warnings: warnings(cur), Replayed: again}
			return err
		}
		if cur.Version != version {
			return ErrStaleVersion
		}
		if err := validateDraft(in); err != nil {
			return err
		}
		if cur.Published {
			if err := validatePublishable(applyDraft(cur, in)); err != nil {
				return err
			}
		}
		oldFacts := factsOfIPO(cur)
		if reflect.DeepEqual(facts(oldFacts), facts(in)) {
			res = Result{IPO: cur, Warnings: warnings(cur)}
			return nil
		}
		now := s.d.Now()
		upd, err := s.store.UpdateIPOFacts(ctx, org, id, FactsUpdate{Draft: in, Version: version, Now: now})
		if err != nil {
			return err
		}
		if err := s.syncSteps(ctx, org, id, cur.Dates, upd.Dates, now); err != nil {
			return err
		}
		before, after := factsDiff(oldFacts, factsOfIPO(upd))
		if err := s.writeAudit(ctx, by, org, id, actUpdated, before, withKey(after, idemKey)); err != nil {
			return err
		}
		if !datesEqual(cur.Dates, upd.Dates) {
			if err := s.emitDates(ctx, DatesChanged{OrgID: org, IPOID: id, Old: cur.Dates, New: upd.Dates, Actor: by, At: now}); err != nil {
				return err
			}
		}
		res = Result{IPO: upd, Warnings: warnings(upd)}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}
