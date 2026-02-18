package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleGetConversationState(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	sender := strings.TrimSpace(r.URL.Query().Get("senderNumber"))
	if sender == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Missing required parameter: senderNumber",
		})
		return
	}

	var (
		stateRaw   sql.NullString
		contextRaw sql.NullString
		createdAt  sql.NullTime
		updatedAt  sql.NullTime
		expiresAt  sql.NullTime
	)
	err := s.db.QueryRowContext(r.Context(), `
		SELECT conversation_state, context_data, created_at, updated_at, expires_at
		FROM conversation_states
		WHERE restaurant_id = ?
		  AND sender_number = ?
		  AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY updated_at DESC
		LIMIT 1
	`, restaurantID, sender).Scan(&stateRaw, &contextRaw, &createdAt, &updatedAt, &expiresAt)
	if err == sql.ErrNoRows {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":     true,
			"hasState":    false,
			"state":       "idle",
			"contextData": nil,
			"message":     "No active conversation state found",
		})
		return
	}
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Database error occurred",
			"error":   err.Error(),
		})
		return
	}

	var contextData any = nil
	if contextRaw.Valid && strings.TrimSpace(contextRaw.String) != "" {
		var decoded any
		if err := json.Unmarshal([]byte(contextRaw.String), &decoded); err == nil {
			contextData = decoded
		}
	}

	formatTS := func(t sql.NullTime) any {
		if !t.Valid {
			return nil
		}
		return t.Time.Format("2006-01-02 15:04:05")
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"hasState":    true,
		"state":       stateRaw.String,
		"contextData": contextData,
		"createdAt":   formatTS(createdAt),
		"updatedAt":   formatTS(updatedAt),
		"expiresAt":   formatTS(expiresAt),
	})
}

func (s *Server) handleSaveConversationState(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	var input struct {
		SenderNumber     string `json:"senderNumber"`
		State            string `json:"state"`
		ContextData      any    `json:"contextData"`
		ExpiresInMinutes int    `json:"expiresInMinutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON: " + err.Error(),
		})
		return
	}

	sender := strings.TrimSpace(input.SenderNumber)
	state := strings.TrimSpace(input.State)
	if sender == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Missing required parameter: senderNumber",
		})
		return
	}
	if state == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Missing required parameter: state",
		})
		return
	}

	expiresMin := input.ExpiresInMinutes
	if expiresMin <= 0 {
		expiresMin = 30
	}
	expiresAt := time.Now().Add(time.Duration(expiresMin) * time.Minute).Format("2006-01-02 15:04:05")

	var contextJSON any = nil
	if input.ContextData != nil {
		b, _ := json.Marshal(input.ContextData)
		contextJSON = string(b)
	}

	var existingID int
	err := s.db.QueryRowContext(r.Context(), "SELECT id FROM conversation_states WHERE restaurant_id = ? AND sender_number = ? LIMIT 1", restaurantID, sender).Scan(&existingID)
	if err != nil && err != sql.ErrNoRows {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Database error occurred",
			"error":   err.Error(),
		})
		return
	}

	msg := "Conversation state created successfully"
	if err == nil {
		_, err = s.db.ExecContext(r.Context(), `
			UPDATE conversation_states
			SET conversation_state = ?,
			    context_data = ?,
			    expires_at = ?,
			    updated_at = NOW()
			WHERE restaurant_id = ?
			  AND sender_number = ?
		`, state, contextJSON, expiresAt, restaurantID, sender)
		msg = "Conversation state updated successfully"
	} else {
		_, err = s.db.ExecContext(r.Context(), `
			INSERT INTO conversation_states (restaurant_id, sender_number, conversation_state, context_data, expires_at)
			VALUES (?, ?, ?, ?, ?)
		`, restaurantID, sender, state, contextJSON, expiresAt)
	}
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Database error occurred",
			"error":   err.Error(),
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"message":   msg,
		"state":     state,
		"expiresAt": expiresAt,
	})
}
