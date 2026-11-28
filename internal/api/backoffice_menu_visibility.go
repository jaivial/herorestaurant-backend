package api

import (
	"database/sql"
	"log"
	"net/http"
	"strings"

	"preactvillacarmen/internal/httpx"
)

type boMenuVisibilityItem struct {
	MenuKey   string `json:"menuKey"`
	MenuName  string `json:"menuName"`
	IsActive  bool   `json:"isActive"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

var boDefaultMenuVisibility = []struct {
	Key  string
	Name string
}{
	{Key: "menudeldia", Name: "Menu Del Dia"},
	{Key: "menufindesemana", Name: "Menu Fin de Semana"},
}

func isValidBOMenuKey(k string) bool {
	for _, d := range boDefaultMenuVisibility {
		if d.Key == k {
			return true
		}
	}
	return false
}

func menuNameForKey(k string) string {
	for _, d := range boDefaultMenuVisibility {
		if d.Key == k {
			return d.Name
		}
	}
	return k
}

func (s *Server) ensureMenuVisibilityRows(ctxCtx *http.Request, restaurantID int) {
	// Best-effort: keep it idempotent.
	ctx := ctxCtx.Context()
	for _, d := range boDefaultMenuVisibility {
		// IGNORE can only skip the (restaurant_id, menu_key) duplicate here: no FK
		// on the table and the constant key/name fit varchar(50)/(100).
		// repair-ignore3: duplicate-only no-op; errors logged, not dropped.
		if _, err := s.db.ExecContext(ctx, nativeSQL(`
			INSERT INTO menu_visibility (restaurant_id, menu_key, menu_name, is_active)
			VALUES (?, ?, ?, 1)
			ON DUPLICATE KEY UPDATE menu_key = menu_key
		`, `
			INSERT INTO menu_visibility (restaurant_id, menu_key, menu_name, is_active)
			VALUES (?, ?, ?, 1)
			ON CONFLICT (restaurant_id, app.norm_ascii(menu_key)) DO NOTHING
		`), restaurantID, d.Key, d.Name); err != nil {
			log.Printf("[menu_visibility] seed restaurant=%d key=%s: %v", restaurantID, d.Key, err)
		}
	}
}

func (s *Server) handleBOMenuVisibilityGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	restaurantID := a.ActiveRestaurantID
	s.ensureMenuVisibilityRows(r, restaurantID)

	rows, err := s.db.QueryContext(r.Context(), portableSQL(`
		SELECT menu_key, menu_name, is_active, updated_at
		FROM menu_visibility
		WHERE restaurant_id = ?
		ORDER BY id ASC
	`), restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando menu_visibility")
		return
	}
	defer rows.Close()

	var out []boMenuVisibilityItem
	for rows.Next() {
		var (
			key     string
			name    string
			active  int
			updated sql.NullString
		)
		if err := rows.Scan(&key, &name, &active, &updated); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo menu_visibility")
			return
		}
		out = append(out, boMenuVisibilityItem{
			MenuKey:   key,
			MenuName:  name,
			IsActive:  active != 0,
			UpdatedAt: updated.String,
		})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"menus":   out,
	})
}

type boMenuVisibilitySetRequest struct {
	MenuKey  string `json:"menuKey"`
	IsActive bool   `json:"isActive"`
}

func (s *Server) handleBOMenuVisibilitySet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boMenuVisibilitySetRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	menuKey := strings.TrimSpace(req.MenuKey)
	if menuKey == "" || !isValidBOMenuKey(menuKey) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid menuKey",
		})
		return
	}

	restaurantID := a.ActiveRestaurantID
	activeInt := 0
	if req.IsActive {
		activeInt = 1
	}

	// MySQL reports CHANGED rows: an unchanged is_active gives 0 and falls into
	// the (then duplicate-failing, ignored) INSERT; PG keeps that contract.
	res, err := s.db.ExecContext(r.Context(), nativeSQL(`
		UPDATE menu_visibility
		SET is_active = ?
		WHERE restaurant_id = ? AND menu_key = ?
	`, `
		UPDATE menu_visibility
		SET is_active = $1
		WHERE restaurant_id = $2 AND app.norm_ascii(menu_key) = app.norm_ascii($3) AND is_active IS DISTINCT FROM CAST($1 AS smallint)
	`), activeInt, restaurantID, menuKey)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando menu_visibility")
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		// Row may not exist yet for this restaurant.
		_, _ = s.db.ExecContext(r.Context(), portableSQL(`
			INSERT INTO menu_visibility (restaurant_id, menu_key, menu_name, is_active)
			VALUES (?, ?, ?, ?)
		`), restaurantID, menuKey, menuNameForKey(menuKey), activeInt)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"menuKey":  menuKey,
		"isActive": req.IsActive,
	})
}
