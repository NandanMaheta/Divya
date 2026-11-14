package audit_test

// T025 - audit.Write (specs/002-scheduler-tick/contracts/go-module-api.md, "internal/platform/audit").
//
// Contract under test: audit.Actor{ID, Kind}, audit.Entry{OrgID, Actor, Entity, EntityID, Action,
// Before, After}, audit.Write(ctx, Entry) error. Refs: FR-020, constitution II.
//
// Assumptions (not spelled out by the contract):
//   - Write needs a transaction in ctx (db.TxRunner.InTx) and returns db.ErrNoTx otherwise,
//     writing nothing; the row commits or rolls back with the caller's transaction.
//   - A nil Before/After is stored as SQL NULL (the columns are nullable), not as JSON null.
//   - An Actor with the zero uuid (the "system" actor) is stored with actor_id NULL.
//   - Before/After are stored as the JSON encoding of the value passed.
//   - Invalid actor kinds and unmarshalable values are returned as errors, never panics.
//   - Tests use the 00001 schema only; the audit table already exists.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/dbtest"
)

type fixture struct {
	pool *pgxpool.Pool
	txr  db.TxRunner
	org  uuid.UUID
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool := dbtest.New(t)
	var org uuid.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO orgs (name) VALUES ('Swastik') RETURNING id`).Scan(&org); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	return fixture{pool: pool, txr: db.NewTxRunner(pool), org: org}
}

func (f fixture) count(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_log`).Scan(&n); err != nil {
		t.Fatalf("count audit_log: %v", err)
	}
	return n
}

func decode(t *testing.T, raw []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("stored value %q is not JSON: %v", raw, err)
	}
	return v
}

func TestAudit_WriteStoresActorEntityActionAndBeforeAfterInsideATransaction(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	admin, entity := uuid.New(), uuid.New()
	type state struct {
		Status   string `json:"status"`
		Attempts int    `json:"attempts"`
	}

	err := f.txr.InTx(ctx, func(ctx context.Context) error {
		return audit.Write(ctx, audit.Entry{
			OrgID:    f.org,
			Actor:    audit.Actor{ID: admin, Kind: "admin"},
			Entity:   "scheduled_job",
			EntityID: entity,
			Action:   "scheduled_job.retry",
			Before:   state{Status: "dead", Attempts: 8},
			After:    map[string]any{"status": "pending", "attempts": 0},
		})
	})
	if err != nil {
		t.Fatalf("Write in a transaction: %v", err)
	}

	var (
		org, actor, entityID uuid.UUID
		kind, ent, action    string
		before, after        []byte
		createdSet           bool
	)
	err = f.pool.QueryRow(ctx, `SELECT org_id, actor_id, actor_kind, entity, entity_id, action, before, after, created_at IS NOT NULL
		FROM audit_log`).Scan(&org, &actor, &kind, &ent, &entityID, &action, &before, &after, &createdSet)
	if err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if org != f.org || actor != admin || kind != "admin" || ent != "scheduled_job" || entityID != entity ||
		action != "scheduled_job.retry" || !createdSet {
		t.Errorf("row = org %v actor %v/%s entity %s/%v action %s created=%v", org, actor, kind, ent, entityID, action, createdSet)
	}
	if got, want := decode(t, before), any(map[string]any{"status": "dead", "attempts": float64(8)}); !reflect.DeepEqual(got, want) {
		t.Errorf("before = %v, want %v", got, want)
	}
	if got, want := decode(t, after), any(map[string]any{"status": "pending", "attempts": float64(0)}); !reflect.DeepEqual(got, want) {
		t.Errorf("after = %v, want %v", got, want)
	}
}

func TestAudit_WriteWithoutTransactionReturnsErrNoTxAndWritesNothing(t *testing.T) {
	f := newFixture(t)

	err := audit.Write(t.Context(), audit.Entry{
		OrgID: f.org, Actor: audit.Actor{ID: uuid.New(), Kind: "admin"},
		Entity: "scheduled_job", EntityID: uuid.New(), Action: "scheduled_job.cancel",
	})
	if !errors.Is(err, db.ErrNoTx) {
		t.Fatalf("Write without tx = %v, want db.ErrNoTx", err)
	}
	if n := f.count(t); n != 0 {
		t.Fatalf("%d audit rows written without a transaction", n)
	}
}

func TestAudit_WriteIsRolledBackWithTheCaller(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	boom := errors.New("business change failed")

	err := f.txr.InTx(ctx, func(ctx context.Context) error {
		if err := audit.Write(ctx, audit.Entry{
			OrgID: f.org, Actor: audit.Actor{ID: uuid.New(), Kind: "admin"},
			Entity: "quiet_hours", EntityID: f.org, Action: "quiet_hours.update",
		}); err != nil {
			t.Errorf("Write: %v", err)
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("InTx = %v, want the caller's error", err)
	}
	if n := f.count(t); n != 0 {
		t.Fatalf("%d audit rows survived the caller's rollback (constitution II: audit commits with the change)", n)
	}
}

func TestAudit_SeveralWritesInOneTransactionCommitTogether(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	err := f.txr.InTx(ctx, func(ctx context.Context) error {
		for _, action := range []string{"outbox_message.retry", "outbox_message.cancel", "quiet_hours.update"} {
			if err := audit.Write(ctx, audit.Entry{
				OrgID: f.org, Actor: audit.Actor{ID: uuid.New(), Kind: "admin"},
				Entity: "x", EntityID: uuid.New(), Action: action,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("InTx: %v", err)
	}
	if n := f.count(t); n != 3 {
		t.Fatalf("rows = %d, want 3", n)
	}
}

func TestAudit_NilBeforeAndAfterAreStoredAsNull(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	err := f.txr.InTx(ctx, func(ctx context.Context) error {
		return audit.Write(ctx, audit.Entry{
			OrgID: f.org, Actor: audit.Actor{ID: uuid.New(), Kind: "admin"},
			Entity: "quiet_hours", EntityID: f.org, Action: "quiet_hours.update",
		})
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	var beforeNull, afterNull bool
	if err := f.pool.QueryRow(ctx, `SELECT before IS NULL, after IS NULL FROM audit_log`).Scan(&beforeNull, &afterNull); err != nil {
		t.Fatal(err)
	}
	if !beforeNull || !afterNull {
		t.Fatalf("before NULL=%v, after NULL=%v; nil values must be stored as SQL NULL", beforeNull, afterNull)
	}
}

func TestAudit_ActorKinds(t *testing.T) {
	tests := []struct {
		name      string
		actor     audit.Actor
		wantErr   bool
		wantActor bool // actor_id stored
	}{
		{"admin with id", audit.Actor{ID: uuid.New(), Kind: "admin"}, false, true},
		{"client with id", audit.Actor{ID: uuid.New(), Kind: "client"}, false, true},
		{"system without id", audit.Actor{Kind: "system"}, false, false},
		{"unknown kind is rejected", audit.Actor{ID: uuid.New(), Kind: "robot"}, true, false},
		{"empty kind is rejected", audit.Actor{ID: uuid.New()}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			ctx := t.Context()
			err := f.txr.InTx(ctx, func(ctx context.Context) error {
				return audit.Write(ctx, audit.Entry{
					OrgID: f.org, Actor: tt.actor, Entity: "scheduled_job", EntityID: uuid.New(), Action: "scheduled_job.cancel",
				})
			})
			if tt.wantErr {
				if err == nil {
					t.Fatal("Write accepted an invalid actor kind")
				}
				if errors.Is(err, db.ErrNoTx) {
					t.Fatalf("rejected for the wrong reason (no transaction): %v", err)
				}
				if n := f.count(t); n != 0 {
					t.Fatalf("%d rows written for an invalid entry", n)
				}
				return
			}
			if err != nil {
				t.Fatalf("Write: %v", err)
			}
			var hasActor bool
			var kind string
			if err := f.pool.QueryRow(ctx, `SELECT actor_id IS NOT NULL, actor_kind FROM audit_log`).Scan(&hasActor, &kind); err != nil {
				t.Fatal(err)
			}
			if hasActor != tt.wantActor || kind != tt.actor.Kind {
				t.Fatalf("stored actor_id present=%v kind=%q, want present=%v kind=%q", hasActor, kind, tt.wantActor, tt.actor.Kind)
			}
		})
	}
}

func TestAudit_WriteReturnsAnErrorForAValueThatCannotBeEncoded(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	err := f.txr.InTx(ctx, func(ctx context.Context) error {
		return audit.Write(ctx, audit.Entry{
			OrgID: f.org, Actor: audit.Actor{ID: uuid.New(), Kind: "admin"},
			Entity: "scheduled_job", EntityID: uuid.New(), Action: "scheduled_job.retry",
			After: make(chan int), // json.Marshal fails
		})
	})
	if err == nil {
		t.Fatal("Write accepted a value that cannot be JSON encoded")
	}
	if errors.Is(err, db.ErrNoTx) {
		t.Fatalf("failed for the wrong reason: %v", err)
	}
	if n := f.count(t); n != 0 {
		t.Fatalf("%d rows written", n)
	}
}

func TestAudit_WriteForAnUnknownOrgFails(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	err := f.txr.InTx(ctx, func(ctx context.Context) error {
		return audit.Write(ctx, audit.Entry{
			OrgID: uuid.New(), Actor: audit.Actor{ID: uuid.New(), Kind: "admin"},
			Entity: "scheduled_job", EntityID: uuid.New(), Action: "scheduled_job.retry",
		})
	})
	if err == nil {
		t.Fatal("Write accepted an org that does not exist")
	}
	if n := f.count(t); n != 0 {
		t.Fatalf("%d rows written", n)
	}
}
