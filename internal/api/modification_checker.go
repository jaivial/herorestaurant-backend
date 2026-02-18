package api

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleModificationChecker(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	senderNumberRaw := strings.TrimSpace(r.URL.Query().Get("senderNumber"))
	if senderNumberRaw == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Missing required parameter: senderNumber",
		})
		return
	}

	// Sender numbers come from WhatsApp automations (UAZAPI) and should be digits-only E.164 without '+'.
	// Accept a permissive range (E.164 max 15 digits).
	senderNumber := onlyDigits(senderNumberRaw)
	if senderNumber == "" || len(senderNumber) < 8 || len(senderNumber) > 15 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid phone number format. Expected digits-only E.164 without '+'",
		})
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT
			id,
			customer_name,
			reservation_date,
			babyStrollers,
			highChairs,
			arroz_type,
			arroz_servings
		FROM bookings
		WHERE restaurant_id = ?
		  AND CONCAT(COALESCE(contact_phone_country_code, '34'), COALESCE(contact_phone, '')) = ?
		  AND reservation_date >= NOW()
		ORDER BY reservation_date ASC
	`, restaurantID, senderNumber)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Database error occurred",
			"error":   err.Error(),
		})
		return
	}
	defer rows.Close()

	bookings := []map[string]any{}
	for rows.Next() {
		var (
			id         int
			name       string
			resDate    time.Time
			baby       sql.NullInt64
			high       sql.NullInt64
			arrozType  sql.NullString
			arrozServs sql.NullString
		)
		if err := rows.Scan(&id, &name, &resDate, &baby, &high, &arrozType, &arrozServs); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": "Database error occurred",
				"error":   err.Error(),
			})
			return
		}

		var babyVal any = nil
		if baby.Valid {
			babyVal = int(baby.Int64)
		}
		var highVal any = nil
		if high.Valid {
			highVal = int(high.Int64)
		}
		var arrozTypeVal any = nil
		if arrozType.Valid {
			arrozTypeVal = arrozType.String
		}
		var arrozServsVal any = nil
		if arrozServs.Valid {
			arrozServsVal = arrozServs.String
		}

		bookings = append(bookings, map[string]any{
			"id":               id,
			"customer_name":    name,
			"reservation_date": resDate.Format("2006-01-02"),
			"babyStrollers":    babyVal,
			"highChairs":       highVal,
			"arroz_type":       arrozTypeVal,
			"arroz_servings":   arrozServsVal,
		})
	}

	if len(bookings) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":     true,
			"hasBookings": false,
			"message":     "No future bookings found for this phone number",
			"bookings":    []any{},
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"hasBookings": true,
		"message":     "Future bookings found",
		"count":       len(bookings),
		"bookings":    bookings,
	})
}
