package api

import (
	"context"
	"database/sql"
	"errors"
	"github.com/go-chi/chi/v5"
	"net/http"
	"strconv"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// Salons (dining rooms) per floor — backoffice config.
// Global defaults live in restaurant_salons; per-day overrides in
// restaurant_salons_overrides (mirrors the floors pattern).

type boConfigSalon struct {
	ID               int    `json:"id"`
	FloorID          int    `json:"floorId"`
	FloorNumber      int    `json:"floorNumber"`
	FloorName        string `json:"floorName"`
	Name             string `json:"name"`
	HasCapacityLimit bool   `json:"hasCapacityLimit"`
	CapacityLimit    int    `json:"capacityLimit"`
	IsActive         bool   `json:"isActive"`
	DisplayOrder     int    `json:"displayOrder"`
}

type boConfigSalonWriteRequest struct {
	FloorID          int    `json:"floorId"`
	Name             string `json:"name"`
	HasCapacityLimit bool   `json:"hasCapacityLimit"`
	CapacityLimit    int    `json:"capacityLimit"`
	IsActive         *bool  `json:"isActive,omitempty"`
}

type boConfigSalonDayStatusRequest struct {
	Date    string `json:"date"`
	SalonID int    `json:"salonId"`
	Active  bool   `json:"active"`
}

const (
	defaultSalonCapacityLimit = 45
	maxSalonCapacityLimit     = 2000
)

func (s *Server) loadSalons(ctx context.Context, restaurantID int, date string) ([]boConfigSalon, error) {
	overrides := map[int]bool{}
	if date != "" {
		rows, err := s.db.QueryContext(ctx, `
			SELECT salon_id, is_active
			FROM restaurant_salons_overrides
			WHERE restaurant_id = ? AND date = ?
		`, restaurantID, date)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var salonID int
			var activeInt int
			if err := rows.Scan(&salonID, &activeInt); err != nil {
				return nil, err
			}
			overrides[salonID] = activeInt != 0
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT sal.id, sal.floor_id, f.floor_number, f.floor_name,
		       sal.name, sal.has_capacity_limit, sal.capacity_limit, sal.is_active, sal.display_order
		FROM restaurant_salons sal
		JOIN restaurant_floors f ON f.id = sal.floor_id AND f.restaurant_id = sal.restaurant_id
		WHERE sal.restaurant_id = ?
		ORDER BY f.floor_number ASC, sal.display_order ASC, sal.id ASC
	`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]boConfigSalon, 0, 8)
	for rows.Next() {
		var row boConfigSalon
		var hasLimitInt, activeInt int
		if err := rows.Scan(&row.ID, &row.FloorID, &row.FloorNumber, &row.FloorName, &row.Name, &hasLimitInt, &row.CapacityLimit, &activeInt, &row.DisplayOrder); err != nil {
			return nil, err
		}
		row.HasCapacityLimit = hasLimitInt != 0
		row.IsActive = activeInt != 0
		if v, ok := overrides[row.ID]; ok {
			row.IsActive = v
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func normalizeSalonWrite(restaurantID int, req boConfigSalonWriteRequest) (boConfigSalonWriteRequest, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return req, errors.New("nombre requerido")
	}
	if len(req.Name) > 120 {
		return req, errors.New("nombre demasiado largo")
	}
	if req.CapacityLimit < 0 || req.CapacityLimit > maxSalonCapacityLimit {
		return req, errors.New("capacidad invalida")
	}
	if !req.HasCapacityLimit {
		req.CapacityLimit = defaultSalonCapacityLimit
	} else if req.CapacityLimit == 0 {
		req.CapacityLimit = defaultSalonCapacityLimit
	}
	_ = restaurantID
	return req, nil
}

func (s *Server) salonFloorBelongs(ctx context.Context, restaurantID, floorID int) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM restaurant_floors WHERE id = ? AND restaurant_id = ?
	`, floorID, restaurantID).Scan(&one)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Server) handleBOConfigSalonsList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date != "" && !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid date"})
		return
	}
	salons, err := s.loadSalons(r.Context(), a.ActiveRestaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando salones")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "salons": salons})
}

func (s *Server) handleBOConfigSalonsCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var req boConfigSalonWriteRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	req, err := normalizeSalonWrite(a.ActiveRestaurantID, req)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": err.Error()})
		return
	}
	belongs, err := s.salonFloorBelongs(r.Context(), a.ActiveRestaurantID, req.FloorID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error validando planta")
		return
	}
	if !belongs {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "planta no encontrada"})
		return
	}

	active := true
	if req.IsActive != nil {
		active = *req.IsActive
	}
	res, err := s.db.ExecContext(r.Context(), `
		INSERT INTO restaurant_salons (restaurant_id, floor_id, name, has_capacity_limit, capacity_limit, is_active)
		VALUES (?, ?, ?, ?, ?, ?)
	`, a.ActiveRestaurantID, req.FloorID, req.Name, boolToInt(req.HasCapacityLimit), req.CapacityLimit, boolToInt(active))
	if err != nil {
		if strings.Contains(err.Error(), "Duplicate entry") {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "ya existe un salon con ese nombre en la planta"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando salon")
		return
	}
	id, _ := res.LastInsertId()

	salons, err := s.loadSalons(r.Context(), a.ActiveRestaurantID, "")
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando salones")
		return
	}
	var created *boConfigSalon
	for i := range salons {
		if salons[i].ID == int(id) {
			created = &salons[i]
			break
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "salon": created, "salons": salons})
}

func (s *Server) handleBOConfigSalonsUpdate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	salonID, err := strconv.Atoi(strings.TrimSpace(chi.URLParam(r, "salonId")))
	if err != nil || salonID <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "salonId invalido"})
		return
	}
	var req boConfigSalonWriteRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	req, err = normalizeSalonWrite(a.ActiveRestaurantID, req)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": err.Error()})
		return
	}
	belongs, err := s.salonFloorBelongs(r.Context(), a.ActiveRestaurantID, req.FloorID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error validando planta")
		return
	}
	if !belongs {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "planta no encontrada"})
		return
	}

	var exists int
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT 1 FROM restaurant_salons WHERE id = ? AND restaurant_id = ?
	`, salonID, a.ActiveRestaurantID).Scan(&exists); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "salon no encontrado"})
		return
	}

	active := true
	if req.IsActive != nil {
		active = *req.IsActive
	}
	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE restaurant_salons
		SET floor_id = ?, name = ?, has_capacity_limit = ?, capacity_limit = ?, is_active = ?
		WHERE id = ? AND restaurant_id = ?
	`, req.FloorID, req.Name, boolToInt(req.HasCapacityLimit), req.CapacityLimit, boolToInt(active), salonID, a.ActiveRestaurantID); err != nil {
		if strings.Contains(err.Error(), "Duplicate entry") {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "ya existe un salon con ese nombre en la planta"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando salon")
		return
	}

	salons, err := s.loadSalons(r.Context(), a.ActiveRestaurantID, "")
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando salones")
		return
	}
	var updated *boConfigSalon
	for i := range salons {
		if salons[i].ID == salonID {
			updated = &salons[i]
			break
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "salon": updated, "salons": salons})
}

func (s *Server) handleBOConfigSalonsDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	salonID, err := strconv.Atoi(strings.TrimSpace(chi.URLParam(r, "salonId")))
	if err != nil || salonID <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "salonId invalido"})
		return
	}
	res, err := s.db.ExecContext(r.Context(), `
		DELETE FROM restaurant_salons WHERE id = ? AND restaurant_id = ?
	`, salonID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando salon")
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "salon no encontrado"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleBOConfigSalonsDayStatusSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var req boConfigSalonDayStatusRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	if !isValidISODate(strings.TrimSpace(req.Date)) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid date"})
		return
	}

	var defaultActive int
	err := s.db.QueryRowContext(r.Context(), `
		SELECT is_active FROM restaurant_salons WHERE id = ? AND restaurant_id = ?
	`, req.SalonID, a.ActiveRestaurantID).Scan(&defaultActive)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "salon no encontrado"})
		return
	}

	if (defaultActive != 0) == req.Active {
		// Same as the effective default: drop the override.
		_, _ = s.db.ExecContext(r.Context(), `
			DELETE FROM restaurant_salons_overrides
			WHERE restaurant_id = ? AND date = ? AND salon_id = ?
		`, a.ActiveRestaurantID, req.Date, req.SalonID)
	} else {
		if _, err := s.db.ExecContext(r.Context(), `
			INSERT INTO restaurant_salons_overrides (restaurant_id, date, salon_id, is_active)
			VALUES (?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE is_active = VALUES(is_active)
		`, a.ActiveRestaurantID, req.Date, req.SalonID, boolToInt(req.Active)); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando salon del dia")
			return
		}
	}

	salons, err := s.loadSalons(r.Context(), a.ActiveRestaurantID, req.Date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando salones")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "date": req.Date, "salons": salons})
}
