package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestBotRunAgentLoop_EarlyStopAfterDelivery(t *testing.T) {
	// wa_bot_early_stop_v1: when every tool of the response was a successful
	// customer delivery (send_message), the reply is out and the loop stops
	// without the extra end_turn model call.
	// Script: 1st call → tool_use send_message; no 2nd call expected.
	llm, calls := fakeLLM(t, []map[string]any{
		{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "tool_use", "id": "tu_1", "name": "send_message",
					"input": map[string]any{"message": "¡Hola! ¿En qué te ayudo?"}},
			},
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

	result, err := s.botRunAgentLoop(context.Background(), 0, "", "SYS", []botMessage{botUserText("hola")}, botToolDefs(botTenantConfig{}), exec)
	if err != nil {
		t.Fatalf("loop error: %v", err)
	}
	if len(sentTexts) != 1 || sentTexts[0] != "¡Hola! ¿En qué te ayudo?" {
		t.Errorf("sentTexts = %v", sentTexts)
	}
	if result.Iterations != 1 {
		t.Errorf("iterations = %d, want 1 (early stop after delivery)", result.Iterations)
	}
	if len(*calls) != 1 {
		t.Fatalf("llm calls = %d, want 1 (early stop after delivery)", len(*calls))
	}

	// The single request is the original user turn.
	var first struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal((*calls)[0], &first)
	if len(first.Messages) != 1 || first.Messages[0].Role != "user" {
		t.Errorf("first call messages = %+v", first.Messages)
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
	result, err := s.botRunAgentLoop(context.Background(), 0, "", "SYS", []botMessage{botUserText("hola")}, nil, exec)
	if err != nil {
		t.Fatalf("loop error: %v", err)
	}
	if result.Iterations != 3 {
		t.Errorf("iterations = %d, want 3", result.Iterations)
	}
	// wa_bot_noncap_v1: with the budget spent, the tool call of the LAST
	// response is deliberately NOT executed (no hidden extra mutation): rounds
	// 1-2 run the tool, round 3 hits the cap and the loop answers with the
	// exhausted notice (botBudgetExhaustedText).
	if execCount != 2 {
		t.Errorf("execCount = %d, want 2", execCount)
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
	if _, err := s.botRunAgentLoop(context.Background(), 0, "", "SYS", []botMessage{botUserText("x")}, nil, exec); err != nil {
		t.Fatalf("loop should survive tool errors: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("llm calls = %d", len(*calls))
	}
}

func TestBotRunAgentLoop_DeadlineReserveForcesWrapUp(t *testing.T) {
	// wa_bot_deadline_reserve_v1: with less than the closing reserve left on
	// the turn deadline, the current call is the wrap-up call: the tool the
	// model asks for must NOT run and the turn closes with the budget notice
	// instead of dying mid-flight with botInterruptedText.
	llm, calls := fakeLLM(t, []map[string]any{
		{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "tool_use", "id": "tu_1", "name": "get_rice_menu", "input": map[string]any{}},
			},
		},
	})
	defer llm.Close()

	s := newBotTestServer(llm.URL)
	execCount := 0
	exec := func(ctx context.Context, name string, input json.RawMessage) (string, error) {
		execCount++
		return `{"rices":[]}`, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), botTurnAnswerReserve/3)
	defer cancel()
	result, err := s.botRunAgentLoop(ctx, 0, "", "SYS", []botMessage{botUserText("hola")}, nil, exec)
	if err != nil {
		t.Fatalf("loop error: %v", err)
	}
	if execCount != 0 {
		t.Errorf("execCount = %d, want 0 (no tool round inside the closing reserve)", execCount)
	}
	if !result.Exhausted {
		t.Errorf("Exhausted = false, want true (wrap-up notice path)")
	}
	if result.Iterations != 1 {
		t.Errorf("iterations = %d, want 1", result.Iterations)
	}
	if !strings.Contains(string((*calls)[0]), "último paso de este turno") {
		t.Errorf("wrap-up call does not carry the operator note: %s", (*calls)[0])
	}
}

func TestBotRunAgentLoop_DeadlineFar_NoWrapUp(t *testing.T) {
	// A deadline far away must not change behaviour: the tool round runs.
	llm, _ := fakeLLM(t, []map[string]any{
		{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "tool_use", "id": "tu_1", "name": "get_rice_menu", "input": map[string]any{}},
			},
		},
		{"stop_reason": "end_turn", "content": []map[string]any{
			{"type": "text", "text": "Hay 3 arroces."},
		}},
	})
	defer llm.Close()

	s := newBotTestServer(llm.URL)
	execCount := 0
	exec := func(ctx context.Context, name string, input json.RawMessage) (string, error) {
		execCount++
		return `{"rices":[]}`, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	result, err := s.botRunAgentLoop(ctx, 0, "", "SYS", []botMessage{botUserText("hola")}, nil, exec)
	if err != nil {
		t.Fatalf("loop error: %v", err)
	}
	if execCount != 1 {
		t.Errorf("execCount = %d, want 1", execCount)
	}
	if result.Iterations != 2 {
		t.Errorf("iterations = %d, want 2", result.Iterations)
	}
}
