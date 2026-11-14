// Unit and property tests for quiet hours and NextDailyAt (feature 002, US8.1-8.4, FR-031).
// Rule: BR-TASK-03 (reminders respect quiet hours; default 21:30 to 08:00 IST).
// Contract: contracts/go-module-api.md, section internal/platform/istdate.
//
// Assumptions where the contract is silent:
//   - A window with Start == End is not specified and is not tested.
//   - Contains/Defer compare on the exact instant: 21:29:59.999999999 is outside, 21:30:00 is
//     inside (Start inclusive), 07:59:59.999999999 is inside, 08:00:00 is outside (End exclusive).
//   - Defer returns the End instant at exactly HH:MM:00.000 IST, in any location (compared with Equal).
package istdate

import (
	"testing"
	"time"

	"pgregory.net/rapid"
)

var fri = d(2026, time.November, 13) // Friday; Saturday 14 Nov follows

func TestDefaultQuietHours_BR_TASK_03(t *testing.T) {
	want := QuietHours{Start: WallClock{Hour: 21, Minute: 30}, End: WallClock{Hour: 8, Minute: 0}}
	if DefaultQuietHours != want {
		t.Fatalf("DefaultQuietHours = %+v, want 21:30 to 08:00", DefaultQuietHours)
	}
}

// US8.1-8.4 and the boundaries named in the task, with the default window.
func TestQuietHours_BR_TASK_03_DefaultWindow(t *testing.T) {
	sat := fri.AddDays(1)
	tests := []struct {
		name         string
		at           time.Time
		wantContains bool
		wantDefer    time.Time
	}{
		{"US8.1 21:29 is outside and relayed", fri.At(21, 29), false, fri.At(21, 29)},
		{"21:29:59.999999999 is outside", fri.At(21, 30).Add(-time.Nanosecond), false, fri.At(21, 30).Add(-time.Nanosecond)},
		{"US8.2 21:30 Friday is inside (start inclusive), deferred to 08:00 Saturday", fri.At(21, 30), true, sat.At(8, 0)},
		{"23:00 is inside, deferred to 08:00 the next day", fri.At(23, 0), true, sat.At(8, 0)},
		{"23:59:59 is inside, deferred to 08:00 the next day", fri.At(23, 59).Add(59 * time.Second), true, sat.At(8, 0)},
		{"midnight is inside, deferred to 08:00 the same day", sat.At(0, 0), true, sat.At(8, 0)},
		{"US8.3 02:00 is inside, deferred to 08:00 the same day", sat.At(2, 0), true, sat.At(8, 0)},
		{"07:59 is inside", sat.At(7, 59), true, sat.At(8, 0)},
		{"07:59:59.999999999 is inside", sat.At(8, 0).Add(-time.Nanosecond), true, sat.At(8, 0)},
		{"US8.4 08:00 is outside (end exclusive) and relayed", sat.At(8, 0), false, sat.At(8, 0)},
		{"08:00:00.000000001 is outside", sat.At(8, 0).Add(time.Nanosecond), false, sat.At(8, 0).Add(time.Nanosecond)},
		{"noon is outside", fri.At(12, 0), false, fri.At(12, 0)},
		{"year end 22:00 deferred to 08:00 on New Year", d(2026, time.December, 31).At(22, 0), true, d(2027, time.January, 1).At(8, 0)},
		{"month end 22:00 deferred into the next month", d(2026, time.February, 28).At(22, 0), true, d(2026, time.March, 1).At(8, 0)},
	}
	forEachHostZone(t, func(t *testing.T) {
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := DefaultQuietHours.Contains(tt.at); got != tt.wantContains {
					t.Errorf("Contains(%v) = %v, want %v", tt.at.In(IST), got, tt.wantContains)
				}
				if got := DefaultQuietHours.Defer(tt.at); !got.Equal(tt.wantDefer) {
					t.Errorf("Defer(%v) = %v, want %v", tt.at.In(IST), got.In(IST), tt.wantDefer.In(IST))
				}
			})
		}
	})
}

// The window is defined in IST, whatever zone the instant is expressed in (21:30 IST = 16:00 UTC).
func TestQuietHours_BR_TASK_03_UsesIST(t *testing.T) {
	tests := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"15:59:59 UTC is 21:29:59 IST", utc(2026, time.November, 13, 16, 0).Add(-time.Second), false},
		{"16:00 UTC is 21:30 IST", utc(2026, time.November, 13, 16, 0), true},
		{"02:29 UTC is 07:59 IST", utc(2026, time.November, 14, 2, 29), true},
		{"02:30 UTC is 08:00 IST", utc(2026, time.November, 14, 2, 30), false},
		{"21:30 UTC is 03:00 IST next day, inside", utc(2026, time.November, 13, 21, 30), true},
	}
	forEachHostZone(t, func(t *testing.T) {
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := DefaultQuietHours.Contains(tt.at); got != tt.want {
					t.Fatalf("Contains(%v) = %v, want %v", tt.at, got, tt.want)
				}
			})
		}
	})
}

func TestQuietHours_BR_TASK_03_NonWrappingWindow(t *testing.T) {
	q := QuietHours{Start: WallClock{Hour: 13, Minute: 0}, End: WallClock{Hour: 15, Minute: 0}}
	tests := []struct {
		name         string
		at           time.Time
		wantContains bool
		wantDefer    time.Time
	}{
		{"12:59 outside", fri.At(12, 59), false, fri.At(12, 59)},
		{"13:00 inside (start inclusive)", fri.At(13, 0), true, fri.At(15, 0)},
		{"14:59 inside", fri.At(14, 59), true, fri.At(15, 0)},
		{"15:00 outside (end exclusive)", fri.At(15, 0), false, fri.At(15, 0)},
		{"midnight outside", fri.At(0, 0), false, fri.At(0, 0)},
		{"23:59 outside, not deferred to the next day", fri.At(23, 59), false, fri.At(23, 59)},
	}
	forEachHostZone(t, func(t *testing.T) {
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := q.Contains(tt.at); got != tt.wantContains {
					t.Errorf("Contains(%v) = %v, want %v", tt.at.In(IST), got, tt.wantContains)
				}
				if got := q.Defer(tt.at); !got.Equal(tt.wantDefer) {
					t.Errorf("Defer(%v) = %v, want %v", tt.at.In(IST), got.In(IST), tt.wantDefer.In(IST))
				}
			})
		}
	})
}

func TestQuietHours_BR_TASK_03_CustomWrappingWindow(t *testing.T) {
	q := QuietHours{Start: WallClock{Hour: 22, Minute: 0}, End: WallClock{Hour: 6, Minute: 45}}
	sat := fri.AddDays(1)
	if q.Contains(fri.At(21, 59)) || !q.Contains(fri.At(22, 0)) || !q.Contains(sat.At(6, 44)) || q.Contains(sat.At(6, 45)) {
		t.Fatal("custom wrapping window 22:00-06:45 has wrong boundaries")
	}
	if got, want := q.Defer(fri.At(23, 30)), sat.At(6, 45); !got.Equal(want) {
		t.Fatalf("Defer(23:30) = %v, want %v", got.In(IST), want.In(IST))
	}
	if got, want := q.Defer(sat.At(1, 0)), sat.At(6, 45); !got.Equal(want) {
		t.Fatalf("Defer(01:00) = %v, want %v", got.In(IST), want.In(IST))
	}
}

// ---- properties -----------------------------------------------------------------------------

func genWallClock(label string) *rapid.Generator[WallClock] {
	return rapid.Custom(func(t *rapid.T) WallClock {
		return WallClock{
			Hour:   rapid.IntRange(0, 23).Draw(t, label+".hour"),
			Minute: rapid.IntRange(0, 59).Draw(t, label+".minute"),
		}
	})
}

// genWindow draws any window with Start != End, wrapping or not.
func genWindow() *rapid.Generator[QuietHours] {
	return rapid.Custom(func(t *rapid.T) QuietHours {
		start := genWallClock("start").Draw(t, "start")
		end := genWallClock("end").Filter(func(w WallClock) bool { return w != start }).Draw(t, "end")
		return QuietHours{Start: start, End: end}
	})
}

// genInstant draws an instant from 2026 to 2028 with second and sub-second resolution.
func genInstant(label string) *rapid.Generator[time.Time] {
	return rapid.Custom(func(t *rapid.T) time.Time {
		secs := rapid.Int64Range(0, 3*365*86400).Draw(t, label+".secs")
		nanos := rapid.SampledFrom([]int64{0, 1, 999999999, 500000000}).Draw(t, label+".nanos")
		return utc(2026, time.January, 1, 0, 0).Add(time.Duration(secs)*time.Second + time.Duration(nanos))
	})
}

// containsOracle restates the contract: [Start, End) on IST wall-clock minutes, wrapping when Start > End.
func containsOracle(q QuietHours, at time.Time) bool {
	ist := at.In(IST)
	m := ist.Hour()*60 + ist.Minute()
	s, e := q.Start.Hour*60+q.Start.Minute, q.End.Hour*60+q.End.Minute
	if s < e {
		return s <= m && m < e
	}
	return m >= s || m < e
}

func TestQuietHours_BR_TASK_03_Properties(t *testing.T) {
	t.Run("Contains matches the [Start, End) definition", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			q, at := genWindow().Draw(t, "q"), genInstant("at").Draw(t, "at")
			if got, want := q.Contains(at), containsOracle(q, at); got != want {
				t.Fatalf("%+v.Contains(%v) = %v, want %v", q, at.In(IST), got, want)
			}
		})
	})

	t.Run("Defer outside the window is the identity", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			q, at := genWindow().Draw(t, "q"), genInstant("at").Draw(t, "at")
			if q.Contains(at) {
				t.Skip("inside the window")
			}
			if got := q.Defer(at); !got.Equal(at) {
				t.Fatalf("%+v.Defer(%v) = %v, want unchanged", q, at.In(IST), got.In(IST))
			}
		})
	})

	t.Run("Defer is minimal: the first instant at or after t that is outside the window", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			q, at := genWindow().Draw(t, "q"), genInstant("at").Draw(t, "at")
			got := q.Defer(at)
			if got.Before(at) {
				t.Fatalf("%+v.Defer(%v) = %v moved backwards", q, at.In(IST), got.In(IST))
			}
			if q.Contains(got) {
				t.Fatalf("%+v.Defer(%v) = %v is still inside the window", q, at.In(IST), got.In(IST))
			}
			if q.Contains(at) {
				// minimal: one nanosecond earlier is still inside, and the result is exactly End:00 IST
				if !q.Contains(got.Add(-time.Nanosecond)) {
					t.Fatalf("%+v.Defer(%v) = %v is not minimal (the instant before it is outside the window)", q, at.In(IST), got.In(IST))
				}
				ist := got.In(IST)
				if ist.Hour() != q.End.Hour || ist.Minute() != q.End.Minute || ist.Second() != 0 || ist.Nanosecond() != 0 {
					t.Fatalf("%+v.Defer(%v) = %v, want exactly %02d:%02d:00 IST", q, at.In(IST), ist, q.End.Hour, q.End.Minute)
				}
				if got.Sub(at) > 24*time.Hour {
					t.Fatalf("%+v.Defer(%v) = %v is more than 24h away", q, at.In(IST), got.In(IST))
				}
			}
		})
	})

	t.Run("Defer is idempotent", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			q, at := genWindow().Draw(t, "q"), genInstant("at").Draw(t, "at")
			once := q.Defer(at)
			if twice := q.Defer(once); !twice.Equal(once) {
				t.Fatalf("%+v.Defer not idempotent at %v: %v then %v", q, at.In(IST), once.In(IST), twice.In(IST))
			}
		})
	})

	t.Run("Defer is monotonic", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			q, a := genWindow().Draw(t, "q"), genInstant("a").Draw(t, "a")
			b := a.Add(time.Duration(rapid.Int64Range(0, 3*86400).Draw(t, "gap")) * time.Second)
			if q.Defer(b).Before(q.Defer(a)) {
				t.Fatalf("%+v: Defer(%v) = %v is earlier than Defer(%v) = %v", q, b.In(IST), q.Defer(b).In(IST), a.In(IST), q.Defer(a).In(IST))
			}
		})
	})
}

// ---- NextDailyAt ----------------------------------------------------------------------------

func TestNextDailyAt_Table(t *testing.T) {
	retention := WallClock{Hour: 3, Minute: 0} // scheduler.retention runs daily at 03:00 IST
	sat := fri.AddDays(1)
	tests := []struct {
		name string
		now  time.Time
		at   WallClock
		want time.Time
	}{
		{"before today's time: later today", fri.At(2, 0), retention, fri.At(3, 0)},
		{"one nanosecond before: later today", fri.At(3, 0).Add(-time.Nanosecond), retention, fri.At(3, 0)},
		{"exactly at the time: strictly after, so tomorrow", fri.At(3, 0), retention, sat.At(3, 0)},
		{"one nanosecond after: tomorrow", fri.At(3, 0).Add(time.Nanosecond), retention, sat.At(3, 0)},
		{"after today's time: tomorrow", fri.At(23, 0), retention, sat.At(3, 0)},
		{"a UTC midnight instant is 05:30 IST, already past 03:00: tomorrow", utc(2026, time.November, 13, 0, 0), retention, sat.At(3, 0)},
		{"21:30 UTC is 03:00 IST next day exactly: strictly after, so the day after", utc(2026, time.November, 13, 21, 30), retention, d(2026, time.November, 15).At(3, 0)},
		{"midnight IST", fri.At(0, 0), retention, fri.At(3, 0)},
		{"year end rolls into the new year", d(2026, time.December, 31).At(4, 0), retention, d(2027, time.January, 1).At(3, 0)},
		{"00:00 target from 23:59", fri.At(23, 59), WallClock{0, 0}, sat.At(0, 0)},
		{"23:59 target from 23:58", fri.At(23, 58), WallClock{23, 59}, fri.At(23, 59)},
		{"minute precision target", fri.At(9, 14), WallClock{9, 15}, fri.At(9, 15)},
	}
	forEachHostZone(t, func(t *testing.T) {
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got := NextDailyAt(tt.now, tt.at)
				if !got.Equal(tt.want) {
					t.Fatalf("NextDailyAt(%v, %+v) = %v, want %v", tt.now.In(IST), tt.at, got.In(IST), tt.want.In(IST))
				}
			})
		}
	})
}

func TestNextDailyAt_Properties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		now, at := genInstant("now").Draw(t, "now"), genWallClock("at").Draw(t, "at")
		got := NextDailyAt(now, at)
		ist := got.In(IST)
		if !got.After(now) {
			t.Fatalf("NextDailyAt(%v, %+v) = %v is not after now", now.In(IST), at, ist)
		}
		if ist.Hour() != at.Hour || ist.Minute() != at.Minute || ist.Second() != 0 || ist.Nanosecond() != 0 {
			t.Fatalf("NextDailyAt(%v, %+v) = %v, want exactly %02d:%02d:00 IST", now.In(IST), at, ist, at.Hour, at.Minute)
		}
		// the first such instant: a day earlier would not be after now
		if got.Add(-24 * time.Hour).After(now) {
			t.Fatalf("NextDailyAt(%v, %+v) = %v is not the first such instant", now.In(IST), at, ist)
		}
		// feeding the result back gives the following day
		if again := NextDailyAt(got, at); !again.Equal(got.Add(24 * time.Hour)) {
			t.Fatalf("NextDailyAt(result) = %v, want exactly 24h later than %v", again.In(IST), ist)
		}
	})
}
