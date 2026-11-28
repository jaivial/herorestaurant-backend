package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"preactvillacarmen/internal/httpx"
)

// Coordination id: menu_reorder_settings_v1
//
// Per (restaurant, menu) switches that let the editor hide the reorder
// controls: section arrows + drag handle, and the dish drag handle inside a
// section. A missing row means both are enabled (default true).

type menuReorderSettings struct {
	SectionsReorderEnabled bool `json:"sections_reorder_enabled"`
	DishesReorderEnabled   bool `json:"dishes_reorder_enabled"`
}

func (s *Server) loadMenuReorderSettings(ctx context.Context, restaurantID int, menuID int64) menuReorderSettings {
	out := menuReorderSettings{SectionsReorderEnabled: true, DishesReorderEnabled: true}
	var sections, dishes int
	err := s.db.QueryRowContext(ctx, portableSQL(`
		SELECT sections_reorder_enabled, dishes_reorder_enabled
		FROM menu_reorder_settings
		WHERE restaurant_id = ? AND menu_id = ?
	`), restaurantID, menuID).Scan(&sections, &dishes)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("menu_reorder_settings_load_failed menu_id=%d err=%v", menuID, err)
		}
		return out
	}
	out.SectionsReorderEnabled = sections != 0
	out.DishesReorderEnabled = dishes != 0
	return out
}

// handleBOGroupMenusV2PutReorderSettings: PUT /admin/group-menus-v2/{id}/reorder-settings
// Body {sections_reorder_enabled?, dishes_reorder_enabled?}; omitted keys keep their value.
func (s *Server) handleBOGroupMenusV2PutReorderSettings(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	menuID, err := parseChiPositiveInt64(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid menu id"})
		return
	}
	var req struct {
		Sections *bool `json:"sections_reorder_enabled"`
		Dishes   *bool `json:"dishes_reorder_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON body"})
		return
	}
	var exists int
	if err := s.db.QueryRowContext(r.Context(), portableSQL(`SELECT COUNT(*) FROM menus WHERE id = ? AND restaurant_id = ?`), menuID, a.ActiveRestaurantID).Scan(&exists); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando el menu")
		return
	}
	if exists == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
		return
	}
	next := s.loadMenuReorderSettings(r.Context(), a.ActiveRestaurantID, menuID)
	if req.Sections != nil {
		next.SectionsReorderEnabled = *req.Sections
	}
	if req.Dishes != nil {
		next.DishesReorderEnabled = *req.Dishes
	}
	sec, dish := 0, 0
	if next.SectionsReorderEnabled {
		sec = 1
	}
	if next.DishesReorderEnabled {
		dish = 1
	}
	_, err = s.db.ExecContext(r.Context(), nativeSQL(`
		INSERT INTO menu_reorder_settings (restaurant_id, menu_id, sections_reorder_enabled, dishes_reorder_enabled)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE sections_reorder_enabled = VALUES(sections_reorder_enabled), dishes_reorder_enabled = VALUES(dishes_reorder_enabled)
	`, `
		INSERT INTO menu_reorder_settings (restaurant_id, menu_id, sections_reorder_enabled, dishes_reorder_enabled)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (restaurant_id, menu_id) DO UPDATE SET sections_reorder_enabled = EXCLUDED.sections_reorder_enabled, dishes_reorder_enabled = EXCLUDED.dishes_reorder_enabled
	`), a.ActiveRestaurantID, menuID, sec, dish)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando los ajustes de orden")
		return
	}
	logCheckpoint(r, "menu_reorder_settings_saved", "menu_id", strconv.FormatInt(menuID, 10), "sections", strconv.FormatBool(next.SectionsReorderEnabled), "dishes", strconv.FormatBool(next.DishesReorderEnabled))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "reorder_settings": next})
}
