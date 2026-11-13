// Package istdate holds the IST business-date helpers shared by every module: the IST zone
// (independent of the host's time.Local), calendar dates, weekday arithmetic and the payment
// due date of BR-SET-08.
package istdate

import (
	"errors"
	"fmt"
	"time"
	_ "time/tzdata" // embed the zone database so IST loads on hosts without zoneinfo
)

// IST is Asia/Kolkata (+05:30, no daylight saving). Loading it cannot fail with tzdata embedded.
var IST = mustLoadIST()

func mustLoadIST() *time.Location {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		panic(fmt.Sprintf("istdate: load Asia/Kolkata: %v", err))
	}
	return loc
}

// Date is a calendar date in IST.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// DateOf returns the IST business date of an instant, whatever zone t is expressed in.
func DateOf(t time.Time) Date {
	y, m, d := t.In(IST).Date()
	return Date{Year: y, Month: m, Day: d}
}

// At returns the instant of d at h:m IST, in UTC.
func (d Date) At(h, m int) time.Time {
	return time.Date(d.Year, d.Month, d.Day, h, m, 0, 0, IST).UTC()
}

// Weekday returns the day of the week of d.
func (d Date) Weekday() time.Weekday {
	return d.civil().Weekday()
}

// AddDays returns the date n calendar days after d (n may be negative).
func (d Date) AddDays(n int) Date {
	y, m, day := d.civil().AddDate(0, 0, n).Date()
	return Date{Year: y, Month: m, Day: day}
}

// String formats d as YYYY-MM-DD.
func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, int(d.Month), d.Day)
}

// civil is d as midnight UTC, used only for calendar arithmetic (no zone rules involved).
func (d Date) civil() time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC)
}

// ParseDate parses the strict YYYY-MM-DD form produced by Date.String.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return Date{}, fmt.Errorf("istdate: parse date %q: %w", s, err)
	}
	return Date{Year: t.Year(), Month: t.Month(), Day: t.Day()}, nil
}

// AddWeekdays returns the date n weekdays after d, skipping only Saturday and Sunday
// (exchange holidays are deliberately not skipped, BR-SET-08). n == 0 returns d unchanged.
// A negative n is a programmer error.
func AddWeekdays(d Date, n int) (Date, error) {
	if n < 0 {
		return Date{}, errors.New("istdate: AddWeekdays: n must not be negative")
	}
	return addWeekdays(d, n), nil
}

func addWeekdays(d Date, n int) Date {
	for ; n > 0; n-- {
		d = d.AddDays(1)
		for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			d = d.AddDays(1)
		}
	}
	return d
}

// PaymentDue is the payment due instant for a listing date: two weekdays later at 23:59 IST
// (BR-SET-08, GS-04), returned in UTC.
func PaymentDue(listing Date) time.Time {
	return addWeekdays(listing, 2).At(23, 59)
}
