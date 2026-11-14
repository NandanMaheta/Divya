package ipos

import "github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"

// DateSuggestion is the weekday-based suggestion from a close date (FR-043).
type DateSuggestion struct {
	Close, Allotment, Refund, DematCredit, Listing istdate.Date
	Notice                                         string
}

// SuggestDates is pure: T+1 allotment, T+2 refund and credit, T+3 listing, weekdays only.
// A weekend close counts from the next Monday.
func SuggestDates(close istdate.Date) DateSuggestion {
	return DateSuggestion{
		Close:       close,
		Allotment:   weekdaysAfter(close, 1),
		Refund:      weekdaysAfter(close, 2),
		DematCredit: weekdaysAfter(close, 2),
		Listing:     weekdaysAfter(close, 3),
		Notice:      "weekdays only; check exchange holidays",
	}
}

func weekdaysAfter(d istdate.Date, n int) istdate.Date {
	out, err := istdate.AddWeekdays(d, n)
	if err != nil {
		return d // n is a small positive constant, so this cannot happen
	}
	return out
}
