package api

import (
	"context"
	"encoding/base64"
	"strings"
)

// Coordination id: wa_bot_attachment_read_v1
//
// Reading WhatsApp attachments as content instead of dropping them on the
// "unsupported media" fallback: documents (PDF/xlsx/docx/text/OCR, the
// existing extraction) and IMAGES (OCR text plus an honest note) become part
// of the turn, so the model reads what the user actually sent.
//
// The note is appended to the last user message of the turn and NEVER invents
// content: a failed extraction comes back as an explicit failure note.
// Audio stays on its own path (transcription, wa_bot_stt_bark_v1).
//
// Scope: allowed (privileged) contacts and groups of this bot version, the
// same gate the document extraction already had.

// botHasReadableAttachment reports whether the turn carries a payload this
// module can read into the prompt (inline, or fetchable by message id).
func botHasReadableAttachment(msg botWebhookMessage) bool {
	if strings.TrimSpace(msg.DocumentB64) != "" || strings.TrimSpace(msg.ImageB64) != "" {
		return true
	}
	// Non-inline image (webhookBase64=false): recoverable from the provider.
	return strings.TrimSpace(msg.MessageID) != "" && strings.Contains(strings.ToLower(msg.MediaKind), "imagen")
}

// botAttachmentTurnNote returns the note to append to the turn plus whether
// real content was extracted.
func (s *Server) botAttachmentTurnNote(ctx context.Context, restaurantID int, msg botWebhookMessage) (string, bool) {
	if strings.TrimSpace(msg.DocumentB64) != "" {
		return botDocumentTurnNote(msg.DocumentB64, msg.DocumentName)
	}
	if strings.TrimSpace(msg.ImageB64) != "" || strings.Contains(strings.ToLower(msg.MediaKind), "imagen") {
		return s.botImageTurnNote(ctx, restaurantID, msg)
	}
	return "", false
}

// botImageTurnNote extracts the readable text of an image (OCR) and returns
// the turn note. The image payload is inline (webhookBase64) or fetched from
// the provider by message id. Coordination id: wa_bot_attachment_read_v1
func (s *Server) botImageTurnNote(ctx context.Context, restaurantID int, msg botWebhookMessage) (string, bool) {
	raw := strings.TrimSpace(msg.ImageB64)
	if raw == "" {
		raw = s.botFetchMediaBase64(ctx, restaurantID, msg.MessageID)
	}
	label := "Imagen adjunta"
	if m := strings.TrimSpace(msg.ImageMime); m != "" {
		label += " (" + m + ")"
	}
	if raw == "" {
		return "\n\n[" + label + ": no se ha podido recuperar la imagen]", false
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return "\n\n[" + label + ": no se ha podido decodificar la imagen: " + err.Error() + "]", false
	}
	text, err := botExtractImageText(data)
	if err != nil {
		return "\n\n[" + label + ": no se ha podido extraer texto de la imagen: " + err.Error() + "]", false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "\n\n[" + label + ": la imagen no contiene texto legible]", false
	}
	return "\n\n[" + label + "]\n" + botCapDocumentText(text), true
}
