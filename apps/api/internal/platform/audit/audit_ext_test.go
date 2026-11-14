package audit_test

// S08 - additive extension of audit.Write (specs/003-auth-core plan C-4, FR-090, US-11 s3):
//
//   - Entry.At is stored as audit_log.created_at; the zero value keeps the column default, so
//     002's existing calls are unchanged. 003 always sets At from its injected clock.
//   - uuid.Nil for EntityID or Actor.ID is stored as SQL NULL (unknown identifier, system actor).
//   - Write without a transaction is db.ErrNoTx; a rolled-back caller leaves no row; rows are
//     append-only (UPDATE and DELETE are refused by the forbid_mutation trigger).
//
// Assumption: a zero At is detected with time.Time.IsZero.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
)

var extT0 = time.Date(2026, time.July, 1, 10, 4, 0, 0, time.UTC)

func (f fixture) write(t *testing.T, e audit.Entry) {
	t.Helper()
	e.OrgID = f.org
	err := f.txr.InTx(t.Context(), func(ctx context.Context) error { return audit.Write(ctx, e) })
	if err != nil {
		t.Fatalf("audit.Write: %v", err)
	}
}

func (f fixture) createdAt(t *testing.T, action string) time.Time {
	t.Helper()
	var at time.Time
	if err := f.pool.QueryRow(t.Context(), `SELECT created_at FROM audit_log WHERE action = $1`, action).Scan(&at); err != nil {
		t.Fatalf("read created_at of %s: %v", action, err)
	}
	return at
}

func TestAuditExt_AtIsStoredAsCreatedAt(t *testing.T) {
	f := newFixture(t)
	user := uuid.New()
	f.write(t, audit.Entry{
		Actor: audit.Actor{ID: user, Kind: audit.KindClient}, Entity: "user", EntityID: user,
		Action: "auth.sign_in.succeeded", At: extT0,
	})
	if got := f.createdAt(t, "auth.sign_in.succeeded"); !got.Equal(extT0) {
		t.Fatalf("created_at = %v, want the entry's At %v", got, extT0)
	}
}

func TestAuditExt_AtKeepsSubSecondPrecisionAndTheInstantAcrossZones(t *testing.T) {
	f := newFixture(t)
	ist := time.FixedZone("IST", 5*3600+1800)
	at := time.Date(2026, time.July, 1, 15, 34, 0, 123456000, ist) // 10:04:00.123456 UTC
	f.write(t, audit.Entry{Actor: audit.Actor{Kind: audit.KindSystem}, Entity: "auth", Action: "auth.lockout.started", At: at})
	if got := f.createdAt(t, "auth.lockout.started"); !got.Equal(at) {
		t.Fatalf("created_at = %v, want the same instant as %v", got, at)
	}
}

func TestAuditExt_AtInThePastAndFutureIsHonoured(t *testing.T) {
	f := newFixture(t)
	past, future := extT0.AddDate(-1, 0, 0), extT0.AddDate(1, 0, 0)
	f.write(t, audit.Entry{Actor: audit.Actor{Kind: audit.KindSystem}, Entity: "auth", Action: "past", At: past})
	f.write(t, audit.Entry{Actor: audit.Actor{Kind: audit.KindSystem}, Entity: "auth", Action: "future", At: future})
	if got := f.createdAt(t, "past"); !got.Equal(past) {
		t.Errorf("past created_at = %v, want %v", got, past)
	}
	if got := f.createdAt(t, "future"); !got.Equal(future) {
		t.Errorf("future created_at = %v, want %v", got, future)
	}
}

func TestAuditExt_ZeroAtKeepsTheColumnDefault(t *testing.T) {
	f := newFixture(t)
	before := time.Now().Add(-time.Minute)
	f.write(t, audit.Entry{Actor: audit.Actor{ID: uuid.New(), Kind: audit.KindAdmin}, Entity: "quiet_hours", EntityID: f.org, Action: "quiet_hours.update"})
	got := f.createdAt(t, "quiet_hours.update")
	if got.Before(before) || got.After(time.Now().Add(time.Minute)) {
		t.Fatalf("created_at = %v, want the database default (about now)", got)
	}
	if got.Year() < 2026 || got.Equal(time.Time{}) {
		t.Fatalf("created_at = %v looks like a zero time written literally", got)
	}
}

func TestAuditExt_NilEntityIDIsStoredAsNull(t *testing.T) {
	f := newFixture(t)
	f.write(t, audit.Entry{
		Actor: audit.Actor{Kind: audit.KindSystem}, Entity: "user", EntityID: uuid.Nil,
		Action: "auth.sign_in.failed", After: map[string]string{"reason": "unknown_identifier"}, At: extT0,
	})
	var entityNull, actorNull bool
	err := f.pool.QueryRow(t.Context(), `SELECT entity_id IS NULL, actor_id IS NULL FROM audit_log WHERE action = 'auth.sign_in.failed'`).Scan(&entityNull, &actorNull)
	if err != nil {
		t.Fatalf("read row: %v", err)
	}
	if !entityNull {
		t.Error("entity_id is not NULL for uuid.Nil (it must not be stored as 00000000-0000-0000-0000-000000000000)")
	}
	if !actorNull {
		t.Error("actor_id is not NULL for the system actor")
	}
}

func TestAuditExt_NilActorIDWithARealEntityStoresOnlyTheActorAsNull(t *testing.T) {
	f := newFixture(t)
	user := uuid.New()
	f.write(t, audit.Entry{Actor: audit.Actor{Kind: audit.KindSystem}, Entity: "user", EntityID: user, Action: "auth.password.reset", At: extT0})
	var entity uuid.NullUUID
	var actorNull bool
	if err := f.pool.QueryRow(t.Context(), `SELECT entity_id, actor_id IS NULL FROM audit_log`).Scan(&entity, &actorNull); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if !entity.Valid || entity.UUID != user || !actorNull {
		t.Fatalf("entity_id = %v, actor NULL = %v; want %v and true", entity, actorNull, user)
	}
}

func TestAuditExt_NoZeroUUIDIsEverStored(t *testing.T) {
	f := newFixture(t)
	f.write(t, audit.Entry{Actor: audit.Actor{Kind: audit.KindSystem}, Entity: "auth", Action: "auth.housekeeping.purged", At: extT0})
	var n int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_log WHERE entity_id = '00000000-0000-0000-0000-000000000000' OR actor_id = '00000000-0000-0000-0000-000000000000'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d rows hold the zero uuid instead of NULL", n)
	}
}

func TestAuditExt_WriteWithAtStillRequiresATransaction(t *testing.T) {
	f := newFixture(t)
	err := audit.Write(t.Context(), audit.Entry{OrgID: f.org, Actor: audit.Actor{Kind: audit.KindSystem}, Entity: "auth", Action: "x", At: extT0})
	if !errors.Is(err, db.ErrNoTx) {
		t.Fatalf("Write without tx = %v, want db.ErrNoTx", err)
	}
	if n := f.count(t); n != 0 {
		t.Fatalf("%d rows written without a transaction", n)
	}
}

func TestAuditExt_RollbackLeavesNoRowEvenWithAt(t *testing.T) {
	f := newFixture(t)
	boom := errors.New("business change failed")
	err := f.txr.InTx(t.Context(), func(ctx context.Context) error {
		if err := audit.Write(ctx, audit.Entry{OrgID: f.org, Actor: audit.Actor{Kind: audit.KindSystem}, Entity: "auth", Action: "x", At: extT0}); err != nil {
			t.Errorf("Write: %v", err)
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("InTx = %v, want the callback error", err)
	}
	if n := f.count(t); n != 0 {
		t.Fatalf("%d rows survived the rollback", n)
	}
}

func TestAuditExt_RowsWithAnExplicitCreatedAtStayAppendOnly(t *testing.T) {
	f := newFixture(t)
	f.write(t, audit.Entry{Actor: audit.Actor{Kind: audit.KindSystem}, Entity: "auth", Action: "x", At: extT0})
	for _, stmt := range []string{
		`UPDATE audit_log SET created_at = now()`,
		`UPDATE audit_log SET action = 'y'`,
		`DELETE FROM audit_log`,
	} {
		_, err := f.pool.Exec(t.Context(), stmt)
		var pe *pgconn.PgError
		if err == nil || !errors.As(err, &pe) {
			t.Errorf("%s: err = %v, want the append-only trigger to refuse it", stmt, err)
		}
	}
	if n := f.count(t); n != 1 {
		t.Fatalf("row count = %d, want 1", n)
	}
}

func TestAuditExt_EntriesOutOfWriteOrderSortByAt(t *testing.T) {
	f := newFixture(t)
	f.write(t, audit.Entry{Actor: audit.Actor{Kind: audit.KindSystem}, Entity: "auth", Action: "second", At: extT0.Add(4 * time.Minute)})
	f.write(t, audit.Entry{Actor: audit.Actor{Kind: audit.KindSystem}, Entity: "auth", Action: "first", At: extT0})
	rows, err := f.pool.Query(t.Context(), `SELECT action FROM audit_log ORDER BY created_at`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, a)
	}
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("order by created_at = %v, want [first second]", got)
	}
}
