package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

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
		FROM menusDeGrupos
		WHERE restaurant_id = ?
		ORDER BY active DESC, created_at DESC
	`
	args := []any{restaurantID}
	switch strings.ToLower(status) {
	case "active":
		query = `
			SELECT id, menu_title, price, included_coffee, active, created_at, modified_at
			FROM menusDeGrupos
			WHERE restaurant_id = ? AND active = 1
			ORDER BY created_at DESC
		`
	case "inactive":
		query = `
			SELECT id, menu_title, price, included_coffee, active, created_at, modified_at
			FROM menusDeGrupos
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
			"id":             id,
			"menu_title":     title,
			"price":          price,
			"included_coffee": includedCoffee != 0,
			"active":         active != 0,
			"created_at":     createdAt.String,
			"modified_at":    modifiedAt.String,
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
		FROM menusDeGrupos
		WHERE restaurant_id = ? AND id = ?
	`

	var (
		menuTitle string
		price     string
		inclCoffee int
		active    int
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
		"id":                      id,
		"menu_title":              menuTitle,
		"price":                   price,
		"included_coffee":         inclCoffee != 0,
		"active":                  active != 0,
		"menu_subtitle":           decodeJSONOrFallback(menuSubtitle.String, []any{}),
		"entrantes":               decodeJSONOrFallback(entrantes.String, []any{}),
		"principales":             decodeJSONOrFallback(principales.String, map[string]any{}),
		"postre":                  decodeJSONOrFallback(postre.String, []any{}),
		"beverage":                decodeJSONOrFallback(beverage.String, map[string]any{}),
		"comments":                decodeJSONOrFallback(comments.String, []any{}),
		"min_party_size":          minPartySize,
		"main_dishes_limit":       mainLimit != 0,
		"main_dishes_limit_number": mainLimitNum,
		"created_at":              createdAt.String,
		"modified_at":             modifiedAt.String,
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
		`INSERT INTO menusDeGrupos
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
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"message":   "Menu created successfully.",
		"menu_id":   menuID,
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
	if err := s.db.QueryRowContext(r.Context(), "SELECT id FROM menusDeGrupos WHERE restaurant_id = ? AND id = ? LIMIT 1", restaurantID, id).Scan(&tmp); err != nil {
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
		`UPDATE menusDeGrupos SET
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

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"message":   "Menu updated successfully.",
		"menu_id":   id,
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
	if err := s.db.QueryRowContext(r.Context(), "SELECT active FROM menusDeGrupos WHERE restaurant_id = ? AND id = ? LIMIT 1", restaurantID, id).Scan(&current); err != nil {
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

	if _, err := s.db.ExecContext(r.Context(), "UPDATE menusDeGrupos SET active = ? WHERE restaurant_id = ? AND id = ?", newStatus, restaurantID, id); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Server error: " + err.Error(),
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Menu status updated successfully.",
		"menu_id":  id,
		"active":   newStatus != 0,
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
	if err := s.db.QueryRowContext(r.Context(), "SELECT menu_title FROM menusDeGrupos WHERE restaurant_id = ? AND id = ? LIMIT 1", restaurantID, id).Scan(&title); err != nil {
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

	if _, err := s.db.ExecContext(r.Context(), "DELETE FROM menusDeGrupos WHERE restaurant_id = ? AND id = ?", restaurantID, id); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Server error: " + err.Error(),
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"message":   "Menu deleted successfully.",
		"menu_id":   id,
		"menu_title": title,
	})
}

func (s *Server) handleGetActiveGroupMenusForDisplay(w http.ResponseWriter, r *http.Request) {
	query := `
		SELECT id, menu_title, price, included_coffee, menu_subtitle, entrantes, principales, postre, beverage, comments,
		       min_party_size, main_dishes_limit, main_dishes_limit_number, created_at
		FROM menusDeGrupos
		WHERE restaurant_id = ? AND active = 1
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

	rows, err := s.db.QueryContext(r.Context(), query, restaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Server error: " + err.Error(),
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

	var menus []map[string]any
	for rows.Next() {
		var (
			id int
			title string
			price float64
			inclCoffee int
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
			&id,
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
				"message": "Server error: " + err.Error(),
			})
			return
		}

		menu := map[string]any{
			"id":               id,
			"menu_title":       title,
			"price":            price,
			"included_coffee":  inclCoffee != 0,
			"menu_subtitle":    decode(menuSubtitle, []any{}),
			"entrantes":        decode(entrantes, []any{}),
			"principales":      decode(principales, map[string]any{"titulo_principales": "Principal a elegir", "items": []any{}}),
			"postre":           decode(postre, []any{}),
			"beverage":         decode(beverage, map[string]any{"type": "no_incluida", "price_per_person": nil}),
			"comments":         decode(comments, []any{}),
			"min_party_size":   minPartySize,
			"main_dishes_limit": mainLimit != 0,
			"main_dishes_limit_number": mainLimitNum,
			"created_at":       createdAt.String,
		}
		menus = append(menus, menu)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"count":   len(menus),
		"menus":   menus,
	})
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
