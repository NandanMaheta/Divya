package auth

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth/commonpasswords"
)

// Policy codes (contracts/error-codes.md).
const (
	PolicyTooShort          = "auth.password_too_short"
	PolicyTooLong           = "auth.password_too_long"
	PolicyMatchesIdentifier = "auth.password_matches_identifier"
	PolicyTooCommon         = "auth.password_too_common"
	PolicyUnchanged         = "auth.password_unchanged"
)

const (
	minPasswordRunes = 10
	maxPasswordRunes = 256
)

// PolicyError reports the first failing password rule. It never contains the password.
type PolicyError struct{ Code string }

func (e *PolicyError) Error() string { return "auth: password rejected: " + e.Code }

// NormalizePassword returns the Unicode NFC form the service hashes and checks. It never trims or
// case-folds: spaces and case are significant.
func NormalizePassword(raw string) string { return norm.NFC.String(raw) }

// CheckPolicy applies the D-07 rules in order and returns a *PolicyError for the first that
// fails. identifiers are the user's stored (normalised) email and mobile; empty ones are ignored.
// "Unchanged" needs the stored hash and is decided by the service, not here.
func CheckPolicy(raw string, identifiers ...string) error {
	pw := NormalizePassword(raw)
	n := utf8.RuneCountInString(pw)
	switch {
	case n < minPasswordRunes:
		return &PolicyError{Code: PolicyTooShort}
	case n > maxPasswordRunes:
		return &PolicyError{Code: PolicyTooLong}
	}
	trimmed := strings.ToLower(strings.TrimSpace(pw))
	mobile, mobileErr := NormalizeMobile(pw)
	for _, id := range identifiers {
		if id == "" {
			continue
		}
		if trimmed == strings.ToLower(id) || (mobileErr == nil && mobile == id) {
			return &PolicyError{Code: PolicyMatchesIdentifier}
		}
	}
	if _, common := commonpasswords.Set()[strings.ToLower(pw)]; common {
		return &PolicyError{Code: PolicyTooCommon}
	}
	return nil
}
