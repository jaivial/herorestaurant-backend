package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleBOProductionOrdersList(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	rows, err := s.db.QueryContext(r.Context(), `SELECT o.id,r.name,o.produced_at,o.batches,o.standard_labour_cost,o.labour_cost_complete,o.actual_labour_minutes,o.actual_labour_cost,o.actual_labour_cost_complete FROM stock_production_orders o JOIN stock_recipes r ON r.restaurant_id=o.restaurant_id AND r.id=o.recipe_id WHERE o.restaurant_id=? AND o.status='CONFIRMED' ORDER BY o.produced_at DESC,o.id DESC LIMIT 100`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading production orders")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var name string
		var produced any
		var batches, standard float64
		var standardComplete, actualMinutes, actualComplete int
		var actual sql.NullFloat64
		if err = rows.Scan(&id, &name, &produced, &batches, &standard, &standardComplete, &actualMinutes, &actual, &actualComplete); err != nil {
			httpx.WriteError(w, 500, "Error reading production orders")
			return
		}
		items = append(items, map[string]any{"id": id, "recipeName": name, "producedAt": produced, "batches": batches, "standardLabourCost": standard, "standardCostComplete": standardComplete != 0, "actualMinutes": actualMinutes, "actualCost": func() any {
			if actual.Valid {
				return actual.Float64
			}
			return nil
		}(), "actualCostComplete": actualComplete != 0})
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "items": items})
}

func (s *Server) handleBOProductionLabourEntries(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	rows, err := s.db.QueryContext(r.Context(), `SELECT e.id,e.work_date,e.restaurant_member_id,TRIM(CONCAT(m.first_name,' ',m.last_name)),e.minutes_worked,COALESCE(SUM(a.minutes),0),e.minutes_worked-COALESCE(SUM(a.minutes),0) FROM member_time_entries e JOIN restaurant_members m ON m.restaurant_id=e.restaurant_id AND m.id=e.restaurant_member_id LEFT JOIN member_time_allocations a ON a.restaurant_id=e.restaurant_id AND a.time_entry_id=e.id WHERE e.restaurant_id=? AND e.end_time IS NOT NULL AND e.work_date>=DATE_SUB(CURDATE(),INTERVAL 31 DAY) GROUP BY e.id,e.work_date,e.restaurant_member_id,m.first_name,m.last_name,e.minutes_worked HAVING e.minutes_worked-COALESCE(SUM(a.minutes),0)>0 ORDER BY e.work_date DESC,e.id DESC LIMIT 200`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading fichaje entries")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var date, name string
		var memberID, total, allocated, remaining int
		if err = rows.Scan(&id, &date, &memberID, &name, &total, &allocated, &remaining); err != nil {
			httpx.WriteError(w, 500, "Error reading fichaje entries")
			return
		}
		items = append(items, map[string]any{"id": id, "workDate": date, "memberId": memberID, "memberName": name, "minutesWorked": total, "allocatedMinutes": allocated, "remainingMinutes": remaining})
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "items": items})
}

func validateProductionLabourAllocation(entryMinutes, allocatedMinutes, newMinutes int) error {
	if newMinutes <= 0 {
		return errors.New("minutes must be positive")
	}
	if allocatedMinutes+newMinutes > entryMinutes {
		return errors.New("allocated minutes exceed fichaje")
	}
	return nil
}

func (s *Server) refreshProductionActualLabour(ctx context.Context, tx *sql.Tx, restaurantID int, orderID int64) error {
	var minutes int
	var cost sql.NullFloat64
	var incomplete int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(minutes),0),SUM(actual_cost),COALESCE(MAX(cost_complete=0),0) FROM member_time_allocations WHERE restaurant_id=? AND production_order_id=?`, restaurantID, orderID).Scan(&minutes, &cost, &incomplete); err != nil {
		return err
	}
	complete := minutes > 0 && incomplete == 0
	_, err := tx.ExecContext(ctx, `UPDATE stock_production_orders SET actual_labour_minutes=?,actual_labour_cost=?,actual_labour_cost_complete=? WHERE restaurant_id=? AND id=?`, minutes, func() any {
		if complete {
			return cost.Float64
		}
		return nil
	}(), stockBoolInt(complete), restaurantID, orderID)
	return err
}

func (s *Server) handleBOProductionLabourList(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	orderID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	rows, err := s.db.QueryContext(r.Context(), `SELECT a.id,a.time_entry_id,a.minutes,a.hourly_cost_snapshot,a.actual_cost,a.cost_complete,e.work_date,e.restaurant_member_id,TRIM(CONCAT(m.first_name,' ',m.last_name)) FROM member_time_allocations a JOIN member_time_entries e ON e.restaurant_id=a.restaurant_id AND e.id=a.time_entry_id JOIN restaurant_members m ON m.restaurant_id=e.restaurant_id AND m.id=e.restaurant_member_id WHERE a.restaurant_id=? AND a.production_order_id=? ORDER BY e.work_date,a.id`, a.ActiveRestaurantID, orderID)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading production labour")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, timeEntryID int64
		var minutes, memberID, complete int
		var hourly, cost sql.NullFloat64
		var date, name string
		if err = rows.Scan(&id, &timeEntryID, &minutes, &hourly, &cost, &complete, &date, &memberID, &name); err != nil {
			httpx.WriteError(w, 500, "Error reading production labour")
			return
		}
		items = append(items, map[string]any{"id": id, "timeEntryId": timeEntryID, "minutes": minutes, "actualCost": func() any {
			if cost.Valid {
				return cost.Float64
			}
			return nil
		}(), "costComplete": complete != 0, "workDate": date, "memberId": memberID, "memberName": name})
		_ = hourly
	}
	var actualMinutes int
	var actualCost sql.NullFloat64
	var complete int
	if err = s.db.QueryRowContext(r.Context(), `SELECT actual_labour_minutes,actual_labour_cost,actual_labour_cost_complete FROM stock_production_orders WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, orderID).Scan(&actualMinutes, &actualCost, &complete); err != nil {
		httpx.WriteError(w, 404, "Production order not found")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "items": items, "actualMinutes": actualMinutes, "actualCost": func() any {
		if actualCost.Valid {
			return actualCost.Float64
		}
		return nil
	}(), "costComplete": complete != 0})
}

func (s *Server) handleBOProductionLabourCreate(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	orderID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		TimeEntryID    int64  `json:"timeEntryId"`
		Minutes        int    `json:"minutes"`
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || orderID <= 0 || in.TimeEntryID <= 0 || in.Minutes <= 0 || strings.TrimSpace(in.IdempotencyKey) == "" {
		httpx.WriteError(w, 400, "Invalid production labour allocation")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error allocating production labour")
		return
	}
	defer tx.Rollback()
	var entryMinutes int
	var memberID int
	var workDate string
	if err = tx.QueryRowContext(r.Context(), `SELECT minutes_worked,restaurant_member_id,work_date FROM member_time_entries WHERE restaurant_id=? AND id=? AND end_time IS NOT NULL FOR UPDATE`, a.ActiveRestaurantID, in.TimeEntryID).Scan(&entryMinutes, &memberID, &workDate); err != nil {
		httpx.WriteError(w, 404, "Closed fichaje entry not found")
		return
	}
	var order int
	if err = tx.QueryRowContext(r.Context(), `SELECT 1 FROM stock_production_orders WHERE restaurant_id=? AND id=? AND status='CONFIRMED' FOR UPDATE`, a.ActiveRestaurantID, orderID).Scan(&order); err != nil {
		httpx.WriteError(w, 404, "Production order not found")
		return
	}
	var allocated int
	if err = tx.QueryRowContext(r.Context(), `SELECT COALESCE(SUM(minutes),0) FROM member_time_allocations WHERE restaurant_id=? AND time_entry_id=?`, a.ActiveRestaurantID, in.TimeEntryID).Scan(&allocated); err != nil {
		httpx.WriteError(w, 500, "Error checking fichaje allocation")
		return
	}
	if err = validateProductionLabourAllocation(entryMinutes, allocated, in.Minutes); err != nil {
		httpx.WriteError(w, 409, err.Error())
		return
	}
	var payType string
	var gross, monthlyHours, burden sql.NullFloat64
	rateComplete := true
	err = tx.QueryRowContext(r.Context(), `SELECT pay_type,gross_amount,monthly_hours,employer_cost_pct FROM member_compensations WHERE restaurant_id=? AND restaurant_member_id=? AND deleted_at IS NULL AND ? BETWEEN effective_from AND COALESCE(effective_to,'9999-12-31') LIMIT 1`, a.ActiveRestaurantID, memberID, workDate).Scan(&payType, &gross, &monthlyHours, &burden)
	if errors.Is(err, sql.ErrNoRows) {
		rateComplete = false
	} else if err != nil {
		httpx.WriteError(w, 500, "Error loading compensation")
		return
	}
	var hourly, actual any
	if rateComplete {
		value, rateErr := effectiveHourlyCost(payType, gross.Float64, monthlyHours.Float64, burden.Float64)
		if rateErr != nil {
			rateComplete = false
		} else {
			hourly = value
			actual = math.Round(value*float64(in.Minutes)/60*10000) / 10000
		}
	}
	res, err := tx.ExecContext(r.Context(), `INSERT INTO member_time_allocations (restaurant_id,time_entry_id,production_order_id,minutes,hourly_cost_snapshot,actual_cost,cost_complete,idempotency_key,created_by) VALUES (?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, in.TimeEntryID, orderID, in.Minutes, hourly, actual, stockBoolInt(rateComplete), in.IdempotencyKey, a.User.ID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			tx.Rollback()
			httpx.WriteJSON(w, 200, map[string]any{"success": true, "duplicate": true})
			return
		}
		httpx.WriteError(w, 500, "Error allocating production labour")
		return
	}
	id, _ := res.LastInsertId()
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO member_time_allocation_audit (restaurant_id,allocation_id,action,snapshot,actor_user_id) VALUES (?,?,'CREATE',JSON_OBJECT('timeEntryId',?,'productionOrderId',?,'minutes',?,'costComplete',?),?)`, a.ActiveRestaurantID, id, in.TimeEntryID, orderID, in.Minutes, stockBoolInt(rateComplete), a.User.ID); err != nil {
		httpx.WriteError(w, 500, "Error auditing production labour")
		return
	}
	if err = s.refreshProductionActualLabour(r.Context(), tx, a.ActiveRestaurantID, orderID); err != nil {
		httpx.WriteError(w, 500, "Error refreshing production labour")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error allocating production labour")
		return
	}
	httpx.WriteJSON(w, 201, map[string]any{"success": true, "id": id, "costComplete": rateComplete})
}

func (s *Server) handleBOProductionLabourDelete(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	orderID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	allocationID, _ := strconv.ParseInt(chi.URLParam(r, "allocationId"), 10, 64)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error deleting production labour")
		return
	}
	defer tx.Rollback()
	var order int
	if err = tx.QueryRowContext(r.Context(), `SELECT 1 FROM stock_production_orders WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, orderID).Scan(&order); err != nil {
		httpx.WriteError(w, 404, "Production order not found")
		return
	}
	var snapshot string
	if err = tx.QueryRowContext(r.Context(), `SELECT JSON_OBJECT('timeEntryId',time_entry_id,'productionOrderId',production_order_id,'minutes',minutes,'costComplete',cost_complete) FROM member_time_allocations WHERE restaurant_id=? AND production_order_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, orderID, allocationID).Scan(&snapshot); err != nil {
		httpx.WriteError(w, 404, "Production labour allocation not found")
		return
	}
	res, err := tx.ExecContext(r.Context(), `DELETE FROM member_time_allocations WHERE restaurant_id=? AND production_order_id=? AND id=?`, a.ActiveRestaurantID, orderID, allocationID)
	if err != nil {
		httpx.WriteError(w, 500, "Error deleting production labour")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, 404, "Production labour allocation not found")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO member_time_allocation_audit (restaurant_id,allocation_id,action,snapshot,actor_user_id) VALUES (?,?,'DELETE',?,?)`, a.ActiveRestaurantID, allocationID, snapshot, a.User.ID); err != nil {
		httpx.WriteError(w, 500, "Error auditing production labour")
		return
	}
	if err = s.refreshProductionActualLabour(r.Context(), tx, a.ActiveRestaurantID, orderID); err != nil {
		httpx.WriteError(w, 500, "Error refreshing production labour")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error deleting production labour")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true})
}
