package api

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
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
// real content was extracted: the document of the message, and the images of
// the request (wa_bot_image_read_v1).
func (s *Server) botAttachmentTurnNote(ctx context.Context, restaurantID int, msg botWebhookMessage) (string, bool) {
	note, read := "", false
	if strings.TrimSpace(msg.DocumentB64) != "" {
		note, read = botDocumentTurnNote(msg.DocumentB64, msg.DocumentName)
	}
	imgNote, imgRead := s.botImagesTurnNote(ctx, restaurantID, msg)
	return note + imgNote, read || imgRead
}

// botTurnHasRequestImages reports whether a privileged turn has images to
// read: the image of the message itself, or a recent image of the chat.
//
// The second case is what keeps a request that spans messages working: the
// photo of the bill comes first, the client's fiscal data is typed a minute
// later, and that second turn still has to see the bill. The note is not part
// of the stored transcript, so without this the bill would be gone.
func botTurnHasRequestImages(restaurantID int, msg botWebhookMessage) bool {
	return len(botRequestImages(restaurantID, msg)) > 0
}

// botChatImagesNoteMax bounds how many other images of the chat are read
// into one turn: the request travels with its photos, and a read is not free.
const botChatImagesNoteMax = 3

// botChatImageRequestWindow bounds how old an image of the chat can be and
// still be part of the request being served. A request and its photos arrive
// together or a few minutes apart (the fiscal data typed after the photo);
// older ones are left alone.
const botChatImageRequestWindow = 15 * time.Minute

// botRequestImages returns the images the turn has to read: the image of the
// message first, then the other recent images of the chat, newest first.
// Coordination id: wa_bot_group_media_context_v1
func botRequestImages(restaurantID int, msg botWebhookMessage) []botChatImage {
	chatJID := strings.TrimSpace(msg.ChatJID)
	ref := botChatImageRef(msg)
	var out []botChatImage
	if ref != "" {
		img, ok := botChatImages.find(restaurantID, chatJID, ref)
		if !ok {
			img = botChatImage{MessageID: ref, Mime: strings.TrimSpace(msg.ImageMime), At: time.Now()}
		}
		out = append(out, img)
	}
	if chatJID == "" {
		return out
	}
	others := 0
	for _, img := range botChatImages.recent(restaurantID, chatJID, time.Now().Add(-botChatImageRequestWindow), botChatImagesNoteMax+1) {
		if img.MessageID == ref || others >= botChatImagesNoteMax {
			continue
		}
		out = append(out, img)
		others++
	}
	return out
}

// botImagesTurnNote reads the images of the request into the turn note, each
// one labelled with its ref (the tools take it) and with how it was read, so
// the model knows what it can rely on. Coordination id: wa_bot_image_read_v1
func (s *Server) botImagesTurnNote(ctx context.Context, restaurantID int, msg botWebhookMessage) (string, bool) {
	imgs := botRequestImages(restaurantID, msg)
	if len(imgs) == 0 {
		return "", false
	}
	readings := s.botReadRequestImages(ctx, restaurantID, msg, imgs)
	current := botChatImageRef(msg)
	var note strings.Builder
	read, vision, ocr, cached := 0, 0, 0, 0
	for i, img := range imgs {
		rd := readings[i]
		if img.Reading != nil {
			cached++
		}
		label := "Imagen de este mensaje"
		if img.MessageID != current {
			label = "Otra imagen reciente de este chat"
			if ago := time.Since(img.At).Round(time.Minute); ago >= time.Minute {
				label += fmt.Sprintf(" (hace %d min)", int(ago.Minutes()))
			}
		}
		label += " · image_ref=" + img.MessageID
		switch rd.Source {
		case botImageReadVision:
			vision++
			read++
			note.WriteString("\n\n[" + label + " · transcripción]\n" + botCapDocumentText(rd.Text))
		case botImageReadOCR:
			ocr++
			read++
			note.WriteString("\n\n[" + label + " · lectura OCR de BAJA FIABILIDAD: puede tener errores; NO tomes de aquí datos fiscales (nombre, CIF/NIF, dirección, email): pídelos por escrito en el grupo, sin buscarlos en el panel]\n" + botCapDocumentText(rd.Text))
		default:
			note.WriteString("\n\n[" + label + ": no se ha podido leer (puede ser texto manuscrito o una foto poco nítida). Si tenía datos necesarios, pídelos por escrito en el grupo; no los supongas ni los busques en el panel]")
		}
	}
	botCheckpoint("wa_bot_image_read_v1", "restaurant_id=%d chat=%s images=%d read=%d vision=%d ocr=%d cached=%d", restaurantID, msg.ChatJID, len(imgs), read, vision, ocr, cached)
	return note.String(), read > 0
}

// botReadRequestImages reads every image of the request concurrently (one
// vision call each, bounded by botVisionReadTimeout) and keeps each reading in
// the chat registry, so the turns that follow reuse it.
func (s *Server) botReadRequestImages(ctx context.Context, restaurantID int, msg botWebhookMessage, imgs []botChatImage) []botImageReading {
	out := make([]botImageReading, len(imgs))
	var wg sync.WaitGroup
	for i, img := range imgs {
		if img.Reading != nil {
			out[i] = *img.Reading
			continue
		}
		wg.Add(1)
		go func(i int, img botChatImage) {
			defer wg.Done()
			data, mime := s.botRequestImageBytes(ctx, restaurantID, msg, img)
			if len(data) == 0 {
				out[i] = botImageReading{}
				return
			}
			out[i] = s.botReadImage(ctx, restaurantID, mime, data)
			botChatImages.storeReading(restaurantID, msg.ChatJID, img.MessageID, out[i])
		}(i, img)
	}
	wg.Wait()
	return out
}

// botRequestImageBytes returns the bytes of an image of the request: kept in
// the registry, inline on the message, or fetched from the provider.
func (s *Server) botRequestImageBytes(ctx context.Context, restaurantID int, msg botWebhookMessage, img botChatImage) ([]byte, string) {
	mime := strings.TrimSpace(img.Mime)
	if len(img.Data) > 0 {
		return img.Data, mime
	}
	raw := ""
	if img.MessageID == strings.TrimSpace(msg.MessageID) {
		raw = strings.TrimSpace(msg.ImageB64)
		if mime == "" {
			mime = strings.TrimSpace(msg.ImageMime)
		}
	}
	if raw == "" {
		raw = s.botFetchMediaBase64(ctx, restaurantID, img.MessageID)
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, mime
	}
	return data, mime
}

// Coordination id: wa_bot_image_read_v1
//
// An image is read with the vision model first: it reads a printed bill
// exactly (lines, quantities, prices, IVA and total). tesseract is only the
// fallback and is labelled as such: it misreads printed text often and reads
// handwriting not at all, and a bill or a CIF misread into an invoice is
// worse than a question to the group.
const (
	botImageReadVision = "vision"
	botImageReadOCR    = "ocr"
)

// botImageReading is what was read from one image. Source is empty when the
// image could not be read.
type botImageReading struct {
	Text   string
	Source string
}

// botVisionReadTimeout bounds one vision read. The model reasons before it
// answers and how much varies a lot: the same printed bill took 11-35s and
// 1.7k-5.6k output tokens over 8 runs (measured 2026-10-27). An image it
// cannot read (hard handwriting) makes it deliberate until the budget runs
// out, so the read is cut here and the turn goes on. The reads of a turn run
// in parallel and are cached, so a turn pays this once at most.
const botVisionReadTimeout = 60 * time.Second

// botVisionReadMaxTokens is the output budget of one read, reasoning
// included; below ~6k a bill sometimes came back with no text at all.
const botVisionReadMaxTokens = 10000

// botVisionReadPrompt asks for the text and nothing else. Short on purpose:
// asking the model to also judge and flag doubtful words made it deliberate
// past its budget on a plain printed bill (measured 2026-10-27).
const botVisionReadPrompt = "Transcribe el texto de esta imagen, línea a línea y en el mismo orden (la imagen puede estar girada). Responde solo con el texto, sin comentarios."

// botVisionRead is the vision call; a variable so tests run without network.
var botVisionRead = func(s *Server, ctx context.Context, restaurantID int, mime string, data []byte) (string, error) {
	return s.botVisionImageText(ctx, restaurantID, mime, data)
}

// botReadImage reads one image: vision first, tesseract as the labelled
// fallback, nothing (Source "") when neither produced text.
func (s *Server) botReadImage(ctx context.Context, restaurantID int, mime string, data []byte) botImageReading {
	vctx, cancel := context.WithTimeout(ctx, botVisionReadTimeout)
	text, err := botVisionRead(s, vctx, restaurantID, mime, data)
	cancel()
	if text = strings.TrimSpace(text); err == nil && text != "" {
		return botImageReading{Text: text, Source: botImageReadVision}
	}
	botCheckpoint("wa_bot_image_read_v1", "restaurant_id=%d vision_failed err=%v empty=%t", restaurantID, err, err == nil)
	// botExtractImageText answers with a NOTE (not an error) when tesseract is
	// missing; that note is not text read from the image.
	if _, lerr := exec.LookPath("tesseract"); lerr == nil {
		if t, oerr := botExtractImageText(data); oerr == nil && strings.TrimSpace(t) != "" {
			return botImageReading{Text: strings.TrimSpace(t), Source: botImageReadOCR}
		}
	}
	return botImageReading{}
}

// botVisionImageText transcribes one image with the tenant's MiniMax model
// (the bot's own provider and key: the Anthropic-compatible endpoint takes
// image blocks).
func (s *Server) botVisionImageText(ctx context.Context, restaurantID int, mime string, data []byte) (string, error) {
	switch mime {
	case "image/jpeg", "image/png", "image/webp", "image/gif":
	default:
		mime = http.DetectContentType(data)
		if !strings.HasPrefix(mime, "image/") {
			return "", fmt.Errorf("no es una imagen (%s)", mime)
		}
	}
	reply, err := s.minimaxMessagesText(ctx, restaurantID, "", minimaxDocumentContent(mime, data, botVisionReadPrompt), botVisionReadMaxTokens, botVisionReadTimeout)
	if err != nil {
		return "", err
	}
	s.recordAIUsage(ctx, restaurantID, aiFeatureWhatsAppBot, botProviderMiniMax, reply.Model, reply.Usage, false)
	if strings.TrimSpace(reply.Text) == "" {
		return "", fmt.Errorf("sin texto (stop=%s, out=%d)", reply.StopReason, reply.Usage.Output)
	}
	return reply.Text, nil
}
