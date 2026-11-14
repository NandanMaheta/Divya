package ipos

import (
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
)

func nov(day int) istdate.Date { return istdate.Date{Year: 2026, Month: time.November, Day: day} }

// 14 Nov 2026 is a Saturday: 16 Mon, 18 Wed, 19 Thu, 20 Fri, 21 Sat, 22 Sun, 23 Mon.
func TestSuggestDates_BR_IPO_03(t *testing.T) {
	tests := []struct {
		name                              string
		close                             istdate.Date
		allotment, refund, demat, listing istdate.Date
	}{
		{"Wednesday close", nov(18), nov(19), nov(20), nov(20), nov(23)},
		{"Thursday close skips the weekend", nov(19), nov(20), nov(23), nov(23), nov(24)},
		{"Friday close", nov(20), nov(23), nov(24), nov(24), nov(25)},
		{"Monday close", nov(23), nov(24), nov(25), nov(25), nov(26)},
		{"Saturday close counts from Monday", nov(21), nov(23), nov(24), nov(24), nov(25)},
		{"Sunday close counts from Monday", nov(22), nov(23), nov(24), nov(24), nov(25)},
		{"across a month end", istdate.Date{Year: 2026, Month: time.November, Day: 30}, istdate.Date{Year: 2026, Month: time.December, Day: 1},
			istdate.Date{Year: 2026, Month: time.December, Day: 2}, istdate.Date{Year: 2026, Month: time.December, Day: 2}, istdate.Date{Year: 2026, Month: time.December, Day: 3}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SuggestDates(tc.close)
			if got.Close != tc.close || got.Allotment != tc.allotment || got.Refund != tc.refund ||
				got.DematCredit != tc.demat || got.Listing != tc.listing {
				t.Fatalf("SuggestDates(%s) = close %s allot %s refund %s demat %s list %s; want allot %s refund %s demat %s list %s",
					tc.close, got.Close, got.Allotment, got.Refund, got.DematCredit, got.Listing,
					tc.allotment, tc.refund, tc.demat, tc.listing)
			}
		})
	}
}

func TestSuggestDates_BR_IPO_03_Notice(t *testing.T) {
	if got := SuggestDates(nov(18)).Notice; got != "weekdays only; check exchange holidays" {
		t.Fatalf("notice = %q", got)
	}
}

func TestSuggestDates_BR_IPO_03_Properties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		c := nov(1).AddDays(rapid.IntRange(0, 400).Draw(t, "offset"))
		s := SuggestDates(c)
		for name, d := range map[string]istdate.Date{"allotment": s.Allotment, "refund": s.Refund, "demat": s.DematCredit, "listing": s.Listing} {
			if wd := d.Weekday(); wd == time.Saturday || wd == time.Sunday {
				t.Fatalf("%s %s falls on a weekend", name, d)
			}
		}
		// The suggestion must itself satisfy FR-040 so the admin can save it unchanged.
		d := okDraft()
		d.Dates = Dates{Close: &s.Close, Allotment: &s.Allotment, Refund: &s.Refund, DematCredit: &s.DematCredit, Listing: &s.Listing}
		if err := validateDraft(d); err != nil {
			t.Fatalf("suggestion for %s is not a valid date set: %v", c, err)
		}
		if s.Refund != s.DematCredit {
			t.Fatalf("refund %s and demat credit %s are both T+2", s.Refund, s.DematCredit)
		}
		w1, _ := istdate.AddWeekdays(c, 1)
		w3, _ := istdate.AddWeekdays(c, 3)
		if s.Allotment != w1 || s.Listing != w3 {
			t.Fatalf("T+1/T+3 mismatch for %s: %s %s", c, s.Allotment, s.Listing)
		}
	})
}
