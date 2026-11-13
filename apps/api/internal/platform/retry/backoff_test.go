// Tests for retry.Backoff (feature 002, FR-017).
//
// Source of the numbers: specs/002-scheduler-tick/data-model.md section 7 and plan.md: waits of
// 1 m, 5 m, 15 m, 1 h, 3 h, then a 6 h cap; +/-20 % jitter below the cap, derived
// deterministically from hash(id, attempt) so tests need no randomness.
//
// Assumption (contract is silent on the signature, plan.md says "Backoff(attempt, seed)"):
//
//	func Backoff(attempt int, seed string) time.Duration
//
// attempt is 1-based (the number of attempts made so far, so attempt 1 is the wait after the
// first failure); seed is any stable string, the scheduler passes the job or message id. An
// attempt below 1 is treated like 1 and must not panic.
package retry

import (
	"strconv"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// base waits for attempts 1..6; every later attempt waits exactly the cap.
var base = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	1 * time.Hour,
	3 * time.Hour,
	6 * time.Hour,
}

const (
	capWait   = 6 * time.Hour
	floorWait = 48 * time.Second // 1 minute minus 20 %
)

func jitterRange(b time.Duration) (lo, hi time.Duration) {
	return b - b/5, b + b/5
}

func TestBackoff_Deterministic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		attempt := rapid.IntRange(1, 50).Draw(t, "attempt")
		seed := rapid.String().Draw(t, "seed")
		a, b := Backoff(attempt, seed), Backoff(attempt, seed)
		if a != b {
			t.Fatalf("Backoff(%d, %q) returned %v then %v", attempt, seed, a, b)
		}
	})
}

func TestBackoff_StaysWithinJitterBandOfTheSchedule(t *testing.T) {
	seeds := []string{"", "a", "job-1", "0198f2c3-0000-7000-8000-000000000001", "0198f2c3-0000-7000-8000-000000000002", "ünïcode"}
	for attempt := 1; attempt <= 5; attempt++ {
		lo, hi := jitterRange(base[attempt-1])
		for _, seed := range seeds {
			got := Backoff(attempt, seed)
			if got < lo || got > hi {
				t.Errorf("Backoff(%d, %q) = %v, want within [%v, %v] (+/-20%% of %v)", attempt, seed, got, lo, hi, base[attempt-1])
			}
		}
	}
}

func TestBackoff_CapAndBeyondIsExactlySixHours(t *testing.T) {
	for _, attempt := range []int{6, 7, 8, 20, 1000, 1 << 30} {
		for _, seed := range []string{"", "job-1", "job-2"} {
			if got := Backoff(attempt, seed); got != capWait {
				t.Errorf("Backoff(%d, %q) = %v, want the 6h cap", attempt, seed, got)
			}
		}
	}
}

func TestBackoff_StrictlyIncreasingUpToTheCapForEverySeed(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		seed := rapid.String().Draw(t, "seed")
		prev := time.Duration(0)
		for attempt := 1; attempt <= 6; attempt++ {
			got := Backoff(attempt, seed)
			if got <= prev {
				t.Fatalf("seed %q: Backoff(%d) = %v is not greater than Backoff(%d) = %v", seed, attempt, got, attempt-1, prev)
			}
			prev = got
		}
	})
}

func TestBackoff_NonDecreasingAndBounded(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		seed := rapid.String().Draw(t, "seed")
		a1 := rapid.IntRange(1, 200).Draw(t, "a1")
		a2 := rapid.IntRange(a1, 200).Draw(t, "a2")
		w1, w2 := Backoff(a1, seed), Backoff(a2, seed)
		if w2 < w1 {
			t.Fatalf("seed %q: Backoff(%d) = %v is shorter than Backoff(%d) = %v", seed, a2, w2, a1, w1)
		}
		for _, w := range []time.Duration{w1, w2} {
			if w < floorWait || w > capWait {
				t.Fatalf("seed %q: wait %v outside [%v, %v]", seed, w, floorWait, capWait)
			}
		}
	})
}

// Jitter must actually vary with the seed, otherwise a burst of failures retries in lockstep.
func TestBackoff_JitterDependsOnTheSeed(t *testing.T) {
	for attempt := 1; attempt <= 5; attempt++ {
		distinct := map[time.Duration]bool{}
		for i := range 64 {
			distinct[Backoff(attempt, "seed-"+strconv.Itoa(i))] = true
		}
		if len(distinct) < 8 {
			t.Errorf("attempt %d: only %d distinct waits over 64 seeds, want real jitter", attempt, len(distinct))
		}
	}
}

func TestBackoff_FirstWaitIsAtLeast48Seconds(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		seed := rapid.String().Draw(t, "seed")
		if got := Backoff(1, seed); got < floorWait {
			t.Fatalf("Backoff(1, %q) = %v, want >= %v", seed, got, floorWait)
		}
	})
}

func TestBackoff_AttemptBelowOneBehavesLikeOneAndDoesNotPanic(t *testing.T) {
	for _, attempt := range []int{0, -1, -1 << 30} {
		got := Backoff(attempt, "job-1")
		if got != Backoff(1, "job-1") {
			t.Errorf("Backoff(%d) = %v, want the same as Backoff(1) = %v", attempt, got, Backoff(1, "job-1"))
		}
	}
}
