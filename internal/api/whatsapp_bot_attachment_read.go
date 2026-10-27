package api

import (
	"context"
	"encoding/base64"
	"strings"
	"time"
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
	note := "\n\n[" + label + "]\n" + botCapDocumentText(text)
	// wa_bot_group_media_context_v1: the photos that came with the request.
	// The mention is on the caption, not on every photo, so the photo that
	// carries the data ("una factura con estos datos") can be a message the
	// mention gate did not accept. The gate registered those photos in the
	// chat registry, and reading the recent ones here gives the model the
	// data the request refers to.
	extra, _ := s.botChatImagesTurnNote(ctx, restaurantID, msg)
	return note + extra, true
}

// botChatImagesNoteMax bounds how many other images of the chat are read
// into one turn: the request travels with its photos, and OCR is not free.
const botChatImagesNoteMax = 2

// botChatImageRequestWindow bounds how old an image of the chat can be and
// still be part of the request being served. The request and its photos
// arrive together, so the photos of the last minutes are the ones it refers
// to; older ones are left alone.
const botChatImageRequestWindow = 5 * time.Minute

// botChatImagesTurnNote reads the other recent images of this chat into the
// turn note, each one labelled with its ref so the tools can use its bytes.
// Coordination id: wa_bot_group_media_context_v1
func (s *Server) botChatImagesTurnNote(ctx context.Context, restaurantID int, msg botWebhookMessage) (string, bool) {
	chatJID := strings.TrimSpace(msg.ChatJID)
	if chatJID == "" {
		return "", false
	}
	imgs := botChatImages.recent(restaurantID, chatJID, time.Now().Add(-botChatImageRequestWindow), botChatImagesNoteMax)
	var note strings.Builder
	read := 0
	for _, img := range imgs {
		if img.MessageID == strings.TrimSpace(msg.MessageID) {
			continue // the image of this message is already in the note
		}
		data := img.Data
		if len(data) == 0 {
			raw := s.botFetchMediaBase64(ctx, restaurantID, img.MessageID)
			if strings.TrimSpace(raw) == "" {
				continue
			}
			decoded, err := base64.StdEncoding.DecodeString(raw)
			if err != nil {
				continue
			}
			data = decoded
		}
		text, err := botExtractImageText(data)
		if err != nil {
			continue
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		label := "Otra imagen de este chat"
		if m := strings.TrimSpace(img.Mime); m != "" {
			label += " (" + m + ")"
		}
		note.WriteString("\n[" + label + " (image_ref=" + img.MessageID + ")]\n")
		note.WriteString(botCapDocumentText(text))
		read++
	}
	botCheckpoint("wa_bot_group_media_context_v1", "restaurant_id=%d chat=%s candidates=%d read=%d", restaurantID, chatJID, len(imgs), read)
	return note.String(), read > 0
}
