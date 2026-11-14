package auth_test

import (
	"context"
	"strings"
	"testing"
)

var authTables = []string{
	"users", "device_sessions", "refresh_tokens", "admin_mfa",
	"mfa_recovery_codes", "mfa_challenges", "auth_throttles",
}

// TestSchemaTablesExist: migration 00003 creates all seven tables (data-model.md sections 1-7).
func TestSchemaTablesExist(t *testing.T) {
	e := newSchemaEnv(t)
	for _, tbl := range authTables {
		t.Run(tbl, func(t *testing.T) {
			var exists bool
			err := e.pool.QueryRow(context.Background(),
				`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)`,
				tbl).Scan(&exists)
			if err != nil {
				t.Fatal(err)
			}
			if !exists {
				t.Fatalf("table %s does not exist", tbl)
			}
		})
	}
}

// TestSchemaEveryTableHasOrgFK: org_id uuid NOT NULL REFERENCES orgs(id) on every table (conventions).
func TestSchemaEveryTableHasOrgFK(t *testing.T) {
	e := newSchemaEnv(t)
	for _, tbl := range authTables {
		t.Run(tbl, func(t *testing.T) {
			var dataType, nullable string
			err := e.pool.QueryRow(context.Background(),
				`SELECT data_type, is_nullable FROM information_schema.columns
				  WHERE table_schema = 'public' AND table_name = $1 AND column_name = 'org_id'`, tbl).Scan(&dataType, &nullable)
			if err != nil {
				t.Fatalf("org_id column: %v", err)
			}
			if dataType != "uuid" || nullable != "NO" {
				t.Fatalf("org_id is %s nullable=%s, want uuid NOT NULL", dataType, nullable)
			}
			var fks int
			err = e.pool.QueryRow(context.Background(),
				`SELECT count(*) FROM pg_constraint c
				  WHERE c.contype = 'f' AND c.conrelid = $1::regclass AND c.confrelid = 'orgs'::regclass`, tbl).Scan(&fks)
			if err != nil {
				t.Fatal(err)
			}
			if fks < 1 {
				t.Fatalf("no foreign key from %s to orgs", tbl)
			}
		})
	}
}

// TestSchemaTimesAreTimestamptz: every *_at column and the array/lock columns use timestamptz.
func TestSchemaTimesAreTimestamptz(t *testing.T) {
	e := newSchemaEnv(t)
	rows, err := e.pool.Query(context.Background(),
		`SELECT table_name, column_name, data_type FROM information_schema.columns
		  WHERE table_schema = 'public' AND table_name = ANY($1)
		    AND (column_name LIKE '%\_at' OR column_name = 'locked_until')`, authTables)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var tbl, col, typ string
		if err := rows.Scan(&tbl, &col, &typ); err != nil {
			t.Fatal(err)
		}
		n++
		if typ != "timestamp with time zone" {
			t.Errorf("%s.%s is %s, want timestamptz", tbl, col, typ)
		}
	}
	if n == 0 {
		t.Fatal("no time columns found (tables missing)")
	}
}

// TestSchemaPrimaryKeysDefaultUUIDv7: surrogate keys default to uuidv7().
func TestSchemaPrimaryKeysDefaultUUIDv7(t *testing.T) {
	e := newSchemaEnv(t)
	for _, tbl := range []string{"users", "device_sessions", "refresh_tokens", "mfa_recovery_codes", "mfa_challenges"} {
		t.Run(tbl, func(t *testing.T) {
			var def *string
			err := e.pool.QueryRow(context.Background(),
				`SELECT column_default FROM information_schema.columns
				  WHERE table_schema = 'public' AND table_name = $1 AND column_name = 'id'`, tbl).Scan(&def)
			if err != nil {
				t.Fatalf("id column: %v", err)
			}
			if def == nil || !strings.Contains(*def, "uuidv7()") {
				t.Fatalf("%s.id default = %v, want uuidv7()", tbl, def)
			}
		})
	}
}

// TestSchemaIndexes: every index in data-model.md exists with the right uniqueness and predicate.
func TestSchemaIndexes(t *testing.T) {
	cases := []struct {
		table, name string
		unique      bool
		columns     []string // substrings expected in the definition, in order of appearance
		predicate   []string // substrings expected after WHERE; empty means not a partial index
	}{
		{"users", "users_email_key", true, []string{"(email)"}, nil},
		{"users", "users_mobile_key", true, []string{"(mobile)"}, []string{"mobile IS NOT NULL"}},
		{"users", "users_org_role_status_idx", false, []string{"(org_id, role, status)"}, nil},
		{"device_sessions", "device_sessions_live_device_key", true, []string{"(user_id, device_id)"}, []string{"ended_at IS NULL"}},
		{"device_sessions", "device_sessions_user_live_idx", false, []string{"(user_id)"}, []string{"ended_at IS NULL"}},
		{"refresh_tokens", "refresh_tokens_hash_key", true, []string{"(token_hash)"}, nil},
		{"refresh_tokens", "refresh_tokens_one_live_key", true, []string{"(session_id)"}, []string{"used_at IS NULL"}},
		{"refresh_tokens", "refresh_tokens_session_idx", false, []string{"(session_id)"}, nil},
		{"mfa_recovery_codes", "mfa_recovery_codes_user_hash_key", true, []string{"(user_id, code_hash)"}, nil},
		{"mfa_recovery_codes", "mfa_recovery_codes_usable_idx", false, []string{"(user_id)"}, []string{"used_at IS NULL", "revoked_at IS NULL"}},
		{"mfa_challenges", "mfa_challenges_expires_idx", false, []string{"(expires_at)"}, nil},
	}
	e := newSchemaEnv(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var isUnique bool
			var def string
			err := e.pool.QueryRow(context.Background(),
				`SELECT i.indisunique, pg_get_indexdef(i.indexrelid)
				   FROM pg_index i JOIN pg_class ic ON ic.oid = i.indexrelid
				  WHERE ic.relname = $1 AND i.indrelid = $2::regclass`, c.name, c.table).Scan(&isUnique, &def)
			if err != nil {
				t.Fatalf("index %s on %s: %v", c.name, c.table, err)
			}
			if isUnique != c.unique {
				t.Errorf("unique = %v, want %v (%s)", isUnique, c.unique, def)
			}
			for _, s := range c.columns {
				if !strings.Contains(def, s) {
					t.Errorf("definition %q lacks %q", def, s)
				}
			}
			where := ""
			if i := strings.Index(def, " WHERE "); i >= 0 {
				where = def[i:]
			}
			if len(c.predicate) == 0 && where != "" {
				t.Errorf("index must not be partial, got %q", where)
			}
			for _, s := range c.predicate {
				if !strings.Contains(where, s) {
					t.Errorf("predicate %q lacks %q", where, s)
				}
			}
		})
	}
}

// TestSchemaAuthThrottlesHasNoUserFK: unknown identifiers need rows too, so the only FK is to orgs (section 7).
func TestSchemaAuthThrottlesHasNoUserFK(t *testing.T) {
	e := newSchemaEnv(t)
	var toUsers, total int
	err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FILTER (WHERE confrelid = 'users'::regclass), count(*)
		   FROM pg_constraint WHERE contype = 'f' AND conrelid = 'auth_throttles'::regclass`).Scan(&toUsers, &total)
	if err != nil {
		t.Fatal(err)
	}
	if toUsers != 0 {
		t.Fatalf("auth_throttles has %d FK(s) to users, want none", toUsers)
	}
	if total != 1 {
		t.Fatalf("auth_throttles has %d FKs, want exactly one (orgs)", total)
	}
}
