package auth

import (
	"net/mail"
	"regexp"
	"strings"
)

// IdentifierKind says whether a sign-in identifier is an email or a mobile number. The values
// appear in throttle subjects and audit records.
type IdentifierKind string

// The two identifier kinds.
const (
	KindEmail  IdentifierKind = "email"
	KindMobile IdentifierKind = "mobile"
)

const maxEmailLength = 254

// mobileGrammar is the D-17 grammar after spaces and hyphens are removed: an optional 0, 91 or
// +91 prefix and ten ASCII digits starting with 6 to 9.
var mobileGrammar = regexp.MustCompile(`^(?:\+91|91|0)?([6-9][0-9]{9})$`)

// KindOf decides by the presence of "@": email if present, otherwise mobile.
func KindOf(raw string) IdentifierKind {
	if strings.Contains(raw, "@") {
		return KindEmail
	}
	return KindMobile
}

// NormalizeIdentifier applies KindOf and then the matching normaliser. The kind is returned even
// on failure. Errors wrap ErrInvalidIdentifier and never echo the input.
func NormalizeIdentifier(raw string) (IdentifierKind, string, error) {
	if kind := KindOf(raw); kind == KindEmail {
		v, err := NormalizeEmail(raw)
		return kind, v, err
	}
	v, err := NormalizeMobile(raw)
	return KindMobile, v, err
}

// NormalizeEmail trims, lowercases and validates an email address (single plain address, ASCII,
// at most 254 characters).
func NormalizeEmail(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" || len(s) > maxEmailLength || strings.Count(s, "@") != 1 {
		return "", ErrInvalidIdentifier
	}
	for _, r := range s {
		if r <= ' ' || r >= 0x7f { // control characters, spaces, non-ASCII
			return "", ErrInvalidIdentifier
		}
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Name != "" || addr.Address != s {
		return "", ErrInvalidIdentifier
	}
	return s, nil
}

// NormalizeMobile returns +91 followed by the ten digits, or ErrInvalidIdentifier.
func NormalizeMobile(raw string) (string, error) {
	stripped := strings.NewReplacer(" ", "", "-", "").Replace(raw)
	m := mobileGrammar.FindStringSubmatch(stripped)
	if m == nil {
		return "", ErrInvalidIdentifier
	}
	return "+91" + m[1], nil
}
