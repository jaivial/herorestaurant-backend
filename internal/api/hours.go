package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strings"

	"preactvillacarmen/internal/httpx"
)

type HourSlot struct {
	Status        string  `json:"status"`
	Capacity      int     `json:"capacity"`
	TotalCapacity int     `json:"totalCapacity,omitempty"`
	Bookings      int     `json:"bookings"`
	Percentage    float64 `json:"percentage"`
	Completion    float64 `json:"completion"`
	IsClosed      bool    `json:"isClosed"`
}

func (s *Server) handleGetHourData(w http.ResponseWriter, r *http.Request) {
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
			"message": "Error al obtener la configuración de horas",
			"debug":   "Date parameter is required (YYYY-MM-DD)",
		})
		return
	}

	dailyLimit := 45
	_ = s.db.QueryRowContext(r.Context(), "SELECT dailyLimit FROM reservation_manager WHERE restaurant_id = ? AND reservationDate = ? LIMIT 1", restaurantID, date).Scan(&dailyLimit)
	if dailyLimit <= 0 {
		dailyLimit = 45
	}

	bookingsByHour, totalPeople, err := s.fetchBookingsByHourHHMM(r, date)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error al obtener la configuración de horas",
			"debug":   err.Error(),
		})
		return
	}

	var hourDataRaw sql.NullString
	err = s.db.QueryRowContext(r.Context(), "SELECT hourData FROM hour_configuration WHERE restaurant_id = ? AND date = ? LIMIT 1", restaurantID, date).Scan(&hourDataRaw)
	if err != nil && err != sql.ErrNoRows {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error al obtener la configuración de horas",
			"debug":   err.Error(),
		})
		return
	}

	if err == sql.ErrNoRows || !hourDataRaw.Valid || strings.TrimSpace(hourDataRaw.String) == "" {
		activeHours, err := s.getOpeningHoursForDate(r, date)
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"message": "Error al obtener la configuración de horas",
				"debug":   err.Error(),
			})
			return
		}

		equalPercentage := 100.0 / float64(len(activeHours))
		hourData := map[string]HourSlot{}
		for _, hour := range activeHours {
			bookings := bookingsByHour[hour]
			totalCapacity := int(math.Ceil((equalPercentage / 100.0) * float64(dailyLimit)))
			availableCapacity := totalCapacity - bookings
			completion := 0.0
			if totalCapacity > 0 {
				completion = (float64(bookings) / float64(totalCapacity)) * 100.0
			}
			status := "available"
			if completion > 90 {
				status = "full"
			} else if completion > 70 {
				status = "limited"
			}

			hourData[hour] = HourSlot{
				Status:        status,
				Capacity:      availableCapacity,
				TotalCapacity: totalCapacity,
				Bookings:      bookings,
				Percentage:    equalPercentage,
				Completion:    completion,
				IsClosed:      false,
			}
		}

		sort.Strings(activeHours)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":       true,
			"hourData":      hourData,
			"activeHours":   activeHours,
			"isDefaultData": true,
			"dailyLimit":    dailyLimit,
			"totalPeople":   totalPeople,
			"date":          date,
		})
		return
	}

	// Existing configuration path.
	var hourData map[string]HourSlot
	if err := json.Unmarshal([]byte(hourDataRaw.String), &hourData); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error al obtener la configuración de horas",
			"debug":   "Invalid hourData JSON in database",
		})
		return
	}

	for hour, data := range hourData {
		bookings := bookingsByHour[hour]
		data.Bookings = bookings

		totalCapacity := int(math.Ceil((data.Percentage / 100.0) * float64(dailyLimit)))
		data.TotalCapacity = totalCapacity
		data.Capacity = totalCapacity - bookings
		if totalCapacity > 0 {
			data.Completion = (float64(bookings) / float64(totalCapacity)) * 100.0
		} else {
			data.Completion = 0
		}

		if !data.IsClosed && data.Status != "closed" {
			if data.Completion > 90 {
				data.Status = "full"
			} else if data.Completion > 70 {
				data.Status = "limited"
			} else {
				data.Status = "available"
			}
		}

		hourData[hour] = data
	}

	activeHours := make([]string, 0, len(hourData))
	for h := range hourData {
		activeHours = append(activeHours, h)
	}
	sort.Strings(activeHours)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":       true,
		"hourData":      hourData,
		"activeHours":   activeHours,
		"isDefaultData": false,
		"dailyLimit":    dailyLimit,
		"totalPeople":   totalPeople,
		"date":          date,
	})
}

func (s *Server) handleSaveHourData(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	var input struct {
		Date     string              `json:"date"`
		HourData map[string]HourSlot `json:"hourData"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error al guardar la configuración de horas",
			"debug":   "Invalid JSON: " + err.Error(),
		})
		return
	}

	date := strings.TrimSpace(input.Date)
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error al guardar la configuración de horas",
			"debug":   "Invalid date format. Use YYYY-MM-DD",
		})
		return
	}
	if input.HourData == nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error al guardar la configuración de horas",
			"debug":   "hourData parameter is required and must be an array",
		})
		return
	}

	dailyLimit := 45
	_ = s.db.QueryRowContext(r.Context(), "SELECT dailyLimit FROM reservation_manager WHERE restaurant_id = ? AND reservationDate = ? LIMIT 1", restaurantID, date).Scan(&dailyLimit)
	if dailyLimit <= 0 {
		dailyLimit = 45
	}

	bookingsByHour, _, err := s.fetchBookingsByHourHHMM(r, date)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error al guardar la configuración de horas",
			"debug":   err.Error(),
		})
		return
	}

	for hour, data := range input.HourData {
		bookings := bookingsByHour[hour]
		data.Bookings = bookings

		totalCapacity := int(math.Ceil((data.Percentage / 100.0) * float64(dailyLimit)))
		data.Capacity = totalCapacity
		if totalCapacity > 0 {
			data.Completion = (float64(bookings) / float64(totalCapacity)) * 100.0
		} else {
			data.Completion = 0
		}

		if data.IsClosed || data.Status == "closed" {
			data.Status = "closed"
		} else {
			if data.Completion > 90 {
				data.Status = "full"
			} else if data.Completion > 70 {
				data.Status = "limited"
			} else {
				data.Status = "available"
			}
		}

		// Don't store totalCapacity here (legacy savehourdata.php doesn't).
		data.TotalCapacity = 0
		input.HourData[hour] = data
	}

	payload, _ := json.Marshal(input.HourData)

	var exists int
	err = s.db.QueryRowContext(r.Context(), "SELECT id FROM hour_configuration WHERE restaurant_id = ? AND date = ? LIMIT 1", restaurantID, date).Scan(&exists)
	if err != nil && err != sql.ErrNoRows {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error al guardar la configuración de horas",
			"debug":   err.Error(),
		})
		return
	}

	if err == nil {
		_, err = s.db.ExecContext(r.Context(), "UPDATE hour_configuration SET hourData = ?, updated_at = NOW() WHERE restaurant_id = ? AND date = ?", string(payload), restaurantID, date)
	} else {
		_, err = s.db.ExecContext(r.Context(), "INSERT INTO hour_configuration (restaurant_id, date, hourData, created_at, updated_at) VALUES (?, ?, ?, NOW(), NOW())", restaurantID, date, string(payload))
	}
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error al guardar la configuración de horas",
			"debug":   err.Error(),
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"message":  "Hour configuration saved successfully",
		"date":     date,
		"hourData": input.HourData,
	})
}

func (s *Server) getOpeningHoursForDate(r *http.Request, date string) ([]string, error) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		return nil, errors.New("unknown restaurant")
	}

	var hoursRaw sql.NullString
	err := s.db.QueryRowContext(r.Context(), "SELECT hoursarray FROM openinghours WHERE restaurant_id = ? AND dateselected = ? LIMIT 1", restaurantID, date).Scan(&hoursRaw)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err == nil && hoursRaw.Valid && strings.TrimSpace(hoursRaw.String) != "" {
		var hours []string
		if err := json.Unmarshal([]byte(hoursRaw.String), &hours); err == nil && len(hours) > 0 {
			return hours, nil
		}
		// Invalid hoursarray JSON: fall back to defaults.
	}

	return []string{"13:30", "14:00", "14:30", "15:00"}, nil
}

func (s *Server) fetchBookingsByHourHHMM(r *http.Request, date string) (map[string]int, int, error) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		return nil, 0, errors.New("unknown restaurant")
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT reservation_time, COALESCE(SUM(party_size), 0) AS total_people
		FROM bookings
		WHERE restaurant_id = ? AND reservation_date = ?
		GROUP BY reservation_time
	`, restaurantID, date)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := map[string]int{}
	totalPeople := 0
	for rows.Next() {
		var reservationTime string
		var total int
		if err := rows.Scan(&reservationTime, &total); err != nil {
			return nil, 0, err
		}
		hour := reservationTime
		if len(hour) >= 5 {
			hour = hour[:5]
		}
		out[hour] = total
		totalPeople += total
	}
	return out, totalPeople, rows.Err()
}

// Ensure stable JSON key ordering for ETag hashing, etc.
func sortedHourKeys(m map[string]HourSlot) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
