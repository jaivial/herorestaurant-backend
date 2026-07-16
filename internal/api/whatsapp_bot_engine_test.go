package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// fakeLLM returns a handler that pops scripted responses one per call.
func fakeLLM(t *testing.T, responses []map[string]any) (*httptest.Server, *[][]byte) {
	t.Helper()
	var mu sync.Mutex
	var calls [][]byte
	idx := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		raw, _ := io.ReadAll(r.Body)
		calls = append(calls, raw)
		if idx >= len(responses) {
			t.Error("LLM called more times than scripted")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(responses[idx])
		idx++
	}))
	return srv, &calls
}

func TestBotRunAgentLoop_SendMessageAndEndTurn(t *testing.T) {
	// Script: 1st call → tool_use send_message; 2nd call → end_turn.
	llm, calls := fakeLLM(t, []map[string]any{
		{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "tool_use", "id": "tu_1", "name": "send_message",
					"input": map[string]any{"message": "¡Hola! ¿En qué te ayudo?"}},
			},
		},
		{
			"stop_reason": "end_turn",
			"content":     []map[string]any{{"type": "text", "text": "listo"}},
		},
	})
	defer llm.Close()

	var sentTexts []string
	s := newBotTestServer(llm.URL)
	exec := func(ctx context.Context, name string, input json.RawMessage) (string, error) {
		if name == "send_message" {
			var in struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(input, &in)
			sentTexts = append(sentTexts, in.Message)
			return `{"sent":true}`, nil
		}
		t.Errorf("unexpected tool %q", name)
		return `{"error":"unknown tool"}`, nil
	}

	result, err := s.botRunAgentLoop(context.Background(), "", "SYS", []botMessage{botUserText("hola")}, botToolDefs(botTenantConfig{}), exec)
	if err != nil {
		t.Fatalf("loop error: %v", err)
	}
	if len(sentTexts) != 1 || sentTexts[0] != "¡Hola! ¿En qué te ayudo?" {
		t.Errorf("sentTexts = %v", sentTexts)
	}
	if result.Iterations != 2 {
		t.Errorf("iterations = %d", result.Iterations)
	}
	if len(*calls) != 2 {
		t.Fatalf("llm calls = %d", len(*calls))
	}

	// Second request must include assistant tool_use + user tool_result.
	var second struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal((*calls)[1], &second)
	if len(second.Messages) != 3 {
		t.Fatalf("second call messages = %d", len(second.Messages))
	}
	if second.Messages[1].Role != "assistant" || second.Messages[2].Role != "user" {
		t.Errorf("roles = %s, %s", second.Messages[1].Role, second.Messages[2].Role)
	}
	if !json.Valid(second.Messages[2].Content) {
		t.Error("tool_result content not valid json")
	}
}

func TestBotRunAgentLoop_MaxIterations(t *testing.T) {
	// LLM always wants another tool call: loop must stop at BotMaxIterations.
	responses := make([]map[string]any, 0, 5)
	for i := 0; i < 5; i++ {
		responses = append(responses, map[string]any{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "tool_use", "id": "tu_x", "name": "get_rice_menu", "input": map[string]any{}},
			},
		})
	}
	llm, _ := fakeLLM(t, responses)
	defer llm.Close()

	s := newBotTestServer(llm.URL)
	s.cfg.BotMaxIterations = 3
	execCount := 0
	exec := func(ctx context.Context, name string, input json.RawMessage) (string, error) {
		execCount++
		return `{"rices":[]}`, nil
	}
	result, err := s.botRunAgentLoop(context.Background(), "", "SYS", []botMessage{botUserText("hola")}, nil, exec)
	if err != nil {
		t.Fatalf("loop error: %v", err)
	}
	if result.Iterations != 3 {
		t.Errorf("iterations = %d, want 3", result.Iterations)
	}
	if execCount != 3 {
		t.Errorf("execCount = %d", execCount)
	}
}

func TestBotRunAgentLoop_ToolError_FeedsErrorBack(t *testing.T) {
	llm, calls := fakeLLM(t, []map[string]any{
		{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "tool_use", "id": "tu_1", "name": "check_day_capacity",
					"input": map[string]any{"date": "15/05/2026"}},
			},
		},
		{"stop_reason": "end_turn", "content": []map[string]any{}},
	})
	defer llm.Close()

	s := newBotTestServer(llm.URL)
	exec := func(ctx context.Context, name string, input json.RawMessage) (string, error) {
		return `{"error":"db down"}`, nil
	}
	if _, err := s.botRunAgentLoop(context.Background(), "", "SYS", []botMessage{botUserText("x")}, nil, exec); err != nil {
		t.Fatalf("loop should survive tool errors: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("llm calls = %d", len(*calls))
	}
}
