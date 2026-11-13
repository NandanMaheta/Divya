// Tests for the tickauth contract: the Authenticator interface, the sentinel errors, dev mode and
// Disabled (feature 002, FR-002, FR-003, US2.1-2.4). Offline OIDC verification is in oidc_test.go.
//
// Contract: contracts/go-module-api.md, section internal/platform/tickauth. The HTTP layer maps
// ErrUnauthenticated to 401 and ErrForbidden to 403, so every rejection here must wrap exactly
// one of them (errors.Is).
//
// Test names contain Dev, Disabled or Contract so that `-run 'Dev|Disabled|Contract'` selects
// everything that does not need the OIDC implementation.
//
// Constant-time comparison cannot be asserted from a black-box test; the tests cover the
// observable consequences instead (different lengths, prefixes, and extensions are all rejected
// without panics) and the reviewer checks the use of crypto/subtle.
package tickauth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Compile-time contract: both constructors return an Authenticator.
var (
	_ Authenticator = NewDev("x")
	_ Authenticator = Disabled()
)

func TestContract_SentinelErrors(t *testing.T) {
	if ErrUnauthenticated == nil || ErrForbidden == nil {
		t.Fatal("sentinel errors must be non-nil")
	}
	if errors.Is(ErrUnauthenticated, ErrForbidden) || errors.Is(ErrForbidden, ErrUnauthenticated) {
		t.Fatal("ErrUnauthenticated and ErrForbidden must be distinct (401 vs 403)")
	}
	if got, want := ErrUnauthenticated.Error(), "tickauth: unauthenticated"; got != want {
		t.Errorf("ErrUnauthenticated = %q, want %q", got, want)
	}
	if got, want := ErrForbidden.Error(), "tickauth: forbidden"; got != want {
		t.Errorf("ErrForbidden = %q, want %q", got, want)
	}
}

func TestContract_AuthenticatorIsASingleMethodInterface(t *testing.T) {
	// A caller (the tick handler, tests) can supply its own implementation.
	var a Authenticator = authenticatorFunc(func(context.Context, string) error { return ErrForbidden })
	if err := a.Authenticate(context.Background(), "Bearer x"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("got %v, want ErrForbidden", err)
	}
}

type authenticatorFunc func(ctx context.Context, header string) error

func (f authenticatorFunc) Authenticate(ctx context.Context, header string) error {
	return f(ctx, header)
}

func TestDev_AcceptsTheConfiguredBearerToken(t *testing.T) {
	const token = "local-dev-tick-token"
	a := NewDev(token)
	if err := a.Authenticate(context.Background(), "Bearer "+token); err != nil {
		t.Fatalf("valid dev token rejected: %v", err)
	}
	// repeated calls keep working (no one-shot state)
	if err := a.Authenticate(context.Background(), "Bearer "+token); err != nil {
		t.Fatalf("second call rejected: %v", err)
	}
}

func TestDev_RejectsEverythingElseAsUnauthenticated(t *testing.T) {
	const token = "local-dev-tick-token"
	tests := []struct {
		name   string
		header string
	}{
		{"missing header", ""},
		{"scheme only", "Bearer"},
		{"scheme and space only", "Bearer "},
		{"wrong token", "Bearer some-other-token"},
		{"token with a different case", "Bearer LOCAL-DEV-TICK-TOKEN"},
		{"prefix of the token", "Bearer local-dev-tick"},
		{"token plus a suffix", "Bearer " + token + "x"},
		{"token plus a trailing newline", "Bearer " + token + "\n"},
		{"much longer token", "Bearer " + strings.Repeat("a", 10000)},
		{"basic scheme", "Basic " + token},
		{"raw token without scheme", token},
		{"other scheme", "Token " + token},
		{"scheme glued to token", "Bearer" + token},
		{"two tokens", "Bearer " + token + " " + token},
		{"whitespace only", "   "},
	}
	a := NewDev(token)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := a.Authenticate(context.Background(), tt.header)
			if !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("Authenticate(%q) = %v, want ErrUnauthenticated", tt.header, err)
			}
			if errors.Is(err, ErrForbidden) {
				t.Fatalf("Authenticate(%q) = %v must not be ErrForbidden", tt.header, err)
			}
		})
	}
}

func TestDev_EmptyTokenRejectsEverything(t *testing.T) {
	a := NewDev("")
	for _, header := range []string{"", "Bearer", "Bearer ", "Bearer  ", "Bearer anything", "Bearer null", "Bearer empty", " "} {
		if err := a.Authenticate(context.Background(), header); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("empty-token dev authenticator: Authenticate(%q) = %v, want ErrUnauthenticated", header, err)
		}
	}
}

func TestDev_ErrorsNeverContainCredentials(t *testing.T) {
	const configured = "configured-secret-value"
	const presented = "presented-secret-value"
	a := NewDev(configured)
	for _, header := range []string{"Bearer " + presented, "Bearer " + configured + "x", "Basic " + presented, ""} {
		err := a.Authenticate(context.Background(), header)
		if err == nil {
			t.Fatalf("Authenticate(%q) accepted", header)
		}
		msg := err.Error()
		if strings.Contains(msg, configured) || strings.Contains(msg, presented) {
			t.Errorf("error %q leaks a credential (header %q)", msg, header)
		}
	}
}

func TestDev_HonoursAnAlreadyCancelledContextWithoutPanicking(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := NewDev("tok")
	// Either outcome is acceptable for a pure in-memory comparison; it must simply not panic
	// and a wrong token must still be rejected.
	if err := a.Authenticate(ctx, "Bearer wrong"); err == nil {
		t.Fatal("wrong token accepted on a cancelled context")
	}
}

func TestDev_InstancesAreIndependent(t *testing.T) {
	a, b := NewDev("token-a"), NewDev("token-b")
	if err := a.Authenticate(context.Background(), "Bearer token-b"); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("a accepted b's token: %v", err)
	}
	if err := b.Authenticate(context.Background(), "Bearer token-b"); err != nil {
		t.Errorf("b rejected its own token: %v", err)
	}
}

func TestDisabled_RejectsEverythingAsUnauthenticated(t *testing.T) {
	a := Disabled()
	for _, header := range []string{
		"",
		"Bearer",
		"Bearer ",
		"Bearer anything",
		"Bearer eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxIn0.c2ln", // JWT-shaped
		"Basic dXNlcjpwYXNz",
	} {
		err := a.Authenticate(context.Background(), header)
		if !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("Disabled().Authenticate(%q) = %v, want ErrUnauthenticated", header, err)
		}
		if errors.Is(err, ErrForbidden) {
			t.Errorf("Disabled().Authenticate(%q) must be 401, not 403", header)
		}
	}
}

func TestDisabled_ErrorNeverEchoesTheHeader(t *testing.T) {
	err := Disabled().Authenticate(context.Background(), "Bearer very-secret-credential")
	if err == nil || strings.Contains(err.Error(), "very-secret-credential") {
		t.Fatalf("error %v must exist and must not echo the credential", err)
	}
}
