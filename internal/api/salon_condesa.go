package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleSalonCondesaGet(w http.ResponseWriter, r *http.Request) {
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

	var state sql.NullInt64
	err := s.db.QueryRowContext(r.Context(), "SELECT state FROM salon_condesa WHERE restaurant_id = ? AND date = ? LIMIT 1", restaurantID, date).Scan(&state)
	if err != nil && err != sql.ErrNoRows {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Error de base de datos: " + err.Error(),
		})
		return
	}

	outState := 0
	if err == nil && state.Valid {
		outState = int(state.Int64)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"state":   outState,
	})
}

func (s *Server) handleSalonCondesaSet(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	if err := r.ParseForm(); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Operación no válida",
		})
		return
	}

	date := strings.TrimSpace(r.FormValue("date"))
	stateStr := strings.TrimSpace(r.FormValue("state"))
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Formato de fecha inválido",
		})
		return
	}
	state, err := strconv.Atoi(stateStr)
	if err != nil || (state != 0 && state != 1) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Valor de estado inválido",
		})
		return
	}

	// Upsert
	_, err = s.db.ExecContext(r.Context(), `
		INSERT INTO salon_condesa (restaurant_id, date, state)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE state = VALUES(state)
	`, restaurantID, date, state)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Error de base de datos: " + err.Error(),
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}
