package istdate

import "time"

// WallClock is an IST wall-clock time of day, 00:00 to 23:59.
type WallClock struct{ Hour, Minute int }

func (w WallClock) minutes() int { return w.Hour*60 + w.Minute }

// QuietHours is a daily window [Start, End) in IST. It wraps midnight when Start is after End.
// A window with Start == End is empty (no quiet hours).
type QuietHours struct{ Start, End WallClock }

// DefaultQuietHours is 21:30 to 08:00 IST (BR-TASK-03).
var DefaultQuietHours = QuietHours{Start: WallClock{Hour: 21, Minute: 30}, End: WallClock{Hour: 8, Minute: 0}}

// Contains reports whether t falls inside the window; Start is inclusive, End exclusive.
func (q QuietHours) Contains(t time.Time) bool {
	ist := t.In(IST)
	m := ist.Hour()*60 + ist.Minute()
	s, e := q.Start.minutes(), q.End.minutes()
	switch {
	case s < e:
		return s <= m && m < e
	case s > e:
		return m >= s || m < e
	default:
		return false
	}
}

// Defer returns t when it is outside the window, otherwise the next instant the window ends.
func (q QuietHours) Defer(t time.Time) time.Time {
	if !q.Contains(t) {
		return t
	}
	day := DateOf(t)
	end := day.At(q.End.Hour, q.End.Minute)
	if !end.After(t) {
		end = day.AddDays(1).At(q.End.Hour, q.End.Minute)
	}
	return end
}

// NextDailyAt returns the first instant strictly after now that is the wall-clock time at in IST.
func NextDailyAt(now time.Time, at WallClock) time.Time {
	day := DateOf(now)
	next := day.At(at.Hour, at.Minute)
	if !next.After(now) {
		next = day.AddDays(1).At(at.Hour, at.Minute)
	}
	return next
}
