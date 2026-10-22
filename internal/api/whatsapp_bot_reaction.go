package api

import (
	"context"
	"encoding/json"
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
		// wa_bot_group_reaction_lid_v1: WhatsApp keys the group message by the
		// raw participant (which may be an opaque LID, …:59@lid), so the reaction
		// must use that same value — a phone-form JID is silently dropped.
		// Coordination id: wa_bot_group_reaction_lid_v1
		participant, variant := "", "none"
		if msg.IsGroup && msg.ParticipantRawJID != "" {
			key["participant"] = msg.ParticipantRawJID
			participant, variant = msg.ParticipantRawJID, "raw_lid"
		} else if msg.IsGroup && msg.ParticipantJID != "" {
			key["participant"] = msg.ParticipantJID
			participant, variant = msg.ParticipantJID, "phone_fallback"
		}
		// Coordination id: wa_bot_reaction_send_key_v1 - the key about to be
		// sent is logged verbatim so a SERVER_ACK without a visible reaction
		// can be compared against the inbound key of the same message.
		log.Printf("[bot] checkpoint wa_bot_reaction_send_key_v1 restaurant_id=%d remote_jid=%s from_me=false id=%s participant_variant=%s participant=%q emoji=%s", restaurantID, jid, msg.MessageID, variant, participant, emoji)
		_, code, err := evo.request(ctx, http.MethodPost, evo.msgPath("sendReaction"), map[string]any{"key": key, "reaction": emoji})
		if err != nil || code < 200 || code >= 300 {
			log.Printf("[bot] checkpoint wa_bot_audio_reaction_v1 restaurant_id=%d emoji=%s failed code=%d err=%v", restaurantID, emoji, code, err)
			return
		}
		log.Printf("[bot] checkpoint wa_bot_audio_reaction_v1 restaurant_id=%d emoji=%s sent", restaurantID, emoji)
	}()
}

// Coordination id: wa_bot_reaction_feedback_v1
//
// Content-aware INSTANT reaction on every accepted message: the user sees
// right away that the bot understood the kind of message sent (a booking, a
// question, a menu ask, an allergy warning...). It is a zero-latency
// heuristic over the text and the media kind — never an LLM call — so the
// reaction lands before any work starts. Voice notes keep their own
// lifecycle reactions (received/done/failed).
func botContentEmoji(msg botWebhookMessage) string {
	if msg.IsAudio {
		return botReactReceived
	}
	t := strings.ToLower(msg.Text)
	switch {
	case msg.ImageB64 != "" || strings.Contains(msg.MediaKind, "imagen"):
		return "👀"
	case msg.DocumentB64 != "" || strings.Contains(msg.MediaKind, "documento"):
		return "📄"
	case strings.Contains(msg.MediaKind, "vídeo") || strings.Contains(msg.MediaKind, "video"):
		return "🎬"
	case strings.Contains(msg.MediaKind, "sticker"):
		return "😄"
	case strings.Contains(t, "alerg") || strings.Contains(t, "intoleran"):
		return "⚠️"
	case strings.Contains(t, "cancel"):
		return "🔄"
	case strings.Contains(t, "reserv") || strings.Contains(t, "mesa") || strings.Contains(t, "cita"):
		return "📅"
	case strings.Contains(t, "gracias"):
		return "🙏"
	case strings.Contains(t, "hola") || strings.Contains(t, "buenos días") || strings.Contains(t, "buenas"):
		return "👋"
	case strings.Contains(t, "carta") || strings.Contains(t, "menú") || strings.Contains(t, "menu") || strings.Contains(t, "arroz") || strings.Contains(t, "plato") || strings.Contains(t, "vino") || strings.Contains(t, "precio"):
		return "🍽️"
	case strings.Contains(t, "factura"):
		return "🧾"
	case strings.Contains(t, "horario") || strings.Contains(t, "abre") || strings.Contains(t, "cierra"):
		return "🕒"
	case strings.Contains(t, "cumple") || strings.Contains(t, "fiesta") || strings.Contains(t, "evento") || strings.Contains(t, "celebra"):
		return "🎉"
	case strings.Contains(t, "sí") || strings.Contains(t, "vale") || strings.Contains(t, "perfecto") || strings.Contains(t, "de acuerdo"):
		return "👌"
	case strings.Contains(t, "?") || strings.Contains(t, "¿"):
		return "🤔"
	case strings.Contains(t, "problema") || strings.Contains(t, "queja") || strings.Contains(t, " mal"):
		return "😟"
	}
	return "👍"
}

// Coordination id: wa_bot_inbound_reaction_key_v1
//
// botLogInboundReactionKey logs the reaction target key exactly as the
// provider's phone sent it, for reactions the backend discards as
// non-conversational (the SERVER_ACK arrives but no reaction is ever
// rendered). It fires only when the webhook body actually carries a
// reactionMessage with a key, so it is safe to call at a choke point that
// every message passes. restaurant_id=0 is logged when the tenant is not
// resolved yet, matching the existing checkpoint style.
type botReactionKeyLog struct {
	Key struct {
		RemoteJid   string `json:"remoteJid"`
		FromMe      bool   `json:"fromMe"`
		ID          string `json:"id"`
		Participant string `json:"participant"`
	} `json:"key"`
	Text string `json:"text"`
}

func botLogInboundReactionKey(restaurantID int, body []byte) {
	var env struct {
		Message botReactionKeyLog `json:"message"`
	}
	if err := json.Unmarshal(body, &env); err != nil || env.Message.Key.ID == "" {
		return
	}
	log.Printf("[bot] checkpoint wa_bot_inbound_reaction_key_v1 restaurant_id=%d remote_jid=%s from_me=%t id=%s participant=%q emoji=%q",
		restaurantID, env.Message.Key.RemoteJid, env.Message.Key.FromMe, env.Message.Key.ID, env.Message.Key.Participant, env.Message.Text)
}
