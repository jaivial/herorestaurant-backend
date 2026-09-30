package api

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

// Coordination id: chatgpt_plugin_v1
//
// A ChatGPT plugin credential is a bearer token pinned to exactly one
// restaurant and one backoffice user. Resolution returns a real boAuth, so
// every downstream ACL check (section gates, tool permissions, confirmation
// tokens) behaves exactly as it does for the backoffice session that minted
// it. There is no separate, wider permission model for the plugin.

// errChatGPTPluginUnauthorized is returned for every rejection reason so the
// HTTP layer never discloses whether a token exists, is revoked or belongs to
// a restaurant the caller may not see.
var errChatGPTPluginUnauthorized = errors.New("unauthorized")

type chatgptPluginTokenRow struct {
	ID            int64
	UserID        int
	RestaurantID  int
	Label         string
	Scopes        sql.NullString
	RevokedAt     sql.NullTime
	TokenHash     string
	Role          string
	AppVersion    string
	IsSuperadmin  bool
	UserName      string
	UserEmail     string
	RoleImportanc int
}

// IssueChatGPTPluginToken mints a plugin bearer token for a backoffice user
// scoped to a single restaurant and returns the raw token. The raw value is
// never persisted: only its SHA-256 digest is stored, so the secret is
// recoverable exactly once, at generation time.
func (s *Server) IssueChatGPTPluginToken(ctx context.Context, userID, restaurantID int, label string) (string, error) {
	if s.db == nil {
		return "", errors.New("database unavailable")
	}
	if userID <= 0 || restaurantID <= 0 {
		return "", errors.New("user and restaurant are required")
	}
	// Never mint a credential for a restaurant the user is not assigned to:
	// the token is a permanent bearer, so a wrong pairing would be a
	// privilege escalation rather than a mistake.
	ok, err := s.userHasRestaurant(ctx, userID, restaurantID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("user is not assigned to that restaurant")
	}

	token, tokenSHA, err := newBOSessionToken()
	if err != nil {
		return "", err
	}
	label = strings.TrimSpace(label)
	if len(label) > 128 {
		label = label[:128]
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO chatgpt_plugin_tokens (user_id, restaurant_id, token_hash, label)
		VALUES (?, ?, ?, ?)
	`, userID, restaurantID, tokenSHA, nullString(label))
	if err != nil {
		return "", err
	}
	return token, nil
}

// userHasRestaurant reports whether the backoffice user is assigned to the
// restaurant (superadmins are assigned implicitly through their role).
func (s *Server) userHasRestaurant(ctx context.Context, userID, restaurantID int) (bool, error) {
	if s.db == nil {
		return false, errors.New("database unavailable")
	}
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM bo_user_restaurants ur
		JOIN bo_users u ON u.id = ur.user_id
		WHERE ur.user_id = ? AND ur.restaurant_id = ? AND (u.is_superadmin <> 0 OR ur.role IS NOT NULL)
	`, userID, restaurantID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// RevokeChatGPTPluginTokens disables every plugin token of a user, optionally
// limited to one restaurant. Returns how many credentials were revoked.
func (s *Server) RevokeChatGPTPluginTokens(ctx context.Context, userID, restaurantID int) (int64, error) {
	if s.db == nil {
		return 0, errors.New("database unavailable")
	}
	if userID <= 0 {
		return 0, errors.New("user is required")
	}
	q := `UPDATE chatgpt_plugin_tokens SET revoked_at = NOW() WHERE user_id = ? AND revoked_at IS NULL`
	args := []any{userID}
	if restaurantID > 0 {
		q += ` AND restaurant_id = ?`
		args = append(args, restaurantID)
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// resolveChatGPTPluginToken turns a raw bearer token into a fully populated
// boAuth for its pinned restaurant. A revoked or unknown token is rejected
// identically so the endpoint cannot be used as a token oracle.
func (s *Server) resolveChatGPTPluginToken(ctx context.Context, token string) (boAuth, error) {
	token = strings.TrimSpace(token)
	if token == "" || s.db == nil {
		return boAuth{}, errChatGPTPluginUnauthorized
	}
	tokenSHA := sha256Hex(token)

	var row chatgptPluginTokenRow
	err := s.db.QueryRowContext(ctx, `
		SELECT
			t.id, t.user_id, t.restaurant_id, t.token_hash,
			COALESCE(t.label, ''), COALESCE(t.scopes_json, ''), t.revoked_at,
			u.name, u.email, u.is_superadmin,
			COALESCE(ur.role, ''), COALESCE(ur.app_version, '')
		FROM chatgpt_plugin_tokens t
		JOIN bo_users u ON u.id = t.user_id
		LEFT JOIN bo_user_restaurants ur
			ON ur.user_id = t.user_id AND ur.restaurant_id = t.restaurant_id
		WHERE t.token_hash = ?
		LIMIT 1
	`, tokenSHA).Scan(
		&row.ID, &row.UserID, &row.RestaurantID, &row.TokenHash,
		&row.Label, &row.Scopes, &row.RevokedAt,
		&row.UserName, &row.UserEmail, &row.IsSuperadmin,
		&row.Role, &row.AppVersion,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return boAuth{}, errChatGPTPluginUnauthorized
		}
		return boAuth{}, err
	}
	if row.RevokedAt.Valid {
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

	// Best-effort usage stamp. A failure here must never fail the request, so
	// it is deliberately ignored.
	_, _ = s.db.ExecContext(ctx, `UPDATE chatgpt_plugin_tokens SET last_used_at = NOW() WHERE id = ?`, row.ID)

	// Advisory scopes are attached for auditing/observability only. They never
	// widen access: the RBAC role above is the sole authorization source.
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
	auth.User.SectionAccess = chatgptPluginNarrowSections(auth.User.SectionAccess, chatgptPluginScope(row.Scopes.String))
	return auth, nil
}

// chatgptPluginNarrowSections intersects the role sections with the token's
// optional scopes. It can only remove capabilities, never add them, so a
// misconfigured scope list degrades safely instead of escalating.
func chatgptPluginNarrowSections(roleSections, scopes []string) []string {
	if len(scopes) == 0 {
		return roleSections
	}
	allowed := make(map[string]bool, len(scopes))
	for _, s := range scopes {
		allowed[strings.ToLower(strings.TrimSpace(s))] = true
	}
	out := make([]string, 0, len(roleSections))
	for _, section := range roleSections {
		if allowed[strings.ToLower(strings.TrimSpace(section))] {
			out = append(out, section)
		}
	}
	return out
}

// chatgptPluginBearerToken extracts the plugin credential from the request.
// Authorization is the transport defined by the ChatGPT plugin spec, so the
// conventional "Bearer" scheme is the only one accepted.
func chatgptPluginBearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

// requireChatGPTPluginAuth resolves the plugin token into a boAuth and pins the
// request to the token's restaurant. It is the only entry point for the plugin
// surface: every handler downstream reuses the backoffice ACL.
func (s *Server) requireChatGPTPluginAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, err := s.resolveChatGPTPluginToken(r.Context(), chatgptPluginBearerToken(r))
		if err != nil {
			if errors.Is(err, errChatGPTPluginUnauthorized) {
				httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
				return
			}
			// Log the cause: a swallowed error here is undiagnosable in
			// production, and the client only ever sees a generic 500.
			log.Printf("[chatgpt_plugin_v1] token resolution failed: %v", err)
			httpx.WriteError(w, http.StatusInternalServerError, "Error validating plugin token")
			return
		}
		next.ServeHTTP(w, r.WithContext(withRestaurantID(withBOAuth(r.Context(), auth), auth.ActiveRestaurantID)))
	})
}

// nullString maps an empty label to SQL NULL so the column keeps its
// "optional" meaning.
func nullString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

// chatgptPluginScope parses the optional comma-separated scope list attached to
// a token. It is advisory metadata for the operator; authorization always
// comes from the user's RBAC role.
func chatgptPluginScope(v string) []string {
	out := []string{}
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// chatgptPluginIdleExpiry bounds the lifetime of the per-request execution
// context so a misbehaving tool cannot pin a connection indefinitely.
const chatgptPluginIdleExpiry = 30 * time.Second

// PluginTokenService is the exported surface of the plugin token store, used by
// the cmd/chatgpt-plugin-token generator. It exists so the CLI can mint and
// revoke credentials without booting the whole server (NewServer starts
// background loops and requires a full config).
type PluginTokenService struct{ s *Server }

// NewPluginTokenService builds the token service on an existing connection.
func NewPluginTokenService(db *sql.DB) *PluginTokenService {
	return &PluginTokenService{s: &Server{db: db}}
}

// Issue mints a plugin token for a user scoped to a restaurant.
func (p *PluginTokenService) Issue(ctx context.Context, userID, restaurantID int, label string) (string, error) {
	return p.s.IssueChatGPTPluginToken(ctx, userID, restaurantID, label)
}

// Revoke disables the user's plugin tokens, optionally for one restaurant only.
func (p *PluginTokenService) Revoke(ctx context.Context, userID, restaurantID int) (int64, error) {
	return p.s.RevokeChatGPTPluginTokens(ctx, userID, restaurantID)
}
