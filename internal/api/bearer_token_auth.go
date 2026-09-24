package api

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// Second shared secret for the admin API (REST + WebSockets). The backoffice
// SSR server injects it on every backend request; it is never sent to or read
// by the browser. Kept separate from VAULT_KEY (Authorization: Bearer) so one
// leaked secret alone does not open /admin.
//
// Header: X-Bearer-Token: <BEARER_TOKEN_KEY>. WebSocket clients that cannot set
// headers may use ?bearer_token=.
// Coordination id: stripe_connect_multitenant_v1

const (
	bearerTokenHeader     = "X-Bearer-Token"
	bearerTokenQueryParam = "bearer_token"
	bearerTokenCode       = "BEARER_TOKEN_REQUIRED"
)

func (s *Server) bearerTokenValid(r *http.Request) bool {
	want := strings.TrimSpace(s.cfg.BearerTokenKey)
	if want == "" {
		return true // not configured: dev convenience; prod fails closed in config.Validate
	}
	got := strings.TrimSpace(r.Header.Get(bearerTokenHeader))
	if got == "" {
		got = strings.TrimSpace(r.URL.Query().Get(bearerTokenQueryParam))
	}
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (s *Server) requireBearerToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.bearerTokenValid(r) {
			next.ServeHTTP(w, r)
			return
		}
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"success": false,
			"message": "Unauthorized",
			"code":    bearerTokenCode,
		})
	})
}
