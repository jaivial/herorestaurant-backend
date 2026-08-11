package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// assistantWSTestServer wraps the handler, injecting boAuth when provided so the
// test does not need a real session cookie. When a is nil the handler runs
// unauthenticated (to exercise the 401 rejection path).
func assistantWSTestServer(s *Server, a *boAuth) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a != nil {
			r = r.WithContext(withBOAuth(r.Context(), *a))
		}
		s.handleBOAssistantWS(w, r)
	}))
}

func assistantWSURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/assistant/ws"
}

// fakeAssistantMiniMax returns an httptest server that speaks the MiniMax SSE
// wire format, emitting one content_block_delta per element of deltas. before,
// when non-nil, runs before streaming (used to block a generation in flight).
func fakeAssistantMiniMax(t *testing.T, deltas []string, before func()) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if before != nil {
			before()
		}
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Error("response writer is not a flusher")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		sseWrite(t, w, fl, map[string]any{"type": "message_start"})
		for _, d := range deltas {
			sseWrite(t, w, fl, map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": d}})
		}
		sseWrite(t, w, fl, map[string]any{"type": "message_stop"})
	}))
}

func configureAssistantLLM(s *Server, baseURL string) {
	s.cfg.MiniMaxAPIKey = "test-key"
	s.cfg.MiniMaxBaseURL = baseURL
	s.cfg.AssistantModel = "MiniMax-M3"
	s.cfg.AssistantMaxTokens = 256
	s.cfg.AssistantTimeout = 5 * time.Second
	if s.cfg.AssistantHistoryLimit == 0 {
		s.cfg.AssistantHistoryLimit = 20
	}
}

func dialAssistantWS(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial(assistantWSURL(srv), nil)
	if err != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		t.Fatalf("dial ws: %v (status %d)", err, code)
	}
	return conn
}

func readAssistantFrame(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal frame %s: %v", raw, err)
	}
	return m
}

func sendAssistantFrame(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	if err := conn.WriteJSON(v); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

func cleanupAssistant(db *sql.DB, restaurantID int) {
	_, _ = db.Exec(`DELETE m FROM assistant_messages m JOIN assistant_sessions s ON m.session_id = s.id WHERE s.restaurant_id = ?`, restaurantID)
	_, _ = db.Exec(`DELETE FROM assistant_sessions WHERE restaurant_id = ?`, restaurantID)
}

func TestAssistantWS_RejectsWithoutSession(t *testing.T) {
	s := &Server{}
	srv := assistantWSTestServer(s, nil) // no boAuth injected
	defer srv.Close()

	_, resp, err := websocket.DefaultDialer.Dial(assistantWSURL(srv), nil)
	if err == nil {
		t.Fatal("expected handshake to fail without session")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		t.Fatalf("expected 401, got %d", code)
	}
}

// An idle connection must survive past the silence deadline: the server pings,
// the client's transport pongs, and the deadline is pushed forward. Before the
// pong handler existed the socket was closed mid-chat and the UI, which never
// saw a done/error frame, stayed "thinking" forever.
func TestAssistantWS_IdleConnectionSurvivesReadTimeout(t *testing.T) {
	s := &Server{assistantKeepalive: assistantKeepaliveConfig{
		readTimeout:  300 * time.Millisecond,
		pingInterval: 100 * time.Millisecond,
	}}
	a := boAuth{User: boUser{ID: 918274}, ActiveRestaurantID: 1}
	srv := assistantWSTestServer(s, &a)
	defer srv.Close()

	conn := dialAssistantWS(t, srv)
	defer conn.Close()

	// Stay silent for several deadlines. gorilla answers pings from within
	// ReadMessage, so a blocking read both keeps the socket alive and observes
	// a close if the server gives up.
	done := make(chan error, 1)
	go func() {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _, err := conn.ReadMessage()
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("connection closed while idle: %v", err)
	case <-time.After(time.Second):
		// Survived >3x the silence deadline: keepalive is working.
	}
}

func TestAssistantWS_HelloCreatesAndReusesSession_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	rid, cleanup := seedRestaurant(t, db, "forky-hello-"+time.Now().Format("150405.000"))
	defer cleanup()
	defer cleanupAssistant(db, rid)

	a := boAuth{User: boUser{ID: 918273}, ActiveRestaurantID: rid}
	srv := assistantWSTestServer(s, &a)
	defer srv.Close()

	conn := dialAssistantWS(t, srv)
	defer conn.Close()

	sendAssistantFrame(t, conn, map[string]any{"type": "hello", "session_id": nil})
	hello := readAssistantFrame(t, conn)
	if hello["type"] != "hello" {
		t.Fatalf("frame type = %v", hello["type"])
	}
	sidF, ok := hello["session_id"].(float64)
	if !ok || sidF <= 0 {
		t.Fatalf("session_id = %v", hello["session_id"])
	}
	sid := int64(sidF)
	if hist, ok := hello["history"].([]any); ok && len(hist) != 0 {
		t.Errorf("new session history should be empty, got %v", hist)
	}

	// The session row must belong to this user + restaurant.
	var gotRid, gotUser int64
	if err := db.QueryRow(`SELECT restaurant_id, user_id FROM assistant_sessions WHERE id = ?`, sid).Scan(&gotRid, &gotUser); err != nil {
		t.Fatalf("select session: %v", err)
	}
	if gotRid != int64(rid) || gotUser != 918273 {
		t.Fatalf("session ownership = rid %d user %d", gotRid, gotUser)
	}

	// Reuse: hello with the same id returns the same session.
	sendAssistantFrame(t, conn, map[string]any{"type": "hello", "session_id": sid})
	hello2 := readAssistantFrame(t, conn)
	if int64(hello2["session_id"].(float64)) != sid {
		t.Fatalf("reuse returned different session: %v vs %d", hello2["session_id"], sid)
	}
}

func TestAssistantWS_MessageFlowPersistsAndStreams_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	rid, cleanup := seedRestaurant(t, db, "forky-msg-"+time.Now().Format("150405.000"))
	defer cleanup()
	defer cleanupAssistant(db, rid)

	sse := fakeAssistantMiniMax(t, []string{"Hola, ", "soy Forky"}, nil)
	defer sse.Close()
	configureAssistantLLM(s, sse.URL)

	a := boAuth{User: boUser{ID: 112233}, ActiveRestaurantID: rid}
	srv := assistantWSTestServer(s, &a)
	defer srv.Close()

	conn := dialAssistantWS(t, srv)
	defer conn.Close()

	sendAssistantFrame(t, conn, map[string]any{"type": "hello", "session_id": nil})
	hello := readAssistantFrame(t, conn)
	sid := int64(hello["session_id"].(float64))

	sendAssistantFrame(t, conn, map[string]any{"type": "message", "content": "¿cómo estás?"})

	var sawThinking bool
	var deltas strings.Builder
	for {
		f := readAssistantFrame(t, conn)
		switch f["type"] {
		case "status":
			if f["state"] == "thinking" {
				sawThinking = true
			}
		case "delta":
			deltas.WriteString(f["text"].(string))
		case "error":
			t.Fatalf("unexpected error frame: %v", f["message"])
		case "done":
			goto DONE
		}
	}
DONE:
	if !sawThinking {
		t.Error("expected a status:thinking frame before deltas")
	}
	if deltas.String() != "Hola, soy Forky" {
		t.Errorf("streamed assistant text = %q", deltas.String())
	}

	// Both turns persisted, oldest-first.
	rows, err := db.Query(`SELECT role, content FROM assistant_messages WHERE session_id = ? ORDER BY id`, sid)
	if err != nil {
		t.Fatalf("select messages: %v", err)
	}
	defer rows.Close()
	type msg struct{ role, content string }
	var got []msg
	for rows.Next() {
		var m msg
		if err := rows.Scan(&m.role, &m.content); err != nil {
			t.Fatal(err)
		}
		got = append(got, m)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 persisted messages, got %d: %+v", len(got), got)
	}
	if got[0].role != "user" || got[0].content != "¿cómo estás?" {
		t.Errorf("user row = %+v", got[0])
	}
	if got[1].role != "assistant" || got[1].content != "Hola, soy Forky" {
		t.Errorf("assistant row = %+v", got[1])
	}
}

func TestAssistantWS_BusyWhileInFlight_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	rid, cleanup := seedRestaurant(t, db, "forky-busy-"+time.Now().Format("150405.000"))
	defer cleanup()
	defer cleanupAssistant(db, rid)

	release := make(chan struct{})
	var once bool
	sse := fakeAssistantMiniMax(t, []string{"listo"}, func() {
		if !once { // block only the first generation
			once = true
			<-release
		}
	})
	defer sse.Close()
	configureAssistantLLM(s, sse.URL)

	a := boAuth{User: boUser{ID: 445566}, ActiveRestaurantID: rid}
	srv := assistantWSTestServer(s, &a)
	defer srv.Close()

	conn := dialAssistantWS(t, srv)
	defer conn.Close()

	sendAssistantFrame(t, conn, map[string]any{"type": "hello", "session_id": nil})
	_ = readAssistantFrame(t, conn) // hello

	sendAssistantFrame(t, conn, map[string]any{"type": "message", "content": "primera"})
	// First frame of the in-flight generation is status:thinking; after this the
	// generation is blocked inside the LLM call.
	f := readAssistantFrame(t, conn)
	if f["type"] != "status" {
		t.Fatalf("expected status frame first, got %v", f)
	}

	// Second message while busy must be rejected.
	sendAssistantFrame(t, conn, map[string]any{"type": "message", "content": "segunda"})
	busy := readAssistantFrame(t, conn)
	if busy["type"] != "error" {
		t.Fatalf("expected error frame while busy, got %v", busy)
	}
	if msg, _ := busy["message"].(string); !strings.Contains(strings.ToLower(msg), "busy") {
		t.Errorf("expected busy error, got %q", msg)
	}

	close(release)
	// Drain until the first generation completes.
	for {
		f := readAssistantFrame(t, conn)
		if f["type"] == "done" {
			break
		}
	}

	// Only the first user turn + its assistant reply persisted (the busy turn was
	// rejected before any DB write).
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM assistant_messages m JOIN assistant_sessions s ON m.session_id = s.id WHERE s.restaurant_id = ?`, rid).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 persisted messages, got %d", count)
	}
}

func TestAssistantWS_HistoryLimitTruncation_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	s.cfg.AssistantHistoryLimit = 2
	rid, cleanup := seedRestaurant(t, db, "forky-hist-"+time.Now().Format("150405.000"))
	defer cleanup()
	defer cleanupAssistant(db, rid)

	a := boAuth{User: boUser{ID: 778899}, ActiveRestaurantID: rid}
	srv := assistantWSTestServer(s, &a)
	defer srv.Close()

	conn := dialAssistantWS(t, srv)
	defer conn.Close()

	sendAssistantFrame(t, conn, map[string]any{"type": "hello", "session_id": nil})
	sid := int64(readAssistantFrame(t, conn)["session_id"].(float64))

	// Seed four messages directly.
	for _, m := range []struct{ role, content string }{
		{"user", "u1"}, {"assistant", "a1"}, {"user", "u2"}, {"assistant", "a2"},
	} {
		if _, err := db.Exec(`INSERT INTO assistant_messages (session_id, role, content) VALUES (?, ?, ?)`, sid, m.role, m.content); err != nil {
			t.Fatalf("seed message: %v", err)
		}
	}

	conn2 := dialAssistantWS(t, srv)
	defer conn2.Close()
	sendAssistantFrame(t, conn2, map[string]any{"type": "hello", "session_id": sid})
	hello := readAssistantFrame(t, conn2)
	hist, ok := hello["history"].([]any)
	if !ok {
		t.Fatalf("history missing: %v", hello)
	}
	if len(hist) != 2 {
		t.Fatalf("expected last 2 messages, got %d: %v", len(hist), hist)
	}
	first, _ := hist[0].(map[string]any)
	last, _ := hist[1].(map[string]any)
	if first["role"] != "user" || first["content"] != "u2" {
		t.Errorf("history[0] = %v", first)
	}
	if last["role"] != "assistant" || last["content"] != "a2" {
		t.Errorf("history[1] = %v", last)
	}
}
