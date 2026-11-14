package auth

import (
	"time"

	"github.com/google/uuid"
)

// Audit action catalogue (data-model.md "Audit action catalogue", US-11). One entry per event,
// written in the same transaction as the change through platform/audit. Payloads are typed
// structs, never maps built from request input, and carry no secret: no password, TOTP code,
// recovery code, token or TOTP secret. The identifier of an unknown account is never stored as
// typed, only its kind and an 8-byte reference of the throttle digest.
const (
	ActionSignInSucceeded            = "auth.sign_in.succeeded"
	ActionSignInFailed               = "auth.sign_in.failed"
	ActionLockoutStarted             = "auth.lockout.started"
	ActionMFAChallengeIssued         = "auth.mfa.challenge_issued"
	ActionSessionRefreshed           = "auth.session.refreshed"
	ActionRefreshReuseDetected       = "auth.session.refresh_reuse_detected"
	ActionDeviceMismatch             = "auth.session.device_mismatch"
	ActionSessionSignedOut           = "auth.session.signed_out"
	ActionSessionSignedOutEverywhere = "auth.session.signed_out_everywhere"
	ActionSessionRevoked             = "auth.session.revoked"
	ActionSessionEndedAllByAdmin     = "auth.session.ended_all_by_admin"
	ActionSessionEndedByFeature      = "auth.session.ended_by_feature"
	ActionPasswordChanged            = "auth.password.changed"
	ActionPasswordReset              = "auth.password.reset"
	ActionMFAEnrolled                = "auth.mfa.enrolled"
	ActionMFAReplaced                = "auth.mfa.replaced"
	ActionMFARecoveryRegenerated     = "auth.mfa.recovery_codes_regenerated"
	ActionMFARecoveryCodeUsed        = "auth.mfa.recovery_code_used"
	ActionUserCreated                = "auth.user.created"
	ActionUserEmailVerified          = "auth.user.email_verified"
	ActionUserMobileVerified         = "auth.user.mobile_verified"
	ActionUserStatusChanged          = "auth.user.status_changed"
	ActionUserContactReplaced        = "auth.user.contact_replaced"
	ActionAdminBootstrapped          = "auth.admin.bootstrapped"
	ActionHousekeepingPurged         = "auth.housekeeping.purged"
)

// Audit entities of the catalogue.
const (
	EntityUser = "user"
	EntityAuth = "auth" // events that belong to no user (housekeeping)
)

// Failure reasons of ActionSignInFailed (internal only; the client always sees one generic code).
const (
	ReasonWrongPassword     = "wrong_password"
	ReasonUnknownIdentifier = "unknown_identifier"
	ReasonLocked            = "locked"
	ReasonNotActive         = "not_active"
	ReasonWrongTOTP         = "wrong_totp"
	ReasonWrongRecoveryCode = "wrong_recovery_code"
)

// Typed "after" payloads. IP is the client address already resolved by httpx.ClientIP.

// SignInSucceededAfter is the payload of ActionSignInSucceeded.
type SignInSucceededAfter struct {
	SessionID         uuid.UUID  `json:"session_id"`
	Platform          string     `json:"platform"`
	ClientType        string     `json:"client_type"`
	IP                string     `json:"ip"`
	MFAMethod         string     `json:"mfa_method,omitempty"`
	ReplacedSessionID *uuid.UUID `json:"replaced_session_id,omitempty"`
}

// SignInFailedAfter is the payload of ActionSignInFailed. SubjectRef is set only when the user is
// unknown.
type SignInFailedAfter struct {
	Reason         string `json:"reason"`
	IdentifierKind string `json:"identifier_kind"`
	SubjectRef     string `json:"subject_ref,omitempty"`
	IP             string `json:"ip"`
}

// LockoutStartedAfter is the payload of ActionLockoutStarted.
type LockoutStartedAfter struct {
	LockedUntil time.Time `json:"locked_until"`
	SubjectRef  string    `json:"subject_ref,omitempty"`
	IP          string    `json:"ip"`
}

// ChallengeIssuedAfter is the payload of ActionMFAChallengeIssued.
type ChallengeIssuedAfter struct {
	Purpose  string `json:"purpose"`
	Platform string `json:"platform"`
	IP       string `json:"ip"`
}

// SessionEventAfter is the payload of the single-session events (refreshed, reuse detected,
// device mismatch, signed out, MFA enrolled).
type SessionEventAfter struct {
	SessionID uuid.UUID `json:"session_id"`
	IP        string    `json:"ip,omitempty"`
}

// SessionRevokedAfter is the payload of ActionSessionRevoked; By is "user" or "admin".
type SessionRevokedAfter struct {
	SessionID uuid.UUID `json:"session_id"`
	By        string    `json:"by"`
}

// SessionsEndedAfter is the payload of the events that end several sessions (signed out
// everywhere, ended by an admin, ended by a feature). Feature is set for ActionSessionEndedByFeature.
type SessionsEndedAfter struct {
	SessionIDs []uuid.UUID `json:"session_ids"`
	Feature    string      `json:"feature,omitempty"`
}

// EndedSessionsAfter is the payload of ActionPasswordChanged and ActionMFAReplaced.
type EndedSessionsAfter struct {
	EndedSessionIDs []uuid.UUID `json:"ended_session_ids"`
}

// PasswordResetAfter is the payload of ActionPasswordReset.
type PasswordResetAfter struct {
	EndedSessionIDs []uuid.UUID `json:"ended_session_ids"`
	LockCleared     bool        `json:"lock_cleared"`
}

// RecoveryCodesRegeneratedAfter is the payload of ActionMFARecoveryRegenerated.
type RecoveryCodesRegeneratedAfter struct {
	BatchID uuid.UUID `json:"batch_id"`
	Count   int       `json:"count"`
}

// RecoveryCodeUsedAfter is the payload of ActionMFARecoveryCodeUsed; Context is "sign_in" or "replace".
type RecoveryCodeUsedAfter struct {
	Remaining int    `json:"remaining"`
	Context   string `json:"context"`
}

// UserCreatedAfter is the payload of ActionUserCreated (the user's own contact data, ADR 0007).
type UserCreatedAfter struct {
	Role   string `json:"role"`
	Status Status `json:"status"`
	Email  string `json:"email"`
	Mobile string `json:"mobile,omitempty"`
}

// StatusAfter is the payload of the status events: the status after (and, on a disable, the
// sessions that ended). The status before goes in Entry.Before as StatusBefore.
type StatusAfter struct {
	Status          Status      `json:"status"`
	EndedSessionIDs []uuid.UUID `json:"ended_session_ids,omitempty"`
}

// StatusBefore is the "before" payload of the status and verification events.
type StatusBefore struct {
	Status Status `json:"status"`
}

// ContactValue is the before and after payload of ActionUserContactReplaced; exactly one of
// the fields is set.
type ContactValue struct {
	Email  string `json:"email,omitempty"`
	Mobile string `json:"mobile,omitempty"`
}

// AdminBootstrappedAfter is the payload of ActionAdminBootstrapped.
type AdminBootstrappedAfter struct {
	Email string    `json:"email"`
	OrgID uuid.UUID `json:"org_id"`
}

// HousekeepingPurgedAfter is the payload of ActionHousekeepingPurged (counts only).
type HousekeepingPurgedAfter struct {
	Throttles     int `json:"throttles"`
	Challenges    int `json:"challenges"`
	RefreshTokens int `json:"refresh_tokens"`
}
