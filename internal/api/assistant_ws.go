package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// assistantClient is one Forky assistant WebSocket connection.
type assistantClient struct {
	s            *Server
	auth         *boAuth // nil for anonymous public-site sessions
	conn         *websocket.Conn
	mu           sync.Mutex // guards writes + busy
	busy         bool       // one generation in flight
	ip           string
	sessionID    int64
	sessionInit  bool
	restaurantID int
	// Keepalive timings, per connection so tests can shorten them without
	// mutating shared state.
	readTimeout  time.Duration
	pingInterval time.Duration
}

// assistantClientIP extracts the caller IP for anonymous rate limiting.
func assistantClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (c *assistantClient) writeJSON(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(7 * time.Second))
	return c.conn.WriteJSON(v)
}

// assistantReadTimeout bounds how long a connection may stay silent. Pongs to
// our own pings refresh it, so a healthy socket survives long generations and
// idle gaps between questions; a dead peer is still reaped.
// assistantPingInterval must stay well below it.
const (
	assistantReadTimeout  = 90 * time.Second
	assistantPingInterval = 25 * time.Second
)

func (c *assistantClient) pingLoop() {
	t := time.NewTicker(c.pingInterval)
	defer t.Stop()
	for range t.C {
		c.mu.Lock()
		err := c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(7*time.Second))
		c.mu.Unlock()
		if err != nil {
			_ = c.conn.Close()
			return
		}
	}
}

// extendReadDeadline pushes the silence deadline forward.
func (c *assistantClient) extendReadDeadline() {
	_ = c.conn.SetReadDeadline(time.Now().Add(c.readTimeout))
}

// handleBOAssistantWS serves GET /api/admin/assistant/ws (session auth).
func (s *Server) handleBOAssistantWS(w http.ResponseWriter, r *http.Request) {
	s.handleAssistantWS(w, r, true)
}

// handlePublicAssistantWS serves GET /api/assistant/ws for the public site.
// No session required: the client identifies itself with a session_token.
func (s *Server) handlePublicAssistantWS(w http.ResponseWriter, r *http.Request) {
	s.handleAssistantWS(w, r, false)
}

// handleAssistantWS is the shared WebSocket chat handler. Protocol (JSON text
// frames): hello(session_id|null[, session_token]) -> hello(session_id,
// history[last N]); message(content[, attachments][, audio]) -> status thinking
// -> delta* -> done; ping -> pong; errors as {type:error}. All chat state lives
// in MySQL.
func (s *Server) handleAssistantWS(w http.ResponseWriter, r *http.Request, requireAuth bool) {
	a, ok := boAuthFromContext(r.Context())
	if requireAuth && !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var auth *boAuth
	wsCtx := r.Context()
	if ok {
		auth = &a
		// Preserve the authenticated principal for goroutines handling messages.
		// Without this, tool authorization sees an anonymous context even though
		// the WebSocket handshake was authenticated.
		wsCtx = withBOAuth(wsCtx, a)
		// The person's own session cookie, for admin_call to replay panel routes AS them
		// through the real gates. Server-side only: never sent to the model. [FORKY-ADMIN-TOOLS-S01]
		if c, err := r.Cookie(boSessionCookieName); err == nil && strings.TrimSpace(c.Value) != "" {
			wsCtx = withBOSessionToken(wsCtx, strings.TrimSpace(c.Value))
		}
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		// The public endpoint is anonymous (token + IP rate limit, no cookies),
		// so origin restrictions are meaningless there; the admin endpoint keeps
		// the strict backoffice origin check.
		CheckOrigin: func(r *http.Request) bool {
			return !requireAuth || s.allowBOWebSocketOrigin(r)
		},
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &assistantClient{
		s:            s,
		auth:         auth,
		conn:         conn,
		ip:           assistantClientIP(r),
		readTimeout:  assistantReadTimeout,
		pingInterval: assistantPingInterval,
	}
	if s.assistantKeepalive.readTimeout > 0 {
		c.readTimeout = s.assistantKeepalive.readTimeout
		c.pingInterval = s.assistantKeepalive.pingInterval
	}
	defer conn.Close()

	go c.pingLoop()

	// Keep the connection alive while the peer is responsive: every pong (and
	// every client frame) refreshes the deadline. Without this the socket died
	// mid-answer on slow tool turns and while the chat sat idle.
	conn.SetPongHandler(func(string) error {
		c.extendReadDeadline()
		return nil
	})
	c.extendReadDeadline()
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		c.extendReadDeadline()

		var frame struct {
			Type         string                   `json:"type"`
			Content      string                   `json:"content"`
			SessionID    *int64                   `json:"session_id"`
			SessionToken string                   `json:"session_token"`
			Attachments  []assistantAttachmentRef `json:"attachments"`
			Audio        *assistantVoiceAudio     `json:"audio"`
		}
		if err := json.Unmarshal(raw, &frame); err != nil {
			_ = c.writeJSON(map[string]any{"type": "error", "message": "invalid frame"})
			continue
		}
		switch frame.Type {
		case "hello":
			c.handleHello(wsCtx, frame.SessionID, frame.SessionToken)
		case "message":
			if c.tryStart() {
				go c.handleMessage(wsCtx, frame.Content, frame.Attachments, frame.Audio)
			} else {
				_ = c.writeJSON(map[string]any{"type": "error", "message": "busy: espera a que termine la respuesta anterior"})
			}
		case "ping":
			_ = c.writeJSON(map[string]any{"type": "pong"})
		default:
			_ = c.writeJSON(map[string]any{"type": "error", "message": "unknown frame type"})
		}
	}
}

// tryStart claims the single in-flight generation slot; false when busy.
func (c *assistantClient) tryStart() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.busy {
		return false
	}
	c.busy = true
	return true
}

func (c *assistantClient) finishGeneration() {
	c.mu.Lock()
	c.busy = false
	c.mu.Unlock()
}

// assistantRateBucket is one IP's message counter within a one-minute window.
type assistantRateBucket struct {
	count int
	start time.Time
}

// assistantRateLimit reports whether the IP may send another public message
// (in-memory sliding window; applies to anonymous sessions only).
func (s *Server) assistantRateLimit(ip string) bool {
	if ip == "" {
		return true
	}
	limit := s.cfg.AssistantPublicRateLimit
	if limit <= 0 {
		limit = 20
	}
	s.assistantRateMu.Lock()
	defer s.assistantRateMu.Unlock()
	if s.assistantRateBuckets == nil {
		s.assistantRateBuckets = make(map[string]*assistantRateBucket)
	}
	b, ok := s.assistantRateBuckets[ip]
	now := time.Now()
	if !ok || now.Sub(b.start) > time.Minute {
		s.assistantRateBuckets[ip] = &assistantRateBucket{count: 1, start: now}
		return true
	}
	b.count++
	return b.count <= limit
}

// historyLimit returns the configured context history window (default 20).
func (c *assistantClient) historyLimit() int {
	if c.s.cfg.AssistantHistoryLimit > 0 {
		return c.s.cfg.AssistantHistoryLimit
	}
	return 20
}

func (c *assistantClient) loadHistory(ctx context.Context, sessionID int64) ([]assistantChatMessage, error) {
	rows, err := c.s.db.QueryContext(ctx,
		`SELECT role, content, COALESCE(audio_url, ''), COALESCE(audio_duration_ms, 0) FROM (
			SELECT id, role, content, audio_url, audio_duration_ms FROM assistant_messages WHERE session_id = ? ORDER BY id DESC LIMIT ?
		) t ORDER BY id ASC`, sessionID, c.historyLimit())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []assistantChatMessage
	for rows.Next() {
		// Scan into explicit strings: scanning TEXT into the interface Content
		// yields []byte, and the hello frame's JSON marshal would emit it as
		// base64 (assistant replies were rescued by the client-side decoder,
		// but user messages rendered as raw base64 blobs).
		var role, content, audioURL string
		var audioDurationMS int64
		if err := rows.Scan(&role, &content, &audioURL, &audioDurationMS); err != nil {
			return nil, err
		}
		out = append(out, assistantChatMessage{Role: role, Content: content, AudioURL: audioURL, AudioDurationMS: audioDurationMS})
	}
	// Contaminated rows already in the database keep poisoning their session on
	// every replay, so filter them out of the context we send to the model.
	// The hello frame reuses this, so the UI stops replaying them too.
	return assistantFilterHistory(out), rows.Err()
}

func (c *assistantClient) handleHello(ctx context.Context, sessionID *int64, sessionToken string) {
	sid := int64(0)
	if c.auth == nil {
		// Anonymous public-site session identified by a client-generated token.
		token := strings.TrimSpace(sessionToken)
		if len(token) < 8 || len(token) > 64 {
			_ = c.writeJSON(map[string]any{"type": "error", "message": "session_token required"})
			return
		}
		err := c.s.db.QueryRowContext(ctx, `SELECT id FROM assistant_sessions WHERE session_token = ?`, token).Scan(&sid)
		if err == sql.ErrNoRows {
			res, err := c.s.db.ExecContext(ctx, `INSERT INTO assistant_sessions (session_token) VALUES (?)`, token)
			if err != nil {
				_ = c.writeJSON(map[string]any{"type": "error", "message": "failed to create session"})
				return
			}
			sid, _ = res.LastInsertId()
		} else if err != nil {
			_ = c.writeJSON(map[string]any{"type": "error", "message": "session lookup failed"})
			return
		}
	} else if sessionID != nil && *sessionID > 0 {
		var rid int
		err := c.s.db.QueryRowContext(ctx, `SELECT restaurant_id FROM assistant_sessions WHERE id = ? AND user_id = ?`, *sessionID, c.auth.User.ID).Scan(&rid)
		if err != nil || rid != c.auth.ActiveRestaurantID {
			_ = c.writeJSON(map[string]any{"type": "error", "message": "session not found"})
			return
		}
		sid = *sessionID
	} else {
		res, err := c.s.db.ExecContext(ctx, `INSERT INTO assistant_sessions (restaurant_id, user_id) VALUES (?, ?)`, c.auth.ActiveRestaurantID, c.auth.User.ID)
		if err != nil {
			_ = c.writeJSON(map[string]any{"type": "error", "message": "failed to create session"})
			return
		}
		sid, _ = res.LastInsertId()
	}

	c.sessionID = sid
	c.sessionInit = true
	if c.auth != nil {
		c.restaurantID = c.auth.ActiveRestaurantID
	} else {
		_ = c.s.db.QueryRowContext(ctx, `SELECT COALESCE(restaurant_id, 0) FROM assistant_sessions WHERE id=?`, sid).Scan(&c.restaurantID)
	}

	hist, err := c.loadHistory(ctx, sid)
	if err != nil {
		_ = c.writeJSON(map[string]any{"type": "error", "message": "failed to load history"})
		return
	}
	histOut := make([]map[string]any, 0, len(hist))
	for _, m := range hist {
		entry := map[string]any{"role": m.Role, "content": m.Content}
		// Only voice turns carry the clip: plain text rows keep the frame lean.
		if m.AudioURL != "" {
			entry["audio_url"] = m.AudioURL
			entry["audio_duration_ms"] = m.AudioDurationMS
		}
		histOut = append(histOut, entry)
	}
	_ = c.writeJSON(map[string]any{"type": "hello", "session_id": sid, "history": histOut})
}

// assistantMaxToolRounds bounds the model <-> tools rounds of one user
// message. 6 was too few for a real multi-step request: "create the campaign
// and design it with AI" needs preview + confirm for each write plus the reads
// the model makes to avoid inventing content (menus, carta), and the turn
// ended half done (2026-10-27, campaign_tools_v1). Each round is one model
// call; 10 covers those flows and still stops a model that loops.
const assistantMaxToolRounds = 10

func (c *assistantClient) handleMessage(ctx context.Context, content string, attachments []assistantAttachmentRef, audio *assistantVoiceAudio) {
	defer c.finishGeneration()
	if strings.TrimSpace(content) == "" && len(attachments) == 0 {
		_ = c.writeJSON(map[string]any{"type": "error", "message": "empty message"})
		return
	}

	// Attachments are validated before anything else so a bad frame fails fast
	// with an honest, actionable error (forky_attachments_v1). The (slower)
	// Bunny downloads happen after the rate-limit and session gates below.
	if err := validateAssistantAttachmentRefs(c.restaurantID, attachments); err != nil {
		_ = c.writeJSON(map[string]any{"type": "error", "message": "adjunto rechazado: " + err.Error()})
		return
	}

	// The voice note's Bunny URL is validated with the same fail-fast rule and
	// the same tenant boundary as an attachment ref: the row must never point
	// its player at another restaurant's object (forky_media_transcribe_v1).
	audioURL, audioDurationMS, err := c.s.validateAssistantVoiceAudio(ctx, c.restaurantID, audio)
	if err != nil {
		_ = c.writeJSON(map[string]any{"type": "error", "message": err.Error()})
		return
	}

	if !c.s.assistantRateLimit(c.ip) {
		_ = c.writeJSON(map[string]any{"type": "error", "message": "rate_limited: demasiados mensajes, espera un momento"})
		return
	}

	// Ensure a session exists (hello may have been skipped).
	var sid int64
	if c.sessionInit {
		sid = c.sessionID
	} else if c.auth == nil {
		_ = c.writeJSON(map[string]any{"type": "error", "message": "no session: envía hello con session_token primero"})
		return
	} else {
		err := c.s.db.QueryRowContext(ctx, `SELECT id FROM assistant_sessions WHERE restaurant_id = ? AND user_id = ? ORDER BY id DESC LIMIT 1`, c.auth.ActiveRestaurantID, c.auth.User.ID).Scan(&sid)
		if err == sql.ErrNoRows {
			_ = c.writeJSON(map[string]any{"type": "error", "message": "no session: envía hello primero"})
			return
		}
		if err != nil {
			_ = c.writeJSON(map[string]any{"type": "error", "message": "session lookup failed"})
			return
		}
	}

	// Re-read the attachment bytes server-side and build the model content
	// blocks. A failure here fails the turn: the model is never told about a
	// file it did not actually receive. Runs after the rate-limit and session
	// gates so a rejected client never causes Bunny traffic.
	attachmentBlocks := make([]assistantAttachmentBlock, 0, len(attachments))
	for _, ref := range attachments {
		block, err := c.s.buildAssistantAttachmentBlock(ctx, c.restaurantID, ref)
		if err != nil {
			_ = c.writeJSON(map[string]any{"type": "error", "message": err.Error()})
			return
		}
		attachmentBlocks = append(attachmentBlocks, block)
	}
	userContentBlocks, userContentPersisted := assistantAttachmentsToUserBlocks(content, attachmentBlocks)
	if note := assistantAttachmentRefsNote(attachmentBlocks); note != "" {
		userContentBlocks = append(userContentBlocks, map[string]any{"type": "text", "text": note})
	}

	// The clip's Bunny URL rides the row so a reload replays it at this exact
	// message; NULLIF keeps a plain text turn at NULL instead of ''.
	if _, err := c.s.db.ExecContext(ctx,
		`INSERT INTO assistant_messages (session_id, role, content, audio_url, audio_duration_ms) VALUES (?, 'user', ?, NULLIF(?, ''), ?)`,
		sid, userContentPersisted, audioURL, audioDurationMS); err != nil {
		_ = c.writeJSON(map[string]any{"type": "error", "message": "failed to persist message"})
		return
	}

	hist, err := c.loadHistory(ctx, sid)
	if err != nil {
		_ = c.writeJSON(map[string]any{"type": "error", "message": "failed to load history"})
		return
	}

	// The current turn carries the real attachment content blocks; older turns
	// come back from the DB as the persisted manifest text.
	hist = append(hist, assistantChatMessage{Role: "user", Content: userContentBlocks})

	restaurantID := 0
	if c.auth != nil {
		restaurantID = c.auth.ActiveRestaurantID
	} else if rid, ok := restaurantIDFromContext(ctx); ok {
		restaurantID = rid
	} else {
		restaurantID = c.restaurantID
	}
	if restaurantID <= 0 && sid > 0 {
		_ = c.s.db.QueryRowContext(ctx, `SELECT COALESCE(restaurant_id,0) FROM assistant_sessions WHERE id=?`, sid).Scan(&restaurantID)
	}
	prompt := c.s.buildAssistantSystemPrompt(ctx, restaurantID)
	if c.auth != nil {
		prompt += assistantMemoryPromptBlock(c.s.loadAssistantMemories(ctx, restaurantID, c.auth.User.ID, 15))
	}
	_ = c.writeJSON(map[string]any{"type": "status", "state": "thinking"})
	toolDefs := assistantToolDefs()
	toolMsgs := append([]assistantChatMessage{}, hist...)
	// campaign_tools_v1: one id per user message, so a tool can require that a
	// confirmation arrives in a LATER message (a human reply), not in the same
	// model turn that produced the preview.
	turnID := uuid.NewString()
	ranTools := map[string]bool{}
	var final strings.Builder
	var lastRoundHadError bool
	exhausted := true
	for turn := 0; turn < assistantMaxToolRounds; turn++ {
		result, callErr := c.s.assistantCall(ctx, restaurantID, prompt, toolMsgs, toolDefs, nil)
		if callErr != nil {
			_ = c.writeJSON(map[string]any{"type": "error", "message": callErr.Error()})
			return
		}
		// MiniMax occasionally wraps the whole reply in base64 (an encoding quirk
		// that persists to the DB). Recover the readable text before streaming it to
		// the client and before persisting, so the chat never shows a raw blob.
		turnText := assistantRecoverEncodedReply(result.Text)
		if turnText != "" {
			if final.Len() > 0 {
				final.WriteString("\n\n")
			}
			final.WriteString(turnText)
			_ = c.writeJSON(map[string]any{"type": "delta", "text": turnText})
		}
		if len(result.ToolUses) == 0 {
			exhausted = false
			break
		}
		blocks := make([]map[string]any, 0, len(result.ToolUses)+1)
		if turnText != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": turnText})
		}
		for _, use := range result.ToolUses {
			blocks = append(blocks, map[string]any{"type": "tool_use", "id": use.ID, "name": use.Name, "input": json.RawMessage(use.Input)})
		}
		toolMsgs = append(toolMsgs, assistantChatMessage{Role: "assistant", Content: blocks})
		results := make([]map[string]any, 0, len(result.ToolUses))
		lastRoundHadError = false
		for _, use := range result.ToolUses {
			// Live trace for the chat (ThinkingState rows): the tool name only, never the
			// input (it can carry customer data). [FORKY-ADMIN-TOOLS-S01]
			_ = c.writeJSON(map[string]any{"type": "status", "state": "tool", "tool": use.Name})
			// Bound every tool independently so a slow catalog/analytics query cannot
			// consume the whole conversation or hold the websocket indefinitely.
			toolCtx, cancelTool := context.WithTimeout(withAssistantTurn(ctx, sid, turnID), assistantToolTimeout(use.Name))
			out, toolErr := c.s.assistantExecuteTool(toolCtx, restaurantID, use.Name, use.Input)
			cancelTool()
			ranTools[use.Name] = true
			if toolErr != nil {
				out = botJSON(map[string]any{"error": toolErr.Error()})
			}
			if toolErr != nil || strings.Contains(out, "\"error\"") {
				lastRoundHadError = true
			}
			results = append(results, map[string]any{"type": "tool_result", "tool_use_id": use.ID, "content": out})
		}
		toolMsgs = append(toolMsgs, assistantChatMessage{Role: "user", Content: results})
	}
	// If the turn budget ran out with the model still requesting tools, close
	// honestly instead of letting the turn dangle. Two cases:
	//  - nothing was streamed (final empty): full closing message;
	//  - an opener was streamed ("busco una alternativa...") but the
	//    investigation never resolved: append a short closure so neither the
	//    live client nor the persisted history is left mid-promise. Hypothesis
	//    (consistent with QA, not proven): a dangling opener primed the NEXT
	//    turn to keep answering the previous question.
	// Budget exhaustion vs tool failure get different wording; neither claims
	// missing permissions (a 403 may be plan/capability, not the user role).
	if exhausted {
		if final.Len() == 0 {
			msg := "No he podido completar eso en los pasos disponibles. Prueba con una peticion mas concreta o dame un poco mas de contexto."
			if lastRoundHadError {
				msg = "No he podido completarlo: una de las consultas necesarias no esta disponible ahora mismo. Prueba con otra cosa o dame un poco mas de contexto."
			}
			final.WriteString(msg)
			_ = c.writeJSON(map[string]any{"type": "delta", "text": msg})
		} else {
			msg := "No he podido terminar la comprobacion completa en los pasos disponibles; arriba esta lo que he podido averiguar."
			if lastRoundHadError {
				msg = "No he podido terminar la comprobacion: una de las consultas necesarias no esta disponible ahora mismo; arriba esta lo que he podido averiguar."
			}
			final.WriteString("\n\n" + msg)
			_ = c.writeJSON(map[string]any{"type": "delta", "text": "\n\n" + msg})
		}
	}
	// campaign_tools_v1: a campaign action claimed in a turn where no tool ran
	// at all is flagged to the user (and in history, so it does not prime the
	// next turn as if it had happened).
	if campaignUnbackedClaim(content, final.String(), ranTools) {
		final.WriteString(campaignUnbackedClaimNotice)
		_ = c.writeJSON(map[string]any{"type": "delta", "text": campaignUnbackedClaimNotice})
		slog.Default().Warn("campaign.tools.unbacked_claim", "restaurant_id", restaurantID, "session_id", sid)
	}
	// Never persist an unusable reply: it would come back as history on the next
	// turn and prime the model to produce more of the same. Dropping it keeps
	// the session clean, and the user simply sees the (already streamed) text
	// without it contaminating future context.
	reply := assistantNormalizeRichBlocks(assistantSanitizeForHistory(final.String()), toolMsgs)
	if strings.TrimSpace(reply) != "" {
		if _, err := c.s.db.ExecContext(ctx, `INSERT INTO assistant_messages (session_id, role, content) VALUES (?, 'assistant', ?)`, sid, reply); err != nil {
			_ = c.writeJSON(map[string]any{"type": "error", "message": "failed to persist reply"})
			return
		}
	}
	if c.auth != nil {
		c.s.kickAssistantMemoryExtraction(ctx, restaurantID, c.auth.User.ID, content, final.String())
	}

	_ = c.writeJSON(map[string]any{"type": "done"})
}
