package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleUpdatePostre(w http.ResponseWriter, r *http.Request) {
	action := ""
	input := map[string]any{}

	switch r.Method {
	case http.MethodGet:
		action = strings.TrimSpace(r.URL.Query().Get("action"))
		for k, vs := range r.URL.Query() {
			if len(vs) > 0 {
				input[k] = vs[0]
			}
		}
	case http.MethodPost:
		ct := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
		if ct == "application/json" {
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
					"status":  "error",
					"message": "Invalid JSON",
				})
				return
			}
		} else {
			_ = r.ParseForm()
			for k, vs := range r.Form {
				if len(vs) > 0 {
					input[k] = vs[0]
				}
			}
		}
		if v, ok := input["action"]; ok {
			action, _ = v.(string)
			action = strings.TrimSpace(action)
		}
	default:
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"status":  "error",
			"message": "Invalid request method or missing action",
		})
		return
	}

	if action == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"status":  "error",
			"message": "Invalid request method or missing action",
		})
		return
	}

	switch action {
	case "getPostres":
		s.handleGetPostres(w, r)
		return
	case "addPostre":
		s.handleAddPostre(w, r, input)
		return
	case "updatePostre":
		s.handleUpdateExistingPostre(w, r, input)
		return
	case "deletePostre":
		s.handleDeletePostre(w, r, input)
		return
	case "toggleActive":
		s.handleTogglePostreActive(w, r, input)
		return
	default:
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "error",
			"message": "Acción desconocida: " + action,
		})
		return
	}
}

func (s *Server) handleGetPostres(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"status":  "error",
			"message": "Unknown restaurant",
		})
		return
	}

	activeRows, err := s.db.QueryContext(r.Context(), "SELECT NUM, DESCRIPCION, alergenos, active FROM POSTRES WHERE restaurant_id = ? AND (active = 1 OR active IS NULL) ORDER BY NUM", restaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"status":  "error",
			"message": "Error fetching postres",
		})
		return
	}
	defer activeRows.Close()

	inactiveRows, err := s.db.QueryContext(r.Context(), "SELECT NUM, DESCRIPCION, alergenos, active FROM POSTRES WHERE restaurant_id = ? AND active = 0 ORDER BY NUM", restaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"status":  "error",
			"message": "Error fetching postres",
		})
		return
	}
	defer inactiveRows.Close()

	type PostreRow struct {
		Num         int      `json:"NUM"`
		Descripcion string   `json:"DESCRIPCION"`
		Alergenos   []string `json:"alergenos"`
		Active      int      `json:"active"`
	}

	var active []PostreRow
	for activeRows.Next() {
		var p PostreRow
		var alergRaw sql.NullString
		if err := activeRows.Scan(&p.Num, &p.Descripcion, &alergRaw, &p.Active); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"status":  "error",
				"message": "Error reading postres",
			})
			return
		}
		p.Alergenos = parseAlergenos(alergRaw)
		active = append(active, p)
	}

	var inactive []PostreRow
	for inactiveRows.Next() {
		var p PostreRow
		var alergRaw sql.NullString
		if err := inactiveRows.Scan(&p.Num, &p.Descripcion, &alergRaw, &p.Active); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"status":  "error",
				"message": "Error reading postres",
			})
			return
		}
		p.Alergenos = parseAlergenos(alergRaw)
		inactive = append(inactive, p)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"status":   "success",
		"message":  "Postres retrieved successfully",
		"active":   active,
		"inactive": inactive,
	})
}

func (s *Server) handleAddPostre(w http.ResponseWriter, r *http.Request, input map[string]any) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"status":  "error",
			"message": "Unknown restaurant",
		})
		return
	}

	descripcion := strings.TrimSpace(anyToString(input["descripcion"]))
	if descripcion == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "error",
			"message": "La descripción no puede estar vacía",
		})
		return
	}

	alergenos := anyToStringSlice(input["alergenos"])
	alergJSON, _ := json.Marshal(alergenos)

	// Match legacy behavior: NUM = MAX(NUM) + 1 (reuses IDs if the max was deleted).
	var maxNum sql.NullInt64
	if err := s.db.QueryRowContext(r.Context(), "SELECT MAX(NUM) as maxNum FROM POSTRES WHERE restaurant_id = ?", restaurantID).Scan(&maxNum); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"status":  "error",
			"message": "Error getting max NUM",
		})
		return
	}
	newNum := int64(1)
	if maxNum.Valid {
		newNum = maxNum.Int64 + 1
	}

	_, err := s.db.ExecContext(r.Context(), "INSERT INTO POSTRES (restaurant_id, NUM, DESCRIPCION, alergenos, active) VALUES (?, ?, ?, ?, 1)", restaurantID, newNum, descripcion, string(alergJSON))
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"status":  "error",
			"message": "Error adding postre",
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"message": "Postre añadido correctamente",
		"newId":   newNum,
	})
}

func (s *Server) handleUpdateExistingPostre(w http.ResponseWriter, r *http.Request, input map[string]any) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"status":  "error",
			"message": "Unknown restaurant",
		})
		return
	}

	num, err := anyToInt(input["num"])
	if err != nil || num <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "error",
			"message": "ID de postre inválido",
		})
		return
	}
	descripcion := strings.TrimSpace(anyToString(input["descripcion"]))
	if descripcion == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "error",
			"message": "La descripción no puede estar vacía",
		})
		return
	}

	alergenos := anyToStringSlice(input["alergenos"])
	alergJSON, _ := json.Marshal(alergenos)

	res, err := s.db.ExecContext(r.Context(), "UPDATE POSTRES SET DESCRIPCION = ?, alergenos = ? WHERE restaurant_id = ? AND NUM = ?", descripcion, string(alergJSON), restaurantID, num)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"status":  "error",
			"message": "Error updating postre",
		})
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "warning",
			"message": "No se realizaron cambios o el postre no existe",
		})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"message": "Postre actualizado correctamente",
	})
}

func (s *Server) handleDeletePostre(w http.ResponseWriter, r *http.Request, input map[string]any) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"status":  "error",
			"message": "Unknown restaurant",
		})
		return
	}

	num, err := anyToInt(input["num"])
	if err != nil || num <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "error",
			"message": "ID de postre inválido",
		})
		return
	}

	res, err := s.db.ExecContext(r.Context(), "DELETE FROM POSTRES WHERE restaurant_id = ? AND NUM = ?", restaurantID, num)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"status":  "error",
			"message": "Error deleting postre",
		})
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "warning",
			"message": "El postre no existe o ya fue eliminado",
		})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"message": "Postre eliminado correctamente",
	})
}

func (s *Server) handleTogglePostreActive(w http.ResponseWriter, r *http.Request, input map[string]any) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"status":  "error",
			"message": "Unknown restaurant",
		})
		return
	}

	num, err := anyToInt(input["num"])
	if err != nil || num <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "error",
			"message": "ID de postre inválido",
		})
		return
	}
	active, err := anyToInt(input["active"])
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "error",
			"message": "Estado activo inválido",
		})
		return
	}
	activeInt := 0
	if active != 0 {
		activeInt = 1
	}

	res, err := s.db.ExecContext(r.Context(), "UPDATE POSTRES SET active = ? WHERE restaurant_id = ? AND NUM = ?", activeInt, restaurantID, num)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"status":  "error",
			"message": "Error toggling postre status",
		})
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "warning",
			"message": "No se realizaron cambios o el postre no existe",
		})
		return
	}

	statusText := "desactivado"
	if activeInt != 0 {
		statusText = "activado"
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"message": "Postre " + statusText + " correctamente",
	})
}

func (s *Server) handleSearchPostres(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"status":      "error",
			"message":     "Unknown restaurant",
			"matchingIds": []int{},
		})
		return
	}

	searchTerm := strings.TrimSpace(r.URL.Query().Get("searchTerm"))

	if searchTerm == "" {
		rows, err := s.db.QueryContext(r.Context(), "SELECT NUM FROM POSTRES WHERE restaurant_id = ? ORDER BY NUM", restaurantID)
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"status":      "error",
				"message":     "Error en la búsqueda",
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
					"message":     "Error en la búsqueda",
					"matchingIds": []int{},
				})
				return
			}
			ids = append(ids, id)
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":      "success",
			"message":     "Se encontraron todos los postres",
			"matchingIds": ids,
		})
		return
	}

	likeTerm := "%" + searchTerm + "%"
	rows, err := s.db.QueryContext(r.Context(), "SELECT NUM FROM POSTRES WHERE restaurant_id = ? AND DESCRIPCION LIKE ?", restaurantID, likeTerm)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"status":      "error",
			"message":     "Error en la búsqueda",
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
				"message":     "Error en la búsqueda",
				"matchingIds": []int{},
			})
			return
		}
		ids = append(ids, id)
	}

	msg := "No se encontraron postres que coincidan con: " + searchTerm
	if len(ids) > 0 {
		msg = "Se encontraron " + strconv.Itoa(len(ids)) + " postres que coinciden con: " + searchTerm
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"status":      "success",
		"message":     msg,
		"matchingIds": ids,
	})
}

func anyToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		b, _ := json.Marshal(x)
		return strings.Trim(string(b), "\"")
	}
}

func anyToInt(v any) (int, error) {
	switch x := v.(type) {
	case float64:
		return int(x), nil
	case int:
		return x, nil
	case int64:
		return int(x), nil
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, errors.New("empty")
		}
		i, err := strconv.Atoi(s)
		if err != nil {
			return 0, err
		}
		return i, nil
	case nil:
		return 0, errors.New("nil")
	default:
		return 0, errors.New("unsupported")
	}
}

func anyToStringSlice(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, it := range x {
			s := strings.TrimSpace(anyToString(it))
			if s == "" {
				continue
			}
			out = append(out, s)
		}
		return out
	case nil:
		return []string{}
	case string:
		// Accept a single string as a 1-length slice (best effort).
		s := strings.TrimSpace(x)
		if s == "" {
			return []string{}
		}
		return []string{s}
	default:
		return []string{}
	}
}
