package main

// T057 - contract test for the public OpenAPI document (US2.6, FR-004, constitution V): the
// hidden tick operation never appears in it, the document is unchanged by feature 002 Phase A,
// and `swastik openapi` builds it with zero dependencies (no database, no network, no config).
//
// The test drives the real `openapi` command through run() so it does not depend on the shape of
// buildAPI's dependency struct (T058).

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openAPIFromCommand runs `swastik openapi` and returns what it printed.
func openAPIFromCommand(t *testing.T) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	done := make(chan []byte)
	go func() {
		b, _ := io.ReadAll(r)
		done <- b
	}()
	runErr := run(slog.New(slog.NewTextHandler(io.Discard, nil)), []string{"openapi"})
	os.Stdout = stdout
	_ = w.Close()
	out := <-done
	_ = r.Close()
	if runErr != nil {
		t.Fatalf("swastik openapi: %v", runErr)
	}
	return string(out)
}

func TestOpenAPI_NoInternalOperations(t *testing.T) {
	// Anything that would need a database, a token issuer or the network fails fast here, so the
	// command can only succeed if building the document touches none of them.
	t.Setenv("DATABASE_URL", "postgres://nobody:nothing@127.0.0.1:1/none?connect_timeout=1")
	t.Setenv("APP_ENV", "production")
	t.Setenv("TICK_AUTH", "oidc")
	t.Setenv("TICK_AUDIENCE", "https://api.example.invalid/internal/tick")
	t.Setenv("TICK_SERVICE_ACCOUNT", "tick@example.invalid")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")

	doc := openAPIFromCommand(t)
	if !strings.Contains(doc, "openapi: 3.1") || !strings.Contains(doc, "/healthz") {
		t.Fatalf("output is not the API's OpenAPI document:\n%.400s", doc)
	}
	for _, banned := range []string{"/internal", "scheduler-tick", "TickSummary", "budget_exhausted"} {
		if strings.Contains(doc, banned) {
			t.Errorf("public OpenAPI contains %q: the tick operation must be Hidden (FR-004, US2.6)", banned)
		}
	}

	t.Run("the_committed_client_contract_is_unchanged", func(t *testing.T) {
		committed, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "packages", "api-client", "openapi.yaml"))
		if err != nil {
			t.Fatalf("read packages/api-client/openapi.yaml: %v", err)
		}
		if !bytes.Equal(bytes.TrimSpace(committed), bytes.TrimSpace([]byte(doc))) {
			t.Error("`swastik openapi` differs from packages/api-client/openapi.yaml; Phase A must not change the public contract (regenerate only in Phase B)")
		}
	})
}
