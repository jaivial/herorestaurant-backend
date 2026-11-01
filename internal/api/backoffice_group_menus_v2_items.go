package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// Coordination id: wa_bot_menu_item_crud_v1
//
// Item-level writes for v2 menus. The editor saves whole lists (PUT sections,
// PUT section dishes), which forced any caller adding ONE dish to resend every
// existing dish and to delete whatever it forgot. These routes create, edit
// and delete one section or one dish without touching the rest, so the bot
// can work dish by dish (and chain them in tool_chain). They reuse the same
// rules as the bulk handlers: tenant scope, the general-desserts mirror guard,
// the legacy snapshot sync and the translations.

type boV2DishItemRequest struct {
	CatalogDishID      *int64   `json:"catalog_dish_id"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	DescriptionEnabled *bool    `json:"description_enabled"`
	Allergens          []string `json:"allergens"`
	SupplementEnabled  bool     `json:"supplement_enabled"`
	SupplementPrice    *float64 `json:"supplement_price"`
	Price              *float64 `json:"price"`
	Active             *bool    `json:"active"`
	Position           *int     `json:"position"`
	ApplyToCatalog     bool     `json:"apply_to_catalog"`
}

// handleBOGroupMenusV2CreateSectionDish appends ONE dish to a section.
// POST /admin/group-menus-v2/{id}/sections/{sectionId}/dishes
func (s *Server) handleBOGroupMenusV2CreateSectionDish(w http.ResponseWriter, r *http.Request) {
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
	sectionID, err := parseChiPositiveInt64(r, "sectionId")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid section id"})
		return
	}
	if s.guardGeneralDessertSection(w, r, a.ActiveRestaurantID, menuID, sectionID) {
		return
	}
	var req boV2DishItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON body"})
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "title es obligatorio"})
		return
	}
	description := strings.TrimSpace(req.Description)
	allergens := make([]string, 0, len(req.Allergens))
	for _, aName := range req.Allergens {
		if aName = strings.TrimSpace(aName); aName != "" {
			allergens = append(allergens, aName)
		}
	}
	descEnabled := req.DescriptionEnabled == nil || *req.DescriptionEnabled
	active := req.Active == nil || *req.Active

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando plato")
		return
	}
	defer tx.Rollback()

	// Lock the section row: concurrent single-dish creates (panel + bot) would
	// otherwise read the same MAX(position) and collide.
	var lockedID int64
	err = tx.QueryRowContext(r.Context(), `
		SELECT id FROM group_menu_sections_v2
		WHERE id = ? AND menu_id = ? AND restaurant_id = ?
		FOR UPDATE
	`, sectionID, menuID, a.ActiveRestaurantID).Scan(&lockedID)
	if err == sql.ErrNoRows {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Section not found"})
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error verificando seccion")
		return
	}
	var nextPos int
	if err := tx.QueryRowContext(r.Context(), `
		SELECT COALESCE(MAX(position), -1) + 1 FROM group_menu_section_dishes_v2
		WHERE section_id = ? AND menu_id = ? AND restaurant_id = ?
	`, sectionID, menuID, a.ActiveRestaurantID).Scan(&nextPos); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error ordenando platos")
		return
	}
	position := nextPos
	if req.Position != nil && *req.Position >= 0 && *req.Position < nextPos {
		position = *req.Position
		if _, err := tx.ExecContext(r.Context(), `
			UPDATE group_menu_section_dishes_v2 SET position = position + 1
			WHERE section_id = ? AND menu_id = ? AND restaurant_id = ? AND position >= ?
		`, sectionID, menuID, a.ActiveRestaurantID, position); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error ordenando platos")
			return
		}
	}

	res, err := tx.ExecContext(r.Context(), `
		INSERT INTO group_menu_section_dishes_v2
			(restaurant_id, menu_id, section_id, catalog_dish_id, title_snapshot, description_snapshot,
			 description_enabled, allergens_json, supplement_enabled, supplement_price, price, active, position)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, a.ActiveRestaurantID, menuID, sectionID, req.CatalogDishID, title, description,
		boolToTinyint(descEnabled), mustJSON(allergens, []any{}), boolToTinyint(req.SupplementEnabled),
		req.SupplementPrice, req.Price, boolToTinyint(active), position)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando plato")
		return
	}
	dishID, _ := res.LastInsertId()
	if req.ApplyToCatalog {
		if err := propagateSectionDishToCatalog(r.Context(), tx, a.ActiveRestaurantID, req.CatalogDishID, title, description, allergens, req.SupplementEnabled, req.SupplementPrice); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando la ficha general del plato")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando plato")
		return
	}
	if active {
		if err := s.syncBOMenuV2LegacySnapshot(r, a.ActiveRestaurantID, menuID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error sincronizando snapshot")
			return
		}
	}
	s.translateSectionDish(r.Context(), a.ActiveRestaurantID, dishID, title, description)
	logCheckpoint(r, "menu_section_dish_created", "menu_id", strconv.FormatInt(menuID, 10),
		"section_id", strconv.FormatInt(sectionID, 10), "dish_id", strconv.FormatInt(dishID, 10))
	dish, err := s.loadBOMenuV2DishByID(r, a.ActiveRestaurantID, menuID, sectionID, dishID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error recargando plato")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "dish_id": dishID, "dish": dish})
}

// handleBOGroupMenusV2DeleteSectionDish removes ONE dish from a section.
// DELETE /admin/group-menus-v2/{id}/sections/{sectionId}/dishes/{dishId}
func (s *Server) handleBOGroupMenusV2DeleteSectionDish(w http.ResponseWriter, r *http.Request) {
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
	sectionID, err := parseChiPositiveInt64(r, "sectionId")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid section id"})
		return
	}
	dishID, err := parseChiPositiveInt64(r, "dishId")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid dish id"})
		return
	}
	if s.guardGeneralDessertSection(w, r, a.ActiveRestaurantID, menuID, sectionID) {
		return
	}
	res, err := s.db.ExecContext(r.Context(), `
		DELETE FROM group_menu_section_dishes_v2
		WHERE id = ? AND section_id = ? AND menu_id = ? AND restaurant_id = ?
	`, dishID, sectionID, menuID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando plato")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Dish not found"})
		return
	}
	if err := s.syncBOMenuV2LegacySnapshot(r, a.ActiveRestaurantID, menuID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error sincronizando snapshot")
		return
	}
	logCheckpoint(r, "menu_section_dish_deleted", "menu_id", strconv.FormatInt(menuID, 10),
		"section_id", strconv.FormatInt(sectionID, 10), "dish_id", strconv.FormatInt(dishID, 10))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "dish_id": dishID})
}

type boV2SectionItemRequest struct {
	Title         *string  `json:"title"`
	DisplayTitle  *string  `json:"display_title"`
	Subtitle      *string  `json:"subtitle"`
	TabLabel      *string  `json:"tab_label"`
	Kind          *string  `json:"kind"`
	DessertSource *string  `json:"dessert_source"`
	Annotations   []string `json:"annotations"`
}

func clipSectionText(v string, max int) string {
	v = strings.TrimSpace(v)
	if len(v) > max {
		return v[:max]
	}
	return v
}

// handleBOGroupMenusV2CreateSection appends ONE section at the end of a menu.
// POST /admin/group-menus-v2/{id}/sections
func (s *Server) handleBOGroupMenusV2CreateSection(w http.ResponseWriter, r *http.Request) {
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
	var req boV2SectionItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON body"})
		return
	}
	if req.Title == nil || strings.TrimSpace(*req.Title) == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "title es obligatorio"})
		return
	}
	owns, err := s.ensureBOMenuV2Belongs(a.ActiveRestaurantID, menuID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error checking menu")
		return
	}
	if !owns {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
		return
	}
	title := clipSectionText(*req.Title, 255)
	kind := normalizeV2SectionKind(firstNonEmpty(derefString(req.Kind), "custom"))
	dessertSource := normalizeV2SectionDessertSource(kind, derefString(req.DessertSource))
	annotations := normalizeV2SectionAnnotations(req.Annotations)
	res, err := s.db.ExecContext(r.Context(), `
		INSERT INTO group_menu_sections_v2
			(restaurant_id, menu_id, title, display_title, subtitle, tab_label, section_kind, dessert_source, position, annotations_json)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, COALESCE(MAX(position), -1) + 1, ?
		FROM group_menu_sections_v2 WHERE restaurant_id = ? AND menu_id = ?
	`, a.ActiveRestaurantID, menuID, title, clipSectionText(derefString(req.DisplayTitle), 255),
		clipSectionText(derefString(req.Subtitle), 500), clipSectionText(derefString(req.TabLabel), 255),
		kind, dessertSource, mustJSON(annotations, []any{}), a.ActiveRestaurantID, menuID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando seccion")
		return
	}
	sectionID, _ := res.LastInsertId()
	if err := s.syncBOMenuV2LegacySnapshot(r, a.ActiveRestaurantID, menuID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error sincronizando snapshot")
		return
	}
	s.translateSection(r.Context(), a.ActiveRestaurantID, sectionID, title, annotations)
	logCheckpoint(r, "menu_section_created", "menu_id", strconv.FormatInt(menuID, 10), "section_id", strconv.FormatInt(sectionID, 10))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "section_id": sectionID, "kind": kind, "title": title})
}

// handleBOGroupMenusV2PatchSection edits ONE section: only the sent fields.
// PATCH /admin/group-menus-v2/{id}/sections/{sectionId}
func (s *Server) handleBOGroupMenusV2PatchSection(w http.ResponseWriter, r *http.Request) {
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
	sectionID, err := parseChiPositiveInt64(r, "sectionId")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid section id"})
		return
	}
	var req boV2SectionItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON body"})
		return
	}
	var curTitle, curKind, curSource, curAnnotations string
	err = s.db.QueryRowContext(r.Context(), `
		SELECT COALESCE(title,''), COALESCE(section_kind,'custom'), COALESCE(dessert_source,'custom'), COALESCE(annotations_json,'')
		FROM group_menu_sections_v2 WHERE id = ? AND menu_id = ? AND restaurant_id = ?
	`, sectionID, menuID, a.ActiveRestaurantID).Scan(&curTitle, &curKind, &curSource, &curAnnotations)
	if err == sql.ErrNoRows {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Section not found"})
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo seccion")
		return
	}
	sets, args := []string{}, []any{}
	title := curTitle
	if req.Title != nil {
		title = clipSectionText(*req.Title, 255)
		sets, args = append(sets, "title = ?"), append(args, title)
	}
	if req.DisplayTitle != nil {
		sets, args = append(sets, "display_title = ?"), append(args, clipSectionText(*req.DisplayTitle, 255))
	}
	if req.Subtitle != nil {
		sets, args = append(sets, "subtitle = ?"), append(args, clipSectionText(*req.Subtitle, 500))
	}
	if req.TabLabel != nil {
		sets, args = append(sets, "tab_label = ?"), append(args, clipSectionText(*req.TabLabel, 255))
	}
	kind := curKind
	if req.Kind != nil {
		kind = normalizeV2SectionKind(*req.Kind)
		sets, args = append(sets, "section_kind = ?"), append(args, kind)
	}
	if req.Kind != nil || req.DessertSource != nil {
		source := curSource
		if req.DessertSource != nil {
			source = *req.DessertSource
		}
		sets, args = append(sets, "dessert_source = ?"), append(args, normalizeV2SectionDessertSource(kind, source))
	}
	// A title-only edit must retranslate with the STORED annotations, or their
	// translations would be dropped.
	var annotations []string
	_ = json.Unmarshal([]byte(curAnnotations), &annotations)
	if req.Annotations != nil {
		annotations = normalizeV2SectionAnnotations(req.Annotations)
		sets, args = append(sets, "annotations_json = ?"), append(args, mustJSON(annotations, []any{}))
	}
	if len(sets) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Nada que actualizar"})
		return
	}
	args = append(args, sectionID, menuID, a.ActiveRestaurantID)
	if _, err := s.db.ExecContext(r.Context(), "UPDATE group_menu_sections_v2 SET "+strings.Join(sets, ", ")+" WHERE id = ? AND menu_id = ? AND restaurant_id = ?", args...); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando seccion")
		return
	}
	if err := s.syncBOMenuV2LegacySnapshot(r, a.ActiveRestaurantID, menuID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error sincronizando snapshot")
		return
	}
	if req.Title != nil || req.Annotations != nil {
		s.translateSection(r.Context(), a.ActiveRestaurantID, sectionID, title, annotations)
	}
	logCheckpoint(r, "menu_section_patched", "menu_id", strconv.FormatInt(menuID, 10), "section_id", strconv.FormatInt(sectionID, 10), "fields", strconv.Itoa(len(sets)))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "section_id": sectionID})
}
