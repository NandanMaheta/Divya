package authtest

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
)

// Deps returns the dependencies of a Service wired to this Env: the real Store, the fake clock,
// the cheap Hasher, the fixed key rings and a discarding logger. Tests override fields on the copy.
func (e *Env) Deps() auth.Deps {
	return auth.Deps{
		Store: e.Store, Tx: e.Tx, Now: e.Clock.Now, Keys: e.Signing, Hasher: e.Hasher,
		Blind: e.Blind, Keyring: e.Keyring, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// Service is a Service on the fake clock and the real Store.
func (e *Env) Service(t testing.TB) *auth.Service { return e.ServiceWith(t, e.Deps()) }

// ServiceWith builds a Service from explicit dependencies (a second instance over the same store,
// a counting Hasher).
func (e *Env) ServiceWith(t testing.TB, d auth.Deps) *auth.Service {
	t.Helper()
	s, err := auth.New(d)
	if err != nil {
		t.Fatalf("authtest: auth.New: %v", err)
	}
	return s
}

// AuditRow is one audit_log row as the tests read it.
type AuditRow struct {
	Action    string
	ActorKind string
	ActorID   uuid.UUID // uuid.Nil when NULL
	Entity    string
	EntityID  uuid.UUID // uuid.Nil when NULL
	Before    string    // JSON text, "" when NULL
	After     string    // JSON text, "" when NULL
	CreatedAt string
}

// AuditRows returns every audit_log row in write order.
func (e *Env) AuditRows(t testing.TB) []AuditRow {
	t.Helper()
	rows, err := e.Pool.Query(context.Background(), `SELECT action, actor_kind, actor_id, entity, entity_id,
		COALESCE(before::text, ''), COALESCE(after::text, ''), created_at::text FROM audit_log ORDER BY created_at, id`)
	if err != nil {
		t.Fatalf("authtest: audit rows: %v", err)
	}
	defer rows.Close()
	var out []AuditRow
	for rows.Next() {
		var r AuditRow
		var actor, entity *uuid.UUID
		if err := rows.Scan(&r.Action, &r.ActorKind, &actor, &r.Entity, &entity, &r.Before, &r.After, &r.CreatedAt); err != nil {
			t.Fatalf("authtest: scan audit row: %v", err)
		}
		if actor != nil {
			r.ActorID = *actor
		}
		if entity != nil {
			r.EntityID = *entity
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("authtest: audit rows: %v", err)
	}
	return out
}

// AuditOf returns the rows with the given action, in write order.
func (e *Env) AuditOf(t testing.TB, action string) []AuditRow {
	t.Helper()
	var out []AuditRow
	for _, r := range e.AuditRows(t) {
		if r.Action == action {
			out = append(out, r)
		}
	}
	return out
}

// Admin is the audit actor of an admin user, System the system actor.
func Admin(id uuid.UUID) audit.Actor { return audit.Actor{ID: id, Kind: audit.KindAdmin} }

// System is the system audit actor (ID NULL).
func System() audit.Actor { return audit.Actor{Kind: audit.KindSystem} }
