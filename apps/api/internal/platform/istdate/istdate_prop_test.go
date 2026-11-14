// Property tests (pgregory.net/rapid) for AddWeekdays and PaymentDue (feature 002, US4, FR-033).
// Rules: BR-SET-08, GS-04. Contract: contracts/go-module-api.md, section internal/platform/istdate.
//
// The table tests in istdate_test.go pin the exact calendar cases; these properties pin the
// invariants that must hold for every date from 2000 to about 2055 and every N.
package istdate

import (
	"testing"
	"time"

	"pgregory.net/rapid"
)

var epoch = Date{Year: 2000, Month: time.January, Day: 1}

// genDate draws a business date between 2000-01-01 and roughly 2055.
func genDate(label string) *rapid.Generator[Date] {
	return rapid.Custom(func(t *rapid.T) Date {
		return epoch.AddDays(rapid.IntRange(0, 20000).Draw(t, label))
	})
}

// ordinal is a date's day number, used only to compare and subtract dates in tests.
func ordinal(d Date) int {
	return int(time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

func isWeekday(d Date) bool {
	w := d.Weekday()
	return w != time.Saturday && w != time.Sunday
}

func mustAdd(t *rapid.T, d Date, n int) Date {
	t.Helper()
	got, err := AddWeekdays(d, n)
	if err != nil {
		t.Fatalf("AddWeekdays(%v, %d) error: %v", d, n, err)
	}
	return got
}

func TestAddWeekdays_BR_SET_08_Properties(t *testing.T) {
	t.Run("n=0 returns d unchanged", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			d := genDate("d").Draw(t, "d")
			if got := mustAdd(t, d, 0); got != d {
				t.Fatalf("AddWeekdays(%v, 0) = %v, want unchanged", d, got)
			}
		})
	})

	t.Run("result is a weekday when n > 0", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			d := genDate("d").Draw(t, "d")
			n := rapid.IntRange(1, 400).Draw(t, "n")
			if got := mustAdd(t, d, n); !isWeekday(got) {
				t.Fatalf("AddWeekdays(%v, %d) = %v which is a %v", d, n, got, got.Weekday())
			}
		})
	})

	t.Run("exactly n weekdays in (d, result]", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			d := genDate("d").Draw(t, "d")
			n := rapid.IntRange(0, 400).Draw(t, "n")
			got := mustAdd(t, d, n)
			count := 0
			for cur := d.AddDays(1); ordinal(cur) <= ordinal(got); cur = cur.AddDays(1) {
				if isWeekday(cur) {
					count++
				}
			}
			if count != n {
				t.Fatalf("AddWeekdays(%v, %d) = %v: %d weekdays in (d, result], want %d", d, n, got, count, n)
			}
		})
	})

	t.Run("composition: adding a then b equals adding a+b", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			d := genDate("d").Draw(t, "d")
			a := rapid.IntRange(0, 200).Draw(t, "a")
			b := rapid.IntRange(0, 200).Draw(t, "b")
			step := mustAdd(t, mustAdd(t, d, a), b)
			direct := mustAdd(t, d, a+b)
			if step != direct {
				t.Fatalf("AddWeekdays(AddWeekdays(%v, %d), %d) = %v but AddWeekdays(%v, %d) = %v", d, a, b, step, d, a+b, direct)
			}
		})
	})

	t.Run("span bound: n <= days <= n + 2*(n/5+1)", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			d := genDate("d").Draw(t, "d")
			n := rapid.IntRange(0, 400).Draw(t, "n")
			days := ordinal(mustAdd(t, d, n)) - ordinal(d)
			if days < n || days > n+2*(n/5+1) {
				t.Fatalf("AddWeekdays(%v, %d) spans %d days, want within [%d, %d]", d, n, days, n, n+2*(n/5+1))
			}
		})
	})

	t.Run("monotonic in n", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			d := genDate("d").Draw(t, "d")
			n1 := rapid.IntRange(0, 400).Draw(t, "n1")
			n2 := rapid.IntRange(n1, 400).Draw(t, "n2")
			r1, r2 := mustAdd(t, d, n1), mustAdd(t, d, n2)
			if ordinal(r1) > ordinal(r2) {
				t.Fatalf("n1=%d gives %v but larger n2=%d gives earlier %v (d=%v)", n1, r1, n2, r2, d)
			}
			if n2 > n1 && r1 == r2 {
				t.Fatalf("n1=%d and n2=%d give the same date %v (d=%v)", n1, n2, r1, d)
			}
		})
	})

	t.Run("monotonic in d", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			d1 := genDate("d1").Draw(t, "d1")
			d2 := d1.AddDays(rapid.IntRange(0, 400).Draw(t, "gap"))
			n := rapid.IntRange(0, 400).Draw(t, "n")
			r1, r2 := mustAdd(t, d1, n), mustAdd(t, d2, n)
			if ordinal(r1) > ordinal(r2) {
				t.Fatalf("d1=%v gives %v but later d2=%v gives earlier %v (n=%d)", d1, r1, d2, r2, n)
			}
		})
	})

	t.Run("negative n is always an error", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			d := genDate("d").Draw(t, "d")
			n := rapid.IntRange(-400, -1).Draw(t, "n")
			if got, err := AddWeekdays(d, n); err == nil {
				t.Fatalf("AddWeekdays(%v, %d) = %v, nil; want an error", d, n, got)
			}
		})
	})
}

// BR-SET-08 / GS-04: due = listing + 2 weekdays at 23:59 IST, never a Saturday or Sunday.
func TestPaymentDue_BR_SET_08_Properties(t *testing.T) {
	wantWeekday := map[time.Weekday]time.Weekday{
		time.Monday:    time.Wednesday,
		time.Tuesday:   time.Thursday,
		time.Wednesday: time.Friday,
		time.Thursday:  time.Monday,
		time.Friday:    time.Tuesday,
		time.Saturday:  time.Tuesday,
		time.Sunday:    time.Tuesday,
	}
	rapid.Check(t, func(t *rapid.T) {
		listing := genDate("listing").Draw(t, "listing")
		due := PaymentDue(listing)
		ist := due.In(IST)

		if ist.Hour() != 23 || ist.Minute() != 59 || ist.Second() != 0 || ist.Nanosecond() != 0 {
			t.Fatalf("PaymentDue(%v) = %v, want exactly 23:59:00 IST", listing, ist)
		}
		if w := ist.Weekday(); w == time.Saturday || w == time.Sunday {
			t.Fatalf("PaymentDue(%v) falls on %v", listing, w)
		}
		if got, want := ist.Weekday(), wantWeekday[listing.Weekday()]; got != want {
			t.Fatalf("PaymentDue(%v %v) falls on %v, want %v", listing, listing.Weekday(), got, want)
		}
		if want := mustAdd(t, listing, 2); DateOf(due) != want {
			t.Fatalf("PaymentDue(%v) is on %v, want AddWeekdays(listing, 2) = %v", listing, DateOf(due), want)
		}
		if gap := due.Sub(listing.At(23, 59)); gap < 48*time.Hour || gap > 96*time.Hour {
			t.Fatalf("PaymentDue(%v) is %v after the listing day's 23:59 IST, want between 48h and 96h", listing, gap)
		}
	})
}

// DateOf and At are inverse for every wall-clock minute of every date, whatever the host zone.
func TestDateAt_BR_SET_08_RoundTripProperties(t *testing.T) {
	forEachHostZone(t, func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			d := genDate("d").Draw(t, "d")
			h := rapid.IntRange(0, 23).Draw(t, "h")
			m := rapid.IntRange(0, 59).Draw(t, "m")
			at := d.At(h, m)
			if got := DateOf(at); got != d {
				t.Fatalf("DateOf(%v.At(%d, %d) = %v) = %v", d, h, m, at, got)
			}
			if ist := at.In(IST); ist.Hour() != h || ist.Minute() != m || ist.Second() != 0 {
				t.Fatalf("%v.At(%d, %d) = %v, which reads %v in IST", d, h, m, at, ist)
			}
			// one nanosecond earlier is never later in the day
			if prev := DateOf(at.Add(-time.Nanosecond)); ordinal(prev) > ordinal(d) {
				t.Fatalf("DateOf is not monotonic around %v", at)
			}
		})
	})
}
