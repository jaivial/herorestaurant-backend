package api

import (
	"net/http"
	"strings"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleBODashboardMetrics(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date format. Use YYYY-MM-DD",
		})
		return
	}

	restaurantID := a.ActiveRestaurantID

	var (
		total       int
		confirmed   int
		pending     int
		totalPeople int
	)
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT
			COUNT(*) AS total,
			COALESCE(SUM(CASE WHEN status = 'confirmed' THEN 1 ELSE 0 END), 0) AS confirmed,
			COALESCE(SUM(CASE WHEN status = 'pending' OR status IS NULL OR status = '' THEN 1 ELSE 0 END), 0) AS pending,
			COALESCE(SUM(party_size), 0) AS totalPeople
		FROM bookings
		WHERE reservation_date = ? AND restaurant_id = ?
	`, date, restaurantID).Scan(&total, &confirmed, &pending, &totalPeople); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando bookings")
		return
	}

	var cancelled int
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT COUNT(*)
		FROM cancelled_bookings
		WHERE reservation_date = ? AND restaurant_id = ?
	`, date, restaurantID).Scan(&cancelled); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando cancelled_bookings")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"metrics": map[string]any{
			"date":        date,
			"total":       total,
			"pending":     pending,
			"confirmed":   confirmed,
			"cancelled":   cancelled,
			"totalPeople": totalPeople,
		},
	})
}

