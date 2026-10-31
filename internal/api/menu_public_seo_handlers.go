package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-sql-driver/mysql"

	"preactvillacarmen/internal/httpx"
)

// Coordination id: menu_public_seo_v1
func (s *Server) handlePublicMenuBySlug(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusInternalServerError, "Restaurant not found")
		return
	}
	slug, err := normalizeMenuSEOSlug(chi.URLParam(r, "slug"))
	if err != nil || slug == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid menu slug"})
		return
	}
	var menuID int64
	err = s.db.QueryRowContext(r.Context(), `SELECT id FROM menus WHERE `+activePublicMenuWhere()+` AND seo_slug = ? LIMIT 1`, restaurantID, slug).Scan(&menuID)
	if err != nil {
		status := http.StatusInternalServerError
		message := "Error fetching menu"
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
			message = "Menu not found"
		}
		httpx.WriteJSON(w, status, map[string]any{"success": false, "message": message})
		return
	}
	logCheckpoint(r, "public_menu_by_slug_resolved", "slug", slug, "menu_id", strconv.FormatInt(menuID, 10))
	s.handlePublicMenuByID(w, r, restaurantID, strconv.FormatInt(menuID, 10))
}

func (s *Server) handleBOGroupMenusV2PatchSEO(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	menuID, err := parseChiPositiveInt64(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid menu id"})
		return
	}
	var input map[string]any
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON body"})
		return
	}
	slug, err := normalizeMenuSEOSlug(anyToString(input["seo_slug"]))
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return
	}
	fields := []struct {
		key   string
		value string
		max   int
		label string
	}{
		{"seo_title", "", 180, "El title SEO"},
		{"seo_title_english", "", 180, "El title SEO en ingles"},
		{"seo_description", "", 500, "La description SEO"},
		{"seo_description_english", "", 500, "La description SEO en ingles"},
		{"seo_h1", "", 180, "El H1 SEO"},
		{"seo_h1_english", "", 180, "El H1 SEO en ingles"},
		// Coordination id: menu_seo_content_v1
		{"seo_intro", "", 4000, "La introduccion SEO"},
		{"seo_intro_english", "", 4000, "La introduccion SEO en ingles"},
		{"seo_faq", "", 8000, "Las preguntas frecuentes"},
		{"seo_faq_english", "", 8000, "Las preguntas frecuentes en ingles"},
	}
	values := make([]any, 0, 14)
	values = append(values, nullIfEmptyMenuSEOSlug(slug))
	for i := range fields {
		fields[i].value, err = normalizeMenuSEOText(anyToString(input[fields[i].key]), fields[i].max, fields[i].label)
		if err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
			return
		}
		values = append(values, fields[i].value)
	}
	values = append(values, menuID, a.ActiveRestaurantID)
	result, err := s.db.ExecContext(r.Context(), `UPDATE menus SET seo_slug = ?, seo_title = ?, seo_title_english = ?, seo_description = ?, seo_description_english = ?, seo_h1 = ?, seo_h1_english = ?, seo_intro = ?, seo_intro_english = ?, seo_faq = ?, seo_faq_english = ? WHERE id = ? AND restaurant_id = ?`, values...)
	if err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "Ese slug ya esta en uso por otro menu"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando SEO del menu")
		return
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		var exists int
		if queryErr := s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM menus WHERE id = ? AND restaurant_id = ?`, menuID, a.ActiveRestaurantID).Scan(&exists); queryErr != nil || exists == 0 {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Menu not found"})
			return
		}
	}
	logCheckpoint(r, "menu_public_seo_saved", "menu_id", strconv.FormatInt(menuID, 10), "slug", slug)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "seo_slug": slug})
}

func nullIfEmptyMenuSEOSlug(slug string) any {
	if strings.TrimSpace(slug) == "" {
		return nil
	}
	return slug
}
