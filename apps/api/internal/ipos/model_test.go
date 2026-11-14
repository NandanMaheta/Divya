package ipos

import (
	"testing"
)

// Helpers dt, p64, p32 live in validate_dates_test.go (same package).

func TestStatusRank(t *testing.T) {
	order := []Status{StatusUpcoming, StatusOpen, StatusClosed, StatusAllotmentOut, StatusListed, StatusSettled}
	for want, s := range order {
		if got := s.Rank(); got != want {
			t.Fatalf("%s.Rank() = %d, want %d", s, got, want)
		}
	}
}

func TestMinApplication_FR004(t *testing.T) {
	tests := []struct {
		name string
		ipo  IPO
		want int64
		ok   bool
	}{
		{"lot x final price", IPO{LotSize: p32(100), IssuePricePaise: p64(14700)}, 1470000, true},
		{"lot x band cap when no final price", IPO{LotSize: p32(100), PriceBandFloorPaise: p64(14000), PriceBandCapPaise: p64(14700)}, 1470000, true},
		{"final price wins over band cap", IPO{LotSize: p32(100), IssuePricePaise: p64(14500), PriceBandFloorPaise: p64(14000), PriceBandCapPaise: p64(14700)}, 1450000, true},
		{"final price outside the band is still used", IPO{LotSize: p32(10), IssuePricePaise: p64(50000), PriceBandFloorPaise: p64(14000), PriceBandCapPaise: p64(14700)}, 500000, true},
		{"fixed price band", IPO{LotSize: p32(50), PriceBandFloorPaise: p64(30000), PriceBandCapPaise: p64(30000)}, 1500000, true},
		{"no price", IPO{LotSize: p32(100)}, 0, false},
		{"no lot size", IPO{IssuePricePaise: p64(14700)}, 0, false},
		{"nothing set", IPO{}, 0, false},
		{"maximum bounds stay exact", IPO{LotSize: p32(1_000_000), IssuePricePaise: p64(1_000_000_000)}, 1_000_000_000_000_000, true},
		{"one share one paisa", IPO{LotSize: p32(1), IssuePricePaise: p64(1)}, 1, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.ipo.MinApplicationPaise()
			if ok != tc.ok || (ok && got != tc.want) {
				t.Fatalf("MinApplicationPaise() = (%d, %v), want (%d, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestWarning_FR005(t *testing.T) {
	at := func(minApp int64) IPO { // lot 1 so the value equals the price
		return IPO{LotSize: p32(1), IssuePricePaise: p64(minApp)}
	}
	tests := []struct {
		name string
		ipo  IPO
		warn bool
	}{
		{"1 paisa below the lower bound", at(999_999), true},
		{"exactly 10,000 rupees is fine", at(1_000_000), false},
		{"inside the range", at(1_470_000), false},
		{"exactly 20,000 rupees is fine", at(2_000_000), false},
		{"1 paisa above the upper bound", at(2_000_001), true},
		{"25,000 rupees", IPO{LotSize: p32(100), IssuePricePaise: p64(25_000)}, true},
		{"unknown value gives no warning", IPO{LotSize: p32(100)}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := warnings(tc.ipo)
			if !tc.warn {
				if len(got) != 0 {
					t.Fatalf("want no warning, got %+v", got)
				}
				return
			}
			if len(got) != 1 || got[0].Code != "ipo.min_application_out_of_range" {
				t.Fatalf("want exactly one ipo.min_application_out_of_range, got %+v", got)
			}
			v, _ := tc.ipo.MinApplicationPaise()
			if got[0].Params["value_paise"] != v || got[0].Params["min_paise"] != 1_000_000 || got[0].Params["max_paise"] != 2_000_000 {
				t.Fatalf("params = %v, want value_paise=%d min_paise=1000000 max_paise=2000000", got[0].Params, v)
			}
		})
	}
}

func TestWarning_FR005_DetailUsesIndianFormatting(t *testing.T) {
	got := warnings(IPO{LotSize: p32(100), IssuePricePaise: p64(25_000)}) // 100 x 250 rupees = 25,000 rupees
	if len(got) != 1 {
		t.Fatalf("want one warning, got %+v", got)
	}
	const want = "minimum application value ₹25,000 is outside ₹10,000–₹20,000"
	if got[0].Detail != want {
		t.Fatalf("detail = %q, want %q", got[0].Detail, want)
	}
	lakh := warnings(IPO{LotSize: p32(100), IssuePricePaise: p64(150_000)}) // 1,50,000 rupees
	if len(lakh) != 1 || lakh[0].Detail != "minimum application value ₹1,50,000 is outside ₹10,000–₹20,000" {
		t.Fatalf("lakh grouping: %+v", lakh)
	}
}

func TestEligible_BR_IPO_02(t *testing.T) {
	apply, dont := FlagApply, FlagDoNotApply
	tests := []struct {
		name      string
		published bool
		flag      *ApplyFlag
		want      bool
	}{
		{"published and APPLY", true, &apply, true},
		{"published and DO_NOT_APPLY", true, &dont, false},
		{"published and no flag", true, nil, false},
		{"draft and APPLY", false, &apply, false},
		{"draft and DO_NOT_APPLY", false, &dont, false},
		{"draft and no flag", false, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := (IPO{Published: tc.published, Flag: tc.flag}).Eligible(); got != tc.want {
				t.Fatalf("Eligible() = %v, want %v", got, tc.want)
			}
		})
	}
}

// Eligibility does not depend on status (spec Edge Cases: a LISTED published APPLY IPO stays eligible
// as far as this feature is concerned).
func TestEligible_BR_IPO_02_IgnoresStatus(t *testing.T) {
	apply := FlagApply
	for _, s := range []Status{StatusUpcoming, StatusOpen, StatusClosed, StatusAllotmentOut, StatusListed, StatusSettled} {
		if !(IPO{Published: true, Flag: &apply, Status: s}).Eligible() {
			t.Fatalf("status %s must not affect eligibility", s)
		}
	}
}
