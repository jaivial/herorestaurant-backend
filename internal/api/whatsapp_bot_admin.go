package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

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

func botSettingsRestaurantID(r *http.Request) (int, bool) {
	rid, err := strconv.Atoi(chi.URLParam(r, "restaurantId"))
	if err != nil || rid <= 0 {
		return 0, false
	}
	return rid, true
}

// botDefaultModel returns the model used when no per-tenant override is set.
func (s *Server) botDefaultModel() string {
	if m := strings.TrimSpace(s.cfg.BotModel); m != "" {
		return m
	}
	return strings.TrimSpace(s.cfg.MiniMaxModel)
}

// botSettingsResponse renders the shared GET/PUT payload: config (with
// contact_phone prefilled from restaurant_info when empty), prompt preview,
// editable default rules and the live multi-tenant data feeding the prompt.
func (s *Server) botSettingsResponse(r *http.Request, rid int, cfg botTenantConfig) map[string]any {
	data := s.loadBotPromptData(r.Context(), rid, "Cliente (ejemplo)", "34600000000 (ejemplo)", cfg)

	// Prefill contact phone from restaurant_info.telefono when no override.
	if strings.TrimSpace(cfg.ContactPhone) == "" {
		cfg.ContactPhone = data.Phone
	}

	return map[string]any{
		"success":       true,
		"config":        cfg,
		"promptPreview": renderBotSystemPrompt(data),
		"defaultModel":  s.botDefaultModel(),
		"defaultRules":  botDefaultRules,
		"restaurant": map[string]any{
			"brandName":  data.BrandName,
			"phone":      data.Phone,
			"address":    data.Address,
			"email":      data.Email,
			"website":    data.Website,
			"menuUrl":    data.MenuURL,
			"riceTypes":  data.RiceTypes,
			"hours":      data.Hours,
			"dailyLimit": data.DailyLimit,
		},
	}
}

// handleBOBotSettingsGet returns the bot config plus a rendered system-prompt
// preview for an explicit restaurant id (root IA tab).
// GET /api/admin/bot/settings/{restaurantId}
func (s *Server) handleBOBotSettingsGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := boAuthFromContext(r.Context()); !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	rid, ok := botSettingsRestaurantID(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "restaurantId inválido")
		return
	}

	cfg := s.loadBotTenantConfig(r.Context(), rid)
	httpx.WriteJSON(w, http.StatusOK, s.botSettingsResponse(r, rid, cfg))
}

// handleBOBotSettingsPreview renders the system prompt for a draft config
// WITHOUT saving it (live preview while editing in the IA tab).
// POST /api/admin/bot/settings/{restaurantId}/preview
func (s *Server) handleBOBotSettingsPreview(w http.ResponseWriter, r *http.Request) {
	if _, ok := boAuthFromContext(r.Context()); !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	rid, ok := botSettingsRestaurantID(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "restaurantId inválido")
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
	if strings.TrimSpace(cfg.Rules) == strings.TrimSpace(botDefaultRules) {
		cfg.Rules = ""
	}

	httpx.WriteJSON(w, http.StatusOK, s.botSettingsResponse(r, rid, cfg))
}

// handleBOBotSettingsPut stores the bot config for an explicit restaurant id
// and returns the refreshed prompt preview.
// PUT /api/admin/bot/settings/{restaurantId}
func (s *Server) handleBOBotSettingsPut(w http.ResponseWriter, r *http.Request) {
	if _, ok := boAuthFromContext(r.Context()); !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	rid, ok := botSettingsRestaurantID(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "restaurantId inválido")
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
	cfg.Model = strings.TrimSpace(cfg.Model)
	// Rules matching the defaults verbatim are stored as empty so future
	// improvements to botDefaultRules reach this tenant automatically.
	if strings.TrimSpace(cfg.Rules) == strings.TrimSpace(botDefaultRules) {
		cfg.Rules = ""
	}

	raw, _ := json.Marshal(cfg)
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO whatsapp_bot_config (restaurant_id, config_json)
		VALUES (?, ?)
		ON DUPLICATE KEY UPDATE config_json = VALUES(config_json)
	`, rid, string(raw))
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error guardando la configuración",
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, s.botSettingsResponse(r, rid, cfg))
}
