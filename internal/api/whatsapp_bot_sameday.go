package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
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
		msg += "\n📞 " + botFormatPhoneDisplay(p)
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

// botRestaurantPhone resolves the restaurant's PUBLIC phone: restaurant_info
// (authored from /app/config?content=contacto) and then the restaurants table.
// The provisioned WhatsApp instance's own connected number is deliberately NOT
// a fallback: it is the bot, not a person, so handing it over as "call this
// number" tells the customer to call a machine. An empty result means the
// restaurant has not published a phone and callers must stay generic.
func (s *Server) botRestaurantPhone(ctx context.Context, restaurantID int) string {
	if branding, err := s.loadRestaurantBranding(ctx, restaurantID); err == nil {
		if v := strings.TrimSpace(branding.Phone); v != "" {
			return v
		}
	}
	var phone sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT contact_phone FROM restaurants WHERE id = ? LIMIT 1`, restaurantID).Scan(&phone); err == nil {
		return strings.TrimSpace(phone.String)
	}
	return ""
}

// botManagementPhone resolves the restaurant's MANAGEMENT phone (the number a
// human answers during service) authored per restaurant from
// /app/config?content=contacto. Empty when the restaurant has not published one.
func (s *Server) botManagementPhone(ctx context.Context, restaurantID int) string {
	branding, err := s.loadRestaurantBranding(ctx, restaurantID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(branding.ManagementPhone)
}

// botContactDetails resolves the human-handoff contact (name + phone) used for
// the contact card. Precedence: tenant override, the restaurant's management
// phone, then its public phone.
func (s *Server) botContactDetails(ctx context.Context, restaurantID int, tenant botTenantConfig) (name, phone string) {
	phone = firstNonEmpty(
		strings.TrimSpace(tenant.ContactPhone),
		s.botManagementPhone(ctx, restaurantID),
		s.botRestaurantPhone(ctx, restaurantID),
	)
	name = strings.TrimSpace(tenant.ContactName)
	if name == "" {
		name = s.botBrandName(ctx, restaurantID)
	}
	return name, phone
}

// botSameDayContactDetails resolves the human-handoff contact used when a
// same-day operation has to be refused. Precedence: tenant same-day override,
// global same-day phone (BOT_SAME_DAY_CONTACT_PHONE), the restaurant's own
// management phone (telefono_gestion, authored from /app/config?content=contacto),
// tenant contact override, restaurant public phone. It stays separate from
// botContactDetails because the number that answers during service is an
// operational decision per tenant, not a generic brand attribute.
func (s *Server) botSameDayContactDetails(ctx context.Context, restaurantID int, tenant botTenantConfig) (name, phone string) {
	phone = firstNonEmpty(
		strings.TrimSpace(tenant.SameDayContactPhone),
		strings.TrimSpace(s.cfg.BotSameDayContactPhone),
		s.botManagementPhone(ctx, restaurantID),
		strings.TrimSpace(tenant.ContactPhone),
		s.botRestaurantPhone(ctx, restaurantID),
	)
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
	return s.botSendContactCardWith(ctx, restaurantID, msg, name, phone)
}

// botSendContactCardWith delivers the contact card for already-resolved contact
// details, so callers that must pin a specific handoff number (same-day guard)
// and callers that resolve it from the tenant (send_contact tool) share one
// delivery + recording path.
func (s *Server) botSendContactCardWith(ctx context.Context, restaurantID int, msg botWebhookMessage, name, phone string) (string, error) {
	phone = digitsOnly(phone)
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
	name, phone := s.botSameDayContactDetails(ctx, restaurantID, tenant)
	log.Printf("[bot] checkpoint booking_same_day_blocked restaurant_id=%d operation=%s sender=%s date=%s", restaurantID, operation, msg.Sender, botTodayISO())

	noticeSent := false
	if gw, ok := s.botGatewayFor(ctx, restaurantID); ok {
		if err := s.sendWhatsAppTextTracked(ctx, restaurantID, gw, msg.Sender, botSameDayNoticeText(phone), "same_day_notice"); err == nil {
			noticeSent = true
		}
	}
	cardPhone, cardErr := s.botSendContactCardWith(ctx, restaurantID, msg, name, phone)
	if cardErr != nil {
		log.Printf("[bot] restaurant=%d same-day contact card failed: %v", restaurantID, cardErr)
	}

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

// botBookingIntentFromText classifies a customer message into the booking
// operation it most plausibly requests. It is deliberately conservative: only
// explicit verbs count, and the more destructive operation wins (cancel >
// modify > create) so "quiero cancelar la reserva" is never read as a booking.
// An empty result means the conversational agent decides.
func botBookingIntentFromText(text string) string {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return ""
	}
	hasAny := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(t, w) {
				return true
			}
		}
		return false
	}
	switch {
	case hasAny("cancelar", "anular", "eliminar", "borrar", "cancel", "delete", "quitar la reserva"):
		return "cancel_booking"
	case hasAny("modificar", "cambiar", "mover", "aplazar", "corregir", "modify", "change", "reschedule"):
		return "modify_booking"
	case hasAny("reservar", "reserva", "reservación", "mesa para", "book", "booking", "reservation"):
		return "create_booking"
	}
	return ""
}

// botTextMentionsToday reports whether the message explicitly points at today,
// so a create request is only pre-empted when the customer is unambiguous.
func botTextMentionsToday(text string) bool {
	t := strings.ToLower(text)
	for _, w := range []string{"hoy", "esta noche", "esta mediodía", "esta mediodia", "tonight", "today"} {
		if strings.Contains(t, w) {
			return true
		}
	}
	return false
}

// botClockTimeRe captures explicit clock times: 14:00, 14.30, 14h, "a las 14".
var botClockTimeRe = regexp.MustCompile(`\b(?:a\s+las?\s+)?([01]?\d|2[0-3])(?:[:h.](\d{2}))?\s*(?:h\b|hs\b|horas?\b)?`)

// botTextClockTimes extracts the explicit 24h clock times written in a message.
// It only accepts clearly time-shaped tokens so party sizes or phone numbers are
// never read as a time.
func botTextClockTimes(text string) []string {
	t := strings.ToLower(text)
	evening := strings.Contains(t, "tarde") || strings.Contains(t, "noche")
	var out []string
	for _, m := range botClockTimeRe.FindAllStringSubmatch(t, -1) {
		hour, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		minutes := 0
		if m[2] != "" {
			minutes, _ = strconv.Atoi(m[2])
		}
		shaped := m[2] != "" || strings.Contains(m[0], "h") || strings.Contains(m[0], "a las") || strings.Contains(m[0], "a la")
		if !shaped {
			continue
		}
		if evening && hour < 12 {
			hour += 12
		}
		out = append(out, fmt.Sprintf("%02d:%02d", hour, minutes))
	}
	return out
}

// botSenderUpcomingBookingStats returns whether the sender owns a booking for
// today and how many upcoming bookings they have, using the same phone
// normalization as every other booking lookup.
func (s *Server) botSenderUpcomingBookingStats(ctx context.Context, restaurantID int, phone string) (today bool, upcoming int) {
	national, digits := botPhoneVariants(phone)
	rows, err := s.db.QueryContext(ctx, `
		SELECT DATE_FORMAT(reservation_date, '%Y-%m-%d')
		FROM bookings
		WHERE restaurant_id = ? AND reservation_date >= ?
			AND (contact_phone = ? OR contact_phone = ? OR CONCAT(COALESCE(contact_phone_country_code,''), contact_phone) = ?)
	`, restaurantID, botTodayISO(), national, digits, digits)
	if err != nil {
		return false, 0
	}
	defer rows.Close()
	todayISO := botTodayISO()
	for rows.Next() {
		var dateISO string
		if rows.Scan(&dateISO) != nil {
			continue
		}
		upcoming++
		if dateISO == todayISO {
			today = true
		}
	}
	return today, upcoming
}

// botSenderTodayBookingTime returns the HH:MM time of the sender's booking for
// today (empty when there is none).
func (s *Server) botSenderTodayBookingTime(ctx context.Context, restaurantID int, phone string) string {
	national, digits := botPhoneVariants(phone)
	var timeHHMM string
	err := s.db.QueryRowContext(ctx, `
		SELECT TIME_FORMAT(reservation_time, '%H:%i')
		FROM bookings
		WHERE restaurant_id = ? AND reservation_date = ?
			AND (contact_phone = ? OR contact_phone = ? OR CONCAT(COALESCE(contact_phone_country_code,''), contact_phone) = ?)
		ORDER BY reservation_time ASC LIMIT 1
	`, restaurantID, botTodayISO(), national, digits, digits).Scan(&timeHHMM)
	if err != nil {
		return ""
	}
	return timeHHMM
}

// botArrivalToleranceMinutes is how far an arrival estimate may drift from the
// booked time ("acudiré sobre las 14:00 - 14:10") before it reads as a change
// of plans instead of a confirmation.
const botArrivalToleranceMinutes = 30

// botMinutesOfDay parses HH:MM into minutes since midnight (-1 when unparsable).
func botMinutesOfDay(hhmm string) int {
	parts := strings.Split(strings.TrimSpace(hhmm), ":")
	if len(parts) != 2 {
		return -1
	}
	h, errH := strconv.Atoi(parts[0])
	m, errM := strconv.Atoi(parts[1])
	if errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return -1
	}
	return h*60 + m
}

// botIsArrivalEstimate reports whether every explicit time in the message sits
// within tolerance of the booked time, i.e. the customer is narrowing an arrival
// window ("sobre las 14:00 - 14:10") rather than asking for a new time. Only the
// no-verb branch uses it: explicit modify/cancel verbs are still blocked.
func botIsArrivalEstimate(times []string, bookingTime string) bool {
	booking := botMinutesOfDay(bookingTime)
	if booking < 0 || len(times) == 0 {
		return false
	}
	for _, t := range times {
		minutes := botMinutesOfDay(t)
		if minutes < 0 || absInt(minutes-booking) > botArrivalToleranceMinutes {
			return false
		}
	}
	return true
}

// botAttendancePhrases are confirmation idioms: they state the customer is
// coming, never that the booking must change.
var botAttendancePhrases = []string{
	"acudiré", "acudire", "acudirá", "llegaré", "llegare", "llegaremos",
	"iremos", "iré", "estaremos", "nos vemos", "hasta ahora", "allí estaremos",
	"confirmo", "confirmada", "confirmado", "ya he confirmado", "gracias",
}

// botTextLooksLikeAttendanceConfirmation reports whether the message reads as a
// courteous confirmation. It only ever skips the inferred-modification branch:
// a message that also carries an explicit modify/cancel verb is handled by the
// intent branch above and stays blocked.
func botTextLooksLikeAttendanceConfirmation(text string) bool {
	t := strings.ToLower(text)
	if botBookingIntentFromText(t) == "modify_booking" || botBookingIntentFromText(t) == "cancel_booking" {
		return false
	}
	for _, phrase := range botAttendancePhrases {
		if strings.Contains(t, phrase) {
			return true
		}
	}
	return false
}

// botSameDayIntentGuard enforces the same-day policy from the raw inbound text,
// before the model can ask any clarifying question. When the customer requests a
// create/modify/cancel that targets today, the AI notice and the contact card
// are delivered immediately and the agent turn is skipped. Returns true when the
// message was handled here.
func (s *Server) botSameDayIntentGuard(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig) bool {
	intent := botBookingIntentFromText(msg.Text)
	today, upcoming := s.botSenderUpcomingBookingStats(ctx, restaurantID, msg.Sender)
	mentionsToday := botTextMentionsToday(msg.Text)

	// A brand-new reservation for today is blocked even with no existing booking.
	if !today {
		if intent == "create_booking" && mentionsToday {
			return s.botBlockSameDayIntent(ctx, restaurantID, msg, tenant, "create_booking")
		}
		return false
	}

	// The customer already owns a booking for today. Only act when it is their
	// sole upcoming booking or when they explicitly mention today, so a future
	// booking request is never mistaken for a same-day operation.
	if upcoming != 1 && !mentionsToday {
		return false
	}

	if intent == "modify_booking" || intent == "cancel_booking" {
		return s.botBlockSameDayIntent(ctx, restaurantID, msg, tenant, intent)
	}
	if intent == "create_booking" && mentionsToday {
		return s.botBlockSameDayIntent(ctx, restaurantID, msg, tenant, "create_booking")
	}

	// No explicit operation verb: a correction that states a different time
	// ("la reserva la he hecho para las 14h") is still a same-day modification.
	// A courtesy confirmation is not: "gracias, acudiré sobre las 14:00 - 14:10"
	// only narrows the arrival window, so blocking it would push a customer with
	// a perfectly valid booking to call the restaurant.
	if bookingTime := s.botSenderTodayBookingTime(ctx, restaurantID, msg.Sender); bookingTime != "" {
		times := botTextClockTimes(msg.Text)
		if botTextLooksLikeAttendanceConfirmation(msg.Text) || botIsArrivalEstimate(times, bookingTime) {
			return false
		}
		for _, t := range times {
			if t != bookingTime {
				return s.botBlockSameDayIntent(ctx, restaurantID, msg, tenant, "modify_booking")
			}
		}
	}
	return false
}

// botBlockSameDayIntent logs the coordination checkpoint and delivers the
// shared same-day notice + contact card.
func (s *Server) botBlockSameDayIntent(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig, intent string) bool {
	log.Printf("[bot] checkpoint booking_same_day_intent_blocked restaurant_id=%d sender=%s intent=%s date=%s", restaurantID, msg.Sender, intent, botTodayISO())
	s.botBlockSameDay(ctx, restaurantID, msg, tenant, intent)
	return true
}
