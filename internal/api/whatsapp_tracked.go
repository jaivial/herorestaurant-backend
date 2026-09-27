package api

import (
	"context"
	"strings"
)

// sendWhatsAppTextTracked records an outbound text in the SQLite conversation
// only after the provider accepts it.
func (s *Server) sendWhatsAppTextTracked(ctx context.Context, restaurantID int, gw WhatsAppGateway, to, text, source string) error {
	if botModelAuthoredSources[source] {
		text = botWhatsAppFormat(text) // wa_bot_whatsapp_format_v1
	}
	// Links in plain text never reach the customer: send them as interactive
	// URL buttons instead (wa_link_buttons_v1). Group chats keep plain text.
	if !strings.HasSuffix(to, "@g.us") {
		if lb, ok := botSplitLinks(text); ok {
			return s.sendWhatsAppMenuTracked(ctx, restaurantID, gw, to, lb.Body, lb.Choices, source)
		}
	}
	if err := gw.SendText(ctx, to, text); err != nil {
		return err
	}
	s.botRecordConversationMessage(ctx, restaurantID, to, "assistant", text, "", source)
	return nil
}

// sendWhatsAppMenuTracked records the visible menu text after a successful send.
func (s *Server) sendWhatsAppMenuTracked(ctx context.Context, restaurantID int, gw WhatsAppGateway, to, text string, choices []string, source string) error {
	if err := gw.SendMenu(ctx, to, text, choices); err != nil {
		return err
	}
	s.botRecordConversationMessage(ctx, restaurantID, to, "assistant", text, "", source)
	return nil
}
