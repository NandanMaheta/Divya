package outbox

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db/dbgen"
)

const (
	defaultPageSize = 50
	maxPageSize     = 100
)

// FailedFilter narrows ListFailedMessages. State is "retrying", "dead" or "" for both; Topic ""
// means every topic. Limit 0 means the default page size; Cursor is the NextCursor of the
// previous page.
type FailedFilter struct {
	State  string
	Topic  string
	Limit  int
	Cursor string
}

// FailedMessage is the admin view of a message (schema FailedMessage in the admin contract). It
// never carries the payload, the lease or the worker name.
type FailedMessage struct {
	ID             uuid.UUID  `json:"id"`
	Topic          string     `json:"topic"`
	Subject        string     `json:"subject"`
	DedupeKey      string     `json:"dedupe_key"`
	RecipientKind  string     `json:"recipient_kind"`
	RecipientID    *uuid.UUID `json:"recipient_id"`
	State          string     `json:"state"`
	Attempts       int        `json:"attempts"`
	MaxAttempts    int        `json:"max_attempts"`
	FirstAttemptAt *time.Time `json:"first_attempt_at"`
	LastAttemptAt  *time.Time `json:"last_attempt_at"`
	NextAttemptAt  *time.Time `json:"next_attempt_at"`
	ExpiresAt      *time.Time `json:"expires_at"`
	LastError      *string    `json:"last_error"`
	CreatedAt      time.Time  `json:"created_at"`
}

// Page is one page of a keyset-paginated list.
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// DeadGroup is the unalerted dead messages of one organisation, for the grouped admin alert.
type DeadGroup struct {
	OrgID  uuid.UUID
	Count  int
	Topics []string // distinct, sorted
}

func failedOf(m dbgen.OutboxMessage) FailedMessage {
	f := FailedMessage{
		ID: m.ID, Topic: m.Topic, Subject: m.Subject, DedupeKey: m.DedupeKey, RecipientKind: m.RecipientKind,
		State: m.Status, Attempts: int(m.Attempts), MaxAttempts: int(m.MaxAttempts),
		FirstAttemptAt: db.TimePtr(m.FirstAttemptAt), LastAttemptAt: db.TimePtr(m.LastAttemptAt),
		ExpiresAt: db.TimePtr(m.ExpiresAt), LastError: db.TextPtr(m.LastError), CreatedAt: db.Time(m.CreatedAt),
	}
	if m.RecipientID.Valid {
		id := m.RecipientID.UUID
		f.RecipientID = &id
	}
	if m.Status == "pending" {
		f.NextAttemptAt = db.TimePtr(m.AvailableAt)
		if m.Attempts > 0 {
			f.State = "retrying"
		}
	}
	return f
}

// ListFailedMessages lists the org's retrying and dead messages, newest last attempt first.
func (s *Service) ListFailedMessages(ctx context.Context, org uuid.UUID, f FailedFilter) (Page[FailedMessage], error) {
	page := Page[FailedMessage]{Items: []FailedMessage{}}
	if f.State != "" && f.State != "retrying" && f.State != "dead" {
		return page, invalid("state %q must be retrying or dead", f.State)
	}
	if len(f.Topic) > maxTopicLen {
		return page, invalid("topic is longer than %d characters", maxTopicLen)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = defaultPageSize
	}
	limit = min(limit, maxPageSize)

	p := dbgen.ListFailedMessagesParams{OrgID: org, State: f.State, Topic: f.Topic, RowLimit: int32(limit + 1)} //nolint:gosec // bounded by maxPageSize
	if f.Cursor != "" {
		at, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return page, err
		}
		p.CursorAt, p.CursorID = db.Timestamptz(at), uuid.NullUUID{UUID: id, Valid: true}
	}
	rows, err := dbgen.New(s.txr.Conn(ctx)).ListFailedMessages(ctx, p)
	if err != nil {
		return page, fmt.Errorf("outbox: list failed messages: %w", err)
	}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		c := encodeCursor(db.Time(last.LastAttemptAt), last.ID)
		page.NextCursor = &c
	}
	for _, r := range rows {
		page.Items = append(page.Items, failedOf(r))
	}
	return page, nil
}

// RetryMessage requeues a dead message with fresh attempts; it goes out on the next tick, after
// quiet hours, and still expires when its expiry has passed. Only dead messages can be retried.
// idemKey is stored in the audit entry so the HTTP layer can recognise a replay.
func (s *Service) RetryMessage(ctx context.Context, actor audit.Actor, org, id uuid.UUID, idemKey string) (FailedMessage, error) {
	return s.adminAct(ctx, actor, org, id, idemKey, "outbox_message.retry", func(ctx context.Context, q *dbgen.Queries, cur dbgen.OutboxMessage, now time.Time) (dbgen.OutboxMessage, error) {
		at, err := s.availableAt(ctx, org, cur.RespectsQuietHours, now)
		if err != nil {
			return dbgen.OutboxMessage{}, err
		}
		return q.AdminRetryMessage(ctx, dbgen.AdminRetryMessageParams{AvailableAt: db.Timestamptz(at), Now: db.Timestamptz(now), ID: id, OrgID: org})
	})
}

// CancelMessage withdraws a dead message so it is never sent; its dedupe key stays reserved.
func (s *Service) CancelMessage(ctx context.Context, actor audit.Actor, org, id uuid.UUID, idemKey string) (FailedMessage, error) {
	return s.adminAct(ctx, actor, org, id, idemKey, "outbox_message.cancel", func(ctx context.Context, q *dbgen.Queries, _ dbgen.OutboxMessage, now time.Time) (dbgen.OutboxMessage, error) {
		return q.AdminCancelMessage(ctx, dbgen.AdminCancelMessageParams{Now: db.Timestamptz(now), ID: id, OrgID: org})
	})
}

type adminChange func(ctx context.Context, q *dbgen.Queries, cur dbgen.OutboxMessage, now time.Time) (dbgen.OutboxMessage, error)

// adminAct runs one admin change on a dead message and its audit entry in one transaction.
func (s *Service) adminAct(ctx context.Context, actor audit.Actor, org, id uuid.UUID, idemKey, action string, change adminChange) (FailedMessage, error) {
	var out FailedMessage
	err := s.txr.InTx(ctx, func(ctx context.Context) error {
		tx, err := db.MustTx(ctx)
		if err != nil {
			return err
		}
		q := dbgen.New(tx)
		cur, err := q.GetMessageForOrg(ctx, dbgen.GetMessageForOrgParams{ID: id, OrgID: org})
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: message %s", ErrNotFound, id)
		}
		if err != nil {
			return fmt.Errorf("outbox: load message: %w", err)
		}
		if cur.Status != "dead" {
			return fmt.Errorf("%w: message %s is %s, only dead messages can be changed", ErrConflict, id, failedOf(cur).State)
		}
		upd, err := change(ctx, q, cur, s.now())
		if err != nil {
			return fmt.Errorf("outbox: %s: %w", action, err)
		}
		out = failedOf(upd)
		return audit.Write(ctx, audit.Entry{
			OrgID: org, Actor: actor, Entity: "outbox_message", EntityID: id, Action: action,
			Before: auditState(cur, ""), After: auditState(upd, idemKey),
		})
	})
	if err != nil {
		return FailedMessage{}, err
	}
	return out, nil
}

func auditState(m dbgen.OutboxMessage, idemKey string) map[string]any {
	state := map[string]any{
		"status": m.Status, "attempts": m.Attempts, "available_at": db.Time(m.AvailableAt),
		"last_error": db.TextPtr(m.LastError),
	}
	if idemKey != "" {
		state["idempotency_key"] = idemKey
	}
	return state
}

// CountDead is the number of dead messages of the org (the dead count of the status view).
func (s *Service) CountDead(ctx context.Context, org uuid.UUID) (int, error) {
	row, err := dbgen.New(s.txr.Conn(ctx)).CountMessagesByState(ctx, org)
	if err != nil {
		return 0, fmt.Errorf("outbox: count dead: %w", err)
	}
	return int(row.Dead), nil
}

// SweepDead returns the dead messages that no alert has covered yet, grouped per organisation,
// and marks them alerted in the caller's transaction, so a rolled back sweep alerts again on the
// next tick. Messages of topics "admin.*" are marked but never reported, so an undeliverable
// alert cannot trigger another alert.
func (s *Service) SweepDead(ctx context.Context) ([]DeadGroup, error) {
	tx, err := db.MustTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("outbox: sweep dead: %w", err)
	}
	q := dbgen.New(tx)
	rows, err := q.SelectUnalertedDeadMessages(ctx)
	if err != nil {
		return nil, fmt.Errorf("outbox: select dead messages: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	ids := make([]uuid.UUID, 0, len(rows))
	byOrg := map[uuid.UUID]*DeadGroup{}
	var order []uuid.UUID
	for _, r := range rows {
		ids = append(ids, r.ID)
		if strings.HasPrefix(r.Topic, "admin.") {
			continue
		}
		g, ok := byOrg[r.OrgID]
		if !ok {
			g = &DeadGroup{OrgID: r.OrgID}
			byOrg[r.OrgID] = g
			order = append(order, r.OrgID)
		}
		g.Count++
		if !slices.Contains(g.Topics, r.Topic) {
			g.Topics = append(g.Topics, r.Topic)
		}
	}
	if err := q.MarkMessagesAlerted(ctx, dbgen.MarkMessagesAlertedParams{Now: db.Timestamptz(s.now()), Ids: ids}); err != nil {
		return nil, fmt.Errorf("outbox: mark alerted: %w", err)
	}
	groups := make([]DeadGroup, 0, len(order))
	for _, org := range order {
		g := byOrg[org]
		slices.Sort(g.Topics)
		groups = append(groups, *g)
	}
	return groups, nil
}

// PurgeFinished deletes the org's sent, withdrawn and expired messages finished before cutoff
// (retention, FR-035); dead messages stay until an admin acts.
func (s *Service) PurgeFinished(ctx context.Context, org uuid.UUID, cutoff time.Time) (int, error) {
	n, err := dbgen.New(s.txr.Conn(ctx)).PurgeFinishedMessages(ctx, dbgen.PurgeFinishedMessagesParams{OrgID: org, Cutoff: db.Timestamptz(cutoff)})
	if err != nil {
		return 0, fmt.Errorf("outbox: purge finished: %w", err)
	}
	return int(n), nil
}
