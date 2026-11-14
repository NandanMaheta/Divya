// Tests for httpx.ClientIP (T008; plan P-17, R-7).
//
// API assumptions (contracts are silent):
//   - ClientIP(r *http.Request, hops int) string returns a bare IP (no port, no brackets).
//   - hops <= 0 means RemoteAddr only. Otherwise the X-Forwarded-For entry at index len-hops of the
//     comma-joined values of ALL X-Forwarded-For header lines is used; the leading entries are never
//     trusted.
//   - When the header is absent, has fewer entries than hops, or the selected entry is not a valid IP
//     address, the result falls back to the RemoteAddr host (never to a client-controlled value).
package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/httpx"
)

func TestClientIP(t *testing.T) {
	const peer = "198.51.100.9"
	tests := []struct {
		name       string
		remoteAddr string
		xff        []string // each element is one header line
		hops       int
		want       string
	}{
		{name: "hops 0 uses RemoteAddr", remoteAddr: peer + ":4321", hops: 0, want: peer},
		{name: "hops 0 ignores a spoofed header", remoteAddr: peer + ":4321", xff: []string{"6.6.6.6"}, hops: 0, want: peer},
		{name: "hops 0 ipv6 RemoteAddr", remoteAddr: "[2001:db8::7]:443", hops: 0, want: "2001:db8::7"},
		{name: "hops 0 RemoteAddr without port", remoteAddr: peer, hops: 0, want: peer},
		{name: "negative hops behave like 0", remoteAddr: peer + ":1", xff: []string{"6.6.6.6"}, hops: -1, want: peer},

		{name: "hops 1 takes the last entry", remoteAddr: peer + ":1", xff: []string{"203.0.113.7"}, hops: 1, want: "203.0.113.7"},
		{name: "hops 1 ignores spoofed leading values", remoteAddr: peer + ":1", xff: []string{"6.6.6.6, 7.7.7.7, 203.0.113.7"}, hops: 1, want: "203.0.113.7"},
		{name: "hops 2 takes the second last", remoteAddr: peer + ":1", xff: []string{"6.6.6.6, 203.0.113.7, 10.0.0.2"}, hops: 2, want: "203.0.113.7"},
		{name: "hops 2 with exactly two entries", remoteAddr: peer + ":1", xff: []string{"203.0.113.7, 10.0.0.2"}, hops: 2, want: "203.0.113.7"},
		{name: "whitespace around entries is trimmed", remoteAddr: peer + ":1", xff: []string{"  6.6.6.6 ,   203.0.113.7  "}, hops: 1, want: "203.0.113.7"},
		{name: "ipv6 entry", remoteAddr: peer + ":1", xff: []string{"6.6.6.6, 2001:db8::1"}, hops: 1, want: "2001:db8::1"},
		{name: "several header lines are joined in order", remoteAddr: peer + ":1", xff: []string{"6.6.6.6", "203.0.113.7, 10.0.0.2"}, hops: 2, want: "203.0.113.7"},
		{name: "several header lines, hops 1", remoteAddr: peer + ":1", xff: []string{"6.6.6.6", "203.0.113.7, 10.0.0.2"}, hops: 1, want: "10.0.0.2"},

		{name: "hops 1 without header falls back to RemoteAddr", remoteAddr: peer + ":1", hops: 1, want: peer},
		{name: "fewer entries than hops falls back", remoteAddr: peer + ":1", xff: []string{"203.0.113.7"}, hops: 2, want: peer},
		{name: "garbage entry falls back", remoteAddr: peer + ":1", xff: []string{"203.0.113.7, not-an-ip"}, hops: 1, want: peer},
		{name: "empty header value falls back", remoteAddr: peer + ":1", xff: []string{""}, hops: 1, want: peer},
		{name: "trailing comma yields an empty entry and falls back", remoteAddr: peer + ":1", xff: []string{"203.0.113.7,"}, hops: 1, want: peer},
		{name: "injected text is never returned", remoteAddr: peer + ":1", xff: []string{"203.0.113.7, <script>alert(1)</script>"}, hops: 1, want: peer},
		{name: "entry with port is not a bare IP and falls back", remoteAddr: peer + ":1", xff: []string{"203.0.113.7:8080"}, hops: 1, want: peer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			for _, line := range tt.xff {
				req.Header.Add("X-Forwarded-For", line)
			}
			if got := httpx.ClientIP(req, tt.hops); got != tt.want {
				t.Errorf("ClientIP(hops=%d) = %q, want %q", tt.hops, got, tt.want)
			}
		})
	}
}

func TestClientIP_SpoofedLeadingValueNeverWins(t *testing.T) {
	// A client that prepends its own value must not change the answer for any hops that
	// stay within the trusted tail of the header.
	for hops := 1; hops <= 3; hops++ {
		trusted := []string{"203.0.113.1", "203.0.113.2", "203.0.113.3"}
		base := trusted[len(trusted)-hops:]
		mk := func(prefix string) string {
			s := prefix
			for _, v := range base {
				if s != "" {
					s += ", "
				}
				s += v
			}
			return s
		}
		a := httptest.NewRequest(http.MethodGet, "/", nil)
		a.RemoteAddr = "198.51.100.9:1"
		a.Header.Set("X-Forwarded-For", mk(""))
		b := httptest.NewRequest(http.MethodGet, "/", nil)
		b.RemoteAddr = "198.51.100.9:1"
		b.Header.Set("X-Forwarded-For", mk("6.6.6.6, 9.9.9.9"))
		if got, want := httpx.ClientIP(b, hops), httpx.ClientIP(a, hops); got != want {
			t.Errorf("hops=%d: spoofed prefix changed the answer from %q to %q", hops, want, got)
		}
		if got := httpx.ClientIP(b, hops); got == "6.6.6.6" || got == "9.9.9.9" {
			t.Errorf("hops=%d: returned a client-controlled value %q", hops, got)
		}
	}
}
