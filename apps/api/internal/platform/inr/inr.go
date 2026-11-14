// Package inr formats integer paise as Indian-grouped rupee strings (integer-only, no floats).
package inr

import (
	"strconv"
	"strings"
)

// Format renders paise as e.g. "₹1,23,456.78". Paise are shown only when non-zero and are
// never rounded; a negative amount renders as "-₹…".
func Format(paise int64) string {
	digits := strconv.FormatInt(paise, 10)
	neg := strings.HasPrefix(digits, "-")
	digits = strings.TrimPrefix(digits, "-")
	if len(digits) < 3 {
		digits = strings.Repeat("0", 3-len(digits)) + digits
	}
	rupees, frac := digits[:len(digits)-2], digits[len(digits)-2:]

	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	b.WriteString("₹")
	b.WriteString(group(rupees))
	if frac != "00" {
		b.WriteByte('.')
		b.WriteString(frac)
	}
	return b.String()
}

// group applies Indian grouping: last three digits, then pairs.
func group(digits string) string {
	if len(digits) <= 3 {
		return digits
	}
	head, tail := digits[:len(digits)-3], digits[len(digits)-3:]
	var parts []string
	for len(head) > 2 {
		parts = append([]string{head[len(head)-2:]}, parts...)
		head = head[:len(head)-2]
	}
	if head != "" {
		parts = append([]string{head}, parts...)
	}
	return strings.Join(parts, ",") + "," + tail
}
