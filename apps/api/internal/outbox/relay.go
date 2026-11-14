package outbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db/dbgen"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/retry"
)

// RelayBudget bounds one Relay call: at most MaxMessages claims, and no new claim once the
// injected clock reaches Deadline (the zero Deadline means no time limit).
type RelayBudget struct {
	MaxMessages int
	Deadline    time.Time
}

// RelaySummary counts what one Relay did; it maps onto the msgs_* columns of tick_runs.
// Failed counts failed attempts that will be retried; Dead counts messages that gave up.
type RelaySummary struct {
	Sent, Failed, Dead, Expired int
}

// Relay expires stale messages, then claims available messages one at a time and hands each to
// the sender of its topic. A claim commits before Send runs, so no transaction is open during
// Send; the "sent" mark is fenced by the claim's token, so an overlapping relay can never mark
// (or resend) a message it does not hold. A crash between Send and the mark makes a later relay
// send the message again with the same dedupe key (at-least-once).
//
// Relay must not run inside a transaction.
func (s *Service) Relay(ctx context.Context, b RelayBudget) (RelaySummary, error) {
	var sum RelaySummary
	if _, err := db.MustTx(ctx); err == nil {
		return sum, errors.New("outbox: Relay must not run inside a transaction")
	}
	q := dbgen.New(s.txr.Conn(ctx))

	expired, err := q.ExpireDueMessages(ctx, db.Timestamptz(s.now()))
	if err != nil {
		return sum, fmt.Errorf("outbox: expire due messages: %w", err)
	}
	sum.Expired = int(expired)

	for claimed := 0; claimed < b.MaxMessages; claimed++ {
		now := s.now()
		if !b.Deadline.IsZero() && !now.Before(b.Deadline) {
			break
		}
		if err := ctx.Err(); err != nil {
			return sum, fmt.Errorf("outbox: relay: %w", err)
		}
		msg, err := q.ClaimMessage(ctx, dbgen.ClaimMessageParams{
			Now:         db.Timestamptz(now),
			LockedUntil: db.Timestamptz(now.Add(s.opts.Lease)),
			LockedBy:    db.Text(s.opts.Instance),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			break
		}
		if err != nil {
			return sum, fmt.Errorf("outbox: claim: %w", err)
		}
		if err := s.deliver(ctx, q, msg, &sum); err != nil {
			return sum, err
		}
	}
	return sum, nil
}

// deliver sends one claimed message and records the outcome. Failures of the sender are data
// (a retry); only database errors are returned.
func (s *Service) deliver(ctx context.Context, q *dbgen.Queries, msg dbgen.OutboxMessage, sum *RelaySummary) error {
	token := msg.LockToken.UUID
	if msg.Attempts > msg.MaxAttempts {
		// The lease ran out so often that the attempts are used up without a reported failure.
		n, err := q.MarkMessageDead(ctx, dbgen.MarkMessageDeadParams{
			Now: db.Timestamptz(s.now()), LastError: "lease expired too often", ID: msg.ID, LockToken: token,
		})
		if err != nil {
			return fmt.Errorf("outbox: mark dead: %w", err)
		}
		if n == 1 {
			sum.Dead++
		}
		return nil
	}

	if sendErr := s.send(ctx, msg); sendErr != nil {
		return s.fail(ctx, q, msg, sendErr, sum)
	}
	n, err := q.MarkMessageSent(ctx, dbgen.MarkMessageSentParams{Now: db.Timestamptz(s.now()), ID: msg.ID, LockToken: token})
	if err != nil {
		return fmt.Errorf("outbox: mark sent: %w", err)
	}
	if n == 0 {
		s.log.WarnContext(ctx, "outbox: lease lost before the sent mark; the message will be relayed again",
			"message_id", msg.ID, "topic", msg.Topic)
		return nil
	}
	sum.Sent++
	return nil
}

// send calls the topic's sender outside any transaction, bounded by the send timeout. A missing
// sender and a panic are failed attempts like any other error.
func (s *Service) send(ctx context.Context, msg dbgen.OutboxMessage) (err error) {
	reg, ok := s.registrationFor(msg.Topic)
	if !ok {
		return fmt.Errorf("no sender registered for topic %q", msg.Topic)
	}
	sendCtx, cancel := context.WithTimeout(ctx, s.opts.SendTimeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("sender panicked: %v", r)
		}
	}()
	return reg.sender.Send(sendCtx, envelopeOf(msg))
}

// fail records a failed attempt: retry after a backoff that never lands inside quiet hours, or
// dead when the attempts are used up.
func (s *Service) fail(ctx context.Context, q *dbgen.Queries, msg dbgen.OutboxMessage, sendErr error, sum *RelaySummary) error {
	now := s.now()
	next, err := s.availableAt(ctx, msg.OrgID, msg.RespectsQuietHours, now.Add(retry.Backoff(int(msg.Attempts), msg.ID.String())))
	if err != nil {
		return err
	}
	res, err := q.FailMessage(ctx, dbgen.FailMessageParams{
		AvailableAt: db.Timestamptz(next),
		Now:         db.Timestamptz(now),
		LastError:   retry.ErrorText(sendErr),
		ID:          msg.ID,
		LockToken:   msg.LockToken.UUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		s.log.WarnContext(ctx, "outbox: lease lost before the failure was recorded", "message_id", msg.ID, "topic", msg.Topic)
		return nil
	}
	if err != nil {
		return fmt.Errorf("outbox: record failure: %w", err)
	}
	if res.Status == "dead" {
		sum.Dead++
		s.log.WarnContext(ctx, "outbox: message is dead", "message_id", msg.ID, "topic", msg.Topic, "attempts", res.Attempts)
	} else {
		sum.Failed++
	}
	return nil
}
