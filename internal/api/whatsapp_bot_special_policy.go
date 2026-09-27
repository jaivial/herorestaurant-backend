package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/url"
	"strings"
	"time"
)

// Coordination id: wa_bot_special_date_policy_v1
//
// Server-side policy for special dates (e.g. "Comida de Navidad"), enforced
// before any booking mutation tool runs, so the LLM cannot bypass it:
//   - a special date with pre-reserva enabled: the bot never creates the
//     booking, it sends the website pre-reserva link for that date
//     (<website>/reservas?date=YYYY-MM-DD), even when there are free seats;
//   - an existing booking on a special date: the bot never modifies or
//     cancels it, it hands over to restaurant management (notice + card).
// Event bookings (bookings.is_event, booking_is_event_v1) get the same
// management handoff for any modification/cancellation.

type botSpecialDatePolicy struct {
	ID                        int64    `json:"special_date_id"`
	Date                      string   `json:"date"`
	Title                     string   `json:"title"`
	Description               string   `json:"description,omitempty"`
	PrereservaEnabled         bool     `json:"prereserva_enabled"`
	PrereservaStartsOn        string   `json:"prereserva_starts_on,omitempty"`
	PrereservaEndsOn          string   `json:"prereserva_ends_on,omitempty"`
	PrereservaOpenNow         bool     `json:"prereserva_open_now"`
	RequiresAdelanto          bool     `json:"requires_adelanto"`
	AllowCustomerModification bool     `json:"allow_customer_modification"`
	MaxPerTable               int      `json:"max_per_table,omitempty"`
	MobilityEnabled           bool     `json:"mobility_enabled"`
	BookingURL                string   `json:"booking_url,omitempty"`
	MenuTitles                []string `json:"menu_titles,omitempty"`
}

// botLoadSpecialDatePolicy returns the active special date for dateISO or nil.
func (s *Server) botLoadSpecialDatePolicy(ctx context.Context, restaurantID int, dateISO string) *botSpecialDatePolicy {
	var (
		p                                         botSpecialDatePolicy
		desc                                      sql.NullString
		pre, adel, allowMod, maxEnabled, mobility int
		maxPer                                    sql.NullInt64
		startsOn, endsOn                          sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, DATE_FORMAT(date,'%Y-%m-%d'), title, description, prereserva_enabled,
		       DATE_FORMAT(prereserva_starts_on,'%Y-%m-%d'), DATE_FORMAT(prereserva_ends_on,'%Y-%m-%d'),
		       requires_adelanto, allow_customer_modification, max_per_table_enabled, max_per_table, mobility_enabled
		FROM special_dates WHERE restaurant_id = ? AND date = ? AND is_active = 1 LIMIT 1`, restaurantID, dateISO).Scan(
		&p.ID, &p.Date, &p.Title, &desc, &pre, &startsOn, &endsOn, &adel, &allowMod, &maxEnabled, &maxPer, &mobility)
	if err != nil {
		if err != sql.ErrNoRows && !isSQLSchemaError(err) {
			log.Printf("[bot] checkpoint wa_bot_special_date_policy_v1 restaurant_id=%d date=%s load_error=%v", restaurantID, dateISO, err)
		}
		return nil
	}
	p.Description = strings.TrimSpace(desc.String)
	p.PrereservaEnabled, p.RequiresAdelanto, p.AllowCustomerModification, p.MobilityEnabled = pre != 0, adel != 0, allowMod != 0, mobility != 0
	p.PrereservaStartsOn, p.PrereservaEndsOn = startsOn.String, endsOn.String
	if maxEnabled != 0 && maxPer.Valid {
		p.MaxPerTable = int(maxPer.Int64)
	}
	today := time.Now().In(boMadridTZ).Format("2006-01-02")
	p.PrereservaOpenNow = p.PrereservaEnabled && (p.PrereservaStartsOn == "" || today >= p.PrereservaStartsOn) && (p.PrereservaEndsOn == "" || today <= p.PrereservaEndsOn)
	p.BookingURL = s.botSpecialBookingURL(ctx, restaurantID, dateISO)
	if rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(NULLIF(sdm.custom_title,''), m.menu_title, '')
		FROM special_date_menus sdm LEFT JOIN menus m ON m.id = sdm.menu_id AND m.restaurant_id = sdm.restaurant_id
		WHERE sdm.restaurant_id = ? AND sdm.special_date_id = ? ORDER BY sdm.position, sdm.id`, restaurantID, p.ID); err == nil {
		defer rows.Close()
		for rows.Next() {
			var t string
			if rows.Scan(&t) == nil && strings.TrimSpace(t) != "" {
				p.MenuTitles = append(p.MenuTitles, strings.TrimSpace(t))
			}
		}
	}
	return &p
}

// botSpecialBookingURL is <website>/reservas?date=YYYY-MM-DD (empty when the
// restaurant has no published website).
func (s *Server) botSpecialBookingURL(ctx context.Context, restaurantID int, dateISO string) string {
	website := ""
	if branding, err := s.loadRestaurantBranding(ctx, restaurantID); err == nil {
		website = branding.Website
	}
	base := botBookingURL(website)
	if base == "" {
		return ""
	}
	return base + "?date=" + url.QueryEscape(dateISO)
}

// botUpcomingSpecialDates lists active special dates from today on (for the
// pipeline "which special date is the customer talking about" question).
func (s *Server) botUpcomingSpecialDates(ctx context.Context, restaurantID int) []botSpecialDatePolicy {
	rows, err := s.db.QueryContext(ctx, `SELECT DATE_FORMAT(date,'%Y-%m-%d') FROM special_dates WHERE restaurant_id = ? AND is_active = 1 AND date >= CURDATE() ORDER BY date LIMIT 12`, restaurantID)
	if err != nil {
		return nil
	}
	var dates []string
	for rows.Next() {
		var d string
		if rows.Scan(&d) == nil {
			dates = append(dates, d)
		}
	}
	rows.Close()
	out := make([]botSpecialDatePolicy, 0, len(dates))
	for _, d := range dates {
		if p := s.botLoadSpecialDatePolicy(ctx, restaurantID, d); p != nil {
			out = append(out, *p)
		}
	}
	return out
}

// botOwnedBookingFlags returns (date, is_event, is_special_booking, found).
func (s *Server) botOwnedBookingFlags(ctx context.Context, restaurantID int, bookingID int64, phone string) (string, bool, bool, bool) {
	national, digits := botPhoneVariants(phone)
	var date string
	var isEvent, isSpecial int
	err := s.db.QueryRowContext(ctx, `
		SELECT DATE_FORMAT(reservation_date,'%Y-%m-%d'), COALESCE(is_event,0), COALESCE(is_special_booking,0)
		FROM bookings WHERE restaurant_id = ? AND id = ?
		  AND (contact_phone = ? OR contact_phone = ? OR CONCAT(COALESCE(contact_phone_country_code,''), contact_phone) = ?)`,
		restaurantID, bookingID, national, digits, digits).Scan(&date, &isEvent, &isSpecial)
	if err != nil {
		return "", false, false, false
	}
	return date, isEvent != 0, isSpecial != 0, true
}

// botSpecialPolicyOperation blocks create/modify/cancel that the special-date
// or event policy forbids. It returns the JSON fed back to the model.
func (s *Server) botSpecialPolicyOperation(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig, name string, input json.RawMessage) (bool, string) {
	var in struct {
		BookingID int64  `json:"booking_id"`
		Date      string `json:"date"`
	}
	if name != "create_booking" && name != "modify_booking" && name != "cancel_booking" {
		return false, ""
	}
	_ = json.Unmarshal(input, &in)

	if name == "create_booking" || (name == "modify_booking" && strings.TrimSpace(in.Date) != "") {
		if dateISO, err := parseBotDate(in.Date); err == nil {
			if p := s.botLoadSpecialDatePolicy(ctx, restaurantID, dateISO); p != nil {
				return true, s.botSendPrereservaLink(ctx, restaurantID, msg, tenant, p, name)
			}
		}
	}
	if (name == "modify_booking" || name == "cancel_booking") && in.BookingID > 0 {
		date, isEvent, isSpecial, found := s.botOwnedBookingFlags(ctx, restaurantID, in.BookingID, msg.Sender)
		if found {
			if isEvent {
				return true, s.botManagementHandoff(ctx, restaurantID, msg, tenant, botEventHandoffText, "event_booking", name)
			}
			if isSpecial || s.botLoadSpecialDatePolicy(ctx, restaurantID, date) != nil {
				return true, s.botManagementHandoff(ctx, restaurantID, msg, tenant, botSpecialBookingHandoffText, "special_date_booking", name)
			}
		}
	}
	return false, ""
}

const botEventHandoffText = "Para el caso de vuestra reserva, como se trata de una reserva especial, os recomiendo acordar los detalles directamente con la gestión del restaurante llamando o escribiendo al número de teléfono que os dejo a continuación 👇"

const botSpecialBookingHandoffText = "Vuestra reserva es para una fecha especial y, como asistente de reservas con Inteligencia Artificial, no puedo modificarla ni cancelarla. Para cualquier cambio, contactad directamente con la gestión del restaurante en el teléfono que os dejo a continuación 👇"

const botEventHandoffTextEN = "As yours is a special booking, I recommend agreeing the details directly with the restaurant management by calling or writing to the phone number below 👇"

const botSpecialBookingHandoffTextEN = "Your booking is for a special date and, as an AI booking assistant, I can't change or cancel it. For any change, please contact the restaurant management at the phone number below 👇"

// botLocalizedHandoff picks the English text for non-Spanish customers
// (wa_bot_language_v1).
func botLocalizedHandoff(es, en, lang string) string {
	if lang == "en" || lang == "other" {
		return en
	}
	return es
}

// botManagementHandoff sends a fixed notice + the management contact card.
func (s *Server) botManagementHandoff(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig, text, reason, operation string) string {
	name, phone := s.botSameDayContactDetails(ctx, restaurantID, tenant)
	noticeSent := false
	if gw, ok := s.botGatewayFor(ctx, restaurantID); ok {
		noticeSent = s.sendWhatsAppTextTracked(ctx, restaurantID, gw, msg.Sender, text, "management_handoff_notice") == nil
	}
	cardPhone, cardErr := s.botSendContactCardWith(ctx, restaurantID, msg, name, phone)
	log.Printf("[bot] checkpoint wa_bot_special_date_policy_v1 restaurant_id=%d sender=%s reason=%s operation=%s notice_sent=%t card_sent=%t",
		restaurantID, msg.Sender, reason, operation, noticeSent, cardErr == nil)
	return botJSON(map[string]any{
		"blocked": true, "reason": reason, "operation": operation, "notice_sent": noticeSent,
		"contact_card_sent": cardErr == nil, "contact_phone": cardPhone,
		"instruction": "La operación NO se ha realizado. Ya se ha avisado al cliente y se ha enviado la tarjeta de contacto de gestión. No envíes ningún mensaje adicional.",
	})
}

// botSendPrereservaLink answers a create request for a special date with the
// website pre-reserva link (never creates the booking via WhatsApp).
func (s *Server) botSendPrereservaLink(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig, p *botSpecialDatePolicy, operation string) string {
	var b strings.Builder
	b.WriteString("El *" + botFormatISODateES(p.Date) + "* es una fecha especial: *" + p.Title + "*.\n")
	if p.PrereservaEnabled {
		b.WriteString("Para esta fecha las reservas se hacen como *pre-reserva* desde nuestra web")
		if p.RequiresAdelanto {
			b.WriteString(" (requiere un adelanto)")
		}
		b.WriteString(", no por WhatsApp.")
	} else {
		b.WriteString("Las reservas de esta fecha se gestionan desde nuestra web, no por WhatsApp.")
	}
	if p.BookingURL != "" {
		b.WriteString("\nPuedes hacerla aquí: " + p.BookingURL)
	}
	if p.PrereservaEnabled && !p.PrereservaOpenNow && p.PrereservaStartsOn != "" {
		b.WriteString("\nLa pre-reserva estará disponible desde el " + botFormatISODateES(p.PrereservaStartsOn) + ".")
	}
	sent := false
	if gw, ok := s.botGatewayFor(ctx, restaurantID); ok {
		sent = s.sendWhatsAppTextTracked(ctx, restaurantID, gw, msg.Sender, b.String(), "special_date_prereserva_link") == nil
	}
	log.Printf("[bot] checkpoint wa_bot_special_date_policy_v1 restaurant_id=%d sender=%s reason=prereserva_web operation=%s date=%s sent=%t", restaurantID, msg.Sender, operation, p.Date, sent)
	return botJSON(map[string]any{
		"blocked": true, "reason": "special_date_web_only", "date": p.Date, "title": p.Title, "booking_url": p.BookingURL, "notice_sent": sent,
		"instruction": "NO se ha creado ninguna reserva: el cliente ya ha recibido el enlace de la web para esa fecha especial. No envíes otro mensaje salvo que tenga otra pregunta.",
	})
}

func botFormatISODateES(iso string) string {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	return botFormatSpanishDate(t)
}
