package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"
)

// The same-day policy is enforced server-side (never left to the LLM): any
// attempt to create, modify or cancel a booking whose date is today is blocked
// and the customer is told to call the restaurant. This module is the single
// reusable source of truth for that policy.
//
// Coordination id: booking-same-day-guard.

// botSameDayMarker is embedded in the JSON tool result produced when a same-day
// operation is blocked. botProcessMessage uses it to avoid sending a duplicate
// fallback reply when the notice was already delivered server-side.
const botSameDayMarker = `"reason":"same_day"`

// botTodayISO returns the current date (YYYY-MM-DD) in the restaurant's
// timezone. Every same-day comparison uses this so a server running in UTC does
// not misclassify late-evening bookings as belonging to tomorrow.
func botTodayISO() string { return time.Now().In(boMadridTZ).Format("2006-01-02") }

// botIsSameDay reports whether an ISO date (YYYY-MM-DD) is today.
func botIsSameDay(dateISO string) bool {
	return strings.TrimSpace(dateISO) == botTodayISO()
}

// botSameDayNoticeText is the mandatory reply for same-day booking changes. It
// states the caller is an AI assistant and that same-day bookings must be
// handled by phone. The phone is appended when available so the number still
// reaches the customer even if the contact card cannot be delivered.
func botSameDayNoticeText(phone string) string {
	msg := "Hola 👋🏻\n" +
		"Soy un asistente de reservas generado con Inteligencia Artificial.\n" +
		"No puedo crear, modificar ni cancelar reservas para el día de hoy: para cualquier gestión de una reserva de hoy, llame directamente al restaurante."
	if p := strings.TrimSpace(phone); p != "" {
		msg += "\n📞 " + p
	}
	msg += "\nLe dejo la tarjeta de contacto del restaurante 👇"
	return msg
}

// botPhoneVariants returns the national and full digit forms of a phone so all
// booking lookups reuse the exact same comparison.
func botPhoneVariants(phone string) (national, digits string) {
	digits = digitsOnly(phone)
	national = digits
	if strings.HasPrefix(digits, "34") && len(digits) == 11 {
		national = digits[2:]
	}
	return national, digits
}

// botRestaurantPhone resolves the restaurant's phone from restaurant_info and,
// as a fallback, the restaurants table.
func (s *Server) botRestaurantPhone(ctx context.Context, restaurantID int) string {
	var phone sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT telefono FROM restaurant_info WHERE restaurant_id = ? LIMIT 1`, restaurantID).Scan(&phone); err == nil {
		if v := strings.TrimSpace(phone.String); v != "" {
			return v
		}
	}
	if err := s.db.QueryRowContext(ctx, `SELECT contact_phone FROM restaurants WHERE id = ? LIMIT 1`, restaurantID).Scan(&phone); err == nil {
		return strings.TrimSpace(phone.String)
	}
	return ""
}

// botContactDetails resolves the human-handoff contact (name + phone) used for
// the contact card. Precedence: tenant override, then restaurant data.
func (s *Server) botContactDetails(ctx context.Context, restaurantID int, tenant botTenantConfig) (name, phone string) {
	phone = strings.TrimSpace(tenant.ContactPhone)
	if phone == "" {
		phone = s.botRestaurantPhone(ctx, restaurantID)
	}
	name = strings.TrimSpace(tenant.ContactName)
	if name == "" {
		name = s.botBrandName(ctx, restaurantID)
	}
	return name, phone
}

// botSendContactCard delivers the restaurant/human-handoff contact card and
// records it. It is shared by the send_contact agent tool and by the same-day
// guard, so the card is described in exactly one place.
func (s *Server) botSendContactCard(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig) (string, error) {
	name, phone := s.botContactDetails(ctx, restaurantID, tenant)
	if phone == "" {
		return "", errors.New("el restaurante no tiene teléfono configurado")
	}
	gw, ok := s.botGatewayFor(ctx, restaurantID)
	if !ok {
		return "", errors.New("whatsapp no configurado")
	}
	brand := s.botBrandName(ctx, restaurantID)
	if err := gw.SendContact(ctx, msg.Sender, waContact{FullName: name, Phone: phone, Organization: brand}); err != nil {
		return "", err
	}
	s.botRecordConversationMessage(ctx, restaurantID, msg.Sender, "assistant", "Contacto: "+name+" "+phone, "send_contact", "agent")
	return phone, nil
}

// botOwnedBookingDate returns the ISO reservation date of a booking owned by
// phone. found=false when the booking does not exist for that phone, so callers
// never leak another customer's booking.
func (s *Server) botOwnedBookingDate(ctx context.Context, restaurantID int, bookingID int64, phone string) (string, bool, error) {
	national, digits := botPhoneVariants(phone)
	var dateISO string
	err := s.db.QueryRowContext(ctx, `
		SELECT DATE_FORMAT(reservation_date, '%Y-%m-%d')
		FROM bookings
		WHERE id = ? AND restaurant_id = ?
			AND (contact_phone = ? OR contact_phone = ? OR CONCAT(COALESCE(contact_phone_country_code,''), contact_phone) = ?)
		LIMIT 1
	`, bookingID, restaurantID, national, digits, digits).Scan(&dateISO)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return dateISO, true, nil
}

// botBlockSameDay communicates the mandatory same-day policy (text notice +
// contact card) and returns the JSON tool result. The requested mutation is
// never performed.
func (s *Server) botBlockSameDay(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig, operation string) string {
	_, phone := s.botContactDetails(ctx, restaurantID, tenant)
	log.Printf("[bot] checkpoint booking_same_day_blocked restaurant_id=%d operation=%s sender=%s date=%s", restaurantID, operation, msg.Sender, botTodayISO())

	noticeSent := false
	if gw, ok := s.botGatewayFor(ctx, restaurantID); ok {
		if err := s.sendWhatsAppTextTracked(ctx, restaurantID, gw, msg.Sender, botSameDayNoticeText(phone), "same_day_notice"); err == nil {
			noticeSent = true
		}
	}
	cardPhone, cardErr := s.botSendContactCard(ctx, restaurantID, msg, tenant)

	return botJSON(map[string]any{
		"blocked":           true,
		"reason":            "same_day",
		"operation":         operation,
		"notice_sent":       noticeSent,
		"contact_card_sent": cardErr == nil,
		"contact_phone":     cardPhone,
		"instruction":       "La operación para HOY no se ha realizado. Ya se ha avisado al cliente y se ha enviado la tarjeta de contacto. No envíes ningún mensaje adicional ni repitas la información.",
	})
}

// botSameDayOperation reports whether a create/modify/cancel request targets a
// booking for today and, when so, blocks it and returns the JSON result fed
// back to the model. Evaluation is done server-side so the policy cannot be
// bypassed by the LLM.
func (s *Server) botSameDayOperation(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig, name string, input json.RawMessage) (bool, string) {
	switch name {
	case "create_booking":
		var in struct {
			Date string `json:"date"`
		}
		if json.Unmarshal(input, &in) != nil {
			return false, ""
		}
		dateISO, err := parseBotDate(in.Date)
		if err != nil || !botIsSameDay(dateISO) {
			return false, ""
		}
		return true, s.botBlockSameDay(ctx, restaurantID, msg, tenant, "create_booking")

	case "modify_booking":
		var in struct {
			BookingID int64  `json:"booking_id"`
			Date      string `json:"date"`
		}
		if json.Unmarshal(input, &in) != nil {
			return false, ""
		}
		// Moving an existing booking to today is also a same-day operation.
		if dateISO, err := parseBotDate(in.Date); err == nil && botIsSameDay(dateISO) {
			return true, s.botBlockSameDay(ctx, restaurantID, msg, tenant, "modify_booking")
		}
		if in.BookingID <= 0 {
			return false, ""
		}
		dateISO, found, err := s.botOwnedBookingDate(ctx, restaurantID, in.BookingID, msg.Sender)
		if err != nil || !found || !botIsSameDay(dateISO) {
			return false, ""
		}
		return true, s.botBlockSameDay(ctx, restaurantID, msg, tenant, "modify_booking")

	case "cancel_booking":
		var in struct {
			BookingID int64 `json:"booking_id"`
		}
		if json.Unmarshal(input, &in) != nil || in.BookingID <= 0 {
			return false, ""
		}
		dateISO, found, err := s.botOwnedBookingDate(ctx, restaurantID, in.BookingID, msg.Sender)
		if err != nil || !found || !botIsSameDay(dateISO) {
			return false, ""
		}
		return true, s.botBlockSameDay(ctx, restaurantID, msg, tenant, "cancel_booking")
	}
	return false, ""
}
