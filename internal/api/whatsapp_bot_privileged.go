package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	"github.com/xuri/excelize/v2"
)

// Coordination id: wa_bot_privileged_group_v1
//
// Inside the configured privileged WhatsApp group(s) the bot runs its turn
// with the FULL forky assistant tool registry (no preventive restrictions):
// the tools listed in assistantToolRegistry are offered to the model and
// executed through s.assistantExecuteTool with a synthetic root backoffice
// auth, so authorization AND the audit row stay on the real production path
// (the Unsafe executor is never called directly).
//
// Everywhere else the bot behavior is byte-for-byte unchanged: every branch
// below is gated on msg.PrivilegedGroup, which is only set for groups listed
// in config.BotPrivilegedGroupJIDs, and an empty config disables the feature
// completely.

// botDocumentMaxBytes bounds the inline document payload accepted for
// extraction (matches the audio bound: webhookBase64 inline media).
const botDocumentMaxBytes = 11 << 20

// botDocumentMaxRunes caps the extracted text handed to the model.
const botDocumentMaxRunes = 12000

// botDocumentTruncationMarker is appended when the extracted text is capped.
const botDocumentTruncationMarker = "…[truncado]"

// botPrivilegedGroupSet normalizes the configured JIDs into a lookup set.
// Values are trimmed; anything that is not a @g.us id is dropped so a stray
// phone number in the env var can never enable the feature.
func botPrivilegedGroupSet(jids []string) map[string]struct{} {
	out := make(map[string]struct{}, len(jids))
	for _, j := range jids {
		j = strings.TrimSpace(j)
		if j == "" || !strings.HasSuffix(j, "@g.us") {
			continue
		}
		out[j] = struct{}{}
	}
	return out
}

// botGroupIsPrivileged reports whether the chat JID belongs to a configured
// privileged conversation. An empty config is always false: the feature is
// off.
//
// There are two ways to be privileged:
//
//   - the conversation was SELECTED in the backoffice
//     (tenant.PrivilegedContacts): a group or a one-to-one contact;
//   - the group is listed in BOT_PRIVILEGED_GROUP_JIDS (the global env) or
//     in tenant.PrivilegedGroupJIDs (the per-restaurant list).
//
// The env list still applies, so an installation that has not been migrated
// keeps working; the tenant lists only ever ADD conversations.
func (s *Server) botGroupIsPrivileged(jid string, tenant botTenantConfig) bool {
	jid = strings.TrimSpace(jid)
	if jid == "" {
		return false
	}
	if _, ok := botPrivilegedContactJIDs(tenant)[jid]; ok {
		return true
	}
	if len(s.cfg.BotPrivilegedGroupJIDs) > 0 {
		if _, ok := botPrivilegedGroupSet(s.cfg.BotPrivilegedGroupJIDs)[jid]; ok {
			return true
		}
	}
	if len(tenant.PrivilegedGroupJIDs) > 0 {
		if _, ok := botPrivilegedGroupSet(tenant.PrivilegedGroupJIDs)[jid]; ok {
			return true
		}
	}
	return false
}

// botForkyExcludedFromWA lists forky tools that must NOT reach the WhatsApp
// bot, neither in the catalog (botForkyToolDefs) nor via the execution router
// (wa_bot_forky_excluded_v1). They exist only for authenticated panel/MCP
// sessions: their data goes through dedicated WA staff tools with the fresh
// member gate (botSpecialAdminAuthorize), and the synthetic root context of
// the privileged path must not become a bypass for that gate.
var botForkyExcludedFromWA = map[string]struct{}{
	"cancelled_bookings_list": {},
	"special_dates_list":      {},
	"tables_list":             {},
}

// botForkyToolDefs converts the full assistant (forky) tool registry into the
// bot's tool-list shape. Original names are kept: the privileged executor
// router distinguishes forky tools by assistantToolLookup(name), which is
// unambiguous because no bot tool shares a registry name.
func botForkyToolDefs() []botToolDef {
	defs := assistantToolDefs()
	out := make([]botToolDef, 0, len(defs))
	for _, d := range defs {
		if _, excluded := botForkyExcludedFromWA[d.Name]; excluded {
			continue
		}
		out = append(out, botToolDef{Name: d.Name, Description: d.Description, InputSchema: d.InputSchema})
	}
	return out
}

// botPrivilegedForkyContext wraps ctx with the minimal privileged backoffice
// auth assistantToolAllowed actually reads (role root ⇒ every section allowed,
// even with an empty SectionAccess list). The auth is synthetic but the
// execution path is the real one: assistantExecuteTool authorizes and writes
// the audit row exactly as it does for backoffice sessions.
//
// The context ALSO carries the panel service session, so admin_call can
// replay a panel route through the real router: those routes authenticate
// on a session cookie, and without a session the whole admin catalogue was
// unreachable from the bot ("sesion del panel no disponible"). The session
// is a real bo_sessions row owned by the deployment's superadmin, bound to
// the restaurant, so the replay is authorized by the same code that
// authorizes a human. See whatsapp_bot_service_session.go.
// botPrivilegedTurnKey marks a context that belongs to a WhatsApp bot turn (a
// model acting on a chat message, not a human in the panel).
type botPrivilegedTurnKey struct{}

func isBotPrivilegedTurn(ctx context.Context) bool {
	v, _ := ctx.Value(botPrivilegedTurnKey{}).(bool)
	return v
}

func (s *Server) botPrivilegedForkyContext(ctx context.Context, restaurantID int) context.Context {
	ctx = withBOAuth(ctx, boAuth{Role: "root", User: boUser{Role: "root"}})
	ctx = context.WithValue(ctx, botPrivilegedTurnKey{}, true)
	if token, err := s.botServiceSession(ctx, restaurantID); err == nil && token != "" {
		ctx = botServiceSessionTokenForContext(ctx, token)
	}
	return ctx
}

// botDocumentTurnNote extracts the inline document of a privileged turn and
// returns the note appended to the last user message plus whether extraction
// succeeded. Failures come back as an explicit note (never empty): the model
// must be able to tell the group the file could not be read.
func botDocumentTurnNote(documentB64, documentName string) (string, bool) {
	name := strings.TrimSpace(documentName)
	if name == "" {
		name = "documento"
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(documentB64))
	if err != nil {
		return "\n\n[Documento adjunto: " + name + "; no se pudo decodificar: " + err.Error() + "]", false
	}
	extract, err := botExtractDocumentText(name, "", data)
	if err != nil {
		return "\n\n[Documento adjunto: " + name + "; no se pudo extraer: " + err.Error() + "]", false
	}
	return "\n\n[Documento adjunto: " + name + "]\n" + extract, true
}

// botAppendTextToLastUserMessage appends text to the last user message of the
// loaded history so document/OCR content travels with the current request.
func botAppendTextToLastUserMessage(messages []botMessage, text string) []botMessage {
	if text == "" {
		return messages
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" {
			continue
		}
		messages[i].Content = append(messages[i].Content, botBlock{Type: "text", Text: text})
		return messages
	}
	return messages
}

// botCapDocumentText trims and caps extracted document text so a huge file can
// never blow up the prompt. Content is never fabricated: only truncation.
func botCapDocumentText(text string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= botDocumentMaxRunes {
		return text
	}
	return string(runes[:botDocumentMaxRunes]) + botDocumentTruncationMarker
}

// botExtractDocumentText extracts plain text from an inline WhatsApp document
// (base64-decoded payload) so the model can read it. Supported: PDF, xlsx,
// docx, plain-text family (txt/csv/md/json/xml/log) and images via tesseract
// when the binary is present. On any extraction failure it returns an error —
// the caller surfaces it verbatim; it never invents content.
func botExtractDocumentText(filename, mediaType string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("documento vacío")
	}
	if len(data) > botDocumentMaxBytes {
		return "", fmt.Errorf("documento demasiado grande (%d bytes)", len(data))
	}
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(filename)))
	mt := strings.ToLower(mediaType)
	switch {
	case ext == ".pdf" || strings.Contains(mt, "pdf"):
		text, err := botExtractPDFText(data)
		if err != nil {
			return "", err
		}
		return botCapDocumentText(text), nil
	case ext == ".xlsx" || ext == ".xlsm" || strings.Contains(mt, "spreadsheetml"):
		text, err := botExtractXLSXText(data)
		if err != nil {
			return "", err
		}
		return botCapDocumentText(text), nil
	case ext == ".docx" || strings.Contains(mt, "wordprocessingml"):
		text, err := botExtractDOCXText(data)
		if err != nil {
			return "", err
		}
		return botCapDocumentText(text), nil
	case ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" || strings.Contains(mt, "image"):
		text, err := botExtractImageText(data)
		if err != nil {
			return "", err
		}
		return botCapDocumentText(text), nil
	case ext == ".txt" || ext == ".csv" || ext == ".md" || ext == ".json" || ext == ".xml" || ext == ".log":
		return botCapDocumentText(botDecodeTextBytes(data)), nil
	default:
		return "", fmt.Errorf("tipo de documento no soportado: %s", ext)
	}
}

// botExtractPDFText pulls the embedded text layer with the pure-Go
// ledongthuc/pdf reader (no external binary).
func botExtractPDFText(data []byte) (string, error) {
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("pdf ilegible: %w", err)
	}
	plain, err := reader.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("pdf sin texto extraíble: %w", err)
	}
	raw, err := io.ReadAll(io.LimitReader(plain, botDocumentMaxBytes))
	if err != nil {
		return "", fmt.Errorf("pdf sin texto extraíble: %w", err)
	}
	text := strings.TrimSpace(botDecodeTextBytes(raw))
	if text == "" {
		return "", fmt.Errorf("el pdf no contiene capa de texto (probablemente es un escaneo)")
	}
	return text, nil
}

// botExtractXLSXText dumps the first rows of every sheet as tab-separated
// lines via excelize (correct shared-strings handling).
func botExtractXLSXText(data []byte) (string, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("xlsx ilegible: %w", err)
	}
	defer f.Close()
	var b strings.Builder
	const maxRows = 500
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "## Hoja: %s\n", sheet)
		for i, row := range rows {
			if i >= maxRows {
				b.WriteString("…[filas truncadas]\n")
				break
			}
			b.WriteString(strings.TrimRight(strings.Join(row, "\t"), "\t"))
			b.WriteString("\n")
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "", fmt.Errorf("el xlsx no contiene celdas legibles")
	}
	return out, nil
}

// botDocxXMLTagRe strips XML tags after paragraph markers were normalized.
var botDocxXMLTagRe = regexp.MustCompile(`<[^>]+>`)

// botExtractDOCXText reads word/document.xml from the docx zip and strips the
// XML markup (stdlib only).
func botExtractDOCXText(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("docx ilegible: %w", err)
	}
	var raw []byte
	for _, zf := range zr.File {
		if zf.Name != "word/document.xml" {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return "", fmt.Errorf("docx ilegible: %w", err)
		}
		raw, err = io.ReadAll(io.LimitReader(rc, botDocumentMaxBytes))
		rc.Close()
		if err != nil {
			return "", fmt.Errorf("docx ilegible: %w", err)
		}
		break
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("docx sin word/document.xml")
	}
	// One line per paragraph; then strip the remaining XML markup.
	paragraphs := strings.Split(strings.ReplaceAll(string(raw), "</w:p>", "\n"), "\n")
	cleaned := make([]string, 0, len(paragraphs))
	for _, p := range paragraphs {
		p = strings.Join(strings.Fields(botDocxXMLTagRe.ReplaceAllString(html.UnescapeString(p), " ")), " ")
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	out := strings.Join(cleaned, "\n")
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("el docx no contiene texto")
	}
	return out, nil
}

// botExtractImageText OCRs an image with tesseract when the binary is present
// (the production image ships tesseract-ocr + spa data). Without it the note
// is a normal (non-error) result so the model can tell the group OCR is not
// available on this deployment.
func botExtractImageText(data []byte) (string, error) {
	if _, err := exec.LookPath("tesseract"); err != nil {
		return "No se ha podido extraer texto de la imagen: el OCR (tesseract) no está disponible en este servidor.", nil
	}
	for _, lang := range []string{"spa+eng", "eng"} {
		var out, errOut bytes.Buffer
		cmd := exec.Command("tesseract", "stdin", "stdout", "-l", lang)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(data), &out, &errOut
		if err := cmd.Run(); err != nil {
			continue // try the next language pack
		}
		text := strings.TrimSpace(out.String())
		if text == "" {
			return "", fmt.Errorf("la imagen no contiene texto reconocible")
		}
		return text, nil
	}
	return "", fmt.Errorf("tesseract no pudo procesar la imagen")
}

// botDecodeTextBytes decodes UTF-8 best effort, falling back to latin-1
// (every byte maps to the same rune) which covers legacy Spanish exports.
func botDecodeTextBytes(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	runes := make([]rune, len(data))
	for i, b := range data {
		runes[i] = rune(b)
	}
	return string(runes)
}
