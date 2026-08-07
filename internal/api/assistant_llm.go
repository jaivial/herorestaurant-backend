package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
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

// assistantStream calls the Messages API through the official Anthropic SDK.
func (s *Server) assistantStream(ctx context.Context, system string, msgs []assistantChatMessage, emit func(chunk string) error) error {
	_, err := s.assistantCall(ctx, system, msgs, nil, emit)
	return err
}

// assistantCall uses the SDK for request construction, authentication,
// streaming, tool blocks, and response decoding. The configured MiniMax
// endpoint is Anthropic-compatible, so it is supplied as the SDK base URL.
func (s *Server) assistantCall(ctx context.Context, system string, msgs []assistantChatMessage, tools []assistantToolDef, emit func(string) error) (result assistantLLMResult, err error) {
	apiKey := strings.TrimSpace(s.cfg.MiniMaxAPIKey)
	if apiKey == "" {
		return result, errors.New("minimax api key not configured")
	}
	model := strings.TrimSpace(s.cfg.AssistantModel)
	if model == "" {
		model = strings.TrimSpace(s.cfg.MiniMaxModel)
	}
	maxTokens := s.cfg.AssistantMaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	timeout := s.cfg.AssistantTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Marshal the existing persisted block representation into SDK params. This
	// preserves text, tool_use and tool_result blocks without lossy conversions.
	messageContent := make([]map[string]any, 0, len(msgs))
	messageParams := make([]anthropic.MessageParam, 0, len(msgs))
	for _, m := range msgs {
		itemContent := m.Content
		if text, ok := itemContent.(string); ok {
			itemContent = []map[string]any{{"type": "text", "text": text}}
		}
		messageContent = append(messageContent, map[string]any{"role": m.Role, "content": itemContent})
		blocks, marshalErr := json.Marshal(map[string]any{"role": m.Role, "content": itemContent})
		if marshalErr != nil {
			return result, marshalErr
		}
		var message anthropic.MessageParam
		if unmarshalErr := json.Unmarshal(blocks, &message); unmarshalErr != nil {
			return result, unmarshalErr
		}
		messageParams = append(messageParams, message)
	}
	body := map[string]any{"model": model, "max_tokens": maxTokens, "system": []map[string]string{{"type": "text", "text": system}}, "messages": messageParams}
	if len(tools) > 0 {
		body["tools"] = tools
		body["tool_choice"] = map[string]any{"type": "auto"}
	}
	raw, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		return result, marshalErr
	}
	var params anthropic.MessageNewParams
	if unmarshalErr := json.Unmarshal(raw, &params); unmarshalErr != nil {
		return result, unmarshalErr
	}
	client := anthropic.NewClient(option.WithAPIKey(apiKey), option.WithBaseURL(strings.TrimRight(s.cfg.MiniMaxBaseURL, "/")+"/"))
	if emit == nil {
		message, callErr := client.Messages.New(ctx, params)
		if callErr != nil {
			return result, callErr
		}
		result.StopReason = string(message.StopReason)
		for _, block := range message.Content {
			if block.Type == "text" {
				result.Text += block.Text
			}
			if block.Type == "tool_use" {
				result.ToolUses = append(result.ToolUses, assistantToolUse{ID: block.ID, Name: block.Name, Input: block.Input})
			}
		}
		return result, nil
	}
	// MiniMax's deployed SSE dialect omits `event:` lines and is not accepted by
	// the SDK stream decoder. Keep the official SDK for non-streaming tool turns,
	// but use the compatible raw SSE transport for streamed UI text.
	streamBody := map[string]any{"model": model, "max_tokens": maxTokens, "system": system, "messages": messageContent, "stream": true}
	if len(tools) > 0 {
		streamBody["tools"] = tools
		streamBody["tool_choice"] = map[string]any{"type": "auto"}
	}
	streamRaw, marshalErr := json.Marshal(streamBody)
	if marshalErr != nil {
		return result, marshalErr
	}
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.MiniMaxBaseURL, "/")+"/v1/messages", bytes.NewReader(streamRaw))
	if reqErr != nil {
		return result, reqErr
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, reqErr := http.DefaultClient.Do(req)
	if reqErr != nil {
		return result, reqErr
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("minimax http %d", resp.StatusCode)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var frame struct {
			Type       string `json:"type"`
			StopReason string `json:"stop_reason"`
			Content    []struct {
				Type, ID, Name, Text string
				Input                json.RawMessage `json:"input"`
			} `json:"content"`
			Delta *struct{ Type, Text, PartialJSON string } `json:"delta"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(data), &frame) != nil {
			continue
		}
		if frame.Type == "error" {
			message := "minimax sse error"
			if frame.Error != nil && frame.Error.Message != "" {
				message += ": " + frame.Error.Message
			}
			return result, errors.New(message)
		}
		if frame.StopReason != "" {
			result.StopReason = frame.StopReason
		}
		for _, block := range frame.Content {
			if block.Type == "tool_use" {
				result.ToolUses = append(result.ToolUses, assistantToolUse{ID: block.ID, Name: block.Name, Input: block.Input})
			}
		}
		if frame.Delta == nil {
			continue
		}
		if frame.Delta.Type == "text_delta" {
			for _, chunk := range splitRunes(frame.Delta.Text, assistantMaxFrameRunes) {
				result.Text += chunk
				if err := emit(chunk); err != nil {
					return result, err
				}
			}
		}
		if frame.Delta.Type == "input_json_delta" && len(result.ToolUses) > 0 {
			i := len(result.ToolUses) - 1
			result.ToolUses[i].Input = append(result.ToolUses[i].Input, []byte(frame.Delta.PartialJSON)...)
		}
	}
	if err := scanner.Err(); err != nil {
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
	sb.WriteString(" Hoy es " + time.Now().Format("2006-01-02") + ". " +
		"Para consultas como «esta semana», «la semana que viene», «próxima semana» o «este mes», " +
		"calcula las fechas exactas (YYYY-MM-DD) usando la fecha de hoy y pasa los rangos a la herramienta correspondiente.")
	if name != "" {
		sb.WriteString(" El restaurante se llama " + name + ".")
	}
	if phone != "" {
		sb.WriteString(" Teléfono del restaurante: " + phone + ".")
	}
	return sb.String()
}
