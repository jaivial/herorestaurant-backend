package api

// Coordination id: wine_image_cutout_v2 - background removal runs on the
// WaveSpeed API (no model on our servers or in the browser); the backend then
// trims the transparent PNG to the object's bounding box, stores it in Bunny
// and returns the public URL.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

const (
	waveSpeedBgRemoverModel = "wavespeed-ai/image-background-remover"
	cutoutAlphaThreshold    = 8
	cutoutPollInterval      = 1500 * time.Millisecond
	cutoutTimeout           = 90 * time.Second
)

func (s *Server) handleBOVinoImageCutout(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	wineNum, err := strconv.Atoi(strings.TrimSpace(chi.URLParam(r, "id")))
	if err != nil || wineNum <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid wine id"})
		return
	}
	maxInput := s.openAIInputMaxBytes()
	if err := r.ParseMultipartForm(int64(maxInput)); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error parsing form"})
		return
	}
	file, _, err := r.FormFile("image")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "No image file provided"})
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, int64(maxInput)+1))
	if err != nil || len(raw) == 0 || len(raw) > maxInput {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Imagen inválida o demasiado grande"})
		return
	}
	ct := http.DetectContentType(raw)
	if !strings.HasPrefix(ct, "image/jpeg") && !strings.HasPrefix(ct, "image/png") && !strings.HasPrefix(ct, "image/webp") {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "File type not allowed"})
		return
	}
	var wineTipo string
	if err := s.db.QueryRowContext(r.Context(), "SELECT COALESCE(tipo,'') FROM VINOS WHERE num = ? AND restaurant_id = ? LIMIT 1", wineNum, a.ActiveRestaurantID).Scan(&wineTipo); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Wine not found"})
		return
	}
	provider := s.resolveAIImageProvider(r.Context(), a.ActiveRestaurantID)
	if provider.APIKey == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "AI provider not configured"})
		return
	}

	started := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), cutoutTimeout)
	defer cancel()
	cutout, err := s.callWaveSpeedBgRemover(ctx, provider.BaseURL, provider.APIKey, raw, ct)
	if err != nil {
		log.Printf("[wine_image_cutout_v2] restaurant=%d wine=%d provider_error=%v", a.ActiveRestaurantID, wineNum, err)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "No se pudo quitar el fondo"})
		return
	}
	trimmed, err := trimTransparentPNG(cutout)
	if err != nil {
		log.Printf("[wine_image_cutout_v2] restaurant=%d wine=%d trim_error=%v", a.ActiveRestaurantID, wineNum, err)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "No se detectó ningún objeto en la imagen"})
		return
	}
	objectPath, err := s.UploadWineImageV2(r.Context(), a.ActiveRestaurantID, wineTipo, wineNum, trimmed, "image/png")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error uploading image"})
		return
	}
	if _, err := s.db.ExecContext(r.Context(), "UPDATE VINOS SET foto_path = ?, foto = NULL WHERE num = ? AND restaurant_id = ?", objectPath, wineNum, a.ActiveRestaurantID); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error saving image path"})
		return
	}
	log.Printf("[wine_image_cutout_v2] restaurant=%d wine=%d ok ms=%d bytes=%d", a.ActiveRestaurantID, wineNum, time.Since(started).Milliseconds(), len(trimmed))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"foto_url": s.bunnyPullURL(r.Context(), a.ActiveRestaurantID, objectPath),
	})
}

// callWaveSpeedBgRemover submits the image to WaveSpeed's background remover
// and polls the prediction until it finishes. Returns the RGBA PNG bytes.
func (s *Server) callWaveSpeedBgRemover(ctx context.Context, baseURL, apiKey string, input []byte, contentType string) ([]byte, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = "https://api.wavespeed.ai"
	}
	body, err := json.Marshal(map[string]any{
		"image": "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(input),
	})
	if err != nil {
		return nil, err
	}
	submit, err := s.waveSpeedDo(ctx, http.MethodPost, base+"/api/v3/"+waveSpeedBgRemoverModel, apiKey, body)
	if err != nil {
		return nil, err
	}
	resultURL := strings.TrimSpace(submit.Data.URLs.Get)
	if resultURL == "" && strings.TrimSpace(submit.Data.ID) != "" {
		resultURL = base + "/api/v3/predictions/" + strings.TrimSpace(submit.Data.ID) + "/result"
	}
	if resultURL == "" {
		return nil, errors.New("wavespeed submit returned no result URL")
	}
	env := submit
	for {
		switch strings.ToLower(strings.TrimSpace(env.Data.Status)) {
		case "completed":
			if len(env.Data.Outputs) == 0 {
				return nil, errors.New("wavespeed completed with no outputs")
			}
			return s.downloadOpenAIImageURL(ctx, strings.TrimSpace(env.Data.Outputs[0]))
		case "failed":
			return nil, fmt.Errorf("wavespeed background removal failed: %s", env.Data.Error)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(cutoutPollInterval):
		}
		if next, err := s.waveSpeedDo(ctx, http.MethodGet, resultURL, apiKey, nil); err == nil {
			env = next
		}
	}
}

// trimTransparentPNG crops an image to the bounding box of its non-transparent
// pixels so no empty margin is left between the object and the file edges.
func trimTransparentPNG(raw []byte) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X-1, b.Min.Y-1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, alpha := src.At(x, y).RGBA(); alpha>>8 <= cutoutAlphaThreshold {
				continue
			}
			minX, minY = min(minX, x), min(minY, y)
			maxX, maxY = max(maxX, x), max(maxY, y)
		}
	}
	if maxX < minX || maxY < minY {
		return nil, errors.New("image is fully transparent")
	}
	box := image.Rect(minX, minY, maxX+1, maxY+1)
	out := image.NewNRGBA(image.Rect(0, 0, box.Dx(), box.Dy()))
	draw.Draw(out, out.Bounds(), src, box.Min, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
