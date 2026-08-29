package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
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

		now := time.Now()
		if entry, ok := s.sessionCache.get(tokenSHA, now); ok {
			finishBOAuth(w, r, entry.auth, entry.expiresAt, next)
			return
		}

		authCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		auth, lastSeenAt, expiresAt, err := s.loadBOSessionAuth(authCtx, tokenSHA)
		if err != nil {
			if err == errBOSessionNotFound || err == errBOSessionNoRole {
				log.Printf("[requireBOSession] token not found in DB, tokenSHA=%s", tokenSHA[:16])
				httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
				return
			}
			log.Printf("[requireBOSession] DB error: %v", err)
			httpx.WriteError(w, http.StatusInternalServerError, "Error validating session")
			return
		}

		ttl := boSessionTTLForRequest(r)
		movingExpiresAt := now.Add(ttl).Truncate(time.Second)
		refresh := shouldRefreshBOSession(lastSeenAt, now) || movingExpiresAt.Before(expiresAt)
		if refresh {
			if _, err := s.db.ExecContext(authCtx, "UPDATE bo_sessions SET last_seen_at = NOW(), expires_at = ? WHERE id = ?", movingExpiresAt, auth.SessionID); err != nil {
				log.Printf("[requireBOSession] DB heartbeat error: %v", err)
				httpx.WriteError(w, http.StatusInternalServerError, "Error validating session")
				return
			}
			setBOSessionCookie(w, r, token, movingExpiresAt, ttl)
			expiresAt = movingExpiresAt
		}

		s.sessionCache.set(tokenSHA, boSessionCacheEntry{
			auth:       auth,
			lastSeenAt: lastSeenAt,
			expiresAt:  expiresAt,
			cachedAt:   now,
		})
		finishBOAuth(w, r, auth, expiresAt, next)
	})
}

// finishBOAuth attaches the resolved auth to the request context and continues.
func finishBOAuth(w http.ResponseWriter, r *http.Request, a boAuth, expiresAt time.Time, next http.Handler) {
	w.Header().Set(boSessionMovingExpirationHeader, expiresAt.UTC().Format(time.RFC3339))
	next.ServeHTTP(w, r.WithContext(withRestaurantID(withBOAuth(r.Context(), a), a.ActiveRestaurantID)))
}

var (
	errBOSessionNotFound = errors.New("bo session not found")
	errBOSessionNoRole   = errors.New("bo session has no role")
)

// loadBOSessionAuth resolves the session+roles from the DB and builds the
// boAuth for the given token SHA. It returns the last_seen/expiry timestamps so
// callers can decide whether to extend the moving expiration.
func (s *Server) loadBOSessionAuth(ctx context.Context, tokenSHA string) (boAuth, time.Time, time.Time, error) {
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
		appVersionRaw      sql.NullString
		memberIDRaw        sql.NullInt64
		lastSeenAt         sql.NullTime
		currentExpiresAt   time.Time
	)
	err := s.db.QueryRowContext(ctx, `
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
			ur.app_version,
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
		&appVersionRaw,
		&memberIDRaw,
		&lastSeenAt,
		&currentExpiresAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return boAuth{}, time.Time{}, time.Time{}, errBOSessionNotFound
		}
		return boAuth{}, time.Time{}, time.Time{}, err
	}

	if isSuper == 0 && !role.Valid {
		return boAuth{}, time.Time{}, time.Time{}, errBOSessionNoRole
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
	appVersion := normalizeAppVersion(appVersionRaw.String)
	sectionAccess, err := s.roleSectionsForVersion(ctx, roleSlug, appVersion)
	if err != nil {
		return boAuth{}, time.Time{}, time.Time{}, err
	}

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
			AppVersion:     appVersion,
			MustChangePass: mustChangePassword != 0,
			isSuperadmin:   isSuper != 0,
		},
		Role:               roleSlug,
		ActiveRestaurantID: activeRestaurantID,
		MemberID:           memberID,
	}
	return a, lastSeenAt.Time, currentExpiresAt, nil
}

func shouldRefreshBOSession(lastSeenAt time.Time, now time.Time) bool {
	return lastSeenAt.IsZero() || !lastSeenAt.After(now.Add(-time.Minute))
}
