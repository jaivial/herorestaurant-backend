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

// fakeLLMFailAt is fakeLLM but the (failAt)th call (0-indexed) answers HTTP
// 500 so the model call returns an error (the interrupted path).
func fakeLLMFailAt(t *testing.T, responses []map[string]any, failAt int) (*httptest.Server, *[][]byte) {
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
		if idx == failAt {
			idx++
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

func TestBotRunAgentLoop_Interrupted_GroundedWrapUp(t *testing.T) {
	// wa_bot_interrupted_grounded_v1: when the model call fails after tools ran
	// (e.g. the 3-min turn deadline passes), the customer must get the grounded
	// answer from the gathered tool_results, not the fixed interrupted text.
	// Regression: prod 2026-10-23 17:12, a 27-iter deadline-exhausted turn
	// answered botInterruptedText even though the data was in the transcript.
	// Script: call 1 -> tool_use (runs); call 2 -> HTTP 500 (model call fails);
	// call 3 (grounded, no tools, on a detached ctx) -> plain text answer.
	llm, calls := fakeLLMFailAt(t, []map[string]any{
		{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "tool_use", "id": "tu_1", "name": "get_rice_menu", "input": map[string]any{}},
			},
		},
		{
			// idx=1 is replaced by the HTTP 500 failure (failAt=1); this entry is
			// the grounded wrap-up answer the loop makes after the interrupt.
			"stop_reason": "end_turn",
			"content": []map[string]any{
				{"type": "text", "text": "El menu de arroces tiene marisco y pollo; no pude terminar de listar los postres."},
			},
		},
		{
			"stop_reason": "end_turn",
			"content": []map[string]any{
				{"type": "text", "text": "El menu de arroces tiene marisco y pollo; no pude terminar de listar los postres."},
			},
		},
	}, 1)
	defer llm.Close()

	s := newBotTestServer(llm.URL)
	exec := func(ctx context.Context, name string, input json.RawMessage) (string, error) {
		return `{"rices":["marisco","pollo"]}`, nil
	}
	result, err := s.botRunAgentLoop(context.Background(), 0, "", "SYS", []botMessage{botUserText("hola")}, nil, exec)
	if err != nil {
		t.Fatalf("loop error: %v", err)
	}
	if !result.Interrupted {
		t.Errorf("result.Interrupted = false, want true")
	}
	got := botFinalAssistantText(result.Messages)
	want := "El menu de arroces tiene marisco y pollo; no pude terminar de listar los postres."
	if got != want {
		t.Errorf("final text = %q, want grounded answer %q", got, want)
	}
	if got == botInterruptedText {
		t.Errorf("interrupted turn must not answer the fixed interrupted text when a grounded answer is available")
	}
	if len(*calls) != 3 {
		t.Fatalf("llm calls = %d, want 3 (loop + failed + grounded wrap-up)", len(*calls))
	}
}

func TestBotRunAgentLoop_NonTerminalRefusal_TurnStaysOpen(t *testing.T) {
	// The admin_call write refusal points the model at panel_change, so it must
	// NOT be terminal: the loop has to make the NEXT model call where the model
	// follows that advice. Regression 2026-10-23 17:30: terminal:true closed
	// the turn before panel_change ran and the customer got a summary instead
	// of the proposal.
	// Script: call 1 -> tool_use admin_call (returns the non-terminal refusal);
	// call 2 -> end_turn with the proposal text.
	llm, calls := fakeLLM(t, []map[string]any{
		{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "tool_use", "id": "tu_1", "name": "admin_call", "input": map[string]any{}},
			},
		},
		{
			"stop_reason": "end_turn",
			"content": []map[string]any{
				{"type": "text", "text": "Voy a proponer el cambio a 11 personas con OK y código."},
			},
		},
	})
	defer llm.Close()

	s := newBotTestServer(llm.URL)
	exec := func(ctx context.Context, name string, input json.RawMessage) (string, error) {
		return `{"error":"el bot no puede ejecutar escrituras genéricas del panel (admin_call) sin aprobación humana. No lo reintentes por admin_call: llama ahora a panel_change para PROPONER el cambio (se aplica cuando el usuario responde 'OK <código>' en el chat)."}`, nil
	}
	result, err := s.botRunAgentLoop(context.Background(), 0, "", "SYS", []botMessage{botUserText("hola")}, nil, exec)
	if err != nil {
		t.Fatalf("loop error: %v", err)
	}
	if result.Exhausted {
		t.Errorf("result.Exhausted = true: the refusal must not close the turn")
	}
	if got := botFinalAssistantText(result.Messages); got != "Voy a proponer el cambio a 11 personas con OK y código." {
		t.Errorf("final text = %q, want the model's proposal (2nd call must have run)", got)
	}
	if len(*calls) != 2 {
		t.Fatalf("llm calls = %d, want 2 (turn must stay open after the refusal)", len(*calls))
	}
}

func TestBotRunAgentLoop_DeadlineWrapUp_AnswersBeforeDeadline(t *testing.T) {
	// wa_bot_deadline_wrapup_v1: when the remaining turn budget drops to
	// botDeadlineWrapUpAt after tools ran, the loop stops calling tools and
	// answers with the grounded wrap-up instead of running into the deadline
	// (where the wrap-up is skipped for no_budget and the fixed text is sent).
	prev := botDeadlineWrapUpAt
	botDeadlineWrapUpAt = 59 * time.Second // turn below has 60s: trips after round 1
	defer func() { botDeadlineWrapUpAt = prev }()
	llm, calls := fakeLLM(t, []map[string]any{
		{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "tool_use", "id": "tu_1", "name": "get_rice_menu", "input": map[string]any{}},
			},
		},
		{
			"stop_reason": "end_turn",
			"content": []map[string]any{
				{"type": "text", "text": "Reserva localizada; propongo el cambio en el siguiente mensaje."},
			},
		},
	})
	defer llm.Close()

	s := newBotTestServer(llm.URL)
	exec := func(ctx context.Context, name string, input json.RawMessage) (string, error) {
		time.Sleep(1100 * time.Millisecond)
		return `{"rices":["marisco"]}`, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	result, err := s.botRunAgentLoop(ctx, 0, "", "SYS", []botMessage{botUserText("hola")}, nil, exec)
	if err != nil {
		t.Fatalf("loop error: %v", err)
	}
	if !result.Exhausted || result.Interrupted {
		t.Errorf("Exhausted=%t Interrupted=%t, want exhausted (deadline wrap-up), not interrupted", result.Exhausted, result.Interrupted)
	}
	if got := botFinalAssistantText(result.Messages); got != "Reserva localizada; propongo el cambio en el siguiente mensaje." {
		t.Errorf("final text = %q, want grounded answer", got)
	}
	if len(*calls) != 2 {
		t.Fatalf("llm calls = %d, want 2 (loop + grounded wrap-up)", len(*calls))
	}
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

func TestBotRunAgentLoop_TerminalSteer_ContinuesOnce(t *testing.T) {
	// wa_bot_terminal_steer_v1: a terminal refusal that names an alternative
	// tool (retry_with) is fed back once and the turn continues with it, so
	// the customer gets the real answer instead of botBudgetExhaustedText.
	llm, _ := fakeLLM(t, []map[string]any{
		{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "tool_use", "id": "tu_1", "name": "admin_call",
					"input": map[string]any{"name": "admin_patch_bookings_id"}},
			},
		},
		{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "tool_use", "id": "tu_2", "name": "panel_change",
					"input": map[string]any{"op": "booking_update"}},
			},
		},
		{"stop_reason": "end_turn", "content": []map[string]any{
			{"type": "text", "text": "Reserva actualizada: 11 personas, mesa 7."},
		}},
	})
	defer llm.Close()

	s := newBotTestServer(llm.URL)
	var ran []string
	exec := func(ctx context.Context, name string, input json.RawMessage) (string, error) {
		ran = append(ran, name)
		if name == "admin_call" {
			return `{"error":"el bot no puede ejecutar escrituras genericas del panel (admin_call) sin aprobacion humana. Usa panel_change para PROponer el cambio.","terminal":true,"retry_with":"panel_change"}`, nil
		}
		return `{"ok":true,"direct":true}`, nil
	}
	result, err := s.botRunAgentLoop(context.Background(), 0, "", "SYS", []botMessage{botUserText("actualiza la reserva")}, nil, exec)
	if err != nil {
		t.Fatalf("loop error: %v", err)
	}
	if len(ran) != 2 || ran[0] != "admin_call" || ran[1] != "panel_change" {
		t.Errorf("ran = %v, want [admin_call panel_change]", ran)
	}
	if result.Exhausted || result.Interrupted {
		t.Errorf("Exhausted=%v Interrupted=%v, want both false (steered turn continued)", result.Exhausted, result.Interrupted)
	}
	if result.Iterations != 3 {
		t.Errorf("iterations = %d, want 3", result.Iterations)
	}
	last := result.Messages[len(result.Messages)-1]
	if len(last.Content) == 0 || last.Content[0].Text != "Reserva actualizada: 11 personas, mesa 7." {
		t.Errorf("last message = %+v, want the model final answer", last)
	}
}

func TestBotRunAgentLoop_TerminalSteer_SecondRefusalCloses(t *testing.T) {
	// A second terminal refusal of the same tool closes the turn with the
	// fixed exhausted notice: steering is bounded to one attempt per tool.
	llm, _ := fakeLLM(t, []map[string]any{
		{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "tool_use", "id": "tu_1", "name": "admin_call",
					"input": map[string]any{"name": "admin_patch_bookings_id"}},
			},
		},
		{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "tool_use", "id": "tu_2", "name": "admin_call",
					"input": map[string]any{"name": "admin_patch_bookings_id", "confirmed": true}},
			},
		},
	})
	defer llm.Close()

	s := newBotTestServer(llm.URL)
	execCount := 0
	exec := func(ctx context.Context, name string, input json.RawMessage) (string, error) {
		execCount++
		return `{"error":"el bot no puede ejecutar escrituras genericas del panel (admin_call) sin aprobacion humana. Usa panel_change para PROponer el cambio.","terminal":true,"retry_with":"panel_change"}`, nil
	}
	result, err := s.botRunAgentLoop(context.Background(), 0, "", "SYS", []botMessage{botUserText("actualiza la reserva")}, nil, exec)
	if err != nil {
		t.Fatalf("loop error: %v", err)
	}
	if execCount != 2 {
		t.Errorf("execCount = %d, want 2", execCount)
	}
	if !result.Exhausted {
		t.Errorf("Exhausted = false, want true (second terminal refusal closes the turn)")
	}
	last := result.Messages[len(result.Messages)-1]
	if len(last.Content) == 0 || last.Content[0].Text != botBudgetExhaustedText {
		t.Errorf("last message = %+v, want botBudgetExhaustedText", last)
	}
}
