//nolint:unparam,revive,unused // S01 compile skeleton; removed as slices land
package ipos

import (
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
)

type Status string

const (
	StatusUpcoming     Status = "UPCOMING"
	StatusOpen         Status = "OPEN"
	StatusClosed       Status = "CLOSED"
	StatusAllotmentOut Status = "ALLOTMENT_OUT"
	StatusListed       Status = "LISTED"
	StatusSettled      Status = "SETTLED"
)

// Rank orders statuses along the lifecycle (UPCOMING lowest).
func (s Status) Rank() int { panic(errNotImplemented) }

type StatusSource string

const (
	SourceSchedule   StatusSource = "schedule"
	SourceAdmin      StatusSource = "admin"
	SourceSettlement StatusSource = "settlement"
)

type ApplyFlag string

const (
	FlagApply      ApplyFlag = "APPLY"
	FlagDoNotApply ApplyFlag = "DO_NOT_APPLY"
)

type Exchange string

const (
	ExchangeNSE Exchange = "NSE"
	ExchangeBSE Exchange = "BSE"
)

type Dates struct{ Open, Close, Allotment, Refund, DematCredit, Listing *istdate.Date }

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
	Source                                                  string
	SourceRef                                               *string
	LastSyncedAt                                            *time.Time
	Version                                                 int32
	CreatedAt, UpdatedAt                                    time.Time
}

// Eligible reports Published && Flag == APPLY (FR-023).
func (i IPO) Eligible() bool { panic(errNotImplemented) }

// MinApplicationPaise is lot x (issue price, else band cap); false when unknown (FR-004).
func (i IPO) MinApplicationPaise() (int64, bool) { panic(errNotImplemented) }

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
