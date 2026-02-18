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

func (s *Server) handleMenuVisibilityToggle(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "Unknown restaurant")
		return
	}

	menuKey, isActive, ok := readMenuVisibilityToggleInput(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "Missing required fields: menu_key and is_active")
		return
	}

	if !isValidMenuKey(menuKey) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid menu_key. Must be one of: menudeldia, menufindesemana")
		return
	}

	activeInt := 0
	if isActive {
		activeInt = 1
	}

	res, err := s.db.ExecContext(r.Context(), "UPDATE menu_visibility SET is_active = ? WHERE restaurant_id = ? AND menu_key = ?", activeInt, restaurantID, menuKey)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando menu_visibility")
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		// Menu exists?
		var tmp int
		if err := s.db.QueryRowContext(r.Context(), "SELECT id FROM menu_visibility WHERE restaurant_id = ? AND menu_key = ? LIMIT 1", restaurantID, menuKey).Scan(&tmp); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				httpx.WriteError(w, http.StatusNotFound, "Menu not found")
				return
			}
			httpx.WriteError(w, http.StatusInternalServerError, "Error consultando menu_visibility")
			return
		}
	}

	// Return updated row.
	var menuName string
	var activeDB int
	var updatedAt sql.NullString
	if err := s.db.QueryRowContext(r.Context(), "SELECT menu_name, is_active, updated_at FROM menu_visibility WHERE restaurant_id = ? AND menu_key = ? LIMIT 1", restaurantID, menuKey).Scan(&menuName, &activeDB, &updatedAt); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando menu_visibility")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"message":  "OK",
		"menu_key": menuKey,
		"is_active": func() bool {
			return activeDB != 0
		}(),
		"menu": map[string]any{
			"menu_key":   menuKey,
			"menu_name":  menuName,
			"is_active":  activeDB != 0,
			"updated_at": updatedAt.String,
		},
	})
}

func (s *Server) handleGetMenuVisibilityLegacy(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	menuKey := strings.TrimSpace(r.URL.Query().Get("menu_key"))

	if menuKey != "" {
		var menuName string
		var isActiveInt int
		var updatedAt sql.NullString
		err := s.db.QueryRowContext(r.Context(), "SELECT menu_name, is_active, updated_at FROM menu_visibility WHERE restaurant_id = ? AND menu_key = ? LIMIT 1", restaurantID, menuKey).
			Scan(&menuName, &isActiveInt, &updatedAt)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				httpx.WriteJSON(w, http.StatusOK, map[string]any{
					"success": false,
					"message": "Menu not found",
				})
				return
			}
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": "Database error",
			})
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"menu": map[string]any{
				"menu_key":   menuKey,
				"menu_name":  menuName,
				"is_active":  isActiveInt != 0,
				"updated_at": updatedAt.String,
			},
		})
		return
	}

	rows, err := s.db.QueryContext(r.Context(), "SELECT menu_key, menu_name, is_active, updated_at FROM menu_visibility WHERE restaurant_id = ? ORDER BY id", restaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Database error",
		})
		return
	}
	defer rows.Close()

	var menus []map[string]any
	for rows.Next() {
		var k, n string
		var a int
		var updatedAt sql.NullString
		if err := rows.Scan(&k, &n, &a, &updatedAt); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": "Database error",
			})
			return
		}
		menus = append(menus, map[string]any{
			"menu_key":   k,
			"menu_name":  n,
			"is_active":  a != 0,
			"updated_at": updatedAt.String,
		})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"menus":   menus,
	})
}

func (s *Server) handleToggleMenuVisibilityLegacy(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	menuKey, isActive, ok := readMenuVisibilityToggleInput(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Missing required fields: menu_key and is_active",
		})
		return
	}

	validKeys := []string{"menudeldia", "menufindesemana"}
	if !isValidMenuKey(menuKey) {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid menu_key. Must be one of: " + strings.Join(validKeys, ", "),
		})
		return
	}

	activeInt := 0
	if isActive {
		activeInt = 1
	}

	res, err := s.db.ExecContext(r.Context(), "UPDATE menu_visibility SET is_active = ? WHERE restaurant_id = ? AND menu_key = ?", activeInt, restaurantID, menuKey)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Database error",
		})
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		var tmp int
		if err := s.db.QueryRowContext(r.Context(), "SELECT id FROM menu_visibility WHERE restaurant_id = ? AND menu_key = ? LIMIT 1", restaurantID, menuKey).Scan(&tmp); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
					"success": false,
					"message": "Menu not found",
				})
				return
			}
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": "Database error",
			})
			return
		}
	}

	var menuName string
	var activeDB int
	var updatedAt sql.NullString
	if err := s.db.QueryRowContext(r.Context(), "SELECT menu_name, is_active, updated_at FROM menu_visibility WHERE restaurant_id = ? AND menu_key = ? LIMIT 1", restaurantID, menuKey).Scan(&menuName, &activeDB, &updatedAt); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Database error",
		})
		return
	}

	statusText := "desactivado"
	if isActive {
		statusText = "activado"
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Menú " + statusText + " correctamente",
		"menu": map[string]any{
			"menu_key":   menuKey,
			"menu_name":  menuName,
			"is_active":  activeDB != 0,
			"updated_at": updatedAt.String,
		},
	})
}

func readMenuVisibilityToggleInput(r *http.Request) (menuKey string, isActive bool, ok bool) {
	ct := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if ct == "application/json" {
		var input struct {
			MenuKey  string `json:"menu_key"`
			IsActive any    `json:"is_active"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err == nil {
			menuKey = strings.TrimSpace(input.MenuKey)
			isActive, ok = parseLooseBool(input.IsActive)
			return menuKey, isActive, menuKey != "" && ok
		}
	}

	// Form fallback.
	_ = r.ParseForm()
	menuKey = strings.TrimSpace(r.FormValue("menu_key"))
	isActive, ok = parseLooseBool(r.FormValue("is_active"))
	return menuKey, isActive, menuKey != "" && ok
}

func parseLooseBool(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		s := strings.TrimSpace(strings.ToLower(x))
		if s == "true" || s == "1" || s == "yes" || s == "on" {
			return true, true
		}
		if s == "false" || s == "0" || s == "no" || s == "off" {
			return false, true
		}
		return false, false
	case float64:
		return x != 0, true
	case int:
		return x != 0, true
	case int64:
		return x != 0, true
	default:
		// Try common string-ish values.
		if s, ok := v.(interface{ String() string }); ok {
			return parseLooseBool(s.String())
		}
		return false, false
	}
}

func isValidMenuKey(menuKey string) bool {
	switch strings.ToLower(strings.TrimSpace(menuKey)) {
	case "menudeldia", "menufindesemana":
		return true
	default:
		return false
	}
}

// Accept boolean-ish query/form fields (used in multiple legacy endpoints).
func parseBoolParam(s string, defaultVal bool) bool {
	if s == "" {
		return defaultVal
	}
	if b, err := strconv.ParseBool(s); err == nil {
		return b
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "yes", "on":
		return true
	case "0", "no", "off":
		return false
	default:
		return defaultVal
	}
}
