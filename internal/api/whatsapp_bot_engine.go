package api

import (
	"context"
	"encoding/json"
)

// botToolExecutor executes a named tool with JSON input and returns a JSON
// string fed back to the LLM as tool_result.
type botToolExecutor func(ctx context.Context, name string, input json.RawMessage) (string, error)

// botLoopResult summarizes one agent run.
type botLoopResult struct {
	Iterations int
	ToolCalls  []string
	Messages   []botMessage // full transcript including tool turns
}

// botRunAgentLoop drives the LLM tool-use loop: call the model, execute any
// tool_use blocks, feed tool_result back, repeat until end_turn or the
// iteration cap.
func (s *Server) botRunAgentLoop(ctx context.Context, system string, messages []botMessage, tools []botToolDef, exec botToolExecutor) (botLoopResult, error) {
	maxIter := s.cfg.BotMaxIterations
	if maxIter <= 0 {
		maxIter = 8
	}

	result := botLoopResult{}
	msgs := append([]botMessage{}, messages...)

	for i := 0; i < maxIter; i++ {
		result.Iterations = i + 1

		resp, err := s.botLLMCall(ctx, system, msgs, tools)
		if err != nil {
			result.Messages = msgs
			return result, err
		}

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
