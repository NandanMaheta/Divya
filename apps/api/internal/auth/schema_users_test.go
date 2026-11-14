package auth_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestSchemaUsersBR_CLI_04_StatusModel: the four statuses of the BR-CLI-04 status model are accepted
// and nothing else (data-model.md section 1). Disabled needs status_before_disabled.
func TestSchemaUsersBR_CLI_04_StatusModel(t *testing.T) {
	e := newSchemaEnv(t)
	for _, s := range []string{"pending_verification", "pending_approval", "active"} {
		t.Run("accepts_"+s, func(t *testing.T) {
			wantOK(t, e.insert("users", e.userRow(row{"status": s})))
		})
	}
	t.Run("accepts_disabled_with_previous_status", func(t *testing.T) {
		wantOK(t, e.insert("users", e.userRow(row{"status": "disabled", "status_before_disabled": "active"})))
	})
	for _, s := range []string{"approved", "ACTIVE", "", "suspended", "deleted"} {
		t.Run("rejects_"+fmt.Sprintf("%q", s), func(t *testing.T) {
			_ = wantState(t, e.insert("users", e.userRow(row{"status": s})), check)
		})
	}
	t.Run("rejects_null_status", func(t *testing.T) {
		_ = wantState(t, e.insert("users", e.userRow(row{"status": nil})), notNull)
	})
}

// TestSchemaUsersBR_CLI_04_DisabledPairing: (status = 'disabled') = (status_before_disabled IS NOT NULL).
func TestSchemaUsersBR_CLI_04_DisabledPairing(t *testing.T) {
	e := newSchemaEnv(t)
	t.Run("disabled_without_previous_status", func(t *testing.T) {
		_ = wantState(t, e.insert("users", e.userRow(row{"status": "disabled"})), check)
	})
	for _, s := range []string{"pending_verification", "pending_approval", "active"} {
		t.Run("previous_status_set_while_"+s, func(t *testing.T) {
			_ = wantState(t, e.insert("users", e.userRow(row{"status": s, "status_before_disabled": "active"})), check)
		})
		t.Run("disabled_remembers_"+s, func(t *testing.T) {
			wantOK(t, e.insert("users", e.userRow(row{"status": "disabled", "status_before_disabled": s})))
		})
	}
	t.Run("previous_status_cannot_be_disabled", func(t *testing.T) {
		_ = wantState(t, e.insert("users", e.userRow(row{"status": "disabled", "status_before_disabled": "disabled"})), check)
	})
	t.Run("previous_status_must_be_known", func(t *testing.T) {
		_ = wantState(t, e.insert("users", e.userRow(row{"status": "disabled", "status_before_disabled": "gone"})), check)
	})
	t.Run("enable_clears_previous_status_in_one_update", func(t *testing.T) {
		id := e.newUser(t, row{"status": "disabled", "status_before_disabled": "pending_approval"})
		wantOK(t, e.exec(`UPDATE users SET status = status_before_disabled, status_before_disabled = NULL WHERE id = $1`, id))
	})
	t.Run("update_to_disabled_without_previous_status", func(t *testing.T) {
		id := e.newUser(t, nil)
		_ = wantState(t, e.exec(`UPDATE users SET status = 'disabled' WHERE id = $1`, id), check)
	})
}

func TestSchemaUsersRole(t *testing.T) {
	e := newSchemaEnv(t)
	wantOK(t, e.insert("users", e.userRow(row{"role": "client"})))
	wantOK(t, e.insert("users", e.userRow(row{"role": "admin", "mobile": nil})))
	for _, r := range []string{"owner", "Admin", "", "superuser"} {
		t.Run(fmt.Sprintf("rejects_%q", r), func(t *testing.T) {
			_ = wantState(t, e.insert("users", e.userRow(row{"role": r})), check)
		})
	}
	_ = wantState(t, e.insert("users", e.userRow(row{"role": nil})), notNull)
}

func TestSchemaUsersName(t *testing.T) {
	e := newSchemaEnv(t)
	wantOK(t, e.insert("users", e.userRow(row{"name": "A"})))
	wantOK(t, e.insert("users", e.userRow(row{"name": strings.Repeat("n", 200)})))
	for name, v := range map[string]string{"empty": "", "blank": "   ", "tabs_and_spaces": " \t "} {
		t.Run("rejects_"+name, func(t *testing.T) {
			_ = wantState(t, e.insert("users", e.userRow(row{"name": v})), check)
		})
	}
	t.Run("rejects_201_chars", func(t *testing.T) {
		_ = wantState(t, e.insert("users", e.userRow(row{"name": strings.Repeat("n", 201)})), check)
	})
	t.Run("rejects_null", func(t *testing.T) {
		_ = wantState(t, e.insert("users", e.userRow(row{"name": nil})), notNull)
	})
}

func TestSchemaUsersEmail(t *testing.T) {
	e := newSchemaEnv(t)
	long := strings.Repeat("a", 254-len("@x.io")) + "@x.io"
	if len(long) != 254 {
		t.Fatalf("fixture length %d", len(long))
	}
	wantOK(t, e.insert("users", e.userRow(row{"email": "a@b"})))
	wantOK(t, e.insert("users", e.userRow(row{"email": long})))
	bad := map[string]string{
		"uppercase":         "Asha@Example.com",
		"leading_space":     " asha@example.com",
		"trailing_space":    "asha@example.com ",
		"no_at_sign":        "asha.example.com",
		"at_in_first_place": "@example.com",
		"too_short":         "a@",
		"empty":             "",
		"255_chars":         strings.Repeat("a", 255-len("@x.io")) + "@x.io",
	}
	for name, v := range bad {
		t.Run("rejects_"+name, func(t *testing.T) {
			_ = wantState(t, e.insert("users", e.userRow(row{"email": v})), check)
		})
	}
	t.Run("rejects_null", func(t *testing.T) {
		_ = wantState(t, e.insert("users", e.userRow(row{"email": nil})), notNull)
	})
}

func TestSchemaUsersMobile(t *testing.T) {
	e := newSchemaEnv(t)
	for _, m := range []string{"+916000000000", "+917123456789", "+918123456789", "+919876543210"} {
		t.Run("accepts_"+m, func(t *testing.T) {
			wantOK(t, e.insert("users", e.userRow(row{"mobile": m})))
		})
	}
	bad := map[string]string{
		"no_country_code":  "9876543210",
		"leading_zero":     "09876543210",
		"starts_with_5":    "+915876543210",
		"starts_with_0":    "+910876543210",
		"nine_digits":      "+91987654321",
		"eleven_digits":    "+9198765432101",
		"other_country":    "+449876543210",
		"spaces":           "+91 9876543210",
		"letters":          "+91987654321a",
		"trailing_newline": "+919876543210\n",
	}
	for name, v := range bad {
		t.Run("rejects_"+name, func(t *testing.T) {
			_ = wantState(t, e.insert("users", e.userRow(row{"mobile": v})), check)
		})
	}
}

// TestSchemaUsersMobileRules: clients must have a mobile; admins may not; verified implies present.
func TestSchemaUsersMobileRules(t *testing.T) {
	e := newSchemaEnv(t)
	t.Run("client_without_mobile_rejected", func(t *testing.T) {
		_ = wantState(t, e.insert("users", e.userRow(row{"role": "client", "mobile": nil})), check)
	})
	t.Run("admin_without_mobile_accepted", func(t *testing.T) {
		wantOK(t, e.insert("users", e.userRow(row{"role": "admin", "mobile": nil})))
	})
	t.Run("admin_with_mobile_accepted", func(t *testing.T) {
		wantOK(t, e.insert("users", e.userRow(row{"role": "admin"})))
	})
	t.Run("mobile_verified_without_mobile_rejected", func(t *testing.T) {
		_ = wantState(t, e.insert("users", e.userRow(row{"role": "admin", "mobile": nil, "mobile_verified_at": schemaT0})), check)
	})
	t.Run("mobile_verified_with_mobile_accepted", func(t *testing.T) {
		wantOK(t, e.insert("users", e.userRow(row{"mobile_verified_at": schemaT0, "email_verified_at": schemaT0})))
	})
	t.Run("clearing_mobile_while_verified_rejected", func(t *testing.T) {
		id := e.newUser(t, row{"role": "admin", "mobile_verified_at": schemaT0})
		_ = wantState(t, e.exec(`UPDATE users SET mobile = NULL WHERE id = $1`, id), check)
	})
}

func TestSchemaUsersPasswordHash(t *testing.T) {
	e := newSchemaEnv(t)
	for name, v := range map[string]string{
		"plaintext":    "correct horse battery staple",
		"bcrypt":       "$2a$10$abcdefghijklmnopqrstuuabcdefghijklmnopqrstuvwxyz01234",
		"argon2i":      "$argon2i$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
		"argon2d":      "$argon2d$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
		"empty":        "",
		"prefix_after": " $argon2id$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA",
	} {
		t.Run("rejects_"+name, func(t *testing.T) {
			_ = wantState(t, e.insert("users", e.userRow(row{"password_hash": v})), check)
		})
	}
	t.Run("accepts_argon2id_phc", func(t *testing.T) {
		wantOK(t, e.insert("users", e.userRow(row{"password_hash": validPHC})))
	})
}

func TestSchemaUsersNotNull(t *testing.T) {
	e := newSchemaEnv(t)
	for _, col := range []string{"org_id", "role", "status", "name", "email", "password_hash", "password_changed_at", "created_at", "updated_at"} {
		t.Run(col, func(t *testing.T) {
			_ = wantState(t, e.insert("users", e.userRow(row{col: nil})), notNull)
		})
	}
	for _, col := range []string{"email_verified_at", "mobile_verified_at", "last_sign_in_at", "status_before_disabled"} {
		t.Run("nullable_"+col, func(t *testing.T) {
			var nullable string
			err := e.pool.QueryRow(t.Context(),
				`SELECT is_nullable FROM information_schema.columns WHERE table_name = 'users' AND column_name = $1`, col).Scan(&nullable)
			if err != nil {
				t.Fatal(err)
			}
			if nullable != "YES" {
				t.Fatalf("users.%s must be nullable", col)
			}
		})
	}
}

func TestSchemaUsersOrgForeignKey(t *testing.T) {
	e := newSchemaEnv(t)
	_ = wantState(t, e.insert("users", e.userRow(row{"org_id": uuid.New()})), foreignKey)
}

// TestSchemaUsersEmailUnique: email is unique across the whole system, not per org (D-18).
func TestSchemaUsersEmailUnique(t *testing.T) {
	e := newSchemaEnv(t)
	e.newUser(t, row{"email": "asha@example.com"})
	t.Run("same_org", func(t *testing.T) {
		wantUnique(t, e.insert("users", e.userRow(row{"email": "asha@example.com"})), "users_email_key")
	})
	t.Run("other_org", func(t *testing.T) {
		other := uuid.New()
		e.mustExec(t, `INSERT INTO orgs (id, name) VALUES ($1, 'Other')`, other)
		wantUnique(t, e.insert("users", e.userRow(row{"org_id": other, "email": "asha@example.com"})), "users_email_key")
	})
	t.Run("case_variant_cannot_be_stored", func(t *testing.T) {
		_ = wantState(t, e.insert("users", e.userRow(row{"email": "Asha@Example.com"})), check)
	})
}

func TestSchemaUsersMobileUnique(t *testing.T) {
	e := newSchemaEnv(t)
	e.newUser(t, row{"mobile": "+919876500001"})
	t.Run("duplicate_rejected", func(t *testing.T) {
		wantUnique(t, e.insert("users", e.userRow(row{"mobile": "+919876500001"})), "users_mobile_key")
	})
	t.Run("many_admins_without_mobile", func(t *testing.T) {
		wantOK(t, e.insert("users", e.userRow(row{"role": "admin", "mobile": nil})))
		wantOK(t, e.insert("users", e.userRow(row{"role": "admin", "mobile": nil})))
	})
}
