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

func (s *Server) handleGetReservationDayContext(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Formato de fecha inválido",
		})
		return
	}

	defaults, err := s.loadReservationDefaults(r.Context(), restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando defaults")
		return
	}

	openingMode := defaults.OpeningMode
	morningHours := cloneStrings(defaults.MorningHours)
	nightHours := cloneStrings(defaults.NightHours)

	var hoursRaw sql.NullString
	var modeRaw sql.NullString
	err = s.db.QueryRowContext(r.Context(), `
		SELECT hoursarray, opening_mode
		FROM openinghours
		WHERE restaurant_id = ? AND dateselected = ?
		LIMIT 1
	`, restaurantID, date).Scan(&hoursRaw, &modeRaw)
	if err != nil && err != sql.ErrNoRows {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando openinghours")
		return
	}

	if list, ok := parseHoursJSON(hoursRaw); ok {
		morningHours, nightHours = splitHoursByShift(list)
		// Use stored opening_mode if available, otherwise derive from hours
		if modeRaw.Valid && modeRaw.String != "" {
			openingMode = normalizeOpeningMode(modeRaw.String)
		} else {
			openingMode = modeFromHours(morningHours, nightHours)
		}
	}

	// Optional party_size gates which floors/salons have enough aforo left.
	// Omitting it keeps the previous (ungated) behaviour for backward compat.
	partySize := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("party_size")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			partySize = n
		}
	}
	occupancy, err := s.loadOccupancyForDate(r.Context(), restaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando ocupacion")
		return
	}

	floors, err := s.loadDateFloors(r.Context(), restaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando plantas")
		return
	}

	floorAforo := func(f boConfigFloor) (capacity int, occ int, remaining int, capped bool) {
		occ = occupancy["floor"][f.ID]
		capacity = f.MaxAforo
		if capacity <= 0 {
			return 0, occ, 0, false
		}
		rem := capacity - occ
		if rem < 0 {
			rem = 0
		}
		return capacity, occ, rem, true
	}
	salonAforo := func(sl boConfigSalon) (capacity int, occ int, remaining int, capped bool) {
		occ = occupancy["salon"][sl.ID]
		if !sl.HasCapacityLimit {
			return 0, occ, 0, false
		}
		cap := sl.CapacityLimit
		rem := cap - occ
		if rem < 0 {
			rem = 0
		}
		return cap, occ, rem, true
	}

	salonMap := func(sl boConfigSalon) map[string]any {
		capacity, occ, rem, _ := salonAforo(sl)
		return map[string]any{
			"id": sl.ID,
			"name": sl.Name,
			"capacityLimit": capacity,
			"occupancy":     occ,
			"remaining":     rem,
		}
	}

	// Full floor list (config, active + inactive) with aforo fields attached.
	floorsOut := make([]map[string]any, 0, len(floors))
	activeFloors := make([]map[string]any, 0, len(floors))
	for _, floor := range floors {
		capacity, occ, rem, capped := floorAforo(floor)
		if floor.Active {
			// Gate active floors to those with enough aforo for the party.
			if partySize > 0 && capped && rem < partySize {
				continue
			}
			activeFloors = append(activeFloors, map[string]any{
				"id": floor.ID, "floorNumber": floor.FloorNumber, "name": floor.Name,
				"isGround": floor.IsGround, "active": true,
				"maxAforo": capacity, "occupancy": occ, "remaining": rem,
			})
		}
		floorsOut = append(floorsOut, map[string]any{
			"id": floor.ID, "floorNumber": floor.FloorNumber, "name": floor.Name,
			"isGround": floor.IsGround, "active": floor.Active,
			"maxAforo": capacity, "occupancy": occ, "remaining": rem,
		})
	}

	flags, err := s.resolveLocationBooking(r.Context(), restaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando configuración de ubicación")
		return
	}
	salons, err := s.loadSalons(r.Context(), restaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando salones")
		return
	}
	floorsWithSalons := make([]map[string]any, 0, len(activeFloors))
	for _, floor := range floors {
		if !floor.Active {
			continue
		}
		capacity, occ, rem, capped := floorAforo(floor)
		if partySize > 0 && capped && rem < partySize {
			continue
		}
		salonList := make([]map[string]any, 0, 4)
		for _, salon := range salons {
			if salon.FloorID != floor.ID || !salon.IsActive {
				continue
			}
			if _, _, srem, scapped := salonAforo(salon); partySize > 0 && scapped && srem < partySize {
				continue
			}
			salonList = append(salonList, salonMap(salon))
		}
		floorsWithSalons = append(floorsWithSalons, map[string]any{
			"id": floor.ID, "floorNumber": floor.FloorNumber, "name": floor.Name,
			"isGround": floor.IsGround,
			"maxAforo": capacity, "occupancy": occ, "remaining": rem,
			"salons": salonList,
		})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"date":         date,
		"openingMode":  openingMode,
		"morningHours": morningHours,
		"nightHours":   nightHours,
		"floors":       floorsOut,
		"activeFloors": activeFloors,
		"locationBooking": map[string]any{
			"allowFloorReservation": flags.Floor.Value,
			"allowSalonReservation": flags.Salon.Value,
			"floors":                floorsWithSalons,
		},
	})
}

// handlePublicMandatoryMenus returns mandatory menu configuration for a date.
// If no config exists or status=false, returns { date, status: false }.
// If status=true, returns full menu details.
func (s *Server) handlePublicMandatoryMenus(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Formato de fecha inválido",
		})
		return
	}

	var id int
	var statusInt, mandatoryInt int
	var menuIDRaw, menuChooseMainRaw sql.NullString

	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, status, mandatory, menu_id, menu_choose_main
		FROM mandatory_menus
		WHERE restaurant_id = ? AND date = ?
		LIMIT 1
	`, restaurantID, date).Scan(&id, &statusInt, &mandatoryInt, &menuIDRaw, &menuChooseMainRaw)

	if err == sql.ErrNoRows {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"date":   date,
			"status": false,
		})
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando mandatory_menus")
		return
	}

	// If status is false, return only date and status
	if statusInt == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"date":   date,
			"status": false,
		})
		return
	}

	// Parse menu IDs
	var menuIDs []int
	if menuIDRaw.Valid && menuIDRaw.String != "" {
		_ = json.Unmarshal([]byte(menuIDRaw.String), &menuIDs)
	}
	if menuIDs == nil || len(menuIDs) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"date":      date,
			"status":    true,
			"mandatory": mandatoryInt != 0,
			"menus":     []any{},
		})
		return
	}

	// Parse menu_choose_main to know which menus have the checkbox
	var menuChooseMain []int
	if menuChooseMainRaw.Valid && menuChooseMainRaw.String != "" {
		_ = json.Unmarshal([]byte(menuChooseMainRaw.String), &menuChooseMain)
	}
	if menuChooseMain == nil {
		menuChooseMain = []int{}
	}
	menuChooseMainSet := make(map[int]bool)
	for _, m := range menuChooseMain {
		menuChooseMainSet[m] = true
	}

	// Fetch menu details from menus table
	menus, err := s.fetchMandatoryMenusDetails(r.Context(), restaurantID, menuIDs, menuChooseMainSet)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando menus")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"date":      date,
		"status":    true,
		"mandatory": mandatoryInt != 0,
		"menus":     menus,
	})
}

// fetchMandatoryMenusDetails fetches menu details for the given IDs.
func (s *Server) fetchMandatoryMenusDetails(ctx context.Context, restaurantID int, menuIDs []int, menuChooseMainSet map[int]bool) ([]map[string]any, error) {
	if len(menuIDs) == 0 {
		return []map[string]any{}, nil
	}

	// Build placeholders for IN clause
	placeholders := make([]string, len(menuIDs))
	args := make([]any, len(menuIDs)+1)
	args[0] = restaurantID
	for i, id := range menuIDs {
		placeholders[i] = "?"
		args[i+1] = id
	}

	query := `
		SELECT id, menu_title, COALESCE(NULLIF(TRIM(menu_type), ''), 'closed_conventional') AS menu_type,
		       menu_subtitle, entrantes, principales, min_party_size, main_dishes_limit, main_dishes_limit_number, price
		FROM menus
		WHERE restaurant_id = ? AND id IN (` + strings.Join(placeholders, ",") + `) AND active = 1 AND is_draft = 0
	`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type menuOut struct {
		ID                 int     `json:"id"`
		MenuTitle          string  `json:"menu_title"`
		MenuType           string  `json:"menu_type"`
		MenuSubtitle       string  `json:"menu_subtitle"`
		Entrantes          string  `json:"entrantes"`
		Principales        string  `json:"principales"`
		MinPartySize       int     `json:"min_party_size"`
		MainDishesLimit    bool    `json:"main_dishes_limit"`
		MainDishesLimitNum int     `json:"main_dishes_limit_number"`
		Price              float64 `json:"price"`
	}

	var results []map[string]any
	for rows.Next() {
		var m menuOut
		var menuSubtitleRaw, entrantesRaw, principalesRaw sql.NullString
		var mainLimitInt int

		if err := rows.Scan(&m.ID, &m.MenuTitle, &m.MenuType, &menuSubtitleRaw, &entrantesRaw, &principalesRaw, &m.MinPartySize, &mainLimitInt, &m.MainDishesLimitNum, &m.Price); err != nil {
			continue
		}

		m.MenuSubtitle = menuSubtitleRaw.String
		m.Entrantes = entrantesRaw.String
		m.Principales = principalesRaw.String
		m.MainDishesLimit = mainLimitInt != 0

		// Parse entrantes
		var entrantes []string
		if m.Entrantes != "" {
			_ = json.Unmarshal([]byte(m.Entrantes), &entrantes)
		}
		if entrantes == nil {
			entrantes = []string{}
		}

		// Parse principales
		var principales map[string]any
		if m.Principales != "" {
			_ = json.Unmarshal([]byte(m.Principales), &principales)
		}
		if principales == nil {
			principales = map[string]any{}
		}

		results = append(results, map[string]any{
			"menuId":                m.ID,
			"menuTitle":             m.MenuTitle,
			"menuSubtitle":          m.MenuSubtitle,
			"menuType":              m.MenuType,
			"entrantes":             entrantes,
			"principales":           principales,
			"minPartySize":          m.MinPartySize,
			"mainDishesLimit":       m.MainDishesLimit,
			"mainDishesLimitNumber": m.MainDishesLimitNum,
			"price":                 m.Price,
			"menuChooseMain":        menuChooseMainSet[m.ID],
		})
	}

	ids := make([]int64, 0, len(results))
	for _, menu := range results {
		if id, ok := groupMenuAnyToInt64(menu["menuId"]); ok {
			ids = append(ids, id)
		}
	}
	if all, err := s.loadTranslations(ctx, restaurantID, entityMenus, ids, translationLang); err == nil {
		for _, menu := range results {
			id, ok := groupMenuAnyToInt64(menu["menuId"])
			if !ok {
				continue
			}
			tr := all[id]
			menu["menuTitleEnglish"] = translationOr(tr, "menu_title")
			menu["entrantesEnglish"] = buildEnglishArray(tr, "entrantes", len(anySliceToStringList(menu["entrantes"])))
			if principales, ok := menu["principales"].(map[string]any); ok {
				menu["principalesEnglish"] = map[string]any{
					"titulo_principales": translationOr(tr, "principales_title"),
					"items":              buildEnglishArray(tr, "principales", len(anySliceToStringList(principales["items"]))),
				}
			}
		}
	}

	return results, nil
}
