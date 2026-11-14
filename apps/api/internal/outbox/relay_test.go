package outbox_test

// T033 - the relay: hand available messages to the sender registered for their topic
// (US3.2, 3.4, 3.6, 3.8, 6.1-6.3, 7.3, 7.4, 8.7; FR-017, FR-025, FR-026, FR-028, FR-030; SC-008; BR-TASK-03).
// Shared fixtures and the assumptions about RelayBudget/RelaySummary: helpers_test.go.

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/outbox"
	"github.com/NandanMaheta/swastik/apps/api/internal/outbox/outboxtest"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
)

// lease is the allowed run time of a claim (data-model section 7).
const lease = 60 * time.Second

func TestOutbox_BR_TASK_03_RelayHandsAnAvailableMessageToItsSenderOnceAndMarksItSent(t *testing.T) {
	e := newEnv(t)
	e.reset()
	m := e.msg(testTopic, "task:7:slot:1")
	m.Subject = "task:7"
	e.add(m)

	var (
		midStatus     string
		midAttempts   int
		midLockedTill time.Time
		sawDeadline   bool
		notLocked     bool
	)
	e.snd.OnSend(func(ctx context.Context, env outbox.Envelope) {
		_, sawDeadline = ctx.Deadline()
		// Seen from another connection while Send runs: the claim is committed ...
		if err := e.pool.QueryRow(ctx, `SELECT status, attempts, locked_until FROM outbox_messages WHERE id = $1`, env.ID).
			Scan(&midStatus, &midAttempts, &midLockedTill); err != nil {
			t.Errorf("read mid-send: %v", err)
		}
		// ... and no transaction holds the row lock, so Send runs outside any transaction.
		tx, err := e.pool.Begin(ctx)
		if err != nil {
			t.Errorf("begin: %v", err)
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		var one int
		notLocked = tx.QueryRow(ctx, `SELECT 1 FROM outbox_messages WHERE id = $1 FOR UPDATE NOWAIT`, env.ID).Scan(&one) == nil
	})

	sum := e.relay()
	if sum.Sent != 1 || sum.Failed != 0 || sum.Dead != 0 || sum.Expired != 0 {
		t.Fatalf("summary = %+v, want exactly one sent", sum)
	}
	if e.snd.Count() != 1 {
		t.Fatalf("sender received %d messages, want 1", e.snd.Count())
	}
	env := e.snd.Envelopes()[0]
	r := e.row(testTopic, "task:7:slot:1")
	if env.ID != r.ID || env.OrgID != e.org || env.Topic != testTopic || env.Subject != "task:7" ||
		env.DedupeKey != "task:7:slot:1" || env.Recipient.Kind != "admin" || env.Recipient.ID != nil || env.Attempt != 1 {
		t.Errorf("envelope = %+v", env)
	}
	jsonEqual(t, env.Payload, m.Payload)
	if env.CreatedAt.IsZero() {
		t.Error("envelope.CreatedAt is zero")
	}

	if r.Status != "sent" || r.Attempts != 1 {
		t.Errorf("row status=%s attempts=%d, want sent/1", r.Status, r.Attempts)
	}
	for name, ts := range map[string]*time.Time{"sent_at": r.SentAt, "finished_at": r.FinishedAt, "first_attempt_at": r.FirstAttemptAt, "last_attempt_at": r.LastAttemptAt} {
		if ts == nil || !ts.Equal(t0) {
			t.Errorf("%s = %v, want %v (the injected clock)", name, ts, t0)
		}
	}

	if midStatus != "sending" || midAttempts != 1 || !midLockedTill.Equal(t0.Add(lease)) {
		t.Errorf("mid-send row = %s attempts=%d locked_until=%v, want sending/1/%v", midStatus, midAttempts, midLockedTill, t0.Add(lease))
	}
	if !notLocked {
		t.Error("the row was locked by an open transaction during Send; the claim must commit before the sender is called")
	}
	if !sawDeadline {
		t.Error("Send received a context without a deadline; the relay must bound Send (5 s)")
	}

	if again := e.relay(); again.Sent != 0 || e.snd.Count() != 1 {
		t.Errorf("a second relay re-sent a sent message: %+v, deliveries=%d", again, e.snd.Count())
	}
}

func TestOutbox_BR_TASK_03_RelayWaitsForAvailableAtAndRunsAtExactlyThatInstant(t *testing.T) {
	e := newEnv(t)
	e.reset()
	e.add(e.msg(testTopic, "k"))
	e.exec(`UPDATE outbox_messages SET available_at = $1`, t0.Add(10*time.Minute))

	e.clk.Advance(10*time.Minute - time.Nanosecond)
	if sum := e.relay(); sum.Sent != 0 || e.snd.Count() != 0 {
		t.Fatalf("relayed %v before available_at", sum)
	}
	e.clk.Advance(time.Nanosecond)
	if sum := e.relay(); sum.Sent != 1 {
		t.Fatalf("not relayed at available_at: %+v", sum)
	}
}

// US3.4: the sender was called but the "sent" mark was lost (a crash). A later tick relays the
// message again with the same de-duplication key, but never while the first claim is still valid.
func TestOutbox_BR_TASK_03_RelayCrashBetweenSendAndMarkResendsWithTheSameKey(t *testing.T) {
	e := newEnv(t)
	const key = "task:7:slot:1"

	t.Run("lost mark after a real send", func(t *testing.T) {
		e.reset()
		e.add(e.msg(testTopic, key))
		calls := 0
		e.snd.OnSend(func(ctx context.Context, env outbox.Envelope) {
			calls++
			if calls == 1 {
				// Simulate the crash: another claimant took the lease, so this tick's fenced mark cannot land.
				if _, err := e.pool.Exec(ctx, `UPDATE outbox_messages SET lock_token = gen_random_uuid() WHERE id = $1`, env.ID); err != nil {
					t.Errorf("simulate crash: %v", err)
				}
			}
		})

		if _, err := e.svc.Relay(t.Context(), e.budgetOf(250, 45*time.Second)); err != nil {
			t.Fatalf("a lost lease must not fail the relay: %v", err)
		}
		r := e.row(testTopic, key)
		if r.Status != "sending" || r.SentAt != nil {
			t.Fatalf("after the lost mark: status=%s sent_at=%v, want sending/nil (a stale mark must not win)", r.Status, r.SentAt)
		}

		e.clk.Advance(lease - time.Second)
		if e.relay(); e.snd.Deliveries(key) != 1 {
			t.Fatalf("resent while the first claim was still valid (deliveries=%d)", e.snd.Deliveries(key))
		}

		e.clk.Advance(time.Second) // now == locked_until: the lease is over
		if sum := e.relay(); sum.Sent != 1 {
			t.Fatalf("not retried after the lease ended: %+v", sum)
		}
		if got := e.snd.Deliveries(key); got != 2 {
			t.Fatalf("deliveries of %q = %d, want 2 (at-least-once)", key, got)
		}
		envs := e.snd.Envelopes()
		if envs[0].DedupeKey != key || envs[1].DedupeKey != key || envs[0].ID != envs[1].ID {
			t.Errorf("the resend must carry the same dedupe key and id: %+v / %+v", envs[0], envs[1])
		}
		if envs[1].Attempt != 2 {
			t.Errorf("resend attempt = %d, want 2", envs[1].Attempt)
		}
		if r := e.row(testTopic, key); r.Status != "sent" || r.Attempts != 2 {
			t.Errorf("final row = %s attempts=%d, want sent/2", r.Status, r.Attempts)
		}
	})

	t.Run("a claim left behind by a dead tick is reclaimed only after its lease", func(t *testing.T) {
		e.reset()
		e.add(e.msg(testTopic, key))
		e.exec(`UPDATE outbox_messages SET status='sending', attempts=1, first_attempt_at=$1, last_attempt_at=$1,
			locked_until=$2, lock_token=gen_random_uuid(), locked_by='dead-tick'`, t0, t0.Add(lease))

		e.clk.Advance(lease - time.Second)
		if e.relay(); e.snd.Count() != 0 {
			t.Fatal("a message claimed by a live tick was handed out again")
		}
		e.clk.Advance(time.Second)
		if sum := e.relay(); sum.Sent != 1 {
			t.Fatalf("expired claim not reclaimed: %+v", sum)
		}
		if env := e.snd.Envelopes()[0]; env.DedupeKey != key || env.Attempt != 2 {
			t.Errorf("envelope = %+v, want the same key with attempt 2", env)
		}
	})
}

// US3.6 and FR-025: a missing sender is a failed attempt, never "sent".
func TestOutbox_BR_TASK_03_RelayTopicWithoutASenderIsAFailedAttempt(t *testing.T) {
	e := newEnv(t)
	e.reset()
	e.add(e.msg("task.nobody_listens", "k"))

	sum := e.relay()
	if sum.Sent != 0 || sum.Failed != 1 {
		t.Fatalf("summary = %+v, want one failure", sum)
	}
	r := e.row("task.nobody_listens", "k")
	if r.Status != "pending" || r.Attempts != 1 || r.SentAt != nil {
		t.Errorf("row = %s attempts=%d sent_at=%v, want pending/1/nil", r.Status, r.Attempts, r.SentAt)
	}
	if r.LastError == nil || *r.LastError == "" {
		t.Error("last_error is empty; the admin must see why it failed")
	}
	if !r.AvailableAt.After(t0) {
		t.Errorf("available_at = %v, want a later retry time", r.AvailableAt)
	}
}

func TestOutbox_BR_TASK_03_RelayFailureStoresASanitisedErrorAndRetriesLater(t *testing.T) {
	e := newEnv(t)
	e.reset()
	e.add(e.msg(testTopic, "k"))
	e.snd.FailWith(errors.New("push gateway rejected: Authorization: Bearer abc.def-ghi_jkl for ABCDE1234F " + strings.Repeat("x", 1000)))
	e.snd.FailNext(1)

	sum := e.relay()
	if sum.Failed != 1 || sum.Sent != 0 || sum.Dead != 0 {
		t.Fatalf("summary = %+v, want one non-final failure", sum)
	}
	r := e.row(testTopic, "k")
	if r.Status != "pending" || r.Attempts != 1 || r.FinishedAt != nil {
		t.Fatalf("row = %s attempts=%d finished=%v, want pending/1/nil", r.Status, r.Attempts, r.FinishedAt)
	}
	if r.LastError == nil || *r.LastError == "" {
		t.Fatal("last_error is empty")
	}
	for _, secret := range []string{"abc.def-ghi_jkl", "ABCDE1234F"} {
		if strings.Contains(*r.LastError, secret) {
			t.Errorf("last_error leaks %q (constitution VI): %q", secret, *r.LastError)
		}
	}
	if n := len([]rune(*r.LastError)); n > 500 {
		t.Errorf("last_error has %d characters, want at most 500", n)
	}
	if r.FirstAttemptAt == nil || r.LastAttemptAt == nil || !r.FirstAttemptAt.Equal(t0) || !r.LastAttemptAt.Equal(t0) {
		t.Errorf("attempt times = %v / %v, want both %v", r.FirstAttemptAt, r.LastAttemptAt, t0)
	}

	// Next attempt: a later instant, first_attempt_at unchanged, delivered with Attempt 2.
	e.clk.Set(r.AvailableAt)
	if sum := e.relay(); sum.Sent != 1 {
		t.Fatalf("retry not delivered: %+v", sum)
	}
	r2 := e.row(testTopic, "k")
	if r2.Status != "sent" || r2.Attempts != 2 || !r2.FirstAttemptAt.Equal(t0) || !r2.LastAttemptAt.Equal(r.AvailableAt) {
		t.Errorf("after retry: %s attempts=%d first=%v last=%v", r2.Status, r2.Attempts, r2.FirstAttemptAt, r2.LastAttemptAt)
	}
	if got := e.snd.Envelopes()[0].Attempt; got != 2 {
		t.Errorf("envelope.Attempt = %d, want 2", got)
	}
}

// US6.2, FR-017: each wait is longer than the previous one, with 1m, 5m, 15m, 1h, 3h, 6h bases and
// +/-20 % jitter, and no retry happens before its time.
func TestOutbox_BR_TASK_03_RelayRetryWaitsGrowAndNeverRunEarly(t *testing.T) {
	e := newEnv(t)
	e.reset()
	e.add(e.msg(testTopic, "k"))
	e.snd.FailAlways()

	bases := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 3 * time.Hour, 6 * time.Hour}
	var prevWait time.Duration
	for i, base := range bases {
		attempt := i + 1
		now := e.clk.Now()
		e.relay()
		r := e.row(testTopic, "k")
		if r.Status != "pending" || r.Attempts != attempt {
			t.Fatalf("attempt %d: row = %s attempts=%d", attempt, r.Status, r.Attempts)
		}
		wait := r.AvailableAt.Sub(now)
		if wait < base*8/10 {
			t.Errorf("attempt %d: wait %v is shorter than 80%% of %v", attempt, wait, base)
		}
		if upper := now.Add(base * 12 / 10); !istdate.DefaultQuietHours.Contains(upper) && wait > base*12/10 {
			t.Errorf("attempt %d: wait %v is longer than 120%% of %v", attempt, wait, base)
		}
		if wait <= prevWait {
			t.Errorf("attempt %d: wait %v is not longer than the previous wait %v", attempt, wait, prevWait)
		}
		prevWait = wait

		calls := len(e.snd.Calls())
		e.clk.Set(r.AvailableAt.Add(-time.Second))
		if e.relay(); len(e.snd.Calls()) != calls {
			t.Fatalf("attempt %d: retried one second before its due time", attempt+1)
		}
		e.clk.Set(r.AvailableAt)
	}
}

// US6.3: after the last allowed attempt the message is dead and nothing retries it on its own.
func TestOutbox_BR_TASK_03_RelayBecomesDeadAfterMaxAttemptsAndStaysThere(t *testing.T) {
	e := newEnv(t)
	e.svc.RegisterSender("task.two_tries", e.snd, 2)

	t.Run("message max attempts", func(t *testing.T) {
		e.reset()
		m := e.msg(testTopic, "k")
		m.MaxAttempts = 3
		e.add(m)
		e.snd.FailAlways()
		var last outbox.RelaySummary
		for i := 1; i <= 3; i++ {
			last = e.relay()
			if i < 3 {
				e.clk.Set(e.row(testTopic, "k").AvailableAt)
			}
		}
		if last.Dead != 1 {
			t.Fatalf("summary of the last attempt = %+v, want one dead", last)
		}
		r := e.row(testTopic, "k")
		if r.Status != "dead" || r.Attempts != 3 || r.FinishedAt == nil || r.LastError == nil || r.AlertedAt != nil {
			t.Fatalf("row = status %s attempts=%d finished=%v last_error=%v alerted=%v, want dead/3/set/set/nil",
				r.Status, r.Attempts, r.FinishedAt, r.LastError, r.AlertedAt)
		}
		e.clk.Advance(30 * 24 * time.Hour)
		e.relay()
		if got := len(e.snd.Calls()); got != 3 {
			t.Fatalf("sender called %d times, want 3: dead messages are never retried automatically (FR-017)", got)
		}
	})

	t.Run("topic default from RegisterSender", func(t *testing.T) {
		e.reset()
		e.add(e.msg("task.two_tries", "k2"))
		e.snd.FailAlways()
		e.relay()
		e.clk.Set(e.row("task.two_tries", "k2").AvailableAt)
		e.relay()
		if got := e.row("task.two_tries", "k2"); got.Status != "dead" || got.Attempts != 2 {
			t.Fatalf("row = %s attempts=%d, want dead/2", got.Status, got.Attempts)
		}
	})
}

// SC-008: a retry never becomes available inside quiet hours.
func TestOutbox_BR_TASK_03_RelayRetryIsDeferredOutOfQuietHours(t *testing.T) {
	e := newEnv(t)
	e.reset()
	e.clk.Set(ist(13, 21, 29).Add(30 * time.Second)) // Friday 21:29:30 IST, still outside quiet hours
	e.add(e.msg(testTopic, "k"))
	e.snd.FailNext(1)

	if sum := e.relay(); sum.Failed != 1 {
		t.Fatalf("summary = %+v, want one failure", sum)
	}
	r := e.row(testTopic, "k")
	// The back-off (at least 48 s) lands after 21:30, so the retry waits for 08:00 IST.
	if want := ist(14, 8, 0); !r.AvailableAt.Equal(want) {
		t.Fatalf("available_at = %v, want %v (08:00 IST Saturday)", r.AvailableAt, want)
	}

	e.clk.Set(ist(14, 7, 59))
	if e.relay(); e.snd.Count() != 0 {
		t.Fatal("retried inside quiet hours")
	}
	e.clk.Set(ist(14, 8, 0))
	if sum := e.relay(); sum.Sent != 1 {
		t.Fatalf("not retried at 08:00: %+v", sum)
	}
}

// US7.3, US7.4, US8.7, FR-028: expired messages are never handed to a sender; messages without expiry go late.
func TestOutbox_BR_TASK_03_RelayExpiredMessagesAreNeverSent(t *testing.T) {
	e := newEnv(t)

	t.Run("expiry passed during an outage", func(t *testing.T) {
		e.reset()
		m := e.msg(testTopic, "k")
		m.ExpiresAt = ptr(t0.Add(2 * time.Hour))
		e.add(m)
		e.clk.Advance(5 * time.Hour)

		sum := e.relay()
		if sum.Expired != 1 || sum.Sent != 0 {
			t.Fatalf("summary = %+v, want one expired", sum)
		}
		r := e.row(testTopic, "k")
		if r.Status != "expired" || r.FinishedAt == nil || !r.FinishedAt.Equal(e.clk.Now()) || r.SentAt != nil {
			t.Errorf("row = %s finished=%v sent=%v", r.Status, r.FinishedAt, r.SentAt)
		}
		if len(e.snd.Calls()) != 0 {
			t.Fatal("an expired message reached the sender")
		}
	})

	t.Run("expires_at equal to now is expired", func(t *testing.T) {
		e.reset()
		m := e.msg(testTopic, "k")
		m.ExpiresAt = ptr(t0)
		e.add(m)
		if sum := e.relay(); sum.Expired != 1 || e.snd.Count() != 0 {
			t.Fatalf("summary = %+v, deliveries=%d", sum, e.snd.Count())
		}
	})

	t.Run("a message with a future expiry is sent, one without any expiry is sent late (US7.4)", func(t *testing.T) {
		e.reset()
		valid := e.msg(testTopic, "valid")
		valid.ExpiresAt = ptr(t0.Add(time.Hour))
		e.add(valid)
		e.add(e.msg(testTopic, "no-expiry"))
		e.clk.Advance(30 * time.Minute)
		e.add(e.msg(testTopic, "late-no-expiry"))
		e.clk.Advance(3 * time.Hour)
		e.add(e.msg(testTopic, "later"))

		sum := e.relay()
		if sum.Sent != 3 || sum.Expired != 1 {
			t.Fatalf("summary = %+v, want 3 sent (no-expiry, late-no-expiry, later) and 1 expired (valid)", sum)
		}
		if e.snd.Deliveries("valid") != 0 || e.snd.Deliveries("no-expiry") != 1 {
			t.Errorf("deliveries valid=%d no-expiry=%d", e.snd.Deliveries("valid"), e.snd.Deliveries("no-expiry"))
		}
	})

	t.Run("a message waiting for a retry expires instead of being re-sent", func(t *testing.T) {
		e.reset()
		m := e.msg(testTopic, "k")
		m.ExpiresAt = ptr(t0.Add(30 * time.Minute))
		e.add(m)
		e.snd.FailNext(1)
		e.relay()
		e.clk.Advance(time.Hour)
		if sum := e.relay(); sum.Expired != 1 || sum.Sent != 0 {
			t.Fatalf("summary = %+v, want one expired", sum)
		}
		if got := len(e.snd.Calls()); got != 1 {
			t.Fatalf("sender calls = %d, want only the first failed attempt", got)
		}
	})
}

// BR-TASK-03 stop on completion: withdrawn means never sent.
func TestOutbox_BR_TASK_03_RelayNeverSendsAWithdrawnMessage(t *testing.T) {
	e := newEnv(t)
	e.reset()
	m := e.msg(testTopic, "k")
	m.Subject = "task:7"
	e.add(m)
	e.add(e.msg(testTopic, "kept"))

	err := e.inTx(func(ctx context.Context) error {
		_, err := e.svc.WithdrawSubject(ctx, e.org, "task:7")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if sum := e.relay(); sum.Sent != 1 {
		t.Fatalf("summary = %+v, want only the message of the other subject", sum)
	}
	if e.snd.Deliveries("k") != 0 || e.snd.Deliveries("kept") != 1 {
		t.Fatalf("deliveries k=%d kept=%d", e.snd.Deliveries("k"), e.snd.Deliveries("kept"))
	}
}

// FR-030: messages for one recipient are relayed in the order they were recorded.
func TestOutbox_BR_TASK_03_RelayKeepsPerRecipientOrder(t *testing.T) {
	e := newEnv(t)
	e.reset()
	alice, bob := uuid.New(), uuid.New()

	err := e.inTx(func(ctx context.Context) error {
		for i := 1; i <= 6; i++ {
			for name, id := range map[string]uuid.UUID{"alice": alice, "bob": bob} {
				if _, err := e.svc.Add(ctx, e.clientMsg(testTopic, fmt.Sprintf("%s-%d", name, i), id)); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	e.add(e.clientMsg(testTopic, "alice-7", alice)) // recorded later, in another transaction

	if sum := e.relay(); sum.Sent != 13 {
		t.Fatalf("summary = %+v, want 13 sent", sum)
	}
	got := map[string][]string{}
	for _, env := range e.snd.Envelopes() {
		who := strings.SplitN(env.DedupeKey, "-", 2)[0]
		got[who] = append(got[who], env.DedupeKey)
	}
	want := map[string][]string{
		"alice": {"alice-1", "alice-2", "alice-3", "alice-4", "alice-5", "alice-6", "alice-7"},
		"bob":   {"bob-1", "bob-2", "bob-3", "bob-4", "bob-5", "bob-6"},
	}
	for who, keys := range want {
		if strings.Join(got[who], ",") != strings.Join(keys, ",") {
			t.Errorf("order for %s = %v, want %v", who, got[who], keys)
		}
	}
}

// US3.8, FR-026: overlapping relays never hand one message to the sender twice.
func TestOutbox_BR_TASK_03_RelayOverlappingRelaysNeverDoubleSend(t *testing.T) {
	e := newEnv(t)

	t.Run("a second relay that starts while the first send is in flight", func(t *testing.T) {
		e.reset()
		for i := 1; i <= 5; i++ {
			e.add(e.msg(testTopic, fmt.Sprintf("k%d", i)))
		}
		var nested outbox.RelaySummary
		first := true
		e.snd.OnSend(func(ctx context.Context, env outbox.Envelope) {
			if !first {
				return
			}
			first = false
			var err error
			nested, err = e.svc.Relay(ctx, e.budgetOf(250, 45*time.Second)) // the overlapping tick
			if err != nil {
				t.Errorf("nested Relay: %v", err)
			}
		})

		outer := e.relay()
		if nested.Sent != 4 {
			t.Errorf("the overlapping relay sent %d, want the 4 messages that were not in flight", nested.Sent)
		}
		if outer.Sent+nested.Sent != 5 {
			t.Errorf("sent counts add up to %d, want 5", outer.Sent+nested.Sent)
		}
		for i := 1; i <= 5; i++ {
			if d := e.snd.Deliveries(fmt.Sprintf("k%d", i)); d != 1 {
				t.Errorf("k%d delivered %d times, want 1", i, d)
			}
		}
	})

	t.Run("concurrent relays over 60 messages", func(t *testing.T) {
		e.reset()
		const total, workers = 60, 4
		for i := 1; i <= total; i++ {
			e.add(e.msg(testTopic, fmt.Sprintf("c%d", i)))
		}
		e.snd.OnSend(func(context.Context, outbox.Envelope) { runtime.Gosched() })

		var (
			wg    sync.WaitGroup
			mu    sync.Mutex
			sent  int
			start = make(chan struct{})
		)
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				sum, err := e.svc.Relay(t.Context(), e.budgetOf(250, 45*time.Second))
				if err != nil {
					t.Errorf("Relay: %v", err)
				}
				mu.Lock()
				sent += sum.Sent
				mu.Unlock()
			}()
		}
		close(start)
		wg.Wait()

		if sent != total {
			t.Errorf("summed Sent = %d, want %d", sent, total)
		}
		for i := 1; i <= total; i++ {
			if d := e.snd.Deliveries(fmt.Sprintf("c%d", i)); d != 1 {
				t.Errorf("c%d delivered %d times, want exactly 1", i, d)
			}
		}
		if n := e.count(`status = 'sent'`); n != total {
			t.Errorf("sent rows = %d, want %d", n, total)
		}
	})
}

// FR-005: a relay stops at its message cap and at its deadline; the rest waits for the next tick, oldest first.
func TestOutbox_BR_TASK_03_RelayStopsAtItsBudgetAndDrainsOldestFirst(t *testing.T) {
	e := newEnv(t)
	e.reset()
	for i := 1; i <= 5; i++ {
		e.add(e.msg(testTopic, fmt.Sprintf("k%d", i)))
	}

	if sum := e.relayWith(e.budgetOf(2, 45*time.Second)); sum.Sent != 2 {
		t.Fatalf("first relay = %+v, want 2 sent", sum)
	}
	if got := e.count(`status = 'pending'`); got != 3 {
		t.Fatalf("pending after the capped relay = %d, want 3", got)
	}

	if sum := e.relayWith(e.budgetOf(250, -time.Second)); sum.Sent != 0 || e.snd.Count() != 2 {
		t.Fatalf("a relay whose deadline has passed took work: %+v", sum)
	}

	if sum := e.relayWith(e.budgetOf(2, 45*time.Second)); sum.Sent != 2 {
		t.Fatalf("second relay = %+v, want 2 sent", sum)
	}
	if sum := e.relay(); sum.Sent != 1 {
		t.Fatalf("third relay = %+v, want the last message", sum)
	}
	envs := e.snd.Envelopes()
	order := make([]string, 0, len(envs))
	for _, env := range envs {
		order = append(order, env.DedupeKey)
	}
	if got := strings.Join(order, ","); got != "k1,k2,k3,k4,k5" {
		t.Fatalf("delivery order over the three relays = %s, want k1..k5 (oldest first)", got)
	}
}

// US3.7: Android and iPhone users are relayed identically; routing is by topic only, and the
// service serves every organisation in one relay.
func TestOutbox_BR_TASK_03_RelayChannelAgnosticAndRoutesByTopicAcrossOrgs(t *testing.T) {
	e := newEnv(t)
	e.reset()
	other := e.newOrg("Other")
	nudges := outboxtest.New()
	e.svc.RegisterSender("task.nudge", nudges, 0)

	android, iphone := uuid.New(), uuid.New()
	a := e.clientMsg(testTopic, "k-android", android)
	a.Payload = map[string]string{"platform": "android"}
	i := e.clientMsg(testTopic, "k-iphone", iphone)
	i.Payload = map[string]string{"platform": "ios_pwa"}
	foreign := e.msg(testTopic, "k-foreign")
	foreign.OrgID = other
	nudge := e.msg("task.nudge", "k-nudge")
	e.add(a)
	e.add(i)
	e.add(foreign)
	e.add(nudge)

	if sum := e.relay(); sum.Sent != 4 {
		t.Fatalf("summary = %+v, want 4 sent", sum)
	}
	if e.snd.Count() != 3 || nudges.Count() != 1 {
		t.Fatalf("task.reminder sender got %d, task.nudge sender got %d; want 3 and 1", e.snd.Count(), nudges.Count())
	}
	orgs := map[string]uuid.UUID{}
	for _, env := range e.snd.Envelopes() {
		orgs[env.DedupeKey] = env.OrgID
		if env.Topic != testTopic {
			t.Errorf("topic %s routed to the %s sender", env.Topic, testTopic)
		}
	}
	if orgs["k-foreign"] != other || orgs["k-android"] != e.org || orgs["k-iphone"] != e.org {
		t.Errorf("org ids in envelopes = %v", orgs)
	}
	for _, c := range []struct {
		org        uuid.UUID
		topic, key string
	}{
		{e.org, testTopic, "k-android"}, {e.org, testTopic, "k-iphone"}, {other, testTopic, "k-foreign"}, {e.org, "task.nudge", "k-nudge"},
	} {
		if got := e.rowOf(c.org, c.topic, c.key); got.Status != "sent" || got.Attempts != 1 {
			t.Errorf("%s = %s attempts=%d, want sent/1", c.key, got.Status, got.Attempts)
		}
	}
}

// SC-008: across a whole evening and night, no message that respects quiet hours is ever handed to
// the sender between 21:30 and 08:00 IST; exempt messages go out immediately.
func TestOutbox_BR_TASK_03_RelayNothingIsSentInsideQuietHours_SC008(t *testing.T) {
	e := newEnv(t)
	e.reset()
	start, end := ist(13, 18, 0), ist(14, 12, 0)
	sentAt := map[string]time.Time{}
	e.snd.OnSend(func(_ context.Context, env outbox.Envelope) { sentAt[env.DedupeKey] = e.clk.Now() })

	recordedAt := map[string]time.Time{}
	exempt := map[string]bool{}
	n := 0
	for now := start; !now.After(end); now = now.Add(5 * time.Minute) {
		e.clk.Set(now)
		// A new message every 35 minutes, every fifth one exempt.
		if int(now.Sub(start)/(5*time.Minute))%7 == 0 {
			n++
			key := fmt.Sprintf("m%02d", n)
			m := e.msg(testTopic, key)
			m.ExemptFromQuietHours = n%5 == 0
			exempt[key] = m.ExemptFromQuietHours
			recordedAt[key] = now
			e.add(m)
		}
		e.relay()
	}

	if n < 10 {
		t.Fatalf("scenario recorded only %d messages", n)
	}
	for key, at := range sentAt {
		if !exempt[key] && istdate.DefaultQuietHours.Contains(at) {
			t.Errorf("%s (recorded %v) was handed to the sender at %v, inside quiet hours", key, recordedAt[key], at.In(istdate.IST))
		}
	}
	for key, rec := range recordedAt {
		at, ok := sentAt[key]
		if !ok {
			t.Errorf("%s recorded at %v was never sent by 12:00 the next day", key, rec.In(istdate.IST))
			continue
		}
		if exempt[key] && !at.Equal(rec) {
			t.Errorf("exempt %s recorded %v was sent %v, want immediately", key, rec.In(istdate.IST), at.In(istdate.IST))
		}
		if at.Before(rec) {
			t.Errorf("%s sent before it was recorded", key)
		}
	}
}
