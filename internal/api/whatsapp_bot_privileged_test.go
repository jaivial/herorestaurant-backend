package api

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"preactvillacarmen/internal/config"
)

// --- gate ----------------------------------------------------------------

// botExtractDocumentText gating (wa_bot_privileged_group_v1): empty config is
// always off; only listed @g.us JIDs match; non-@g.us values are ignored.
func TestBotGroupIsPrivileged(t *testing.T) {
	tests := []struct {
		name string
		jids []string
		in   string
		want bool
	}{
		{"empty config always false", nil, "120363@g.us", false},
		{"empty config empty jid", nil, "", false},
		{"match", []string{"120363040916666666@g.us"}, "120363040916666666@g.us", true},
		{"match trims input", []string{"120363040916666666@g.us"}, " 120363040916666666@g.us ", true},
		{"other group no match", []string{"120363040916666666@g.us"}, "999@g.us", false},
		{"non g.us values ignored", []string{"34600111222@s.whatsapp.net", "notagroup"}, "34600111222@s.whatsapp.net", false},
		{"non g.us set never matches", []string{"34600111222@s.whatsapp.net"}, "34600111222@s.whatsapp.net", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{cfg: config.Config{BotPrivilegedGroupJIDs: tt.jids}}
			if got := s.botGroupIsPrivileged(tt.in, botTenantConfig{}); got != tt.want {
				t.Errorf("botGroupIsPrivileged(%q) with %v = %v, want %v", tt.in, tt.jids, got, tt.want)
			}
			// The set builder must agree with the server lookup.
			set := botPrivilegedGroupSet(tt.jids)
			_, inSet := set[strings.TrimSpace(tt.in)]
			if inSet != tt.want {
				t.Errorf("set lookup %v disagrees with server lookup %v", inSet, tt.want)
			}
		})
	}
}

// --- forky defs conversion ------------------------------------------------

func TestBotForkyToolDefs(t *testing.T) {
	defs := botForkyToolDefs()
	if len(defs) == 0 {
		t.Fatal("botForkyToolDefs() returned an empty list")
	}
	registry := assistantToolDefs()
	// wa_bot_catalog_diet_v1: the WA staff catalog no longer carries the raw
	// forky defs (~85 tools, ~18k tokens per call). It advertises ONLY the
	// meta-catalogue trio; every other panel operation is reached through
	// admin_call, and the gated reads through staff_domain_read. Excluded
	// tools must never leak either way.
	if len(defs) != len(botForkyMetaTools) {
		t.Errorf("converted %d defs, want exactly the %d meta tools", len(defs), len(botForkyMetaTools))
	}
	seen := map[string]bool{}
	for _, d := range defs {
		if d.Name == "" {
			t.Error("converted def with empty name")
		}
		if !botForkyMetaTools[d.Name] {
			t.Errorf("non-meta tool %q advertised in the WA staff catalog", d.Name)
		}
		if _, excluded := botForkyExcludedFromWA[d.Name]; excluded {
			t.Errorf("excluded tool %q reached the bot defs", d.Name)
		}
		if seen[d.Name] {
			t.Errorf("duplicate tool name %q", d.Name)
		}
		seen[d.Name] = true
		if len(d.InputSchema) > 0 {
			var v any
			if err := json.Unmarshal(d.InputSchema, &v); err != nil {
				t.Errorf("tool %q: schema is not valid JSON: %v", d.Name, err)
			}
		}
		if d.Description == "" {
			t.Errorf("tool %q: empty description", d.Name)
		}
	}
	// The meta trio must all be present; a raw data tool must NOT.
	for _, m := range []string{"admin_catalog", "admin_describe", "admin_call"} {
		if !seen[m] {
			t.Errorf("meta tool %q missing from the WA staff catalog", m)
		}
	}
	if seen["booking_limits_get"] {
		t.Errorf("raw forky data tool booking_limits_get must not be advertised (reached via admin_call)")
	}
	// The registry still holds the full set: capability is unchanged, only the
	// advertised catalog shrank.
	if len(registry) <= len(botForkyMetaTools) {
		t.Errorf("registry shrank unexpectedly: %d tools", len(registry))
	}
}

// --- extraction helpers ----------------------------------------------------

func TestBotCapDocumentText(t *testing.T) {
	long := strings.Repeat("á", botDocumentMaxRunes+500)
	got := botCapDocumentText(long)
	runes := []rune(got)
	wantLen := botDocumentMaxRunes + len([]rune(botDocumentTruncationMarker))
	if len(runes) != wantLen || string(runes[:botDocumentMaxRunes]) != strings.Repeat("á", botDocumentMaxRunes) || !strings.HasSuffix(got, botDocumentTruncationMarker) {
		t.Errorf("cap did not truncate at %d runes with the truncation marker (len runes=%d, want %d)", botDocumentMaxRunes, len(runes), wantLen)
	}
	if got := botCapDocumentText("  hola  "); got != "hola" {
		t.Errorf("short text changed: %q", got)
	}
}

func TestBotAppendTextToLastUserMessage(t *testing.T) {
	msgs := []botMessage{
		{Role: "user", Content: []botBlock{{Type: "text", Text: "primero"}}},
		{Role: "assistant", Content: []botBlock{{Type: "text", Text: "respuesta"}}},
		{Role: "user", Content: []botBlock{{Type: "text", Text: "lee esto"}}},
	}
	out := botAppendTextToLastUserMessage(msgs, "\n\n[Documento adjunto: a.txt]\ncontenido")
	last := out[2].Content[len(out[2].Content)-1]
	if last.Text != "\n\n[Documento adjunto: a.txt]\ncontenido" {
		t.Errorf("note appended in the wrong place: %+v", out[2])
	}
	if out[0].Content[0].Text != "primero" || len(out[1].Content) != 1 {
		t.Error("earlier messages were modified")
	}
	// No user message: unchanged, no panic.
	empty := botAppendTextToLastUserMessage(nil, "x")
	if len(empty) != 0 {
		t.Error("nil history should stay nil")
	}
}

func TestBotDocumentTurnNote_ErrorPaths(t *testing.T) {
	// Invalid base64 → decode failure note.
	note, ok := botDocumentTurnNote("!!!no-base64!!!", "informe.pdf")
	if ok || !strings.Contains(note, "no se pudo decodificar") {
		t.Errorf("invalid base64: ok=%t note=%q", ok, note)
	}
	// Valid base64 but unsupported extension → extraction failure note.
	b64 := base64.StdEncoding.EncodeToString([]byte("algo"))
	note, ok = botDocumentTurnNote(b64, "datos.bin")
	if ok || !strings.Contains(note, "no se pudo extraer") {
		t.Errorf("unsupported type: ok=%t note=%q", ok, note)
	}
	// Empty name falls back to a generic label.
	note, _ = botDocumentTurnNote(b64, "")
	if !strings.Contains(note, "[Documento adjunto: documento") {
		t.Errorf("missing name not defaulted: %q", note)
	}
}

// --- extraction: plain text --------------------------------------------------

func TestBotExtractDocumentText_PlainText(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		data     []byte
		want     string
	}{
		{"txt utf8", "notas.txt", []byte("Reserva para 6 personas"), "Reserva para 6 personas"},
		{"csv", "lista.csv", []byte("nombre,comensales\nJaime,6"), "nombre,comensales\nJaime,6"},
		{"md", "reglas.md", []byte("# Reglas\n\nNo fumar"), "# Reglas\n\nNo fumar"},
		{"latin1 best effort", "legacy.txt", []byte{0x43, 0x61, 0xF1, 0x6F, 0x6E}, "Cañon"},
		{"json", "d.json", []byte(`{"a":1}`), `{"a":1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := botExtractDocumentText(tt.filename, "", tt.data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}

	// Bounds: empty and oversized payloads are errors, never fabricated text.
	if _, err := botExtractDocumentText("a.txt", "", nil); err == nil {
		t.Error("empty payload should error")
	}
	if _, err := botExtractDocumentText("a.txt", "", make([]byte, botDocumentMaxBytes+1)); err == nil {
		t.Error("oversized payload should error")
	}
	// Unsupported extension errors.
	if _, err := botExtractDocumentText("a.exe", "", []byte("x")); err == nil {
		t.Error("unsupported extension should error")
	}
}

// --- extraction: pdf ---------------------------------------------------------

// botBuildTestPDF assembles a minimal one-page PDF with a single text object,
// computing the xref offsets so the ledongthuc/pdf parser accepts it.
func botBuildTestPDF(t *testing.T, text string) []byte {
	t.Helper()
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\nBT /F1 24 Tf 72 720 Td (%s) Tj ET\nendstream", len(text)+28, text),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, obj := range objects {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xrefAt := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(objects)+1)
	buf.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xrefAt)
	return buf.Bytes()
}

func TestBotExtractDocumentText_PDF(t *testing.T) {
	pdfBytes := botBuildTestPDF(t, "Hola reserva de 6")
	got, err := botExtractDocumentText("reserva.pdf", "application/pdf", pdfBytes)
	if err != nil {
		t.Fatalf("pdf extraction failed: %v", err)
	}
	if !strings.Contains(got, "Hola") || !strings.Contains(got, "reserva") {
		t.Errorf("pdf text not extracted: %q", got)
	}
	// A scan-like pdf (no text layer) must error, not fabricate content.
	if _, err := botExtractDocumentText("scan.pdf", "application/pdf", botBuildTestPDF(t, "")); err == nil {
		t.Error("pdf without text should error")
	}
}

// --- extraction: xlsx ---------------------------------------------------------

func TestBotExtractDocumentText_XLSX(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	// Default sheet is "Sheet1".
	if err := f.SetSheetRow("Sheet1", "A1", &[]any{"nombre", "comensales"}); err != nil {
		t.Fatalf("SetSheetRow: %v", err)
	}
	if err := f.SetSheetRow("Sheet1", "A2", &[]any{"Jaime", int64(6)}); err != nil {
		t.Fatalf("SetSheetRow: %v", err)
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := botExtractDocumentText("lista.xlsx", "", buf.Bytes())
	if err != nil {
		t.Fatalf("xlsx extraction failed: %v", err)
	}
	if !strings.Contains(got, "nombre\tcomensales") || !strings.Contains(got, "Jaime\t6") {
		t.Errorf("xlsx rows not extracted: %q", got)
	}
}

// --- extraction: docx ----------------------------------------------------------

func TestBotExtractDocumentText_DOCX(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	doc := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		`<w:p><w:r><w:t>Reserva &amp; menú</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>6 comensales</w:t></w:r></w:p>` +
		`</w:body></w:document>`
	if _, err := w.Write([]byte(doc)); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	got, err := botExtractDocumentText("nota.docx", "", buf.Bytes())
	if err != nil {
		t.Fatalf("docx extraction failed: %v", err)
	}
	if got != "Reserva & menú\n6 comensales" {
		t.Errorf("docx text mismatch: %q", got)
	}
}

// --- extraction: images (OCR) ----------------------------------------------------

func TestBotExtractDocumentText_Image_NoTesseract(t *testing.T) {
	// Force the "tesseract absent" path deterministically, wherever the test
	// runs (the dev host and the CI image both may ship tesseract).
	t.Setenv("PATH", "")
	// PNG magic bytes; without tesseract nothing is executed so the payload
	// content is irrelevant.
	got, err := botExtractDocumentText("foto.png", "image/png", []byte{0x89, 'P', 'N', 'G'})
	if err != nil {
		t.Fatalf("absent tesseract must be a note, not an error: %v", err)
	}
	if !strings.Contains(got, "tesseract") {
		t.Errorf("note should mention tesseract: %q", got)
	}
	if os.Getenv("PATH") != "" {
		t.Error("t.Setenv failed to isolate PATH")
	}
}

// --- gateway passthrough -----------------------------------------------------------

// A group documentMessage with inline base64 must reach waInbound with its
// file name (wa_bot_privileged_group_v1); a 1:1 document also carries the
// fields without changing any other behavior.
func TestEvoParseInboundMessage_DocumentPayload(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("contenido del documento"))
	groupBody := fmt.Sprintf(`{"event":"messages.upsert","instance":"nv_1","data":{
		"key":{"remoteJid":"120363040916666666@g.us","id":"m1","participant":"34600111222@s.whatsapp.net"},
		"pushName":"Jaime","messageType":"documentMessage",
		"message":{"documentMessage":{"fileName":"lista.xlsx","caption":"la lista"},"base64":"%s","mentionText":["346999888777@s.whatsapp.net"]},
		"messageContextInfo":{}}}`, b64)
	// ownJIDs matches the mention so the group gate lets the message through.
	in, ok := (&evolutionGateway{ownJIDs: []string{"346999888777"}}).ParseInboundMessage([]byte(groupBody))
	if !ok {
		t.Fatal("group document message dropped")
	}
	if in.DocumentName != "lista.xlsx" || in.DocumentB64 != b64 {
		t.Errorf("group doc fields not carried: name=%q b64len=%d", in.DocumentName, len(in.DocumentB64))
	}
	if !in.Mentioned {
		t.Error("mentioned group message must carry Mentioned")
	}
	// And without a mention the message is rejected, document or not.
	noMention := fmt.Sprintf(`{"event":"messages.upsert","instance":"nv_1","data":{
		"key":{"remoteJid":"120363040916666666@g.us","id":"m3","participant":"34600111222@s.whatsapp.net"},
		"messageType":"documentMessage",
		"message":{"documentMessage":{"fileName":"lista.xlsx"},"base64":"%s"}}}`, b64)
	if in, ok := (&evolutionGateway{ownJIDs: []string{"346999888777"}}).ParseInboundMessage([]byte(noMention)); ok {
		t.Errorf("unmentioned group document must be dropped, got %+v", in)
	}

	// Oversized document payload is dropped (11 MB bound).
	huge := strings.Repeat("A", (botDocumentMaxBytes/3)*4+100)
	oneToOne := fmt.Sprintf(`{"event":"messages.upsert","instance":"nv_1","data":{
		"key":{"remoteJid":"34600111222@s.whatsapp.net","id":"m2"},
		"messageType":"documentMessage",
		"message":{"documentMessage":{"fileName":"big.pdf"},"base64":"%s"}}}`, huge)
	in1, ok := (&evolutionGateway{}).ParseInboundMessage([]byte(oneToOne))
	if !ok {
		t.Fatal("1:1 document message dropped")
	}
	if in1.DocumentB64 != "" || in1.DocumentName != "" {
		t.Errorf("oversized document must be dropped: name=%q", in1.DocumentName)
	}
}

// --- catalog diet + consolidation -----------------------------------------

func TestBotStaffBookingsOverviewRegistered(t *testing.T) {
	// wa_bot_staff_bookings_overview_v1: the staff anti-N+1 read must be in the
	// admin-reads catalog with a valid schema, gated like the rest.
	var found *botToolDef
	for _, d := range botToolDefsAdminReads() {
		if d.Name == "staff_bookings_overview" {
			cp := d
			found = &cp
		}
	}
	if found == nil {
		t.Fatal("staff_bookings_overview not in botToolDefsAdminReads")
	}
	var schema map[string]any
	if err := json.Unmarshal(found.InputSchema, &schema); err != nil {
		t.Fatalf("schema not valid JSON: %v", err)
	}
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["booking_id"]; !ok {
		t.Error("staff_bookings_overview schema must accept booking_id")
	}
	if _, ok := props["date"]; !ok {
		t.Error("staff_bookings_overview schema must accept date")
	}
}

func TestAdminCallBatchReadOnlyAndCapped(t *testing.T) {
	// wa_bot_admin_call_batch_v1: the schema documents the calls array, and the
	// cap constant bounds it. The write refusal is covered by the single-call
	// path (assistantAdminCallSingle), shared by every batch item.
	var adminCall *botToolDef
	for _, d := range botForkyToolDefs() {
		if d.Name == "admin_call" {
			cp := d
			adminCall = &cp
		}
	}
	if adminCall == nil {
		t.Fatal("admin_call not advertised in the staff meta catalog")
	}
	var schema map[string]any
	if err := json.Unmarshal(adminCall.InputSchema, &schema); err != nil {
		t.Fatalf("admin_call schema not valid JSON: %v", err)
	}
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["calls"]; !ok {
		t.Error("admin_call schema must document the batch 'calls' array")
	}
	if adminCallMaxBatch < 2 || adminCallMaxBatch > 100 {
		t.Errorf("adminCallMaxBatch out of sane range: %d", adminCallMaxBatch)
	}
}
