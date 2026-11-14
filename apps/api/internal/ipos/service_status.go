package ipos

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
)

func isNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// OverrideStatus sets any status with a reason (FR-035). Steps whose instant is before the
// override never fire again (FR-036); a later date change re-arms them.
func (s *Service) OverrideStatus(ctx context.Context, by audit.Actor, org, id uuid.UUID, version int32, to Status, reason string, idemKey string) (Result, error) {
	reason = strings.TrimSpace(reason)
	var is []FieldIssue
	if reason == "" {
		is = append(is, FieldIssue{Field: "reason", Message: "a reason is required"})
	}
	if to.Rank() < 0 {
		is = append(is, FieldIssue{Field: "status", Message: "unknown status"})
	}
	if len(is) > 0 {
		return Result{}, &ValidationError{Code: "ipo.invalid_value", Issues: is}
	}
	var res Result
	err := s.inTx(ctx, func(ctx context.Context) error {
		cur, err := s.store.GetIPOForUpdate(ctx, org, id)
		if err != nil {
			return err
		}
		if again, err := s.replayed(ctx, org, id, actStatus, idemKey); err != nil || again {
			res = Result{IPO: cur, Replayed: again}
			return err
		}
		if cur.Version != version {
			return ErrStaleVersion
		}
		if cur.Status == to {
			return ErrNoChange
		}
		upd, err := s.applyStatus(ctx, cur, statusChange{
			To: to, Source: SourceAdmin, By: by, Version: version, Reason: reason, IdemKey: idemKey, Override: true,
		})
		if err != nil {
			return err
		}
		res = Result{IPO: upd}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

// MarkSettled moves LISTED to SETTLED for 019 (system actor, source settlement). Already SETTLED
// is a silent no-op; any other status gives *NotListedError. It joins the caller's transaction.
func (s *Service) MarkSettled(ctx context.Context, org, id uuid.UUID) error {
	return s.inTx(ctx, func(ctx context.Context) error {
		cur, err := s.store.GetIPOForUpdate(ctx, org, id)
		if err != nil {
			return err
		}
		if cur.Status == StatusSettled {
			return nil
		}
		if cur.Status != StatusListed {
			return &NotListedError{Current: cur.Status}
		}
		_, err = s.applyStatus(ctx, cur, statusChange{
			To: StatusSettled, Source: SourceSettlement, By: systemActor, Version: cur.Version,
		})
		return err
	})
}

// Delete removes a never-published IPO and cancels its steps. A repeated key returns replayed.
func (s *Service) Delete(ctx context.Context, by audit.Actor, org, id uuid.UUID, version int32, idemKey string) (bool, error) {
	replayed := false
	err := s.inTx(ctx, func(ctx context.Context) error {
		cur, err := s.store.GetIPOForUpdate(ctx, org, id)
		if isNotFound(err) {
			// The row is gone: a known key means this is a replay of the delete that removed it.
			if again, aerr := s.replayed(ctx, org, id, actDeleted, idemKey); aerr != nil {
				return aerr
			} else if again {
				replayed = true
				return nil
			}
		}
		if err != nil {
			return err
		}
		if cur.Version != version {
			return ErrStaleVersion
		}
		if cur.FirstPublishedAt != nil {
			return ErrPublishedNotDeletable
		}
		n, err := s.store.DeleteIPO(ctx, org, id, version)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrStaleVersion
		}
		if _, err := s.d.Scheduler.CancelSubject(ctx, org, jobSubject(id)); err != nil {
			return err
		}
		return s.writeAudit(ctx, by, org, id, actDeleted, withKey(snapshot(cur), idemKey), nil)
	})
	return replayed, err
}
