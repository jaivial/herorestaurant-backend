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

// Coordination id: cafes_page_seo_v1
//
// SEO of the public /cafes page, edited in /app/comida/cafes > Configuración
// with the same fields as a menu's SEO panel. The /cafes path is fixed (it is
// a public route of its own, like /vinos), so there is no slug.

// cafesPageSEO uses the menu SEO JSON names so the backoffice and the public
// site reuse the same field contract (MenuPublicSEO without seo_slug).
type cafesPageSEO struct {
	SEOTitle              string `json:"seo_title"`
	SEOTitleEnglish       string `json:"seo_title_english"`
	SEODescription        string `json:"seo_description"`
	SEODescriptionEnglish string `json:"seo_description_english"`
	SEOH1                 string `json:"seo_h1"`
	SEOH1English          string `json:"seo_h1_english"`
	SEOIntro              string `json:"seo_intro"`
	SEOIntroEnglish       string `json:"seo_intro_english"`
	SEOFAQ                string `json:"seo_faq"`
	SEOFAQEnglish         string `json:"seo_faq_english"`
}

// cafesSEOFields binds each field to its column, length limit and label, in
// one place for the read, the write and the validation.
func cafesSEOFields(seo *cafesPageSEO) []struct {
	column string
	value  *string
	max    int
	label  string
} {
	return []struct {
		column string
		value  *string
		max    int
		label  string
	}{
		{"cafes_seo_title", &seo.SEOTitle, 180, "El title SEO"},
		{"cafes_seo_title_english", &seo.SEOTitleEnglish, 180, "El title SEO en ingles"},
		{"cafes_seo_description", &seo.SEODescription, 500, "La description SEO"},
		{"cafes_seo_description_english", &seo.SEODescriptionEnglish, 500, "La description SEO en ingles"},
		{"cafes_seo_h1", &seo.SEOH1, 180, "El H1 SEO"},
		{"cafes_seo_h1_english", &seo.SEOH1English, 180, "El H1 SEO en ingles"},
		{"cafes_seo_intro", &seo.SEOIntro, 4000, "La introduccion SEO"},
		{"cafes_seo_intro_english", &seo.SEOIntroEnglish, 4000, "La introduccion SEO en ingles"},
		{"cafes_seo_faq", &seo.SEOFAQ, 8000, "Las preguntas frecuentes"},
		{"cafes_seo_faq_english", &seo.SEOFAQEnglish, 8000, "Las preguntas frecuentes en ingles"},
	}
}

func (s *Server) loadCafesPageSEO(ctx context.Context, restaurantID int) cafesPageSEO {
	var seo cafesPageSEO
	fields := cafesSEOFields(&seo)
	cols := ""
	raw := make([]sql.NullString, len(fields))
	targets := make([]any, len(fields))
	for i, f := range fields {
		if i > 0 {
			cols += ", "
		}
		cols += f.column
		targets[i] = &raw[i]
	}
	err := s.db.QueryRowContext(ctx, "SELECT "+cols+" FROM restaurant_page_visibility WHERE restaurant_id = ?", restaurantID).Scan(targets...)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("cafes_page_seo_v1 load_failed restaurant_id=%d err=%v", restaurantID, err)
		}
		return seo
	}
	for i, f := range fields {
		*f.value = raw[i].String
	}
	return seo
}

// handleBOCafesSEOGet / handleBOCafesSEOPut: GET|PUT /admin/restaurant/pages/cafes-seo.
// PUT stores every field (an omitted field is saved empty), like the menu SEO.
func (s *Server) handleBOCafesSEOGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok || a.ActiveRestaurantID <= 0 {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "seo": s.loadCafesPageSEO(r.Context(), a.ActiveRestaurantID)})
}

func (s *Server) handleBOCafesSEOPut(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok || a.ActiveRestaurantID <= 0 {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var seo cafesPageSEO
	if err := json.NewDecoder(r.Body).Decode(&seo); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON body"})
		return
	}
	fields := cafesSEOFields(&seo)
	cols, sets := "", ""
	values := make([]any, 0, len(fields))
	for i, f := range fields {
		clean, err := normalizeMenuSEOText(*f.value, f.max, f.label)
		if err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
			return
		}
		*f.value = clean
		if i > 0 {
			cols += ", "
			sets += ", "
		}
		cols += f.column
		sets += f.column + " = VALUES(" + f.column + ")"
		values = append(values, clean)
	}
	placeholders := ""
	for range fields {
		placeholders += ", ?"
	}
	args := append([]any{a.ActiveRestaurantID}, values...)
	if _, err := s.db.ExecContext(r.Context(),
		"INSERT INTO restaurant_page_visibility (restaurant_id, "+cols+") VALUES (?"+placeholders+") ON DUPLICATE KEY UPDATE "+sets,
		args...); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando el SEO de cafés")
		return
	}
	logCheckpoint(r, "cafes_page_seo_saved", "title_len", itoaLen(seo.SEOTitle), "h1_len", itoaLen(seo.SEOH1))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "seo": seo})
}

// handlePublicCafesSEO: GET /api/public/cafes-seo, read by the public site
// (prerender + client) to build the /cafes head, H1, intro and FAQ.
func (s *Server) handlePublicCafesSEO(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusInternalServerError, "Restaurant not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "seo": s.loadCafesPageSEO(r.Context(), restaurantID)})
}

func itoaLen(v string) string { return strconv.Itoa(len([]rune(v))) }
