package api

import (
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/lib/specialmenuimage"
)

// handleBOComidaImageUpload handles POST /api/admin/comida/{tipo}/{id}/image.
// Uploads a user-provided image, compresses to WebP, stores in Bunny CDN,
// and saves the URL to comida_items.foto_url.
func (s *Server) handleBOComidaImageUpload(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	tipoStr := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "tipo")))
	ct, ok := normalizeComidaTipo(tipoStr)
	if !ok || ct == comidaTipoVinos || ct == comidaTipoPostres {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Tipo no soportado para subida de imagen",
		})
		return
	}

	idStr := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "ID invalido",
		})
		return
	}

	// Verify item exists and belongs to restaurant
	var exists int
	err = s.db.QueryRowContext(r.Context(),
		"SELECT 1 FROM comida_items WHERE id = ? AND restaurant_id = ? AND source_type = ?",
		id, a.ActiveRestaurantID, string(ct),
	).Scan(&exists)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Elemento no encontrado",
		})
		return
	}

	// Parse multipart form — max 8 MB input
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Error parsing form",
		})
		return
	}

	file, _, err := r.FormFile("image")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "No image file provided",
		})
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, 8<<20+1))
	if err != nil || len(raw) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Error reading file or file is empty",
		})
		return
	}
	if len(raw) > 8<<20 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "File too large (max 8 MB)",
		})
		return
	}

	contentType := http.DetectContentType(raw)
	allowedTypes := map[string]bool{
		"image/jpeg": true,
		"image/png":  true,
		"image/webp": true,
	}
	if !allowedTypes[contentType] {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "File type not allowed. Use JPG, PNG or WebP.",
		})
		return
	}

	// Compress and normalize to WebP using ImageMagick (reuses existing infrastructure)
	ctx := r.Context()
	normalizedWebP, err := specialmenuimage.NormalizeToWebP(ctx, raw, "item.webp", contentType)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Failed to process image: " + err.Error(),
		})
		return
	}

	// Timestamped filename so each upload produces a unique URL (cache-busting;
	// mirrors the AI path which uses {id}-ai-{ms}.webp).
	version := strconv.FormatInt(time.Now().UTC().UnixMilli(), 10)
	objectPath := path.Join("images", "comida", string(ct), strconv.Itoa(id)+"-"+version+".webp")
	if err := s.bunnyPut(ctx, a.ActiveRestaurantID, objectPath, normalizedWebP, "image/webp"); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Failed to upload image to storage",
		})
		return
	}

	fullURL := s.bunnyPullURL(ctx, a.ActiveRestaurantID, objectPath)

	// Save foto_url (not foto_path — keeps AI workflow separate)
	_, err = s.db.ExecContext(ctx, `
		UPDATE comida_items
		SET foto_url = ?, foto_path = NULL, foto = NULL, ai_generating = 0
		WHERE id = ? AND restaurant_id = ? AND source_type = ?
	`, fullURL, id, a.ActiveRestaurantID, string(ct))
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Image uploaded but failed to save URL to database",
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"foto_url": fullURL,
		"message":  "Image uploaded successfully",
	})
}
