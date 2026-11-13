# Exported Go contracts of 003 (for 004, 005, 007, 022 and every later module)

These are the only ways other modules use auth. They never read the auth tables directly (constitution IV). Signatures use only standard library types and the types defined here. IDs are canonical lowercase UUID strings.

## 1. `internal/platform/authz` (every module)

```go
type Role string
const (RoleAdmin Role = "admin"; RoleClient Role = "client")

type Principal struct {
    UserID, OrgID, SessionID string
    Role Role
}

// Declaring security on huma.Operation.Security (exactly one of these per operation):
func Public() []map[string][]string                 // [] (must also be added to the contract-test allowlist)
func Require(roles ...Role) []map[string][]string   // [{bearer: [roles...]}]; no roles = any signed-in user
func ChallengeOnly() []map[string][]string          // [{mfaChallenge: []}] (auth only)

// Handlers:
func PrincipalFrom(ctx context.Context) (Principal, bool)
func MustPrincipal(ctx context.Context) Principal   // panics if absent; use only in operations that declare Require

// Wiring (cmd/divya only):
type Verifier interface {
    Verify(ctx context.Context, token string) (Principal, error) // error => 401 auth.unauthenticated
}
func Install(api huma.API, verifiers map[string]Verifier)   // before any huma.Register
func AssertDeclared(api huma.API)                           // after all Register calls; panics on nil Security / unknown scheme
```

## 2. `internal/auth` Accounts capability (FR-050…FR-055; no HTTP operations)

Every method runs inside the caller's transaction when the context carries one (`db.TxRunner` joins it). Otherwise it opens its own transaction. Every method writes its audit entry in that same transaction.

```go
type Actor struct {
    Kind string // "system" | "admin" | "client"
    ID   string // empty for system
}

type NewClient struct {
    OrgID, Name, Email, Mobile, Password string // raw input; normalised and policy-checked here
}

type Accounts interface {
    // FR-050: role client, status pending_verification. ErrEmailTaken / ErrMobileTaken / ErrInvalidIdentifier / *PolicyError.
    CreateClient(ctx context.Context, in NewClient, by Actor) (userID string, err error)
    // FR-051: idempotent; returns the resulting status (pending_approval once both are verified).
    MarkEmailVerified(ctx context.Context, userID string, by Actor) (Status, error)
    MarkMobileVerified(ctx context.Context, userID string, by Actor) (Status, error)
    // FR-052
    Approve(ctx context.Context, userID string, by Actor) error   // only from pending_approval, else ErrInvalidTransition
    Disable(ctx context.Context, userID string, by Actor) error   // ends all sessions; ErrLastAdmin
    Enable(ctx context.Context, userID string, by Actor) error    // restores status_before_disabled
    // FR-053: after 004 has verified the person. Ends ALL sessions, clears lock; *PolicyError.
    ResetPassword(ctx context.Context, userID, newPassword string, by Actor) error
    // FR-054
    EndAllSessions(ctx context.Context, userID string, feature string, by Actor) (ended int, err error)
    // FR-055: after 007 approval (BR-CLI-05 is enforced by 007). Status unchanged; uniqueness enforced.
    ReplaceEmail(ctx context.Context, userID, email string, by Actor) error
    ReplaceMobile(ctx context.Context, userID, mobile string, by Actor) error
    // Read-only helper for 005/022 screens (status gate display); no secrets.
    GetUser(ctx context.Context, userID string) (UserView, error)
}

type Status string // "pending_verification" | "pending_approval" | "active" | "disabled"

type UserView struct {
    ID, OrgID, Name, Email, Mobile string
    Role   string
    Status Status
    EmailVerified, MobileVerified bool
    CreatedAt time.Time
    LastSignInAt *time.Time
}
```

## 3. `internal/auth` Throttle (for 004 OTP limits; P-07)

```go
type ThrottlePolicy struct { MaxFailures int; Window, Lock time.Duration }
type Throttle interface {
    // scope must be in the auth_throttles CHECK list (004 extends it in its migration).
    // key is hashed with the blind-index key before storage; never stored as given.
    Check(ctx context.Context, orgID, scope, key string) (lockedUntil *time.Time, err error)
    Fail(ctx context.Context, orgID, scope, key string, p ThrottlePolicy) (startedLock bool, lockedUntil *time.Time, err error)
    Clear(ctx context.Context, orgID, scope, key string) error
}
```

## 4. `internal/platform/crypto` (006 reuses for PAN)

```go
type Keyring struct{ /* active id + map[id][32]byte, built by config */ }
func (k *Keyring) Seal(plaintext, aad []byte) ([]byte, error)   // 0x01 | key_id uint16 BE | nonce(12) | ct+tag
func (k *Keyring) Open(envelope, aad []byte) ([]byte, error)    // any known key id
type BlindIndexer struct{ /* 32-byte key */ }
func (b BlindIndexer) Index(purpose string, value []byte) []byte // HMAC-SHA-256(key, purpose || 0x00 || value)
func NewToken(prefix string) (plain string, sha256Digest []byte, err error) // prefix + base64url(32 random bytes)
func Digest(s string) []byte                                     // SHA-256
```

## 5. `internal/platform/audit` and `internal/platform/db`

```go
type Entry struct {
    OrgID, ActorKind, Entity, Action string
    ActorID, EntityID *string
    Before, After any // typed structs only
    At time.Time
}
type Writer interface{ Write(ctx context.Context, e Entry) error } // error if ctx carries no tx

type TxRunner interface{ InTx(ctx context.Context, fn func(ctx context.Context) error) error } // nested calls join
```
