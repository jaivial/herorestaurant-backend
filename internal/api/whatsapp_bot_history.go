package api

import (
	"context"
	"log"
)

// botLoadHistory loads the recent conversation as alternating user/assistant
// text messages. Tool turns are not replayed (results are stale anyway); the
// text transcript keeps the model in context.
func (s *Server) botLoadHistory(ctx context.Context, restaurantID int, userPhone string) []botMessage {
	limit := s.cfg.BotHistoryLimit
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT role, content FROM (
			SELECT id, role, content
			FROM whatsapp_bot_messages
			WHERE restaurant_id = ? AND user_phone = ? AND role IN ('user','assistant')
			ORDER BY id DESC
			LIMIT ?
		) recent ORDER BY id ASC
	`, restaurantID, userPhone, limit)
	if err != nil {
		if !isSQLSchemaError(err) {
			log.Printf("[bot] load history: %v", err)
		}
		return nil
	}
	defer rows.Close()

	out := make([]botMessage, 0, limit)
	for rows.Next() {
		var role, content string
		if err := rows.Scan(&role, &content); err != nil {
			return out
		}
		if content == "" {
			continue
		}
		out = append(out, botMessage{Role: role, Content: []botBlock{{Type: "text", Text: content}}})
	}

	// The Messages API requires the first message to be from the user and
	// strict alternation is safest: collapse consecutive same-role turns.
	normalized := make([]botMessage, 0, len(out))
	for _, m := range out {
		if len(normalized) == 0 {
			if m.Role != "user" {
				continue
			}
			normalized = append(normalized, m)
			continue
		}
		last := &normalized[len(normalized)-1]
		if last.Role == m.Role {
			last.Content[0].Text += "\n" + m.Content[0].Text
			continue
		}
		normalized = append(normalized, m)
	}
	// History must end on an assistant turn so the new user message alternates.
	if len(normalized) > 0 && normalized[len(normalized)-1].Role == "user" {
		normalized = normalized[:len(normalized)-1]
	}
	return normalized
}

// botSaveMessage persists one transcript row. Best-effort: bot flow must not
// fail on logging problems.
func (s *Server) botSaveMessage(ctx context.Context, restaurantID int, userPhone string, role string, content string, toolName string) {
	if content == "" {
		return
	}
	var tool any
	if toolName != "" {
		tool = toolName
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO whatsapp_bot_messages (restaurant_id, user_phone, role, content, tool_name, created_at)
		VALUES (?, ?, ?, ?, ?, NOW(3))
	`, restaurantID, userPhone, role, content, tool)
	if err != nil && !isSQLSchemaError(err) {
		log.Printf("[bot] save message: %v", err)
	}
}

// botTouchSession upserts the session row.
func (s *Server) botTouchSession(ctx context.Context, restaurantID int, userPhone string, pushName string) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO whatsapp_bot_sessions (restaurant_id, user_phone, push_name, last_message_at)
		VALUES (?, ?, ?, NOW(3))
		ON DUPLICATE KEY UPDATE push_name = VALUES(push_name), last_message_at = NOW(3)
	`, restaurantID, userPhone, pushName)
	if err != nil && !isSQLSchemaError(err) {
		log.Printf("[bot] touch session: %v", err)
	}
}
