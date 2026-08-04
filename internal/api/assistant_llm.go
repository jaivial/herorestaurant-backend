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
)

// assistantMaxFrameRunes caps each delta frame relayed to the WebSocket client
// (chunks are split on rune boundaries so UTF-8 never breaks mid-sequence).
const assistantMaxFrameRunes = 120

// assistantChatMessage is one conversation turn sent to the LLM.
type assistantChatMessage struct {
	Role    string
	Content string
}

// assistantStream calls the MiniMax Anthropic-compatible Messages API
// ({MINIMAX_BASE_URL}/v1/messages) with stream:true and relays text deltas to
// emit. Long deltas are split into assistantMaxFrameRunes-rune chunks.
func (s *Server) assistantStream(ctx context.Context, system string, msgs []assistantChatMessage, emit func(chunk string) error) error {
	apiKey := strings.TrimSpace(s.cfg.MiniMaxAPIKey)
	if apiKey == "" {
		return errors.New("minimax api key not configured")
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
		content = append(content, map[string]any{
			"role":    m.Role,
			"content": []map[string]any{{"type": "text", "text": m.Content}},
		})
	}

	raw, err := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"system":     system,
		"messages":   content,
		"stream":     true,
	})
	if err != nil {
		return err
	}

	timeout := s.cfg.AssistantTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.MiniMaxBaseURL, "/")+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("minimax http %d", resp.StatusCode)
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
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
			Type  string `json:"type"`
			Delta *struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
			Error *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue // tolerate unknown frames
		}
		switch ev.Type {
		case "content_block_delta":
			if ev.Delta != nil && ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				for _, chunk := range splitRunes(ev.Delta.Text, assistantMaxFrameRunes) {
					if err := emit(chunk); err != nil {
						return err
					}
				}
			}
		case "error":
			msg := "minimax sse error"
			if ev.Error != nil {
				msg += ": " + ev.Error.Message
			}
			return errors.New(msg)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return nil
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
		"Responde en español, sé breve, amable y con un toque de humor. Eres el asistente de IA del restaurante."
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
