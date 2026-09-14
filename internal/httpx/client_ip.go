package httpx

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP returns the client IP address used for rate limiting.
//
// X-Real-IP is set by our reverse proxy (`proxy_set_header X-Real-IP
// $remote_addr`), so it is the only trusted header and is used first when it
// parses as an IP. Otherwise the peer address from RemoteAddr is used, with
// IPv6 brackets and the port stripped via net.SplitHostPort. X-Forwarded-For is
// client-appendable, so it is only consulted as a last resort and every
// candidate must parse with net.ParseIP before it is used.
//
// The result is never empty: callers always get a stable value ("unknown" as a
// last resort) so an unattributable request still counts against a shared
// bucket instead of skipping its rate limit.
func ClientIP(r *http.Request) string {
	// Prefer the proxy-set X-Real-IP.
	if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
		return ip.String()
	}

	// Fall back to the peer address, stripping any port/brackets.
	if host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr)); err == nil {
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
	} else if ip := net.ParseIP(strings.TrimSpace(r.RemoteAddr)); ip != nil {
		return ip.String()
	}

	// X-Forwarded-For may list several hops ("client, proxy, ..."): only use it
	// when a candidate actually parses, and prefer the rightmost one (the hop
	// appended by our own proxy) over the client-supplied prefix.
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		if ip := net.ParseIP(strings.TrimSpace(parts[i])); ip != nil {
			return ip.String()
		}
	}

	return "unknown"
}
