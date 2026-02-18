package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleUpdateDishDia(w http.ResponseWriter, r *http.Request) {
	s.handleUpdateDishForTable(w, r, "DIA")
}

func (s *Server) handleUpdateDishFinde(w http.ResponseWriter, r *http.Request) {
	s.handleUpdateDishForTable(w, r, "FINDE")
}

func (s *Server) handleUpdateDishForTable(w http.ResponseWriter, r *http.Request, table string) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"status":  "error",
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	if err := parseLegacyForm(r, 1<<20); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"status":  "error",
			"success": false,
			"message": "Invalid form data",
		})
		return
	}
	data := r.Form

	// Delete dish: eliminaplato + formID
	if hasFormKey(data, "eliminaplato") && strings.TrimSpace(data.Get("formID")) != "" {
		formID, err := parsePositiveInt(data.Get("formID"))
		if err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"status":  "error",
				"success": false,
				"message": "Invalid formID",
			})
			return
		}

		res, err := s.db.ExecContext(r.Context(), "DELETE FROM "+table+" WHERE restaurant_id = ? AND NUM = ?", restaurantID, formID)
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"status":  "error",
				"success": false,
				"message": "Error al eliminar el plato",
			})
			return
		}
		affected, _ := res.RowsAffected()
		if affected <= 0 {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"status":  "error",
				"success": false,
				"message": "No dish found with that ID",
			})
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "success",
			"success": true,
			"message": "Plato eliminado correctamente",
		})
		return
	}

	// Update dish: update + inputText + formID + selectedAlergenos[]
	if hasFormKey(data, "update") {
		texto := strings.TrimSpace(data.Get("inputText"))
		if texto == "" {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"status":  "error",
				"success": false,
				"message": "El texto no puede estar vacío",
			})
			return
		}
		formID, err := parsePositiveInt(data.Get("formID"))
		if err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"status":  "error",
				"success": false,
				"message": "Invalid formID",
			})
			return
		}

		selected := formArray(data, "selectedAlergenos")
		jsonAlergenos, _ := json.Marshal(selected)

		_, err = s.db.ExecContext(r.Context(), "UPDATE "+table+" SET DESCRIPCION = ?, alergenos = ? WHERE restaurant_id = ? AND NUM = ?", texto, string(jsonAlergenos), restaurantID, formID)
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"status":  "error",
				"success": false,
				"message": "Error al actualizar el plato",
			})
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "success",
			"success": true,
			"message": "Plato actualizado correctamente",
		})
		return
	}

	// Toggle active status (legacy support inside updateDishDia.php, used by some older clients).
	if hasFormKey(data, "toggleActive") && strings.TrimSpace(data.Get("dishId")) != "" {
		dishID, err := parsePositiveInt(data.Get("dishId"))
		if err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"status":  "error",
				"success": false,
				"message": "Invalid dishId",
			})
			return
		}
		newStatus := parseBoolParam(data.Get("newStatus"), false)
		activeInt := 0
		if newStatus {
			activeInt = 1
		}

		_, err = s.db.ExecContext(r.Context(), "UPDATE "+table+" SET active = ? WHERE restaurant_id = ? AND NUM = ?", activeInt, restaurantID, dishID)
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"status":  "error",
				"success": false,
				"message": "Error al cambiar el estado",
			})
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "success",
			"success": true,
			"message": "Estado actualizado correctamente",
		})
		return
	}

	// Add dish: anyadeEntrante|anyadePrincipal|anyadeArroz + inputText
	if hasFormKey(data, "anyadeEntrante") || hasFormKey(data, "anyadePrincipal") || hasFormKey(data, "anyadeArroz") {
		texto := strings.TrimSpace(data.Get("inputText"))
		if texto == "" {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"status":  "error",
				"success": false,
				"message": "El texto no puede estar vacío",
			})
			return
		}

		tipo := ""
		var selected []string
		switch {
		case hasFormKey(data, "anyadeEntrante"):
			tipo = "ENTRANTE"
			selected = formArray(data, "selectedAlergenos")
		case hasFormKey(data, "anyadePrincipal"):
			tipo = "PRINCIPAL"
			selected = formArray(data, "selectedAlergenos2")
		case hasFormKey(data, "anyadeArroz"):
			tipo = "ARROZ"
			selected = formArray(data, "selectedAlergenos3")
		}

		jsonAlergenos, _ := json.Marshal(selected)

		res, err := s.db.ExecContext(r.Context(), "INSERT INTO "+table+" (restaurant_id, DESCRIPCION, TIPO, alergenos) VALUES (?, ?, ?, ?)", restaurantID, texto, tipo, string(jsonAlergenos))
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"status":  "error",
				"success": false,
				"message": "Error al añadir el plato",
			})
			return
		}
		newID, _ := res.LastInsertId()

		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "success",
			"success": true,
			"message": "Plato añadido correctamente",
			"newId":   newID,
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"status":  "error",
		"success": false,
		"message": "No action specified",
	})
}

func (s *Server) handleToggleDishStatusDia(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"status":  "error",
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	if err := parseLegacyForm(r, 1<<20); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"status":  "error",
			"success": false,
			"message": "Invalid form data",
		})
		return
	}
	dishID, err := parsePositiveInt(r.Form.Get("dishId"))
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"status":  "error",
			"success": false,
			"message": "Parámetros incompletos (dishId y isActive son requeridos)",
		})
		return
	}

	isActive := parseBoolParam(r.Form.Get("isActive"), false)
	activeInt := 0
	if isActive {
		activeInt = 1
	}

	_, err = s.db.ExecContext(r.Context(), "UPDATE DIA SET active = ? WHERE restaurant_id = ? AND NUM = ?", activeInt, restaurantID, dishID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"status":  "error",
			"success": false,
			"message": "Error al actualizar el estado del plato",
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"status":    "success",
		"success":   true,
		"message":   "Estado del plato actualizado correctamente",
		"dishId":    dishID,
		"newStatus": activeInt,
	})
}

func (s *Server) handleToggleDishStatusGeneric(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"status":  "error",
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	if err := parseLegacyForm(r, 1<<20); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"status":  "error",
			"success": false,
			"message": "Invalid form data",
		})
		return
	}

	dishID, err := parsePositiveInt(r.Form.Get("dishId"))
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "error",
			"success": false,
			"message": "Invalid dish ID provided.",
		})
		return
	}

	isActive := parseBoolParam(r.Form.Get("isActive"), false)
	table := strings.TrimSpace(r.Form.Get("table"))
	if table == "" {
		table = "FINDE"
	}

	allowed := map[string]bool{"FINDE": true, "POSTRES": true}
	if !allowed[table] {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "error",
			"success": false,
			"message": "Invalid table name provided.",
		})
		return
	}

	activeInt := 0
	if isActive {
		activeInt = 1
	}

	res, err := s.db.ExecContext(r.Context(), "UPDATE "+table+" SET active = ? WHERE restaurant_id = ? AND NUM = ?", activeInt, restaurantID, dishID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "error",
			"success": false,
			"message": "Error updating dish status",
		})
		return
	}
	affected, _ := res.RowsAffected()
	if affected <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "error",
			"success": false,
			"message": "No changes were made. Dish not found or status already set.",
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"success": true,
		"message": "Dish status updated successfully.",
	})
}

func (s *Server) handleSearchDishesDia(w http.ResponseWriter, r *http.Request) {
	s.handleSearchDishesByTable(w, r, "DIA")
}

func (s *Server) handleSearchDishesFinde(w http.ResponseWriter, r *http.Request) {
	s.handleSearchDishesByTable(w, r, "FINDE")
}

func (s *Server) handleSearchDishesByTable(w http.ResponseWriter, r *http.Request, table string) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"status":      "error",
			"success":     false,
			"message":     "Unknown restaurant",
			"matchingIds": []int{},
		})
		return
	}

	searchTerm := strings.TrimSpace(r.URL.Query().Get("searchTerm"))
	if searchTerm == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":      "error",
			"success":     false,
			"message":     "Search term not provided.",
			"matchingIds": []int{},
		})
		return
	}

	likeTerm := "%" + searchTerm + "%"
	rows, err := s.db.QueryContext(r.Context(), "SELECT NUM FROM "+table+" WHERE restaurant_id = ? AND TIPO IN ('ENTRANTE', 'PRINCIPAL', 'ARROZ') AND DESCRIPCION LIKE ?", restaurantID, likeTerm)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"status":      "error",
			"success":     false,
			"message":     "Database error.",
			"matchingIds": []int{},
		})
		return
	}
	defer rows.Close()

	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"status":      "error",
				"success":     false,
				"message":     "Database error.",
				"matchingIds": []int{},
			})
			return
		}
		ids = append(ids, id)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"status":      "success",
		"success":     true,
		"message":     "Search completed.",
		"matchingIds": ids,
	})
}

func parseLegacyForm(r *http.Request, maxMemory int64) error {
	if r.Method == http.MethodGet {
		return nil
	}

	// Most legacy calls use multipart/form-data (FormData in the browser), but some use x-www-form-urlencoded.
	if err := r.ParseMultipartForm(maxMemory); err == nil {
		return nil
	}
	// Try normal form parsing.
	return r.ParseForm()
}

func hasFormKey(v url.Values, key string) bool {
	_, ok := v[key]
	return ok
}

func formArray(v url.Values, base string) []string {
	if xs, ok := v[base]; ok {
		return xs
	}
	if xs, ok := v[base+"[]"]; ok {
		return xs
	}
	return []string{}
}

func parsePositiveInt(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty")
	}
	i, err := strconv.Atoi(s)
	if err != nil || i <= 0 {
		return 0, errors.New("invalid")
	}
	return i, nil
}

func parseAlergenosJSON(raw sql.NullString) []string {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(raw.String), &out); err == nil {
		return out
	}
	return []string{}
}
