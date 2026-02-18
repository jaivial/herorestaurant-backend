package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

type publicBooking struct {
	ID              int
	ReservationDate string
	ReservationTime string
	PartySize       int
	CustomerName    string
	ContactPhone    sql.NullString
	ContactEmail    sql.NullString
	Commentary      sql.NullString
	ArrozType       sql.NullString
	ArrozServings   sql.NullString
	BabyStrollers   sql.NullInt64
	HighChairs      sql.NullInt64
	Status          sql.NullString
	SpecialMenu     sql.NullInt64
	MenuDeGrupoID   sql.NullInt64
	PrincipalesJSON sql.NullString
}

func (s *Server) fetchPublicBooking(ctx context.Context, id int) (publicBooking, error) {
	restaurantID, ok := restaurantIDFromContext(ctx)
	if !ok {
		return publicBooking{}, sql.ErrNoRows
	}

	var b publicBooking
	err := s.db.QueryRowContext(ctx, `
		SELECT
			id,
			DATE_FORMAT(reservation_date, '%Y-%m-%d') AS reservation_date,
			TIME_FORMAT(reservation_time, '%H:%i:%s') AS reservation_time,
			party_size,
			customer_name,
			contact_phone,
			contact_email,
			commentary,
			arroz_type,
			arroz_servings,
			babyStrollers,
			highChairs,
			status,
			special_menu,
			menu_de_grupo_id,
			principales_json
		FROM bookings
		WHERE restaurant_id = ?
		  AND id = ?
		LIMIT 1
	`, restaurantID, id).Scan(
		&b.ID,
		&b.ReservationDate,
		&b.ReservationTime,
		&b.PartySize,
		&b.CustomerName,
		&b.ContactPhone,
		&b.ContactEmail,
		&b.Commentary,
		&b.ArrozType,
		&b.ArrozServings,
		&b.BabyStrollers,
		&b.HighChairs,
		&b.Status,
		&b.SpecialMenu,
		&b.MenuDeGrupoID,
		&b.PrincipalesJSON,
	)
	return b, err
}

func parseJSONArrayOrScalarString(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "null") {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var out []string
		if err := json.Unmarshal([]byte(raw), &out); err == nil {
			var cleaned []string
			for _, v := range out {
				v = strings.TrimSpace(v)
				if v != "" {
					cleaned = append(cleaned, v)
				}
			}
			return cleaned
		}
	}
	return []string{raw}
}

func parseJSONArrayOrScalarInt(raw string) []int {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "null") {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var out []int
		if err := json.Unmarshal([]byte(raw), &out); err == nil {
			var cleaned []int
			for _, v := range out {
				if v > 0 {
					cleaned = append(cleaned, v)
				}
			}
			return cleaned
		}
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return nil
	}
	return []int{n}
}

func formatArrozList(typesRaw, servingsRaw sql.NullString) string {
	if !typesRaw.Valid || strings.TrimSpace(typesRaw.String) == "" || strings.EqualFold(strings.TrimSpace(typesRaw.String), "null") {
		return "No Arroz"
	}
	types := parseJSONArrayOrScalarString(typesRaw.String)
	servs := []int{}
	if servingsRaw.Valid {
		servs = parseJSONArrayOrScalarInt(servingsRaw.String)
	}
	if len(types) == 0 || len(servs) == 0 {
		return "No Arroz"
	}
	n := len(types)
	if len(servs) < n {
		n = len(servs)
	}
	var parts []string
	for i := 0; i < n; i++ {
		t := strings.TrimSpace(types[i])
		s := servs[i]
		if t == "" || s <= 0 {
			continue
		}
		parts = append(parts, t+" x "+strconv.Itoa(s))
	}
	if len(parts) == 0 {
		return "No Arroz"
	}
	return strings.Join(parts, ", ")
}

func publicBaseURL(r *http.Request) string {
	if base := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/"); base != "" {
		return base
	}
	// Best-effort fallback: derive from request.
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = "alqueriavillacarmen.com"
	}
	return scheme + "://" + host
}

var confirmReservationTmpl = template.Must(template.New("confirm_reservation").Parse(`<!DOCTYPE html>
<html lang="es">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Confirmar Reserva - {{.BrandName}}</title>
  <style>
    :root { --primary:#4a6741; --danger:#dc3545; --success:#28a745; --warning:#ffc107; --bg1:#e8f5e9; --bg2:#c8e6c9; --bg3:#a5d6a7; --text:#2d3748; --muted:#718096; }
    * { box-sizing:border-box; }
    body { margin:0; font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Oxygen,Ubuntu,sans-serif; min-height:100vh; display:flex; align-items:center; justify-content:center; padding:16px; background:linear-gradient(135deg,var(--bg1),var(--bg2),var(--bg3)); background-attachment:fixed; color:var(--text); }
    .card { width:100%; max-width:520px; background:rgba(255,255,255,0.75); border:1px solid rgba(255,255,255,0.35); border-radius:18px; padding:24px; box-shadow:0 8px 32px rgba(0,0,0,0.08); backdrop-filter: blur(18px); }
    .logo { display:block; margin:0 auto 10px; width:120px; height:auto; }
    h1 { margin:0 0 6px; font-size:22px; text-align:center; }
    .sub { margin:0 0 16px; color:var(--muted); font-size:14px; text-align:center; }
    .msg { padding:12px 14px; border-radius:12px; margin:14px 0; border:1px solid rgba(0,0,0,0.08); background:rgba(255,255,255,0.55); }
    .msg.success { border-color: rgba(40,167,69,0.35); background: rgba(40,167,69,0.12); }
    .msg.error { border-color: rgba(220,53,69,0.35); background: rgba(220,53,69,0.10); }
    .details { background:rgba(255,255,255,0.55); border:1px solid rgba(74,103,65,0.22); border-radius:14px; padding:14px; margin:14px 0; }
    .details h3 { margin:0 0 10px; font-size:16px; }
    .details p { margin:6px 0; }
    .btn { display:inline-block; width:100%; text-align:center; border:none; border-radius:12px; padding:12px 14px; cursor:pointer; font-size:16px; background:var(--primary); color:white; text-decoration:none; }
    .btn.secondary { background:rgba(74,103,65,0.12); color:var(--primary); border:1px solid rgba(74,103,65,0.35); }
    .row { display:flex; gap:10px; flex-wrap:wrap; }
    .row .btn { flex:1; min-width:200px; }
  </style>
</head>
<body>
  <main class="card">
    <img class="logo" src="{{.LogoURL}}" alt="{{.BrandName}}" />
    <h1>Confirmar Reserva</h1>
    <p class="sub">Revise los datos y confirme su asistencia.</p>

    {{if .Message}}
      <div class="msg {{if .Success}}success{{else}}error{{end}}">{{.Message}}</div>
    {{end}}

    {{if .HasBooking}}
      <section class="details">
        <h3>Detalles de la reserva</h3>
        <p><strong>ID:</strong> #{{.BookingID}}</p>
        <p><strong>Cliente:</strong> {{.CustomerName}}</p>
        <p><strong>Fecha:</strong> {{.DateDisplay}}</p>
        <p><strong>Hora:</strong> {{.TimeDisplay}}</p>
        <p><strong>Personas:</strong> {{.PartySize}}</p>
        {{if .ArrozDisplay}}<p><strong>Arroz:</strong> {{.ArrozDisplay}}</p>{{end}}
      </section>
    {{end}}

    {{if .ShowConfirmation}}
      <form method="post" action="{{.Action}}">
        <button class="btn" type="submit" name="confirm_booking" value="1">Confirmar Reserva</button>
      </form>
      <div style="height:10px"></div>
      <a class="btn secondary" href="index.php">Volver a la página principal</a>
    {{else}}
      <a class="btn" href="index.php">Volver a la página principal</a>
    {{end}}
  </main>
</body>
</html>`))

func (s *Server) handleConfirmReservationPage(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "Unknown restaurant")
		return
	}

	branding, _ := s.loadRestaurantBranding(r.Context(), restaurantID)
	brandName := strings.TrimSpace(branding.BrandName)
	if brandName == "" {
		brandName = "Restaurante"
	}
	logoURL := strings.TrimSpace(branding.LogoURL)
	if logoURL == "" {
		logoURL = "/media/logos/logo-negro.png"
	}

	idRaw := strings.TrimSpace(r.URL.Query().Get("id"))
	id, _ := strconv.Atoi(idRaw)
	data := map[string]any{
		"BrandName":        brandName,
		"LogoURL":          logoURL,
		"Message":          "",
		"Success":          false,
		"HasBooking":       false,
		"ShowConfirmation": false,
		"Action":           r.URL.Path + "?id=" + url.QueryEscape(idRaw),
	}

	if id <= 0 {
		data["Message"] = "ID de reserva inválido. Por favor, inténtelo de nuevo."
		writeHTMLTemplate(w, confirmReservationTmpl, data)
		return
	}

	b, err := s.fetchPublicBooking(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			data["Message"] = "No se encontró ninguna reserva con el ID proporcionado."
			writeHTMLTemplate(w, confirmReservationTmpl, data)
			return
		}
		data["Message"] = "Error al cargar la reserva. Por favor, inténtelo de nuevo."
		writeHTMLTemplate(w, confirmReservationTmpl, data)
		return
	}

	dateDisplay := b.ReservationDate
	if t, err := time.Parse("2006-01-02", b.ReservationDate); err == nil {
		dateDisplay = t.Format("02/01/2006")
	}
	timeDisplay := formatHHMM(b.ReservationTime)
	arrozDisplay := ""
	if s := formatArrozList(b.ArrozType, b.ArrozServings); s != "No Arroz" {
		arrozDisplay = s
	}

	data["HasBooking"] = true
	data["BookingID"] = b.ID
	data["CustomerName"] = b.CustomerName
	data["DateDisplay"] = dateDisplay
	data["TimeDisplay"] = timeDisplay
	data["PartySize"] = b.PartySize
	data["ArrozDisplay"] = arrozDisplay

	status := ""
	if b.Status.Valid {
		status = strings.TrimSpace(b.Status.String)
	}

	isPost := r.Method == http.MethodPost
	process := isPost && strings.TrimSpace(r.FormValue("confirm_booking")) != ""

	if status == "confirmed" {
		data["Success"] = true
		data["Message"] = "Esta reserva ya estaba confirmada."
		writeHTMLTemplate(w, confirmReservationTmpl, data)
		return
	}

	if process {
		_, err := s.db.ExecContext(r.Context(), "UPDATE bookings SET status = 'confirmed' WHERE restaurant_id = ? AND id = ?", restaurantID, b.ID)
		if err == nil {
			data["Success"] = true
			data["Message"] = "¡Su reserva ha sido confirmada correctamente!"
			s.emitN8nWebhookAsync(restaurantID, "booking.confirmed", map[string]any{
				"source":          "public_confirm_page",
				"bookingId":       b.ID,
				"reservationDate": b.ReservationDate,
				"reservationTime": b.ReservationTime,
				"partySize":       b.PartySize,
				"customerName":    b.CustomerName,
				"contactPhone":    defaultString(b.ContactPhone, ""),
				"contactEmail":    defaultString(b.ContactEmail, ""),
			})
		} else {
			data["Message"] = "Error al confirmar la reserva. Por favor, inténtelo de nuevo."
		}
		writeHTMLTemplate(w, confirmReservationTmpl, data)
		return
	}

	data["ShowConfirmation"] = true
	writeHTMLTemplate(w, confirmReservationTmpl, data)
}

var cancelReservationTmpl = template.Must(template.New("cancel_reservation").Parse(`<!DOCTYPE html>
<html lang="es">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Cancelar Reserva - {{.BrandName}}</title>
  <style>
    :root { --primary:#4a6741; --danger:#dc3545; --success:#28a745; --warning:#ffc107; --bg1:#e8f5e9; --bg2:#c8e6c9; --bg3:#a5d6a7; --text:#2d3748; --muted:#718096; }
    * { box-sizing:border-box; }
    body { margin:0; font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Oxygen,Ubuntu,sans-serif; min-height:100vh; display:flex; align-items:center; justify-content:center; padding:16px; background:linear-gradient(135deg,var(--bg1),var(--bg2),var(--bg3)); background-attachment:fixed; color:var(--text); }
    .card { width:100%; max-width:560px; background:rgba(255,255,255,0.75); border:1px solid rgba(255,255,255,0.35); border-radius:18px; padding:24px; box-shadow:0 8px 32px rgba(0,0,0,0.08); backdrop-filter: blur(18px); }
    .logo { display:block; margin:0 auto 10px; width:120px; height:auto; }
    h1 { margin:0 0 6px; font-size:22px; text-align:center; }
    .sub { margin:0 0 16px; color:var(--muted); font-size:14px; text-align:center; }
    .msg { padding:12px 14px; border-radius:12px; margin:14px 0; border:1px solid rgba(0,0,0,0.08); background:rgba(255,255,255,0.55); }
    .msg.success { border-color: rgba(40,167,69,0.35); background: rgba(40,167,69,0.12); }
    .msg.error { border-color: rgba(220,53,69,0.35); background: rgba(220,53,69,0.10); }
    .msg.warn { border-color: rgba(255,193,7,0.50); background: rgba(255,193,7,0.16); }
    .details { background:rgba(255,255,255,0.55); border:1px solid rgba(74,103,65,0.22); border-radius:14px; padding:14px; margin:14px 0; }
    .details h3 { margin:0 0 10px; font-size:16px; }
    .details p { margin:6px 0; }
    .btn { display:inline-block; width:100%; text-align:center; border:none; border-radius:12px; padding:12px 14px; cursor:pointer; font-size:16px; background:var(--danger); color:white; text-decoration:none; }
    .btn.secondary { background:rgba(74,103,65,0.12); color:var(--primary); border:1px solid rgba(74,103,65,0.35); }
    .btn.call { background:var(--success); }
  </style>
</head>
<body>
  <main class="card">
    <img class="logo" src="{{.LogoURL}}" alt="{{.BrandName}}" />
    <h1>Cancelar Reserva</h1>
    <p class="sub">Si cancela su reserva, se liberará la mesa para otros clientes.</p>

    {{if .Message}}
      <div class="msg {{if .Success}}success{{else if .IsSameDay}}warn{{else}}error{{end}}">{{.Message}}</div>
    {{end}}

    {{if .HasBooking}}
      <section class="details">
        <h3>Detalles de la reserva</h3>
        <p><strong>ID:</strong> #{{.BookingID}}</p>
        <p><strong>Cliente:</strong> {{.CustomerName}}</p>
        <p><strong>Fecha:</strong> {{.DateDisplay}}</p>
        <p><strong>Hora:</strong> {{.TimeDisplay}}</p>
        <p><strong>Personas:</strong> {{.PartySize}}</p>
      </section>
    {{end}}

    {{if .IsSameDay}}
      <a class="btn call" href="tel:638857294">Llamar ahora</a>
      <div style="height:10px"></div>
      <a class="btn secondary" href="index.php">Volver a la página principal</a>
    {{else if .ShowConfirmation}}
      <form method="post" action="{{.Action}}">
        <button class="btn" type="submit" name="confirm_cancel" value="1">Confirmar Cancelación</button>
      </form>
      <div style="height:10px"></div>
      <a class="btn secondary" href="index.php">Volver a la página principal</a>
    {{else}}
      <a class="btn secondary" href="index.php">Volver a la página principal</a>
    {{end}}
  </main>
</body>
</html>`))

func (s *Server) handleCancelReservationPage(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "Unknown restaurant")
		return
	}

	branding, _ := s.loadRestaurantBranding(r.Context(), restaurantID)
	brandName := strings.TrimSpace(branding.BrandName)
	if brandName == "" {
		brandName = "Restaurante"
	}
	logoURL := strings.TrimSpace(branding.LogoURL)
	if logoURL == "" {
		logoURL = "/media/logos/logo-negro.png"
	}

	q := r.URL.Query()
	idRaw := strings.TrimSpace(q.Get("id"))
	cancelledBy := strings.TrimSpace(q.Get("cancelled_by"))
	if cancelledBy == "" {
		cancelledBy = "customer"
	}
	if cancelledBy != "customer" && cancelledBy != "staff" {
		cancelledBy = "customer"
	}
	id, _ := strconv.Atoi(idRaw)

	actionQS := url.Values{}
	actionQS.Set("id", idRaw)
	actionQS.Set("cancelled_by", cancelledBy)
	action := r.URL.Path + "?" + actionQS.Encode()

	data := map[string]any{
		"BrandName":        brandName,
		"LogoURL":          logoURL,
		"Message":          "",
		"Success":          false,
		"HasBooking":       false,
		"ShowConfirmation": false,
		"IsSameDay":        false,
		"Action":           action,
	}

	if id <= 0 {
		data["Message"] = "ID de reserva inválido. Por favor, inténtelo de nuevo."
		writeHTMLTemplate(w, cancelReservationTmpl, data)
		return
	}

	b, err := s.fetchPublicBooking(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			data["Message"] = "No se encontró ninguna reserva con el ID proporcionado."
			writeHTMLTemplate(w, cancelReservationTmpl, data)
			return
		}
		data["Message"] = "Error al cargar la reserva. Por favor, inténtelo de nuevo."
		writeHTMLTemplate(w, cancelReservationTmpl, data)
		return
	}

	dateDisplay := b.ReservationDate
	if t, err := time.Parse("2006-01-02", b.ReservationDate); err == nil {
		dateDisplay = t.Format("02/01/2006")
	}
	timeDisplay := formatHHMM(b.ReservationTime)

	data["HasBooking"] = true
	data["BookingID"] = b.ID
	data["CustomerName"] = b.CustomerName
	data["DateDisplay"] = dateDisplay
	data["TimeDisplay"] = timeDisplay
	data["PartySize"] = b.PartySize

	// Same-day cancellation restriction (legacy behavior).
	today := time.Now().Format("2006-01-02")
	if today == b.ReservationDate {
		data["IsSameDay"] = true
		data["Message"] = "Las reservas para el mismo día no se pueden cancelar online. Por favor, llame al restaurante."
		writeHTMLTemplate(w, cancelReservationTmpl, data)
		return
	}

	process := r.Method == http.MethodPost && strings.TrimSpace(r.FormValue("confirm_cancel")) != ""
	if !process {
		data["ShowConfirmation"] = true
		writeHTMLTemplate(w, cancelReservationTmpl, data)
		return
	}

	// Transaction: move booking to cancelled_bookings and delete it.
	err = withTx(r.Context(), s.db, func(ctx context.Context, tx *sql.Tx) error {
		resTimeNorm, _ := ensureHHMMSS(b.ReservationTime)
		if resTimeNorm != "" {
			b.ReservationTime = resTimeNorm
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO cancelled_bookings
				(restaurant_id, booking_id, reservation_date, party_size, reservation_time, customer_name,
				 contact_phone, contact_email, commentary, arroz_type, arroz_servings,
				 babyStrollers, highChairs, cancellation_date, cancelled_by,
				 special_menu, menu_de_grupo_id, principales_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), ?, ?, ?, ?)
		`,
			restaurantID,
			b.ID,
			b.ReservationDate,
			b.PartySize,
			b.ReservationTime,
			b.CustomerName,
			defaultString(b.ContactPhone, ""),
			defaultString(b.ContactEmail, ""),
			defaultString(b.Commentary, ""),
			nullStringOrNil(b.ArrozType),
			nullStringOrNil(b.ArrozServings),
			int64OrZero(b.BabyStrollers),
			int64OrZero(b.HighChairs),
			cancelledBy,
			int64OrZero(b.SpecialMenu),
			nullInt64OrNil(b.MenuDeGrupoID),
			nullStringOrNil(b.PrincipalesJSON),
		)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, "DELETE FROM bookings WHERE restaurant_id = ? AND id = ?", restaurantID, b.ID)
		return err
	})
	if err != nil {
		data["Message"] = "Error al cancelar la reserva. Por favor, inténtelo de nuevo."
		writeHTMLTemplate(w, cancelReservationTmpl, data)
		return
	}

	data["Success"] = true
	data["Message"] = "Su reserva ha sido cancelada correctamente."
	data["ShowConfirmation"] = false
	writeHTMLTemplate(w, cancelReservationTmpl, data)

	s.emitN8nWebhookAsync(restaurantID, "booking.cancelled", map[string]any{
		"source":          "public_cancel_page",
		"cancelledBy":     cancelledBy,
		"bookingId":       b.ID,
		"reservationDate": b.ReservationDate,
		"reservationTime": b.ReservationTime,
		"partySize":       b.PartySize,
		"customerName":    b.CustomerName,
		"contactPhone":    defaultString(b.ContactPhone, ""),
		"contactEmail":    defaultString(b.ContactEmail, ""),
	})

	// Best-effort: notify restaurant via WhatsApp.
	cancelledByText := "👤 Cliente"
	if cancelledBy == "staff" {
		cancelledByText = "👨‍💼 Equipo"
	}
	arrozFormatted := formatArrozList(b.ArrozType, b.ArrozServings)
	msg := "🚨 *RESERVA CANCELADA* 🚨\n\n"
	msg += "*Detalles de la reserva:*\n"
	msg += "━━━━━━━━━━━━━━━━━━━━\n"
	msg += "📋 *ID:* #" + strconv.Itoa(b.ID) + "\n"
	msg += "👤 *Cliente:* " + strings.TrimSpace(b.CustomerName) + "\n"
	msg += "📞 *Teléfono:* " + defaultString(b.ContactPhone, "No disponible") + "\n"
	msg += "📅 *Fecha:* " + dateDisplay + "\n"
	msg += "⏰ *Hora:* " + timeDisplay + "\n"
	msg += "👥 *Personas:* " + strconv.Itoa(b.PartySize) + "\n"
	if arrozFormatted != "No Arroz" {
		msg += "🍚 *Arroz:* " + arrozFormatted + "\n"
	}
	msg += "━━━━━━━━━━━━━━━━━━━━\n"
	msg += "*Cancelada por:* " + cancelledByText + "\n"
	msg += "🕐 *Hora cancelación:* " + time.Now().Format("15:04 02/01/2006")
	s.sendRestaurantWhatsAppText(context.Background(), restaurantID, msg)
}

var bookRiceTmpl = template.Must(template.New("book_rice").Parse(`<!DOCTYPE html>
<html lang="es">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Reservar Arroz - {{.BrandName}}</title>
  <style>
    :root { --primary:#4a6741; --danger:#dc3545; --success:#28a745; --warning:#ffc107; --bg:#ffffff; --text:#2d3748; --muted:#718096; }
    * { box-sizing:border-box; }
    body { margin:0; font-family: Arial, sans-serif; line-height:1.6; padding:18px; text-align:center; color:var(--text); background:#f7faf7; }
    .logo { max-width:200px; height:auto; margin:0 auto 10px; display:block; }
    h1 { color:var(--primary); margin:0 0 12px; }
    .message { padding:14px; border-radius:10px; margin:14px auto; max-width:760px; border:1px solid rgba(0,0,0,0.08); background:rgba(255,255,255,0.85); }
    .message.success { background:rgba(40,167,69,0.12); border-color: rgba(40,167,69,0.35); }
    .message.error { background:rgba(220,53,69,0.10); border-color: rgba(220,53,69,0.35); }
    .message.warn { background:rgba(255,193,7,0.16); border-color: rgba(255,193,7,0.50); }
    .details { text-align:left; margin:14px auto; max-width:420px; background:#fff; padding:14px; border-radius:10px; border:1px solid #ddd; }
    .details p { margin:8px 0; }
    form { max-width:420px; margin:0 auto; text-align:left; }
    label { display:block; font-weight:bold; margin:12px 0 6px; }
    select, input[type=number] { width:100%; padding:10px; border:1px solid #ddd; border-radius:6px; }
    .btn { display:inline-block; background:var(--primary); color:white; text-decoration:none; padding:10px 20px; border-radius:6px; margin-top:16px; border:none; cursor:pointer; font-size:16px; }
    .btn:hover { background:#3a5331; }
    .btn.call { background:var(--success); }
    .countdown { font-size:16px; margin-top:14px; font-weight:bold; }
  </style>
</head>
<body>
  <img class="logo" src="{{.LogoURL}}" alt="{{.BrandName}}" />
  <h1>Reservar Arroz</h1>

  {{if .Message}}
    <div class="message {{if .Success}}success{{else if .IsSameDay}}warn{{else}}error{{end}}">{{.Message}}</div>
  {{end}}

  {{if .HasBooking}}
    <div class="details">
      <h3>Detalles de la reserva:</h3>
      <p><strong>Cliente:</strong> {{.CustomerName}}</p>
      <p><strong>Fecha:</strong> {{.DateDisplay}}</p>
      <p><strong>Hora:</strong> {{.TimeDisplay}}</p>
      <p><strong>Personas:</strong> {{.PartySize}}</p>
      <p><strong>Arroz:</strong> {{.ArrozRawDisplay}}</p>
    </div>
  {{end}}

  {{if .ShowForm}}
    <form method="post" action="{{.Action}}">
      <label for="rice_type">Seleccione tipo de arroz:</label>
      <select id="rice_type" name="rice_type" required>
        <option value="">Seleccione una opción</option>
        {{range .RiceOptions}}
          <option value="{{.}}">{{.}}</option>
        {{end}}
      </select>

      <label for="rice_servings">Número de raciones (máximo {{.PartySize}}):</label>
      <input id="rice_servings" name="rice_servings" type="number" min="1" max="{{.PartySize}}" required />

      <button class="btn" type="submit" name="submit" value="1">Reservar Arroz</button>
    </form>
  {{else if .Success}}
    <p>Gracias por reservar su arroz. Le esperamos en {{.BrandName}}.</p>
    <div class="countdown" id="countdown">Redirección a la página principal en 15 segundos</div>
    <a href="index.php" class="btn">Volver a la página principal</a>
  {{end}}

  {{if .IsSameDay}}
    <div style="margin-top: 18px;">
      <p>Puede llamar directamente haciendo click en el siguiente botón:</p>
      <a href="tel:638857294" class="btn call">Llamar ahora</a>
    </div>
  {{end}}

  {{if not .Success}}
    <div style="margin-top: 16px;">
      <a href="index.php" class="btn">Volver a la página principal</a>
    </div>
  {{end}}

  {{if .Countdown}}
    <script>
      let seconds = 15;
      const el = document.getElementById('countdown');
	      function tick() {
	        seconds--;
	        if (seconds > 0) {
	          el.textContent = 'Redirección a la página principal en ' + seconds + ' segundos';
	          setTimeout(tick, 1000);
	        } else {
	          window.location.href = 'index.php';
	        }
	      }
      setTimeout(tick, 1000);
    </script>
  {{end}}
</body>
</html>`))

func (s *Server) handleBookRicePage(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "Unknown restaurant")
		return
	}

	branding, _ := s.loadRestaurantBranding(r.Context(), restaurantID)
	brandName := strings.TrimSpace(branding.BrandName)
	if brandName == "" {
		brandName = "Restaurante"
	}
	logoURL := strings.TrimSpace(branding.LogoURL)
	if logoURL == "" {
		logoURL = "/media/logos/logo-negro.png"
	}

	idRaw := strings.TrimSpace(r.URL.Query().Get("id"))
	id, _ := strconv.Atoi(idRaw)
	data := map[string]any{
		"BrandName":    brandName,
		"LogoURL":      logoURL,
		"Message":     "",
		"Success":     false,
		"HasBooking":  false,
		"ShowForm":    false,
		"Countdown":   false,
		"IsSameDay":   false,
		"RiceOptions": []string{},
		"Action":      r.URL.Path + "?id=" + url.QueryEscape(idRaw),
	}

	if id <= 0 {
		data["Message"] = "ID de reserva inválido. Por favor, inténtelo de nuevo."
		writeHTMLTemplate(w, bookRiceTmpl, data)
		return
	}

	b, err := s.fetchPublicBooking(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			data["Message"] = "No se encontró ninguna reserva con el ID proporcionado."
			writeHTMLTemplate(w, bookRiceTmpl, data)
			return
		}
		data["Message"] = "Error al cargar la reserva. Por favor, inténtelo de nuevo."
		writeHTMLTemplate(w, bookRiceTmpl, data)
		return
	}

	dateDisplay := b.ReservationDate
	if t, err := time.Parse("2006-01-02", b.ReservationDate); err == nil {
		dateDisplay = t.Format("02/01/2006")
	}
	timeDisplay := b.ReservationTime
	if t := formatHHMM(b.ReservationTime); t != "" {
		timeDisplay = t
	}

	data["HasBooking"] = true
	data["CustomerName"] = b.CustomerName
	data["DateDisplay"] = dateDisplay
	data["TimeDisplay"] = timeDisplay
	data["PartySize"] = b.PartySize
	data["ArrozRawDisplay"] = func() string {
		if b.ArrozType.Valid && strings.TrimSpace(b.ArrozType.String) != "" && b.ArrozServings.Valid && strings.TrimSpace(b.ArrozServings.String) != "" {
			return b.ArrozType.String + " (" + b.ArrozServings.String + " raciones)"
		}
		return "No Arroz"
	}()

	// Same-day restriction: no rice bookings online for same day.
	today := time.Now().Format("2006-01-02")
	if today == b.ReservationDate {
		data["IsSameDay"] = true
		data["Message"] = "Reserva para el mismo día. Las reservas de arroz para el mismo día no se pueden realizar por la web debido a que los tipos de arroz están limitados. Por favor, llame al número de teléfono: 638857294 para consultar disponibilidad."
		writeHTMLTemplate(w, bookRiceTmpl, data)
		return
	}

	// Load rice options from FINDE table.
	rows, err := s.db.QueryContext(r.Context(), "SELECT DESCRIPCION FROM FINDE WHERE restaurant_id = ? AND TIPO = 'ARROZ' ORDER BY DESCRIPCION", restaurantID)
	if err == nil {
		defer rows.Close()
		var opts []string
		for rows.Next() {
			var d string
			if err := rows.Scan(&d); err == nil {
				d = strings.TrimSpace(d)
				if d != "" {
					opts = append(opts, d)
				}
			}
		}
		data["RiceOptions"] = opts
	}

	// Show form only if arroz_type is empty (legacy behavior).
	if b.ArrozType.Valid && strings.TrimSpace(b.ArrozType.String) != "" && !strings.EqualFold(strings.TrimSpace(b.ArrozType.String), "null") {
		writeHTMLTemplate(w, bookRiceTmpl, data)
		return
	}

	process := r.Method == http.MethodPost && strings.TrimSpace(r.FormValue("submit")) != ""
	if !process {
		data["ShowForm"] = true
		writeHTMLTemplate(w, bookRiceTmpl, data)
		return
	}

	selectedRice := strings.TrimSpace(r.FormValue("rice_type"))
	servingsRaw := strings.TrimSpace(r.FormValue("rice_servings"))
	servings, err := strconv.Atoi(servingsRaw)
	if selectedRice == "" {
		data["Message"] = "Por favor, seleccione un tipo de arroz."
		data["ShowForm"] = true
		writeHTMLTemplate(w, bookRiceTmpl, data)
		return
	}
	if err != nil || servings <= 0 {
		data["Message"] = "Por favor, indique un número válido de raciones."
		data["ShowForm"] = true
		writeHTMLTemplate(w, bookRiceTmpl, data)
		return
	}
	if servings > b.PartySize {
		data["Message"] = "El número de raciones no puede ser mayor que el número de comensales (" + strconv.Itoa(b.PartySize) + ")."
		data["ShowForm"] = true
		writeHTMLTemplate(w, bookRiceTmpl, data)
		return
	}

	oldRiceType := ""
	oldRiceServs := ""
	if b.ArrozType.Valid {
		oldRiceType = b.ArrozType.String
	}
	if b.ArrozServings.Valid {
		oldRiceServs = b.ArrozServings.String
	}

	typeJSON, _ := json.Marshal([]string{selectedRice})
	servJSON, _ := json.Marshal([]int{servings})
	_, err = s.db.ExecContext(r.Context(), "UPDATE bookings SET arroz_type = ?, arroz_servings = ? WHERE restaurant_id = ? AND id = ?", string(typeJSON), string(servJSON), restaurantID, b.ID)
	if err != nil {
		data["Message"] = "Error al actualizar la reserva. Por favor, inténtelo de nuevo."
		data["ShowForm"] = true
		writeHTMLTemplate(w, bookRiceTmpl, data)
		return
	}

	data["Success"] = true
	data["Countdown"] = true
	data["Message"] = "¡Arroz reservado correctamente para su reserva!"
	data["ShowForm"] = false
	writeHTMLTemplate(w, bookRiceTmpl, data)

	// Best-effort: notify restaurant about rice change.
	newRiceFormatted := selectedRice + " x " + strconv.Itoa(servings)
	oldFormatted := formatArrozList(sql.NullString{String: oldRiceType, Valid: strings.TrimSpace(oldRiceType) != ""}, sql.NullString{String: oldRiceServs, Valid: strings.TrimSpace(oldRiceServs) != ""})
	msg := "🔄 *ARROZ MODIFICADO* 🔄\n\n"
	msg += "*Detalles de la reserva:*\n"
	msg += "━━━━━━━━━━━━━━━━━━━━\n"
	msg += "📋 *ID:* #" + strconv.Itoa(b.ID) + "\n"
	msg += "👤 *Cliente:* " + strings.TrimSpace(b.CustomerName) + "\n"
	msg += "📞 *Teléfono:* " + defaultString(b.ContactPhone, "No disponible") + "\n"
	msg += "📅 *Fecha:* " + dateDisplay + "\n"
	msg += "⏰ *Hora:* " + timeDisplay + "\n"
	msg += "👥 *Personas:* " + strconv.Itoa(b.PartySize) + "\n"
	msg += "━━━━━━━━━━━━━━━━━━━━\n"
	if oldFormatted != "No Arroz" {
		msg += "❌ *Arroz anterior:* " + oldFormatted + "\n"
	} else {
		msg += "❌ *Arroz anterior:* Sin arroz\n"
	}
	msg += "✅ *Arroz nuevo:* " + newRiceFormatted + "\n"
	msg += "━━━━━━━━━━━━━━━━━━━━\n"
	msg += "🕐 *Hora modificación:* " + time.Now().Format("15:04 02/01/2006")
	s.sendRestaurantWhatsAppText(context.Background(), restaurantID, msg)
}

func writeHTMLTemplate(w http.ResponseWriter, tmpl *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := tmpl.Execute(w, data); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": err.Error(),
		})
	}
}
