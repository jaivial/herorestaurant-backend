package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// buildBookingWhatsAppMessage formats the WhatsApp confirmation text for a booking.
// It mirrors the legacy PHP sendWhatsAppConfirmationWithButtonsUazApi message structure.
func bookedFloorNumber(booking map[string]any) (int, bool) {
	raw, exists := booking["preferred_floor_number"]
	if !exists || raw == nil {
		return 0, false
	}
	n, err := anyToInt(raw)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

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
	if floorNumber, ok := bookedFloorNumber(booking); ok {
		msg += fmt.Sprintf("📍 *Planta:* Planta %d\n", floorNumber)
	}
	if salonName := strings.TrimSpace(anyToString(booking["preferred_salon_name"])); salonName != "" {
		msg += fmt.Sprintf("🚪 *Salón:* %s\n", salonName)
	}

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

	// Tronas and carritos are always listed, including 0, so the customer can
	// verify at a glance that nothing was requested by mistake.
	msg += fmt.Sprintf("👶 *Tronas:* %d\n", highChairs)
	msg += fmt.Sprintf("🍼 *Carros de bebé:* %d\n", babyStrollers)

	msg += "\nAl hacer esta reserva, usted ha confirmado y aceptado las condiciones de reserva y políticas del restaurante, las cuales puede consultar en el botón de abajo."

	return msg
}

// bookingWhatsAppMessage is the provider-neutral confirmation to deliver.
type bookingWhatsAppMessage struct {
	To      string
	Text    string
	Choices []string
}

// buildBookingWhatsAppButtonPayload builds the confirmation message. Choices use
// the shared "label|url" convention understood by every gateway.
func buildBookingWhatsAppButtonPayload(brandName string, booking map[string]any, bookingID int64, baseURL string) (bookingWhatsAppMessage, error) {
	cc := anyToString(booking["contact_phone_country_code"])
	phone := anyToString(booking["contact_phone"])
	_, _, cleanPhone, ok := normalizePhoneParts(cc, phone)
	if !ok || cleanPhone == "" {
		return bookingWhatsAppMessage{}, fmt.Errorf("teléfono de contacto inválido")
	}

	base := strings.TrimRight(baseURL, "/")
	return bookingWhatsAppMessage{
		To:   cleanPhone,
		Text: buildBookingWhatsAppMessage(brandName, booking, bookingID, baseURL),
		Choices: []string{
			"CONDICIONES|" + base + "/booking-policies",
			fmt.Sprintf("Cancelar Reserva|%s/cancel?id=%d", base, bookingID),
		},
	}, nil
}

// buildBookingReminderPayload builds the reconfirmation (reminder) message and
// its single confirm button. Shared by the n8n webhook path and the scheduled
// reminder worker so both render one identical template, with the confirm URL
// resolved from the restaurant's own base URL.
func buildBookingReminderPayload(brandName, customerName, dateDisplay, timeDisplay string, partySize int, floorDisplay, salonDisplay string, extras bookingReminderExtras, bookingID int64, baseURL string) bookingWhatsAppMessage {
	base := strings.TrimRight(baseURL, "/")
	return bookingWhatsAppMessage{
		Text:    buildBookingReminderMessage(customerName, brandName, dateDisplay, timeDisplay, partySize, floorDisplay, salonDisplay, extras),
		Choices: []string{fmt.Sprintf("✅ Confirmar Reserva|%s/confirm?id=%d", base, bookingID)},
	}
}

// resolveRestaurantPublicBaseURL prefers the restaurant's configured website
// (ConfigContacto → restaurant_info.website) so public booking links point to
// its own domain, then falls back to env var / primary domain.
func resolveRestaurantPublicBaseURL(ctx context.Context, s *Server, restaurantID int) string {
	if info, err := s.loadRestaurantInfo(ctx, restaurantID); err == nil && info.Website != "" {
		if normalized, nErr := normalizeRestaurantWebsiteURL(info.Website); nErr == nil && normalized != "" {
			return normalized
		}
	}
	return publicBaseURLFromContext(ctx, s, restaurantID)
}

// restaurantBrandNameOrDefault resolves the display brand name for messages.
func restaurantBrandNameOrDefault(ctx context.Context, s *Server, restaurantID int) string {
	branding, _ := s.loadRestaurantBranding(ctx, restaurantID)
	if name := strings.TrimSpace(branding.BrandName); name != "" {
		return name
	}
	return "Restaurante"
}

// sendBookingWhatsAppToCustomer sends a WhatsApp confirmation to the customer
// through the restaurant's provisioned gateway (UAZAPI or Evolution).
// It tries the button message first and falls back to plain text.
func sendBookingWhatsAppToCustomer(ctx context.Context, s *Server, restaurantID int, booking map[string]any, bookingID int64) error {
	// Operators can disable booking confirmations per restaurant from
	// ConfigContacto → Notificaciones de reserva (bkg-wa-notif). A disabled
	// confirmation is not a failure, so callers keep reporting success.
	if cfg, err := s.loadBookingNotificationSettings(ctx, restaurantID); err == nil && !cfg.SendConfirmation {
		log.Printf("%s.confirmation.skipped restaurant=%d booking=%d", bookingNotifCoordinationID, restaurantID, bookingID)
		return nil
	}
	if rec, found, err := s.loadRestaurantUAZAPIInstance(ctx, restaurantID); err == nil && found && strings.EqualFold(rec.Provider, "evolution") {
		if _, refreshErr := s.refreshRestaurantUAZAPIConnectionStatus(ctx, restaurantID); refreshErr != nil {
			log.Printf("WhatsApp connection refresh failed for booking #%d: %v", bookingID, refreshErr)
		} else if refreshed, ok, loadErr := s.loadRestaurantUAZAPIInstance(ctx, restaurantID); loadErr == nil && ok && !isUAZAPIConnected(refreshed.Status) {
			return fmt.Errorf("WhatsApp desconectado (estado: %s)", refreshed.Status)
		}
	}

	gw, ok := s.botGatewayFor(ctx, restaurantID)
	if !ok {
		return fmt.Errorf("WhatsApp no configurado")
	}

	brandName := restaurantBrandNameOrDefault(ctx, s, restaurantID)
	baseURL := resolveRestaurantPublicBaseURL(ctx, s, restaurantID)

	msg, err := buildBookingWhatsAppButtonPayload(brandName, booking, bookingID, baseURL)
	if err != nil {
		return err
	}

	if err := s.sendWhatsAppMenuTracked(ctx, restaurantID, gw, msg.To, msg.Text, msg.Choices, "booking_confirmation"); err == nil {
		log.Printf("WhatsApp button confirmation sent for booking #%d", bookingID)
		return nil
	} else {
		log.Printf("WhatsApp button send failed for booking #%d (%v), falling back to text", bookingID, err)
	}

	sendErr := s.sendWhatsAppTextTracked(ctx, restaurantID, gw, msg.To, msg.Text, "booking_confirmation")
	if sendErr == nil {
		log.Printf("WhatsApp text confirmation sent for booking #%d", bookingID)
		return nil
	}

	// A confirmation the customer never receives is worse than a late one, so
	// hand it to the outbox before reporting the failure upstream.
	if qErr := s.enqueueWhatsAppDelivery(
		ctx, restaurantID, "booking_confirmation",
		fmt.Sprintf("booking_confirmation:%d:%d", restaurantID, bookingID),
		msg.To, whatsappOutboxPayload{Text: msg.Text, Choices: msg.Choices}, sendErr,
	); qErr != nil {
		log.Printf("WhatsApp outbox enqueue failed for booking #%d: %v", bookingID, qErr)
	}
	return fmt.Errorf("error enviando WhatsApp: %w", sendErr)
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
