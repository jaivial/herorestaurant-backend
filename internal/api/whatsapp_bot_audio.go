package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Coordination id: wa_bot_audio_transcription_v1
//
// Voice notes arrive inline as base64 (Evolution webhookBase64). They are sent
// to the pipeline sidecar's /transcribe (faster-whisper "small", CPU int8,
// auto language) and the transcript enters the normal text flow. The audio is
// never persisted; only the transcript is stored, prefixed so staff and the
// model know it came from a voice note.

const botAudioTranscriptPrefix = "🎤 (audio transcrito) "

func (s *Server) botTranscribeAudio(ctx context.Context, restaurantID int, msg botWebhookMessage) string {
	audio := msg.AudioB64
	if audio == "" {
		audio = s.botFetchAudioBase64(ctx, restaurantID, msg.MessageID)
	}
	if audio == "" {
		return ""
	}
	body, _ := json.Marshal(map[string]any{"audio_b64": audio, "restaurant_id": restaurantID})
	reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, s.botPipelineURL()+"/transcribe", bytes.NewReader(body))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	started := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("[bot] checkpoint wa_bot_audio_transcription_v1 restaurant_id=%d sender=%s error=%v", restaurantID, msg.Sender, err)
		return ""
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Text     string `json:"text"`
		Language string `json:"language"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &out) != nil {
		log.Printf("[bot] checkpoint wa_bot_audio_transcription_v1 restaurant_id=%d sender=%s status=%d", restaurantID, msg.Sender, resp.StatusCode)
		return ""
	}
	text := strings.TrimSpace(out.Text)
	log.Printf("[bot] checkpoint wa_bot_audio_transcription_v1 restaurant_id=%d sender=%s lang=%s chars=%d ms=%d",
		restaurantID, msg.Sender, out.Language, len(text), time.Since(started).Milliseconds())
	return text
}

// botFetchAudioBase64 downloads a voice note from Evolution when the webhook
// did not inline it (instances registered with webhookBase64=false).
func (s *Server) botFetchAudioBase64(ctx context.Context, restaurantID int, messageID string) string {
	if strings.TrimSpace(messageID) == "" {
		return ""
	}
	gw, ok := s.botGatewayFor(ctx, restaurantID)
	if !ok {
		return ""
	}
	evo, ok := gw.(*evolutionGateway)
	if !ok {
		return ""
	}
	resp, code, err := evo.request(ctx, http.MethodPost, "/chat/getBase64FromMediaMessage/"+evo.instanceName, map[string]any{
		"message": map[string]any{"key": map[string]any{"id": messageID}}, "convertToMp4": false,
	})
	if err != nil || code < 200 || code >= 300 {
		log.Printf("[bot] checkpoint wa_bot_audio_transcription_v1 restaurant_id=%d fetch_failed code=%d err=%v", restaurantID, code, err)
		return ""
	}
	b64, _ := resp["base64"].(string)
	return strings.TrimSpace(b64)
}
