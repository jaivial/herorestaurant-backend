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
		PushName:  pushName,
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

	msg, ok := parseBotWebhookMessage(body)
	if !ok || msg.FromMe {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": false})
		return
	}

	restaurantID, ok := s.resolveBotRestaurant(r.Context(), msg.InstanceToken, msg.Owner)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"processed": false,
			"message":   "unknown instance",
		})
		return
	}

	// Subscription gate: WhatsApp Pack must be active.
	entitled, err := s.hasActiveRecurringFeature(r.Context(), restaurantID, boPremiumWhatsAppFeatureKey)
	if err != nil || !entitled {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"processed": false,
			"code":      "NEEDS_SUBSCRIPTION",
		})
		return
	}

	if msg.Text == "" {
		// Unsupported media: polite fallback.
		uazURL, uazToken := s.uazapiBaseAndToken(r.Context(), restaurantID)
		if uazURL != "" {
			_ = botUazapiSend(r.Context(), uazURL, uazToken, "text", map[string]any{
				"number": msg.Sender,
				"text":   "Ahora mismo solo puedo gestionar mensajes de texto. ¿Me lo puedes escribir por aquí?",
			})
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": true, "unsupportedContent": true})
		return
	}

	if s.botSeenBefore(msg.Sender, msg.MessageID) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": true, "duplicate": true})
		return
	}

	// Daily cap per restaurant.
	if !s.botCheckDailyCap(restaurantID) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": false, "code": "DAILY_CAP"})
		return
	}

	// Process in background: UAZAPI webhooks time out quickly.
	go func() {
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

	result, err := s.botRunAgentLoop(ctx, system, messages, tools, exec)
	if err != nil {
		return err
	}
	log.Printf("[bot] restaurant=%d sender=%s iterations=%d tools=%s",
		restaurantID, msg.Sender, result.Iterations, strings.Join(result.ToolCalls, ","))
	return nil
}
