package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"time"
)

// buildBookingWhatsAppMessage formats the WhatsApp confirmation text for a booking.
// It mirrors the legacy PHP sendWhatsAppConfirmationWithButtonsUazApi message structure.
func buildBookingWhatsAppMessage(brandName string, booking map[string]any, bookingID int64, baseURL string) string {
	customerName := anyToString(booking["customer_name"])
	resDate := anyToString(booking["reservation_date"])
	resTime := anyToString(booking["reservation_time"])
	partySize, _ := anyToInt(booking["party_size"])
	highChairs, _ := anyToInt(booking["high_chairs"])
	babyStrollers, _ := anyToInt(booking["baby_strollers"])

	specialMenu := toBool(booking["special_menu"])

	// Format date DD/MM/YYYY.
	dateDisplay := resDate
	if t, err := time.Parse("2006-01-02", resDate); err == nil {
		dateDisplay = t.Format("02/01/2006")
	}
	// Format time HH:MM.
	timeDisplay := formatHHMM(resTime)

	msg := fmt.Sprintf("*Confirmación de Reserva - %s*\n\n", brandName)
	msg += fmt.Sprintf("Hola %s,\n\n", customerName)
	msg += fmt.Sprintf("Gracias por elegir %s. Su reserva ha sido confirmada:\n\n", brandName)
	msg += fmt.Sprintf("📅 *Fecha:* %s\n", dateDisplay)
	msg += fmt.Sprintf("🕒 *Hora:* %s\n", timeDisplay)
	msg += fmt.Sprintf("👥 *Personas:* %d\n", partySize)

	if specialMenu {
		menuTitle := extractFirstJSONArrayItem(anyToString(booking["arroz_type"]))
		if menuTitle != "" {
			msg += fmt.Sprintf("🍽️ *Menu:* %s\n", menuTitle)
		}
		principales := strings.TrimSpace(anyToString(booking["commentary"]))
		if principales != "" {
			msg += formatPrincipalesWhatsApp(principales)
		}
	} else {
		msg += formatArrozWhatsApp(booking)
	}

	if highChairs > 0 {
		msg += fmt.Sprintf("👶 *Tronas:* %d\n", highChairs)
	}
	if babyStrollers > 0 {
		msg += fmt.Sprintf("🍼 *Carros de bebé:* %d\n", babyStrollers)
	}

	msg += "\nAl hacer esta reserva, usted ha confirmado y aceptado las condiciones de reserva y políticas del restaurante, las cuales puede consultar en el botón de abajo."

	return msg
}

// buildBookingWhatsAppButtonPayload creates the full UAZAPI button payload.
func buildBookingWhatsAppButtonPayload(brandName string, booking map[string]any, bookingID int64, baseURL string) map[string]any {
	cc := anyToString(booking["contact_phone_country_code"])
	phone := anyToString(booking["contact_phone"])
	_, _, cleanPhone, _ := normalizePhoneParts(cc, phone)

	text := buildBookingWhatsAppMessage(brandName, booking, bookingID, baseURL)

	base := strings.TrimRight(baseURL, "/")
	choices := []string{
		"CONDICIONES|" + base + "/booking-policies",
		fmt.Sprintf("Cancelar Reserva|%s/cancel?id=%d", base, bookingID),
	}

	return map[string]any{
		"number":  cleanPhone,
		"type":    "button",
		"text":    text,
		"choices": choices,
	}
}

// sendBookingWhatsAppToCustomer sends a WhatsApp confirmation to the customer.
// It tries button message first, falls back to plain text. Returns error on failure.
func sendBookingWhatsAppToCustomer(ctx context.Context, s *Server, restaurantID int, booking map[string]any, bookingID int64) error {
	uazURL, uazToken := s.uazapiBaseAndToken(ctx, restaurantID)
	if uazURL == "" || uazToken == "" {
		return fmt.Errorf("WhatsApp (UAZAPI) no configurado")
	}

	branding, _ := s.loadRestaurantBranding(ctx, restaurantID)
	brandName := branding.BrandName
	if brandName == "" {
		brandName = "Restaurante"
	}
	baseURL := publicBaseURLFromContext(ctx, s, restaurantID)

	payload := buildBookingWhatsAppButtonPayload(brandName, booking, bookingID, baseURL)

	// Try button message first.
	buttonEndpoint := uazURL + "/send/menu?token=" + url.QueryEscape(uazToken)
	body, status, err := sendUazAPI(ctx, buttonEndpoint, payload)
	if err == nil && (status == 200 || status == 201) {
		log.Printf("WhatsApp button confirmation sent for booking #%d", bookingID)
		return nil
	}
	log.Printf("WhatsApp button send failed (HTTP %d: %s), falling back to text", status, body)

	// Fallback: plain text.
	textEndpoint := uazURL + "/send/text?token=" + url.QueryEscape(uazToken)
	textPayload := map[string]any{
		"number": payload["number"],
		"text":   payload["text"],
	}
	body, status, err = sendUazAPI(ctx, textEndpoint, textPayload)
	if err != nil {
		return fmt.Errorf("error enviando WhatsApp: %v", err)
	}
	if status != 200 && status != 201 {
		return fmt.Errorf("WhatsApp respondió HTTP %d: %s", status, truncate(body, 200))
	}

	log.Printf("WhatsApp text confirmation sent for booking #%d", bookingID)
	return nil
}

// publicBaseURLFromContext resolves the public base URL for generating links.
// Priority: env var > DB primary domain > hardcoded fallback.
func publicBaseURLFromContext(ctx context.Context, s *Server, restaurantID int) string {
	if v := strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	if domain := s.fetchPrimaryDomain(ctx, restaurantID); domain != "" && domain != "localhost" && domain != "127.0.0.1" {
		return "https://" + domain
	}
	return "https://alqueriavillacarmen.com"
}

// --- WhatsApp formatting helpers ---

func formatArrozWhatsApp(booking map[string]any) string {
	toggleArroz := strings.TrimSpace(anyToString(booking["toggleArroz"]))
	if toggleArroz != "true" {
		return "🍚 *Arroz:* No\n"
	}

	types := parseJSONStringArray(anyToString(booking["arroz_type"]))
	servs := parseJSONIntArray(anyToString(booking["arroz_servings"]))

	if len(types) == 0 || len(servs) == 0 {
		return "🍚 *Arroz:* No\n"
	}

	parts := make([]string, 0, len(types))
	n := min(len(types), len(servs))
	for i := 0; i < n; i++ {
		t := strings.TrimSpace(types[i])
		s := servs[i]
		if t == "" || s <= 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s x %d", t, s))
	}

	if len(parts) == 0 {
		return "🍚 *Arroz:* No\n"
	}

	if len(parts) == 1 {
		return fmt.Sprintf("🍚 *Arroz:* %s\n", parts[0])
	}

	return "🍚 *Arroz:*\n  • " + strings.Join(parts, "\n  • ") + "\n"
}

func formatPrincipalesWhatsApp(summary string) string {
	items := splitAndClean(summary)
	if len(items) == 0 {
		return ""
	}
	if len(items) == 1 {
		return fmt.Sprintf("🍽️ *Principales:* %s\n", items[0])
	}
	return "🍽️ *Principales:*\n  • " + strings.Join(items, "\n  • ") + "\n"
}

// --- Shared helpers ---

func extractFirstJSONArrayItem(raw string) string {
	var arr []string
	if err := json.Unmarshal([]byte(raw), &arr); err != nil || len(arr) == 0 {
		return ""
	}
	return strings.TrimSpace(arr[0])
}

func parseJSONStringArray(raw string) []string {
	var arr []string
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return nil
	}
	return arr
}

func parseJSONIntArray(raw string) []int {
	var arr []int
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return nil
	}
	return arr
}

func splitAndClean(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func toBool(v any) bool {
	switch val := v.(type) {
	case bool:
		return val
	case int:
		return val != 0
	case int64:
		return val != 0
	case float64:
		return val != 0
	case string:
		return val == "1" || strings.EqualFold(val, "true")
	default:
		return false
	}
}
