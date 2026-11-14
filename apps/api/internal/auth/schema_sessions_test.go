package auth_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

var endReasons = []string{
	"signed_out", "signed_out_everywhere", "revoked_by_user", "revoked_by_admin", "ended_by_admin",
	"refresh_reuse", "device_mismatch", "password_changed", "password_reset", "mfa_replaced",
	"disabled", "replaced", "ended_by_feature", "expired",
}

func TestSchemaDeviceSessionsEnums(t *testing.T) {
	e := newSchemaEnv(t)
	user := e.newUser(t, nil)

	for _, p := range []string{"android", "iphone", "web"} {
		t.Run("platform_accepts_"+p, func(t *testing.T) {
			wantOK(t, e.insert("device_sessions", e.sessionRow(user, row{"platform": p})))
		})
	}
	for _, p := range []string{"ios", "Android", "", "desktop"} {
		t.Run(fmt.Sprintf("platform_rejects_%q", p), func(t *testing.T) {
			_ = wantState(t, e.insert("device_sessions", e.sessionRow(user, row{"platform": p})), check)
		})
	}
	for _, c := range []string{"mobile", "web"} {
		t.Run("client_type_accepts_"+c, func(t *testing.T) {
			wantOK(t, e.insert("device_sessions", e.sessionRow(user, row{"client_type": c})))
		})
	}
	for _, c := range []string{"native", "Web", ""} {
		t.Run(fmt.Sprintf("client_type_rejects_%q", c), func(t *testing.T) {
			_ = wantState(t, e.insert("device_sessions", e.sessionRow(user, row{"client_type": c})), check)
		})
	}
	for _, m := range []string{"totp", "recovery_code", "enrolment"} {
		t.Run("mfa_method_accepts_"+m, func(t *testing.T) {
			wantOK(t, e.insert("device_sessions", e.sessionRow(user, row{"mfa_method": m})))
		})
	}
	t.Run("mfa_method_null_accepted", func(t *testing.T) {
		wantOK(t, e.insert("device_sessions", e.sessionRow(user, row{"mfa_method": nil})))
	})
	for _, m := range []string{"sms", "TOTP", "", "password"} {
		t.Run(fmt.Sprintf("mfa_method_rejects_%q", m), func(t *testing.T) {
			_ = wantState(t, e.insert("device_sessions", e.sessionRow(user, row{"mfa_method": m})), check)
		})
	}
}

func TestSchemaDeviceSessionsDeviceName(t *testing.T) {
	e := newSchemaEnv(t)
	user := e.newUser(t, nil)
	wantOK(t, e.insert("device_sessions", e.sessionRow(user, row{"device_name": "x"})))
	wantOK(t, e.insert("device_sessions", e.sessionRow(user, row{"device_name": strings.Repeat("d", 100)})))
	_ = wantState(t, e.insert("device_sessions", e.sessionRow(user, row{"device_name": ""})), check)
	_ = wantState(t, e.insert("device_sessions", e.sessionRow(user, row{"device_name": strings.Repeat("d", 101)})), check)
	_ = wantState(t, e.insert("device_sessions", e.sessionRow(user, row{"device_name": nil})), notNull)
}

// TestSchemaDeviceSessionsEndReason: the end_reason list is closed, and ended_at and end_reason are
// set together or not at all.
func TestSchemaDeviceSessionsEndReason(t *testing.T) {
	e := newSchemaEnv(t)
	user := e.newUser(t, nil)
	for _, r := range endReasons {
		t.Run("accepts_"+r, func(t *testing.T) {
			wantOK(t, e.insert("device_sessions", e.sessionRow(user, row{"ended_at": schemaT0, "end_reason": r})))
		})
	}
	for _, r := range []string{"logout", "SIGNED_OUT", "", "timeout"} {
		t.Run(fmt.Sprintf("rejects_%q", r), func(t *testing.T) {
			_ = wantState(t, e.insert("device_sessions", e.sessionRow(user, row{"ended_at": schemaT0, "end_reason": r})), check)
		})
	}
	t.Run("ended_at_without_reason", func(t *testing.T) {
		_ = wantState(t, e.insert("device_sessions", e.sessionRow(user, row{"ended_at": schemaT0})), check)
	})
	t.Run("reason_without_ended_at", func(t *testing.T) {
		_ = wantState(t, e.insert("device_sessions", e.sessionRow(user, row{"end_reason": "signed_out"})), check)
	})
	t.Run("live_session_has_neither", func(t *testing.T) {
		wantOK(t, e.insert("device_sessions", e.sessionRow(user, nil)))
	})
	t.Run("end_in_one_update", func(t *testing.T) {
		id := e.newSession(t, user, nil)
		wantOK(t, e.exec(`UPDATE device_sessions SET ended_at = $2, end_reason = 'signed_out' WHERE id = $1`, id, schemaT0))
	})
	t.Run("end_without_reason_in_update", func(t *testing.T) {
		id := e.newSession(t, user, nil)
		_ = wantState(t, e.exec(`UPDATE device_sessions SET ended_at = $2 WHERE id = $1`, id, schemaT0), check)
	})
}

func TestSchemaDeviceSessionsNotNull(t *testing.T) {
	e := newSchemaEnv(t)
	user := e.newUser(t, nil)
	for _, col := range []string{"org_id", "user_id", "device_id", "device_name", "platform", "client_type",
		"created_at", "last_active_at", "idle_expires_at", "absolute_expires_at"} {
		t.Run(col, func(t *testing.T) {
			_ = wantState(t, e.insert("device_sessions", e.sessionRow(user, row{col: nil})), notNull)
		})
	}
}

func TestSchemaDeviceSessionsForeignKeys(t *testing.T) {
	e := newSchemaEnv(t)
	user := e.newUser(t, nil)
	t.Run("user", func(t *testing.T) {
		_ = wantState(t, e.insert("device_sessions", e.sessionRow(uuid.New(), nil)), foreignKey)
	})
	t.Run("org", func(t *testing.T) {
		_ = wantState(t, e.insert("device_sessions", e.sessionRow(user, row{"org_id": uuid.New()})), foreignKey)
	})
	t.Run("user_with_sessions_cannot_be_deleted", func(t *testing.T) {
		e.newSession(t, user, nil)
		_ = wantState(t, e.exec(`DELETE FROM users WHERE id = $1`, user), foreignKey)
	})
}

// TestSchemaDeviceSessionsOneLivePerDevice: at most one live session per (user, device) (D-11); an
// ended session frees the device, and other users or other devices are unaffected.
func TestSchemaDeviceSessionsOneLivePerDevice(t *testing.T) {
	e := newSchemaEnv(t)
	user := e.newUser(t, nil)
	device := uuid.New()
	first := e.newSession(t, user, row{"device_id": device})

	t.Run("second_live_session_same_device_rejected", func(t *testing.T) {
		wantUnique(t, e.insert("device_sessions", e.sessionRow(user, row{"device_id": device})), "device_sessions_live_device_key")
	})
	t.Run("other_device_ok", func(t *testing.T) {
		wantOK(t, e.insert("device_sessions", e.sessionRow(user, nil)))
	})
	t.Run("other_user_same_device_ok", func(t *testing.T) {
		other := e.newUser(t, nil)
		wantOK(t, e.insert("device_sessions", e.sessionRow(other, row{"device_id": device})))
	})
	t.Run("ended_sessions_do_not_count", func(t *testing.T) {
		d := uuid.New()
		wantOK(t, e.insert("device_sessions", e.sessionRow(user, row{"device_id": d, "ended_at": schemaT0, "end_reason": "signed_out"})))
		wantOK(t, e.insert("device_sessions", e.sessionRow(user, row{"device_id": d, "ended_at": schemaT0, "end_reason": "replaced"})))
		wantOK(t, e.insert("device_sessions", e.sessionRow(user, row{"device_id": d})))
	})
	t.Run("replace_flow_end_then_insert", func(t *testing.T) {
		wantOK(t, e.exec(`UPDATE device_sessions SET ended_at = $2, end_reason = 'replaced' WHERE id = $1`, first, schemaT0))
		wantOK(t, e.insert("device_sessions", e.sessionRow(user, row{"device_id": device})))
	})
}

// ---- refresh_tokens ----

func TestSchemaRefreshTokensHashLength(t *testing.T) {
	e := newSchemaEnv(t)
	sid := e.newSession(t, e.newUser(t, nil), nil)
	for _, n := range []int{0, 1, 31, 33, 64} {
		t.Run(fmt.Sprintf("rejects_%d_bytes", n), func(t *testing.T) {
			_ = wantState(t, e.insert("refresh_tokens", e.tokenRow(sid, 1, row{"token_hash": make([]byte, n)})), check)
		})
	}
	t.Run("accepts_32_bytes", func(t *testing.T) {
		wantOK(t, e.insert("refresh_tokens", e.tokenRow(sid, 1, nil)))
	})
	t.Run("rejects_null", func(t *testing.T) {
		_ = wantState(t, e.insert("refresh_tokens", e.tokenRow(sid, 1, row{"token_hash": nil})), notNull)
	})
}

func TestSchemaRefreshTokensNotNull(t *testing.T) {
	e := newSchemaEnv(t)
	sid := e.newSession(t, e.newUser(t, nil), nil)
	for _, col := range []string{"org_id", "session_id", "issued_at"} {
		t.Run(col, func(t *testing.T) {
			_ = wantState(t, e.insert("refresh_tokens", e.tokenRow(sid, 1, row{col: nil})), notNull)
		})
	}
}

func TestSchemaRefreshTokensHashUniqueAcrossFamilies(t *testing.T) {
	e := newSchemaEnv(t)
	user := e.newUser(t, nil)
	s1 := e.newSession(t, user, nil)
	s2 := e.newSession(t, user, nil)
	e.newToken(t, s1, 7, nil)
	wantUnique(t, e.insert("refresh_tokens", e.tokenRow(s2, 7, nil)), "refresh_tokens_hash_key")
	wantUnique(t, e.insert("refresh_tokens", e.tokenRow(s1, 7, row{"used_at": schemaT0})), "refresh_tokens_hash_key")
}

// TestSchemaRefreshTokensOneLivePerFamily: at most one unused credential per session (plan P-03).
func TestSchemaRefreshTokensOneLivePerFamily(t *testing.T) {
	e := newSchemaEnv(t)
	user := e.newUser(t, nil)
	s1 := e.newSession(t, user, nil)
	s2 := e.newSession(t, user, nil)
	r1 := e.newToken(t, s1, 1, nil)

	t.Run("second_unused_in_same_family_rejected", func(t *testing.T) {
		wantUnique(t, e.insert("refresh_tokens", e.tokenRow(s1, 2, nil)), "refresh_tokens_one_live_key")
	})
	t.Run("unused_in_other_family_ok", func(t *testing.T) {
		wantOK(t, e.insert("refresh_tokens", e.tokenRow(s2, 3, nil)))
	})
	t.Run("many_used_tokens_in_family_ok", func(t *testing.T) {
		wantOK(t, e.insert("refresh_tokens", e.tokenRow(s1, 4, row{"used_at": schemaT0})))
		wantOK(t, e.insert("refresh_tokens", e.tokenRow(s1, 5, row{"used_at": schemaT0})))
	})
	t.Run("rotation_mark_used_then_insert_next", func(t *testing.T) {
		next := uuid.New()
		wantOK(t, e.exec(`UPDATE refresh_tokens SET used_at = $2 WHERE id = $1`, r1, schemaT0.Add(time.Hour)))
		wantOK(t, e.insert("refresh_tokens", e.tokenRow(s1, 6, row{"id": next})))
		wantOK(t, e.exec(`UPDATE refresh_tokens SET replaced_by = $2 WHERE id = $1`, r1, next))
	})
}

func TestSchemaRefreshTokensReplacedBy(t *testing.T) {
	e := newSchemaEnv(t)
	user := e.newUser(t, nil)
	sid := e.newSession(t, user, nil)
	other := e.newToken(t, sid, 1, row{"used_at": schemaT0})

	t.Run("replaced_by_needs_used_at", func(t *testing.T) {
		_ = wantState(t, e.insert("refresh_tokens", e.tokenRow(sid, 2, row{"replaced_by": other})), check)
	})
	t.Run("replaced_by_with_used_at_ok", func(t *testing.T) {
		wantOK(t, e.insert("refresh_tokens", e.tokenRow(sid, 3, row{"used_at": schemaT0, "replaced_by": other})))
	})
	t.Run("replaced_by_must_exist", func(t *testing.T) {
		_ = wantState(t, e.insert("refresh_tokens", e.tokenRow(sid, 4, row{"used_at": schemaT0, "replaced_by": uuid.New()})), foreignKey)
	})
	t.Run("unused_token_cannot_gain_replaced_by", func(t *testing.T) {
		live := e.newToken(t, sid, 5, nil)
		_ = wantState(t, e.exec(`UPDATE refresh_tokens SET replaced_by = $2 WHERE id = $1`, live, other), check)
	})
}

func TestSchemaRefreshTokensForeignKeys(t *testing.T) {
	e := newSchemaEnv(t)
	sid := e.newSession(t, e.newUser(t, nil), nil)
	t.Run("session", func(t *testing.T) {
		_ = wantState(t, e.insert("refresh_tokens", e.tokenRow(uuid.New(), 1, nil)), foreignKey)
	})
	t.Run("org", func(t *testing.T) {
		_ = wantState(t, e.insert("refresh_tokens", e.tokenRow(sid, 2, row{"org_id": uuid.New()})), foreignKey)
	})
	t.Run("session_with_tokens_cannot_be_deleted", func(t *testing.T) {
		e.newToken(t, sid, 3, nil)
		_ = wantState(t, e.exec(`DELETE FROM device_sessions WHERE id = $1`, sid), foreignKey)
	})
}
