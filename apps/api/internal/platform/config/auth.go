package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/crypto"
)

const (
	defaultArgon2Concurrency = 4
	maxArgon2Concurrency     = 16
	keyBytes                 = 32
	ephemeralKID             = "ephemeral"
	ephemeralPIIID           = 1
	defaultLocalOrigin       = "http://localhost:5173"
)

// AuthConfig carries the key material and network settings authentication needs.
type AuthConfig struct {
	JWTSeeds             map[string][]byte // kid -> Ed25519 seed (32 bytes)
	JWTActiveKID         string
	PII                  *crypto.Keyring
	BlindIndex           crypto.BlindIndexer
	WebOrigins           []string
	TrustedProxyHops     int
	Argon2MaxConcurrency int
}

// LogValue implements slog.LogValuer: key ids and counts are shown, key material never is.
func (a AuthConfig) LogValue() slog.Value {
	kids := make([]string, 0, len(a.JWTSeeds))
	for kid := range a.JWTSeeds {
		kids = append(kids, kid)
	}
	sort.Strings(kids)
	return slog.GroupValue(
		slog.String("jwt_active_kid", a.JWTActiveKID),
		slog.Int("jwt_keys", len(kids)),
		slog.Bool("pii_keyring", a.PII != nil),
		slog.Any("web_origins", a.WebOrigins),
		slog.Int("trusted_proxy_hops", a.TrustedProxyHops),
		slog.Int("argon2_max_concurrency", a.Argon2MaxConcurrency),
	)
}

func loadAuth(appEnv string) (AuthConfig, error) {
	strict := appEnv == "staging" || appEnv == "production"
	var a AuthConfig
	var ephemeral []string

	// JWT signing keys.
	jwtKeys, jwtActive := os.Getenv("JWT_SIGNING_KEYS"), os.Getenv("JWT_ACTIVE_KID")
	switch {
	case jwtKeys == "" && jwtActive == "" && !strict:
		seed, err := randomBytes(keyBytes)
		if err != nil {
			return AuthConfig{}, err
		}
		a.JWTSeeds, a.JWTActiveKID = map[string][]byte{ephemeralKID: seed}, ephemeralKID
		ephemeral = append(ephemeral, "JWT_SIGNING_KEYS")
	default:
		if err := requireVars(map[string]string{"JWT_SIGNING_KEYS": jwtKeys, "JWT_ACTIVE_KID": jwtActive}, "JWT_SIGNING_KEYS", "JWT_ACTIVE_KID"); err != nil {
			return AuthConfig{}, err
		}
		seeds, err := parseJWTKeys(jwtKeys)
		if err != nil {
			return AuthConfig{}, err
		}
		if _, ok := seeds[jwtActive]; !ok {
			return AuthConfig{}, errors.New("JWT_ACTIVE_KID is not in JWT_SIGNING_KEYS")
		}
		a.JWTSeeds, a.JWTActiveKID = seeds, jwtActive
	}

	// PII encryption keys.
	piiKeys, piiActive := os.Getenv("PII_ENCRYPTION_KEYS"), os.Getenv("PII_ACTIVE_KEY_ID")
	var piiMaterial map[uint16][]byte
	switch {
	case piiKeys == "" && piiActive == "" && !strict:
		key, err := randomBytes(keyBytes)
		if err != nil {
			return AuthConfig{}, err
		}
		piiMaterial = map[uint16][]byte{ephemeralPIIID: key}
		ring, err := crypto.NewKeyring(ephemeralPIIID, piiMaterial)
		if err != nil {
			return AuthConfig{}, err
		}
		a.PII = ring
		ephemeral = append(ephemeral, "PII_ENCRYPTION_KEYS")
	default:
		if err := requireVars(map[string]string{"PII_ENCRYPTION_KEYS": piiKeys, "PII_ACTIVE_KEY_ID": piiActive}, "PII_ENCRYPTION_KEYS", "PII_ACTIVE_KEY_ID"); err != nil {
			return AuthConfig{}, err
		}
		var err error
		if piiMaterial, err = parsePIIKeys(piiKeys); err != nil {
			return AuthConfig{}, err
		}
		id, err := strconv.ParseUint(piiActive, 10, 16)
		if err != nil || id == 0 {
			return AuthConfig{}, errors.New("PII_ACTIVE_KEY_ID must be a number from 1 to 65535")
		}
		if _, ok := piiMaterial[uint16(id)]; !ok {
			return AuthConfig{}, errors.New("PII_ACTIVE_KEY_ID is not in PII_ENCRYPTION_KEYS")
		}
		if a.PII, err = crypto.NewKeyring(uint16(id), piiMaterial); err != nil {
			return AuthConfig{}, errors.New("PII_ENCRYPTION_KEYS: invalid key")
		}
	}

	// Blind index key.
	blindRaw := os.Getenv("BLIND_INDEX_KEY")
	var blindKey []byte
	if blindRaw == "" && !strict {
		var err error
		if blindKey, err = randomBytes(keyBytes); err != nil {
			return AuthConfig{}, err
		}
		ephemeral = append(ephemeral, "BLIND_INDEX_KEY")
	} else {
		if err := requireVars(map[string]string{"BLIND_INDEX_KEY": blindRaw}, "BLIND_INDEX_KEY"); err != nil {
			return AuthConfig{}, err
		}
		var err error
		if blindKey, err = decodeKey(blindRaw); err != nil {
			return AuthConfig{}, errors.New("BLIND_INDEX_KEY must be standard base64 of exactly 32 bytes")
		}
	}
	for _, material := range piiMaterial {
		if string(material) == string(blindKey) {
			return AuthConfig{}, errors.New("BLIND_INDEX_KEY must differ from every PII encryption key")
		}
	}
	var err error
	if a.BlindIndex, err = crypto.NewBlindIndexer(blindKey); err != nil {
		return AuthConfig{}, errors.New("BLIND_INDEX_KEY must be exactly 32 bytes")
	}

	// Web origins.
	origins := os.Getenv("WEB_ORIGINS")
	switch {
	case origins == "" && !strict:
		a.WebOrigins = []string{defaultLocalOrigin}
	default:
		if err := requireVars(map[string]string{"WEB_ORIGINS": origins}, "WEB_ORIGINS"); err != nil {
			return AuthConfig{}, err
		}
		if a.WebOrigins, err = parseOrigins(origins); err != nil {
			return AuthConfig{}, err
		}
	}

	// Numeric settings.
	if a.TrustedProxyHops, err = intVar("TRUSTED_PROXY_HOPS", 0, 0, 1<<10); err != nil {
		return AuthConfig{}, err
	}
	if a.Argon2MaxConcurrency, err = intVar("ARGON2_MAX_CONCURRENCY", defaultArgon2Concurrency, 1, maxArgon2Concurrency); err != nil {
		return AuthConfig{}, err
	}

	if len(ephemeral) > 0 {
		slog.Warn("using ephemeral keys: sessions and encrypted data do not survive a restart",
			"variables", strings.Join(ephemeral, ","), "env", appEnv)
	}
	return a, nil
}

// requireVars errors naming the first listed variable that is empty.
func requireVars(values map[string]string, order ...string) error {
	for _, name := range order {
		if values[name] == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	return nil
}

func parseJWTKeys(raw string) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, entry := range strings.Split(raw, ",") {
		kid, b64, ok := strings.Cut(strings.TrimSpace(entry), ":")
		if !ok || kid == "" {
			return nil, errors.New("JWT_SIGNING_KEYS entries must look like kid:base64seed")
		}
		seed, err := decodeKey(b64)
		if err != nil {
			return nil, errors.New("JWT_SIGNING_KEYS: each seed must be standard base64 of exactly 32 bytes")
		}
		if _, dup := out[kid]; dup {
			return nil, errors.New("JWT_SIGNING_KEYS: duplicate kid")
		}
		out[kid] = seed
	}
	return out, nil
}

func parsePIIKeys(raw string) (map[uint16][]byte, error) {
	out := map[uint16][]byte{}
	for _, entry := range strings.Split(raw, ",") {
		idText, b64, ok := strings.Cut(strings.TrimSpace(entry), ":")
		id, err := strconv.ParseUint(idText, 10, 16)
		if !ok || err != nil || id == 0 {
			return nil, errors.New("PII_ENCRYPTION_KEYS entries must look like id:base64key with id 1 to 65535")
		}
		key, err := decodeKey(b64)
		if err != nil {
			return nil, errors.New("PII_ENCRYPTION_KEYS: each key must be standard base64 of exactly 32 bytes")
		}
		if _, dup := out[uint16(id)]; dup {
			return nil, errors.New("PII_ENCRYPTION_KEYS: duplicate key id")
		}
		out[uint16(id)] = key
	}
	return out, nil
}

func decodeKey(b64 string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(b) != keyBytes {
		return nil, errors.New("invalid key")
	}
	return b, nil
}

func parseOrigins(raw string) ([]string, error) {
	var out []string
	for _, entry := range strings.Split(raw, ",") {
		o := strings.TrimSpace(entry)
		u, err := url.Parse(o)
		if o == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
			u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || strings.Contains(o, "*") {
			return nil, errors.New("WEB_ORIGINS must be a comma-separated list of exact origins like https://host[:port]")
		}
		out = append(out, o)
	}
	return out, nil
}

func intVar(name string, fallback, lo, hi int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < lo || n > hi {
		return 0, fmt.Errorf("%s must be a whole number from %d to %d", name, lo, hi)
	}
	return n, nil
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("random key: %w", err)
	}
	return b, nil
}
