package api

import (
	"encoding/json"
	"net/http"

	"preactvillacarmen/internal/httpx"
)

// Backoffice surface for the ChatGPT plugin credentials, consumed by the
// /app/config?content=ia panel. The tokens themselves are bearer secrets, so
// this file never returns a digest: the raw value is handed back exactly once,
// at issue time, and the listing only ever exposes metadata.
//
// Coordination id: chatgpt_plugin_v1

// chatgptPluginTokenDTO is the safe-to-cache shape of a token row. The digest
// is deliberately absent, so neither this endpoint nor the UI can leak it.
type chatgptPluginTokenDTO struct {
	ID           int64  `json:"id"`
	Label        string `json:"label"`
	RestaurantID int    `json:"restaurant_id"`
	CreatedAt    string `json:"created_at"`
	LastUsedAt   string `json:"last_used_at,omitempty"`
	RevokedAt    string `json:"revoked_at,omitempty"`
	Active       bool   `json:"active"`
}

func (s *Server) handleBOChatGPTPluginTokensGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}
	if s.db == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error cargando los tokens"})
		return
	}
	// Scoped to the caller's own credentials on the active restaurant: a root
	// user must not be able to mint or inspect another user's tokens.
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, COALESCE(label, ''), restaurant_id,
		       DATE_FORMAT(created_at, '%Y-%m-%d %H:%i'),
		       COALESCE(DATE_FORMAT(last_used_at, '%Y-%m-%d %H:%i'), ''),
		       COALESCE(DATE_FORMAT(revoked_at, '%Y-%m-%d %H:%i'), '')
		FROM chatgpt_plugin_tokens
		WHERE user_id = ? AND restaurant_id = ?
		ORDER BY id DESC
		LIMIT 200
	`, a.User.ID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error cargando los tokens"})
		return
	}
	defer rows.Close()

	tokens := []chatgptPluginTokenDTO{}
	for rows.Next() {
		var t chatgptPluginTokenDTO
		if err := rows.Scan(&t.ID, &t.Label, &t.RestaurantID, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error cargando los tokens"})
			return
		}
		t.Active = t.RevokedAt == ""
		tokens = append(tokens, t)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":       true,
		"tokens":        tokens,
		"manifest_url":  s.chatgptPluginBaseURL(r) + "/.well-known/ai-plugin.json",
		"user_id":       a.User.ID,
		"restaurant_id": a.ActiveRestaurantID,
		"user_email":    a.User.Email,
		"role":          a.Role,
	})
}

func (s *Server) handleBOChatGPTPluginTokenIssue(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}
	var in struct {
		Label string `json:"label"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in)

	// Always pinned to the caller's own user and active restaurant, regardless
	// of what the request body asks for, so the panel cannot issue a credential
	// with more authority than the person clicking the button.
	token, err := s.IssueChatGPTPluginToken(r.Context(), a.User.ID, a.ActiveRestaurantID, in.Label)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": err.Error()})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"token":    token,
		"message":  "Token creado. Copialo ahora: no se volvera a mostrar.",
		"manifest": s.chatgptPluginBaseURL(r) + "/.well-known/ai-plugin.json",
	})
}

func (s *Server) handleBOChatGPTPluginTokenRevoke(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}
	n, err := s.RevokeChatGPTPluginTokens(r.Context(), a.User.ID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error revocando los tokens"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "revoked": n})
}
