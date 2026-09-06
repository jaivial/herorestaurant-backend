package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// botToolExecutorFor returns the executor closure bound to a tenant and the
// current WhatsApp sender. Every tool is scoped by restaurantID.
func (s *Server) botToolExecutorFor(restaurantID int, msg botWebhookMessage, tenant botTenantConfig) botToolExecutor {
	return func(ctx context.Context, name string, input json.RawMessage) (string, error) {
		out, err := s.botExecuteTool(ctx, restaurantID, msg, tenant, name, input)
		if err != nil {
			return "", err
		}
		return out, nil
	}
}

func botJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return `{"error":"internal serialization error"}`
	}
	return string(raw)
}

func (s *Server) botExecuteTool(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig, name string, input json.RawMessage) (string, error) {
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	switch name {
	case "send_message":
		return s.botToolSendMessage(ctx, restaurantID, msg, input)
	case "get_restaurant_info":
		return s.botToolRestaurantInfo(ctx, restaurantID)
	case "get_rice_menu":
		return s.botToolRiceMenu(ctx, restaurantID)
	case "list_menus":
		return s.botToolListMenus(ctx, restaurantID)
	case "get_menu_details":
		return s.botToolMenuDetails(ctx, restaurantID, input)
	case "get_coffee_menu":
		return s.botToolCoffeeMenu(ctx, restaurantID)
	case "get_drinks_menu":
		return s.botToolDrinksMenu(ctx, restaurantID)
	case "get_wines_menu":
		return s.botToolWinesMenu(ctx, restaurantID)
	case "get_default_schedule":
		return s.botToolDefaultSchedule(ctx, restaurantID)
	case "get_day_schedule":
		return s.botToolDaySchedule(ctx, restaurantID, input)
	case "check_day_capacity":
		return s.botToolDayCapacity(ctx, restaurantID, input)
	case "check_availability_for_party":
		return s.botToolAvailabilityForParty(ctx, restaurantID, input)
	case "get_bookings":
		return s.botToolGetBookings(ctx, restaurantID, msg.Sender)
	case "create_booking":
		return s.botToolCreateBooking(ctx, restaurantID, msg, input)
	case "cancel_booking":
		return s.botToolCancelBooking(ctx, restaurantID, msg.Sender, input)
	case "modify_booking":
		return s.botToolModifyBooking(ctx, restaurantID, msg.Sender, input)
	case "get_member_schedule":
		return s.botToolMemberSchedule(ctx, restaurantID, msg.Sender, input)
	case "get_member_attendance":
		return s.botToolMemberAttendance(ctx, restaurantID, msg.Sender, input)
	case "get_member_access":
		return s.botToolMemberAccess(ctx, restaurantID, msg.Sender)
	case "send_menu_buttons":
		return s.botToolSendButtons(ctx, restaurantID, msg, input)
	case "send_image", "send_document":
		if tenant.DisableAttachments {
			return botJSON(map[string]any{"error": "los adjuntos están desactivados para este restaurante"}), nil
		}
		return s.botToolSendMedia(ctx, restaurantID, msg, name, input)
	case "send_location":
		if tenant.DisableAttachments {
			return botJSON(map[string]any{"error": "los adjuntos están desactivados para este restaurante"}), nil
		}
		return s.botToolSendLocation(ctx, restaurantID, msg)
	case "send_contact":
		if tenant.DisableAttachments {
			return botJSON(map[string]any{"error": "los adjuntos están desactivados para este restaurante"}), nil
		}
		return s.botToolSendContact(ctx, restaurantID, msg, tenant)
	default:
		return botJSON(map[string]any{"error": "herramienta desconocida: " + name}), nil
	}
}

// --- messaging tools -------------------------------------------------------

func (s *Server) botToolSendMessage(ctx context.Context, restaurantID int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	var in struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(input, &in); err != nil || strings.TrimSpace(in.Message) == "" {
		return botJSON(map[string]any{"error": "message requerido"}), nil
	}
	gw, ok := s.botGatewayFor(ctx, restaurantID)
	if !ok {
		return botJSON(map[string]any{"error": "whatsapp no configurado"}), nil
	}
	if err := s.sendWhatsAppTextTracked(ctx, restaurantID, gw, msg.Sender, in.Message, "agent"); err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	return botJSON(map[string]any{"sent": true}), nil
}

func (s *Server) botToolSendButtons(ctx context.Context, restaurantID int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	var in struct {
		Text    string   `json:"text"`
		Choices []string `json:"choices"`
	}
	if err := json.Unmarshal(input, &in); err != nil || strings.TrimSpace(in.Text) == "" || len(in.Choices) == 0 {
		return botJSON(map[string]any{"error": "text y choices requeridos"}), nil
	}
	if len(in.Choices) > 3 {
		in.Choices = in.Choices[:3]
	}
	gw, ok := s.botGatewayFor(ctx, restaurantID)
	if !ok {
		return botJSON(map[string]any{"error": "whatsapp no configurado"}), nil
	}
	if err := s.sendWhatsAppMenuTracked(ctx, restaurantID, gw, msg.Sender, in.Text, in.Choices, "agent"); err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	return botJSON(map[string]any{"sent": true}), nil
}

func (s *Server) botToolSendMedia(ctx context.Context, restaurantID int, msg botWebhookMessage, kind string, input json.RawMessage) (string, error) {
	var in struct {
		URL      string `json:"url"`
		Caption  string `json:"caption"`
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal(input, &in); err != nil || strings.TrimSpace(in.URL) == "" {
		return botJSON(map[string]any{"error": "url requerida"}), nil
	}
	if !strings.HasPrefix(in.URL, "https://") {
		return botJSON(map[string]any{"error": "la url debe ser https"}), nil
	}

	mediaType := "image"
	if kind == "send_document" {
		mediaType = "document"
	}
	gw, ok := s.botGatewayFor(ctx, restaurantID)
	if !ok {
		return botJSON(map[string]any{"error": "whatsapp no configurado"}), nil
	}
	if err := gw.SendMedia(ctx, msg.Sender, waMedia{
		Kind: mediaType, URL: in.URL, Caption: in.Caption, Filename: in.Filename,
	}); err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	visible := strings.TrimSpace(in.Caption)
	if visible == "" {
		visible = "[" + mediaType + " enviado: " + in.URL + "]"
	}
	s.botRecordConversationMessage(ctx, restaurantID, msg.Sender, "assistant", visible, kind, "agent")
	return botJSON(map[string]any{"sent": true}), nil
}

func (s *Server) botToolSendLocation(ctx context.Context, restaurantID int, msg botWebhookMessage) (string, error) {
	var direccion sql.NullString
	_ = s.db.QueryRowContext(ctx, `SELECT direccion FROM restaurant_info WHERE restaurant_id = ? LIMIT 1`, restaurantID).Scan(&direccion)
	address := strings.TrimSpace(direccion.String)
	if address == "" {
		return botJSON(map[string]any{"error": "el restaurante no tiene dirección configurada"}), nil
	}

	gw, ok := s.botGatewayFor(ctx, restaurantID)
	if !ok {
		return botJSON(map[string]any{"error": "whatsapp no configurado"}), nil
	}
	if err := gw.SendLocation(ctx, msg.Sender, waLocation{Address: address, Name: s.botBrandName(ctx, restaurantID)}); err != nil {
		// Fallback: send as text.
		if terr := s.sendWhatsAppTextTracked(ctx, restaurantID, gw, msg.Sender, "📍 "+address, "agent_location"); terr != nil {
			return botJSON(map[string]any{"error": terr.Error()}), nil
		}
	} else {
		s.botRecordConversationMessage(ctx, restaurantID, msg.Sender, "assistant", "📍 "+address, "send_location", "agent")
	}
	return botJSON(map[string]any{"sent": true, "address": address}), nil
}

func (s *Server) botToolSendContact(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig) (string, error) {
	phone := strings.TrimSpace(tenant.ContactPhone)
	if phone == "" {
		var telefono sql.NullString
		_ = s.db.QueryRowContext(ctx, `SELECT telefono FROM restaurant_info WHERE restaurant_id = ? LIMIT 1`, restaurantID).Scan(&telefono)
		phone = strings.TrimSpace(telefono.String)
	}
	if phone == "" {
		return botJSON(map[string]any{"error": "el restaurante no tiene teléfono configurado"}), nil
	}
	brand := s.botBrandName(ctx, restaurantID)

	gw, ok := s.botGatewayFor(ctx, restaurantID)
	if !ok {
		return botJSON(map[string]any{"error": "whatsapp no configurado"}), nil
	}
	if err := gw.SendContact(ctx, msg.Sender, waContact{FullName: brand, Phone: phone, Organization: brand}); err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	s.botRecordConversationMessage(ctx, restaurantID, msg.Sender, "assistant", "Contacto: "+brand+" "+phone, "send_contact", "agent")
	return botJSON(map[string]any{"sent": true, "phone": phone}), nil
}

func (s *Server) botBrandName(ctx context.Context, restaurantID int) string {
	if branding, err := s.loadRestaurantBranding(ctx, restaurantID); err == nil && strings.TrimSpace(branding.BrandName) != "" {
		return strings.TrimSpace(branding.BrandName)
	}
	var name sql.NullString
	_ = s.db.QueryRowContext(ctx, `SELECT name FROM restaurants WHERE id = ? LIMIT 1`, restaurantID).Scan(&name)
	if strings.TrimSpace(name.String) != "" {
		return strings.TrimSpace(name.String)
	}
	return "Restaurante"
}

// --- info tools ------------------------------------------------------------

func (s *Server) botToolRestaurantInfo(ctx context.Context, restaurantID int) (string, error) {
	var direccion, telefono, email, website, menuURL sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT direccion, telefono, email, website, menu_url
		FROM restaurant_info WHERE restaurant_id = ? LIMIT 1
	`, restaurantID).Scan(&direccion, &telefono, &email, &website, &menuURL)
	if err != nil && !errors.Is(err, sql.ErrNoRows) && !isSQLSchemaError(err) {
		return botJSON(map[string]any{"error": "error consultando información"}), nil
	}
	return botJSON(map[string]any{
		"name":     s.botBrandName(ctx, restaurantID),
		"address":  strings.TrimSpace(direccion.String),
		"phone":    strings.TrimSpace(telefono.String),
		"email":    strings.TrimSpace(email.String),
		"website":  strings.TrimSpace(website.String),
		"menu_url": strings.TrimSpace(menuURL.String),
	}), nil
}

func (s *Server) botToolRiceMenu(ctx context.Context, restaurantID int) (string, error) {
	rices, english, err := s.loadRiceTypes(ctx, restaurantID)
	if err != nil {
		return botJSON(map[string]any{"error": "error consultando arroces"}), nil
	}
	return botJSON(map[string]any{
		"rice_types":         rices,
		"rice_types_english": english,
	}), nil
}

// --- availability tools ----------------------------------------------------

func (s *Server) botDayCapacity(ctx context.Context, restaurantID int, dateISO string) (limit int, total int, err error) {
	var limitRaw sql.NullInt64
	if err = s.db.QueryRowContext(ctx,
		"SELECT dailyLimit FROM reservation_manager WHERE restaurant_id = ? AND reservationDate = ? ORDER BY id DESC LIMIT 1",
		restaurantID, dateISO).Scan(&limitRaw); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, err
	}
	if limitRaw.Valid {
		limit = int(limitRaw.Int64)
	} else {
		// No per-day override: use the tenant's configured default, not a literal.
		defaults, derr := s.loadReservationDefaults(ctx, restaurantID)
		if derr != nil {
			return 0, 0, derr
		}
		limit = defaults.DailyLimit
	}
	if err = s.db.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(party_size),0) FROM bookings WHERE restaurant_id = ? AND reservation_date = ?",
		restaurantID, dateISO).Scan(&total); err != nil {
		return 0, 0, err
	}
	return limit, total, nil
}

// botWeekdayKeys maps time.Weekday() (Sunday=0) to the weekday_open JSON keys.
var botWeekdayKeys = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

type botDaySchedule struct {
	Date         string
	Weekday      string
	WeekdayKey   string
	WeekdayOpen  bool
	HasOverride  bool
	OpeningMode  string
	MorningHours []string
	NightHours   []string
	Open         bool
}

// botResolveDaySchedule computes the effective schedule for a date: the default
// weekday configuration plus any per-day override stored in openinghours.
func (s *Server) botResolveDaySchedule(ctx context.Context, restaurantID int, dateISO string) (botDaySchedule, error) {
	defaults, err := s.loadReservationDefaults(ctx, restaurantID)
	if err != nil {
		return botDaySchedule{}, err
	}
	t, err := time.Parse("2006-01-02", dateISO)
	if err != nil {
		return botDaySchedule{}, err
	}
	wdKey := botWeekdayKeys[int(t.Weekday())]
	out := botDaySchedule{
		Date:         dateISO,
		Weekday:      botSpanishDays[int(t.Weekday())],
		WeekdayKey:   wdKey,
		WeekdayOpen:  defaults.WeekdayOpen[wdKey],
		OpeningMode:  defaults.OpeningMode,
		MorningHours: cloneStrings(defaults.MorningHours),
		NightHours:   cloneStrings(defaults.NightHours),
	}

	var hoursRaw, modeRaw sql.NullString
	err = s.db.QueryRowContext(ctx, `
		SELECT hoursarray, opening_mode FROM openinghours
		WHERE restaurant_id = ? AND dateselected = ? LIMIT 1
	`, restaurantID, dateISO).Scan(&hoursRaw, &modeRaw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return botDaySchedule{}, err
	}
	if err == nil {
		if list, ok := parseHoursJSON(hoursRaw); ok {
			out.HasOverride = true
			out.MorningHours, out.NightHours = splitHoursByShift(list)
			if modeRaw.Valid && modeRaw.String != "" {
				out.OpeningMode = normalizeOpeningMode(modeRaw.String)
			} else {
				out.OpeningMode = modeFromHours(out.MorningHours, out.NightHours)
			}
		}
	}

	hasHours := len(out.MorningHours) > 0 || len(out.NightHours) > 0
	if out.HasOverride {
		// An explicit per-day override defines the day regardless of weekday flag.
		out.Open = hasHours
	} else {
		out.Open = out.WeekdayOpen && hasHours
	}
	return out, nil
}

func (s *Server) botToolDefaultSchedule(ctx context.Context, restaurantID int) (string, error) {
	defaults, err := s.loadReservationDefaults(ctx, restaurantID)
	if err != nil {
		return botJSON(map[string]any{"error": "error consultando el horario por defecto"}), nil
	}
	openDays := []string{}
	for i, key := range botWeekdayKeys {
		if defaults.WeekdayOpen[key] {
			openDays = append(openDays, botSpanishDays[i])
		}
	}
	return botJSON(map[string]any{
		"opening_mode":  defaults.OpeningMode,
		"morning_hours": defaults.MorningHours,
		"night_hours":   defaults.NightHours,
		"daily_limit":   defaults.DailyLimit,
		"weekday_open":  defaults.WeekdayOpen,
		"open_days":     openDays,
	}), nil
}

func (s *Server) botToolDaySchedule(ctx context.Context, restaurantID int, input json.RawMessage) (string, error) {
	var in struct {
		Date string `json:"date"`
	}
	_ = json.Unmarshal(input, &in)
	dateISO, err := parseBotDate(in.Date)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	sched, err := s.botResolveDaySchedule(ctx, restaurantID, dateISO)
	if err != nil {
		return botJSON(map[string]any{"error": "error consultando el horario del día"}), nil
	}
	return botJSON(map[string]any{
		"date":          sched.Date,
		"weekday":       sched.Weekday,
		"weekday_open":  sched.WeekdayOpen,
		"has_override":  sched.HasOverride,
		"opening_mode":  sched.OpeningMode,
		"morning_hours": sched.MorningHours,
		"night_hours":   sched.NightHours,
		"open":          sched.Open,
	}), nil
}

func (s *Server) botToolDayCapacity(ctx context.Context, restaurantID int, input json.RawMessage) (string, error) {
	var in struct {
		Date string `json:"date"`
	}
	_ = json.Unmarshal(input, &in)
	dateISO, err := parseBotDate(in.Date)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	limit, total, err := s.botDayCapacity(ctx, restaurantID, dateISO)
	if err != nil {
		return botJSON(map[string]any{"error": "error consultando capacidad"}), nil
	}
	free := limit - total
	if free < 0 {
		free = 0
	}
	status := "open"
	if limit <= 0 {
		status = "closed"
	} else if free == 0 {
		status = "full"
	}
	return botJSON(map[string]any{
		"date":         dateISO,
		"daily_limit":  limit,
		"total_people": total,
		"free_seats":   free,
		"status":       status,
	}), nil
}

func (s *Server) botToolAvailabilityForParty(ctx context.Context, restaurantID int, input json.RawMessage) (string, error) {
	var in struct {
		Date      string `json:"date"`
		PartySize int    `json:"party_size"`
	}
	_ = json.Unmarshal(input, &in)
	dateISO, err := parseBotDate(in.Date)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	if in.PartySize <= 0 {
		return botJSON(map[string]any{"error": "party_size debe ser mayor que 0"}), nil
	}
	limit, total, err := s.botDayCapacity(ctx, restaurantID, dateISO)
	if err != nil {
		return botJSON(map[string]any{"error": "error consultando capacidad"}), nil
	}
	free := limit - total
	if free < 0 {
		free = 0
	}
	return botJSON(map[string]any{
		"date":       dateISO,
		"party_size": in.PartySize,
		"fits":       in.PartySize <= free,
		"free_seats": free,
	}), nil
}

// --- booking tools ---------------------------------------------------------

type botBookingRow struct {
	ID            int64  `json:"booking_id"`
	Date          string `json:"date"`
	Time          string `json:"time"`
	People        int    `json:"people"`
	Name          string `json:"name"`
	RiceType      string `json:"rice_type,omitempty"`
	RiceServings  string `json:"rice_servings,omitempty"`
	HighChairs    int    `json:"high_chairs,omitempty"`
	BabyStrollers int    `json:"baby_strollers,omitempty"`
}

func (s *Server) botFindBookings(ctx context.Context, restaurantID int, phone string) ([]botBookingRow, error) {
	digits := digitsOnly(phone)
	national := digits
	if strings.HasPrefix(digits, "34") && len(digits) == 11 {
		national = digits[2:]
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,
			DATE_FORMAT(reservation_date, '%Y-%m-%d'),
			TIME_FORMAT(reservation_time, '%H:%i'),
			party_size, customer_name,
			COALESCE(arroz_type, ''), COALESCE(arroz_servings, ''),
			COALESCE(highChairs, 0), COALESCE(babyStrollers, 0)
		FROM bookings
		WHERE restaurant_id = ?
			AND reservation_date >= CURDATE()
			AND (contact_phone = ? OR contact_phone = ? OR CONCAT(COALESCE(contact_phone_country_code,''), contact_phone) = ?)
		ORDER BY reservation_date ASC, reservation_time ASC
		LIMIT 10
	`, restaurantID, national, digits, digits)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []botBookingRow{}
	for rows.Next() {
		var b botBookingRow
		if err := rows.Scan(&b.ID, &b.Date, &b.Time, &b.People, &b.Name, &b.RiceType, &b.RiceServings, &b.HighChairs, &b.BabyStrollers); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Server) botToolGetBookings(ctx context.Context, restaurantID int, phone string) (string, error) {
	bookings, err := s.botFindBookings(ctx, restaurantID, phone)
	if err != nil {
		return botJSON(map[string]any{"error": "error consultando reservas"}), nil
	}
	return botJSON(map[string]any{"bookings": bookings, "count": len(bookings)}), nil
}

func (s *Server) botToolCreateBooking(ctx context.Context, restaurantID int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	var in struct {
		Date          string `json:"date"`
		Time          string `json:"time"`
		People        int    `json:"people"`
		Name          string `json:"name"`
		RiceType      string `json:"rice_type"`
		RiceServings  int    `json:"rice_servings"`
		HighChairs    int    `json:"high_chairs"`
		BabyStrollers int    `json:"baby_strollers"`
		Commentary    string `json:"commentary"`
		Confirmed     bool   `json:"confirmed"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return botJSON(map[string]any{"error": "parámetros inválidos"}), nil
	}
	if !in.Confirmed {
		return botJSON(map[string]any{"error": "requiere confirmed=true tras confirmar con el cliente"}), nil
	}
	dateISO, err := parseBotDate(in.Date)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	today := time.Now().Format("2006-01-02")
	if dateISO <= today {
		return botJSON(map[string]any{"error": "no se aceptan reservas para hoy ni fechas pasadas; el cliente debe llamar por teléfono"}), nil
	}
	if in.People < 1 || in.People > 100 {
		return botJSON(map[string]any{"error": "número de personas inválido"}), nil
	}
	resTime, err := ensureHHMMSS(strings.TrimSpace(in.Time))
	if err != nil {
		return botJSON(map[string]any{"error": "hora inválida, usa HH:MM"}), nil
	}
	if in.RiceType != "" && in.RiceServings > 0 && in.RiceServings < 2 {
		return botJSON(map[string]any{"error": "mínimo 2 raciones de arroz"}), nil
	}

	// Day must be open (server-side, not just via LLM instruction).
	if sched, serr := s.botResolveDaySchedule(ctx, restaurantID, dateISO); serr == nil && !sched.Open {
		return botJSON(map[string]any{"error": "el restaurante no abre ese día; propón otra fecha"}), nil
	}

	// Capacity check.
	limit, total, err := s.botDayCapacity(ctx, restaurantID, dateISO)
	if err != nil {
		return botJSON(map[string]any{"error": "error consultando capacidad"}), nil
	}
	if over, free := botOverCapacity(limit, total, 0, in.People); over {
		return botJSON(map[string]any{"error": "no hay disponibilidad suficiente para esa fecha", "free_seats": free}), nil
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = msg.PushName
	}
	if name == "" {
		name = "Cliente WhatsApp"
	}

	// Phone: sender is E.164 digits (e.g. 34612345678); store national part.
	digits := digitsOnly(msg.Sender)
	cc := "34"
	national := digits
	if strings.HasPrefix(digits, "34") && len(digits) == 11 {
		national = digits[2:]
	} else if len(digits) > 9 {
		cc = digits[:len(digits)-9]
		national = digits[len(digits)-9:]
	}

	var arrozTypeJSON, arrozServingsJSON any
	if in.RiceType != "" {
		servings := in.RiceServings
		if servings < 2 {
			servings = max(2, in.People)
		}
		bt, _ := json.Marshal([]string{in.RiceType})
		bs, _ := json.Marshal([]int{servings})
		arrozTypeJSON = string(bt)
		arrozServingsJSON = string(bs)
	}

	contactEmail := s.restaurantFallbackEmail(ctx, restaurantID)

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO bookings (
			restaurant_id, reservation_date, party_size, children, reservation_time,
			customer_name, contact_phone, contact_phone_country_code, commentary,
			arroz_type, arroz_servings, babyStrollers, highChairs, contact_email
		) VALUES (?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, restaurantID, dateISO, in.People, resTime, name, national, cc,
		strings.TrimSpace(in.Commentary), arrozTypeJSON, arrozServingsJSON,
		clampBotInt(in.BabyStrollers, 0, 3), clampBotInt(in.HighChairs, 0, 3), contactEmail)
	if err != nil {
		return botJSON(map[string]any{"error": "error creando la reserva"}), nil
	}
	bookingID, _ := res.LastInsertId()

	return botJSON(map[string]any{
		"created":    true,
		"booking_id": bookingID,
		"date":       dateISO,
		"time":       resTime[:5],
		"people":     in.People,
		"name":       name,
	}), nil
}

func (s *Server) botToolCancelBooking(ctx context.Context, restaurantID int, phone string, input json.RawMessage) (string, error) {
	var in struct {
		BookingID int64 `json:"booking_id"`
		Confirmed bool  `json:"confirmed"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.BookingID <= 0 {
		return botJSON(map[string]any{"error": "booking_id inválido"}), nil
	}
	if !in.Confirmed {
		return botJSON(map[string]any{"error": "requiere confirmed=true tras confirmar con el cliente"}), nil
	}
	owned, err := s.botBookingBelongsToPhone(ctx, restaurantID, in.BookingID, phone)
	if err != nil {
		return botJSON(map[string]any{"error": "error verificando la reserva"}), nil
	}
	if !owned {
		return botJSON(map[string]any{"error": "reserva no encontrada para este teléfono"}), nil
	}

	err = withTx(ctx, s.db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO cancelled_bookings
				(restaurant_id, booking_id, reservation_date, party_size, reservation_time, customer_name,
				 contact_phone, contact_email, commentary, arroz_type, arroz_servings,
				 babyStrollers, highChairs, cancellation_date, cancelled_by)
			SELECT restaurant_id, id, reservation_date, party_size, reservation_time, customer_name,
				 contact_phone, contact_email, commentary, arroz_type, arroz_servings,
				 COALESCE(babyStrollers,0), COALESCE(highChairs,0), NOW(), 'whatsapp'
			FROM bookings WHERE id = ? AND restaurant_id = ?
		`, in.BookingID, restaurantID)
		if err != nil {
			return err
		}
		// Release the reserved floor/salon headcount before deleting.
		var wDate sql.NullString
		var wParty sql.NullInt64
		var wFloor, wSalon sql.NullInt64
		if qErr := tx.QueryRowContext(ctx, `
			SELECT DATE_FORMAT(reservation_date, '%Y-%m-%d'), party_size, preferred_floor_number, preferred_salon_id
			FROM bookings WHERE id = ? AND restaurant_id = ?
		`, in.BookingID, restaurantID).Scan(&wDate, &wParty, &wFloor, &wSalon); qErr == nil && wDate.Valid && wParty.Valid {
			if occErr := s.applyBookingLocationOccupancy(ctx, tx, restaurantID, wDate.String, wFloor, wSalon, int(wParty.Int64), -1); occErr != nil {
				return occErr
			}
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM bookings WHERE id = ? AND restaurant_id = ?`, in.BookingID, restaurantID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return sql.ErrNoRows
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return botJSON(map[string]any{"error": "reserva no encontrada"}), nil
		}
		return botJSON(map[string]any{"error": "error cancelando la reserva"}), nil
	}
	return botJSON(map[string]any{"cancelled": true, "booking_id": in.BookingID}), nil
}

func (s *Server) botToolModifyBooking(ctx context.Context, restaurantID int, phone string, input json.RawMessage) (string, error) {
	var in struct {
		BookingID     int64  `json:"booking_id"`
		Date          string `json:"date"`
		Time          string `json:"time"`
		People        int    `json:"people"`
		RiceType      string `json:"rice_type"`
		RiceServings  int    `json:"rice_servings"`
		ClearRice     bool   `json:"clear_rice"`
		HighChairs    *int   `json:"high_chairs"`
		BabyStrollers *int   `json:"baby_strollers"`
		Confirmed     bool   `json:"confirmed"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.BookingID <= 0 {
		return botJSON(map[string]any{"error": "booking_id inválido"}), nil
	}
	if !in.Confirmed {
		return botJSON(map[string]any{"error": "requiere confirmed=true tras confirmar con el cliente"}), nil
	}
	owned, err := s.botBookingBelongsToPhone(ctx, restaurantID, in.BookingID, phone)
	if err != nil {
		return botJSON(map[string]any{"error": "error verificando la reserva"}), nil
	}
	if !owned {
		return botJSON(map[string]any{"error": "reserva no encontrada para este teléfono"}), nil
	}

	sets := []string{}
	args := []any{}
	newDateISO := ""     // set when the date is being changed
	newPeople := 0       // set (>0) when party size is being changed
	resTimeStored := ""  // set when the time is being changed
	riceTypeStored := "" // set when rice is being changed
	riceServingsStored := ""
	if in.Date != "" {
		dateISO, err := parseBotDate(in.Date)
		if err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		if dateISO <= time.Now().Format("2006-01-02") {
			return botJSON(map[string]any{"error": "la nueva fecha debe ser futura"}), nil
		}
		newDateISO = dateISO
		sets = append(sets, "reservation_date = ?")
		args = append(args, dateISO)
	}
	if in.Time != "" {
		resTime, err := ensureHHMMSS(strings.TrimSpace(in.Time))
		if err != nil {
			return botJSON(map[string]any{"error": "hora inválida"}), nil
		}
		sets = append(sets, "reservation_time = ?")
		args = append(args, resTime)
		resTimeStored = resTime
	}
	if in.People > 0 {
		if in.People > 100 {
			return botJSON(map[string]any{"error": "número de personas inválido"}), nil
		}
		newPeople = in.People
		sets = append(sets, "party_size = ?")
		args = append(args, in.People)
	}
	if in.ClearRice {
		sets = append(sets, "arroz_type = NULL", "arroz_servings = NULL")
	} else if in.RiceType != "" {
		servings := in.RiceServings
		if servings < 2 {
			servings = 2
		}
		bt, _ := json.Marshal([]string{in.RiceType})
		bs, _ := json.Marshal([]int{servings})
		sets = append(sets, "arroz_type = ?", "arroz_servings = ?")
		args = append(args, string(bt), string(bs))
		riceTypeStored = string(bt)
		riceServingsStored = string(bs)
	}
	if in.HighChairs != nil {
		sets = append(sets, "highChairs = ?")
		args = append(args, clampBotInt(*in.HighChairs, 0, 3))
	}
	if in.BabyStrollers != nil {
		sets = append(sets, "babyStrollers = ?")
		args = append(args, clampBotInt(*in.BabyStrollers, 0, 3))
	}
	if len(sets) == 0 {
		return botJSON(map[string]any{"error": "no se ha indicado ningún cambio"}), nil
	}

	// Moving the date or growing the party must re-validate day-open + capacity,
	// exactly like create_booking, so a modification can't overbook or land on a
	// closed day.
	if newDateISO != "" || newPeople > 0 {
		var curDate string
		var curParty int
		if err := s.db.QueryRowContext(ctx,
			"SELECT reservation_date, party_size FROM bookings WHERE id = ? AND restaurant_id = ?",
			in.BookingID, restaurantID).Scan(&curDate, &curParty); err != nil {
			return botJSON(map[string]any{"error": "error verificando la reserva"}), nil
		}
		effDate := curDate
		if newDateISO != "" {
			effDate = newDateISO
		}
		effPeople := curParty
		if newPeople > 0 {
			effPeople = newPeople
		}
		if sched, serr := s.botResolveDaySchedule(ctx, restaurantID, effDate); serr == nil && !sched.Open {
			return botJSON(map[string]any{"error": "el restaurante no abre ese día; propón otra fecha"}), nil
		}
		limit, total, err := s.botDayCapacity(ctx, restaurantID, effDate)
		if err != nil {
			return botJSON(map[string]any{"error": "error consultando capacidad"}), nil
		}
		// This booking's own seats are already counted in `total` when it stays on
		// the same date; exclude them so we measure the *net* change.
		existing := 0
		if curDate == effDate {
			existing = curParty
		}
		if over, free := botOverCapacity(limit, total, existing, effPeople); over {
			return botJSON(map[string]any{"error": "no hay disponibilidad suficiente para esa fecha", "free_seats": free}), nil
		}
	}

	args = append(args, in.BookingID, restaurantID)

	// Snapshot the pre-update values so every applied change is recorded for
	// the "Modificadas" tab. Coordination id: booking-modification-recorded.
	var snap struct {
		date, time, customerName, contactPhone, riceType, riceServings string
		party, strollers, highChairs                                   int
	}
	var riceType, riceServings sql.NullString
	snapErr := s.db.QueryRowContext(ctx, `
		SELECT DATE_FORMAT(reservation_date, '%Y-%m-%d'),
		       TIME_FORMAT(reservation_time, '%H:%i:%s'),
		       party_size, customer_name, contact_phone,
		       arroz_type, arroz_servings, babyStrollers, highChairs
		FROM bookings WHERE id = ? AND restaurant_id = ?
	`, in.BookingID, restaurantID).Scan(
		&snap.date, &snap.time, &snap.party, &snap.customerName, &snap.contactPhone,
		&riceType, &riceServings, &snap.strollers, &snap.highChairs)
	if snapErr != nil {
		return botJSON(map[string]any{"error": "error verificando la reserva"}), nil
	}
	snap.riceType = riceType.String
	snap.riceServings = riceServings.String

	query := fmt.Sprintf("UPDATE bookings SET %s WHERE id = ? AND restaurant_id = ?", strings.Join(sets, ", "))
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		return botJSON(map[string]any{"error": "error modificando la reserva"}), nil
	}

	// Record tracked changes with the WhatsApp attribution for the tab.
	const botActor = "whatsapp"
	const botActorName = "Asistente WhatsApp"
	record := func(field, oldVal, newVal string) {
		s.insertBookingModification(ctx, restaurantID, int(in.BookingID), snap.date, field,
			oldVal, newVal, botActor, nil, botActorName, snap.customerName, snap.contactPhone)
	}
	if newDateISO != "" {
		record("date", snap.date, newDateISO)
	}
	if in.Time != "" {
		record("time", snap.time, resTimeStored)
	}
	if newPeople > 0 {
		record("party_size", fmt.Sprintf("%d", snap.party), fmt.Sprintf("%d", in.People))
	}
	if in.ClearRice || in.RiceType != "" {
		newType, newServ := "", ""
		if !in.ClearRice {
			newType, newServ = riceTypeStored, riceServingsStored
		}
		record("rice", fmt.Sprintf("%v|%v", snap.riceType, snap.riceServings), newType+"|"+newServ)
	}
	if in.BabyStrollers != nil {
		record("strollers", fmt.Sprintf("%d", snap.strollers), fmt.Sprintf("%d", clampBotInt(*in.BabyStrollers, 0, 3)))
	}
	if in.HighChairs != nil {
		record("high_chairs", fmt.Sprintf("%d", snap.highChairs), fmt.Sprintf("%d", clampBotInt(*in.HighChairs, 0, 3)))
	}
	return botJSON(map[string]any{"modified": true, "booking_id": in.BookingID}), nil
}

func (s *Server) botBookingBelongsToPhone(ctx context.Context, restaurantID int, bookingID int64, phone string) (bool, error) {
	digits := digitsOnly(phone)
	national := digits
	if strings.HasPrefix(digits, "34") && len(digits) == 11 {
		national = digits[2:]
	}
	var one int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM bookings
		WHERE id = ? AND restaurant_id = ?
			AND (contact_phone = ? OR contact_phone = ? OR CONCAT(COALESCE(contact_phone_country_code,''), contact_phone) = ?)
		LIMIT 1
	`, bookingID, restaurantID, national, digits, digits).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func clampBotInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// botOverCapacity decides whether adding `want` seats exceeds the day limit,
// given the day's current booked `total` and the seats (`existing`) already
// counted in `total` for the booking being modified (0 for a new booking).
// Returns the exceeded flag and the number of still-free seats.
func botOverCapacity(limit, total, existing, want int) (over bool, free int) {
	free = maxInt(0, limit-(total-existing))
	over = limit <= 0 || (total-existing+want) > limit
	return over, free
}

// botMemberForPhone resolves an active employee by either phone field. The
// comparison is done in Go because existing installations store numbers in
// national and E.164 formats inconsistently.
func (s *Server) botMemberForPhone(ctx context.Context, restaurantID int, phone string) (int64, string, error) {
	want := digitsOnly(phone)
	rows, err := s.db.QueryContext(ctx, `SELECT id, first_name, last_name, COALESCE(phone,''), COALESCE(whatsapp_number,'') FROM restaurant_members WHERE restaurant_id=? AND is_active=1`, restaurantID)
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var first, last, p, wp string
		if err := rows.Scan(&id, &first, &last, &p, &wp); err != nil {
			return 0, "", err
		}
		for _, candidate := range []string{p, wp} {
			d := digitsOnly(candidate)
			if d != "" && (d == want || strings.TrimPrefix(d, "34") == strings.TrimPrefix(want, "34")) {
				return id, strings.TrimSpace(first + " " + last), nil
			}
		}
	}
	return 0, "", nil
}

func botMemberDateRange(input json.RawMessage) (string, string, error) {
	var in struct {
		Week     string `json:"week"`
		Date     string `json:"date"`
		DateFrom string `json:"date_from"`
		DateTo   string `json:"date_to"`
		FromDate string `json:"from_date"`
		ToDate   string `json:"to_date"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", "", errors.New("parámetros inválidos")
	}
	// A week is always Monday-Sunday in the restaurant's local calendar.
	// Explicit dates remain supported for backwards compatibility and take
	// precedence when supplied alongside week.
	fromRaw := strings.TrimSpace(in.DateFrom)
	if fromRaw == "" {
		fromRaw = strings.TrimSpace(in.FromDate)
	}
	toRaw := strings.TrimSpace(in.DateTo)
	if toRaw == "" {
		toRaw = strings.TrimSpace(in.ToDate)
	}
	if fromRaw == "" {
		fromRaw = strings.TrimSpace(in.Date)
	}
	if fromRaw == "" && toRaw == "" && strings.TrimSpace(in.Week) != "" {
		week := strings.ToLower(strings.TrimSpace(in.Week))
		if week != "current" && week != "next" {
			return "", "", errors.New(`week debe ser "current" o "next"`)
		}
		now := time.Now()
		mondayOffset := (int(now.Weekday()) + 6) % 7
		monday := now.AddDate(0, 0, -mondayOffset)
		if week == "next" {
			monday = monday.AddDate(0, 0, 7)
		}
		return monday.Format("2006-01-02"), monday.AddDate(0, 0, 6).Format("2006-01-02"), nil
	}
	if fromRaw == "" {
		fromRaw = time.Now().Format("2006-01-02")
	}
	if toRaw == "" {
		toRaw = fromRaw
	}
	var err error
	from, err := parseBotDate(fromRaw)
	if err != nil {
		return "", "", err
	}
	to, err := parseBotDate(toRaw)
	if err != nil {
		return "", "", err
	}
	if from > to {
		return "", "", errors.New("date_from no puede ser posterior a date_to")
	}
	return from, to, nil
}

func (s *Server) botToolMemberSchedule(ctx context.Context, restaurantID int, phone string, input json.RawMessage) (string, error) {
	id, name, err := s.botMemberForPhone(ctx, restaurantID, phone)
	if err != nil {
		return botJSON(map[string]any{"error": "error consultando empleado"}), nil
	}
	if id == 0 {
		return botJSON(map[string]any{"error": "no hay un empleado activo asociado a este teléfono"}), nil
	}
	from, to, err := botMemberDateRange(input)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, DATE_FORMAT(work_date,'%Y-%m-%d'), TIME_FORMAT(start_time,'%H:%i'), TIME_FORMAT(end_time,'%H:%i'), COALESCE(notes,'') FROM member_work_schedules WHERE restaurant_id=? AND restaurant_member_id=? AND work_date BETWEEN ? AND ? ORDER BY work_date,start_time,id LIMIT 100`, restaurantID, id, from, to)
	if err != nil {
		return botJSON(map[string]any{"error": "error consultando horario"}), nil
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var sid int64
		var date, start, end, note string
		if err := rows.Scan(&sid, &date, &start, &end, &note); err != nil {
			return botJSON(map[string]any{"error": "error leyendo horario"}), nil
		}
		out = append(out, map[string]any{"id": sid, "date": date, "start_time": start, "end_time": end, "notes": note})
	}
	return botJSON(map[string]any{"member": name, "date_from": from, "date_to": to, "schedules": out, "count": len(out)}), nil
}

func (s *Server) botToolMemberAttendance(ctx context.Context, restaurantID int, phone string, input json.RawMessage) (string, error) {
	id, name, err := s.botMemberForPhone(ctx, restaurantID, phone)
	if err != nil {
		return botJSON(map[string]any{"error": "error consultando empleado"}), nil
	}
	if id == 0 {
		return botJSON(map[string]any{"error": "no hay un empleado activo asociado a este teléfono"}), nil
	}
	from, to, err := botMemberDateRange(input)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,DATE_FORMAT(work_date,'%Y-%m-%d'),COALESCE(TIME_FORMAT(start_time,'%H:%i'),''),COALESCE(TIME_FORMAT(end_time,'%H:%i'),''),minutes_worked,COALESCE(source,''),COALESCE(notes,'') FROM member_time_entries WHERE restaurant_id=? AND restaurant_member_id=? AND work_date BETWEEN ? AND ? ORDER BY work_date,start_time,id LIMIT 100`, restaurantID, id, from, to)
	if err != nil {
		return botJSON(map[string]any{"error": "error consultando asistencia"}), nil
	}
	defer rows.Close()
	out := []map[string]any{}
	total := 0
	for rows.Next() {
		var eid int64
		var date, start, end, source, note string
		var mins int
		if err := rows.Scan(&eid, &date, &start, &end, &mins, &source, &note); err != nil {
			return botJSON(map[string]any{"error": "error leyendo asistencia"}), nil
		}
		total += mins
		out = append(out, map[string]any{"id": eid, "date": date, "start_time": start, "end_time": end, "minutes_worked": mins, "source": source, "notes": note})
	}
	return botJSON(map[string]any{"member": name, "date_from": from, "date_to": to, "entries": out, "count": len(out), "total_minutes": total}), nil
}

func (s *Server) botToolMemberAccess(ctx context.Context, restaurantID int, phone string) (string, error) {
	id, name, err := s.botMemberForPhone(ctx, restaurantID, phone)
	if err != nil {
		return botJSON(map[string]any{"error": "error consultando empleado"}), nil
	}
	if id == 0 {
		return botJSON(map[string]any{"error": "no hay un empleado activo asociado a este teléfono"}), nil
	}
	var userID sql.NullInt64
	var email sql.NullString
	var must sql.NullBool
	err = s.db.QueryRowContext(ctx, `SELECT m.bo_user_id,COALESCE(u.email,''),COALESCE(u.must_change_password,0) FROM restaurant_members m LEFT JOIN bo_users u ON u.id=m.bo_user_id WHERE m.restaurant_id=? AND m.id=?`, restaurantID, id).Scan(&userID, &email, &must)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return botJSON(map[string]any{"error": "error consultando acceso"}), nil
	}
	return botJSON(map[string]any{"member": name, "has_access": userID.Valid && userID.Int64 > 0, "email": email.String, "must_change_password": must.Bool, "password": nil}), nil
}

// botHandleAttendanceCommand executes the small, deliberately non-LLM clock
// command surface exposed over WhatsApp. Exact commands only are accepted.
// Returning false means the message belongs to the normal conversational bot.
// botAttendanceCommand recognizes the deliberately small, exact command
// surface exposed over WhatsApp. It is kept independent from Server so command
// recognition can be tested without a database or provider connection.
func botAttendanceCommand(text string) (string, bool) {
	command := strings.ToLower(strings.TrimSpace(text))
	command = strings.TrimPrefix(command, "/")
	switch command {
	case "start", "iniciar":
		return "start", true
	case "stop", "detener":
		return "stop", true
	case "status", "estado", "fichaje":
		return "status", true
	default:
		return "", false
	}
}

func (s *Server) botHandleAttendanceCommand(ctx context.Context, restaurantID int, msg botWebhookMessage) bool {
	command, ok := botAttendanceCommand(msg.Text)
	if !ok {
		return false
	}
	id, _, err := s.botMemberForPhone(ctx, restaurantID, msg.Sender)
	if err != nil || id == 0 {
		s.botSendAttendanceReply(ctx, restaurantID, msg.Sender, "No hay un empleado activo asociado a este teléfono.")
		return true
	}
	entitled, entitlementErr := s.hasActiveRecurringFeature(ctx, restaurantID, boPremiumWhatsAppFeatureKey)
	if entitlementErr != nil || !entitled {
		s.botSendAttendanceReply(ctx, restaurantID, msg.Sender, "El fichaje por WhatsApp no está habilitado para este restaurante.")
		return true
	}
	var verified bool
	if err := s.db.QueryRowContext(ctx, `SELECT whatsapp_verified_at IS NOT NULL FROM restaurant_members WHERE restaurant_id=? AND id=? AND is_active=1`, restaurantID, id).Scan(&verified); err != nil || !verified {
		s.botSendAttendanceReply(ctx, restaurantID, msg.Sender, "Debes verificar tu número de WhatsApp antes de fichar.")
		return true
	}
	// Serialize check-then-write operations, as the backoffice handlers do.
	s.fichajeMu.Lock()
	defer s.fichajeMu.Unlock()
	var entryID int64
	var workDate, start string
	err = s.db.QueryRowContext(ctx, `SELECT id, DATE_FORMAT(work_date,'%Y-%m-%d'), TIME_FORMAT(start_time,'%H:%i')
		FROM member_time_entries WHERE restaurant_id=? AND restaurant_member_id=? AND end_time IS NULL ORDER BY id DESC LIMIT 1`, restaurantID, id).Scan(&entryID, &workDate, &start)
	active := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.botSendAttendanceReply(ctx, restaurantID, msg.Sender, "No se ha podido consultar el fichaje.")
		return true
	}

	switch command {
	case "start", "iniciar":
		if active {
			s.botSendAttendanceReply(ctx, restaurantID, msg.Sender, "Ya tienes un fichaje activo desde "+start+".")
			return true
		}
		now := time.Now().In(boMadridTZ)
		res, e := s.db.ExecContext(ctx, `INSERT INTO member_time_entries (restaurant_member_id,restaurant_id,work_date,start_time,end_time,minutes_worked,source) VALUES (?,?,?,?,NULL,0,'whatsapp')`, id, restaurantID, now.Format("2006-01-02"), now.Format("15:04:05"))
		if e != nil {
			s.botSendAttendanceReply(ctx, restaurantID, msg.Sender, "No se ha podido iniciar el fichaje.")
		} else {
			s.botSendAttendanceReply(ctx, restaurantID, msg.Sender, "Fichaje iniciado a las "+now.Format("15:04")+".")
			_ = res
		}
	case "stop", "detener":
		if !active {
			s.botSendAttendanceReply(ctx, restaurantID, msg.Sender, "No tienes un fichaje activo.")
			return true
		}
		now := time.Now().In(boMadridTZ)
		_, e := s.db.ExecContext(ctx, `UPDATE member_time_entries SET end_time=?, minutes_worked=GREATEST(0,TIMESTAMPDIFF(MINUTE,CONCAT(work_date,' ',start_time),?)), source='whatsapp' WHERE id=? AND restaurant_id=? AND restaurant_member_id=? AND end_time IS NULL`, now.Format("15:04:05"), now.Format("2006-01-02 15:04:05"), entryID, restaurantID, id)
		if e != nil {
			s.botSendAttendanceReply(ctx, restaurantID, msg.Sender, "No se ha podido detener el fichaje.")
		} else {
			s.botSendAttendanceReply(ctx, restaurantID, msg.Sender, "Fichaje detenido a las "+now.Format("15:04")+".")
		}
	case "status", "estado", "fichaje":
		if active {
			s.botSendAttendanceReply(ctx, restaurantID, msg.Sender, "Tienes un fichaje activo desde "+start+" del "+workDate+".")
		} else {
			s.botSendAttendanceReply(ctx, restaurantID, msg.Sender, "No tienes un fichaje activo.")
		}
	}
	return true
}

func (s *Server) botSendAttendanceReply(ctx context.Context, restaurantID int, sender, text string) {
	if gw, ok := s.botGatewayFor(ctx, restaurantID); ok && s.sendWhatsAppTextTracked(ctx, restaurantID, gw, sender, text, "attendance") == nil {
	}
}
