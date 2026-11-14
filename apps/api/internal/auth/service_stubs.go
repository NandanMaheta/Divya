package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/authz"
)

// Compile stubs added by the test-engineer (S11/S13) so the service tests have the exported
// signatures they exercise. Every method returns errNotImplemented. The implementer (S12 for
// SignIn, S14 for the rest) replaces each stub in place and may adjust a field name only with the
// owner's agreement, because the tests are protected. Delete this file once nothing is left in it.

// Sentinel errors of sign-in, refresh and re-authentication (contracts/error-codes.md).
var (
	// ErrInvalidCredentials is the one generic sign-in failure (D-09): wrong identifier or password,
	// locked, pending_verification, pending_approval, disabled, admin by mobile. It never says which.
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	// ErrRefreshRejected is every refresh failure (unknown, reuse, device mismatch, transport
	// mismatch, expired, ended, user not active).
	ErrRefreshRejected = errors.New("auth: refresh rejected")
	// ErrReauthFailed is a wrong or locked current password on change-password.
	ErrReauthFailed = errors.New("auth: re-authentication failed")
)

// Client types: the refresh transport of a session (P-04). On a RefreshInput it names the
// transport the request used: "mobile" = token in the body, "web" = token from the cookie.
const (
	ClientTypeMobile = "mobile"
	ClientTypeWeb    = "web"
)

// Platforms of a device session.
const (
	PlatformAndroid = "android"
	PlatformIPhone  = "iphone"
	PlatformWeb     = "web"
)

// NextStep values of SignInResult.
const (
	NextStepSignedIn           = "signed_in"
	NextStepMFARequired        = "mfa_required"
	NextStepMFAEnrolmentNeeded = "mfa_enrolment_required"
)

// DeviceInfo is the installation that signs in.
type DeviceInfo struct {
	ID       uuid.UUID
	Name     string
	Platform string
}

// SignInInput is one sign-in attempt. Password is raw and never logged. IP is the client address
// already resolved by httpx.ClientIP.
type SignInInput struct {
	Identifier, Password string
	ClientType           string
	Device               DeviceInfo
	IP                   string
}

// UserSummary is the user part of SessionTokens.
type UserSummary struct {
	ID                uuid.UUID
	Role, Name, Email string
}

// SessionTokens is the result of a successful sign-in, refresh or MFA completion. RefreshToken is
// always set; the HTTP layer keeps it out of the body for web sessions.
type SessionTokens struct {
	AccessToken          string
	AccessTokenExpiresAt time.Time
	RefreshToken         string
	SessionID            uuid.UUID
	SessionExpiresAt     time.Time // absolute
	IdleExpiresAt        time.Time
	User                 UserSummary
}

// Challenge is the interim proof of an admin after the password step.
type Challenge struct {
	Token     string // dmc_...
	ExpiresAt time.Time
}

// SignInResult is the outcome of a correct password: a session (client) or a challenge (admin).
type SignInResult struct {
	NextStep  string
	Session   *SessionTokens
	Challenge *Challenge
}

// RefreshInput is one refresh attempt. ClientType is the transport used (ClientTypeMobile: token
// in the body, ClientTypeWeb: token from the cookie).
type RefreshInput struct {
	DeviceID     uuid.UUID
	RefreshToken string
	ClientType   string
	IP           string
}

// Device is one live device session as listed to its user or to an admin.
type Device struct {
	SessionID    uuid.UUID
	DeviceName   string
	Platform     string
	SignedInAt   time.Time
	LastActiveAt time.Time
	IsCurrent    bool
}

// SignIn is the password step for clients and admins (P-08).
func (s *Service) SignIn(context.Context, SignInInput) (SignInResult, error) {
	return SignInResult{}, errNotImplemented
}

// Refresh rotates a refresh credential (P-03).
func (s *Service) Refresh(context.Context, RefreshInput) (SessionTokens, error) {
	return SessionTokens{}, errNotImplemented
}

// SignOut ends the caller's own session.
func (s *Service) SignOut(context.Context, authz.Principal) error { return errNotImplemented }

// SignOutEverywhere ends every session of the caller, the current one included (D-12) and
// returns how many it ended.
func (s *Service) SignOutEverywhere(context.Context, authz.Principal) (int, error) {
	return 0, errNotImplemented
}

// ListDevices lists the caller's live sessions, most recently active first.
func (s *Service) ListDevices(context.Context, authz.Principal) ([]Device, error) {
	return nil, errNotImplemented
}

// RevokeDevice ends one of the caller's own sessions: ErrSessionNotFound when it is absent, ended
// or owned by someone else.
func (s *Service) RevokeDevice(context.Context, authz.Principal, uuid.UUID) error {
	return errNotImplemented
}

// AdminListDevices lists any user's live sessions of the admin's org (ErrUserNotFound otherwise).
func (s *Service) AdminListDevices(context.Context, authz.Principal, uuid.UUID) ([]Device, error) {
	return nil, errNotImplemented
}

// AdminRevokeDevice ends one session of the user (ErrUserNotFound, ErrSessionNotFound).
func (s *Service) AdminRevokeDevice(context.Context, authz.Principal, uuid.UUID, uuid.UUID) error {
	return errNotImplemented
}

// AdminEndAllSessions ends every session of the user and returns how many it ended.
func (s *Service) AdminEndAllSessions(context.Context, authz.Principal, uuid.UUID) (int, error) {
	return 0, errNotImplemented
}

// ChangePassword changes the caller's password: ErrReauthFailed for a wrong or locked current
// password, *PolicyError for a weak new one. All other sessions end, the current one stays.
func (s *Service) ChangePassword(context.Context, authz.Principal, string, string) error {
	return errNotImplemented
}

// BearerVerifier verifies access tokens (scheme "bearer") against the session lookup of P-02.
func (s *Service) BearerVerifier() authz.Verifier { return notImplementedVerifier{} }

// ChallengeVerifier verifies interim MFA challenge tokens (scheme "mfaChallenge"). It does not
// consume the challenge.
func (s *Service) ChallengeVerifier() authz.Verifier { return notImplementedVerifier{} }

type notImplementedVerifier struct{}

func (notImplementedVerifier) Verify(context.Context, string) (authz.Principal, error) {
	return authz.Principal{}, errNotImplemented
}
