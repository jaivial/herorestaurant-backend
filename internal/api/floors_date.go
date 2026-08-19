package api

import (
	"context"
	"net/http"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// Date-scoped floor count: POST /api/admin/config/floors/date sets how many
// floors exist for one date, overriding the global default.
//
// - Increasing beyond the global floors creates date-scoped rows
//   (restaurant_floors.specific_date = date), visible only on that date.
// - Decreasing deactivates floors for that date (existing override), and
//   deletes date-scoped rows created for it (never touches global rows).
// - A date-scoped floor with the same floor_number shadows the global one.

type boConfigFloorsDateSetRequest struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

func (s *Server) handleBOConfigFloorsDateSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boConfigFloorsDateSetRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	date := strings.TrimSpace(req.Date)
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid date"})
		return
	}
	if req.Count < 1 || req.Count > maxRestaurantFloors {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "count invalido"})
		return
	}

	if err := s.setFloorsForDate(r.Context(), a.ActiveRestaurantID, date, req.Count); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando plantas del dia")
		return
	}

	floors, err := s.loadDateFloors(r.Context(), a.ActiveRestaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando plantas")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"date":    date,
		"floors":  floors,
	})
}

func (s *Server) setFloorsForDate(ctx context.Context, restaurantID int, date string, count int) error {
	floors, err := s.loadDateFloors(ctx, restaurantID, date)
	if err != nil {
		return err
	}

	active := make([]boConfigFloor, 0, len(floors))
	for _, floor := range floors {
		if floor.Active {
			active = append(active, floor)
		}
	}

	// Decrease: drop floors from the top down.
	for len(active) > count {
		top := active[len(active)-1]
		active = active[:len(active)-1]
		if top.DateScoped != "" {
			// Date-scoped rows only exist for this date: remove outright.
			if _, err := s.db.ExecContext(ctx, `
				DELETE FROM restaurant_floors
				WHERE id = ? AND restaurant_id = ? AND specific_date = ?
			`, top.ID, restaurantID, date); err != nil {
				return err
			}
			if _, err := s.db.ExecContext(ctx, `
				DELETE FROM restaurant_floor_overrides
				WHERE restaurant_id = ? AND date = ? AND floor_id = ?
			`, restaurantID, date, top.ID); err != nil {
				return err
			}
			continue
		}
		if err := s.setFloorOverrideForDate(ctx, restaurantID, date, top.ID, false); err != nil {
			return err
		}
	}

	// Increase: activate existing floors first, then create date-scoped ones.
	for len(active) < count {
		var next *boConfigFloor
		for i := range floors {
			if floors[i].Active {
				continue
			}
			if next == nil || floors[i].FloorNumber < next.FloorNumber {
				next = &floors[i]
			}
		}
		if next != nil {
			if err := s.setFloorOverrideForDate(ctx, restaurantID, date, next.ID, true); err != nil {
				return err
			}
			next.Active = true
			active = append(active, *next)
			continue
		}

		// No inactive floor left for this date: create a date-scoped floor.
		maxNumber := 0
		for _, floor := range floors {
			if floor.FloorNumber > maxNumber {
				maxNumber = floor.FloorNumber
			}
		}
		if maxNumber+1 >= maxRestaurantFloors {
			break
		}
		res, err := s.db.ExecContext(ctx, `
			INSERT INTO restaurant_floors (restaurant_id, floor_number, floor_name, is_ground, is_active, specific_date)
			VALUES (?, ?, ?, 0, 1, ?)
		`, restaurantID, maxNumber+1, floorNameForNumber(maxNumber+1), date)
		if err != nil {
			return err
		}
		newID, _ := res.LastInsertId()
		created := boConfigFloor{
			ID:          int(newID),
			FloorNumber: maxNumber + 1,
			Name:        floorNameForNumber(maxNumber + 1),
			Active:      true,
			DateScoped:  date,
		}
		floors = append(floors, created)
		active = append(active, created)
	}
	return nil
}

// setFloorOverrideForDate writes/removes the per-date activation override for
// one floor (same inherit semantics as handleBOConfigFloorsSet).
func (s *Server) setFloorOverrideForDate(ctx context.Context, restaurantID int, date string, floorID int, active bool) error {
	var defaultActive int
	err := s.db.QueryRowContext(ctx, `
		SELECT is_active FROM restaurant_floors WHERE id = ? AND restaurant_id = ?
	`, floorID, restaurantID).Scan(&defaultActive)
	if err != nil {
		return err
	}

	if (defaultActive != 0) == active {
		_, err = s.db.ExecContext(ctx, `
			DELETE FROM restaurant_floor_overrides
			WHERE restaurant_id = ? AND date = ? AND floor_id = ?
		`, restaurantID, date, floorID)
		return err
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO restaurant_floor_overrides (restaurant_id, date, floor_id, is_active)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE is_active = VALUES(is_active)
	`, restaurantID, date, floorID, boolToInt(active))
	return err
}
