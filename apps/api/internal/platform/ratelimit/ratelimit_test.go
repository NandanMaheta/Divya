// Tests for the in-memory per-instance fixed-window limiter (T010; plan P-17).
//
// API assumptions (contracts are silent):
//
//	ratelimit.New(limit int, window time.Duration, maxKeys int, now func() time.Time) *Limiter
//	(*Limiter).Allow(key string) bool   // true = request admitted, false = answer 429 rate_limited
//	(*Limiter).Len() int                // number of keys currently tracked (for the size-cap test)
//
// A window opens at a key's first request and lasts exactly `window`; the tests start every window
// on a whole minute so "window opens at first request" and "windows aligned to the clock" agree.
// A rejected request does not extend the window. When the table is full, expired keys are evicted
// first and otherwise the least recently opened one; the key being inserted is always kept.
package ratelimit_test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/ratelimit"
)

var t0 = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) // a whole minute

type clock struct{ now time.Time }

func (c *clock) Now() time.Time          { return c.now }
func (c *clock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func newLimiter(limit int, window time.Duration, maxKeys int) (*ratelimit.Limiter, *clock) {
	c := &clock{now: t0}
	return ratelimit.New(limit, window, maxKeys, c.Now), c
}

func TestRateLimit_ThirtyPerMinuteThenRejected(t *testing.T) {
	l, _ := newLimiter(30, time.Minute, 1000)
	for i := 1; i <= 30; i++ {
		if !l.Allow("203.0.113.7") {
			t.Fatalf("request %d of 30 was rejected", i)
		}
	}
	for i := 31; i <= 35; i++ {
		if l.Allow("203.0.113.7") {
			t.Fatalf("request %d was admitted, want 429 after 30 per minute", i)
		}
	}
}

func TestRateLimit_KeysAreIndependent(t *testing.T) {
	l, _ := newLimiter(2, time.Minute, 1000)
	l.Allow("a")
	l.Allow("a")
	if l.Allow("a") {
		t.Fatal("a should be exhausted")
	}
	if !l.Allow("b") {
		t.Error("b was limited by a's traffic")
	}
}

func TestRateLimit_WindowRolloverAtExactBoundary(t *testing.T) {
	l, c := newLimiter(3, time.Minute, 1000)
	for range 3 {
		if !l.Allow("ip") {
			t.Fatal("setup request rejected")
		}
	}
	c.Advance(time.Minute - time.Nanosecond) // 12:00:59.999999999
	if l.Allow("ip") {
		t.Fatal("admitted one nanosecond before the window ends")
	}
	c.Advance(time.Nanosecond) // 12:01:00
	for i := 1; i <= 3; i++ {
		if !l.Allow("ip") {
			t.Fatalf("request %d in the new window was rejected", i)
		}
	}
	if l.Allow("ip") {
		t.Fatal("new window admitted more than the limit")
	}
}

func TestRateLimit_RejectedRequestsDoNotExtendTheWindow(t *testing.T) {
	l, c := newLimiter(1, time.Minute, 1000)
	l.Allow("ip")
	for range 10 { // hammering during the window must not push the reset out
		c.Advance(5 * time.Second)
		l.Allow("ip")
	}
	c.Advance(10 * time.Second) // now exactly 60s after the first request
	if !l.Allow("ip") {
		t.Fatal("window did not reset 60s after it opened")
	}
}

func TestRateLimit_SkippedWindowsAreFresh(t *testing.T) {
	l, c := newLimiter(2, time.Minute, 1000)
	l.Allow("ip")
	l.Allow("ip")
	c.Advance(10 * time.Minute)
	if !l.Allow("ip") || !l.Allow("ip") || l.Allow("ip") {
		t.Fatal("after a long idle period the key must get a full fresh window")
	}
}

func TestRateLimit_SizeCapEvictsExpiredFirst(t *testing.T) {
	l, c := newLimiter(5, time.Minute, 3)
	for _, k := range []string{"k1", "k2", "k3"} {
		l.Allow(k)
	}
	if got := l.Len(); got != 3 {
		t.Fatalf("Len = %d, want 3", got)
	}
	c.Advance(time.Minute) // all three windows have ended
	l.Allow("k4")
	if got := l.Len(); got > 3 {
		t.Errorf("Len = %d after insert, cap is 3", got)
	}
}

func TestRateLimit_SizeCapHoldsUnderAFlood(t *testing.T) {
	l, _ := newLimiter(30, time.Minute, 100)
	for i := range 10_000 {
		l.Allow(fmt.Sprintf("10.%d.%d.%d", i/65536, (i/256)%256, i%256))
		if got := l.Len(); got > 100 {
			t.Fatalf("after %d keys Len = %d, cap is 100", i+1, got)
		}
	}
}

func TestRateLimit_NewestKeyIsKeptWhenTableIsFull(t *testing.T) {
	l, _ := newLimiter(30, time.Minute, 3)
	for _, k := range []string{"k1", "k2", "k3"} { // all live in the window
		l.Allow(k)
	}
	admitted := 0
	for range 40 {
		if l.Allow("newcomer") {
			admitted++
		}
	}
	if admitted != 30 {
		t.Errorf("newcomer admitted %d times, want exactly 30 (it must not be evicted by its own insert)", admitted)
	}
}

func TestRateLimit_ConcurrentUseAdmitsExactlyTheLimit(t *testing.T) {
	l, _ := newLimiter(30, time.Minute, 1000)
	var admitted atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if l.Allow("shared") {
				admitted.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := admitted.Load(); got != 30 {
		t.Errorf("admitted = %d, want exactly 30", got)
	}
}

func TestRateLimit_ConcurrentMixedKeysUnderRace(t *testing.T) {
	l, _ := newLimiter(5, time.Minute, 50)
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 500 {
				l.Allow(fmt.Sprintf("k%d", (g*500+i)%200))
				_ = l.Len()
			}
		}()
	}
	wg.Wait()
	if got := l.Len(); got > 50 {
		t.Errorf("Len = %d, cap is 50", got)
	}
}

func TestRateLimit_PropertyAdmitsMinOfLimitAndRequestsPerWindow(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		limit := rapid.IntRange(1, 10).Draw(t, "limit")
		l, c := newLimiter(limit, time.Minute, 1000)
		minutes := rapid.IntRange(1, 8).Draw(t, "minutes")
		for m := range minutes {
			c.now = t0.Add(time.Duration(m) * time.Minute) // each window opens on a whole minute
			n := rapid.IntRange(1, 40).Draw(t, "requests")
			// The first request lands on the whole minute; the rest at increasing offsets inside it.
			admitted := 0
			offset := time.Duration(0)
			for i := range n {
				if i > 0 {
					offset += time.Duration(rapid.IntRange(0, 1_400).Draw(t, "ms")) * time.Millisecond // < 60s in total
				}
				c.now = t0.Add(time.Duration(m)*time.Minute + offset)
				if l.Allow("ip") {
					admitted++
				}
			}
			if want := min(limit, n); admitted != want {
				t.Fatalf("minute %d: admitted %d of %d requests, want %d (limit %d)", m, admitted, n, want, limit)
			}
		}
	})
}
