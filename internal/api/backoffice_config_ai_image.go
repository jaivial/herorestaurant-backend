package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// AI image provider configuration (root-only). DB-backed replacement for the
// env-only WAVESPEED_API_KEY. The raw API key is never returned to clients:
// GET returns only hasApiKey + a masked preview.

type boAIImageProvider struct {
	Slug    string `json:"slug"`
	Label   string `json:"label"`
	BaseURL string `json:"baseUrl"`
	DocsURL string `json:"docsUrl,omitempty"`
}

type boAIImageModel struct {
	ProviderSlug string `json:"providerSlug"`
	Slug         string `json:"slug"`
	Label        string `json:"label"`
	Mode         string `json:"mode"` // "t2i" | "i2i"
}

type boAIImageConfig struct {
	ProviderSlug string `json:"providerSlug"`
	HasAPIKey    bool   `json:"hasApiKey"`
	APIKeyMask   string `json:"apiKeyMask,omitempty"`
	T2IModelSlug string `json:"t2iModelSlug,omitempty"`
	I2IModelSlug string `json:"i2iModelSlug,omitempty"`
	IsActive     bool   `json:"isActive"`
}

type boAIImageConfigSetRequest struct {
	ProviderSlug *string `json:"providerSlug,omitempty"`
	// APIKey: nil or empty string = keep the stored key. Any non-empty value replaces it.
	APIKey       *string `json:"apiKey,omitempty"`
	T2IModelSlug *string `json:"t2iModelSlug,omitempty"`
	I2IModelSlug *string `json:"i2iModelSlug,omitempty"`
	IsActive     *bool   `json:"isActive,omitempty"`
}

func maskAPIKey(key string) string {
	k := strings.TrimSpace(key)
	if k == "" {
		return ""
	}
	if len(k) <= 4 {
		return "••••"
	}
	return "••••" + k[len(k)-4:]
}

// --- catalog ---

func (s *Server) handleBOAIImageProvidersGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := boAuthFromContext(r.Context()); !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}
	ctx := r.Context()

	providers := []boAIImageProvider{}
	pr, err := s.db.QueryContext(ctx, `SELECT slug, label, base_url, COALESCE(docs_url,'') FROM ai_image_providers WHERE active = 1 ORDER BY sort, label`)
	if err == nil {
		defer pr.Close()
		for pr.Next() {
			var p boAIImageProvider
			if err := pr.Scan(&p.Slug, &p.Label, &p.BaseURL, &p.DocsURL); err == nil {
				providers = append(providers, p)
			}
		}
	}

	models := []boAIImageModel{}
	mr, err := s.db.QueryContext(ctx, `SELECT provider_slug, slug, label, mode FROM ai_image_models WHERE active = 1 ORDER BY provider_slug, mode, sort, label`)
	if err == nil {
		defer mr.Close()
		for mr.Next() {
			var m boAIImageModel
			if err := mr.Scan(&m.ProviderSlug, &m.Slug, &m.Label, &m.Mode); err == nil {
				models = append(models, m)
			}
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"providers": providers,
		"models":    models,
	})
}

// --- config get (redacted) ---

func (s *Server) loadAIImageConfig(ctx context.Context, restaurantID int) (boAIImageConfig, string, error) {
	out := boAIImageConfig{ProviderSlug: "wavespeed"}
	var providerSlug, t2i, i2i sql.NullString
	var apiKey sql.NullString
	var isActiveInt int
	err := s.db.QueryRowContext(ctx, `
		SELECT provider_slug, api_key, t2i_model_slug, i2i_model_slug, is_active
		FROM ai_image_provider_config
		WHERE restaurant_id = ?
		LIMIT 1
	`, restaurantID).Scan(&providerSlug, &apiKey, &t2i, &i2i, &isActiveInt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, "", nil
		}
		return out, "", err
	}
	if providerSlug.Valid && strings.TrimSpace(providerSlug.String) != "" {
		out.ProviderSlug = strings.TrimSpace(providerSlug.String)
	}
	rawKey := ""
	if apiKey.Valid {
		rawKey = strings.TrimSpace(apiKey.String)
	}
	out.HasAPIKey = rawKey != ""
	out.APIKeyMask = maskAPIKey(rawKey)
	if t2i.Valid {
		out.T2IModelSlug = strings.TrimSpace(t2i.String)
	}
	if i2i.Valid {
		out.I2IModelSlug = strings.TrimSpace(i2i.String)
	}
	out.IsActive = isActiveInt != 0
	return out, rawKey, nil
}

// aiImageConfigValid reports whether the AI image config for a restaurant is
// fully usable for image enhancement: activation ON + API key present + an
// image-to-image (edit) model selected. (Comida enhancement is image-to-image.)
func (s *Server) aiImageConfigValid(ctx context.Context, restaurantID int) bool {
	cfg, rawKey, err := s.loadAIImageConfig(ctx, restaurantID)
	if err != nil {
		return false
	}
	if !cfg.IsActive {
		return false
	}
	if strings.TrimSpace(rawKey) == "" {
		return false
	}
	if strings.TrimSpace(cfg.I2IModelSlug) == "" {
		return false
	}
	return true
}

// handleBOAIImageStatus is a lightweight, non-root endpoint used by the comida
// pages to decide whether to offer the "Mejorar con IA" advisor. Returns
// { success: true, valid: bool }.
func (s *Server) handleBOAIImageStatus(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"valid":   s.aiImageConfigValid(r.Context(), a.ActiveRestaurantID),
	})
}

func (s *Server) handleBOAIImageConfigGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}
	cfg, _, err := s.loadAIImageConfig(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error cargando configuracion"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "config": cfg})
}

// --- config set (upsert; blank key keeps existing) ---

func (s *Server) handleBOAIImageConfigSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	var req boAIImageConfigSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "JSON invalido"})
		return
	}

	ctx := r.Context()
	current, currentKey, err := s.loadAIImageConfig(ctx, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error cargando configuracion"})
		return
	}

	providerSlug := current.ProviderSlug
	if req.ProviderSlug != nil && strings.TrimSpace(*req.ProviderSlug) != "" {
		providerSlug = strings.TrimSpace(*req.ProviderSlug)
	}

	// Blank/absent key = keep existing.
	apiKey := currentKey
	if req.APIKey != nil && strings.TrimSpace(*req.APIKey) != "" {
		apiKey = strings.TrimSpace(*req.APIKey)
	}

	t2i := current.T2IModelSlug
	if req.T2IModelSlug != nil {
		t2i = strings.TrimSpace(*req.T2IModelSlug)
	}
	i2i := current.I2IModelSlug
	if req.I2IModelSlug != nil {
		i2i = strings.TrimSpace(*req.I2IModelSlug)
	}
	isActive := current.IsActive
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	// Validate provider + models exist in catalog (defensive; ignore empties).
	if providerSlug != "" && !s.aiImageProviderExists(ctx, providerSlug) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Proveedor no valido"})
		return
	}
	if t2i != "" && !s.aiImageModelExists(ctx, providerSlug, t2i, "t2i") {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Modelo texto-a-imagen no valido"})
		return
	}
	if i2i != "" && !s.aiImageModelExists(ctx, providerSlug, i2i, "i2i") {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Modelo imagen-a-imagen no valido"})
		return
	}

	activeInt := 0
	if isActive {
		activeInt = 1
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO ai_image_provider_config
			(restaurant_id, provider_slug, api_key, t2i_model_slug, i2i_model_slug, is_active, updated_by_user_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			provider_slug = VALUES(provider_slug),
			api_key = VALUES(api_key),
			t2i_model_slug = VALUES(t2i_model_slug),
			i2i_model_slug = VALUES(i2i_model_slug),
			is_active = VALUES(is_active),
			updated_by_user_id = VALUES(updated_by_user_id)
	`, a.ActiveRestaurantID, providerSlug, nullIfEmpty(apiKey), nullIfEmpty(t2i), nullIfEmpty(i2i), activeInt, a.User.ID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error guardando configuracion"})
		return
	}

	cfg, _, err := s.loadAIImageConfig(ctx, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Guardado, pero error recargando"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "config": cfg})
}

func (s *Server) aiImageProviderExists(ctx context.Context, slug string) bool {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM ai_image_providers WHERE slug = ? AND active = 1 LIMIT 1`, slug).Scan(&one)
	return err == nil
}

func (s *Server) aiImageModelExists(ctx context.Context, providerSlug, modelSlug, mode string) bool {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM ai_image_models WHERE provider_slug = ? AND slug = ? AND mode = ? AND active = 1 LIMIT 1`, providerSlug, modelSlug, mode).Scan(&one)
	return err == nil
}

func nullIfEmpty(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

// resolveAIImageProvider returns the effective AI image provider settings for a
// restaurant: DB config (if active + key present) takes precedence, otherwise
// falls back to the env-configured WaveSpeed values.
type resolvedAIImageProvider struct {
	APIKey       string
	BaseURL      string
	T2IModelSlug string
	I2IModelSlug string
	FromDB       bool
}

func (s *Server) resolveAIImageProvider(ctx context.Context, restaurantID int) resolvedAIImageProvider {
	res := resolvedAIImageProvider{
		APIKey:  strings.TrimSpace(s.cfg.OpenAIAPIKey),
		BaseURL: s.waveSpeedBaseURL(),
	}
	cfg, rawKey, err := s.loadAIImageConfig(ctx, restaurantID)
	if err != nil {
		return res
	}
	// A saved key from the config page is authoritative. The is_active flag is a
	// soft on/off; treat a stored key as usable unless explicitly disabled with
	// no env fallback intended. Here we prefer the DB key whenever it is present.
	if strings.TrimSpace(rawKey) != "" {
		res.APIKey = strings.TrimSpace(rawKey)
		res.T2IModelSlug = cfg.T2IModelSlug
		res.I2IModelSlug = cfg.I2IModelSlug
		res.FromDB = true
		if base := s.aiImageProviderBaseURL(ctx, cfg.ProviderSlug); base != "" {
			res.BaseURL = base
		}
	}
	return res
}

func (s *Server) aiImageProviderBaseURL(ctx context.Context, slug string) string {
	var base sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT base_url FROM ai_image_providers WHERE slug = ? LIMIT 1`, slug).Scan(&base)
	if err != nil || !base.Valid {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(base.String), "/")
}

// --- per-request provider override (threads a DB-resolved API key through the
// shared AI request path without changing every function signature) ---

type aiProviderOverride struct {
	APIKey string
	// EditURL is the full image-to-image (edit) endpoint for the DB-selected
	// model, e.g. https://api.wavespeed.ai/api/v3/openai/gpt-image-2/edit.
	// Empty = fall back to the env-configured edit URL.
	EditURL string
}

type aiProviderOverrideCtxKeyType int

const aiProviderOverrideCtxKey aiProviderOverrideCtxKeyType = 1

func withAIProviderOverride(ctx context.Context, o aiProviderOverride) context.Context {
	return context.WithValue(ctx, aiProviderOverrideCtxKey, o)
}

func aiProviderOverrideFromContext(ctx context.Context) (aiProviderOverride, bool) {
	if ctx == nil {
		return aiProviderOverride{}, false
	}
	o, ok := ctx.Value(aiProviderOverrideCtxKey).(aiProviderOverride)
	return o, ok
}

// aiRequestAPIKey returns the API key to use for an outbound AI provider request:
// a context override (DB-resolved) if present, otherwise the env-configured key.
func (s *Server) aiRequestAPIKey(ctx context.Context) string {
	if o, ok := aiProviderOverrideFromContext(ctx); ok && strings.TrimSpace(o.APIKey) != "" {
		return strings.TrimSpace(o.APIKey)
	}
	return strings.TrimSpace(s.cfg.OpenAIAPIKey)
}

// aiRequestEditURL returns the image-to-image (edit) endpoint to use: a context
// override (DB-selected model) if present, otherwise the env-configured URL.
func (s *Server) aiRequestEditURL(ctx context.Context) string {
	if o, ok := aiProviderOverrideFromContext(ctx); ok && strings.TrimSpace(o.EditURL) != "" {
		return strings.TrimSpace(o.EditURL)
	}
	return s.openAIImageEditURL()
}

// aiImageEditURLForModel builds the WaveSpeed edit endpoint for a given base URL
// and model slug, e.g. base=https://api.wavespeed.ai, model=openai/gpt-image-2/edit
// -> https://api.wavespeed.ai/api/v3/openai/gpt-image-2/edit.
func aiImageEditURLForModel(baseURL, modelSlug string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	model := strings.Trim(strings.TrimSpace(modelSlug), "/")
	if base == "" || model == "" {
		return ""
	}
	return base + "/api/v3/" + model
}
