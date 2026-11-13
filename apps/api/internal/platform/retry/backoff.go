// Package retry holds the retry policy shared by the scheduler and the outbox: the backoff
// schedule and the redaction of error text before it is stored.
package retry

import (
	"hash/fnv"
	"strconv"
	"time"
)

// schedule is the base wait after the 1st..5th failed attempt; from the 6th on the wait is maxWait.
var schedule = [...]time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	1 * time.Hour,
	3 * time.Hour,
}

const maxWait = 6 * time.Hour

// Backoff returns the wait before the next try after `attempt` failed attempts (1-based; values
// below 1 count as 1). Waits are 1 m, 5 m, 15 m, 1 h, 3 h, then 6 h, with +/-20 % jitter below the
// cap. The jitter is derived from hash(seed, attempt), so it is deterministic and tests need no
// randomness, while different ids spread out instead of retrying in lockstep.
func Backoff(attempt int, seed string) time.Duration {
	attempt = max(attempt, 1)
	if attempt > len(schedule) {
		return maxWait
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(seed + "#" + strconv.Itoa(attempt)))
	percent := time.Duration(80 + h.Sum32()%41) // 80..120
	return schedule[attempt-1] / 100 * percent
}
