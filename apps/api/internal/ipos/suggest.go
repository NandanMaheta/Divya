//nolint:unparam,revive,unused // S01 compile skeleton; removed as slices land
package ipos

import "github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"

// DateSuggestion is the weekday-based suggestion from a close date (FR-043).
type DateSuggestion struct {
	Close, Allotment, Refund, DematCredit, Listing istdate.Date
	Notice                                         string
}

// SuggestDates is pure: T+1 allotment, T+2 refund and credit, T+3 listing, weekdays only.
func SuggestDates(close istdate.Date) DateSuggestion { panic(errNotImplemented) }
