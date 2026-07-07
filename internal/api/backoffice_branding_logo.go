package api

import (
	"io"
	"net/http"
	"strconv"
	"time"

	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/lib/specialmenuimage"
)

// handleBOBrandingLogoUpload handles POST /api/admin/branding/logo.
// Accepts a user-provided image, normalizes to WebP ≤50 KB, uploads to BunnyCDN
// at branding/{restaurantId}/logo.webp, and stores the resulting pull URL in
// restaurant_branding.logo_url.
func (s *Server) handleBOBrandingLogoUpload(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}
	restaurantID := a.ActiveRestaurantID

	if !s.bunnyConfigured() {
		httpx.WriteError(w, http.StatusServiceUnavailable, "Almacenamiento de imágenes no configurado")
		return
	}

	if err := r.ParseMultipartForm(8 << 20); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Error parsing form"})
		return
	}

	file, _, err := r.FormFile("image")
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "No image file provided"})
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, 8<<20+1))
	if err != nil || len(raw) == 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Error reading file or file is empty"})
		return
	}
	if len(raw) > 8<<20 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "File too large (max 8 MB)"})
		return
	}

	contentType := http.DetectContentType(raw)
	allowedTypes := map[string]bool{
		"image/jpeg": true,
		"image/png":  true,
		"image/webp": true,
	}
	if !allowedTypes[contentType] {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "File type not allowed. Use JPG, PNG or WebP."})
		return
	}

	ctx := r.Context()
	normalizedWebP, err := specialmenuimage.NormalizeToWebPWithLimit(ctx, raw, "logo.webp", contentType, 50*1024)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Failed to process image: " + err.Error()})
		return
	}

	objectPath := "branding/" + strconv.Itoa(restaurantID) + "/logo.webp"
	if err := s.bunnyPut(ctx, objectPath, normalizedWebP, "image/webp"); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Failed to upload logo to storage")
		return
	}

	fullURL := s.bunnyPullURL(objectPath) + "?v=" + strconv.FormatInt(time.Now().UnixNano(), 10)

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO restaurant_branding (restaurant_id, logo_url) VALUES (?, ?)
		ON DUPLICATE KEY UPDATE logo_url = VALUES(logo_url)
	`, restaurantID, fullURL)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Image uploaded but failed to save logo URL")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"logoUrl": fullURL,
	})
}