package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"preactvillacarmen/internal/config"
)

func newBotTestServer(baseURL string) *Server {
	return &Server{
		cfg: config.Config{
			MiniMaxAPIKey:    "test-key",
			MiniMaxBaseURL:   baseURL,
			MiniMaxModel:     "MiniMax-M3",
			BotModel:         "MiniMax-M3",
			BotTimeout:       5 * time.Second,
			BotMaxTokens:     512,
			BotMaxIterations: 5,
			BotHistoryLimit:  20,
		},
	}
}

func TestBotLLMCall_ParsesToolUse(t *testing.T) {
	var gotReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotReq)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "text", "text": "voy a responder"},
				{"type": "tool_use", "id": "tu_1", "name": "send_message",
					"input": map[string]any{"message": "¡Hola!"}},
			},
		})
	}))
	defer srv.Close()

	s := newBotTestServer(srv.URL)
	msgs := []botMessage{botUserText("hola")}
	resp, err := s.botLLMCall(context.Background(), "", "SYSTEM", msgs, botToolDefs(botTenantConfig{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("stop_reason = %q", resp.StopReason)
	}
	var tu *botBlock
	for i := range resp.Content {
		if resp.Content[i].Type == "tool_use" {
			tu = &resp.Content[i]
		}
	}
	if tu == nil {
		t.Fatal("expected tool_use block")
	}
	if tu.Name != "send_message" || tu.ID != "tu_1" {
		t.Errorf("tool block = %+v", tu)
	}
	var input struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(tu.Input, &input); err != nil || input.Message != "¡Hola!" {
		t.Errorf("input = %s err=%v", tu.Input, err)
	}

	// Request shape checks.
	if gotReq["model"] != "MiniMax-M3" {
		t.Errorf("model = %v", gotReq["model"])
	}
	if gotReq["system"] != "SYSTEM" {
		t.Errorf("system = %v", gotReq["system"])
	}
	tools, ok := gotReq["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("tools missing in request: %v", gotReq["tools"])
	}
	first, _ := tools[0].(map[string]any)
	if _, ok := first["input_schema"]; !ok {
		t.Error("tool must carry input_schema")
	}
}

func TestBotLLMCall_NoKey(t *testing.T) {
	s := &Server{cfg: config.Config{}}
	if _, err := s.botLLMCall(context.Background(), "", "sys", nil, nil); err == nil {
		t.Fatal("expected error when api key missing")
	}
}

func TestBotLLMCall_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	s := newBotTestServer(srv.URL)
	if _, err := s.botLLMCall(context.Background(), "", "sys", []botMessage{botUserText("x")}, nil); err == nil {
		t.Fatal("expected error on HTTP 502")
	}
}

func TestBotMessageSerialization(t *testing.T) {
	msg := botMessage{
		Role: "assistant",
		Content: []botBlock{
			{Type: "text", Text: "ok"},
			{Type: "tool_use", ID: "tu_9", Name: "get_rice_menu", Input: json.RawMessage(`{}`)},
		},
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var round map[string]any
	_ = json.Unmarshal(raw, &round)
	blocks := round["content"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d", len(blocks))
	}
	b1 := blocks[1].(map[string]any)
	if b1["id"] != "tu_9" || b1["name"] != "get_rice_menu" {
		t.Errorf("tool_use block = %v", b1)
	}
	if _, has := blocks[0].(map[string]any)["id"]; has {
		t.Error("text block must not carry id")
	}
}

func TestBotToolResultMessage(t *testing.T) {
	msg := botToolResult("tu_1", `{"ok":true}`)
	if msg.Role != "user" {
		t.Errorf("role = %q", msg.Role)
	}
	if len(msg.Content) != 1 || msg.Content[0].Type != "tool_result" || msg.Content[0].ToolUseID != "tu_1" {
		t.Errorf("content = %+v", msg.Content)
	}
}
