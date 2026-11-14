// Package audit writes append-only audit_log rows in the caller's transaction (constitution II).
package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db/dbgen"
)

// Actor kinds accepted by audit_log.actor_kind.
const (
	KindAdmin  = "admin"
	KindClient = "client"
	KindSystem = "system"
)

// Actor is who made a change. The zero ID (the system actor) is stored as NULL.
type Actor struct {
	ID   uuid.UUID
	Kind string // "admin" | "client" | "system"
}

// Entry is one audit row. Before and After are stored as their JSON encoding; nil is SQL NULL.
type Entry struct {
	OrgID         uuid.UUID
	Actor         Actor
	Entity        string
	EntityID      uuid.UUID
	Action        string
	Before, After any
}

// Write inserts the entry in the transaction carried by ctx, so it commits or rolls back with the
// caller's change. It returns db.ErrNoTx, writing nothing, when ctx has no transaction.
func Write(ctx context.Context, e Entry) error {
	tx, err := db.MustTx(ctx)
	if err != nil {
		return fmt.Errorf("audit: write %s: %w", e.Action, err)
	}
	switch e.Actor.Kind {
	case KindAdmin, KindClient, KindSystem:
	default:
		return fmt.Errorf("audit: write %s: invalid actor kind %q", e.Action, e.Actor.Kind)
	}
	before, err := encode(e.Before)
	if err != nil {
		return fmt.Errorf("audit: write %s: encode before: %w", e.Action, err)
	}
	after, err := encode(e.After)
	if err != nil {
		return fmt.Errorf("audit: write %s: encode after: %w", e.Action, err)
	}
	err = dbgen.New(tx).InsertAuditLog(ctx, dbgen.InsertAuditLogParams{
		OrgID:     e.OrgID,
		ActorID:   uuid.NullUUID{UUID: e.Actor.ID, Valid: e.Actor.ID != uuid.Nil},
		ActorKind: e.Actor.Kind,
		Entity:    e.Entity,
		EntityID:  uuid.NullUUID{UUID: e.EntityID, Valid: e.EntityID != uuid.Nil},
		Action:    e.Action,
		Before:    before,
		After:     after,
	})
	if err != nil {
		return fmt.Errorf("audit: write %s: %w", e.Action, err)
	}
	return nil
}

func encode(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	return b, nil
}
