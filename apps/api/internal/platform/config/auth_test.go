// Tests for the auth settings of config.Load (T012; plan P-14, P-13, Deviation 2).
//
// API assumptions (contracts are silent; the implementer must match these names):
//
//	config.Load() still returns (Config, error) and now fills Config.Auth (type AuthConfig):
//	  Auth.JWTSeeds             map[string][]byte  // kid -> decoded 32-byte Ed25519 seed
//	  Auth.JWTActiveKID         string
//	  Auth.PII                  *crypto.Keyring
//	  Auth.BlindIndex           crypto.BlindIndexer
//	  Auth.WebOrigins           []string
//	  Auth.TrustedProxyHops     int
//	  Auth.Argon2MaxConcurrency int
//
// Environment formats (plan P-14): JWT_SIGNING_KEYS="kid:<std base64 of 32-byte seed>[,...]",
// PII_ENCRYPTION_KEYS="<id 1..65535>:<std base64 of 32 bytes>[,...]", BLIND_INDEX_KEY=<std base64 of
// 32 bytes>. "base64" means standard, padded encoding.
//
// Ephemeral keys (local and ci only) are random per Load; the WARN record that announces them goes
// through slog.Default() and never contains key material.
//
// Config and Config.Auth implement slog.LogValuer; a JSON log of either never contains key material
// in base64 (standard or URL), hex, or raw form.
package config_test

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"log/slog"
	"strings"
	"testing"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/config"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/crypto"
)

var authEnvVars = []string{
	"DATABASE_URL", "APP_ENV", "PORT", "HTTP_ADDR",
	"JWT_SIGNING_KEYS", "JWT_ACTIVE_KID", "PII_ENCRYPTION_KEYS", "PII_ACTIVE_KEY_ID", "BLIND_INDEX_KEY",
	"WEB_ORIGINS", "TRUSTED_PROXY_HOPS", "ARGON2_MAX_CONCURRENCY",
}

func fill(b byte) []byte  { return bytes.Repeat([]byte{b}, 32) }
func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

var (
	seedA, seedB = fill(0xA1), fill(0xB2)
	pii1, pii2   = fill(0x11), fill(0x22)
	blind        = fill(0x33)
)

// good returns a complete, valid staging/production environment.
func good() map[string]string {
	return map[string]string{
		"DATABASE_URL":        "postgres://u:p@h/db",
		"JWT_SIGNING_KEYS":    "k1:" + b64(seedA) + ",k2:" + b64(seedB),
		"JWT_ACTIVE_KID":      "k2",
		"PII_ENCRYPTION_KEYS": "1:" + b64(pii1) + ",2:" + b64(pii2),
		"PII_ACTIVE_KEY_ID":   "2",
		"BLIND_INDEX_KEY":     b64(blind),
		"WEB_ORIGINS":         "https://admin.example.in",
	}
}

func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, k := range authEnvVars {
		t.Setenv(k, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func with(base map[string]string, kv ...string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for i := 0; i < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

func without(base map[string]string, keys ...string) map[string]string {
	out := with(base)
	for _, k := range keys {
		delete(out, k)
	}
	return out
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestAuthConfig_ExplicitKeysAreDecoded(t *testing.T) {
	setEnv(t, with(good(), "APP_ENV", "production", "TRUSTED_PROXY_HOPS", "2", "ARGON2_MAX_CONCURRENCY", "6",
		"WEB_ORIGINS", " https://admin.example.in , http://localhost:5173 "))
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	a := cfg.Auth
	if len(a.JWTSeeds) != 2 || !bytes.Equal(a.JWTSeeds["k1"], seedA) || !bytes.Equal(a.JWTSeeds["k2"], seedB) {
		t.Errorf("JWTSeeds not decoded as configured: %d entries", len(a.JWTSeeds))
	}
	if a.JWTActiveKID != "k2" {
		t.Errorf("JWTActiveKID = %q, want k2", a.JWTActiveKID)
	}
	if got, want := a.WebOrigins, []string{"https://admin.example.in", "http://localhost:5173"}; !equalStrings(got, want) {
		t.Errorf("WebOrigins = %q, want %q (trimmed, in order)", got, want)
	}
	if a.TrustedProxyHops != 2 || a.Argon2MaxConcurrency != 6 {
		t.Errorf("hops/concurrency = %d/%d, want 2/6", a.TrustedProxyHops, a.Argon2MaxConcurrency)
	}

	// The PII key ring signs with the active id and still opens the older key's data.
	env, err := a.PII.Seal([]byte("totp"), []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := a.PII.KeyID(env); id != 2 {
		t.Errorf("active PII key id = %d, want 2", id)
	}
	oldRing, _ := crypto.NewKeyring(1, map[uint16][]byte{1: pii1})
	oldEnv, _ := oldRing.Seal([]byte("legacy"), []byte("aad"))
	if got, err := a.PII.Open(oldEnv, []byte("aad")); err != nil || string(got) != "legacy" {
		t.Errorf("configured ring cannot open an envelope sealed under key 1: %q, %v", got, err)
	}

	// The blind index uses exactly BLIND_INDEX_KEY.
	ref, _ := crypto.NewBlindIndexer(blind)
	if !bytes.Equal(a.BlindIndex.Index("throttle", []byte("v")), ref.Index("throttle", []byte("v"))) {
		t.Error("BlindIndex was not built from BLIND_INDEX_KEY")
	}
}

func TestAuthConfig_Defaults(t *testing.T) {
	setEnv(t, with(good(), "APP_ENV", "staging"))
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.TrustedProxyHops != 0 {
		t.Errorf("TrustedProxyHops default = %d, want 0", cfg.Auth.TrustedProxyHops)
	}
	if cfg.Auth.Argon2MaxConcurrency != 4 {
		t.Errorf("Argon2MaxConcurrency default = %d, want 4", cfg.Auth.Argon2MaxConcurrency)
	}
}

func TestAuthConfig_EphemeralKeysInLocalAndCI(t *testing.T) {
	for _, env := range []string{"", "local", "ci"} {
		t.Run("APP_ENV="+env, func(t *testing.T) {
			setEnv(t, map[string]string{"DATABASE_URL": "postgres://x", "APP_ENV": env})
			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("Load without any key variables must succeed outside staging/production: %v", err)
			}
			a := cfg.Auth
			if len(a.JWTSeeds) == 0 {
				t.Fatal("no ephemeral JWT key generated")
			}
			if seed, ok := a.JWTSeeds[a.JWTActiveKID]; !ok || len(seed) != 32 {
				t.Errorf("active kid %q is not in the ring or its seed is not 32 bytes", a.JWTActiveKID)
			}
			if a.PII == nil {
				t.Fatal("no ephemeral PII key ring")
			}
			if env, err := a.PII.Seal([]byte("x"), nil); err != nil {
				t.Errorf("ephemeral PII ring cannot seal: %v", err)
			} else if got, err := a.PII.Open(env, nil); err != nil || string(got) != "x" {
				t.Errorf("ephemeral PII ring cannot open its own data: %v", err)
			}
			if got := a.BlindIndex.Index("p", []byte("v")); len(got) != 32 {
				t.Errorf("ephemeral blind indexer is unusable (output length %d)", len(got))
			}
			if want := []string{"http://localhost:5173"}; !equalStrings(a.WebOrigins, want) {
				t.Errorf("default WebOrigins = %q, want %q", a.WebOrigins, want)
			}
			if a.TrustedProxyHops != 0 || a.Argon2MaxConcurrency != 4 {
				t.Errorf("defaults hops/concurrency = %d/%d, want 0/4", a.TrustedProxyHops, a.Argon2MaxConcurrency)
			}
		})
	}
}

func TestAuthConfig_EphemeralKeysDifferPerLoad(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://x", "APP_ENV": "local"})
	a, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	b, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a.Auth.JWTSeeds[a.Auth.JWTActiveKID], b.Auth.JWTSeeds[b.Auth.JWTActiveKID]) {
		t.Error("ephemeral JWT seed is identical across loads: it must be random, never a constant")
	}
	if bytes.Equal(a.Auth.BlindIndex.Index("p", []byte("v")), b.Auth.BlindIndex.Index("p", []byte("v"))) {
		t.Error("ephemeral blind-index key is identical across loads")
	}
}

func TestAuthConfig_EphemeralKeysAreWarnedAboutWithoutValues(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://x", "APP_ENV": "local"})
	logs := captureLogs(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	out := logs.String()
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("no WARN record announcing ephemeral keys; log was %q", out)
	}
	assertNoKeyMaterial(t, out, cfg.Auth)
}

func TestAuthConfig_ProvidedKeysSuppressTheEphemeralWarning(t *testing.T) {
	setEnv(t, with(good(), "APP_ENV", "ci"))
	logs := captureLogs(t)
	if _, err := config.Load(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("WARN logged although every key was provided: %q", logs.String())
	}
}

func TestAuthConfig_StagingAndProductionFailClosed(t *testing.T) {
	required := []string{"JWT_SIGNING_KEYS", "JWT_ACTIVE_KID", "PII_ENCRYPTION_KEYS", "PII_ACTIVE_KEY_ID", "BLIND_INDEX_KEY", "WEB_ORIGINS"}
	for _, appEnv := range []string{"staging", "production"} {
		t.Run(appEnv+" complete", func(t *testing.T) {
			setEnv(t, with(good(), "APP_ENV", appEnv))
			if _, err := config.Load(); err != nil {
				t.Fatalf("complete configuration rejected: %v", err)
			}
		})
		for _, missing := range required {
			t.Run(appEnv+" missing "+missing, func(t *testing.T) {
				setEnv(t, with(without(good(), missing), "APP_ENV", appEnv))
				_, err := config.Load()
				if err == nil {
					t.Fatalf("Load succeeded in %s without %s", appEnv, missing)
				}
				if !strings.Contains(err.Error(), missing) {
					t.Errorf("error %q does not name the missing variable %s", err, missing)
				}
			})
		}
	}
}

func TestAuthConfig_InvalidValuesAreRejected(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"jwt seed 16 bytes", with(good(), "JWT_SIGNING_KEYS", "k1:"+b64(fill(1)[:16]), "JWT_ACTIVE_KID", "k1")},
		{"jwt seed 33 bytes", with(good(), "JWT_SIGNING_KEYS", "k1:"+b64(append(fill(1), 0)), "JWT_ACTIVE_KID", "k1")},
		{"jwt seed not base64", with(good(), "JWT_SIGNING_KEYS", "k1:!!!not-base64!!!", "JWT_ACTIVE_KID", "k1")},
		{"jwt entry without colon", with(good(), "JWT_SIGNING_KEYS", b64(seedA), "JWT_ACTIVE_KID", "k1")},
		{"jwt empty kid", with(good(), "JWT_SIGNING_KEYS", ":"+b64(seedA), "JWT_ACTIVE_KID", "")},
		{"jwt duplicate kid", with(good(), "JWT_SIGNING_KEYS", "k1:"+b64(seedA)+",k1:"+b64(seedB), "JWT_ACTIVE_KID", "k1")},
		{"jwt active kid missing from ring", with(good(), "JWT_ACTIVE_KID", "k9")},

		{"pii key 16 bytes", with(good(), "PII_ENCRYPTION_KEYS", "1:"+b64(fill(1)[:16]), "PII_ACTIVE_KEY_ID", "1")},
		{"pii key 33 bytes", with(good(), "PII_ENCRYPTION_KEYS", "1:"+b64(append(fill(1), 0)), "PII_ACTIVE_KEY_ID", "1")},
		{"pii key not base64", with(good(), "PII_ENCRYPTION_KEYS", "1:@@@", "PII_ACTIVE_KEY_ID", "1")},
		{"pii id zero", with(good(), "PII_ENCRYPTION_KEYS", "0:"+b64(pii1), "PII_ACTIVE_KEY_ID", "0")},
		{"pii id too large", with(good(), "PII_ENCRYPTION_KEYS", "65536:"+b64(pii1), "PII_ACTIVE_KEY_ID", "65536")},
		{"pii id not a number", with(good(), "PII_ENCRYPTION_KEYS", "one:"+b64(pii1), "PII_ACTIVE_KEY_ID", "1")},
		{"pii duplicate id", with(good(), "PII_ENCRYPTION_KEYS", "1:"+b64(pii1)+",1:"+b64(pii2), "PII_ACTIVE_KEY_ID", "1")},
		{"pii active id missing from ring", with(good(), "PII_ACTIVE_KEY_ID", "9")},
		{"pii active id not a number", with(good(), "PII_ACTIVE_KEY_ID", "two")},

		{"blind key 16 bytes", with(good(), "BLIND_INDEX_KEY", b64(fill(3)[:16]))},
		{"blind key 33 bytes", with(good(), "BLIND_INDEX_KEY", b64(append(fill(3), 0)))},
		{"blind key not base64", with(good(), "BLIND_INDEX_KEY", "%%%")},
		{"blind key equals pii key 1", with(good(), "BLIND_INDEX_KEY", b64(pii1))},
		{"blind key equals the active pii key", with(good(), "BLIND_INDEX_KEY", b64(pii2))},

		{"origin wildcard", with(good(), "WEB_ORIGINS", "*")},
		{"origin pattern", with(good(), "WEB_ORIGINS", "https://*.example.in")},
		{"origin with path", with(good(), "WEB_ORIGINS", "https://admin.example.in/app")},
		{"origin with trailing slash", with(good(), "WEB_ORIGINS", "https://admin.example.in/")},
		{"origin without scheme", with(good(), "WEB_ORIGINS", "admin.example.in")},
		{"origin list with an empty entry", with(good(), "WEB_ORIGINS", "https://admin.example.in,,http://localhost:5173")},

		{"hops negative", with(good(), "TRUSTED_PROXY_HOPS", "-1")},
		{"hops not a number", with(good(), "TRUSTED_PROXY_HOPS", "two")},
		{"concurrency zero", with(good(), "ARGON2_MAX_CONCURRENCY", "0")},
		{"concurrency above 16", with(good(), "ARGON2_MAX_CONCURRENCY", "17")},
		{"concurrency not a number", with(good(), "ARGON2_MAX_CONCURRENCY", "many")},
	}
	for _, tt := range tests {
		for _, appEnv := range []string{"local", "production"} {
			t.Run(tt.name+" in "+appEnv, func(t *testing.T) {
				setEnv(t, with(tt.env, "APP_ENV", appEnv))
				if cfg, err := config.Load(); err == nil {
					t.Fatalf("Load accepted invalid configuration (%+v)", cfg.Auth.WebOrigins)
				}
			})
		}
	}
}

func TestAuthConfig_ValidBoundaries(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"pii id 1", with(good(), "PII_ENCRYPTION_KEYS", "1:"+b64(pii1), "PII_ACTIVE_KEY_ID", "1")},
		{"pii id 65535", with(good(), "PII_ENCRYPTION_KEYS", "65535:"+b64(pii1), "PII_ACTIVE_KEY_ID", "65535")},
		{"single jwt key", with(good(), "JWT_SIGNING_KEYS", "only:"+b64(seedA), "JWT_ACTIVE_KID", "only")},
		{"concurrency 1", with(good(), "ARGON2_MAX_CONCURRENCY", "1")},
		{"concurrency 16", with(good(), "ARGON2_MAX_CONCURRENCY", "16")},
		{"hops 0", with(good(), "TRUSTED_PROXY_HOPS", "0")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, with(tt.env, "APP_ENV", "production"))
			if _, err := config.Load(); err != nil {
				t.Fatalf("Load rejected a valid configuration: %v", err)
			}
		})
	}
}

func TestAuthConfig_ErrorsNeverEchoKeyMaterial(t *testing.T) {
	short := b64(fill(1)[:16])
	setEnv(t, with(good(), "APP_ENV", "production", "JWT_SIGNING_KEYS", "k1:"+short, "JWT_ACTIVE_KID", "k1",
		"PII_ENCRYPTION_KEYS", "1:"+b64(pii1)[:20], "PII_ACTIVE_KEY_ID", "1"))
	_, err := config.Load()
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), short) || strings.Contains(err.Error(), b64(pii1)[:20]) {
		t.Errorf("error echoes key material: %v", err)
	}
	// Equal-key conflict must not reveal the key either.
	setEnv(t, with(good(), "APP_ENV", "production", "BLIND_INDEX_KEY", b64(pii2)))
	if _, err := config.Load(); err == nil || strings.Contains(err.Error(), b64(pii2)) {
		t.Errorf("blind/PII conflict error missing or echoing the key: %v", err)
	}
}

func TestAuthConfig_LogValuerRedactsEveryKeyField(t *testing.T) {
	setEnv(t, with(good(), "APP_ENV", "production"))
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]any{"config": cfg, "auth": cfg.Auth} {
		t.Run(name, func(t *testing.T) {
			if _, ok := v.(slog.LogValuer); !ok {
				t.Fatalf("%T does not implement slog.LogValuer", v)
			}
			var buf bytes.Buffer
			slog.New(slog.NewJSONHandler(&buf, nil)).Info("loaded", name, v)
			out := buf.String()
			if !strings.Contains(out, "loaded") || len(out) < 20 {
				t.Fatalf("log output unexpectedly empty: %q", out)
			}
			assertNoKeyMaterial(t, out, cfg.Auth)
			if strings.Contains(out, "k1") && strings.Contains(out, b64(seedA)) {
				t.Error("JWT ring leaked")
			}
		})
	}
	// Also through the text handler and as a pointer.
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("loaded", "config", &cfg)
	assertNoKeyMaterial(t, buf.String(), cfg.Auth)
}

// assertNoKeyMaterial fails if out contains any secret in raw, hex, std or URL base64 form.
func assertNoKeyMaterial(t *testing.T, out string, a config.AuthConfig) {
	t.Helper()
	secrets := [][]byte{seedA, seedB, pii1, pii2, blind}
	for _, s := range a.JWTSeeds {
		secrets = append(secrets, s)
	}
	for _, s := range secrets {
		for _, form := range []string{
			string(s), hex.EncodeToString(s), base64.StdEncoding.EncodeToString(s), base64.URLEncoding.EncodeToString(s),
			base64.RawStdEncoding.EncodeToString(s), base64.RawURLEncoding.EncodeToString(s),
		} {
			if strings.Contains(out, form) {
				t.Errorf("output contains key material %q", form[:8]+"...")
			}
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
