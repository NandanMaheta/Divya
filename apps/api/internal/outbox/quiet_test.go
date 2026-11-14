package outbox_test

// T034 - quiet hours (US8.1-8.8; FR-031; SC-008; BR-TASK-03; BR-SET-08 helpers are tested in istdate).
// US8.9 (jobs are never delayed by quiet hours) is a scheduler behaviour and is covered with the tick tests.
//
// Test name prefixes decide which implementation task makes them pass:
//   TestQuietHours_BR_TASK_03_*              settings only (QuietHours, SetQuietHours + audit)      -> T035
//   TestQuietHoursAvailability_BR_TASK_03_*  available_at computed by Add                           -> T036/T037
//   TestQuietHoursRelay_BR_TASK_03_*         what the relay hands out around 21:30 and 08:00        -> T037
//
// Assumption: SetQuietHours and QuietHours may be called without a transaction (they open their
// own, joining an outer one when present), because the contract does not say "requires a
// transaction in ctx" for them, unlike Add/Withdraw. The first change's audit "before" is the
// default window, which is what applied until then (data-model section 3).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"pgregory.net/rapid"

	"github.com/NandanMaheta/swastik/apps/api/internal/outbox"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
)

func qh(startH, startM, endH, endM int) istdate.QuietHours {
	return istdate.QuietHours{
		Start: istdate.WallClock{Hour: startH, Minute: startM},
		End:   istdate.WallClock{Hour: endH, Minute: endM},
	}
}

// quietAudit returns the quiet_hours audit rows of an org, oldest first.
func (e *env) quietAudit(org uuid.UUID) []auditRow {
	e.t.Helper()
	rows, err := e.pool.Query(e.t.Context(), `SELECT org_id, actor_id, actor_kind, entity, entity_id, action, before, after
		FROM audit_log WHERE org_id = $1 AND entity = 'quiet_hours' ORDER BY created_at, id`, org)
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
		if before != nil {
			_ = json.Unmarshal(before, &r.Before)
		}
		if after != nil {
			_ = json.Unmarshal(after, &r.After)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *env) quietRows(org uuid.UUID) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.t.Context(), `SELECT count(*) FROM quiet_hours WHERE org_id = $1`, org).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) setQuiet(actor audit.Actor, org uuid.UUID, q istdate.QuietHours) {
	e.t.Helper()
	if err := e.svc.SetQuietHours(e.t.Context(), actor, org, q); err != nil {
		e.t.Fatalf("SetQuietHours(%v): %v", q, err)
	}
}

func (e *env) quiet(org uuid.UUID) istdate.QuietHours {
	e.t.Helper()
	q, err := e.svc.QuietHours(e.t.Context(), org)
	if err != nil {
		e.t.Fatalf("QuietHours: %v", err)
	}
	return q
}

// ---------------------------------------------------------------------------------------------
// settings

func TestQuietHours_BR_TASK_03_DefaultsToTwentyOneThirtyUntilEightIST(t *testing.T) {
	e := newEnv(t)
	e.reset()
	if got := e.quiet(e.org); got != istdate.DefaultQuietHours || got != qh(21, 30, 8, 0) {
		t.Fatalf("QuietHours = %+v, want 21:30-08:00", got)
	}
	if n := e.quietRows(e.org); n != 0 {
		t.Fatalf("reading the default created %d rows; a missing row means the default (no seeding)", n)
	}
}

func TestQuietHours_BR_TASK_03_SetStoresTheWindowAndWritesAnAuditEntry(t *testing.T) {
	e := newEnv(t)
	e.reset()
	admin := audit.Actor{ID: uuid.New(), Kind: "admin"}

	e.setQuiet(admin, e.org, qh(22, 0, 7, 30))
	if got := e.quiet(e.org); got != qh(22, 0, 7, 30) {
		t.Fatalf("QuietHours after Set = %+v", got)
	}
	var start, end int
	if err := e.pool.QueryRow(t.Context(), `SELECT start_minute, end_minute FROM quiet_hours WHERE org_id = $1`, e.org).Scan(&start, &end); err != nil {
		t.Fatalf("read quiet_hours: %v", err)
	}
	if start != 22*60 || end != 7*60+30 {
		t.Errorf("stored minutes = %d..%d, want 1320..450", start, end)
	}

	e.setQuiet(admin, e.org, qh(23, 0, 6, 0)) // a second change replaces the row
	if e.quietRows(e.org) != 1 {
		t.Fatal("a second change must update the single row of the org")
	}

	rows := e.quietAudit(e.org)
	if len(rows) != 2 {
		t.Fatalf("audit rows = %d, want one per change", len(rows))
	}
	first, second := rows[0], rows[1]
	if first.Action != "quiet_hours.update" || first.Entity != "quiet_hours" || first.ActorKind != "admin" ||
		first.ActorID == nil || *first.ActorID != admin.ID || first.EntityID == nil || *first.EntityID != e.org {
		t.Errorf("audit identity = %+v", first)
	}
	want := func(s, en int) map[string]any {
		return map[string]any{"start_minute": float64(s), "end_minute": float64(en)}
	}
	if !reflect.DeepEqual(first.Before, want(21*60+30, 8*60)) || !reflect.DeepEqual(first.After, want(22*60, 7*60+30)) {
		t.Errorf("first change before/after = %v / %v", first.Before, first.After)
	}
	if !reflect.DeepEqual(second.Before, want(22*60, 7*60+30)) || !reflect.DeepEqual(second.After, want(23*60, 6*60)) {
		t.Errorf("second change before/after = %v / %v", second.Before, second.After)
	}
}

func TestQuietHours_BR_TASK_03_SetIsAtomicWithItsAuditAndWithTheCallersTransaction(t *testing.T) {
	e := newEnv(t)
	e.reset()
	admin := audit.Actor{ID: uuid.New(), Kind: "admin"}

	err := e.inTx(func(ctx context.Context) error {
		if err := e.svc.SetQuietHours(ctx, admin, e.org, qh(22, 0, 7, 0)); err != nil {
			t.Errorf("SetQuietHours: %v", err)
		}
		return errChange
	})
	if !errors.Is(err, errChange) {
		t.Fatalf("InTx = %v", err)
	}
	if got := e.quiet(e.org); got != istdate.DefaultQuietHours {
		t.Errorf("window = %+v after a rolled back change, want the default", got)
	}
	if n := len(e.quietAudit(e.org)); n != 0 {
		t.Errorf("%d audit rows survived the rollback (constitution II: audit commits with the change)", n)
	}
	if e.quietRows(e.org) != 0 {
		t.Error("quiet_hours row survived the rollback")
	}
}

func TestQuietHours_BR_TASK_03_SetRejectsInvalidWindows(t *testing.T) {
	e := newEnv(t)
	admin := audit.Actor{ID: uuid.New(), Kind: "admin"}
	tests := []struct {
		name string
		q    istdate.QuietHours
		ok   bool
	}{
		{"start equals end", qh(8, 0, 8, 0), false},
		{"hour 24", qh(24, 0, 8, 0), false},
		{"minute 60", qh(21, 60, 8, 0), false},
		{"negative hour", qh(-1, 0, 8, 0), false},
		{"end hour 24", qh(21, 30, 24, 0), false},
		{"negative minute", qh(21, 30, 8, -1), false},
		{"wrapping window", qh(21, 30, 8, 0), true},
		{"non wrapping window", qh(13, 0, 14, 0), true},
		{"midnight to last minute", qh(0, 0, 23, 59), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e.reset()
			err := e.svc.SetQuietHours(t.Context(), admin, e.org, tt.q)
			if tt.ok {
				if err != nil {
					t.Fatalf("valid window rejected: %v", err)
				}
				return
			}
			if !errors.Is(err, outbox.ErrInvalid) {
				t.Fatalf("SetQuietHours(%+v) = %v, want ErrInvalid", tt.q, err)
			}
			if e.quietRows(e.org) != 0 || len(e.quietAudit(e.org)) != 0 {
				t.Error("an invalid window left a row or an audit entry")
			}
		})
	}
}

func TestQuietHours_BR_TASK_03_SettingsAreKeptPerOrganisation(t *testing.T) {
	e := newEnv(t)
	e.reset()
	other := e.newOrg("Other")
	e.setQuiet(audit.Actor{ID: uuid.New(), Kind: "admin"}, e.org, qh(13, 0, 14, 0))

	if got := e.quiet(e.org); got != qh(13, 0, 14, 0) {
		t.Errorf("org window = %+v", got)
	}
	if got := e.quiet(other); got != istdate.DefaultQuietHours {
		t.Errorf("other org window = %+v, want the default", got)
	}
}

// ---------------------------------------------------------------------------------------------
// available_at computed on Add

func TestQuietHoursAvailability_BR_TASK_03_AddComputesAvailableAt(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		name   string
		at     time.Time
		window *istdate.QuietHours // nil = the default
		exempt bool
		want   time.Time
	}{
		{"US8.1 recorded 21:29 is available now", ist(13, 21, 29), nil, false, ist(13, 21, 29)},
		{"21:29:59 is still outside", ist(13, 21, 29).Add(59 * time.Second), nil, false, ist(13, 21, 29).Add(59 * time.Second)},
		{"US8.2 recorded 21:30 Friday waits for 08:00 Saturday", ist(13, 21, 30), nil, false, ist(14, 8, 0)},
		{"23:59 Friday waits for 08:00 Saturday", ist(13, 23, 59), nil, false, ist(14, 8, 0)},
		{"00:00 Saturday waits for 08:00 the same day", ist(14, 0, 0), nil, false, ist(14, 8, 0)},
		{"US8.3 recorded 02:00 waits for 08:00 the same day", ist(14, 2, 0), nil, false, ist(14, 8, 0)},
		{"07:59 waits one minute", ist(14, 7, 59), nil, false, ist(14, 8, 0)},
		{"US8.4 recorded exactly 08:00 is available now", ist(14, 8, 0), nil, false, ist(14, 8, 0)},
		{"midday is available now", ist(14, 12, 0), nil, false, ist(14, 12, 0)},
		{"it applies every day: Saturday 22:00 waits for Sunday 08:00", ist(14, 22, 0), nil, false, ist(15, 8, 0)},
		{"US8.5 an exempt message recorded 23:00 is available now", ist(13, 23, 0), nil, true, ist(13, 23, 0)},
		{"an exempt message recorded at 02:00 is available now", ist(14, 2, 0), nil, true, ist(14, 2, 0)},
		{"custom window: inside waits for its end", ist(14, 13, 30), ptr(qh(13, 0, 14, 0)), false, ist(14, 14, 0)},
		{"custom window: its start is inclusive", ist(14, 13, 0), ptr(qh(13, 0, 14, 0)), false, ist(14, 14, 0)},
		{"custom window: its end is exclusive", ist(14, 14, 0), ptr(qh(13, 0, 14, 0)), false, ist(14, 14, 0)},
		{"custom window: the default night no longer applies", ist(14, 2, 0), ptr(qh(13, 0, 14, 0)), false, ist(14, 2, 0)},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e.reset()
			if tt.window != nil {
				e.setQuiet(audit.Actor{ID: uuid.New(), Kind: "admin"}, e.org, *tt.window)
			}
			e.clk.Set(tt.at)
			m := e.msg(testTopic, fmt.Sprintf("k%d", i))
			m.ExemptFromQuietHours = tt.exempt
			e.add(m)
			if got := e.row(testTopic, m.DedupeKey).AvailableAt; !got.Equal(tt.want) {
				t.Fatalf("available_at = %v, want %v", got.In(istdate.IST), tt.want.In(istdate.IST))
			}
		})
	}
}

// US8.8: a settings change applies to messages recorded afterwards; messages already deferred keep their time.
func TestQuietHoursAvailability_BR_TASK_03_ASettingsChangeOnlyAffectsNewMessages(t *testing.T) {
	e := newEnv(t)
	e.reset()
	other := e.newOrg("Other")
	e.clk.Set(ist(13, 22, 0))

	e.add(e.msg(testTopic, "old")) // default window: deferred to Saturday 08:00
	oldWant := ist(14, 8, 0)
	if got := e.row(testTopic, "old").AvailableAt; !got.Equal(oldWant) {
		t.Fatalf("precondition: old available_at = %v, want %v", got, oldWant)
	}

	e.setQuiet(audit.Actor{ID: uuid.New(), Kind: "admin"}, e.org, qh(23, 0, 6, 0))
	e.clk.Set(ist(13, 22, 30))
	e.add(e.msg(testTopic, "new"))
	foreign := e.msg(testTopic, "foreign")
	foreign.OrgID = other
	e.add(foreign)

	if got := e.row(testTopic, "new").AvailableAt; !got.Equal(ist(13, 22, 30)) {
		t.Errorf("new message available_at = %v, want now (22:30 is outside the new window)", got.In(istdate.IST))
	}
	if got := e.row(testTopic, "old").AvailableAt; !got.Equal(oldWant) {
		t.Errorf("old message available_at = %v, want it untouched at %v", got.In(istdate.IST), oldWant.In(istdate.IST))
	}
	if got := e.rowOf(other, testTopic, "foreign").AvailableAt; !got.Equal(ist(14, 8, 0)) {
		t.Errorf("other org message available_at = %v, want the default window's 08:00", got.In(istdate.IST))
	}
}

// SC-008 as a property: for any instant and any valid window, the computed available_at is not
// before now, is outside the window, is now itself when now is outside, and is never a day away.
func TestQuietHoursAvailability_BR_TASK_03_PropertyNeverAvailableInsideTheWindow(t *testing.T) {
	e := newEnv(t)
	e.reset()
	admin := audit.Actor{ID: uuid.New(), Kind: "admin"}
	from := ist(8, 0, 0)
	counter := 0

	rapid.Check(t, func(rt *rapid.T) {
		start := rapid.IntRange(0, 1439).Draw(rt, "start")
		end := rapid.IntRange(0, 1439).Filter(func(v int) bool { return v != start }).Draw(rt, "end")
		offset := rapid.Int64Range(0, 14*24*3600-1).Draw(rt, "offsetSeconds")
		exempt := rapid.Bool().Draw(rt, "exempt")
		q := qh(start/60, start%60, end/60, end%60)
		now := from.Add(time.Duration(offset) * time.Second)

		e.clk.Set(now)
		if err := e.svc.SetQuietHours(t.Context(), admin, e.org, q); err != nil {
			rt.Fatalf("SetQuietHours(%+v): %v", q, err)
		}
		counter++
		m := e.msg(testTopic, fmt.Sprintf("p%d", counter))
		m.ExemptFromQuietHours = exempt
		e.add(m)
		got := e.row(testTopic, m.DedupeKey).AvailableAt

		switch {
		case exempt || !q.Contains(now):
			if !got.Equal(now) {
				rt.Fatalf("window %+v now %v exempt=%v: available_at = %v, want now", q, now.In(istdate.IST), exempt, got.In(istdate.IST))
			}
		default:
			if q.Contains(got) {
				rt.Fatalf("window %+v now %v: available_at %v is inside the window (SC-008)", q, now.In(istdate.IST), got.In(istdate.IST))
			}
			if got.Before(now) || got.Sub(now) > 24*time.Hour {
				rt.Fatalf("window %+v now %v: available_at %v is not within the next 24 h", q, now.In(istdate.IST), got.In(istdate.IST))
			}
		}
	})
}

// ---------------------------------------------------------------------------------------------
// relay around the boundaries

func TestQuietHoursRelay_BR_TASK_03_MessagesWaitForEightAndGoOutOnTheNextTick(t *testing.T) {
	e := newEnv(t)

	t.Run("US8.2 recorded 21:30 Friday: ticks before 08:00 relay nothing, the 08:00 tick relays it", func(t *testing.T) {
		e.reset()
		e.clk.Set(ist(13, 21, 30))
		e.add(e.msg(testTopic, "k"))
		for _, at := range []time.Time{ist(13, 21, 35), ist(13, 23, 55), ist(14, 3, 0), ist(14, 7, 59)} {
			e.clk.Set(at)
			if e.relay(); e.snd.Count() != 0 {
				t.Fatalf("relayed at %v, inside quiet hours", at.In(istdate.IST))
			}
		}
		e.clk.Set(ist(14, 8, 0))
		if sum := e.relay(); sum.Sent != 1 {
			t.Fatalf("08:00 tick = %+v, want the message", sum)
		}
	})

	t.Run("US8.1 recorded 21:29: the next tick relays it", func(t *testing.T) {
		e.reset()
		e.clk.Set(ist(13, 21, 29))
		e.add(e.msg(testTopic, "k"))
		if sum := e.relay(); sum.Sent != 1 {
			t.Fatalf("summary = %+v, want sent", sum)
		}
	})

	t.Run("US8.4 recorded exactly 08:00: the next tick relays it", func(t *testing.T) {
		e.reset()
		e.clk.Set(ist(14, 8, 0))
		e.add(e.msg(testTopic, "k"))
		if sum := e.relay(); sum.Sent != 1 {
			t.Fatalf("summary = %+v, want sent", sum)
		}
	})

	t.Run("US8.5 an exempt message recorded 23:00 is relayed at once", func(t *testing.T) {
		e.reset()
		e.clk.Set(ist(13, 23, 0))
		m := e.msg(testTopic, "k")
		m.ExemptFromQuietHours = true
		e.add(m)
		if sum := e.relay(); sum.Sent != 1 {
			t.Fatalf("summary = %+v, want sent", sum)
		}
	})

	t.Run("US8.6 withdrawn at 23:15 while deferred: nothing is sent at 08:00", func(t *testing.T) {
		e.reset()
		e.clk.Set(ist(13, 23, 0))
		m := e.msg(testTopic, "k")
		m.Subject = "task:7"
		e.add(m)
		e.clk.Set(ist(13, 23, 15))
		if err := e.inTx(func(ctx context.Context) error {
			_, err := e.svc.WithdrawSubject(ctx, e.org, "task:7")
			return err
		}); err != nil {
			t.Fatal(err)
		}
		e.clk.Set(ist(14, 8, 0))
		if sum := e.relay(); sum.Sent != 0 || e.snd.Count() != 0 {
			t.Fatalf("08:00 tick = %+v, want nothing", sum)
		}
		if got := e.row(testTopic, "k").Status; got != "withdrawn" {
			t.Errorf("status = %s, want withdrawn", got)
		}
	})

	t.Run("US8.7 deferred to 08:00 with an expiry of 07:00: expired, not sent", func(t *testing.T) {
		e.reset()
		e.clk.Set(ist(13, 23, 0))
		m := e.msg(testTopic, "k")
		m.ExpiresAt = ptr(ist(14, 7, 0))
		e.add(m)
		e.clk.Set(ist(14, 8, 0))
		sum := e.relay()
		if sum.Expired != 1 || sum.Sent != 0 || e.snd.Count() != 0 {
			t.Fatalf("08:00 tick = %+v, want the message expired and not sent", sum)
		}
		if got := e.row(testTopic, "k").Status; got != "expired" {
			t.Errorf("status = %s, want expired", got)
		}
	})
}
