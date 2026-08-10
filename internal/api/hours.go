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
	_ = s.db.QueryRowContext(r.Context(), "SELECT dailyLimit FROM reservation_manager WHERE restaurant_id = ? AND reservationDate = ? ORDER BY id DESC LIMIT 1", restaurantID, date).Scan(&dailyLimit)
	if dailyLimit <= 0 {
		dailyLimit = 45
	}

	salonState := 0
	var salonStateRaw sql.NullInt64
	if err := s.db.QueryRowContext(r.Context(), "SELECT state FROM salon_condesa WHERE restaurant_id = ? AND date = ? LIMIT 1", restaurantID, date).Scan(&salonStateRaw); err == nil && salonStateRaw.Valid {
		salonState = int(salonStateRaw.Int64)
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

	activeHours, err := s.getOpeningHoursForDate(r, date)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error al obtener la configuración de horas",
			"debug":   err.Error(),
		})
		return
	}

	// Canonical percentage source: hours_percentage (decision #3). hour_configuration is
	// only consulted for per-slot closed/status flags.
	percentages, _, err := s.loadHourPercentages(r.Context(), restaurantID, date, activeHours)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error al obtener la configuración de horas",
			"debug":   err.Error(),
		})
		return
	}

	// Legacy per-slot closed flags (best-effort).
	legacyClosed := map[string]bool{}
	if hourDataRaw.Valid && strings.TrimSpace(hourDataRaw.String) != "" {
		var legacy map[string]HourSlot
		if uerr := json.Unmarshal([]byte(hourDataRaw.String), &legacy); uerr == nil {
			for h, slot := range legacy {
				if slot.IsClosed || slot.Status == "closed" {
					legacyClosed[h] = true
				}
			}
		}
	}

	hourSplitEnabled, _, err := s.resolveHourSplitEnabled(r.Context(), restaurantID, date)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error al obtener la configuración de horas",
			"debug":   err.Error(),
		})
		return
	}

	hourlyCapacities := percentagesToPeople(percentages, dailyLimit)
	isDefaultData := len(hourDataRaw.String) == 0 || strings.TrimSpace(hourDataRaw.String) == ""
	hourData := map[string]HourSlot{}
	for _, hour := range activeHours {
		bookings := bookingsByHour[hour]
		isClosed := legacyClosed[hour]
		pct := percentages[hour]

		var statusVal string
		var completionVal float64
		var capacity int
		var totalCapacity int
		if isClosed {
			statusVal = "closed"
			completionVal = 0
			capacity = 0
			totalCapacity = 0
		} else {
			if !hourSplitEnabled {
				// No per-hour cap: the whole daily limit is available across every open hour.
				available := dailyLimit - totalPeople
				if available < 0 {
					available = 0
				}
				capacity = available
				totalCapacity = 0 // omitted (decision #4)
				if dailyLimit > 0 {
					completionVal = (float64(totalPeople) / float64(dailyLimit)) * 100.0
				}
			} else {
				totalCapacity = hourlyCapacities[hour]
				if totalCapacity == 0 && pct > 0 {
					totalCapacity = int(math.Ceil((pct / 100.0) * float64(dailyLimit)))
				}
				capacity = totalCapacity - bookings
				if capacity < 0 {
					capacity = 0
				}
				if totalCapacity > 0 {
					completionVal = (float64(bookings) / float64(totalCapacity)) * 100.0
				}
			}
			statusVal = statusForCompletion(completionVal)
		}

		slot := HourSlot{
			Status:     statusVal,
			Capacity:   capacity,
			Bookings:   bookings,
			Percentage: pct,
			Completion: completionVal,
			IsClosed:   isClosed,
		}
		if hourSplitEnabled && !isClosed {
			slot.TotalCapacity = totalCapacity
		}
		hourData[hour] = slot
	}

	sortServiceHours(activeHours)

	resp := map[string]any{
		"success":          true,
		"hourData":         hourData,
		"activeHours":      activeHours,
		"isDefaultData":    isDefaultData,
		"dailyLimit":       dailyLimit,
		"totalPeople":      totalPeople,
		"date":             date,
		"salonState":       salonState,
		"hourSplitEnabled": hourSplitEnabled,
		"percentages":      percentages,
	}
	if hourSplitEnabled {
		resp["hourlyCapacities"] = hourlyCapacities
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
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

	defaults, err := s.loadReservationDefaults(r.Context(), restaurantID)
	if err != nil {
		return nil, err
	}
	return mergeHoursByMode(defaults.OpeningMode, defaults.MorningHours, defaults.NightHours), nil
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

// statusForCompletion maps a completion percentage to the available/limited/full
// bucket used by the reservation hour-data status field.
func statusForCompletion(completion float64) string {
	if completion > 90 {
		return "full"
	}
	if completion > 70 {
		return "limited"
	}
	return "available"
}
