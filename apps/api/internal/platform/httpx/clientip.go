package httpx

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP returns the caller's IP address as a bare string. With hops <= 0 only RemoteAddr is
// used. Otherwise the X-Forwarded-For entry hops from the end is used (entries before it are
// client-controlled and never trusted). Anything missing or not a valid IP falls back to the
// RemoteAddr host.
func ClientIP(r *http.Request, hops int) string {
	peer := remoteHost(r.RemoteAddr)
	if hops <= 0 {
		return peer
	}
	var entries []string
	for _, line := range r.Header.Values("X-Forwarded-For") {
		entries = append(entries, strings.Split(line, ",")...)
	}
	if len(entries) < hops {
		return peer
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(entries[len(entries)-hops]))
	if err != nil {
		return peer
	}
	return addr.String()
}

func remoteHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}
