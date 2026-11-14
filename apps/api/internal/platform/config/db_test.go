// Tests for config.LoadDB (T012; plan P-13, P-14): the bootstrap CLI needs only the database.
//
// API assumption: config.LoadDB() returns (X, error) where X has the string fields Env and
// DatabaseURL, like Config. It reads only DATABASE_URL and APP_ENV.
package config_test

import (
	"testing"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/config"
)

func TestLoadDB_ReadsOnlyDatabaseURLAndEnv(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://u:p@h/db", "APP_ENV": "production"})
	cfg, err := config.LoadDB()
	if err != nil {
		t.Fatalf("LoadDB: %v", err)
	}
	if cfg.DatabaseURL != "postgres://u:p@h/db" || cfg.Env != "production" {
		t.Errorf("LoadDB = %+v", cfg)
	}
}

func TestLoadDB_NeedsNoKeysEvenInProduction(t *testing.T) {
	// No JWT, PII, blind-index or origin variables: Load would refuse in production, LoadDB must not.
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://x", "APP_ENV": "production"})
	if _, err := config.Load(); err == nil {
		t.Fatal("precondition: Load must fail closed in production without keys")
	}
	if _, err := config.LoadDB(); err != nil {
		t.Fatalf("LoadDB must not need auth keys: %v", err)
	}
}

func TestLoadDB_IgnoresInvalidAuthVariables(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL": "postgres://x", "APP_ENV": "staging",
		"JWT_SIGNING_KEYS": "garbage", "PII_ENCRYPTION_KEYS": "garbage", "BLIND_INDEX_KEY": "garbage",
		"WEB_ORIGINS": "*", "TRUSTED_PROXY_HOPS": "minus", "ARGON2_MAX_CONCURRENCY": "lots",
	})
	if _, err := config.LoadDB(); err != nil {
		t.Fatalf("LoadDB parsed an auth variable: %v", err)
	}
}

func TestLoadDB_DefaultsEnvToLocal(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://x"})
	cfg, err := config.LoadDB()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Env != "local" {
		t.Errorf("Env = %q, want local", cfg.Env)
	}
}

func TestLoadDB_Validation(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"missing database url", map[string]string{"APP_ENV": "local"}},
		{"unknown env", map[string]string{"DATABASE_URL": "postgres://x", "APP_ENV": "dev"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.env)
			if _, err := config.LoadDB(); err == nil {
				t.Fatal("LoadDB accepted invalid input")
			}
		})
	}
}
