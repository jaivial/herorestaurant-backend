package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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

	// Use a unique object key per upload. BunnyCDN caches by object path and
	// ignores the query string as a cache key, so a fixed key (logo.webp) would
	// serve the first uploaded bytes forever. A unique path is always a cache
	// miss, so the freshly uploaded object is served immediately.
	objectPath := "branding/" + strconv.Itoa(restaurantID) + "/logo-" + strconv.FormatInt(time.Now().UnixNano(), 10) + ".webp"

	// Best-effort: remove the previously stored object to avoid orphaned files.
	if prev, perr := s.currentLogoObjectPath(ctx, restaurantID); perr == nil && prev != "" {
		_ = s.bunnyDelete(ctx, prev)
	}

	if err := s.bunnyPut(ctx, objectPath, normalizedWebP, "image/webp"); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Failed to upload logo to storage")
		return
	}

	fullURL := s.bunnyPullURL(objectPath)

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

// currentLogoObjectPath returns the BunnyCDN object path stored for a restaurant,
// or "" if none/unknown.
func (s *Server) currentLogoObjectPath(ctx context.Context, restaurantID int) (string, error) {
	var raw sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT logo_url FROM restaurant_branding WHERE restaurant_id = ? LIMIT 1`, restaurantID).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	u := strings.TrimSpace(raw.String)
	if u == "" {
		return "", nil
	}
	// Stored URL is the pull URL: https://<pull-base>/<objectPath>. Extract the
	// object path after the pull base.
	base := strings.TrimRight(strings.TrimSpace(s.cfg.BunnyPullBaseURL), "/")
	path := strings.TrimPrefix(u, base+"/")
	if path == u || path == "" {
		return "", nil
	}
	return path, nil
}

// bunnyDelete removes an object from BunnyCDN storage.
func (s *Server) bunnyDelete(ctx context.Context, objectPath string) error {
	if !s.bunnyConfigured() {
		return errors.New("BunnyCDN storage not configured")
	}
	return bunnyDeleteWithCredentials(ctx, strings.TrimSpace(s.cfg.BunnyStorageZone), strings.TrimSpace(s.cfg.BunnyStorageKey), objectPath)
}

// bunnyDeleteWithCredentials removes an object from BunnyCDN storage.
func bunnyDeleteWithCredentials(ctx context.Context, zone, accessKey, objectPath string) error {
	if strings.TrimSpace(zone) == "" || strings.TrimSpace(accessKey) == "" {
		return errors.New("invalid bunny credentials")
	}
	objectPath = strings.TrimLeft(objectPath, "/")
	escaped := bunnyEscapePath(objectPath)

	u := "https://storage.bunnycdn.com/" + url.PathEscape(zone) + "/" + escaped
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("AccessKey", accessKey)

	cli := &http.Client{Timeout: 30 * time.Second}
	res, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	// 404 means the object was already gone; treat as success.
	if res.StatusCode == http.StatusNotFound {
		return nil
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(res.Body, 8<<10))
	msg := strings.TrimSpace(string(b))
	if msg == "" {
		msg = res.Status
	}
	return fmt.Errorf("bunny delete failed (%d): %s", res.StatusCode, msg)
}
