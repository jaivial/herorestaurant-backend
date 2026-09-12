package api

import (
	"context"
	"database/sql"
	"errors"
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

// reservasColumnIDs is the canonical order of the bookings table columns. The
// preference stores the visible subset as a CSV in this order so the value is
// deterministic whatever order the client sent, and unknown ids are dropped.
// Coordination id: reservas_columns_realtime_v1
var reservasColumnIDs = []string{
	"added", "mesa", "time", "client", "status", "floor",
	"salon", "pax", "children", "phone", "rice", "comment",
}

const boPrefReservasVisibleColumns = "reservasVisibleColumns"

// normalizeReservasVisibleColumns validates a CSV of column ids against the
// canonical set and re-serializes it in canonical order. An empty selection is
// rejected so the table can never end up with zero data columns.
func normalizeReservasVisibleColumns(value string) (string, bool) {
	selected := map[string]struct{}{}
	for _, part := range strings.Split(value, ",") {
		selected[strings.ToLower(strings.TrimSpace(part))] = struct{}{}
	}
	out := make([]string, 0, len(reservasColumnIDs))
	for _, id := range reservasColumnIDs {
		if _, ok := selected[id]; ok {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return "", false
	}
	return strings.Join(out, ","), true
}

// parseReservasVisibleColumns expands a stored preference into the ordered id
// slice broadcast to clients; an unset value means every column is visible.
func parseReservasVisibleColumns(value string) []string {
	if strings.TrimSpace(value) == "" {
		return append([]string{}, reservasColumnIDs...)
	}
	out := make([]string, 0, len(reservasColumnIDs))
	for _, part := range strings.Split(value, ",") {
		if id := strings.TrimSpace(part); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// normalizeBOPreference lower-cases the value and validates (key, value)
// against allowedBOPreferences. Returns the normalized value and ok=true when
// the pair is accepted.
func normalizeBOPreference(key, value string) (string, bool) {
	key = strings.TrimSpace(key)
	// Column visibility is a validated list, not a fixed enum, so it does not
	// fit the value-set map below.
	if key == boPrefReservasVisibleColumns {
		return normalizeReservasVisibleColumns(value)
	}
	allowed, ok := allowedBOPreferences[key]
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

// getUserPreference reads a single preference. Returns (value, ok=true)
// when a row exists, ("", false, nil) when the key is unset, and the
// underlying error otherwise. Use this instead of getUserPreferences
// when the caller only needs one key — picking the row in SQL avoids
// sending every other stored preference over the wire and serializing
// it onto a JSON response that has no business knowing it.
func (s *Server) getUserPreference(ctx context.Context, userID, restaurantID int, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx,
		`SELECT pref_value FROM user_preferences WHERE user_id = ? AND restaurant_id = ? AND pref_key = ?`,
		userID, restaurantID, key,
	).Scan(&value)
	switch {
	case err == nil:
		return value, true, nil
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	default:
		return "", false, err
	}
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
	// Fan the new column selection out to every open tab of this user in
	// real time, without waiting for the next page load.
	if strings.TrimSpace(req.Key) == boPrefReservasVisibleColumns {
		s.broadcastReservasColumns(restaurantID, a.User.ID, parseReservasVisibleColumns(normValue))
	}
	prefs, err := s.getUserPreferences(r.Context(), a.User.ID, restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo preferencias")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "preferences": prefs})
}
