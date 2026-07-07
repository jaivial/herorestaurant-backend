package api

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

// legalPageMaxFieldBytes caps the size of the stored content_json / content_html
// fields on upsert (per-field, not per-request).
const legalPageMaxFieldBytes = 4 << 20 // 4 MB

// isValidLegalSlug restricts the slug to the three known legal pages.
func isValidLegalSlug(slug string) bool {
	switch slug {
	case "aviso-legal", "booking-policies", "proteccion-datos":
		return true
	default:
		return false
	}
}

// fetchLegalPage loads a single legal page row for a restaurant/slug.
// Returns sql.ErrNoRows if the row does not exist.
func (s *Server) fetchLegalPage(r *http.Request, restaurantID int, slug string) (LegalPage, error) {
	page := LegalPage{Slug: slug}
	err := s.db.QueryRowContext(r.Context(), `
		SELECT title, content_json, content_html, updated_at
		FROM legal_pages
		WHERE restaurant_id = ? AND slug = ?
		LIMIT 1
	`, restaurantID, slug).Scan(&page.Title, &page.ContentJSON, &page.ContentHTML, &page.UpdatedAt)
	return page, err
}

// GET /api/public/legal-page?slug=…
// Public, tenant-aware. Reads from DB only (no reference to BookingPoliciesHTML).
func (s *Server) handlePublicLegalPageGet(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := s.resolveRestaurantID(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Unknown restaurant"})
		return
	}

	slug := strings.TrimSpace(r.URL.Query().Get("slug"))
	if !isValidLegalSlug(slug) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid slug")
		return
	}

	page, err := s.fetchLegalPage(r, restaurantID, slug)
	if err != nil {
		if err == sql.ErrNoRows {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Página no encontrada"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando la página legal")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"slug":        page.Slug,
		"title":       page.Title,
		"contentHtml": page.ContentHTML,
		"contentJson": page.ContentJSON,
		"updatedAt":   page.UpdatedAt,
	})
}

// GET /api/admin/legal-pages
// Admin. Lists the three legal pages for the active restaurant.
func (s *Server) handleAdminLegalPageList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT lp.slug, lp.title, lp.updated_at, COALESCE(u.name, u.email, '') AS updated_by_name
		FROM legal_pages lp
		LEFT JOIN bo_users u ON u.id = lp.updated_by_user_id
		WHERE lp.restaurant_id = ?
		ORDER BY lp.slug
	`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando las páginas legales")
		return
	}
	defer rows.Close()

	pages := make([]LegalPageSummary, 0, 3)
	for rows.Next() {
		var p LegalPageSummary
		if err := rows.Scan(&p.Slug, &p.Title, &p.UpdatedAt, &p.UpdatedByName); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo las páginas legales")
			return
		}
		pages = append(pages, p)
	}
	if err := rows.Err(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo las páginas legales")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, LegalPageListResponse{Success: true, Pages: pages})
}

// GET /api/admin/legal-pages/{slug}
// Admin. Full row including content_json for editor restore.
func (s *Server) handleAdminLegalPageGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	if !isValidLegalSlug(slug) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid slug")
		return
	}

	page, err := s.fetchLegalPage(r, a.ActiveRestaurantID, slug)
	if err != nil {
		if err == sql.ErrNoRows {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Página no encontrada"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando la página legal")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"page":    page,
	})
}

// POST /api/admin/legal-pages/{slug}
// Admin. Upserts the row for the active restaurant.
func (s *Server) handleAdminLegalPageUpsert(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	if !isValidLegalSlug(slug) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid slug")
		return
	}

	var req LegalPageUpsertRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if len(req.ContentJSON) > legalPageMaxFieldBytes || len(req.ContentHTML) > legalPageMaxFieldBytes {
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, "Contenido demasiado grande")
		return
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		httpx.WriteError(w, http.StatusBadRequest, "El título es obligatorio")
		return
	}

	contentJSON := req.ContentJSON
	if strings.TrimSpace(contentJSON) == "" {
		contentJSON = "[]"
	}

	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO legal_pages (restaurant_id, slug, title, content_json, content_html, updated_by_user_id)
		VALUES (?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			title = VALUES(title),
			content_json = VALUES(content_json),
			content_html = VALUES(content_html),
			updated_by_user_id = VALUES(updated_by_user_id)
	`, a.ActiveRestaurantID, slug, title, contentJSON, req.ContentHTML, a.User.ID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando la página legal")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}
