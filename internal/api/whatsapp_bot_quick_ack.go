package api

import (
	"context"
	"strings"
	"time"
)

// Coordination id: wa_bot_quick_ack_v1
//
// The first feedback of this bot version in ALLOWED chats (privileged contacts
// and groups): the moment a turn is accepted, ONE short acknowledgement goes
// out ("Vale, voy a mirar lo de tu reserva…") in PARALLEL with the worker that
// executes the real task, so the group never stares at silence while the
// agent loop runs.
//
// No custom tools are involved: it is plain text produced by one tiny
// tool-less LLM call over the user's own message, with an instant heuristic
// fallback when the model is slow or fails. Everywhere else the bot is
// byte-for-byte unchanged.

// botQuickAck fires the acknowledgement without blocking the caller: the turn
// worker keeps executing in parallel.
func (s *Server) botQuickAck(restaurantID int, msg botWebhookMessage) {
	if !msg.PrivilegedGroup || msg.FromMe || msg.Ignored {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		text := s.botQuickAckText(ctx, restaurantID, msg)
		cancel()
		if text == "" {
			botCheckpoint("wa_bot_quick_ack_v1", "restaurant_id=%d message_id=%s decision=empty", restaurantID, msg.MessageID)
			return
		}
		// Text generation and delivery have separate budgets. Reusing the LLM
		// context meant the heuristic fallback after a timeout always tried to
		// send with an already-expired context and silently disappeared.
		sendCtx, sendCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer sendCancel()
		gw, ok := s.botGatewayFor(sendCtx, restaurantID)
		if !ok {
			botCheckpoint("wa_bot_quick_ack_v1", "restaurant_id=%d message_id=%s decision=no_gateway", restaurantID, msg.MessageID)
			return
		}
		if err := s.sendWhatsAppTextTracked(sendCtx, restaurantID, gw, msg.Sender, text, "quick_ack"); err != nil {
			botCheckpoint("wa_bot_quick_ack_v1", "restaurant_id=%d message_id=%s decision=send_failed class=%s", restaurantID, msg.MessageID, botErrClass(err))
			return
		}
		botCheckpoint("wa_bot_quick_ack_v1", "restaurant_id=%d sender=%s message_id=%s chars=%d decision=sent", restaurantID, msg.Sender, msg.MessageID, len(text))
	}()
}

// botQuickAckText builds the acknowledgement: one tool-less LLM call grounded
// on the message, falling back to the heuristic template (which is also the
// instant path for media messages with no text yet).
func (s *Server) botQuickAckText(ctx context.Context, restaurantID int, msg botWebhookMessage) string {
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return botQuickAckHeuristic("")
	}
	// Long operational requests (a whole menu/invoice) do not need a model to
	// acknowledge receipt. Keep the quick ack quick and avoid spending its
	// entire budget reasoning over thousands of characters.
	if len([]rune(text)) > 500 {
		return botQuickAckHeuristic(text)
	}
	system := "Eres el asistente de reservas de un restaurante por WhatsApp. Acaba de llegarte este mensaje y vas a empezar a atenderlo en unos segundos. " +
		"Escribe UNA sola frase de acuse muy breve (máximo 12 palabras) en el idioma del mensaje, tipo 'Vale, voy a mirar lo de tu reserva'. " +
		"Confirma que lo estás atendiendo: NO respondas la pregunta, NO des datos ni cifras, NO uses herramientas, sin listas ni markdown."
	out, err := s.botLLMCall(ctx, restaurantID, "", system, []botMessage{botUserText(text)}, nil)
	if err == nil {
		var b strings.Builder
		for _, blk := range out.Content {
			if blk.Type == "text" {
				b.WriteString(blk.Text)
			}
		}
		ack := strings.TrimSpace(b.String())
		ack = strings.Trim(ack, "\"'")
		if ack != "" && len([]rune(ack)) <= 80 {
			return ack
		}
	}
	return botQuickAckHeuristic(text)
}

// botQuickAckHeuristic is the zero-latency fallback, content-aware by intent
// keywords in Spanish (the bot's operating language). It also answers media
// turns with no text yet (the transcript/extraction arrives later).
func botQuickAckHeuristic(text string) string {
	t := strings.ToLower(text)
	if strings.TrimSpace(t) == "" {
		return "¡Vale! Lo reviso ahora mismo y te digo."
	}
	switch {
	case strings.Contains(t, "cancel"):
		return "Vale, miro lo de la cancelación ahora mismo."
	case strings.Contains(t, "reserv") || strings.Contains(t, "mesa") || strings.Contains(t, "cita"):
		return "¡Vale! Voy a mirar lo de tu reserva ahora mismo."
	case strings.Contains(t, "carta") || strings.Contains(t, "menú") || strings.Contains(t, "menu") || strings.Contains(t, "arroz") || strings.Contains(t, "plato") || strings.Contains(t, "precio") || strings.Contains(t, "vino"):
		return "¡Perfecto! Te miro eso de la carta ahora mismo."
	case strings.Contains(t, "factura") || strings.Contains(t, "factur"):
		return "Vale, reviso lo de la factura ahora mismo."
	case strings.Contains(t, "horario") || strings.Contains(t, "abre") || strings.Contains(t, "cierra"):
		return "¡Vale! Te miro el horario ahora mismo."
	case strings.Contains(t, "hola") || strings.Contains(t, "buenos días") || strings.Contains(t, "buenas"):
		return "¡Hola! Ya estoy con ello, un momento."
	case strings.Contains(t, "gracias"):
		return "¡A ti! Sigo con ello ahora mismo."
	}
	return "¡Vale! Lo miro ahora mismo y te digo."
}
