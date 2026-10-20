package api

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"
)

// Coordination id: wa_bot_audio_reaction_v1
//
// Emoji reactions on received voice notes: the customer sees right away that
// the audio arrived (and later whether it was understood). Best effort and
// Evolution only: a failed reaction never affects the conversation turn.
const (
	botReactReceived = "🎧"
	botReactDone     = "✅"
	botReactFailed   = "❓"
)

func (s *Server) botReactToMessage(restaurantID int, msg botWebhookMessage, emoji string) {
	if strings.TrimSpace(msg.MessageID) == "" || msg.FromMe || msg.Ignored {
		return
	}
	jid := strings.TrimSpace(msg.ChatJID)
	if jid == "" {
		sender := strings.TrimSpace(msg.Sender)
		if sender == "" {
			return
		}
		if strings.Contains(sender, "@") {
			jid = sender
		} else {
			jid = sender + "@s.whatsapp.net"
		}
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		gw, ok := s.botGatewayFor(ctx, restaurantID)
		if !ok {
			return
		}
		evo, ok := gw.(*evolutionGateway)
		if !ok {
			return
		}
		key := map[string]any{"remoteJid": jid, "fromMe": false, "id": msg.MessageID}
		if msg.IsGroup && msg.ParticipantJID != "" {
			key["participant"] = msg.ParticipantJID
		}
		_, code, err := evo.request(ctx, http.MethodPost, evo.msgPath("sendReaction"), map[string]any{"key": key, "reaction": emoji})
		if err != nil || code < 200 || code >= 300 {
			log.Printf("[bot] checkpoint wa_bot_audio_reaction_v1 restaurant_id=%d emoji=%s failed code=%d err=%v", restaurantID, emoji, code, err)
			return
		}
		log.Printf("[bot] checkpoint wa_bot_audio_reaction_v1 restaurant_id=%d emoji=%s sent", restaurantID, emoji)
	}()
}
