package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Coordination id: wa_bot_ai_providers_v1 - OpenAI-compatible wire adapter.
// The agent loop speaks Anthropic content blocks; this adapter converts them
// to chat/completions (OpenCode Go) and back, so every provider shares one loop.

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    string       `json:"content"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

func botToOpenAIMessages(system string, messages []botMessage) []oaMessage {
	out := []oaMessage{{Role: "system", Content: system}}
	for _, m := range messages {
		var text []string
		var calls []oaToolCall
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				if strings.TrimSpace(b.Text) != "" {
					text = append(text, b.Text)
				}
			case "tool_use":
				c := oaToolCall{ID: b.ID, Type: "function"}
				c.Function.Name = b.Name
				c.Function.Arguments = string(b.Input)
				if c.Function.Arguments == "" {
					c.Function.Arguments = "{}"
				}
				calls = append(calls, c)
			case "tool_result":
				out = append(out, oaMessage{Role: "tool", ToolCallID: b.ToolUseID, Content: b.Content})
			}
		}
		if len(text) == 0 && len(calls) == 0 {
			continue
		}
		out = append(out, oaMessage{Role: m.Role, Content: strings.Join(text, "\n"), ToolCalls: calls})
	}
	return out
}

func botToOpenAITools(tools []botToolDef) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{"type": "function", "function": map[string]any{
			"name": t.Name, "description": t.Description, "parameters": t.InputSchema,
		}})
	}
	return out
}

// botOpenAICall runs one chat/completions request and maps it to botLLMResponse.
func (s *Server) botOpenAICall(ctx context.Context, p botAIProvider, apiKey, model, sessionID, system string, messages []botMessage, tools []botToolDef) (botLLMResponse, error) {
	maxTokens := s.cfg.BotMaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	body := map[string]any{"model": model, "max_tokens": maxTokens, "messages": botToOpenAIMessages(system, messages)}
	if len(tools) > 0 {
		body["tools"] = botToOpenAITools(tools)
	}
	raw, _ := json.Marshal(body)
	timeout := s.cfg.BotTimeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, strings.TrimRight(p.BaseURL, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return botLLMResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	// OpenCode Go refuses requests without a session id (MissingSessionID).
	req.Header.Set("x-opencode-session", sessionID)
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return botLLMResponse{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return botLLMResponse{}, fmt.Errorf("%s http %d: %s", p.ID, resp.StatusCode, truncate(string(data), 300))
	}
	var parsed struct {
		Choices []struct {
			FinishReason string    `json:"finish_reason"`
			Message      oaMessage `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return botLLMResponse{}, err
	}
	if parsed.Error != nil {
		return botLLMResponse{}, fmt.Errorf("%s error: %s", p.ID, parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return botLLMResponse{}, fmt.Errorf("%s: empty response", p.ID)
	}
	ch := parsed.Choices[0]
	out := botLLMResponse{StopReason: ch.FinishReason}
	if t := strings.TrimSpace(ch.Message.Content); t != "" {
		out.Content = append(out.Content, botBlock{Type: "text", Text: t})
	}
	for _, c := range ch.Message.ToolCalls {
		args := strings.TrimSpace(c.Function.Arguments)
		if args == "" || !json.Valid([]byte(args)) {
			args = "{}"
		}
		out.Content = append(out.Content, botBlock{Type: "tool_use", ID: c.ID, Name: c.Function.Name, Input: json.RawMessage(args)})
	}
	if ch.FinishReason == "length" && len(out.Content) == 0 {
		return botLLMResponse{}, fmt.Errorf("%s: token limit reached", p.ID)
	}
	return out, nil
}
