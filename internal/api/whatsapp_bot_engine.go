package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// botToolExecutor executes a named tool with JSON input and returns a JSON
// string fed back to the LLM as tool_result.
type botToolExecutor func(ctx context.Context, name string, input json.RawMessage) (string, error)

// botTurnState carries per-turn delivery bookkeeping from the tool executor
// back to botProcessMessage. noticeDelivered is set when a tool already sent a
// deterministic server-side reply (e.g. the same-day notice) so the generic
// fallback is not emitted on top of it. contactSent/contactPhone deduplicate
// the human-handoff contact card: the LLM may call send_contact several times
// (even inside one parallel tool_use block) and every extra call would deliver
// another vCard to the customer (prod incident 2026-09-17: 5 cards in one turn
// for sender 34679042882, iterations=8). Only the first call per turn sends;
// later calls return the cached phone without a new delivery.
type botTurnState struct {
	noticeDelivered bool
	contactSent     bool
	contactPhone    string
}

// botLoopResult summarizes one agent run.
type botLoopResult struct {
	Iterations int
	ToolCalls  []string
	Messages   []botMessage // full transcript including tool turns
	ModelUsed  string       // provider/model that produced the last response
	Ran        int          // tool calls executed this turn
	Failed     int          // of those, how many reported an error
	Exhausted  bool         // budget spent and the model still asked for a tool
}

// botModelCaller performs one model call for the loop and reports which
// provider/model answered (routing + fallback live behind it).
type botModelCaller func(ctx context.Context, system string, messages []botMessage, tools []botToolDef) (botLLMResponse, string, error)

// botRunAgentLoop drives the LLM tool-use loop: call the model, execute any
// tool_use blocks, feed tool_result back, repeat until end_turn or the
// iteration cap.
func (s *Server) botRunAgentLoop(ctx context.Context, restaurantID int, model string, system string, messages []botMessage, tools []botToolDef, exec botToolExecutor) (botLoopResult, error) {
	return s.botRunAgentLoopWith(ctx, func(ctx context.Context, system string, msgs []botMessage, tools []botToolDef) (botLLMResponse, string, error) {
		resp, err := s.botLLMCall(ctx, restaurantID, model, system, msgs, tools)
		return resp, model, err
	}, system, messages, tools, exec)
}

// botDefaultMaxIterations is the tool-use budget of one turn when the config
// does not set one. wa_bot_iter_cap_v1: 8 was not enough for a booking edit
// that needs catalog lookups first, and a turn that spends the whole budget on
// tools ends with no text, so the customer got the generic apology.
const botDefaultMaxIterations = 12

// botBudgetOperatorNote is appended to the SYSTEM prompt (a trusted channel,
// never to tool results) for the last model call of a turn, so that call
// answers instead of asking for another tool.
const botBudgetOperatorNote = "\n\n[Instrucción del operador: es el último paso de este turno. No llames a más herramientas. Responde al usuario solo con lo que las herramientas ya confirmaron y di explícitamente qué falta por hacer. No afirmes ningún cambio que no haya confirmado una herramienta.]"

// botBudgetExhaustedText is the fixed reply when the model still asks for a
// tool on its last allowed call. It states only what is known (how many steps
// ran, how many failed) and never claims an effect.
func botBudgetExhaustedText(ran, failed int) string {
	return fmt.Sprintf("No he podido terminar tu petición en este turno: se agotó el límite de pasos y paré antes de hacer más cambios. Se ejecutaron %d pasos (%d con error). No he repetido ni deshecho nada; puede que algún cambio ya esté aplicado, así que comprueba el estado antes de volver a pedírmelo.", ran, failed)
}

// botRunAgentLoopWith is the provider-agnostic loop used by every caller.
func (s *Server) botRunAgentLoopWith(ctx context.Context, call botModelCaller, system string, messages []botMessage, tools []botToolDef, exec botToolExecutor) (botLoopResult, error) {
	maxIter := s.cfg.BotMaxIterations
	if maxIter <= 0 {
		maxIter = botDefaultMaxIterations
	}

	result := botLoopResult{}
	msgs := append([]botMessage{}, messages...)

	for i := 0; i < maxIter; i++ {
		result.Iterations = i + 1

		last := i == maxIter-1
		callSystem := system
		if last && maxIter > 1 {
			callSystem = system + botBudgetOperatorNote
		}
		resp, used, err := call(ctx, callSystem, msgs, tools)
		if err != nil {
			result.Messages = msgs
			return result, err
		}
		result.ModelUsed = used

		// Append the assistant turn as-is (text + tool_use blocks).
		assistant := botMessage{Role: "assistant", Content: resp.Content}
		toolResults := make([]botBlock, 0, 2)
		if last && maxIter > 1 {
			wantsTool := false
			for _, block := range resp.Content {
				if block.Type == "tool_use" {
					wantsTool = true
				}
			}
			if wantsTool {
				// Budget spent and the model still wants a tool: execute nothing
				// more (no hidden extra mutation) and answer with the known facts.
				log.Printf("[bot] checkpoint wa_bot_iter_cap_v1 exhausted iterations=%d ran=%d failed=%d", i+1, result.Ran, result.Failed)
				result.Exhausted = true
				msgs = append(msgs, botMessage{Role: "assistant", Content: []botBlock{{Type: "text", Text: botBudgetExhaustedText(result.Ran, result.Failed)}}})
				result.Messages = msgs
				return result, nil
			}
		}
		for _, block := range resp.Content {
			if block.Type != "tool_use" {
				continue
			}
			result.ToolCalls = append(result.ToolCalls, block.Name)
			out, err := exec(ctx, block.Name, block.Input)
			if err != nil {
				out = `{"error":` + jsonQuote(err.Error()) + `}`
			}
			result.Ran++
			if err != nil || strings.Contains(out, `"error"`) {
				result.Failed++
			}
			toolResults = append(toolResults, botBlock{
				Type:      "tool_result",
				ToolUseID: block.ID,
				Content:   out,
			})
		}

		if len(toolResults) == 0 {
			// end_turn (or a response with no tool calls): done.
			msgs = append(msgs, assistant)
			result.Messages = msgs
			return result, nil
		}

		msgs = append(msgs, assistant, botMessage{Role: "user", Content: toolResults})

		// Coordination id: wa_bot_early_stop_v1 - when every tool of this
		// response was a successful customer delivery (send_message,
		// send_contact...), the reply is out: skip the extra model call that
		// only produces end_turn (2-5 s per turn) and cannot double-send.
		if botAllDelivered(resp.Content, toolResults) {
			result.Messages = msgs
			return result, nil
		}
	}

	result.Messages = msgs
	return result, nil
}

func jsonQuote(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// botAllDelivered reports whether every tool_use in the response is a
// delivery tool whose result did not report an error.
func botAllDelivered(content []botBlock, results []botBlock) bool {
	n := 0
	for _, b := range content {
		if b.Type != "tool_use" {
			continue
		}
		n++
		if !botDeliveryTools[b.Name] {
			return false
		}
	}
	if n == 0 {
		return false
	}
	for _, r := range results {
		if strings.Contains(r.Content, `"error"`) {
			return false
		}
	}
	return true
}
