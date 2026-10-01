package api

import "strings"

// Coordination id: mcp_server_v1
//
// One table describing where the OAuth discovery documents live, so the root
// redirects and the advertised resource_metadata URL can never drift apart.
//
// Every MCP client (Claude, Gemini, ChatGPT) starts at the bare issuer URL and
// probes the OAuth metadata from there. The two specifications disagree on
// which path they probe, so all of these spellings must resolve to the same
// two documents:
//
//	RFC 9728  /mcp/.well-known/oauth-protected-resource
//	RFC 8414  /mcp/.well-known/oauth-authorization-server
//
// plus the same two documents at the origin, which is where a client that has
// not yet resolved the issuer looks first.

const (
	mcpPRPath = "/.well-known/oauth-protected-resource"
	mcpASPath = "/.well-known/oauth-authorization-server"
)

// mcpScopeName is the single scope this server issues. It is a constant because
// it appears in three documents that must agree.
const mcpScopeName = "villacarmen"

// mcpDiscoveryRedirect is one root spelling a client may probe, and the
// issuer-scoped document it canonicalises to.
type mcpDiscoveryRedirect struct {
	from string
	to   string
}

// mcpDiscoveryPaths lists only the root spellings. The issuer-scoped paths are
// absent on purpose: they are the canonical documents themselves, and mounting
// a redirect on them would shadow the real handler.
var mcpDiscoveryPaths = []mcpDiscoveryRedirect{
	{from: mcpPRPath, to: mcpIssuerSuffix + mcpPRPath},
	{from: mcpASPath, to: mcpIssuerSuffix + mcpASPath},
	// The issuer-scoped spelling from the root: the path already ends in the
	// issuer suffix, so the canonical target is the path itself. Prepending the
	// issuer again — which produced the unresolvable
	// /mcp/.well-known/.../mcp target — cannot happen.
	{from: mcpPRPath + mcpIssuerSuffix, to: mcpIssuerSuffix + mcpPRPath},
	{from: mcpASPath + mcpIssuerSuffix, to: mcpIssuerSuffix + mcpASPath},
}

// mcpDiscoveryTarget resolves the canonical document URL for a root spelling,
// falling back to the issuer-prefixed path for any spelling not in the table.
func mcpDiscoveryTarget(path string) string {
	for _, d := range mcpDiscoveryPaths {
		if d.from == path {
			return d.to
		}
	}
	if strings.HasSuffix(strings.TrimSuffix(path, "/"), mcpIssuerSuffix) {
		return path
	}
	return mcpIssuerSuffix + path
}
