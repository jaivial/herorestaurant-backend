package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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
// Kept for the legacy MiniMax-only call sites; the WhatsApp agent loop uses
// botLLMCallRouted (primary + fallback providers).
func (s *Server) botLLMCall(ctx context.Context, restaurantID int, modelOverride string, system string, messages []botMessage, tools []botToolDef) (botLLMResponse, error) {
	apiKey := s.resolveMiniMaxKey(ctx, restaurantID)
	if apiKey == "" {
		return botLLMResponse{}, errors.New("minimax api key not configured")
	}

	model := strings.TrimSpace(modelOverride)
	if model == "" {
		m := s.resolvedMiniMax(ctx, restaurantID).Model // DB config when present
		if m == "" {
			m = strings.TrimSpace(s.cfg.BotModel)
		}
		if m == "" {
			m = strings.TrimSpace(s.cfg.MiniMaxModel)
		}
		if m == "" {
			m = "MiniMax-M3"
		}
		model = m
	}
	return s.botAnthropicCall(ctx, strings.TrimRight(s.cfg.MiniMaxBaseURL, "/"), apiKey, model, system, messages, tools)
}

// botAnthropicCall performs one Anthropic-compatible Messages API request.
func (s *Server) botAnthropicCall(ctx context.Context, baseURL, apiKey, model, system string, messages []botMessage, tools []botToolDef) (botLLMResponse, error) {
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

	url := baseURL + "/v1/messages"
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
	kept := parsed.Content[:0]
	for _, b := range parsed.Content {
		if b.Type == "text" || b.Type == "tool_use" {
			kept = append(kept, b)
		}
	}
	parsed.Content = kept
	if parsed.StopReason == "max_tokens" && len(kept) == 0 {
		return botLLMResponse{}, errors.New("minimax bot: token limit reached")
	}
	return parsed, nil
}

// botLLMCallRouted calls the restaurant's primary model and, when it fails
// (HTTP error, quota/token limit, timeout, missing key), retries the same turn
// on the fallback model so the customer always gets an answer.
// Coordination id: wa_bot_ai_providers_v1
func (s *Server) botLLMCallRouted(ctx context.Context, restaurantID int, routing botAIRouting, sessionID, system string, messages []botMessage, tools []botToolDef) (botLLMResponse, string, error) {
	refs := []string{routing.PrimaryModel}
	if routing.FallbackModel != "" && routing.FallbackModel != routing.PrimaryModel {
		refs = append(refs, routing.FallbackModel)
	}
	var lastErr error
	for i, ref := range refs {
		resp, err := s.botCallModelRef(ctx, restaurantID, ref, sessionID, system, messages, tools)
		if err == nil {
			if i > 0 {
				log.Printf("[bot] checkpoint wa_bot_llm_fallback_used restaurant_id=%d model=%s", restaurantID, ref)
			}
			return resp, ref, nil
		}
		log.Printf("[bot] checkpoint wa_bot_llm_call_failed restaurant_id=%d model=%s err=%v", restaurantID, ref, err)
		lastErr = err
	}
	return botLLMResponse{}, "", lastErr
}

func (s *Server) botCallModelRef(ctx context.Context, restaurantID int, ref, sessionID, system string, messages []botMessage, tools []botToolDef) (botLLMResponse, error) {
	providerID, model := botSplitModelRef(ref)
	p, ok := botFindProvider(providerID)
	if !ok {
		return botLLMResponse{}, fmt.Errorf("unknown provider %q", providerID)
	}
	key := s.botProviderKey(ctx, restaurantID, p.ID)
	if key == "" {
		return botLLMResponse{}, fmt.Errorf("%s api key not configured", p.ID)
	}
	if p.Wire == "openai" {
		return s.botOpenAICall(ctx, p, key, model, sessionID, system, messages, tools)
	}
	return s.botAnthropicCall(ctx, strings.TrimRight(s.cfg.MiniMaxBaseURL, "/"), key, model, system, messages, tools)
}
