package api

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// Vault-key auth is a single shared secret that machine clients and the
// backoffice SSR proxy present on every admin request. Keeping the check here
// means any HTTP route or WebSocket can opt into it with one middleware.
//
// Coordination id: vault_key_auth_v1

const (
	vaultKeyHeader     = "X-Vault-Key"
	vaultKeyQueryParam = "vault_key"
	vaultKeyCode       = "VAULT_KEY_REQUIRED"
)

// vaultKeyEnabled reports whether enforcement is configured. An empty key
// disables the check so local/dev environments keep working and a missing env
// var can never lock every client out.
func (s *Server) vaultKeyEnabled() bool {
	return s != nil && strings.TrimSpace(s.cfg.VaultKey) != ""
}

// vaultKeyFromRequest reads the shared secret from any supported location.
// WebSocket clients, which cannot set headers, may use the query parameter;
// REST clients should use the Authorization bearer header.
func vaultKeyFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if h := strings.TrimSpace(r.Header.Get("Authorization")); len(h) >= 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	if h := strings.TrimSpace(r.Header.Get(vaultKeyHeader)); h != "" {
		return h
	}
	return strings.TrimSpace(r.URL.Query().Get(vaultKeyQueryParam))
}

// vaultKeyValid reports whether the request carries the configured secret.
// Returns true when enforcement is disabled. The comparison is constant-time.
func (s *Server) vaultKeyValid(r *http.Request) bool {
	if !s.vaultKeyEnabled() {
		return true
	}
	provided := vaultKeyFromRequest(r)
	if provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(strings.TrimSpace(s.cfg.VaultKey))) == 1
}

// requireVaultKey is the reusable middleware: attach it to any route group that
// must demand the shared secret, including WebSocket upgrade routes.
func (s *Server) requireVaultKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.vaultKeyValid(r) {
			next.ServeHTTP(w, r)
			return
		}
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"success": false,
			"message": "Unauthorized",
			"code":    vaultKeyCode,
		})
	})
}
