package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strings"
)

// PKCE (RFC 7636) support for the MCP OAuth flow. Only S256 is accepted: a
// public client cannot keep a secret, so the code verifier is what proves the
// token exchange comes from the same client that started the authorization.
//
// Coordination id: mcp_server_v1

// mcpPKCEChallenge derives the S256 challenge from a verifier.
func mcpPKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// mcpVerifyPKCE reports whether the verifier matches the stored challenge. The
// comparison is constant-time, and an empty verifier never matches, so a
// missing code_verifier cannot degrade PKCE into a no-op.
func mcpVerifyPKCE(verifier, challenge string) bool {
	verifier = strings.TrimSpace(verifier)
	challenge = strings.TrimSpace(challenge)
	if verifier == "" || challenge == "" {
		return false
	}
	got := mcpPKCEChallenge(verifier)
	return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}
