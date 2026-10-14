package api

// Tests for the Forky chat attachment pipeline (forky_attachments_v1):
// frame validation, block building from bytes, and the user-block merge.
// The end-to-end WS path is covered by the _DB tests below the pure ones.

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateAssistantAttachmentRefs(t *testing.T) {
	ok := assistantAttachmentRef{
		Name: "menu.pdf", MediaType: "application/pdf", Size: 1024,
		Path: "forky/7/uuid.pdf", URL: "https://z.b-cdn.net/forky/7/uuid.pdf",
	}
	tests := []struct {
		name    string
		refs    func() []assistantAttachmentRef
		wantErr bool
	}{
		{"empty is fine", func() []assistantAttachmentRef { return nil }, false},
		{"valid image", func() []assistantAttachmentRef {
			r := ok
			r.Name, r.MediaType, r.Path = "foto.png", "image/png", "forky/7/uuid.png"
			return []assistantAttachmentRef{r}
		}, false},
		{"too many", func() []assistantAttachmentRef {
			refs := make([]assistantAttachmentRef, assistantAttachmentMaxCount+1)
			for i := range refs {
				refs[i] = ok
			}
			return refs
		}, true},
		{"oversize", func() []assistantAttachmentRef {
			r := ok
			r.Size = assistantAttachmentMaxBytes + 1
			return []assistantAttachmentRef{r}
		}, true},
		{"zero size", func() []assistantAttachmentRef {
			r := ok
			r.Size = 0
			return []assistantAttachmentRef{r}
		}, true},
		{"unsupported media", func() []assistantAttachmentRef {
			r := ok
			r.MediaType = "application/zip"
			return []assistantAttachmentRef{r}
		}, true},
		{"other tenant path", func() []assistantAttachmentRef {
			r := ok
			r.Path = "forky/999/uuid.pdf"
			return []assistantAttachmentRef{r}
		}, true},
		{"path escape", func() []assistantAttachmentRef {
			r := ok
			r.Path = "forky/7/../../etc/passwd"
			return []assistantAttachmentRef{r}
		}, true},
		{"empty name", func() []assistantAttachmentRef {
			r := ok
			r.Name = "  "
			return []assistantAttachmentRef{r}
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAssistantAttachmentRefs(7, tt.refs())
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestBuildAssistantAttachmentBlockFromBytes_Image(t *testing.T) {
	// 1x1 red PNG.
	raw, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatal(err)
	}
	block, err := buildAssistantAttachmentBlockFromBytes("foto.png", "image/png", raw)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if block.block["type"] != "image" {
		t.Fatalf("block type = %v", block.block["type"])
	}
	src, ok := block.block["source"].(map[string]any)
	if !ok || src["media_type"] != "image/png" || src["data"] == "" {
		t.Fatalf("source = %v", block.block["source"])
	}
	if !strings.Contains(block.manifest, "foto.png") {
		t.Fatalf("manifest = %q", block.manifest)
	}
}

func TestBuildAssistantAttachmentBlockFromBytes_RejectsMisdeclaredImage(t *testing.T) {
	// Not an image at all — must be refused, never fed to the model.
	block, err := buildAssistantAttachmentBlockFromBytes("fake.png", "image/png", []byte("hello there, definitely not a PNG"))
	if err == nil {
		t.Fatalf("expected error, got block %v", block)
	}
}

func TestBuildAssistantAttachmentBlockFromBytes_PDF(t *testing.T) {
	pdf := "%PDF-1.4 fake body for sniffing"
	block, err := buildAssistantAttachmentBlockFromBytes("carta.pdf", "application/pdf", []byte(pdf))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if block.block["type"] != "document" {
		t.Fatalf("block type = %v", block.block["type"])
	}
	src := block.block["source"].(map[string]any)
	if src["media_type"] != "application/pdf" {
		t.Fatalf("media_type = %v", src["media_type"])
	}
	if _, err := base64.StdEncoding.DecodeString(src["data"].(string)); err != nil {
		t.Fatalf("data is not base64: %v", err)
	}
}

func TestBuildAssistantAttachmentBlockFromBytes_RejectsMisdeclaredPDF(t *testing.T) {
	if _, err := buildAssistantAttachmentBlockFromBytes("falso.pdf", "application/pdf", []byte("not a pdf")); err == nil {
		t.Fatal("expected error for non-PDF payload")
	}
}

func TestBuildAssistantAttachmentBlockFromBytes_TextInlineCapped(t *testing.T) {
	big := strings.Repeat("áéíóú", assistantTextAttachmentMaxChars) // multi-byte, way over cap
	block, err := buildAssistantAttachmentBlockFromBytes("notas.txt", "text/plain", []byte(big))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if block.block["type"] != "text" {
		t.Fatalf("block type = %v", block.block["type"])
	}
	text := block.block["text"].(string)
	if !strings.Contains(text, "texto truncado") {
		t.Fatalf("expected truncation note, got %d chars head %q", len(text), text[:60])
	}
	if !utf8ValidString(text) {
		t.Fatal("truncation broke a UTF-8 sequence")
	}
}

func TestBuildAssistantAttachmentBlockFromBytes_RejectsBinaryText(t *testing.T) {
	if _, err := buildAssistantAttachmentBlockFromBytes("bin.txt", "text/plain", []byte{0xff, 0xfe, 0x00, 0x01}); err == nil {
		t.Fatal("expected error for non-UTF8 text attachment")
	}
}

func TestAssistantAttachmentsToUserBlocks(t *testing.T) {
	img := assistantAttachmentBlock{block: map[string]any{"type": "image"}, manifest: "[Adjunto: foto.png (image/png, 1 KB)]"}
	txt := assistantAttachmentBlock{block: map[string]any{"type": "text", "text": "csv..."}, manifest: "[Adjunto: datos.csv (text/csv, 2 KB)]"}

	blocks, manifest := assistantAttachmentsToUserBlocks("¿Qué ves?", []assistantAttachmentBlock{img, txt})
	if len(blocks) != 3 {
		t.Fatalf("blocks = %d, want 3", len(blocks))
	}
	if blocks[0]["type"] != "text" || blocks[0]["text"] != "¿Qué ves?" {
		t.Fatalf("first block = %v (question must lead)", blocks[0])
	}
	if !strings.HasPrefix(manifest, "¿Qué ves?\n") || !strings.Contains(manifest, "foto.png") || !strings.Contains(manifest, "datos.csv") {
		t.Fatalf("manifest = %q", manifest)
	}

	// Attachment-only message: no empty text block (MiniMax rejects it).
	blocks2, _ := assistantAttachmentsToUserBlocks("   ", []assistantAttachmentBlock{img})
	if len(blocks2) != 1 || blocks2[0]["type"] != "image" {
		t.Fatalf("attachment-only blocks = %v", blocks2)
	}
}

// utf8ValidString is a tiny indirection so the test does not import
// unicode/utf8 for one call.
func utf8ValidString(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}

// TestAssistantAttachmentRefJSON keeps the wire shape locked: the frontend
// sends exactly these fields in the message frame.
func TestAssistantAttachmentRefJSON(t *testing.T) {
	var ref assistantAttachmentRef
	if err := json.Unmarshal([]byte(`{"name":"a.png","media_type":"image/png","size":5,"url":"https://x/y.png","path":"forky/1/y.png"}`), &ref); err != nil {
		t.Fatal(err)
	}
	if ref.Name != "a.png" || ref.MediaType != "image/png" || ref.Size != 5 || ref.Path != "forky/1/y.png" {
		t.Fatalf("ref = %+v", ref)
	}
}
