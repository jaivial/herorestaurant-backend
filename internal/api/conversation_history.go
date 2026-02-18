package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleGetConversationHistory(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   "Unknown restaurant",
		})
		return
	}

	senderNumber := strings.TrimSpace(r.URL.Query().Get("sender_number"))
	if senderNumber == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"error":   "sender_number parameter is required",
		})
		return
	}

	limit := 30
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	hours := 168
	if raw := strings.TrimSpace(r.URL.Query().Get("hours")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			hours = n
		}
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT
			id,
			message_type,
			message_role,
			message_content,
			created_at,
			conversation_session_id
		FROM conversation_messages
		WHERE restaurant_id = ?
		  AND sender_number = ?
		  AND created_at >= DATE_SUB(NOW(), INTERVAL ? HOUR)
		ORDER BY created_at ASC
		LIMIT ?
	`, restaurantID, senderNumber, hours, limit)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return
	}
	defer rows.Close()

	messages := []map[string]any{}
	var sessionID sql.NullString
	for rows.Next() {
		var (
			id        int64
			msgType   string
			role      string
			content   string
			createdAt time.Time
			sess      sql.NullString
		)
		if err := rows.Scan(&id, &msgType, &role, &content, &createdAt, &sess); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"error":   err.Error(),
			})
			return
		}
		messages = append(messages, map[string]any{
			"id":         id,
			"role":       role,
			"content":    content,
			"created_at": createdAt.Format("2006-01-02 15:04:05"),
		})
		sessionID = sess
	}

	// Current state (optional).
	var stateRaw sql.NullString
	var contextRaw sql.NullString
	_ = s.db.QueryRowContext(r.Context(), `
		SELECT conversation_state, context_data
		FROM conversation_states
		WHERE restaurant_id = ?
		  AND sender_number = ?
		LIMIT 1
	`, restaurantID, senderNumber).Scan(&stateRaw, &contextRaw)

	var contextData any = map[string]any{}
	if contextRaw.Valid && strings.TrimSpace(contextRaw.String) != "" {
		var decoded any
		if err := json.Unmarshal([]byte(contextRaw.String), &decoded); err == nil {
			contextData = decoded
		}
	}

	// Session info (optional).
	var sessionInfo any = nil
	if sessionID.Valid && strings.TrimSpace(sessionID.String) != "" {
		var (
			id              string
			status          string
			startedAt       time.Time
			lastActivityAt  time.Time
			messageCount    int
			aiResponseCount int
		)
		if err := s.db.QueryRowContext(r.Context(), `
			SELECT id, status, started_at, last_activity_at, message_count, ai_response_count
			FROM conversation_sessions
			WHERE restaurant_id = ?
			  AND id = ?
			LIMIT 1
		`, restaurantID, sessionID.String).Scan(&id, &status, &startedAt, &lastActivityAt, &messageCount, &aiResponseCount); err == nil {
			sessionInfo = map[string]any{
				"id":               id,
				"status":           status,
				"started_at":       startedAt.Format("2006-01-02 15:04:05"),
				"last_activity_at": lastActivityAt.Format("2006-01-02 15:04:05"),
				"message_count":    messageCount,
				"ai_response_count": aiResponseCount,
			}
		}
	}

	var sessionIDOut any = nil
	if sessionID.Valid && strings.TrimSpace(sessionID.String) != "" {
		sessionIDOut = sessionID.String
	}

	var stateOut any = nil
	if stateRaw.Valid && strings.TrimSpace(stateRaw.String) != "" {
		stateOut = stateRaw.String
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":           true,
		"sender_number":     senderNumber,
		"session_id":        sessionIDOut,
		"message_count":     len(messages),
		"time_window_hours": hours,
		"messages":          messages,
		"current_state":     stateOut,
		"context_data":      contextData,
		"session_info":      sessionInfo,
		"retrieved_at":      time.Now().Format("2006-01-02 15:04:05"),
	})
}

func (s *Server) handleSaveConversationMessage(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   "Unknown restaurant",
		})
		return
	}

	var input struct {
		SenderNumber   string `json:"sender_number"`
		MessageType    string `json:"message_type"`
		MessageContent string `json:"message_content"`
		MessageRole    string `json:"message_role"`
		MessageID      string `json:"message_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	senderNumber := strings.TrimSpace(input.SenderNumber)
	content := strings.TrimSpace(input.MessageContent)
	if senderNumber == "" || content == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"error":   "Missing required fields: sender_number and message_content",
		})
		return
	}

	msgType := strings.TrimSpace(input.MessageType)
	if msgType == "" {
		msgType = "user"
	}
	if msgType == "assistant" {
		msgType = "ai"
	}
	role := strings.TrimSpace(input.MessageRole)
	if role == "" {
		if msgType == "user" {
			role = "user"
		} else {
			role = "assistant"
		}
	}

	// Get or create active session.
	var sessionID string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id
		FROM conversation_sessions
		WHERE restaurant_id = ?
		  AND sender_number = ?
		  AND status = 'active'
		ORDER BY last_activity_at DESC
		LIMIT 1
	`, restaurantID, senderNumber).Scan(&sessionID)
	if err != nil && err != sql.ErrNoRows {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	if err == nil {
		aiInc := 0
		if msgType == "ai" {
			aiInc = 1
		}
		_, _ = s.db.ExecContext(r.Context(), `
			UPDATE conversation_sessions
			SET last_activity_at = NOW(),
				message_count = message_count + 1,
				ai_response_count = ai_response_count + ?
			WHERE restaurant_id = ?
			  AND id = ?
		`, aiInc, restaurantID, sessionID)
	} else {
		sessionID = "sess_" + strconv.Itoa(restaurantID) + "_" + senderNumber + "_" + strconv.FormatInt(time.Now().Unix(), 10)
		_, err := s.db.ExecContext(r.Context(), `
			INSERT INTO conversation_sessions (restaurant_id, id, sender_number, message_count)
			VALUES (?, ?, ?, 1)
		`, restaurantID, sessionID, senderNumber)
		if err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"error":   err.Error(),
			})
			return
		}
	}

	// Save message.
	res, err := s.db.ExecContext(r.Context(), `
		INSERT INTO conversation_messages
			(restaurant_id, sender_number, conversation_session_id, message_type, message_role, message_content, message_id, processed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, NOW())
	`, restaurantID, senderNumber, sessionID, msgType, role, content, strings.TrimSpace(input.MessageID))
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return
	}
	insertedID, _ := res.LastInsertId()

	// Link session to conversation_states if present.
	_, _ = s.db.ExecContext(r.Context(), `
		UPDATE conversation_states
		SET conversation_session_id = ?,
			message_count = message_count + 1,
			last_message_at = NOW()
		WHERE restaurant_id = ?
		  AND sender_number = ?
	`, sessionID, restaurantID, senderNumber)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"message_id":   insertedID,
		"session_id":   sessionID,
		"message_type": msgType,
		"timestamp":    time.Now().Format("2006-01-02 15:04:05"),
	})
}
