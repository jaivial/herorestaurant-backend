package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	ImageURL string `json:"image_url"`
	// Coordination id: special_menu_sections_image_state_v1
	ImageState string `json:"image_state"`
	Position   int    `json:"position"`
	CreatedAt  string `json:"created_at"`
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
			ID:         newID,
			Title:      title,
			ImageState: boSpecialSectionImageStateEmpty,
			Position:   nextPos,
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

// =============================================================================
// Section image uploads over the group-menus-v2 socket ("socket method").
//
// Coordination id: special_menu_sections_image_state_v1
//
// The upload is a WS frame carrying the image as base64. The row flips to
// 'uploading' right away and the image is normalized and pushed to BunnyCDN by
// a background task, so closing the page never cancels an upload. The stored
// state (empty/uploading/ready) is what a reload renders: default dropzone,
// skeleton or the final image.
// =============================================================================

const (
	boSpecialSectionImageStateEmpty     = "empty"
	boSpecialSectionImageStateUploading = "uploading"
	boSpecialSectionImageStateReady     = "ready"
)

// maxSpecialSectionImageBytes matches the 10MB the editor accepts.
const maxSpecialSectionImageBytes = 10 << 20

type boSpecialSectionImageJob struct {
	RestaurantID int
	MenuID       int64
	SectionID    int64
	Filename     string
	RawImage     []byte
}

// handleBOSpecialSectionImageWSMessage validates a special_section_image_upload
// frame, persists the 'uploading' state and queues the background task.
func (s *Server) handleBOSpecialSectionImageWSMessage(r *http.Request, restaurantID int, menuID int64, client *boGroupMenuV2AIClient, raw []byte) {
	var msg struct {
		Type          string `json:"type"`
		SectionID     int64  `json:"section_id"`
		Filename      string `json:"filename"`
		Data          string `json:"data"`
		CorrelationID string `json:"correlation_id"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}
	fail := func(message string) {
		_ = client.writeJSON(map[string]any{
			"type":           "special_section_image_error",
			"restaurant_id":  restaurantID,
			"menu_id":        menuID,
			"section_id":     msg.SectionID,
			"image_state":    boSpecialSectionImageStateEmpty,
			"message":        message,
			"correlation_id": msg.CorrelationID,
		})
	}
	if strings.ToLower(strings.TrimSpace(msg.Type)) != "special_section_image_upload" || msg.SectionID <= 0 {
		fail("Seccion o imagen no validas")
		return
	}
	payload, err := base64.StdEncoding.DecodeString(msg.Data)
	if err != nil || len(payload) == 0 {
		fail("Imagen vacia o invalida")
		return
	}
	if len(payload) > maxSpecialSectionImageBytes {
		fail("Imagen demasiado grande (maximo 10MB)")
		return
	}
	var count int
	if err := s.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM special_menu_sections WHERE id = ? AND menu_id = ? AND restaurant_id = ?`,
		msg.SectionID, menuID, restaurantID).Scan(&count); err != nil || count == 0 {
		fail("Section not found")
		return
	}
	if err := s.setBOSpecialSectionImageState(r.Context(), restaurantID, menuID, msg.SectionID, boSpecialSectionImageStateUploading); err != nil {
		fail("No se pudo guardar el estado de la subida")
		return
	}
	s.broadcastBOGroupMenuV2AIEvent(restaurantID, menuID, "special_section_image_started", map[string]any{
		"section_id":     msg.SectionID,
		"image_state":    boSpecialSectionImageStateUploading,
		"correlation_id": msg.CorrelationID,
	})
	s.logBOGroupMenuV2AITrace(
		"special section image upload queued restaurant=%d menu=%d section=%d bytes=%d",
		restaurantID, menuID, msg.SectionID, len(payload),
	)
	go s.runBOSpecialSectionImageJob(boSpecialSectionImageJob{
		RestaurantID: restaurantID,
		MenuID:       menuID,
		SectionID:    msg.SectionID,
		Filename:     msg.Filename,
		RawImage:     payload,
	})
}

// setBOSpecialSectionImageState writes the upload state the editor hydrates
// from. Coordination id: special_menu_sections_image_state_v1
func (s *Server) setBOSpecialSectionImageState(ctx context.Context, restaurantID int, menuID, sectionID int64, state string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE special_menu_sections
		SET image_state = ?, image_state_at = CURRENT_TIMESTAMP
		WHERE id = ? AND menu_id = ? AND restaurant_id = ?
	`, state, sectionID, menuID, restaurantID)
	return err
}

// runBOSpecialSectionImageJob normalizes the uploaded bytes to WebP, pushes
// them to BunnyCDN under a fresh object name and flips the section to 'ready'.
// It runs as a background task, so closing the page keeps the upload going and
// the state broadcast lands on whatever clients are connected by then.
func (s *Server) runBOSpecialSectionImageJob(job boSpecialSectionImageJob) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	fail := func(message string) {
		if err := s.setBOSpecialSectionImageState(ctx, job.RestaurantID, job.MenuID, job.SectionID, boSpecialSectionImageStateEmpty); err != nil {
			s.logBOGroupMenuV2AITrace("special section image state reset error section=%d err=%v", job.SectionID, err)
		}
		s.broadcastBOGroupMenuV2AIEvent(job.RestaurantID, job.MenuID, "special_section_image_error", map[string]any{
			"section_id":  job.SectionID,
			"image_state": boSpecialSectionImageStateEmpty,
			"message":     message,
		})
	}

	normalizedWebP, err := specialmenuimage.NormalizeToWebP(ctx, job.RawImage, job.Filename, http.DetectContentType(job.RawImage))
	if err != nil {
		s.logBOGroupMenuV2AITrace("special section image normalize error section=%d err=%v", job.SectionID, err)
		fail("No se pudo procesar la imagen")
		return
	}

	var prevImagePath string
	_ = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(image_path, '') FROM special_menu_sections WHERE id = ? AND menu_id = ? AND restaurant_id = ?`,
		job.SectionID, job.MenuID, job.RestaurantID).Scan(&prevImagePath)

	// Timestamped object name: a re-upload can never keep serving the
	// previously cached image at the same path.
	objectPath := path.Join(
		strconv.Itoa(job.RestaurantID),
		"pictures",
		"menus_especiales",
		"sections",
		fmt.Sprintf("%d-%d.webp", job.SectionID, time.Now().UnixMilli()),
	)
	if err := s.bunnyPut(ctx, job.RestaurantID, objectPath, normalizedWebP, "image/webp"); err != nil {
		s.logBOGroupMenuV2AITrace("special section image put error section=%d err=%v", job.SectionID, err)
		fail("No se pudo subir la imagen")
		return
	}

	if _, err := s.db.ExecContext(ctx, `
		UPDATE special_menu_sections
		SET image_path = ?, image_state = ?, image_state_at = CURRENT_TIMESTAMP
		WHERE id = ? AND menu_id = ? AND restaurant_id = ?
	`, objectPath, boSpecialSectionImageStateReady, job.SectionID, job.MenuID, job.RestaurantID); err != nil {
		fail("No se pudo guardar la imagen")
		return
	}

	// Best-effort cleanup of the object the row pointed at before this upload.
	if prevImagePath != "" && prevImagePath != objectPath {
		_ = s.bunnyDelete(ctx, job.RestaurantID, prevImagePath)
	}

	s.logBOGroupMenuV2AITrace(
		"special section image uploaded restaurant=%d menu=%d section=%d",
		job.RestaurantID, job.MenuID, job.SectionID,
	)
	s.broadcastBOGroupMenuV2AIEvent(job.RestaurantID, job.MenuID, "special_section_image_ready", map[string]any{
		"section_id":  job.SectionID,
		"image_state": boSpecialSectionImageStateReady,
		"image_url":   s.bunnyPullURL(ctx, job.RestaurantID, objectPath),
	})
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
		`UPDATE special_menu_sections SET image_path = NULL, image_state = 'empty', image_state_at = CURRENT_TIMESTAMP WHERE id = ? AND menu_id = ? AND restaurant_id = ?`,
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
		SELECT id, title, COALESCE(image_path, ''), position, COALESCE(created_at, CURRENT_TIMESTAMP),
		       CASE
		           WHEN image_state = 'uploading' AND image_state_at >= NOW() - INTERVAL 5 MINUTE THEN 'uploading'
		           WHEN COALESCE(image_path, '') <> '' THEN 'ready'
		           ELSE 'empty'
		       END
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
		if err := rows.Scan(&sec.ID, &sec.Title, &sec.ImageURL, &sec.Position, &sec.CreatedAt, &sec.ImageState); err != nil {
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
