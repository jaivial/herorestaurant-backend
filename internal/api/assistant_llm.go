package api

import (
	"bufio"
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

// assistantMaxFrameRunes caps each delta frame relayed to the WebSocket client
// (chunks are split on rune boundaries so UTF-8 never breaks mid-sequence).
const assistantMaxFrameRunes = 120

// assistantChatMessage is one conversation turn sent to the LLM.
type assistantChatMessage struct {
	Role    string
	Content any
}

// assistantToolDef uses Anthropic-compatible custom tools. Tool execution is
// always server-side and tenant-scoped; model never receives raw DB access.
type assistantToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type assistantToolUse struct {
	ID    string
	Name  string
	Input json.RawMessage
}

type assistantLLMResult struct {
	Text       string
	ToolUses   []assistantToolUse
	StopReason string
}

// assistantStream calls the MiniMax Anthropic-compatible Messages API
// ({MINIMAX_BASE_URL}/v1/messages) with stream:true and relays text deltas to
// emit. Long deltas are split into assistantMaxFrameRunes-rune chunks.
func (s *Server) assistantStream(ctx context.Context, system string, msgs []assistantChatMessage, emit func(chunk string) error) error {
	_, err := s.assistantCall(ctx, system, msgs, nil, emit)
	return err
}

// assistantCall executes one model turn. Kept non-streaming for tool turns so
// tool_use blocks can be persisted and answered before the next model turn.
func (s *Server) assistantCall(ctx context.Context, system string, msgs []assistantChatMessage, tools []assistantToolDef, emit func(string) error) (result assistantLLMResult, err error) {
	apiKey := strings.TrimSpace(s.cfg.MiniMaxAPIKey)
	if apiKey == "" {
		return assistantLLMResult{}, errors.New("minimax api key not configured")
	}

	model := strings.TrimSpace(s.cfg.AssistantModel)
	if model == "" {
		model = strings.TrimSpace(s.cfg.MiniMaxModel)
	}
	maxTokens := s.cfg.AssistantMaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	content := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		messageContent := m.Content
		if text, ok := m.Content.(string); ok {
			messageContent = []map[string]any{{"type": "text", "text": text}}
		}
		content = append(content, map[string]any{"role": m.Role, "content": messageContent})
	}

	body := map[string]any{
		"model": model, "max_tokens": maxTokens, "system": system,
		"messages": content, "stream": emit != nil,
	}
	if len(tools) > 0 {
		body["tools"] = tools
		// Explicitly select automatic tool routing for MiniMax-compatible
		// endpoints; some deployments otherwise silently ignore the tools array.
		body["tool_choice"] = map[string]any{"type": "auto"}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return assistantLLMResult{}, err
	}

	timeout := s.cfg.AssistantTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.MiniMaxBaseURL, "/")+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return assistantLLMResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return assistantLLMResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return assistantLLMResult{}, fmt.Errorf("minimax http %d", resp.StatusCode)
	}

	if emit == nil {
		// Non-streaming tool turns return one regular JSON message (not SSE).
		// Parse it directly; treating it as SSE silently discarded all tool_use
		// blocks from MiniMax's Anthropic-compatible endpoint.
		b, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return result, readErr
		}
		var msg struct {
			StopReason string `json:"stop_reason"`
			Content    []struct {
				Type  string          `json:"type"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Text  string          `json:"text"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		}
		if err := json.Unmarshal(b, &msg); err != nil {
			return result, fmt.Errorf("minimax response decode: %w", err)
		}
		result.StopReason = msg.StopReason
		for _, block := range msg.Content {
			if block.Type == "text" {
				result.Text += block.Text
			}
			if block.Type == "tool_use" {
				result.ToolUses = append(result.ToolUses, assistantToolUse{ID: block.ID, Name: block.Name, Input: block.Input})
			}
		}
		return result, nil
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	// Tool inputs are streamed as JSON fragments. Keep one accumulator per
	// content block; MiniMax may emit content_block_start before any input.
	toolInputs := make(map[int]string)
	toolIndexes := make(map[string]int)
	ensureTool := func(index int, id, name string, input json.RawMessage) int {
		if existing, ok := toolIndexes[id]; ok && id != "" {
			return existing
		}
		if index < 0 {
			index = len(result.ToolUses)
		}
		for len(result.ToolUses) <= index {
			result.ToolUses = append(result.ToolUses, assistantToolUse{})
		}
		result.ToolUses[index] = assistantToolUse{ID: id, Name: name, Input: input}
		if id != "" {
			toolIndexes[id] = index
		}
		return index
	}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var ev struct {
			Type       string `json:"type"`
			StopReason string `json:"stop_reason"`
			Content    []struct {
				Index          int `json:"index"`
				Type, ID, Name string
				Text           string          `json:"text"`
				Input          json.RawMessage `json:"input"`
			} `json:"content"`
			Delta *struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
			Error *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue // tolerate unknown frames
		}
		if ev.StopReason != "" {
			result.StopReason = ev.StopReason
		}
		switch ev.Type {
		case "message_start":
		case "message_stop":
		case "content_block_start":
			for i, block := range ev.Content {
				if block.Type == "tool_use" {
					idx := block.Index
					if idx == 0 && i > 0 {
						idx = i
					}
					idx = ensureTool(idx, block.ID, block.Name, block.Input)
					if len(block.Input) > 0 && string(block.Input) != "null" {
						toolInputs[idx] = string(block.Input)
					}
				}
			}
		case "message":
			for _, block := range ev.Content {
				if block.Type == "text" && block.Text != "" {
					result.Text += block.Text
					if emit != nil {
						if err := emit(block.Text); err != nil {
							return result, err
						}
					}
				}
				if block.Type == "tool_use" {
					ensureTool(len(result.ToolUses), block.ID, block.Name, block.Input)
				}
			}
		case "content_block_delta":
			if ev.Delta != nil && ev.Delta.Type == "input_json_delta" {
				// Index is supplied by real MiniMax events; absent index is
				// tolerated by attaching to the latest tool block.
				idx := len(result.ToolUses) - 1
				toolInputs[idx] += ev.Delta.PartialJSON
				if idx >= 0 {
					result.ToolUses[idx].Input = json.RawMessage(toolInputs[idx])
				}
			}
			if ev.Delta != nil && ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				for _, chunk := range splitRunes(ev.Delta.Text, assistantMaxFrameRunes) {
					result.Text += chunk
					if emit == nil {
						continue
					}
					if err := emit(chunk); err != nil {
						return result, err
					}
				}
			}
		case "error":
			msg := "minimax sse error"
			if ev.Error != nil {
				msg += ": " + ev.Error.Message
			}
			return result, errors.New(msg)
		}
	}
	if err := sc.Err(); err != nil {
		return result, err
	}
	return result, nil
}

// splitRunes splits s into chunks of at most n runes each (rune-safe).
func splitRunes(s string, n int) []string {
	runes := []rune(s)
	if len(runes) <= n {
		return []string{s}
	}
	out := make([]string, 0, (len(runes)+n-1)/n)
	for i := 0; i < len(runes); i += n {
		end := i + n
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[i:end]))
	}
	return out
}

// buildAssistantSystemPrompt renders the Forky persona prompt. Restaurant
// context (name/phone) is appended when a DB is available; a nil db yields the
// generic prompt.
func (s *Server) buildAssistantSystemPrompt(ctx context.Context, restaurantID int) string {
	const base = "Eres Forky, el asistente de IA del restaurante. " +
		"Responde en español, sé breve, amable y con un toque de humor. Eres el asistente de IA del restaurante. " +
		"Para cualquier dato factual del restaurante (nombre, reservas, menú, stock, POS, clientes o analítica), DEBES usar la herramienta correspondiente antes de responder; nunca inventes datos ni digas que careces de acceso si existe una herramienta."
	if s.db == nil || restaurantID <= 0 {
		return base
	}
	var name, phone string
	if err := s.db.QueryRowContext(ctx, `SELECT name, phone FROM restaurants WHERE id = ?`, restaurantID).Scan(&name, &phone); err != nil {
		return base
	}
	var sb strings.Builder
	sb.WriteString(base)
	if name != "" {
		sb.WriteString(" El restaurante se llama " + name + ".")
	}
	if phone != "" {
		sb.WriteString(" Teléfono del restaurante: " + phone + ".")
	}
	return sb.String()
}
