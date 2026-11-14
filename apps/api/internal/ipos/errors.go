package ipos

import "errors"

// errNotImplemented is returned by every compile-skeleton body until its slice lands.
var errNotImplemented = errors.New("ipos: not implemented")

// Sentinel errors; HTTP mapping in contracts/error-codes.md.
var (
	ErrNotFound              = errors.New("ipos: not found")
	ErrStaleVersion          = errors.New("ipos: stale version")
	ErrNoChange              = errors.New("ipos: no change")
	ErrPublishedNotDeletable = errors.New("ipos: published IPOs can only be unpublished")
	ErrNotListed             = errors.New("ipos: not listed yet")
	ErrNotEligible           = errors.New("ipos: not eligible")
)

// FieldIssue is one field-level validation problem.
type FieldIssue struct{ Field, Message string }

// ValidationError Code is one of ipo.sme_not_allowed, ipo.invalid_segment, ipo.invalid_value,
// ipo.date_order, ipo.publish_incomplete.
type ValidationError struct {
	Code   string
	Issues []FieldIssue
}

func (e *ValidationError) Error() string { return "ipos: validation failed: " + e.Code }

// NotListedError is returned by MarkSettled when the IPO is not LISTED.
type NotListedError struct{ Current Status }

func (e *NotListedError) Error() string {
	return "ipos: not listed yet (status " + string(e.Current) + ")"
}

// Is makes errors.Is(err, ErrNotListed) true.
func (e *NotListedError) Is(target error) bool { return target == ErrNotListed }

// RejectedError is returned by listeners to reject a change with a user-facing reason.
type RejectedError struct{ Code, Detail string }

func (e *RejectedError) Error() string { return "ipos: rejected: " + e.Code }
