package ipos

import (
	"errors"
	"testing"

	"pgregory.net/rapid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
)

type dateRel struct {
	a, b   int // indexes into dateFields
	strict bool
}

var dateFields = []string{"open_date", "close_date", "allotment_date", "refund_date", "demat_credit_date", "listing_date"}

// FR-040: open <= close < allotment <= refund <= listing, allotment <= demat <= listing, allotment < listing.
var directRels = []dateRel{{0, 1, false}, {1, 2, true}, {2, 3, false}, {3, 5, false}, {2, 4, false}, {4, 5, false}, {2, 5, true}}

func datesFromDays(days [6]int) Dates {
	at := func(i int) *istdate.Date {
		if days[i] < 0 {
			return nil
		}
		return dt(days[i] + 1)
	}
	return Dates{Open: at(0), Close: at(1), Allotment: at(2), Refund: at(3), DematCredit: at(4), Listing: at(5)}
}

// closureModel is an independent brute-force model of research R-07: Floyd-Warshall over the six
// dates, then every related pair of set dates is checked. It returns the violated pairs.
func closureModel(days [6]int) [][2]int {
	const n = 6
	var rel [n][n]int // 0 none, 1 <=, 2 <
	for _, r := range directRels {
		v := 1
		if r.strict {
			v = 2
		}
		rel[r.a][r.b] = v
	}
	for k := 0; k < n; k++ {
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				if rel[i][k] > 0 && rel[k][j] > 0 {
					v := 1
					if rel[i][k] == 2 || rel[k][j] == 2 {
						v = 2
					}
					if v > rel[i][j] {
						rel[i][j] = v
					}
				}
			}
		}
	}
	var bad [][2]int
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if rel[i][j] == 0 || days[i] < 0 || days[j] < 0 {
				continue
			}
			if (rel[i][j] == 2 && days[i] >= days[j]) || (rel[i][j] == 1 && days[i] > days[j]) {
				bad = append(bad, [2]int{i, j})
			}
		}
	}
	return bad
}

func TestDateOrder_BR_IPO_03_Properties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var days [6]int
		for i := range days {
			days[i] = rapid.IntRange(-1, 6).Draw(t, dateFields[i]) // -1 = not set
		}
		d := okDraft()
		d.Dates = datesFromDays(days)
		err := validateDraft(d)
		bad := closureModel(days)

		if len(bad) == 0 {
			if err != nil {
				t.Fatalf("model accepts %v but validator rejected: %v", days, err)
			}
			return
		}
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Code != "ipo.date_order" {
			t.Fatalf("model rejects %v (pairs %v) but validator returned %v", days, bad, err)
		}
		allowed := map[string]bool{}
		for _, p := range bad {
			allowed[dateFields[p[0]]] = true
			allowed[dateFields[p[1]]] = true
		}
		got := issueFields(err)
		if len(got) == 0 {
			t.Fatalf("date_order without any named field")
		}
		for _, f := range got {
			if !allowed[f] {
				t.Fatalf("issue names %s which is in no violated pair %v", f, bad)
			}
		}
		for f := range allowed {
			if !contains(got, f) {
				t.Fatalf("violated pair field %s is not named (got %v)", f, got)
			}
		}
	})
}

// FR-004 / SC: exact integer paise, no overflow inside the allowed bounds, always JSON-safe (< 2^53).
func TestMinApplication_Properties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		lot := rapid.Int32Range(1, 1_000_000).Draw(t, "lot")
		price := rapid.Int64Range(1, 1_000_000_000).Draw(t, "price")
		band := rapid.Int64Range(1, 1_000_000_000).Draw(t, "band")

		withFinal := IPO{LotSize: &lot, IssuePricePaise: &price, PriceBandCapPaise: &band, PriceBandFloorPaise: &band}
		got, ok := withFinal.MinApplicationPaise()
		if !ok || got != int64(lot)*price {
			t.Fatalf("final price wins: got (%d,%v), want %d", got, ok, int64(lot)*price)
		}
		if got > 1<<53 {
			t.Fatalf("%d exceeds 2^53", got)
		}

		bandOnly := IPO{LotSize: &lot, PriceBandCapPaise: &band, PriceBandFloorPaise: &band}
		got, ok = bandOnly.MinApplicationPaise()
		if !ok || got != int64(lot)*band {
			t.Fatalf("band cap used without a final price: got (%d,%v), want %d", got, ok, int64(lot)*band)
		}

		if _, ok := (IPO{IssuePricePaise: &price}).MinApplicationPaise(); ok {
			t.Fatalf("no lot size: value must be unknown")
		}
		if _, ok := (IPO{LotSize: &lot}).MinApplicationPaise(); ok {
			t.Fatalf("no price: value must be unknown")
		}
	})
}

var (
	mainboardTokens = []Exchange{ExchangeNSE, ExchangeBSE}
	smeTokens       = []Exchange{"NSE_EMERGE", "BSE_SME"}
)

// BR-IPO-01 / FR-010 / FR-012: any segment list containing an SME token is rejected with
// ipo.sme_not_allowed, whatever else it contains; a list without one is accepted iff it is a
// non-empty set of distinct mainboard exchanges, else ipo.invalid_segment.
func TestSegments_BR_IPO_01_Properties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		tokenGen := rapid.SampledFrom([]Exchange{ExchangeNSE, ExchangeBSE, "NSE_EMERGE", "BSE_SME", "MCX", "", "nse", "SME"})
		in := rapid.SliceOfN(tokenGen, 0, 5).Draw(t, "segments")

		hasSME, distinct, allMain := false, map[Exchange]bool{}, true
		for _, e := range in {
			distinct[e] = true
			isMain := e == ExchangeNSE || e == ExchangeBSE
			allMain = allMain && isMain
			for _, s := range smeTokens {
				if e == s {
					hasSME = true
				}
			}
		}
		d := okDraft()
		d.Exchanges = in
		err := validateDraft(d)

		switch {
		case hasSME:
			wantValidation(t, err, "ipo.sme_not_allowed")
		case len(in) > 0 && allMain && len(distinct) == len(in):
			if err != nil {
				t.Fatalf("%v is a valid mainboard set, got %v", in, err)
			}
		default:
			wantValidation(t, err, "ipo.invalid_segment")
		}
	})
}

// FR-012: SME is recognised by the stated segment only, never by name, symbol, price or size.
func TestSegments_BR_IPO_01_NameSymbolPriceSizeNeverDecideSME(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		d := okDraft()
		d.Name = rapid.SampledFrom([]string{"SME Solar Ltd", "Emerge Foods", "BSE SME Holdings", "NSE_EMERGE Corp"}).Draw(t, "name")
		d.NSESymbol = rapid.Ptr(rapid.SampledFrom([]string{"SMEX", "EMERGE", "A&B"}), true).Draw(t, "symbol")
		d.IssueSizePaise = p64(rapid.Int64Range(1, 1_000_000_000_000_000).Draw(t, "size"))
		d.PriceBandCapPaise = p64(rapid.Int64Range(1, 1_000_000_000).Draw(t, "cap"))
		d.PriceBandFloorPaise = p64(*d.PriceBandCapPaise)
		d.Dates = Dates{Open: dt(18), Close: dt(20)}
		if err := validateDraft(d); err != nil {
			t.Fatalf("a mainboard segment with an SME-looking name/size/price must be accepted: %v", err)
		}
	})
}
