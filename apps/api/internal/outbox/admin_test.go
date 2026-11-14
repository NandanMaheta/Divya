package outbox_test

// T038 - admin capabilities of the outbox at service level (US6.4-6.9, FR-019, FR-020, SC-007).
// The HTTP layer is Phase B; these tests pin what it will call.
//
// Assumptions (the contract leaves them open, see also helpers_test.go):
//   - FailedFilter has string fields State ("retrying" | "dead" | "" for both) and Topic.
//   - FailedMessage and Page[FailedMessage] marshal to JSON exactly as the FailedMessage /
//     FailedMessagePage schemas of contracts/admin-scheduler.openapi.yaml (snake_case keys, "items"),
//     so the tests inspect them through JSON and never depend on Go field types.
//   - DeadGroup has the fields OrgID uuid.UUID, Count int and Topics []string (distinct, sorted).
//   - Service.CountDead(ctx, org) (int, error).
//   - SweepDead runs inside the caller's transaction and marks the rows it returns (alerted_at = now)
//     there, so a rolled back sweep alerts again on the next tick (contracts/outbox-events.md);
//     messages whose topic starts with "admin." are marked alerted but never returned.
//   - RetryMessage / CancelMessage / SweepDead use the idemKey only to store it in audit "after".
//   - Retry and cancel open their own transaction when called without one and join an outer one.

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/outbox"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
)

var adminActor = audit.Actor{ID: uuid.MustParse("0197a000-0000-7000-8000-00000000ad01"), Kind: "admin"}

// allowedKeys are the properties of the FailedMessage schema; nothing else may be exposed (no payload, tokens, lease data).
var allowedKeys = []string{"id", "topic", "subject", "dedupe_key", "recipient_kind", "recipient_id", "state", "attempts",
	"max_attempts", "first_attempt_at", "last_attempt_at", "next_attempt_at", "expires_at", "last_error", "created_at"}

// seed records a message and forces it into the given state with realistic attempt data.
func (e *env) seed(org uuid.UUID, topic, key, state string) uuid.UUID {
	e.t.Helper()
	m := e.msg(topic, key)
	m.OrgID = org
	m.Payload = map[string]string{"template": "reminder", "secret": "hunter2"}
	e.add(m)
	where := ` WHERE org_id = $1 AND topic = $2 AND dedupe_key = $3`
	args := []any{org, topic, key}
	switch state {
	case "dead":
		e.exec(`UPDATE outbox_messages SET status='dead', attempts=8, first_attempt_at=$4, last_attempt_at=$5,
			last_error='push gateway timeout', finished_at=$5, locked_by='host-1'`+where, append(args, t0.Add(-2*time.Hour), t0.Add(-time.Hour))...)
	case "retrying":
		e.exec(`UPDATE outbox_messages SET attempts=3, first_attempt_at=$4, last_attempt_at=$5, last_error='boom', available_at=$6`+where,
			append(args, t0.Add(-3*time.Hour), t0.Add(-30*time.Minute), t0.Add(20*time.Minute))...)
	case "pending":
	case "sent":
		e.exec(`UPDATE outbox_messages SET status='sent', attempts=1, sent_at=$4, finished_at=$4`+where, append(args, t0)...)
	case "withdrawn", "expired":
		e.exec(`UPDATE outbox_messages SET status=$5, finished_at=$4`+where, append(args, t0, state)...)
	case "sending":
		e.exec(`UPDATE outbox_messages SET status='sending', attempts=1, locked_until=$4, lock_token=gen_random_uuid()`+where, append(args, t0.Add(time.Minute))...)
	default:
		e.t.Fatalf("unknown seed state %q", state)
	}
	return e.rowOf(org, topic, key).ID
}

func (e *env) list(org uuid.UUID, f outbox.FailedFilter) []map[string]any {
	e.t.Helper()
	page, err := e.svc.ListFailedMessages(e.t.Context(), org, f)
	if err != nil {
		e.t.Fatalf("ListFailedMessages(%+v): %v", f, err)
	}
	return asItems(e.t, page)
}

func asItems(t *testing.T, page any) []map[string]any {
	t.Helper()
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("marshal page: %v", err)
	}
	var shape struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatalf("page is not a FailedMessagePage: %v (%s)", err, raw)
	}
	return shape.Items
}

func asItem(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal item: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("item is not a FailedMessage: %v (%s)", err, raw)
	}
	return m
}

func jsonTime(t *testing.T, item map[string]any, key string) *time.Time {
	t.Helper()
	v, ok := item[key]
	if !ok || v == nil {
		return nil
	}
	s, isStr := v.(string)
	if !isStr {
		t.Fatalf("%s = %v, want an RFC 3339 string or null", key, v)
	}
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("%s = %q: %v", key, s, err)
	}
	return &ts
}

func (e *env) auditFor(id uuid.UUID) []auditRow {
	e.t.Helper()
	rows, err := e.pool.Query(e.t.Context(), `SELECT org_id, actor_id, actor_kind, entity, entity_id, action, before, after
		FROM audit_log WHERE entity_id = $1 ORDER BY created_at, id`, id)
	if err != nil {
		e.t.Fatalf("read audit: %v", err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		var before, after []byte
		if err := rows.Scan(&r.Org, &r.ActorID, &r.ActorKind, &r.Entity, &r.EntityID, &r.Action, &before, &after); err != nil {
			e.t.Fatal(err)
		}
		_ = json.Unmarshal(before, &r.Before)
		_ = json.Unmarshal(after, &r.After)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *env) retry(org, id uuid.UUID, idem string) (map[string]any, error) {
	fm, err := e.svc.RetryMessage(e.t.Context(), adminActor, org, id, idem)
	if err != nil {
		return nil, err
	}
	return asItem(e.t, fm), nil
}

func (e *env) cancel(org, id uuid.UUID, idem string) (map[string]any, error) {
	fm, err := e.svc.CancelMessage(e.t.Context(), adminActor, org, id, idem)
	if err != nil {
		return nil, err
	}
	return asItem(e.t, fm), nil
}

// ---------------------------------------------------------------------------------------------

func TestOutboxAdmin_ListShowsRetryingAndDeadMessagesWithTheRequiredFieldsOnly(t *testing.T) {
	e := newEnv(t)
	e.reset()
	other := e.newOrg("Other")
	client := uuid.New()

	dead := e.clientMsg("task.reminder", "dead-1", client)
	dead.Subject = "task:1"
	e.add(dead)
	e.exec(`UPDATE outbox_messages SET status='dead', attempts=8, first_attempt_at=$1, last_attempt_at=$2, last_error='push gateway timeout',
		finished_at=$2, locked_by='host-1' WHERE dedupe_key='dead-1'`, t0.Add(-2*time.Hour), t0.Add(-time.Hour))
	e.seed(e.org, "task.reminder", "retrying-1", "retrying")
	for _, st := range []string{"pending", "sent", "withdrawn", "expired", "sending"} {
		e.seed(e.org, "task.reminder", st+"-1", st)
	}
	e.seed(other, "task.reminder", "dead-foreign", "dead")

	items := e.list(e.org, outbox.FailedFilter{})
	if len(items) != 2 {
		t.Fatalf("list returned %d items, want the retrying and the dead one only: %v", len(items), items)
	}
	// Newest last attempt first (data-model index: last_attempt_at DESC, id DESC).
	retrying, deadItem := items[0], items[1]
	if retrying["dedupe_key"] != "retrying-1" || deadItem["dedupe_key"] != "dead-1" {
		t.Fatalf("order = %v, %v; want retrying-1 (30 min ago) before dead-1 (1 h ago)", retrying["dedupe_key"], deadItem["dedupe_key"])
	}

	for _, it := range items {
		for _, required := range []string{"id", "topic", "subject", "dedupe_key", "recipient_kind", "state", "attempts", "max_attempts", "created_at"} {
			if v, ok := it[required]; !ok || v == nil {
				t.Errorf("%v: required field %q is missing", it["dedupe_key"], required)
			}
		}
		for k := range it {
			if !slices.Contains(allowedKeys, k) {
				t.Errorf("%v exposes %q, which is not in the FailedMessage schema", it["dedupe_key"], k)
			}
		}
	}

	if deadItem["state"] != "dead" || deadItem["attempts"] != float64(8) || deadItem["topic"] != "task.reminder" ||
		deadItem["subject"] != "task:1" || deadItem["recipient_kind"] != "client" || deadItem["recipient_id"] != client.String() ||
		deadItem["last_error"] != "push gateway timeout" {
		t.Errorf("dead item = %v", deadItem)
	}
	if first, last := jsonTime(t, deadItem, "first_attempt_at"), jsonTime(t, deadItem, "last_attempt_at"); first == nil || !first.Equal(t0.Add(-2*time.Hour)) || last == nil || !last.Equal(t0.Add(-time.Hour)) {
		t.Errorf("dead item attempt times = %v / %v", first, last)
	}
	if next := jsonTime(t, deadItem, "next_attempt_at"); next != nil {
		t.Errorf("a dead item has no next attempt, got %v", next)
	}

	if retrying["state"] != "retrying" || retrying["attempts"] != float64(3) || retrying["last_error"] != "boom" {
		t.Errorf("retrying item = %v", retrying)
	}
	if next := jsonTime(t, retrying, "next_attempt_at"); next == nil || !next.Equal(t0.Add(20*time.Minute)) {
		t.Errorf("retrying next_attempt_at = %v, want %v", next, t0.Add(20*time.Minute))
	}

	raw, _ := json.Marshal(items)
	for _, secret := range []string{"hunter2", "host-1", "lock_token", "payload"} {
		if strings.Contains(strings.ToLower(string(raw)), secret) {
			t.Errorf("the list exposes %q: %s", secret, raw)
		}
	}

	if foreign := e.list(other, outbox.FailedFilter{}); len(foreign) != 1 || foreign[0]["dedupe_key"] != "dead-foreign" {
		t.Errorf("the other org sees %v, want only its own dead message (US6.9)", foreign)
	}
}

func TestOutboxAdmin_ListFiltersByStateAndTopic(t *testing.T) {
	e := newEnv(t)
	e.reset()
	e.seed(e.org, "task.reminder", "d1", "dead")
	e.seed(e.org, "task.nudge", "d2", "dead")
	e.seed(e.org, "task.reminder", "r1", "retrying")

	keys := func(f outbox.FailedFilter) []string {
		items := e.list(e.org, f)
		out := make([]string, 0, len(items))
		for _, it := range items {
			out = append(out, it["dedupe_key"].(string))
		}
		slices.Sort(out)
		return out
	}
	tests := []struct {
		name string
		f    outbox.FailedFilter
		want []string
	}{
		{"no filter lists both states", outbox.FailedFilter{}, []string{"d1", "d2", "r1"}},
		{"dead only", outbox.FailedFilter{State: "dead"}, []string{"d1", "d2"}},
		{"retrying only", outbox.FailedFilter{State: "retrying"}, []string{"r1"}},
		{"topic", outbox.FailedFilter{Topic: "task.nudge"}, []string{"d2"}},
		{"topic and state", outbox.FailedFilter{Topic: "task.reminder", State: "retrying"}, []string{"r1"}},
		{"no match", outbox.FailedFilter{Topic: "task.unknown"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := keys(tt.f); !slices.Equal(got, tt.want) {
				t.Fatalf("keys = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("an unknown state is invalid", func(t *testing.T) {
		if _, err := e.svc.ListFailedMessages(t.Context(), e.org, outbox.FailedFilter{State: "bogus"}); !errors.Is(err, outbox.ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})
}

func TestOutboxAdmin_RetryRequeuesADeadMessageAndWritesAnAuditEntry(t *testing.T) {
	e := newEnv(t)

	t.Run("fresh attempts, runs on the next tick, audited (US6.5, SC-007)", func(t *testing.T) {
		e.reset()
		id := e.seed(e.org, testTopic, "k", "dead")
		e.exec(`UPDATE outbox_messages SET alerted_at = $1 WHERE id = $2`, t0.Add(-time.Hour), id)

		item, err := e.retry(e.org, id, "idem-key-0001")
		if err != nil {
			t.Fatalf("RetryMessage: %v", err)
		}
		if item["state"] != "pending" || item["attempts"] != float64(0) || item["id"] != id.String() {
			t.Errorf("returned item = %v, want the pending item with 0 attempts", item)
		}
		r := e.rowByID(id)
		if r.Status != "pending" || r.Attempts != 0 || !r.AvailableAt.Equal(t0) || r.AlertedAt != nil || r.FinishedAt != nil {
			t.Errorf("row = %s attempts=%d available=%v alerted=%v finished=%v; want pending/0/now/nil/nil", r.Status, r.Attempts, r.AvailableAt, r.AlertedAt, r.FinishedAt)
		}

		rows := e.auditFor(id)
		if len(rows) != 1 {
			t.Fatalf("audit rows = %d, want 1", len(rows))
		}
		a := rows[0]
		if a.Org != e.org || a.Entity != "outbox_message" || a.Action != "outbox_message.retry" || a.ActorKind != "admin" ||
			a.ActorID == nil || *a.ActorID != adminActor.ID || a.EntityID == nil || *a.EntityID != id {
			t.Errorf("audit identity = %+v", a)
		}
		if a.Before["status"] != "dead" || a.Before["attempts"] != float64(8) || a.After["status"] != "pending" || a.After["attempts"] != float64(0) {
			t.Errorf("audit before/after = %v / %v", a.Before, a.After)
		}
		if a.After["idempotency_key"] != "idem-key-0001" {
			t.Errorf("audit after = %v, want the idempotency key stored for replay detection", a.After)
		}

		if sum := e.relay(); sum.Sent != 1 {
			t.Fatalf("next tick = %+v, want the retried message sent", sum)
		}
		if env := e.snd.Envelopes()[0]; env.Attempt != 1 || env.DedupeKey != "k" {
			t.Errorf("envelope = %+v, want attempt 1 with the original key", env)
		}
	})

	t.Run("without an idempotency key none is stored", func(t *testing.T) {
		e.reset()
		id := e.seed(e.org, testTopic, "k", "dead")
		if _, err := e.retry(e.org, id, ""); err != nil {
			t.Fatal(err)
		}
		if _, ok := e.auditFor(id)[0].After["idempotency_key"]; ok {
			t.Error("an empty idempotency key was stored")
		}
	})

	t.Run("quiet hours still apply to the retried message", func(t *testing.T) {
		e.reset()
		id := e.seed(e.org, testTopic, "k", "dead")
		e.clk.Set(ist(13, 22, 0))
		if _, err := e.retry(e.org, id, ""); err != nil {
			t.Fatal(err)
		}
		if got := e.rowByID(id).AvailableAt; !got.Equal(ist(14, 8, 0)) {
			t.Fatalf("available_at = %v, want 08:00 Saturday", got.In(istdate.IST))
		}
	})

	t.Run("expiry still applies: a retried message past its expiry is expired, not sent (US6.7)", func(t *testing.T) {
		e.reset()
		id := e.seed(e.org, testTopic, "k", "dead")
		e.exec(`UPDATE outbox_messages SET expires_at = $1 WHERE id = $2`, t0.Add(-time.Minute), id)
		if _, err := e.retry(e.org, id, ""); err != nil {
			t.Fatalf("retry of an expired message must still be accepted: %v", err)
		}
		sum := e.relay()
		if sum.Expired != 1 || sum.Sent != 0 || len(e.snd.Calls()) != 0 {
			t.Fatalf("next tick = %+v, calls=%d; want expired and never delivered", sum, len(e.snd.Calls()))
		}
		if got := e.rowByID(id).Status; got != "expired" {
			t.Errorf("status = %s, want expired", got)
		}
	})

	t.Run("a message that dies again is eligible for a new dead alert", func(t *testing.T) {
		e.reset()
		id := e.seed(e.org, testTopic, "k", "dead")
		e.exec(`UPDATE outbox_messages SET alerted_at = $1 WHERE id = $2`, t0, id)
		if _, err := e.retry(e.org, id, ""); err != nil {
			t.Fatal(err)
		}
		if e.rowByID(id).AlertedAt != nil {
			t.Fatal("retry did not clear alerted_at")
		}
	})

	t.Run("rolled back with the caller's transaction: still dead, no audit", func(t *testing.T) {
		e.reset()
		id := e.seed(e.org, testTopic, "k", "dead")
		err := e.inTx(func(ctx context.Context) error {
			if _, err := e.svc.RetryMessage(ctx, adminActor, e.org, id, ""); err != nil {
				t.Errorf("RetryMessage: %v", err)
			}
			return errChange
		})
		if !errors.Is(err, errChange) {
			t.Fatalf("InTx = %v", err)
		}
		if got := e.rowByID(id).Status; got != "dead" {
			t.Errorf("status = %s after rollback, want dead", got)
		}
		if n := len(e.auditFor(id)); n != 0 {
			t.Errorf("%d audit rows survived the rollback", n)
		}
	})
}

func TestOutboxAdmin_CancelWithdrawsADeadMessageAndWritesAnAuditEntry(t *testing.T) {
	e := newEnv(t)
	e.reset()
	id := e.seed(e.org, testTopic, "k", "dead")

	item, err := e.cancel(e.org, id, "idem-key-0002")
	if err != nil {
		t.Fatalf("CancelMessage: %v", err)
	}
	if item["state"] != "withdrawn" {
		t.Errorf("returned state = %v, want withdrawn", item["state"])
	}
	r := e.rowByID(id)
	if r.Status != "withdrawn" || r.FinishedAt == nil {
		t.Errorf("row = %s finished=%v", r.Status, r.FinishedAt)
	}
	rows := e.auditFor(id)
	if len(rows) != 1 || rows[0].Action != "outbox_message.cancel" || rows[0].Entity != "outbox_message" ||
		rows[0].ActorID == nil || *rows[0].ActorID != adminActor.ID || rows[0].Before["status"] != "dead" || rows[0].After["status"] != "withdrawn" {
		t.Fatalf("audit = %+v", rows)
	}
	if rows[0].After["idempotency_key"] != "idem-key-0002" {
		t.Errorf("audit after = %v, want the idempotency key", rows[0].After)
	}

	e.clk.Advance(24 * time.Hour)
	if e.relay(); len(e.snd.Calls()) != 0 {
		t.Error("a cancelled message reached the sender")
	}
	if e.add(e.msg(testTopic, "k")) {
		t.Error("cancelling must not free the dedupe key")
	}
}

func TestOutboxAdmin_RetryAndCancelOnlyAcceptDeadMessages(t *testing.T) {
	e := newEnv(t)
	for _, state := range []string{"pending", "retrying", "sending", "sent", "withdrawn", "expired"} {
		for _, action := range []string{"retry", "cancel"} {
			t.Run(action+" of a "+state+" message is a conflict", func(t *testing.T) {
				e.reset()
				id := e.seed(e.org, testTopic, "k", state)
				before := e.rowByID(id)

				var err error
				if action == "retry" {
					_, err = e.retry(e.org, id, "")
				} else {
					_, err = e.cancel(e.org, id, "")
				}
				if !errors.Is(err, outbox.ErrConflict) {
					t.Fatalf("err = %v, want ErrConflict", err)
				}
				after := e.rowByID(id)
				if after.Status != before.Status || after.Attempts != before.Attempts || !after.AvailableAt.Equal(before.AvailableAt) {
					t.Errorf("the message changed: %+v -> %+v", before, after)
				}
				if n := len(e.auditFor(id)); n != 0 {
					t.Errorf("%d audit rows written for a rejected action", n)
				}
			})
		}
	}
}

func TestOutboxAdmin_AMessageOfAnotherOrganisationIsNotFound_US69(t *testing.T) {
	e := newEnv(t)
	e.reset()
	other := e.newOrg("Other")
	id := e.seed(e.org, testTopic, "k", "dead")

	for name, call := range map[string]func() error{
		"retry":                   func() error { _, err := e.retry(other, id, ""); return err },
		"cancel":                  func() error { _, err := e.cancel(other, id, ""); return err },
		"retry of an unknown id":  func() error { _, err := e.retry(e.org, uuid.New(), ""); return err },
		"cancel of an unknown id": func() error { _, err := e.cancel(e.org, uuid.New(), ""); return err },
	} {
		if err := call(); !errors.Is(err, outbox.ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
	if got := e.rowByID(id).Status; got != "dead" {
		t.Errorf("status = %s, want it untouched", got)
	}
	if n := len(e.auditFor(id)); n != 0 {
		t.Errorf("%d audit rows written", n)
	}
}

func TestOutboxAdmin_CountDeadIsPerOrganisation(t *testing.T) {
	e := newEnv(t)
	e.reset()
	other := e.newOrg("Other")
	e.seed(e.org, testTopic, "d1", "dead")
	e.seed(e.org, "task.nudge", "d2", "dead")
	e.seed(e.org, testTopic, "r1", "retrying")
	e.seed(e.org, testTopic, "s1", "sent")
	e.seed(other, testTopic, "d3", "dead")

	for org, want := range map[uuid.UUID]int{e.org: 2, other: 1, uuid.New(): 0} {
		got, err := e.svc.CountDead(t.Context(), org)
		if err != nil || got != want {
			t.Errorf("CountDead(%s) = (%d, %v), want %d", org, got, err, want)
		}
	}
}

func TestOutboxAdmin_SweepDeadGroupsPerOrganisationAndNeverAlertsAboutAlerts(t *testing.T) {
	e := newEnv(t)
	e.reset()
	other := e.newOrg("Other")
	e.seed(e.org, "task.reminder", "d1", "dead")
	e.seed(e.org, "task.reminder", "d2", "dead")
	e.seed(e.org, "task.nudge", "d3", "dead")
	adminID := e.seed(e.org, "admin.jobs_dead", "a1", "dead") // an undeliverable alert must not trigger an alert
	e.seed(e.org, "task.reminder", "r1", "retrying")
	alreadyAlerted := e.seed(e.org, "task.reminder", "d4", "dead")
	e.exec(`UPDATE outbox_messages SET alerted_at = $1 WHERE id = $2`, t0, alreadyAlerted)
	e.seed(other, "payment.overdue", "d5", "dead")

	sweep := func(fail bool) ([]outbox.DeadGroup, error) {
		var groups []outbox.DeadGroup
		err := e.inTx(func(ctx context.Context) error {
			var err error
			groups, err = e.svc.SweepDead(ctx)
			if err == nil && fail {
				return errChange
			}
			return err
		})
		return groups, err
	}
	byOrg := func(gs []outbox.DeadGroup) map[uuid.UUID]outbox.DeadGroup {
		m := map[uuid.UUID]outbox.DeadGroup{}
		for _, g := range gs {
			m[g.OrgID] = g
		}
		return m
	}

	// A failed alert transaction marks nothing: the next tick sweeps the same rows again.
	if _, err := sweep(true); !errors.Is(err, errChange) {
		t.Fatalf("rolled back sweep = %v", err)
	}
	if got := e.count(`alerted_at IS NOT NULL`); got != 1 {
		t.Fatalf("rows marked alerted after a rolled back sweep = %d, want only the pre-existing one", got)
	}

	groups, err := sweep(false)
	if err != nil {
		t.Fatalf("SweepDead: %v", err)
	}
	g := byOrg(groups)
	if len(g) != 2 {
		t.Fatalf("groups = %+v, want one per organisation with unalerted dead messages", groups)
	}
	if a := g[e.org]; a.Count != 3 || !slices.Equal(a.Topics, []string{"task.nudge", "task.reminder"}) {
		t.Errorf("org group = %+v, want 3 messages over topics [task.nudge task.reminder] (admin.* excluded, already alerted excluded)", a)
	}
	if b := g[other]; b.Count != 1 || !slices.Equal(b.Topics, []string{"payment.overdue"}) {
		t.Errorf("other org group = %+v", b)
	}
	if e.rowByID(adminID).AlertedAt == nil {
		t.Error("a dead admin.* message must be marked alerted so it can never alert again")
	}

	// Not repeated on the next tick.
	if again, err := sweep(false); err != nil || len(again) != 0 {
		t.Fatalf("second sweep = (%+v, %v), want nothing", again, err)
	}

	// Retry, die again, alert again.
	id := e.rowOf(e.org, "task.reminder", "d1").ID
	if _, err := e.retry(e.org, id, ""); err != nil {
		t.Fatal(err)
	}
	e.exec(`UPDATE outbox_messages SET status='dead', attempts=8, finished_at=$1 WHERE id = $2`, t0, id)
	again, err := sweep(false)
	if err != nil {
		t.Fatal(err)
	}
	if a := byOrg(again)[e.org]; len(again) != 1 || a.Count != 1 || !slices.Equal(a.Topics, []string{"task.reminder"}) {
		t.Fatalf("sweep after retry and a second death = %+v, want one group of 1 task.reminder", again)
	}
}
