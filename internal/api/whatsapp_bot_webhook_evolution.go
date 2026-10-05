package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

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

	// 12 MB: voice notes arrive inline as base64 (webhookBase64=true), which a
	// 1 MB cap truncated into invalid JSON. wa_bot_audio_transcription_v1
	body, err := io.ReadAll(io.LimitReader(r.Body, 12<<20))
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": false})
		return
	}

	// The gateway needs to know which JIDs address the bot so a group mention
	// can be recognised before the tenant is resolved (coordination id:
	// wa_bot_group_mention_v1). The database phone plus the provider's LID
	// mapping are cached for botOwnJIDsTTL, so this is a cache hit on all but
	// the first message after the TTL expires - it is not a provider round trip
	// per webhook.
	gw := &evolutionGateway{s: s, ownJIDs: s.botOwnJIDsByInstanceName(r.Context(), evoEnvelopeInstanceName(body))}

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

	// Group gate: the bot only answers inside its own management group and
	// only when mentioned. Everything else is dropped with a checkpoint log
	// (wa_bot_group_mention_v1) so a noisy group never reaches the LLM.
	if botGroupIsGroupJID(in.ChatJID) {
		tenant := s.loadBotTenantConfig(r.Context(), restaurantID)
		if !s.botGroupIsManagementGroup(r.Context(), restaurantID, in.ChatJID, tenant) {
			botGroupMessageAudit(restaurantID, in.ChatJID, in.ParticipantJID, "skipped_not_management_group")
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": false, "group": true, "reason": "not_management_group"})
			return
		}
		if !in.Mentioned {
			botGroupMessageAudit(restaurantID, in.ChatJID, in.ParticipantJID, "skipped_not_mentioned")
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": false, "group": true, "reason": "not_mentioned"})
			return
		}
		botGroupMessageAudit(restaurantID, in.ChatJID, in.ParticipantJID, "accepted")
	}

	if in.Ignored {
		log.Printf("[bot] checkpoint wa_bot_ignore_non_conversational_v1 restaurant_id=%d sender=%s", restaurantID, in.Sender)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": false, "ignored": true})
		return
	}
	if in.FromMe {
		// Messages typed manually by restaurant staff are part of the customer
		// conversation and hand it over to a human: record them and pause the
		// bot for this customer. Never run the inbound pipeline for them.
		manual := s.botHandleManualStaffMessage(r.Context(), restaurantID, in.Sender, in.MessageID, in.Text, in.MediaKind)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": manual, "manual": true})
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
		AudioB64:      in.AudioB64,
		// Group routing (wa_bot_group_mention_v1): Sender is the group JID,
		// so every tool, the transcript and the reply target the group.
		IsGroup:        botGroupIsGroupJID(in.ChatJID),
		ChatJID:        in.ChatJID,
		ParticipantJID: in.ParticipantJID,
		Mentioned:      in.Mentioned,
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
// state without polling. On any transition into the connected state it also
// flushes the outbox + reminder queues so messages parked while the link was
// down fire on the next scan.
func (s *Server) handleEvolutionConnectionEvent(ctx context.Context, ev waConnEvent) bool {
	restaurantID, ok := s.resolveBotRestaurantByProviderInstance(ctx, ev.SessionRef)
	if !ok {
		return false
	}

	// Capture the prior status BEFORE the runtime update so we can tell
	// "transitioned from disconnected -> connected" apart from "stayed
	// connected across a status refresh". Only the former should kick the
	// queue; refreshing every successful status would create duplicate
	// triggers (debounced, but wasteful).
	prevStatus := ""
	if rec, found, _ := s.loadRestaurantUAZAPIInstance(ctx, restaurantID); found {
		prevStatus = normalizeUAZAPIConnectionStatus(rec.Status)
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
		// Reconnect transition only (not every connected-status refresh).
		if !isUAZAPIConnected(prevStatus) {
			if rearmed, err := s.triggerWhatsAppQueueOnReconnect(ctx, restaurantID); err != nil {
				log.Printf("[bot] restaurant=%d reconnect queue trigger failed: %v", restaurantID, err)
			} else if rearmed > 0 {
				log.Printf("[bot] restaurant=%d evolution reconnected; outbox rearmed (%d rows)", restaurantID, rearmed)
			}
		}
	}
	s.broadcastWhatsAppConnection(ctx, restaurantID)
	return true
}

// evoEnvelopeInstanceName extracts the instance name from a raw Evolution
// payload without a full parse, so the gateway can resolve the bot's own JIDs
// before the message is normalized.
func evoEnvelopeInstanceName(body []byte) string {
	var env struct {
		Instance string `json:"instance"`
	}
	if json.Unmarshal(body, &env) != nil {
		return ""
	}
	return strings.TrimSpace(env.Instance)
}

// botOwnJIDsByInstanceName returns the JIDs that address the bot for the
// instance that produced this webhook. Used so a group @mention of the linked
// number is recognised as addressed-to-bot (wa_bot_group_mention_v1).
func (s *Server) botOwnJIDsByInstanceName(ctx context.Context, instanceName string) []string {
	if s == nil || s.db == nil || strings.TrimSpace(instanceName) == "" {
		return nil
	}
	restaurantID, ok := s.resolveBotRestaurantByProviderInstance(ctx, instanceName)
	if !ok {
		return nil
	}
	return s.botGroupFetchOwnJIDs(ctx, restaurantID)
}
