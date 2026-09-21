package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/lib/specialmenuimage"
)

// =============================================================================
// Special menu image sections (step 4 of /app/comida/menus/crear?menuId=).
//
// One special menu can carry several image sections. Each section has an
// optional title above an image. The same flow serves both the backoffice
// editor (CRUD over these endpoints) and the public site (read-only JSON).
//
// Coordination id: special_menu_sections_v1
// =============================================================================

type boSpecialMenuSection struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	ImageURL  string `json:"image_url"`
	Position  int    `json:"position"`
	CreatedAt string `json:"created_at"`
}

// handleBOGroupMenusV2ListSpecialSections returns every section for one menu.
// The list is ordered by position so the editor can render it in display order
// without re-sorting client-side.
func (s *Server) handleBOGroupMenusV2ListSpecialSections(w http.ResponseWriter, r *http.Request) {
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
	owns, err := s.ensureBOMenuV2Belongs(a.ActiveRestaurantID, menuID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error checking menu")
		return
	}
	if !owns {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
		return
	}

	sections, err := s.loadSpecialMenuSections(r.Context(), a.ActiveRestaurantID, menuID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando secciones")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "sections": sections})
}

// handleBOGroupMenusV2CreateSpecialSection appends a new section. It accepts a
// JSON body so the editor can create an empty section and fill it later, or
// create one with title + image in a single call.
func (s *Server) handleBOGroupMenusV2CreateSpecialSection(w http.ResponseWriter, r *http.Request) {
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
	owns, err := s.ensureBOMenuV2Belongs(a.ActiveRestaurantID, menuID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error checking menu")
		return
	}
	if !owns {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
		return
	}

	var req struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	title := strings.TrimSpace(req.Title)

	var nextPos int
	if err := s.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(MAX(position), -1) + 1 FROM special_menu_sections WHERE restaurant_id = ? AND menu_id = ?`,
		a.ActiveRestaurantID, menuID).Scan(&nextPos); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reservando posicion")
		return
	}

	res, err := s.db.ExecContext(r.Context(),
		`INSERT INTO special_menu_sections (restaurant_id, menu_id, title, position) VALUES (?, ?, ?, ?)`,
		a.ActiveRestaurantID, menuID, title, nextPos)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando seccion")
		return
	}
	newID, _ := res.LastInsertId()

	logCheckpoint(r, "special_menu_section_created",
		"menu_id", strconv.FormatInt(menuID, 10),
		"section_id", strconv.FormatInt(newID, 10),
		"position", strconv.Itoa(nextPos))

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"section": boSpecialMenuSection{
			ID:       newID,
			Title:    title,
			Position: nextPos,
		},
	})
}

// handleBOGroupMenusV2PatchSpecialSection updates the title of a single section.
// The image lives behind its own upload endpoint so each field has a single
// writer and we never have to deal with multipart bodies when only the title
// changed.
func (s *Server) handleBOGroupMenusV2PatchSpecialSection(w http.ResponseWriter, r *http.Request) {
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
	owns, err := s.ensureBOMenuV2Belongs(a.ActiveRestaurantID, menuID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error checking menu")
		return
	}
	if !owns {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
		return
	}

	var req struct {
		Title *string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	if req.Title == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Nothing to update"})
		return
	}
	title := strings.TrimSpace(*req.Title)

	if _, err := s.db.ExecContext(r.Context(),
		`UPDATE special_menu_sections SET title = ? WHERE id = ? AND menu_id = ? AND restaurant_id = ?`,
		title, sectionID, menuID, a.ActiveRestaurantID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando seccion")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// handleBOGroupMenusV2DeleteSpecialSection removes a section row and any
// image it had on BunnyCDN. The image is best-effort: if the upload bucket
// was already cleaned out, we still want the row to be gone.
func (s *Server) handleBOGroupMenusV2DeleteSpecialSection(w http.ResponseWriter, r *http.Request) {
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
	owns, err := s.ensureBOMenuV2Belongs(a.ActiveRestaurantID, menuID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error checking menu")
		return
	}
	if !owns {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
		return
	}

	var imagePath string
	err = s.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(image_path, '') FROM special_menu_sections WHERE id = ? AND menu_id = ? AND restaurant_id = ?`,
		sectionID, menuID, a.ActiveRestaurantID).Scan(&imagePath)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Section not found"})
		return
	}

	if _, err := s.db.ExecContext(r.Context(),
		`DELETE FROM special_menu_sections WHERE id = ? AND menu_id = ? AND restaurant_id = ?`,
		sectionID, menuID, a.ActiveRestaurantID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando seccion")
		return
	}

	if imagePath != "" {
		// image_path already is the full BunnyCDN object path.
		_ = s.bunnyDelete(r.Context(), a.ActiveRestaurantID, imagePath)
	}

	// Re-pack positions so the remaining sections stay 0..N-1 in display order.
	if err := repackSpecialMenuSections(r, s, a.ActiveRestaurantID, menuID); err != nil {
		logCheckpoint(r, "special_menu_section_repack_failed",
			"menu_id", strconv.FormatInt(menuID, 10))
	}

	logCheckpoint(r, "special_menu_section_deleted",
		"menu_id", strconv.FormatInt(menuID, 10),
		"section_id", strconv.FormatInt(sectionID, 10))

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// handleBOGroupMenusV2ReorderSpecialSections rewrites the position column for
// every section in one transaction so the editor's drag-reorder lands atomically.
func (s *Server) handleBOGroupMenusV2ReorderSpecialSections(w http.ResponseWriter, r *http.Request) {
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
	owns, err := s.ensureBOMenuV2Belongs(a.ActiveRestaurantID, menuID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error checking menu")
		return
	}
	if !owns {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
		return
	}

	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	if len(req.IDs) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error abriendo transaccion")
		return
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(r.Context(),
		`UPDATE special_menu_sections SET position = ? WHERE id = ? AND menu_id = ? AND restaurant_id = ?`)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error preparando actualizacion")
		return
	}
	defer stmt.Close()

	for idx, sectionID := range req.IDs {
		if sectionID <= 0 {
			continue
		}
		if _, err := stmt.ExecContext(r.Context(), idx, sectionID, menuID, a.ActiveRestaurantID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reordenando secciones")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando reorden")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// handleBOGroupMenusV2UploadSpecialSectionImage stores an image for one section.
// The body is a multipart/form-data with field "image"; we normalize it to WebP
// on the server, push it to BunnyCDN under
// {restaurant_id}/pictures/menus_especiales/sections/{section_id}-{millis}.webp
// and persist the public URL on the section row. The timestamped object name
// keeps every upload at a fresh URL so a re-upload can never keep serving the
// previously cached image at the same path.
// Coordination id: special_menu_sections_v1
func (s *Server) handleBOGroupMenusV2UploadSpecialSectionImage(w http.ResponseWriter, r *http.Request) {
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
	owns, err := s.ensureBOMenuV2Belongs(a.ActiveRestaurantID, menuID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error checking menu")
		return
	}
	if !owns {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
		return
	}
	var prevImagePath string
	if err := s.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(image_path, '') FROM special_menu_sections WHERE id = ? AND menu_id = ? AND restaurant_id = ?`,
		sectionID, menuID, a.ActiveRestaurantID).Scan(&prevImagePath); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Section not found"})
		return
	}

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error parsing form"})
		return
	}
	file, header, err := r.FormFile("image")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "No image file provided"})
		return
	}
	defer file.Close()

	imgData, err := io.ReadAll(file)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error reading file"})
		return
	}

	normalizedWebP, err := specialmenuimage.NormalizeToWebP(
		r.Context(),
		imgData,
		header.Filename,
		header.Header.Get("Content-Type"),
	)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error procesando imagen: " + err.Error()})
		return
	}

	objectPath := path.Join(
		strconv.Itoa(a.ActiveRestaurantID),
		"pictures",
		"menus_especiales",
		"sections",
		fmt.Sprintf("%d-%d.webp", sectionID, time.Now().UnixMilli()),
	)
	if err := s.bunnyPut(r.Context(), a.ActiveRestaurantID, objectPath, normalizedWebP, "image/webp"); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error subiendo imagen: " + err.Error()})
		return
	}

	if _, err := s.db.ExecContext(r.Context(),
		`UPDATE special_menu_sections SET image_path = ? WHERE id = ? AND menu_id = ? AND restaurant_id = ?`,
		objectPath, sectionID, menuID, a.ActiveRestaurantID); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error guardando imagen"})
		return
	}

	imageURL := s.bunnyPullURL(r.Context(), a.ActiveRestaurantID, objectPath)
	// Best-effort cleanup of the object the row pointed at before this upload.
	if prevImagePath != "" && prevImagePath != objectPath {
		_ = s.bunnyDelete(r.Context(), a.ActiveRestaurantID, prevImagePath)
	}
	logCheckpoint(r, "special_menu_section_image_uploaded",
		"menu_id", strconv.FormatInt(menuID, 10),
		"section_id", strconv.FormatInt(sectionID, 10))

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "image_url": imageURL})
}

// handleBOGroupMenusV2DeleteSpecialSectionImage clears the image_path of a
// section. The CDN object is removed best-effort; the row gets the field
// cleared so the editor renders the empty state again.
func (s *Server) handleBOGroupMenusV2DeleteSpecialSectionImage(w http.ResponseWriter, r *http.Request) {
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
	owns, err := s.ensureBOMenuV2Belongs(a.ActiveRestaurantID, menuID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error checking menu")
		return
	}
	if !owns {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
		return
	}

	var imagePath string
	err = s.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(image_path, '') FROM special_menu_sections WHERE id = ? AND menu_id = ? AND restaurant_id = ?`,
		sectionID, menuID, a.ActiveRestaurantID).Scan(&imagePath)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Section not found"})
		return
	}

	if _, err := s.db.ExecContext(r.Context(),
		`UPDATE special_menu_sections SET image_path = NULL WHERE id = ? AND menu_id = ? AND restaurant_id = ?`,
		sectionID, menuID, a.ActiveRestaurantID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando imagen")
		return
	}
	if imagePath != "" {
		_ = s.bunnyDelete(r.Context(), a.ActiveRestaurantID, imagePath)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// =============================================================================
// Special menu public visibility (per-menu web_placement / menu_public_active).
//
// Mirrors restaurant_page_visibility so the backoffice configuracion tab can
// reuse the same dropdown the food-type settings already use.
// Coordination id: special_menu_visibility_v1
// =============================================================================

type specialMenuVisibilityPatch struct {
	WebPlacement *string `json:"web_placement,omitempty"`
	Active       *bool   `json:"menu_public_active,omitempty"`
}

// handleBOGroupMenusV2PatchSpecialMenuVisibility persists the visibility pair
// for one menu. Anything missing from the patch leaves the stored value alone,
// so the editor can toggle a single field without re-sending the other.
func (s *Server) handleBOGroupMenusV2PatchSpecialMenuVisibility(w http.ResponseWriter, r *http.Request) {
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
	owns, err := s.ensureBOMenuV2Belongs(a.ActiveRestaurantID, menuID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error checking menu")
		return
	}
	if !owns {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Menu not found"})
		return
	}

	var req specialMenuVisibilityPatch
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}

	sets := make([]string, 0, 2)
	args := make([]any, 0, 4)
	placement := ""
	if req.WebPlacement != nil {
		placement = normalizedWebPlacement(*req.WebPlacement)
		sets = append(sets, "web_placement = ?")
		args = append(args, placement)
	}
	if req.Active != nil {
		sets = append(sets, "menu_public_active = ?")
		args = append(args, boolToTinyint(*req.Active))
	}
	if len(sets) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Nothing to update"})
		return
	}
	args = append(args, menuID, a.ActiveRestaurantID)

	if _, err := s.db.ExecContext(r.Context(), fmt.Sprintf(`
		UPDATE menus SET %s
		WHERE id = ? AND restaurant_id = ?
	`, strings.Join(sets, ", ")), args...); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando visibilidad")
		return
	}

	logCheckpoint(r, "special_menu_visibility_persisted",
		"menu_id", strconv.FormatInt(menuID, 10),
		"web_placement", placement)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":            true,
		"web_placement":      placement,
		"menu_public_active": req.Active,
	})
}

// loadSpecialMenuSections returns the sections for one menu, ordered by
// position, with image_path resolved to the public BunnyCDN URL.
func (s *Server) loadSpecialMenuSections(ctx context.Context, restaurantID int, menuID int64) ([]boSpecialMenuSection, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title, COALESCE(image_path, ''), position, COALESCE(created_at, CURRENT_TIMESTAMP)
		FROM special_menu_sections
		WHERE restaurant_id = ? AND menu_id = ?
		ORDER BY position ASC, id ASC
	`, restaurantID, menuID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]boSpecialMenuSection, 0, 4)
	for rows.Next() {
		var sec boSpecialMenuSection
		if err := rows.Scan(&sec.ID, &sec.Title, &sec.ImageURL, &sec.Position, &sec.CreatedAt); err != nil {
			return nil, err
		}
		if sec.ImageURL != "" {
			sec.ImageURL = s.publicMenuMediaURL(ctx, restaurantID, sec.ImageURL)
		}
		out = append(out, sec)
	}
	return out, rows.Err()
}

// repackSpecialMenuSections rewrites the position column to 0..N-1 so any
// holes left by a delete are closed before the next render.
func repackSpecialMenuSections(r *http.Request, s *Server, restaurantID int, menuID int64) error {
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id FROM special_menu_sections WHERE restaurant_id = ? AND menu_id = ? ORDER BY position ASC, id ASC`,
		restaurantID, menuID)
	if err != nil {
		return err
	}
	defer rows.Close()
	ids := make([]int64, 0, 4)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(r.Context(),
		`UPDATE special_menu_sections SET position = ? WHERE id = ? AND restaurant_id = ? AND menu_id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for idx, id := range ids {
		if _, err := stmt.ExecContext(r.Context(), idx, id, restaurantID, menuID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
