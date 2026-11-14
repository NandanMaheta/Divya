package ipos

import "github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"

type dateField int

const (
	fOpen dateField = iota
	fClose
	fAllotment
	fRefund
	fDemat
	fListing
	nDateFields
)

var dateFieldNames = [nDateFields]string{
	"open_date", "close_date", "allotment_date", "refund_date", "demat_credit_date", "listing_date",
}

// Order relation between consecutive dates (FR-040): ord weak means a <= b, strict means a < b.
const (
	ordNone = iota
	ordWeak
	ordStrict
)

// dateEdges are the direct rules of BR-IPO-03; the closure below derives the rest (research R-07).
var dateEdges = []struct {
	a, b dateField
	ord  int
}{
	{fOpen, fClose, ordWeak},
	{fClose, fAllotment, ordStrict},
	{fAllotment, fRefund, ordWeak},
	{fAllotment, fDemat, ordWeak},
	{fRefund, fListing, ordWeak},
	{fDemat, fListing, ordWeak},
	{fAllotment, fListing, ordStrict},
}

// dateClosure is the transitive closure of dateEdges; a path is strict if any edge on it is.
var dateClosure = buildClosure()

func buildClosure() [nDateFields][nDateFields]int {
	var r [nDateFields][nDateFields]int
	for _, e := range dateEdges {
		r[e.a][e.b] = e.ord
	}
	for k := range nDateFields {
		for i := range nDateFields {
			for j := range nDateFields {
				if r[i][k] == ordNone || r[k][j] == ordNone {
					continue
				}
				ord := ordWeak
				if r[i][k] == ordStrict || r[k][j] == ordStrict {
					ord = ordStrict
				}
				if ord > r[i][j] {
					r[i][j] = ord
				}
			}
		}
	}
	return r
}

func (d Dates) list() [nDateFields]*istdate.Date {
	return [nDateFields]*istdate.Date{d.Open, d.Close, d.Allotment, d.Refund, d.DematCredit, d.Listing}
}

func dateCompare(a, b istdate.Date) int {
	switch {
	case a.Year != b.Year:
		return sign(a.Year - b.Year)
	case a.Month != b.Month:
		return sign(int(a.Month) - int(b.Month))
	default:
		return sign(a.Day - b.Day)
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// dateOrderIssues names both fields of every violated pair among the dates that are set.
func dateOrderIssues(d Dates) []FieldIssue {
	set := d.list()
	var is []FieldIssue
	for a := range nDateFields {
		for b := range nDateFields {
			ord := dateClosure[a][b]
			if ord == ordNone || set[a] == nil || set[b] == nil {
				continue
			}
			c := dateCompare(*set[a], *set[b])
			if c > 0 || (c == 0 && ord == ordStrict) {
				msg := dateFieldNames[a] + " must be on or before " + dateFieldNames[b]
				if ord == ordStrict {
					msg = dateFieldNames[a] + " must be before " + dateFieldNames[b]
				}
				is = append(is,
					FieldIssue{Field: dateFieldNames[a], Message: msg},
					FieldIssue{Field: dateFieldNames[b], Message: msg})
			}
		}
	}
	return is
}
