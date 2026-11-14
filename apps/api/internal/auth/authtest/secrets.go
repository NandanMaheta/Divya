package authtest

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// minSecretLen keeps a one-character "secret" from matching everywhere.
const minSecretLen = 6

// Secrets records every secret an acceptance test uses (passwords, TOTP codes and secrets, recovery
// codes, access, refresh and challenge tokens, identifiers that matched no user) and scans text for
// them (SC-006, FR-091). A failure names the label and the place, never the secret.
type Secrets struct {
	mu   sync.Mutex
	vals map[string]string // value -> label
}

// NewSecrets returns an empty registry.
func NewSecrets() *Secrets { return &Secrets{vals: map[string]string{}} }

// Add records a secret under a label. Empty and very short values are ignored.
func (s *Secrets) Add(label, value string) {
	if len(value) < minSecretLen {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.vals[value]; !ok {
		s.vals[value] = label
	}
}

// AddTOTPSecret records a TOTP secret in its base32 form and in the hex form of its raw bytes.
func (s *Secrets) AddTOTPSecret(base32Secret string) {
	s.Add("totp secret (base32)", base32Secret)
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimRight(base32Secret, "=")))
	if err == nil {
		s.Add("totp secret (hex)", hex.EncodeToString(raw))
		s.Add("totp secret (HEX)", strings.ToUpper(hex.EncodeToString(raw)))
	}
}

// Hits returns the sorted, de-duplicated labels of every recorded secret found in haystack.
func (s *Secrets) Hits(haystack string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	for v, label := range s.vals {
		if strings.Contains(haystack, v) {
			seen[label] = true
		}
	}
	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// Scan fails the test for every recorded secret present in haystack; where names the source.
func (s *Secrets) Scan(t testing.TB, where, haystack string) {
	t.Helper()
	for _, label := range s.Hits(haystack) {
		t.Errorf("secret leak: %s found in %s", label, where)
	}
}

// ScanAuditLog scans every audit_log row, cast to text, for recorded secrets.
func (s *Secrets) ScanAuditLog(ctx context.Context, t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT a::text FROM audit_log a`)
	if err != nil {
		t.Fatalf("authtest: scan audit_log: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatalf("authtest: scan audit_log row: %v", err)
		}
		s.Scan(t, "an audit_log row", text)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("authtest: scan audit_log: %v", err)
	}
}
