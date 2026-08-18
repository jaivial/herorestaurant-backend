package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/vault"
)

// ---------- shared MiniMax resolve helpers ----------

// minimaxSettings is the decrypted, resolved MiniMax configuration.
type minimaxSettings struct {
	APIKey    string
	Model     string
	HasAPIKey bool
}

// minimaxStoreCache is a short-lived in-memory cache of the MiniMax config per
// restaurant, mirroring bunnyCredentialsCache. It avoids decrypting the vault
// on every single AI call.
type minimaxStoreCache struct {
	mu     sync.RWMutex
	byRID  map[int]minimaxSettings
	expiry time.Time
}

func newMiniMaxStoreCache() *minimaxStoreCache {
	return &minimaxStoreCache{byRID: make(map[int]minimaxSettings)}
}

// get returns cached settings if fresh (<= 30s), else a miss.
func (c *minimaxStoreCache) get(restaurantID int) (minimaxSettings, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if time.Since(c.expiry) > 30*time.Second {
		return minimaxSettings{}, false
	}
	v, ok := c.byRID[restaurantID]
	return v, ok
}

func (c *minimaxStoreCache) set(restaurantID int, v minimaxSettings) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byRID == nil {
		c.byRID = make(map[int]minimaxSettings)
	}
	c.byRID[restaurantID] = v
	c.expiry = time.Now()
}

func (c *minimaxStoreCache) invalidate(restaurantID int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.byRID, restaurantID)
}

// loadMiniMaxConfig reads the row from restaurant_minimax_config and decrypts
// the API key. Returns an empty config (not an error) when no row exists.
func (s *Server) loadMiniMaxConfig(ctx context.Context, restaurantID int) (minimaxSettings, error) {
	if s.cfg.VaultToken == "" {
		return minimaxSettings{}, errors.New("VAULT_TOKEN not configured")
	}
	var encrypted sql.NullString
	var model sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT api_key_encrypted, model FROM restaurant_minimax_config WHERE restaurant_id = ?`,
		restaurantID,
	).Scan(&encrypted, &model)
	if errors.Is(err, sql.ErrNoRows) {
		return minimaxSettings{}, nil
	}
	if err != nil {
		return minimaxSettings{}, err
	}

	out := minimaxSettings{Model: model.String}
	if encrypted.Valid && encrypted.String != "" {
		plain, derr := vault.Decrypt(s.cfg.VaultToken, encrypted.String)
		if derr != nil {
			// Wrong / rotated VAULT_TOKEN: log loudly, degrade to env fallback.
			log.Printf("[minimax-config] restaurant=%d decrypt error: %v", restaurantID, derr)
			return minimaxSettings{Model: model.String}, nil
		}
		out.APIKey = strings.TrimSpace(plain)
		out.HasAPIKey = out.APIKey != ""
	}
	return out, nil
}

// resolvedMiniMax returns the MiniMax key+model for a restaurant, preferring
// the per-restaurant DB config and falling back to global env vars (legacy).
func (s *Server) resolvedMiniMax(ctx context.Context, restaurantID int) minimaxSettings {
	if restaurantID > 0 {
		if cached, ok := s.minimaxStoreCache.get(restaurantID); ok {
			return cached
		}
		cfg, err := s.loadMiniMaxConfig(ctx, restaurantID)
		if err == nil && cfg.HasAPIKey {
			s.minimaxStoreCache.set(restaurantID, cfg)
			return cfg
		}
		// No DB row or decrypt failure: fall back to env.
		if err != nil {
			log.Printf("[minimax-config] restaurant=%d load error: %v (falling back to env)", restaurantID, err)
		}
	}
	return minimaxSettings{
		APIKey:    strings.TrimSpace(s.cfg.MiniMaxAPIKey),
		Model:     strings.TrimSpace(s.cfg.MiniMaxModel),
		HasAPIKey: strings.TrimSpace(s.cfg.MiniMaxAPIKey) != "",
	}
}

// resolveMiniMaxKey returns the API key for a restaurant (DB first, env fallback).
func (s *Server) resolveMiniMaxKey(ctx context.Context, restaurantID int) string {
	return s.resolvedMiniMax(ctx, restaurantID).APIKey
}

// resolveMiniMaxModel returns the model for a restaurant (DB first, env fallback).
func (s *Server) resolveMiniMaxModel(ctx context.Context, restaurantID int) string {
	m := s.resolvedMiniMax(ctx, restaurantID).Model
	if m == "" {
		m = "MiniMax-M3"
	}
	return m
}

// hasMiniMaxConfig reports whether a restaurant has a DB-stored MiniMax API key.
func (s *Server) hasMiniMaxConfig(ctx context.Context, restaurantID int) bool {
	if restaurantID <= 0 {
		return false
	}
	cfg, err := s.loadMiniMaxConfig(ctx, restaurantID)
	return err == nil && cfg.HasAPIKey
}

// ---------- admin handlers ----------

// minimaxConfigDTO is what the backoffice sees: never the raw key.
type minimaxConfigDTO struct {
	HasAPIKey bool   `json:"has_api_key"`
	Model     string `json:"model"`
}

type minimaxConfigSetRequest struct {
	APIKey string `json:"api_key"`
	Model  string `json:"model"`
}

func (s *Server) handleBOMiniMaxConfigGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}
	cfg, err := s.loadMiniMaxConfig(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error cargando configuración"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "config": minimaxConfigDTO{
		HasAPIKey: cfg.HasAPIKey,
		Model:     cfg.Model,
	}})
}

func (s *Server) handleBOMiniMaxConfigSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	var req minimaxConfigSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "JSON inválido"})
		return
	}
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.Model = strings.TrimSpace(req.Model)

	var encrypted sql.NullString
	if req.APIKey != "" {
		if s.cfg.VaultToken == "" {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "VAULT_TOKEN no configurado"})
			return
		}
		enc, e := vault.Encrypt(s.cfg.VaultToken, req.APIKey)
		if e != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error encriptando API key"})
			return
		}
		encrypted = sql.NullString{String: enc, Valid: true}
	} else {
		// No key supplied: keep the existing one (model-only update).
		existing, lerr := s.loadMiniMaxConfig(r.Context(), a.ActiveRestaurantID)
		if lerr != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error cargando configuración"})
			return
		}
		if existing.HasAPIKey {
			enc, e := vault.Encrypt(s.cfg.VaultToken, existing.APIKey)
			if e != nil {
				httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error encriptando API key"})
				return
			}
			encrypted = sql.NullString{String: enc, Valid: true}
		} else {
			encrypted = sql.NullString{}
		}
	}

	var err error
	if !encrypted.Valid && req.Model == "" {
		_, err = s.db.ExecContext(r.Context(),
			`DELETE FROM restaurant_minimax_config WHERE restaurant_id = ?`, a.ActiveRestaurantID)
	} else {
		_, err = s.db.ExecContext(r.Context(),
			`INSERT INTO restaurant_minimax_config (restaurant_id, api_key_encrypted, model)
			 VALUES (?, ?, ?)
			 ON DUPLICATE KEY UPDATE api_key_encrypted = VALUES(api_key_encrypted), model = VALUES(model)`,
			a.ActiveRestaurantID, encrypted, req.Model)
	}
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error guardando configuración"})
		return
	}
	s.minimaxStoreCache.invalidate(a.ActiveRestaurantID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "config": minimaxConfigDTO{
		HasAPIKey: req.APIKey != "" || (encrypted.Valid && encrypted.String != ""),
		Model:     req.Model,
	}})
}
