package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"preactvillacarmen/internal/config"
)

func newAssistantTestServer(baseURL string) *Server {
	return &Server{
		cfg: config.Config{
			MiniMaxAPIKey:         "test-key",
			MiniMaxBaseURL:        baseURL,
			MiniMaxModel:          "MiniMax-M3",
			AssistantModel:        "MiniMax-M3",
			AssistantTimeout:      5 * time.Second,
			AssistantMaxTokens:    512,
			AssistantHistoryLimit: 20,
		},
	}
}

// sseFlush writes one SSE `data:` frame and flushes it so the client parser can
// consume it incrementally.
func sseWrite(t *testing.T, w http.ResponseWriter, fl http.Flusher, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal sse payload: %v", err)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
		t.Fatalf("write sse: %v", err)
	}
	fl.Flush()
}

func TestAssistantStream_RequestShapeAndDeltas(t *testing.T) {
	var gotReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotReq)

		fl, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer is not a flusher")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		sseWrite(t, w, fl, map[string]any{"type": "message_start"})
		sseWrite(t, w, fl, map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": "Hola"}})
		sseWrite(t, w, fl, map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": " mundo"}})
		sseWrite(t, w, fl, map[string]any{"type": "message_stop"})
	}))
	defer srv.Close()

	s := newAssistantTestServer(srv.URL)
	msgs := []assistantChatMessage{
		{Role: "user", Content: "hola"},
		{Role: "assistant", Content: "hey"},
		{Role: "user", Content: "que tal"},
	}
	var got strings.Builder
	err := s.assistantStream(context.Background(), "SYS", msgs, func(chunk string) error {
		got.WriteString(chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.String() != "Hola mundo" {
		t.Errorf("streamed text = %q", got.String())
	}

	if gotReq["model"] != "MiniMax-M3" {
		t.Errorf("model = %v", gotReq["model"])
	}
	if gotReq["system"] != "SYS" {
		t.Errorf("system = %v", gotReq["system"])
	}
	if gotReq["stream"] != true {
		t.Errorf("stream = %v (want true)", gotReq["stream"])
	}
	if _, ok := gotReq["max_tokens"]; !ok {
		t.Error("max_tokens missing from request")
	}
	rawMsgs, ok := gotReq["messages"].([]any)
	if !ok || len(rawMsgs) != 3 {
		t.Fatalf("messages = %v", gotReq["messages"])
	}
	wantRoles := []string{"user", "assistant", "user"}
	wantText := []string{"hola", "hey", "que tal"}
	for i, m := range rawMsgs {
		mm, _ := m.(map[string]any)
		if mm["role"] != wantRoles[i] {
			t.Errorf("messages[%d].role = %v (want %s)", i, mm["role"], wantRoles[i])
		}
		blocks, ok := mm["content"].([]any)
		if !ok || len(blocks) != 1 {
			t.Fatalf("messages[%d].content = %v", i, mm["content"])
		}
		blk, _ := blocks[0].(map[string]any)
		if blk["type"] != "text" || blk["text"] != wantText[i] {
			t.Errorf("messages[%d] block = %v", i, blk)
		}
	}
}

func TestAssistantStream_ChunksLongDelta(t *testing.T) {
	long := strings.Repeat("á", 300) // 300 runes, 600 bytes
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		sseWrite(t, w, fl, map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": long}})
		sseWrite(t, w, fl, map[string]any{"type": "message_stop"})
	}))
	defer srv.Close()

	s := newAssistantTestServer(srv.URL)
	var chunks []string
	err := s.assistantStream(context.Background(), "SYS", []assistantChatMessage{{Role: "user", Content: "hi"}}, func(chunk string) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected long delta to be split into multiple frames, got %d", len(chunks))
	}
	var joined strings.Builder
	for _, c := range chunks {
		if utf8.RuneCountInString(c) > assistantMaxFrameRunes {
			t.Errorf("chunk exceeds %d runes: %d", assistantMaxFrameRunes, utf8.RuneCountInString(c))
		}
		if !utf8.ValidString(c) {
			t.Errorf("chunk is not valid UTF-8: %q", c)
		}
		joined.WriteString(c)
	}
	if joined.String() != long {
		t.Error("reassembled chunks != original delta text")
	}
}

func TestAssistantStream_NoKey(t *testing.T) {
	s := newAssistantTestServer("http://127.0.0.1:1")
	s.cfg.MiniMaxAPIKey = ""
	err := s.assistantStream(context.Background(), "SYS", []assistantChatMessage{{Role: "user", Content: "hi"}}, func(string) error { return nil })
	if err == nil {
		t.Fatal("expected error when api key is missing")
	}
}

func TestAssistantStream_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	s := newAssistantTestServer(srv.URL)
	err := s.assistantStream(context.Background(), "SYS", []assistantChatMessage{{Role: "user", Content: "hi"}}, func(string) error { return nil })
	if err == nil {
		t.Fatal("expected error on HTTP 500")
	}
}

func TestAssistantStream_SSEError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		sseWrite(t, w, fl, map[string]any{"type": "error", "error": map[string]any{"type": "overloaded_error", "message": "overloaded"}})
	}))
	defer srv.Close()

	s := newAssistantTestServer(srv.URL)
	err := s.assistantStream(context.Background(), "SYS", []assistantChatMessage{{Role: "user", Content: "hi"}}, func(string) error { return nil })
	if err == nil {
		t.Fatal("expected error on SSE error event")
	}
}

func TestAssistantStream_Timeout(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // never respond until released
	}))
	defer srv.Close()
	defer close(block)

	s := newAssistantTestServer(srv.URL)
	s.cfg.AssistantTimeout = 150 * time.Millisecond
	start := time.Now()
	err := s.assistantStream(context.Background(), "SYS", []assistantChatMessage{{Role: "user", Content: "hi"}}, func(string) error { return nil })
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("timeout took too long: %v", time.Since(start))
	}
}

func TestAssistantSystemPrompt_ContainsPersona(t *testing.T) {
	s := &Server{cfg: config.Config{}} // nil db -> generic prompt, no queries
	prompt := s.buildAssistantSystemPrompt(context.Background(), 0)
	if !strings.Contains(prompt, "Forky") {
		t.Errorf("prompt missing persona name Forky: %q", prompt)
	}
	want := "Responde en español, sé breve, amable y con un toque de humor. Eres el asistente de IA del restaurante."
	if !strings.Contains(prompt, want) {
		t.Errorf("prompt missing required directive.\nprompt=%q", prompt)
	}
}
