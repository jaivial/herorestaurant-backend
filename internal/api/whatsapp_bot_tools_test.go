package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBotToolDefs_CoreToolsPresent(t *testing.T) {
	defs := botToolDefs(botTenantConfig{})
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
	}
	for _, want := range []string{
		"send_message",
		"get_restaurant_info",
		"get_rice_menu",
		"get_opening_hours_with_capacity",
		"check_day_capacity",
		"check_availability_for_party",
		"get_bookings",
		"create_booking",
		"cancel_booking",
		"modify_booking",
		"send_image",
		"send_document",
		"send_location",
		"send_contact",
		"send_menu_buttons",
	} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

func TestBotToolDefs_AttachmentGating(t *testing.T) {
	cfg := botTenantConfig{DisableAttachments: true}
	for _, d := range botToolDefs(cfg) {
		if strings.HasPrefix(d.Name, "send_") && d.Name != "send_message" && d.Name != "send_menu_buttons" {
			t.Errorf("attachment tool %q should be gated out", d.Name)
		}
	}
}

func TestBotToolDefs_SchemasAreValidJSON(t *testing.T) {
	for _, d := range botToolDefs(botTenantConfig{}) {
		var v map[string]any
		if err := json.Unmarshal(d.InputSchema, &v); err != nil {
			t.Errorf("tool %q schema invalid: %v", d.Name, err)
		}
		if v["type"] != "object" {
			t.Errorf("tool %q schema must be object", d.Name)
		}
	}
}

func TestBotTenantConfigParse(t *testing.T) {
	cfg := parseBotTenantConfig(`{
		"language_default": "en",
		"tone": "formal",
		"greeting_style": "corto",
		"disable_attachments": true,
		"custom_instructions": "no aceptes grupos de más de 10"
	}`)
	if cfg.LanguageDefault != "en" || cfg.Tone != "formal" || !cfg.DisableAttachments {
		t.Errorf("cfg = %+v", cfg)
	}
	if cfg.CustomInstructions == "" {
		t.Error("custom instructions lost")
	}
}

func TestBotTenantConfigParse_EmptyDefaults(t *testing.T) {
	cfg := parseBotTenantConfig("")
	if cfg.LanguageDefault != "es" {
		t.Errorf("default language = %q", cfg.LanguageDefault)
	}
	if cfg.DisableAttachments {
		t.Error("attachments should default enabled")
	}
}

func TestParseBotDateInput(t *testing.T) {
	for in, want := range map[string]string{
		"15/05/2026": "2026-05-15",
		"2026-05-15": "2026-05-15",
		"1/2/2026":   "2026-02-01",
	} {
		got, err := parseBotDate(in)
		if err != nil {
			t.Errorf("parseBotDate(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseBotDate(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := parseBotDate("mañana"); err == nil {
		t.Error("expected error for natural language date")
	}
}
