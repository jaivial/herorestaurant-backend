package httpx

import (
	"net/http"
	"strings"
)

// ClientIP extracts the real client IP from the request.
// It checks X-Forwarded-For, X-Real-IP, and falls back to r.RemoteAddr.
func ClientIP(r *http.Request) string {
	// Check X-Forwarded-For (may contain multiple IPs; take the first).
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if idx := strings.Index(fwd, ","); idx != -1 {
			fwd = fwd[:idx]
		}
		fwd = strings.TrimSpace(fwd)
		if fwd != "" {
			return fwd
		}
	}

	// Check X-Real-IP.
	if rip := r.Header.Get("X-Real-IP"); rip != "" {
		rip = strings.TrimSpace(rip)
		if rip != "" {
			return rip
		}
	}

	// Fall back to RemoteAddr (may include port).
	if ra := r.RemoteAddr; ra != "" {
		// Remove port if present.
		if host, _, ok := strings.Cut(ra, ":"); ok {
			return host
		}
		return ra
	}

	return "unknown"
}
