package api

import (
	"context"
	"database/sql"
	"net/http"

	"preactvillacarmen/internal/httpx"
)

type dishSameDayBooking struct {
	ID           int64  `json:"id"`
	DishID       int64  `json:"dish_id"`
	MenuID       int64  `json:"menu_id"`
	RestaurantID int    `json:"restaurant_id"`
	State        int    `json:"state"`
	DateModified string `json:"date_modified"`
	UserID       int    `json:"user_id"`
}

func (s *Server) handleBODishSameDayBookingList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	menuID, err := parseChiPositiveInt64(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid menu id"})
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, dish_id, menu_id, restaurant_id, state, date_modified, user_id
		FROM dish_same_day_booking
		WHERE menu_id = ? AND restaurant_id = ?
	`, menuID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading same day booking records")
		return
	}
	defer rows.Close()

	var records []dishSameDayBooking
	for rows.Next() {
		var rec dishSameDayBooking
		if err := rows.Scan(&rec.ID, &rec.DishID, &rec.MenuID, &rec.RestaurantID, &rec.State, &rec.DateModified, &rec.UserID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error scanning same day booking records")
			return
		}
		records = append(records, rec)
	}

	dishIDs := make([]int64, 0, len(records))
	for _, rec := range records {
		dishIDs = append(dishIDs, rec.DishID)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"dish_ids": dishIDs,
		"records":  records,
	})
}

func (s *Server) handleBODishSameDayBookingCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	menuID, err := parseChiPositiveInt64(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid menu id"})
		return
	}

	dishID, err := parseChiPositiveInt64(r, "dishId")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid dish id"})
		return
	}

	var exists int
	err = s.db.QueryRowContext(r.Context(), `
		SELECT COUNT(*) FROM group_menu_section_dishes_v2
		WHERE id = ? AND menu_id = ? AND restaurant_id = ?
	`, dishID, menuID, a.ActiveRestaurantID).Scan(&exists)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error verifying dish")
		return
	}
	if exists == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Dish not found in this menu"})
		return
	}

	result, err := s.db.ExecContext(r.Context(), `
		INSERT INTO dish_same_day_booking (dish_id, menu_id, restaurant_id, state, user_id)
		VALUES (?, ?, ?, 1, ?)
		ON DUPLICATE KEY UPDATE state = 1, user_id = VALUES(user_id), date_modified = CURRENT_TIMESTAMP
	`, dishID, menuID, a.ActiveRestaurantID, a.User.ID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creating same day booking record")
		return
	}

	insertedID, _ := result.LastInsertId()

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"record": map[string]any{
			"id":            insertedID,
			"dish_id":       dishID,
			"menu_id":       menuID,
			"restaurant_id": a.ActiveRestaurantID,
		},
	})
}

func (s *Server) handleBODishSameDayBookingDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	menuID, err := parseChiPositiveInt64(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid menu id"})
		return
	}

	dishID, err := parseChiPositiveInt64(r, "dishId")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid dish id"})
		return
	}

	_, err = s.db.ExecContext(r.Context(), `
		DELETE FROM dish_same_day_booking
		WHERE dish_id = ? AND menu_id = ? AND restaurant_id = ?
	`, dishID, menuID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error deleting same day booking record")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"dish_id": dishID,
		"menu_id": menuID,
	})
}

func (s *Server) loadDishSameDayBookingDishIDs(ctx context.Context, restaurantID int, menuID int64) (map[int64]bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT dish_id FROM dish_same_day_booking
		WHERE menu_id = ? AND restaurant_id = ?
	`, menuID, restaurantID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64]bool)
	for rows.Next() {
		var dishID int64
		if err := rows.Scan(&dishID); err != nil {
			return nil, err
		}
		result[dishID] = true
	}
	return result, nil
}
