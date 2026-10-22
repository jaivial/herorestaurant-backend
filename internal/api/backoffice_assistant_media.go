package api

// Coordination id: forky_media_transcribe_v1
//
// REST helpers for the Forky chat prompt bar (session auth, same gate as the
// assistant WebSocket: any logged-in backoffice user):
//
//   POST /api/admin/assistant/media/upload  (multipart "file")
//     Stores the file in the restaurant's existing Bunny storage zone under
//     forky/<restaurantID>/<uuid><ext> and returns its pull URL. Serves both
//     the voice-note clips and the document/image attachments.
//
//   POST /api/admin/assistant/transcribe ({ audio_b64, engine })
//     Forwards the clip to the bot pipeline sidecar's /transcribe
//     (faster-whisper, the same STT path the WhatsApp voice notes use,
//     coord wa_bot_audio_transcription_v1) and returns the transcript.
//     The transcript is the payload: the text model never "hears" audio.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"preactvillacarmen/internal/httpx"
)

// assistantMediaMaxBytes caps uploads (voice notes are small; PDFs and
// sheets are not). 25 MB matches the WhatsApp document limit.
const assistantMediaMaxBytes = 25 << 20

// assistantMediaExtAllowlist keeps the object extension predictable; files
// with other extensions (or none) fall back to a safe binary extension.
var assistantMediaExtAllowlist = map[string]bool{
	".webm": true, ".ogg": true, ".oga": true, ".mp3": true, ".mp4": true, ".m4a": true, ".wav": true, ".aac": true,
	".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true, ".avif": true,
	".pdf": true, ".csv": true, ".txt": true, ".md": true, ".xlsx": true, ".xls": true, ".doc": true, ".docx": true, ".json": true,
}

func assistantMediaSafeExt(name string, contentType string) string {
	ext := strings.ToLower(strings.TrimSpace(path.Ext(name)))
	if !assistantMediaExtAllowlist[ext] {
		switch contentType {
		case "audio/webm":
			ext = ".webm"
		case "audio/ogg":
			ext = ".ogg"
		case "audio/mp4", "audio/m4a":
			ext = ".m4a"
		case "audio/mpeg":
			ext = ".mp3"
		case "audio/wav", "audio/x-wav":
			ext = ".wav"
		case "image/png":
			ext = ".png"
		case "image/jpeg":
			ext = ".jpg"
		case "image/webp":
			ext = ".webp"
		case "application/pdf":
			ext = ".pdf"
		case "text/csv":
			ext = ".csv"
		case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "application/vnd.ms-excel":
			ext = ".xlsx"
		case "application/msword":
			ext = ".doc"
		case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
			ext = ".docx"
		default:
			ext = ".bin"
		}
	}
	return ext
}

// writeMediaError emits the error shape the backoffice frontend expects for
// the assistant media endpoints: {"success":false,"error":msg}.
func writeMediaError(w http.ResponseWriter, status int, msg string) {
	httpx.WriteJSON(w, status, map[string]any{
		"success": false,
		"error":   msg,
	})
}

// handleBOAssistantMediaUpload serves POST /api/admin/assistant/media/upload.
func (s *Server) handleBOAssistantMediaUpload(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		writeMediaError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, assistantMediaMaxBytes+1<<20)
	if err := r.ParseMultipartForm(assistantMediaMaxBytes); err != nil {
		writeMediaError(w, http.StatusBadRequest, "Invalid or oversized upload")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeMediaError(w, http.StatusBadRequest, "No file provided")
		return
	}
	defer file.Close()

	payload, err := io.ReadAll(io.LimitReader(file, assistantMediaMaxBytes+1))
	if err != nil {
		writeMediaError(w, http.StatusBadRequest, "Error reading file")
		return
	}
	if len(payload) == 0 {
		writeMediaError(w, http.StatusBadRequest, "Empty file")
		return
	}
	if len(payload) > assistantMediaMaxBytes {
		writeMediaError(w, http.StatusRequestEntityTooLarge, "File exceeds 25MB")
		return
	}

	contentType := header.Header.Get("Content-Type")
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = http.DetectContentType(payload)
	}
	ext := assistantMediaSafeExt(header.Filename, contentType)
	objectPath := path.Join("forky", strconv.Itoa(a.ActiveRestaurantID), uuid.NewString()+ext)
	if err := s.bunnyPut(r.Context(), a.ActiveRestaurantID, objectPath, payload, contentType); err != nil {
		log.Printf("[forky-media] upload failed restaurant_id=%d %v", a.ActiveRestaurantID, err)
		writeMediaError(w, http.StatusInternalServerError, "Error uploading file")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"url":          s.bunnyPullURL(r.Context(), a.ActiveRestaurantID, objectPath),
		"path":         objectPath,
		"name":         header.Filename,
		"content_type": contentType,
		"size":         len(payload),
	})
}

// handleBOAssistantTranscribe serves POST /api/admin/assistant/transcribe.
func (s *Server) handleBOAssistantTranscribe(w http.ResponseWriter, r *http.Request) {
	if _, ok := boAuthFromContext(r.Context()); !ok {
		writeMediaError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var in struct {
		AudioB64 string `json:"audio_b64"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, assistantMediaMaxBytes+1<<20)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.AudioB64) == "" {
		writeMediaError(w, http.StatusBadRequest, "audio_b64 required")
		return
	}

	// Pin the engine instead of letting the sidecar pick its own default:
	// the local faster-whisper model is warm from sidecar startup, speaks
	// Spanish, and returns in ~4 s; the Bark default is English-only and pays
	// a cold model download on its first call (51 s, lang=en — the timeout the
	// prompt bar used to hit). BOT_STT_ENGINE overrides for ops.
	body, _ := json.Marshal(map[string]any{"audio_b64": in.AudioB64, "engine": botSTTEngine()})
	reqCtx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, s.botPipelineURL()+"/transcribe", bytes.NewReader(body))
	if err != nil {
		writeMediaError(w, http.StatusInternalServerError, "Transcription unavailable")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	started := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("[forky-media] transcribe pipeline error %v", err)
		writeMediaError(w, http.StatusBadGateway, "Transcription unavailable")
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Text     string `json:"text"`
		Language string `json:"language"`
		Engine   string `json:"engine"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &out) != nil {
		log.Printf("[forky-media] transcribe pipeline status=%d", resp.StatusCode)
		writeMediaError(w, http.StatusBadGateway, "Transcription failed")
		return
	}
	log.Printf("[forky-media] transcribe ok engine=%s lang=%s chars=%d ms=%d", out.Engine, out.Language, len(strings.TrimSpace(out.Text)), time.Since(started).Milliseconds())
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"text":     strings.TrimSpace(out.Text),
		"language": out.Language,
	})
}
