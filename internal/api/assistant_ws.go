package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

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

func (c *assistantClient) pingLoop() {
	t := time.NewTicker(25 * time.Second)
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
// history[last N]); message(content) -> status thinking -> delta* -> done;
// ping -> pong; errors as {type:error}. All chat state lives in MySQL.
func (s *Server) handleAssistantWS(w http.ResponseWriter, r *http.Request, requireAuth bool) {
	a, ok := boAuthFromContext(r.Context())
	if requireAuth && !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var auth *boAuth
	if ok {
		auth = &a
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
	c := &assistantClient{s: s, auth: auth, conn: conn, ip: assistantClientIP(r)}
	defer conn.Close()

	go c.pingLoop()

	_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))

		var frame struct {
			Type         string `json:"type"`
			Content      string `json:"content"`
			SessionID    *int64 `json:"session_id"`
			SessionToken string `json:"session_token"`
		}
		if err := json.Unmarshal(raw, &frame); err != nil {
			_ = c.writeJSON(map[string]any{"type": "error", "message": "invalid frame"})
			continue
		}
		switch frame.Type {
		case "hello":
			c.handleHello(r.Context(), frame.SessionID, frame.SessionToken)
		case "message":
			if c.tryStart() {
				go c.handleMessage(r.Context(), frame.Content)
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
		`SELECT role, content FROM (
			SELECT id, role, content FROM assistant_messages WHERE session_id = ? ORDER BY id DESC LIMIT ?
		) t ORDER BY id ASC`, sessionID, c.historyLimit())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []assistantChatMessage
	for rows.Next() {
		var m assistantChatMessage
		if err := rows.Scan(&m.Role, &m.Content); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
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
		histOut = append(histOut, map[string]any{"role": m.Role, "content": m.Content})
	}
	_ = c.writeJSON(map[string]any{"type": "hello", "session_id": sid, "history": histOut})
}

func (c *assistantClient) handleMessage(ctx context.Context, content string) {
	defer c.finishGeneration()
	if strings.TrimSpace(content) == "" {
		_ = c.writeJSON(map[string]any{"type": "error", "message": "empty message"})
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

	if _, err := c.s.db.ExecContext(ctx, `INSERT INTO assistant_messages (session_id, role, content) VALUES (?, 'user', ?)`, sid, content); err != nil {
		_ = c.writeJSON(map[string]any{"type": "error", "message": "failed to persist message"})
		return
	}

	hist, err := c.loadHistory(ctx, sid)
	if err != nil {
		_ = c.writeJSON(map[string]any{"type": "error", "message": "failed to load history"})
		return
	}
	hist = append(hist, assistantChatMessage{Role: "user", Content: content})

	restaurantID := 0
	if c.auth != nil {
		restaurantID = c.auth.ActiveRestaurantID
	} else if rid, ok := restaurantIDFromContext(ctx); ok {
		restaurantID = rid
	}
	prompt := c.s.buildAssistantSystemPrompt(ctx, restaurantID)
	_ = c.writeJSON(map[string]any{"type": "status", "state": "thinking"})
	toolDefs := assistantToolDefs()
	toolMsgs := append([]assistantChatMessage{}, hist...)
	var final strings.Builder
	for turn := 0; turn < 6; turn++ {
		result, callErr := c.s.assistantCall(ctx, prompt, toolMsgs, toolDefs, nil)
		if callErr != nil {
			_ = c.writeJSON(map[string]any{"type": "error", "message": callErr.Error()})
			return
		}
		if result.Text != "" {
			final.WriteString(result.Text)
			_ = c.writeJSON(map[string]any{"type": "delta", "text": result.Text})
		}
		if len(result.ToolUses) == 0 {
			break
		}
		blocks := make([]map[string]any, 0, len(result.ToolUses)+1)
		if result.Text != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": result.Text})
		}
		for _, use := range result.ToolUses {
			blocks = append(blocks, map[string]any{"type": "tool_use", "id": use.ID, "name": use.Name, "input": json.RawMessage(use.Input)})
		}
		toolMsgs = append(toolMsgs, assistantChatMessage{Role: "assistant", Content: blocks})
		results := make([]map[string]any, 0, len(result.ToolUses))
		for _, use := range result.ToolUses {
			// Bound every tool independently so a slow catalog/analytics query cannot
			// consume the whole conversation or hold the websocket indefinitely.
			toolCtx, cancelTool := context.WithTimeout(ctx, 5*time.Second)
			out, toolErr := c.s.assistantExecuteTool(toolCtx, restaurantID, use.Name, use.Input)
			cancelTool()
			if toolErr != nil {
				out = botJSON(map[string]any{"error": toolErr.Error()})
			}
			results = append(results, map[string]any{"type": "tool_result", "tool_use_id": use.ID, "content": out})
		}
		toolMsgs = append(toolMsgs, assistantChatMessage{Role: "user", Content: results})
	}
	if _, err := c.s.db.ExecContext(ctx, `INSERT INTO assistant_messages (session_id, role, content) VALUES (?, 'assistant', ?)`, sid, final.String()); err != nil {
		_ = c.writeJSON(map[string]any{"type": "error", "message": "failed to persist reply"})
		return
	}
	_ = c.writeJSON(map[string]any{"type": "done"})
}
