package api

import (
	"strings"
	"testing"
)

func TestRenderBotSystemPrompt_IncludesTenantData(t *testing.T) {
	data := botPromptData{
		BrandName:  "Tasca María",
		Phone:      "961234567",
		Address:    "Calle Mayor 1, Valencia",
		Email:      "hola@tascamaria.es",
		Website:    "https://tascamaria.es",
		TodayES:    "sábado, 14 de febrero de 2026",
		TodayISO:   "2026-02-14",
		PushName:   "Jaime",
		UserPhone:  "34612345678",
		RiceTypes:  []string{"Paella Valenciana", "Arroz Negro"},
		Hours:      "13:30, 14:00, 14:30",
		DailyLimit: 45,
		Tenant:     botTenantConfig{LanguageDefault: "es", Tone: "cercano", CustomInstructions: "REGLA-PERSONALIZADA-XYZ"},
		MenuURL:    "https://tascamaria.es/carta.pdf",
	}
	out := renderBotSystemPrompt(data)

	for _, want := range []string{
		"Tasca María",
		"961234567",
		"Calle Mayor 1, Valencia",
		"Jaime",
		"34612345678",
		"sábado, 14 de febrero de 2026",
		"REGLA-PERSONALIZADA-XYZ",
		"send_message",
		// Rices and hours are no longer inlined; the bot must fetch them
		// via tools, so the prompt references those tools instead.
		"get_rice_menu",
		"get_default_schedule",
		"get_day_schedule",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt missing %q", want)
		}
	}

	// The concrete rice names and hours must NOT be written into the prompt.
	for _, forbidden := range []string{"Paella Valenciana", "Arroz Negro", "13:30", "TIPOS DE ARROZ", "HORARIOS DE HOY"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("prompt must not inline %q", forbidden)
		}
	}
}

func TestRenderBotSystemPrompt_LanguageEnglish(t *testing.T) {
	out := renderBotSystemPrompt(botPromptData{
		BrandName: "Villa",
		Tenant:    botTenantConfig{LanguageDefault: "en"},
	})
	if !strings.Contains(out, "Idioma por defecto: en") {
		t.Error("prompt should state default language")
	}
	if !strings.Contains(out, "idioma del cliente") {
		t.Error("prompt should instruct to mirror customer language")
	}
}

func TestRenderBotSystemPrompt_NoRice(t *testing.T) {
	out := renderBotSystemPrompt(botPromptData{BrandName: "X"})
	if strings.Contains(out, "TIPOS DE ARROZ") {
		t.Error("rice section should be omitted when no rice types")
	}
}
