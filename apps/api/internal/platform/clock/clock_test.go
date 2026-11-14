// Tests for the injected clock (feature 002, FR-032, constitution III).
//
// Contract (specs/002-scheduler-tick/contracts/go-module-api.md): Func is an alias of
// func() time.Time, Real returns time.Now().UTC(), Fake is mutex guarded with Now/Set/Advance
// and f.Now is usable wherever a Func is expected.
package clock

import (
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, time.November, 13, 4, 30, 0, 0, time.UTC)

// A Fake's Now method must be assignable to Func (compile-time check).
var _ Func = (*Fake)(nil).Now

// Func must be an alias, so a plain func() time.Time can be passed as a Func and back.
var (
	_ Func             = func() time.Time { return time.Time{} }
	_ func() time.Time = Func(nil)
	_ func() time.Time = Real
	_ Func             = Real
)

func TestFake_NowReturnsInitialInstant(t *testing.T) {
	f := NewFake(t0)
	if got := f.Now(); !got.Equal(t0) {
		t.Fatalf("Now() = %v, want %v", got, t0)
	}
	// Reading does not move the clock: no hidden ticking.
	if got := f.Now(); !got.Equal(t0) {
		t.Fatalf("second Now() = %v, want %v", got, t0)
	}
}

func TestFake_SetAndAdvance(t *testing.T) {
	tests := []struct {
		name string
		do   func(f *Fake)
		want time.Time
	}{
		{"set forward", func(f *Fake) { f.Set(t0.Add(48 * time.Hour)) }, t0.Add(48 * time.Hour)},
		{"set backward", func(f *Fake) { f.Set(t0.Add(-time.Hour)) }, t0.Add(-time.Hour)},
		{"advance", func(f *Fake) { f.Advance(5 * time.Minute) }, t0.Add(5 * time.Minute)},
		{"advance zero", func(f *Fake) { f.Advance(0) }, t0},
		{"advance twice accumulates", func(f *Fake) { f.Advance(time.Minute); f.Advance(2 * time.Minute) }, t0.Add(3 * time.Minute)},
		{"set then advance", func(f *Fake) { f.Set(t0.Add(time.Hour)); f.Advance(time.Second) }, t0.Add(time.Hour + time.Second)},
		{"advance across a day boundary", func(f *Fake) { f.Advance(24*time.Hour + time.Nanosecond) }, t0.Add(24*time.Hour + time.Nanosecond)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NewFake(t0)
			tt.do(f)
			if got := f.Now(); !got.Equal(tt.want) {
				t.Fatalf("Now() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFake_NowIsUsableAsFunc(t *testing.T) {
	f := NewFake(t0)
	// Passing the method value where a Func is required asserts that Fake.Now satisfies Func.
	read := func(fn Func) time.Time { return fn() }
	now := f.Now
	f.Advance(time.Hour)
	if got := read(now); !got.Equal(t0.Add(time.Hour)) {
		t.Fatalf("Func bound to Fake.Now = %v, want %v (it must follow the fake, not a snapshot)", got, t0.Add(time.Hour))
	}
}

func TestFake_IndependentInstances(t *testing.T) {
	a, b := NewFake(t0), NewFake(t0)
	a.Advance(time.Hour)
	if got := b.Now(); !got.Equal(t0) {
		t.Fatalf("advancing one Fake moved another: %v", got)
	}
}

// Run with -race: concurrent readers and writers must not race, and every observed value must
// be one a writer actually produced (no torn reads).
func TestFake_ConcurrentReadersAndWriters(t *testing.T) {
	f := NewFake(t0)
	const readers, writers, iters = 8, 4, 500

	var wg sync.WaitGroup
	for range readers {
		wg.Go(func() {
			prev := f.Now()
			for range iters {
				got := f.Now()
				if got.Before(t0) {
					t.Errorf("Now() = %v is before the initial instant; only Advance(+1ms) is used here", got)
					return
				}
				if got.Before(prev) {
					t.Errorf("Now() went backwards: %v after %v", got, prev)
					return
				}
				prev = got
			}
		})
	}
	for range writers {
		wg.Go(func() {
			for range iters {
				f.Advance(time.Millisecond)
			}
		})
	}
	wg.Wait()

	want := t0.Add(writers * iters * time.Millisecond)
	if got := f.Now(); !got.Equal(want) {
		t.Fatalf("after %d concurrent Advance calls Now() = %v, want %v (lost updates)", writers*iters, got, want)
	}
}

func TestFake_ConcurrentSetAndNow(t *testing.T) {
	f := NewFake(t0)
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			for range 200 {
				f.Set(t0.Add(time.Duration(i) * time.Hour))
				_ = f.Now()
			}
		})
	}
	wg.Wait()
	got := f.Now()
	ok := false
	for i := range 4 {
		if got.Equal(t0.Add(time.Duration(i) * time.Hour)) {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("Now() = %v is not any value that was Set", got)
	}
}

// Real is the only place allowed to read the machine clock; the test needs the machine clock
// too, to prove Real is the machine's now in UTC.
func TestReal_ReturnsCurrentInstantInUTC(t *testing.T) {
	before := time.Now()
	got := Real()
	after := time.Now()

	if got.Location() != time.UTC {
		t.Fatalf("Real().Location() = %v, want UTC", got.Location())
	}
	if got.Before(before.Add(-time.Second)) || got.After(after.Add(time.Second)) {
		t.Fatalf("Real() = %v, not within [%v, %v]", got, before, after)
	}
}
