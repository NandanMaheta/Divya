package config

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		addr    string
	}{
		{name: "defaults", env: map[string]string{"DATABASE_URL": "postgres://x"}, addr: ":8080"},
		{name: "cloud run port", env: map[string]string{"DATABASE_URL": "postgres://x", "PORT": "9090"}, addr: ":9090"},
		{name: "missing database url", env: map[string]string{}, wantErr: true},
		{name: "unknown env", env: map[string]string{"DATABASE_URL": "postgres://x", "APP_ENV": "dev"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"DATABASE_URL", "PORT", "APP_ENV", "HTTP_ADDR"} {
				t.Setenv(k, "")
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			cfg, err := Load()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && cfg.HTTPAddr != tt.addr {
				t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, tt.addr)
			}
		})
	}
}

// Tick settings (feature 002, FR-003, US2.5; plan.md section Configuration).
//
// Assumption (contract is silent on field names): Load() fills a nested struct
//
//	cfg.Tick.Auth            string  // "oidc" | "dev"
//	cfg.Tick.Audience        string  // TICK_AUDIENCE
//	cfg.Tick.ServiceAccount  string  // TICK_SERVICE_ACCOUNT
//	cfg.Tick.DevToken        string  // TICK_DEV_TOKEN
//
// Operational meaning (not tested here, wired in main): oidc without audience or service account
// disables the tick; an empty dev token rejects every tick.

var tickEnvKeys = []string{"DATABASE_URL", "PORT", "APP_ENV", "HTTP_ADDR", "TICK_AUTH", "TICK_AUDIENCE", "TICK_SERVICE_ACCOUNT", "TICK_DEV_TOKEN", "K_SERVICE"}

func loadWith(t *testing.T, env map[string]string) (Config, error) {
	t.Helper()
	for _, k := range tickEnvKeys {
		t.Setenv(k, "")
	}
	// Feature 003 requires auth keys in staging/production; give every case a valid set so these
	// tick tests exercise only the tick settings .
	key := func(b byte) string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)) }
	for k, v := range map[string]string{
		"JWT_SIGNING_KEYS": "k1:" + key(0x5A), "JWT_ACTIVE_KID": "k1",
		"PII_ENCRYPTION_KEYS": "1:" + key(0x5B), "PII_ACTIVE_KEY_ID": "1",
		"BLIND_INDEX_KEY": key(0x5C), "WEB_ORIGINS": "https://admin.example.in",
	} {
		t.Setenv(k, v)
	}
	t.Setenv("DATABASE_URL", "postgres://x")
	for k, v := range env {
		t.Setenv(k, v)
	}
	return Load()
}

func TestLoad_TickAuthDefaultsPerAppEnv(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"APP_ENV unset defaults to local, so dev", map[string]string{}, "dev"},
		{"local", map[string]string{"APP_ENV": "local"}, "dev"},
		{"ci", map[string]string{"APP_ENV": "ci"}, "dev"},
		{"staging", map[string]string{"APP_ENV": "staging"}, "oidc"},
		{"production", map[string]string{"APP_ENV": "production"}, "oidc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := loadWith(t, tt.env)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.Tick.Auth != tt.want {
				t.Fatalf("Tick.Auth = %q, want %q", cfg.Tick.Auth, tt.want)
			}
		})
	}
}

func TestLoad_TickAuthExplicitValues(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    string
		wantErr bool
	}{
		{"oidc explicit in local", map[string]string{"APP_ENV": "local", "TICK_AUTH": "oidc"}, "oidc", false},
		{"oidc explicit in ci", map[string]string{"APP_ENV": "ci", "TICK_AUTH": "oidc"}, "oidc", false},
		{"dev explicit in local", map[string]string{"APP_ENV": "local", "TICK_AUTH": "dev"}, "dev", false},
		{"dev explicit in ci", map[string]string{"APP_ENV": "ci", "TICK_AUTH": "dev"}, "dev", false},
		{"oidc explicit in staging", map[string]string{"APP_ENV": "staging", "TICK_AUTH": "oidc"}, "oidc", false},
		{"oidc explicit in production", map[string]string{"APP_ENV": "production", "TICK_AUTH": "oidc"}, "oidc", false},
		// US2.5: staging and production refuse to start with the test-only trigger.
		{"dev refused in staging", map[string]string{"APP_ENV": "staging", "TICK_AUTH": "dev"}, "", true},
		{"dev refused in production", map[string]string{"APP_ENV": "production", "TICK_AUTH": "dev"}, "", true},
		{"dev refused in staging even with a token", map[string]string{"APP_ENV": "staging", "TICK_AUTH": "dev", "TICK_DEV_TOKEN": "t"}, "", true},
		{"invalid value", map[string]string{"APP_ENV": "local", "TICK_AUTH": "bogus"}, "", true},
		{"invalid value in production", map[string]string{"APP_ENV": "production", "TICK_AUTH": "none"}, "", true},
		{"invalid value: oauth", map[string]string{"APP_ENV": "local", "TICK_AUTH": "oauth"}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := loadWith(t, tt.env)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && cfg.Tick.Auth != tt.want {
				t.Fatalf("Tick.Auth = %q, want %q", cfg.Tick.Auth, tt.want)
			}
		})
	}
}

func TestLoad_TickDevRefusalNamesTheSettingButNeverTheToken(t *testing.T) {
	const token = "super-secret-dev-token-value"
	for _, env := range []string{"staging", "production"} {
		_, err := loadWith(t, map[string]string{"APP_ENV": env, "TICK_AUTH": "dev", "TICK_DEV_TOKEN": token})
		if err == nil {
			t.Fatalf("%s: Load() accepted TICK_AUTH=dev", env)
		}
		if !strings.Contains(err.Error(), "TICK_AUTH") {
			t.Errorf("%s: error %q does not name TICK_AUTH", env, err)
		}
		if strings.Contains(err.Error(), token) {
			t.Errorf("%s: error leaks the dev token: %q", env, err)
		}
	}
}

func TestLoad_TickValuesAreRead(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{
		"APP_ENV":              "production",
		"TICK_AUTH":            "oidc",
		"TICK_AUDIENCE":        "https://swastik-api-abc123-el.a.run.app",
		"TICK_SERVICE_ACCOUNT": "scheduler@swastik-prod.iam.gserviceaccount.com",
		"TICK_DEV_TOKEN":       "ignored-in-oidc-mode",
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Tick.Audience != "https://swastik-api-abc123-el.a.run.app" {
		t.Errorf("Tick.Audience = %q", cfg.Tick.Audience)
	}
	if cfg.Tick.ServiceAccount != "scheduler@swastik-prod.iam.gserviceaccount.com" {
		t.Errorf("Tick.ServiceAccount = %q", cfg.Tick.ServiceAccount)
	}
	if cfg.Tick.DevToken != "ignored-in-oidc-mode" {
		t.Errorf("Tick.DevToken = %q", cfg.Tick.DevToken)
	}
}

func TestLoad_TickAudienceServiceAccountAndDevTokenDefaultToEmpty(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{"APP_ENV": "local"})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Tick.DevToken != "" || cfg.Tick.Audience != "" || cfg.Tick.ServiceAccount != "" {
		t.Fatalf("Tick = %+v, want empty audience, service account and dev token (empty dev token rejects every tick)", cfg.Tick)
	}
}

// oidc without audience or service account is allowed: the tick is disabled (every call 401)
// and a startup warning is logged, so deploys do not break before the scheduler job exists.
func TestLoad_TickOIDCWithoutAudienceOrServiceAccountIsAllowed(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"staging, both empty", map[string]string{"APP_ENV": "staging"}},
		{"production, both empty", map[string]string{"APP_ENV": "production"}},
		{"production, audience only", map[string]string{"APP_ENV": "production", "TICK_AUDIENCE": "https://x"}},
		{"production, service account only", map[string]string{"APP_ENV": "production", "TICK_SERVICE_ACCOUNT": "s@x.iam.gserviceaccount.com"}},
		{"explicit oidc, both empty", map[string]string{"APP_ENV": "production", "TICK_AUTH": "oidc"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := loadWith(t, tt.env)
			if err != nil {
				t.Fatalf("Load() error = %v, want nil (tick disabled, service still starts)", err)
			}
			if cfg.Tick.Auth != "oidc" {
				t.Fatalf("Tick.Auth = %q, want oidc", cfg.Tick.Auth)
			}
		})
	}
}

// The tick settings must not disturb the existing validation.
func TestLoad_TickSettingsDoNotRelaxExistingValidation(t *testing.T) {
	if _, err := loadWith(t, map[string]string{"APP_ENV": "dev"}); err == nil {
		t.Error("unknown APP_ENV accepted")
	}
	for _, k := range tickEnvKeys {
		t.Setenv(k, "")
	}
	if _, err := Load(); err == nil {
		t.Error("missing DATABASE_URL accepted")
	}
}

// Cloud Run sets K_SERVICE. A missing APP_ENV there must not fall back to "local" (which would
// default the tick to unauthenticated dev mode), and dev auth is never allowed on Cloud Run.
func TestLoad_CloudRunGuards(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"K_SERVICE set, APP_ENV unset", map[string]string{"K_SERVICE": "swastik-api"}},
		{"K_SERVICE set, APP_ENV empty", map[string]string{"K_SERVICE": "swastik-api", "APP_ENV": ""}},
		{"K_SERVICE set, TICK_AUTH=dev, APP_ENV local", map[string]string{"K_SERVICE": "swastik-api", "APP_ENV": "local", "TICK_AUTH": "dev"}},
		{"K_SERVICE set, TICK_AUTH=dev, APP_ENV ci", map[string]string{"K_SERVICE": "swastik-api", "APP_ENV": "ci", "TICK_AUTH": "dev"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := loadWith(t, tt.env); err == nil {
				t.Fatal("Load() succeeded, want an error on Cloud Run")
			}
		})
	}
	if _, err := loadWith(t, map[string]string{"K_SERVICE": "swastik-api", "APP_ENV": "production"}); err != nil {
		t.Fatalf("Load() on Cloud Run with APP_ENV=production: %v", err)
	}
}
