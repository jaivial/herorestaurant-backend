package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// wa_bot_noncap_v1: a turn has no fixed tool/iteration limit. The owner asked
// for none (a long booking edit legitimately needs many lookups). BOT_MAX_ITERATIONS
// stays only as an optional operator override: 0 or unset means no cap. What ends
// a turn that is not progressing is the loop guard below, which looks at what the
// tools returned, not at how many calls were made.
const (
	// The same tool with the same input returned the same result this many times.
	botLoopRepeatLimit = 3
	// Only the most recent calls are compared, so memory stays bounded and
	// real progress (new calls pushing old ones out) never accumulates a strike.
	botLoopWindow = 24
	// Consecutive rounds in which every tool call failed.
	botLoopFailRounds = 4
)

// botLoopGuard detects a turn that is not making progress.
type botLoopGuard struct {
	recent    []string
	failRound int
	stalled   bool
	reason    string
}

func newBotLoopGuard() *botLoopGuard { return &botLoopGuard{} }

func (g *botLoopGuard) observe(name string, input json.RawMessage, out string) {
	sum := sha256.Sum256([]byte(name + "\x00" + string(input) + "\x00" + out))
	key := hex.EncodeToString(sum[:8])
	g.recent = append(g.recent, key)
	if len(g.recent) > botLoopWindow {
		g.recent = g.recent[len(g.recent)-botLoopWindow:]
	}
	n := 0
	for _, k := range g.recent {
		if k == key {
			n++
		}
	}
	if n >= botLoopRepeatLimit {
		g.stalled, g.reason = true, "repeated_identical_call"
	}
}

func (g *botLoopGuard) endRound(allFailed bool) {
	if allFailed {
		g.failRound++
	} else {
		g.failRound = 0
	}
	if g.failRound >= botLoopFailRounds {
		g.stalled, g.reason = true, "consecutive_failed_rounds"
	}
}

// botBudgetOperatorNote is appended to the SYSTEM prompt (a trusted channel,
// never to tool results) for the last model call of a turn, so that call
// answers instead of asking for another tool.
const botBudgetOperatorNote = "\n\n[Instrucción del operador: es el último paso de este turno. No llames a más herramientas. Responde al usuario solo con lo que las herramientas ya confirmaron y di explícitamente qué falta por hacer. No afirmes ningún cambio que no haya confirmado una herramienta.]"

// botBudgetExhaustedText is the fixed reply when the model still asks for a
// tool on its last allowed call. It claims nothing about outcomes: counters and
// tool names stay in the logs only.
const botBudgetExhaustedText = "No he podido completar tu petición. He detenido el proceso antes de ejecutar más acciones. Algunas acciones anteriores podrían haberse aplicado; no puedo confirmar que todo haya quedado terminado."

// botRunAgentLoopWith is the provider-agnostic loop used by every caller.
func (s *Server) botRunAgentLoopWith(ctx context.Context, call botModelCaller, system string, messages []botMessage, tools []botToolDef, exec botToolExecutor) (botLoopResult, error) {
	maxIter := s.cfg.BotMaxIterations // <= 0: no fixed cap (wa_bot_noncap_v1)
	guard := newBotLoopGuard()

	result := botLoopResult{}
	msgs := append([]botMessage{}, messages...)

	for i := 0; ; i++ {
		if err := ctx.Err(); err != nil {
			result.Messages = msgs
			return result, err
		}
		result.Iterations = i + 1

		// Wrap-up call: only when the loop guard saw no progress, or when an
		// operator-set cap is reached. Never because of a call count by default.
		last := guard.stalled || (maxIter > 1 && i == maxIter-1)
		callSystem := system
		if last {
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
		roundFailed := 0
		if last {
			wantsTool := false
			for _, block := range resp.Content {
				if block.Type == "tool_use" {
					wantsTool = true
				}
			}
			if wantsTool {
				// Budget spent and the model still wants a tool: execute nothing
				// more (no hidden extra mutation) and answer with the known facts.
				log.Printf("[bot] checkpoint wa_bot_iter_cap_v1 exhausted reason=%s iterations=%d ran=%d failed=%d tools=%s", guard.reason, i+1, result.Ran, result.Failed, strings.Join(result.ToolCalls, ","))
				result.Exhausted = true
				msgs = append(msgs, botMessage{Role: "assistant", Content: []botBlock{{Type: "text", Text: botBudgetExhaustedText}}})
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
			failed := err != nil || strings.Contains(out, `"error"`)
			if failed {
				result.Failed++
				roundFailed++
			}
			guard.observe(block.Name, block.Input, out)
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

		guard.endRound(roundFailed == len(toolResults))
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
