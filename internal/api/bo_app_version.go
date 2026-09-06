package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

// Backoffice A/B app versions. Stored per user+restaurant in
// bo_user_restaurants.app_version (see migration 111).
const (
	boAppVersion001 = "0.0.1"
	boAppVersion01  = "0.1"
	boAppVersion02  = "0.2"
)

type boAppCapability string

const (
	boCapabilityStock        boAppCapability = "stock"
	boCapabilityPOS          boAppCapability = "pos"
	boCapabilityEstadisticas boAppCapability = "estadisticas"
	boCapabilityPlataforma   boAppCapability = "plataforma"
	boCapabilityAds          boAppCapability = "ads"
	boCapabilityCampanas     boAppCapability = "campanas"
)

var boCapabilityMinVersion = map[boAppCapability]string{
	boCapabilityStock:        boAppVersion02,
	boCapabilityPOS:          boAppVersion02,
	boCapabilityEstadisticas: boAppVersion02,
	boCapabilityPlataforma:   boAppVersion02,
	boCapabilityAds:          boAppVersion02,
	// Campanas ships to every app version: it replaces manual mailing outside
	// the product, so gating it behind 0.2 would leave 0.1 users without it.
	boCapabilityCampanas: boAppVersion001,
}

var boSectionCapability = map[string]boAppCapability{
	boSectionStock:        boCapabilityStock,
	boSectionPOS:          boCapabilityPOS,
	boSectionEstadisticas: boCapabilityEstadisticas,
	boSectionPlataforma:   boCapabilityPlataforma,
}

func parseSupportedBOAppVersion(raw string) (string, bool) {
	version := strings.TrimSpace(raw)
	if version == boAppVersion001 || version == boAppVersion01 || version == boAppVersion02 {
		return version, true
	}
	return "", false
}

func normalizeAppVersion(raw string) string {
	if version, ok := parseSupportedBOAppVersion(raw); ok {
		return version
	}
	return boAppVersion01
}

// appVersionAtLeast compares dotted numeric versions like "0.0.1"/"0.1"/"0.2".
func appVersionAtLeast(version, minimum string) bool {
	va, oka := parseAppVersion(version)
	vb, okb := parseAppVersion(minimum)
	if !oka || !okb {
		return false
	}
	for i := 0; i < 3; i++ {
		if va[i] != vb[i] {
			return va[i] > vb[i]
		}
	}
	return true
}

func parseAppVersion(v string) ([3]int, bool) {
	parts := strings.Split(strings.TrimSpace(v), ".")
	if len(parts) < 2 || len(parts) > 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, part := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// sectionAllowedForAppVersion reports whether a user on `version` may access
// the RBAC section. Sections not gated by version are always allowed here;
// role ACL is enforced separately.
func sectionAllowedForAppVersion(section, version string) bool {
	capability, gated := boSectionCapability[section]
	if !gated {
		return true
	}
	return appCapabilityAllowed(capability, version)
}

func appCapabilityAllowed(capability boAppCapability, version string) bool {
	minVersion, known := boCapabilityMinVersion[capability]
	if !known {
		return false
	}
	return appVersionAtLeast(normalizeAppVersion(version), minVersion)
}

// sectionsForAppVersion filters a section list down to what the version
// unlocks, keeping every section that is not version-gated.
func sectionsForAppVersion(version string, sections []string) []string {
	out := make([]string, 0, len(sections))
	for _, section := range sections {
		if sectionAllowedForAppVersion(section, version) {
			out = append(out, section)
		}
	}
	return out
}

// getBOUserAppVersionForRestaurant resolves the app version of a user in a
// restaurant, defaulting to 0.1 when there is no link row (e.g. superadmins
// without an explicit bo_user_restaurants entry).
func (s *Server) getBOUserAppVersionForRestaurant(ctx context.Context, userID, restaurantID int) (string, error) {
	var raw sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT app_version
		FROM bo_user_restaurants
		WHERE user_id = ? AND restaurant_id = ?
		LIMIT 1
	`, userID, restaurantID).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return boAppVersion01, nil
		}
		return "", err
	}
	return normalizeAppVersion(raw.String), nil
}

func (s *Server) requireBOCapability(capability boAppCapability) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a, ok := boAuthFromContext(r.Context())
			if !ok {
				httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
				return
			}
			if !appCapabilityAllowed(capability, a.User.AppVersion) {
				httpx.WriteError(w, http.StatusForbidden, "Forbidden")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type boPatchUserVersionRequest struct {
	AppVersion string `json:"appVersion"`
}

// handleBOUserVersionPatch sets a user's app version for the active restaurant.
// Route-gated to root (importance >= 100). Mirrors handleBOUserRolePatch: it
// upserts the bo_user_restaurants link so the change is per restaurant.
func (s *Server) handleBOUserVersionPatch(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	rawID := strings.TrimSpace(chi.URLParam(r, "id"))
	userID, err := strconv.Atoi(rawID)
	if err != nil || userID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "id invalido",
		})
		return
	}

	var req boPatchUserVersionRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}
	version, valid := parseSupportedBOAppVersion(req.AppVersion)
	if !valid {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Version invalida",
		})
		return
	}

	// The target must belong to the active restaurant (member or user link).
	var targetIsSuper int
	err = s.db.QueryRowContext(r.Context(), `
		SELECT u.is_superadmin
		FROM bo_users u
		LEFT JOIN bo_user_restaurants ur
			ON ur.user_id = u.id AND ur.restaurant_id = ?
		LEFT JOIN restaurant_members m
			ON m.restaurant_id = ?
			AND m.is_active = 1
			AND (
				m.bo_user_id = u.id
				OR (
					m.bo_user_id IS NULL
					AND m.email IS NOT NULL
					AND LOWER(TRIM(m.email)) = LOWER(TRIM(u.email))
				)
			)
		WHERE
			u.id = ?
			AND (ur.user_id IS NOT NULL OR m.id IS NOT NULL OR u.is_superadmin = 1)
		LIMIT 1
	`, a.ActiveRestaurantID, a.ActiveRestaurantID, userID).Scan(&targetIsSuper)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
				"success": false,
				"message": "Usuario no encontrado",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando usuario")
		return
	}

	if _, err := s.db.ExecContext(r.Context(), `
		INSERT INTO bo_user_restaurants (user_id, restaurant_id, role, app_version)
		VALUES (?, ?, 'admin', ?)
		ON DUPLICATE KEY UPDATE
			app_version = VALUES(app_version)
	`, userID, a.ActiveRestaurantID, version); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando version")
		return
	}

	// The user's own session caches the old version; drop it so the next
	// request reloads from DB.
	if s.sessionCache != nil {
		s.sessionCache.invalidateByUserRestaurant(userID, a.ActiveRestaurantID)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"user": map[string]any{
			"id":         userID,
			"appVersion": version,
		},
	})
}
