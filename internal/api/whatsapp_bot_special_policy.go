package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"strconv"
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
	// FreeSeats / IsFull: day capacity of the date (wa_bot_special_date_full_v1).
	// FreeSeats is nil when the capacity could not be read.
	FreeSeats *int `json:"free_seats,omitempty"`
	IsFull    bool `json:"is_full"`
}

// botFits reports whether a party (0 = size unknown) still fits the date.
func (p *botSpecialDatePolicy) botFits(party int) bool {
	return !p.IsFull && (party <= 0 || p.FreeSeats == nil || party <= *p.FreeSeats)
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
			botCheckpoint("wa_bot_special_date_policy_v1", "restaurant_id=%d date=%s load_error=%v", restaurantID, dateISO, err)
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
	if limit, total, err := s.botDayCapacity(ctx, restaurantID, dateISO); err == nil {
		free := max(0, limit-total)
		p.FreeSeats, p.IsFull = &free, free == 0
	}
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
		People    int    `json:"people"`
	}
	if name != "create_booking" && name != "modify_booking" && name != "cancel_booking" {
		return false, ""
	}
	_ = json.Unmarshal(input, &in)

	if name == "create_booking" || (name == "modify_booking" && strings.TrimSpace(in.Date) != "") {
		if dateISO, err := parseBotDate(in.Date); err == nil {
			if p := s.botLoadSpecialDatePolicy(ctx, restaurantID, dateISO); p != nil {
				return true, s.botSendPrereservaLink(ctx, restaurantID, msg, tenant, p, name, in.People)
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

// botManagementHandoff forwards the issue to the management group and tells
// the customer (wa_bot_management_group_v1). text is kept as an optional
// explicit prefix; empty uses the per-reason default.
func (s *Server) botManagementHandoff(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig, text, reason, operation string) string {
	prefix := botManagementPrefixFor(reason, msg.Language)
	if prefix == "" {
		prefix = text
	}
	sent := s.botForwardToManagement(ctx, restaurantID, msg, tenant, reason, botOperationLabel(operation), prefix, msg.Language, s.botIsDuplicateRequest(msg))
	botCheckpoint("wa_bot_special_date_policy_v1", "restaurant_id=%d sender=%s reason=%s operation=%s forwarded=%t", restaurantID, msg.Sender, reason, operation, sent)
	return botJSON(map[string]any{
		"blocked": true, "reason": reason, "operation": operation, "forwarded": sent,
		"instruction": "La operación NO se ha realizado. La solicitud ya se ha enviado al equipo de gestión y el cliente ha sido avisado. No envíes ningún mensaje adicional.",
	})
}

// botSendPrereservaLink answers a create request for a special date with the
// website pre-reserva link (never creates the booking via WhatsApp). A full
// date gets the "completo" notice instead (wa_bot_special_date_full_v1).
func (s *Server) botSendPrereservaLink(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig, p *botSpecialDatePolicy, operation string, party int) string {
	reason, text := "special_date_web_only", botPrereservaLinkText(p)
	instruction := "NO se ha creado ninguna reserva: el cliente ya ha recibido el enlace de la web para esa fecha especial. No envíes otro mensaje salvo que tenga otra pregunta."
	if !p.botFits(party) {
		reason, text = "special_date_full", botSpecialDateFullText(p, party, msg.Language)
		instruction = "NO se ha creado ninguna reserva: la fecha especial está COMPLETA y el cliente ya ha sido avisado. No envíes otro mensaje salvo que tenga otra pregunta."
	}
	sent := false
	if gw, ok := s.botGatewayFor(ctx, restaurantID); ok {
		sent = s.sendWhatsAppTextTracked(ctx, restaurantID, gw, msg.Sender, text, "special_date_prereserva_link") == nil
	}
	botCheckpoint("wa_bot_special_date_policy_v1", "restaurant_id=%d sender=%s reason=%s operation=%s date=%s sent=%t", restaurantID, msg.Sender, reason, operation, p.Date, sent)
	return botJSON(map[string]any{
		"blocked": true, "reason": reason, "date": p.Date, "title": p.Title, "booking_url": p.BookingURL, "notice_sent": sent,
		"instruction": instruction,
	})
}

func botPrereservaLinkText(p *botSpecialDatePolicy) string {
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
		b.WriteString("\nPuedes hacerla desde el botón de abajo 👇\n" + p.BookingURL)
	}
	if p.PrereservaEnabled && !p.PrereservaOpenNow && p.PrereservaStartsOn != "" {
		b.WriteString("\nLa pre-reserva estará disponible desde el " + botFormatISODateES(p.PrereservaStartsOn) + ".")
	}
	return b.String()
}

// botSpecialDateFullText is the customer notice for a special date with no
// room left for the party (wa_bot_special_date_full_v1).
func botSpecialDateFullText(p *botSpecialDatePolicy, party int, lang string) string {
	if lang == "en" || lang == "other" {
		day := p.Date
		if t, err := time.Parse("2006-01-02", p.Date); err == nil {
			day = t.Format("Monday, 2 January 2006")
		}
		return "Sorry, *" + day + "* (*" + p.Title + "*) is fully booked: there are no seats left for that date. If you like, I can help you find another day 😊"
	}
	full := "ya está completo: no nos quedan plazas para esa fecha"
	if !p.IsFull && party > 0 {
		full = "ya no nos quedan plazas para " + strconv.Itoa(party) + " personas"
	}
	return "Lo sentimos, el *" + botFormatISODateES(p.Date) + "* (*" + p.Title + "*) " + full + ". Si quieres, te ayudo a buscar otro día 😊"
}

// botSpecialDateFullInstruction is the tool/route instruction for a special
// date with no room left (wa_bot_special_date_full_v1). Prod 2026-10-30: the
// tools said free_seats=0 for the Comida de Navidad (176/125 pax) but every
// instruction only said "give the booking link" and the bot answered "Sí".
func botSpecialDateFullInstruction(p *botSpecialDatePolicy) string {
	return "FECHA ESPECIAL COMPLETA: " + p.Title + " (" + botFormatISODateES(p.Date) + ") no tiene plazas libres para este cliente. Díselo claramente desde la primera frase: NUNCA digas que hay sitio ni que puede reservar o pre-reservar esa fecha, y NO envíes el enlace de reserva. Ofrécele buscar otra fecha (compruébala con get_date_overview)."
}

// botSpecialDateRouteDirective adapts the pipeline directive of the special
// date route to the real capacity: the pipeline always asks for the booking
// link, even when the date is full (wa_bot_special_date_full_v1).
func (s *Server) botSpecialDateRouteDirective(ctx context.Context, restaurantID int, d botPipelineDecision) string {
	if d.Node != "agent_special_date" || d.SpecialDate == "" {
		return d.Directive
	}
	p := s.botLoadSpecialDatePolicy(ctx, restaurantID, d.SpecialDate)
	if p == nil {
		return d.Directive
	}
	if p.IsFull {
		botCheckpoint("wa_bot_special_date_full_v1", "restaurant_id=%d date=%s directive=full", restaurantID, p.Date)
		return botSpecialDateFullInstruction(p)
	}
	return d.Directive + " Si get_date_overview devuelve fits_party=false, no hay sitio para ese grupo: dilo claramente y no envíes el enlace."
}

// botEnsureSpecialDateLink guarantees the booking link on the special date
// route (wa_bot_special_date_link_guard_v1): the directive asks the model for
// it, but prod 2026-10-30 answered without it. When nothing delivered this
// turn carried the booking URL, the server sends it as a button. Skipped when
// the date does not fit the party, the pre-reserva is not open yet or a
// server notice already answered.
func (s *Server) botEnsureSpecialDateLink(ctx context.Context, restaurantID int, msg botWebhookMessage, d botPipelineDecision, turn *botTurnState, replyText string) {
	if d.Node != "agent_special_date" || d.SpecialDate == "" || turn.noticeDelivered || turn.noFit {
		return
	}
	p := s.botLoadSpecialDatePolicy(ctx, restaurantID, d.SpecialDate)
	if p == nil || p.BookingURL == "" || !p.botFits(0) || (p.PrereservaEnabled && !p.PrereservaOpenNow) {
		return
	}
	base, _, _ := strings.Cut(p.BookingURL, "?")
	if strings.Contains(turn.delivered+replyText, base) {
		return
	}
	es := "Puedes hacer la reserva desde el botón de abajo 👇"
	if p.PrereservaEnabled {
		es = "Puedes hacer la pre-reserva desde el botón de abajo 👇"
	}
	text := botLocalizedHandoff(es, "You can book it from the button below 👇", msg.Language) + "\n" + p.BookingURL
	sent := false
	if gw, ok := s.botGatewayFor(ctx, restaurantID); ok {
		sent = s.sendWhatsAppTextTracked(ctx, restaurantID, gw, msg.Sender, text, "special_date_link_guard") == nil
	}
	botCheckpoint("wa_bot_special_date_link_guard_v1", "restaurant_id=%d sender=%s date=%s sent=%t", restaurantID, msg.Sender, p.Date, sent)
}

func botFormatISODateES(iso string) string {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	return botFormatSpanishDate(t)
}
