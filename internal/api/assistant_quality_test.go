package api

import (
	"strings"
	"testing"
)

// Forky replies were reaching the chat as mojibake. The model itself answers
// cleanly for the same prompt, but a garbled reply gets persisted and then
// replayed as conversation history on the next turn, where it primes the model
// to produce more garbage — a feedback loop that degrades a session once any
// single reply is corrupted (18% of stored history was contaminated).
//
// The fix is to detect unusable text and keep it out of the loop: it is neither
// persisted nor replayed as context.

func TestAssistantReplyIsGarbled(t *testing.T) {
	garbled := []struct{ name, text string }{
		{"symbol soup", "A C c c ⛺⛅⛟C⌃F Rv⌃ R⌃ Av ⌅ IEste⌅ ⛅⛅CCc⛅⌅A⌅ ⛅⌅⛅⌅⌅⌅⌅⌅ ⌃F⌅⌅⌅ ⛅⌅F⌅⌅⌅⌅"},
		{"base64 blob", "wqFIb2xhISDwn5GOIGdyYWNpYXMgcG9yIGNvbnN1bHRhcmxvLiBBcXXDrSBlbCByZXN1bWVuIGRlIHN0b2Nr"},
		{"letter salad", "ido=HHwhHHwh=1HHwh-cDD/DD/DDDD=DD/DD/DDDD"},
		{"mixed scripts", "Flip, adí el probÎ¼ema ר de una planta de hOrarios ﻯ ゲ43##BУ8+223"},
		{"replacement chars", "Resultado: \ufffd\ufffd\ufffd stock \ufffd\ufffd\ufffd agotado \ufffd\ufffd\ufffd\ufffd"},
	}
	for _, c := range garbled {
		if !assistantReplyIsGarbled(c.text) {
			t.Errorf("%s: expected garbled, got clean: %q", c.name, c.text)
		}
	}

	// Normal replies — including tables, charts, emoji and accents — must never
	// be classified as garbled, or the assistant would go mute.
	clean := []struct{ name, text string }{
		{"greeting", "¡Hola! 👋 Soy Forky, el asistente de tu restaurante. ¿En qué puedo ayudarte?"},
		{"accents", "Aquí están las reservas de mañana: García, Pérez y Muñoz."},
		{"emoji", "Listo ✅ la reserva quedó confirmada ➡ mesa 4 ⚡ ✨"},
		{"markdown table", "| Cliente | Fecha | Hora |\n|---|---|---|\n| García | 2026-08-12 | 20:30 |"},
		{"chart block", "Aquí va el resumen 📊\n\n```forky-chart\n{\"title\": \"Stock\", \"type\": \"donut\", \"data\": [{\"label\": \"Agotados\", \"value\": 222}]}\n```"},
		{"prices and money", "El menú cuesta 35,00 € e incluye bebida. Total: 1.250,50 €."},
		{"unaccented prose", "Si es posible revisalo y te cuento el menu de comida disponible"},
		{"short ack", "Hecho ✅"},
		{"empty", ""},
		{"phone and url", "Llama al 962 123 456 o visita https://villacarmen.com/menu"},
	}
	for _, c := range clean {
		if assistantReplyIsGarbled(c.text) {
			t.Errorf("%s: clean reply misclassified as garbled: %q", c.name, c.text)
		}
	}
}

// A garbled reply must not be persisted: it would come back as history and
// prime the model to emit more garbage.
func TestAssistantSanitizeForHistory(t *testing.T) {
	if got := assistantSanitizeForHistory("A C c ⛺⛅⛟C⌃F ⌅⌅⌅⌅⌅⌅⌅⌅ ⌃F⌅⌅⌅ ⛅⌅F⌅⌅⌅⌅"); got != "" {
		t.Errorf("garbled reply should not be storable, got %q", got)
	}
	clean := "¡Hola! Hoy tienes 2 reservas 😊"
	if got := assistantSanitizeForHistory(clean); got != clean {
		t.Errorf("clean reply must round-trip: %q", got)
	}
}

// loadHistory must drop contaminated rows already sitting in the database, so
// existing poisoned sessions recover instead of degrading forever.
func TestAssistantFilterHistory(t *testing.T) {
	in := []assistantChatMessage{
		{Role: "user", Content: "hola"},
		{Role: "assistant", Content: "¡Hola! ¿En qué puedo ayudarte?"},
		{Role: "user", Content: "dame el stock"},
		{Role: "assistant", Content: "wqFIb2xhISDwn5GOIGdyYWNpYXMgcG9yIGNvbnN1bHRhcmxvLiBBcXXDrSBlbCByZXN1bWVu"},
		{Role: "user", Content: "y los horarios"},
		{Role: "assistant", Content: "A C c ⛺⛅⛟C⌃F ⌅⌅⌅⌅⌅⌅⌅⌅ ⌃F⌅⌅⌅ ⛅⌅F⌅⌅⌅⌅"},
	}
	out := assistantFilterHistory(in)

	for _, m := range out {
		text, _ := m.Content.(string)
		if assistantReplyIsGarbled(text) {
			t.Errorf("garbled row survived filtering: %q", text)
		}
	}
	// User turns are never dropped: they carry the question the model needs.
	users := 0
	for _, m := range out {
		if m.Role == "user" {
			users++
		}
	}
	if users != 3 {
		t.Errorf("user turns = %d, want 3 (user input must be preserved)", users)
	}
	// The clean assistant reply survives.
	found := false
	for _, m := range out {
		if s, _ := m.Content.(string); strings.Contains(s, "En qué puedo ayudarte") {
			found = true
		}
	}
	if !found {
		t.Error("clean assistant reply was dropped")
	}
}

// Non-string content (tool_use / tool_result blocks) must pass through
// untouched: the filter only judges plain text replies.
func TestAssistantFilterHistory_KeepsStructuredContent(t *testing.T) {
	blocks := []map[string]any{{"type": "tool_use", "id": "t1", "name": "stock_summary"}}
	in := []assistantChatMessage{{Role: "assistant", Content: blocks}}
	out := assistantFilterHistory(in)
	if len(out) != 1 {
		t.Fatalf("structured content dropped: %+v", out)
	}
}

// End-to-end shape of the feedback loop: whatever the model returns, what goes
// back into the conversation must be clean. This is the property that actually
// matters — a garbled turn must not be able to influence the next one.
func TestAssistantGarbledReplyNeverReentersConversation(t *testing.T) {
	garbled := "A C c c ⛺⛅⛟C⌃F Rv⌃ R⌃ Av ⌅ IEste⌅ ⛅⛅CCc⛅⌅A⌅ ⛅⌅⌅⌅⌅⌅⌅⌅ ⌃F⌅⌅⌅"

	// 1. It is not written to the transcript.
	if assistantSanitizeForHistory(garbled) != "" {
		t.Error("garbled reply would be persisted")
	}
	// 2. Even if an older row exists, it is not replayed as context.
	replayed := assistantFilterHistory([]assistantChatMessage{
		{Role: "user", Content: "dame el stock"},
		{Role: "assistant", Content: garbled},
		{Role: "user", Content: "y los horarios"},
	})
	for _, m := range replayed {
		if s, ok := m.Content.(string); ok && s == garbled {
			t.Error("garbled reply was replayed as context")
		}
	}
	// 3. The surrounding user turns survive, so the thread still makes sense.
	if len(replayed) != 2 {
		t.Errorf("replayed turns = %d, want 2 user turns", len(replayed))
	}
}
