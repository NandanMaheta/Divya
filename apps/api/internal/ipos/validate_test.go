package ipos

import (
	"strings"
	"testing"
)

// Draft.Exchanges carries the stated segment of each listing. Tokens other than NSE and BSE
// arrive here from the API mapping (R-05): NSE_EMERGE and BSE_SME are SME tokens.

func TestValidate_BR_IPO_01_RejectsSMESegments(t *testing.T) {
	tests := []struct {
		name string
		in   []Exchange
	}{
		{"NSE Emerge alone", []Exchange{"NSE_EMERGE"}},
		{"BSE SME alone", []Exchange{"BSE_SME"}},
		{"both SME segments", []Exchange{"NSE_EMERGE", "BSE_SME"}},
		{"mainboard NSE mixed with BSE SME", []Exchange{ExchangeNSE, "BSE_SME"}},
		{"NSE Emerge mixed with BSE mainboard", []Exchange{"NSE_EMERGE", ExchangeBSE}},
		{"SME token mixed with both mainboard", []Exchange{ExchangeNSE, ExchangeBSE, "NSE_EMERGE"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := okDraft()
			d.Exchanges = tc.in
			wantValidation(t, validateDraft(d), "ipo.sme_not_allowed")
		})
	}
}

func TestValidate_BR_IPO_01_UnknownOrDuplicateSegment(t *testing.T) {
	tests := []struct {
		name string
		in   []Exchange
	}{
		{"missing", nil},
		{"empty", []Exchange{}},
		{"unknown segment", []Exchange{"MCX"}},
		{"unknown next to a valid one", []Exchange{ExchangeNSE, "MCX"}},
		{"empty token", []Exchange{""}},
		{"lowercase token", []Exchange{"nse"}},
		{"duplicate NSE", []Exchange{ExchangeNSE, ExchangeNSE}},
		{"duplicate BSE", []Exchange{ExchangeBSE, ExchangeBSE}},
		{"three entries", []Exchange{ExchangeNSE, ExchangeBSE, ExchangeNSE}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := okDraft()
			d.Exchanges = tc.in
			wantValidation(t, validateDraft(d), "ipo.invalid_segment")
		})
	}
}

func TestValidate_BR_IPO_01_AcceptsMainboardCombos(t *testing.T) {
	for _, in := range [][]Exchange{
		{ExchangeNSE}, {ExchangeBSE}, {ExchangeNSE, ExchangeBSE}, {ExchangeBSE, ExchangeNSE},
	} {
		d := okDraft()
		d.Exchanges = in
		if err := validateDraft(d); err != nil {
			t.Fatalf("%v must be accepted, got %v", in, err)
		}
	}
}

// FR-012: the name never decides.
func TestValidate_BR_IPO_01_NameWithSMEIsAccepted(t *testing.T) {
	for _, name := range []string{"SME Solar Limited", "Emerge Foods", "NSE Emerge Holdings", "BSE SME Power"} {
		d := okDraft()
		d.Name = name
		if err := validateDraft(d); err != nil {
			t.Fatalf("name %q on a mainboard segment must be accepted, got %v", name, err)
		}
	}
}

func TestValidate_PriceBand(t *testing.T) {
	tests := []struct {
		name       string
		floor, cap *int64
		ok         bool
		wantFields []string
	}{
		{"no band", nil, nil, true, nil},
		{"floor below cap", p64(14000), p64(14700), true, nil},
		{"floor equals cap is a fixed price", p64(14700), p64(14700), true, nil},
		{"only floor", p64(14000), nil, false, []string{"price_band_cap_paise"}},
		{"only cap", nil, p64(14700), false, []string{"price_band_floor_paise"}},
		{"floor above cap", p64(14800), p64(14700), false, []string{"price_band_floor_paise", "price_band_cap_paise"}},
		{"floor one paisa above cap", p64(14701), p64(14700), false, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := okDraft()
			d.PriceBandFloorPaise, d.PriceBandCapPaise = tc.floor, tc.cap
			err := validateDraft(d)
			if tc.ok {
				if err != nil {
					t.Fatalf("want accepted, got %v", err)
				}
				return
			}
			wantValidation(t, err, "ipo.invalid_value")
			got := issueFields(err)
			for _, f := range tc.wantFields {
				if !contains(got, f) {
					t.Fatalf("issue fields %v must include %s", got, f)
				}
			}
		})
	}
}

func TestValidate_Values(t *testing.T) {
	long := strings.Repeat("x", 201)
	tests := []struct {
		name  string
		mut   func(d *Draft)
		field string // "" = accepted
	}{
		{"valid minimal", func(*Draft) {}, ""},
		{"name with surrounding blanks is fine", func(d *Draft) { d.Name = "  Acme  " }, ""},
		{"blank name", func(d *Draft) { d.Name = "   " }, "name"},
		{"empty name", func(d *Draft) { d.Name = "" }, "name"},
		{"name over 200", func(d *Draft) { d.Name = strings.Repeat("a", 201) }, "name"},
		{"issue price zero", func(d *Draft) { d.IssuePricePaise = p64(0) }, "issue_price_paise"},
		{"issue price negative", func(d *Draft) { d.IssuePricePaise = p64(-5) }, "issue_price_paise"},
		{"issue price above one crore rupees", func(d *Draft) { d.IssuePricePaise = p64(1_000_000_001) }, "issue_price_paise"},
		{"issue price at the maximum", func(d *Draft) { d.IssuePricePaise = p64(1_000_000_000) }, ""},
		{"issue size zero", func(d *Draft) { d.IssueSizePaise = p64(0) }, "issue_size_paise"},
		{"issue size above the maximum", func(d *Draft) { d.IssueSizePaise = p64(1_000_000_000_000_001) }, "issue_size_paise"},
		{"issue size at the maximum", func(d *Draft) { d.IssueSizePaise = p64(1_000_000_000_000_000) }, ""},
		{"band end zero", func(d *Draft) { d.PriceBandFloorPaise, d.PriceBandCapPaise = p64(0), p64(100) }, "price_band_floor_paise"},
		{"band cap above the maximum", func(d *Draft) { d.PriceBandFloorPaise, d.PriceBandCapPaise = p64(100), p64(1_000_000_001) }, "price_band_cap_paise"},
		{"lot size zero", func(d *Draft) { d.LotSize = p32(0) }, "lot_size"},
		{"lot size negative", func(d *Draft) { d.LotSize = p32(-1) }, "lot_size"},
		{"lot size above the maximum", func(d *Draft) { d.LotSize = p32(1_000_001) }, "lot_size"},
		{"lot size at the maximum", func(d *Draft) { d.LotSize = p32(1_000_000) }, ""},
		{"lead managers ok", func(d *Draft) { d.LeadManagers = []string{"Axis Capital", "ICICI Securities"} }, ""},
		{"empty lead manager", func(d *Draft) { d.LeadManagers = []string{"Axis Capital", ""} }, "lead_managers"},
		{"blank lead manager", func(d *Draft) { d.LeadManagers = []string{"   "} }, "lead_managers"},
		{"lead manager over 200", func(d *Draft) { d.LeadManagers = []string{long[:201]} }, "lead_managers"},
		{"more than 20 lead managers", func(d *Draft) {
			d.LeadManagers = make([]string, 21)
			for i := range d.LeadManagers {
				d.LeadManagers[i] = "LM"
			}
		}, "lead_managers"},
		{"nse symbol ok", func(d *Draft) { d.NSESymbol = ptrStr("M&M-FIN") }, ""},
		{"nse symbol lowercase", func(d *Draft) { d.NSESymbol = ptrStr("acme") }, "nse_symbol"},
		{"nse symbol too long", func(d *Draft) { d.NSESymbol = ptrStr(strings.Repeat("A", 21)) }, "nse_symbol"},
		{"offer document https", func(d *Draft) { d.OfferDocumentURL = ptrStr("https://example.com/rhp.pdf") }, ""},
		{"offer document http", func(d *Draft) { d.OfferDocumentURL = ptrStr("http://example.com/rhp.pdf") }, "offer_document_url"},
		{"registrar empty", func(d *Draft) { d.Registrar = ptrStr("") }, "registrar"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := okDraft()
			tc.mut(&d)
			err := validateDraft(d)
			if tc.field == "" {
				if err != nil {
					t.Fatalf("want accepted, got %v", err)
				}
				return
			}
			wantValidation(t, err, "ipo.invalid_value")
			if got := issueFields(err); !contains(got, tc.field) {
				t.Fatalf("issue fields %v must include %s", got, tc.field)
			}
		})
	}
}

func ptrStr(s string) *string { return &s }
