package api

import (
	"context"
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
		authCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

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
			roleImportanceRaw  sql.NullInt64
			memberIDRaw        sql.NullInt64
			lastSeenAt         sql.NullTime
			currentExpiresAt   time.Time
		)
		err = s.db.QueryRowContext(authCtx, `
			SELECT
				s.id,
				s.user_id,
				s.active_restaurant_id,
				u.email,
				u.username,
				u.name,
				u.is_superadmin,
				u.must_change_password,
				ur.role,
				br.importance,
				rm.id,
				s.last_seen_at,
				s.expires_at
			FROM bo_sessions s
			JOIN bo_users u ON u.id = s.user_id
			LEFT JOIN bo_user_restaurants ur
				ON ur.user_id = s.user_id AND ur.restaurant_id = s.active_restaurant_id
			LEFT JOIN bo_roles br
				ON br.slug = CASE WHEN u.is_superadmin <> 0 THEN 'root' ELSE ur.role END
				AND br.is_active = 1
			LEFT JOIN restaurant_members rm
				ON rm.bo_user_id = s.user_id AND rm.restaurant_id = s.active_restaurant_id AND rm.is_active = 1
			WHERE s.token_sha256 = ? AND s.expires_at > NOW()
			LIMIT 1
		`, tokenSHA).Scan(
			&sessionID,
			&userID,
			&activeRestaurantID,
			&email,
			&username,
			&name,
			&isSuper,
			&mustChangePassword,
			&role,
			&roleImportanceRaw,
			&memberIDRaw,
			&lastSeenAt,
			&currentExpiresAt,
		)
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

		roleImportance := defaultRoleImportance[roleSlug]
		if roleImportanceRaw.Valid {
			roleImportance = int(roleImportanceRaw.Int64)
			if roleImportance < 0 {
				roleImportance = 0
			} else if roleImportance > 100 {
				roleImportance = 100
			}
		}
		sectionAccess, err := s.roleSections(authCtx, roleSlug)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error validating session role")
			return
		}

		ttl := boSessionTTLForRequest(r)
		now := time.Now()
		movingExpiresAt := now.Add(ttl).Truncate(time.Second)
		refresh := shouldRefreshBOSession(lastSeenAt.Time, now) || movingExpiresAt.Before(currentExpiresAt)
		if refresh {
			if _, err := s.db.ExecContext(authCtx, "UPDATE bo_sessions SET last_seen_at = NOW(), expires_at = ? WHERE id = ?", movingExpiresAt, sessionID); err != nil {
				log.Printf("[requireBOSession] DB heartbeat error: %v", err)
				httpx.WriteError(w, http.StatusInternalServerError, "Error validating session")
				return
			}
			setBOSessionCookie(w, r, token, movingExpiresAt, ttl)
			currentExpiresAt = movingExpiresAt
		}
		w.Header().Set(boSessionMovingExpirationHeader, currentExpiresAt.UTC().Format(time.RFC3339))

		var memberID *int64
		if memberIDRaw.Valid {
			mid := memberIDRaw.Int64
			memberID = &mid
		}

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
			MemberID:           memberID,
		}
		next.ServeHTTP(w, r.WithContext(withBOAuth(r.Context(), a)))
	})
}

func shouldRefreshBOSession(lastSeenAt time.Time, now time.Time) bool {
	return lastSeenAt.IsZero() || !lastSeenAt.After(now.Add(-time.Minute))
}
