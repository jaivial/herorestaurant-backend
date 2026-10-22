package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Coordination ids: wa_bot_stt_bark_v1 (engine selection) and
// wa_bot_audio_transcription_v1 (the flow itself).
//
// Voice notes arrive inline as base64 (Evolution webhookBase64). They are
// transcribed by the pipeline sidecar /transcribe and the transcript enters
// the normal text flow. The audio is never persisted; only the transcript is
// stored, prefixed so staff and the model know it came from a voice note.
//
// Engine: faster-whisper (the sidecar's local model, warmed at startup) is
// the DEFAULT through the sidecar. Bark 3.0 (Underdog, coord wa_bot_stt_bark_v1)
// stays reachable through the sidecar's `engine` field, but it is English-only
// and pays a cold model download on its first call (measured 51 s returning
// lang=en for Spanish audio), which is why it no longer leads the flow.
// BOT_STT_ENGINE ("bark"|"whisper", default "whisper") picks the engine;
// BOT_STT_LANGUAGE optionally pins the language.

const botAudioTranscriptPrefix = "🎤 (audio transcrito) "

// botSTTEngine returns the transcription engine for voice notes.
func botSTTEngine() string {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("BOT_STT_ENGINE"))); v != "" {
		return v
	}
	return "whisper"
}

// botTranscribeAudio transcribes one voice note: engine first, automatic
// whisper fallback when the engine yields nothing.
func (s *Server) botTranscribeAudio(ctx context.Context, restaurantID int, msg botWebhookMessage) string {
	audio := msg.AudioB64
	if audio == "" {
		audio = s.botFetchMediaBase64(ctx, restaurantID, msg.MessageID)
	}
	if audio == "" {
		return ""
	}
	engine := botSTTEngine()
	if text := s.botTranscribeOnce(ctx, restaurantID, msg, audio, engine); text != "" {
		return text
	}
	if engine != "whisper" {
		botCheckpoint("wa_bot_stt_bark_v1", "restaurant_id=%d sender=%s fallback=whisper", restaurantID, msg.Sender)
		return s.botTranscribeOnce(ctx, restaurantID, msg, audio, "whisper")
	}
	return ""
}

// botTranscribeOnce performs ONE transcription call to the sidecar.
func (s *Server) botTranscribeOnce(ctx context.Context, restaurantID int, msg botWebhookMessage, audio, engine string) string {
	payload := map[string]any{"audio_b64": audio, "restaurant_id": restaurantID, "engine": engine}
	if lang := strings.TrimSpace(os.Getenv("BOT_STT_LANGUAGE")); lang != "" {
		payload["language"] = lang
	}
	body, _ := json.Marshal(payload)
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
		botCheckpoint("wa_bot_audio_transcription_v1", "restaurant_id=%d sender=%s engine=%s error=%v", restaurantID, msg.Sender, engine, err)
		return ""
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Text     string `json:"text"`
		Language string `json:"language"`
		Engine   string `json:"engine"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &out) != nil {
		botCheckpoint("wa_bot_audio_transcription_v1", "restaurant_id=%d sender=%s engine=%s status=%d", restaurantID, msg.Sender, engine, resp.StatusCode)
		return ""
	}
	text := strings.TrimSpace(out.Text)
	botCheckpoint("wa_bot_audio_transcription_v1", "restaurant_id=%d sender=%s engine=%s lang=%s chars=%d ms=%d",
		restaurantID, msg.Sender, out.Engine, out.Language, len(text), time.Since(started).Milliseconds())
	return text
}

// botFetchMediaBase64 downloads any media payload from Evolution when the
// webhook did not inline it (instances registered with webhookBase64=false).
// Used by the voice-note path and by the image reading path
// (wa_bot_attachment_read_v1).
func (s *Server) botFetchMediaBase64(ctx context.Context, restaurantID int, messageID string) string {
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
		botCheckpoint("wa_bot_audio_transcription_v1", "restaurant_id=%d fetch_failed code=%d err=%v", restaurantID, code, err)
		return ""
	}
	b64, _ := resp["base64"].(string)
	return strings.TrimSpace(b64)
}
