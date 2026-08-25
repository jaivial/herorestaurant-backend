package api

import (
	"context"
	"log"
)

// botLoadHistory returns the complete persisted WhatsApp transcript for this
// restaurant/customer from SQLite. There is intentionally no arbitrary message
// count limit: SQLite is the source of conversational context for the agent.
func (s *Server) botLoadHistory(ctx context.Context, restaurantID int, userPhone string) []botMessage {
	if s.botConversation == nil {
		return nil
	}
	history, err := s.botConversation.History(ctx, restaurantID, userPhone)
	if err != nil {
		log.Printf("[bot] load SQLite conversation: %v", err)
		return nil
	}
	return history
}

// botRecordConversationMessage records exactly what the customer or restaurant
// sent on WhatsApp. It is best-effort so an observability write cannot prevent
// delivery, while errors remain visible in production logs.
func (s *Server) botRecordConversationMessage(ctx context.Context, restaurantID int, userPhone, role, content, toolName, source string) {
	if s.botConversation == nil || content == "" {
		return
	}
	if err := s.botConversation.Append(ctx, restaurantID, userPhone, role, content, toolName, source); err != nil {
		log.Printf("[bot] save SQLite conversation: %v", err)
	}
}

// botTouchSession keeps the existing MySQL session metadata used by the
// backoffice (display name / last activity). Conversation content itself lives
// exclusively in SQLite.
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
