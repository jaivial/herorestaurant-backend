package api

import (
	"database/sql"
	"net/http"
	"strings"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleGetReservationDayContext(w http.ResponseWriter, r *http.Request) {
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
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Formato de fecha inválido",
		})
		return
	}

	defaults, err := s.loadReservationDefaults(r.Context(), restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando defaults")
		return
	}

	openingMode := defaults.OpeningMode
	morningHours := cloneStrings(defaults.MorningHours)
	nightHours := cloneStrings(defaults.NightHours)

	var hoursRaw sql.NullString
	err = s.db.QueryRowContext(r.Context(), `
		SELECT hoursarray
		FROM openinghours
		WHERE restaurant_id = ? AND dateselected = ?
		LIMIT 1
	`, restaurantID, date).Scan(&hoursRaw)
	if err != nil && err != sql.ErrNoRows {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando openinghours")
		return
	}

	if list, ok := parseHoursJSON(hoursRaw); ok {
		morningHours, nightHours = splitHoursByShift(list)
		openingMode = modeFromHours(morningHours, nightHours)
	}

	floors, err := s.loadDateFloors(r.Context(), restaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando plantas")
		return
	}
	activeFloors := make([]boConfigFloor, 0, len(floors))
	for _, floor := range floors {
		if floor.Active {
			activeFloors = append(activeFloors, floor)
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"date":         date,
		"openingMode":  openingMode,
		"morningHours": morningHours,
		"nightHours":   nightHours,
		"floors":       floors,
		"activeFloors": activeFloors,
	})
}
