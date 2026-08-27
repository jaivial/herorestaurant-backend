package api

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/lib/specialmenuimage"
)

var boAdPublicRoutes = []string{"/", "/contacto", "/eventos", "/menufindesemana", "/menudeldia", "/menusdegrupos", "/postres", "/vinos", "/cafes", "/bebidas", "/reservas", "/reservas.php", "/avisolegal", "/avisolegal.html", "/booking-policies", "/booking_policies.php", "/confirm", "/cancel", "/update-rice", "/protecciondatos", "/protecciondatos.html", "/menusanvalentin", "/regala"}

const (
	boAdMaxTextElements = 5
	boAdMaxCTAs         = 5
	boAdMaxImageBytes   = 100 * 1024
	boAdT2IModel        = "wavespeed-ai/z-image/turbo"
	boAdEnhanceModel    = "openai/gpt-image-2/edit"
)

type boAdContentElement struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

type boAdCTA struct {
	ID             string `json:"id"`
	Text           string `json:"text"`
	Color          string `json:"color"`
	NavigationMode string `json:"navigation_mode"`
	Route          string `json:"route,omitempty"`
	CustomURL      string `json:"custom_url,omitempty"`
}

type boAdImageGenerationStatus string

const (
	boAdImageGenerationIdle    boAdImageGenerationStatus = "idle"
	boAdImageGenerationPending boAdImageGenerationStatus = "pending"
	boAdImageGenerationReady   boAdImageGenerationStatus = "ready"
	boAdImageGenerationFailed  boAdImageGenerationStatus = "failed"
)

type boAd struct {
	ID                       int64                     `json:"id"`
	Name                     string                    `json:"name"`
	Active                   bool                      `json:"active"`
	Content                  []boAdContentElement      `json:"content"`
	CTAs                     []boAdCTA                 `json:"ctas"`
	ImageGenerationStatus    boAdImageGenerationStatus `json:"image_generation_status,omitempty"`
	ImageGenerationStartedAt string                    `json:"image_generation_started_at,omitempty"`
	CreatedAt                string                    `json:"created_at,omitempty"`
	UpdatedAt                string                    `json:"updated_at,omitempty"`
}

type boAdInput struct {
	Name    string               `json:"name"`
	Active  bool                 `json:"active"`
	Content []boAdContentElement `json:"content"`
	CTAs    []boAdCTA            `json:"ctas"`
}

func normalizeBOAdContent(input []boAdContentElement) ([]boAdContentElement, error) {
	out := make([]boAdContentElement, 0, len(input))
	counts := map[string]int{}
	seen := map[string]bool{}
	for _, item := range input {
		item.ID = strings.TrimSpace(item.ID)
		item.Type = strings.ToLower(strings.TrimSpace(item.Type))
		item.Value = strings.TrimSpace(item.Value)
		if item.ID == "" {
			return nil, errors.New("content item id is required")
		}
		if seen[item.ID] {
			return nil, errors.New("duplicate content item id")
		}
		seen[item.ID] = true
		switch item.Type {
		case "title", "subtitle", "text":
			counts[item.Type]++
			if counts[item.Type] > boAdMaxTextElements {
				return nil, fmt.Errorf("maximum %d %s elements", boAdMaxTextElements, item.Type)
			}
		case "image":
			counts[item.Type]++
			if counts[item.Type] > 1 {
				return nil, errors.New("maximum 1 image element")
			}
		default:
			return nil, fmt.Errorf("invalid content type %q", item.Type)
		}
		out = append(out, item)
	}
	return out, nil
}

func normalizeBOAdCTAs(input []boAdCTA) ([]boAdCTA, error) {
	if len(input) > boAdMaxCTAs {
		return nil, fmt.Errorf("maximum %d call to action buttons", boAdMaxCTAs)
	}
	out := make([]boAdCTA, 0, len(input))
	seen := map[string]bool{}
	for _, cta := range input {
		cta.ID = strings.TrimSpace(cta.ID)
		cta.Text = strings.TrimSpace(cta.Text)
		cta.Color = strings.TrimSpace(cta.Color)
		cta.NavigationMode = strings.ToLower(strings.TrimSpace(cta.NavigationMode))
		cta.Route = strings.TrimSpace(cta.Route)
		cta.CustomURL = strings.TrimSpace(cta.CustomURL)
		if cta.ID == "" || seen[cta.ID] {
			return nil, errors.New("invalid call to action id")
		}
		seen[cta.ID] = true
		if cta.Color == "" {
			cta.Color = "#436754"
		}
		if cta.NavigationMode != "route" && cta.NavigationMode != "custom" {
			return nil, errors.New("invalid call to action navigation mode")
		}
		if cta.NavigationMode == "route" {
			allowed := false
			for _, route := range boAdPublicRoutes {
				if cta.Route == route {
					allowed = true
					break
				}
			}
			if !allowed {
				return nil, errors.New("route call to action requires a supported page route")
			}
			cta.CustomURL = ""
		}
		if cta.NavigationMode == "custom" {
			u, err := url.ParseRequestURI(cta.CustomURL)
			if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return nil, errors.New("custom call to action requires a valid http(s) URL")
			}
			cta.Route = ""
		}
		out = append(out, cta)
	}
	return out, nil
}

func boAdTextToImagePrompt(content []boAdContentElement) string {
	parts := make([]string, 0, len(content))
	for _, item := range content {
		if item.Type == "image" || strings.TrimSpace(item.Value) == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(item.Value))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Create a premium editorial restaurant advertising image inspired by this banner copy, without rendering any text inside the image. Mood should be elegant, photographic, inviting and suitable for a website popover. Banner copy in display order: " + strings.Join(parts, " | ")
}

func boAdTextToImageURL(baseURL string) string { return aiImageEditURLForModel(baseURL, boAdT2IModel) }
func boAdEnhanceURL(baseURL string) string     { return aiImageEditURLForModel(baseURL, boAdEnhanceModel) }

func (s *Server) readBOAd(ctx context.Context, restaurantID int, adID int64) (boAd, error) {
	var ad boAd
	var active int
	var contentRaw, ctasRaw []byte
	var statusRaw sql.NullString
	var startedAt, createdAt, updatedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT id, name, active, content_json, ctas_json, image_generation_status, image_generation_started_at, created_at, updated_at FROM restaurant_ads WHERE id = ? AND restaurant_id = ? LIMIT 1`, adID, restaurantID).
		Scan(&ad.ID, &ad.Name, &active, &contentRaw, &ctasRaw, &statusRaw, &startedAt, &createdAt, &updatedAt)
	if err != nil {
		return ad, err
	}
	ad.Active = active != 0
	if statusRaw.Valid && strings.TrimSpace(statusRaw.String) != "" {
		ad.ImageGenerationStatus = boAdImageGenerationStatus(strings.TrimSpace(statusRaw.String))
	}
	if startedAt.Valid {
		ad.ImageGenerationStartedAt = startedAt.Time.UTC().Format(time.RFC3339)
	}
	if err := json.Unmarshal(contentRaw, &ad.Content); err != nil {
		return ad, err
	}
	if err := json.Unmarshal(ctasRaw, &ad.CTAs); err != nil {
		return ad, err
	}
	if createdAt.Valid {
		ad.CreatedAt = createdAt.Time.UTC().Format(time.RFC3339)
	}
	if updatedAt.Valid {
		ad.UpdatedAt = updatedAt.Time.UTC().Format(time.RFC3339)
	}
	return ad, nil
}

func validateBOAdInput(input boAdInput) (boAdInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		input.Name = "Nuevo anuncio"
	}
	if len([]rune(input.Name)) > 160 {
		return input, errors.New("name is too long")
	}
	content, err := normalizeBOAdContent(input.Content)
	if err != nil {
		return input, err
	}
	ctas, err := normalizeBOAdCTAs(input.CTAs)
	if err != nil {
		return input, err
	}
	input.Content, input.CTAs = content, ctas
	return input, nil
}

func (s *Server) handleBOAdsList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id FROM restaurant_ads WHERE restaurant_id = ? ORDER BY updated_at DESC, id DESC`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading ads")
		return
	}
	defer rows.Close()
	ads := make([]boAd, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			httpx.WriteError(w, 500, "Error loading ads")
			return
		}
		ad, err := s.readBOAd(r.Context(), a.ActiveRestaurantID, id)
		if err != nil {
			httpx.WriteError(w, 500, "Error loading ads")
			return
		}
		ads = append(ads, ad)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "ads": ads})
}

func (s *Server) handleBOAdsCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	input := boAdInput{Name: "Nuevo anuncio", Content: []boAdContentElement{}, CTAs: []boAdCTA{}}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON body"})
			return
		}
	}
	normalized, err := validateBOAdInput(input)
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": err.Error()})
		return
	}
	contentRaw, _ := json.Marshal(normalized.Content)
	ctasRaw, _ := json.Marshal(normalized.CTAs)
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO restaurant_ads (restaurant_id, name, active, content_json, ctas_json) VALUES (?, ?, ?, ?, ?)`, a.ActiveRestaurantID, normalized.Name, boolToTinyint(normalized.Active), contentRaw, ctasRaw)
	if err != nil {
		httpx.WriteError(w, 500, "Error creating ad")
		return
	}
	id, _ := res.LastInsertId()
	ad, err := s.readBOAd(r.Context(), a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading ad")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "ad": ad})
}

func (s *Server) handleBOAdsUpdate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	adID, err := parseChiPositiveInt64(r, "adId")
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Invalid ad id"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	var input boAdInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Invalid JSON body"})
		return
	}
	input, err = validateBOAdInput(input)
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": err.Error()})
		return
	}
	contentRaw, _ := json.Marshal(input.Content)
	ctasRaw, _ := json.Marshal(input.CTAs)
	res, err := s.db.ExecContext(r.Context(), `UPDATE restaurant_ads SET name = ?, active = ?, content_json = ?, ctas_json = ? WHERE id = ? AND restaurant_id = ?`, input.Name, boolToTinyint(input.Active), contentRaw, ctasRaw, adID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, 500, "Error saving ad")
		return
	}
	// MySQL returns 0 from RowsAffected when the new payload is byte-for-byte
	// equal to the stored row (no actual change), so we cannot use it as a
	// proxy for "ad does not exist". readBOAd enforces the same WHERE scope
	// (id + restaurant_id) and returns sql.ErrNoRows when the row genuinely
	// doesn't exist.
	_ = res
	ad, err := s.readBOAd(r.Context(), a.ActiveRestaurantID, adID)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteJSON(w, 404, map[string]any{"success": false, "message": "Ad not found"})
		return
	}
	if err != nil {
		httpx.WriteError(w, 500, "Error loading ad")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "ad": ad})
}

func (s *Server) handleBOAdsDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	adID, err := parseChiPositiveInt64(r, "adId")
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Invalid ad id"})
		return
	}
	res, err := s.db.ExecContext(r.Context(), `DELETE FROM restaurant_ads WHERE id = ? AND restaurant_id = ?`, adID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, 500, "Error deleting ad")
		return
	}
	n, _ := res.RowsAffected()
	httpx.WriteJSON(w, 200, map[string]any{"success": n > 0})
}

func (s *Server) ensureBOAd(ctx context.Context, restaurantID int, adID int64) (boAd, error) {
	return s.readBOAd(ctx, restaurantID, adID)
}

// setBOAdImageGenerationStatus persists the AI image-generation lifecycle state
// for an ad. Best-effort: failures are logged but do not mask the original
// error from the caller, since callers wrap this around their primary work.
func (s *Server) setBOAdImageGenerationStatus(ctx context.Context, restaurantID int, adID int64, status boAdImageGenerationStatus, setStartedAt bool) {
	startedClause := ""
	args := []any{string(status), restaurantID, adID}
	if setStartedAt {
		startedClause = ", image_generation_started_at = NOW()"
	}
	res, err := s.db.ExecContext(ctx, "UPDATE restaurant_ads SET image_generation_status = ?"+startedClause+" WHERE id = ? AND restaurant_id = ?", args...)
	if err != nil {
		slog.Default().Warn("failed to update ad image_generation_status", "ad_id", adID, "restaurant_id", restaurantID, "status", status, "err", err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// No row matched — either the ad was deleted mid-flight or never existed.
		slog.Default().Warn("no ad row updated for image_generation_status", "ad_id", adID, "restaurant_id", restaurantID, "status", status)
	}
}

func readBOAdMultipartImage(r *http.Request, maxInput int) ([]byte, string, string, error) {
	if err := r.ParseMultipartForm(int64(maxInput)); err != nil {
		return nil, "", "", err
	}
	file, header, err := r.FormFile("image")
	if err != nil {
		return nil, "", "", err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, int64(maxInput)+1))
	if err != nil {
		return nil, "", "", err
	}
	if len(raw) == 0 || len(raw) > maxInput {
		return nil, "", "", errors.New("invalid image size")
	}
	return raw, header.Filename, header.Header.Get("Content-Type"), nil
}

func (s *Server) saveBOAdImage(ctx context.Context, restaurantID int, adID int64, raw []byte, filename, contentType, suffix string) (string, error) {
	normalized, err := specialmenuimage.NormalizeToWebPWithLimit(ctx, raw, filename, contentType, boAdMaxImageBytes)
	if err != nil {
		return "", err
	}
	objectPath := path.Join(strconv.Itoa(restaurantID), "pictures", "ads", strconv.FormatInt(adID, 10), fmt.Sprintf("%s-%d.webp", suffix, time.Now().UTC().UnixMilli()))
	if err := s.bunnyPut(ctx, restaurantID, objectPath, normalized, "image/webp"); err != nil {
		return "", err
	}
	return s.bunnyPullURL(ctx, restaurantID, objectPath), nil
}

func (s *Server) handleBOAdImageUpload(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	adID, err := parseChiPositiveInt64(r, "adId")
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Invalid ad id"})
		return
	}
	if _, err := s.ensureBOAd(r.Context(), a.ActiveRestaurantID, adID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, 404, map[string]any{"success": false, "message": "Ad not found"})
		} else {
			httpx.WriteError(w, http.StatusInternalServerError, "Error loading ad")
		}
		return
	}
	if !s.bunnyConfigured(r.Context(), a.ActiveRestaurantID) {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Image storage not configured"})
		return
	}
	raw, filename, ct, err := readBOAdMultipartImage(r, specialmenuimage.MaxInputBytes)
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Invalid image"})
		return
	}
	url, err := s.saveBOAdImage(r.Context(), a.ActiveRestaurantID, adID, raw, filename, ct, "upload")
	if err != nil {
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": err.Error()})
		return
	}
	s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationReady, false)
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "url": url})
}

func (s *Server) handleBOAdImageEnhance(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	adID, err := parseChiPositiveInt64(r, "adId")
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Invalid ad id"})
		return
	}
	if _, err := s.ensureBOAd(r.Context(), a.ActiveRestaurantID, adID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, 404, map[string]any{"success": false, "message": "Ad not found"})
		} else {
			httpx.WriteError(w, http.StatusInternalServerError, "Error loading ad")
		}
		return
	}
	provider := s.resolveAIImageProvider(r.Context(), a.ActiveRestaurantID)
	if strings.TrimSpace(provider.APIKey) == "" {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "WaveSpeed API key not configured"})
		return
	}
	if !s.bunnyConfigured(r.Context(), a.ActiveRestaurantID) {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Image storage not configured"})
		return
	}
	s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationPending, true)
	raw, filename, ct, err := readBOAdMultipartImage(r, specialmenuimage.MaxInputBytes)
	if err != nil {
		s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationFailed, false)
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Invalid image"})
		return
	}
	compressed, err := specialmenuimage.NormalizeToWebPWithLimit(r.Context(), raw, filename, ct, boAdMaxImageBytes)
	if err != nil {
		s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationFailed, false)
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.openAIRequestTimeout())
	defer cancel()
	output, err := s.callComidaImageEdit(ctx, boAdEnhanceURL(provider.BaseURL), provider.APIKey, "Enhance this restaurant advertising image into a premium website campaign photo. Preserve the subject and composition, improve lighting, detail and polish, and do not add any text or logos.", compressed, "image/webp")
	if err != nil {
		s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationFailed, false)
		httpx.WriteJSON(w, 502, map[string]any{"success": false, "message": aiFailureMessage("AI image enhancement failed", err)})
		return
	}
	url, err := s.saveBOAdImage(r.Context(), a.ActiveRestaurantID, adID, output, "enhanced", http.DetectContentType(output), "ai")
	if err != nil {
		s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationFailed, false)
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": err.Error()})
		return
	}
	s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationReady, false)
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "url": url})
}

func (s *Server) callBOAdTextToImage(ctx context.Context, endpoint, apiKey, prompt string) ([]byte, error) {
	body, _ := json.Marshal(map[string]any{"prompt": prompt, "enable_sync_mode": false, "enable_base64_output": false, "size": "1024*1024"})
	submit, err := s.waveSpeedDo(ctx, http.MethodPost, endpoint, apiKey, body)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(submit.Data.Status, "failed") {
		return nil, fmt.Errorf("wavespeed submit failed: %s", submit.Data.Error)
	}
	resultURL := strings.TrimSpace(submit.Data.URLs.Get)
	if resultURL == "" && strings.TrimSpace(submit.Data.ID) != "" {
		resultURL = s.waveSpeedResultFetchURL(submit.Data.ID)
	}
	if resultURL == "" {
		return nil, errors.New("wavespeed submit returned no result URL")
	}
	const maxIterations = 90 // ~3 min at 2s/poll, matches typical WaveSpeed timeout
	for i := 0; i < maxIterations; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
		env, err := s.waveSpeedDo(ctx, http.MethodGet, resultURL, apiKey, nil)
		if err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(env.Data.Status)) {
		case "failed":
			return nil, fmt.Errorf("wavespeed generation failed: %s", env.Data.Error)
		case "completed":
			if len(env.Data.Outputs) == 0 {
				return nil, errors.New("wavespeed completed with no outputs")
			}
			out := strings.TrimSpace(env.Data.Outputs[0])
			if strings.HasPrefix(out, "http://") || strings.HasPrefix(out, "https://") {
				return s.downloadOpenAIImageURL(ctx, out)
			}
			if strings.HasPrefix(out, "data:") {
				if i := strings.Index(out, ","); i >= 0 {
					out = out[i+1:]
				}
			}
			return base64.StdEncoding.DecodeString(out)
		}
	}
	return nil, errors.New("wavespeed generation timed out")
}

func (s *Server) handleBOAdImageGenerate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	adID, err := parseChiPositiveInt64(r, "adId")
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Invalid ad id"})
		return
	}
	ad, err := s.ensureBOAd(r.Context(), a.ActiveRestaurantID, adID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, 404, map[string]any{"success": false, "message": "Ad not found"})
		} else {
			httpx.WriteError(w, http.StatusInternalServerError, "Error loading ad")
		}
		return
	}
	provider := s.resolveAIImageProvider(r.Context(), a.ActiveRestaurantID)
	if strings.TrimSpace(provider.APIKey) == "" {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "WaveSpeed API key not configured"})
		return
	}
	if !s.bunnyConfigured(r.Context(), a.ActiveRestaurantID) {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Image storage not configured"})
		return
	}
	prompt := boAdTextToImagePrompt(ad.Content)
	if strings.TrimSpace(prompt) == "" {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Write banner text before generating an image"})
		return
	}
	s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationPending, true)
	ctx, cancel := context.WithTimeout(r.Context(), s.openAIRequestTimeout())
	defer cancel()
	output, err := s.callBOAdTextToImage(ctx, boAdTextToImageURL(provider.BaseURL), provider.APIKey, prompt)
	if err != nil {
		s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationFailed, false)
		httpx.WriteJSON(w, 502, map[string]any{"success": false, "message": aiFailureMessage("AI image generation failed", err)})
		return
	}
	url, err := s.saveBOAdImage(r.Context(), a.ActiveRestaurantID, adID, output, "generated", http.DetectContentType(output), "generated")
	if err != nil {
		s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationFailed, false)
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": err.Error()})
		return
	}
	s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationReady, false)
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "url": url})
}
