package api

import "context"

// sendWhatsAppTextTracked records an outbound text in the SQLite conversation
// only after the provider accepts it.
func (s *Server) sendWhatsAppTextTracked(ctx context.Context, restaurantID int, gw WhatsAppGateway, to, text, source string) error {
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
