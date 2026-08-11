package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"preactvillacarmen/internal/config"
)

func newAssistantTestServer(baseURL string) *Server {
	return &Server{
		cfg: config.Config{
			MiniMaxAPIKey:         "test-key",
			MiniMaxBaseURL:        baseURL,
			MiniMaxModel:          "MiniMax-M3",
			AssistantModel:        "MiniMax-M3",
			AssistantTimeout:      5 * time.Second,
			AssistantMaxTokens:    512,
			AssistantHistoryLimit: 20,
		},
	}
}

// sseFlush writes one SSE `data:` frame and flushes it so the client parser can
// consume it incrementally.
func sseWrite(t *testing.T, w http.ResponseWriter, fl http.Flusher, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal sse payload: %v", err)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
		t.Fatalf("write sse: %v", err)
	}
	fl.Flush()
}

func TestAssistantStream_RequestShapeAndDeltas(t *testing.T) {
	var gotReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotReq)

		fl, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer is not a flusher")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		sseWrite(t, w, fl, map[string]any{"type": "message_start"})
		sseWrite(t, w, fl, map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": "Hola"}})
		sseWrite(t, w, fl, map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": " mundo"}})
		sseWrite(t, w, fl, map[string]any{"type": "message_stop"})
	}))
	defer srv.Close()

	s := newAssistantTestServer(srv.URL)
	msgs := []assistantChatMessage{
		{Role: "user", Content: "hola"},
		{Role: "assistant", Content: "hey"},
		{Role: "user", Content: "que tal"},
	}
	var got strings.Builder
	err := s.assistantStream(context.Background(), "SYS", msgs, func(chunk string) error {
		got.WriteString(chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.String() != "Hola mundo" {
		t.Errorf("streamed text = %q", got.String())
	}

	if gotReq["model"] != "MiniMax-M3" {
		t.Errorf("model = %v", gotReq["model"])
	}
	if gotReq["system"] != "SYS" {
		t.Errorf("system = %v", gotReq["system"])
	}
	if gotReq["stream"] != true {
		t.Errorf("stream = %v (want true)", gotReq["stream"])
	}
	if _, ok := gotReq["max_tokens"]; !ok {
		t.Error("max_tokens missing from request")
	}
	rawMsgs, ok := gotReq["messages"].([]any)
	if !ok || len(rawMsgs) != 3 {
		t.Fatalf("messages = %v", gotReq["messages"])
	}
	wantRoles := []string{"user", "assistant", "user"}
	wantText := []string{"hola", "hey", "que tal"}
	for i, m := range rawMsgs {
		mm, _ := m.(map[string]any)
		if mm["role"] != wantRoles[i] {
			t.Errorf("messages[%d].role = %v (want %s)", i, mm["role"], wantRoles[i])
		}
		blocks, ok := mm["content"].([]any)
		if !ok || len(blocks) != 1 {
			t.Fatalf("messages[%d].content = %v", i, mm["content"])
		}
		blk, _ := blocks[0].(map[string]any)
		if blk["type"] != "text" || blk["text"] != wantText[i] {
			t.Errorf("messages[%d] block = %v", i, blk)
		}
	}
}

func TestAssistantStream_ChunksLongDelta(t *testing.T) {
	long := strings.Repeat("á", 300) // 300 runes, 600 bytes
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		sseWrite(t, w, fl, map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": long}})
		sseWrite(t, w, fl, map[string]any{"type": "message_stop"})
	}))
	defer srv.Close()

	s := newAssistantTestServer(srv.URL)
	var chunks []string
	err := s.assistantStream(context.Background(), "SYS", []assistantChatMessage{{Role: "user", Content: "hi"}}, func(chunk string) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected long delta to be split into multiple frames, got %d", len(chunks))
	}
	var joined strings.Builder
	for _, c := range chunks {
		if utf8.RuneCountInString(c) > assistantMaxFrameRunes {
			t.Errorf("chunk exceeds %d runes: %d", assistantMaxFrameRunes, utf8.RuneCountInString(c))
		}
		if !utf8.ValidString(c) {
			t.Errorf("chunk is not valid UTF-8: %q", c)
		}
		joined.WriteString(c)
	}
	if joined.String() != long {
		t.Error("reassembled chunks != original delta text")
	}
}

func TestAssistantStream_NoKey(t *testing.T) {
	s := newAssistantTestServer("http://127.0.0.1:1")
	s.cfg.MiniMaxAPIKey = ""
	err := s.assistantStream(context.Background(), "SYS", []assistantChatMessage{{Role: "user", Content: "hi"}}, func(string) error { return nil })
	if err == nil {
		t.Fatal("expected error when api key is missing")
	}
}

func TestAssistantStream_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	s := newAssistantTestServer(srv.URL)
	err := s.assistantStream(context.Background(), "SYS", []assistantChatMessage{{Role: "user", Content: "hi"}}, func(string) error { return nil })
	if err == nil {
		t.Fatal("expected error on HTTP 500")
	}
}

func TestAssistantStream_SSEError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		sseWrite(t, w, fl, map[string]any{"type": "error", "error": map[string]any{"type": "overloaded_error", "message": "overloaded"}})
	}))
	defer srv.Close()

	s := newAssistantTestServer(srv.URL)
	err := s.assistantStream(context.Background(), "SYS", []assistantChatMessage{{Role: "user", Content: "hi"}}, func(string) error { return nil })
	if err == nil {
		t.Fatal("expected error on SSE error event")
	}
}

func TestAssistantStream_Timeout(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // never respond until released
	}))
	defer srv.Close()
	defer close(block)

	s := newAssistantTestServer(srv.URL)
	s.cfg.AssistantTimeout = 150 * time.Millisecond
	start := time.Now()
	err := s.assistantStream(context.Background(), "SYS", []assistantChatMessage{{Role: "user", Content: "hi"}}, func(string) error { return nil })
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("timeout took too long: %v", time.Since(start))
	}
}

func TestAssistantSystemPrompt_ContainsPersona(t *testing.T) {
	s := &Server{cfg: config.Config{}} // nil db -> generic prompt, no queries
	prompt := s.buildAssistantSystemPrompt(context.Background(), 0)
	if !strings.Contains(prompt, "Forky") {
		t.Errorf("prompt missing persona name Forky: %q", prompt)
	}
	want := "Responde en español, sé breve, amable y con un toque de humor. Eres el asistente de IA del restaurante."
	if !strings.Contains(prompt, want) {
		t.Errorf("prompt missing required directive.\nprompt=%q", prompt)
	}
}

func TestAssistantRecoverEncodedReply(t *testing.T) {
	// Clean base64 of a Spanish reply → decoded.
	b64 := "wqFIb2xhISDwn5iEIFBhcmEgbGEgc2VtYW5hIHF1ZSB2aWVuZSAoMTcgYWwgMjMgZGUgYWdvc3RvIGRlIDIwMjYpLCB0ZW5lcyAqKmRvcyByZXNlcnZhcyoq"
	got := assistantRecoverEncodedReply(b64)
	if !strings.Contains(got, "dos reservas") && !strings.Contains(got, "reservas") {
		t.Errorf("expected decoded Spanish, got %q", got)
	}
	if got == b64 {
		t.Errorf("base64 reply was not decoded")
	}

	// Plain readable prose is left untouched.
	plain := "¡Hola! Hoy tienes 2 reservas confirmadas 😊\n"
	if got := assistantRecoverEncodedReply(plain); got != plain {
		t.Errorf("plain prose was modified: %q", got)
	}

	// Markdown table is left untouched (not sniffed as base64).
	table := "| Fecha | Hora |\n|---|---|\n| 10 | 20:30 |"
	if got := assistantRecoverEncodedReply(table); got != table {
		t.Errorf("markdown table was modified")
	}

	// Base64 that decodes to binary garbage is kept as-is (avoid mangling).
	garbage := "wodobGEbm8gaGF5IHJlc2VydmFzIHBhcmEgaG95ICEgwr9BcsOtIHVuIGRpYSBt"
	if got := assistantRecoverEncodedReply(garbage); got != garbage {
		t.Errorf("binary garbage should be kept unchanged, got %q", got)
	}

	// Literal "\\n" escapes in a decoded base64 payload are repaired to newlines.
	escaped := base64.StdEncoding.EncodeToString([]byte("hola\\nlinea2"))
	if got := assistantRecoverEncodedReply(escaped); !strings.Contains(got, "\n") || strings.Contains(got, `\n`) {
		t.Errorf("literal newline escape not repaired: %q", got)
	}
}

// Misaligned (length % 4 != 0) base64 that MiniMax sometimes emits.
func TestRecoverMisalignedBase64(t *testing.T) {
	// base64 of "Información de reservas de la semana proxima."
	raw := base64.StdEncoding.EncodeToString([]byte("Información de reservas de la semana proxima."))
	got := assistantRecoverEncodedReply(raw)
	if !strings.Contains(got, "reservas de la semana") {
		t.Errorf("aligned base64 not decoded: %q", got)
	}
	// Trim 1 char to force length % 4 == 3 (misaligned) and still decode.
	mis := raw[:len(raw)-1]
	got = assistantRecoverEncodedReply(mis)
	if !strings.Contains(got, "reservas de la semana") || got == mis {
		t.Errorf("misaligned base64 (len%%4==3) not decoded: %q", got)
	}
}

func TestAssistantSystemPrompt_OutputContract(t *testing.T) {
	s := &Server{cfg: config.Config{}} // nil db -> generic prompt
	prompt := s.buildAssistantSystemPrompt(context.Background(), 0)
	for _, substr := range []string{
		"FORMATO DE RESPUESTA",
		"tabla Markdown",
		"```forky-chart",
		"\"type\": \"bar\"",
		"\"data\":",
		"stacked",
		"series",
		"NUNCA devuelvas el texto en base64",
		"PROHIBIDO envolver la respuesta en base64",
	} {
		if !strings.Contains(prompt, substr) {
			t.Errorf("prompt missing %q substrings.\nprompt=%q", substr, prompt)
		}
	}
}

func TestAssistantCleanseReply_StripsCJKAndFiller(t *testing.T) {
	cases := []struct {
		in, wantSub, notWant string
	}{
		{"©Hhoye, tienes 7 reservas con 具体的 y 人数.", "", "具体"},
		{"3Cléspero! 😄 ⚳ 共pó de suiernos! 😊", "Cléspero", "共"},
		{"Detalle: ⚳ 共 ⪮ ⊒ ⊓ © 劲爆", "", "劲爆"},
	}
	for _, c := range cases {
		got := assistantCleanseReply(c.in)
		if c.notWant != "" && strings.Contains(got, c.notWant) {
			t.Errorf("assistantCleanseReply(%q) still contains %q: got %q", c.in, c.notWant, got)
		}
		if c.wantSub != "" && !strings.Contains(got, c.wantSub) {
			t.Errorf("assistantCleanseReply(%q) missing %q: got %q", c.in, c.wantSub, got)
		}
	}
}

func TestAssistantCleanseReply_LeavesSpanishUntouched(t *testing.T) {
	clean := "¡Hola! 👋 Hoy tienes 7 reservas para 49 comensales: García y López 😊"
	if got := assistantCleanseReply(clean); got != clean {
		t.Errorf("clean Spanish was modified: %q", got)
	}
}

func TestAssistantCleanseReply_PreservesMarkdownTable(t *testing.T) {
	table := "| Fecha | Cliente | Personas |\n" +
		"|---|---|---|\n" +
		"| 19/07 | García | 具体的 6 |"
	got := assistantCleanseReply(table)
	if strings.Contains(got, "具体") {
		t.Errorf("CJK not stripped: %q", got)
	}
	for _, keep := range []string{"---|---|---|", "García"} {
		if !strings.Contains(got, keep) {
			t.Errorf("table marker lost %q: %q", keep, got)
		}
	}
}

func TestAssistantRecoverEncodedReply_WrappedVariants(t *testing.T) {
	msg := "Hola! Hoy tienes 1 reserva confirmada para el 2026-09-26."
	b64 := base64.StdEncoding.EncodeToString([]byte(msg))
	cases := map[string]string{
		"plain":  b64,
		"fence":  "```\n" + b64 + "\n```",
		"json":   "```json\n" + b64 + "\n```",
		"quoted": "\"" + b64 + "\"",
	}
	for name, in := range cases {
		out := assistantRecoverEncodedReply(in)
		if !strings.Contains(out, "reserva") {
			t.Errorf("%s: not recovered: %q", name, out)
		}
		if strings.ContainsAny(out, "`\uFFFD") {
			t.Errorf("%s: still has fence/replacement: %q", name, out)
		}
	}
}

// Unaccented Spanish prose must survive the base64 sniffer. Every word here is
// spelled with characters that also belong to the base64 alphabet, so once the
// sniffer stripped spaces the whole sentence looked like a payload and was
// "decoded" into garbage — silently corrupting good replies in the live chat.
func TestAssistantRecoverEncodedReply_PlainProseIsNotDecoded(t *testing.T) {
	cases := []string{
		"Si es posible Revisalo y te cuento el menu de comida disponible",
		"Estos son los horarios que tenemos fijados en el sistema",
		"Aqui tienes el resumen de stock con todas las categorias",
		"Hola Soy Forky el asistente de tu restaurante",
		"Hoy no tenemos reservas registradas en el sistema",
	}
	for _, c := range cases {
		if got := assistantRecoverEncodedReply(c); got != c {
			t.Errorf("plain prose was corrupted\n in : %q\n out: %q", c, got)
		}
	}
}

// Emoji from the Dingbats / Miscellaneous Symbols blocks are ordinary output,
// not MiniMax filler glyphs. The filler range used to swallow them, silently
// deleting ✨/✅/➡ from otherwise perfect replies.
func TestAssistantCleanseReply_KeepsCommonEmoji(t *testing.T) {
	cases := []string{
		"Listo ✅ la reserva quedo confirmada",
		"Mesa lista ➡ pasa por caja ⚡",
		"¡Todo correcto! ✔ Buen servicio ✨",
		"¡Hola! 😊 Aqui tienes los horarios 🍽️ y la carta 🍴✨",
	}
	for _, c := range cases {
		if got := assistantCleanseReply(c); got != c {
			t.Errorf("emoji were stripped\n in : %q\n out: %q", c, got)
		}
	}
}

// A tool-using turn concatenates the text of every model round, so a wrapped
// answer can arrive as several base64 blobs separated by blank lines (the model
// repeats itself once per round). The whole string is not valid base64, so the
// single-payload path cannot decode it and users saw a raw blob in the chat.
func TestAssistantRecoverEncodedReply_MultipleBlocks(t *testing.T) {
	blob := base64.StdEncoding.EncodeToString([]byte("¡Hola! Aqui el resumen de stock: 222 articulos."))
	prose := "Resultado: **222 articulos** sin existencias."
	chart := "```forky-chart\n{\"title\":\"Stock\"}\n```"

	// The same blob repeated per round, then real prose and a chart block.
	in := blob + "\n\n" + blob + "\n\n" + prose + "\n\n" + chart
	got := assistantRecoverEncodedReply(in)

	if strings.Contains(got, blob) {
		t.Errorf("base64 block was not decoded: %q", got)
	}
	if !strings.Contains(got, "resumen de stock") {
		t.Errorf("decoded text missing: %q", got)
	}
	if strings.Count(got, "resumen de stock") != 1 {
		t.Errorf("repeated identical blob should be collapsed once: %q", got)
	}
	if !strings.Contains(got, prose) {
		t.Errorf("plain prose block was lost: %q", got)
	}
	if !strings.Contains(got, "forky-chart") {
		t.Errorf("chart block was lost: %q", got)
	}
	// The closing fence must survive: the UI parser requires ```forky-chart ...
	// ``` and silently renders nothing without it.
	if !strings.HasSuffix(strings.TrimSpace(got), "```") {
		t.Errorf("chart closing fence was stripped: %q", got)
	}
}

// Blocks may be separated by CRLF or by more than one blank line.
func TestAssistantRecoverEncodedReply_MultipleBlocksCRLF(t *testing.T) {
	blob := base64.StdEncoding.EncodeToString([]byte("Resumen de stock disponible."))
	for _, sep := range []string{"\r\n\r\n", "\n\n\n"} {
		in := blob + sep + "Texto normal de cierre."
		got := assistantRecoverEncodedReply(in)
		if strings.Contains(got, blob) {
			t.Errorf("sep %q: base64 not decoded: %q", sep, got)
		}
		if !strings.Contains(got, "Texto normal de cierre.") {
			t.Errorf("sep %q: trailing prose lost: %q", sep, got)
		}
	}
}

func TestAssistantRecoverEncodedReply_Truncated(t *testing.T) {
	// len%4 == 1 truncation must still recover the readable head.
	msg := "Información de la semana próxima para el lunes con detalle."
	b64 := base64.StdEncoding.EncodeToString([]byte(msg))
	// ensure at least one truncation form works: trim until len%4==1
	for i := 0; i < 8; i++ {
		cut := b64[:len(b64)-i]
		if len(cut)%4 == 1 {
			out := assistantRecoverEncodedReply(cut)
			if !strings.Contains(out, "semana") {
				t.Errorf("truncated(pad+%d) not recovered: %q", i, out)
			}
			break
		}
	}
}
