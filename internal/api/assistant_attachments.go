package api

// Coordination id: forky_attachments_v1 (forky F2 — adjuntos en el PromptBar).
//
// Chat attachments for the Forky prompt bar. The client uploads files through
// the existing REST endpoint POST /api/admin/assistant/media/upload (Bunny
// storage zone, coord forky_media_transcribe_v1) and references them in the
// WebSocket message frame by name + media type + storage path. The server
// re-reads the bytes from Bunny at turn time, so nothing binary ever rides the
// WebSocket, and feeds the model real Anthropic-dialect content blocks:
//
//	images  -> {"type":"image","source":{"type":"base64",...}}
//	PDFs    -> {"type":"document","source":{"type":"base64",...}}
//	text    -> {"type":"text", ...} inline (capped)
//
// The MiniMax Anthropic-compatible endpoint accepts image blocks (already used
// by minimaxVisionOCR) and document blocks (probed live against MiniMax-M3,
// 2026-10-14: 200 OK reading a base64 PDF). A text model never pretends to
// have "seen" a file it did not receive — if the object cannot be read the
// turn fails with an honest error.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

// assistantAttachmentMaxBytes caps each attachment the model is fed (10 MB,
// mirrors the PromptBar cap; uploads to Bunny may be larger for voice notes,
// but chat attachments beyond this are rejected with an honest error).
const assistantAttachmentMaxBytes = 10 << 20

// assistantAttachmentMaxCount caps how many attachments may ride one message.
const assistantAttachmentMaxCount = 4

// assistantTextAttachmentMaxChars caps inline plain-text attachments so a
// dumped CSV cannot blow the context window on its own.
const assistantTextAttachmentMaxChars = 200_000

// assistantAttachmentRef is one client-declared attachment in the message
// frame. Path must point inside the caller's own restaurant folder
// ("forky/<restaurantID>/...") — that is the tenant isolation: a chat can
// only ever reference objects its own restaurant uploaded.
type assistantAttachmentRef struct {
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Size      int    `json:"size"`
	URL       string `json:"url"`
	Path      string `json:"path"`
}

// assistantAttachmentMediaTypes allowlists what the model can be fed. The
// list is intentionally tight: images + PDF travel as base64 blocks, the
// text-likes travel inline. Anything else (zip, docx, xlsx…) is refused with
// an honest message instead of being silently unread by the model.
var assistantAttachmentMediaTypes = map[string]bool{
	"image/png":                 true,
	"image/jpeg":                true,
	"image/webp":                true,
	"image/gif":                 true,
	"application/pdf":           true,
	"text/plain":                true,
	"text/csv":                  true,
	"text/markdown":             true,
	"text/tab-separated-values": true,
	"application/json":          true,
}

// validateAssistantAttachmentRefs enforces the frame-level invariants before
// anything is downloaded: count, declared size, media type, tenant path and a
// sane file name. Returns a user-facing Spanish message on failure.
func validateAssistantAttachmentRefs(restaurantID int, refs []assistantAttachmentRef) error {
	if len(refs) == 0 {
		return nil
	}
	if len(refs) > assistantAttachmentMaxCount {
		return fmt.Errorf("demasiados adjuntos (máximo %d)", assistantAttachmentMaxCount)
	}
	prefix := "forky/" + strconv.Itoa(restaurantID) + "/"
	for _, ref := range refs {
		name := strings.TrimSpace(ref.Name)
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "\x00\n\r") || len(name) > 200 {
			return errors.New("adjunto con nombre no válido")
		}
		if !assistantAttachmentMediaTypes[strings.ToLower(strings.TrimSpace(ref.MediaType))] {
			return fmt.Errorf("tipo de archivo no soportado: %s", ref.MediaType)
		}
		if ref.Size <= 0 || ref.Size > assistantAttachmentMaxBytes {
			return fmt.Errorf("%s supera el límite de 10 MB", name)
		}
		clean := path.Clean("/" + strings.TrimSpace(ref.Path))
		if !strings.HasPrefix(clean, "/"+prefix) || strings.HasSuffix(clean, "/") {
			return fmt.Errorf("%s: origen del adjunto no válido", name)
		}
	}
	return nil
}

// assistantAttachmentBlock is one content block built from an attachment.
type assistantAttachmentBlock struct {
	block    map[string]any
	manifest string // human-readable line for the persisted message
}

// buildAssistantAttachmentBlock downloads one attachment from Bunny and turns
// it into the content block the model consumes.
func (s *Server) buildAssistantAttachmentBlock(ctx context.Context, restaurantID int, ref assistantAttachmentRef) (assistantAttachmentBlock, error) {
	mediaType := strings.ToLower(strings.TrimSpace(ref.MediaType))
	payload, _, err := s.bunnyGet(ctx, restaurantID, strings.TrimSpace(ref.Path))
	if err != nil {
		log.Printf("[forky-attachments] download failed restaurant_id=%d path=%q: %v", restaurantID, ref.Path, err)
		return assistantAttachmentBlock{}, fmt.Errorf("no se pudo leer «%s»: inténtalo de nuevo", ref.Name)
	}
	return buildAssistantAttachmentBlockFromBytes(ref.Name, mediaType, payload)
}

// buildAssistantAttachmentBlockFromBytes turns already-downloaded attachment
// bytes into a model content block. Content sniffing overrides the
// client-declared media type for images/PDF so a mislabeled payload is never
// forced on the model.
func buildAssistantAttachmentBlockFromBytes(name, declaredType string, payload []byte) (assistantAttachmentBlock, error) {
	mediaType := strings.ToLower(strings.TrimSpace(declaredType))
	if len(payload) == 0 {
		return assistantAttachmentBlock{}, fmt.Errorf("«%s» está vacío", name)
	}
	if len(payload) > assistantAttachmentMaxBytes {
		return assistantAttachmentBlock{}, fmt.Errorf("«%s» supera el límite de 10 MB", name)
	}

	manifest := fmt.Sprintf("[Adjunto: %s (%s, %d KB)]", name, mediaType, len(payload)/1024)

	switch {
	case strings.HasPrefix(mediaType, "image/"):
		sniffed := http.DetectContentType(payload)
		if !strings.HasPrefix(sniffed, "image/") {
			return assistantAttachmentBlock{}, fmt.Errorf("«%s» no contiene una imagen válida", name)
		}
		if sniffed != mediaType {
			// DetectContentType reports "jpg" as "image/jpeg" already; keep the
			// sniffed value so the model always sees the true type.
			mediaType = sniffed
		}
		return assistantAttachmentBlock{
			block: map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": mediaType,
					"data":       base64.StdEncoding.EncodeToString(payload),
				},
			},
			manifest: manifest,
		}, nil
	case mediaType == "application/pdf":
		if !strings.HasPrefix(string(payload[:minInt(5, len(payload))]), "%PDF-") {
			return assistantAttachmentBlock{}, fmt.Errorf("«%s» no contiene un PDF válido", name)
		}
		return assistantAttachmentBlock{
			block: map[string]any{
				"type": "document",
				"source": map[string]any{
					"type":       "base64",
					"media_type": "application/pdf",
					"data":       base64.StdEncoding.EncodeToString(payload),
				},
			},
			manifest: manifest,
		}, nil
	default:
		// text/plain, text/csv, text/markdown, application/json,
		// text/tab-separated-values: inline as text, capped.
		if !utf8.Valid(payload) {
			return assistantAttachmentBlock{}, fmt.Errorf("«%s» no es texto legible (UTF-8)", name)
		}
		text := string(payload)
		truncated := false
		if len(text) > assistantTextAttachmentMaxChars {
			// Cut on a rune boundary so the tail is never a broken sequence.
			text = text[:assistantTextAttachmentMaxChars]
			for !utf8.ValidString(text) {
				text = text[:len(text)-1]
			}
			truncated = true
		}
		note := ""
		if truncated {
			note = fmt.Sprintf(" (texto truncado a %d caracteres)", utf8.RuneCountInString(text))
		}
		return assistantAttachmentBlock{
			block: map[string]any{
				"type": "text",
				"text": fmt.Sprintf("Contenido del adjunto «%s» (%s):%s\n%s", name, mediaType, note, text),
			},
			manifest: manifest + note,
		}, nil
	}
}

// assistantAttachmentsToUserBlocks merges the typed text with the attachment
// blocks into the user message the model receives, and returns the manifest
// line persisted instead of the binary payload. The text block leads so the
// question stays first, attachments follow in frame order.
func assistantAttachmentsToUserBlocks(content string, blocks []assistantAttachmentBlock) (blocksOut []map[string]any, manifestText string) {
	manifests := make([]string, 0, len(blocks))
	out := make([]map[string]any, 0, len(blocks)+1)
	if strings.TrimSpace(content) != "" {
		// An attachment-only message must not carry an empty text block: the
		// MiniMax endpoint rejects it.
		out = append(out, map[string]any{"type": "text", "text": content})
	}
	for _, b := range blocks {
		out = append(out, b.block)
		manifests = append(manifests, b.manifest)
	}
	return out, content + "\n" + strings.Join(manifests, "\n")
}

// assistantAttachmentJSON is kept only for tests that want to build frames.
func assistantAttachmentJSON(ref assistantAttachmentRef) string {
	raw, _ := json.Marshal(ref)
	return string(raw)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
