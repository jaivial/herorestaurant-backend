package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// Dessert source for a menu section.
// Coordination id: dessert_section_source_v1
// (backoffice add-section modal -> DB group_menu_sections_v2.dessert_source
//
//	-> backoffice/public section loaders -> preactvillacarmen)
//
// A "postres" section can either mirror the restaurant's general desserts carta
// (DessertSourceGeneral: read-only everywhere but /app/comida/postres) or own its
// own fully editable dish list (DessertSourceCustom, the default).
const (
	DessertSourceGeneral = "general"
	DessertSourceCustom  = "custom"
)

// SectionKindPostres is the canonical dessert section kind produced by
// normalizeV2SectionKind.
const SectionKindPostres = "postres"

// normalizeV2SectionDessertSource constrains the stored dessert source, and
// forces every non-dessert section back to "custom" so only dessert sections can
// ever be synced to the general carta.
func normalizeV2SectionDessertSource(rawKind, rawSource string) string {
	if normalizeV2SectionKind(rawKind) != SectionKindPostres {
		return DessertSourceCustom
	}
	switch strings.ToLower(strings.TrimSpace(rawSource)) {
	case DessertSourceGeneral, "carta_general", "general_carta", "shared":
		return DessertSourceGeneral
	default:
		return DessertSourceCustom
	}
}

// isGeneralDessertSection reports whether the section's dishes are owned by the
// general desserts carta, which makes them read-only in every menu editor.
func isGeneralDessertSection(kind, source string) bool {
	return normalizeV2SectionDessertSource(kind, source) == DessertSourceGeneral
}

// resolveGeneralDessertsSectionID returns the id of the restaurant's general
// desserts carta section (the single "postres" section of the POSTRES carrier
// menu), creating the carrier menu and section when the restaurant has none.
// Coordination id: dessert_section_source_v1
func (s *Server) resolveGeneralDessertsSectionID(ctx context.Context, restaurantID int) (menuID int64, sectionID int64, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT m.id, sec.id
		  FROM menus m
		  JOIN group_menu_sections_v2 sec
		    ON sec.menu_id = m.id AND sec.restaurant_id = m.restaurant_id AND sec.section_kind = 'postres'
		 WHERE m.restaurant_id = ? AND UPPER(COALESCE(m.legacy_source_table, '')) = 'POSTRES'
		 ORDER BY m.id ASC, sec.position ASC, sec.id ASC
		 LIMIT 1
	`, restaurantID).Scan(&menuID, &sectionID)
	if err == nil {
		return menuID, sectionID, nil
	}
	if err != sql.ErrNoRows {
		return 0, 0, err
	}

	// The carrier menu may exist without its section (older rows), so reuse it
	// when present instead of creating a duplicate carrier.
	err = s.db.QueryRowContext(ctx, `
		SELECT id FROM menus
		 WHERE restaurant_id = ? AND UPPER(COALESCE(legacy_source_table, '')) = 'POSTRES'
		 ORDER BY id ASC LIMIT 1
	`, restaurantID).Scan(&menuID)
	if err == sql.ErrNoRows {
		res, insErr := s.db.ExecContext(ctx, `
			INSERT INTO menus (restaurant_id, menu_title, price, menu_type, active, is_draft, legacy_source_table)
			VALUES (?, 'Postres', 0, 'special', 1, 0, 'POSTRES')
		`, restaurantID)
		if insErr != nil {
			return 0, 0, insErr
		}
		menuID, _ = res.LastInsertId()
	} else if err != nil {
		return 0, 0, err
	}

	res, insErr := s.db.ExecContext(ctx, `
		INSERT INTO group_menu_sections_v2
			(restaurant_id, menu_id, title, display_title, section_kind, dessert_source, position, public_page_active, web_placement)
		VALUES (?, ?, 'Postres', 'Postres', 'postres', 'custom', 0, 0, 'inside_menus')
	`, restaurantID, menuID)
	if insErr != nil {
		return 0, 0, insErr
	}
	sectionID, _ = res.LastInsertId()
	return menuID, sectionID, nil
}

// generalDessertSectionIDs returns the ids of the restaurant's general desserts
// carta sections, so loaders can tell a mirror apart from its source without an
// extra round trip per section.
func (s *Server) generalDessertSectionIDs(ctx context.Context, restaurantID int) (map[int64]bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sec.id
		  FROM group_menu_sections_v2 sec
		  JOIN menus m ON m.id = sec.menu_id
		 WHERE sec.restaurant_id = ?
		   AND sec.section_kind = 'postres'
		   AND UPPER(COALESCE(m.legacy_source_table, '')) = 'POSTRES'
	`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// boV2GeneralDessertDishes loads the general desserts carta dishes shaped as
// backoffice section dishes, re-keyed onto the mirroring section so the editor
// renders them in place (read-only).
// Coordination id: dessert_section_source_v1
func (s *Server) boV2GeneralDessertDishes(r *http.Request, restaurantID int, mirrorSectionID int64) ([]boV2Dish, error) {
	carrierMenuID, carrierSectionID, err := s.resolveGeneralDessertsSectionID(r.Context(), restaurantID)
	if err != nil {
		return nil, err
	}
	dishes, err := s.loadBOMenuV2SectionDishes(r, restaurantID, carrierMenuID, carrierSectionID)
	if err != nil {
		return nil, err
	}
	for i := range dishes {
		dishes[i].SectionID = mirrorSectionID
		dishes[i].ReadOnly = true
	}
	return dishes, nil
}

// applyGeneralDessertMirrors replaces the dish list of every section that reads
// from the general desserts carta, and flags it read-only for the editor.
// Coordination id: dessert_section_source_v1
func (s *Server) applyGeneralDessertMirrors(r *http.Request, restaurantID int, sections []boV2Section) error {
	var cached []boV2Dish
	loaded := false
	for i := range sections {
		if !isGeneralDessertSection(sections[i].Kind, sections[i].DessertSource) {
			continue
		}
		if !loaded {
			var err error
			cached, err = s.boV2GeneralDessertDishes(r, restaurantID, sections[i].ID)
			if err != nil {
				return err
			}
			loaded = true
		}
		mirrored := make([]boV2Dish, len(cached))
		copy(mirrored, cached)
		for j := range mirrored {
			mirrored[j].SectionID = sections[i].ID
		}
		sections[i].Dishes = mirrored
	}
	return nil
}

// publicGeneralDessertDishes loads the general desserts carta dishes shaped for
// the public payload, so a mirroring section renders exactly what
// /app/comida/postres holds.
// Coordination id: dessert_section_source_v1
func (s *Server) publicGeneralDessertDishes(ctx context.Context, restaurantID int) ([]publicMenuDish, error) {
	_, carrierSectionID, err := s.resolveGeneralDessertsSectionID(ctx, restaurantID)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, d.title_snapshot,
		       COALESCE(NULLIF(TRIM(d.description_snapshot), ''), c.description, '') AS description_snapshot,
		       COALESCE(d.description_enabled, 1), d.allergens_json, d.foto_path,
		       d.supplement_enabled, d.supplement_price, d.price, d.position
		FROM group_menu_section_dishes_v2 d
		LEFT JOIN menu_dishes_catalog c ON c.id = d.catalog_dish_id
		WHERE d.restaurant_id = ? AND d.section_id = ? AND d.active = 1
		ORDER BY d.position ASC, d.id ASC
	`, restaurantID, carrierSectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]publicMenuDish, 0, 16)
	for rows.Next() {
		var (
			id                 int64
			title              string
			description        sql.NullString
			descriptionEnabled int
			allergensRaw       sql.NullString
			fotoPath           sql.NullString
			supplementInt      int
			supplementPrice    sql.NullFloat64
			priceRaw           sql.NullFloat64
			position           int
		)
		if err := rows.Scan(&id, &title, &description, &descriptionEnabled, &allergensRaw, &fotoPath,
			&supplementInt, &supplementPrice, &priceRaw, &position); err != nil {
			return nil, err
		}
		dish := publicMenuDish{
			ID:                 id,
			Title:              strings.TrimSpace(title),
			Description:        publicMenuDescription(description.String, descriptionEnabled != 0),
			DescriptionEnabled: descriptionEnabled != 0,
			FotoURL:            s.publicMenuMediaURL(ctx, restaurantID, fotoPath.String),
			Allergens:          anySliceToStringList(decodeJSONOrFallback(allergensRaw.String, []any{})),
			SupplementEnabled:  supplementInt != 0,
			Position:           position,
		}
		if supplementPrice.Valid {
			p := supplementPrice.Float64
			dish.SupplementPrice = &p
		}
		if priceRaw.Valid {
			p := priceRaw.Float64
			dish.Price = &p
		}
		out = append(out, dish)
	}
	return out, rows.Err()
}

// loadSectionDessertSource returns the section's kind and normalized dessert
// source, plus whether it exists inside the given menu/restaurant. One query
// replaces the ad-hoc existence checks the section mutation handlers used to run.
// Coordination id: dessert_section_source_v1
func (s *Server) loadSectionDessertSource(ctx context.Context, restaurantID int, menuID, sectionID int64) (kind string, source string, found bool, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT section_kind, COALESCE(dessert_source, 'custom')
		  FROM group_menu_sections_v2
		 WHERE id = ? AND menu_id = ? AND restaurant_id = ?
		 LIMIT 1
	`, sectionID, menuID, restaurantID).Scan(&kind, &source)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	kind = normalizeV2SectionKind(kind)
	return kind, normalizeV2SectionDessertSource(kind, source), true, nil
}

// guardGeneralDessertSection rejects a write when the section mirrors the
// general desserts carta. Returns true when the request was already answered.
// Coordination id: dessert_section_source_v1
func (s *Server) guardGeneralDessertSection(w http.ResponseWriter, r *http.Request, restaurantID int, menuID, sectionID int64) bool {
	kind, source, found, err := s.loadSectionDessertSource(r.Context(), restaurantID, menuID, sectionID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error verificando seccion")
		return true
	}
	if !found {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Section not found"})
		return true
	}
	if isGeneralDessertSection(kind, source) {
		logCheckpoint(r, "dessert_general_section_write_rejected", "section_id", strconv.FormatInt(sectionID, 10))
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Los postres de esta seccion se gestionan en la carta general de postres (/app/comida/postres)",
		})
		return true
	}
	return false
}

// handleBOGroupMenusV2PatchSectionDessertSource flips a dessert section between
// the general desserts carta ("general", read-only mirror) and its own editable
// dish list ("custom"). Switching to custom seeds the section with a snapshot of
// the general carta, so the operator starts from what the public already sees
// instead of an empty list.
// Coordination id: dessert_section_source_v1
// (backoffice section settings tab -> this endpoint -> DB -> public snapshot -> preact)
func (s *Server) handleBOGroupMenusV2PatchSectionDessertSource(w http.ResponseWriter, r *http.Request) {
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
	sectionID, err := parseChiPositiveInt64(r, "sectionId")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid section id"})
		return
	}

	var req struct {
		DessertSource string `json:"dessert_source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON body"})
		return
	}

	kind, current, found, err := s.loadSectionDessertSource(r.Context(), a.ActiveRestaurantID, menuID, sectionID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error verificando seccion")
		return
	}
	if !found {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Section not found"})
		return
	}
	if kind != SectionKindPostres {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Solo las secciones de postres tienen origen de carta"})
		return
	}

	// The general desserts carta itself must never mirror another carta.
	generalIDs, err := s.generalDessertSectionIDs(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error verificando carta general")
		return
	}
	if generalIDs[sectionID] {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Esta seccion ES la carta general de postres"})
		return
	}

	next := normalizeV2SectionDessertSource(kind, req.DessertSource)
	logCheckpoint(r, "dessert_section_source_patch_received",
		"section_id", strconv.FormatInt(sectionID, 10), "from", current, "to", next)

	if next == current {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "section_id": sectionID, "dessert_source": next})
		return
	}

	// general -> custom: materialize the carta snapshot so the operator can edit it.
	if next == DessertSourceCustom {
		if err := s.seedSectionFromGeneralDesserts(r.Context(), a.ActiveRestaurantID, menuID, sectionID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error copiando la carta general de postres")
			return
		}
	}

	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE group_menu_sections_v2 SET dessert_source = ?
		 WHERE id = ? AND menu_id = ? AND restaurant_id = ?
	`, next, sectionID, menuID, a.ActiveRestaurantID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando origen de postres")
		return
	}

	if err := s.syncBOMenuV2LegacySnapshot(r, a.ActiveRestaurantID, menuID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error sincronizando snapshot")
		return
	}

	logCheckpoint(r, "dessert_section_source_persisted",
		"section_id", strconv.FormatInt(sectionID, 10), "dessert_source", next)

	sections, err := s.loadBOMenuV2SectionsWithDishes(r, a.ActiveRestaurantID, menuID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error recargando secciones")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":        true,
		"section_id":     sectionID,
		"dessert_source": next,
		"sections":       sections,
	})
}

// seedSectionFromGeneralDesserts replaces the section's own dish rows with a
// copy of the general desserts carta, so switching general -> custom hands the
// operator an editable list that starts identical to the public one.
// Coordination id: dessert_section_source_v1
func (s *Server) seedSectionFromGeneralDesserts(ctx context.Context, restaurantID int, menuID, sectionID int64) error {
	_, carrierSectionID, err := s.resolveGeneralDessertsSectionID(ctx, restaurantID)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM group_menu_section_dishes_v2
		 WHERE restaurant_id = ? AND menu_id = ? AND section_id = ?
	`, restaurantID, menuID, sectionID); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO group_menu_section_dishes_v2
			(restaurant_id, menu_id, section_id, catalog_dish_id, title_snapshot, description_snapshot,
			 description_enabled, allergens_json, supplement_enabled, supplement_price, price, foto_path, active, position)
		SELECT ?, ?, ?, src.catalog_dish_id, src.title_snapshot,
		       COALESCE(NULLIF(TRIM(src.description_snapshot), ''), c.description, ''),
		       COALESCE(src.description_enabled, 1), src.allergens_json, src.supplement_enabled,
		       src.supplement_price, src.price, src.foto_path, src.active, src.position
		  FROM group_menu_section_dishes_v2 src
		  LEFT JOIN menu_dishes_catalog c ON c.id = src.catalog_dish_id
		 WHERE src.restaurant_id = ? AND src.section_id = ?
		 ORDER BY src.position ASC, src.id ASC
	`, restaurantID, menuID, sectionID, restaurantID, carrierSectionID); err != nil {
		return err
	}

	return tx.Commit()
}

// generalDessertMirrorSectionIDs returns the ids of the sections that mirror the
// general desserts carta, restricted to the given menus (empty = all menus).
// Coordination id: dessert_section_source_v1
func (s *Server) generalDessertMirrorSectionIDs(ctx context.Context, restaurantID int, menuIDs []int64) (map[int64]bool, error) {
	query := `
		SELECT id FROM group_menu_sections_v2
		 WHERE restaurant_id = ? AND section_kind = 'postres' AND COALESCE(dessert_source, 'custom') = 'general'
	`
	args := []any{restaurantID}
	if len(menuIDs) > 0 {
		query += " AND menu_id IN (" + placeholderList(len(menuIDs)) + ")"
		for _, id := range menuIDs {
			args = append(args, id)
		}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// applyPublicGeneralDessertMirrors swaps the dish list of every public section
// that mirrors the general desserts carta, so the public site always shows what
// /app/comida/postres holds. Loads the carta at most once per request and no-ops
// when no section mirrors it.
// Coordination id: dessert_section_source_v1
func (s *Server) applyPublicGeneralDessertMirrors(ctx context.Context, restaurantID int, menuIDs []int64, sections map[int64]*publicMenuSection) error {
	if len(sections) == 0 {
		return nil
	}
	mirrors, err := s.generalDessertMirrorSectionIDs(ctx, restaurantID, menuIDs)
	if err != nil {
		return err
	}
	if len(mirrors) == 0 {
		return nil
	}

	var carta []publicMenuDish
	loaded := false
	for sectionID, section := range sections {
		if section == nil || !mirrors[sectionID] {
			continue
		}
		if !loaded {
			carta, err = s.publicGeneralDessertDishes(ctx, restaurantID)
			if err != nil {
				return err
			}
			loaded = true
		}
		mirrored := make([]publicMenuDish, len(carta))
		copy(mirrored, carta)
		section.Dishes = mirrored
	}
	return nil
}
