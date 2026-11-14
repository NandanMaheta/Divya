package authz_test

// S08 - Principal.AuditActor (contracts/go-capabilities.md section 1; plan C-9):
// {ID: p.UserID, Kind: string(p.Role)}, so admin operations audit as the signed-in admin.

import (
	"testing"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/authz"
)

func TestAuditExt_PrincipalAuditActor(t *testing.T) {
	user := uuid.New()
	tests := []struct {
		name string
		p    authz.Principal
		want audit.Actor
	}{
		{"admin", authz.Principal{UserID: user, OrgID: uuid.New(), SessionID: uuid.New(), Role: authz.RoleAdmin}, audit.Actor{ID: user, Kind: audit.KindAdmin}},
		{"client", authz.Principal{UserID: user, OrgID: uuid.New(), SessionID: uuid.New(), Role: authz.RoleClient}, audit.Actor{ID: user, Kind: audit.KindClient}},
		{"mfa challenge principal has no session", authz.Principal{UserID: user, OrgID: uuid.New(), Role: authz.RoleAdmin}, audit.Actor{ID: user, Kind: audit.KindAdmin}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.AuditActor(); got != tc.want {
				t.Fatalf("AuditActor() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestAuditExt_PrincipalAuditActorKindsAreAcceptedByAuditWrite(t *testing.T) {
	for _, r := range []authz.Role{authz.RoleAdmin, authz.RoleClient} {
		switch k := (authz.Principal{UserID: uuid.New(), Role: r}).AuditActor().Kind; k {
		case audit.KindAdmin, audit.KindClient:
		default:
			t.Errorf("role %q maps to actor kind %q, which audit.Write rejects", r, k)
		}
	}
}
