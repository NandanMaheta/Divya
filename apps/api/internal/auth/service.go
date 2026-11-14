package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/clock"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/crypto"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
)

// Skeleton (S10): the Service shape and the methods of the Accounts, Throttle and housekeeping
// contracts (contracts/go-capabilities.md sections 2 to 4). Methods return errNotImplemented
// until the slice that owns them (S12 onward) fills them in.
var errNotImplemented = errors.New("auth: not implemented")

// Deps are the collaborators of Service. Time comes from Now, transactions from Tx and audit
// entries from platform/audit; auth defines none of them (plan C-2 to C-4).
type Deps struct {
	Store   Store
	Tx      db.TxRunner
	Now     clock.Func
	Keys    *SigningKeys
	Hasher  *Hasher
	Blind   crypto.BlindIndexer
	Keyring *crypto.Keyring
	Log     *slog.Logger // optional; defaults to slog.Default()
}

// Service is the auth module: sign-in, sessions, refresh, MFA, accounts and housekeeping.
type Service struct {
	store   Store
	txr     db.TxRunner
	now     clock.Func
	tokens  *Tokens
	hasher  *Hasher
	blind   crypto.BlindIndexer
	keyring *crypto.Keyring
	log     *slog.Logger
}

// New validates the dependencies and returns the Service.
func New(d Deps) (*Service, error) {
	switch {
	case d.Store == nil:
		return nil, errors.New("auth: Deps.Store is required")
	case d.Now == nil:
		return nil, errors.New("auth: Deps.Now is required")
	case d.Keys == nil:
		return nil, errors.New("auth: Deps.Keys is required")
	case d.Hasher == nil:
		return nil, errors.New("auth: Deps.Hasher is required")
	case d.Keyring == nil:
		return nil, errors.New("auth: Deps.Keyring is required")
	}
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		store: d.Store, txr: d.Tx, now: d.Now, tokens: NewTokens(d.Keys, d.Now),
		hasher: d.Hasher, blind: d.Blind, keyring: d.Keyring, log: log,
	}, nil
}

// NewClient is a client account to create (FR-050). Password is raw and never logged.
type NewClient struct {
	OrgID                         uuid.UUID
	Name, Email, Mobile, Password string
}

// UserView is the read-only view of a user for other modules; it never carries secrets.
type UserView struct {
	ID, OrgID                     uuid.UUID
	Name, Email, Mobile           string
	Role                          string
	Status                        Status
	EmailVerified, MobileVerified bool
	CreatedAt                     time.Time
	LastSignInAt                  *time.Time
}

// Accounts is the internal capability other features use to manage users (FR-050 to FR-055). Each
// method joins the caller's transaction and audits in it.
type Accounts interface {
	CreateClient(ctx context.Context, in NewClient, by audit.Actor) (uuid.UUID, error)
	MarkEmailVerified(ctx context.Context, userID uuid.UUID, by audit.Actor) (Status, error)
	MarkMobileVerified(ctx context.Context, userID uuid.UUID, by audit.Actor) (Status, error)
	Approve(ctx context.Context, userID uuid.UUID, by audit.Actor) error
	Disable(ctx context.Context, userID uuid.UUID, by audit.Actor) error
	Enable(ctx context.Context, userID uuid.UUID, by audit.Actor) error
	ResetPassword(ctx context.Context, userID uuid.UUID, newPassword string, by audit.Actor) error
	EndAllSessions(ctx context.Context, userID uuid.UUID, feature string, by audit.Actor) (ended int, err error)
	ReplaceEmail(ctx context.Context, userID uuid.UUID, email string, by audit.Actor) error
	ReplaceMobile(ctx context.Context, userID uuid.UUID, mobile string, by audit.Actor) error
	GetUser(ctx context.Context, userID uuid.UUID) (UserView, error)
}

// Throttle is the lockout capability for other features (OTP limits in 004). Fail and Clear
// require a transaction in ctx.
type Throttle interface {
	Check(ctx context.Context, orgID uuid.UUID, scope, key string) (lockedUntil *time.Time, err error)
	Fail(ctx context.Context, orgID uuid.UUID, scope, key string, p ThrottlePolicy) (startedLock bool, lockedUntil *time.Time, err error)
	Clear(ctx context.Context, orgID uuid.UUID, scope, key string) error
}

var (
	_ Accounts = (*Service)(nil)
	_ Throttle = (*Service)(nil)
)

// CreateClient creates a client in pending_verification (FR-050).
func (s *Service) CreateClient(context.Context, NewClient, audit.Actor) (uuid.UUID, error) {
	return uuid.Nil, errNotImplemented
}

// MarkEmailVerified records a verified email and returns the resulting status (FR-051).
func (s *Service) MarkEmailVerified(context.Context, uuid.UUID, audit.Actor) (Status, error) {
	return "", errNotImplemented
}

// MarkMobileVerified records a verified mobile and returns the resulting status (FR-051).
func (s *Service) MarkMobileVerified(context.Context, uuid.UUID, audit.Actor) (Status, error) {
	return "", errNotImplemented
}

// Approve moves a pending_approval user to active (FR-052).
func (s *Service) Approve(context.Context, uuid.UUID, audit.Actor) error { return errNotImplemented }

// Disable disables a user and ends all their sessions (FR-052).
func (s *Service) Disable(context.Context, uuid.UUID, audit.Actor) error { return errNotImplemented }

// Enable restores the status a disabled user had before (FR-052).
func (s *Service) Enable(context.Context, uuid.UUID, audit.Actor) error { return errNotImplemented }

// ResetPassword sets a new password after 004 verified the person (FR-053).
func (s *Service) ResetPassword(context.Context, uuid.UUID, string, audit.Actor) error {
	return errNotImplemented
}

// EndAllSessions ends every session of a user on behalf of a feature (FR-054).
func (s *Service) EndAllSessions(context.Context, uuid.UUID, string, audit.Actor) (int, error) {
	return 0, errNotImplemented
}

// ReplaceEmail replaces a stored email after 007 approved it (FR-055).
func (s *Service) ReplaceEmail(context.Context, uuid.UUID, string, audit.Actor) error {
	return errNotImplemented
}

// ReplaceMobile replaces a stored mobile after 007 approved it (FR-055).
func (s *Service) ReplaceMobile(context.Context, uuid.UUID, string, audit.Actor) error {
	return errNotImplemented
}

// GetUser returns the read-only view of a user.
func (s *Service) GetUser(context.Context, uuid.UUID) (UserView, error) {
	return UserView{}, errNotImplemented
}

// Check reports until when the subject is locked, nil when it is not.
func (s *Service) Check(context.Context, uuid.UUID, string, string) (*time.Time, error) {
	return nil, errNotImplemented
}

// Fail records one counted failure for the subject.
func (s *Service) Fail(context.Context, uuid.UUID, string, string, ThrottlePolicy) (bool, *time.Time, error) {
	return false, nil, errNotImplemented
}

// Clear removes the failure record of the subject.
func (s *Service) Clear(context.Context, uuid.UUID, string, string) error { return errNotImplemented }

// HousekeepingKind is the scheduler kind of the daily purge (plan P-20).
const HousekeepingKind = "auth.housekeeping"

// HousekeepingNext is the next run after now: 03:30 IST every day.
func HousekeepingNext(now time.Time) time.Time {
	return istdate.NextDailyAt(now, istdate.WallClock{Hour: 3, Minute: 30})
}

// PurgeCounts is what one Housekeep run removed.
type PurgeCounts struct{ Throttles, Challenges, RefreshTokens int }

// Housekeep purges stale throttles, expired challenges and dead refresh families. It runs inside
// the job transaction (db.MustTx).
func (s *Service) Housekeep(context.Context, uuid.UUID) (PurgeCounts, error) {
	return PurgeCounts{}, fmt.Errorf("housekeep: %w", errNotImplemented)
}
