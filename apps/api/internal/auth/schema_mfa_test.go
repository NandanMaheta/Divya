package auth_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ---- admin_mfa ----

func TestSchemaAdminMFA(t *testing.T) {
	e := newSchemaEnv(t)
	secret := []byte("envelope-bytes")

	t.Run("fresh_row_has_no_secret_and_step_zero", func(t *testing.T) {
		admin := e.newAdmin(t)
		e.mustInsert(t, "admin_mfa", e.mfaRow(admin, nil))
		var step int64
		var enc []byte
		err := e.pool.QueryRow(context.Background(),
			`SELECT last_used_step, secret_enc FROM admin_mfa WHERE user_id = $1`, admin).Scan(&step, &enc)
		if err != nil {
			t.Fatal(err)
		}
		if step != 0 || enc != nil {
			t.Fatalf("last_used_step=%d secret_enc=%v, want 0 and NULL", step, enc)
		}
	})
	t.Run("one_row_per_user", func(t *testing.T) {
		admin := e.newAdmin(t)
		e.mustInsert(t, "admin_mfa", e.mfaRow(admin, nil))
		wantState(t, e.insert("admin_mfa", e.mfaRow(admin, nil)), unique)
	})
	t.Run("enrolled_needs_secret_and_enrolled_at_together", func(t *testing.T) {
		wantOK(t, e.insert("admin_mfa", e.mfaRow(e.newAdmin(t), row{"secret_enc": secret, "enrolled_at": schemaT0})))
		wantState(t, e.insert("admin_mfa", e.mfaRow(e.newAdmin(t), row{"secret_enc": secret})), check)
		wantState(t, e.insert("admin_mfa", e.mfaRow(e.newAdmin(t), row{"enrolled_at": schemaT0})), check)
	})
	t.Run("pending_needs_secret_and_created_at_together", func(t *testing.T) {
		wantOK(t, e.insert("admin_mfa", e.mfaRow(e.newAdmin(t), row{"pending_secret_enc": secret, "pending_created_at": schemaT0})))
		wantState(t, e.insert("admin_mfa", e.mfaRow(e.newAdmin(t), row{"pending_secret_enc": secret})), check)
		wantState(t, e.insert("admin_mfa", e.mfaRow(e.newAdmin(t), row{"pending_created_at": schemaT0})), check)
	})
	t.Run("replacement_keeps_confirmed_secret_while_pending", func(t *testing.T) {
		wantOK(t, e.insert("admin_mfa", e.mfaRow(e.newAdmin(t), row{
			"secret_enc": secret, "enrolled_at": schemaT0, "pending_secret_enc": []byte("new"), "pending_created_at": schemaT0.Add(time.Hour),
		})))
	})
	t.Run("last_used_step_is_not_null", func(t *testing.T) {
		wantState(t, e.insert("admin_mfa", e.mfaRow(e.newAdmin(t), row{"last_used_step": nil})), notNull)
	})
	t.Run("last_used_step_holds_large_steps", func(t *testing.T) {
		wantOK(t, e.insert("admin_mfa", e.mfaRow(e.newAdmin(t), row{"last_used_step": int64(1) << 40})))
	})
	t.Run("not_null_columns", func(t *testing.T) {
		for _, col := range []string{"user_id", "org_id", "updated_at"} {
			wantState(t, e.insert("admin_mfa", e.mfaRow(e.newAdmin(t), row{col: nil})), notNull)
		}
	})
	t.Run("user_fk", func(t *testing.T) {
		wantState(t, e.insert("admin_mfa", e.mfaRow(uuid.New(), nil)), foreignKey)
	})
	t.Run("org_fk", func(t *testing.T) {
		wantState(t, e.insert("admin_mfa", e.mfaRow(e.newAdmin(t), row{"org_id": uuid.New()})), foreignKey)
	})
}

// ---- mfa_recovery_codes ----

func TestSchemaRecoveryCodes(t *testing.T) {
	e := newSchemaEnv(t)
	admin := e.newAdmin(t)

	t.Run("hash_must_be_32_bytes", func(t *testing.T) {
		for _, n := range []int{0, 16, 31, 33} {
			wantState(t, e.insert("mfa_recovery_codes", e.recoveryRow(admin, 1, row{"code_hash": make([]byte, n)})), check)
		}
		wantOK(t, e.insert("mfa_recovery_codes", e.recoveryRow(admin, 1, nil)))
	})
	t.Run("same_hash_twice_for_one_user_rejected", func(t *testing.T) {
		wantUnique(t, e.insert("mfa_recovery_codes", e.recoveryRow(admin, 1, nil)), "mfa_recovery_codes_user_hash_key")
	})
	t.Run("same_hash_in_another_batch_still_rejected", func(t *testing.T) {
		wantUnique(t, e.insert("mfa_recovery_codes", e.recoveryRow(admin, 1, row{"batch_id": uuid.New()})), "mfa_recovery_codes_user_hash_key")
	})
	t.Run("same_hash_for_another_user_ok", func(t *testing.T) {
		wantOK(t, e.insert("mfa_recovery_codes", e.recoveryRow(e.newAdmin(t), 1, nil)))
	})
	t.Run("used_and_revoked_markers_are_nullable", func(t *testing.T) {
		wantOK(t, e.insert("mfa_recovery_codes", e.recoveryRow(admin, 2, row{"used_at": schemaT0})))
		wantOK(t, e.insert("mfa_recovery_codes", e.recoveryRow(admin, 3, row{"revoked_at": schemaT0})))
	})
	t.Run("not_null_columns", func(t *testing.T) {
		for i, col := range []string{"org_id", "user_id", "batch_id", "code_hash", "created_at"} {
			wantState(t, e.insert("mfa_recovery_codes", e.recoveryRow(admin, byte(10+i), row{col: nil})), notNull)
		}
	})
	t.Run("user_fk", func(t *testing.T) {
		wantState(t, e.insert("mfa_recovery_codes", e.recoveryRow(uuid.New(), 20, nil)), foreignKey)
	})
	t.Run("org_fk", func(t *testing.T) {
		wantState(t, e.insert("mfa_recovery_codes", e.recoveryRow(admin, 21, row{"org_id": uuid.New()})), foreignKey)
	})
}

// ---- mfa_challenges ----

func TestSchemaMFAChallenges(t *testing.T) {
	e := newSchemaEnv(t)
	admin := e.newAdmin(t)

	t.Run("token_hash_must_be_32_bytes", func(t *testing.T) {
		for _, n := range []int{0, 31, 33} {
			wantState(t, e.insert("mfa_challenges", e.challengeRow(admin, 1, row{"token_hash": make([]byte, n)})), check)
		}
	})
	t.Run("token_hash_unique", func(t *testing.T) {
		wantOK(t, e.insert("mfa_challenges", e.challengeRow(admin, 1, nil)))
		pe := wantState(t, e.insert("mfa_challenges", e.challengeRow(e.newAdmin(t), 1, nil)), unique)
		if pe.ConstraintName == "" {
			t.Fatal("unique violation without a constraint name")
		}
	})
	for _, p := range []string{"verify", "enrol"} {
		t.Run("purpose_accepts_"+p, func(t *testing.T) {
			wantOK(t, e.insert("mfa_challenges", e.challengeRow(admin, byte(30+len(p)), row{"purpose": p})))
		})
	}
	for i, p := range []string{"enroll", "login", "", "VERIFY"} {
		t.Run(fmt.Sprintf("purpose_rejects_%q", p), func(t *testing.T) {
			wantState(t, e.insert("mfa_challenges", e.challengeRow(admin, byte(40+i), row{"purpose": p})), check)
		})
	}
	t.Run("platform_and_client_type_enums", func(t *testing.T) {
		for i, p := range []string{"android", "iphone", "web"} {
			wantOK(t, e.insert("mfa_challenges", e.challengeRow(admin, byte(50+i), row{"platform": p})))
		}
		wantState(t, e.insert("mfa_challenges", e.challengeRow(admin, 60, row{"platform": "ios"})), check)
		wantOK(t, e.insert("mfa_challenges", e.challengeRow(admin, 61, row{"client_type": "mobile"})))
		wantState(t, e.insert("mfa_challenges", e.challengeRow(admin, 62, row{"client_type": "native"})), check)
	})
	t.Run("device_name_length", func(t *testing.T) {
		wantOK(t, e.insert("mfa_challenges", e.challengeRow(admin, 70, row{"device_name": strings.Repeat("d", 100)})))
		wantState(t, e.insert("mfa_challenges", e.challengeRow(admin, 71, row{"device_name": ""})), check)
		wantState(t, e.insert("mfa_challenges", e.challengeRow(admin, 72, row{"device_name": strings.Repeat("d", 101)})), check)
	})
	t.Run("consumed_at_nullable", func(t *testing.T) {
		wantOK(t, e.insert("mfa_challenges", e.challengeRow(admin, 80, row{"consumed_at": schemaT0.Add(time.Minute)})))
	})
	t.Run("not_null_columns", func(t *testing.T) {
		for i, col := range []string{"org_id", "user_id", "token_hash", "purpose", "device_id", "device_name",
			"platform", "client_type", "created_at", "expires_at"} {
			wantState(t, e.insert("mfa_challenges", e.challengeRow(admin, byte(90+i), row{col: nil})), notNull)
		}
	})
	t.Run("user_fk", func(t *testing.T) {
		wantState(t, e.insert("mfa_challenges", e.challengeRow(uuid.New(), 120, nil)), foreignKey)
	})
	t.Run("org_fk", func(t *testing.T) {
		wantState(t, e.insert("mfa_challenges", e.challengeRow(admin, 121, row{"org_id": uuid.New()})), foreignKey)
	})
}

// ---- auth_throttles ----

func TestSchemaAuthThrottles(t *testing.T) {
	e := newSchemaEnv(t)

	t.Run("defaults_to_empty_failures_and_no_lock", func(t *testing.T) {
		e.mustInsert(t, "auth_throttles", e.throttleRow(1, nil))
		var n int
		var locked *time.Time
		err := e.pool.QueryRow(context.Background(),
			`SELECT cardinality(failures), locked_until FROM auth_throttles WHERE subject = $1`, digest(1)).Scan(&n, &locked)
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 || locked != nil {
			t.Fatalf("failures=%d locked_until=%v, want 0 and NULL", n, locked)
		}
	})
	t.Run("scope_is_closed_list", func(t *testing.T) {
		wantState(t, e.insert("auth_throttles", e.throttleRow(2, row{"scope": "otp_send"})), check)
		wantState(t, e.insert("auth_throttles", e.throttleRow(2, row{"scope": ""})), check)
		wantState(t, e.insert("auth_throttles", e.throttleRow(2, row{"scope": "SIGNIN"})), check)
		wantState(t, e.insert("auth_throttles", e.throttleRow(2, row{"scope": nil})), notNull)
	})
	t.Run("subject_must_be_32_bytes", func(t *testing.T) {
		for _, n := range []int{0, 8, 31, 33} {
			wantState(t, e.insert("auth_throttles", e.throttleRow(3, row{"subject": make([]byte, n)})), check)
		}
		wantState(t, e.insert("auth_throttles", e.throttleRow(3, row{"subject": nil})), notNull)
	})
	t.Run("failures_capped_at_five", func(t *testing.T) {
		five := []time.Time{schemaT0, schemaT0.Add(time.Second), schemaT0.Add(2 * time.Second), schemaT0.Add(3 * time.Second), schemaT0.Add(4 * time.Second)}
		wantOK(t, e.insert("auth_throttles", e.throttleRow(4, row{"failures": five})))
		six := append(append([]time.Time{}, five...), schemaT0.Add(5*time.Second))
		wantState(t, e.insert("auth_throttles", e.throttleRow(5, row{"failures": six})), check)
	})
	t.Run("failures_cannot_grow_past_five_by_update", func(t *testing.T) {
		e.mustInsert(t, "auth_throttles", e.throttleRow(6, row{"failures": []time.Time{schemaT0, schemaT0, schemaT0, schemaT0, schemaT0}}))
		wantState(t, e.exec(`UPDATE auth_throttles SET failures = array_append(failures, $2::timestamptz) WHERE subject = $1`,
			digest(6), schemaT0), check)
	})
	t.Run("failures_not_null", func(t *testing.T) {
		wantState(t, e.insert("auth_throttles", e.throttleRow(7, row{"failures": nil})), notNull)
	})
	t.Run("locked_until_nullable_and_settable", func(t *testing.T) {
		wantOK(t, e.insert("auth_throttles", e.throttleRow(8, row{"locked_until": schemaT0.Add(15 * time.Minute)})))
	})
	t.Run("primary_key_is_org_scope_subject", func(t *testing.T) {
		wantState(t, e.insert("auth_throttles", e.throttleRow(1, nil)), unique)
		other := uuid.New()
		e.mustExec(t, `INSERT INTO orgs (id, name) VALUES ($1, 'Other')`, other)
		wantOK(t, e.insert("auth_throttles", e.throttleRow(1, row{"org_id": other})))
	})
	t.Run("primary_key_columns", func(t *testing.T) {
		var cols string
		err := e.pool.QueryRow(context.Background(),
			`SELECT string_agg(a.attname, ',' ORDER BY k.ord)
			   FROM pg_index i
			   CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord)
			   JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
			  WHERE i.indrelid = 'auth_throttles'::regclass AND i.indisprimary`).Scan(&cols)
		if err != nil {
			t.Fatal(err)
		}
		if cols != "org_id,scope,subject" {
			t.Fatalf("primary key = %q, want org_id,scope,subject", cols)
		}
	})
	t.Run("not_null_columns", func(t *testing.T) {
		for i, col := range []string{"org_id", "updated_at"} {
			wantState(t, e.insert("auth_throttles", e.throttleRow(byte(20+i), row{col: nil})), notNull)
		}
	})
	t.Run("org_fk", func(t *testing.T) {
		wantState(t, e.insert("auth_throttles", e.throttleRow(30, row{"org_id": uuid.New()})), foreignKey)
	})
	t.Run("unknown_identifier_needs_no_user", func(t *testing.T) {
		wantOK(t, e.insert("auth_throttles", e.throttleRow(31, nil)))
	})
}
