// Table tests for the IST business-date helpers (feature 002, US4, FR-033).
// Rules: BR-SET-08 (due date = listing day + 2 weekdays at 23:59 IST), GS-04.
//
// Contract: specs/002-scheduler-tick/contracts/go-module-api.md, section internal/platform/istdate.
//
// Assumptions where the contract is silent (simplest API consistent with plan.md):
//   - ParseDate accepts only the strict "YYYY-MM-DD" form that Date.String produces and returns a
//     non-nil error for anything else, including impossible dates such as 2026-02-30.
//   - Date.AddDays accepts negative n.
//   - AddWeekdays(d, 0) returns d unchanged even when d is a Saturday or Sunday (contract text).
//   - Date.At returns the instant in UTC (contract text), checked via Location() == time.UTC.
//
// Every table is run with time.Local forced to several host zones: results must never depend on
// the server's time zone. These tests change the process-wide time.Local, so none runs in parallel.
package istdate

import (
	"testing"
	"time"
	_ "time/tzdata" // the host may lack zoneinfo; the tests force other host zones
)

// hostZones are the values forced into time.Local for every table.
var hostZones = []string{"UTC", "America/New_York", "Pacific/Kiritimati"}

// forEachHostZone runs fn once per host zone with time.Local set to that zone.
func forEachHostZone(t *testing.T, fn func(t *testing.T)) {
	t.Helper()
	for _, name := range hostZones {
		t.Run("local="+name, func(t *testing.T) {
			loc, err := time.LoadLocation(name)
			if err != nil {
				t.Fatalf("load %s: %v", name, err)
			}
			prev := time.Local
			time.Local = loc
			t.Cleanup(func() { time.Local = prev })
			fn(t)
		})
	}
}

func d(y int, m time.Month, day int) Date { return Date{Year: y, Month: m, Day: day} }

func utc(y int, m time.Month, day, h, mi int) time.Time {
	return time.Date(y, m, day, h, mi, 0, 0, time.UTC)
}

func TestIST_IsAsiaKolkata(t *testing.T) {
	if IST == nil {
		t.Fatal("IST is nil")
	}
	if IST.String() != "Asia/Kolkata" {
		t.Fatalf("IST = %q, want Asia/Kolkata", IST)
	}
	_, off := utc(2026, time.November, 13, 12, 0).In(IST).Zone()
	if off != 5*3600+30*60 {
		t.Fatalf("IST offset = %d s, want 19800 (+05:30, no daylight saving)", off)
	}
}

// US4.8: 13 Nov 2026 19:00 UTC is 14 Nov 2026 00:30 IST.
func TestDateOf_BR_SET_08_ServerZoneIndependent(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		in   time.Time
		want Date
	}{
		{"US4.8 19:00 UTC is next IST day", utc(2026, time.November, 13, 19, 0), d(2026, time.November, 14)},
		{"23:59:59 IST still the same day", utc(2026, time.November, 13, 18, 29).Add(59 * time.Second), d(2026, time.November, 13)},
		{"00:00 IST starts the next day", utc(2026, time.November, 13, 18, 30), d(2026, time.November, 14)},
		{"18:29:59.999999999 UTC is still 13 Nov IST", utc(2026, time.November, 13, 18, 29).Add(59*time.Second + 999999999*time.Nanosecond), d(2026, time.November, 13)},
		{"midnight UTC is 05:30 IST same day", utc(2026, time.November, 13, 0, 0), d(2026, time.November, 13)},
		{"year end rollover", utc(2026, time.December, 31, 18, 30), d(2027, time.January, 1)},
		{"month end rollover", utc(2026, time.February, 28, 18, 30), d(2026, time.March, 1)},
		{"leap day", utc(2028, time.February, 28, 18, 30), d(2028, time.February, 29)},
		{"instant expressed in New York", time.Date(2026, time.November, 13, 20, 0, 0, 0, ny), d(2026, time.November, 14)}, // 01:00 UTC = 06:30 IST
		{"instant expressed in IST", time.Date(2026, time.November, 13, 23, 59, 0, 0, IST), d(2026, time.November, 13)},
	}
	forEachHostZone(t, func(t *testing.T) {
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := DateOf(tt.in); got != tt.want {
					t.Fatalf("DateOf(%v) = %v, want %v", tt.in, got, tt.want)
				}
			})
		}
	})
}

// A time built in the host's local zone must be read as the instant it is, not as a wall time.
func TestDateOf_BR_SET_08_LocalWallTimeIsNotReinterpreted(t *testing.T) {
	forEachHostZone(t, func(t *testing.T) {
		in := time.Date(2026, time.November, 13, 12, 0, 0, 0, time.Local)
		want := DateOf(in.UTC())
		if got := DateOf(in); got != want {
			t.Fatalf("DateOf(local %v) = %v but DateOf(same instant in UTC) = %v", in, got, want)
		}
	})
}

// US4.9: a business date plus a wall-clock time is that date at that time in IST.
func TestDate_At_BR_SET_08(t *testing.T) {
	tests := []struct {
		name string
		date Date
		h, m int
		want time.Time
	}{
		{"US4.9 09:15 IST is 03:45 UTC", d(2026, time.November, 13), 9, 15, utc(2026, time.November, 13, 3, 45)},
		{"midnight IST is the previous UTC day", d(2026, time.November, 13), 0, 0, utc(2026, time.November, 12, 18, 30)},
		{"23:59 IST", d(2026, time.November, 13), 23, 59, utc(2026, time.November, 13, 18, 29)},
		{"05:30 IST is 00:00 UTC", d(2026, time.November, 13), 5, 30, utc(2026, time.November, 13, 0, 0)},
		{"year end", d(2026, time.December, 31), 23, 59, utc(2026, time.December, 31, 18, 29)},
		{"new year midnight IST", d(2027, time.January, 1), 0, 0, utc(2026, time.December, 31, 18, 30)},
	}
	forEachHostZone(t, func(t *testing.T) {
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got := tt.date.At(tt.h, tt.m)
				if !got.Equal(tt.want) {
					t.Fatalf("%v.At(%d, %d) = %v, want %v", tt.date, tt.h, tt.m, got, tt.want)
				}
				if got.Location() != time.UTC {
					t.Fatalf("At returned location %v, want UTC", got.Location())
				}
				if back := DateOf(got); back != tt.date {
					t.Fatalf("DateOf(At) = %v, want round trip to %v", back, tt.date)
				}
			})
		}
	})
}

func TestDate_Weekday(t *testing.T) {
	tests := []struct {
		date Date
		want time.Weekday
	}{
		{d(2026, time.November, 9), time.Monday},
		{d(2026, time.November, 12), time.Thursday},
		{d(2026, time.November, 13), time.Friday},
		{d(2026, time.November, 14), time.Saturday},
		{d(2026, time.November, 15), time.Sunday},
		{d(2026, time.December, 31), time.Thursday},
		{d(2028, time.February, 29), time.Tuesday},
	}
	forEachHostZone(t, func(t *testing.T) {
		for _, tt := range tests {
			if got := tt.date.Weekday(); got != tt.want {
				t.Errorf("%v.Weekday() = %v, want %v", tt.date, got, tt.want)
			}
		}
	})
}

func TestDate_AddDays(t *testing.T) {
	tests := []struct {
		name string
		in   Date
		n    int
		want Date
	}{
		{"zero", d(2026, time.November, 13), 0, d(2026, time.November, 13)},
		{"one", d(2026, time.November, 13), 1, d(2026, time.November, 14)},
		{"month end", d(2026, time.February, 28), 1, d(2026, time.March, 1)},
		{"year end", d(2026, time.December, 31), 1, d(2027, time.January, 1)},
		{"leap day", d(2028, time.February, 28), 1, d(2028, time.February, 29)},
		{"negative across year start", d(2026, time.January, 1), -1, d(2025, time.December, 31)},
		{"a week", d(2026, time.November, 13), 7, d(2026, time.November, 20)},
		{"a leap year", d(2027, time.March, 1), 366, d(2028, time.March, 1)},
	}
	forEachHostZone(t, func(t *testing.T) {
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := tt.in.AddDays(tt.n); got != tt.want {
					t.Fatalf("%v.AddDays(%d) = %v, want %v", tt.in, tt.n, got, tt.want)
				}
			})
		}
	})
}

func TestDate_String_And_ParseDate(t *testing.T) {
	roundTrips := []struct {
		date Date
		text string
	}{
		{d(2026, time.November, 13), "2026-11-13"},
		{d(2026, time.January, 5), "2026-01-05"},
		{d(2028, time.February, 29), "2028-02-29"},
	}
	for _, tt := range roundTrips {
		if got := tt.date.String(); got != tt.text {
			t.Errorf("%#v.String() = %q, want %q", tt.date, got, tt.text)
		}
		got, err := ParseDate(tt.text)
		if err != nil || got != tt.date {
			t.Errorf("ParseDate(%q) = %v, %v; want %v, nil", tt.text, got, err, tt.date)
		}
	}

	for _, bad := range []string{"", "abc", "2026-13-01", "2026-02-30", "2027-02-29", "2026/11/13", "13-11-2026", " 2026-11-13", "2026-11-13 ", "2026-11-13T00:00:00Z", "2026-11"} {
		if got, err := ParseDate(bad); err == nil {
			t.Errorf("ParseDate(%q) = %v, nil; want an error", bad, got)
		}
	}
}

// US4.10 (BR-SET-08): adding N weekdays skips Saturdays and Sundays and nothing else.
func TestAddWeekdays_BR_SET_08_Table(t *testing.T) {
	fri := d(2026, time.November, 13)
	sat := d(2026, time.November, 14)
	sun := d(2026, time.November, 15)
	tests := []struct {
		name string
		in   Date
		n    int
		want Date
	}{
		// Friday 13 Nov 2026, N = 0..10
		{"Fri +0", fri, 0, d(2026, time.November, 13)},
		{"Fri +1 is Mon", fri, 1, d(2026, time.November, 16)},
		{"Fri +2 is Tue", fri, 2, d(2026, time.November, 17)},
		{"Fri +3", fri, 3, d(2026, time.November, 18)},
		{"Fri +4", fri, 4, d(2026, time.November, 19)},
		{"Fri +5 is next Fri", fri, 5, d(2026, time.November, 20)},
		{"Fri +6", fri, 6, d(2026, time.November, 23)},
		{"Fri +7", fri, 7, d(2026, time.November, 24)},
		{"Fri +8", fri, 8, d(2026, time.November, 25)},
		{"Fri +9", fri, 9, d(2026, time.November, 26)},
		{"Fri +10 is two Fridays on", fri, 10, d(2026, time.November, 27)},
		// weekend starts (D2)
		{"Sat +0 unchanged", sat, 0, sat},
		{"Sun +0 unchanged", sun, 0, sun},
		{"Sat +1 is Mon", sat, 1, d(2026, time.November, 16)},
		{"Sat +2 is Tue", sat, 2, d(2026, time.November, 17)},
		{"Sun +1 is Mon", sun, 1, d(2026, time.November, 16)},
		{"Sun +2 is Tue", sun, 2, d(2026, time.November, 17)},
		{"Sat +5 is Fri", sat, 5, d(2026, time.November, 20)},
		{"Sun +5 is Fri", sun, 5, d(2026, time.November, 20)},
		// calendar edges
		{"year end Thu +1 is New Year Fri, not skipped", d(2026, time.December, 31), 1, d(2027, time.January, 1)},
		{"year end Thu +2 is Mon", d(2026, time.December, 31), 2, d(2027, time.January, 4)},
		{"month end Fri +2", d(2026, time.February, 27), 2, d(2026, time.March, 3)},
		{"leap day crossed", d(2028, time.February, 28), 2, d(2028, time.March, 1)},
		{"Mon +4 is Fri", d(2026, time.November, 9), 4, d(2026, time.November, 13)},
		{"Mon +5 is next Mon", d(2026, time.November, 9), 5, d(2026, time.November, 16)},
		{"52 weeks of weekdays", fri, 260, d(2027, time.November, 12)},
	}
	forEachHostZone(t, func(t *testing.T) {
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got, err := AddWeekdays(tt.in, tt.n)
				if err != nil {
					t.Fatalf("AddWeekdays(%v, %d) error: %v", tt.in, tt.n, err)
				}
				if got != tt.want {
					t.Fatalf("AddWeekdays(%v, %d) = %v (%v), want %v (%v)", tt.in, tt.n, got, got.Weekday(), tt.want, tt.want.Weekday())
				}
			})
		}
	})
}

func TestAddWeekdays_BR_SET_08_NegativeIsError(t *testing.T) {
	for _, n := range []int{-1, -2, -100} {
		if got, err := AddWeekdays(d(2026, time.November, 13), n); err == nil {
			t.Errorf("AddWeekdays(.., %d) = %v, nil; want an error (programmer error, not a business case)", n, got)
		}
	}
}

// GS-04: listing on Friday is due Tuesday 23:59 IST; Thursday is due Monday 23:59 IST.
func TestDueDate_GS_04_FridayListingDueTuesday(t *testing.T) {
	forEachHostZone(t, func(t *testing.T) {
		listing := d(2026, time.November, 13) // Friday
		if listing.Weekday() != time.Friday {
			t.Fatalf("fixture: %v is %v, not Friday", listing, listing.Weekday())
		}
		got := PaymentDue(listing)
		want := utc(2026, time.November, 17, 18, 29) // Tue 17 Nov 2026 23:59 IST
		if !got.Equal(want) {
			t.Fatalf("PaymentDue(Fri 13 Nov 2026) = %v, want %v (Tue 17 Nov 2026 23:59 IST)", got, want)
		}
		ist := got.In(IST)
		if ist.Weekday() != time.Tuesday || ist.Hour() != 23 || ist.Minute() != 59 || ist.Second() != 0 || ist.Nanosecond() != 0 {
			t.Fatalf("due in IST = %v, want Tuesday 23:59:00", ist)
		}
	})
}

func TestDueDate_GS_04_ThursdayListingDueMonday(t *testing.T) {
	forEachHostZone(t, func(t *testing.T) {
		listing := d(2026, time.November, 12) // Thursday
		if listing.Weekday() != time.Thursday {
			t.Fatalf("fixture: %v is %v, not Thursday", listing, listing.Weekday())
		}
		got := PaymentDue(listing)
		want := utc(2026, time.November, 16, 18, 29) // Mon 16 Nov 2026 23:59 IST
		if !got.Equal(want) {
			t.Fatalf("PaymentDue(Thu 12 Nov 2026) = %v, want %v (Mon 16 Nov 2026 23:59 IST)", got, want)
		}
		if ist := got.In(IST); ist.Weekday() != time.Monday || ist.Hour() != 23 || ist.Minute() != 59 {
			t.Fatalf("due in IST = %v, want Monday 23:59", ist)
		}
	})
}

// US4.1-4.7 (BR-SET-08) as one table.
func TestPaymentDue_BR_SET_08_Table(t *testing.T) {
	tests := []struct {
		name    string
		listing Date
		want    time.Time // the instant of 23:59 IST on the due date, in UTC
	}{
		{"US4.3 Mon to Wed", d(2026, time.November, 9), utc(2026, time.November, 11, 18, 29)},
		{"US4.3 Tue to Thu", d(2026, time.November, 10), utc(2026, time.November, 12, 18, 29)},
		{"US4.3 Wed to Fri", d(2026, time.November, 11), utc(2026, time.November, 13, 18, 29)},
		{"US4.2 Thu to Mon", d(2026, time.November, 12), utc(2026, time.November, 16, 18, 29)},
		{"US4.1 Fri to Tue", d(2026, time.November, 13), utc(2026, time.November, 17, 18, 29)},
		{"US4.4 year end Thu 31 Dec 2026 to Mon 4 Jan 2027 (1 Jan not skipped)", d(2026, time.December, 31), utc(2027, time.January, 4, 18, 29)},
		{"US4.5 exchange holiday on Thursday is not skipped (Wed to Fri)", d(2026, time.November, 11), utc(2026, time.November, 13, 18, 29)},
		{"US4.6 month end Fri 27 Feb 2026 to Tue 3 Mar 2026", d(2026, time.February, 27), utc(2026, time.March, 3, 18, 29)},
		{"US4.7 Saturday listing to Tue", d(2026, time.November, 14), utc(2026, time.November, 17, 18, 29)},
		{"US4.7 Sunday listing to Tue", d(2026, time.November, 15), utc(2026, time.November, 17, 18, 29)},
		{"leap day crossed Mon 28 Feb 2028 to Wed 1 Mar 2028", d(2028, time.February, 28), utc(2028, time.March, 1, 18, 29)},
	}
	forEachHostZone(t, func(t *testing.T) {
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got := PaymentDue(tt.listing)
				if !got.Equal(tt.want) {
					t.Fatalf("PaymentDue(%v %v) = %v, want %v", tt.listing, tt.listing.Weekday(), got, tt.want)
				}
			})
		}
	})
}

// The exchange holiday case spelled out: nothing but Saturday and Sunday is ever skipped, so a
// Wednesday listing is due Friday even though Thursday would be a holiday (owner decision D1).
func TestPaymentDue_BR_SET_08_HolidayNotSkipped(t *testing.T) {
	wed := d(2026, time.November, 11)
	thuHoliday := d(2026, time.November, 12)
	if wed.Weekday() != time.Wednesday || thuHoliday.Weekday() != time.Thursday {
		t.Fatal("fixture weekdays are wrong")
	}
	got := DateOf(PaymentDue(wed))
	if want := d(2026, time.November, 13); got != want {
		t.Fatalf("due date = %v, want %v (Friday)", got, want)
	}
}
