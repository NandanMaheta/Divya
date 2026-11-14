// Tests for the exact-origin CORS middleware (T008; plan P-04 and P-17, FR-091).
//
// API assumptions (contracts are silent):
//   - httpx.CORS(allowed []string) func(http.Handler) http.Handler; origins are compared as exact
//     strings (scheme, host and port), never as patterns, and "*" in the list is just an
//     origin that no browser sends.
//   - A preflight is an OPTIONS request with Origin and Access-Control-Request-Method. For an
//     allowed origin the middleware answers it itself with a 2xx and does not call the next handler.
//   - For a disallowed origin no Access-Control-Allow-* header is ever set.
package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/httpx"
)

const adminOrigin = "https://admin.example.in"

func corsHandler(allowed []string, calls *int) http.Handler {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*calls++
		w.WriteHeader(http.StatusOK)
	})
	return httpx.CORS(allowed)(next)
}

func do(h http.Handler, method, origin string, extra map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/v1/auth/refresh", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func varyHasOrigin(h http.Header) bool {
	for _, v := range h.Values("Vary") {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), "Origin") {
				return true
			}
		}
	}
	return false
}

func TestCORS_AllowedOriginGetsCredentialedHeaders(t *testing.T) {
	calls := 0
	rec := do(corsHandler([]string{adminOrigin, "http://localhost:5173"}, &calls), http.MethodPost, adminOrigin, nil)
	if calls != 1 {
		t.Fatalf("next handler calls = %d, want 1", calls)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != adminOrigin {
		t.Errorf("Allow-Origin = %q, want %q", got, adminOrigin)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Allow-Credentials = %q, want true", got)
	}
	if !varyHasOrigin(rec.Header()) {
		t.Errorf("Vary = %v, want it to include Origin", rec.Header().Values("Vary"))
	}
}

func TestCORS_EachListedOriginIsEchoedExactly(t *testing.T) {
	allowed := []string{adminOrigin, "http://localhost:5173"}
	for _, o := range allowed {
		calls := 0
		rec := do(corsHandler(allowed, &calls), http.MethodGet, o, nil)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != o {
			t.Errorf("origin %q: Allow-Origin = %q", o, got)
		}
	}
}

func TestCORS_DisallowedOriginsGetNoCORSHeaders(t *testing.T) {
	tests := map[string]string{
		"unlisted":                  "https://evil.example.com",
		"suffix attack":             adminOrigin + ".evil.com",
		"prefix attack":             "https://evil" + adminOrigin[len("https://"):],
		"scheme differs":            "http://admin.example.in",
		"port differs":              "https://admin.example.in:8443",
		"sibling subdomain":         "https://www.example.in",
		"parent domain":             "https://example.in",
		"deeper subdomain":          "https://x.admin.example.in",
		"trailing slash":            adminOrigin + "/",
		"null origin":               "null",
		"origin with path":          adminOrigin + "/path",
		"uppercase scheme and host": "HTTPS://ADMIN.EXAMPLE.IN",
	}
	for name, origin := range tests {
		t.Run(name, func(t *testing.T) {
			calls := 0
			rec := do(corsHandler([]string{adminOrigin}, &calls), http.MethodPost, origin, nil)
			for h := range rec.Header() {
				if strings.HasPrefix(strings.ToLower(h), "access-control-allow-") {
					t.Errorf("disallowed origin %q got header %s: %v", origin, h, rec.Header()[h])
				}
			}
			if !varyHasOrigin(rec.Header()) {
				t.Errorf("Vary = %v, want Origin (responses depend on it even when denied)", rec.Header().Values("Vary"))
			}
		})
	}
}

func TestCORS_NeverWildcardNeverPattern(t *testing.T) {
	for _, allowed := range [][]string{{"*"}, {"https://*.example.in"}, {"*", "https://*.example.in"}, {}, nil} {
		for _, origin := range []string{"https://anything.example.in", "https://evil.com", "*"} {
			calls := 0
			rec := do(corsHandler(allowed, &calls), http.MethodGet, origin, nil)
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("allowlist %v, origin %q: Allow-Origin = %q, want none", allowed, origin, got)
			}
			if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
				t.Errorf("allowlist %v, origin %q: Allow-Credentials = %q, want none", allowed, origin, got)
			}
		}
	}
}

func TestCORS_NoOriginHeaderIsPassedThroughUntouched(t *testing.T) {
	calls := 0
	rec := do(corsHandler([]string{adminOrigin}, &calls), http.MethodPost, "", nil)
	if calls != 1 || rec.Code != http.StatusOK {
		t.Fatalf("calls=%d status=%d, want the request to reach the handler", calls, rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q for a same-origin or non-browser request", got)
	}
}

func TestCORS_PreflightAllowsCSRFHeader(t *testing.T) {
	calls := 0
	rec := do(corsHandler([]string{adminOrigin}, &calls), http.MethodOptions, adminOrigin, map[string]string{
		"Access-Control-Request-Method":  "POST",
		"Access-Control-Request-Headers": "content-type, x-swastik-csrf",
	})
	if rec.Code < 200 || rec.Code > 299 {
		t.Fatalf("preflight status = %d, want 2xx", rec.Code)
	}
	if calls != 0 {
		t.Error("preflight reached the next handler")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != adminOrigin {
		t.Errorf("Allow-Origin = %q, want %q", got, adminOrigin)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Allow-Credentials = %q, want true", got)
	}
	if !varyHasOrigin(rec.Header()) {
		t.Errorf("Vary = %v, want Origin", rec.Header().Values("Vary"))
	}
	headers := strings.ToLower(rec.Header().Get("Access-Control-Allow-Headers"))
	for _, want := range []string{"x-swastik-csrf", "content-type", "authorization"} {
		if !strings.Contains(headers, want) {
			t.Errorf("Allow-Headers = %q, missing %s", headers, want)
		}
	}
	if strings.TrimSpace(headers) == "*" {
		t.Error("Allow-Headers must list headers, not *: wildcard is not honoured with credentials")
	}
	methods := strings.ToUpper(rec.Header().Get("Access-Control-Allow-Methods"))
	for _, want := range []string{"GET", "POST", "DELETE"} {
		if !strings.Contains(methods, want) {
			t.Errorf("Allow-Methods = %q, missing %s", methods, want)
		}
	}
}

func TestCORS_PreflightFromDisallowedOriginIsDenied(t *testing.T) {
	calls := 0
	rec := do(corsHandler([]string{adminOrigin}, &calls), http.MethodOptions, "https://evil.example.com", map[string]string{
		"Access-Control-Request-Method":  "POST",
		"Access-Control-Request-Headers": "x-swastik-csrf",
	})
	for h := range rec.Header() {
		if strings.HasPrefix(strings.ToLower(h), "access-control-allow-") {
			t.Errorf("denied preflight carries %s: %v", h, rec.Header()[h])
		}
	}
}
