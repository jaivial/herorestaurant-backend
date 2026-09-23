package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// =============================================================================
// Special menu principales (main courses per image section).
//
// A special menu is a set of image sections; when the editor switches on
// "Anadir platos principales" each section gets its own list of dishes picked
// from the restaurant's platos (comida_items). The public payload exposes the
// list per section and the reservas wizard asks guests to choose from it.
//
// Rule shared by every reader: a section only "has principales" when the menu
// toggle is on AND the list is non-empty. On with no dishes == off.
// Coordination id: special_menu_principales_v1
// =============================================================================

// specialMenuPrincipal is one dish attached to a special-menu section.
type specialMenuPrincipal struct {
	ID          int64    `json:"id"`
	DishID      int64    `json:"dish_id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Allergens   []string `json:"allergens"`
	Position    int      `json:"position"`
}

// loadSpecialMenuPrincipales returns every section's dishes keyed by section id,
// ordered by position. Carta visibility (comida_items.active) is deliberately
// ignored: the regular create-dish flow saves platos hidden from the carta, and
// being offered inside a special menu is a separate, explicit choice.
func (s *Server) loadSpecialMenuPrincipales(ctx context.Context, restaurantID int, menuID int64) map[int64][]specialMenuPrincipal {
	out := map[int64][]specialMenuPrincipal{}
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, p.section_id, p.dish_id, ci.nombre, COALESCE(ci.descripcion, ''), COALESCE(ci.alergenos_json, '[]'), p.position
		FROM special_menu_section_principales p
		JOIN comida_items ci ON ci.id = p.dish_id AND ci.restaurant_id = p.restaurant_id
		WHERE p.restaurant_id = ? AND p.menu_id = ?
		ORDER BY p.section_id ASC, p.position ASC, p.id ASC
	`, restaurantID, menuID)
	if err != nil {
		log.Printf("[special_menu_principales_v1] load menu=%d err=%v", menuID, err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var (
			p            specialMenuPrincipal
			sectionID    int64
			allergensRaw string
		)
		if err := rows.Scan(&p.ID, &sectionID, &p.DishID, &p.Title, &p.Description, &allergensRaw, &p.Position); err != nil {
			continue
		}
		p.Allergens = anySliceToStringList(decodeJSONOrFallback(allergensRaw, []any{}))
		out[sectionID] = append(out[sectionID], p)
	}
	return out
}

// specialMenuPrincipalesEnabled reads the per-menu toggle.
func (s *Server) specialMenuPrincipalesEnabled(ctx context.Context, restaurantID int, menuID int64) bool {
	var enabled int
	if err := s.db.QueryRowContext(ctx,
		`SELECT special_principales_enabled FROM menus WHERE id = ? AND restaurant_id = ?`,
		menuID, restaurantID).Scan(&enabled); err != nil {
		return false
	}
	return enabled != 0
}

// loadPublicSpecialMenuPrincipales applies the shared rule for guests: with
// the toggle off every section is returned with no principales.
func (s *Server) loadPublicSpecialMenuPrincipales(ctx context.Context, restaurantID int, menuID int64) map[int64][]specialMenuPrincipal {
	if !s.specialMenuPrincipalesEnabled(ctx, restaurantID, menuID) {
		return map[int64][]specialMenuPrincipal{}
	}
	return s.loadSpecialMenuPrincipales(ctx, restaurantID, menuID)
}

// specialMenuPrincipalIDs maps every dish the menu offers guests (toggle on)
// to its title. Used to validate booking selections.
func (s *Server) specialMenuPrincipalIDs(ctx context.Context, restaurantID int, menuID int64) map[int64]string {
	out := map[int64]string{}
	for _, list := range s.loadPublicSpecialMenuPrincipales(ctx, restaurantID, menuID) {
		for _, p := range list {
			out[p.DishID] = p.Title
		}
	}
	return out
}

// requireBOSpecialSection resolves {id}/{sectionId} for the active restaurant.
func (s *Server) requireBOSpecialSection(w http.ResponseWriter, r *http.Request) (restaurantID int, menuID, sectionID int64, ok bool) {
	echoCorrelationID(w, r)
	a, authed := boAuthFromContext(r.Context())
	if !authed {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return 0, 0, 0, false
	}
	menuID, err := parseChiPositiveInt64(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid menu id"})
		return 0, 0, 0, false
	}
	sectionID, err = parseChiPositiveInt64(r, "sectionId")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid section id"})
		return 0, 0, 0, false
	}
	var found int
	if err := s.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM special_menu_sections WHERE id = ? AND menu_id = ? AND restaurant_id = ?`,
		sectionID, menuID, a.ActiveRestaurantID).Scan(&found); err != nil || found == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Seccion no encontrada"})
		return 0, 0, 0, false
	}
	return a.ActiveRestaurantID, menuID, sectionID, true
}

// handleBOSpecialMenuPrincipalesToggle persists "Anadir platos principales".
func (s *Server) handleBOSpecialMenuPrincipalesToggle(w http.ResponseWriter, r *http.Request) {
	echoCorrelationID(w, r)
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
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	res, err := s.db.ExecContext(r.Context(),
		`UPDATE menus SET special_principales_enabled = ? WHERE id = ? AND restaurant_id = ?`,
		boolToTinyint(req.Enabled), menuID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando principales")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if owns, _ := s.ensureBOMenuV2Belongs(a.ActiveRestaurantID, menuID); !owns {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
			return
		}
	}
	logCheckpoint(r, "special_menu_principales_toggled",
		"menu_id", strconv.FormatInt(menuID, 10), "enabled", strconv.FormatBool(req.Enabled))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "enabled": req.Enabled})
}

// handleBOSpecialSectionPrincipalAdd appends one platos dish to a section.
func (s *Server) handleBOSpecialSectionPrincipalAdd(w http.ResponseWriter, r *http.Request) {
	restaurantID, menuID, sectionID, ok := s.requireBOSpecialSection(w, r)
	if !ok {
		return
	}
	var req struct {
		DishID int64 `json:"dish_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DishID <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Plato invalido"})
		return
	}
	var found int
	if err := s.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM comida_items WHERE id = ? AND restaurant_id = ? AND source_type = 'platos'`,
		req.DishID, restaurantID).Scan(&found); err != nil || found == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Plato no encontrado"})
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `
		INSERT INTO special_menu_section_principales (restaurant_id, menu_id, section_id, dish_id, position)
		SELECT ?, ?, ?, ?, COALESCE(MAX(position), -1) + 1
		FROM special_menu_section_principales WHERE section_id = ?
		ON DUPLICATE KEY UPDATE dish_id = dish_id
	`, restaurantID, menuID, sectionID, req.DishID, sectionID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error anadiendo principal")
		return
	}
	logCheckpoint(r, "special_section_principal_added",
		"menu_id", strconv.FormatInt(menuID, 10), "section_id", strconv.FormatInt(sectionID, 10),
		"dish_id", strconv.FormatInt(req.DishID, 10))
	s.writeSectionPrincipales(w, r, restaurantID, menuID, sectionID)
}

// handleBOSpecialSectionPrincipalDelete removes one dish from a section.
func (s *Server) handleBOSpecialSectionPrincipalDelete(w http.ResponseWriter, r *http.Request) {
	restaurantID, menuID, sectionID, ok := s.requireBOSpecialSection(w, r)
	if !ok {
		return
	}
	dishID, err := parseChiPositiveInt64(r, "dishId")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Plato invalido"})
		return
	}
	if _, err := s.db.ExecContext(r.Context(),
		`DELETE FROM special_menu_section_principales WHERE restaurant_id = ? AND section_id = ? AND dish_id = ?`,
		restaurantID, sectionID, dishID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando principal")
		return
	}
	logCheckpoint(r, "special_section_principal_removed",
		"menu_id", strconv.FormatInt(menuID, 10), "section_id", strconv.FormatInt(sectionID, 10),
		"dish_id", strconv.FormatInt(dishID, 10))
	s.writeSectionPrincipales(w, r, restaurantID, menuID, sectionID)
}

func (s *Server) writeSectionPrincipales(w http.ResponseWriter, r *http.Request, restaurantID int, menuID, sectionID int64) {
	list := s.loadSpecialMenuPrincipales(r.Context(), restaurantID, menuID)[sectionID]
	if list == nil {
		list = []specialMenuPrincipal{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "section_id": sectionID, "principales": list})
}

// validateSpecialMenuPrincipalItems checks a special-menu booking selection:
// every dish must be offered by the menu. Returns the snapshot items.
func (s *Server) validateSpecialMenuPrincipalItems(ctx context.Context, restaurantID int, menuID int64, ids []int64) ([]specialBookingSnapshotItem, bool) {
	offered := s.specialMenuPrincipalIDs(ctx, restaurantID, menuID)
	items := make([]specialBookingSnapshotItem, 0, len(ids))
	for _, id := range ids {
		name, ok := offered[id]
		if !ok {
			return nil, false
		}
		items = append(items, specialBookingSnapshotItem{DishID: id, Name: strings.TrimSpace(name)})
	}
	return items, true
}
