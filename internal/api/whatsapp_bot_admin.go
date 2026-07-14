package api

import (
	"encoding/json"
	"net/http"

	"preactvillacarmen/internal/httpx"
)

// handleBOBotConfigGet returns the per-restaurant bot personalization config.
// GET /api/admin/bot/config
func (s *Server) handleBOBotConfigGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	cfg := s.loadBotTenantConfig(r.Context(), a.ActiveRestaurantID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"config":  cfg,
	})
}

// handleBOBotConfigPut stores the per-restaurant bot personalization config.
// PUT /api/admin/bot/config
func (s *Server) handleBOBotConfigPut(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var cfg botTenantConfig
	if err := readJSONBody(r, &cfg); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "JSON inválido",
		})
		return
	}
	if cfg.LanguageDefault != "es" && cfg.LanguageDefault != "en" {
		cfg.LanguageDefault = "es"
	}

	raw, _ := json.Marshal(cfg)
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO whatsapp_bot_config (restaurant_id, config_json)
		VALUES (?, ?)
		ON DUPLICATE KEY UPDATE config_json = VALUES(config_json)
	`, a.ActiveRestaurantID, string(raw))
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error guardando la configuración",
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"config":  cfg,
	})
}
