package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
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
	IsAudio       bool   // voice note (audioMessage/ptvMessage)
	AudioB64      string // inline voice note for transcription (wa_bot_audio_transcription_v1)
	Transcribed   bool   // Text comes from a voice-note transcription
	Burst         int    // number of coalesced messages (wa_bot_burst_coalesce_v1)
	// Per-turn pipeline verdicts used by every management handoff
	// (wa_bot_management_group_v1): customer language and whether Jev judged
	// this message the same issue as one already forwarded.
	Language         string
	DuplicateRequest bool
	Ignored          bool   // reaction/edit/delete/poll event: never a customer turn
	MediaKind        string // label of a non-text message, for the transcript
	// Coordination id: wa_bot_group_mention_v1 - group routing. When IsGroup
	// is true, Sender is the @g.us group JID (so every tool and the reply
	// address the group) and ParticipantJID identifies the member who
	// mentioned the bot.
	IsGroup        bool
	ChatJID        string
	ParticipantJID string
	Mentioned      bool
	// Coordination id: wa_bot_privileged_group_v1 - the group JID is listed in
	// config.BotPrivilegedGroupJIDs: the turn runs with the full forky
	// assistant tool registry and the preventive guards are skipped.
	PrivilegedGroup bool
	// DocumentB64/DocumentName: inline documentMessage payload, extracted
	// into the prompt only for privileged turns.
	DocumentB64  string
	DocumentName string
	// ActorPhoneJID: provider phone JID of a one-to-one chat whose number the
	// webhook matched to a member of the special-admin group (set only then).
	ActorPhoneJID string
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
		IsAudio:   strings.Contains(strings.ToLower(msg.MessageType), "audio"),
		Ignored:   botIsIgnoredMessageType(msg.MessageType),
		MediaKind: botMediaKindLabel(msg.MessageType),
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
// its provisioning row so the QR onboarding UI reflects the live state. On any
// transition into the connected state it also flushes the outbox + reminder
// queues so messages parked while the link was down fire on the next scan.
func (s *Server) handleBotConnectionEvent(ctx context.Context, ev botConnectionEvent) bool {
	restaurantID, ok := s.resolveBotRestaurant(ctx, ev.InstanceToken, ev.Owner)
	if !ok {
		return false
	}

	// Capture prior status to detect the disconnected -> connected transition.
	prevStatus := ""
	if rec, found, _ := s.loadRestaurantUAZAPIInstance(ctx, restaurantID); found {
		prevStatus = normalizeUAZAPIConnectionStatus(rec.Status)
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
		// Only kick the queue on a true reconnect (not every connected refresh).
		if !isUAZAPIConnected(prevStatus) {
			if rearmed, err := s.triggerWhatsAppQueueOnReconnect(ctx, restaurantID); err != nil {
				log.Printf("[bot] restaurant=%d reconnect queue trigger failed: %v", restaurantID, err)
			} else if rearmed > 0 {
				log.Printf("[bot] restaurant=%d reconnected; outbox rearmed (%d rows)", restaurantID, rearmed)
			}
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
	if !ok {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": false})
		return
	}
	if msg.Ignored {
		log.Printf("[bot] checkpoint wa_bot_ignore_non_conversational_v1 sender=%s", msg.Sender)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": false, "ignored": true})
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
	// Human takeover: staff wrote manually to this customer recently, so the
	// bot stays silent (no agent turn, no media fallback) but keeps the
	// transcript complete. Coordination id: wa_bot_human_handoff_v1
	if s.botIsPausedForHuman(r.Context(), restaurantID, msg.Sender) {
		content := msg.Text
		if content == "" && msg.MediaKind != "" {
			content = "[El cliente envió " + msg.MediaKind + "]"
		}
		s.botRecordConversationMessage(r.Context(), restaurantID, msg.Sender, "user", content, "", "inbound_paused")
		s.botTouchSession(r.Context(), restaurantID, msg.Sender, msg.PushName)
		log.Printf("[bot] checkpoint wa_bot_human_takeover_skip restaurant_id=%d sender=%s", restaurantID, msg.Sender)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": true, "paused": true})
		return
	}
	if !s.botCheckDailyCap(restaurantID) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": false, "code": "DAILY_CAP"})
		return
	}

	// Coordination id: wa_bot_audio_transcription_v1 - voice notes with an
	// inline payload are transcribed in the background turn below and then
	// handled like text; only audios without payload get the fallback here.
	transcribe := msg.Text == "" && msg.IsAudio && (msg.AudioB64 != "" || msg.MessageID != "")
	// Coordination id: wa_bot_privileged_group_v1 - a document in a privileged
	// group is extracted in the turn below, so it must not hit the
	// unsupported-media fallback.
	privilegedDoc := msg.PrivilegedGroup && msg.DocumentB64 != ""
	if msg.Text == "" && !transcribe && !privilegedDoc {
		// Unsupported media: polite fallback. Voice notes get a specific reply:
		// the bot is a reservation assistant and cannot listen to audio.
		fallback := "Ahora mismo solo puedo gestionar mensajes de texto. ¿Me lo puedes escribir por aquí?"
		if msg.IsAudio {
			fallback = "¡Hola! Soy el robot de reservas. No tengo capacidad para escuchar audios ni atender llamadas. Escríbeme por texto, por favor."
		}
		if gw, ok := s.botGatewayFor(r.Context(), restaurantID); ok {
			_ = s.sendWhatsAppTextTracked(r.Context(), restaurantID, gw, msg.Sender, fallback, "unsupported_content")
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": true, "unsupportedContent": true})
		return
	}

	// Attendance commands are handled locally and never sent to the LLM. This
	// prevents prompt injection and guarantees that clock operations are scoped
	// to the sender's active member record and this restaurant.
	if handled := !transcribe && s.botHandleAttendanceCommand(r.Context(), restaurantID, msg); handled {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": true, "attendanceCommand": true})
		return
	}

	// Process in background (bounded): provider webhooks time out quickly.
	// Messages of one conversation are serialized and coalesced into bursts
	// (wa_bot_burst_coalesce_v1); the semaphore bounds concurrent workers.
	burstKey := botBurstKey(restaurantID, msg.Sender)
	if s.botBursts.enqueue(burstKey, msg) {
		select {
		case s.botSem <- struct{}{}:
		default:
			// At capacity: shed load rather than pile up unbounded goroutines.
			s.botBursts.release(burstKey)
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": false, "code": "BUSY"})
			return
		}
		go func() {
			defer func() { <-s.botSem }()
			s.botRunConversationWorker(restaurantID, msg.Sender)
		}()
	}

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
func (s *Server) botProcessMessage(ctx context.Context, restaurantID int, msg botWebhookMessage, parts ...botWebhookMessage) error {
	turnStarted := time.Now()
	tenant := s.loadBotTenantConfig(ctx, restaurantID)
	if len(parts) == 0 {
		parts = []botWebhookMessage{msg}
	}

	// Each message of a coalesced burst is stored as sent.
	for _, p := range parts {
		stored := p.Text
		if p.Transcribed {
			stored = botAudioTranscriptPrefix + p.Text
		}
		s.botRecordConversationMessage(ctx, restaurantID, p.Sender, "user", stored, "", "inbound")
	}
	s.botTouchSession(ctx, restaurantID, msg.Sender, msg.PushName)

	routing := s.loadBotAIRouting(ctx, restaurantID)
	// A tenant-level model override (legacy BotTenantConfig.Model) still wins
	// as the primary model; the routing fallback stays in place.
	if m := strings.TrimSpace(tenant.Model); m != "" {
		if !strings.Contains(m, "/") {
			m = botProviderMiniMax + "/" + m
		}
		if botValidModelRef(m) {
			routing.PrimaryModel = m
		}
	}
	messages := s.botLoadHistory(ctx, restaurantID, msg.Sender)

	// Coordination id: wa_bot_dspy_pipeline_v1 - the decision tree picks the
	// handoff guard or the agent route. Unreachable pipeline -> legacy guards.
	// Coordination id: wa_bot_privileged_group_v1 - a privileged group turn is
	// an internal management turn: the DSPy pipeline decision, the legacy
	// guards (same-day, extras, allergen) and the human-handoff/duplicate
	// handling are the "preventive restrictions" designed for customers and
	// are skipped entirely there. Infrastructure (burst, dedup, cap) still
	// applies upstream in processInboundBotMessage.
	var decision botPipelineDecision
	pipelineOK := false
	if !msg.PrivilegedGroup {
		facts := s.botPipelineFacts(ctx, restaurantID, msg)
		decision, pipelineOK = s.botRunPipeline(ctx, restaurantID, msg, routing, messages, facts)
	} else {
		log.Printf("[bot] checkpoint wa_bot_privileged_group_v1 restaurant_id=%d sender=%s decision=privileged_turn_pipeline_skipped", restaurantID, msg.Sender)
	}
	var decisionID int64
	if pipelineOK {
		if s.botConversation != nil {
			id, err := s.botConversation.AppendDecision(ctx, restaurantID, msg.Sender, msg.Text, decision, "")
			if err != nil {
				log.Printf("[bot] checkpoint wa_bot_dspy_pipeline_v1 restaurant_id=%d store_error=%v", restaurantID, err)
			}
			decisionID = id
		}
		msg.Language, msg.DuplicateRequest = decision.Language, decision.DuplicateRequest
		if decision.HandoffCleared {
			_ = s.botConversation.ClearHandoff(ctx, restaurantID, msg.Sender)
			log.Printf("[bot] checkpoint wa_bot_sticky_handoff_v1 restaurant_id=%d sender=%s cleared new_topic", restaurantID, msg.Sender)
		}
		if s.botApplyPipelineHandoff(ctx, restaurantID, msg, tenant, decision) {
			return nil
		}
	} else if !msg.PrivilegedGroup {
		// Same-day policy is resolved from the raw message before the model runs, so
		// a same-day create/modify/cancel gets the AI notice + contact card instead
		// of a clarifying question the model would ask first.
		if s.botSameDayIntentGuard(ctx, restaurantID, msg, tenant) {
			return nil
		}
		// Coordination id: booking-extras-handoff - extras are managed by the
		// restaurant; the guard answers with the AI notice + contact card before the
		// model runs so it can never promise an extras change.
		if s.botExtrasIntentGuard(ctx, restaurantID, msg, tenant) {
			return nil
		}
		// Coordination id: wa_bot_allergen_handoff_v1 - never let the model guess
		// ingredients/allergens; hand over to the restaurant instead.
		if s.botAllergenIntentGuard(ctx, restaurantID, msg, tenant) {
			return nil
		}
	}

	// Coordination id: wa_bot_rag_fts_v1 - only the rules relevant to this
	// message and route reach the prompt. A tenant with custom rules keeps them.
	promptData := s.loadBotPromptData(ctx, restaurantID, msg.PushName, msg.Sender, tenant)
	if strings.TrimSpace(tenant.Rules) == "" {
		routes := decision.Routes
		if !pipelineOK {
			if intent := botBookingIntentFromText(msg.Text); intent != "" {
				routes = []string{intent}
			}
		}
		promptData.Knowledge = s.botRetrieveKnowledge(ctx, restaurantID, routing, routes, msg.Text)
	}
	promptData.RouteDirective = decision.Directive
	promptData.RouteNode = decision.Node
	// Coordination id: wa_bot_group_mention_v1 - a group turn is a staff turn:
	// the prompt changes and the special-menu CRUD tools become available.
	promptData.InGroup = msg.IsGroup
	if msg.IsGroup {
		promptData.GroupMember = msg.PushName
	}
	// Coordination id: wa_bot_privileged_group_v1 - privileged group prompt block.
	promptData.PrivilegedGroup = msg.PrivilegedGroup
	promptData.SpecialAdminTools = botSpecialAdminToolsAllowed(restaurantID, msg)
	// Document understanding is privileged-only: the inline document is
	// extracted BEFORE the loop and appended to the last user message so the
	// model reads the file content as part of the request. On any failure the
	// note says so verbatim — content is never invented.
	if msg.PrivilegedGroup && msg.DocumentB64 != "" {
		note, extracted := botDocumentTurnNote(msg.DocumentB64, msg.DocumentName)
		messages = botAppendTextToLastUserMessage(messages, note)
		log.Printf("[bot] checkpoint wa_bot_privileged_group_v1 restaurant_id=%d sender=%s document=%s extracted=%t", restaurantID, msg.Sender, msg.DocumentName, extracted)
	}
	system := renderBotSystemPrompt(promptData)
	_, _, memberErr := s.botMemberForPhone(ctx, restaurantID, msg.Sender)
	tools := botCustomerToolDefs(botToolDefsForChat(tenant, msg.IsGroup), memberErr == nil)
	turn := &botTurnState{}
	exec := s.botToolExecutorForTurn(restaurantID, msg, tenant, turn)
	if msg.PrivilegedGroup {
		// Full forky registry plus every bot tool; the executor routes forky
		// tools through the real assistant path (authorization + audit) with
		// the synthetic root auth.
		//
		// The two catalogs overlap (create_booking exists in both) and the
		// provider rejects a tool list with duplicate names ("Tool names
		// must be unique"), which killed the primary model on every
		// privileged turn. The bot tool wins: it is the one whose input
		// schema the bot's executor understands, and the executor routes it
		// to the bot path, not to the forky path.
		botTools := botToolDefsForChat(tenant, true)
		if botSpecialAdminToolsAllowed(restaurantID, msg) {
			botTools = append(botTools, botSpecialAdminToolDefs()...)
			// wa_bot_admin_reads_v1: read-only staff reads share the exact
			// admin scope gate (scope group + privileged turn + fresh member).
			botTools = append(botTools, botToolDefsAdminReads()...)
		}
		// Coordination id: wa_backoffice_ops_v1 / wa_bot_privileged_control_v1:
		// the backoffice operation surface and the control plane of the bot.
		// They are advertised on every privileged turn and re-prove the actor
		// (the verified phone of a current member of the authorized group) on
		// every call.
		botTools = append(botTools, botToolDefsBackoffice()...)
		tools = botMergeToolDefs(botTools, botForkyToolDefs())
		normalExec := exec
		// A name the bot defines belongs to the bot, even when forky also has
		// a tool with that name: the bot keeps its own schema and its own
		// executor. Every other forky tool goes through the assistant path,
		// with the authorization and the audit row it deserves.
		//
		// The check is against the BOT list, not against the merged one: the
		// merged list carries every name, so asking it would send every
		// forky tool to the bot executor, which does not know them.
		botOwned := map[string]struct{}{}
		for _, d := range botTools {
			botOwned[d.Name] = struct{}{}
		}
		exec = func(ctx context.Context, name string, input json.RawMessage) (string, error) {
			// Temporary containment (wa_bot_admin_write_denied_v1): these bot-owned
			// mutations are gated only by a confirmed=true the model writes itself,
			// which is not a human approval. Refused in the server for privileged
			// groups until an approval bound to the exact preview exists. Reads,
			// messaging and add_booking_note are unchanged.
			if botPrivilegedDeniedWrites[name] {
				log.Printf("[bot] checkpoint wa_bot_admin_write_denied_v1 restaurant_id=%d tool=%s", restaurantID, name)
				return botJSON(map[string]any{"error": "esta operación está desactivada temporalmente para el bot (no se ejecuta). No la reintentes ni busques otra vía: di al grupo que debe hacerse en el panel de administración."}), nil
			}
			if _, ok := assistantToolLookup(name); ok {
				if _, mine := botOwned[name]; !mine {
					// Enforcement mirror of the catalog filter
					// (wa_bot_forky_excluded_v1): these forky reads stay
					// panel/MCP-only; the WA staff path uses the dedicated
					// gated tools instead. Refused even if a merged tool list
					// ever exposes the name to the model.
					if _, excluded := botForkyExcludedFromWA[name]; excluded {
						log.Printf("[bot] checkpoint wa_bot_forky_excluded_v1 restaurant_id=%d tool=%s", restaurantID, name)
						return botJSON(map[string]any{"error": "esa consulta del panel no está disponible en este chat. Usa la herramienta dedicada del bot equivalente si existe; si no, debe consultarse en el panel de administración."}), nil
					}
					// A forky write tool the bot does not own is a generic panel
					// mutation whose confirmation token the model redeems itself:
					// refused in the server (wa_bot_admin_write_denied_v1). Reads and
					// the bot's own tools are unchanged.
					if def, _ := assistantToolLookup(name); def.Write {
						log.Printf("[bot] checkpoint wa_bot_admin_write_denied_v1 restaurant_id=%d tool=%s", restaurantID, name)
						return botJSON(map[string]any{"error": "el bot no puede ejecutar esta escritura genérica del panel. Si lo pedido tiene una herramienta dedicada del bot, úsala; si no, di al grupo que esa operación no está soportada y que debe hacerse en el panel. No busques otra vía."}), nil
					}
					return s.assistantExecuteTool(s.botPrivilegedForkyContext(ctx, restaurantID), restaurantID, name, input)
				}
			}
			return normalExec(ctx, name, input)
		}
	}
	sessionID := botSessionID(restaurantID, msg.Sender)
	caller := func(ctx context.Context, system string, msgs []botMessage, tools []botToolDef) (botLLMResponse, string, error) {
		return s.botLLMCallRouted(ctx, restaurantID, routing, sessionID, system, msgs, tools)
	}

	// Coordination id: wa_bot_privileged_control_v1 - per-chat tool control:
	// the catalog offered to the model is filtered, and the executor
	// re-checks.
	tools = botToolGrantsFilter(tools, tenant, msg.ChatJID)
	result, err := s.botRunAgentLoopWith(ctx, caller, system, messages, tools, exec)
	if decisionID > 0 {
		s.botConversation.SetDecisionModel(ctx, decisionID, result.ModelUsed, map[string]any{
			"total_ms": time.Since(turnStarted).Milliseconds(), "iterations": result.Iterations,
			"tools": result.ToolCalls, "burst": max(1, msg.Burst), "transcribed": msg.Transcribed,
		})
	}
	if err != nil {
		// Never ghost the customer: send a graceful fallback on any LLM failure.
		s.botSendFallback(ctx, restaurantID, msg.Sender)
		return err
	}
	// If the model ended its turn without actually delivering anything (plain
	// text, or only read-only tools), send the final assistant text so the
	// customer is never left in silence. A server-side notice (same-day policy)
	// counts as delivered and must not be duplicated.
	sendCtx := ctx
	if result.Interrupted {
		// The turn context may already be expired: send the fixed reply on a short fresh one.
		// WithoutCancel keeps the turn's values (tenant, tracing) but not its expiry.
		var cancelSend context.CancelFunc
		sendCtx, cancelSend = context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancelSend()
	}
	if (result.Interrupted || !botDeliveredReply(result.ToolCalls)) && !turn.noticeDelivered {
		if text := botFinalAssistantText(result.Messages); botPlainTextLeaksInternal(text) {
			// The model typed a contact card or pasted a staff notification as
			// text: deliver the real intro + vCard instead (wa_bot_context_hygiene_v1).
			log.Printf("[bot] checkpoint wa_bot_plain_text_rewritten restaurant_id=%d sender=%s", restaurantID, msg.Sender)
			_, _ = s.botToolSendContact(sendCtx, restaurantID, msg, tenant, nil)
		} else if text != "" {
			log.Printf("[bot] checkpoint wa_bot_plain_text_fallback restaurant_id=%d sender=%s", restaurantID, msg.Sender)
			if gw, ok := s.botGatewayFor(sendCtx, restaurantID); ok && s.sendWhatsAppTextTracked(sendCtx, restaurantID, gw, msg.Sender, text, "agent_plain_text") == nil {
			}
		} else {
			s.botSendFallback(sendCtx, restaurantID, msg.Sender)
		}
	}
	log.Printf("[bot] restaurant=%d sender=%s iterations=%d model=%s node=%s tools=%s",
		restaurantID, msg.Sender, result.Iterations, result.ModelUsed, decision.Node, strings.Join(result.ToolCalls, ","))
	return nil
}

// botPlainTextLeakRe matches plain-text replies that imitate a contact card or
// a staff-only booking notification instead of answering the customer.
var botPlainTextLeakRe = regexp.MustCompile(`(?i)(^|\n)\s*contacto:|🚨|cancelada por:|hora cancelaci`)

func botPlainTextLeaksInternal(text string) bool {
	return text != "" && botPlainTextLeakRe.MatchString(text)
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
	if s.sendWhatsAppTextTracked(ctx, restaurantID, gw, sender, fallback, "agent_fallback") == nil {
	}
}

// botPrivilegedDeniedWrites lists the bot-owned destructive or special-booking
// mutations refused for privileged groups while their confirmation is only a
// model-written flag.
var botPrivilegedDeniedWrites = map[string]bool{
	"create_booking":         true,
	"modify_booking":         true,
	"cancel_booking":         true,
	"modify_special_booking": true,
	"cancel_special_booking": true,
	"create_special_booking": true,
}
