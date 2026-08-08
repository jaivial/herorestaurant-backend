package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// assistantMaxFrameRunes caps each delta frame relayed to the WebSocket client
// (chunks are split on rune boundaries so UTF-8 never breaks mid-sequence).
const assistantMaxFrameRunes = 120

// assistantChatMessage is one conversation turn sent to the LLM.
type assistantChatMessage struct {
	Role    string
	Content any
}

// assistantToolDef uses Anthropic-compatible custom tools. Tool execution is
// always server-side and tenant-scoped; model never receives raw DB access.
type assistantToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type assistantToolUse struct {
	ID    string
	Name  string
	Input json.RawMessage
}

type assistantLLMResult struct {
	Text       string
	ToolUses   []assistantToolUse
	StopReason string
}

// assistantStream calls the Messages API through the official Anthropic SDK.
func (s *Server) assistantStream(ctx context.Context, system string, msgs []assistantChatMessage, emit func(chunk string) error) error {
	_, err := s.assistantCall(ctx, system, msgs, nil, emit)
	return err
}

// assistantCall uses the SDK for request construction, authentication,
// streaming, tool blocks, and response decoding. The configured MiniMax
// endpoint is Anthropic-compatible, so it is supplied as the SDK base URL.
func (s *Server) assistantCall(ctx context.Context, system string, msgs []assistantChatMessage, tools []assistantToolDef, emit func(string) error) (result assistantLLMResult, err error) {
	apiKey := strings.TrimSpace(s.cfg.MiniMaxAPIKey)
	if apiKey == "" {
		return result, errors.New("minimax api key not configured")
	}
	model := strings.TrimSpace(s.cfg.AssistantModel)
	if model == "" {
		model = strings.TrimSpace(s.cfg.MiniMaxModel)
	}
	maxTokens := s.cfg.AssistantMaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	timeout := s.cfg.AssistantTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Marshal the existing persisted block representation into SDK params. This
	// preserves text, tool_use and tool_result blocks without lossy conversions.
	messageContent := make([]map[string]any, 0, len(msgs))
	messageParams := make([]anthropic.MessageParam, 0, len(msgs))
	for _, m := range msgs {
		itemContent := m.Content
		if text, ok := itemContent.(string); ok {
			itemContent = []map[string]any{{"type": "text", "text": text}}
		}
		messageContent = append(messageContent, map[string]any{"role": m.Role, "content": itemContent})
		blocks, marshalErr := json.Marshal(map[string]any{"role": m.Role, "content": itemContent})
		if marshalErr != nil {
			return result, marshalErr
		}
		var message anthropic.MessageParam
		if unmarshalErr := json.Unmarshal(blocks, &message); unmarshalErr != nil {
			return result, unmarshalErr
		}
		messageParams = append(messageParams, message)
	}
	body := map[string]any{"model": model, "max_tokens": maxTokens, "system": []map[string]string{{"type": "text", "text": system}}, "messages": messageParams}
	if len(tools) > 0 {
		body["tools"] = tools
		body["tool_choice"] = map[string]any{"type": "auto"}
	}
	raw, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		return result, marshalErr
	}
	var params anthropic.MessageNewParams
	if unmarshalErr := json.Unmarshal(raw, &params); unmarshalErr != nil {
		return result, unmarshalErr
	}
	client := anthropic.NewClient(option.WithAPIKey(apiKey), option.WithBaseURL(strings.TrimRight(s.cfg.MiniMaxBaseURL, "/")+"/"))
	if emit == nil {
		message, callErr := client.Messages.New(ctx, params)
		if callErr != nil {
			return result, callErr
		}
		result.StopReason = string(message.StopReason)
		for _, block := range message.Content {
			if block.Type == "text" {
				result.Text += block.Text
			}
			if block.Type == "tool_use" {
				result.ToolUses = append(result.ToolUses, assistantToolUse{ID: block.ID, Name: block.Name, Input: block.Input})
			}
		}
		return result, nil
	}
	// MiniMax's deployed SSE dialect omits `event:` lines and is not accepted by
	// the SDK stream decoder. Keep the official SDK for non-streaming tool turns,
	// but use the compatible raw SSE transport for streamed UI text.
	streamBody := map[string]any{"model": model, "max_tokens": maxTokens, "system": system, "messages": messageContent, "stream": true}
	if len(tools) > 0 {
		streamBody["tools"] = tools
		streamBody["tool_choice"] = map[string]any{"type": "auto"}
	}
	streamRaw, marshalErr := json.Marshal(streamBody)
	if marshalErr != nil {
		return result, marshalErr
	}
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.MiniMaxBaseURL, "/")+"/v1/messages", bytes.NewReader(streamRaw))
	if reqErr != nil {
		return result, reqErr
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, reqErr := http.DefaultClient.Do(req)
	if reqErr != nil {
		return result, reqErr
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("minimax http %d", resp.StatusCode)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var frame struct {
			Type       string `json:"type"`
			StopReason string `json:"stop_reason"`
			Content    []struct {
				Type, ID, Name, Text string
				Input                json.RawMessage `json:"input"`
			} `json:"content"`
			Delta *struct{ Type, Text, PartialJSON string } `json:"delta"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(data), &frame) != nil {
			continue
		}
		if frame.Type == "error" {
			message := "minimax sse error"
			if frame.Error != nil && frame.Error.Message != "" {
				message += ": " + frame.Error.Message
			}
			return result, errors.New(message)
		}
		if frame.StopReason != "" {
			result.StopReason = frame.StopReason
		}
		for _, block := range frame.Content {
			if block.Type == "tool_use" {
				result.ToolUses = append(result.ToolUses, assistantToolUse{ID: block.ID, Name: block.Name, Input: block.Input})
			}
		}
		if frame.Delta == nil {
			continue
		}
		if frame.Delta.Type == "text_delta" {
			for _, chunk := range splitRunes(frame.Delta.Text, assistantMaxFrameRunes) {
				result.Text += chunk
				if err := emit(chunk); err != nil {
					return result, err
				}
			}
		}
		if frame.Delta.Type == "input_json_delta" && len(result.ToolUses) > 0 {
			i := len(result.ToolUses) - 1
			result.ToolUses[i].Input = append(result.ToolUses[i].Input, []byte(frame.Delta.PartialJSON)...)
		}
	}
	if err := scanner.Err(); err != nil {
		return result, err
	}
	return result, nil
}

// assistantLikelyBase64 reports whether s looks like a base64/base64url payload
// (strong signal: only the base64 alphabet plus optional trailing `=`, no
// spaces). Alignment is NOT required: MiniMax sometimes emits truncated/
// misaligned base64 (length % 4 != 0); decode pads/strips the tail. A normal
// Spanish reply never matches because it always contains spaces/accents/
// markdown/emoji.
func assistantLikelyBase64(s string) bool {
	t := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || r == ' ' {
			return -1
		}
		return r
	}, s)
	t = strings.TrimRight(t, "=")
	if len(t) < 16 {
		return false
	}
	for _, r := range t {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '+' || r == '/' || r == '-':
		default:
			return false
		}
	}
	return true
}

// assistantDecodeBase64 best-effort decodes a base64 or base64url string to
// UTF-8. Returns (decoded, ok). ok is false when the payload is not cleanly
// decodable as UTF-8, so callers keep the original text when the guess fails.
func assistantDecodeBase64(s string) (string, bool) {
	t := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || r == ' ' {
			return -1
		}
		return r
	}, s)
	// Try the exact string first (tolerating a wrong but harmless pad count).
	if out, ok := assistantTryDecodeBase64(t, 0); ok {
		return assistantCleanDecodedText(out), true
	}
	// MiniMax sometimes truncates the base64 mid-stream (e.g. a large reply gets
	// cut at an awkward byte, leaving len%4==1, which Go's decoder rejects with
	// "cannot be 1 more than a multiple of 4"). When the exact decode fails, drop
	// data characters one by one from the END and keep the longest chunk that
	// decodes to valid, mostly-readable UTF-8.
	for drop := 1; drop <= 8; drop++ {
		cut := t[:len(t)-drop]
		if out, ok := assistantTryDecodeBase64(cut, 0); ok {
			return assistantCleanDecodedText(out), true
		}
	}
	// Last resort: decode the longest prefix aligned to 4. This still recovers
	// the readable head of the message even when the tail is garbage.
	if len(t) >= 4 {
		aligned := t[:len(t)-(len(t)%4)]
		if out, ok := assistantTryDecodeBase64(aligned, 2); ok {
			return assistantCleanDecodedText(out), true
		}
	}
	return s, false
}

// assistantTryDecodeBase64 decodes a cleaned base64/base64url payload and reports
// "ok" only when the result is non-empty, valid UTF-8 and mostly printable/readable.
// extraPad forces extra '=' padding for up to extraPad units when useful (used by
// the aligned-prefix recovery after a truncated tail).
func assistantTryDecodeBase64(t string, extraPad int) (string, bool) {
	if len(t) == 0 {
		return "", false
	}
	for alts := 0; alts < 2; alts++ {
		tt := t
		if len(tt)%4 == 1 {
			// A single dangling datum byte is never valid; skip a full 4-unit pad.
			continue
		}
		pad := (4 - len(tt)%4) % 4
		for p := 0; p <= extraPad; p++ {
			cand := tt + strings.Repeat("=", pad+p*4)
			dec, err := base64.StdEncoding.DecodeString(cand)
			if err != nil {
				dec, err = base64.URLEncoding.DecodeString(cand)
			}
			if err != nil {
				continue
			}
			deco := bytes.ToValidUTF8(dec, []byte(""))
			decStr := string(deco)
			if len(decStr) == 0 {
				return "", false
			}
			printable := 0
			for _, r := range decStr {
				// U+FFFD (replacement char a truncated tail leaves behind) counts
				// as NON-printable: a cut-off reply keeps its readable head while
				// binary garbage fails the ratio.
				if r == 0xFFFD {
					continue
				}
				if r >= 0x20 || r == '\n' || r == '\r' || r == '\t' {
					printable++
				}
			}
			if float64(printable)/float64(len(decStr)) < 0.90 {
				if alts == 0 {
					continue
				}
				return "", false
			}
			return decStr, true
		}
	}
	return "", false
}

// assistantCleanDecodedText repairs common artifacts left after base64-decode of
// a model reply: literal "\\n"/"\\t" escape sequences (the model double-escaped
// its markdown) back to real newlines/tabs. It does not rewrite legitimate
// characters; isolated model glitches (©, ⪮...) are left untouched rather than
// risking corrupting real accents.
func assistantCleanDecodedText(s string) string {
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, `\t`, "\t")
	s = strings.ReplaceAll(s, `\r`, "\r")
	return s
}

// assistantRecoverEncodedReply inspects a completed assistant reply; when the
// model wrapped it in base64 (an intermittent MiniMax encoding quirk), decode it
// back to readable text. Used before persisting and before final echo.
func assistantRecoverEncodedReply(text string) string {
	// The model sometimes wraps the base64 payload in markdown code fences or
	// labels ("```wqlJ...```", "<base64>", padding with quotes). Remove that
	// surrounding noise before sniffing, but keep any inner prose as-is.
	candidate := assistantStripBase64Wrapper(text)
	stripped := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, candidate)
	if len(stripped) >= 16 && assistantLikelyBase64(stripped) {
		if dec, ok := assistantDecodeBase64(stripped); ok {
			// The decoded reply can still carry MiniMax CJK/filler glyphs (the
			// model wraps even corrupted text), so cleanse it too.
			return assistantCleanDecodedText(assistantCleanseReply(dec))
		}
	}
	// MiniMax intermittently hallucinates CJK (Chinese) phrases and filler
	// symbols mid-Spanish-reply (e.g. "具体的"), no matter the model. None of
	// these are ever legit Spanish chat, so strip them defensively both before
	// persisting and before streaming to the client.
	return assistantCleanDecodedText(assistantCleanseReply(text))
}

// assistantStripBase64Wrapper removes markdown code-fence backticks, leading
// "data:", quotes and stray punctuation that MiniMax may place around a
// base64-wrapped answer. It never touches the interior payload character set.
func assistantStripBase64Wrapper(s string) string {
	t := strings.TrimSpace(s)
	// Trim a leading fence ("```", "```json" ...) or a known label line/quote.
	for {
		orig := t
		t = strings.TrimPrefix(t, "```")
		t = strings.TrimPrefix(t, "`")
		t = strings.TrimPrefix(t, "\"")
		t = strings.TrimPrefix(t, "'")
		t = strings.TrimPrefix(t, "<base64>")
		t = strings.TrimPrefix(t, "data:")
		// A fence with a language tag ("```json...") — drop the tag and any
		// following whitespace up to the payload.
		for _, tag := range []string{"json", "text", "base64", "plain"} {
			if strings.HasPrefix(t, tag) {
				rest := t[len(tag):]
				i := 0
				for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t' || rest[i] == '\n' || rest[i] == '\r') {
					i++
				}
				t = rest[i:]
				break
			}
		}
		t = strings.TrimRight(t, "```")
		t = strings.TrimRight(t, "`")
		t = strings.TrimRight(t, "\"")
		t = strings.TrimRight(t, "'")
		if t == orig {
			break
		}
	}
	return t
}

// assistantMinimaxGlitch reports whether r is a glyph MiniMax spuriously
// injects into Spanish replies (an encoding/token quirk). CJK ideographs
// (MiniMax sometimes emits real Chinese words like 具体的/人数) and graphic
// "filler" symbols (© ⚳ ⪮ ⊒ ⊓…) never belong in a restaurant chat in Spanish,
// so removing them is safe and cannot corrupt accents or emoji.
func assistantMinimaxGlitch(r rune) bool {
	switch {
	case r >= 0x3400 && r <= 0x4DBF: // CJK Unified Ext A
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
	case r >= 0xF900 && r <= 0xFAFF: // CJK Compatibility Ideographs
	case r >= 0x20000 && r <= 0x2FA1F: // CJK ext B–F & supplement
	case r == 0x3000, r == 0x3001, r == 0x3002, r == 0x300C, r == 0x300D, r == 0xFF01, r == 0xFF0C, r == 0xFF1F: // CJK punctuation / full-width
	case r >= 0x2140 && r <= 0x2AFF: // math ops, ⪮ ⊒ ⊓ ⪻ and friends used as filler
	case r == 0x00A9 || r == 0x26B3: // © · ⚳
	default:
		return false
	}
	return true
}

// assistantCleanseReply removes MiniMax's spurious CJK text and filler symbol
// glyphs, then collapses any double spaces/blank lines those removals leave.
func assistantCleanseReply(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		if assistantMinimaxGlitch(r) {
			return -1
		}
		return r
	}, s)
	if cleaned == s {
		return s
	}
	// Re-flow spaces/nl that bleeding a CJK char left behind ("  hola   ").
	lines := strings.Split(cleaned, "\n")
	for i, ln := range lines {
		// trim leading/trailing spaces on each line, then collapse internal runs.
		ln = strings.Join(strings.Fields(ln), " ")
		lines[i] = ln
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// splitRunes splits s into chunks of at most n runes each (rune-safe).
func splitRunes(s string, n int) []string {
	runes := []rune(s)
	if len(runes) <= n {
		return []string{s}
	}
	out := make([]string, 0, (len(runes)+n-1)/n)
	for i := 0; i < len(runes); i += n {
		end := i + n
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[i:end]))
	}
	return out
}

// buildAssistantSystemPrompt renders the Forky persona prompt. Restaurant
// context (name/phone) is appended when a DB is available; a nil db yields the
// generic prompt.
const assistantOutputContract = "\n\nFORMATO DE RESPUESTA (obligatorio):\n" +
	"1. LISTAS DE RESERVAS U OTROS REGISTROS MULTIFILA: muéstralos SIEMPRE como tabla Markdown (con cabecera \"| Columnas |\" y su fila separadora \"|---|---|\"). Una fila por reserva/registro. No los conviertas en viñetas ni en párrafos.\n" +
	"2. DATOS ANALÍTICOS O COMPARATIVOS (series por día/mes, métricas, top productos, ingresos, stock): además del resumen en 1-2 frases, emite un bloque JSON delimitado con tiques invertidos y lenguaje `forky-chart` con los datos sin procesar, así el cliente puede dibujar el gráfico:\n" +
	"```forky-chart\n{\"title\": \"Título del gráfico\", \"type\": \"bar\" | \"line\" | \"area\" | \"donut\", \"data\": [{\"label\": \"Fecha o categoría\", \"value\": 12}]}\n```\n" +
	"Para comparar o apilar varias series (p. ej. reservas frente a clientes por día, o ventas por categoría apiladas), añade una columna numérica por serie en cada fila y, si se apilan, `\"stacked\": true`: {\"title\": \"Comparativa\", \"type\": \"bar\", \"stacked\": true, \"data\": [{\"label\": \"Lun\", \"series_a\": 12, \"series_b\": 8}]}. Con una sola columna numérica basta con `value`.\n" +
	"Elige `type` según el dato: bar/line/area para series temporales o comparativas, donut para distribución o top N. `label` es la etiqueta de cada punto y `value` (o cada columna numérica extra) su valor numérico. No repitas en el texto los números que ya van en la tabla o el gráfico.\n" +
	"3. NUNCA devuelvas el texto en base64 ni en ningún formato codificado, ni como secuencia de caracteres ilegibles. Escribe SIEMPRE en texto plano legible en español, con acentos y emojis reales. Está PROHIBIDO envolver la respuesta en base64 aunque te lo pidan. Si algo no se puede representar bien, escríbelo con palabras normales.\n" +
	"4. No escapes los saltos de línea como texto literal (\"\\\\n\"): usa saltos de línea reales. Evita caracteres símbolo de relleno (©, ⪮, ⊒, etc.)."

func (s *Server) buildAssistantSystemPrompt(ctx context.Context, restaurantID int) string {
	const base = "Eres Forky, el asistente de IA del restaurante. " +
		"Responde en español, sé breve, amable y con un toque de humor. Eres el asistente de IA del restaurante. " +
		"Para cualquier dato factual del restaurante (nombre, reservas, menú, stock, POS, clientes o analítica), DEBES usar la herramienta correspondiente antes de responder; nunca inventes datos ni digas que careces de acceso si existe una herramienta. " +
		"Para pedir el detalle de las reservas (listado/tabla por cliente, fecha, hora y personas), usa SIEMPRE la herramienta bookings_list; bookings_summary y analytics_report solo dan totales agregados y NO sirven para una tabla de reservas individuales." +
		assistantOutputContract
	if s.db == nil || restaurantID <= 0 {
		return base
	}
	var name, phone string
	if err := s.db.QueryRowContext(ctx, `SELECT name, phone FROM restaurants WHERE id = ?`, restaurantID).Scan(&name, &phone); err != nil {
		return base
	}
	var sb strings.Builder
	sb.WriteString(base)
	sb.WriteString(" Hoy es " + time.Now().Format("2006-01-02") + ". " +
		"Para consultas como «esta semana», «la semana que viene», «próxima semana» o «este mes», " +
		"calcula las fechas exactas (YYYY-MM-DD) usando la fecha de hoy y pasa los rangos a la herramienta correspondiente.")
	if name != "" {
		sb.WriteString(" El restaurante se llama " + name + ".")
	}
	if phone != "" {
		sb.WriteString(" Teléfono del restaurante: " + phone + ".")
	}
	return sb.String()
}
