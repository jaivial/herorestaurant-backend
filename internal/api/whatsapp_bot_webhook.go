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

	"preactvillacarmen/internal/diag"
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
	// wa_bot_group_reaction_lid_v1: the reaction key needs the participant
	// exactly as WhatsApp keyed the message (possibly an opaque LID).
	ParticipantRawJID string
	// Coordination id: wa_bot_privileged_group_v1 - the group JID is listed in
	// config.BotPrivilegedGroupJIDs: the turn runs with the full forky
	// assistant tool registry and the preventive guards are skipped.
	PrivilegedGroup bool
	// DocumentB64/DocumentName: inline documentMessage payload, extracted
	// into the prompt only for privileged turns.
	DocumentB64  string
	DocumentName string
	// ImageB64/ImageMime: inline imageMessage payload, OCR-read into the turn
	// for privileged turns. Coordination id: wa_bot_attachment_read_v1
	ImageB64  string
	ImageMime string
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
		botCheckpoint("wa_bot_ignore_non_conversational_v1", "sender=%s", msg.Sender)
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
		botCheckpoint("wa_bot_human_takeover_skip", "restaurant_id=%d sender=%s", restaurantID, msg.Sender)
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
	// Coordination id: wa_bot_attachment_read_v1 - a document OR an image in a
	// privileged chat is read into the turn below, so it must not hit the
	// unsupported-media fallback.
	privilegedDoc := msg.PrivilegedGroup && botHasReadableAttachment(msg)
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

	// Coordination id: wa_bot_reaction_feedback_v1 - the instant content-aware
	// reaction goes out the moment the message is accepted, before any work.
	s.botReactToMessage(restaurantID, msg, botContentEmoji(msg))

	// Process in background (bounded): provider webhooks time out quickly.
	// Messages of one conversation are serialized and coalesced into bursts
	// (wa_bot_burst_coalesce_v1); the semaphore bounds concurrent workers.
	burstKey := botBurstKey(restaurantID, msg.Sender)
	// wa_bot_inbound_queue_v1: persist BEFORE accepting. The webhook answers
	// 200 to the provider either way; this row is what survives a deploy or a
	// crash instead of silently dropping the turn (incident 2026-10-22 14:35).
	s.botInboundEnqueue(r.Context(), restaurantID, burstKey, msg)
	if s.botBursts.enqueue(burstKey, msg) {
		select {
		case s.botSem <- struct{}{}:
		default:
			// At capacity: shed load rather than pile up unbounded goroutines.
			// The conversation queue is dropped (its rows stay pending and are
			// replayed at the next boot); this row is explicitly shed.
			s.botBursts.release(burstKey)
			s.botInboundFinish(restaurantID, []string{msg.MessageID}, "shed")
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"processed": false, "code": "BUSY"})
			return
		}
		go func() {
			defer func() { <-s.botSem }()
			// Coordination id: wa_bot_quick_ack_v1 - the quick ack goes out in
			// PARALLEL with the real task below (allowed chats only).
			s.botQuickAck(restaurantID, msg)
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

// botFallbackReplyText is the generic apology delivered when the agent
// produced no reply (shared by botSendFallback and the turn diag event).
const botFallbackReplyText = "Perdona, ahora mismo no puedo responderte. ¿Puedes intentarlo de nuevo en un momento?"

// botProcessMessage runs the full agent turn for an inbound message.
func (s *Server) botProcessMessage(ctx context.Context, restaurantID int, msg botWebhookMessage, parts ...botWebhookMessage) error {
	turnStarted := time.Now()
	// Coordination id: wa_bot_turn_diag_v1 - the whole turn is correlated by
	// the existing per-conversation session id (botSessionID, shared with the
	// LLM session header, the pipeline log and the stored decision trace), so
	// every diag event of this turn (tool results, guards, reply) reconstructs
	// it end to end without inventing a new identifier.
	ctx = diag.WithCoordination(ctx, botSessionID(restaurantID, msg.Sender))
	// wa_bot_privileged_turn_budget_v1: a privileged turn is a staff job, not
	// a chat: its model calls may use the turn's remaining budget instead of
	// the fixed per-call cap.
	if msg.PrivilegedGroup {
		ctx = withBotTurnBudget(ctx)
	}
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
		botCheckpoint("wa_bot_privileged_group_v1", "restaurant_id=%d sender=%s decision=privileged_turn_pipeline_skipped", restaurantID, msg.Sender)
	}
	var decisionID int64
	if pipelineOK {
		if s.botConversation != nil {
			id, err := s.botConversation.AppendDecision(ctx, restaurantID, msg.Sender, msg.Text, decision, "")
			if err != nil {
				botCheckpoint("wa_bot_dspy_pipeline_v1", "restaurant_id=%d store_error=%v", restaurantID, err)
			}
			decisionID = id
		}
		msg.Language, msg.DuplicateRequest = decision.Language, decision.DuplicateRequest
		// Coordination id: wa_bot_dup_decision_v1 - a "repeat" derivation is
		// only trusted when the classifier was confident enough. Below
		// botRepeatHandoffMinConfidence (prod 2026-10-22: "Hoka",
		// confidence=0.00, node=handoff_repeat -> the message was derived to
		// management and never answered) the message is treated as a NEW query:
		// the duplicate verdict and the repeat handoff are dropped and the
		// agent turn runs normally.
		if msg.DuplicateRequest || (decision.Action == "handoff_human" && decision.HandoffReason == "repeat") {
			derive := decision.Confidence >= botRepeatHandoffMinConfidence
			if !derive {
				msg.DuplicateRequest = false
				decision.Action = ""
				decision.HandoffReason = ""
			}
			botCheckpointCtx(ctx, "wa_bot_dup_decision_v1", "derive=%t confidence=%.2f threshold=%.2f node=%s intent=%s",
				derive, decision.Confidence, botRepeatHandoffMinConfidence, decision.Node, decision.Intent)
		}
		if decision.HandoffCleared {
			_ = s.botConversation.ClearHandoff(ctx, restaurantID, msg.Sender)
			botCheckpoint("wa_bot_sticky_handoff_v1", "restaurant_id=%d sender=%s cleared new_topic", restaurantID, msg.Sender)
		}
		if s.botApplyPipelineHandoff(ctx, restaurantID, msg, tenant, decision) {
			// Coordination id: wa_bot_turn_end_v1 - closed by the pipeline
			// handoff route before any agent loop ran.
			botCheckpointCtx(ctx, "wa_bot_turn_end_v1", "restaurant_id=%d sender=%s message_id=%s reason=handoff node=%s action=%s handoff_reason=%s duplicate_request=%t confidence=%.2f ms=%d",
				restaurantID, msg.Sender, msg.MessageID, decision.Node, decision.Action, decision.HandoffReason, decision.DuplicateRequest, decision.Confidence, time.Since(turnStarted).Milliseconds())
			return nil
		}
	} else if !msg.PrivilegedGroup {
		// Same-day policy is resolved from the raw message before the model runs, so
		// a same-day create/modify/cancel gets the AI notice + contact card instead
		// of a clarifying question the model would ask first.
		if s.botSameDayIntentGuard(ctx, restaurantID, msg, tenant) {
			botCheckpointCtx(ctx, "wa_bot_turn_end_v1", "restaurant_id=%d sender=%s message_id=%s reason=same_day_guard ms=%d", restaurantID, msg.Sender, msg.MessageID, time.Since(turnStarted).Milliseconds())
			return nil
		}
		// Coordination id: booking-extras-handoff - extras are managed by the
		// restaurant; the guard answers with the AI notice + contact card before the
		// model runs so it can never promise an extras change.
		if s.botExtrasIntentGuard(ctx, restaurantID, msg, tenant) {
			botCheckpointCtx(ctx, "wa_bot_turn_end_v1", "restaurant_id=%d sender=%s message_id=%s reason=extras_guard ms=%d", restaurantID, msg.Sender, msg.MessageID, time.Since(turnStarted).Milliseconds())
			return nil
		}
		// Coordination id: wa_bot_allergen_handoff_v1 - never let the model guess
		// ingredients/allergens; hand over to the restaurant instead.
		if s.botAllergenIntentGuard(ctx, restaurantID, msg, tenant) {
			botCheckpointCtx(ctx, "wa_bot_turn_end_v1", "restaurant_id=%d sender=%s message_id=%s reason=allergen_guard ms=%d", restaurantID, msg.Sender, msg.MessageID, time.Since(turnStarted).Milliseconds())
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
	promptData.RouteDirective = s.botSpecialDateRouteDirective(ctx, restaurantID, decision)
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
	// wa_bot_privileged_direct_writes_v1: the prompt tells the model whether
	// writes apply directly or go through the OK flow.
	promptData.DirectWrites = tenant.PrivilegedDirectWrites
	// Document understanding is privileged-only: the inline document is
	// extracted BEFORE the loop and appended to the last user message so the
	// model reads the file content as part of the request. On any failure the
	// note says so verbatim — content is never invented.
	// Coordination id: wa_bot_attachment_read_v1 - documents AND images are
	// extracted BEFORE the loop and appended to the last user message so the
	// model reads the content as part of the request. On any failure the note
	// says so verbatim — content is never invented.
	// wa_bot_image_read_v1: a text turn of a privileged chat reads the recent
	// images of the chat too (cached): "estos son los datos del cliente" typed
	// after the photo of the bill still needs the bill.
	if msg.PrivilegedGroup && (botHasReadableAttachment(msg) || botTurnHasRequestImages(restaurantID, msg)) {
		// campaign_tools_v1: the image is also a resource the tools can use
		// (manage_campaign action "image" uploads it to the restaurant CDN);
		// the webhook already remembered it, the model only needs its ref.
		imageRef := botChatImageRef(msg)
		note, extracted := s.botAttachmentTurnNote(ctx, restaurantID, msg)
		if imageRef != "" {
			note += "\n[Imagen disponible para herramientas: image_ref=" + imageRef + " (también vale sin image_ref: se usa la imagen más reciente de este chat)]"
		}
		messages = botAppendTextToLastUserMessage(messages, note)
		botCheckpoint("wa_bot_attachment_read_v1", "restaurant_id=%d sender=%s media=%s extracted=%t", restaurantID, msg.Sender, msg.MediaKind, extracted)
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
			// wa_bot_catalog_v2: drop customer phone-scoped tools (see botStaffPhoneScopedTools).
			botTools = botWithoutTools(botTools, botStaffPhoneScopedTools)
			// wa_bot_intent_catalog_v1: drop tool groups unrelated to this message.
			if drop := botIntentPrunedTools(msg.Text); len(drop) > 0 {
				botTools = botWithoutTools(botTools, drop)
			}
		}
		// Coordination id: wa_bot_privileged_control_v1 - the control plane of
		// this bot version (allowed chats + per-chat tool grants + allergen
		// data) exists ONLY in allowed (privileged) contacts and groups.
		control := botToolDefsPrivilegedControl()
		if botSpecialAdminToolsAllowed(restaurantID, msg) {
			control = botWithoutTools(control, botIntentPrunedTools(msg.Text))
		}
		botTools = append(botTools, control...)
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
		// Ownership comes from the UNPRUNED bot catalogs (wa_bot_catalog_v2 /
		// wa_bot_intent_catalog_v1 only hide names from the model): a pruned bot
		// name must still route to the bot executor, never to the forky path.
		botOwned := map[string]struct{}{}
		for _, d := range botMergeToolDefs(botToolDefsForChat(tenant, true), botSpecialAdminToolDefs(), botToolDefsAdminReads(), botToolDefsPrivilegedControl()) {
			botOwned[d.Name] = struct{}{}
		}
		exec = func(ctx context.Context, name string, input json.RawMessage) (string, error) {
			// Coordination id: wa_bot_tool_grants_v1 - the per-chat grant guard
			// also covers the forky names, which never reach botExecuteTool.
			if reason := botToolDeniedForChat(name, tenant, msg.ChatJID); reason != "" {
				botCheckpoint("wa_bot_tool_grants_v1", "restaurant_id=%d tool=%s", restaurantID, name)
				// wa_bot_terminal_tool_v1: a non-granted tool will not be granted
				// mid-turn: the refusal is final and must not be retried.
				return botJSON(map[string]any{"error": reason, "terminal": true}), nil
			}
			// Temporary containment (wa_bot_admin_write_denied_v1): these bot-owned
			// mutations are gated only by a confirmed=true the model writes itself,
			// which is not a human approval. Refused in the server for privileged
			// groups until an approval bound to the exact preview exists. Reads,
			// messaging and add_booking_note are unchanged.
			if botPrivilegedDeniedWrites[name] && !tenant.PrivilegedDirectWrites {
				botCheckpoint("wa_bot_admin_write_denied_v1", "restaurant_id=%d tool=%s", restaurantID, name)
				// wa_bot_terminal_tool_v1: the refusal is the policy itself; the
				// model insisting on the same call is what burned 22 iterations in
				// prod. Marked terminal so the loop closes honestly instead.
				return botJSON(map[string]any{"error": "esta operación directa está desactivada para el bot (no se ejecuta). No la reintentes: usa la herramienta de PROPUESTA equivalente (booking_propose_create/edit/cancel, special_booking_propose_create, booking_propose_restore...), que se aplica con 'OK <código>'.", "terminal": true}), nil
			}
			if _, ok := assistantToolLookup(name); ok {
				if _, mine := botOwned[name]; !mine {
					// Enforcement mirror of the catalog filter
					// (wa_bot_forky_excluded_v1): these forky reads stay
					// panel/MCP-only; the WA staff path uses the dedicated
					// gated tools instead. Refused even if a merged tool list
					// ever exposes the name to the model.
					if _, excluded := botForkyExcludedFromWA[name]; excluded {
						botCheckpoint("wa_bot_forky_excluded_v1", "restaurant_id=%d tool=%s", restaurantID, name)
						// wa_bot_terminal_tool_v1: panel-only query, never available here.
						return botJSON(map[string]any{"error": "esa consulta del panel no está disponible en este chat. Usa la herramienta dedicada del bot equivalente si existe; si no, debe consultarse en el panel de administración.", "terminal": true}), nil
					}
					// A forky write tool the bot does not own is a generic panel
					// mutation whose confirmation token the model redeems itself:
					// refused in the server (wa_bot_admin_write_denied_v1). Reads and
					// the bot's own tools are unchanged.
					if def, _ := assistantToolLookup(name); def.Write && !tenant.PrivilegedDirectWrites {
						botCheckpoint("wa_bot_admin_write_denied_v1", "restaurant_id=%d tool=%s", restaurantID, name)
						// wa_bot_terminal_tool_v1: generic panel write, final refusal.
						return botJSON(map[string]any{"error": "el bot no puede ejecutar esta escritura genérica del panel directamente. Si lo pedido tiene una herramienta dedicada del bot, úsala; si no, PROPON el cambio equivalente con panel_change (se aplica cuando el usuario responde 'OK <código>'). No busques otra vía.", "terminal": true}), nil
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

	// Coordination id: wa_bot_tool_grants_v1 - per-chat tool control: the
	// catalog offered to the model is filtered, and the executor re-checks.
	tools = botToolGrantsFilter(tools, tenant, msg.ChatJID)
	result, err := s.botRunAgentLoopWith(ctx, caller, system, messages, tools, exec)
	if decisionID > 0 {
		s.botConversation.SetDecisionModel(ctx, decisionID, result.ModelUsed, map[string]any{
			"total_ms": time.Since(turnStarted).Milliseconds(), "iterations": result.Iterations,
			"tools": result.ToolCalls, "burst": max(1, msg.Burst), "transcribed": msg.Transcribed,
		})
	}
	if err != nil {
		// Coordination id: wa_bot_turn_end_v1 - the model call failed before any
		// tool ran; the customer gets the generic fallback.
		botCheckpointCtx(ctx, "wa_bot_turn_end_v1", "restaurant_id=%d sender=%s message_id=%s reason=model_call_failed class=%s ms=%d",
			restaurantID, msg.Sender, msg.MessageID, botErrClass(err), time.Since(turnStarted).Milliseconds())
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
	// Coordination id: wa_bot_turn_reply_v1 - classify what the customer got
	// this turn (kind + bounded preview) so a silent or broken reply is visible
	// next to what happened (wa_bot_turn_end_v1) without reading the transcript.
	replyKind, replyText := "tool_delivery", ""
	switch {
	case turn.noticeDelivered:
		replyKind = "server_notice"
	case result.Interrupted:
		replyKind = "interrupted_notice"
	}
	if (result.Interrupted || !botDeliveredReply(result.ToolCalls)) && !turn.noticeDelivered {
		if text := botFinalAssistantText(result.Messages); botPlainTextLeaksInternal(text) {
			// The model typed a contact card or pasted a staff notification as
			// text: deliver the real intro + vCard instead (wa_bot_context_hygiene_v1).
			replyKind = "contact_card_rewritten"
			botCheckpoint("wa_bot_plain_text_rewritten", "restaurant_id=%d sender=%s", restaurantID, msg.Sender)
			_, _ = s.botToolSendContact(sendCtx, restaurantID, msg, tenant, nil)
		} else if text := botFinalAssistantText(result.Messages); text != "" {
			replyKind, replyText = "plain_text", text
			botCheckpoint("wa_bot_plain_text_fallback", "restaurant_id=%d sender=%s", restaurantID, msg.Sender)
			if gw, ok := s.botGatewayFor(sendCtx, restaurantID); ok {
				// wa_bot_outbound_guard_v1: no jargon, no walls of text.
				for _, part := range botOutboundText(text) {
					if s.sendWhatsAppTextTracked(sendCtx, restaurantID, gw, msg.Sender, part, "agent_plain_text") != nil {
						break
					}
				}
			}
		} else {
			replyKind, replyText = "fallback", botFallbackReplyText
			s.botSendFallback(sendCtx, restaurantID, msg.Sender)
		}
	}
	s.botEnsureSpecialDateLink(sendCtx, restaurantID, msg, decision, turn, replyText)
	botCheckpointCtx(ctx, "wa_bot_turn_reply_v1", "restaurant_id=%d sender=%s message_id=%s kind=%s chars=%d preview=%s",
		restaurantID, msg.Sender, msg.MessageID, replyKind, len(replyText), botDiagPreview(replyText, 160))
	endReason := "end_turn"
	switch {
	case result.Exhausted:
		endReason = "exhausted"
	case result.Interrupted:
		endReason = "interrupted"
	}
	// Coordination id: wa_bot_turn_end_v1 - why the turn closed, with how much
	// work it did (iterations, tool calls, model) and how long it took. This is
	// the event that reconstructs a turn together with wa_bot_tool_result_v1.
	botCheckpointCtx(ctx, "wa_bot_turn_end_v1", "restaurant_id=%d sender=%s message_id=%s reason=%s iterations=%d ran=%d failed=%d model=%s node=%s ms=%d tools=%s",
		restaurantID, msg.Sender, msg.MessageID, endReason, result.Iterations, result.Ran, result.Failed, result.ModelUsed, decision.Node, time.Since(turnStarted).Milliseconds(), strings.Join(result.ToolCalls, ","))
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
	"send_poll": true,
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
	gw, ok := s.botGatewayFor(ctx, restaurantID)
	if !ok {
		return
	}
	s.sendWhatsAppTextTracked(ctx, restaurantID, gw, sender, botFallbackReplyText, "agent_fallback")
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
