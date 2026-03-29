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

// publicBaseURLForRestaurant resolves the base URL using the restaurant's primary domain from DB.
// Priority: env var > DB primary domain > request host > hardcoded fallback.
func publicBaseURLForRestaurant(r *http.Request, s *Server, restaurantID int) string {
	if base := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/"); base != "" {
		return base
	}
	if domain := s.fetchPrimaryDomain(r.Context(), restaurantID); domain != "" && domain != "localhost" && domain != "127.0.0.1" {
		return "https://" + domain
	}
	// Fallback to request host.
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
  <style>` + publicPageCSS + `
  .booking-icon {
    background: var(--success-bg);
    border: 1px solid var(--success-border);
    color: var(--success);
  }
  </style>
</head>
<body>
  <main class="page-card" data-ui="confirm-reservation">
    <div class="logo-wrap">
      <img src="{{.LogoURL}}" alt="{{.BrandName}}" />
    </div>

    <h1 class="page-title" data-slot="title">Confirmar Reserva</h1>
    <p class="page-sub">Revise los datos y confirme su asistencia</p>

    {{if .Message}}
    <div class="alert {{if .Success}}alert-success{{else}}alert-danger{{end}}" role="status">
      <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">{{if .Success}}<path d="M20 6 9 17l-5-5"/>{{else}}<circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/>{{end}}</svg>
      <span>{{.Message}}</span>
    </div>
    {{end}}

    {{if .HasBooking}}
    <div class="booking-card" data-slot="details">
      <div class="booking-header">
        <div class="booking-icon">
          <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round"><path d="M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2"/><rect width="6" height="4" x="9" y="3" rx="1"/></svg>
        </div>
        <div>
          <div class="booking-name" data-role="customer-name">{{.CustomerName}}</div>
          <div class="booking-id">Reserva #{{.BookingID}}</div>
        </div>
      </div>
      <div class="detail-grid">
        <div class="detail-item"><span class="detail-label">Fecha</span><span class="detail-value">{{.DateDisplay}}</span></div>
        <div class="detail-item"><span class="detail-label">Hora</span><span class="detail-value">{{.TimeDisplay}}</span></div>
        <div class="detail-item"><span class="detail-label">Personas</span><span class="detail-value">{{.PartySize}}</span></div>
        {{if .ArrozDisplay}}<div class="detail-item full"><span class="detail-label">Arroz</span><span class="detail-value">{{.ArrozDisplay}}</span></div>{{end}}
      </div>
    </div>
    {{end}}

    {{if .ShowConfirmation}}
    <form method="post" action="{{.Action}}">
      <button class="btn btn-success" type="submit" name="confirm_booking" value="1">
        <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>
        Confirmar Reserva
      </button>
    </form>
    <a href="/" class="btn btn-accent">Volver al inicio</a>
    {{else}}
    <a href="/" class="btn btn-accent">Volver al inicio</a>
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

var publicPageCSS = `
  *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
  :root {
    --bg: #0f1015;
    --surface: #1a1b22;
    --surface-2: #22232b;
    --border: rgba(255,255,255,0.06);
    --border-2: rgba(255,255,255,0.09);
    --text: #eef0f6;
    --text-muted: rgba(238,240,246,0.62);
    --text-faint: rgba(238,240,246,0.46);
    --accent: #b9a8ff;
    --accent-2: #93efe7;
    --success: #16a34a;
    --success-bg: rgba(22,163,74,0.12);
    --success-border: rgba(22,163,74,0.3);
    --danger: #dc2626;
    --danger-bg: rgba(220,38,38,0.12);
    --danger-border: rgba(220,38,38,0.3);
    --warning: #d97706;
    --warning-bg: rgba(217,119,6,0.12);
    --warning-border: rgba(217,119,6,0.3);
    --radius-sm: 8px;
    --radius-md: 12px;
    --radius-lg: 16px;
    --radius-xl: 20px;
    --font: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, Cantarell, sans-serif;
    --shadow: 0 8px 32px rgba(0,0,0,0.25);
  }
  @media (prefers-color-scheme: light) {
    :root {
      --bg: #f0f2f7;
      --surface: #ffffff;
      --surface-2: #f8f9fc;
      --border: rgba(0,0,0,0.08);
      --border-2: rgba(0,0,0,0.12);
      --text: rgba(20,21,26,0.95);
      --text-muted: rgba(20,21,26,0.58);
      --text-faint: rgba(20,21,26,0.42);
      --accent: #7c5ce7;
      --accent-2: #0d9488;
      --success: #16a34a;
      --success-bg: rgba(22,163,74,0.08);
      --success-border: rgba(22,163,74,0.2);
      --danger: #dc2626;
      --danger-bg: rgba(220,38,38,0.08);
      --danger-border: rgba(220,38,38,0.2);
      --warning: #d97706;
      --warning-bg: rgba(217,119,6,0.08);
      --warning-border: rgba(217,119,6,0.2);
      --shadow: 0 8px 32px rgba(0,0,0,0.08);
    }
  }
  html { font-size: 16px; -webkit-text-size-adjust: 100%; }
  body {
    font-family: var(--font);
    line-height: 1.6;
    color: var(--text);
    background: var(--bg);
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 16px;
  }
  @media (prefers-reduced-motion: reduce) {
    *, *::before, *::after { animation: none !important; transition: none !important; }
  }
  .page-card {
    width: 100%;
    max-width: 480px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-xl);
    padding: 28px 24px;
    box-shadow: var(--shadow);
    backdrop-filter: blur(18px);
    -webkit-backdrop-filter: blur(18px);
  }
  @media (min-width: 520px) { .page-card { padding: 36px 32px; } }
  .logo-wrap { text-align: center; margin-bottom: 24px; }
  .logo-wrap img { max-width: 140px; height: auto; }
  .page-title {
    font-size: clamp(1.25rem, 4vw, 1.5rem);
    font-weight: 600;
    text-align: center;
    margin-bottom: 4px;
  }
  .page-sub {
    font-size: 14px;
    color: var(--text-muted);
    text-align: center;
    margin-bottom: 24px;
  }
  .alert {
    padding: 14px 16px;
    border-radius: var(--radius-md);
    margin-bottom: 20px;
    display: flex;
    align-items: flex-start;
    gap: 12px;
    border: 1px solid transparent;
    font-size: 14px;
    line-height: 1.5;
  }
  .alert svg { flex-shrink: 0; width: 20px; height: 20px; stroke-width: 1.8; }
  .alert-success { background: var(--success-bg); border-color: var(--success-border); color: var(--success); }
  .alert-danger  { background: var(--danger-bg);  border-color: var(--danger-border);  color: var(--danger); }
  .alert-warning { background: var(--warning-bg); border-color: var(--warning-border); color: var(--warning); }
  .booking-card {
    background: var(--surface-2);
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    padding: 16px;
    margin-bottom: 20px;
  }
  .booking-header {
    display: flex; align-items: center; gap: 12px;
    margin-bottom: 12px; padding-bottom: 12px;
    border-bottom: 1px solid var(--border);
  }
  .booking-icon {
    width: 40px; height: 40px;
    border-radius: var(--radius-md);
    display: flex; align-items: center; justify-content: center;
  }
  .booking-icon svg { width: 20px; height: 20px; stroke-width: 1.8; }
  .booking-name { font-weight: 600; font-size: 15px; }
  .booking-id   { font-size: 12px; color: var(--text-muted); }
  .detail-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }
  .detail-item { display: flex; flex-direction: column; gap: 2px; }
  .detail-item.full { grid-column: 1 / -1; }
  .detail-label { font-size: 11px; text-transform: uppercase; letter-spacing: 0.5px; color: var(--text-faint); }
  .detail-value { font-size: 14px; font-weight: 500; }
  .btn {
    display: inline-flex; align-items: center; justify-content: center; gap: 8px;
    width: 100%; padding: 14px 20px;
    font-size: 15px; font-weight: 500;
    border-radius: var(--radius-md);
    cursor: pointer;
    text-decoration: none;
    border: 2px solid transparent;
    background: transparent;
    transition: background 120ms ease, color 120ms ease, border-color 120ms ease;
  }
  .btn:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  .btn-danger { border-color: var(--danger); color: var(--danger); }
  .btn-danger:hover { background: var(--danger); color: #fff; }
  .btn-success { border-color: var(--success); color: var(--success); }
  .btn-success:hover { background: var(--success); color: #fff; }
  .btn-accent { border-color: var(--accent); color: var(--accent); }
  .btn-accent:hover { background: var(--accent); color: #fff; }
  .btn-primary { background: var(--accent); color: #fff; border-color: var(--accent); }
  .btn-primary:hover { opacity: 0.9; }
  .btn + .btn { margin-top: 12px; }
  .note {
    font-size: 13px; color: var(--text-faint); text-align: center;
    margin-top: 16px; padding-top: 16px; border-top: 1px solid var(--border);
  }
  .form-group { margin-bottom: 16px; text-align: left; }
  .form-label { display: block; font-size: 13px; font-weight: 600; margin-bottom: 6px; color: var(--text-muted); }
  .form-control {
    width: 100%; height: 44px; padding: 0 12px;
    font-size: 15px; font-family: var(--font);
    background: var(--surface);
    color: var(--text);
    border: 1px solid var(--border-2);
    border-radius: var(--radius-sm);
    appearance: none;
    transition: border-color 120ms ease;
  }
  .form-control:focus { outline: none; border-color: var(--accent); }
  @media (max-width: 400px) {
    .page-card { padding: 20px 16px; border-radius: var(--radius-lg); }
    .detail-grid { grid-template-columns: 1fr; }
  }
`

var cancelReservationTmpl = template.Must(template.New("cancel_reservation").Parse(`<!DOCTYPE html>
<html lang="es">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Cancelar Reserva - {{.BrandName}}</title>
  <style>` + publicPageCSS + `
  .booking-icon {
    background: var(--danger-bg);
    border: 1px solid var(--danger-border);
    color: var(--danger);
  }
  </style>
</head>
<body>
  <main class="page-card" data-ui="cancel-reservation">
    <div class="logo-wrap">
      <img src="{{.LogoURL}}" alt="{{.BrandName}}" />
    </div>

    {{if .Success}}
    <h1 class="page-title" data-slot="title">Reserva Cancelada</h1>
    <p class="page-sub">Su reserva ha sido cancelada correctamente</p>
    <div class="alert alert-success" role="status">
      <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>
      <span>{{.Message}}</span>
    </div>
    {{if .HasBooking}}
    <div class="booking-card" data-slot="summary">
      <div class="booking-header">
        <div class="booking-icon">
          <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round"><path d="M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2"/><rect width="6" height="4" x="9" y="3" rx="1"/></svg>
        </div>
        <div>
          <div class="booking-name" data-role="customer-name">{{.CustomerName}}</div>
          <div class="booking-id">Reserva #{{.BookingID}}</div>
        </div>
      </div>
      <div class="detail-grid">
        <div class="detail-item"><span class="detail-label">Fecha</span><span class="detail-value">{{.DateDisplay}}</span></div>
        <div class="detail-item"><span class="detail-label">Hora</span><span class="detail-value">{{.TimeDisplay}}</span></div>
        <div class="detail-item"><span class="detail-label">Personas</span><span class="detail-value">{{.PartySize}}</span></div>
      </div>
    </div>
    {{end}}
    <a href="/" class="btn btn-accent">Volver al inicio</a>

    {{else if .IsSameDay}}
    <h1 class="page-title" data-slot="title">Cancelación No Disponible</h1>
    <p class="page-sub">Reserva para hoy</p>
    <div class="alert alert-warning" role="alert">
      <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
      <span>Las reservas para el mismo día no se pueden cancelar online. Por favor, llame al restaurante.</span>
    </div>
    {{if .HasBooking}}
    <div class="booking-card" data-slot="summary">
      <div class="booking-header">
        <div class="booking-icon">
          <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round"><path d="M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2"/><rect width="6" height="4" x="9" y="3" rx="1"/></svg>
        </div>
        <div>
          <div class="booking-name">{{.CustomerName}}</div>
          <div class="booking-id">Reserva #{{.BookingID}}</div>
        </div>
      </div>
      <div class="detail-grid">
        <div class="detail-item"><span class="detail-label">Fecha</span><span class="detail-value">{{.DateDisplay}}</span></div>
        <div class="detail-item"><span class="detail-label">Hora</span><span class="detail-value">{{.TimeDisplay}}</span></div>
        <div class="detail-item"><span class="detail-label">Personas</span><span class="detail-value">{{.PartySize}}</span></div>
      </div>
    </div>
    {{end}}
    <a href="tel:+34638857294" class="btn btn-success">
      <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M22 16.92v3a2 2 0 0 1-2.18 2 19.79 19.79 0 0 1-8.63-3.07 19.5 19.5 0 0 1-6-6 19.79 19.79 0 0 1-3.07-8.67A2 2 0 0 1 4.11 2h3a2 2 0 0 1 2 1.72 12.84 12.84 0 0 0 .7 2.81 2 2 0 0 1-.45 2.11L8.09 9.91a16 16 0 0 0 6 6l1.27-1.27a2 2 0 0 1 2.11-.45 12.84 12.84 0 0 0 2.81.7A2 2 0 0 1 22 16.92z"/></svg>
      Llamar ahora
    </a>
    <a href="/" class="btn btn-accent">Volver al inicio</a>

    {{else if .ShowConfirmation}}
    <h1 class="page-title" data-slot="title">Cancelar Reserva</h1>
    <p class="page-sub">Revise los detalles antes de confirmar</p>
    {{if .HasBooking}}
    <div class="booking-card" data-slot="details">
      <div class="booking-header">
        <div class="booking-icon">
          <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round"><path d="M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2"/><rect width="6" height="4" x="9" y="3" rx="1"/></svg>
        </div>
        <div>
          <div class="booking-name" data-role="customer-name">{{.CustomerName}}</div>
          <div class="booking-id">Reserva #{{.BookingID}}</div>
        </div>
      </div>
      <div class="detail-grid">
        <div class="detail-item"><span class="detail-label">Fecha</span><span class="detail-value">{{.DateDisplay}}</span></div>
        <div class="detail-item"><span class="detail-label">Hora</span><span class="detail-value">{{.TimeDisplay}}</span></div>
        <div class="detail-item"><span class="detail-label">Personas</span><span class="detail-value">{{.PartySize}}</span></div>
      </div>
    </div>
    {{end}}
    <form method="post" action="{{.Action}}">
      <input type="hidden" name="confirm_cancel" value="1" />
      <button type="submit" class="btn btn-danger">
        <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg>
        Cancelar Reserva
      </button>
    </form>
    <a href="/" class="btn btn-accent">Volver sin cancelar</a>
    <p class="note">Esta acción no se puede deshacer. Se notificará al restaurante de la cancelación.</p>

    {{else}}
    <h1 class="page-title" data-slot="title">Error</h1>
    <p class="page-sub">No se pudo procesar la solicitud</p>
    <div class="alert alert-danger" role="alert">
      <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>
      <span>{{.Message}}</span>
    </div>
    <a href="/" class="btn btn-accent">Volver al inicio</a>
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
  <style>` + publicPageCSS + `
  .booking-icon {
    background: rgba(183,168,255,0.12);
    border: 1px solid rgba(183,168,255,0.3);
    color: var(--accent);
  }
  </style>
</head>
<body>
  <main class="page-card" data-ui="book-rice">
    <div class="logo-wrap">
      <img src="{{.LogoURL}}" alt="{{.BrandName}}" />
    </div>

    {{if .Success}}
    <h1 class="page-title" data-slot="title">Arroz Reservado</h1>
    <p class="page-sub">{{.Message}}</p>
    {{if .HasBooking}}
    <div class="booking-card" data-slot="summary">
      <div class="booking-header">
        <div class="booking-icon">
          <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round"><path d="M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2"/><rect width="6" height="4" x="9" y="3" rx="1"/></svg>
        </div>
        <div>
          <div class="booking-name" data-role="customer-name">{{.CustomerName}}</div>
          <div class="booking-id">{{.DateDisplay}} · {{.TimeDisplay}} · {{.PartySize}} personas</div>
        </div>
      </div>
    </div>
    {{end}}
    <a href="/" class="btn btn-accent">Volver al inicio</a>
    {{if .Countdown}}
    <script>
      let seconds = 15;
      const el = document.getElementById('countdown');
      function tick() {
        seconds--;
        if (seconds > 0 && el) { el.textContent = 'Redirección en ' + seconds + ' segundos'; setTimeout(tick, 1000); }
        else { window.location.href = '/'; }
      }
      setTimeout(tick, 1000);
    </script>
    <p class="note" id="countdown">Redirección en 15 segundos</p>
    {{end}}

    {{else if .IsSameDay}}
    <h1 class="page-title" data-slot="title">No Disponible</h1>
    <p class="page-sub">Reserva para hoy</p>
    <div class="alert alert-warning" role="alert">
      <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
      <span>Las reservas de arroz para el mismo día deben hacerse por teléfono.</span>
    </div>
    <a href="tel:+34638857294" class="btn btn-success">
      <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M22 16.92v3a2 2 0 0 1-2.18 2 19.79 19.79 0 0 1-8.63-3.07 19.5 19.5 0 0 1-6-6 19.79 19.79 0 0 1-3.07-8.67A2 2 0 0 1 4.11 2h3a2 2 0 0 1 2 1.72 12.84 12.84 0 0 0 .7 2.81 2 2 0 0 1-.45 2.11L8.09 9.91a16 16 0 0 0 6 6l1.27-1.27a2 2 0 0 1 2.11-.45 12.84 12.84 0 0 0 2.81.7A2 2 0 0 1 22 16.92z"/></svg>
      Llamar ahora
    </a>
    <a href="/" class="btn btn-accent">Volver al inicio</a>

    {{else if .ShowForm}}
    <h1 class="page-title" data-slot="title">Reservar Arroz</h1>
    <p class="page-sub">Seleccione el tipo de arroz para su reserva</p>
    {{if .HasBooking}}
    <div class="booking-card" data-slot="details">
      <div class="booking-header">
        <div class="booking-icon">
          <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round"><path d="M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2"/><rect width="6" height="4" x="9" y="3" rx="1"/></svg>
        </div>
        <div>
          <div class="booking-name" data-role="customer-name">{{.CustomerName}}</div>
          <div class="booking-id">{{.DateDisplay}} · {{.TimeDisplay}} · {{.PartySize}} personas</div>
        </div>
      </div>
    </div>
    {{end}}
    <form method="post" action="{{.Action}}">
      <div class="form-group">
        <label class="form-label" for="rice_type">Tipo de arroz</label>
        <select class="form-control" id="rice_type" name="rice_type" required>
          <option value="">Seleccione una opción</option>
          {{range .RiceOptions}}
          <option value="{{.}}">{{.}}</option>
          {{end}}
        </select>
      </div>
      <div class="form-group">
        <label class="form-label" for="rice_servings">Raciones (máximo {{.PartySize}})</label>
        <input class="form-control" id="rice_servings" name="rice_servings" type="number" min="1" max="{{.PartySize}}" required />
      </div>
      <button class="btn btn-primary" type="submit" name="submit" value="1">Reservar Arroz</button>
    </form>
    <a href="/" class="btn btn-accent">Volver sin reservar</a>

    {{else if .HasBooking}}
    <h1 class="page-title" data-slot="title">Tu Arroz</h1>
    <p class="page-sub">Arroz actual de tu reserva</p>
    <div class="booking-card" data-slot="current-rice">
      <div class="booking-header">
        <div class="booking-icon">
          <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round"><path d="M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2"/><rect width="6" height="4" x="9" y="3" rx="1"/></svg>
        </div>
        <div>
          <div class="booking-name" data-role="customer-name">{{.CustomerName}}</div>
          <div class="booking-id">{{.DateDisplay}} · {{.TimeDisplay}}</div>
        </div>
      </div>
      <div class="detail-grid">
        <div class="detail-item full"><span class="detail-label">Arroz</span><span class="detail-value">{{.ArrozRawDisplay}}</span></div>
      </div>
    </div>
    <a href="/" class="btn btn-accent">Volver al inicio</a>

    {{else}}
    <h1 class="page-title" data-slot="title">Reservar Arroz</h1>
    <p class="page-sub">No se pudo procesar la solicitud</p>
    {{if .Message}}
    <div class="alert alert-danger" role="alert">
      <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>
      <span>{{.Message}}</span>
    </div>
    {{end}}
    <a href="/" class="btn btn-accent">Volver al inicio</a>
    {{end}}
  </main>
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
		"BrandName":   brandName,
		"LogoURL":     logoURL,
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
