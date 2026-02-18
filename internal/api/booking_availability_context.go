package api

import (
	"net/http"
	"time"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleGetBookingAvailabilityContext(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	// Mirrors legacy /api/get_booking_availability_context.php.
	today := time.Now()
	endDate := today.AddDate(0, 0, 35)

	availableDates := []string{}
	unavailableDates := []string{}
	closedDays := []string{}
	dailyAvailability := map[string]any{}

	// STEP 1: closed days (explicit).
	closedRows, err := s.db.QueryContext(r.Context(), "SELECT date FROM restaurant_days WHERE restaurant_id = ? AND is_open = FALSE", restaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error retrieving booking availability",
			"error":   err.Error(),
		})
		return
	}
	for closedRows.Next() {
		var d time.Time
		if err := closedRows.Scan(&d); err != nil {
			_ = closedRows.Close()
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": "Error retrieving booking availability",
				"error":   err.Error(),
			})
			return
		}
		closedDays = append(closedDays, d.Format("2006-01-02"))
	}
	_ = closedRows.Close()

	closedSet := map[string]bool{}
	for _, d := range closedDays {
		closedSet[d] = true
	}

	// STEP 2: openedDays are fetched in PHP but not used; keep behavior (no-op).

	// STEP 3: loop through next 35 days (inclusive, like PHP).
	cur := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.Local)
	end := time.Date(endDate.Year(), endDate.Month(), endDate.Day(), 0, 0, 0, 0, time.Local)
	for !cur.After(end) {
		dateStr := cur.Format("2006-01-02")

		if closedSet[dateStr] {
			unavailableDates = append(unavailableDates, dateStr)
			dailyAvailability[dateStr] = map[string]any{
				"date":          dateStr,
				"available":     false,
				"reason":        "closed",
				"freeSeats":     0,
				"totalCapacity": 0,
			}
			cur = cur.AddDate(0, 0, 1)
			continue
		}

		dailyLimit := 45
		_ = s.db.QueryRowContext(r.Context(), "SELECT dailyLimit FROM reservation_manager WHERE restaurant_id = ? AND reservationDate = ? LIMIT 1", restaurantID, dateStr).Scan(&dailyLimit)
		if dailyLimit <= 0 {
			dailyLimit = 45
		}

		totalPeople := 0
		_ = s.db.QueryRowContext(r.Context(), "SELECT COALESCE(SUM(party_size), 0) FROM bookings WHERE restaurant_id = ? AND reservation_date = ?", restaurantID, dateStr).Scan(&totalPeople)

		freeSeats := dailyLimit - totalPeople
		if freeSeats <= 0 {
			unavailableDates = append(unavailableDates, dateStr)
			dailyAvailability[dateStr] = map[string]any{
				"date":          dateStr,
				"available":     false,
				"reason":        "fully_booked",
				"freeSeats":     0,
				"totalCapacity": dailyLimit,
				"booked":        totalPeople,
			}
		} else {
			availableDates = append(availableDates, dateStr)
			occupancy := 0.0
			if dailyLimit > 0 {
				occupancy = float64(totalPeople) / float64(dailyLimit) * 100.0
			}
			// round to 1 decimal
			occupancy = float64(int(occupancy*10+0.5)) / 10.0
			dailyAvailability[dateStr] = map[string]any{
				"date":                dateStr,
				"available":           true,
				"freeSeats":           freeSeats,
				"totalCapacity":       dailyLimit,
				"booked":              totalPeople,
				"occupancyPercentage": occupancy,
			}
		}

		cur = cur.AddDate(0, 0, 1)
	}

	summary := map[string]any{
		"totalDays":          35,
		"availableDays":      len(availableDates),
		"unavailableDays":    len(unavailableDates),
		"closedDays":         len(closedDays),
		"bookingWindowStart": time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.Local).Format("2006-01-02"),
		"bookingWindowEnd":   end.Format("2006-01-02"),
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":          true,
		"summary":          summary,
		"availableDates":   availableDates,
		"unavailableDates": unavailableDates,
		"closedDays":       closedDays,
		"dailyAvailability": dailyAvailability,
		"message":          "Booking availability context retrieved successfully",
	})
}
