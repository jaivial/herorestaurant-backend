package api

import (
	"database/sql"
	"net/http"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleGetAvailableRiceTypes(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success":   false,
			"riceTypes": []string{},
			"message":   "Unknown restaurant",
		})
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT DESCRIPCION as rice_name
		FROM FINDE
		WHERE restaurant_id = ?
		  AND TIPO = 'ARROZ'
		  AND active = 1
		ORDER BY DESCRIPCION
	`, restaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":   false,
			"riceTypes": []string{},
			"message":   err.Error(),
		})
		return
	}
	defer rows.Close()

	var riceTypes []string
	for rows.Next() {
		var name sql.NullString
		if err := rows.Scan(&name); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success":   false,
				"riceTypes": []string{},
				"message":   err.Error(),
			})
			return
		}
		if name.Valid {
			riceTypes = append(riceTypes, name.String)
		}
	}

	if len(riceTypes) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":   true,
			"riceTypes": []string{},
			"count":     0,
			"message":   "No hay tipos de arroz activos en este momento",
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"riceTypes": riceTypes,
		"count":     len(riceTypes),
		"message":   "Tipos de arroz disponibles obtenidos correctamente",
	})
}
