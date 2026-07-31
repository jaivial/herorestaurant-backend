package api

import (
	"context"
	"net/http"
	"strings"
	"time"

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
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var (
		total       int
		confirmed   int
		pending     int
		totalPeople int
	)
	var cancelled int
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) AS total,
			COALESCE(SUM(CASE WHEN status = 'confirmed' THEN 1 ELSE 0 END), 0) AS confirmed,
			COALESCE(SUM(CASE WHEN status = 'pending' OR status IS NULL OR status = '' THEN 1 ELSE 0 END), 0) AS pending,
			COALESCE(SUM(party_size), 0) AS totalPeople,
			(SELECT COUNT(*) FROM cancelled_bookings WHERE reservation_date = ? AND restaurant_id = ?) AS cancelled
		FROM bookings
		WHERE reservation_date = ? AND restaurant_id = ?
	`, date, restaurantID, date, restaurantID).Scan(&total, &confirmed, &pending, &totalPeople, &cancelled); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando bookings")
		return
	}

	var invoiceMetrics any
	if dashboardContainsSection(a.User.SectionAccess, boSectionFacturas) {
		fromMonth, fromWeek, through := invoiceDashboardRange(time.Now())
		metrics := map[string]any{}
		var pendingCount, weekSentCount int
		var pendingAmount, monthIncome float64
		if err := s.db.QueryRowContext(ctx, `
			SELECT
				COALESCE(SUM(CASE WHEN status = 'pendiente' THEN 1 ELSE 0 END), 0),
				COALESCE(SUM(CASE WHEN status = 'pendiente' THEN amount ELSE 0 END), 0),
				COALESCE(SUM(CASE WHEN invoice_date BETWEEN ? AND ? THEN amount ELSE 0 END), 0),
				COALESCE(SUM(CASE WHEN status = 'enviada' AND invoice_date BETWEEN ? AND ? THEN 1 ELSE 0 END), 0)
			FROM invoices
			WHERE restaurant_id = ?
		`, fromMonth, through, fromWeek, through, restaurantID).Scan(&pendingCount, &pendingAmount, &monthIncome, &weekSentCount); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error consultando facturas")
			return
		}
		metrics["pendingCount"] = pendingCount
		metrics["pendingAmount"] = pendingAmount
		metrics["monthIncome"] = monthIncome
		metrics["weekSentCount"] = weekSentCount
		invoiceMetrics = metrics
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
		"invoiceMetrics": invoiceMetrics,
	})
}

func invoiceDashboardRange(now time.Time) (fromMonth string, fromWeek string, through string) {
	date := now.In(time.Local)
	month := time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, date.Location())
	daysSinceMonday := (int(date.Weekday()) + 6) % 7
	week := time.Date(date.Year(), date.Month(), date.Day()-daysSinceMonday, 0, 0, 0, 0, date.Location())
	return month.Format("2006-01-02"), week.Format("2006-01-02"), date.Format("2006-01-02")
}

func dashboardContainsSection(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
