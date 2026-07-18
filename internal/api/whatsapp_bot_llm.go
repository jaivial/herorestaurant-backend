package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// botBlock is a single content block in the Anthropic-compatible Messages API
// that MiniMax exposes. It covers text, tool_use and tool_result blocks.
type botBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

// botMessage is one conversation turn sent to the LLM.
type botMessage struct {
	Role    string     `json:"role"`
	Content []botBlock `json:"content"`
}

// botToolDef mirrors an Anthropic tool definition.
type botToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// botLLMResponse is the parsed Messages API response.
type botLLMResponse struct {
	StopReason string     `json:"stop_reason"`
	Content    []botBlock `json:"content"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func botUserText(text string) botMessage {
	return botMessage{Role: "user", Content: []botBlock{{Type: "text", Text: text}}}
}

func botToolResult(toolUseID string, content string) botMessage {
	return botMessage{Role: "user", Content: []botBlock{{
		Type:      "tool_result",
		ToolUseID: toolUseID,
		Content:   content,
	}}}
}

// botLLMCall performs one Messages API request with tools against MiniMax
// using the same credentials as the translation system. modelOverride, when
// non-empty, takes precedence over the configured BotModel (per-tenant knob).
func (s *Server) botLLMCall(ctx context.Context, modelOverride string, system string, messages []botMessage, tools []botToolDef) (botLLMResponse, error) {
	apiKey := strings.TrimSpace(s.cfg.MiniMaxAPIKey)
	if apiKey == "" {
		return botLLMResponse{}, errors.New("minimax api key not configured")
	}

	model := strings.TrimSpace(modelOverride)
	if model == "" {
		model = strings.TrimSpace(s.cfg.BotModel)
	}
	if model == "" {
		model = strings.TrimSpace(s.cfg.MiniMaxModel)
	}
	maxTokens := s.cfg.BotMaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	reqBody := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"system":     system,
		"messages":   messages,
	}
	if len(tools) > 0 {
		reqBody["tools"] = tools
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return botLLMResponse{}, err
	}

	url := strings.TrimRight(s.cfg.MiniMaxBaseURL, "/") + "/v1/messages"
	timeout := s.cfg.BotTimeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return botLLMResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	cli := &http.Client{Timeout: timeout}
	resp, err := cli.Do(httpReq)
	if err != nil {
		return botLLMResponse{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return botLLMResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return botLLMResponse{}, fmt.Errorf("minimax bot http %d: %s", resp.StatusCode, truncate(string(body), 300))
	}

	var parsed botLLMResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return botLLMResponse{}, err
	}
	if parsed.Error != nil {
		return botLLMResponse{}, fmt.Errorf("minimax bot error: %s", parsed.Error.Type)
	}
	return parsed, nil
}
