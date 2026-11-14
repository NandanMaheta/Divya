# Exported Go contracts of 003 (for 004, 005, 007, 022, 002 Phase B and every later module)

Other modules use auth only through the contracts below. They never read the auth tables directly (constitution IV). The signatures are binding for the implementation; the names of unexported helpers are not.

Common rules:

- IDs are `uuid.UUID` (`github.com/google/uuid`, made direct by 002; sqlc override `uuid → uuid.UUID`).
- Time comes from 002's `clock.Func`; transactions from 002's `db.TxRunner`; audit entries from 002's `audit.Write`. 003 does not redefine any of them (plan C-2…C-4).
- No pgx or HTTP types appear in these signatures.

## 1. `internal/platform/authz` (every module; new in 003)

```go
type Role string
const (RoleAdmin Role = "admin"; RoleClient Role = "client")

type Principal struct {
    UserID, OrgID, SessionID uuid.UUID
    Role Role
}
func (p Principal) AuditActor() audit.Actor // {ID: p.UserID, Kind: string(p.Role)}

// Exactly one of these per huma.Operation.Security:
func Public() []map[string][]string                // []; must also be on the contract-test allowlist
func Require(roles ...Role) []map[string][]string  // [{bearer: [roles...]}]; no roles = any signed-in user
func ChallengeOnly() []map[string][]string         // [{mfaChallenge: []}]; auth MFA operations only
func SelfAuthenticated() []map[string][]string     // []; plus Metadata["authz"]="self"; ONLY for Hidden operations
                                                   // that authenticate themselves (002's /internal/tick). AssertDeclared panics
                                                   // if a documented operation uses it; the middleware answers 500 at runtime.

// Handlers:
func PrincipalFrom(ctx context.Context) (Principal, bool)
func MustPrincipal(ctx context.Context) Principal // panics if absent; only in operations declared with Require

// The only dependency of the middleware: implemented by internal/auth (bearer JWT + session lookup; mfaChallenge).
type Verifier interface {
    Verify(ctx context.Context, token string) (Principal, error) // any error => 401 auth.unauthenticated
}

// Wiring (cmd/swastik only):
func Install(api huma.API, verifiers map[string]Verifier) // before any huma.Register; registers security schemes + middleware
func AssertDeclared(api huma.API)                         // after all Register calls; panics on nil Security / unknown scheme
```

The middleware is fail-closed: an operation (documented or hidden) with nil `Security` gets 500 `platform.security_undeclared`. For an `mfaChallenge` principal, `SessionID` is `uuid.Nil` and the challenge id is visible only to `internal/auth`.

## 2. `internal/auth` Accounts capability (FR-050…FR-055; no HTTP operations)

Each method joins the caller's transaction when the context carries one (002's `TxRunner.InTx` nesting); otherwise it opens its own. Each writes its audit entry in that same transaction. The actor is 002's `audit.Actor` (`Kind` = `"system"`, `"admin"` or `"client"`; `ID` = `uuid.Nil` for system).

```go
type NewClient struct {
    OrgID                         uuid.UUID
    Name, Email, Mobile, Password string // raw input; normalised and policy-checked here; Password never logged
}

type Status string // "pending_verification" | "pending_approval" | "active" | "disabled"

type Accounts interface {
    // FR-050: role client, status pending_verification.
    // Errors: ErrEmailTaken, ErrMobileTaken, ErrInvalidIdentifier, *PolicyError.
    CreateClient(ctx context.Context, in NewClient, by audit.Actor) (uuid.UUID, error)
    // FR-051: idempotent; returns the resulting status (pending_approval once both are verified).
    MarkEmailVerified(ctx context.Context, userID uuid.UUID, by audit.Actor) (Status, error)
    MarkMobileVerified(ctx context.Context, userID uuid.UUID, by audit.Actor) (Status, error)
    // FR-052
    Approve(ctx context.Context, userID uuid.UUID, by audit.Actor) error // only from pending_approval, else ErrInvalidTransition
    Disable(ctx context.Context, userID uuid.UUID, by audit.Actor) error // ends all sessions; ErrLastAdmin
    Enable(ctx context.Context, userID uuid.UUID, by audit.Actor) error  // restores status_before_disabled; sessions stay ended
    // FR-053: after 004 has verified the person. Ends ALL sessions, clears the lock; *PolicyError.
    ResetPassword(ctx context.Context, userID uuid.UUID, newPassword string, by audit.Actor) error
    // FR-054: feature names the caller (audit after.feature).
    EndAllSessions(ctx context.Context, userID uuid.UUID, feature string, by audit.Actor) (ended int, err error)
    // FR-055: after 007's approval (BR-CLI-05 is enforced by 007). Status unchanged; uniqueness enforced.
    ReplaceEmail(ctx context.Context, userID uuid.UUID, email string, by audit.Actor) error
    ReplaceMobile(ctx context.Context, userID uuid.UUID, mobile string, by audit.Actor) error
    // Read-only, for 005/022 screens; never returns secrets.
    GetUser(ctx context.Context, userID uuid.UUID) (UserView, error)
}

type UserView struct {
    ID, OrgID                     uuid.UUID
    Name, Email, Mobile           string
    Role                          string
    Status                        Status
    EmailVerified, MobileVerified bool
    CreatedAt                     time.Time
    LastSignInAt                  *time.Time
}

var (
    ErrEmailTaken, ErrMobileTaken, ErrInvalidIdentifier error
    ErrInvalidTransition, ErrLastAdmin, ErrUserNotFound error
)
type PolicyError struct{ Code string } // auth.password_* (contracts/error-codes.md)
```

`CreateClient` and `ResetPassword` hash with Argon2id **before** joining the transaction, so a caller's transaction is never held open during hashing. A caller that already holds a transaction should validate and call these methods early in it.

## 3. `internal/auth` Throttle (for 004 OTP limits; plan P-07)

```go
type ThrottlePolicy struct{ MaxFailures int; Window, Lock time.Duration }

type Throttle interface {
    // scope must be in the auth_throttles CHECK list (004 extends it in its own migration).
    // key is passed through the blind index before storage; it is never stored as given.
    Check(ctx context.Context, orgID uuid.UUID, scope, key string) (lockedUntil *time.Time, err error)
    Fail(ctx context.Context, orgID uuid.UUID, scope, key string, p ThrottlePolicy) (startedLock bool, lockedUntil *time.Time, err error)
    Clear(ctx context.Context, orgID uuid.UUID, scope, key string) error
}
```

`Fail` and `Clear` require a transaction in the context (`db.ErrNoTx` otherwise) so they commit with the caller's audit entry.

## 4. `internal/auth` housekeeping (wired by `cmd/swastik` into 002's scheduler; plan P-20)

```go
const HousekeepingKind = "auth.housekeeping"
func HousekeepingNext(now time.Time) time.Time // istdate.NextDailyAt(now, istdate.WallClock{Hour: 3, Minute: 30})

type PurgeCounts struct{ Throttles, Challenges, RefreshTokens int }
// Requires the job transaction in ctx (db.MustTx). Idempotent; behaviour-neutral; one audit row when any count > 0.
func (s *Service) Housekeep(ctx context.Context, orgID uuid.UUID) (PurgeCounts, error)
```

Wiring in `cmd/swastik/main.go` (`internal/auth` does not import `internal/scheduler`):

```go
sch.Register(auth.HousekeepingKind,
    func(ctx context.Context, job scheduler.Job) error { _, err := authSvc.Housekeep(ctx, job.OrgID); return err },
    scheduler.KindOptions{Recurring: auth.HousekeepingNext})
```

## 5. `internal/platform/crypto` (new in 003; 006 reuses it for PAN)

```go
type Keyring struct{ /* active id + map[uint16][32]byte, built by config */ }
func NewKeyring(active uint16, keys map[uint16][]byte) (*Keyring, error)
func (k *Keyring) Seal(plaintext, aad []byte) ([]byte, error) // 0x01 | key_id uint16 BE | nonce(12) | ct+tag (AES-256-GCM)
func (k *Keyring) Open(envelope, aad []byte) ([]byte, error)  // any known key id
func (k *Keyring) KeyID(envelope []byte) (uint16, error)      // for rotation checks

type BlindIndexer struct{ /* 32-byte key */ }
func NewBlindIndexer(key []byte) (BlindIndexer, error)
func (b BlindIndexer) Index(purpose string, value []byte) []byte // HMAC-SHA-256(key, purpose || 0x00 || value)

func NewToken(prefix string) (plain string, digest []byte, err error) // prefix + base64url(32 random bytes); digest = SHA-256(plain)
func Digest(s string) []byte                                          // SHA-256
```

## 6. Additive extension to 002's `internal/platform/audit` (plan C-4)

```go
type Entry struct {
    OrgID    uuid.UUID
    Actor    Actor     // Actor.ID == uuid.Nil is stored as NULL (system actor)
    Entity   string
    EntityID uuid.UUID // uuid.Nil is stored as NULL (e.g. sign-in failure for an unknown identifier)
    Action   string
    Before, After any
    At       time.Time // NEW: zero keeps the column default; 003 always sets it from its clock
}
```

No other change to 002's `audit`, `db`, `clock` or `istdate` contracts.
