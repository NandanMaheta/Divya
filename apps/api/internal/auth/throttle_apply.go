package auth

import (
	"slices"
	"time"
)

// ThrottlePolicy describes a lockout: MaxFailures counted failures within Window lock for Lock.
type ThrottlePolicy struct {
	MaxFailures  int
	Window, Lock time.Duration
}

// ThrottleState is the persisted lockout state for one subject.
type ThrottleState struct {
	Failures    []time.Time // failures still inside the window, oldest first; at most MaxFailures
	LockedUntil *time.Time  // nil = never locked or counter cleared
}

// Locked reports whether the subject is locked at now (locked iff now < LockedUntil).
func (s ThrottleState) Locked(now time.Time) bool {
	return s.LockedUntil != nil && now.Before(*s.LockedUntil)
}

// ApplyThrottle records one counted failure at now (D-08). It is pure: s is never mutated and the
// result shares no memory with it. The bool reports that this failure started a lock.
//
//   - During a lock nothing changes (no extension).
//   - A lock that has ended clears the counter before the new failure is recorded.
//   - Otherwise failures older than Window are dropped, now is appended, at most MaxFailures are
//     kept, and reaching MaxFailures sets LockedUntil = now + Lock and clears the failures.
func ApplyThrottle(s ThrottleState, now time.Time, p ThrottlePolicy) (ThrottleState, bool) {
	if s.Locked(now) {
		return cloneState(s), false
	}
	failures := s.Failures
	if s.LockedUntil != nil { // the lock has ended
		failures = nil
	}
	kept := make([]time.Time, 0, len(failures)+1)
	for _, f := range failures {
		if now.Sub(f) < p.Window {
			kept = append(kept, f)
		}
	}
	kept = append(kept, now)
	if len(kept) >= p.MaxFailures {
		until := now.Add(p.Lock)
		return ThrottleState{LockedUntil: &until}, true
	}
	return ThrottleState{Failures: kept}, false
}

func cloneState(s ThrottleState) ThrottleState {
	out := ThrottleState{Failures: slices.Clone(s.Failures)}
	if s.LockedUntil != nil {
		u := *s.LockedUntil
		out.LockedUntil = &u
	}
	return out
}
