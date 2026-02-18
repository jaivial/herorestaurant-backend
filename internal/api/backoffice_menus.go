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

type boMenuDish struct {
	Num         int      `json:"num"`
	Descripcion string   `json:"descripcion"`
	Tipo        string   `json:"tipo"`
	Alergenos   []string `json:"alergenos"`
	Active      bool     `json:"active"`
}

func isValidMenuDishType(t string) bool {
	switch t {
	case "ENTRANTE", "PRINCIPAL", "ARROZ":
		return true
	default:
		return false
	}
}

func (s *Server) handleBOMenuDiaGet(w http.ResponseWriter, r *http.Request) {
	s.handleBOMenuTableGet(w, r, "DIA")
}

func (s *Server) handleBOMenuFindeGet(w http.ResponseWriter, r *http.Request) {
	s.handleBOMenuTableGet(w, r, "FINDE")
}

func (s *Server) handleBOMenuTableGet(w http.ResponseWriter, r *http.Request, table string) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	restaurantID := a.ActiveRestaurantID

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT NUM, DESCRIPCION, TIPO, alergenos, active
		FROM `+table+`
		WHERE restaurant_id = ?
		ORDER BY
		  CASE WHEN TIPO = 'PRECIO' THEN 0 ELSE 1 END,
		  TIPO ASC,
		  NUM ASC
	`, restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando "+table)
		return
	}
	defer rows.Close()

	var (
		dishes []boMenuDish
		price  *string
	)
	for rows.Next() {
		var (
			num         int
			desc        string
			tipo        string
			alergRaw    sql.NullString
			activeInt   int
		)
		if err := rows.Scan(&num, &desc, &tipo, &alergRaw, &activeInt); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo "+table)
			return
		}
		tipo = strings.TrimSpace(tipo)
		d := boMenuDish{
			Num:         num,
			Descripcion: desc,
			Tipo:        tipo,
			Alergenos:   parseAlergenos(alergRaw),
			Active:      activeInt != 0,
		}
		dishes = append(dishes, d)
		if tipo == "PRECIO" && activeInt != 0 {
			v := strings.TrimSpace(desc)
			price = &v
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"menu": map[string]any{
			"table":  table,
			"price":  price,
			"dishes": dishes,
		},
	})
}

type boMenuDishCreateRequest struct {
	Tipo        string   `json:"tipo"`
	Descripcion string   `json:"descripcion"`
	Alergenos   []string `json:"alergenos"`
	Active      *bool    `json:"active,omitempty"`
}

func (s *Server) handleBOMenuDiaDishCreate(w http.ResponseWriter, r *http.Request) {
	s.handleBOMenuDishCreate(w, r, "DIA")
}

func (s *Server) handleBOMenuFindeDishCreate(w http.ResponseWriter, r *http.Request) {
	s.handleBOMenuDishCreate(w, r, "FINDE")
}

func (s *Server) handleBOMenuDishCreate(w http.ResponseWriter, r *http.Request, table string) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boMenuDishCreateRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	tipo := strings.ToUpper(strings.TrimSpace(req.Tipo))
	if !isValidMenuDishType(tipo) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid tipo (expected ENTRANTE|PRINCIPAL|ARROZ)",
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
		INSERT INTO `+table+` (restaurant_id, DESCRIPCION, TIPO, alergenos, active)
		VALUES (?, ?, ?, ?, ?)
	`, restaurantID, desc, tipo, string(alergJSON), activeInt)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error insertando en "+table)
		return
	}
	newID, _ := res.LastInsertId()

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"dish": boMenuDish{
			Num:         int(newID),
			Descripcion: desc,
			Tipo:        tipo,
			Alergenos:   req.Alergenos,
			Active:      active,
		},
	})
}

type boMenuDishPatchRequest struct {
	Tipo        *string  `json:"tipo,omitempty"`
	Descripcion *string  `json:"descripcion,omitempty"`
	Alergenos   *[]string `json:"alergenos,omitempty"`
	Active      *bool    `json:"active,omitempty"`
}

func (s *Server) handleBOMenuDiaDishPatch(w http.ResponseWriter, r *http.Request) {
	s.handleBOMenuDishPatch(w, r, "DIA")
}

func (s *Server) handleBOMenuFindeDishPatch(w http.ResponseWriter, r *http.Request) {
	s.handleBOMenuDishPatch(w, r, "FINDE")
}

func (s *Server) handleBOMenuDishPatch(w http.ResponseWriter, r *http.Request, table string) {
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
			"message": "Invalid dish id",
		})
		return
	}

	var req boMenuDishPatchRequest
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
	if req.Tipo != nil {
		t := strings.ToUpper(strings.TrimSpace(*req.Tipo))
		if t != "PRECIO" && !isValidMenuDishType(t) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Invalid tipo",
			})
			return
		}
		sets = append(sets, "TIPO = ?")
		args = append(args, t)
	}
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

	q := "UPDATE " + table + " SET " + strings.Join(sets, ", ") + " WHERE NUM = ? AND restaurant_id = ?"
	res, err := s.db.ExecContext(r.Context(), q, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando "+table)
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Dish not found",
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}

func (s *Server) handleBOMenuDiaDishDelete(w http.ResponseWriter, r *http.Request) {
	s.handleBOMenuDishDelete(w, r, "DIA")
}

func (s *Server) handleBOMenuFindeDishDelete(w http.ResponseWriter, r *http.Request) {
	s.handleBOMenuDishDelete(w, r, "FINDE")
}

func (s *Server) handleBOMenuDishDelete(w http.ResponseWriter, r *http.Request, table string) {
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
			"message": "Invalid dish id",
		})
		return
	}

	restaurantID := a.ActiveRestaurantID
	res, err := s.db.ExecContext(r.Context(), "DELETE FROM "+table+" WHERE NUM = ? AND restaurant_id = ?", id, restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando "+table)
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Dish not found",
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}

type boMenuPriceSetRequest struct {
	Price string `json:"price"`
}

func (s *Server) handleBOMenuDiaSetPrice(w http.ResponseWriter, r *http.Request) {
	s.handleBOMenuSetPrice(w, r, "DIA")
}

func (s *Server) handleBOMenuFindeSetPrice(w http.ResponseWriter, r *http.Request) {
	s.handleBOMenuSetPrice(w, r, "FINDE")
}

func (s *Server) handleBOMenuSetPrice(w http.ResponseWriter, r *http.Request, table string) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boMenuPriceSetRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}
	price := strings.TrimSpace(req.Price)
	if price == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Price requerido",
		})
		return
	}

	restaurantID := a.ActiveRestaurantID

	res, err := s.db.ExecContext(r.Context(), `
		UPDATE `+table+`
		SET DESCRIPCION = ?, active = 1
		WHERE restaurant_id = ? AND TIPO = 'PRECIO'
	`, price, restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando precio en "+table)
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		// Create if missing.
		_, err := s.db.ExecContext(r.Context(), `
			INSERT INTO `+table+` (restaurant_id, DESCRIPCION, TIPO, alergenos, active)
			VALUES (?, ?, 'PRECIO', '[]', 1)
		`, restaurantID, price)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error insertando precio en "+table)
			return
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"price":   price,
	})
}

