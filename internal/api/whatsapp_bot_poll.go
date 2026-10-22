package api

import (
	"context"
	"encoding/json"
	"strings"
)

// Coordination id: wa_bot_poll_v1
//
// WhatsApp polls as a bot tool: when the bot needs a CHOICE (which main dish
// each guest wants, which schedule works, which option of a menu), it asks
// with a poll instead of free text: the answers arrive as real WhatsApp poll
// results the model can read on the next turn. Registered in the base catalog
// (customers and staff alike) and treated as a delivery tool, so tool grants
// never restrict it.

// botToolSendPoll sends a WhatsApp poll to the conversation that triggered
// the turn (the group, or the customer's chat).
func (s *Server) botToolSendPoll(ctx context.Context, restaurantID int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	var in struct {
		Question   string   `json:"question"`
		Options    []string `json:"options"`
		MaxChoices int      `json:"max_choices"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return botJSON(map[string]any{"error": "argumentos ilegibles"}), nil
	}
	in.Question = strings.TrimSpace(in.Question)
	opts := make([]string, 0, len(in.Options))
	seen := map[string]struct{}{}
	for _, o := range in.Options {
		v := strings.TrimSpace(o)
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		opts = append(opts, v)
	}
	if in.Question == "" || len(opts) < 2 || len(opts) > 10 {
		return botJSON(map[string]any{"error": "hacen falta question y entre 2 y 10 opciones"}), nil
	}
	gw, ok := s.botGatewayFor(ctx, restaurantID)
	if !ok {
		return botJSON(map[string]any{"error": "conexión de WhatsApp no disponible"}), nil
	}
	if err := gw.SendPoll(ctx, msg.Sender, in.Question, opts, in.MaxChoices); err != nil {
		botCheckpoint("wa_bot_poll_v1", "restaurant_id=%d error=%v", restaurantID, err)
		return botJSON(map[string]any{"error": "no se pudo enviar la encuesta: " + err.Error()}), nil
	}
	botCheckpoint("wa_bot_poll_v1", "restaurant_id=%d options=%d", restaurantID, len(opts))
	return botJSON(map[string]any{"sent": true}), nil
}
