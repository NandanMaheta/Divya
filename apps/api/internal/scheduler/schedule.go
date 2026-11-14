package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db/dbgen"
)

// Schedule records the job in the caller's transaction, so it commits or vanishes with the
// business change. There is one active (pending or running) job per (org, kind, key): a second
// Schedule replaces the due time, expiry, subject and payload of the pending job; when the job
// is running right now it runs again at the new time after it finishes. A finished job for the
// same key stays as history and a new pending row is created.
func (s *Service) Schedule(ctx context.Context, spec JobSpec) error {
	tx, err := db.MustTx(ctx)
	if err != nil {
		return fmt.Errorf("scheduler: schedule %s: %w", spec.Kind, err)
	}
	payload, err := validate(spec)
	if err != nil {
		return err
	}
	reg, ok := s.registrationFor(spec.Kind)
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownKind, spec.Kind)
	}
	_, err = dbgen.New(tx).UpsertJob(ctx, dbgen.UpsertJobParams{
		OrgID: spec.OrgID, Kind: spec.Kind, DedupeKey: spec.Key, Subject: spec.Subject, Payload: payload,
		RunAt: db.Timestamptz(spec.RunAt), ExpiresAt: db.NullTimestamptz(ceilMicro(spec.ExpiresAt)),
		Recurring: reg.opts.Recurring != nil, MaxAttempts: s.maxAttemptsFor(reg), Now: db.Timestamptz(s.now()),
	})
	if err != nil {
		return fmt.Errorf("scheduler: schedule %s: %w", spec.Kind, err)
	}
	return nil
}

// Reschedule moves the active job of a key to a new due time and reports whether there was one;
// it never creates a job. A running job runs again at the new time when it finishes.
func (s *Service) Reschedule(ctx context.Context, org uuid.UUID, kind, key string, runAt time.Time) (bool, error) {
	tx, err := db.MustTx(ctx)
	if err != nil {
		return false, fmt.Errorf("scheduler: reschedule %s: %w", kind, err)
	}
	if runAt.IsZero() {
		return false, invalid("run_at is required")
	}
	n, err := dbgen.New(tx).RescheduleJob(ctx, dbgen.RescheduleJobParams{
		RunAt: db.Timestamptz(runAt), Now: db.Timestamptz(s.now()), OrgID: org, Kind: kind, DedupeKey: key,
	})
	if err != nil {
		return false, fmt.Errorf("scheduler: reschedule %s: %w", kind, err)
	}
	return n > 0, nil
}

// Cancel stops the active job of a key (stop on completion) and returns how many jobs it
// affected; none is not an error. A running job is flagged: its in-flight run reads current
// data and decides for itself, and a failing run ends cancelled instead of retrying.
func (s *Service) Cancel(ctx context.Context, org uuid.UUID, kind, key string) (int, error) {
	tx, err := db.MustTx(ctx)
	if err != nil {
		return 0, fmt.Errorf("scheduler: cancel %s: %w", kind, err)
	}
	n, err := dbgen.New(tx).CancelJobByKey(ctx, dbgen.CancelJobByKeyParams{
		Now: db.Timestamptz(s.now()), OrgID: org, Kind: kind, DedupeKey: key,
	})
	if err != nil {
		return 0, fmt.Errorf("scheduler: cancel %s: %w", kind, err)
	}
	return int(n), nil
}

// CancelSubject stops every active job of one business item in the organisation.
func (s *Service) CancelSubject(ctx context.Context, org uuid.UUID, subject string) (int, error) {
	tx, err := db.MustTx(ctx)
	if err != nil {
		return 0, fmt.Errorf("scheduler: cancel subject: %w", err)
	}
	n, err := dbgen.New(tx).CancelJobsBySubject(ctx, dbgen.CancelJobsBySubjectParams{
		Now: db.Timestamptz(s.now()), OrgID: org, Subject: subject,
	})
	if err != nil {
		return 0, fmt.Errorf("scheduler: cancel subject: %w", err)
	}
	return int(n), nil
}

// ceilMicro rounds an expiry up to the microsecond PostgreSQL stores, so work is never expired
// earlier than the owning feature asked (a plain conversion would truncate).
func ceilMicro(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	r := t.Truncate(time.Microsecond)
	if r.Before(*t) {
		r = r.Add(time.Microsecond)
	}
	return &r
}
