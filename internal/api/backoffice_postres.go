package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

type boPostre struct {
	Num         int      `json:"num"`
	Descripcion string   `json:"descripcion"`
	Alergenos   []string `json:"alergenos"`
	Active      bool     `json:"active"`
}

func (s *Server) handleBOPostresList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	restaurantID := a.ActiveRestaurantID
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT NUM, DESCRIPCION, alergenos, active
		FROM POSTRES
		WHERE restaurant_id = ?
		ORDER BY active DESC, NUM ASC
	`, restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando POSTRES")
		return
	}
	defer rows.Close()

	var out []boPostre
	for rows.Next() {
		var (
			num       int
			desc      string
			alergRaw  sql.NullString
			activeInt int
		)
		if err := rows.Scan(&num, &desc, &alergRaw, &activeInt); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo POSTRES")
			return
		}
		out = append(out, boPostre{
			Num:         num,
			Descripcion: desc,
			Alergenos:   parseAlergenos(alergRaw),
			Active:      activeInt != 0,
		})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"postres": out,
	})
}

type boPostreCreateRequest struct {
	Descripcion string   `json:"descripcion"`
	Alergenos   []string `json:"alergenos"`
	Active      *bool    `json:"active,omitempty"`
}

func (s *Server) handleBOPostreCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boPostreCreateRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	desc := strings.TrimSpace(req.Descripcion)
	if desc == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Descripcion requerida",
		})
		return
	}

	active := true
	if req.Active != nil {
		active = *req.Active
	}
	activeInt := 0
	if active {
		activeInt = 1
	}

	alergJSON, _ := json.Marshal(req.Alergenos)

	restaurantID := a.ActiveRestaurantID
	res, err := s.db.ExecContext(r.Context(), `
		INSERT INTO POSTRES (restaurant_id, DESCRIPCION, alergenos, active)
		VALUES (?, ?, ?, ?)
	`, restaurantID, desc, string(alergJSON), activeInt)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error insertando POSTRES")
		return
	}
	newID, _ := res.LastInsertId()

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"postre": boPostre{
			Num:         int(newID),
			Descripcion: desc,
			Alergenos:   req.Alergenos,
			Active:      active,
		},
	})
}

type boPostrePatchRequest struct {
	Descripcion *string   `json:"descripcion,omitempty"`
	Alergenos   *[]string `json:"alergenos,omitempty"`
	Active      *bool     `json:"active,omitempty"`
}

func (s *Server) handleBOPostrePatch(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	idStr := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid postre id",
		})
		return
	}

	var req boPostrePatchRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	var (
		sets []string
		args []any
	)
	if req.Descripcion != nil {
		d := strings.TrimSpace(*req.Descripcion)
		if d == "" {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Descripcion no puede estar vacia",
			})
			return
		}
		sets = append(sets, "DESCRIPCION = ?")
		args = append(args, d)
	}
	if req.Alergenos != nil {
		alergJSON, _ := json.Marshal(*req.Alergenos)
		sets = append(sets, "alergenos = ?")
		args = append(args, string(alergJSON))
	}
	if req.Active != nil {
		activeInt := 0
		if *req.Active {
			activeInt = 1
		}
		sets = append(sets, "active = ?")
		args = append(args, activeInt)
	}
	if len(sets) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "No fields to update",
		})
		return
	}

	restaurantID := a.ActiveRestaurantID
	args = append(args, id, restaurantID)

	q := "UPDATE POSTRES SET " + strings.Join(sets, ", ") + " WHERE NUM = ? AND restaurant_id = ?"
	res, err := s.db.ExecContext(r.Context(), q, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando POSTRES")
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Postre not found",
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}

func (s *Server) handleBOPostreDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	idStr := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid postre id",
		})
		return
	}

	restaurantID := a.ActiveRestaurantID
	res, err := s.db.ExecContext(r.Context(), "DELETE FROM POSTRES WHERE NUM = ? AND restaurant_id = ?", id, restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando POSTRES")
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Postre not found",
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}

