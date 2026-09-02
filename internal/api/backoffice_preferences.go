package api

import (
	"context"
	"net/http"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// allowedBOPreferences is the whitelist of preference keys the backoffice may
// persist, mapping each key to the set of accepted (lower-cased) values. This
// keeps the generic key/value store from becoming an arbitrary write surface.
var allowedBOPreferences = map[string]map[string]struct{}{
	"reservasDisplayMode": {"tabla": {}, "grid": {}},
	// Collapsed/expanded state of the "Reparto por hora" details accordion.
	// Separate keys: /app/reservas/config (per-day) and /app/config (defaults).
	"hourSplitDetailsOpenDay":     {"0": {}, "1": {}},
	"hourSplitDetailsOpenDefault": {"0": {}, "1": {}},
	// Whether the /app/stock?tab=sheets grid renders each card's picture. The
	// sheets list response carries it so the switcher hydrates on first load.
	"stockSheetsShowImages": {"0": {}, "1": {}},
}

// normalizeBOPreference lower-cases the value and validates (key, value)
// against allowedBOPreferences. Returns the normalized value and ok=true when
// the pair is accepted.
func normalizeBOPreference(key, value string) (string, bool) {
	allowed, ok := allowedBOPreferences[strings.TrimSpace(key)]
	if !ok {
		return "", false
	}
	norm := strings.ToLower(strings.TrimSpace(value))
	if _, ok := allowed[norm]; !ok {
		return "", false
	}
	return norm, true
}

// getUserPreferences returns all stored preferences for (userID, restaurantID).
// Missing rows yield an empty (non-nil) map so JSON serializes to {}.
func (s *Server) getUserPreferences(ctx context.Context, userID, restaurantID int) (map[string]string, error) {
	prefs := map[string]string{}
	rows, err := s.db.QueryContext(ctx, `SELECT pref_key, pref_value FROM user_preferences WHERE user_id = ? AND restaurant_id = ?`, userID, restaurantID)
	if err != nil {
		return prefs, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return prefs, err
		}
		prefs[k] = v
	}
	return prefs, rows.Err()
}

// setUserPreference upserts a single preference for (userID, restaurantID).
// The caller is responsible for validating key/value via normalizeBOPreference.
func (s *Server) setUserPreference(ctx context.Context, userID, restaurantID int, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO user_preferences (user_id, restaurant_id, pref_key, pref_value)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE pref_value = VALUES(pref_value)
	`, userID, restaurantID, key, value)
	return err
}

type boPreferencesSetRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// handleBOPreferencesSet persists one UI preference for the authenticated user
// in their active restaurant. Only whitelisted keys/values are accepted.
func (s *Server) handleBOPreferencesSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var req boPreferencesSetRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	normValue, ok := normalizeBOPreference(req.Key, req.Value)
	if !ok {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Preferencia no válida"})
		return
	}
	restaurantID := a.ActiveRestaurantID
	if restaurantID == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Sin restaurante activo"})
		return
	}
	if err := s.setUserPreference(r.Context(), a.User.ID, restaurantID, strings.TrimSpace(req.Key), normValue); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando preferencia")
		return
	}
	prefs, err := s.getUserPreferences(r.Context(), a.User.ID, restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo preferencias")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "preferences": prefs})
}
