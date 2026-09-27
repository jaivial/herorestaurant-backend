package api

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"
)

// Coordination id: wa_bot_tools_v2
//
// Tools added after the booking-schema review:
//   - get_date_overview: one call with everything the bot needs about a date
//     (open/closed, hours, capacity, special-date settings + linked menus,
//     pre-reserva window + website link, mobility question, same-day flag).
//   - get_booking_details: full record of one of the customer's bookings,
//     including staff commentary, event flag and special-date status.
//   - add_booking_note: appends a note (allergy, celebration, mobility...) to
//     the commentary of a normal booking, never overwriting staff notes.

func botToolDefsV2() []botToolDef {
	return []botToolDef{
		{
			Name:        "get_date_overview",
			Description: "Resumen completo de una fecha: si abre, horas disponibles, plazas libres, si es FECHA ESPECIAL (título, pre-reserva, adelanto, menús vinculados, enlace web de reserva) y si pregunta por movilidad reducida. Úsalo SIEMPRE antes de hablar de disponibilidad o reservar para una fecha.",
			InputSchema: botSchema(`{"type":"object","properties":{"date":{"type":"string","description":"Fecha YYYY-MM-DD o dd/MM/yyyy"},"party_size":{"type":"integer","description":"Personas (opcional)"}},"required":["date"]}`),
		},
		{
			Name:        "get_booking_details",
			Description: "Detalle completo de una reserva del cliente (por booking_id de get_bookings): comentarios del personal, si es reserva de EVENTO, si es de fecha especial o pre-reserva, arroz, tronas, extras.",
			InputSchema: botSchema(`{"type":"object","properties":{"booking_id":{"type":"integer"}},"required":["booking_id"]}`),
		},
		{
			Name:        "add_booking_note",
			Description: "Añade una nota a los comentarios de una reserva normal del cliente (alergias, intolerancias, celebración, movilidad, bebé). No borra los comentarios existentes. Requiere confirmed=true tras confirmarlo con el cliente.",
			InputSchema: botSchema(`{"type":"object","properties":{"booking_id":{"type":"integer"},"note":{"type":"string","description":"Texto breve de la nota"},"confirmed":{"type":"boolean"}},"required":["booking_id","note","confirmed"]}`),
		},
	}
}

func (s *Server) botToolDateOverview(ctx context.Context, restaurantID int, input json.RawMessage) (string, error) {
	var in struct {
		Date      string `json:"date"`
		PartySize int    `json:"party_size"`
	}
	_ = json.Unmarshal(input, &in)
	dateISO, err := parseBotDate(in.Date)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	out := map[string]any{"date": dateISO, "date_es": botFormatISODateES(dateISO), "is_today": botIsSameDay(dateISO)}
	if t, err := time.Parse("2006-01-02", dateISO); err == nil {
		out["is_past"] = t.Format("2006-01-02") < botTodayISO()
	}
	if sched, err := s.botResolveDaySchedule(ctx, restaurantID, dateISO); err == nil {
		out["open"] = sched.Open
		out["weekday"] = sched.Weekday
		out["morning_hours"] = sched.MorningHours
		out["night_hours"] = sched.NightHours
		out["has_schedule_override"] = sched.HasOverride
	}
	if limit, total, err := s.botDayCapacity(ctx, restaurantID, dateISO); err == nil {
		free := max(0, limit-total)
		out["daily_limit"], out["free_seats"] = limit, free
		if in.PartySize > 0 {
			out["fits_party"] = in.PartySize <= free
		}
	}
	if p := s.botLoadSpecialDatePolicy(ctx, restaurantID, dateISO); p != nil {
		out["is_special_date"] = true
		out["special_date"] = p
		out["whatsapp_booking_allowed"] = false
		out["instruction"] = "Fecha especial: NO se puede reservar por WhatsApp. Da el enlace booking_url para reservar/pre-reservar en la web. Si el cliente ya tiene reserva ese día, no se puede modificar ni cancelar por aquí: gestión del restaurante."
		if raw, err := s.botToolGetSpecialDateInfo(ctx, restaurantID, json.RawMessage(`{"date":"`+dateISO+`"}`)); err == nil {
			var menus map[string]any
			if json.Unmarshal([]byte(raw), &menus) == nil {
				out["special_menus"] = menus["menus"]
			}
		}
	} else {
		out["is_special_date"] = false
		out["whatsapp_booking_allowed"] = !botIsSameDay(dateISO)
	}
	var mobility int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(mobility_enabled,0) FROM mobility_day_override WHERE restaurant_id = ? AND reservationDate = ? LIMIT 1`, restaurantID, dateISO).Scan(&mobility); err == nil {
		out["asks_mobility"] = mobility != 0
	}
	return botJSON(out), nil
}

func (s *Server) botToolBookingDetails(ctx context.Context, restaurantID int, phone string, input json.RawMessage) (string, error) {
	var in struct {
		BookingID int64 `json:"booking_id"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.BookingID <= 0 {
		return botJSON(map[string]any{"error": "booking_id inválido"}), nil
	}
	bookings, err := s.botFindBookings(ctx, restaurantID, phone)
	if err != nil {
		return botJSON(map[string]any{"error": "error consultando la reserva"}), nil
	}
	for _, b := range bookings {
		if b.ID != in.BookingID {
			continue
		}
		out := map[string]any{"booking": b, "commentary_signals": botCommentarySignals(b.Commentary)}
		if b.IsEvent {
			out["instruction"] = "Reserva de EVENTO: sé muy prudente, no negocies ni prometas nada; usa send_contact para trasladar la solicitud al equipo de gestión, que le contactará."
		} else if b.IsSpecialBooking || b.SpecialDateTitle != "" {
			out["instruction"] = "Reserva de FECHA ESPECIAL: no se puede modificar ni cancelar por WhatsApp; para cambios, gestión del restaurante."
		}
		return botJSON(out), nil
	}
	return botJSON(map[string]any{"error": "reserva no encontrada para este teléfono"}), nil
}

func (s *Server) botToolAddBookingNote(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig, input json.RawMessage) (string, error) {
	var in struct {
		BookingID int64  `json:"booking_id"`
		Note      string `json:"note"`
		Confirmed bool   `json:"confirmed"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.BookingID <= 0 || strings.TrimSpace(in.Note) == "" {
		return botJSON(map[string]any{"error": "booking_id y note son obligatorios"}), nil
	}
	if !in.Confirmed {
		return botJSON(map[string]any{"error": "requiere confirmed=true tras confirmar con el cliente"}), nil
	}
	date, isEvent, isSpecial, found := s.botOwnedBookingFlags(ctx, restaurantID, in.BookingID, msg.Sender)
	if !found {
		return botJSON(map[string]any{"error": "reserva no encontrada para este teléfono"}), nil
	}
	if botIsSameDay(date) {
		return botJSON(map[string]any{"error": "la reserva es para hoy: el cliente debe llamar al restaurante"}), nil
	}
	if isEvent || isSpecial {
		return botJSON(map[string]any{"error": "reserva especial/evento: los detalles se acuerdan con la gestión del restaurante (usa send_contact)"}), nil
	}
	note := "[WhatsApp " + time.Now().In(boMadridTZ).Format("02/01") + "] " + truncate(strings.TrimSpace(in.Note), 300)
	res, err := s.db.ExecContext(ctx, `UPDATE bookings SET commentary = TRIM(CONCAT(COALESCE(commentary,''), IF(COALESCE(commentary,'')='', '', '\n'), ?)) WHERE restaurant_id = ? AND id = ?`, note, restaurantID, in.BookingID)
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo guardar la nota"}), nil
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return botJSON(map[string]any{"error": "no se pudo guardar la nota"}), nil
	}
	s.broadcastBookingChanged(restaurantID, in.BookingID, "booking_updated")
	log.Printf("[bot] checkpoint wa_bot_tools_v2 restaurant_id=%d sender=%s booking_id=%d note_added", restaurantID, msg.Sender, in.BookingID)
	return botJSON(map[string]any{"saved": true, "booking_id": in.BookingID, "note": note}), nil
}

// botCommentarySignals tags staff commentary so the pipeline and the model
// know whether the booking hides an event negotiation, a menu tasting, a
// celebration, allergies or a special table (wa_bot_booking_context_v2).
func botCommentarySignals(commentary string) []string {
	c := normalizeBotIntentText(commentary)
	if c == "" {
		return nil
	}
	checks := []struct {
		tag   string
		words []string
	}{
		{"event_negotiation", []string{"evento", "bautizo", "comunion", "boda", "banquete", "empresa", "cumpleanos", "celebracion", "vienen a preguntar", "vienen a pedir", "viene para informacion", "informacion", "prueba de menu", "degustacion", "presupuesto"}},
		{"children_menu", []string{"infantil", "ninos", "nino"}},
		{"allergy", []string{"alerg", "celiac", "intoleran", "sin gluten", "sin pulpo", "vegano", "vegetarian"}},
		{"special_table", []string{"salon", "mesa ", "chimenea", "terraza", "romantic"}},
		{"payment", []string{"bizum", "pagado", "a cuenta", "adelanto", "paga "}},
		{"vip", []string{"importantes", "vip", "negocios"}},
	}
	var out []string
	for _, ch := range checks {
		for _, w := range ch.words {
			if strings.Contains(c, w) {
				out = append(out, ch.tag)
				break
			}
		}
	}
	return out
}
