package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/vault"
)

// Coordination id: wa_bot_ai_providers_v1
//
// The WhatsApp bot picks a primary and a fallback model per restaurant, each
// "provider/model". Provider API keys (opencode-go, minimax, jev) are stored in
// restaurant_ai_provider_keys encrypted with VAULT_KEY and bound to
// restaurant+provider, so a ciphertext cannot be replayed on another tenant.

const (
	botProviderOpenCodeGo = "opencode-go"
	botProviderMiniMax    = "minimax"
	botProviderJev        = "jev"

	botDefaultPrimaryModel  = "opencode-go/deepseek-v4.1-flash"
	botDefaultFallbackModel = "minimax/MiniMax-M3"
)

// botAIProvider describes one LLM provider and its selectable models. Wire is
// the request dialect: "openai" (chat/completions) or "anthropic" (messages).
type botAIProvider struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	BaseURL string   `json:"-"`
	Wire    string   `json:"-"`
	Models  []string `json:"models"`
}

// botAIProviders is the model catalog shown in /app/config (IA tab). MiniMax
// lists the coding-plan models; opencode-go mirrors ~/mini-tui providers.json.
var botAIProviders = []botAIProvider{
	{
		ID: botProviderOpenCodeGo, Label: "OpenCode Go", BaseURL: "https://opencode.ai/zen/go/v1", Wire: "openai",
		Models: []string{
			"deepseek-v4.1-flash", "deepseek-v4-flash", "deepseek-flash", "deepseek-v4-pro",
			"glm-5.3", "glm-5.3-flash", "glm-5.2", "glm-5.1",
			"kimi-k3", "kimi-k2.7-code", "kimi-k2.6",
			"qwen3.8-max", "qwen3.8-flash", "qwen3.7-max", "qwen3.7-plus", "qwen3.6-plus",
			"minimax-m3", "minimax-m2.7", "minimax-m2.5",
			"mimo-v2.6-pro", "mimo-v2.6-flash", "mimo-v2.5-pro", "mimo-v2.5",
			"grok-4.7", "grok-4.6", "gpt-6-luna", "gpt-5.6-luna",
			"hy4-preview", "hy3", "longcat-2.0", "omen-alpha", "space-bunny-free",
		},
	},
	{
		ID: botProviderMiniMax, Label: "MiniMax (Coding Plan)", Wire: "anthropic",
		Models: []string{"MiniMax-M3", "MiniMax-M2.7", "MiniMax-M2.7-highspeed", "MiniMax-M2.5"},
	},
}

// botKeyProviders are the providers whose API key can be configured.
var botKeyProviders = []string{botProviderOpenCodeGo, botProviderMiniMax, botProviderJev}

func botFindProvider(id string) (botAIProvider, bool) {
	for _, p := range botAIProviders {
		if p.ID == id {
			return p, true
		}
	}
	return botAIProvider{}, false
}

// botSplitModelRef parses "provider/model". A bare model (legacy tenant config
// like "MiniMax-M3") is treated as a MiniMax model.
func botSplitModelRef(ref string) (provider, model string) {
	ref = strings.TrimSpace(ref)
	if i := strings.Index(ref, "/"); i > 0 {
		return ref[:i], ref[i+1:]
	}
	return botProviderMiniMax, ref
}

func botValidModelRef(ref string) bool {
	provider, model := botSplitModelRef(ref)
	p, ok := botFindProvider(provider)
	if !ok || model == "" {
		return false
	}
	for _, m := range p.Models {
		if m == model {
			return true
		}
	}
	return false
}

func botProviderKeyAAD(restaurantID int, provider string) string {
	return fmt.Sprintf("restaurant:%d:ai_provider_key:%s", restaurantID, provider)
}

// botProviderKey returns the decrypted API key of a provider for a restaurant.
// MiniMax falls back to the existing MiniMax config (DB, then env) so the
// current production key keeps working with no migration step.
func (s *Server) botProviderKey(ctx context.Context, restaurantID int, provider string) string {
	var enc string
	err := s.db.QueryRowContext(ctx, `SELECT api_key_encrypted FROM restaurant_ai_provider_keys WHERE restaurant_id = ? AND provider = ?`, restaurantID, provider).Scan(&enc)
	if err == nil && enc != "" {
		plain, derr := vault.DecryptBound(s.cfg.VaultKey, botProviderKeyAAD(restaurantID, provider), enc)
		if derr == nil && strings.TrimSpace(plain) != "" {
			return strings.TrimSpace(plain)
		}
		log.Printf("[bot] checkpoint wa_bot_ai_providers_v1 restaurant_id=%d provider=%s decrypt_failed", restaurantID, provider)
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) && !isSQLSchemaError(err) {
		log.Printf("[bot] checkpoint wa_bot_ai_providers_v1 restaurant_id=%d provider=%s load_error=%v", restaurantID, provider, err)
	}
	if provider == botProviderMiniMax {
		return s.resolveMiniMaxKey(ctx, restaurantID)
	}
	return ""
}

// botAIRouting is the resolved per-restaurant model routing.
type botAIRouting struct {
	PrimaryModel  string
	FallbackModel string
	Knowledge     []botKnowledgeChunk
}

func (s *Server) loadBotAIRouting(ctx context.Context, restaurantID int) botAIRouting {
	out := botAIRouting{PrimaryModel: botDefaultPrimaryModel, FallbackModel: botDefaultFallbackModel}
	var primary, fallback, knowledge sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT primary_model, fallback_model, knowledge_json FROM restaurant_bot_ai_config WHERE restaurant_id = ?`, restaurantID).Scan(&primary, &fallback, &knowledge)
	if err != nil {
		return out
	}
	if botValidModelRef(primary.String) {
		out.PrimaryModel = primary.String
	}
	if botValidModelRef(fallback.String) {
		out.FallbackModel = fallback.String
	}
	if strings.TrimSpace(knowledge.String) != "" {
		_ = json.Unmarshal([]byte(knowledge.String), &out.Knowledge)
	}
	return out
}

// ---------- backoffice endpoints (root-only IA tab) ----------

type botAIProviderStatus struct {
	Provider  string `json:"provider"`
	HasAPIKey bool   `json:"hasApiKey"`
	Mask      string `json:"mask"`
}

func (s *Server) botAIConfigPayload(ctx context.Context, restaurantID int) map[string]any {
	routing := s.loadBotAIRouting(ctx, restaurantID)
	keys := make([]botAIProviderStatus, 0, len(botKeyProviders))
	for _, p := range botKeyProviders {
		k := s.botProviderKey(ctx, restaurantID, p)
		keys = append(keys, botAIProviderStatus{Provider: p, HasAPIKey: k != "", Mask: maskAPIKey(k)})
	}
	knowledge := routing.Knowledge
	if len(knowledge) == 0 {
		knowledge = botDefaultKnowledge()
	}
	return map[string]any{
		"success":       true,
		"restaurantId":  restaurantID,
		"primaryModel":  routing.PrimaryModel,
		"fallbackModel": routing.FallbackModel,
		"providers":     botAIProviders,
		"keys":          keys,
		"knowledge":     knowledge,
	}
}

// GET /api/admin/bot/ai/{restaurantId}
func (s *Server) handleBOBotAIGet(w http.ResponseWriter, r *http.Request) {
	rid, ok := botSettingsRestaurantID(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "restaurantId inválido")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, s.botAIConfigPayload(r.Context(), rid))
}

type botAIConfigPutRequest struct {
	PrimaryModel  string              `json:"primaryModel"`
	FallbackModel string              `json:"fallbackModel"`
	Knowledge     []botKnowledgeChunk `json:"knowledge"`
	// APIKeys: provider -> new key. Blank/absent keeps the stored ciphertext.
	APIKeys map[string]string `json:"apiKeys"`
}

// PUT /api/admin/bot/ai/{restaurantId}
func (s *Server) handleBOBotAIPut(w http.ResponseWriter, r *http.Request) {
	rid, ok := botSettingsRestaurantID(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "restaurantId inválido")
		return
	}
	var req botAIConfigPutRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "JSON inválido"})
		return
	}
	if !botValidModelRef(req.PrimaryModel) || !botValidModelRef(req.FallbackModel) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Modelo no válido"})
		return
	}
	ctx := r.Context()
	for provider, key := range req.APIKeys {
		key = strings.TrimSpace(key)
		if key == "" || !botIsKeyProvider(provider) {
			continue
		}
		if strings.TrimSpace(s.cfg.VaultKey) == "" {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "VAULT_KEY no configurado"})
			return
		}
		enc, err := vault.EncryptBound(s.cfg.VaultKey, botProviderKeyAAD(rid, provider), key)
		if err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "No se pudo cifrar la clave"})
			return
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO restaurant_ai_provider_keys (restaurant_id, provider, api_key_encrypted) VALUES (?, ?, ?)
			ON DUPLICATE KEY UPDATE api_key_encrypted = VALUES(api_key_encrypted)`, rid, provider, enc); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error guardando la clave"})
			return
		}
		log.Printf("[bot] checkpoint wa_bot_ai_providers_v1 restaurant_id=%d provider=%s key_saved", rid, provider)
	}
	knowledge := botCleanKnowledge(req.Knowledge)
	rawKnowledge, _ := json.Marshal(knowledge)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO restaurant_bot_ai_config (restaurant_id, primary_model, fallback_model, knowledge_json) VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE primary_model = VALUES(primary_model), fallback_model = VALUES(fallback_model), knowledge_json = VALUES(knowledge_json)`,
		rid, req.PrimaryModel, req.FallbackModel, string(rawKnowledge)); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error guardando la configuración"})
		return
	}
	s.botKnowledge.invalidate(rid)
	httpx.WriteJSON(w, http.StatusOK, s.botAIConfigPayload(ctx, rid))
}

func botIsKeyProvider(p string) bool {
	for _, k := range botKeyProviders {
		if k == p {
			return true
		}
	}
	return false
}
