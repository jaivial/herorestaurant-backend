package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

const boSessionCookieName = "bo_session"
const boSessionMovingExpirationHeader = httpx.MovingExpirationHeader

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (s *Server) requireBOSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(boSessionCookieName)
		if err != nil || strings.TrimSpace(c.Value) == "" {
			log.Printf("[requireBOSession] UNAUTHORIZED no cookie, path=%s", r.URL.Path)
			httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		token := strings.TrimSpace(c.Value)
		tokenSHA := sha256Hex(token)

		var (
			sessionID          int64
			userID             int
			activeRestaurantID int
			email              string
			username           sql.NullString
			name               string
			isSuper            int
			mustChangePassword int
			role               sql.NullString
		)
		err = s.db.QueryRowContext(r.Context(), `
			SELECT
				s.id,
				s.user_id,
				s.active_restaurant_id,
				u.email,
				u.username,
				u.name,
				u.is_superadmin,
				u.must_change_password,
				ur.role
			FROM bo_sessions s
			JOIN bo_users u ON u.id = s.user_id
			LEFT JOIN bo_user_restaurants ur
				ON ur.user_id = s.user_id AND ur.restaurant_id = s.active_restaurant_id
			WHERE s.token_sha256 = ? AND s.expires_at > NOW()
			LIMIT 1
		`, tokenSHA).Scan(&sessionID, &userID, &activeRestaurantID, &email, &username, &name, &isSuper, &mustChangePassword, &role)
		if err != nil {
			if err == sql.ErrNoRows {
				log.Printf("[requireBOSession] token not found in DB, tokenSHA=%s", tokenSHA[:16])
				httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
				return
			}
			log.Printf("[requireBOSession] DB error: %v", err)
			httpx.WriteError(w, http.StatusInternalServerError, "Error validating session")
			return
		}

		if isSuper == 0 && !role.Valid {
			httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}

		roleSlug := normalizeBORole(role.String)
		if isSuper != 0 {
			roleSlug = "root"
		} else if roleSlug == "" {
			roleSlug = "admin"
		}

		roleImportance, err := s.roleImportance(r.Context(), roleSlug)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error validating session role")
			return
		}
		sectionAccess, err := s.roleSections(r.Context(), roleSlug)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error validating session role")
			return
		}

		ttl := boSessionTTLForRequest(r)
		movingExpiresAt := time.Now().Add(ttl).Truncate(time.Second)
		if _, err := s.db.ExecContext(r.Context(), "UPDATE bo_sessions SET last_seen_at = NOW(), expires_at = ? WHERE id = ?", movingExpiresAt, sessionID); err != nil {
			log.Printf("[requireBOSession] DB heartbeat error: %v", err)
			httpx.WriteError(w, http.StatusInternalServerError, "Error validating session")
			return
		}

		setBOSessionCookie(w, r, token, movingExpiresAt, ttl)
		w.Header().Set(boSessionMovingExpirationHeader, movingExpiresAt.UTC().Format(time.RFC3339))

		a := boAuth{
			SessionID:   sessionID,
			TokenSHA256: tokenSHA,
			User: boUser{
				ID:             userID,
				Email:          email,
				Username:       strings.TrimSpace(username.String),
				Name:           name,
				Role:           roleSlug,
				RoleImportance: roleImportance,
				SectionAccess:  sectionAccess,
				MustChangePass: mustChangePassword != 0,
				isSuperadmin:   isSuper != 0,
			},
			Role:               roleSlug,
			ActiveRestaurantID: activeRestaurantID,
		}
		next.ServeHTTP(w, r.WithContext(withBOAuth(r.Context(), a)))
	})
}
