package api

// Coordination id: forky_media_transcribe_v1
//
// Voice notes in the Forky chat (forky F3). The client records the clip, then
// runs BOTH of these in parallel:
//
//	POST /api/admin/assistant/media/upload   -> Bunny object + pull URL
//	POST /api/admin/assistant/transcribe     -> local faster-whisper transcript
//
// and only once both land sends ONE message frame:
//
//	{type:"message", content:<transcript>, audio:{url, duration_ms}}
//
// content is the transcript: it is what the model reads as the user's prompt
// (the text model never "hears" audio) and what the UI shows, muted, under the
// player. audio ties the row to the clip, so a reloaded session replays the
// right object at the right message instead of an in-memory map that dies with
// the tab.

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// assistantVoiceAudio is the clip reference riding a chat message frame.
type assistantVoiceAudio struct {
	// Bunny pull URL of the uploaded clip.
	URL string `json:"url"`
	// Wall-clock duration measured while recording (webm often reports Infinity).
	DurationMS int64 `json:"duration_ms"`
}

// assistantVoiceMaxDurationMS caps what a frame may claim. The composer
// auto-stops at 60 s; 10 minutes is the hard ceiling that keeps a crafted
// frame from poisoning the row (and the player's seek maths) with a bogus
// duration.
const assistantVoiceMaxDurationMS = 10 * 60 * 1000

// assistantVoiceMaxURLLen matches the audio_url column (VARCHAR 512).
const assistantVoiceMaxURLLen = 512

// validateAssistantVoiceAudio resolves the frame's clip reference into the
// values stored on the message row, enforcing the same tenant rule as an
// attachment ref: the URL must live inside the caller's own restaurant folder,
// compared against the SAME function the upload endpoint answers with
// (bunnyPullURL), so a chat can never point its player at another restaurant's
// object. Returns ""/0 for a text turn (audio == nil).
func (s *Server) validateAssistantVoiceAudio(ctx context.Context, restaurantID int, in *assistantVoiceAudio) (string, int64, error) {
	if in == nil {
		return "", 0, nil
	}
	raw := strings.TrimSpace(in.URL)
	if raw == "" {
		return "", 0, errors.New("nota de voz sin audio")
	}
	if len(raw) > assistantVoiceMaxURLLen {
		return "", 0, errors.New("url de la nota de voz no válida")
	}
	prefix := s.bunnyPullURL(ctx, restaurantID, "forky/"+strconv.Itoa(restaurantID)+"/")
	// bunnyPullURL returns a relative path when the zone is unconfigured; the
	// http prefix check turns that misconfiguration into an honest refusal
	// instead of accepting a same-origin path as a CDN object.
	if !strings.HasPrefix(prefix, "http") || !strings.HasPrefix(raw, prefix) {
		return "", 0, errors.New("origen del audio no válido")
	}
	if in.DurationMS < 0 || in.DurationMS > assistantVoiceMaxDurationMS {
		return "", 0, errors.New("duración de la nota de voz no válida")
	}
	return raw, in.DurationMS, nil
}
