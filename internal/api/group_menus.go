package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleGetAllGroupMenus(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	status := strings.TrimSpace(r.URL.Query().Get("status"))

	query := `
		SELECT id, menu_title, price, included_coffee, active, created_at, modified_at
		FROM menus
		WHERE restaurant_id = ?
		ORDER BY active DESC, created_at DESC
	`
	args := []any{restaurantID}
	switch strings.ToLower(status) {
	case "active":
		query = `
			SELECT id, menu_title, price, included_coffee, active, created_at, modified_at
			FROM menus
			WHERE restaurant_id = ? AND active = 1
			ORDER BY created_at DESC
		`
	case "inactive":
		query = `
			SELECT id, menu_title, price, included_coffee, active, created_at, modified_at
			FROM menus
			WHERE restaurant_id = ? AND active = 0
			ORDER BY created_at DESC
		`
	}

	rows, err := s.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Server error: " + err.Error(),
		})
		return
	}
	defer rows.Close()

	var menus []map[string]any
	for rows.Next() {
		var id int
		var title string
		var price string
		var includedCoffee int
		var active int
		var createdAt sql.NullString
		var modifiedAt sql.NullString
		if err := rows.Scan(&id, &title, &price, &includedCoffee, &active, &createdAt, &modifiedAt); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": "Server error: " + err.Error(),
			})
			return
		}

		menus = append(menus, map[string]any{
			"id":              id,
			"menu_title":      title,
			"price":           price,
			"included_coffee": includedCoffee != 0,
			"active":          active != 0,
			"created_at":      createdAt.String,
			"modified_at":     modifiedAt.String,
		})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"count":   len(menus),
		"menus":   menus,
	})
}

func (s *Server) handleGetGroupMenu(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	idStr := strings.TrimSpace(r.URL.Query().Get("id"))
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Menu ID is required.",
		})
		return
	}

	query := `
		SELECT id, menu_title, price, included_coffee, active,
		       menu_subtitle, entrantes, principales, postre, beverage, comments,
		       min_party_size, main_dishes_limit, main_dishes_limit_number,
		       created_at, modified_at
		FROM menus
		WHERE restaurant_id = ? AND id = ?
	`

	var (
		menuTitle    string
		price        string
		inclCoffee   int
		active       int
		menuSubtitle sql.NullString
		entrantes    sql.NullString
		principales  sql.NullString
		postre       sql.NullString
		beverage     sql.NullString
		comments     sql.NullString
		minPartySize int
		mainLimit    int
		mainLimitNum int
		createdAt    sql.NullString
		modifiedAt   sql.NullString
	)

	err = s.db.QueryRowContext(r.Context(), query, restaurantID, id).Scan(
		&id,
		&menuTitle,
		&price,
		&inclCoffee,
		&active,
		&menuSubtitle,
		&entrantes,
		&principales,
		&postre,
		&beverage,
		&comments,
		&minPartySize,
		&mainLimit,
		&mainLimitNum,
		&createdAt,
		&modifiedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Menu not found.",
			})
			return
		}
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Server error: " + err.Error(),
		})
		return
	}

	menu := map[string]any{
		"id":                       id,
		"menu_title":               menuTitle,
		"price":                    price,
		"included_coffee":          inclCoffee != 0,
		"active":                   active != 0,
		"menu_subtitle":            decodeJSONOrFallback(menuSubtitle.String, []any{}),
		"entrantes":                decodeJSONOrFallback(entrantes.String, []any{}),
		"principales":              decodeJSONOrFallback(principales.String, map[string]any{}),
		"postre":                   decodeJSONOrFallback(postre.String, []any{}),
		"beverage":                 decodeJSONOrFallback(beverage.String, map[string]any{}),
		"comments":                 decodeJSONOrFallback(comments.String, []any{}),
		"min_party_size":           minPartySize,
		"main_dishes_limit":        mainLimit != 0,
		"main_dishes_limit_number": mainLimitNum,
		"created_at":               createdAt.String,
		"modified_at":              modifiedAt.String,
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"menu":    menu,
	})
}

func (s *Server) handleAddGroupMenu(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	var input map[string]any
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON body.",
		})
		return
	}

	menuTitle := strings.TrimSpace(anyToString(input["menu_title"]))
	if menuTitle == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Menu title is required.",
		})
		return
	}

	price, err := anyToFloat64(input["price"])
	if err != nil || price <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Price is required.",
		})
		return
	}

	includedCoffee := boolToTinyint(parseLooseBoolOrDefault(input["included_coffee"], false))
	active := boolToTinyint(parseLooseBoolOrDefault(input["active"], true))

	minPartySize, _ := anyToInt(input["min_party_size"])
	if minPartySize <= 0 {
		minPartySize = 8
	}
	mainLimit := boolToTinyint(parseLooseBoolOrDefault(input["main_dishes_limit"], false))
	mainLimitNum, _ := anyToInt(input["main_dishes_limit_number"])
	if mainLimitNum <= 0 {
		mainLimitNum = 1
	}

	menuSubtitleJSON := mustJSON(input["menu_subtitle"], []any{})
	entrantesJSON := mustJSON(input["entrantes"], []any{})
	principalesJSON := mustJSON(input["principales"], map[string]any{})
	postreJSON := mustJSON(input["postre"], []any{})
	beverageJSON := mustJSON(input["beverage"], map[string]any{})
	commentsJSON := mustJSON(input["comments"], []any{})

	res, err := s.db.ExecContext(
		r.Context(),
		`INSERT INTO menus
		 (restaurant_id, menu_title, price, included_coffee, active, menu_subtitle, entrantes, principales, postre, beverage, comments, min_party_size, main_dishes_limit, main_dishes_limit_number)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		restaurantID,
		menuTitle,
		price,
		includedCoffee,
		active,
		menuSubtitleJSON,
		entrantesJSON,
		principalesJSON,
		postreJSON,
		beverageJSON,
		commentsJSON,
		minPartySize,
		mainLimit,
		mainLimitNum,
	)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Server error: " + err.Error(),
		})
		return
	}

	menuID, _ := res.LastInsertId()
	entrantesList := anySliceToStringList(decodeJSONOrFallback(entrantesJSON, []any{}))
	principalesTitle := decodePrincipalesTitleJSON(principalesJSON)
	principalesItemsList := decodePrincipalesItemsJSON(principalesJSON)
	postreList := anySliceToStringList(decodeJSONOrFallback(postreJSON, []any{}))
	s.translateMenuConventionalArrays(r.Context(), restaurantID, menuID, entrantesList, principalesTitle, principalesItemsList, postreList)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"message":    "Menu created successfully.",
		"menu_id":    menuID,
		"menu_title": menuTitle,
	})
}

func (s *Server) handleUpdateGroupMenu(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	var input map[string]any
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON body.",
		})
		return
	}

	id, err := anyToInt(input["id"])
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Menu ID is required.",
		})
		return
	}
	menuTitle := strings.TrimSpace(anyToString(input["menu_title"]))
	if menuTitle == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Menu title is required.",
		})
		return
	}
	price, err := anyToFloat64(input["price"])
	if err != nil || price <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Price is required.",
		})
		return
	}

	// Ensure menu exists.
	var tmp int
	if err := s.db.QueryRowContext(r.Context(), "SELECT id FROM menus WHERE restaurant_id = ? AND id = ? LIMIT 1", restaurantID, id).Scan(&tmp); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Menu not found.",
			})
			return
		}
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Server error: " + err.Error(),
		})
		return
	}

	includedCoffee := boolToTinyint(parseLooseBoolOrDefault(input["included_coffee"], false))
	active := boolToTinyint(parseLooseBoolOrDefault(input["active"], true))

	minPartySize, _ := anyToInt(input["min_party_size"])
	if minPartySize <= 0 {
		minPartySize = 8
	}
	mainLimit := boolToTinyint(parseLooseBoolOrDefault(input["main_dishes_limit"], false))
	mainLimitNum, _ := anyToInt(input["main_dishes_limit_number"])
	if mainLimitNum <= 0 {
		mainLimitNum = 1
	}

	menuSubtitleJSON := mustJSON(input["menu_subtitle"], []any{})
	entrantesJSON := mustJSON(input["entrantes"], []any{})
	principalesJSON := mustJSON(input["principales"], map[string]any{})
	postreJSON := mustJSON(input["postre"], []any{})
	beverageJSON := mustJSON(input["beverage"], map[string]any{})
	commentsJSON := mustJSON(input["comments"], []any{})

	_, err = s.db.ExecContext(
		r.Context(),
		`UPDATE menus SET
			menu_title = ?,
			price = ?,
			included_coffee = ?,
			active = ?,
			menu_subtitle = ?,
			entrantes = ?,
			principales = ?,
			postre = ?,
			beverage = ?,
			comments = ?,
			min_party_size = ?,
			main_dishes_limit = ?,
			main_dishes_limit_number = ?
		WHERE restaurant_id = ? AND id = ?`,
		menuTitle,
		price,
		includedCoffee,
		active,
		menuSubtitleJSON,
		entrantesJSON,
		principalesJSON,
		postreJSON,
		beverageJSON,
		commentsJSON,
		minPartySize,
		mainLimit,
		mainLimitNum,
		restaurantID,
		id,
	)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Server error: " + err.Error(),
		})
		return
	}

	entrantesList := anySliceToStringList(decodeJSONOrFallback(entrantesJSON, []any{}))
	principalesTitle := decodePrincipalesTitleJSON(principalesJSON)
	principalesItemsList := decodePrincipalesItemsJSON(principalesJSON)
	postreList := anySliceToStringList(decodeJSONOrFallback(postreJSON, []any{}))
	s.translateMenuConventionalArrays(r.Context(), restaurantID, int64(id), entrantesList, principalesTitle, principalesItemsList, postreList)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"message":    "Menu updated successfully.",
		"menu_id":    id,
		"menu_title": menuTitle,
	})
}

func (s *Server) handleToggleGroupMenuActive(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	var input map[string]any
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON body.",
		})
		return
	}

	id, err := anyToInt(input["id"])
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Menu ID is required.",
		})
		return
	}

	var current int
	if err := s.db.QueryRowContext(r.Context(), "SELECT active FROM menus WHERE restaurant_id = ? AND id = ? LIMIT 1", restaurantID, id).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Menu not found.",
			})
			return
		}
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Server error: " + err.Error(),
		})
		return
	}

	newStatus := 1
	if current != 0 {
		newStatus = 0
	}

	if _, err := s.db.ExecContext(r.Context(), "UPDATE menus SET active = ? WHERE restaurant_id = ? AND id = ?", newStatus, restaurantID, id); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Server error: " + err.Error(),
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Menu status updated successfully.",
		"menu_id": id,
		"active":  newStatus != 0,
	})
}

func (s *Server) handleDeleteGroupMenu(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	var input map[string]any
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON body.",
		})
		return
	}

	id, err := anyToInt(input["id"])
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Menu ID is required.",
		})
		return
	}

	var title string
	if err := s.db.QueryRowContext(r.Context(), "SELECT menu_title FROM menus WHERE restaurant_id = ? AND id = ? LIMIT 1", restaurantID, id).Scan(&title); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Menu not found.",
			})
			return
		}
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Server error: " + err.Error(),
		})
		return
	}

	if _, err := s.db.ExecContext(r.Context(), "DELETE FROM menus WHERE restaurant_id = ? AND id = ?", restaurantID, id); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Server error: " + err.Error(),
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"message":    "Menu deleted successfully.",
		"menu_id":    id,
		"menu_title": title,
	})
}

func (s *Server) handleGetActiveGroupMenusForDisplay(w http.ResponseWriter, r *http.Request) {
	s.handleActiveGroupMenusForDisplay(w, r)
}

func (s *Server) handleGetActiveGroupMenusForDisplayRich(w http.ResponseWriter, r *http.Request) {
	s.handleActiveGroupMenusForDisplay(w, r)
}

// handleActiveGroupMenusForDisplay returns the slim active group-menu list:
// per menu only id, menu_title and menu_title_english. Full per-menu content
// (sections, dishes, description toggles) is served by getGroupMenuForDisplay.
func (s *Server) handleActiveGroupMenusForDisplay(w http.ResponseWriter, r *http.Request) {
	echoCorrelationID(w, r)
	logCheckpoint(r, "group_menus_list_request_received")

	query := `
		SELECT id, menu_title
		FROM menus
		WHERE restaurant_id = ? AND active = 1
		  AND COALESCE(NULLIF(TRIM(menu_type), ''), 'closed_conventional') IN ('closed_group', 'a_la_carte_group')
		ORDER BY created_at ASC
	`

	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	logCheckpoint(r, "group_menus_list_db_query_started", "restaurant_id", fmt.Sprintf("%d", restaurantID))
	queryCtx, cancelQuery := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancelQuery()
	rows, err := s.db.QueryContext(queryCtx, query, restaurantID)
	if err != nil {
		logCheckpoint(r, "group_menus_list_db_query_failed", "error", err.Error())
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Server error",
		})
		return
	}
	defer rows.Close()
	logCheckpoint(r, "group_menus_list_db_query_completed")

	menus := []map[string]any{}
	for rows.Next() {
		var (
			id    int
			title string
		)
		if err := rows.Scan(&id, &title); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": "Server error",
			})
			return
		}
		menus = append(menus, map[string]any{
			"id":         id,
			"menu_title": title,
		})
	}
	if err := rows.Err(); err != nil {
		logCheckpoint(r, "group_menus_list_rows_iteration_failed", "error", err.Error())
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Server error",
		})
		return
	}

	enrichCtx, enrichCancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer enrichCancel()
	s.enrichGroupMenuDisplayMenus(enrichCtx, restaurantID, menus)

	logCheckpoint(r, "group_menus_list_response_sent", "count", strconv.Itoa(len(menus)))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"count":   len(menus),
		"menus":   menus,
	})
}

// handleGetGroupMenuForDisplay returns the full display payload for ONE active
// group menu (?id=...): legacy blobs plus v2 dish details. Dishes with
// description_enabled = 0 are returned without a "descripcion" field.
func (s *Server) handleGetGroupMenuForDisplay(w http.ResponseWriter, r *http.Request) {
	echoCorrelationID(w, r)
	logCheckpoint(r, "public_group_menu_request_received")

	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	id, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("id")))
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid menu id",
		})
		return
	}

	query := `
		SELECT id, menu_title, price, included_coffee, menu_subtitle, entrantes, principales, postre, beverage, comments,
		       min_party_size, main_dishes_limit, main_dishes_limit_number, created_at
		FROM menus
		WHERE id = ? AND restaurant_id = ? AND active = 1
		  AND COALESCE(NULLIF(TRIM(menu_type), ''), 'closed_conventional') IN ('closed_group', 'a_la_carte_group')
		LIMIT 1
	`

	logCheckpoint(r, "public_group_menu_db_query_started", "menu_id", strconv.Itoa(id), "restaurant_id", fmt.Sprintf("%d", restaurantID))
	queryCtx, cancelQuery := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancelQuery()
	rows, err := s.db.QueryContext(queryCtx, query, id, restaurantID)
	if err != nil {
		logCheckpoint(r, "public_group_menu_db_query_failed", "error", err.Error())
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Server error",
		})
		return
	}
	defer rows.Close()

	decode := func(raw sql.NullString, fallback any) any {
		if !raw.Valid || strings.TrimSpace(raw.String) == "" {
			return fallback
		}
		var out any
		if err := json.Unmarshal([]byte(raw.String), &out); err != nil {
			return fallback
		}
		return out
	}

	menuFound := false
	var menu map[string]any
	for rows.Next() {
		var (
			menuID       int
			title        string
			price        float64
			inclCoffee   int
			menuSubtitle sql.NullString
			entrantes    sql.NullString
			principales  sql.NullString
			postre       sql.NullString
			beverage     sql.NullString
			comments     sql.NullString
			minPartySize int
			mainLimit    int
			mainLimitNum int
			createdAt    sql.NullString
		)
		if err := rows.Scan(
			&menuID,
			&title,
			&price,
			&inclCoffee,
			&menuSubtitle,
			&entrantes,
			&principales,
			&postre,
			&beverage,
			&comments,
			&minPartySize,
			&mainLimit,
			&mainLimitNum,
			&createdAt,
		); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": "Server error",
			})
			return
		}
		menuFound = true
		menu = map[string]any{
			"id":                       menuID,
			"menu_title":               title,
			"price":                    price,
			"included_coffee":          inclCoffee != 0,
			"menu_subtitle":            decode(menuSubtitle, []any{}),
			"entrantes":                decode(entrantes, []any{}),
			"principales":              decode(principales, map[string]any{"titulo_principales": "Principal a elegir", "items": []any{}}),
			"postre":                   decode(postre, []any{}),
			"beverage":                 decode(beverage, map[string]any{"type": "no_incluida", "price_per_person": nil}),
			"comments":                 decode(comments, []any{}),
			"min_party_size":           minPartySize,
			"main_dishes_limit":        mainLimit != 0,
			"main_dishes_limit_number": mainLimitNum,
			"created_at":               createdAt.String,
		}
	}
	if err := rows.Err(); err != nil {
		logCheckpoint(r, "public_group_menu_db_query_failed", "error", err.Error())
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Server error",
		})
		return
	}
	if !menuFound {
		logCheckpoint(r, "public_group_menu_not_found", "menu_id", strconv.Itoa(id))
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Menu not found",
		})
		return
	}
	logCheckpoint(r, "public_group_menu_db_query_completed", "menu_id", strconv.Itoa(id))

	menus := []map[string]any{menu}
	if err := s.addActiveGroupMenuDishDetails(r, menus); err != nil {
		logCheckpoint(r, "public_group_menu_dish_details_failed", "error", err.Error())
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error leyendo platos de menus",
		})
		return
	}
	enrichCtx, enrichCancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer enrichCancel()
	s.enrichGroupMenuDisplayMenus(enrichCtx, restaurantID, menus)

	logCheckpoint(r, "public_group_menu_response_sent", "menu_id", strconv.Itoa(id))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"menu":    menu,
	})
}

// enrichGroupMenuDisplayMenus adds menu-level English fields (title/subtitle/comments)
// to the legacy group-menu display payload.
func (s *Server) enrichGroupMenuDisplayMenus(ctx context.Context, restaurantID int, menus []map[string]any) {
	if len(menus) == 0 {
		return
	}
	ids := make([]int64, 0, len(menus))
	for _, menu := range menus {
		if id, ok := groupMenuAnyToInt64(menu["id"]); ok && id > 0 {
			ids = append(ids, id)
		}
	}
	all, err := s.loadTranslations(ctx, restaurantID, entityMenus, ids, translationLang)
	if err != nil {
		return
	}
	arrLen := func(v any) int {
		if arr, ok := v.([]any); ok {
			return len(arr)
		}
		if arr, ok := v.([]string); ok {
			return len(arr)
		}
		return 0
	}
	for i := range menus {
		id, ok := groupMenuAnyToInt64(menus[i]["id"])
		if !ok {
			continue
		}
		mt := all[id]
		if mt == nil {
			continue
		}
		if en := translationOr(mt, "menu_title"); en != "" {
			menus[i]["menu_title_english"] = en
		}
		if en := buildEnglishArray(mt, "menu_subtitle", arrLen(menus[i]["menu_subtitle"])); en != nil {
			menus[i]["menu_subtitle_english"] = en
		}
		if en := buildEnglishArray(mt, "comments", arrLen(menus[i]["comments"])); en != nil {
			menus[i]["comments_english"] = en
		}
	}
}

func (s *Server) addActiveGroupMenuDishDetails(r *http.Request, menus []map[string]any) error {
	if len(menus) == 0 {
		return nil
	}

	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		return errors.New("restaurant not found")
	}
	foodByKey, err := s.loadGroupMenuFoodItemsByName(r, restaurantID)
	if err != nil {
		return err
	}
	for i := range menus {
		menus[i]["entrantes"] = s.enrichGroupMenuLegacyDishArray(menus[i]["entrantes"], foodByKey)
		if p, ok := menus[i]["principales"].(map[string]any); ok {
			p["items"] = s.enrichGroupMenuLegacyDishArray(p["items"], foodByKey)
			menus[i]["principales"] = p
		}
		menus[i]["postre"] = s.enrichGroupMenuLegacyDishArray(menus[i]["postre"], foodByKey)
	}

	menuIDs := make([]int64, 0, len(menus))
	args := make([]any, 0, len(menus)+1)
	args = append(args, restaurantID)
	for _, menu := range menus {
		id, ok := groupMenuAnyToInt64(menu["id"])
		if !ok || id <= 0 {
			continue
		}
		menuIDs = append(menuIDs, id)
		args = append(args, id)
	}
	if len(menuIDs) == 0 {
		return nil
	}

	sectionArgs := append([]any(nil), args...)
	sectionRows, err := s.db.QueryContext(r.Context(), fmt.Sprintf(`
		SELECT id, menu_id, section_kind, title
		FROM group_menu_sections_v2
		WHERE restaurant_id = ? AND menu_id IN (%s)
		ORDER BY menu_id ASC, position ASC, id ASC
	`, placeholderList(len(menuIDs))), sectionArgs...)
	if err != nil {
		return err
	}
	sectionKinds := make(map[int64]string)
	for sectionRows.Next() {
		var id, menuID int64
		var kind, title string
		if err := sectionRows.Scan(&id, &menuID, &kind, &title); err != nil {
			sectionRows.Close()
			return err
		}
		kind = normalizeV2SectionKind(kind)
		if kind == "custom" {
			kind = normalizeV2SectionKind(title)
		}
		sectionKinds[id] = kind
	}
	if err := sectionRows.Err(); err != nil {
		sectionRows.Close()
		return err
	}
	sectionRows.Close()

	dishRows, err := s.db.QueryContext(r.Context(), fmt.Sprintf(`
		SELECT d.id, d.menu_id, d.section_id, d.title_snapshot, d.description_snapshot,
		       d.allergens_json, COALESCE(d.description_enabled, 1),
		       c.description, c.allergens_json, COALESCE(c.default_supplement_enabled, 0), c.default_supplement_price,
		       ci.descripcion, ci.alergenos_json, ci.suplemento,
		       d.supplement_enabled, d.supplement_price, d.active
		FROM group_menu_section_dishes_v2 d
		LEFT JOIN menu_dishes_catalog c
		  ON c.id = d.catalog_dish_id AND c.restaurant_id = d.restaurant_id
		LEFT JOIN comida_items ci
		  ON ci.restaurant_id = d.restaurant_id
		 AND ci.source_type = 'platos'
		 AND (
		   ci.nombre = d.title_snapshot
		   OR LOWER(d.title_snapshot) = LOWER(ci.nombre)
		   OR d.title_snapshot LIKE CONCAT(ci.nombre, ' (+%%')
		 )
		WHERE d.restaurant_id = ? AND d.menu_id IN (%s)
		ORDER BY d.menu_id ASC, d.section_id ASC, d.position ASC, d.id ASC
	`, placeholderList(len(menuIDs))), args...)
	if err != nil {
		return err
	}
	type dishGroups map[string][]map[string]any
	groups := make(map[int64]dishGroups)
	for dishRows.Next() {
		var id, menuID, sectionID int64
		var name, description string
		var allergensRaw sql.NullString
		var descriptionEnabled int
		var catalogDescription, catalogAllergens, foodDescription, foodAllergens sql.NullString
		var catalogSupplementEnabled, supplementEnabled, active int
		var catalogSupplementPrice, foodSupplement, supplementPrice sql.NullFloat64
		if err := dishRows.Scan(
			&id, &menuID, &sectionID, &name, &description, &allergensRaw, &descriptionEnabled,
			&catalogDescription, &catalogAllergens, &catalogSupplementEnabled, &catalogSupplementPrice,
			&foodDescription, &foodAllergens, &foodSupplement,
			&supplementEnabled, &supplementPrice, &active,
		); err != nil {
			dishRows.Close()
			return err
		}
		kind := sectionKinds[sectionID]
		if kind != "entrantes" && kind != "principales" && kind != "postres" {
			continue
		}
		if strings.TrimSpace(description) == "" {
			description = catalogDescription.String
		}
		if strings.TrimSpace(description) == "" {
			description = foodDescription.String
		}
		allergens := anySliceToStringList(decodeJSONOrFallback(allergensRaw.String, []any{}))
		if len(allergens) == 0 {
			allergens = anySliceToStringList(decodeJSONOrFallback(catalogAllergens.String, []any{}))
		}
		if len(allergens) == 0 {
			allergens = anySliceToStringList(decodeJSONOrFallback(foodAllergens.String, []any{}))
		}
		supplementActive := supplementEnabled != 0
		if !supplementActive {
			supplementActive = catalogSupplementEnabled != 0
		}
		if !supplementActive && foodSupplement.Valid && foodSupplement.Float64 > 0 {
			supplementActive = true
		}
		dish := map[string]any{
			"id":                  id,
			"nombre":              strings.TrimSpace(name),
			"alergenos":           allergens,
			"suplemento":          nil,
			"suplemento_activo":   supplementActive,
			"active":              active != 0,
			"descripcion_enabled": descriptionEnabled != 0,
		}
		if descriptionEnabled != 0 {
			dish["descripcion"] = strings.TrimSpace(description)
		}
		if supplementPrice.Valid && supplementPrice.Float64 > 0 {
			dish["suplemento"] = supplementPrice.Float64
		} else if catalogSupplementPrice.Valid && catalogSupplementPrice.Float64 > 0 {
			dish["suplemento"] = catalogSupplementPrice.Float64
		} else if foodSupplement.Valid && foodSupplement.Float64 > 0 {
			dish["suplemento"] = foodSupplement.Float64
		}
		if groups[menuID] == nil {
			groups[menuID] = make(dishGroups)
		}
		groups[menuID][kind] = append(groups[menuID][kind], dish)
	}
	if err := dishRows.Err(); err != nil {
		dishRows.Close()
		return err
	}
	dishRows.Close()

	for _, menu := range menus {
		menuID, ok := groupMenuAnyToInt64(menu["id"])
		if !ok {
			continue
		}
		group := groups[menuID]
		if len(group["entrantes"]) > 0 {
			menu["entrantes"] = group["entrantes"]
		} else if current, ok := menu["entrantes"].([]map[string]any); !ok || len(current) == 0 {
			menu["entrantes"] = richLegacyDishArray(menu["entrantes"])
		}
		if len(group["principales"]) > 0 {
			principales, _ := menu["principales"].(map[string]any)
			if principales == nil {
				principales = map[string]any{}
			}
			principales["items"] = group["principales"]
			menu["principales"] = principales
		} else if principales, ok := menu["principales"].(map[string]any); ok {
			if items, ok := principales["items"].([]map[string]any); !ok || len(items) == 0 {
				principales["items"] = richLegacyDishArray(principales["items"])
			}
		}
		if len(group["postres"]) > 0 {
			menu["postre"] = group["postres"]
		} else if current, ok := menu["postre"].([]map[string]any); !ok || len(current) == 0 {
			menu["postre"] = richLegacyDishArray(menu["postre"])
		}
	}
	return nil
}

func (s *Server) loadGroupMenuFoodItemsByName(r *http.Request, restaurantID int) (map[string]map[string]any, error) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, nombre, descripcion, alergenos_json, COALESCE(suplemento, 0)
		FROM comida_items
		WHERE restaurant_id = ? AND source_type = 'platos' AND active = 1
	`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]map[string]any)
	for rows.Next() {
		var (
			id           int64
			name         sql.NullString
			desc         sql.NullString
			allergensRaw sql.NullString
			supplement   float64
		)
		if err := rows.Scan(&id, &name, &desc, &allergensRaw, &supplement); err != nil {
			return nil, err
		}
		key := normalizeGroupMenuDishKey(name.String)
		if key == "" {
			continue
		}
		entry := map[string]any{
			"id":          id,
			"nombre":      name.String,
			"descripcion": desc.String,
			"alergenos":   anySliceToStringList(decodeJSONOrFallback(allergensRaw.String, []any{})),
			"suplemento":  supplement,
		}
		if existing, ok := out[key]; ok {
			if len(existing["alergenos"].([]string)) == 0 && len(entry["alergenos"].([]string)) > 0 {
				out[key] = entry
			}
			continue
		}
		out[key] = entry
	}
	return out, rows.Err()
}

func normalizeGroupMenuDishKey(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, ".")
	return strings.ToLower(s)
}

func (s *Server) enrichGroupMenuLegacyDishArray(raw any, foodByKey map[string]map[string]any) []map[string]any {
	values, ok := raw.([]any)
	if !ok {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(values))
	for _, value := range values {
		name := strings.TrimSpace(anyToString(value))
		if name == "" {
			continue
		}
		out = append(out, enrichGroupMenuLegacyDish(name, foodByKey))
	}
	return out
}

func enrichGroupMenuLegacyDish(name string, foodByKey map[string]map[string]any) map[string]any {
	dish := map[string]any{
		"nombre":              name,
		"descripcion":         "",
		"descripcion_enabled": true,
		"alergenos":           []string{},
		"suplemento":          nil,
		"active":              true,
	}
	key := normalizeGroupMenuDishKey(name)
	if key == "" {
		return dish
	}
	food, ok := foodByKey[key]
	if !ok {
		return dish
	}
	if description := strings.TrimSpace(anyToString(food["descripcion"])); description != "" {
		dish["descripcion"] = description
	}
	if allergens, ok := food["alergenos"].([]string); ok && len(allergens) > 0 {
		dish["alergenos"] = allergens
	}
	if supplement, ok := food["suplemento"].(float64); ok && supplement > 0 {
		dish["suplemento"] = supplement
	}
	return dish
}

func richLegacyDishArray(raw any) []map[string]any {
	values, ok := raw.([]any)
	if !ok {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(values))
	for _, value := range values {
		name := strings.TrimSpace(anyToString(value))
		if name == "" {
			continue
		}
		out = append(out, map[string]any{
			"nombre":              name,
			"descripcion":         "",
			"descripcion_enabled": true,
			"alergenos":           []string{},
			"suplemento":          nil,
			"active":              true,
		})
	}
	return out
}

func groupMenuAnyToInt64(v any) (int64, bool) {
	switch value := v.(type) {
	case int:
		return int64(value), true
	case int64:
		return value, true
	case float64:
		return int64(value), value == float64(int64(value))
	default:
		parsed, err := strconv.ParseInt(strings.TrimSpace(anyToString(v)), 10, 64)
		return parsed, err == nil
	}
}

func decodeJSONOrFallback(raw string, fallback any) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	var out any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return fallback
	}
	return out
}

func mustJSON(v any, fallback any) string {
	// This is intentionally forgiving: we always write valid JSON to satisfy DB CHECK constraints.
	if v == nil {
		b, _ := json.Marshal(fallback)
		return string(b)
	}

	// If it's already a string containing JSON, accept it if valid.
	if s, ok := v.(string); ok {
		st := strings.TrimSpace(s)
		if st == "" {
			b, _ := json.Marshal(fallback)
			return string(b)
		}
		var tmp any
		if err := json.Unmarshal([]byte(st), &tmp); err == nil {
			return st
		}
	}

	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(fallback)
	}
	return string(b)
}

func anyToFloat64(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case int:
		return float64(x), nil
	case int64:
		return float64(x), nil
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, errors.New("empty")
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, err
		}
		return f, nil
	default:
		return 0, errors.New("unsupported")
	}
}

func parseLooseBoolOrDefault(v any, def bool) bool {
	b, ok := parseLooseBool(v)
	if !ok {
		return def
	}
	return b
}

func boolToTinyint(b bool) int {
	if b {
		return 1
	}
	return 0
}
