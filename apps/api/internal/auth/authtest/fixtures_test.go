package authtest_test

// S08 - self-test of the authtest fixtures: they build, every fixture row satisfies the schema's
// CHECK constraints, and the Secrets registry finds recorded values without printing them.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
	"github.com/NandanMaheta/swastik/apps/api/internal/auth/authtest"
)

func TestAuthtest_EnvHasOneOrgAndAFixedClock(t *testing.T) {
	e := authtest.New(t)
	var n int
	if err := e.Pool.QueryRow(context.Background(), `SELECT count(*) FROM orgs`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("orgs = %d (%v), want exactly 1", n, err)
	}
	if !e.Clock.Now().Equal(authtest.T0) {
		t.Fatalf("clock = %v, want T0 %v", e.Clock.Now(), authtest.T0)
	}
	if e.Store == nil || e.Hasher == nil || e.Keyring == nil || e.Signing == nil || e.Secrets == nil {
		t.Fatal("Env has a nil collaborator")
	}
}

func TestAuthtest_UsersInEveryStatusAndRoleSatisfyTheSchema(t *testing.T) {
	e := authtest.New(t)
	for _, st := range []auth.Status{auth.StatusPendingVerification, auth.StatusPendingApproval, auth.StatusActive, auth.StatusDisabled} {
		u := e.UserWithStatus(t, st)
		var status string
		var before *string
		if err := e.Pool.QueryRow(context.Background(), `SELECT status, status_before_disabled FROM users WHERE id=$1`, u.ID).Scan(&status, &before); err != nil {
			t.Fatalf("%s: %v", st, err)
		}
		if status != string(st) {
			t.Errorf("status = %s, want %s", status, st)
		}
		if (st == auth.StatusDisabled) != (before != nil) {
			t.Errorf("%s: status_before_disabled = %v", st, before)
		}
	}
	a := e.NewAdmin(t)
	if a.Role != "admin" || a.Mobile != "" {
		t.Fatalf("admin fixture = %+v, want role admin and no mobile", a)
	}
}

func TestAuthtest_FixtureUsersAreUniqueAndVerifyTheirPassword(t *testing.T) {
	e := authtest.New(t)
	a, b := e.NewUser(t, authtest.UserSpec{}), e.NewUser(t, authtest.UserSpec{})
	if a.Email == b.Email || a.Mobile == b.Mobile || a.ID == b.ID {
		t.Fatalf("fixtures collide: %+v / %+v", a, b)
	}
	var phc string
	if err := e.Pool.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE id=$1`, a.ID).Scan(&phc); err != nil {
		t.Fatal(err)
	}
	ok, err := e.Hasher.Verify(context.Background(), phc, auth.NormalizePassword(a.Password))
	if err != nil || !ok {
		t.Fatalf("Verify(fixture password) = %v, %v; want true", ok, err)
	}
}

func TestAuthtest_FixtureUsersAreReadableThroughTheStore(t *testing.T) {
	e := authtest.New(t)
	u := e.UserWithStatus(t, auth.StatusPendingApproval)
	got, err := e.Store.GetUser(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("Store.GetUser(fixture) = %v", err)
	}
	if got.Email != u.Email || got.Status != auth.StatusPendingApproval || got.OrgID != e.Org {
		t.Fatalf("Store.GetUser = %+v, want the fixture %+v", got, u)
	}
}

func TestAuthtest_SessionsAreLiveAtTheClockAndHonourOverrides(t *testing.T) {
	e := authtest.New(t)
	u := e.NewUser(t, authtest.UserSpec{})
	id := e.NewSession(t, u.ID, authtest.SessionSpec{})
	var idle, abs, created, last = e.Clock.Now(), e.Clock.Now(), e.Clock.Now(), e.Clock.Now()
	if err := e.Pool.QueryRow(context.Background(), `SELECT created_at, last_active_at, idle_expires_at, absolute_expires_at FROM device_sessions WHERE id=$1`, id).Scan(&created, &last, &idle, &abs); err != nil {
		t.Fatal(err)
	}
	if !created.Equal(authtest.T0) || idle.Sub(created).Hours() != 30*24 || abs.Sub(created).Hours() != 90*24 {
		t.Fatalf("session times = %v %v %v, want created T0, idle +30d, absolute +90d", created, idle, abs)
	}
	admin := e.NewAdmin(t)
	e.NewSession(t, admin.ID, authtest.SessionSpec{ClientType: "web", Platform: "web", MFAMethod: "totp", Idle: 12 * time.Hour, Absolute: 7 * 24 * time.Hour})
}

func TestAuthtest_KeyRingsAreDeterministicAndRotatable(t *testing.T) {
	k := authtest.Keyring(t)
	env, err := k.Seal([]byte("x"), []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	if id, err := k.KeyID(env); err != nil || id != 2 {
		t.Fatalf("active key id = %d (%v), want 2", id, err)
	}
	if got, err := authtest.Keyring(t).Open(env, []byte("aad")); err != nil || string(got) != "x" {
		t.Fatalf("a second keyring instance must open the same envelope: %q %v", got, err)
	}
	if a, b := authtest.BlindIndexer(t).Index("throttle", []byte("v")), authtest.BlindIndexer(t).Index("throttle", []byte("v")); string(a) != string(b) {
		t.Fatal("blind indexer is not deterministic across instances")
	}
}

func TestAuthtest_SecretsFindsEveryRecordedFormAndNamesOnlyTheLabel(t *testing.T) {
	s := authtest.NewSecrets()
	s.Add("access token", "dat_abc123def456")
	s.Add("short", "ab") // ignored: too short to scan for
	s.AddTOTPSecret("JBSWY3DPEHPK3PXP")

	if got := s.Hits("nothing to see here, ab"); len(got) != 0 {
		t.Fatalf("clean text hits = %v", got)
	}
	got := s.Hits(`{"token":"dat_abc123def456","k":"JBSWY3DPEHPK3PXP"}`)
	if strings.Join(got, ",") != "access token,totp secret (base32)" {
		t.Fatalf("hits = %v", got)
	}
	// 'Hello!\xDE\xAD\xBE\xEF' base32 -> hex form of the raw secret bytes is also searched.
	raw := "48656c6c6f21deadbeef"
	s2 := authtest.NewSecrets()
	s2.AddTOTPSecret("JBSWY3DPEHPK3PXP") // raw bytes: "Hello!\xde\xad\xbe\xef"
	if got := s2.Hits("secret=" + raw); len(got) != 1 || got[0] != "totp secret (hex)" {
		t.Fatalf("hex form not detected: %v", got)
	}
}

type recordingTB struct {
	testing.TB
	msgs []string
}

func (r *recordingTB) Helper() {}
func (r *recordingTB) Errorf(format string, args ...any) {
	r.msgs = append(r.msgs, strings.TrimSpace(sprintf(format, args...)))
}

func TestAuthtest_ScanFailsWithoutEchoingTheSecret(t *testing.T) {
	s := authtest.NewSecrets()
	s.Add("refresh token", "drt_supersecretvalue")
	rec := &recordingTB{TB: t}
	s.Scan(rec, "slog output", "level=INFO token=drt_supersecretvalue")
	if len(rec.msgs) != 1 || !strings.Contains(rec.msgs[0], "refresh token") || !strings.Contains(rec.msgs[0], "slog output") {
		t.Fatalf("messages = %v, want one naming the label and the source", rec.msgs)
	}
	if strings.Contains(rec.msgs[0], "drt_supersecretvalue") {
		t.Fatalf("the failure message echoes the secret: %q", rec.msgs[0])
	}
}

func TestAuthtest_ScanAuditLogReadsEveryRowAsText(t *testing.T) {
	e := authtest.New(t)
	e.Exec(t, `INSERT INTO audit_log (org_id, actor_kind, entity, action, after) VALUES ($1,'system','user','auth.sign_in.failed','{"reason":"leaky-password-value"}')`, e.Org)
	e.Secrets.Add("leaked", "leaky-password-value")
	rec := &recordingTB{TB: t}
	e.Secrets.ScanAuditLog(context.Background(), rec, e.Pool)
	if len(rec.msgs) != 1 || !strings.Contains(rec.msgs[0], "audit_log") {
		t.Fatalf("messages = %v, want one audit_log leak", rec.msgs)
	}
}

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }
