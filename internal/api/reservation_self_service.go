package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

// Coordination id: reservation_self_modification_v1
//
// Public self-service flow that resolves the "same contact details for the same
// day" duplicate the phone team kept deleting by hand:
//
//	POST /reservations/contact-lookup -> duplicate? (+ modifiable? + summary)
//	GET  /reservations/modify-context -> booking to prefill the modify form
//	POST /reservations/modify         -> apply the customer edit
//
// The modifiability rules live in one place (evaluateSelfModifiable) so the
// modal copy, the modify page and the write path can never disagree.
const (
	selfModifyReasonSameDay       = "same_day"
	selfModifyReasonSpecialLocked = "special_date_locked"
	selfModifyReasonCancelled     = "cancelled"
	selfModifyReasonPast          = "past_date"
)

type selfServiceBooking struct {
	ID               int
	ReservationDate  string
	ReservationTime  string
	PartySize        int
	Children         int
	CustomerName     string
	ContactEmail     string
	ContactPhone     string
	ContactPhoneCC   string
	HighChairs       int
	BabyStrollers    int
	Status           string
	SpecialTitle     string
	IsSpecialBooking int
}

// selfServiceBookingSelect is the single projection shared by the lookup and
// the prefill read so both understand the exact same booking shape.
const selfServiceBookingSelect = `
	SELECT
		b.id,
		DATE_FORMAT(b.reservation_date, '%Y-%m-%d'),
		TIME_FORMAT(b.reservation_time, '%H:%i:%s'),
		b.party_size,
		COALESCE(b.children, 0),
		b.customer_name,
		COALESCE(b.contact_email, ''),
		COALESCE(b.contact_phone, ''),
		COALESCE(b.contact_phone_country_code, ''),
		COALESCE(b.highChairs, 0),
		COALESCE(b.babyStrollers, 0),
		COALESCE(b.status, ''),
		COALESCE(sd.title, ''),
		COALESCE(b.is_special_booking, 0)
	FROM bookings b
	LEFT JOIN special_dates sd
	  ON sd.restaurant_id = b.restaurant_id
	 AND sd.date = b.reservation_date
	 AND sd.is_active = 1`

func scanSelfServiceBooking(row interface{ Scan(...any) error }) (selfServiceBooking, error) {
	var b selfServiceBooking
	err := row.Scan(
		&b.ID, &b.ReservationDate, &b.ReservationTime, &b.PartySize, &b.Children,
		&b.CustomerName, &b.ContactEmail, &b.ContactPhone, &b.ContactPhoneCC,
		&b.HighChairs, &b.BabyStrollers, &b.Status, &b.SpecialTitle, &b.IsSpecialBooking,
	)
	return b, err
}

// jsonView is the wire shape consumed by the preact client (camelCase, same as
// the confirm/cancel pages).
func (b selfServiceBooking) jsonView() map[string]any {
	return map[string]any{
		"id":                      b.ID,
		"reservationDate":         b.ReservationDate,
		"reservationTime":         formatHHMM(b.ReservationTime),
		"partySize":               b.PartySize,
		"children":                b.Children,
		"customerName":            b.CustomerName,
		"contactEmail":            b.ContactEmail,
		"contactPhone":            b.ContactPhone,
		"contactPhoneCountryCode": b.ContactPhoneCC,
		"highChairs":              b.HighChairs,
		"babyStrollers":           b.BabyStrollers,
		"specialDateTitle":        b.SpecialTitle,
	}
}

func (s *Server) fetchSelfServiceBooking(ctx context.Context, restaurantID, id int) (selfServiceBooking, error) {
	row := s.db.QueryRowContext(ctx, selfServiceBookingSelect+`
		WHERE b.restaurant_id = ? AND b.id = ?
		LIMIT 1`, restaurantID, id)
	return scanSelfServiceBooking(row)
}

// findDuplicateContactBooking returns the newest live booking on `date` whose
// email OR phone matches the contact the guest just typed.
func (s *Server) findDuplicateContactBooking(ctx context.Context, restaurantID int, date, email, cc, national string) (selfServiceBooking, bool, error) {
	email = strings.TrimSpace(email)
	if email == "" && strings.TrimSpace(national) == "" {
		return selfServiceBooking{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, selfServiceBookingSelect+`
		WHERE b.restaurant_id = ?
		  AND b.reservation_date = ?
		  AND (b.status IS NULL OR LOWER(TRIM(b.status)) NOT IN ('cancelled', 'cancelada'))
		  AND (
		        (? <> '' AND LOWER(TRIM(COALESCE(b.contact_email, ''))) = LOWER(?))
		     OR (? <> '' AND b.contact_phone = ? AND COALESCE(b.contact_phone_country_code, '') = ?)
		  )
		ORDER BY b.id DESC
		LIMIT 1`,
		restaurantID, date,
		email, email,
		national, national, cc,
	)
	b, err := scanSelfServiceBooking(row)
	if err == sql.ErrNoRows {
		return selfServiceBooking{}, false, nil
	}
	if err != nil {
		return selfServiceBooking{}, false, err
	}
	return b, true, nil
}

// evaluateSelfModifiable is the single gate for the "modify instead of rebook"
// offer. Same-day (and past) bookings can never be self-modified, and active
// special dates only allow it when the operator enabled it for that date.
func (s *Server) evaluateSelfModifiable(ctx context.Context, restaurantID int, b selfServiceBooking) (bool, string) {
	status := strings.ToLower(strings.TrimSpace(b.Status))
	if status == "cancelled" || status == "cancelada" {
		return false, selfModifyReasonCancelled
	}
	today := time.Now().Format("2006-01-02")
	if b.ReservationDate == today {
		return false, selfModifyReasonSameDay
	}
	if b.ReservationDate < today {
		return false, selfModifyReasonPast
	}
	var isActive, allowed int
	err := s.db.QueryRowContext(ctx, `
		SELECT is_active, allow_customer_modification
		FROM special_dates
		WHERE restaurant_id = ? AND date = ?
		LIMIT 1`, restaurantID, b.ReservationDate).Scan(&isActive, &allowed)
	if err == nil && isActive == 1 && allowed == 0 {
		return false, selfModifyReasonSpecialLocked
	}
	return true, ""
}

func selfModifyReasonMessage(reason string) string {
	switch reason {
	case selfModifyReasonSameDay:
		return "Las reservas para hoy no se pueden modificar online. Contacta con el restaurante."
	case selfModifyReasonSpecialLocked:
		return "Esta fecha especial no permite la modificacion online. Contacta con el restaurante."
	case selfModifyReasonCancelled:
		return "La reserva esta cancelada y no se puede modificar."
	case selfModifyReasonPast:
		return "La reserva ya ha pasado y no se puede modificar."
	default:
		return "No se puede modificar la reserva online. Contacta con el restaurante."
	}
}

// handleReservationContactLookup backs the personal-details step of the booking
// wizard: it is called when the guest presses "Continuar".
func (s *Server) handleReservationContactLookup(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Unknown restaurant"})
		return
	}

	var input struct {
		ReservationDate string `json:"reservation_date"`
		ContactEmail    string `json:"contact_email"`
		CountryCode     string `json:"country_code"`
		ContactPhone    string `json:"contact_phone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "JSON invalido"})
		return
	}

	date := strings.TrimSpace(input.ReservationDate)
	if !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Fecha invalida"})
		return
	}

	cc, national, _, phoneOK := normalizePhoneParts(input.CountryCode, input.ContactPhone)
	if !phoneOK {
		cc, national = "", ""
	}
	email := strings.TrimSpace(input.ContactEmail)
	if email == "" && national == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "duplicate": false})
		return
	}

	b, found, err := s.findDuplicateContactBooking(r.Context(), restaurantID, date, email, cc, national)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error consultando reservas"})
		return
	}
	if !found {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "duplicate": false})
		return
	}

	modifiable, reason := s.evaluateSelfModifiable(r.Context(), restaurantID, b)
	resp := map[string]any{
		"success":    true,
		"duplicate":  true,
		"modifiable": modifiable,
		"booking":    b.jsonView(),
	}
	if modifiable {
		resp["modifyUrl"] = "/reservas/modificar?id=" + strconv.Itoa(b.ID)
	} else {
		resp["reason"] = reason
		resp["message"] = selfModifyReasonMessage(reason)
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// handleReservationModifyContext returns the booking prefill for the modify
// route, plus whether it is still eligible.
func (s *Server) handleReservationModifyContext(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Unknown restaurant"})
		return
	}
	id, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("id")))
	if id <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "ID de reserva invalido"})
		return
	}

	b, err := s.fetchSelfServiceBooking(r.Context(), restaurantID, id)
	if err == sql.ErrNoRows {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Reserva no encontrada"})
		return
	}
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error consultando la reserva"})
		return
	}

	modifiable, reason := s.evaluateSelfModifiable(r.Context(), restaurantID, b)
	resp := map[string]any{
		"success":    true,
		"modifiable": modifiable,
		"booking":    b.jsonView(),
		// A special-menu booking carries a frozen menu / adelanto snapshot, so
		// moving it to another day would leave that snapshot stale. Time, party
		// and contact edits stay open, only the date is pinned.
		"dateLocked": b.IsSpecialBooking == 1,
	}
	if !modifiable {
		resp["reason"] = reason
		resp["message"] = selfModifyReasonMessage(reason)
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// handleReservationModify applies a customer edit and records it as a
// "customer" modification so the backoffice Modificadas tab stays truthful.
func (s *Server) handleReservationModify(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Unknown restaurant"})
		return
	}

	var input struct {
		BookingID       int    `json:"booking_id"`
		ReservationDate string `json:"reservation_date"`
		ReservationTime string `json:"reservation_time"`
		PartySize       int    `json:"party_size"`
		Children        int    `json:"children"`
		CustomerName    string `json:"customer_name"`
		ContactEmail    string `json:"contact_email"`
		CountryCode     string `json:"country_code"`
		ContactPhone    string `json:"contact_phone"`
		HighChairs      int    `json:"high_chairs"`
		BabyStrollers   int    `json:"baby_strollers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "JSON invalido"})
		return
	}
	if input.BookingID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "ID de reserva invalido"})
		return
	}

	old, err := s.fetchSelfServiceBooking(r.Context(), restaurantID, input.BookingID)
	if err == sql.ErrNoRows {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Reserva no encontrada"})
		return
	}
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "Error consultando la reserva"})
		return
	}

	if modifiable, reason := s.evaluateSelfModifiable(r.Context(), restaurantID, old); !modifiable {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"reason":  reason,
			"message": selfModifyReasonMessage(reason),
		})
		return
	}

	date := strings.TrimSpace(input.ReservationDate)
	if !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Fecha invalida"})
		return
	}
	// A booking can never be moved to today or into the past.
	if date <= time.Now().Format("2006-01-02") {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "La nueva fecha debe ser posterior a hoy"})
		return
	}
	// Special-menu bookings keep their frozen menu / adelanto snapshot: the date
	// is not editable through self-service, call the restaurant for a move.
	if old.IsSpecialBooking == 1 && date != old.ReservationDate {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"reason":  selfModifyReasonSpecialLocked,
			"message": "Para cambiar de fecha una reserva de menu especial, contacta con el restaurante.",
		})
		return
	}
	resTime, err := ensureHHMMSS(strings.TrimSpace(input.ReservationTime))
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Hora invalida"})
		return
	}
	if input.PartySize < 2 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Numero de comensales invalido"})
		return
	}
	name := strings.TrimSpace(input.CustomerName)
	if name == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Nombre requerido"})
		return
	}
	email := strings.TrimSpace(input.ContactEmail)
	if !strings.Contains(email, "@") {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Email invalido"})
		return
	}
	cc, national, _, phoneOK := normalizePhoneParts(input.CountryCode, input.ContactPhone)
	if !phoneOK {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Telefono invalido"})
		return
	}

	party := input.PartySize
	children := clampIntRange(input.Children, 0, party-1)
	highChairs := clampIntRange(input.HighChairs, 0, party)
	strollers := clampIntRange(input.BabyStrollers, 0, party)

	// Reconcile the occupancy ledger when the date or party size changes.
	oldDate, oldParty, oldFloor, oldSalon, locErr := s.bookingLocationSnapshot(r.Context(), restaurantID, input.BookingID)

	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE bookings SET
			reservation_date = ?,
			reservation_time = ?,
			party_size = ?,
			children = ?,
			customer_name = ?,
			contact_email = ?,
			contact_phone = ?,
			contact_phone_country_code = ?,
			highChairs = ?,
			babyStrollers = ?
		WHERE restaurant_id = ? AND id = ?`,
		date, resTime, party, children, name, email, national, cc, highChairs, strollers,
		restaurantID, input.BookingID,
	); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "No se pudo actualizar la reserva"})
		return
	}

	if locErr == nil {
		if nd, np, nf, ns, snapErr := s.bookingLocationSnapshot(r.Context(), restaurantID, input.BookingID); snapErr == nil {
			_ = s.applyBookingLocationDelta(r.Context(), s.db, restaurantID, oldDate, oldFloor, oldSalon, oldParty, nd, nf, ns, np)
		}
	}

	s.recordCustomerBookingModifications(r.Context(), restaurantID, input.BookingID, old, date, resTime, party, children, highChairs, strollers)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "booking_id": input.BookingID})
}

// recordCustomerBookingModifications mirrors the backoffice patch tracker so a
// self-service edit lands in the Modificadas tab exactly like a staff edit.
// Coordination id: booking-modification-recorded.
func (s *Server) recordCustomerBookingModifications(ctx context.Context, restaurantID, bookingID int, old selfServiceBooking, date, resTime string, party, children, highChairs, strollers int) {
	phone := old.ContactPhoneCC + old.ContactPhone
	record := func(field, oldVal, newVal string) {
		s.insertBookingModification(ctx, restaurantID, bookingID, old.ReservationDate, field, oldVal, newVal, "customer", nil, "", old.CustomerName, phone)
	}
	record("date", old.ReservationDate, date)
	record("time", formatHHMM(old.ReservationTime), formatHHMM(resTime))
	record("party_size", strconv.Itoa(old.PartySize), strconv.Itoa(party))
	record("children", strconv.Itoa(old.Children), strconv.Itoa(children))
	record("high_chairs", strconv.Itoa(old.HighChairs), strconv.Itoa(highChairs))
	record("strollers", strconv.Itoa(old.BabyStrollers), strconv.Itoa(strollers))
}

// clampIntRange is the inclusive-range twin of clampInt (which parses strings).
func clampIntRange(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
