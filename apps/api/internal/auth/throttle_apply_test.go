// Tests for the pure lockout function (T022; D-08, US-4 s1-5, SC-004, plan P-07).
//
// API assumptions (go-capabilities names only ThrottlePolicy; the rest follows plan P-07):
//
//	type ThrottlePolicy struct{ MaxFailures int; Window, Lock time.Duration }   // sign-in: 5, 15m, 15m
//	type ThrottleState struct {
//	    Failures    []time.Time   // failures still inside the window, oldest first; never more than MaxFailures
//	    LockedUntil *time.Time    // nil = never locked / counter cleared
//	}
//	func (s ThrottleState) Locked(now time.Time) bool                       // now.Before(*LockedUntil)
//	func ApplyThrottle(s ThrottleState, now time.Time, p ThrottlePolicy) (ThrottleState, bool)
//	    // records ONE counted failure at now; the bool is startedLock. Pure: never mutates s or its slice.
//
// Rules (D-08, P-07): locked iff now < LockedUntil; a failure during a lock changes nothing; a lock
// that has ended (LockedUntil <= now) clears the counter before the new failure is recorded;
// otherwise keep failures with now-f < Window, append now, keep at most MaxFailures; on reaching
// MaxFailures set LockedUntil = now+Lock, clear Failures and report startedLock.
package auth_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
)

var (
	t0     = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	signIn = auth.ThrottlePolicy{MaxFailures: 5, Window: 15 * time.Minute, Lock: 15 * time.Minute}
)

func at(d time.Duration) time.Time { return t0.Add(d) }

func lockedUntil(s auth.ThrottleState) time.Time {
	if s.LockedUntil == nil {
		return time.Time{}
	}
	return *s.LockedUntil
}

// failAt applies one failure at t0+d and returns the new state and whether a lock started.
func failAt(s auth.ThrottleState, d time.Duration) (auth.ThrottleState, bool) {
	return auth.ApplyThrottle(s, at(d), signIn)
}

// lockAfterFiveFailures builds the US-4 s1 state: failures at T, T+1m, T+2m, T+3m, T+4m.
func lockAfterFiveFailures(t *testing.T) auth.ThrottleState {
	t.Helper()
	var s auth.ThrottleState
	var started bool
	for i := range 5 {
		s, started = failAt(s, time.Duration(i)*time.Minute)
		if started != (i == 4) {
			t.Fatalf("failure %d: startedLock = %v", i+1, started)
		}
	}
	return s
}

func TestLockout_LocksAt5thFailureUntilPlus15m(t *testing.T) {
	var s auth.ThrottleState
	for i := range 4 { // T, T+1m, T+2m, T+3m
		var started bool
		s, started = failAt(s, time.Duration(i)*time.Minute)
		if started || s.Locked(at(time.Duration(i)*time.Minute)) {
			t.Fatalf("failure %d locked the account", i+1)
		}
		if len(s.Failures) != i+1 {
			t.Fatalf("after failure %d: %d failures kept, want %d", i+1, len(s.Failures), i+1)
		}
	}
	s, started := failAt(s, 4*time.Minute) // T+4m: the 5th failure
	if !started {
		t.Fatal("the 5th failure within 15 minutes did not start a lock")
	}
	if got, want := lockedUntil(s), at(19*time.Minute); !got.Equal(want) {
		t.Errorf("LockedUntil = %v, want T+19m (%v)", got, want)
	}
	if len(s.Failures) != 0 {
		t.Errorf("Failures after lock start = %v, want cleared", s.Failures)
	}
	if !s.Locked(at(4 * time.Minute)) {
		t.Error("account is not locked at the moment the lock starts")
	}
}

func TestLockout_LockedAt18m59s_UnlockedAt19m(t *testing.T) {
	s := lockAfterFiveFailures(t)
	if !s.Locked(at(19*time.Minute - time.Second)) {
		t.Error("not locked at T+18m59s")
	}
	if s.Locked(at(19 * time.Minute)) {
		t.Error("still locked at exactly T+19m (locked iff now < locked_until)")
	}
	if s.Locked(at(19*time.Minute + time.Nanosecond)) {
		t.Error("still locked after T+19m")
	}
	if !s.Locked(at(4 * time.Minute)) {
		t.Error("not locked when the lock started")
	}
}

func TestLockout_NoExtensionDuringLock(t *testing.T) {
	s := lockAfterFiveFailures(t) // locked until T+19m
	want := lockedUntil(s)
	for _, d := range []time.Duration{5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 19*time.Minute - time.Second} {
		next, started := failAt(s, d)
		if started {
			t.Errorf("failure at %v during the lock reported startedLock", d)
		}
		if !lockedUntil(next).Equal(want) {
			t.Errorf("failure at %v moved LockedUntil from %v to %v", d, want, lockedUntil(next))
		}
		if !reflect.DeepEqual(next, s) {
			t.Errorf("failure at %v during the lock changed the state: %+v -> %+v", d, s, next)
		}
	}
	// US-4 s4 end to end: failures at T+10m and T+15m, lock still ends at T+19m.
	s, _ = failAt(s, 10*time.Minute)
	s, _ = failAt(s, 15*time.Minute)
	if !s.Locked(at(19*time.Minute-time.Second)) || s.Locked(at(19*time.Minute)) {
		t.Error("the lock must still end at T+19m after failures at T+10m and T+15m")
	}
}

func TestLockout_SlidingWindowNotLocked(t *testing.T) {
	// US-4 s5: failures at T, T+1m, T+2m, T+3m and again at T+17m do not lock (outcome only).
	var s auth.ThrottleState
	for i := range 4 {
		s, _ = failAt(s, time.Duration(i)*time.Minute)
	}
	s, started := failAt(s, 17*time.Minute)
	if started || s.Locked(at(17*time.Minute)) || s.Locked(at(17*time.Minute+30*time.Second)) {
		t.Fatal("a failure at T+17m locked the account although most earlier failures left the window")
	}
}

func TestLockout_WindowBoundaryIsExclusiveOfFifteenMinutes(t *testing.T) {
	// A failure exactly 15 minutes old no longer counts (now - f < Window).
	four := func() auth.ThrottleState {
		var s auth.ThrottleState
		for i := range 4 {
			s, _ = failAt(s, time.Duration(i)*time.Minute) // T, T+1m, T+2m, T+3m
		}
		return s
	}
	if _, started := failAt(four(), 15*time.Minute); started {
		t.Error("T was exactly 15m old at T+15m and must have left the window: only 4 failures count")
	}
	if _, started := failAt(four(), 15*time.Minute-time.Nanosecond); !started {
		t.Error("T was still inside the window one nanosecond earlier: 5 failures must lock")
	}
}

func TestLockout_FailuresInsideWindowAccumulateAcrossGaps(t *testing.T) {
	var s auth.ThrottleState
	var started bool
	for _, d := range []time.Duration{0, 4 * time.Minute, 8 * time.Minute, 12 * time.Minute, 14*time.Minute + 59*time.Second} {
		s, started = failAt(s, d)
	}
	if !started || lockedUntil(s).IsZero() {
		t.Fatal("five failures within one 15 minute window must lock")
	}
	if got, want := lockedUntil(s), at(14*time.Minute+59*time.Second+15*time.Minute); !got.Equal(want) {
		t.Errorf("LockedUntil = %v, want %v (now + Lock)", got, want)
	}
}

func TestLockout_EndOfLockClearsTheCounter(t *testing.T) {
	s := lockAfterFiveFailures(t)
	// After the lock ends a single failure starts a fresh count of 1 (not 5).
	next, started := failAt(s, 19*time.Minute)
	if started || next.Locked(at(19*time.Minute)) {
		t.Fatal("the first failure after a lock ended locked the account again")
	}
	if len(next.Failures) != 1 || !next.Failures[0].Equal(at(19*time.Minute)) {
		t.Errorf("Failures = %v, want exactly [T+19m]", next.Failures)
	}
	// It then takes five new failures, not one, to lock again.
	for i := 1; i <= 3; i++ {
		next, started = failAt(next, 19*time.Minute+time.Duration(i)*time.Minute)
		if started {
			t.Fatalf("lock restarted after only %d new failures", i+1)
		}
	}
	if _, started = failAt(next, 23*time.Minute); !started {
		t.Error("the 5th failure after the previous lock ended did not lock")
	}
	// A failure long after the lock ended also starts from a clean count.
	late, started := failAt(s, 2*time.Hour)
	if started || len(late.Failures) != 1 {
		t.Errorf("failure 2h later: started=%v failures=%v, want a fresh count of 1", started, late.Failures)
	}
}

func TestLockout_AtMostFiveFailuresKept(t *testing.T) {
	// A state with more failures than the policy allows (should not exist) is still capped.
	var crowded auth.ThrottleState
	for i := range 7 {
		crowded.Failures = append(crowded.Failures, at(time.Duration(i)*time.Second))
	}
	next, started := failAt(crowded, 10*time.Second)
	if !started {
		t.Error("a crowded window must lock")
	}
	if len(next.Failures) > 5 {
		t.Errorf("%d failures kept, the cap is 5 (database CHECK cardinality(failures) <= 5)", len(next.Failures))
	}
	// And in normal operation the stored list stays below the limit.
	var s auth.ThrottleState
	for i := range 40 {
		s, _ = failAt(s, time.Duration(i)*time.Second)
		if len(s.Failures) > 5 {
			t.Fatalf("after %d failures: %d kept", i+1, len(s.Failures))
		}
	}
}

func TestLockout_ApplyIsPure(t *testing.T) {
	orig := auth.ThrottleState{Failures: []time.Time{at(0), at(time.Minute)}}
	snapshot := auth.ThrottleState{Failures: append([]time.Time(nil), orig.Failures...)}
	next, _ := failAt(orig, 2*time.Minute)
	if !reflect.DeepEqual(orig, snapshot) {
		t.Errorf("Apply mutated its input: %+v, was %+v", orig, snapshot)
	}
	next.Failures[0] = at(99 * time.Hour) // the result must not alias the input either
	if !reflect.DeepEqual(orig, snapshot) {
		t.Error("the returned state shares its Failures backing array with the input")
	}
}

func TestLockout_PolicyIsRespected(t *testing.T) {
	// OTP-style policy for 004: 3 failures within 1 minute lock for 10 minutes.
	p := auth.ThrottlePolicy{MaxFailures: 3, Window: time.Minute, Lock: 10 * time.Minute}
	var s auth.ThrottleState
	var started bool
	s, started = auth.ApplyThrottle(s, at(0), p)
	s, started2 := auth.ApplyThrottle(s, at(30*time.Second), p)
	if started || started2 {
		t.Fatal("locked before the 3rd failure")
	}
	s, started = auth.ApplyThrottle(s, at(59*time.Second), p)
	if !started || !lockedUntil(s).Equal(at(59*time.Second+10*time.Minute)) {
		t.Errorf("3rd failure: started=%v until=%v", started, lockedUntil(s))
	}
	// The same three failures spread over more than the window do not lock.
	var spread auth.ThrottleState
	for _, d := range []time.Duration{0, 40 * time.Second, 80 * time.Second} {
		spread, started = auth.ApplyThrottle(spread, at(d), p)
	}
	if started {
		t.Error("failures spread over 80s locked a 1 minute window policy")
	}
}

func TestLockout_LockedMethod(t *testing.T) {
	var zero auth.ThrottleState
	if zero.Locked(at(0)) {
		t.Error("the zero state must not be locked")
	}
	u := at(time.Hour)
	s := auth.ThrottleState{LockedUntil: &u}
	if !s.Locked(at(0)) || !s.Locked(at(time.Hour-time.Nanosecond)) || s.Locked(at(time.Hour)) || s.Locked(at(2*time.Hour)) {
		t.Error("Locked must be true exactly while now < LockedUntil")
	}
}
