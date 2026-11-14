package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// The Store contract; store_pg.go implements it and is the only file that may import pgx.
// Semantics, shared by every method:
//
//   - IDs are uuid.UUID; every time comes from the caller (an @now parameter), never the database.
//   - Reads run on the transaction in ctx when there is one, otherwise on the pool.
//   - Methods documented "requires a transaction" return db.ErrNoTx, touching nothing, when ctx
//     carries none. Lock methods take row locks in the order of data-model.md "Lock order".

// Status is a user's lifecycle status (BR-CLI-04).
type Status string

// The four statuses of the users.status CHECK.
const (
	StatusPendingVerification Status = "pending_verification"
	StatusPendingApproval     Status = "pending_approval"
	StatusActive              Status = "active"
	StatusDisabled            Status = "disabled"
)

// Errors returned by the Store (in addition to ErrEmailTaken, ErrMobileTaken, ErrUserNotFound).
var (
	ErrNoOrg               = errors.New("auth: no organisation configured")
	ErrMultipleOrgs        = errors.New("auth: more than one organisation")
	ErrSessionNotFound     = errors.New("auth: session not found")
	ErrDeviceSessionExists = errors.New("auth: a live session exists for this device")
	ErrRefreshNotFound     = errors.New("auth: refresh token not found")
	ErrRefreshExists       = errors.New("auth: refresh token already exists or the session has an unused one")
	ErrRefreshUsed         = errors.New("auth: refresh token already used")
	ErrMFANotFound         = errors.New("auth: no authenticator record")
	ErrNoPendingSecret     = errors.New("auth: no pending authenticator secret")
	ErrChallengeNotFound   = errors.New("auth: challenge not found")
)

// UserRow is one users row. Mobile and StatusBeforeDisabled are "" when NULL.
type UserRow struct {
	ID, OrgID            uuid.UUID
	Role                 string // "admin" | "client"
	Status               Status
	StatusBeforeDisabled Status
	Name, Email, Mobile  string
	EmailVerifiedAt      *time.Time
	MobileVerifiedAt     *time.Time
	PasswordHash         string
	PasswordChangedAt    time.Time
	LastSignInAt         *time.Time
	CreatedAt, UpdatedAt time.Time
}

// NewUser is the input of CreateUser. Email and Mobile are already normalised; Mobile "" is NULL.
// CreatedAt, UpdatedAt and PasswordChangedAt are all Now.
type NewUser struct {
	OrgID           uuid.UUID
	Role            string
	Status          Status
	Name            string
	Email, Mobile   string
	EmailVerifiedAt *time.Time
	PasswordHash    string
	Now             time.Time
}

// SessionRow is one device_sessions row. MFAMethod and EndReason are "" when NULL.
type SessionRow struct {
	ID, OrgID, UserID, DeviceID uuid.UUID
	DeviceName                  string
	Platform, ClientType        string
	MFAMethod                   string
	CreatedAt, LastActiveAt     time.Time
	IdleExpiresAt               time.Time
	AbsoluteExpiresAt           time.Time
	EndedAt                     *time.Time
	EndReason                   string
}

// NewSession is the input of CreateSession; CreatedAt and LastActiveAt are both Now.
type NewSession struct {
	OrgID, UserID, DeviceID uuid.UUID
	DeviceName              string
	Platform, ClientType    string
	MFAMethod               string
	Now                     time.Time
	IdleExpiresAt           time.Time
	AbsoluteExpiresAt       time.Time
}

// SessionPrincipal is the per-request join of device_sessions and users (plan P-02).
type SessionPrincipal struct {
	SessionID, UserID, OrgID uuid.UUID
	EndedAt                  *time.Time
	IdleExpiresAt            time.Time
	AbsoluteExpiresAt        time.Time
	Role                     string
	Status                   Status
}

// Live reports whether the session may authenticate a request at now: not ended, now before both
// expiry instants (a request at exactly an expiry instant is expired) and the user active.
func (p SessionPrincipal) Live(now time.Time) bool {
	return p.EndedAt == nil && p.Status == StatusActive &&
		now.Before(p.IdleExpiresAt) && now.Before(p.AbsoluteExpiresAt)
}

// RefreshRow is one refresh_tokens row. ReplacedBy is uuid.Nil when NULL.
type RefreshRow struct {
	ID, OrgID, SessionID uuid.UUID
	TokenHash            []byte
	IssuedAt             time.Time
	UsedAt               *time.Time
	ReplacedBy           uuid.UUID
}

// MFARow is one admin_mfa row. LastUsedStep is the highest accepted TOTP step.
type MFARow struct {
	UserID, OrgID    uuid.UUID
	SecretEnc        []byte
	PendingSecretEnc []byte
	PendingCreatedAt *time.Time
	EnrolledAt       *time.Time
	LastUsedStep     int64
	UpdatedAt        time.Time
}

// NewChallenge is the input of CreateChallenge; ExpiresAt is Now + 5 minutes.
type NewChallenge struct {
	OrgID, UserID, DeviceID uuid.UUID
	TokenHash               []byte
	Purpose                 string // "verify" | "enrol"
	DeviceName              string
	Platform, ClientType    string
	Now, ExpiresAt          time.Time
}

// ChallengeRow is one mfa_challenges row.
type ChallengeRow struct {
	ID, OrgID, UserID, DeviceID uuid.UUID
	TokenHash                   []byte
	Purpose                     string
	DeviceName                  string
	Platform, ClientType        string
	CreatedAt, ExpiresAt        time.Time
	ConsumedAt                  *time.Time
}

// Store is the persistence port of internal/auth.
type Store interface {
	// DefaultOrg returns the single orgs row: ErrNoOrg when none, ErrMultipleOrgs when several.
	DefaultOrg(ctx context.Context) (uuid.UUID, error)

	// CreateUser inserts a user: ErrEmailTaken or ErrMobileTaken when the unique index refuses.
	CreateUser(ctx context.Context, in NewUser) (UserRow, error)
	// GetUser: ErrUserNotFound when absent.
	GetUser(ctx context.Context, id uuid.UUID) (UserRow, error)
	// FindUserByIdentifier looks up by a normalised email or mobile (global, no org):
	// ErrUserNotFound when absent.
	FindUserByIdentifier(ctx context.Context, kind IdentifierKind, value string) (UserRow, error)
	// LockUser reads one user FOR UPDATE; requires a transaction.
	LockUser(ctx context.Context, id uuid.UUID) (UserRow, error)
	// LockAdmins reads every admin of the org (any status) ordered by id FOR UPDATE; requires a
	// transaction.
	LockAdmins(ctx context.Context, orgID uuid.UUID) ([]UserRow, error)
	// SetStatus sets status and updated_at. before is stored as status_before_disabled when to is
	// disabled and must be "" otherwise (the CHECK pair is kept consistent).
	SetStatus(ctx context.Context, id uuid.UUID, to, before Status, now time.Time) error
	// MarkVerified stamps email_verified_at or mobile_verified_at. It reports whether it changed
	// anything: an already-verified contact keeps its first timestamp and returns false.
	MarkVerified(ctx context.Context, id uuid.UUID, kind IdentifierKind, now time.Time) (bool, error)
	// ReplaceContact sets a new email or mobile (ErrEmailTaken / ErrMobileTaken on a clash,
	// ErrUserNotFound when the user is absent); the status is untouched.
	ReplaceContact(ctx context.Context, id uuid.UUID, kind IdentifierKind, value string, now time.Time) error
	// ChangePassword stores a new hash and sets password_changed_at = now.
	ChangePassword(ctx context.Context, id uuid.UUID, phc string, now time.Time) error
	// Rehash replaces the hash only if it still equals oldPHC (compare and set); it does not touch
	// password_changed_at. It reports whether the row changed.
	Rehash(ctx context.Context, id uuid.UUID, oldPHC, newPHC string, now time.Time) (bool, error)
	// RecordSignIn sets last_sign_in_at.
	RecordSignIn(ctx context.Context, id uuid.UUID, now time.Time) error

	// CreateSession: ErrDeviceSessionExists when the user already has a live session on that device.
	CreateSession(ctx context.Context, in NewSession) (SessionRow, error)
	// GetSession: ErrSessionNotFound when absent.
	GetSession(ctx context.Context, id uuid.UUID) (SessionRow, error)
	// LockSession reads one session FOR UPDATE; requires a transaction.
	LockSession(ctx context.Context, id uuid.UUID) (SessionRow, error)
	// GetSessionPrincipal is the join of plan P-02: ErrSessionNotFound when absent. It returns the
	// raw columns; SessionPrincipal.Live decides.
	GetSessionPrincipal(ctx context.Context, sessionID uuid.UUID) (SessionPrincipal, error)
	// ListLiveSessions returns the user's sessions that are not ended and not expired at now,
	// most recently active first (ties by id).
	ListLiveSessions(ctx context.Context, userID uuid.UUID, now time.Time) ([]SessionRow, error)
	// TouchSession sets last_active_at = now and idle_expires_at.
	TouchSession(ctx context.Context, id uuid.UUID, now, idleExpiresAt time.Time) error
	// EndSession ends a session that is not ended yet; it reports whether it did.
	EndSession(ctx context.Context, id uuid.UUID, reason string, now time.Time) (bool, error)
	// EndUserSessions ends every not-ended session of the user except exceptSessionID (uuid.Nil
	// excepts none) and returns the ended ids ordered ascending.
	EndUserSessions(ctx context.Context, userID uuid.UUID, exceptSessionID uuid.UUID, reason string, now time.Time) ([]uuid.UUID, error)

	// InsertRefresh adds an unused token: ErrRefreshExists on a duplicate digest or when the
	// session already has an unused token.
	InsertRefresh(ctx context.Context, orgID, sessionID uuid.UUID, hash []byte, now time.Time) (RefreshRow, error)
	// FindRefreshByHash reads without a lock: ErrRefreshNotFound when absent.
	FindRefreshByHash(ctx context.Context, hash []byte) (RefreshRow, error)
	// LockRefresh reads one token FOR UPDATE; requires a transaction.
	LockRefresh(ctx context.Context, id uuid.UUID) (RefreshRow, error)
	// RotateRefresh marks the old token used and inserts its successor, linking replaced_by, in
	// one step; requires a transaction. ErrRefreshUsed when old was already used.
	RotateRefresh(ctx context.Context, oldID uuid.UUID, newHash []byte, now time.Time) (RefreshRow, error)

	// GetMFA: ErrMFANotFound when the user has no row.
	GetMFA(ctx context.Context, userID uuid.UUID) (MFARow, error)
	// LockMFA reads the row FOR UPDATE; requires a transaction. ErrMFANotFound when absent.
	LockMFA(ctx context.Context, userID uuid.UUID) (MFARow, error)
	// SetPendingSecret creates the row if needed and sets (or overwrites) the pending secret; a
	// confirmed secret stays untouched.
	SetPendingSecret(ctx context.Context, orgID, userID uuid.UUID, enc []byte, now time.Time) error
	// ConfirmPending promotes the pending secret to the confirmed one, sets enrolled_at = now and
	// last_used_step = step: ErrNoPendingSecret when there is none.
	ConfirmPending(ctx context.Context, userID uuid.UUID, step int64, now time.Time) error
	// AdvanceStep is the replay guard: it raises last_used_step to step only when step is greater
	// than the stored value, atomically, and reports whether it did.
	AdvanceStep(ctx context.Context, userID uuid.UUID, step int64, now time.Time) (bool, error)

	// ReplaceRecoveryCodes revokes every unrevoked code of the user (revoked_at = now) and inserts
	// the new batch; it returns how many usable (unused) codes it revoked. Requires a transaction.
	ReplaceRecoveryCodes(ctx context.Context, orgID, userID, batchID uuid.UUID, hashes [][]byte, now time.Time) (int, error)
	// UseRecoveryCode marks one unused, unrevoked code used and reports ok, with the number of
	// usable codes left. An unknown, used or revoked code is ok=false and changes nothing.
	UseRecoveryCode(ctx context.Context, userID uuid.UUID, hash []byte, now time.Time) (remaining int, ok bool, err error)
	// CountUsableRecoveryCodes counts unused, unrevoked codes.
	CountUsableRecoveryCodes(ctx context.Context, userID uuid.UUID) (int, error)

	CreateChallenge(ctx context.Context, in NewChallenge) (ChallengeRow, error)
	// FindChallengeByHash: ErrChallengeNotFound when absent.
	FindChallengeByHash(ctx context.Context, hash []byte) (ChallengeRow, error)
	// ConsumeChallenge sets consumed_at when the challenge is unconsumed and now < expires_at, and
	// reports whether it did (single use, exactly one winner under concurrency).
	ConsumeChallenge(ctx context.Context, id uuid.UUID, now time.Time) (bool, error)

	// GetThrottle returns the zero state when no row exists. subject is the blind-index digest.
	GetThrottle(ctx context.Context, orgID uuid.UUID, scope string, subject []byte) (ThrottleState, error)
	// LockThrottle creates the row when missing and reads it FOR UPDATE; requires a transaction.
	LockThrottle(ctx context.Context, orgID uuid.UUID, scope string, subject []byte, now time.Time) (ThrottleState, error)
	// SaveThrottle upserts the state; requires a transaction.
	SaveThrottle(ctx context.Context, orgID uuid.UUID, scope string, subject []byte, st ThrottleState, now time.Time) error
	// DeleteThrottle removes the row (no error when absent); requires a transaction.
	DeleteThrottle(ctx context.Context, orgID uuid.UUID, scope string, subject []byte) error
}
