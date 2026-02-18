package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleGetOpeningHours(w http.ResponseWriter, r *http.Request) {
	if _, ok := restaurantIDFromContext(r.Context()); !ok {
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
			"message": "Error al cargar las horas disponibles",
			"debug":   "Date parameter is required (YYYY-MM-DD)",
		})
		return
	}

	hours, err := s.getOpeningHoursForDate(r, date)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error al cargar las horas disponibles",
			"debug":   err.Error(),
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"hours":   hours,
	})
}

func (s *Server) handleEditOpeningHours(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	var input struct {
		Date  string   `json:"date"`
		Hours []string `json:"hours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid JSON: " + err.Error(),
		})
		return
	}

	date := strings.TrimSpace(input.Date)
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date format. Use YYYY-MM-DD",
		})
		return
	}
	if input.Hours == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Missing required fields: date and hours",
		})
		return
	}

	validHours := map[string]bool{
		"13:00": true, "13:30": true, "14:00": true, "14:30": true, "15:00": true, "15:30": true,
		"19:00": true, "19:30": true, "20:00": true, "20:30": true, "21:00": true, "21:30": true, "22:00": true, "22:30": true, "23:00": true,
	}

	for _, h := range input.Hours {
		h = strings.TrimSpace(h)
		if !validHours[h] {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Invalid hour format: " + h,
			})
			return
		}
	}

	hoursJSON, _ := json.Marshal(input.Hours)

	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO openinghours (restaurant_id, dateselected, hoursarray) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE hoursarray = VALUES(hoursarray)
	`, restaurantID, date, string(hoursJSON))
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Database error: " + err.Error(),
		})
		return
	}

	// Keep hour_configuration in sync (legacy behavior).
	_, _ = s.db.ExecContext(r.Context(), "DELETE FROM hour_configuration WHERE restaurant_id = ? AND date = ?", restaurantID, date)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Opening hours updated successfully",
	})
}
