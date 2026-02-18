package api

import (
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleGetHourPercentages(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error al cargar los porcentajes de horas",
			"debug":   "Date parameter is required (YYYY-MM-DD)",
		})
		return
	}

	// Step 1: active hours for the selected date.
	var activeHours []string
	{
		var hoursRaw sql.NullString
		err := s.db.QueryRowContext(r.Context(), "SELECT hoursarray FROM openinghours WHERE restaurant_id = ? AND dateselected = ? LIMIT 1", restaurantID, date).Scan(&hoursRaw)
		if err != nil && err != sql.ErrNoRows {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": "Error al cargar los porcentajes de horas",
				"debug":   err.Error(),
			})
			return
		}
		if err == nil && hoursRaw.Valid && strings.TrimSpace(hoursRaw.String) != "" {
			_ = json.Unmarshal([]byte(hoursRaw.String), &activeHours)
		}
		if len(activeHours) == 0 {
			activeHours = []string{"13:30", "14:00", "14:30", "15:00", "15:30"}
		}
	}

	// Ensure stable order (legacy UI expects sorted hours).
	sort.Strings(activeHours)

	// Step 2: hour percentages (custom or equal distribution).
	hourPercentages := map[string]float64{}
	{
		var percRaw sql.NullString
		err := s.db.QueryRowContext(r.Context(), "SELECT hoursPercentages FROM hours_percentage WHERE restaurant_id = ? AND reservationDate = ? LIMIT 1", restaurantID, date).Scan(&percRaw)
		if err != nil && err != sql.ErrNoRows {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": "Error al cargar los porcentajes de horas",
				"debug":   err.Error(),
			})
			return
		}

		if err == nil && percRaw.Valid && strings.TrimSpace(percRaw.String) != "" {
			_ = json.Unmarshal([]byte(percRaw.String), &hourPercentages)
		}

		if len(hourPercentages) == 0 {
			equal := 100.0 / float64(len(activeHours))
			for _, h := range activeHours {
				hourPercentages[h] = equal
			}
		}
	}

	// Step 3: bookings by hour.
	bookingsByHour := map[string]int{}
	for _, h := range activeHours {
		bookingsByHour[h] = 0
	}

	bookingRows, err := s.db.QueryContext(r.Context(), `
		SELECT reservation_time, COALESCE(SUM(party_size), 0) AS total_people
		FROM bookings
		WHERE restaurant_id = ? AND reservation_date = ?
		GROUP BY reservation_time
	`, restaurantID, date)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error al cargar los porcentajes de horas",
			"debug":   err.Error(),
		})
		return
	}
	defer bookingRows.Close()

	for bookingRows.Next() {
		var t time.Time
		var total int
		if err := bookingRows.Scan(&t, &total); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": "Error al cargar los porcentajes de horas",
				"debug":   err.Error(),
			})
			return
		}
		hhmm := t.Format("15:04")
		if _, ok := bookingsByHour[hhmm]; ok {
			bookingsByHour[hhmm] = total
		}
	}

	// Step 4: total daily bookings and capacity (dailyLimit).
	dailyLimit := 45
	_ = s.db.QueryRowContext(r.Context(), "SELECT dailyLimit FROM reservation_manager WHERE restaurant_id = ? AND reservationDate = ? LIMIT 1", restaurantID, date).Scan(&dailyLimit)
	if dailyLimit <= 0 {
		dailyLimit = 45
	}

	totalPeople := 0
	_ = s.db.QueryRowContext(r.Context(), "SELECT COALESCE(SUM(party_size), 0) FROM bookings WHERE restaurant_id = ? AND reservation_date = ?", restaurantID, date).Scan(&totalPeople)

	// Step 5: completion percentages + per-hour capacity.
	hourlyCapacities := map[string]int{}
	completionPercentages := map[string]float64{}
	for _, h := range activeHours {
		hourCapacity := (hourPercentages[h] / 100.0) * float64(dailyLimit)
		hourlyCapacities[h] = int(math.Round(hourCapacity))

		bookings := bookingsByHour[h]
		completion := 0.0
		if hourCapacity > 0 {
			completion = (float64(bookings) / hourCapacity) * 100.0
		}
		rounded := math.Round(completion*10.0) / 10.0
		if rounded > 100 {
			rounded = 100
		}
		completionPercentages[h] = rounded
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":                true,
		"activeHours":            activeHours,
		"hourPercentages":        hourPercentages,
		"bookingsByHour":         bookingsByHour,
		"totalPeople":            totalPeople,
		"dailyLimit":             dailyLimit,
		"hourlyCapacities":       hourlyCapacities,
		"completionPercentages":  completionPercentages,
	})
}

func (s *Server) handleUpdateHourPercentages(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	var input struct {
		Date        string             `json:"date"`
		Percentages map[string]float64 `json:"percentages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error al actualizar los porcentajes de horas",
			"debug":   "Invalid JSON: " + err.Error(),
		})
		return
	}

	date := strings.TrimSpace(input.Date)
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error al actualizar los porcentajes de horas",
			"debug":   "Invalid date format. Use YYYY-MM-DD",
		})
		return
	}
	if input.Percentages == nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error al actualizar los porcentajes de horas",
			"debug":   "Date and percentages are required",
		})
		return
	}

	total := 0.0
	for _, v := range input.Percentages {
		total += v
	}
	if math.Abs(total-100.0) > 0.1 {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error al actualizar los porcentajes de horas",
			"debug":   "Percentages must sum to 100, got " + strconv.FormatFloat(total, 'f', -1, 64),
		})
		return
	}

	percentagesJSON, _ := json.Marshal(input.Percentages)

	var exists int
	err := s.db.QueryRowContext(r.Context(), "SELECT 1 FROM hours_percentage WHERE restaurant_id = ? AND reservationDate = ? LIMIT 1", restaurantID, date).Scan(&exists)
	if err != nil && err != sql.ErrNoRows {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error al actualizar los porcentajes de horas",
			"debug":   err.Error(),
		})
		return
	}

	action := "inserted"
	var res sql.Result
	if err == nil {
		action = "updated"
		res, err = s.db.ExecContext(r.Context(), "UPDATE hours_percentage SET hoursPercentages = ? WHERE restaurant_id = ? AND reservationDate = ?", string(percentagesJSON), restaurantID, date)
	} else {
		res, err = s.db.ExecContext(r.Context(), "INSERT INTO hours_percentage (restaurant_id, reservationDate, hoursPercentages) VALUES (?, ?, ?)", restaurantID, date, string(percentagesJSON))
	}
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error al actualizar los porcentajes de horas",
			"debug":   err.Error(),
		})
		return
	}

	affected, _ := res.RowsAffected()
	if affected > 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"message": "Hour percentages updated successfully",
			"action":  action,
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "No changes were made",
		"action":  "none",
	})
}
