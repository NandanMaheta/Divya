package outbox_test

// T032 - recording and withdrawing messages (US3.1, 3.3, 3.5, 3.7; FR-023, FR-024, FR-027; BR-TASK-03).
// These tests deliberately avoid Relay: they pass as soon as Add/Withdraw exist (T036). Relay
// behaviour lives in relay_test.go. Shared fixtures and assumptions: helpers_test.go.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/outbox"
	"github.com/NandanMaheta/swastik/apps/api/internal/outbox/outboxtest"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
)

var errChange = errors.New("business change failed")

func jsonEqual(t *testing.T, raw []byte, want any) {
	t.Helper()
	var got, exp any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("stored payload %q is not JSON: %v", raw, err)
	}
	wb, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wb, &exp); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, exp) {
		t.Fatalf("payload = %s, want %s", raw, wb)
	}
}

func TestOutbox_BR_TASK_03_AddRecordsAPendingMessageWithTheCallersChange(t *testing.T) {
	e := newEnv(t)

	t.Run("commit keeps the message pending and available now (US3.1)", func(t *testing.T) {
		e.reset()
		client := uuid.New()
		m := e.clientMsg(testTopic, "task:7:slot:2026-11-14T10:00", client)
		m.Subject = "task:7"
		m.ExpiresAt = ptr(t0.Add(time.Hour))
		if !e.add(m) {
			t.Fatal("Add reported inserted=false for a new key")
		}
		r := e.row(testTopic, "task:7:slot:2026-11-14T10:00")
		if r.Status != "pending" || r.Attempts != 0 || r.MaxAttempts != 8 {
			t.Errorf("status=%s attempts=%d max=%d, want pending/0/8", r.Status, r.Attempts, r.MaxAttempts)
		}
		if r.Org != e.org || r.Subject != "task:7" || r.RecipientKind != "client" || r.RecipientID == nil || *r.RecipientID != client {
			t.Errorf("row identity wrong: %+v", r)
		}
		if !r.RespectsQuietHours {
			t.Error("a message must respect quiet hours unless the owner exempts it (FR-031)")
		}
		if !r.AvailableAt.Equal(t0) {
			t.Errorf("available_at = %v, want %v (10:00 IST is outside quiet hours)", r.AvailableAt, t0)
		}
		if r.ExpiresAt == nil || !r.ExpiresAt.Equal(t0.Add(time.Hour)) {
			t.Errorf("expires_at = %v, want %v", r.ExpiresAt, t0.Add(time.Hour))
		}
		jsonEqual(t, r.Payload, m.Payload)
		if r.SentAt != nil || r.FinishedAt != nil {
			t.Errorf("a new message has sent_at=%v finished_at=%v", r.SentAt, r.FinishedAt)
		}
	})

	t.Run("an admin broadcast has no recipient id", func(t *testing.T) {
		e.reset()
		e.add(e.msg(testTopic, "k-admin"))
		r := e.row(testTopic, "k-admin")
		if r.RecipientKind != "admin" || r.RecipientID != nil {
			t.Errorf("recipient = %s/%v, want admin/nil", r.RecipientKind, r.RecipientID)
		}
	})

	t.Run("exempt flag is stored", func(t *testing.T) {
		e.reset()
		m := e.msg(testTopic, "k-exempt")
		m.ExemptFromQuietHours = true
		e.add(m)
		if e.row(testTopic, "k-exempt").RespectsQuietHours {
			t.Error("respects_quiet_hours = true for an exempt message")
		}
	})

	t.Run("rollback of the business change leaves no message (US3.1)", func(t *testing.T) {
		e.reset()
		err := e.inTx(func(ctx context.Context) error {
			if _, err := e.svc.Add(ctx, e.msg(testTopic, "k-rollback")); err != nil {
				return err
			}
			return errChange
		})
		if !errors.Is(err, errChange) {
			t.Fatalf("InTx = %v", err)
		}
		if n := e.count(`true`); n != 0 {
			t.Fatalf("%d messages exist after the rollback", n)
		}
	})

	t.Run("message and business change commit together", func(t *testing.T) {
		e.reset()
		err := e.inTx(func(ctx context.Context) error {
			tx, err := db.MustTx(ctx)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO orgs (name) VALUES ('with-message')`); err != nil {
				return err
			}
			_, err = e.svc.Add(ctx, e.msg(testTopic, "k-together"))
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if n := e.count(`dedupe_key = 'k-together'`); n != 1 {
			t.Fatalf("messages = %d, want 1", n)
		}
	})

	t.Run("without a transaction Add returns ErrNoTx and writes nothing (FR-023)", func(t *testing.T) {
		e.reset()
		inserted, err := e.svc.Add(t.Context(), e.msg(testTopic, "k-notx"))
		if !errors.Is(err, db.ErrNoTx) {
			t.Fatalf("Add without tx = %v, want db.ErrNoTx", err)
		}
		if inserted {
			t.Error("inserted = true together with an error")
		}
		if n := e.count(`true`); n != 0 {
			t.Fatalf("%d messages written without a transaction", n)
		}
	})
}

func TestOutbox_BR_TASK_03_AddRejectsInvalidMessagesWithoutPoisoningTheTransaction(t *testing.T) {
	e := newEnv(t)
	client := uuid.New()

	tests := []struct {
		name   string
		mutate func(m *outbox.Message)
	}{
		{"topic without a dot", func(m *outbox.Message) { m.Topic = "reminder" }},
		{"topic in upper case", func(m *outbox.Message) { m.Topic = "Task.reminder" }},
		{"topic over 64 characters", func(m *outbox.Message) { m.Topic = "a." + strings.Repeat("b", 63) }},
		{"zero org", func(m *outbox.Message) { m.OrgID = uuid.Nil }},
		{"unknown recipient kind", func(m *outbox.Message) { m.Recipient = outbox.Recipient{Kind: "staff"} }},
		{"client without id", func(m *outbox.Message) { m.Recipient = outbox.Recipient{Kind: "client"} }},
		{"empty dedupe key", func(m *outbox.Message) { m.DedupeKey = "" }},
		{"dedupe key over 200 characters", func(m *outbox.Message) { m.DedupeKey = strings.Repeat("k", 201) }},
		{"payload over 8 KB", func(m *outbox.Message) { m.Payload = map[string]string{"x": strings.Repeat("a", 9000)} }},
		{"payload that is not JSON-encodable", func(m *outbox.Message) { m.Payload = make(chan int) }},
		{"negative max attempts", func(m *outbox.Message) { m.MaxAttempts = -1 }},
		{"max attempts over 50", func(m *outbox.Message) { m.MaxAttempts = 51 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e.reset()
			bad := e.msg(testTopic, "k-bad")
			tt.mutate(&bad)
			err := e.inTx(func(ctx context.Context) error {
				inserted, err := e.svc.Add(ctx, bad)
				if !errors.Is(err, outbox.ErrInvalid) {
					t.Errorf("Add = (%v, %v), want ErrInvalid", inserted, err)
				}
				if inserted {
					t.Error("inserted = true for an invalid message")
				}
				// The caller's transaction must still be usable: validation happens before any SQL.
				good := e.clientMsg(testTopic, "k-good", client)
				if ok, err := e.svc.Add(ctx, good); err != nil || !ok {
					t.Errorf("valid Add after the invalid one = (%v, %v); the transaction was poisoned", ok, err)
				}
				return nil
			})
			if err != nil {
				t.Fatalf("outer transaction failed: %v", err)
			}
			if n := e.count(`dedupe_key = 'k-bad'`); n != 0 {
				t.Errorf("invalid message was stored (%d rows)", n)
			}
			if n := e.count(`dedupe_key = 'k-good'`); n != 1 {
				t.Errorf("valid message after the invalid one rows = %d, want 1", n)
			}
		})
	}
}

func TestOutbox_BR_TASK_03_AddAppliesMaxAttemptsFromMessageTopicOrDefault(t *testing.T) {
	e := newEnv(t)
	e.svc.RegisterSender("task.five_tries", outboxtest.New(), 5)

	tests := []struct {
		name  string
		topic string
		max   int
		want  int
	}{
		{"message value wins", "task.five_tries", 3, 3},
		{"topic default from RegisterSender", "task.five_tries", 0, 5},
		{"global default when the topic has no override", testTopic, 0, 8},
		{"topic without a registered sender", "task.unregistered", 0, 8},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e.reset()
			m := e.msg(tt.topic, "k-max-"+string(rune('a'+i)))
			m.MaxAttempts = tt.max
			e.add(m)
			if got := e.row(tt.topic, m.DedupeKey).MaxAttempts; got != tt.want {
				t.Fatalf("max_attempts = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestOutbox_BR_TASK_03_DedupeIgnoresARepeatedKey(t *testing.T) {
	e := newEnv(t)
	const key = "task:7:slot:2026-11-14T10:00"

	t.Run("second Add with the same org, topic and key is silently ignored (US3.3, FR-024)", func(t *testing.T) {
		e.reset()
		first := e.msg(testTopic, key)
		first.Payload = map[string]string{"template": "first"}
		second := e.msg(testTopic, key)
		second.Payload = map[string]string{"template": "second"}

		if !e.add(first) {
			t.Fatal("first Add not inserted")
		}
		var inserted bool
		err := e.inTx(func(ctx context.Context) error {
			var err error
			inserted, err = e.svc.Add(ctx, second)
			return err
		})
		if err != nil {
			t.Fatalf("duplicate Add returned an error %v; it must be silent", err)
		}
		if inserted {
			t.Error("duplicate Add reported inserted=true")
		}
		if n := e.count(`true`); n != 1 {
			t.Fatalf("messages = %d, want 1", n)
		}
		jsonEqual(t, e.row(testTopic, key).Payload, first.Payload)
	})

	t.Run("a duplicate inside the same transaction is ignored and the transaction stays usable", func(t *testing.T) {
		e.reset()
		err := e.inTx(func(ctx context.Context) error {
			if ok, err := e.svc.Add(ctx, e.msg(testTopic, key)); err != nil || !ok {
				t.Errorf("first = (%v, %v)", ok, err)
			}
			if ok, err := e.svc.Add(ctx, e.msg(testTopic, key)); err != nil || ok {
				t.Errorf("duplicate = (%v, %v), want (false, nil)", ok, err)
			}
			if ok, err := e.svc.Add(ctx, e.msg(testTopic, key+":next")); err != nil || !ok {
				t.Errorf("next key = (%v, %v)", ok, err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if n := e.count(`true`); n != 2 {
			t.Fatalf("messages = %d, want 2", n)
		}
	})

	t.Run("the same key under another topic or in another org is a different message", func(t *testing.T) {
		e.reset()
		other := e.newOrg("Other")
		e.add(e.msg(testTopic, key))
		otherTopic := e.msg("task.other", key)
		otherOrg := e.msg(testTopic, key)
		otherOrg.OrgID = other
		if !e.add(otherTopic) || !e.add(otherOrg) {
			t.Fatal("same key under another topic/org was treated as a duplicate")
		}
		if n := e.count(`true`); n != 3 {
			t.Fatalf("messages = %d, want 3", n)
		}
	})

	// FR-024: ignored "for at least as long as messages are retained", whatever happened to the first.
	for _, state := range []struct {
		name string
		sql  string
	}{
		{"sent", `UPDATE outbox_messages SET status='sent', sent_at=$1, finished_at=$1`},
		{"withdrawn", `UPDATE outbox_messages SET status='withdrawn', finished_at=$1`},
		{"expired", `UPDATE outbox_messages SET status='expired', finished_at=$1`},
		{"dead", `UPDATE outbox_messages SET status='dead', attempts=8, finished_at=$1`},
	} {
		t.Run("a repeat is still ignored after the first was "+state.name, func(t *testing.T) {
			e.reset()
			e.add(e.msg(testTopic, key))
			e.exec(state.sql, t0)
			var inserted bool
			err := e.inTx(func(ctx context.Context) error {
				var err error
				inserted, err = e.svc.Add(ctx, e.msg(testTopic, key))
				return err
			})
			if err != nil || inserted {
				t.Fatalf("Add after %s = (%v, %v), want (false, nil)", state.name, inserted, err)
			}
			if got := e.row(testTopic, key).Status; got != state.name {
				t.Errorf("status = %s, want it untouched (%s)", got, state.name)
			}
		})
	}

	t.Run("a repeat after a rolled back first Add is a first Add", func(t *testing.T) {
		e.reset()
		_ = e.inTx(func(ctx context.Context) error {
			_, _ = e.svc.Add(ctx, e.msg(testTopic, key))
			return errChange
		})
		if !e.add(e.msg(testTopic, key)) {
			t.Fatal("key stayed reserved by a rolled back transaction")
		}
	})

	t.Run("concurrent Adds of one key insert exactly one row", func(t *testing.T) {
		e.reset()
		const workers = 6
		var (
			wg       sync.WaitGroup
			mu       sync.Mutex
			inserted int
		)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := e.inTx(func(ctx context.Context) error {
					ok, err := e.svc.Add(ctx, e.msg(testTopic, key))
					if ok {
						mu.Lock()
						inserted++
						mu.Unlock()
					}
					return err
				})
				if err != nil {
					t.Errorf("concurrent Add: %v", err)
				}
			}()
		}
		wg.Wait()
		if inserted != 1 {
			t.Errorf("%d Adds reported inserted=true, want exactly 1", inserted)
		}
		if n := e.count(`true`); n != 1 {
			t.Errorf("rows = %d, want 1", n)
		}
	})
}

func TestOutbox_BR_TASK_03_WithdrawByKeyStopsAPendingMessage(t *testing.T) {
	e := newEnv(t)
	const key = "task:7:slot:1"

	withdraw := func(t *testing.T, org uuid.UUID, topic, k string) int {
		t.Helper()
		var n int
		err := e.inTx(func(ctx context.Context) error {
			var err error
			n, err = e.svc.Withdraw(ctx, org, topic, k)
			return err
		})
		if err != nil {
			t.Fatalf("Withdraw: %v", err)
		}
		return n
	}

	t.Run("a pending message becomes withdrawn and finished (US3.5, FR-027)", func(t *testing.T) {
		e.reset()
		e.add(e.msg(testTopic, key))
		e.clk.Advance(time.Minute)
		if n := withdraw(t, e.org, testTopic, key); n != 1 {
			t.Fatalf("Withdraw = %d, want 1", n)
		}
		r := e.row(testTopic, key)
		if r.Status != "withdrawn" {
			t.Fatalf("status = %s, want withdrawn", r.Status)
		}
		if r.FinishedAt == nil || !r.FinishedAt.Equal(e.clk.Now()) {
			t.Errorf("finished_at = %v, want %v (the injected clock)", r.FinishedAt, e.clk.Now())
		}
		if r.SentAt != nil {
			t.Error("a withdrawn message has sent_at")
		}
	})

	t.Run("a message waiting for a retry is withdrawn too", func(t *testing.T) {
		e.reset()
		e.add(e.msg(testTopic, key))
		e.exec(`UPDATE outbox_messages SET attempts = 2, last_error = 'boom', available_at = $1`, t0.Add(time.Hour))
		if n := withdraw(t, e.org, testTopic, key); n != 1 {
			t.Fatalf("Withdraw = %d, want 1", n)
		}
		if got := e.row(testTopic, key).Status; got != "withdrawn" {
			t.Fatalf("status = %s", got)
		}
	})

	t.Run("nothing to withdraw is not an error (0, nil)", func(t *testing.T) {
		e.reset()
		if n := withdraw(t, e.org, testTopic, "no-such-key"); n != 0 {
			t.Fatalf("Withdraw = %d, want 0", n)
		}
	})

	for _, status := range []string{"sent", "sending", "dead", "expired", "withdrawn"} {
		t.Run("a "+status+" message is left alone", func(t *testing.T) {
			e.reset()
			e.add(e.msg(testTopic, key))
			switch status {
			case "sending":
				e.exec(`UPDATE outbox_messages SET status='sending', attempts=1, locked_until=$1, lock_token=gen_random_uuid()`, t0.Add(time.Minute))
			case "sent":
				e.exec(`UPDATE outbox_messages SET status='sent', sent_at=$1, finished_at=$1`, t0)
			default:
				e.exec(`UPDATE outbox_messages SET status=$2, finished_at=$1`, t0, status)
			}
			if n := withdraw(t, e.org, testTopic, key); n != 0 {
				t.Fatalf("Withdraw = %d, want 0 (at-least-once: an in-flight or finished message is not withdrawn)", n)
			}
			if got := e.row(testTopic, key).Status; got != status {
				t.Fatalf("status = %s, want %s", got, status)
			}
		})
	}

	t.Run("only the exact org, topic and key are touched", func(t *testing.T) {
		e.reset()
		other := e.newOrg("Other")
		e.add(e.msg(testTopic, key))
		e.add(e.msg("task.other", key))
		e.add(e.msg(testTopic, key+"-2"))
		om := e.msg(testTopic, key)
		om.OrgID = other
		e.add(om)

		if n := withdraw(t, e.org, testTopic, key); n != 1 {
			t.Fatalf("Withdraw = %d, want 1", n)
		}
		if got := e.count(`status = 'withdrawn'`); got != 1 {
			t.Fatalf("withdrawn rows = %d, want 1", got)
		}
		if got := e.rowOf(other, testTopic, key).Status; got != "pending" {
			t.Errorf("the other org's message is %s", got)
		}
	})

	t.Run("withdraw rolls back with the caller", func(t *testing.T) {
		e.reset()
		e.add(e.msg(testTopic, key))
		_ = e.inTx(func(ctx context.Context) error {
			if _, err := e.svc.Withdraw(ctx, e.org, testTopic, key); err != nil {
				t.Errorf("Withdraw: %v", err)
			}
			return errChange
		})
		if got := e.row(testTopic, key).Status; got != "pending" {
			t.Fatalf("status = %s after rollback, want pending", got)
		}
	})

	t.Run("stop on completion: withdraw commits with the change that completes the task (BR-TASK-03)", func(t *testing.T) {
		e.reset()
		e.add(e.msg(testTopic, key))
		err := e.inTx(func(ctx context.Context) error {
			tx, err := db.MustTx(ctx)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO orgs (name) VALUES ('task-7-done')`); err != nil { // stands for "mark task done"
				return err
			}
			_, err = e.svc.Withdraw(ctx, e.org, testTopic, key)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := e.row(testTopic, key).Status; got != "withdrawn" {
			t.Fatalf("status = %s, want withdrawn", got)
		}
	})

	t.Run("without a transaction Withdraw returns ErrNoTx", func(t *testing.T) {
		e.reset()
		e.add(e.msg(testTopic, key))
		n, err := e.svc.Withdraw(t.Context(), e.org, testTopic, key)
		if !errors.Is(err, db.ErrNoTx) || n != 0 {
			t.Fatalf("Withdraw without tx = (%d, %v), want (0, ErrNoTx)", n, err)
		}
		if got := e.row(testTopic, key).Status; got != "pending" {
			t.Fatalf("status = %s", got)
		}
	})

	t.Run("a withdrawn key cannot be re-added (dedupe outlives withdrawal)", func(t *testing.T) {
		e.reset()
		e.add(e.msg(testTopic, key))
		withdraw(t, e.org, testTopic, key)
		if e.add(e.msg(testTopic, key)) {
			t.Fatal("re-Add after withdraw inserted a second message")
		}
	})
}

func TestOutbox_BR_TASK_03_WithdrawSubjectStopsEverythingForOneItem(t *testing.T) {
	e := newEnv(t)
	other := e.newOrg("Other")
	client := uuid.New()

	seed := func() {
		e.reset()
		for i, topic := range []string{testTopic, "task.nudge", testTopic} {
			m := e.clientMsg(topic, "k"+string(rune('a'+i)), client)
			m.Subject = "task:7"
			e.add(m)
		}
		unrelated := e.msg(testTopic, "k-other-subject")
		unrelated.Subject = "task:8"
		e.add(unrelated)
		foreign := e.msg(testTopic, "k-other-org")
		foreign.OrgID = other
		foreign.Subject = "task:7"
		e.add(foreign)
	}
	withdrawSubject := func(t *testing.T, org uuid.UUID, subject string) int {
		t.Helper()
		var n int
		err := e.inTx(func(ctx context.Context) error {
			var err error
			n, err = e.svc.WithdrawSubject(ctx, org, subject)
			return err
		})
		if err != nil {
			t.Fatalf("WithdrawSubject: %v", err)
		}
		return n
	}

	t.Run("every pending message of the subject in that org, nothing else (FR-027)", func(t *testing.T) {
		seed()
		if n := withdrawSubject(t, e.org, "task:7"); n != 3 {
			t.Fatalf("WithdrawSubject = %d, want 3", n)
		}
		if got := e.count(`status = 'withdrawn' AND subject = 'task:7' AND org_id = $1`, e.org); got != 3 {
			t.Errorf("withdrawn rows of the subject = %d, want 3", got)
		}
		if got := e.count(`status = 'pending'`); got != 2 {
			t.Errorf("pending rows = %d, want 2 (other subject, other org)", got)
		}
	})

	t.Run("sent messages of the subject stay sent", func(t *testing.T) {
		seed()
		e.exec(`UPDATE outbox_messages SET status='sent', sent_at=$1, finished_at=$1 WHERE dedupe_key = 'ka'`, t0)
		if n := withdrawSubject(t, e.org, "task:7"); n != 2 {
			t.Fatalf("WithdrawSubject = %d, want 2", n)
		}
		if got := e.row(testTopic, "ka").Status; got != "sent" {
			t.Errorf("sent message became %s", got)
		}
	})

	t.Run("unknown subject returns 0 and no error", func(t *testing.T) {
		seed()
		if n := withdrawSubject(t, e.org, "task:999"); n != 0 {
			t.Fatalf("WithdrawSubject = %d, want 0", n)
		}
	})

	t.Run("rolls back with the caller and needs a transaction", func(t *testing.T) {
		seed()
		_ = e.inTx(func(ctx context.Context) error {
			if _, err := e.svc.WithdrawSubject(ctx, e.org, "task:7"); err != nil {
				t.Errorf("WithdrawSubject: %v", err)
			}
			return errChange
		})
		if got := e.count(`status = 'withdrawn'`); got != 0 {
			t.Errorf("%d rows withdrawn after the rollback", got)
		}
		if n, err := e.svc.WithdrawSubject(t.Context(), e.org, "task:7"); !errors.Is(err, db.ErrNoTx) || n != 0 {
			t.Errorf("WithdrawSubject without tx = (%d, %v), want (0, ErrNoTx)", n, err)
		}
	})
}

// US3.7: the outbox knows nothing about devices. Android and iPhone (home-screen web app) users
// produce identical rows; choosing push, web push or e-mail is feature 014.
func TestOutbox_BR_TASK_03_ChannelAgnosticMessagesLookTheSameForEveryPlatform(t *testing.T) {
	e := newEnv(t)
	android, iphone := uuid.New(), uuid.New()

	a := e.clientMsg(testTopic, "k-android", android)
	a.Subject = "task:1"
	a.Payload = map[string]string{"template": "reminder", "platform": "android"}
	i := e.clientMsg(testTopic, "k-iphone", iphone)
	i.Subject = "task:1"
	i.Payload = map[string]string{"template": "reminder", "platform": "ios_pwa"}
	if !e.add(a) || !e.add(i) {
		t.Fatal("a message was not recorded")
	}

	ra, ri := e.row(testTopic, "k-android"), e.row(testTopic, "k-iphone")
	if ra.Status != ri.Status || ra.Attempts != ri.Attempts || ra.MaxAttempts != ri.MaxAttempts ||
		!ra.AvailableAt.Equal(ri.AvailableAt) || ra.RecipientKind != ri.RecipientKind || ra.RespectsQuietHours != ri.RespectsQuietHours {
		t.Errorf("rows differ by platform:\nandroid %+v\niphone  %+v", ra, ri)
	}

	// Several audiences and topics share one mechanism.
	for _, topic := range []string{"ipo.status_changed", "payment.overdue", "admin.jobs_dead"} {
		if !e.add(e.msg(topic, "k-"+topic)) {
			t.Errorf("topic %s not accepted", topic)
		}
	}
}
