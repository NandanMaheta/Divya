package outbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db/dbgen"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
)

func toMinutes(w istdate.WallClock) int { return w.Hour*60 + w.Minute }

func fromMinutes(m int) istdate.WallClock { return istdate.WallClock{Hour: m / 60, Minute: m % 60} }

func validWallClock(w istdate.WallClock) bool {
	return w.Hour >= 0 && w.Hour <= 23 && w.Minute >= 0 && w.Minute <= 59
}

// QuietHours returns the org's quiet window in IST; an org without a row has the default
// 21:30-08:00 (no seeding). It reads inside the caller's transaction when there is one.
func (s *Service) QuietHours(ctx context.Context, org uuid.UUID) (istdate.QuietHours, error) {
	row, err := dbgen.New(s.txr.Conn(ctx)).GetQuietHours(ctx, org)
	if errors.Is(err, pgx.ErrNoRows) {
		return istdate.DefaultQuietHours, nil
	}
	if err != nil {
		return istdate.QuietHours{}, fmt.Errorf("outbox: quiet hours: %w", err)
	}
	return istdate.QuietHours{Start: fromMinutes(int(row.StartMinute)), End: fromMinutes(int(row.EndMinute))}, nil
}

// SetQuietHours replaces the org's window. The change and its audit entry commit together, and
// join the caller's transaction when there is one. Messages already deferred keep their time;
// only messages recorded or retried afterwards use the new window (US8.8).
func (s *Service) SetQuietHours(ctx context.Context, actor audit.Actor, org uuid.UUID, q istdate.QuietHours) error {
	if !validWallClock(q.Start) || !validWallClock(q.End) || toMinutes(q.Start) == toMinutes(q.End) {
		return invalid("quiet hours %02d:%02d-%02d:%02d must be valid times and differ", q.Start.Hour, q.Start.Minute, q.End.Hour, q.End.Minute)
	}
	return s.txr.InTx(ctx, func(ctx context.Context) error {
		tx, err := db.MustTx(ctx)
		if err != nil {
			return err
		}
		before, err := s.QuietHours(ctx, org)
		if err != nil {
			return err
		}
		err = dbgen.New(tx).UpsertQuietHours(ctx, dbgen.UpsertQuietHoursParams{
			OrgID:       org,
			StartMinute: int16(toMinutes(q.Start)), //nolint:gosec // validated to 0..1439
			EndMinute:   int16(toMinutes(q.End)),   //nolint:gosec // validated to 0..1439
			Now:         db.Timestamptz(s.now()),
		})
		if err != nil {
			return fmt.Errorf("outbox: set quiet hours: %w", err)
		}
		return audit.Write(ctx, audit.Entry{
			OrgID: org, Actor: actor, Entity: "quiet_hours", EntityID: org, Action: "quiet_hours.update",
			Before: quietJSON(before), After: quietJSON(q),
		})
	})
}

func quietJSON(q istdate.QuietHours) map[string]int {
	return map[string]int{"start_minute": toMinutes(q.Start), "end_minute": toMinutes(q.End)}
}

// availableAt is the earliest instant a message may go out when it is due at `from`: `from`
// itself for an exempt message or one outside the org's quiet window, else the end of the window.
func (s *Service) availableAt(ctx context.Context, org uuid.UUID, respectsQuietHours bool, from time.Time) (time.Time, error) {
	if !respectsQuietHours {
		return from, nil
	}
	q, err := s.QuietHours(ctx, org)
	if err != nil {
		return time.Time{}, err
	}
	return q.Defer(from), nil
}
