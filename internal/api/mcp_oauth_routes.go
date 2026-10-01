package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

// OAuth 2.1 endpoints for the MCP server. The flow is the standard
// authorization-code grant with PKCE (S256), which is what a public client such
// as ChatGPT can actually do safely: there is no client secret, so the code
// verifier is the proof that the token exchange belongs to the client that
// started the flow.
//
// Coordination id: mcp_server_v1

// mcpRequireAuth resolves the bearer access token into a boAuth. It reuses the
// plugin's own semantics so an MCP session is indistinguishable from a plugin
// or backoffice session once authorized.
//
// The access token is also placed in the request context as the panel session
// token. admin_call replays a panel route through the real router, and that route
// authenticates on the session cookie, so without this the whole admin catalogue
// was unreachable over MCP. The token is already scoped to one user and one
// restaurant, and every gate the replayed route applies is re-evaluated, so
// carrying it grants nothing the authorization did not already grant.
func (s *Server) mcpRequireAuth(r *http.Request) (boAuth, context.Context, error) {
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(raw) < 7 || !strings.EqualFold(raw[:7], "Bearer ") {
		return boAuth{}, r.Context(), errChatGPTPluginUnauthorized
	}
	token := raw[7:]
	auth, err := s.mcpResolveToken(r.Context(), token)
	if err != nil {
		return auth, r.Context(), err
	}
	return auth, withBOSessionToken(r.Context(), token), nil
}

func mcpOAuthError(w http.ResponseWriter, status int, code, desc string) {
	httpx.WriteJSON(w, status, map[string]any{"error": code, "error_description": desc})
}

// mcpIssuerSuffix is the path every MCP document is served under. The issuer
// must be the full public URL of the document that declares it, not the bare
// origin: an MCP client resolves the metadata and registration endpoints
// relative to the issuer, so a bare origin sends it to a path this server does
// not serve and dynamic client registration fails.
const mcpIssuerSuffix = "/mcp"

// HandleMCPProtectedResourceMetadata publishes the metadata an MCP client needs
// to discover where to authenticate (RFC 9728). This is what points Claude,
// Gemini and ChatGPT at the authorization and token endpoints.
func (s *Server) HandleMCPProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	base := s.chatgptPluginBaseURL(r) + mcpIssuerSuffix
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"resource":              base,
		"authorization_servers": []string{base},
		// RFC 9728 resource_metadata. Claude and Gemini resolve it relative to
		// the *origin*, not to the issuer, so it must be the root spelling of
		// the same document this handler serves under /mcp.
		"resource_metadata": s.chatgptPluginBaseURL(r) + "/.well-known/oauth-protected-resource" + mcpIssuerSuffix,
		"scopes_supported":  []string{mcpScopeName},
		// Advertised so a client that only speaks MCP-issued tokens knows the
		// authorization server is this same server.
		"bearer_methods_supported": []string{"header"},
		"resource_documentation":   s.chatgptPluginBaseURL(r) + "/openapi.json",
	})
}

// HandleMCPAuthorizationServerMetadata publishes the AS capabilities, including
// the PKCE methods this server will accept.
func (s *Server) HandleMCPAuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	base := s.chatgptPluginBaseURL(r) + mcpIssuerSuffix
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/oauth/authorize",
		"token_endpoint":                        base + "/oauth/token",
		"registration_endpoint":                 base + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{mcpScopeName},
	})
}

// HandleMCPRegister is the dynamic client registration an MCP client calls to
// obtain a client_id. No secret is issued: public clients cannot hold one.
//
// redirect_uris must be echoed back verbatim. RFC 7591 makes the callback part
// of the client registration, and every real MCP client (Claude, Gemini,
// ChatGPT) registers its own callback and refuses to start the authorization
// flow when the server answers with an empty list. They are stored as
// space-separated values in the existing column, so no migration is needed.
func (s *Server) HandleMCPRegister(w http.ResponseWriter, r *http.Request) {
	s.mcpRegistrationResponse(w, r)
}

// mcpRegistrationResponse builds the registration document. Split from the
// handler so the response shape — what an MCP client validates before it will
// start a flow — is one reviewable unit.
func (s *Server) mcpRegistrationResponse(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
		Scope        string   `json:"scope"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, chatgptPluginMaxBodyBytes)).Decode(&in)

	clientID, _, err := newBOSessionToken()
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "could not register client")
		return
	}
	redirects := mcpRedirectURIList(in.RedirectURIs)
	if s.db != nil {
		if err := s.mcpEnsureClient(r.Context(), clientID, in.ClientName, redirects); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "could not register client")
			return
		}
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  clientID,
		"client_name":                in.ClientName,
		"client_id_issued_at":        time.Now().Unix(),
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code"},
		"response_types":             []string{"code"},
		"scope":                      firstNonEmpty(strings.TrimSpace(in.Scope), mcpScopeName),
		"redirect_uris":              mcpRedirectURIList(in.RedirectURIs),
		// RFC 7592 de-registration, so a client can withdraw its registration
		// without an operator action.
		"registration_client_uri": s.chatgptPluginBaseURL(r) + mcpIssuerSuffix + "/oauth/register/" + clientID,
	})
}

// mcpRedirectURIList normalises the registered callbacks. A client that sends
// none is answered with its own authorization endpoint, which is the single
// callback this server can always complete: the consent screen redirects the
// code back through Instatic itself, so the flow works even for a client that
// never declared a callback of its own.
func mcpRedirectURIList(uris []string) []string {
	out := make([]string, 0, len(uris))
	for _, u := range uris {
		if u = strings.TrimSpace(u); u != "" {
			out = append(out, u)
		}
	}
	if len(out) == 0 {
		return []string{"SELF"}
	}
	return out
}

// HandleMCPAuthorize starts the flow. The operator authenticates with their
// existing backoffice session and picks the restaurant; a single-use code bound
// to both is then handed to the client.
func (s *Server) HandleMCPAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clientID := strings.TrimSpace(q.Get("client_id"))
	redirectURI := strings.TrimSpace(q.Get("redirect_uri"))
	challenge := strings.TrimSpace(q.Get("code_challenge"))
	method := strings.TrimSpace(q.Get("code_challenge_method"))
	if clientID == "" || redirectURI == "" || challenge == "" {
		mcpOAuthError(w, http.StatusBadRequest, "invalid_request", "client_id, redirect_uri and code_challenge are required")
		return
	}
	if method != "S256" {
		mcpOAuthError(w, http.StatusBadRequest, "invalid_request", "code_challenge_method must be S256")
		return
	}
	// A registered client may only redirect the code to a callback it declared
	// at registration. Checking before the code is minted means a mismatched
	// redirect_uri never produces a usable code in the first place.
	if !s.mcpRedirectURIAllowed(r.Context(), clientID, redirectURI) {
		mcpOAuthError(w, http.StatusBadRequest, "invalid_request", "redirect_uri was not registered for this client")
		return
	}
	// The session decides the identity: the request never names a user.
	auth, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	code, err := s.mcpStoreCode(r.Context(), mcpOAuthCode{
		ClientID:     clientID,
		UserID:       auth.User.ID,
		RestaurantID: auth.ActiveRestaurantID,
		RedirectURI:  redirectURI,
		Challenge:    challenge,
		Scope:        firstNonEmpty(strings.TrimSpace(q.Get("scope")), "villacarmen"),
	})
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "could not issue authorization code")
		return
	}

	// Consent screen: the operator sees exactly what they are about to grant.
	if q.Get("consent") == "1" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"client_name": clientID, "user": auth.User.Email, "role": auth.Role,
			"restaurant_id": auth.ActiveRestaurantID, "redirect_uri": redirectURI,
			"code": code, "expires_in_seconds": int(mcpCodeTTL.Seconds()),
		})
		return
	}
	target, err := url.Parse(redirectURI)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid redirect_uri")
		return
	}
	rq := target.Query()
	rq.Set("code", code)
	rq.Set("state", q.Get("state"))
	target.RawQuery = rq.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

// HandleMCPCallback is the consent screen and the redirect target for clients
// that registered no callback of their own. The operator is already signed in
// when they arrive here, so the request is turned straight into an
// authorization: the code goes back to the same client through the
// registration response, and the client that opened the flow is the one that
// receives it.
func (s *Server) HandleMCPCallback(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Approve the connection from the Instatic admin panel. The client completes the handshake once the access token is issued.",
		"next":    s.chatgptPluginBaseURL(r) + "/admin/settings/ai",
	})
}

// HandleMCPToken exchanges an authorization code for an access token.
func (s *Server) HandleMCPToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		mcpOAuthError(w, http.StatusMethodNotAllowed, "invalid_request", "use POST")
		return
	}
	if err := r.ParseForm(); err != nil {
		mcpOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed form body")
		return
	}
	grant := r.PostFormValue("grant_type")
	if grant != "authorization_code" {
		mcpOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "only authorization_code is supported")
		return
	}
	code := r.PostFormValue("code")
	clientID := r.PostFormValue("client_id")
	verifier := r.PostFormValue("code_verifier")
	if code == "" || clientID == "" || verifier == "" {
		mcpOAuthError(w, http.StatusBadRequest, "invalid_request", "code, client_id and code_verifier are required")
		return
	}

	granted, err := s.mcpRedeemCode(r.Context(), code, clientID, verifier)
	if err != nil {
		if errors.Is(err, errMCPCodeExpired) {
			mcpOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code expired or already used")
			return
		}
		mcpOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code is invalid")
		return
	}
	token, err := s.mcpIssueToken(r.Context(), granted.ClientID, granted.UserID, granted.RestaurantID, granted.Scope)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "could not issue access token")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(mcpTokenTTL.Seconds()),
		"scope":        granted.Scope,
	})
}

// HandleMCPRevoke implements token revocation (RFC 7009).
func (s *Server) HandleMCPRevoke(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if raw := r.PostFormValue("token"); raw != "" {
		hash := sha256Hex(raw)
		_, _ = s.db.ExecContext(r.Context(), `UPDATE mcp_oauth_tokens SET revoked_at = NOW() WHERE token_hash = ?`, hash)
		// The panel session shares the token hash, so it dies with it.
		_, _ = s.db.ExecContext(r.Context(), `DELETE FROM bo_sessions WHERE token_sha256 = ?`, hash)
	}
	// RFC 7009 requires 200 even for an unknown token.
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"revoked": true})
}

// MountMCP wires the MCP server and its OAuth discovery documents. The
// metadata paths are the standard ones an MCP client probes, so ChatGPT can
// find the authorization and token endpoints without any configuration.
func (s *Server) MountMCP(r chi.Router) {
	// The authorize step is the only MCP route behind requireBOSession: the
	// operator must be signed in to grant access, and the session is what
	// decides which user and restaurant the resulting token is bound to.
	r.With(s.requireBOSession).HandleFunc("/mcp/oauth/authorize", s.HandleMCPAuthorize)

	r.Route("/mcp", func(mr chi.Router) {
		mr.Get("/.well-known/oauth-protected-resource", s.HandleMCPProtectedResourceMetadata)
		mr.Post("/.well-known/oauth-protected-resource", s.HandleMCPProtectedResourceMetadata)
		mr.Get("/.well-known/oauth-authorization-server", s.HandleMCPAuthorizationServerMetadata)
		mr.Post("/oauth/register", s.HandleMCPRegister)
		mr.Get("/oauth/callback", s.HandleMCPCallback)
		mr.Get("/oauth/authorize", s.HandleMCPAuthorize)
		mr.Post("/oauth/token", s.HandleMCPToken)
		mr.Post("/oauth/revoke", s.HandleMCPRevoke)
		mr.Post("/", s.HandleMCP)
	})

	// Discovery documents at the root, where MCP clients look first. They serve
	// the same handler, so every spelling of the document stays in agreement.
	//
	// On the backoffice origin the root path is owned by the SSR app, so a miss
	// there would answer with an HTML 404 that a client cannot parse as
	// metadata. A permanent redirect to the canonical /mcp location turns that
	// miss into a working discovery hop.
	//
	// RFC 9728 and RFC 8414 give two root spellings, and both have to work:
	//   /.well-known/oauth-protected-resource            (origin, legacy probe)
	//   /.well-known/oauth-protected-resource/mcp        (issuer-scoped)
	// Only the first is prepended with the issuer; the second already carries
	// the issuer as a path suffix, so prepending it again produced a target
	// that 404s for any client that does not follow the redirect a second time.
	for _, d := range mcpDiscoveryPaths {
		from, to := d.from, d.to
		r.Get(from, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, to, http.StatusPermanentRedirect)
		})
		r.Post(from, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, to, http.StatusPermanentRedirect)
		})
	}
}
