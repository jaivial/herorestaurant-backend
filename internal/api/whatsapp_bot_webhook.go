package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

// botMaxConcurrentTurns bounds simultaneous inbound agent turns across all
// tenants so a burst cannot spawn unbounded goroutines. ponytail: fixed cap;
// make it configurable if a large fleet needs per-tenant fairness.
const botMaxConcurrentTurns = 32

// botWebhookMessage is the normalized inbound WhatsApp message extracted from
// a UAZAPI webhook payload.
type botWebhookMessage struct {
	Sender        string
	Text          string
	PushName      string
	MessageID     string
	FromMe        bool
	InstanceToken string
	Owner         string
}

// parseBotWebhookMessage extracts the message from a UAZAPI webhook body.
// Returns ok=false for payloads without a direct chat message (calls,
// connection events, group chats).
func parseBotWebhookMessage(body []byte) (botWebhookMessage, bool) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return botWebhookMessage{}, false
	}

	rawMsg, ok := root["message"]
	if !ok {
		return botWebhookMessage{}, false
	}
	var msg struct {
		ChatID      string `json:"chatid"`
		Text        string `json:"text"`
		Vote        string `json:"vote"`
		FromMe      bool   `json:"fromMe"`
		PushName    string `json:"pushname"`
		MessageID   string `json:"messageid"`
		MessageID2  string `json:"messageId"`
		ID          string `json:"id"`
		MessageType string `json:"messageType"`
	}
	if err := json.Unmarshal(rawMsg, &msg); err != nil {
		return botWebhookMessage{}, false
	}

	chatID := strings.TrimSpace(msg.ChatID)
	if chatID == "" || !strings.HasSuffix(chatID, "@s.whatsapp.net") {
		return botWebhookMessage{}, false
	}
	sender := strings.TrimSuffix(chatID, "@s.whatsapp.net")

	text := strings.TrimSpace(msg.Text)
	if v := strings.TrimSpace(msg.Vote); v != "" {
		text = v
	}

	messageID := msg.MessageID
	if messageID == "" {
		messageID = msg.MessageID2
	}
	if messageID == "" {
		messageID = msg.ID
	}

	pushName := strings.TrimSpace(msg.PushName)
	if pushName == "" {
		pushName = "Cliente"
	}

	out := botWebhookMessage{
		Sender:    sender,
		Text:      text,
		PushName:  sanitizeBotPushName(pushName),
		MessageID: messageID,
		FromMe:    msg.FromMe,
	}

	var tokenField struct {
		Token string `json:"token"`
		Owner string `json:"owner"`
	}
	_ = json.Unmarshal(body, &tokenField)
	out.InstanceToken = strings.TrimSpace(tokenField.Token)
	out.Owner = strings.TrimSpace(tokenField.Owner)

	return out, true
}

// botConnectionEvent is a normalized UAZAPI instance/connection lifecycle event
// (connection.update, qrcode, pairing) used to keep the provisioning row and the
// backoffice QR screen in sync without polling /instance/status.
type botConnectionEvent struct {
	InstanceToken  string
	Owner          string
	Status         string
	ConnectedPhone string
	QR             string
	PairCode       string
}

// parseBotConnectionEvent extracts a connection lifecycle event from a UAZAPI
// webhook body. Returns ok=false for message payloads (handled elsewhere).
func parseBotConnectionEvent(body []byte) (botConnectionEvent, bool) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return botConnectionEvent{}, false
	}
	// If it carries a direct chat message, it is not a connection event.
	if _, hasMsg := root["message"]; hasMsg {
		return botConnectionEvent{}, false
	}

	eventType := strings.ToLower(strings.TrimSpace(jsonStringField(root, "event", "EventType", "type", "eventType")))
	// Only treat known lifecycle events (or presence of qr/status) as connection.
	hasQR := len(root["qrcode"]) > 0 || len(root["qr"]) > 0
	looksConn := strings.Contains(eventType, "connect") || strings.Contains(eventType, "qr") ||
		strings.Contains(eventType, "pair") || strings.Contains(eventType, "status") || hasQR
	if !looksConn {
		return botConnectionEvent{}, false
	}

	ev := botConnectionEvent{
		InstanceToken:  strings.TrimSpace(jsonStringField(root, "token", "instance_token", "instanceToken")),
		Owner:          digitsOnly(jsonStringField(root, "owner", "wid", "connected_phone", "phone", "number")),
		Status:         jsonStringField(root, "status", "state", "connection", "connectionState", "connection_status"),
		ConnectedPhone: digitsOnly(jsonStringField(root, "phone", "number", "wid", "connected_phone")),
		QR:             jsonStringField(root, "qrcode", "qr", "qr_code", "qrCode", "base64", "base64_qr"),
		PairCode:       jsonStringField(root, "pair_code", "pairCode", "paircode", "code"),
	}
	if ev.InstanceToken == "" && ev.Owner == "" {
		// Try nested instance object.
		if raw, ok := root["instance"]; ok {
			var inst map[string]json.RawMessage
			if json.Unmarshal(raw, &inst) == nil {
				ev.InstanceToken = strings.TrimSpace(jsonStringField(inst, "token", "instance_token", "instanceToken"))
				if ev.Owner == "" {
					ev.Owner = digitsOnly(jsonStringField(inst, "owner", "phone", "number", "wid"))
				}
				if ev.Status == "" {
					ev.Status = jsonStringField(inst, "status", "state")
				}
			}
		}
	}
	if ev.InstanceToken == "" && ev.Owner == "" {
		return botConnectionEvent{}, false
	}
	return ev, true
}

// jsonStringField returns the first present string field (by any of keys) from a
// raw JSON object map.
func jsonStringField(root map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		raw, ok := root[k]
		if !ok {
			continue
		}
		var str string
		if json.Unmarshal(raw, &str) == nil && strings.TrimSpace(str) != "" {
			return strings.TrimSpace(str)
		}
	}
	return ""
}

// handleBotConnectionEvent maps the lifecycle event to a restaurant and updates
// its provisioning row so the QR onboarding UI reflects the live state.
func (s *Server) handleBotConnectionEvent(ctx context.Context, ev botConnectionEvent) bool {
	restaurantID, ok := s.resolveBotRestaurant(ctx, ev.InstanceToken, ev.Owner)
	if !ok {
		return false
	}
	status := normalizeUAZAPIConnectionStatus(ev.Status)
	if status == "" && (ev.QR != "" || ev.PairCode != "") {
		status = "pending"
	}
	if err := s.updateRestaurantUAZAPIInstanceRuntime(ctx, restaurantID, status, ev.ConnectedPhone, ev.QR, ev.PairCode); err != nil {
		log.Printf("[bot] restaurant=%d connection event update failed: %v", restaurantID, err)
		return false
	}
	// On connect, (re)assert webhook + integration sync defensively.
	if isUAZAPIConnected(status) {
		if rec, found, err := s.loadRestaurantUAZAPIInstance(ctx, restaurantID); err == nil && found {
			_ = s.syncRestaurantUAZAPIIntegration(ctx, restaurantID, rec.ServerBaseURL, rec.InstanceToken)
		}
	}
	s.broadcastWhatsAppConnection(ctx, restaurantID)
	return true
}

// botSeenBefore is an in-process dedup on (sender, messageID) with pruning.
func (s *Server) botSeenBefore(sender string, messageID string) bool {
	if messageID == "" {
		return false
	}
	key := sender + ":" + messageID
	now := time.Now().Unix()

	s.botSeenMu.Lock()
	defer s.botSeenMu.Unlock()
	if s.botSeen == nil {
		s.botSeen = map[string]int64{}
	}
	if _, ok := s.botSeen[key]; ok {
		return true
	}
	// Prune entries older than 1h.
	if len(s.botSeen) > 4096 {
		for k, ts := range s.botSeen {
			if now-ts > 3600 {
				delete(s.botSeen, k)
			}
		}
	}
	s.botSeen[key] = now
	return false
}

// resolveBotRestaurant maps an instance token (and/or owner phone) from the
// webhook to a restaurant with an active provisioned UAZAPI instance.
func (s *Server) resolveBotRestaurant(ctx context.Context, instanceToken string, owner string) (int, bool) {
	if instanceToken != "" {
		var rid int
		err := s.db.QueryRowContext(ctx, `
			SELECT restaurant_id FROM restaurant_uazapi_instances
			WHERE instance_token = ? AND is_active = 1
			LIMIT 1
		`, instanceToken).Scan(&rid)
		if err == nil {
			return rid, true
		}
		if !errors.Is(err, sql.ErrNoRows) && !isSQLSchemaError(err) {
			return 0, false
		}
	}
	if owner != "" {
		var rid int
		err := s.db.QueryRowContext(ctx, `
			SELECT restaurant_id FROM restaurant_uazapi_instances
			WHERE connected_phone = ? AND is_active = 1
			LIMIT 1
		`, digitsOnly(owner)).Scan(&rid)
		if err == nil {
			return rid, true
		}
	}
	return 0, false
}

// loadBotTenantConfig reads whatsapp_bot_config for the restaurant.
func (s *Server) loadBotTenantConfig(ctx context.Context, restaurantID int) botTenantConfig {
	var raw sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT config_json FROM whatsapp_bot_config WHERE restaurant_id = ? LIMIT 1
	`, restaurantID).Scan(&raw)
	if err != nil {
		return parseBotTenantConfig("")
	}
	return parseBotTenantConfig(raw.String)
}

// handleBotWebhook is the inbound UAZAPI webhook for the multi-tenant bot.
// POST /bot/webhook
func (s *Server) handleBotWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": false})
		return
	}

	// Connection lifecycle events (qr/pairing/connected) update provisioning
	// state so the QR onboarding UI stays live without polling.
	if ev, isConn := parseBotConnectionEvent(body); isConn {
		updated := s.handleBotConnectionEvent(r.Context(), ev)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": updated, "connection": true})
		return
	}

	msg, ok := parseBotWebhookMessage(body)
	if !ok || msg.FromMe {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": false})
		return
	}

	// A message must carry a valid instance token. Owner (phone) resolution is
	// only trusted for connection lifecycle events; allowing owner-only message
	// routing would let a forged webhook drive any tenant's bot.
	restaurantID, ok := s.resolveBotRestaurant(r.Context(), msg.InstanceToken, "")
	if !ok {
		// Unknown/unauthenticated: 200 so the provider does not disable or hammer
		// the webhook on non-2xx responses.
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"processed": false,
			"message":   "unknown instance",
		})
		return
	}

	s.processInboundBotMessage(w, r, restaurantID, msg)
}

// processInboundBotMessage runs the shared inbound pipeline (entitlement gate,
// dedup, daily cap, media fallback, bounded background agent turn) for a
// resolved tenant. Used by both the UAZAPI and Evolution webhook handlers.
func (s *Server) processInboundBotMessage(w http.ResponseWriter, r *http.Request, restaurantID int, msg botWebhookMessage) {
	// Subscription gate: WhatsApp Pack must be active.
	entitled, err := s.hasActiveRecurringFeature(r.Context(), restaurantID, boPremiumWhatsAppFeatureKey)
	if err != nil {
		// A transient entitlement lookup failure must not masquerade as
		// "unsubscribed" — that would silently drop a paying tenant's message.
		log.Printf("[bot] restaurant=%d entitlement check failed: %v", restaurantID, err)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": false, "code": "ENTITLEMENT_CHECK_FAILED"})
		return
	}
	if !entitled {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"processed": false,
			"code":      "NEEDS_SUBSCRIPTION",
		})
		return
	}

	// Dedup + daily cap first, so provider retries neither re-trigger replies
	// (incl. the unsupported-media fallback) nor bypass the per-tenant cap.
	if s.botSeenBefore(msg.Sender, msg.MessageID) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": true, "duplicate": true})
		return
	}
	if !s.botCheckDailyCap(restaurantID) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": false, "code": "DAILY_CAP"})
		return
	}

	if msg.Text == "" {
		// Unsupported media: polite fallback.
		if gw, ok := s.botGatewayFor(r.Context(), restaurantID); ok {
			_ = gw.SendText(r.Context(), msg.Sender, "Ahora mismo solo puedo gestionar mensajes de texto. ¿Me lo puedes escribir por aquí?")
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": true, "unsupportedContent": true})
		return
	}

	// Process in background (bounded): provider webhooks time out quickly. A
	// recover() keeps one bad turn from crashing the whole multi-tenant process.
	select {
	case s.botSem <- struct{}{}:
	default:
		// At capacity: shed load rather than pile up unbounded goroutines.
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": false, "code": "BUSY"})
		return
	}
	go func() {
		defer func() {
			<-s.botSem
			if rec := recover(); rec != nil {
				log.Printf("[bot] restaurant=%d sender=%s PANIC recovered: %v", restaurantID, msg.Sender, rec)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if err := s.botProcessMessage(ctx, restaurantID, msg); err != nil {
			log.Printf("[bot] restaurant=%d sender=%s error: %v", restaurantID, msg.Sender, err)
		}
	}()

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": true})
}

// botCheckDailyCap enforces a per-restaurant daily turn cap in-process.
func (s *Server) botCheckDailyCap(restaurantID int) bool {
	cap := s.cfg.BotDailyTurnsCap
	if cap <= 0 {
		cap = 2000
	}
	today := time.Now().Format("2006-01-02")

	s.botCapMu.Lock()
	defer s.botCapMu.Unlock()
	if s.botCapDay != today {
		s.botCapDay = today
		s.botCapCount = map[int]int{}
	}
	if s.botCapCount == nil {
		s.botCapCount = map[int]int{}
	}
	if s.botCapCount[restaurantID] >= cap {
		return false
	}
	s.botCapCount[restaurantID]++
	return true
}

// sanitizeBotPushName strips control chars / markdown / newlines and length-caps
// the customer-controlled WhatsApp display name before it is interpolated into
// the system prompt, closing a prompt-injection vector (attacker sets their
// display name to instructions).
func sanitizeBotPushName(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case r == '\n', r == '\r', r == '\t':
			return ' '
		case r < 0x20: // other control chars
			return -1
		case r == '*', r == '#', r == '`', r == '_', r == '[', r == ']', r == '<', r == '>', r == '{', r == '}':
			return -1
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ") // collapse whitespace
	if len(name) > 60 {
		name = strings.TrimSpace(name[:60])
	}
	if name == "" {
		name = "Cliente"
	}
	return name
}

// botProcessMessage runs the full agent turn for an inbound message.
func (s *Server) botProcessMessage(ctx context.Context, restaurantID int, msg botWebhookMessage) error {
	tenant := s.loadBotTenantConfig(ctx, restaurantID)
	system := s.buildBotSystemPrompt(ctx, restaurantID, msg.PushName, msg.Sender, tenant)

	history := s.botLoadHistory(ctx, restaurantID, msg.Sender)
	s.botSaveMessage(ctx, restaurantID, msg.Sender, "user", msg.Text, "")
	s.botTouchSession(ctx, restaurantID, msg.Sender, msg.PushName)

	messages := append(history, botUserText(msg.Text))
	tools := botToolDefs(tenant)
	exec := s.botToolExecutorFor(restaurantID, msg, tenant)

	result, err := s.botRunAgentLoop(ctx, tenant.Model, system, messages, tools, exec)
	if err != nil {
		// Never ghost the customer: send a graceful fallback on any LLM failure.
		s.botSendFallback(ctx, restaurantID, msg.Sender)
		return err
	}
	// If the model ended its turn without actually delivering anything (plain
	// text, or only read-only tools), send the final assistant text so the
	// customer is never left in silence.
	if !botDeliveredReply(result.ToolCalls) {
		if text := botFinalAssistantText(result.Messages); text != "" {
			if gw, ok := s.botGatewayFor(ctx, restaurantID); ok && gw.SendText(ctx, msg.Sender, text) == nil {
				s.botSaveMessage(ctx, restaurantID, msg.Sender, "assistant", text, "")
			}
		} else {
			s.botSendFallback(ctx, restaurantID, msg.Sender)
		}
	}
	log.Printf("[bot] restaurant=%d sender=%s iterations=%d tools=%s",
		restaurantID, msg.Sender, result.Iterations, strings.Join(result.ToolCalls, ","))
	return nil
}

// botDeliveryTools are the tools that actually push a message to the customer.
var botDeliveryTools = map[string]bool{
	"send_message": true, "send_menu_buttons": true, "send_image": true,
	"send_document": true, "send_location": true, "send_contact": true,
}

func botDeliveredReply(toolCalls []string) bool {
	for _, name := range toolCalls {
		if botDeliveryTools[name] {
			return true
		}
	}
	return false
}

// botFinalAssistantText returns the concatenated text of the last assistant turn.
func botFinalAssistantText(msgs []botMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "assistant" {
			continue
		}
		var parts []string
		for _, b := range msgs[i].Content {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				parts = append(parts, strings.TrimSpace(b.Text))
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return ""
}

// botSendFallback delivers a generic apology when the agent produced no reply.
func (s *Server) botSendFallback(ctx context.Context, restaurantID int, sender string) {
	const fallback = "Perdona, ahora mismo no puedo responderte. ¿Puedes intentarlo de nuevo en un momento?"
	gw, ok := s.botGatewayFor(ctx, restaurantID)
	if !ok {
		return
	}
	if gw.SendText(ctx, sender, fallback) == nil {
		s.botSaveMessage(ctx, restaurantID, sender, "assistant", fallback, "")
	}
}
