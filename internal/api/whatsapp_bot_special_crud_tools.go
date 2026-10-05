package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// WhatsApp bot CRUD tools for special-menu days (e.g. Christmas, New Year).
//
// Coordination id: wa_bot_special_crud_v1
//
// The bot can answer AND act on a special date ("crea una reserva para el 25
// con dos menús deaini", "cámbiala a las 22:00", "anula la del 25"). Today the
// policy (wa_bot_special_date_policy_v1) sent every special-date request to
// the website, which left staff doing the work by hand from the very group
// the bot already lives in. These tools close that gap.
//
// They are the client-SDK surface for the whole special booking lifecycle and
// deliberately reuse the exact server cores instead of re-implementing them:
//
//	create_special_booking  -> boNormalizeAndValidateBookingInput +
//	                           boInsertBooking (same validation + snapshot as
//	                           the backoffice and the public form)
//	modify_special_booking  -> resolveSpecialBookingInput + the same UPDATE the
//	                           backoffice PATCH issues
//	cancel_special_booking  -> the same cancelled_bookings archive + delete
//	                           transaction the backoffice uses
//
// So a booking written from a WhatsApp group is indistinguishable from one
// written in the backoffice: same snapshot, same is_prereserva, same
// is_special_booking flags, same totals, same notifications.
//
// Security: every tool is tenant-scoped by restaurantID and, in a group, the
// actor is the member who mentioned the bot (msg.ParticipantJID). Ownership is
// verified against that phone, so a group member can only touch bookings that
// belong to the phone they are writing from. The tools are only offered in a
// group (msg.IsGroup) and when the tenant has not disabled them, because they
// are a management surface, not a customer one.

// botSpecialCRUDEnabled reports whether the tenant may use the special-menu CRUD
// tools. A restaurant that prefers the website-only flow opts out in
// whatsapp_bot_config.config_json with "disable_special_crud": true.
func botSpecialCRUDEnabled(tenant botTenantConfig) bool {
	return !tenant.DisableSpecialCRUD
}

// botSpecialCRUDActorPhone is the identity a group turn acts as: the member
// that mentioned the bot. For 1:1 turns it is the customer sender, but the
// tools are gated to groups, so this is effectively the staff member's phone.
func botSpecialCRUDActorPhone(msg botWebhookMessage) string {
	if msg.ParticipantJID != "" {
		if p := botGroupParticipantPhone(msg.ParticipantJID); p != "" {
			return p
		}
	}
	if msg.IsGroup {
		// A group message without a resolvable participant has no identity:
		// refuse rather than acting on behalf of the group itself.
		return ""
	}
	return msg.Sender
}

// botSpecialCRUDGate is the single entry point every special CRUD tool goes
// through. It enforces the group-only + enabled rules so no individual tool can
// forget them. It returns (result, blocked): blocked=true means the returned
// string is the error to hand back to the model and the tool must not run.
func (s *Server) botSpecialCRUDGate(msg botWebhookMessage, tenant botTenantConfig, name string) (string, bool) {
	if !botSpecialCRUDEnabled(tenant) {
		return botJSON(map[string]any{
			"error":       "la gestión de menús especiales por WhatsApp está desactivada en este restaurante",
			"tool":        name,
			"instruction": "Informa al cliente de que esta gestión la hace el equipo del restaurante.",
		}), true
	}
	if !msg.IsGroup {
		// Customers never mutate a special booking from a 1:1 chat: that
		// flow keeps handing over to management (wa_bot_special_date_policy_v1).
		return botJSON(map[string]any{
			"error":       "las reservas de menú especial solo se gestionan desde el grupo del restaurante, no en un chat privado",
			"tool":        name,
			"instruction": "Deriva la gestión al equipo: envía la solicitud con send_contact o indica el enlace de la web.",
		}), true
	}
	if botSpecialCRUDActorPhone(msg) == "" {
		return botJSON(map[string]any{
			"error":       "no se ha podido identificar quién escribe en el grupo",
			"tool":        name,
			"instruction": "Pide que repitan la petición desde un número de WhatsApp identificado.",
		}), true
	}
	// Not blocked: the caller proceeds with the tool.
	return "", false
}

// botToolCreateSpecialBooking creates a special-menu-day booking (e.g. 25
// December). Reuses the backoffice normalization so the snapshot, the
// prereserva flag and the online-only section rules match the web form
// exactly.
func (s *Server) botToolCreateSpecialBooking(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig, input json.RawMessage) (string, error) {
	if out, blocked := s.botSpecialCRUDGate(msg, tenant, "create_special_booking"); blocked {
		return out, nil
	}
	var in struct {
		Date      string             `json:"date"`
		Time      string             `json:"time"`
		People    int                `json:"people"`
		Name      string             `json:"name"`
		Phone     string             `json:"phone"`
		Comment   string             `json:"commentary"`
		Special   *specialBookingReq `json:"special"`
		Confirmed bool               `json:"confirmed"`
		RawMenus  json.RawMessage    `json:"menus"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return botJSON(map[string]any{"error": "parámetros inválidos"}), nil
	}
	if !in.Confirmed {
		return botJSON(map[string]any{"error": "requiere confirmed=true tras repetir los datos al grupo"}), nil
	}
	dateISO, err := parseBotDate(in.Date)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	// The date must actually be a special date: never let the tool book a
	// regular day through the special path.
	settings, _, sErr := s.loadSpecialDateSettings(ctx, restaurantID, dateISO)
	if sErr != nil {
		return botJSON(map[string]any{"error": "no se pudo consultar la fecha especial"}), nil
	}
	if settings == nil {
		return botJSON(map[string]any{"error": "no hay fecha especial activa para " + dateISO}), nil
	}
	if in.People < 1 {
		return botJSON(map[string]any{"error": "número de personas inválido"}), nil
	}
	phone := botSpecialCRUDActorPhone(msg)
	if p := strings.TrimSpace(in.Phone); p != "" {
		phone = p
	}
	cc, national, _, ok := normalizePhoneParts("", phone)
	if !ok {
		return botJSON(map[string]any{"error": "teléfono inválido"}), nil
	}
	comment := strings.TrimSpace(in.Comment)
	email := s.restaurantFallbackEmail(ctx, restaurantID)

	specialReq, errMsg := botSpecialBookingReqFromInput(&in.Special, in.RawMenus)
	if errMsg != "" {
		return botJSON(map[string]any{"error": errMsg}), nil
	}
	if specialReq == nil {
		return botJSON(map[string]any{"error": "indica los menús especiales con special.menus (special_date_menu_id + count)"}), nil
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = strings.TrimSpace(msg.PushName)
	}
	if name == "" || name == "Cliente" {
		name = "Cliente WhatsApp"
	}
	// The group is a STAFF surface, so boNormalizeAndValidateBookingInput runs
	// with its normal backoffice semantics: resolveSpecialBookingInput is called
	// with onlineOnly=false, so a section the backoffice disabled for online
	// booking is still bookable here and the deposit may be recorded under any
	// canonical SPEC 2 method. That is the same permission a backoffice user
	// has, which is why this is correct for a group but would NOT be correct on
	// the public form.
	booking, err := s.boNormalizeAndValidateBookingInput(ctx, restaurantID, boNormalizeInput{
		ReservationDate:         dateISO,
		ReservationTime:         strings.TrimSpace(in.Time),
		PartySize:               in.People,
		CustomerName:            name,
		ContactPhone:            national,
		ContactPhoneCountryCode: cc,
		ContactEmail:            &email,
		Commentary:              &comment,
		Special:                 specialReq,
		SpecialTouched:          true,
	})
	if err != nil {
		log.Printf("[bot] checkpoint wa_bot_special_crud_v1 restaurant_id=%d tool=create_special_booking date=%s rejected=%v", restaurantID, dateISO, err)
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	id, err := s.boInsertBooking(ctx, restaurantID, booking)
	if err != nil {
		return botJSON(map[string]any{"error": "error creando la reserva especial"}), nil
	}
	// Same notifications as every other booking (WhatsApp + email), plus the
	// special summary / QR the festive flow expects.
	s.botNotifySpecialBooking(ctx, restaurantID, int64(id), booking)
	log.Printf("[bot] checkpoint wa_bot_special_crud_v1 restaurant_id=%d tool=create_special_booking booking_id=%d date=%s people=%d", restaurantID, id, dateISO, in.People)
	return botJSON(map[string]any{
		"created":            true,
		"booking_id":         id,
		"date":               dateISO,
		"time":               botClipTime(booking.ReservationTime),
		"people":             in.People,
		"name":               booking.CustomerName,
		"title":              settings.Title,
		"is_prereserva":      booking.IsPrereserva,
		"is_special_booking": true,
	}), nil
}

// botSpecialBookingReqFromInput accepts either the nested "special" object or
// a flat "menus" array, so the LLM does not have to guess the envelope.
func botSpecialBookingReqFromInput(special **specialBookingReq, rawMenus json.RawMessage) (*specialBookingReq, string) {
	if special != nil && *special != nil {
		return *special, ""
	}
	trimmed := strings.TrimSpace(string(rawMenus))
	if trimmed == "" || trimmed == "null" {
		return nil, ""
	}
	// A bare array is the menus list itself; an object may be the whole
	// "special" block. Accept both so the LLM does not have to guess.
	if strings.HasPrefix(trimmed, "[") {
		var menus []specialBookingMenuReq
		if err := json.Unmarshal([]byte(trimmed), &menus); err != nil {
			return nil, "menus inválido: debe ser una lista de {special_date_menu_id, count}"
		}
		return &specialBookingReq{Menus: menus}, ""
	}
	var req specialBookingReq
	if err := json.Unmarshal([]byte(trimmed), &req); err != nil {
		return nil, "menus inválido: debe ser una lista de {special_date_menu_id, count}"
	}
	return &req, ""
}

// botNotifySpecialBooking fires the standard booking notifications for a
// special booking created by the bot, reusing the backoffice notification path
// so the customer receives the same summary, adelanto balance and QR as a
// booking created from the backoffice.
func (s *Server) botNotifySpecialBooking(ctx context.Context, restaurantID int, bookingID int64, booking boNormalizedBooking) {
	data := boBookingToNotificationData(booking, int(bookingID))
	row, err := s.boFetchBookingByID(ctx, restaurantID, int(bookingID))
	if err != nil {
		log.Printf("[bot] checkpoint wa_bot_special_crud_v1 restaurant_id=%d booking_id=%d notify_fetch_failed=%v", restaurantID, bookingID, err)
		return
	}
	if toBool(row["is_special_booking"]) {
		data["is_special_booking"] = true
		data["is_prereserva"] = row["is_prereserva"]
		data["special"] = row["special"]
		if qr := s.ensureBookingQR(ctx, restaurantID, bookingID, anyToString(row["reservation_date"])); qr != nil {
			data[bookingQRKey] = qr
		}
	}
	if err := sendBookingWhatsAppToCustomer(ctx, s, restaurantID, data, bookingID); err != nil {
		log.Printf("[bot] checkpoint wa_bot_special_crud_v1 restaurant_id=%d booking_id=%d notify_whatsapp_failed=%v", restaurantID, bookingID, err)
	}
	s.broadcastBookingChanged(restaurantID, bookingID, "booking_created")
}

// botClipTime renders an HH:MM:SS reservation time as HH:MM.
func botClipTime(t string) string {
	t = strings.TrimSpace(t)
	if len(t) >= 5 {
		return t[:5]
	}
	return t
}

// botToolModifySpecialBooking changes an existing special booking (time, size,
// menus, commentary). It delegates to the real backoffice PATCH handler so the
// snapshot is re-validated, the occupancy ledger is reconciled, the
// "Modificadas" trail is written and the observers are broadcast — exactly what
// a backoffice edit does. Reuse over duplication (SOLID).
func (s *Server) botToolModifySpecialBooking(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig, input json.RawMessage) (string, error) {
	if out, blocked := s.botSpecialCRUDGate(msg, tenant, "modify_special_booking"); blocked {
		return out, nil
	}
	var in struct {
		BookingID int64              `json:"booking_id"`
		Date      string             `json:"date"`
		Time      string             `json:"time"`
		People    int                `json:"people"`
		Comment   string             `json:"commentary"`
		Phone     string             `json:"phone"`
		Special   *specialBookingReq `json:"special"`
		Confirmed bool               `json:"confirmed"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.BookingID <= 0 {
		return botJSON(map[string]any{"error": "booking_id inválido"}), nil
	}
	if !in.Confirmed {
		return botJSON(map[string]any{"error": "requiere confirmed=true tras repetir los cambios al grupo"}), nil
	}
	target := botSpecialCRUDTargetPhone(msg, in.Phone)
	owned, err := s.botSpecialBookingIsOwned(ctx, restaurantID, in.BookingID, target)
	if err != nil {
		return botJSON(map[string]any{"error": "error verificando la reserva"}), nil
	}
	if !owned {
		// Never confirm the existence of a booking the actor may not touch.
		return botJSON(map[string]any{
			"error":       "no hay ninguna reserva especial con ese id para este teléfono",
			"booking_id":  in.BookingID,
			"instruction": "Usa get_special_date_bookings para listar las reservas de esa fecha antes de modificar.",
		}), nil
	}
	ctx = botBOAuthContext(ctx, restaurantID)
	body := map[string]any{}
	if v := strings.TrimSpace(in.Date); v != "" {
		// Moving a special booking to another date re-validates the snapshot
		// against that date's settings (and clears it when the target date is
		// not special), which is exactly the backoffice PATCH behaviour.
		iso, dErr := parseBotDate(v)
		if dErr != nil {
			return botJSON(map[string]any{"error": dErr.Error()}), nil
		}
		body["reservation_date"] = iso
	}
	if v := strings.TrimSpace(in.Time); v != "" {
		body["reservation_time"] = v
	}
	if in.People > 0 {
		body["party_size"] = in.People
	}
	if v := strings.TrimSpace(in.Comment); v != "" {
		body["commentary"] = v
	}
	if in.Special != nil && len(in.Special.Menus) > 0 {
		body["special"] = in.Special
	}
	if len(body) == 0 {
		return botJSON(map[string]any{"error": "indica al menos un campo a cambiar (date, time, people, commentary o special)"}), nil
	}
	raw, code, err := s.assistantCallHandler(ctx, s.handleBOBookingPatch, assistantHandlerInput{
		Method:   "PATCH",
		URLParam: map[string]string{"id": fmt.Sprintf("%d", in.BookingID)},
		Body:     body,
	})
	if err != nil {
		return botJSON(map[string]any{"error": "error aplicando la modificación"}), nil
	}
	if code >= 400 {
		return s.botSpecialCRUDHandlerError("modify_special_booking", raw, code)
	}
	out, perr := botHandlerResponse("modify_special_booking", raw)
	if perr != nil {
		return botJSON(map[string]any{"error": "modificación aplicada"}), nil
	}
	log.Printf("[bot] checkpoint wa_bot_special_crud_v1 restaurant_id=%d tool=modify_special_booking booking_id=%d fields=%d", restaurantID, in.BookingID, len(body))
	return out, nil
}

// botToolCancelSpecialBooking cancels a special booking through the backoffice
// cancel handler, so the cancelled_bookings archive and the occupancy release
// happen exactly as they do from the backoffice.
func (s *Server) botToolCancelSpecialBooking(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig, input json.RawMessage) (string, error) {
	if out, blocked := s.botSpecialCRUDGate(msg, tenant, "cancel_special_booking"); blocked {
		return out, nil
	}
	var in struct {
		BookingID int64  `json:"booking_id"`
		Reason    string `json:"reason"`
		Phone     string `json:"phone"`
		Confirmed bool   `json:"confirmed"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.BookingID <= 0 {
		return botJSON(map[string]any{"error": "booking_id inválido"}), nil
	}
	if !in.Confirmed {
		return botJSON(map[string]any{"error": "requiere confirmed=true tras repetir la cancelación al grupo"}), nil
	}
	// Same target resolution as modify: staff cancel on behalf of a CUSTOMER,
	// so an explicit phone (quoted in the group message) wins over the member's
	// own number. Cancelling for someone else is the most common case, so the
	// two tools must behave identically here.
	owned, err := s.botSpecialBookingIsOwned(ctx, restaurantID, in.BookingID, botSpecialCRUDTargetPhone(msg, in.Phone))
	if err != nil {
		return botJSON(map[string]any{"error": "error verificando la reserva"}), nil
	}
	if !owned {
		return botJSON(map[string]any{
			"error":       "no hay ninguna reserva especial con ese id para este teléfono",
			"booking_id":  in.BookingID,
			"instruction": "Usa get_special_date_bookings para listar las reservas de esa fecha antes de cancelar.",
		}), nil
	}
	ctx = botBOAuthContext(ctx, restaurantID)
	raw, code, err := s.assistantCallHandler(ctx, s.handleBOBookingCancel, assistantHandlerInput{
		Method:   "POST",
		URLParam: map[string]string{"id": fmt.Sprintf("%d", in.BookingID)},
		Body:     map[string]any{"reason": strings.TrimSpace(in.Reason)},
	})
	if err != nil {
		return botJSON(map[string]any{"error": "error cancelando la reserva"}), nil
	}
	if code >= 400 {
		return s.botSpecialCRUDHandlerError("cancel_special_booking", raw, code)
	}
	out, perr := botHandlerResponse("cancel_special_booking", raw)
	if perr != nil {
		return botJSON(map[string]any{"error": "reserva cancelada", "booking_id": in.BookingID}), nil
	}
	log.Printf("[bot] checkpoint wa_bot_special_crud_v1 restaurant_id=%d tool=cancel_special_booking booking_id=%d", restaurantID, in.BookingID)
	return out, nil
}

// botSpecialBookingIsOwned reports whether the booking exists for the tenant
// AND belongs to the acting phone. In a group the acting phone is the member
// who mentioned the bot, so a member can only manage their own reservations —
// the same ownership rule the 1:1 tools apply (wa_bot_foreign_phone_policy_v1).
func (s *Server) botSpecialBookingIsOwned(ctx context.Context, restaurantID int, bookingID int64, phone string) (bool, error) {
	owned, err := s.botBookingBelongsToPhone(ctx, restaurantID, bookingID, phone)
	if err != nil {
		return false, err
	}
	if !owned {
		return false, nil
	}
	// Only special bookings are manageable through these tools.
	var isSpecial int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(is_special_booking, 0) FROM bookings WHERE restaurant_id = ? AND id = ?
	`, restaurantID, bookingID).Scan(&isSpecial); err != nil {
		return false, err
	}
	return isSpecial != 0, nil
}

// botSpecialCRUDHandlerError shapes a backoffice handler error into the tool
// result so the LLM can read the real reason instead of a generic failure.
func (s *Server) botSpecialCRUDHandlerError(tool string, raw []byte, code int) (string, error) {
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err == nil {
		if msg := strings.TrimSpace(anyToString(v["message"])); msg != "" {
			return botJSON(map[string]any{"error": msg, "tool": tool}), nil
		}
	}
	return botJSON(map[string]any{"error": "la operación no se ha podido completar", "tool": tool}), nil
}

// botBOAuthContext synthesizes the backoffice session context the reused
// handlers read (boAuthFromContext / restaurantID context key). The WhatsApp
// bot is already authenticated upstream: the webhook secret proved the caller,
// resolveBotRestaurantByProviderInstance resolved the tenant, and the tools are
// tenant-scoped and ownership-checked, so there is no staff session to carry.
// The synthesized auth only exists so the canonical handlers can run; it grants
// nothing beyond the restaurantID already fixed by the caller.
func botBOAuthContext(ctx context.Context, restaurantID int) context.Context {
	if _, ok := boAuthFromContext(ctx); ok {
		return ctx
	}
	ctx = withBOAuth(ctx, boAuth{ActiveRestaurantID: restaurantID, Role: "bot"})
	return withRestaurantID(ctx, restaurantID)
}

// botToolGetSpecialDateMenu is the read half of the CRUD set: it returns what
// can actually be booked for a date, including the per-section menus of a
// special-type menu and their principals, so the bot can build a valid
// special.menus[] payload without guessing ids.
//
// Coordination id: wa_bot_special_crud_v1
func (s *Server) botToolGetSpecialDateMenu(ctx context.Context, restaurantID int, input json.RawMessage) (string, error) {
	var in struct {
		Date string `json:"date"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return botJSON(map[string]any{"error": "parámetros inválidos"}), nil
	}
	date, ok := botNormaliseToolDate(in.Date)
	if !ok {
		return botJSON(map[string]any{"error": "fecha inválida; usa YYYY-MM-DD o dd/MM/yyyy"}), nil
	}
	settings, menus, err := s.loadSpecialDateSettings(ctx, restaurantID, date)
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo consultar la fecha especial"}), nil
	}
	if settings == nil {
		return botJSON(map[string]any{"error": "no hay fecha especial activa para " + date}), nil
	}
	accepted := make([]string, 0, len(settings.AdelantoPaymentMethods))
	for _, m := range settings.AdelantoPaymentMethods {
		if m = strings.TrimSpace(m); m != "" {
			accepted = append(accepted, m)
		}
	}
	out := make([]map[string]any, 0, len(menus))
	for _, m := range menus {
		entry := map[string]any{
			"special_date_menu_id": m.ID,
			"label":                firstNonEmpty(strings.TrimSpace(m.CustomTitle.String), strings.TrimSpace(m.MenuTitle)),
			"is_custom":            !m.MenuID.Valid,
		}
		if m.MenuID.Valid {
			entry["menu_id"] = m.MenuID.Int64
			entry["unit_price"] = m.MenuPrice
		}
		if m.AdelantoAmount.Valid {
			entry["adelanto_amount"] = m.AdelantoAmount.Float64
		}
		if settings.AdelantoUnified && settings.AdelantoUnifiedAmount != nil {
			entry["adelanto_amount"] = *settings.AdelantoUnifiedAmount
		}
		// Coordination id: special_date_section_menus_v1 - a special-type menu
		// is booked per section, so the bookable unit is the section.
		//
		// The full list is exposed, not just onlineSpecialDateMenuSections:
		// create_special_booking runs with backoffice (onlineOnly=false)
		// semantics, so filtering here would hide sections the caller IS
		// allowed to book and could report a date with only offline sections as
		// having no menus at all. Each section still carries its own
		// "online_enabled" flag so the model can prefer the online ones.
		if m.MenuID.Valid && m.MenuType == "special" {
			sections := s.loadSpecialDateMenuSections(ctx, restaurantID, m.ID, m.MenuID.Int64, true)
			if len(sections) == 0 {
				continue
			}
			entry["sections"] = sections
			out = append(out, entry)
			continue
		}
		out = append(out, entry)
	}
	payload := map[string]any{
		"date":                     date,
		"title":                    settings.Title,
		"prereserva_enabled":       settings.PrereservaEnabled,
		"requires_adelanto":        settings.RequiresAdelanto,
		"adelanto_payment_methods": accepted,
		"menus":                    out,
		"usage":                    "para reservar usa create_special_booking con special.menus=[{\"special_date_menu_id\":<id>,\"count\":<comensales>}] (o sections[] para un menú por secciones)",
	}
	return botJSON(payload), nil
}

// botSpecialCRUDTargetPhone resolves whose booking a group turn acts on. Staff
// write in the group on behalf of a CUSTOMER, so an explicit `phone` (the
// customer's number quoted in the message) wins over the member's own number,
// which is the fallback. The booking is still looked up by phone and scoped to
// the tenant, so this can only ever resolve bookings that really exist.
func botSpecialCRUDTargetPhone(msg botWebhookMessage, explicit string) string {
	if p := botGroupParticipantPhone(explicit); p != "" {
		return p
	}
	return botSpecialCRUDActorPhone(msg)
}
