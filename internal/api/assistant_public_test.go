package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// assistantPublicWSTestServer mounts the public handler (no auth required).
func assistantPublicWSTestServer(s *Server) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.handlePublicAssistantWS(w, r)
	}))
}

func TestAssistantPublicWS_HelloWithTokenCreatesAndReusesSession_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	rid, cleanup := seedRestaurant(t, db, "forky-pub-"+time.Now().Format("150405.000"))
	defer cleanup()
	defer cleanupAssistant(db, rid)

	srv := assistantPublicWSTestServer(s)
	defer srv.Close()

	conn := dialAssistantWS(t, srv)
	defer conn.Close()

	sendAssistantFrame(t, conn, map[string]any{"type": "hello", "session_id": nil, "session_token": "pub-token-abc123"})
	hello := readAssistantFrame(t, conn)
	if hello["type"] != "hello" {
		t.Fatalf("frame type = %v (%v)", hello["type"], hello["message"])
	}
	sid := int64(hello["session_id"].(float64))
	if sid <= 0 {
		t.Fatalf("session_id = %v", hello["session_id"])
	}

	// The row must be bound to the token with no user.
	var gotToken sql.NullString
	var gotUser sql.NullInt64
	if err := db.QueryRow(`SELECT session_token, user_id FROM assistant_sessions WHERE id = ?`, sid).Scan(&gotToken, &gotUser); err != nil {
		t.Fatalf("select session: %v", err)
	}
	if !gotToken.Valid || gotToken.String != "pub-token-abc123" {
		t.Fatalf("session_token = %v", gotToken)
	}
	if gotUser.Valid {
		t.Fatalf("anonymous session must not have a user, got %v", gotUser.Int64)
	}

	// A second connection with the same token reuses the session.
	conn2 := dialAssistantWS(t, srv)
	defer conn2.Close()
	sendAssistantFrame(t, conn2, map[string]any{"type": "hello", "session_id": nil, "session_token": "pub-token-abc123"})
	hello2 := readAssistantFrame(t, conn2)
	if int64(hello2["session_id"].(float64)) != sid {
		t.Fatalf("token reuse returned different session: %v vs %d", hello2["session_id"], sid)
	}
}

func TestAssistantPublicWS_RateLimit_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	s.cfg.AssistantPublicRateLimit = 3
	rid, cleanup := seedRestaurant(t, db, "forky-rate-"+time.Now().Format("150405.000"))
	defer cleanup()
	defer cleanupAssistant(db, rid)

	sse := fakeAssistantMiniMax(t, []string{"ok"}, nil)
	defer sse.Close()
	configureAssistantLLM(s, sse.URL)

	srv := assistantPublicWSTestServer(s)
	defer srv.Close()

	conn := dialAssistantWS(t, srv)
	defer conn.Close()
	sendAssistantFrame(t, conn, map[string]any{"type": "hello", "session_id": nil, "session_token": "rate-token-xyz"})
	_ = readAssistantFrame(t, conn)

	// 3 allowed messages, then the 4th is rate limited.
	for i := 0; i < 3; i++ {
		sendAssistantFrame(t, conn, map[string]any{"type": "message", "content": "hola"})
		var sawDone bool
		for !sawDone {
			f := readAssistantFrame(t, conn)
			switch f["type"] {
			case "done":
				sawDone = true
			case "error":
				t.Fatalf("unexpected error on allowed message %d: %v", i+1, f["message"])
			}
		}
	}
	sendAssistantFrame(t, conn, map[string]any{"type": "message", "content": "demasiado"})
	f := readAssistantFrame(t, conn)
	if f["type"] != "error" {
		t.Fatalf("expected rate limit error, got %v", f)
	}
	if msg, _ := f["message"].(string); !strings.Contains(strings.ToLower(msg), "rate") {
		t.Fatalf("expected rate limit message, got %q", msg)
	}
}
