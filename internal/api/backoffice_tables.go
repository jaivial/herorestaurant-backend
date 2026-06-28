package api

import (
	"encoding/json"
	"net/http"

	"preactvillacarmen/internal/httpx"
)

type TableArea struct {
	ID           int            `json:"id"`
	RestaurantID int            `json:"restaurant_id"`
	Name         string         `json:"name"`
	BgColor      string         `json:"bg_color"`
	Tables       []TableElement `json:"tables"`
}

type TableElement struct {
	ID           int    `json:"id"`
	RestaurantID int    `json:"restaurant_id"`
	AreaID       int    `json:"area_id"`
	Name         string `json:"name"`
	Capacity     int    `json:"capacity"`
	XPos         int    `json:"x_pos"`
	YPos         int    `json:"y_pos"`
	Status       string `json:"status"`
}

func (s *Server) handleGetTables(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "No autorizado")
		return
	}

	areas := make([]TableArea, 0)

	rows, err := s.db.QueryContext(r.Context(), "SELECT id, restaurant_id, name, bg_color FROM restaurant_areas WHERE restaurant_id = ?", a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error al obtener áreas")
		return
	}
	defer rows.Close()

	for rows.Next() {
		var area TableArea
		if err := rows.Scan(&area.ID, &area.RestaurantID, &area.Name, &area.BgColor); err != nil {
			continue
		}
		area.Tables = make([]TableElement, 0)
		areas = append(areas, area)
	}

	tablesRows, err := s.db.QueryContext(r.Context(), "SELECT id, restaurant_id, area_id, name, capacity, x_pos, y_pos, status FROM restaurant_tables WHERE restaurant_id = ?", a.ActiveRestaurantID)
	if err == nil {
		defer tablesRows.Close()
		for tablesRows.Next() {
			var t TableElement
			if err := tablesRows.Scan(&t.ID, &t.RestaurantID, &t.AreaID, &t.Name, &t.Capacity, &t.XPos, &t.YPos, &t.Status); err == nil {
				for i, area := range areas {
					if area.ID == t.AreaID {
						areas[i].Tables = append(areas[i].Tables, t)
						break
					}
				}
			}
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    areas,
	})
}

func (s *Server) handleCreateArea(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "No autorizado")
		return
	}

	var req struct {
		Name    string `json:"name"`
		BgColor string `json:"bg_color"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Datos inválidos")
		return
	}

	if req.BgColor == "" {
		req.BgColor = "#ffffff"
	}

	res, err := s.db.ExecContext(r.Context(), "INSERT INTO restaurant_areas (restaurant_id, name, bg_color) VALUES (?, ?, ?)", a.ActiveRestaurantID, req.Name, req.BgColor)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error al crear área")
		return
	}

	id, _ := res.LastInsertId()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"id":            id,
			"restaurant_id": a.ActiveRestaurantID,
			"name":          req.Name,
			"bg_color":      req.BgColor,
			"tables":        []TableElement{},
		},
	})
}

func (s *Server) handleCreateTable(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "No autorizado")
		return
	}

	var req struct {
		AreaID   int    `json:"area_id"`
		Name     string `json:"name"`
		Capacity int    `json:"capacity"`
		XPos     int    `json:"x_pos"`
		YPos     int    `json:"y_pos"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AreaID == 0 || req.Name == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Datos inválidos")
		return
	}

	res, err := s.db.ExecContext(r.Context(), "INSERT INTO restaurant_tables (restaurant_id, area_id, name, capacity, x_pos, y_pos, status) VALUES (?, ?, ?, ?, ?, ?, 'available')",
		a.ActiveRestaurantID, req.AreaID, req.Name, req.Capacity, req.XPos, req.YPos)

	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error al crear mesa")
		return
	}

	id, _ := res.LastInsertId()
	newTable := TableElement{
		ID:           int(id),
		RestaurantID: a.ActiveRestaurantID,
		AreaID:       req.AreaID,
		Name:         req.Name,
		Capacity:     req.Capacity,
		XPos:         req.XPos,
		YPos:         req.YPos,
		Status:       "available",
	}

	s.tablesHub.broadcast(a.ActiveRestaurantID, map[string]any{
		"type":  "table_created",
		"table": newTable,
	})

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    newTable,
	})
}

func (s *Server) handleUpdateTable(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "No autorizado")
		return
	}

	var req struct {
		ID     int    `json:"id"`
		XPos   int    `json:"x_pos"`
		YPos   int    `json:"y_pos"`
		Status string `json:"status"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Datos inválidos")
		return
	}

	query := "UPDATE restaurant_tables SET "
	args := []any{}

	if req.XPos != 0 || req.YPos != 0 {
		query += "x_pos = ?, y_pos = ?, "
		args = append(args, req.XPos, req.YPos)
	}
	if req.Status != "" {
		query += "status = ?, "
		args = append(args, req.Status)
	}

	if len(args) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
		return
	}

	query = query[:len(query)-2]
	query += " WHERE id = ? AND restaurant_id = ?"
	args = append(args, req.ID, a.ActiveRestaurantID)

	_, err := s.db.ExecContext(r.Context(), query, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error al actualizar mesa")
		return
	}

	s.tablesHub.broadcast(a.ActiveRestaurantID, map[string]any{
		"type":     "table_updated",
		"table_id": req.ID,
		"x_pos":    req.XPos,
		"y_pos":    req.YPos,
		"status":   req.Status,
	})

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}
