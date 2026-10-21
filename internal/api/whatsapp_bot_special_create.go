package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// WhatsApp bot proposal+OK flow for creating SPECIAL-date bookings from the
// privileged group.
//
// Coordination id: wa_bot_special_create_v1
//
// Until now the privileged group could read special bookings and propose
// adelanto / menus / reschedule on them, but it could not CREATE one: the
// direct create_special_booking tool stays inactive in the privileged group
// (weaker model, no proposal), so the admin turn had to refuse and send the
// staff to the panel. This closes the gap with the exact panel create
// semantics (handleBOBookingCreate):
//
//	propose  -> boNormalizeAndValidateBookingInput with Special+SpecialTouched
//	            (resolveSpecialBookingInput onlineOnly=false: backoffice
//	            semantics for sections and adelanto methods), duplicate guard,
//	            preview bound to the full resolved write set by after-hash.
//	execute  -> inside the claim transaction: re-validate, FOR UPDATE
//	            duplicate re-check, after-hash compare, the same INSERT column
//	            list boInsertBooking writes, occupancy +1, second validation
//	            before commit. After commit: the panel's notification block
//	            (WhatsApp summary + QR, confirmation emails), the
//	            booking.created broadcast and the n8n booking.created event.
//
// Unlike the NORMAL create (booking_propose_create), the panel DOES notify
// for special bookings, so this flow notifies too — and the preview says so
// verbatim, so the human OK covers the external messages explicitly.

// botSpecialCreateArgs are the validated inputs of a
// special_booking_propose_create proposal.
type botSpecialCreateArgs struct {
	DedupKey         string            `json:"dedup_key"`
	Date             string            `json:"date"`
	Time             string            `json:"time"`
	PartySize        int               `json:"party_size"`
	CustomerName     string            `json:"customer_name"`
	ContactPhone     string            `json:"contact_phone"`
	ContactPhoneCC   string            `json:"contact_phone_country_code"`
	ContactEmail     string            `json:"contact_email,omitempty"`
	Commentary       string            `json:"commentary,omitempty"`
	Children         *int              `json:"children,omitempty"`
	BabyStrollers    *int              `json:"baby_strollers,omitempty"`
	HighChairs       *int              `json:"high_chairs,omitempty"`
	PreferredFloor   *int              `json:"preferred_floor_number,omitempty"`
	Special          specialBookingReq `json:"special"`
	AllowDuplicateOf int64             `json:"allow_duplicate_of,omitempty"`
}

func botSpecialCreateDedupKey(rid int, b boNormalizedBooking) string {
	return botSha(fmt.Sprintf("screate|%d|%s|%s|%s|%s|%d", rid, b.ReservationDate, b.ReservationTime, b.ContactPhoneCountryCode, b.ContactPhone, b.PartySize))
}

// botSpecialCreateAfterKey binds the preview to the FULL resolved set the
// insert will write, including the validated special snapshot (menus, counts,
// prices, adelantos and items as special_json), so any drift between propose
// and execute is a stale, never a silent change.
func botSpecialCreateAfterKey(rid int, b boNormalizedBooking, allowDup int64) string {
	return botSha("specialcreatev1|" + botCanon(map[string]any{
		"rid": rid, "date": b.ReservationDate, "time": b.ReservationTime,
		"party": b.PartySize, "children": b.Children,
		"name": b.CustomerName, "phone": b.ContactPhone, "cc": b.ContactPhoneCountryCode,
		"email": b.ContactEmail, "commentary": b.Commentary.String, "commentary_set": b.Commentary.Valid,
		"strollers": b.BabyStrollers, "chairs": b.HighChairs,
		"floor":        nullableInt64OrNil(b.PreferredFloorNumber),
		"special_menu": false, "arroz": nil, "extras": nil, "group_menu": nil,
		"is_special_booking": b.IsSpecialBooking,
		"is_prereserva":      b.IsPrereserva,
		"special_json":       b.SpecialJSON,
		"allow_duplicate_of": allowDup,
	}))
}

// botSpecialCreateValidate mirrors botCreateValidate, inverted on the special
// guard: the date MUST be an active special date and the special block must
// carry at least one menu. Everything else (past date, closed day, party
// bounds, phone/floor/email normalization) is the same panel contract.
func (s *Server) botSpecialCreateValidate(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, rid int, in botSpecialCreateArgs) (boNormalizedBooking, error) {
	out := boNormalizedBooking{}
	date := strings.TrimSpace(in.Date)
	d, derr := time.Parse("2006-01-02", date)
	if derr != nil {
		return out, errors.New("fecha inválida (YYYY-MM-DD)")
	}
	loc, _ := time.LoadLocation("Europe/Madrid")
	if loc == nil {
		loc = time.UTC
	}
	n := time.Now().In(loc)
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
	if d.Before(today) {
		return out, errors.New("la fecha es pasada: no se crea desde aquí")
	}
	if in.PartySize <= 0 || in.PartySize > 10000 {
		return out, errors.New("número de comensales inválido")
	}
	if strings.TrimSpace(in.CustomerName) == "" {
		return out, errors.New("nombre inválido")
	}
	for _, v := range []*int{in.Children, in.BabyStrollers, in.HighChairs} {
		if v != nil && *v < 0 {
			return out, errors.New("niños, carritos y tronas no pueden ser negativos")
		}
	}
	if in.AllowDuplicateOf < 0 {
		return out, errors.New("allow_duplicate_of inválido")
	}
	var closed int
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM closed_days WHERE restaurant_id = ? AND date = ?", rid, date).Scan(&closed); err != nil || closed > 0 {
		return out, errors.New("el día " + date + " está cerrado (o no se pudo comprobar): no se crea desde aquí")
	}
	st, _, serr := s.loadSpecialDateSettings(ctx, rid, date)
	if serr != nil {
		return out, errors.New("no se pudo comprobar la fecha especial: no se crea desde aquí")
	}
	if st == nil {
		return out, errors.New("la fecha no es una fecha especial activa: para una reserva normal usa booking_propose_create")
	}
	if len(in.Special.Menus) == 0 {
		return out, errors.New("faltan los menús especiales: indica special.menus con special_date_menu_id y plazas (count) usando get_special_date_menu")
	}
	var email *string
	if v := strings.TrimSpace(in.ContactEmail); v != "" {
		email = &v
	}
	var commentary *string
	if v := strings.TrimSpace(in.Commentary); v != "" {
		commentary = &v
	}
	sp := in.Special
	return s.boNormalizeAndValidateBookingInput(ctx, rid, boNormalizeInput{
		ReservationDate:         date,
		ReservationTime:         strings.TrimSpace(in.Time),
		PartySize:               in.PartySize,
		CustomerName:            in.CustomerName,
		ContactPhone:            strings.TrimSpace(in.ContactPhone),
		ContactPhoneCountryCode: strings.TrimSpace(in.ContactPhoneCC),
		ContactEmail:            email,
		PreferredFloorNumber:    in.PreferredFloor,
		Commentary:              commentary,
		Children:                in.Children,
		BabyStrollers:           in.BabyStrollers,
		HighChairs:              in.HighChairs,
		Special:                 &sp,
		SpecialTouched:          true,
	})
}

// botSpecialCreateMenuLines renders the validated snapshot for the preview.
// Only actor-provided ids/counts and validated numbers are shown; mutable DB
// names (menu titles, dish names, the date title) stay out of the preview.
func botSpecialCreateMenuLines(snap specialBookingSnapshot) string {
	var sb strings.Builder
	for _, m := range snap.Menus {
		if m.SectionID != nil {
			fmt.Fprintf(&sb, "\n  · menú especial #%d, sección #%d: %d plazas a %s/ud", m.SpecialDateMenuID, *m.SectionID, m.Count, botEUR(m.UnitPrice))
		} else {
			fmt.Fprintf(&sb, "\n  · menú especial #%d: %d plazas a %s/ud", m.SpecialDateMenuID, m.Count, botEUR(m.UnitPrice))
		}
		if m.AdelantoPerUnit > 0 {
			sb.WriteString(", adelanto " + botEUR(m.AdelantoPerUnit) + "/ud")
			if m.AdelantoPaymentMethod != nil {
				sb.WriteString(" (método " + *m.AdelantoPaymentMethod + ")")
			}
		}
		if len(m.Items) > 0 {
			fmt.Fprintf(&sb, ", %d platos seleccionados", len(m.Items))
		}
	}
	if snap.PaymentMethod != nil {
		sb.WriteString("\n  · método de pago del adelanto: " + *snap.PaymentMethod)
	}
	for _, p := range snap.AdelantosPaid {
		fmt.Fprintf(&sb, "\n  · abono ya recibido: %s por %s", botEUR(p.Amount), p.Method)
	}
	return sb.String()
}

func (s *Server) botToolSpecialCreatePropose(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	actor, origin, ok := s.botSpecialAdminAuthorize(ctx, rid, msg)
	if !ok {
		return botJSON(map[string]any{"error": "no autorizado: solo para miembros verificados del grupo de gestión"}), nil
	}
	s.botSpecialAdminFlushNotify(ctx, rid)
	var in botSpecialCreateArgs
	// Unknown fields are REJECTED, never silently ignored.
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil {
		return botJSON(map[string]any{"error": "argumentos inválidos: esta herramienta solo admite date, time, party_size, customer_name, contact_phone, contact_phone_country_code, contact_email, commentary, children, baby_strollers, high_chairs, preferred_floor_number, special (menus con special_date_menu_id y count; payment_method y adelantos_paid opcionales) y allow_duplicate_of"}), nil
	}
	if strings.TrimSpace(in.Date) == "" || strings.TrimSpace(in.Time) == "" || in.PartySize == 0 || strings.TrimSpace(in.CustomerName) == "" || strings.TrimSpace(in.ContactPhone) == "" {
		return botJSON(map[string]any{"error": "faltan datos: fecha, hora, comensales, nombre y teléfono son obligatorios"}), nil
	}
	return s.botSpecialCreatePropose(ctx, rid, msg, actor, origin, in)
}

func (s *Server) botSpecialCreatePropose(ctx context.Context, rid int, msg botWebhookMessage, actor, origin string, in botSpecialCreateArgs) (string, error) {
	b, err := s.botSpecialCreateValidate(ctx, s.db, rid, in)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	dups, derr := botCreateFindDuplicates(ctx, s.db, rid, b, false)
	if derr != nil {
		return botJSON(map[string]any{"error": "no se pudo comprobar si ya existe la reserva"}), nil
	}
	if len(dups) > 1 {
		return botJSON(map[string]any{"error": "ya hay varias reservas con esos mismos datos (fecha, hora, teléfono y comensales): revísalo desde el panel"}), nil
	}
	if len(dups) == 1 && in.AllowDuplicateOf != dups[0] {
		return botJSON(map[string]any{"error": fmt.Sprintf("ya existe la reserva #%d con esos mismos datos (fecha, hora, teléfono y comensales). Si es una reserva distinta legítima, confírmalo expresamente repitiendo la petición con allow_duplicate_of=%d", dups[0], dups[0])}), nil
	}
	in.DedupKey = botSpecialCreateDedupKey(rid, b)
	code, cerr := botSpecialNewCode()
	if cerr != nil {
		return botJSON(map[string]any{"error": "no se pudo generar la propuesta"}), nil
	}
	det := fmt.Sprintf("%s a las %s, %d comensales", b.ReservationDate, strings.TrimSuffix(b.ReservationTime, ":00"), b.PartySize)
	if b.Children > 0 {
		det += fmt.Sprintf(" (%d niños)", b.Children)
	}
	det += ", a nombre de " + b.CustomerName + ", teléfono +" + b.ContactPhoneCountryCode + " " + b.ContactPhone + ", email " + b.ContactEmail
	if b.PreferredFloorNumber.Valid {
		det += fmt.Sprintf(", salón %d", b.PreferredFloorNumber.Int64)
	}
	if b.BabyStrollers > 0 {
		det += fmt.Sprintf(", %d carritos", b.BabyStrollers)
	}
	if b.HighChairs > 0 {
		det += fmt.Sprintf(", %d tronas", b.HighChairs)
	}
	if b.Commentary.Valid {
		det += ", comentario: «" + b.Commentary.String + "»"
	}
	var snap specialBookingSnapshot
	if spStr, okStr := b.SpecialJSON.(string); okStr {
		_ = json.Unmarshal([]byte(spStr), &snap)
	}
	totals := botSnapTotals(snap)
	det += ". Menús:" + botSpecialCreateMenuLines(snap)
	det += fmt.Sprintf(". Total: %s. Adelanto requerido: %s. Abonado: %s", botEUR(totals["total_eur"].(float64)), botEUR(totals["adelanto_requerido_eur"].(float64)), botEUR(totals["abonado_eur"].(float64)))
	if b.IsPrereserva {
		det += ". Se marca como prereserva (la fecha lo tiene activado)"
	}
	extra := ""
	if len(dups) == 1 {
		extra = fmt.Sprintf(" Se crea AUNQUE YA EXISTE la reserva #%d con los mismos datos (duplicado legítimo confirmado expresamente).", dups[0])
	}
	preview := fmt.Sprintf("Propuesta: CREAR una reserva NUEVA de FECHA ESPECIAL: %s.%s Se registra con el estado por defecto del sistema, igual que las creadas desde el panel, y ocupa el aforo sin comprobación de capacidad. EFECTO EXTERNO al confirmarla: se envía al cliente un WhatsApp de confirmación con el resumen y el QR de la reserva al teléfono indicado, un email de confirmación si el cliente tiene email, y se emite el aviso booking.created.", det, extra)
	after := botSpecialCreateAfterKey(rid, b, in.AllowDuplicateOf)
	before := botSha("special_create|sin-reserva")
	return s.botProposalPersist(ctx, rid, msg, "special_create", 0, in, in.DedupKey, actor, origin, before, after, preview, code)
}

// botSpecialCreateExecute runs INSIDE the claim transaction: claim + dedupe +
// INSERT + occupancy + result_json + event commit or roll back together.
// Notifications, the broadcast and the n8n event run after commit, exactly
// like the panel handler (a notification failure never rolls the booking
// back; it is reported in the notice).
func (s *Server) botSpecialCreateExecute(ctx context.Context, tx *sql.Tx, rid int, msg botWebhookMessage, p botSpecialProposalRow) (string, error) {
	stale := func(why string) (string, error) {
		_ = tx.Rollback()
		s.markStale(ctx, rid, p, msg)
		return "", errors.New(why)
	}
	var in botSpecialCreateArgs
	if json.Unmarshal([]byte(p.Args), &in) != nil {
		return "", errors.New("propuesta ilegible")
	}
	b, err := s.botSpecialCreateValidate(ctx, tx, rid, in)
	if err != nil {
		return stale("la validación ya no pasa: " + err.Error())
	}
	if botSpecialCreateDedupKey(rid, b) != in.DedupKey {
		return stale("la propuesta no coincide con los datos validados; pide una propuesta nueva")
	}
	dups, derr := botCreateFindDuplicates(ctx, tx, rid, b, true)
	if derr != nil {
		return "", errors.New("no se pudo comprobar duplicados; no se aplicó nada")
	}
	if len(dups) > 1 {
		return stale("ya hay varias reservas con esos mismos datos; revísalo desde el panel")
	}
	if len(dups) == 1 && in.AllowDuplicateOf != dups[0] {
		return stale(fmt.Sprintf("ya existe la reserva #%d con esos mismos datos; si es otra legítima, pide una propuesta nueva confirmándolo expresamente", dups[0]))
	}
	if after := botSpecialCreateAfterKey(rid, b, in.AllowDuplicateOf); after != p.After {
		return stale("los datos resueltos cambiaron desde la vista previa; pide una propuesta nueva")
	}
	// Same column list and values the panel create writes (boInsertBooking),
	// but inside the claim transaction.
	res, err := tx.ExecContext(ctx, `
		INSERT INTO bookings (
			restaurant_id,
			reservation_date,
			party_size,
			children,
			reservation_time,
			customer_name,
			contact_phone,
			contact_phone_country_code,
			commentary,
			arroz_type,
			arroz_servings,
			babyStrollers,
			highChairs,
			contact_email,
			special_menu,
			menu_de_grupo_id,
			menu_de_grupo_assigned,
			principales_json,
			extras_json,
			table_number,
			preferred_floor_number,
			is_special_booking,
			is_prereserva,
			special_json,
			has_mobility_issues,
			mobility_people
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		rid,
		b.ReservationDate,
		b.PartySize,
		b.Children,
		b.ReservationTime,
		b.CustomerName,
		b.ContactPhone,
		b.ContactPhoneCountryCode,
		nullableStringOrNil(b.Commentary),
		b.ArrozTypeJSON,
		b.ArrozServingsJSON,
		b.BabyStrollers,
		b.HighChairs,
		b.ContactEmail,
		boolToTinyint(b.SpecialMenu),
		b.MenuDeGrupoID,
		boolToTinyint(b.MenuDeGrupoAssigned),
		b.PrincipalesJSON,
		b.ExtrasJSON,
		nullableStringOrNil(b.TableNumber),
		nullableInt64OrNil(b.PreferredFloorNumber),
		boolToTinyint(b.IsSpecialBooking),
		boolToTinyint(b.IsPrereserva),
		nullableStringOrNilFromAny(b.SpecialJSON),
		boolToTinyint(b.HasMobilityIssues),
		b.MobilityPeople,
	)
	if err != nil {
		return "", errors.New("no se pudo crear la reserva; no se aplicó nada")
	}
	id64, err := res.LastInsertId()
	if err != nil || id64 <= 0 {
		return "", errors.New("no se pudo crear la reserva; no se aplicó nada")
	}
	if err := s.applyBookingLocationOccupancy(ctx, tx, rid, b.ReservationDate, b.PreferredFloorNumber, nil, b.PartySize, +1); err != nil {
		return "", errors.New("no se pudo registrar la ocupación; no se aplicó nada")
	}
	// Second validation just before commit: reference data read again.
	again, aerr := s.botSpecialCreateValidate(ctx, tx, rid, in)
	if aerr != nil || botSpecialCreateAfterKey(rid, again, in.AllowDuplicateOf) != p.After {
		return stale("los datos de referencia cambiaron durante la creación; no se aplicó nada")
	}
	notice := fmt.Sprintf("Reserva especial #%d creada: %s %s, %d comensales, a nombre de %s.", id64, b.ReservationDate, strings.TrimSuffix(b.ReservationTime, ":00"), b.PartySize, b.CustomerName)
	rj, _ := json.Marshal(map[string]any{"booking_id": id64, "op": p.Op, "notice": notice})
	if _, err := tx.ExecContext(ctx, `UPDATE wa_bot_op_proposals SET result_json=?, notify_state='pending' WHERE id=?`, string(rj), p.ID); err != nil {
		return "", errors.New("error interno")
	}
	s.botSpecialAdminEvent(ctx, tx, rid, p.ID, "executed", p.Actor, strings.TrimSpace(msg.ChatJID), map[string]any{"op": p.Op, "booking_id": id64, "before_hash": p.Before, "after_hash": p.After, "approval_message_id": msg.MessageID})
	if err := tx.Commit(); err != nil {
		// Outcome unknown to us: never re-create by matching name/phone/time;
		// the durable proposal result is the source of truth.
		log.Printf("[bot] wa_bot_special_create_v1 commit_unknown rid=%d proposal=%d err=%v", rid, p.ID, err)
		return "", errors.New("no pude confirmar si se creó: consulta el resultado de la propuesta o el panel antes de repetir")
	}
	// Post-commit, exactly the panel create block (handleBOBookingCreate):
	// broadcast, WhatsApp summary + QR, confirmation emails, n8n event.
	// Failures here are reported, never rolled back.
	s.broadcastBookingChanged(rid, id64, "booking_created")
	notifData := boBookingToNotificationData(b, int(id64))
	notifData["is_special_booking"] = true
	notifData["is_prereserva"] = b.IsPrereserva
	notifData["special"] = b.SpecialJSON
	if qr := s.ensureBookingQR(ctx, rid, id64, b.ReservationDate); qr != nil {
		notifData[bookingQRKey] = qr
	}
	var warning string
	waSent := false
	if waErr := sendBookingWhatsAppToCustomer(ctx, s, rid, notifData, id64); waErr != nil {
		log.Printf("[bot] wa_bot_special_create_v1 rid=%d booking=%d whatsapp_failed=%v", rid, id64, waErr)
		warning = "WhatsApp: " + waErr.Error()
	} else {
		waSent = true
	}
	emailSent := false
	if custSent, restSent, emErr := sendBookingConfirmationEmails(ctx, s, rid, notifData, id64); emErr != nil {
		log.Printf("[bot] wa_bot_special_create_v1 rid=%d booking=%d email_failed=%v", rid, id64, emErr)
		if warning != "" {
			warning += "; "
		}
		warning += "Email: " + emErr.Error()
	} else {
		emailSent = custSent || restSent
	}
	s.emitN8nWebhookAsync(rid, "booking.created", map[string]any{
		"source":               "whatsapp_bot",
		"bookingId":            int(id64),
		"reservationDate":      b.ReservationDate,
		"reservationTime":      b.ReservationTime,
		"partySize":            b.PartySize,
		"customerName":         b.CustomerName,
		"contactPhone":         b.ContactPhone,
		"contactEmail":         b.ContactEmail,
		"specialMenu":          b.SpecialMenu,
		"menuDeGrupoId":        b.MenuDeGrupoID,
		"preferredFloorNumber": nullableInt64OrNil(b.PreferredFloorNumber),
	})
	switch {
	case warning != "":
		notice += " Aviso: no se pudo notificar del todo (" + warning + "): la reserva SÍ está creada; el aviso al cliente se puede reenviar desde el panel."
	case waSent && emailSent:
		notice += " Cliente avisado por WhatsApp (resumen + QR) y email."
	case waSent:
		notice += " Cliente avisado por WhatsApp (resumen + QR); sin email (no tenía o está desactivado)."
	default:
		notice += " No se pudo avisar al cliente desde aquí; la reserva SÍ está creada."
	}
	return notice, nil
}
