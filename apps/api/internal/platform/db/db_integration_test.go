package db_test

import (
	"context"
	"testing"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/dbtest"
)

func TestMigrationsAndAuditLogIsAppendOnly(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()

	var orgID string
	if err := pool.QueryRow(ctx, `INSERT INTO orgs (name) VALUES ('Swastik') RETURNING id`).Scan(&orgID); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	var auditID string
	err := pool.QueryRow(ctx,
		`INSERT INTO audit_log (org_id, actor_kind, entity, action) VALUES ($1, 'system', 'org', 'create') RETURNING id`,
		orgID).Scan(&auditID)
	if err != nil {
		t.Fatalf("insert audit: %v", err)
	}

	for _, stmt := range []string{
		`UPDATE audit_log SET action = 'tampered' WHERE id = $1`,
		`DELETE FROM audit_log WHERE id = $1`,
	} {
		if _, err := pool.Exec(ctx, stmt, auditID); err == nil {
			t.Errorf("%q succeeded; audit_log must be append-only", stmt)
		}
	}
}
