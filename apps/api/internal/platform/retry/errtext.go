package retry

import (
	"regexp"
)

// maxErrorRunes is the length of the last_error columns.
const maxErrorRunes = 500

const redacted = "[redacted]"

var (
	jwtPattern    = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
	bearerPattern = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]+`)
	panPattern    = regexp.MustCompile(`[A-Z]{5}[0-9]{4}[A-Z]`)
)

// ErrorText turns an error into text safe to store in a last_error column (constitution VI):
// Bearer tokens, JWT-shaped strings and PANs are redacted first, then the result is cut to 500
// characters. Redacting before cutting leaves no fragment of a secret at the boundary.
func ErrorText(err error) string {
	if err == nil {
		return ""
	}
	s := jwtPattern.ReplaceAllString(err.Error(), redacted)
	s = bearerPattern.ReplaceAllString(s, redacted)
	s = panPattern.ReplaceAllString(s, redacted)
	if r := []rune(s); len(r) > maxErrorRunes {
		s = string(r[:maxErrorRunes])
	}
	return s
}
