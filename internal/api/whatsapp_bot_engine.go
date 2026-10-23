package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
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
	Iterations  int
	ToolCalls   []string
	Messages    []botMessage // full transcript including tool turns
	ModelUsed   string       // provider/model that produced the last response
	Ran         int          // tool calls executed this turn
	Failed      int          // of those, how many reported an error
	Exhausted   bool         // budget spent and the model still asked for a tool
	Interrupted bool         // model call failed or the turn deadline passed after tools ran
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
	// The same tool with the same input returned the same result this many times IN A ROW.
	botLoopRepeatLimit = 3
	// Consecutive rounds in which every tool call failed.
	botLoopFailRounds = 4
)

// botLoopGuard detects a turn that is not making progress.
type botLoopGuard struct {
	lastKey   string
	repeats   int
	failRound int
	stalled   bool
	reason    string
}

// botResultFailed reports whether a tool result is a failure. A JSON result
// whose top-level "error" is null, empty or false is not a failure (the tool
// executor reports failures as a top-level {"error": "..."}); non-JSON output keeps the
// old substring rule.
func botResultFailed(out string) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &m); err == nil {
		raw, ok := m["error"]
		if !ok {
			// No top-level error key: keep the old rule so a nested real error still counts.
			return strings.Contains(out, `"error"`)
		}
		v := strings.TrimSpace(string(raw))
		return v != "null" && v != `""` && v != "false"
	}
	return strings.Contains(out, `"error"`)
}

// botToolTerminal (wa_bot_terminal_tool_v1) reports whether a FAILED tool
// result is TERMINAL: retrying the same call can never succeed, so the model
// must not be sent back to retry it and the round must not count as a failed
// round for the loop guard. A terminal result is:
//   - an explicit refusal marked "terminal": true by the server (write
//     denials, tool-grant denials, panel-only queries), or
//   - a panel replay answer with a 4xx status (admin_call answered; the same
//     call will answer the same way), or
//   - an "operación desconocida" error (the named operation does not exist).
//
// Everything else (DB errors, timeouts, 5xx, 408/429) stays transient.
func botToolTerminal(out string) bool {
	if strings.Contains(out, "operación desconocida") {
		return true
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		return false
	}
	if raw, ok := m["terminal"]; ok && strings.TrimSpace(string(raw)) == "true" {
		return true
	}
	// Panel replay shape (admin_call / admin replay): {"tool":..., "status":<http code>, "ok":bool}.
	if _, hasTool := m["tool"]; hasTool {
		if raw, ok := m["status"]; ok {
			var code int
			if json.Unmarshal(raw, &code) == nil && code >= 400 && code < 500 && code != http.StatusRequestTimeout && code != http.StatusTooManyRequests {
				return true
			}
		}
	}
	return false
}

func newBotLoopGuard() *botLoopGuard { return &botLoopGuard{} }

func (g *botLoopGuard) observe(name string, input json.RawMessage, out string) {
	sum := sha256.Sum256([]byte(name + "\x00" + string(input) + "\x00" + out))
	key := hex.EncodeToString(sum[:8])
	// Consecutive repetition only: any different call/result resets the count,
	// so real progress never accumulates strikes and nothing grows with the turn.
	if key == g.lastKey {
		g.repeats++
	} else {
		g.lastKey, g.repeats = key, 1
	}
	if g.repeats >= botLoopRepeatLimit {
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
// tool on its last allowed call AND the grounded no-tools wrap-up also came
// back asking for a tool (or failed). It claims nothing about outcomes:
// counters and tool names stay in the logs only.
const botBudgetExhaustedText = "No he podido completar tu petición. He detenido el proceso antes de ejecutar más acciones. Algunas acciones anteriores podrían haberse aplicado; no puedo confirmar que todo haya quedado terminado."

// botGroundedAnswerNote drives the no-tools wrap-up call
// (wa_bot_grounded_wrapup_v1). It is sent as an extra user message (never
// appended to a tool_result) so it is the freshest instruction the model
// sees. The call runs with NO tools, so the model cannot ask for another
// tool: it must answer in text with what the tools already confirmed.
const botGroundedAnswerNote = "Es el último paso de este turno y ya no puedes llamar a más herramientas. Resume al usuario, en texto claro, SOLO lo que las herramientas ya confirmaron, y di explícitamente qué parte de la petición quedó sin hacer. No afirmes ningún cambio que una herramienta no haya confirmado."

// botRunAgentLoopWith is the provider-agnostic loop used by every caller.
func (s *Server) botRunAgentLoopWith(ctx context.Context, call botModelCaller, system string, messages []botMessage, tools []botToolDef, exec botToolExecutor) (botLoopResult, error) {
	// BOT_MAX_ITERATIONS semantics (wa_bot_noncap_v1): unset or 0 = NO cap (the
	// default). A positive value N is an explicit operator cap: the Nth model
	// call is the wrap-up call. Negative values are treated as unset.
	maxIter := s.cfg.BotMaxIterations
	if maxIter < 0 {
		maxIter = 0
	}
	guard := newBotLoopGuard()

	result := botLoopResult{}
	msgs := append([]botMessage{}, messages...)

	for i := 0; ; i++ {
		if err := ctx.Err(); err != nil {
			return s.botInterrupted(ctx, call, system, &result, msgs, "context_done", err)
		}
		result.Iterations = i + 1

		// Wrap-up call: only when the loop guard saw no progress, or when an
		// operator-set cap is reached. Never because of a call count by default.
		last := guard.stalled || (maxIter > 0 && i == maxIter-1)
		callSystem := system
		if last {
			callSystem = system + botBudgetOperatorNote
		}
		resp, used, err := call(ctx, callSystem, msgs, tools)
		if err != nil {
			return s.botInterrupted(ctx, call, system, &result, msgs, "model_call_failed", err)
		}
		result.ModelUsed = used

		// Append the assistant turn as-is (text + tool_use blocks).
		assistant := botMessage{Role: "assistant", Content: resp.Content}
		toolResults := make([]botBlock, 0, 2)
		roundFailed := 0
		terminalTool := ""
		roundDelivered := false
		if last {
			wantsTool := false
			for _, block := range resp.Content {
				if block.Type == "tool_use" {
					wantsTool = true
				}
			}
			if wantsTool {
				// Budget spent and the model still wants a tool: execute nothing
				// more (no hidden extra mutation). Before giving up with the
				// fixed text, try a grounded no-tools wrap-up: one extra call
				// with an EMPTY tool list, so the model cannot ask for a tool
				// and must answer in text with the facts already gathered.
				// wa_bot_grounded_wrapup_v1: prod 2026-10-23 (Bot Alquería,
				// 18 iter / 37 tools / 169s) ended reason=exhausted with the
				// fixed text even though every fact was already in the
				// transcript — the wrap-up model call simply asked for one
				// more tool. The grounded retry turns that dead end into a
				// real, honest answer.
				botCheckpointCtx(ctx, "wa_bot_iter_cap_v1", "exhausted reason=%s iterations=%d ran=%d failed=%d tools=%s", guard.reason, i+1, result.Ran, result.Failed, strings.Join(result.ToolCalls, ","))
				result.Exhausted = true
				if text := s.botGroundedWrapUp(ctx, call, callSystem, msgs); text != "" {
					msgs = append(msgs, botMessage{Role: "assistant", Content: []botBlock{{Type: "text", Text: text}}})
				} else {
					msgs = append(msgs, botMessage{Role: "assistant", Content: []botBlock{{Type: "text", Text: botBudgetExhaustedText}}})
				}
				result.Messages = msgs
				return result, nil
			}
		}
		for _, block := range resp.Content {
			if block.Type != "tool_use" {
				continue
			}
			result.ToolCalls = append(result.ToolCalls, block.Name)
			started := time.Now()
			out, err := exec(ctx, block.Name, block.Input)
			if err != nil {
				out = `{"error":` + jsonQuote(err.Error()) + `}`
			}
			result.Ran++
			failed := err != nil || botResultFailed(out)
			// wa_bot_terminal_tool_v1: a refusal the server knows is final must
			// not be retried by the model (see botToolTerminal).
			terminal := failed && botToolTerminal(out)
			if terminal && terminalTool == "" {
				terminalTool = block.Name
			}
			if failed {
				result.Failed++
				roundFailed++
			} else if botDeliveryTools[block.Name] {
				roundDelivered = true
			}
			// Coordination id: wa_bot_tool_result_v1 - one event per tool call:
			// name, ok/fail, error class (terminal vs transient), duration and a
			// bounded input preview. With the turn correlation id from ctx this
			// reconstructs call by call a turn like the 33-tool-call incident.
			// err.Error() is deliberately NOT recorded (provider errors can carry
			// URLs/tokens): only its fixed class, plus the tool's own domain
			// error text when the tool result reported one.
			botCheckpointCtx(ctx, "wa_bot_tool_result_v1", "iter=%d tool=%s ok=%t err_class=%s terminal=%t ms=%d input=%s result_error=%s",
				i+1, block.Name, !failed, botErrClass(err), botErrTerminal(err), time.Since(started).Milliseconds(),
				botDiagPreview(string(block.Input), 300), botResultErrorPreview(out, err))
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

		// wa_bot_terminal_tool_v1: a round with a terminal refusal never counts
		// as a failed guard round — the refusal is not the model's fault and the
		// loop is closed below instead of striking rounds.
		guard.endRound(roundFailed == len(toolResults) && terminalTool == "")
		msgs = append(msgs, assistant, botMessage{Role: "user", Content: toolResults})

		// Coordination id: wa_bot_early_stop_v1 - when every tool of this
		// response was a successful customer delivery (send_message,
		// send_contact...), the reply is out: skip the extra model call that
		// only produces end_turn (2-5 s per turn) and cannot double-send.
		if botAllDelivered(resp.Content, toolResults) {
			result.Messages = msgs
			return result, nil
		}

		if terminalTool != "" {
			// wa_bot_terminal_tool_v1: close the turn NOW instead of letting the
			// model burn iterations retrying a refusal. A terminal result is any
			// tool result with terminal:true OR an admin_call replay that returned
			// an HTTP 4xx (botToolTerminal). That second class usually fires while
			// OTHER tools in the same round DID succeed — prod 2026-10-23 16:47:
			// an 18-iter turn had already gathered the bookings + menu facts but
			// answered botBudgetExhaustedText because the close jumped straight to
			// the fixed text, discarding the sibling answers and skipping the
			// grounded wrap-up the normal exhausted path uses.
			//
			// Close terminally (no more tool calls) but answer honestly: try the
			// grounded no-tools wrap-up (wa_bot_grounded_wrapup_v1) so the customer
			// gets the facts already in the transcript; fall back to the fixed text
			// only when the wrap-up is not possible. When a delivery tool of this
			// same round already answered the customer (roundDelivered), send
			// nothing extra.
			botCheckpointCtx(ctx, "wa_bot_terminal_tool_v1", "tool=%s iterations=%d ran=%d failed=%d", terminalTool, i+1, result.Ran, result.Failed)
			result.Exhausted = true
			if !roundDelivered {
				text := s.botGroundedWrapUp(ctx, call, callSystem, msgs)
				if text == "" {
					text = botBudgetExhaustedText
				}
				msgs = append(msgs, botMessage{Role: "assistant", Content: []botBlock{{Type: "text", Text: text}}})
			}
			result.Messages = msgs
			return result, nil
		}
	}
}

// botGroundedWrapUp (wa_bot_grounded_wrapup_v1) makes ONE final model call
// with an EMPTY tool catalog so the model cannot ask for another tool and
// must answer in plain text with the facts already in the transcript. It
// returns the answer text, or "" when the call failed, the model still
// returned no text, or the turn context has no budget left for another call.
//
// The turn runs under a hard deadline (the conversation worker's 3-minute
// budget). The wrap-up is only attempted when enough time remains for one
// more model call; otherwise the caller falls back to the fixed text. The
// call uses context.WithoutCancel so an already-expired turn deadline does
// not kill it, but it gets its own short timeout so it cannot hang.
func (s *Server) botGroundedWrapUp(ctx context.Context, call botModelCaller, system string, msgs []botMessage) string {
	// Only spend the extra call when the transcript actually has tool
	// results to summarize; otherwise the fixed text is just as honest.
	if !botTranscriptHasToolResult(msgs) {
		return ""
	}
	// Respect the turn budget: if the deadline is too close for one more
	// model call, do not start it (it would die mid-flight and waste the
	// reserve the honest reply is meant to protect).
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= botGroundedWrapUpReserve {
		botCheckpointCtx(ctx, "wa_bot_grounded_wrapup_v1", "skipped reason=no_budget")
		return ""
	}
	// Fresh context that keeps the turn's values (tenant, tracing) but is
	// not cancelled with the turn, bounded by its own timeout.
	wrapCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), botGroundedWrapUpTimeout)
	defer cancel()
	wrapMsgs := append(append([]botMessage{}, msgs...), botUserText(botGroundedAnswerNote))
	resp, _, err := call(wrapCtx, system+botBudgetOperatorNote, wrapMsgs, nil)
	if err != nil {
		botCheckpointCtx(ctx, "wa_bot_grounded_wrapup_v1", "failed class=%s", botErrClass(err))
		return ""
	}
	text := botFinalAssistantText([]botMessage{{Role: "assistant", Content: resp.Content}})
	if strings.TrimSpace(text) == "" {
		botCheckpointCtx(ctx, "wa_bot_grounded_wrapup_v1", "empty")
		return ""
	}
	botCheckpointCtx(ctx, "wa_bot_grounded_wrapup_v1", "answered chars=%d", len(text))
	return text
}

// botGroundedWrapUpReserve is the minimum turn budget left for the grounded
// no-tools wrap-up call to be worth starting.
const botGroundedWrapUpReserve = 20 * time.Second

// botGroundedWrapUpTimeout bounds the grounded wrap-up call itself, so it can
// never hang a turn whose own deadline already passed.
const botGroundedWrapUpTimeout = 25 * time.Second

// botTranscriptHasToolResult reports whether the transcript already contains
// at least one tool_result worth summarizing.
func botTranscriptHasToolResult(msgs []botMessage) bool {
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == "tool_result" {
				return true
			}
		}
	}
	return false
}

// botInterruptedText is the fixed reply when the turn cannot continue (model
// call failed, context too long, or the turn deadline passed) after tools ran.
const botInterruptedText = "No he podido terminar tu petición: el proceso se interrumpió antes de acabar. Algunas acciones anteriores podrían haberse aplicado; no puedo confirmar que todo haya quedado terminado."

// botInterrupted ends the turn without any further model or tool call. Before
// any tool ran there is nothing to report, so the error goes to the caller
// (which sends its usual fallback). After tools ran, the customer gets the
// fixed honest reply instead of a generic apology.
// botErrClass maps an error to a fixed, safe label.
func botErrClass(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	low := strings.ToLower(err.Error())
	switch {
	case strings.Contains(low, "context length") || strings.Contains(low, "context window") || strings.Contains(low, "too long") || strings.Contains(low, "maximum context"):
		return "context_length"
	case strings.Contains(low, "timeout") || strings.Contains(low, "deadline"):
		return "timeout"
	}
	return "provider_error"
}

// botErrTerminal reports whether an error class means the turn cannot be
// retried as-is (terminal) rather than a transient hiccup a retry could fix.
func botErrTerminal(err error) bool {
	switch botErrClass(err) {
	case "canceled", "context_length":
		return true
	}
	return false
}

// botDiagPreview bounds and flattens a value so it fits one diagnostic line.
func botDiagPreview(v string, max int) string {
	v = strings.Join(strings.Fields(v), " ")
	if len(v) > max {
		return v[:max] + "…"
	}
	return v
}

// botResultErrorPreview extracts the bounded error message of a failed tool
// result (the top-level {"error": ...} the executor produces). Empty unless
// the tool itself reported a domain error; provider transport errors stay
// class-only.
func botResultErrorPreview(out string, execErr error) string {
	if execErr != nil {
		return ""
	}
	var m struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(out), &m) != nil || m.Error == "" {
		return ""
	}
	return botDiagPreview(m.Error, 200)
}

func (s *Server) botInterrupted(ctx context.Context, call botModelCaller, system string, result *botLoopResult, msgs []botMessage, reason string, err error) (botLoopResult, error) {
	result.Messages = msgs
	if result.Ran == 0 {
		return *result, err
	}
	// err.Error() is never logged: provider errors can carry URLs, query
	// strings, tokens or customer content. Only a fixed class is recorded.
	botCheckpointCtx(ctx, "wa_bot_iter_cap_v1", "interrupted reason=%s class=%s terminal=%t iterations=%d ran=%d failed=%d tools=%s", reason, botErrClass(err), botErrTerminal(err), result.Iterations, result.Ran, result.Failed, strings.Join(result.ToolCalls, ","))
	result.Interrupted = true
	// wa_bot_interrupted_grounded_v1: a deadline-exhausted turn already holds
	// the facts in its tool_results. Answer with the grounded no-tools wrap-up
	// (wa_bot_grounded_wrapup_v1) instead of the fixed interrupted text; fall
	// back to that text only when the wrap-up cannot run or returns nothing.
	// Prod 2026-10-23 17:12: a 27-iter turn hit the 3-min deadline after
	// gathering the booking + menu data and answered botInterruptedText even
	// though a grounded summary was possible.
	text := s.botGroundedWrapUp(ctx, call, system, msgs)
	if text == "" {
		text = botInterruptedText
	}
	result.Messages = append(msgs, botMessage{Role: "assistant", Content: []botBlock{{Type: "text", Text: text}}})
	return *result, nil
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
		if botResultFailed(r.Content) {
			return false
		}
	}
	return true
}
