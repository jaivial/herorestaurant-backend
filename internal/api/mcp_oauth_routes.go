package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	"strings"

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
func (s *Server) mcpRequireAuth(r *http.Request) (boAuth, error) {
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(raw) < 7 || !strings.EqualFold(raw[:7], "Bearer ") {
		return boAuth{}, errChatGPTPluginUnauthorized
	}
	return s.mcpResolveToken(r.Context(), raw[7:])
}

func mcpOAuthError(w http.ResponseWriter, status int, code, desc string) {
	httpx.WriteJSON(w, status, map[string]any{"error": code, "error_description": desc})
}

// HandleMCPProtectedResourceMetadata publishes the metadata an MCP client needs
// to discover where to authenticate. This is what points ChatGPT at the
// authorization and token endpoints.
func (s *Server) HandleMCPProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	base := s.chatgptPluginBaseURL(r)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"resource":                 base,
		"authorization_servers":    []string{base},
		"scopes_supported":         []string{"villacarmen"},
		"bearer_methods_supported": []string{"header"},
	})
}

// HandleMCPAuthorizationServerMetadata publishes the AS capabilities, including
// the PKCE methods this server will accept.
func (s *Server) HandleMCPAuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	base := s.chatgptPluginBaseURL(r)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/mcp/oauth/authorize",
		"token_endpoint":                        base + "/mcp/oauth/token",
		"registration_endpoint":                 base + "/mcp/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{"villacarmen"},
	})
}

// HandleMCPRegister is the dynamic client registration an MCP client calls to
// obtain a client_id. No secret is issued: public clients cannot hold one.
func (s *Server) HandleMCPRegister(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ClientName string `json:"client_name"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, chatgptPluginMaxBodyBytes)).Decode(&in)
	clientID, _, err := newBOSessionToken()
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "could not register client")
		return
	}
	if err := s.mcpEnsureClient(r.Context(), clientID, in.ClientName); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "could not register client")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  clientID,
		"client_name":                in.ClientName,
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code"},
		"response_types":             []string{"code"},
		"redirect_uris":              []string{},
	})
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
		_, _ = s.db.ExecContext(r.Context(), `UPDATE mcp_oauth_tokens SET revoked_at = NOW() WHERE token_hash = ?`, sha256Hex(raw))
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
		mr.Get("/oauth/authorize", s.HandleMCPAuthorize)
		mr.Post("/oauth/token", s.HandleMCPToken)
		mr.Post("/oauth/revoke", s.HandleMCPRevoke)
		mr.Post("/", s.HandleMCP)
	})

	// Standard discovery documents at the root, where MCP clients look first.
	r.Get("/.well-known/oauth-authorization-server", s.HandleMCPAuthorizationServerMetadata)
	r.Get("/.well-known/oauth-authorization-server/mcp", s.HandleMCPAuthorizationServerMetadata)
}
