package api

// End-to-end WebSocket test for chat attachments (forky_attachments_v1):
// a message frame carrying attachment refs must download the bytes from
// Bunny, hand real content blocks to the LLM, and persist a manifest (never
// binary) as the user message. Requires TEST_DB_DSN like the other _DB tests.

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAssistantWS_MessageWithAttachment_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	rid, cleanup := seedRestaurant(t, db, "forky-att-"+time.Now().Format("150405.000"))
	defer cleanup()
	defer cleanupAssistant(db, rid)

	// Storage stub: serves the "uploaded" object on any path.
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatal(err)
	}
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("AccessKey") == "" {
			t.Error("bunnyGet must authenticate with the storage AccessKey")
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer storage.Close()
	s.cfg.BunnyStorageBaseURL = storage.URL
	s.cfg.BunnyStorageZone = "testzone"
	s.cfg.BunnyStorageKey = "test-key"

	// LLM stub: captures the request so the test asserts the model really
	// received the image block next to the question. Answers in plain JSON
	// (the non-streaming tool-turn path of assistantCall).
	var llmBody map[string]any
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &llmBody); err != nil {
			t.Errorf("llm body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_test", "type": "message", "role": "assistant",
			"model": "MiniMax-M3", "stop_reason": "end_turn",
			"content": []map[string]any{{"type": "text", "text": "Veo una imagen"}},
			"usage":   map[string]any{"input_tokens": 10, "output_tokens": 3},
		})
	}))
	defer llm.Close()
	configureAssistantLLM(s, llm.URL)

	a := boAuth{User: boUser{ID: 445566}, ActiveRestaurantID: rid}
	srv := assistantWSTestServer(s, &a)
	defer srv.Close()

	conn := dialAssistantWS(t, srv)
	defer conn.Close()

	sendAssistantFrame(t, conn, map[string]any{"type": "hello", "session_id": nil})
	hello := readAssistantFrame(t, conn)
	sid := int64(hello["session_id"].(float64))

	sendAssistantFrame(t, conn, map[string]any{
		"type":    "message",
		"content": "¿Qué hay en la imagen?",
		"attachments": []map[string]any{{
			"name":       "foto.png",
			"media_type": "image/png",
			"size":       len(png),
			"url":        "https://pull.example/forky/" + strconv.Itoa(rid) + "/x.png",
			"path":       "forky/" + strconv.Itoa(rid) + "/x.png",
		}},
	})

	var deltas strings.Builder
	for {
		f := readAssistantFrame(t, conn)
		switch f["type"] {
		case "delta":
			deltas.WriteString(f["text"].(string))
		case "error":
			t.Fatalf("unexpected error frame: %v", f["message"])
		case "done":
			goto DONE
		}
	}
DONE:
	if deltas.String() != "Veo una imagen" {
		t.Errorf("assistant reply = %q", deltas.String())
	}

	// The model request must carry the image block after the question text.
	msgs, ok := llmBody["messages"].([]any)
	if !ok || len(msgs) == 0 {
		t.Fatalf("llm messages = %v", llmBody["messages"])
	}
	last := msgs[len(msgs)-1].(map[string]any)
	if last["role"] != "user" {
		t.Fatalf("last role = %v", last["role"])
	}
	content := last["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("user content blocks = %d, want text+image", len(content))
	}
	if content[0].(map[string]any)["type"] != "text" {
		t.Fatalf("first block = %v", content[0])
	}
	imgBlock := content[1].(map[string]any)
	if imgBlock["type"] != "image" {
		t.Fatalf("second block = %v", imgBlock)
	}
	src := imgBlock["source"].(map[string]any)
	if src["media_type"] != "image/png" || src["data"] == "" {
		t.Fatalf("image source = %v", src)
	}

	// The persisted user message is the question + manifest, never binary.
	var stored string
	if err := db.QueryRow(`SELECT content FROM assistant_messages WHERE session_id = ? AND role = 'user' ORDER BY id DESC LIMIT 1`, sid).Scan(&stored); err != nil {
		t.Fatalf("select user message: %v", err)
	}
	if !strings.HasPrefix(stored, "¿Qué hay en la imagen?") || !strings.Contains(stored, "[Adjunto: foto.png (image/png,") {
		t.Errorf("persisted user message = %q", stored)
	}
	if strings.Contains(stored, "iVBORw0") {
		t.Error("binary payload leaked into the persisted message")
	}
}

func TestAssistantWS_MessageWithBadAttachmentFails_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	rid, cleanup := seedRestaurant(t, db, "forky-att-bad-"+time.Now().Format("150405.000"))
	defer cleanup()
	defer cleanupAssistant(db, rid)

	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_test", "type": "message", "role": "assistant",
			"model": "MiniMax-M3", "stop_reason": "end_turn",
			"content": []map[string]any{{"type": "text", "text": "no debería llegar"}},
			"usage":   map[string]any{"input_tokens": 10, "output_tokens": 3},
		})
	}))
	defer llm.Close()
	configureAssistantLLM(s, llm.URL)

	// Storage stub that 404s: the object is gone.
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer storage.Close()
	s.cfg.BunnyStorageBaseURL = storage.URL
	s.cfg.BunnyStorageZone = "testzone"
	s.cfg.BunnyStorageKey = "test-key"

	a := boAuth{User: boUser{ID: 445567}, ActiveRestaurantID: rid}
	srv := assistantWSTestServer(s, &a)
	defer srv.Close()

	conn := dialAssistantWS(t, srv)
	defer conn.Close()

	sendAssistantFrame(t, conn, map[string]any{"type": "hello", "session_id": nil})
	readAssistantFrame(t, conn)

	sendAssistantFrame(t, conn, map[string]any{
		"type":    "message",
		"content": "mensaje con adjunto perdido",
		"attachments": []map[string]any{{
			"name":       "perdido.png",
			"media_type": "image/png",
			"size":       10,
			"url":        "https://pull.example/forky/" + strconv.Itoa(rid) + "/gone.png",
			"path":       "forky/" + strconv.Itoa(rid) + "/gone.png",
		}},
	})

	var sawHonestError bool
	for {
		f := readAssistantFrame(t, conn)
		switch f["type"] {
		case "error":
			msg, _ := f["message"].(string)
			if !strings.Contains(msg, "perdido.png") {
				t.Fatalf("error frame should name the file: %v", msg)
			}
			sawHonestError = true
			goto ERRDONE
		case "done":
			t.Fatal("turn must not complete when the attachment cannot be read")
		}
	}
ERRDONE:
	if !sawHonestError {
		t.Fatal("expected an honest error frame")
	}
	// Nothing persisted: the turn never reached the model.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM assistant_messages m JOIN assistant_sessions s ON m.session_id = s.id WHERE s.restaurant_id = ?`, rid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("persisted %d messages for a failed attachment turn", n)
	}
}
