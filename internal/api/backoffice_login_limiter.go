package api

import "time"

// Brute-force protection for the backoffice login. Reuses the shared
// fixedWindowLimiter (one counter/pruning implementation for the whole API)
// and caps both the source IP and the target identifier, so neither a single
// host nor a single account can be hammered.
//
// Coordination id: bo_login_rate_limit_v1

var boLoginLimiter fixedWindowLimiter

const (
	boLoginMaxPerIP         = 20
	boLoginMaxPerIdentifier = 10
	boLoginWindow           = 15 * time.Minute
)

// allowBOLoginAttempt reports whether this attempt may proceed. It records the
// attempt for both keys even when the other key rejects, closing the window
// where an attacker alternates keys to bypass one of the two limits.
func allowBOLoginAttempt(ip, identifier string) bool {
	// An empty IP/identifier is still counted, against a shared "unknown"
	// bucket: never skip the limit because a key could not be resolved.
	if ip == "" {
		ip = "unknown"
	}
	if identifier == "" {
		identifier = "unknown"
	}
	ipOK := boLoginLimiter.allow("ip:"+ip, boLoginMaxPerIP, boLoginWindow)
	idOK := boLoginLimiter.allow("id:"+identifier, boLoginMaxPerIdentifier, boLoginWindow)
	return ipOK && idOK
}
