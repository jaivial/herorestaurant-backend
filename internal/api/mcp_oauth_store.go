package api

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// OAuth 2.1 authorization-code store for the MCP server. Every secret is held
// as a SHA-256 digest: the plaintext authorization code and access token exist
// only in the HTTP response that carries them to the client.
//
// Coordination id: mcp_server_v1

var (
	errMCPInvalidGrant = errors.New("invalid_grant")
	errMCPCodeExpired  = errors.New("expired")
)

const (
	mcpCodeTTL  = 10 * time.Minute
	mcpTokenTTL = 12 * time.Hour
)

// mcpTokenRow mirrors the joined OAuth token + user + restaurant row.
type mcpTokenRow struct {
	ID           int64
	UserID       int
	RestaurantID int
	UserName     string
	UserEmail    string
	IsSuperadmin bool
	Role         string
	AppVersion   string
}

type mcpOAuthCode struct {
	ClientID     string
	UserID       int
	RestaurantID int
	RedirectURI  string
	Challenge    string
	Scope        string
}

// mcpEnsureClient registers the public client ChatGPT uses. Public clients
// have no secret; PKCE S256 is what protects the code exchange.
func (s *Server) mcpEnsureClient(ctx context.Context, clientID, name string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO mcp_oauth_clients (client_id, name) VALUES (?, ?)
		ON DUPLICATE KEY UPDATE name = VALUES(name)
	`, clientID, nullString(name))
	return err
}

// mcpStoreCode persists a single-use authorization code, returning the
// plaintext exactly once.
func (s *Server) mcpStoreCode(ctx context.Context, c mcpOAuthCode) (string, error) {
	code, _, err := newBOSessionToken()
	if err != nil {
		return "", err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO mcp_oauth_codes
			(code_hash, client_id, user_id, restaurant_id, redirect_uri, code_challenge, scope, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, sha256Hex(code), c.ClientID, c.UserID, c.RestaurantID, c.RedirectURI, c.Challenge, nullString(c.Scope), time.Now().Add(mcpCodeTTL))
	if err != nil {
		return "", err
	}
	return code, nil
}

// mcpRedeemCode consumes an authorization code atomically. The UPDATE only
// matches when used_at IS NULL, so two concurrent exchanges cannot both win.
func (s *Server) mcpRedeemCode(ctx context.Context, code, clientID, verifier string) (mcpOAuthCode, error) {
	var out mcpOAuthCode
	var challenge, method string
	var expires time.Time
	var used sql.NullTime

	err := s.db.QueryRowContext(ctx, `
		SELECT client_id, user_id, restaurant_id, redirect_uri, code_challenge, code_challenge_method, scope, expires_at, used_at
		FROM mcp_oauth_codes WHERE code_hash = ?
	`, sha256Hex(strings.TrimSpace(code))).Scan(
		&out.ClientID, &out.UserID, &out.RestaurantID, &out.RedirectURI, &challenge, &method, &out.Scope, &expires, &used)
	if err != nil {
		return out, errMCPInvalidGrant
	}
	if out.ClientID != clientID {
		return out, errMCPInvalidGrant
	}
	if used.Valid || time.Now().After(expires) {
		return out, errMCPCodeExpired
	}
	// PKCE S256: the verifier must hash to the challenge sent at authorize time.
	if !mcpVerifyPKCE(verifier, challenge) {
		return out, errMCPInvalidGrant
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE mcp_oauth_codes SET used_at = NOW() WHERE code_hash = ? AND used_at IS NULL
	`, sha256Hex(strings.TrimSpace(code)))
	if err != nil {
		return out, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return out, errMCPInvalidGrant
	}
	return out, nil
}

// mcpIssueToken mints an access token bound to the authorized user and
// restaurant. Only the digest is stored.
func (s *Server) mcpIssueToken(ctx context.Context, clientID string, userID, restaurantID int, scope string) (string, error) {
	token, _, err := newBOSessionToken()
	if err != nil {
		return "", err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO mcp_oauth_tokens (token_hash, client_id, user_id, restaurant_id, scope, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, sha256Hex(token), clientID, userID, restaurantID, nullString(scope), time.Now().Add(mcpTokenTTL))
	if err != nil {
		return "", err
	}
	return token, nil
}

// mcpResolveToken turns an access token into a fully populated boAuth, so MCP
// tools run under exactly the same ACL as the plugin and the backoffice. The
// role, importance and sections are resolved with the same helpers the plugin
// uses, and advisory scopes can only narrow them, never widen them.
func (s *Server) mcpResolveToken(ctx context.Context, token string) (boAuth, error) {
	token = strings.TrimSpace(token)
	if token == "" || s.db == nil {
		return boAuth{}, errChatGPTPluginUnauthorized
	}
	tokenSHA := sha256Hex(token)

	var (
		row     mcpTokenRow
		scope   sql.NullString
		revoked sql.NullTime
		expires time.Time
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT
			t.id, t.user_id, t.restaurant_id, t.scope, t.expires_at, t.revoked_at,
			u.name, u.email, u.is_superadmin,
			COALESCE(ur.role, ''), COALESCE(ur.app_version, '')
		FROM mcp_oauth_tokens t
		JOIN bo_users u ON u.id = t.user_id
		LEFT JOIN bo_user_restaurants ur
			ON ur.user_id = t.user_id AND ur.restaurant_id = t.restaurant_id
		WHERE t.token_hash = ?
		LIMIT 1
	`, tokenSHA).Scan(
		&row.ID, &row.UserID, &row.RestaurantID, &scope, &expires, &revoked,
		&row.UserName, &row.UserEmail, &row.IsSuperadmin,
		&row.Role, &row.AppVersion,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return boAuth{}, errChatGPTPluginUnauthorized
		}
		return boAuth{}, err
	}
	if revoked.Valid || time.Now().After(expires) {
		return boAuth{}, errChatGPTPluginUnauthorized
	}

	role := normalizeBORole(row.Role)
	if row.IsSuperadmin {
		role = "root"
	} else if role == "" {
		role = "admin"
	}
	importance, err := s.roleImportance(ctx, role)
	if err != nil {
		return boAuth{}, err
	}
	appVersion := normalizeAppVersion(row.AppVersion)
	sectionAccess, err := s.roleSectionsForVersion(ctx, role, appVersion)
	if err != nil {
		return boAuth{}, err
	}

	// Best-effort usage stamp; never fail the request on it.
	_, _ = s.db.ExecContext(ctx, `UPDATE mcp_oauth_tokens SET expires_at = expires_at WHERE id = ?`, row.ID)

	auth := boAuth{
		TokenSHA256:        tokenSHA,
		Role:               role,
		ActiveRestaurantID: row.RestaurantID,
		User: boUser{
			ID:             row.UserID,
			Email:          row.UserEmail,
			Name:           row.UserName,
			Role:           role,
			RoleImportance: importance,
			SectionAccess:  sectionAccess,
			AppVersion:     appVersion,
			isSuperadmin:   row.IsSuperadmin,
		},
	}
	auth.User.SectionAccess = chatgptPluginNarrowSections(auth.User.SectionAccess, chatgptPluginScope(scope.String))
	return auth, nil
}

// mcpRevokeClientTokens kills every live token for a client (logout / reset).
func (s *Server) mcpRevokeClientTokens(ctx context.Context, clientID string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE mcp_oauth_tokens SET revoked_at = NOW() WHERE client_id = ? AND revoked_at IS NULL
	`, clientID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
