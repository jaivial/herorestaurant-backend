package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleBOPOSReservationsEligible(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" {
		settings, loadErr := s.loadPOSSettings(r.Context(), a.ActiveRestaurantID)
		if loadErr != nil {
			httpx.WriteError(w, 500, "Error resolving reservation date")
			return
		}
		moment, momentErr := s.loadPOSBusinessMoment(r.Context(), a.ActiveRestaurantID, settings)
		if momentErr != nil {
			httpx.WriteError(w, 500, "Error resolving reservation date")
			return
		}
		date = moment.ServiceDate
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	like := "%" + q + "%"
	rows, err := s.db.QueryContext(r.Context(), `SELECT b.id,b.customer_name,DATE_FORMAT(b.reservation_date,'%Y-%m-%d'),TIME_FORMAT(b.reservation_time,'%H:%i'),b.party_size,COALESCE(b.status,'pending'),v.id,v.status FROM bookings b LEFT JOIN pos_visits v ON v.restaurant_id=b.restaurant_id AND v.booking_id=b.id WHERE b.restaurant_id=? AND b.reservation_date=? AND (?='' OR b.customer_name LIKE ? OR COALESCE(b.contact_phone,'') LIKE ?) ORDER BY b.reservation_time,b.id LIMIT 100`, a.ActiveRestaurantID, date, q, like, like)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading reservations")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, party int
		var name, resDate, resTime, status string
		var visitID sql.NullInt64
		var visitStatus sql.NullString
		if err = rows.Scan(&id, &name, &resDate, &resTime, &party, &status, &visitID, &visitStatus); err != nil {
			httpx.WriteError(w, 500, "Error reading reservations")
			return
		}
		items = append(items, map[string]any{"id": id, "customerName": name, "reservationDate": normalizePOSDate(resDate), "reservationTime": resTime, "partySize": party, "status": status, "visitId": stockNullableDBInt(visitID), "visitStatus": func() any {
			if visitStatus.Valid {
				return visitStatus.String
			}
			return nil
		}()})
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "items": items})
}

func (s *Server) handleBOPOSReservationVisit(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	bookingID, _ := strconv.Atoi(chiURLParam(r, "bookingId"))
	if bookingID <= 0 {
		httpx.WriteError(w, 400, "Invalid reservation")
		return
	}
	var visitID int64
	err := s.db.QueryRowContext(r.Context(), `SELECT id FROM pos_visits WHERE restaurant_id=? AND booking_id=?`, a.ActiveRestaurantID, bookingID).Scan(&visitID)
	if err == sql.ErrNoRows {
		httpx.WriteError(w, 404, "Reservation visit not found")
		return
	}
	if err != nil {
		httpx.WriteError(w, 500, "Error loading reservation visit")
		return
	}
	rows, err := s.loadPOSVisits(r.Context(), a.ActiveRestaurantID, "")
	if err != nil {
		httpx.WriteError(w, 500, "Error loading reservation visit")
		return
	}
	var visit map[string]any
	for _, candidate := range rows {
		if candidate["id"] == visitID {
			visit = candidate
			break
		}
	}
	if visit == nil {
		httpx.WriteError(w, 404, "Reservation visit not found")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "visit": visit})
}

func chiURLParam(r *http.Request, key string) string { return chi.URLParam(r, key) }
