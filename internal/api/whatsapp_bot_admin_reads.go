package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strconv"
	"time"
)

// Coordination id: wa_bot_admin_reads_v1
//
// Read-only tools for the staff group. They answer "¿qué pasó?" questions the
// panel already answers, without giving the bot any write path: cancelled
// bookings of a day (who cancelled, when) and the active tables layout
// (areas + tables). Registered only for group turns (botToolDefsForChat
// inGroup), never for customer chats.

func botToolDefsAdminReads() []botToolDef {
	return []botToolDef{
		{
			Name:        "list_cancelled_bookings",
			Description: "Lista las reservas CANCELADAS de una fecha (por defecto hoy): número de reserva, cliente, personas, hora, cuándo se canceló y quién la canceló. ÚSALO cuando el grupo pregunte por cancelaciones o para localizar una reserva cancelada antes de pedir su reactivación (la reactivación se hace en el panel).",
			InputSchema: botSchema(`{"type":"object","properties":{"date":{"type":"string","description":"Fecha YYYY-MM-DD o dd/MM/yyyy (opcional, por defecto hoy)"}}}`),
		},
		{
			Name:        "list_special_dates",
			Description: "Lista las fechas especiales activas desde hoy en adelante (Navidad, Nochevieja...): título, si tiene pre-reserva, máximo por mesa y los menús vinculados con su adelanto y precio. ÚSALO cuando el grupo pregunte qué fechas especiales hay configuradas o planifique campañas.",
			InputSchema: botSchema(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "get_booking_config",
			Description: "Configuración operativa de reservas del restaurante: modo de apertura, horas por defecto (mediodía/noche), días de la semana que abre, límite diario de comensales, límites de mesas de dos/tres, si permite elegir salón/planta y la pregunta de movilidad. Con una fecha indica además si ese día usa el horario por defecto o un override. ÚSALO cuando el grupo pregunte por la configuración de reservas.",
			InputSchema: botSchema(`{"type":"object","properties":{"date":{"type":"string","description":"Fecha YYYY-MM-DD o dd/MM/yyyy (opcional)"}}}`),
		},
		{
			Name:        "list_tables",
			Description: "Lista los salones (áreas) activos y sus mesas activas: número, nombre, forma y capacidad. ÚSALO cuando el grupo pregunte qué mesas o salones hay configurados.",
			InputSchema: botSchema(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "bookings_summary",
			Description: "Conteo de reservas por rango de fechas (por defecto desde hoy a 7 días, máximo 31 fechas incluidas): para cada día, reservas activas y comensales, con las canceladas contadas aparte, y totales del rango. Son conteos de reservas, no ocupación física de mesas. ÚSALO cuando el grupo pregunte cómo va la semana o cuántas reservas hay entre dos fechas.",
			InputSchema: botSchema(`{"type":"object","properties":{"date_from":{"type":"string","description":"Fecha inicio YYYY-MM-DD o dd/MM/yyyy (opcional, por defecto hoy)"},"date_to":{"type":"string","description":"Fecha fin YYYY-MM-DD o dd/MM/yyyy (opcional, por defecto +6 días; máximo 31 fechas incluidas)"}}}`),
		},
	}
}

func (s *Server) botToolListCancelledBookings(ctx context.Context, restaurantID int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	if _, _, ok := s.botSpecialAdminAuthorize(ctx, restaurantID, msg); !ok {
		return botJSON(map[string]any{"error": "no autorizado"}), nil
	}
	var in struct {
		Date string `json:"date"`
	}
	_ = json.Unmarshal(input, &in)
	dateISO := botTodayISO()
	if in.Date != "" {
		parsed, err := parseBotDate(in.Date)
		if err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		dateISO = parsed
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT booking_id, customer_name, contact_phone, party_size, reservation_time,
		       DATE_FORMAT(cancellation_date, '%Y-%m-%d %H:%i') AS cancellation_date,
		       cancelled_by, cancelled_by_name
		FROM cancelled_bookings
		WHERE restaurant_id = ? AND reservation_date = ?
		ORDER BY cancellation_date DESC
		LIMIT 50
	`, restaurantID, dateISO)
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudieron cargar las cancelaciones"}), nil
	}
	defer rows.Close()
	type item struct {
		BookingID        int    `json:"booking_id"`
		CustomerName     string `json:"customer_name"`
		ContactPhone     string `json:"contact_phone,omitempty"`
		PartySize        int    `json:"party_size"`
		ReservationTime  string `json:"reservation_time"`
		CancellationDate string `json:"cancellation_date,omitempty"`
		CancelledBy      string `json:"cancelled_by,omitempty"`
		CancelledByName  string `json:"cancelled_by_name,omitempty"`
	}
	items := make([]item, 0)
	for rows.Next() {
		var it item
		var phone, cancelDate, cancelledBy, cancelledByName sql.NullString
		var t sql.NullString
		if err := rows.Scan(&it.BookingID, &it.CustomerName, &phone, &it.PartySize, &t, &cancelDate, &cancelledBy, &cancelledByName); err != nil {
			return botJSON(map[string]any{"error": "no se pudieron leer las cancelaciones"}), nil
		}
		it.ContactPhone = phone.String
		it.ReservationTime = t.String
		it.CancellationDate = cancelDate.String
		it.CancelledBy = cancelledBy.String
		it.CancelledByName = cancelledByName.String
		items = append(items, it)
	}
	return botJSON(map[string]any{
		"date":      dateISO,
		"date_es":   botFormatISODateES(dateISO),
		"count":     len(items),
		"cancelled": items,
		"note":      "La reactivación de una reserva cancelada se hace desde el panel de administración.",
	}), nil
}

func (s *Server) botToolListTables(ctx context.Context, restaurantID int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	if _, _, ok := s.botSpecialAdminAuthorize(ctx, restaurantID, msg); !ok {
		return botJSON(map[string]any{"error": "no autorizado"}), nil
	}
	areaRows, err := s.db.QueryContext(ctx, `
		SELECT id, name FROM restaurant_areas
		WHERE restaurant_id = ? AND is_active = 1
		ORDER BY display_order ASC, id ASC
	`, restaurantID)
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudieron cargar los salones"}), nil
	}
	defer areaRows.Close()
	type area struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	areas := make([]area, 0)
	for areaRows.Next() {
		var a area
		if err := areaRows.Scan(&a.ID, &a.Name); err != nil {
			return botJSON(map[string]any{"error": "no se pudieron leer los salones"}), nil
		}
		areas = append(areas, a)
	}
	tableRows, err := s.db.QueryContext(ctx, `
		SELECT id, area_id, numero_mesa, name, shape, capacity, status
		FROM restaurant_tables
		WHERE restaurant_id = ? AND is_active = 1
		ORDER BY area_id ASC, display_order ASC, id ASC
	`, restaurantID)
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudieron cargar las mesas"}), nil
	}
	defer tableRows.Close()
	type table struct {
		ID         int64  `json:"id"`
		AreaID     int64  `json:"area_id"`
		NumeroMesa string `json:"numero_mesa"`
		Name       string `json:"name,omitempty"`
		Shape      string `json:"shape,omitempty"`
		Capacity   int    `json:"capacity"`
		Status     string `json:"status"`
	}
	tables := make([]table, 0)
	for tableRows.Next() {
		var t table
		var areaID sql.NullInt64
		var name, shape sql.NullString
		if err := tableRows.Scan(&t.ID, &areaID, &t.NumeroMesa, &name, &shape, &t.Capacity, &t.Status); err != nil {
			return botJSON(map[string]any{"error": "no se pudieron leer las mesas"}), nil
		}
		t.AreaID = areaID.Int64
		t.Name = name.String
		t.Shape = shape.String
		tables = append(tables, t)
	}
	return botJSON(map[string]any{
		"areas":  areas,
		"tables": tables,
		"count":  len(tables),
	}), nil
}

func (s *Server) botToolListSpecialDates(ctx context.Context, restaurantID int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	if _, _, ok := s.botSpecialAdminAuthorize(ctx, restaurantID, msg); !ok {
		return botJSON(map[string]any{"error": "no autorizado"}), nil
	}
	today := botTodayISO()
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, DATE_FORMAT(date, '%Y-%m-%d') AS d, title, prereserva_enabled,
		       max_per_table_enabled, max_per_table
		FROM special_dates
		WHERE restaurant_id = ? AND is_active = 1 AND date >= ?
		ORDER BY date ASC
		LIMIT 30
	`, restaurantID, today)
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudieron cargar las fechas especiales"}), nil
	}
	defer rows.Close()
	type item struct {
		ID                 int64  `json:"id"`
		Date               string `json:"date"`
		Title              string `json:"title"`
		PrereservaEnabled  bool   `json:"prereserva_enabled"`
		MaxPerTableEnabled bool   `json:"max_per_table_enabled"`
		MaxPerTable        sql.NullInt64
	}
	items := make([]item, 0)
	ids := make([]int64, 0)
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ID, &it.Date, &it.Title, &it.PrereservaEnabled, &it.MaxPerTableEnabled, &it.MaxPerTable); err != nil {
			return botJSON(map[string]any{"error": "no se pudieron leer las fechas especiales"}), nil
		}
		items = append(items, it)
		ids = append(ids, it.ID)
	}
	// Linked menus per special date (adelanto + price), keyed by special date id.
	menus := map[int64][]map[string]any{}
	for _, id := range ids {
		mrows, err := s.db.QueryContext(ctx, `
			SELECT menu_id, custom_title, adelanto_amount, price
			FROM special_date_menus
			WHERE restaurant_id = ? AND special_date_id = ?
		`, restaurantID, id)
		if err != nil {
			// A partial catalogue must not look complete: fail the whole call.
			return botJSON(map[string]any{"error": "no se pudieron cargar los menús vinculados"}), nil
		}
		list := make([]map[string]any, 0)
		for mrows.Next() {
			var menuID int64
			var title, adelanto, price sql.NullString
			// Scan DECIMAL as text and parse: keeps the value exact and the
			// scan independent of the driver's numeric conversion.
			if err := mrows.Scan(&menuID, &title, &adelanto, &price); err == nil {
				adv, _ := strconv.ParseFloat(adelanto.String, 64)
				prc, _ := strconv.ParseFloat(price.String, 64)
				list = append(list, map[string]any{"menu_id": menuID, "custom_title": title.String, "adelanto_amount": adv, "price": prc})
			}
		}
		mrows.Close()
		menus[id] = list
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		entry := map[string]any{
			"id": it.ID, "date": it.Date, "date_es": botFormatISODateES(it.Date), "title": it.Title,
			"prereserva_enabled": it.PrereservaEnabled, "max_per_table_enabled": it.MaxPerTableEnabled,
			"menus": menus[it.ID],
		}
		if it.MaxPerTable.Valid {
			entry["max_per_table"] = it.MaxPerTable.Int64
		}
		out = append(out, entry)
	}
	return botJSON(map[string]any{"count": len(out), "special_dates": out}), nil
}

func (s *Server) botToolGetBookingConfig(ctx context.Context, restaurantID int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	if _, _, ok := s.botSpecialAdminAuthorize(ctx, restaurantID, msg); !ok {
		return botJSON(map[string]any{"error": "no autorizado"}), nil
	}
	var in struct {
		Date string `json:"date"`
	}
	_ = json.Unmarshal(input, &in)
	defaults, err := s.loadReservationDefaults(ctx, restaurantID)
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo cargar la configuración"}), nil
	}
	out := map[string]any{
		"opening_mode":            defaults.OpeningMode,
		"morning_hours":           defaults.MorningHours,
		"night_hours":             defaults.NightHours,
		"weekday_open":            defaults.WeekdayOpen,
		"daily_limit":             defaults.DailyLimit,
		"mesas_de_dos_limit":      defaults.MesasDeDosLimit,
		"mesas_de_tres_limit":     defaults.MesasDeTresLimit,
		"hour_split_enabled":      defaults.HourSplitEnabled,
		"allow_floor_reservation": defaults.AllowFloorReservation,
		"allow_salon_reservation": defaults.AllowSalonReservation,
		"mobility_enabled":        defaults.MobilityEnabled,
	}
	if in.Date != "" {
		dateISO, err := parseBotDate(in.Date)
		if err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		var hoursRaw, modeRaw sql.NullString
		err = s.db.QueryRowContext(ctx, `
			SELECT hoursarray, opening_mode
			FROM openinghours
			WHERE restaurant_id = ? AND dateselected = ?
			LIMIT 1
		`, restaurantID, dateISO).Scan(&hoursRaw, &modeRaw)
		if err != nil && err != sql.ErrNoRows {
			return botJSON(map[string]any{"error": "no se pudo consultar el día"}), nil
		}
		source := "default"
		mode := defaults.OpeningMode
		morning := defaults.MorningHours
		night := defaults.NightHours
		if list, ok := parseHoursJSON(hoursRaw); ok {
			source = "override"
			morning, night = splitHoursByShift(list)
			if modeRaw.Valid && modeRaw.String != "" {
				mode = normalizeOpeningMode(modeRaw.String)
			} else {
				mode = modeFromHours(morning, night)
			}
		}
		out["date"] = dateISO
		out["date_es"] = botFormatISODateES(dateISO)
		out["day_source"] = source
		out["day_opening_mode"] = mode
		out["day_morning_hours"] = morning
		out["day_night_hours"] = night
	}
	return botJSON(out), nil
}

func (s *Server) botToolBookingsSummary(ctx context.Context, restaurantID int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	if _, _, ok := s.botSpecialAdminAuthorize(ctx, restaurantID, msg); !ok {
		return botJSON(map[string]any{"error": "no autorizado"}), nil
	}
	var in struct {
		DateFrom string `json:"date_from"`
		DateTo   string `json:"date_to"`
	}
	_ = json.Unmarshal(input, &in)
	fromISO := botTodayISO()
	if in.DateFrom != "" {
		parsed, err := parseBotDate(in.DateFrom)
		if err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		fromISO = parsed
	}
	fromT, err := time.Parse("2006-01-02", fromISO)
	if err != nil {
		return botJSON(map[string]any{"error": "fecha inicio inválida"}), nil
	}
	toISO := fromT.AddDate(0, 0, 6).Format("2006-01-02")
	if in.DateTo != "" {
		parsed, err := parseBotDate(in.DateTo)
		if err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		toISO = parsed
	}
	toT, err := time.Parse("2006-01-02", toISO)
	if err != nil {
		return botJSON(map[string]any{"error": "fecha fin inválida"}), nil
	}
	if toT.Before(fromT) {
		return botJSON(map[string]any{"error": "date_to no puede ser anterior a date_from"}), nil
	}
	if toT.Sub(fromT) > 30*24*time.Hour {
		return botJSON(map[string]any{"error": "rango máximo 31 fechas incluidas"}), nil
	}
	// Cancelled storage is mixed: panel/bot cancellation MOVES the row to
	// cancelled_bookings (DELETE from bookings), while other flows only mark
	// status in bookings. Count both sources for the reservation day; a moved
	// row no longer exists in bookings, so the two sources cannot double-count
	// the same booking.
	rows, err := s.db.QueryContext(ctx, `
		SELECT DATE_FORMAT(reservation_date, '%Y-%m-%d') AS d,
		       SUM(CASE WHEN LOWER(TRIM(status)) IN ('cancelled','cancelada','canceled') THEN 0 ELSE 1 END),
		       COALESCE(SUM(CASE WHEN LOWER(TRIM(status)) IN ('cancelled','cancelada','canceled') THEN 0 ELSE party_size END),0),
		       SUM(CASE WHEN LOWER(TRIM(status)) IN ('cancelled','cancelada','canceled') THEN 1 ELSE 0 END)
		FROM bookings
		WHERE restaurant_id = ? AND reservation_date >= ? AND reservation_date <= ?
		GROUP BY reservation_date
		ORDER BY reservation_date
		LIMIT 31
	`, restaurantID, fromISO, toISO)
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo cargar el resumen"}), nil
	}
	defer rows.Close()
	cancelRows, err := s.db.QueryContext(ctx, `
		SELECT DATE_FORMAT(reservation_date, '%Y-%m-%d') AS d, COUNT(*)
		FROM cancelled_bookings
		WHERE restaurant_id = ? AND reservation_date >= ? AND reservation_date <= ?
		GROUP BY reservation_date
		ORDER BY reservation_date
		LIMIT 31
	`, restaurantID, fromISO, toISO)
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo cargar el resumen"}), nil
	}
	defer cancelRows.Close()
	cancelledByDay := map[string]int{}
	for cancelRows.Next() {
		var d string
		var n int
		if err := cancelRows.Scan(&d, &n); err != nil {
			return botJSON(map[string]any{"error": "no se pudo leer el resumen"}), nil
		}
		cancelledByDay[d] = n
	}
	type day struct {
		Date      string `json:"date"`
		DateES    string `json:"date_es"`
		Bookings  int    `json:"bookings"`
		Diners    int    `json:"diners"`
		Cancelled int    `json:"cancelled"`
	}
	days := make([]day, 0)
	totalBookings, totalDiners, totalCancelled := 0, 0, 0
	for rows.Next() {
		var d string
		var n, p, c int
		if err := rows.Scan(&d, &n, &p, &c); err != nil {
			return botJSON(map[string]any{"error": "no se pudo leer el resumen"}), nil
		}
		c += cancelledByDay[d]
		days = append(days, day{Date: d, DateES: botFormatISODateES(d), Bookings: n, Diners: p, Cancelled: c})
		totalBookings += n
		totalDiners += p
		totalCancelled += c
	}
	seen := map[string]bool{}
	for _, dy := range days {
		seen[dy.Date] = true
	}
	for d, n := range cancelledByDay {
		if seen[d] {
			continue
		}
		days = append(days, day{Date: d, DateES: botFormatISODateES(d), Cancelled: n})
		totalCancelled += n
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Date < days[j].Date })
	return botJSON(map[string]any{
		"date_from":       fromISO,
		"date_to":         toISO,
		"days":            days,
		"total_bookings":  totalBookings,
		"total_diners":    totalDiners,
		"total_cancelled": totalCancelled,
		"note":            "Conteos de reservas por día de la reserva: activas y comensales por un lado; canceladas aparte, sumando el registro de cancelaciones y las marcadas en bookings.",
	}), nil
}
