package ipos

import (
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/inr"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
)

// Status is the IPO lifecycle status (FR-030).
type Status string

// Lifecycle statuses, lowest rank first.
const (
	StatusUpcoming     Status = "UPCOMING"
	StatusOpen         Status = "OPEN"
	StatusClosed       Status = "CLOSED"
	StatusAllotmentOut Status = "ALLOTMENT_OUT"
	StatusListed       Status = "LISTED"
	StatusSettled      Status = "SETTLED"
)

// Rank orders statuses along the lifecycle (UPCOMING lowest).
func (s Status) Rank() int {
	switch s {
	case StatusUpcoming:
		return 0
	case StatusOpen:
		return 1
	case StatusClosed:
		return 2
	case StatusAllotmentOut:
		return 3
	case StatusListed:
		return 4
	case StatusSettled:
		return 5
	}
	return -1
}

// StatusSource records who set the current status.
type StatusSource string

// Status sources.
const (
	SourceSchedule   StatusSource = "schedule"
	SourceAdmin      StatusSource = "admin"
	SourceSettlement StatusSource = "settlement"
)

// ApplyFlag is the operator verdict on an IPO (BR-IPO-02).
type ApplyFlag string

// Apply flags.
const (
	FlagApply      ApplyFlag = "APPLY"
	FlagDoNotApply ApplyFlag = "DO_NOT_APPLY"
)

// Exchange is a mainboard exchange a listing is stated on.
type Exchange string

// Mainboard exchanges.
const (
	ExchangeNSE Exchange = "NSE"
	ExchangeBSE Exchange = "BSE"
)

// Dates are the IST civil dates of an IPO; nil means not set.
type Dates struct{ Open, Close, Allotment, Refund, DematCredit, Listing *istdate.Date }

// IPO is one catalogue entry.
type IPO struct {
	ID, OrgID                                               uuid.UUID
	Name                                                    string
	Exchanges                                               []Exchange
	NSESymbol                                               *string
	IssueSizePaise                                          *int64
	PriceBandFloorPaise, PriceBandCapPaise, IssuePricePaise *int64
	LotSize                                                 *int32
	Dates                                                   Dates
	Registrar                                               *string
	LeadManagers                                            []string
	OfferDocumentURL                                        *string
	Flag                                                    *ApplyFlag
	FlagNote                                                *string
	Published                                               bool
	FirstPublishedAt                                        *time.Time
	Status                                                  Status
	StatusSource                                            StatusSource
	StatusChangedAt                                         time.Time
	LastOverrideAt                                          *time.Time
	Source                                                  string
	SourceRef                                               *string
	LastSyncedAt                                            *time.Time
	Version                                                 int32
	CreatedAt, UpdatedAt                                    time.Time
}

// Eligible reports Published && Flag == APPLY (FR-023).
func (i IPO) Eligible() bool { return i.Published && i.Flag != nil && *i.Flag == FlagApply }

// MinApplicationPaise is lot x (issue price, else band cap); false when unknown (FR-004).
func (i IPO) MinApplicationPaise() (int64, bool) {
	if i.LotSize == nil {
		return 0, false
	}
	var price int64
	switch {
	case i.IssuePricePaise != nil:
		price = *i.IssuePricePaise
	case i.PriceBandCapPaise != nil:
		price = *i.PriceBandCapPaise
	default:
		return 0, false
	}
	return int64(*i.LotSize) * price, true
}

// Draft is the editable facts (create and PUT input).
type Draft struct {
	Name                                                    string
	Exchanges                                               []Exchange
	NSESymbol                                               *string
	IssueSizePaise                                          *int64
	PriceBandFloorPaise, PriceBandCapPaise, IssuePricePaise *int64
	LotSize                                                 *int32
	Dates                                                   Dates
	Registrar                                               *string
	LeadManagers                                            []string
	OfferDocumentURL                                        *string
	FlagNote                                                *string
}

// Warning is a soft, non-blocking finding (only ipo.min_application_out_of_range in 008).
type Warning struct {
	Code   string
	Fields []string
	Detail string
	Params map[string]int64
}

// Result is what every mutation returns.
type Result struct {
	IPO      IPO
	Warnings []Warning
	Replayed bool
}

// Page is one keyset page; NextCursor is empty on the last page.
type Page[T any] struct {
	Items      []T
	NextCursor string
}

// AdminFilter filters the admin list. Flag "NONE" means no flag set.
type AdminFilter struct {
	Status     *Status
	Published  *bool
	Flag       *string
	MonthStart *istdate.Date
	MonthEnd   *istdate.Date
	Cursor     string
	Limit      int
}

// ClientFilter filters the client list (published IPOs only).
type ClientFilter struct {
	Status *Status
	Cursor string
	Limit  int
}

// Soft bounds of the minimum application value (FR-005), integer paise.
const (
	minApplicationLowPaise  int64 = 1_000_000
	minApplicationHighPaise int64 = 2_000_000
)

// warnings returns the soft findings for an IPO (FR-005): the minimum application value is
// outside Rs 10,000 to Rs 20,000. Bounds are inclusive; an unknown value gives no warning.
func warnings(i IPO) []Warning {
	v, ok := i.MinApplicationPaise()
	if !ok || (v >= minApplicationLowPaise && v <= minApplicationHighPaise) {
		return nil
	}
	return []Warning{{
		Code:   "ipo.min_application_out_of_range",
		Fields: []string{"lot_size", "issue_price_paise", "price_band_cap_paise"},
		Detail: "minimum application value " + inr.Format(v) + " is outside " +
			inr.Format(minApplicationLowPaise) + "–" + inr.Format(minApplicationHighPaise),
		Params: map[string]int64{"value_paise": v, "min_paise": minApplicationLowPaise, "max_paise": minApplicationHighPaise},
	}}
}
