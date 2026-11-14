//nolint:unparam,revive,unused // S01 compile skeleton; removed as slices land
package ipos

// validateDraft checks segments (BR-IPO-01), values and price band; returns a *ValidationError.
func validateDraft(d Draft) error { return errNotImplemented }

// validatePublishable checks the FR-020 publish requirements.
func validatePublishable(i IPO) error { return errNotImplemented }

// warnings returns the soft findings for an IPO (FR-005).
func warnings(i IPO) []Warning { panic(errNotImplemented) }
