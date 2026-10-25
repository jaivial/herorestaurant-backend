package api

import (
	"context"
	"encoding/json"
	"strings"
)

// Coordination id: wa_bot_staff_bookings_overview_v1
//
// The Bot Alqueria incident of 2026-10-23 15:33 (18 iterations / 37 tools)
// burned ~8 iterations calling admin_get_bookings_id once per reservation:
// the model listed the day, then looped id-by-id to read each booking's
// commentary/rice/specials. That N+1 pattern is what pushed the turn into its
// 3-minute budget. staff_bookings_overview answers it in ONE call: the whole
// day (or a single booking) with every field the staff questions ask for.
//
// Two modes, one tool (minimum common multiple):
//   - booking_id set -> that booking alone (full detail, commentary signals).
//   - otherwise      -> every ACTIVE booking of date (default today), full rows.

// botToolStaffBookingsOverview is the catalog def (staff groups only).
func botToolStaffBookingsOverview() botToolDef {
	return botToolDef{
		Name: "staff_bookings_overview",
		Description: "Reservas de una fecha del restaurante (por defecto hoy) con TODOS los detalles de cada una en UNA sola llamada: cliente, hora, personas, estado, arroz, tronas, carritos, comentarios del personal, si es evento o fecha especial o pre-reserva, y senales derivadas (alergias, celebracion, salon...). " +
			"Pasa booking_id para el detalle de UNA reserva concreta, o date para el dia completo. Prefiere ESTA herramienta a llamar admin_call varias veces (una por reserva): evita el patron una-reserva-por-llamada que agota el turno.",
		InputSchema: botSchema(`{"type":"object","properties":{"date":{"type":"string","description":"Fecha YYYY-MM-DD o dd/MM/yyyy (opcional, por defecto hoy)"},"booking_id":{"type":"integer","description":"ID de UNA reserva para su detalle completo (opcional; si se indica, ignora date)"}}}`),
	}
}

// botToolStaffBookingsOverview executes staff_bookings_overview. It reuses the
// same fresh-member gate as the rest of the staff admin reads
// (botSpecialAdminAuthorize), so it is fail-closed outside the allowed groups.
func (s *Server) botToolStaffBookingsOverview(ctx context.Context, restaurantID int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	if _, _, ok := s.botSpecialAdminAuthorize(ctx, restaurantID, msg); !ok {
		return botJSON(map[string]any{"error": "no autorizado"}), nil
	}
	var in struct {
		Date      string `json:"date"`
		BookingID int64  `json:"booking_id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return botJSON(map[string]any{"error": "argumentos ilegibles"}), nil
	}
	if in.BookingID > 0 {
		return s.adminBookingDetail(ctx, restaurantID, in.BookingID)
	}
	dateISO := botTodayISO()
	if strings.TrimSpace(in.Date) != "" {
		parsed, err := parseBotDate(in.Date)
		if err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		dateISO = parsed
	}
	return s.adminBookingsDayDetail(ctx, restaurantID, dateISO)
}

// adminBookingDetail returns the full record of one booking, with commentary
// signals, or a not-found error. It is the single-booking half of
// staff_bookings_overview.
func (s *Server) adminBookingDetail(ctx context.Context, restaurantID int, bookingID int64) (string, error) {
	b, err := s.adminBookingByID(ctx, restaurantID, bookingID)
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo leer la reserva"}), nil
	}
	if b == nil {
		return botJSON(map[string]any{"error": "reserva no encontrada", "terminal": true}), nil
	}
	out := map[string]any{"booking": b, "commentary_signals": botCommentarySignals(b.Commentary)}
	// wa_bot_catalog_v2: a change request needs the group menu and its picked
	// principales; without them the model explored the panel catalog for ~10
	// iterations (prod 2026-10-23 18:15).
	var menuID int64
	var principales, extras string
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(menu_de_grupo_id,0), COALESCE(principales_json,''), COALESCE(extras_json,'') FROM bookings WHERE restaurant_id = ? AND id = ?`, restaurantID, bookingID).Scan(&menuID, &principales, &extras); err == nil {
		if menuID > 0 {
			out["menu_de_grupo_id"] = menuID
			out["menu_hint"] = "Platos y principales elegibles: get_menu_details(menu_id). Cambiar principales: booking_propose_set_group_menu."
		}
		if p := strings.TrimSpace(principales); p != "" && json.Valid([]byte(p)) {
			out["principales"] = json.RawMessage(p)
		}
		if e := strings.TrimSpace(extras); e != "" && json.Valid([]byte(e)) {
			out["extras"] = json.RawMessage(e)
		}
	}
	if b.IsEvent {
		out["instruction"] = "Reserva de EVENTO: se muy prudente, no negocies ni prometas nada; la gestion la realiza el restaurante."
	} else if b.IsSpecialBooking || b.SpecialDateTitle != "" {
		out["instruction"] = "Reserva de FECHA ESPECIAL: no se puede modificar ni cancelar por WhatsApp; para cambios, gestion del restaurante."
	}
	return botJSON(out), nil
}

// adminBookingsDayDetail returns every ACTIVE booking of a date with full
// detail in one JSON payload.
func (s *Server) adminBookingsDayDetail(ctx context.Context, restaurantID int, dateISO string) (string, error) {
	bookings, err := s.adminBookingsByDate(ctx, restaurantID, dateISO)
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudieron cargar las reservas"}), nil
	}
	type dayItem struct {
		*botBookingRow
		CommentarySignals []string `json:"commentary_signals,omitempty"`
	}
	items := make([]dayItem, 0, len(bookings))
	for i := range bookings {
		items = append(items, dayItem{&bookings[i], botCommentarySignals(bookings[i].Commentary)})
	}
	return botJSON(map[string]any{
		"date":     dateISO,
		"date_es":  botFormatISODateES(dateISO),
		"count":    len(items),
		"bookings": items,
	}), nil
}

// adminBookingByID loads one booking row for the restaurant, or nil if absent.
func (s *Server) adminBookingByID(ctx context.Context, restaurantID int, bookingID int64) (*botBookingRow, error) {
	rows, err := s.adminQueryBookings(ctx, restaurantID, " AND id = ? LIMIT 1", bookingID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// adminBookingsByDate loads the active bookings of one date, ordered by time.
func (s *Server) adminBookingsByDate(ctx context.Context, restaurantID int, dateISO string) ([]botBookingRow, error) {
	return s.adminQueryBookings(ctx, restaurantID,
		" AND reservation_date = ? AND status NOT IN ('cancelled','canceled') ORDER BY reservation_time, id LIMIT 200", dateISO)
}

// adminQueryBookings is the shared SELECT that maps a bookings row to
// botBookingRow, with the event / special-date flags the staff questions need.
// The extra WHERE/ORDER clause and its args are supplied by the caller.
func (s *Server) adminQueryBookings(ctx context.Context, restaurantID int, extra string, args ...any) ([]botBookingRow, error) {
	q := `SELECT id,
		DATE_FORMAT(reservation_date, '%Y-%m-%d'),
		TIME_FORMAT(reservation_time, '%H:%i'),
		party_size, customer_name, COALESCE(contact_phone,''), COALESCE(status,''),
		COALESCE(arroz_type, ''), COALESCE(arroz_servings, ''),
		COALESCE(highChairs, 0), COALESCE(babyStrollers, 0),
		COALESCE(commentary, ''), COALESCE(is_event, 0),
		COALESCE(is_special_booking, 0), COALESCE(is_prereserva, 0),
		COALESCE((SELECT sd.title FROM special_dates sd WHERE sd.restaurant_id = bookings.restaurant_id AND sd.date = bookings.reservation_date AND sd.is_active = 1 LIMIT 1), ''),
		COALESCE(children, 0), COALESCE(has_mobility_issues, 0)
	FROM bookings WHERE restaurant_id = ?` + extra
	full := append([]any{restaurantID}, args...)
	rows, err := s.db.QueryContext(ctx, q, full...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]botBookingRow, 0, 16)
	for rows.Next() {
		var (
			b                                botBookingRow
			isEvent, isSpecial, isPre, mobil int
		)
		if err := rows.Scan(&b.ID, &b.Date, &b.Time, &b.People, &b.Name, &b.ContactPhone, &b.Status,
			&b.RiceType, &b.RiceServings, &b.HighChairs, &b.BabyStrollers,
			&b.Commentary, &isEvent, &isSpecial, &isPre, &b.SpecialDateTitle,
			&b.Children, &mobil); err != nil {
			return nil, err
		}
		b.Commentary = strings.TrimSpace(b.Commentary)
		b.IsEvent, b.IsSpecialBooking, b.IsPrereserva, b.HasMobilityIssues = isEvent != 0, isSpecial != 0, isPre != 0, mobil != 0
		out = append(out, b)
	}
	return out, rows.Err()
}
