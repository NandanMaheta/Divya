package ipos

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Value bounds, mirrored by the CHECK constraints of migration 00004 (defence in depth).
const (
	maxNameLen        = 200
	maxIssueSizePaise = 1_000_000_000_000_000
	maxSharePaise     = 1_000_000_000
	maxLotSize        = 1_000_000
	maxLeadManagers   = 20
	maxTextLen        = 200
	maxURLLen         = 2000
)

var nseSymbolRE = regexp.MustCompile(`^[A-Z0-9&-]{1,20}$`)

// smeSegments are the SME segments the API mapping passes through so they can be refused (R-05).
var smeSegments = map[Exchange]bool{"NSE_EMERGE": true, "BSE_SME": true}

// validateDraft checks segments (BR-IPO-01), values, price band and date order; it returns a
// *ValidationError whose Code is the first failing category in that order.
func validateDraft(d Draft) error {
	if err := validateSegments(d.Exchanges); err != nil {
		return err
	}
	if issues := valueIssues(d); len(issues) > 0 {
		return &ValidationError{Code: "ipo.invalid_value", Issues: issues}
	}
	if issues := dateOrderIssues(d.Dates); len(issues) > 0 {
		return &ValidationError{Code: "ipo.date_order", Issues: issues}
	}
	return nil
}

func validateSegments(in []Exchange) error {
	for _, e := range in {
		if smeSegments[e] {
			return &ValidationError{Code: "ipo.sme_not_allowed", Issues: []FieldIssue{
				{Field: "segments", Message: "SME segments are not supported; only NSE and BSE mainboard"}}}
		}
	}
	valid := len(in) >= 1 && len(in) <= 2
	seen := map[Exchange]bool{}
	for _, e := range in {
		if (e != ExchangeNSE && e != ExchangeBSE) || seen[e] {
			valid = false
		}
		seen[e] = true
	}
	if !valid {
		return &ValidationError{Code: "ipo.invalid_segment", Issues: []FieldIssue{
			{Field: "segments", Message: "give NSE_MAINBOARD, BSE_MAINBOARD or both, without repeats"}}}
	}
	return nil
}

func valueIssues(d Draft) []FieldIssue {
	var is []FieldIssue
	add := func(field, msg string) { is = append(is, FieldIssue{Field: field, Message: msg}) }
	if n := utf8.RuneCountInString(strings.TrimSpace(d.Name)); n < 1 || n > maxNameLen {
		add("name", "must be 1 to 200 characters")
	}
	if d.NSESymbol != nil && !nseSymbolRE.MatchString(*d.NSESymbol) {
		add("nse_symbol", "must be 1 to 20 characters of A-Z, 0-9, & or -")
	}
	if v := d.IssueSizePaise; v != nil && (*v < 1 || *v > maxIssueSizePaise) {
		add("issue_size_paise", "out of range")
	}
	if v := d.IssuePricePaise; v != nil && (*v < 1 || *v > maxSharePaise) {
		add("issue_price_paise", "out of range")
	}
	is = append(is, bandIssues(d.PriceBandFloorPaise, d.PriceBandCapPaise)...)
	if v := d.LotSize; v != nil && (*v < 1 || *v > maxLotSize) {
		add("lot_size", "must be 1 to 1,000,000")
	}
	if d.Registrar != nil {
		if n := utf8.RuneCountInString(strings.TrimSpace(*d.Registrar)); n < 1 || n > maxTextLen {
			add("registrar", "must be 1 to 200 characters")
		}
	}
	if len(d.LeadManagers) > maxLeadManagers {
		add("lead_managers", "at most 20 lead managers")
	}
	for _, lm := range d.LeadManagers {
		if n := utf8.RuneCountInString(strings.TrimSpace(lm)); n < 1 || n > maxTextLen {
			add("lead_managers", "each lead manager must be 1 to 200 characters")
			break
		}
	}
	if u := d.OfferDocumentURL; u != nil && (!strings.HasPrefix(*u, "https://") || utf8.RuneCountInString(*u) > maxURLLen) {
		add("offer_document_url", "must be an https URL of at most 2000 characters")
	}
	return is
}

func bandIssues(floor, capP *int64) []FieldIssue {
	var is []FieldIssue
	add := func(field, msg string) { is = append(is, FieldIssue{Field: field, Message: msg}) }
	switch {
	case floor == nil && capP == nil:
		return nil
	case floor == nil:
		add("price_band_floor_paise", "give both ends of the band or neither")
	case capP == nil:
		add("price_band_cap_paise", "give both ends of the band or neither")
	}
	if floor != nil && (*floor < 1 || *floor > maxSharePaise) {
		add("price_band_floor_paise", "out of range")
	}
	if capP != nil && (*capP < 1 || *capP > maxSharePaise) {
		add("price_band_cap_paise", "out of range")
	}
	if floor != nil && capP != nil && *floor > *capP {
		add("price_band_floor_paise", "floor must not exceed the cap")
		add("price_band_cap_paise", "cap must not be below the floor")
	}
	return is
}

// validatePublishable checks the FR-020 publish requirements and lists every missing item.
func validatePublishable(i IPO) error {
	var is []FieldIssue
	missing := func(field string) { is = append(is, FieldIssue{Field: field, Message: "required to publish"}) }
	if i.Flag == nil {
		missing("apply_flag")
	}
	if i.Dates.Open == nil {
		missing("open_date")
	}
	if i.Dates.Close == nil {
		missing("close_date")
	}
	if i.IssuePricePaise == nil && i.PriceBandCapPaise == nil {
		missing("price")
	}
	if i.LotSize == nil {
		missing("lot_size")
	}
	if len(is) == 0 {
		return nil
	}
	return &ValidationError{Code: "ipo.publish_incomplete", Issues: is}
}
