// Package authtest is test support for internal/auth and the features that build on it: a migrated
// database with one org, a fake clock, the real Store, fixed key rings, fixture users per status
// and role, device sessions, and the Secrets registry behind the no-secrets scan (SC-006).
//
// It is imported by tests only. Fixtures are written with plain SQL, not through the Store, so a
// Store bug cannot hide behind a fixture. The Service factory (fake clock plus the real Store) is
// added with the Service skeleton in S10/S11.
package authtest

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/clock"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/crypto"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/dbtest"
)

// T0 is the fake clock's start: a fixed instant, well inside every fixture's validity.
var T0 = time.Date(2026, time.July, 1, 10, 0, 0, 0, time.UTC)

// Password is the raw password of every fixture user unless a spec says otherwise. It satisfies
// the password policy and is registered with Env.Secrets.
const Password = "river-lantern-42-monsoon"

// CheapHasherParams keep Argon2 fast in tests; Env.Hasher uses them as its "current" parameters.
var CheapHasherParams = auth.HasherParams{MemoryKiB: 8, Time: 1, Threads: 1}

// Env is one isolated test world.
type Env struct {
	Pool    *pgxpool.Pool
	Tx      db.TxRunner
	Clock   *clock.Fake
	Org     uuid.UUID
	Store   auth.Store
	Hasher  *auth.Hasher
	Keyring *crypto.Keyring
	Blind   crypto.BlindIndexer
	Signing *auth.SigningKeys
	Secrets *Secrets
}

// New returns an Env on a fresh migrated database with exactly one org, a clock at T0 and the
// real Store.
func New(t *testing.T) *Env {
	t.Helper()
	pool := dbtest.New(t)
	e := &Env{
		Pool:    pool,
		Tx:      db.NewTxRunner(pool),
		Clock:   clock.NewFake(T0),
		Org:     uuid.New(),
		Hasher:  auth.NewHasher(auth.HasherOptions{Params: CheapHasherParams}),
		Keyring: Keyring(t),
		Blind:   BlindIndexer(t),
		Signing: SigningKeys(t),
		Secrets: NewSecrets(),
	}
	e.Store = auth.NewPGStore(e.Tx)
	e.Exec(t, `INSERT INTO orgs (id, name) VALUES ($1, 'Swastik')`, e.Org)
	e.Secrets.Add("fixture password", Password)
	return e
}

// Exec runs one statement and fails the test on error.
func (e *Env) Exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := e.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("authtest: %q: %v", sql, err)
	}
}

// Keyring is a fixed two-key AES-256-GCM ring (active key 2, key 1 still opens old envelopes).
func Keyring(t testing.TB) *crypto.Keyring {
	t.Helper()
	k, err := crypto.NewKeyring(2, map[uint16][]byte{1: fill(32, 0x11), 2: fill(32, 0x22)})
	if err != nil {
		t.Fatalf("authtest: keyring: %v", err)
	}
	return k
}

// BlindIndexer is a fixed-key blind indexer.
func BlindIndexer(t testing.TB) crypto.BlindIndexer {
	t.Helper()
	b, err := crypto.NewBlindIndexer(fill(32, 0x33))
	if err != nil {
		t.Fatalf("authtest: blind indexer: %v", err)
	}
	return b
}

// SigningKeys is a fixed ring of two Ed25519 keys; "k2" is active, "k1" still verifies.
func SigningKeys(t testing.TB) *auth.SigningKeys {
	t.Helper()
	s, err := auth.NewSigningKeys("k2", map[string][]byte{"k1": fill(32, 0x44), "k2": fill(32, 0x55)})
	if err != nil {
		t.Fatalf("authtest: signing keys: %v", err)
	}
	return s
}

func fill(n int, b byte) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

// UserSpec describes a fixture user; zero values pick: role client, status active, a unique
// email and mobile, the shared Password.
type UserSpec struct {
	Role     string // "client" (default) | "admin"
	Status   auth.Status
	Name     string
	Email    string
	Mobile   string // admins get none unless set
	Password string
}

// User is a fixture user as written.
type User struct {
	ID       uuid.UUID
	Role     string
	Status   auth.Status
	Name     string
	Email    string
	Mobile   string
	Password string
}

var seq atomic.Int64

// NewUser inserts one user with a real Argon2id hash of its password. A status other than
// pending_verification gets verified contacts (an admin only an email); disabled users remember
// "active" as the status before disabling.
func (e *Env) NewUser(t *testing.T, s UserSpec) User {
	t.Helper()
	n := seq.Add(1)
	if s.Role == "" {
		s.Role = "client"
	}
	if s.Status == "" {
		s.Status = auth.StatusActive
	}
	if s.Name == "" {
		s.Name = fmt.Sprintf("Fixture User %d", n)
	}
	if s.Email == "" {
		s.Email = fmt.Sprintf("user%d@example.com", n)
	}
	if s.Mobile == "" && s.Role == "client" {
		s.Mobile = fmt.Sprintf("+9190%08d", n)
	}
	if s.Password == "" {
		s.Password = Password
	}
	e.Secrets.Add("fixture password", s.Password)
	phc, err := e.Hasher.Hash(context.Background(), auth.NormalizePassword(s.Password))
	if err != nil {
		t.Fatalf("authtest: hash: %v", err)
	}
	var emailVerified, mobileVerified any
	if s.Status != auth.StatusPendingVerification {
		emailVerified = T0
		if s.Mobile != "" {
			mobileVerified = T0
		}
	}
	var before any
	if s.Status == auth.StatusDisabled {
		before = string(auth.StatusActive)
	}
	var mobile any
	if s.Mobile != "" {
		mobile = s.Mobile
	}
	u := User{ID: uuid.New(), Role: s.Role, Status: s.Status, Name: s.Name, Email: s.Email, Mobile: s.Mobile, Password: s.Password}
	e.Exec(t, `INSERT INTO users (id, org_id, role, status, status_before_disabled, name, email, mobile,
		email_verified_at, mobile_verified_at, password_hash, password_changed_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12,$12)`,
		u.ID, e.Org, u.Role, string(u.Status), before, u.Name, u.Email, mobile, emailVerified, mobileVerified, phc, T0)
	return u
}

// UserWithStatus is a client in the given status.
func (e *Env) UserWithStatus(t *testing.T, status auth.Status) User {
	t.Helper()
	return e.NewUser(t, UserSpec{Status: status})
}

// NewAdmin is an active admin with an email and no mobile.
func (e *Env) NewAdmin(t *testing.T) User {
	t.Helper()
	return e.NewUser(t, UserSpec{Role: "admin"})
}

// SessionSpec describes a fixture device session; zero values pick: a new device id, android
// mobile, created at the clock's current time, idle 30 days and absolute 90 days from creation.
type SessionSpec struct {
	DeviceID   uuid.UUID
	DeviceName string
	Platform   string
	ClientType string
	MFAMethod  string
	Idle       time.Duration
	Absolute   time.Duration
}

// NewSession inserts a live session for the user, created at the clock's current time.
func (e *Env) NewSession(t *testing.T, user uuid.UUID, s SessionSpec) uuid.UUID {
	t.Helper()
	if s.DeviceID == uuid.Nil {
		s.DeviceID = uuid.New()
	}
	if s.DeviceName == "" {
		s.DeviceName = "Pixel 9"
	}
	if s.Platform == "" {
		s.Platform = "android"
	}
	if s.ClientType == "" {
		s.ClientType = "mobile"
	}
	if s.Idle == 0 {
		s.Idle = 30 * 24 * time.Hour
	}
	if s.Absolute == 0 {
		s.Absolute = 90 * 24 * time.Hour
	}
	var mfa any
	if s.MFAMethod != "" {
		mfa = s.MFAMethod
	}
	now := e.Clock.Now()
	id := uuid.New()
	e.Exec(t, `INSERT INTO device_sessions (id, org_id, user_id, device_id, device_name, platform, client_type,
		mfa_method, created_at, last_active_at, idle_expires_at, absolute_expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,$10,$11)`,
		id, e.Org, user, s.DeviceID, s.DeviceName, s.Platform, s.ClientType, mfa, now, now.Add(s.Idle), now.Add(s.Absolute))
	return id
}

// AuditActions returns every audit_log action in write order (created_at, then insertion).
func (e *Env) AuditActions(t *testing.T) []string {
	t.Helper()
	rows, err := e.Pool.Query(context.Background(), `SELECT action FROM audit_log ORDER BY created_at, id`)
	if err != nil {
		t.Fatalf("authtest: audit actions: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatalf("authtest: scan: %v", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("authtest: audit actions: %v", err)
	}
	return out
}
