package api

import (
	"context"
	"database/sql"
	"encoding/json"
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
			Name:        "list_tables",
			Description: "Lista los salones (áreas) activos y sus mesas activas: número, nombre, forma y capacidad. ÚSALO cuando el grupo pregunte qué mesas o salones hay configurados.",
			InputSchema: botSchema(`{"type":"object","properties":{}}`),
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
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, DATE_FORMAT(date, '%Y-%m-%d') AS d, title, prereserva_enabled,
		       max_per_table_enabled, max_per_table
		FROM special_dates
		WHERE restaurant_id = ? AND is_active = 1 AND date >= CURDATE()
		ORDER BY date ASC
		LIMIT 30
	`, restaurantID)
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
		MaxPerTable        int    `json:"max_per_table,omitempty"`
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
			continue
		}
		list := make([]map[string]any, 0)
		for mrows.Next() {
			var menuID int64
			var title sql.NullString
			var adelanto, price sql.NullFloat64
			if err := mrows.Scan(&menuID, &title, &adelanto, &price); err == nil {
				list = append(list, map[string]any{"menu_id": menuID, "custom_title": title.String, "adelanto_amount": adelanto.Float64, "price": price.Float64})
			}
		}
		mrows.Close()
		menus[id] = list
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]any{
			"id": it.ID, "date": it.Date, "date_es": botFormatISODateES(it.Date), "title": it.Title,
			"prereserva_enabled": it.PrereservaEnabled, "max_per_table_enabled": it.MaxPerTableEnabled,
			"max_per_table": it.MaxPerTable, "menus": menus[it.ID],
		})
	}
	return botJSON(map[string]any{"count": len(out), "special_dates": out}), nil
}
