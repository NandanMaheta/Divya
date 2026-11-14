package auth

import (
	"errors"
	"net/http"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/httpx"
)

// Sentinel errors of the Accounts capability (contracts/go-capabilities.md section 2). Other
// features map them to HTTP problems or their own messages.
var (
	ErrEmailTaken        = errors.New("auth: email already in use")
	ErrMobileTaken       = errors.New("auth: mobile already in use")
	ErrInvalidIdentifier = errors.New("auth: invalid identifier")
	ErrInvalidTransition = errors.New("auth: invalid status transition")
	ErrLastAdmin         = errors.New("auth: cannot remove the last active admin")
	ErrUserNotFound      = errors.New("auth: user not found")
)

// Problem codes of contracts/error-codes.md (stable; UIs translate them).
const (
	CodeInvalidCredentials = "auth.invalid_credentials" //nolint:gosec // a problem code, not a credential
	CodeIdentifierInvalid  = "auth.identifier_invalid"
	CodeUnauthenticated    = "auth.unauthenticated"
	CodeForbidden          = "auth.forbidden"
	CodeRefreshRejected    = "auth.refresh_rejected"
	CodeCSRFRejected       = "auth.csrf_rejected"
	CodeReauthFailed       = "auth.reauth_failed"
	CodeDeviceNotFound     = "auth.device_not_found"
	CodeUserNotFound       = "auth.user_not_found"
	CodeMFANoPendingSecret = "auth.mfa_no_pending_secret" //nolint:gosec // a problem code, not a credential
	CodeBusy               = "auth.busy"
	CodeNotConfigured      = "auth.not_configured"
	CodeEmailTaken         = "auth.email_taken"
	CodeMobileTaken        = "auth.mobile_taken"
	CodeStatusTransition   = "auth.status_transition_invalid"
	CodeLastAdmin          = "auth.last_admin"
)

// CodeInternal is the platform code of an unexpected failure.
const CodeInternal = "internal"

// ProblemFor maps a domain error to its RFC 9457 problem. Credential failures are not mapped here
// (the service answers them with one generic problem). An unknown error becomes a bare 500 with no
// detail, so no internal text reaches a client (FR-091).
func ProblemFor(err error) error {
	var policy *PolicyError
	switch {
	case errors.As(err, &policy):
		return httpx.Problem(http.StatusUnprocessableEntity, policy.Code, "The password does not meet the policy.")
	case errors.Is(err, ErrInvalidIdentifier):
		return httpx.Problem(http.StatusUnprocessableEntity, CodeIdentifierInvalid, "The identifier is not a valid email address or mobile number.")
	case errors.Is(err, ErrUserNotFound):
		return httpx.Problem(http.StatusNotFound, CodeUserNotFound, "The user does not exist.")
	case errors.Is(err, ErrSessionNotFound):
		return httpx.Problem(http.StatusNotFound, CodeDeviceNotFound, "The device session does not exist.")
	case errors.Is(err, ErrNoPendingSecret):
		return httpx.Problem(http.StatusConflict, CodeMFANoPendingSecret, "There is no authenticator secret waiting for confirmation.")
	case errors.Is(err, ErrEmailTaken):
		return httpx.Problem(http.StatusConflict, CodeEmailTaken, "The email address is already in use.")
	case errors.Is(err, ErrMobileTaken):
		return httpx.Problem(http.StatusConflict, CodeMobileTaken, "The mobile number is already in use.")
	case errors.Is(err, ErrInvalidTransition):
		return httpx.Problem(http.StatusConflict, CodeStatusTransition, "The status change is not allowed from the current status.")
	case errors.Is(err, ErrLastAdmin):
		return httpx.Problem(http.StatusConflict, CodeLastAdmin, "The last active admin cannot be removed.")
	case errors.Is(err, ErrNoOrg), errors.Is(err, ErrMultipleOrgs):
		return httpx.Problem(http.StatusServiceUnavailable, CodeNotConfigured, "No organisation is configured.")
	default:
		return httpx.Problem(http.StatusInternalServerError, CodeInternal, "")
	}
}
