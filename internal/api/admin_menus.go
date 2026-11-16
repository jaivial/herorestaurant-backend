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
	"preactvillacarmen/internal/sqldb"
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

		// table is the fixed "DIA"/"FINDE" literal of the two route wrappers. SOURCE
		// ANOMALY: neither table exists in the exported/live schema, so these legacy
		// statements fail on MySQL and PG alike; PG quotes the uppercase identifiers as
		// written (no substitute table chosen).
		res, err := s.db.ExecContext(r.Context(), portableSQL("DELETE FROM "+sqlFragment(table, `"`+table+`"`)+sqlFragment(" WHERE restaurant_id = ? AND NUM = ?", ` WHERE restaurant_id = ? AND "NUM" = ?`)), restaurantID, formID)
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

		_, err = s.db.ExecContext(r.Context(), portableSQL("UPDATE "+sqlFragment(table, `"`+table+`"`)+sqlFragment(" SET DESCRIPCION = ?, alergenos = ? WHERE restaurant_id = ? AND NUM = ?", ` SET "DESCRIPCION" = ?, alergenos = ? WHERE restaurant_id = ? AND "NUM" = ?`)), texto, string(jsonAlergenos), restaurantID, formID)
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

		_, err = s.db.ExecContext(r.Context(), portableSQL("UPDATE "+sqlFragment(table, `"`+table+`"`)+sqlFragment(" SET active = ? WHERE restaurant_id = ? AND NUM = ?", ` SET active = ? WHERE restaurant_id = ? AND "NUM" = ?`)), activeInt, restaurantID, dishID)
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

		newID, err := sqldb.InsertID(r.Context(), sqlDialect, s.db, portableSQL("INSERT INTO "+sqlFragment(table, `"`+table+`"`)+sqlFragment(" (restaurant_id, DESCRIPCION, TIPO, alergenos) VALUES (?, ?, ?, ?)", ` (restaurant_id, "DESCRIPCION", "TIPO", alergenos) VALUES (?, ?, ?, ?)`)), `"NUM"`, restaurantID, texto, tipo, string(jsonAlergenos))
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"status":  "error",
				"success": false,
				"message": "Error al añadir el plato",
			})
			return
		}

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

// handleToggleDishStatusDia toggles the active flag of a dish.
//
// The legacy tables (DIA, FINDE, POSTERS) are gone: every write goes to the v2
// table (group_menu_section_dishes_v2), and the legacy id is resolved through
// legacy_dish_map, which keeps the (legacy_table, legacy_num) -> v2_dish_id
// correspondence. Same pattern as scanDishName in special_booking_snapshot.go.
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

	// The dish id may already be a v2 id (the backoffice works with v2) or a
	// legacy num that legacy_dish_map knows. Try both, in that order.
	res, err := s.db.ExecContext(r.Context(),
		nativeSQL("UPDATE group_menu_section_dishes_v2 SET active = ? WHERE restaurant_id = ? AND id = ?", `UPDATE group_menu_section_dishes_v2 SET active = $1 WHERE restaurant_id = $2 AND id = $3 AND active IS DISTINCT FROM CAST($1 AS smallint)`),
		activeInt, restaurantID, int64(dishID))
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"status":  "error",
			"success": false,
			"message": "Error al actualizar el estado del plato",
		})
		return
	}
	affected, _ := res.RowsAffected()
	if affected <= 0 {
		res, err = s.db.ExecContext(r.Context(), nativeSQL(`
			UPDATE group_menu_section_dishes_v2 d
			   JOIN legacy_dish_map m ON m.v2_dish_id = d.id
			   SET d.active = ?
			 WHERE d.restaurant_id = ? AND m.restaurant_id = ?
			   AND m.legacy_table = 'DIA' AND m.legacy_num = ?`, `
			UPDATE group_menu_section_dishes_v2 d
			   SET active = $1
			  FROM legacy_dish_map m
			 WHERE m.v2_dish_id = d.id
			   AND d.restaurant_id = $2 AND m.restaurant_id = $3
			   AND m.legacy_table = 'DIA' AND m.legacy_num = $4
			   AND d.active IS DISTINCT FROM CAST($1 AS smallint)`),
			activeInt, restaurantID, restaurantID, dishID)
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"status":  "error",
				"success": false,
				"message": "Error al actualizar el estado del plato",
			})
			return
		}
		affected, _ = res.RowsAffected()
	}
	if affected <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "error",
			"success": false,
			"message": "No changes were made. Dish not found or status already set.",
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

// handleToggleDishStatusGeneric toggles the active flag of a dish, addressed by
// its legacy table+num.
//
// The legacy tables (FINDE, POSTERS) are gone: the write goes to
// group_menu_section_dishes_v2 and the legacy num is resolved through
// legacy_dish_map. The table name is kept only to pick the right legacy_table
// in that map, never to build a table name for the query.
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

	allowed := map[string]bool{"DIA": true, "FINDE": true, "POSTRES": true}
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

	res, err := s.db.ExecContext(r.Context(), nativeSQL(`
		UPDATE group_menu_section_dishes_v2 d
		   JOIN legacy_dish_map m ON m.v2_dish_id = d.id
		   SET d.active = ?
		 WHERE d.restaurant_id = ? AND m.restaurant_id = ?
		   AND m.legacy_table = ? AND m.legacy_num = ?`, `
		UPDATE group_menu_section_dishes_v2 d
		   SET active = $1
		  FROM legacy_dish_map m
		 WHERE m.v2_dish_id = d.id
		   AND d.restaurant_id = $2 AND m.restaurant_id = $3
		   AND m.legacy_table = $4 AND m.legacy_num = $5
		   AND d.active IS DISTINCT FROM CAST($1 AS smallint)`),
		activeInt, restaurantID, restaurantID, table, dishID)
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
	// SOURCE ANOMALY (DIA/FINDE absent) as above; PG LIKE uses LOWER + C collation (CI/accent gap).
	if sqlDialect == sqldb.Postgres {
		likeTerm = strings.ToLower(likeTerm)
	}
	rows, err := s.db.QueryContext(r.Context(), portableSQL(sqlFragment("SELECT NUM FROM ", `SELECT "NUM" FROM `)+sqlFragment(table, `"`+table+`"`)+sqlFragment(" WHERE restaurant_id = ? AND TIPO IN ('ENTRANTE', 'PRINCIPAL', 'ARROZ') AND DESCRIPCION LIKE ?", ` WHERE restaurant_id = ? AND "TIPO" IN ('ENTRANTE', 'PRINCIPAL', 'ARROZ') AND LOWER("DESCRIPCION") COLLATE "C" LIKE ?`)), restaurantID, likeTerm)
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
