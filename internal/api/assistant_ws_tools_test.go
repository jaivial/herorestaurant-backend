package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAssistantWS_ToolUseLoop_DB drives a full tool loop over the WebSocket:
// the (fake) MiniMax first answers with a tool_use block, the server executes
// the tool against the DB, then feeds the tool_result back and streams the
// final text turn.
func TestAssistantWS_ToolUseLoop_DB(t *testing.T) {
	db := requireToolTestDB(t)
	ctx := context.Background()
	rid := 9015
	if _, err := db.ExecContext(ctx, `INSERT INTO restaurants (id, slug, name, contact_phone) VALUES (9015, 'forky-tl', 'Tooly', '600000015') ON DUPLICATE KEY UPDATE name=VALUES(name), contact_phone=VALUES(contact_phone)`); err != nil {
		t.Fatalf("seed restaurant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurants WHERE id = 9015`)
	})

	var calls int
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "msg_1", "type": "message", "role": "assistant", "model": "MiniMax-M3",
				"content":     []any{map[string]any{"type": "tool_use", "id": "tu_1", "name": "restaurant_info", "input": map[string]any{}}},
				"stop_reason": "tool_use",
				"usage":       map[string]any{"input_tokens": 10, "output_tokens": 10},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_2", "type": "message", "role": "assistant", "model": "MiniMax-M3",
			"content":     []any{map[string]any{"type": "text", "text": "El restaurante se llama Tooly"}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 10, "output_tokens": 10},
		})
	}))
	defer llm.Close()

	s := &Server{db: db}
	configureAssistantLLM(s, llm.URL)

	a := boAuth{User: boUser{ID: 112244}, Role: "admin", ActiveRestaurantID: rid}
	wsSrv := assistantWSTestServer(s, &a)
	defer wsSrv.Close()

	conn := dialAssistantWS(t, wsSrv)
	defer conn.Close()
	sendAssistantFrame(t, conn, map[string]any{"type": "hello", "session_id": nil})
	hello := readAssistantFrame(t, conn)
	sid := int64(hello["session_id"].(float64))
	defer cleanupAssistant(db, rid)

	sendAssistantFrame(t, conn, map[string]any{"type": "message", "content": "¿cómo se llama el restaurante?"})

	var deltas strings.Builder
	for {
		f := readAssistantFrame(t, conn)
		switch f["type"] {
		case "status", "pong":
		case "delta":
			deltas.WriteString(f["text"].(string))
		case "error":
			t.Fatalf("unexpected error frame: %v", f["message"])
		case "done":
			goto DONE
		}
	}
DONE:
	if deltas.String() != "El restaurante se llama Tooly" {
		t.Fatalf("streamed text = %q", deltas.String())
	}
	if calls != 2 {
		t.Fatalf("expected 2 LLM calls (tool loop), got %d", calls)
	}
	if sid <= 0 {
		t.Fatalf("session id = %d", sid)
	}
}
