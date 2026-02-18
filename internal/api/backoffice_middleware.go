package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"log"
	"net/http"
	"strings"

	"preactvillacarmen/internal/httpx"
)

const boSessionCookieName = "bo_session"

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
			name               string
			isSuper            int
			role               sql.NullString
		)
		err = s.db.QueryRowContext(r.Context(), `
			SELECT
				s.id,
				s.user_id,
				s.active_restaurant_id,
				u.email,
				u.name,
				u.is_superadmin,
				ur.role
			FROM bo_sessions s
			JOIN bo_users u ON u.id = s.user_id
			LEFT JOIN bo_user_restaurants ur
				ON ur.user_id = s.user_id AND ur.restaurant_id = s.active_restaurant_id
			WHERE s.token_sha256 = ? AND s.expires_at > NOW()
			LIMIT 1
		`, tokenSHA).Scan(&sessionID, &userID, &activeRestaurantID, &email, &name, &isSuper, &role)
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

		// Best-effort heartbeat.
		_, _ = s.db.ExecContext(r.Context(), "UPDATE bo_sessions SET last_seen_at = NOW() WHERE id = ?", sessionID)

		a := boAuth{
			SessionID:   sessionID,
			TokenSHA256: tokenSHA,
			User: boUser{
				ID:             userID,
				Email:          email,
				Name:           name,
				Role:           roleSlug,
				RoleImportance: roleImportance,
				SectionAccess:  sectionAccess,
				isSuperadmin:   isSuper != 0,
			},
			Role:               roleSlug,
			ActiveRestaurantID: activeRestaurantID,
		}
		next.ServeHTTP(w, r.WithContext(withBOAuth(r.Context(), a)))
	})
}
