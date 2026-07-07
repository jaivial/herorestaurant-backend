package api

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WhatsApp confirmation message builder tests
// ---------------------------------------------------------------------------

func TestBuildWhatsAppConfirmationMessage(t *testing.T) {
	booking := map[string]any{
		"customer_name":    "Juan García",
		"reservation_date": "2026-04-15",
		"reservation_time": "14:00",
		"party_size":       4,
		"special_menu":     0,
		"toggleArroz":      "true",
		"arroz_type":       `["Arroz a la valenciana","Arroz del senyoret"]`,
		"arroz_servings":   `[2,1]`,
		"high_chairs":      1,
		"baby_strollers":   0,
	}

	msg := buildBookingWhatsAppMessage("Alquería Villa Carmen", booking, 123, "https://example.com")

	if !strings.Contains(msg, "Juan García") {
		t.Error("message should contain customer name")
	}
	if !strings.Contains(msg, "15/04/2026") {
		t.Error("message should contain formatted date DD/MM/YYYY")
	}
	if !strings.Contains(msg, "14:00") {
		t.Error("message should contain reservation time")
	}
	if !strings.Contains(msg, "4") {
		t.Error("message should contain party size")
	}
	if !strings.Contains(msg, "Arroz a la valenciana") {
		t.Error("message should contain arroz type")
	}
	if !strings.Contains(msg, "Arroz del senyoret") {
		t.Error("message should contain second arroz type")
	}
}

func TestBuildWhatsAppConfirmationMessageSpecialMenu(t *testing.T) {
	booking := map[string]any{
		"customer_name":    "María López",
		"reservation_date": "2026-05-01",
		"reservation_time": "21:30",
		"party_size":       8,
		"special_menu":     1,
		"arroz_type":       `["Menú Degustación Premium"]`,
		"commentary":       "Lubina x 3, Solomillo x 2",
		"high_chairs":      0,
		"baby_strollers":   0,
	}

	msg := buildBookingWhatsAppMessage("Mi Restaurante", booking, 456, "https://example.com")

	if !strings.Contains(msg, "María López") {
		t.Error("message should contain customer name")
	}
	if !strings.Contains(msg, "Menú Degustación Premium") {
		t.Error("message should contain menu title")
	}
	if !strings.Contains(msg, "Lubina") {
		t.Error("message should contain principales items")
	}
	if !strings.Contains(msg, "Mi Restaurante") {
		t.Error("message should contain brand name")
	}
}

func TestBuildWhatsAppButtonPayload(t *testing.T) {
	booking := map[string]any{
		"customer_name":              "Juan García",
		"contact_phone":              "612345678",
		"contact_phone_country_code": "34",
		"reservation_date":           "2026-04-15",
		"reservation_time":           "14:00",
		"party_size":                 4,
		"special_menu":               0,
		"high_chairs":                0,
		"baby_strollers":             0,
	}

	payload := buildBookingWhatsAppButtonPayload(
		"Alquería Villa Carmen",
		booking,
		123,
		"https://example.com",
	)

	if payload["type"] != "button" {
		t.Error("payload type should be 'button'")
	}
	number, ok := payload["number"].(string)
	if !ok || number == "" {
		t.Error("payload should have a non-empty 'number' field")
	}
	if !strings.HasPrefix(number, "34") {
		t.Errorf("number should include country code, got %q", number)
	}
	text, _ := payload["text"].(string)
	if text == "" {
		t.Error("payload should have non-empty 'text'")
	}
	choices, ok := payload["choices"].([]string)
	if !ok || len(choices) < 2 {
		t.Fatalf("payload should have at least 2 choices, got %v", payload["choices"])
	}
	// First choice should be policies button.
	if !strings.Contains(choices[0], "CONDICIONES") {
		t.Errorf("first choice should contain CONDICIONES, got %q", choices[0])
	}
	// Second choice should be cancel button with booking ID.
	if !strings.Contains(choices[1], "Cancelar") {
		t.Errorf("second choice should contain Cancelar, got %q", choices[1])
	}
	if !strings.Contains(choices[1], "123") {
		t.Errorf("cancel choice should contain booking ID 123, got %q", choices[1])
	}
}

func TestBuildWhatsAppButtonPayloadWrongCountryCode(t *testing.T) {
	// Simulate a Spanish mobile (692...) with wrong country code 351 (Portugal).
	// normalizePhoneParts should produce the same E.164 by joining cc+phone
	// since the phone doesn't start with the cc prefix.
	booking := map[string]any{
		"customer_name":              "Juan García",
		"contact_phone":              "692747052",
		"contact_phone_country_code": "351",
		"reservation_date":           "2026-04-15",
		"reservation_time":           "14:00",
		"party_size":                 4,
		"special_menu":               0,
	}

	payload := buildBookingWhatsAppButtonPayload("Test", booking, 2386, "https://example.com")

	number, ok := payload["number"].(string)
	if !ok || number == "" {
		t.Fatal("payload should have a non-empty 'number' field")
	}
	// The number should be the full E.164: cc+phone = "351692747052"
	// (the user chose Portugal as country code — that's what gets sent)
	if len(number) < 10 {
		t.Errorf("number should be a valid E.164 number, got %q (too short)", number)
	}
}

func TestBuildWhatsAppButtonPayloadPhoneWithCountryCode(t *testing.T) {
	// Phone already includes country code in the phone field.
	booking := map[string]any{
		"customer_name":              "Ana Martín",
		"contact_phone":              "34612345678",
		"contact_phone_country_code": "34",
		"reservation_date":           "2026-05-01",
		"reservation_time":           "21:00",
		"party_size":                 2,
		"special_menu":               0,
	}

	payload := buildBookingWhatsAppButtonPayload("Test", booking, 999, "https://example.com")

	number, ok := payload["number"].(string)
	if !ok || number == "" {
		t.Fatal("payload should have a non-empty 'number' field")
	}
	// Should NOT double-prefix: "3434612345678" would be wrong.
	if number == "3434612345678" {
		t.Errorf("number should not be double-prefixed, got %q", number)
	}
}

// ---------------------------------------------------------------------------
// Email HTML builder tests
// ---------------------------------------------------------------------------

func TestBuildBookingEmailHTML(t *testing.T) {
	booking := map[string]any{
		"customer_name":    "Juan García",
		"reservation_date": "2026-04-15",
		"reservation_time": "14:00",
		"party_size":       4,
		"special_menu":     0,
		"toggleArroz":      "true",
		"arroz_type":       `["Arroz a la valenciana"]`,
		"arroz_servings":   `[3]`,
		"high_chairs":      1,
		"baby_strollers":   0,
	}

	html := buildBookingEmailHTML("Alquería Villa Carmen", "https://example.com/logo.png", "600111222", "info@x.com", "Calle Falsa 123", booking, 789, "https://example.com")

	if !strings.Contains(html, "Juan García") {
		t.Error("HTML should contain customer name")
	}
	if !strings.Contains(html, "15/04/2026") {
		t.Error("HTML should contain formatted date")
	}
	if !strings.Contains(html, "14:00") {
		t.Error("HTML should contain reservation time")
	}
	if !strings.Contains(html, "Arroz a la valenciana") {
		t.Error("HTML should contain arroz info")
	}
	if !strings.Contains(html, "cancel?id=789") {
		t.Error("HTML should contain cancel link with booking ID")
	}
	if !strings.Contains(html, "booking-policies") {
		t.Error("HTML should contain policies link")
	}
	if !strings.Contains(html, "logo.png") {
		t.Error("HTML should contain logo URL")
	}
	if !strings.Contains(html, "600111222") {
		t.Error("HTML should contain contact phone")
	}
	if !strings.Contains(html, "mailto:info@x.com") {
		t.Error("HTML should contain contact email mailto link")
	}
	if !strings.Contains(html, "Calle Falsa 123") {
		t.Error("HTML should contain contact address")
	}
	if strings.Contains(html, "638 85 72 94") {
		t.Error("HTML must not contain old hardcoded phone")
	}
	if strings.Contains(html, "reservas@alqueriavillacarmen.com") {
		t.Error("HTML must not contain old hardcoded contact email")
	}
}

func TestBuildBookingEmailHTMLAllEmptyContact(t *testing.T) {
	booking := map[string]any{
		"customer_name":    "Empty Contact",
		"reservation_date": "2026-04-15",
		"reservation_time": "14:00",
		"party_size":       2,
		"special_menu":     0,
	}

	html := buildBookingEmailHTML("Empty", "https://example.com/logo.png", "", "", "", booking, 555, "https://example.com")

	if strings.Contains(html, "Teléfono:") {
		t.Error("HTML should not contain Teléfono line when phone empty")
	}
	if strings.Contains(html, "mailto:") {
		t.Error("HTML should not contain mailto when email empty")
	}
	if strings.Contains(html, "Dirección:") {
		t.Error("HTML should not contain Dirección line when address empty")
	}
	if strings.Contains(html, "Si necesita modificar o cancelar su reserva") {
		t.Error("HTML should omit contact paragraph when all contact fields empty")
	}
}

func TestBuildBookingEmailHTMLSpecialMenu(t *testing.T) {
	booking := map[string]any{
		"customer_name":    "María López",
		"reservation_date": "2026-05-01",
		"reservation_time": "21:30",
		"party_size":       8,
		"special_menu":     1,
		"arroz_type":       `["Menú Degustación"]`,
		"commentary":       "Lubina x 3, Solomillo x 2",
		"high_chairs":      0,
		"baby_strollers":   0,
	}

	html := buildBookingEmailHTML("Restaurante", "https://example.com/logo.png", "600111222", "info@x.com", "Calle Falsa 123", booking, 101, "https://example.com")

	if !strings.Contains(html, "Menú Degustación") {
		t.Error("HTML should contain menu title for special menu booking")
	}
	if !strings.Contains(html, "Lubina") {
		t.Error("HTML should contain principales items")
	}
}

func TestBuildBookingEmailHTMLMultipleArroz(t *testing.T) {
	booking := map[string]any{
		"customer_name":    "Carlos Ruiz",
		"reservation_date": "2026-06-10",
		"reservation_time": "14:30",
		"party_size":       6,
		"special_menu":     0,
		"toggleArroz":      "true",
		"arroz_type":       `["Arroz a la valenciana","Arroz del senyoret","Arroz negro"]`,
		"arroz_servings":   `[2,2,1]`,
		"high_chairs":      0,
		"baby_strollers":   1,
	}

	html := buildBookingEmailHTML("Test Restaurant", "https://example.com/logo.png", "600111222", "info@x.com", "Calle Falsa 123", booking, 202, "https://example.com")

	if !strings.Contains(html, "Arroz a la valenciana") {
		t.Error("HTML should contain first arroz type")
	}
	if !strings.Contains(html, "Arroz del senyoret") {
		t.Error("HTML should contain second arroz type")
	}
	if !strings.Contains(html, "Arroz negro") {
		t.Error("HTML should contain third arroz type")
	}
}
