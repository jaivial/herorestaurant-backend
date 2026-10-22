package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
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
// botStoredPrivilegedGroupJIDs returns the privileged groups already
// configured for a restaurant, so a write by a non-root caller keeps them
// instead of dropping them.
// botStoredTenantConfig returns the whole stored config of a restaurant, so a
// partial write can be merged onto it instead of replacing it.
func (s *Server) botStoredTenantConfig(ctx context.Context, restaurantID int) botTenantConfig {
	var raw sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT config_json FROM whatsapp_bot_config WHERE restaurant_id = ? LIMIT 1
	`, restaurantID).Scan(&raw)
	if err != nil || !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return botTenantConfig{}
	}
	return parseBotTenantConfig(raw.String)
}

func (s *Server) botStoredPrivilegedGroupJIDs(ctx context.Context, restaurantID int) []string {
	var raw sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT config_json FROM whatsapp_bot_config WHERE restaurant_id = ? LIMIT 1
	`, restaurantID).Scan(&raw)
	if err != nil || !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil
	}
	return parseBotTenantConfig(raw.String).PrivilegedGroupJIDs
}

func (s *Server) handleBOBotConfigPut(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	// The body is read twice on purpose. The raw map tells us WHICH fields
	// the caller actually sent; the struct gives us the values. A field that
	// is NOT in the body is not being changed, it is simply not mentioned.
	//
	// This route is used by three callers with very different bodies: the
	// settings form (which sends everything), the bot tool
	// whatsapp_bot_config_update (which sends 8 of the 16 fields) and the
	// privileged-contacts route (which sends only the list). Treating an
	// absent field as "empty" made the second caller wipe the fields it
	// does not know about: the management group id, the contact name, the
	// same-day phone. The operator would open the panel, ask the bot to
	// change the tone, and lose the management group.
	bodyRaw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "JSON invalido",
		})
		return
	}
	sent := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(bodyRaw))) > 0 {
		if err := json.Unmarshal(bodyRaw, &sent); err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"message": "JSON invalido",
			})
			return
		}
	}

	// Start from what is stored, and apply only what the body carries.
	cfg := s.botStoredTenantConfig(r.Context(), a.ActiveRestaurantID)
	if len(strings.TrimSpace(string(bodyRaw))) > 0 {
		dec := json.NewDecoder(bytes.NewReader(bodyRaw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cfg); err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"message": "JSON invalido",
			})
			return
		}
	}
	if cfg.LanguageDefault != "es" && cfg.LanguageDefault != "en" {
		cfg.LanguageDefault = "es"
	}

	// The privileged groups decide where the bot answers with the whole
	// backoffice catalog and a root auth, so only a root may set them. This
	// route is open to any admin (role importance >= 90); without this guard
	// an admin could name a group they belong to and elevate themselves to
	// the privileged catalog. A non-root write keeps the value that is
	// already stored, so an admin editing the tone does not silently drop
	// the privileged groups either.
	//
	// The privileged CONTACTS decide the same thing for a one-to-one chat
	// (wa_bot_privileged_contacts_v1), so they are guarded exactly the same
	// way: otherwise an admin could name their own number here and get the
	// root catalog on their private chat, which is the same elevation the
	// group list would give.
	if a.Role != "root" {
		cfg.PrivilegedGroupJIDs = s.botStoredPrivilegedGroupJIDs(r.Context(), a.ActiveRestaurantID)
		cfg.PrivilegedContacts = s.botStoredPrivilegedContacts(r.Context(), a.ActiveRestaurantID)
		cfg.ToolGrants = s.botStoredTenantConfig(r.Context(), a.ActiveRestaurantID).ToolGrants
	} else {
		// A root write may CHANGE the privileged lists, but it may not DROP
		// them by accident. This route is a general settings form: a body that
		// simply omits the list (the operator edited the tone) would replace
		// the whole config_json and silently revoke the selection, which is
		// the opposite of what the operator asked.
		//
		// The dedicated route (bot/ai/{restaurantId}/privileged-contacts) is
		// the one that writes the selection, and it writes the whole list.
		// Here, an omitted list means "leave it as it was".
		if _, ok := sent["privileged_group_jids"]; !ok {
			cfg.PrivilegedGroupJIDs = s.botStoredPrivilegedGroupJIDs(r.Context(), a.ActiveRestaurantID)
		}
		if _, ok := sent["privileged_contacts"]; !ok {
			cfg.PrivilegedContacts = s.botStoredPrivilegedContacts(r.Context(), a.ActiveRestaurantID)
		}
		// wa_bot_tool_grants_v1: same rule for the per-chat tool grants.
		if _, ok := sent["tool_grants"]; !ok {
			cfg.ToolGrants = s.botStoredTenantConfig(r.Context(), a.ActiveRestaurantID).ToolGrants
		}
	}

	raw, _ := json.Marshal(cfg)
	if _, err := s.db.ExecContext(r.Context(), `
		INSERT INTO whatsapp_bot_config (restaurant_id, config_json)
		VALUES (?, ?)
		ON DUPLICATE KEY UPDATE config_json = VALUES(config_json)
	`, a.ActiveRestaurantID, string(raw)); err != nil {
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
	// Coordination ids: wa_bot_privileged_contacts_v1 / wa_bot_tool_grants_v1.
	// An omitted list means "leave it as it was" (same rule as
	// handleBOBotConfigPut): a settings write must never revoke who is
	// authorised nor their per-chat tool grants.
	stored := s.botStoredTenantConfig(r.Context(), rid)
	if len(cfg.PrivilegedGroupJIDs) == 0 {
		cfg.PrivilegedGroupJIDs = stored.PrivilegedGroupJIDs
	}
	if len(cfg.PrivilegedContacts) == 0 {
		cfg.PrivilegedContacts = stored.PrivilegedContacts
	}
	if len(cfg.ToolGrants) == 0 {
		cfg.ToolGrants = stored.ToolGrants
	}

	raw, _ := json.Marshal(cfg)
	if _, err := s.db.ExecContext(r.Context(), `
		INSERT INTO whatsapp_bot_config (restaurant_id, config_json)
		VALUES (?, ?)
		ON DUPLICATE KEY UPDATE config_json = VALUES(config_json)
	`, rid, string(raw)); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error guardando la configuración",
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, s.botSettingsResponse(r, rid, cfg))
}
