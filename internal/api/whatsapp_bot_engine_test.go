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
	// 1-2 run the tool, round 3 hits the cap and the loop tries the grounded
	// no-tools wrap-up (wa_bot_grounded_wrapup_v1) before the fixed notice.
	if execCount != 2 {
		t.Errorf("execCount = %d, want 2", execCount)
	}
	if !result.Exhausted {
		t.Errorf("result.Exhausted = false, want true")
	}
}

func TestBotRunAgentLoop_GroundedWrapUp_AnswersInsteadOfFixedText(t *testing.T) {
	// wa_bot_grounded_wrapup_v1: when the model still asks for a tool on the
	// capped call, the loop makes ONE extra no-tools call. If that call
	// returns text, the customer gets the grounded answer, not the fixed
	// botBudgetExhaustedText.
	// Script: call 1 -> tool_use (runs); call 2 (capped) -> tool_use again;
	// call 3 (grounded, no tools) -> plain text answer.
	llm, calls := fakeLLM(t, []map[string]any{
		{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "tool_use", "id": "tu_1", "name": "get_rice_menu", "input": map[string]any{}},
			},
		},
		{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "tool_use", "id": "tu_2", "name": "get_rice_menu", "input": map[string]any{}},
			},
		},
		{
			"stop_reason": "end_turn",
			"content": []map[string]any{
				{"type": "text", "text": "He confirmado el menú de arroces; falta aplicar el cambio de precio."},
			},
		},
	})
	defer llm.Close()

	s := newBotTestServer(llm.URL)
	s.cfg.BotMaxIterations = 2 // call 2 is the capped (last) one
	exec := func(ctx context.Context, name string, input json.RawMessage) (string, error) {
		return `{"rices":["marisco"]}`, nil
	}
	result, err := s.botRunAgentLoop(context.Background(), 0, "", "SYS", []botMessage{botUserText("hola")}, nil, exec)
	if err != nil {
		t.Fatalf("loop error: %v", err)
	}
	if !result.Exhausted {
		t.Errorf("result.Exhausted = false, want true")
	}
	got := botFinalAssistantText(result.Messages)
	want := "He confirmado el menú de arroces; falta aplicar el cambio de precio."
	if got != want {
		t.Errorf("final text = %q, want grounded answer %q", got, want)
	}
	if len(*calls) != 3 {
		t.Fatalf("llm calls = %d, want 3 (loop x2 + grounded wrap-up)", len(*calls))
	}
	// The grounded wrap-up call must send NO tools.
	var grounded struct {
		Tools []any `json:"tools"`
	}
	_ = json.Unmarshal((*calls)[2], &grounded)
	if len(grounded.Tools) != 0 {
		t.Errorf("grounded wrap-up tools = %d, want 0", len(grounded.Tools))
	}
}

func TestBotRunAgentLoop_GroundedWrapUp_FallsBackWhenNoText(t *testing.T) {
	// wa_bot_grounded_wrapup_v1: when the grounded no-tools call returns no
	// text (reasoning model burned its budget), the loop falls back to the
	// fixed botBudgetExhaustedText.
	llm, _ := fakeLLM(t, []map[string]any{
		{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "tool_use", "id": "tu_1", "name": "get_rice_menu", "input": map[string]any{}},
			},
		},
		{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "tool_use", "id": "tu_2", "name": "get_rice_menu", "input": map[string]any{}},
			},
		},
		{"stop_reason": "max_tokens", "content": []map[string]any{}}, // grounded call: no text
	})
	defer llm.Close()

	s := newBotTestServer(llm.URL)
	s.cfg.BotMaxIterations = 2
	exec := func(ctx context.Context, name string, input json.RawMessage) (string, error) {
		return `{"rices":[]}`, nil
	}
	result, err := s.botRunAgentLoop(context.Background(), 0, "", "SYS", []botMessage{botUserText("hola")}, nil, exec)
	if err != nil {
		t.Fatalf("loop error: %v", err)
	}
	if got := botFinalAssistantText(result.Messages); got != botBudgetExhaustedText {
		t.Errorf("final text = %q, want fixed exhausted text", got)
	}
}

func TestBotRunAgentLoop_TerminalTool_GroundedWrapUp(t *testing.T) {
	// wa_bot_terminal_tool_v1 + wa_bot_grounded_wrapup_v1: a terminal refusal
	// (admin_call 4xx) closes the turn, but the customer must get the grounded
	// answer from the tools that DID succeed, not the fixed exhausted text.
	// Regression: prod 2026-10-23 16:47, an 18-iter turn answered the fixed
	// text even though the booking + menu facts were already in the transcript.
	// Script: call 1 -> parallel tool_use (admin_call 4xx terminal + a read
	// that succeeded); call 2 (grounded, no tools) -> plain text answer.
	llm, calls := fakeLLM(t, []map[string]any{
		{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "tool_use", "id": "tu_1", "name": "admin_call", "input": map[string]any{}},
				{"type": "tool_use", "id": "tu_2", "name": "get_rice_menu", "input": map[string]any{}},
			},
		},
		{
			"stop_reason": "end_turn",
			"content": []map[string]any{
				{"type": "text", "text": "Hay 3 reservas el 31/12 y el men\u00fa 50 est\u00e1 disponible; no pude leer los platos elegidos."},
			},
		},
	})
	defer llm.Close()

	s := newBotTestServer(llm.URL)
	exec := func(ctx context.Context, name string, input json.RawMessage) (string, error) {
		switch name {
		case "admin_call":
			// Panel replay shape with a 4xx status -> botToolTerminal = true.
			return `{"tool":"admin_get_booking_dishes","status":404,"ok":false,"error":"no encontrada"}`, nil
		case "get_rice_menu":
			return `{"rices":["marisco","pollo"]}`, nil
		}
		t.Errorf("unexpected tool %q", name)
		return `{"error":"unknown"}`, nil
	}
	result, err := s.botRunAgentLoop(context.Background(), 0, "", "SYS", []botMessage{botUserText("hola")}, nil, exec)
	if err != nil {
		t.Fatalf("loop error: %v", err)
	}
	if !result.Exhausted {
		t.Errorf("result.Exhausted = false, want true (terminal close)")
	}
	got := botFinalAssistantText(result.Messages)
	want := "Hay 3 reservas el 31/12 y el men\u00fa 50 est\u00e1 disponible; no pude leer los platos elegidos."
	if got != want {
		t.Errorf("final text = %q, want grounded answer %q", got, want)
	}
	if got == botBudgetExhaustedText {
		t.Errorf("terminal close must not answer the fixed exhausted text when a grounded answer is available")
	}
	if len(*calls) != 2 {
		t.Fatalf("llm calls = %d, want 2 (loop + grounded wrap-up)", len(*calls))
	}
	// The grounded wrap-up call must send NO tools.
	var grounded struct {
		Tools []any `json:"tools"`
	}
	_ = json.Unmarshal((*calls)[1], &grounded)
	if len(grounded.Tools) != 0 {
		t.Errorf("grounded wrap-up tools = %d, want 0", len(grounded.Tools))
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
