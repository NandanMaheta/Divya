package ipos

import (
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
)

// Assumptions the contracts leave open (see specs/008-ipo-catalogue/research.md R-07 and
// contracts/error-codes.md): date order is checked by validateDraft (Draft.Dates) with Code
// "ipo.date_order"; FieldIssue.Field is the API field name (open_date, close_date, allotment_date,
// refund_date, demat_credit_date, listing_date), optionally prefixed "body."; publish
// completeness is validatePublishable(IPO) with Code "ipo.publish_incomplete" and one issue per
// missing item: apply_flag, open_date, close_date, price, lot_size.

func dt(day int) *istdate.Date {
	d := istdate.Date{Year: 2026, Month: time.November, Day: day}
	return &d
}

func p64(v int64) *int64 { return &v }
func p32(v int32) *int32 { return &v }

func okDraft() Draft {
	return Draft{Name: "Acme Industries", Exchanges: []Exchange{ExchangeNSE}}
}

func issueFields(err error) []string {
	var ve *ValidationError
	if !errors.As(err, &ve) {
		return nil
	}
	out := make([]string, 0, len(ve.Issues))
	for _, i := range ve.Issues {
		out = append(out, strings.TrimPrefix(i.Field, "body."))
	}
	sort.Strings(out)
	return out
}

func wantValidation(t fataler, err error, code string, fields ...string) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want *ValidationError %s, got %T %v", code, err, err)
	}
	if ve.Code != code {
		t.Fatalf("code = %q, want %q (issues %v)", ve.Code, code, ve.Issues)
	}
	if fields == nil {
		return
	}
	want := append([]string(nil), fields...)
	sort.Strings(want)
	got := issueFields(err)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("issue fields = %v, want %v", got, want)
	}
}

func TestValidate_BR_IPO_03_DateOrder(t *testing.T) {
	tests := []struct {
		name   string
		dates  Dates
		fields []string // nil = accepted
	}{
		{"no dates", Dates{}, nil},
		{"full valid order", Dates{Open: dt(18), Close: dt(20), Allotment: dt(23), Refund: dt(24), DematCredit: dt(24), Listing: dt(25)}, nil},
		{"open equals close", Dates{Open: dt(18), Close: dt(18)}, nil},
		{"open after close", Dates{Open: dt(19), Close: dt(18)}, []string{"open_date", "close_date"}},
		{"allotment equals close", Dates{Close: dt(20), Allotment: dt(20)}, []string{"close_date", "allotment_date"}},
		{"allotment before close", Dates{Close: dt(20), Allotment: dt(19)}, []string{"close_date", "allotment_date"}},
		{"refund equals allotment", Dates{Allotment: dt(23), Refund: dt(23)}, nil},
		{"refund before allotment", Dates{Allotment: dt(23), Refund: dt(22)}, []string{"allotment_date", "refund_date"}},
		{"refund after listing", Dates{Refund: dt(26), Listing: dt(25)}, []string{"refund_date", "listing_date"}},
		{"refund equals listing", Dates{Refund: dt(25), Listing: dt(25)}, nil},
		{"demat before allotment", Dates{Allotment: dt(23), DematCredit: dt(22)}, []string{"allotment_date", "demat_credit_date"}},
		{"demat after listing", Dates{DematCredit: dt(26), Listing: dt(25)}, []string{"demat_credit_date", "listing_date"}},
		{"listing equals allotment", Dates{Allotment: dt(23), Listing: dt(23)}, []string{"allotment_date", "listing_date"}},
		{"listing before allotment", Dates{Allotment: dt(23), Listing: dt(20)}, []string{"allotment_date", "listing_date"}},
		{"refund and demat may differ", Dates{Allotment: dt(23), Refund: dt(24), DematCredit: dt(25), Listing: dt(26)}, nil},
		{"refund and demat unordered between themselves", Dates{Allotment: dt(23), Refund: dt(25), DematCredit: dt(24), Listing: dt(26)}, nil},
		// R-07: transitive closure over the dates that are set.
		{"open after listing without close", Dates{Open: dt(26), Listing: dt(25)}, []string{"open_date", "listing_date"}},
		{"open equal to listing without close", Dates{Open: dt(25), Listing: dt(25)}, []string{"open_date", "listing_date"}},
		{"open before listing without close", Dates{Open: dt(18), Listing: dt(25)}, nil},
		{"close after refund without allotment", Dates{Close: dt(24), Refund: dt(23)}, []string{"close_date", "refund_date"}},
		{"close equals refund without allotment", Dates{Close: dt(23), Refund: dt(23)}, []string{"close_date", "refund_date"}},
		{"open after demat without allotment", Dates{Open: dt(24), DematCredit: dt(23)}, []string{"open_date", "demat_credit_date"}},
		{"weekend and holiday dates are allowed", Dates{Open: dt(14), Close: dt(15), Allotment: dt(21), Listing: dt(22)}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := okDraft()
			d.Dates = tc.dates
			err := validateDraft(d)
			if tc.fields == nil {
				if err != nil {
					t.Fatalf("want accepted, got %v", err)
				}
				return
			}
			wantValidation(t, err, "ipo.date_order")
			got := issueFields(err)
			for _, f := range tc.fields {
				if !contains(got, f) {
					t.Fatalf("issue fields %v must name %s (pair %v)", got, f, tc.fields)
				}
			}
		})
	}
}

func TestValidate_BR_IPO_03_DateOrderNamesEveryViolatedPair(t *testing.T) {
	d := okDraft()
	// open > close and refund < allotment: two independent violations, all four fields named.
	d.Dates = Dates{Open: dt(20), Close: dt(18), Allotment: dt(23), Refund: dt(22)}
	err := validateDraft(d)
	wantValidation(t, err, "ipo.date_order")
	got := issueFields(err)
	for _, f := range []string{"open_date", "close_date", "allotment_date", "refund_date"} {
		if !contains(got, f) {
			t.Fatalf("issue fields %v must name %s", got, f)
		}
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func publishable() IPO {
	flag := FlagApply
	return IPO{
		Name: "Acme", Exchanges: []Exchange{ExchangeNSE}, Flag: &flag,
		LotSize: p32(100), PriceBandFloorPaise: p64(14000), PriceBandCapPaise: p64(14700),
		Dates: Dates{Open: dt(18), Close: dt(20)},
	}
}

func TestValidate_BR_IPO_02_PublishRequirements(t *testing.T) {
	flag := FlagDoNotApply
	tests := []struct {
		name   string
		mut    func(i *IPO)
		fields []string // nil = publishable
	}{
		{"complete with band", func(*IPO) {}, nil},
		{"complete with final price only", func(i *IPO) {
			i.PriceBandFloorPaise, i.PriceBandCapPaise, i.IssuePricePaise = nil, nil, p64(14700)
		}, nil},
		{"DO_NOT_APPLY is publishable", func(i *IPO) { i.Flag = &flag }, nil},
		{"final price and band", func(i *IPO) { i.IssuePricePaise = p64(14500) }, nil},
		{"missing flag", func(i *IPO) { i.Flag = nil }, []string{"apply_flag"}},
		{"missing open date", func(i *IPO) { i.Dates.Open = nil }, []string{"open_date"}},
		{"missing close date", func(i *IPO) { i.Dates.Close = nil }, []string{"close_date"}},
		{"missing lot size", func(i *IPO) { i.LotSize = nil }, []string{"lot_size"}},
		{"missing any price", func(i *IPO) { i.PriceBandFloorPaise, i.PriceBandCapPaise = nil, nil }, []string{"price"}},
	}
	tests = append(tests, struct {
		name   string
		mut    func(i *IPO)
		fields []string
	}{"floor without cap is not a band", func(i *IPO) { i.PriceBandCapPaise = nil }, []string{"price"}})
	tests = append(tests, struct {
		name   string
		mut    func(i *IPO)
		fields []string
	}{"every item missing is listed at once", func(i *IPO) {
		i.Flag, i.LotSize = nil, nil
		i.PriceBandFloorPaise, i.PriceBandCapPaise = nil, nil
		i.Dates = Dates{}
	}, []string{"apply_flag", "open_date", "close_date", "price", "lot_size"}})

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			i := publishable()
			tc.mut(&i)
			err := validatePublishable(i)
			if tc.fields == nil {
				if err != nil {
					t.Fatalf("want publishable, got %v", err)
				}
				return
			}
			wantValidation(t, err, "ipo.publish_incomplete", tc.fields...)
		})
	}
}

// FR-021: while published an IPO keeps meeting FR-020; the service validates the result of an
// edit or flag clear with validatePublishable before it writes.
func TestValidate_BR_IPO_02_PublishedEditKeepsRequirements_FR021(t *testing.T) {
	i := publishable()
	i.Published = true
	i.Dates.Open = nil // an edit that clears the open date
	wantValidation(t, validatePublishable(i), "ipo.publish_incomplete", "open_date")

	i = publishable()
	i.Published = true
	i.Flag = nil // a flag clear on a published IPO
	wantValidation(t, validatePublishable(i), "ipo.publish_incomplete", "apply_flag")

	i = publishable()
	i.Published = true
	i.PriceBandFloorPaise, i.PriceBandCapPaise, i.IssuePricePaise = nil, nil, p64(15000)
	if err := validatePublishable(i); err != nil {
		t.Fatalf("swapping band for a final price keeps it publishable: %v", err)
	}
}

// fataler is satisfied by *testing.T and *rapid.T.
type fataler interface {
	Helper()
	Fatalf(format string, args ...any)
}
