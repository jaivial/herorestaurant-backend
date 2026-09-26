package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

// allowedBOPreferences is the whitelist of preference keys the backoffice may
// persist, mapping each key to the set of accepted (lower-cased) values. This
// keeps the generic key/value store from becoming an arbitrary write surface.
var allowedBOPreferences = map[string]map[string]struct{}{
	"reservasDisplayMode": {"tabla": {}, "grid": {}},
	// Facturas tabla/grid card display toggle (/app/facturas).
	// Coordination id: facturas_display_preference_v1
	"facturasDisplayMode": {"tabla": {}, "grid": {}},
	// Collapsed/expanded state of the "Reparto por hora" details accordion.
	// Separate keys: /app/reservas/config (per-day) and /app/config (defaults).
	"hourSplitDetailsOpenDay":     {"0": {}, "1": {}},
	"hourSplitDetailsOpenDefault": {"0": {}, "1": {}},
	// Whether the /app/stock?tab=sheets grid renders each card's picture. The
	// sheets list response carries it so the switcher hydrates on first load.
	"stockSheetsShowImages": {"0": {}, "1": {}},
	// Editor/preview split of /app/comida/menus/crear?menuId=. Stored per user
	// and restaurant, written over the group-menus-v2 socket and hydrated from
	// the session REST on the next page load.
	// Coordination id: menu_editor_preview_open_v1
	"menuEditorPreviewOpen": {"0": {}, "1": {}},
}

// boMenuEditorPreviewPrefKey is the user_preferences key of the editor/preview
// split above. Coordination id: menu_editor_preview_open_v1
const boMenuEditorPreviewPrefKey = "menuEditorPreviewOpen"

// reservasColumnIDs is the canonical order of the bookings table columns. The
// preference stores the visible subset as a CSV in this order so the value is
// deterministic whatever order the client sent, and unknown ids are dropped.
// Coordination id: reservas_columns_realtime_v1
var reservasColumnIDs = []string{
	"added", "mesa", "time", "client", "status", "floor",
	"salon", "pax", "children", "highChairs", "strollers", "phone", "rice", "comment",
	// Coordination id: special_booking_v1 / mobility_issues_v1 /
	// special_booking_qr_v1 - must mirror ReservasColumnId in the backoffice or
	// toggles on these columns are silently dropped.
	"adelantoEstado", "adelantoTotal", "adelantoDesglose", "adelantoMetodos",
	"menusEspeciales", "movilidad", "movilidadPax", "pendiente", "qrPdf",
}

// facturasColumnIDs is the canonical order of the invoices table data columns
// (must match InvoiceColumnId in the backoffice). The preference stores the
// visible subset as a CSV in this order.
// Coordination id: facturas_columns_preference_v1
var facturasColumnIDs = []string{
	"invoice_number", "customer_name", "customer_email", "amount", "currency",
	"payment_progress", "invoice_date", "due_date", "payment_date",
	"payment_method", "status", "is_reservation", "deposit", "category",
}

const (
	boPrefReservasVisibleColumns = "reservasVisibleColumns"
	boPrefFacturasVisibleColumns = "facturasVisibleColumns"
)

// normalizeVisibleColumnsCSV validates a CSV of column ids against the given
// canonical set and re-serializes it in canonical order. An empty selection is
// rejected so the table can never end up with zero data columns.
func normalizeVisibleColumnsCSV(ids []string, value string) (string, bool) {
	selected := map[string]struct{}{}
	for _, part := range strings.Split(value, ",") {
		selected[strings.ToLower(strings.TrimSpace(part))] = struct{}{}
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := selected[strings.ToLower(id)]; ok {
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
	switch key {
	case boPrefReservasVisibleColumns:
		return normalizeVisibleColumnsCSV(reservasColumnIDs, value)
	case boPrefFacturasVisibleColumns:
		return normalizeVisibleColumnsCSV(facturasColumnIDs, value)
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

// setMenuUserPreference upserts a single preference for
// (userID, restaurantID, menuID). One row per user, restaurant and menu so the
// editor/preview split is remembered for each menu id, whatever its type.
// Coordination id: menu_editor_preview_open_v1
func (s *Server) setMenuUserPreference(ctx context.Context, userID, restaurantID int, menuID int64, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO user_menu_preferences (user_id, restaurant_id, menu_id, pref_key, pref_value)
		VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE pref_value = VALUES(pref_value)
	`, userID, restaurantID, menuID, key, value)
	return err
}

// getMenuUserPreference reads a single per-menu preference. Returns
// (value, ok=true) when a row exists for that menu, ("", false, nil) when the
// key is unset and the underlying error otherwise.
// Coordination id: menu_editor_preview_open_v1
func (s *Server) getMenuUserPreference(ctx context.Context, userID, restaurantID int, menuID int64, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx,
		`SELECT pref_value FROM user_menu_preferences WHERE user_id = ? AND restaurant_id = ? AND menu_id = ? AND pref_key = ?`,
		userID, restaurantID, menuID, key,
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

// handleBOMenuEditorPrefWSMessage persists the editor/preview split of
// /app/comida/menus/crear?menuId= over the group-menus-v2 socket (socket
// method), so the toggle never shares the menu autosave channel and never
// touches the menu row. The value lives in user_menu_preferences scoped by
// (user_id, restaurant_id, menu_id) -one toggle per menu, whatever its type-
// and comes back through the menu REST (GET /group-menus-v2/{id}) so the editor
// hydrates with the saved split for that menu on the next load.
// Coordination id: menu_editor_preview_open_v1
func (s *Server) handleBOMenuEditorPrefWSMessage(r *http.Request, restaurantID int, menuID int64, client *boGroupMenuV2AIClient, raw []byte) {
	ctx := r.Context()
	a, ok := boAuthFromContext(ctx)
	if !ok {
		return
	}
	var msg struct {
		Type          string `json:"type"`
		Open          *bool  `json:"open"`
		CorrelationID string `json:"correlation_id"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}
	fail := func(code, message string) {
		_ = client.writeJSON(map[string]any{
			"type":    "editor_preview_error",
			"menu_id": menuID,
			"code":    code,
			"message": message,
		})
	}
	if strings.ToLower(strings.TrimSpace(msg.Type)) != "editor_preview_set" || msg.Open == nil {
		fail("validation", "open es obligatorio")
		return
	}
	value := "0"
	if *msg.Open {
		value = "1"
	}
	norm, ok := normalizeBOPreference(boMenuEditorPreviewPrefKey, value)
	if !ok {
		fail("validation", "Preferencia no válida")
		return
	}
	if restaurantID == 0 {
		fail("validation", "Sin restaurante activo")
		return
	}
	if err := s.setMenuUserPreference(ctx, a.User.ID, restaurantID, menuID, boMenuEditorPreviewPrefKey, norm); err != nil {
		fail("server", "No se pudo guardar la vista del editor")
		return
	}
	s.logBOGroupMenuV2AITrace(
		"ws editor preview pref saved user=%d restaurant=%d menu=%d value=%s",
		a.User.ID, restaurantID, menuID, norm,
	)
	_ = client.writeJSON(map[string]any{
		"type":           "editor_preview_saved",
		"restaurant_id":  restaurantID,
		"menu_id":        menuID,
		"open":           norm == "1",
		"correlation_id": msg.CorrelationID,
		"at":             time.Now().UTC().Format(time.RFC3339),
	})
}
