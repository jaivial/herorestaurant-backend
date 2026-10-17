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
		SELECT d.id AS NUM,
			  CONCAT(TRIM(d.title_snapshot),
			    IF(d.supplement_price > 0, CONCAT(' (+', FORMAT(d.supplement_price, 0), '€)')'), '')) AS rice_name
		FROM group_menu_section_dishes_v2 d
		JOIN group_menu_sections_v2 s ON s.id = d.section_id
		WHERE d.restaurant_id = ?
		  AND d.active = 1
		  AND s.section_kind = 'arroces'
		  AND d.menu_id = 42
		ORDER BY d.title_snapshot
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
	var ids []int64
	for rows.Next() {
		var id int64
		var name sql.NullString
		if err := rows.Scan(&id, &name); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success":   false,
				"riceTypes": []string{},
				"message":   err.Error(),
			})
			return
		}
		if name.Valid {
			ids = append(ids, id)
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

	riceTypesEnglish := make([]string, len(riceTypes))
	if all, err := s.loadTranslations(r.Context(), restaurantID, entitySectionDishes, ids, translationLang); err == nil {
		for i, id := range ids {
			riceTypesEnglish[i] = translationOr(all[id], "title")
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":          true,
		"riceTypes":        riceTypes,
		"riceTypesEnglish": riceTypesEnglish,
		"count":            len(riceTypes),
		"message":          "Tipos de arroz disponibles obtenidos correctamente",
	})
}
