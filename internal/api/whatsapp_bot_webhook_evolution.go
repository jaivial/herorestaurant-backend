package api

import (
	"context"
	"crypto/subtle"
	"io"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

// handleBotWebhookEvolution is the inbound webhook for Evolution API instances.
// POST /bot/webhook/evolution/{secret}
//
// Evolution has no HMAC signing by default, so the unguessable {secret} path
// segment (EVOLUTION_WEBHOOK_SECRET) authenticates the caller. Tenant routing
// is by the instance name carried in the payload -> provider_instance_id.
func (s *Server) handleBotWebhookEvolution(w http.ResponseWriter, r *http.Request) {
	secret := chi.URLParam(r, "secret")
	want := s.cfg.EvolutionWebhookSecret
	if want == "" || subtle.ConstantTimeCompare([]byte(secret), []byte(want)) != 1 {
		// 200 (not 401) so Evolution does not disable the webhook on failures.
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": false, "message": "unauthorized"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": false})
		return
	}

	gw := &evolutionGateway{s: s}

	// Connection lifecycle first (keeps the QR onboarding UI live).
	if ev, ok := gw.ParseConnectionEvent(body); ok {
		updated := s.handleEvolutionConnectionEvent(r.Context(), ev)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": updated, "connection": true})
		return
	}

	in, ok := gw.ParseInboundMessage(body)
	if !ok {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": false})
		return
	}

	restaurantID, ok := s.resolveBotRestaurantByProviderInstance(r.Context(), in.SessionRef)
	if !ok {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": false, "message": "unknown instance"})
		return
	}
	if in.FromMe {
		// Messages typed manually by restaurant staff are part of the customer
		// conversation too. Persist plain text so the bot sees that intervention
		// on the next customer turn, but never run the inbound bot pipeline (which
		// would otherwise answer our own outbound message).
		if in.Text != "" {
			s.botRecordConversationMessage(r.Context(), restaurantID, in.Sender, "assistant", in.Text, "", "manual_whatsapp")
			s.botTouchSession(r.Context(), restaurantID, in.Sender, in.PushName)
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": true, "manual": true})
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": false, "manual": true})
		return
	}

	msg := botWebhookMessage{
		Sender:        in.Sender,
		Text:          in.Text,
		PushName:      in.PushName,
		MessageID:     in.MessageID,
		FromMe:        in.FromMe,
		InstanceToken: in.SessionRef,
		IsAudio:       in.IsAudio,
	}
	s.processInboundBotMessage(w, r, restaurantID, msg)
}

// resolveBotRestaurantByProviderInstance maps an Evolution instance name to a
// restaurant with an active provisioned instance.
func (s *Server) resolveBotRestaurantByProviderInstance(ctx context.Context, instanceName string) (int, bool) {
	if instanceName == "" {
		return 0, false
	}
	var rid int
	err := s.db.QueryRowContext(ctx, `
		SELECT restaurant_id FROM restaurant_uazapi_instances
		WHERE provider_instance_id = ? AND is_active = 1
		LIMIT 1
	`, instanceName).Scan(&rid)
	if err != nil {
		return 0, false
	}
	return rid, true
}

// handleEvolutionConnectionEvent updates the provisioning row from an Evolution
// connection.update / qrcode.updated event so the onboarding UI reflects live
// state without polling.
func (s *Server) handleEvolutionConnectionEvent(ctx context.Context, ev waConnEvent) bool {
	restaurantID, ok := s.resolveBotRestaurantByProviderInstance(ctx, ev.SessionRef)
	if !ok {
		return false
	}
	status := ev.Status
	if status == "" && (ev.QR != "" || ev.PairCode != "") {
		status = "pending"
	}
	if err := s.updateRestaurantUAZAPIInstanceRuntime(ctx, restaurantID, status, ev.ConnectedPhone, ev.QR, ev.PairCode); err != nil {
		log.Printf("[bot] restaurant=%d evolution connection update failed: %v", restaurantID, err)
		return false
	}
	if isUAZAPIConnected(status) {
		if rec, found, err := s.loadRestaurantUAZAPIInstance(ctx, restaurantID); err == nil && found {
			_ = s.syncRestaurantUAZAPIIntegration(ctx, restaurantID, rec.ServerBaseURL, rec.InstanceToken)
		}
	}
	s.broadcastWhatsAppConnection(ctx, restaurantID)
	return true
}
