package auth

import "errors"

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
