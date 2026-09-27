package api

import (
	"context"
	"encoding/json"
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

// botRunAgentLoopWith is the provider-agnostic loop used by every caller.
func (s *Server) botRunAgentLoopWith(ctx context.Context, call botModelCaller, system string, messages []botMessage, tools []botToolDef, exec botToolExecutor) (botLoopResult, error) {
	maxIter := s.cfg.BotMaxIterations
	if maxIter <= 0 {
		maxIter = 8
	}

	result := botLoopResult{}
	msgs := append([]botMessage{}, messages...)

	for i := 0; i < maxIter; i++ {
		result.Iterations = i + 1

		resp, used, err := call(ctx, system, msgs, tools)
		if err != nil {
			result.Messages = msgs
			return result, err
		}
		result.ModelUsed = used

		// Append the assistant turn as-is (text + tool_use blocks).
		assistant := botMessage{Role: "assistant", Content: resp.Content}
		toolResults := make([]botBlock, 0, 2)
		for _, block := range resp.Content {
			if block.Type != "tool_use" {
				continue
			}
			result.ToolCalls = append(result.ToolCalls, block.Name)
			out, err := exec(ctx, block.Name, block.Input)
			if err != nil {
				out = `{"error":` + jsonQuote(err.Error()) + `}`
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
	}

	result.Messages = msgs
	return result, nil
}

func jsonQuote(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}
