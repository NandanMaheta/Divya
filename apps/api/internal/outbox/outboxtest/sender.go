// Package outboxtest holds the test double for outbox.Sender. It lives in a non-test package so
// other modules' tests (013 reminders, 014 delivery, 019 payments) can reuse it (FR-029).
package outboxtest

import (
	"context"
	"errors"
	"sync"

	"github.com/NandanMaheta/swastik/apps/api/internal/outbox"
)

// ErrInjected is returned by Send when a failure was requested and no other error was set.
var ErrInjected = errors.New("outboxtest: injected send failure")

// Sender is an in-memory outbox.Sender. The zero value is ready to use and safe for concurrent
// use. It records every envelope it successfully accepts, can fail on demand, and counts
// deliveries per dedupe key so tests can assert "at most once" and "same key on a resend".
type Sender struct {
	mu         sync.Mutex
	calls      []outbox.Envelope // every call, including failed ones
	delivered  []outbox.Envelope // calls that returned nil
	byKey      map[string]int
	failNext   int
	failAlways bool
	failErr    error
	onSend     func(ctx context.Context, e outbox.Envelope)
}

// New returns an empty Sender.
func New() *Sender { return &Sender{} }

var _ outbox.Sender = (*Sender)(nil)

// Send implements outbox.Sender. The OnSend hook (if any) runs first, without the internal lock
// held, so a hook may call back into the outbox or the database.
func (s *Sender) Send(ctx context.Context, e outbox.Envelope) error {
	s.mu.Lock()
	hook := s.onSend
	s.mu.Unlock()
	if hook != nil {
		hook(ctx, e)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, e)
	if s.failAlways || s.failNext > 0 {
		if s.failNext > 0 {
			s.failNext--
		}
		if s.failErr != nil {
			return s.failErr
		}
		return ErrInjected
	}
	if s.byKey == nil {
		s.byKey = map[string]int{}
	}
	s.delivered = append(s.delivered, e)
	s.byKey[e.DedupeKey]++
	return nil
}

// FailNext makes the next n calls to Send return an error.
func (s *Sender) FailNext(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext = n
}

// FailAlways makes every call to Send return an error until Recover is called.
func (s *Sender) FailAlways() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failAlways = true
}

// Recover undoes FailAlways and FailNext.
func (s *Sender) Recover() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failAlways, s.failNext = false, 0
}

// FailWith sets the error returned by injected failures (default ErrInjected), for tests that
// check how error text is stored.
func (s *Sender) FailWith(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failErr = err
}

// OnSend registers a hook that runs at the start of every Send, before failure injection and
// recording. Tests use it to observe the database mid-send or to simulate a crash.
func (s *Sender) OnSend(fn func(ctx context.Context, e outbox.Envelope)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSend = fn
}

// Envelopes returns the envelopes accepted so far, in delivery order.
func (s *Sender) Envelopes() []outbox.Envelope {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]outbox.Envelope(nil), s.delivered...)
}

// Calls returns every envelope Send was called with, failed attempts included.
func (s *Sender) Calls() []outbox.Envelope {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]outbox.Envelope(nil), s.calls...)
}

// Deliveries returns how many times the dedupe key was accepted.
func (s *Sender) Deliveries(dedupeKey string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byKey[dedupeKey]
}

// Count returns the total number of accepted envelopes.
func (s *Sender) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.delivered)
}

// Reset forgets everything recorded and clears failure injection and the hook.
func (s *Sender) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls, s.delivered, s.byKey = nil, nil, nil
	s.failNext, s.failAlways, s.failErr, s.onSend = 0, false, nil, nil
}
