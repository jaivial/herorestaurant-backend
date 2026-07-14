package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

// ---------------------------------------------------------------------------
// DTO for public booking JSON responses
// ---------------------------------------------------------------------------

type publicBookingResponse struct {
	ID              int                  `json:"id"`
	ReservationDate string               `json:"reservationDate"`
	ReservationTime string               `json:"reservationTime"`
	PartySize       int                  `json:"partySize"`
	Adults          int                  `json:"adults"`
	Children        int                  `json:"children"`
	CustomerName    string               `json:"customerName"`
	ContactPhone    string               `json:"contactPhone,omitempty"`
	ContactEmail    string               `json:"contactEmail,omitempty"`
	ArrozType       string               `json:"arrozType,omitempty"`
	ArrozServings   string               `json:"arrozServings,omitempty"`
	ArrozDisplay    string               `json:"arrozDisplay,omitempty"`
	MenuDisplay     string               `json:"menuDisplay,omitempty"`
	Principales     []principalesJSONRow `json:"principales,omitempty"`
	Commentary      string               `json:"commentary,omitempty"`
	BabyStrollers   int                  `json:"babyStrollers"`
	HighChairs      int                  `json:"highChairs"`
	FloorDisplay    string               `json:"floorDisplay,omitempty"`
	TableNumber     string               `json:"tableNumber,omitempty"`
	Status          string               `json:"status,omitempty"`
	IsSameDay       bool                 `json:"isSameDay"`
	IsConfirmed     bool                 `json:"isConfirmed"`
}

func publicBookingToResponse(b *publicBooking) publicBookingResponse {
	dateDisplay := b.ReservationDate
	if t, err := time.Parse("2006-01-02", b.ReservationDate); err == nil {
		dateDisplay = t.Format("02/01/2006")
	}
	timeDisplay := formatHHMM(b.ReservationTime)
	today := time.Now().Format("2006-01-02")

	arrozDisplay := ""
	menuDisplay := ""
	commentary := nullStringValue(b.Commentary)
	if b.SpecialMenu.Valid && b.SpecialMenu.Int64 == 1 {
		commentary = ""
		if titles := parseJSONStringArray(nullStringValue(b.ArrozType)); len(titles) > 0 {
			menuDisplay = strings.TrimSpace(titles[0])
		}
		if menuDisplay == "" {
			menuDisplay = "Menú de grupo"
		}
	} else if s := formatArrozList(b.ArrozType, b.ArrozServings); s != "No Arroz" {
		arrozDisplay = s
	}

	children := b.Children
	if children < 0 || children > b.PartySize {
		children = 0
	}
	floorDisplay := ""
	if b.PreferredFloor.Valid && b.PreferredFloor.Int64 > 0 {
		floorDisplay = fmt.Sprintf("Planta %d", b.PreferredFloor.Int64)
	}

	status := ""
	if b.Status.Valid {
		status = strings.TrimSpace(b.Status.String)
	}

	return publicBookingResponse{
		ID:              b.ID,
		ReservationDate: dateDisplay,
		ReservationTime: timeDisplay,
		PartySize:       b.PartySize,
		Adults:          b.PartySize - children,
		Children:        children,
		CustomerName:    b.CustomerName,
		ContactPhone:    defaultString(b.ContactPhone, ""),
		ContactEmail:    defaultString(b.ContactEmail, ""),
		ArrozType:       nullStringValue(b.ArrozType),
		ArrozServings:   nullStringValue(b.ArrozServings),
		ArrozDisplay:    arrozDisplay,
		MenuDisplay:     menuDisplay,
		Principales:     parsePrincipalesJSON(nullStringValue(b.PrincipalesJSON)),
		Commentary:      commentary,
		BabyStrollers:   int(b.BabyStrollers.Int64),
		HighChairs:      int(b.HighChairs.Int64),
		FloorDisplay:    floorDisplay,
		TableNumber:     nullStringValue(b.TableNumber),
		Status:          status,
		IsSameDay:       today == b.ReservationDate,
		IsConfirmed:     status == "confirmed",
	}
}

func nullStringValue(v sql.NullString) string {
	if v.Valid {
		return strings.TrimSpace(v.String)
	}
	return ""
}

// ---------------------------------------------------------------------------
// GET /api/public/booking?id=123
// ---------------------------------------------------------------------------

// resolveRestaurantID tries context first, then falls back to DEFAULT_RESTAURANT_ID env var.
func (s *Server) resolveRestaurantID(r *http.Request) (int, bool) {
	if id, ok := restaurantIDFromContext(r.Context()); ok && id > 0 {
		return id, true
	}
	if def := strings.TrimSpace(os.Getenv("DEFAULT_RESTAURANT_ID")); def != "" {
		if id, err := strconv.Atoi(def); err == nil && id > 0 {
			return id, true
		}
	}
	// Last resort: look up by request host.
	host := strings.ToLower(stripPort(r.Host))
	if host != "" && s.db != nil {
		id, err := s.lookupRestaurantIDByDomain(r.Context(), host)
		if err == nil && id > 0 {
			return id, true
		}
	}
	return 0, false
}

func (s *Server) handlePublicBookingGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	restaurantID, ok := s.resolveRestaurantID(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Unknown restaurant"})
		return
	}
	r = r.WithContext(withRestaurantID(r.Context(), restaurantID))

	idRaw := strings.TrimSpace(r.URL.Query().Get("id"))
	id, err := strconv.Atoi(idRaw)
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "ID de reserva inválido"})
		return
	}

	b, err := s.fetchPublicBooking(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Reserva no encontrada"})
			return
		}
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error al cargar la reserva"})
		return
	}

	// Load rice options for rice page.
	rows, err := s.db.QueryContext(r.Context(),
		"SELECT DESCRIPCION FROM FINDE WHERE restaurant_id = ? AND TIPO = 'ARROZ' ORDER BY DESCRIPCION",
		restaurantID)
	riceOptions := []string{}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var d string
			if err := rows.Scan(&d); err == nil {
				d = strings.TrimSpace(d)
				if d != "" {
					riceOptions = append(riceOptions, d)
				}
			}
		}
	}

	resp := publicBookingToResponse(&b)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"booking":     resp,
		"riceOptions": riceOptions,
	})
}

// ---------------------------------------------------------------------------
// POST /api/public/booking/confirm  { id: 123 }
// ---------------------------------------------------------------------------

func (s *Server) handlePublicBookingConfirm(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := s.resolveRestaurantID(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Unknown restaurant"})
		return
	}
	r = r.WithContext(withRestaurantID(r.Context(), restaurantID))

	var body struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "ID de reserva inválido"})
		return
	}

	b, err := s.fetchPublicBooking(r.Context(), body.ID)
	if err != nil {
		if err == sql.ErrNoRows {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Reserva no encontrada"})
			return
		}
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error al cargar la reserva"})
		return
	}

	status := ""
	if b.Status.Valid {
		status = strings.TrimSpace(b.Status.String)
	}
	if status == "confirmed" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Esta reserva ya estaba confirmada.", "alreadyConfirmed": true})
		return
	}

	_, err = s.db.ExecContext(r.Context(),
		"UPDATE bookings SET status = 'confirmed' WHERE restaurant_id = ? AND id = ?",
		restaurantID, b.ID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error al confirmar la reserva"})
		return
	}

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

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "¡Su reserva ha sido confirmada correctamente!",
		"booking": publicBookingToResponse(&b),
	})
}

// ---------------------------------------------------------------------------
// POST /api/public/booking/cancel  { id: 123, cancelled_by?: "customer"|"staff" }
// ---------------------------------------------------------------------------

func (s *Server) handlePublicBookingCancel(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := s.resolveRestaurantID(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Unknown restaurant"})
		return
	}
	r = r.WithContext(withRestaurantID(r.Context(), restaurantID))

	var body struct {
		ID          int    `json:"id"`
		CancelledBy string `json:"cancelledBy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "ID de reserva inválido"})
		return
	}

	cancelledBy := strings.TrimSpace(body.CancelledBy)
	if cancelledBy == "" {
		cancelledBy = "customer"
	}
	if cancelledBy != "customer" && cancelledBy != "staff" && cancelledBy != "whatsapp" {
		cancelledBy = "customer"
	}

	b, err := s.fetchPublicBooking(r.Context(), body.ID)
	if err != nil {
		if err == sql.ErrNoRows {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Reserva no encontrada"})
			return
		}
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error al cargar la reserva"})
		return
	}

	// Same-day check
	today := time.Now().Format("2006-01-02")
	if today == b.ReservationDate {
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{
			"success":   false,
			"message":   "Las reservas para el mismo día no se pueden cancelar online. Por favor, llame al restaurante.",
			"isSameDay": true,
		})
		return
	}

	// Move to cancelled_bookings and delete.
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
				 cancelled_by_user_id, cancelled_by_name,
				 special_menu, menu_de_grupo_id, principales_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), ?, ?, ?, ?, ?, ?)
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
			nil, /* cancelled_by_user_id */
			nil, /* cancelled_by_name */
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
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error al cancelar la reserva"})
		return
	}

	resp := publicBookingToResponse(&b)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Su reserva ha sido cancelada correctamente.",
		"booking": resp,
	})

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

	// WhatsApp notification to restaurant.
	cancelledByText := "Cliente"
	if cancelledBy == "staff" {
		cancelledByText = "Equipo"
	}
	arrozFormatted := formatArrozList(b.ArrozType, b.ArrozServings)
	dateDisplay := b.ReservationDate
	if t, err := time.Parse("2006-01-02", b.ReservationDate); err == nil {
		dateDisplay = t.Format("02/01/2006")
	}
	msg := fmt.Sprintf("🚨 *RESERVA CANCELADA* 🚨\n\n*Detalles de la reserva:*\n━━━━━━━━━━━━━━━━━━━━\n📋 *ID:* #%d\n👤 *Cliente:* %s\n📞 *Teléfono:* %s\n📅 *Fecha:* %s\n⏰ *Hora:* %s\n👥 *Personas:* %d\n",
		b.ID,
		strings.TrimSpace(b.CustomerName),
		defaultString(b.ContactPhone, "No disponible"),
		dateDisplay,
		formatHHMM(b.ReservationTime),
		b.PartySize,
	)
	if arrozFormatted != "No Arroz" {
		msg += "🍚 *Arroz:* " + arrozFormatted + "\n"
	}
	msg += fmt.Sprintf("━━━━━━━━━━━━━━━━━━━━\n*Cancelada por:* %s\n🕐 *Hora cancelación:* %s",
		cancelledByText,
		time.Now().Format("15:04 02/01/2006"),
	)
	s.sendRestaurantWhatsAppText(context.Background(), restaurantID, msg)
}

// ---------------------------------------------------------------------------
// POST /api/public/booking/rice  { id: 123, riceType: "...", servings: 2 }
// ---------------------------------------------------------------------------

func (s *Server) handlePublicBookingRice(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := s.resolveRestaurantID(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Unknown restaurant"})
		return
	}
	r = r.WithContext(withRestaurantID(r.Context(), restaurantID))

	var body struct {
		ID       int    `json:"id"`
		RiceType string `json:"riceType"`
		Servings int    `json:"servings"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "ID de reserva inválido"})
		return
	}

	selectedRice := strings.TrimSpace(body.RiceType)
	if selectedRice == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Por favor, seleccione un tipo de arroz."})
		return
	}
	if body.Servings <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Por favor, indique un número válido de raciones."})
		return
	}

	b, err := s.fetchPublicBooking(r.Context(), body.ID)
	if err != nil {
		if err == sql.ErrNoRows {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Reserva no encontrada"})
			return
		}
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error al cargar la reserva"})
		return
	}

	// Same-day check.
	today := time.Now().Format("2006-01-02")
	if today == b.ReservationDate {
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{
			"success":   false,
			"message":   "Las reservas de arroz para el mismo día deben hacerse por teléfono.",
			"isSameDay": true,
		})
		return
	}

	if body.Servings > b.PartySize {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": fmt.Sprintf("El número de raciones no puede ser mayor que el número de comensales (%d).", b.PartySize),
		})
		return
	}

	oldRiceType := nullStringValue(b.ArrozType)
	oldRiceServs := nullStringValue(b.ArrozServings)

	typeJSON, _ := json.Marshal([]string{selectedRice})
	servJSON, _ := json.Marshal([]int{body.Servings})
	_, err = s.db.ExecContext(r.Context(),
		"UPDATE bookings SET arroz_type = ?, arroz_servings = ? WHERE restaurant_id = ? AND id = ?",
		string(typeJSON), string(servJSON), restaurantID, b.ID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error al actualizar la reserva"})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "¡Arroz reservado correctamente para su reserva!",
	})

	// WhatsApp notification.
	newRiceFormatted := selectedRice + " x " + strconv.Itoa(body.Servings)
	oldFormatted := formatArrozList(
		sql.NullString{String: oldRiceType, Valid: strings.TrimSpace(oldRiceType) != ""},
		sql.NullString{String: oldRiceServs, Valid: strings.TrimSpace(oldRiceServs) != ""},
	)
	dateDisplay := b.ReservationDate
	if t, err := time.Parse("2006-01-02", b.ReservationDate); err == nil {
		dateDisplay = t.Format("02/01/2006")
	}
	msg := fmt.Sprintf("🔄 *ARROZ MODIFICADO* 🔄\n\n*Detalles de la reserva:*\n━━━━━━━━━━━━━━━━━━━━\n📋 *ID:* #%d\n👤 *Cliente:* %s\n📞 *Teléfono:* %s\n📅 *Fecha:* %s\n⏰ *Hora:* %s\n👥 *Personas:* %d\n━━━━━━━━━━━━━━━━━━━━\n",
		b.ID,
		strings.TrimSpace(b.CustomerName),
		defaultString(b.ContactPhone, "No disponible"),
		dateDisplay,
		formatHHMM(b.ReservationTime),
		b.PartySize,
	)
	if oldFormatted != "No Arroz" {
		msg += "❌ *Arroz anterior:* " + oldFormatted + "\n"
	} else {
		msg += "❌ *Arroz anterior:* Sin arroz\n"
	}
	msg += fmt.Sprintf("✅ *Arroz nuevo:* %s\n━━━━━━━━━━━━━━━━━━━━\n🕐 *Hora modificación:* %s",
		newRiceFormatted,
		time.Now().Format("15:04 02/01/2006"),
	)
	s.sendRestaurantWhatsAppText(context.Background(), restaurantID, msg)
}

// ---------------------------------------------------------------------------
// GET /api/public/booking-policies
// ---------------------------------------------------------------------------

func (s *Server) handlePublicBookingPolicies(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := s.resolveRestaurantID(r)
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Unknown restaurant"})
		return
	}
	r = r.WithContext(withRestaurantID(r.Context(), restaurantID))

	branding, _ := s.loadRestaurantBranding(r.Context(), restaurantID)
	brandName := strings.TrimSpace(branding.BrandName)
	if brandName == "" {
		brandName = "Restaurante"
	}

	// Source the policies HTML from the per-tenant legal_pages row
	// (slug=booking-policies). Fall back to the seeded var only if the row is
	// missing, so the endpoint never returns empty content.
	policies := BookingPoliciesHTML
	updatedDate := "10/12/2024"
	if page, err := s.fetchLegalPage(r, restaurantID, "booking-policies"); err == nil {
		if strings.TrimSpace(page.ContentHTML) != "" {
			policies = page.ContentHTML
		}
		if !page.UpdatedAt.IsZero() {
			updatedDate = page.UpdatedAt.Format("02/01/2006")
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"brandName":   brandName,
		"policies":    policies,
		"updatedDate": updatedDate,
	})
}

var BookingPoliciesHTML = `<h2>I. Información General</h2>
<p>El presente documento establece las condiciones de reserva y políticas aplicables a todos los clientes que realicen una reserva en el restaurante. Al realizar una reserva, el cliente acepta íntegramente estas condiciones.</p>

<h2>II. Precios y Menús</h2>
<h3>Información sobre Precios</h3>
<p>Los precios de cada menú son los que se muestran en la página de menús correspondiente, en la sección de precios de cada tipo de menú. Las condiciones específicas y la vigencia de cada menú también están indicadas en la página de cada menú.</p>
<p><strong>Vigencia de los precios y condiciones:</strong> Los precios, condiciones y políticas aplicables a una reserva son únicamente los vigentes en la fecha de la reserva realizada. Estos pueden variar de forma dinámica según las necesidades del restaurante, por lo que se recomienda consultar siempre la información actualizada antes de realizar una reserva.</p>

<h3>Contenido del Menú</h3>
<p>El contenido incluido en cada menú (entrantes, platos principales, postres, cafés, bebidas, etc.) está especificado en la página de cada menú. Se recomienda consultar dicha información antes de realizar la reserva para conocer exactamente qué incluye cada opción.</p>
<p><strong>Variación de platos:</strong> Los platos ofrecidos pueden variar de un día a otro, e incluso durante el propio servicio, debido a la producción de cocina, disponibilidad de ingredientes o decisión del restaurante. Al realizar una reserva, el cliente acepta que esta variación puede ocurrir y que el restaurante no está obligado a mantener todos los platos indicados en la carta en todo momento.</p>

<h2>III. Consumo Mínimo</h2>
<p>Se establece un consumo mínimo obligatorio de 1 menú por cada plaza reservada en la mesa, independientemente de la edad de los comensales. Esta política se aplica a todas las reservas sin excepción.</p>
<p><strong>Menú infantil:</strong> No disponemos de menú infantil. Los menores de edad deberán consumir el menú estándar del restaurante.</p>

<h2>IV. Política de No Asistencia (No-Show)</h2>
<p>Si un cliente no se presenta a su reserva y han transcurrido <strong>30 minutos</strong> desde la hora reservada sin haber notificado previamente al restaurante, la mesa quedará automáticamente libre y podrá ser asignada a otros clientes.</p>
<p>En caso de retraso o imposibilidad de asistir, rogamos que se comunique al restaurante lo antes posible para poder gestionar adecuadamente las mesas y ofrecer el servicio a otros clientes.</p>

<h3>Canales de Comunicación</h3>
<p>Para notificar retrasos, modificaciones o cancelaciones de reservas:</p>
<ul><li>Teléfono: 638 85 72 94</li><li>Email: reservas@alqueriavillacarmen.com</li></ul>

<h2>V. Modificación y Cancelación de Reservas</h2>
<p>Los clientes pueden modificar o cancelar su reserva contactando con el restaurante a través de los canales indicados. Se ruega que cualquier cambio se comunique con la mayor antelación posible para permitir una gestión eficiente del servicio.</p>

<h2>VI. Reserva de Arroces</h2>
<p>Los platos de arroz únicamente pueden servirse con reserva previa. Si el cliente desea disfrutar de los arroces del restaurante, deberá indicarlo en el momento de realizar la reserva o contactar posteriormente con el restaurante para añadirlo a su reserva.</p>

<h2>VII. Servicios Adicionales</h2>
<h3>Tronas y Carritos</h3>
<p>El restaurante dispone de tronas para bebés y espacio para carritos. Se ruega indicar estas necesidades al realizar la reserva para tenerlo preparado a la llegada del cliente.</p>
<h3>Envases para Llevar</h3>
<p>De conformidad con la Ley 7/2022, de 8 de abril, de residuos y suelos contaminados para una economía circular, el coste de los envases para llevar es de 1€ por envase. Este cobro es obligatorio por ley.</p>

<h2>VIII. Grupos</h2>
<p>Para grupos de más de 10 personas, se recomienda contactar directamente con el restaurante para gestionar la reserva de forma personalizada y garantizar la mejor experiencia posible.</p>

<h2>IX. Ubicación de la Reserva</h2>
<p>En determinadas fechas o circunstancias, las reservas podrán ubicarse en la primera planta del establecimiento, la cual no dispone de ascensor. En caso de que esto afecte a su reserva, será informado previamente.</p>

<h2>X. Modificación de las Condiciones</h2>
<p>El restaurante se reserva el derecho de modificar estas condiciones de reserva en cualquier momento. Las modificaciones serán efectivas inmediatamente después de su publicación en el sitio web. Se recomienda revisar periódicamente estas condiciones para estar informado sobre los cambios.</p>
<p>Las condiciones aplicables a cada reserva serán las vigentes en el momento de realizarse dicha reserva.</p>

<h2>XI. Aceptación de las Condiciones</h2>
<p>Al realizar una reserva, el cliente declara haber leído, comprendido y aceptado íntegramente las presentes condiciones de reserva y políticas del restaurante.</p>`
