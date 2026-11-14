// Property test: ApplyThrottle against a reference model that keeps every failure (T022; plan P-07).
package auth_test

import (
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
)

// model is the specification written the long way: it remembers every failure since the last lock.
type model struct {
	failures []time.Time
	locked   time.Time // zero = none
}

func (m *model) fail(now time.Time, p auth.ThrottlePolicy) (started bool) {
	if now.Before(m.locked) {
		return false // attempts during the lock change nothing
	}
	if !m.locked.IsZero() { // the lock has ended: the counter is cleared
		m.failures, m.locked = nil, time.Time{}
	}
	m.failures = append(m.failures, now)
	inWindow := 0
	for _, f := range m.failures {
		if now.Sub(f) < p.Window {
			inWindow++
		}
	}
	if inWindow >= p.MaxFailures {
		m.locked, m.failures = now.Add(p.Lock), nil
		return true
	}
	return false
}

func TestThrottleApply_PropertyMatchesReferenceModel(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		p := auth.ThrottlePolicy{
			MaxFailures: rapid.IntRange(2, 5).Draw(t, "max"),
			Window:      time.Duration(rapid.IntRange(1, 20).Draw(t, "windowMin")) * time.Minute,
			Lock:        time.Duration(rapid.IntRange(1, 20).Draw(t, "lockMin")) * time.Minute,
		}
		var (
			s   auth.ThrottleState
			m   model
			now = t0
		)
		for i := range rapid.IntRange(1, 80).Draw(t, "events") {
			// Gaps are drawn in seconds so that exact-boundary hits (window, lock end) are common.
			now = now.Add(time.Duration(rapid.IntRange(0, 25*60).Draw(t, "gapSeconds")) * time.Second)
			next, started := auth.ApplyThrottle(s, now, p)
			wantStarted := m.fail(now, p)
			if started != wantStarted {
				t.Fatalf("event %d at %v: startedLock = %v, model says %v\nstate %+v", i, now.Sub(t0), started, wantStarted, s)
			}
			if got, want := lockedUntil(next), m.locked; !got.Equal(want) && !(want.IsZero() && !next.Locked(now)) {
				t.Fatalf("event %d at %v: LockedUntil = %v, model %v", i, now.Sub(t0), got, want)
			}
			if next.Locked(now) != now.Before(m.locked) {
				t.Fatalf("event %d: Locked(now) = %v, model %v", i, next.Locked(now), now.Before(m.locked))
			}
			if len(next.Failures) > p.MaxFailures {
				t.Fatalf("event %d: %d failures kept, max %d", i, len(next.Failures), p.MaxFailures)
			}
			// Probe a few later instants: the lock must end exactly where the model says.
			if !m.locked.IsZero() {
				for _, probe := range []time.Time{m.locked.Add(-time.Nanosecond), m.locked, m.locked.Add(time.Second)} {
					if next.Locked(probe) != probe.Before(m.locked) {
						t.Fatalf("event %d: Locked(%v) = %v, model lock ends %v", i, probe.Sub(t0), next.Locked(probe), m.locked.Sub(t0))
					}
				}
			}
			s = next
		}
	})
}

func TestThrottleApply_PropertyNeverShortensAnActiveLock(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var s auth.ThrottleState
		now := t0
		for range rapid.IntRange(1, 60).Draw(t, "events") {
			now = now.Add(time.Duration(rapid.IntRange(0, 600).Draw(t, "gapSeconds")) * time.Second)
			before := lockedUntil(s)
			next, _ := auth.ApplyThrottle(s, now, signIn)
			if now.Before(before) && !lockedUntil(next).Equal(before) {
				t.Fatalf("lock until %v was altered to %v by a failure at %v", before, lockedUntil(next), now)
			}
			s = next
		}
	})
}
