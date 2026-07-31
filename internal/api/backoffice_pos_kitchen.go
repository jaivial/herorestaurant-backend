package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

type posKitchenDelta struct {
	LineID   int64
	Action   string
	Quantity float64
}

func calculatePOSKitchenDeltas(current, sent map[int64]float64) []posKitchenDelta {
	ids := make([]int64, 0, len(current)+len(sent))
	seen := map[int64]bool{}
	for id := range current {
		seen[id] = true
		ids = append(ids, id)
	}
	for id := range sent {
		if !seen[id] {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := []posKitchenDelta{}
	for _, id := range ids {
		delta := current[id] - sent[id]
		if delta > 0 {
			out = append(out, posKitchenDelta{id, "ADD", delta})
		} else if delta < 0 {
			out = append(out, posKitchenDelta{id, "VOID", delta})
		}
	}
	return out
}

func validPOSKitchenTransition(from, to string) bool {
	allowed := map[string]map[string]bool{"PENDING": {"ACKNOWLEDGED": true, "PREPARING": true, "READY": true, "FAILED": true, "CANCELLED": true}, "ACKNOWLEDGED": {"PREPARING": true, "READY": true, "FAILED": true, "CANCELLED": true}, "PREPARING": {"READY": true, "FAILED": true, "CANCELLED": true}, "FAILED": {"PENDING": true, "CANCELLED": true}}
	return allowed[from][to]
}

func requiresPOSActivationAcceptance(from, to string) bool { return from != "LIVE" && to == "LIVE" }

func (s *Server) handleBOPOSKitchenStationsList(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,name,sort_order,is_active FROM pos_kitchen_stations WHERE restaurant_id=? ORDER BY sort_order,id`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading kitchen stations")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var name string
		var order, active int
		if err = rows.Scan(&id, &name, &order, &active); err != nil {
			httpx.WriteError(w, 500, "Error reading kitchen stations")
			return
		}
		items = append(items, map[string]any{"id": id, "name": name, "sortOrder": order, "isActive": active != 0})
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "items": items})
}

func (s *Server) saveBOPOSKitchenStation(w http.ResponseWriter, r *http.Request, id int64) {
	a, _ := boAuthFromContext(r.Context())
	var in struct {
		Name      string `json:"name"`
		SortOrder int    `json:"sortOrder"`
		IsActive  bool   `json:"isActive"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" {
		httpx.WriteError(w, 400, "Invalid kitchen station")
		return
	}
	if id == 0 {
		res, err := s.db.ExecContext(r.Context(), `INSERT INTO pos_kitchen_stations (restaurant_id,name,sort_order,is_active) VALUES (?,?,?,?)`, a.ActiveRestaurantID, strings.TrimSpace(in.Name), in.SortOrder, stockBoolInt(in.IsActive))
		if err != nil {
			httpx.WriteError(w, 400, "Kitchen station could not be created")
			return
		}
		id, _ = res.LastInsertId()
		httpx.WriteJSON(w, 201, map[string]any{"success": true, "id": id})
		return
	}
	res, err := s.db.ExecContext(r.Context(), `UPDATE pos_kitchen_stations SET name=?,sort_order=?,is_active=? WHERE restaurant_id=? AND id=?`, strings.TrimSpace(in.Name), in.SortOrder, stockBoolInt(in.IsActive), a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteError(w, 400, "Kitchen station could not be updated")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, 404, "Kitchen station not found")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true})
}
func (s *Server) handleBOPOSKitchenStationCreate(w http.ResponseWriter, r *http.Request) {
	s.saveBOPOSKitchenStation(w, r, 0)
}
func (s *Server) handleBOPOSKitchenStationPatch(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	s.saveBOPOSKitchenStation(w, r, id)
}

func (s *Server) handleBOPOSKitchenRoutesList(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	rows, err := s.db.QueryContext(r.Context(), `SELECT r.id,r.station_id,s.name,r.pos_product_id,r.category_id,COALESCE(p.name,''),COALESCE(c.name,''),r.priority,r.is_active FROM pos_kitchen_routes r JOIN pos_kitchen_stations s ON s.restaurant_id=r.restaurant_id AND s.id=r.station_id LEFT JOIN pos_products p ON p.restaurant_id=r.restaurant_id AND p.id=r.pos_product_id LEFT JOIN pos_product_categories c ON c.restaurant_id=r.restaurant_id AND c.id=r.category_id WHERE r.restaurant_id=? ORDER BY s.sort_order,r.priority,r.id`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading kitchen routes")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, station int64
		var product, category sql.NullInt64
		var stationName, productName, categoryName string
		var priority, active int
		if err = rows.Scan(&id, &station, &stationName, &product, &category, &productName, &categoryName, &priority, &active); err != nil {
			httpx.WriteError(w, 500, "Error reading kitchen routes")
			return
		}
		items = append(items, map[string]any{"id": id, "stationId": station, "stationName": stationName, "productId": stockNullableDBInt(product), "categoryId": stockNullableDBInt(category), "productName": productName, "categoryName": categoryName, "priority": priority, "isActive": active != 0})
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "items": items})
}

func (s *Server) handleBOPOSKitchenRouteCreate(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var in struct {
		StationID  int64  `json:"stationId"`
		ProductID  *int64 `json:"productId"`
		CategoryID *int64 `json:"categoryId"`
		Priority   int    `json:"priority"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || in.StationID <= 0 || (in.ProductID == nil) == (in.CategoryID == nil) {
		httpx.WriteError(w, 400, "Invalid kitchen route")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO pos_kitchen_routes (restaurant_id,station_id,pos_product_id,category_id,priority) SELECT ?,?,?,?,? FROM pos_kitchen_stations WHERE restaurant_id=? AND id=? AND is_active=1`, a.ActiveRestaurantID, in.StationID, in.ProductID, in.CategoryID, in.Priority, a.ActiveRestaurantID, in.StationID)
	if err != nil {
		httpx.WriteError(w, 400, "Kitchen route could not be created")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, 404, "Kitchen station not found")
		return
	}
	id, _ := res.LastInsertId()
	httpx.WriteJSON(w, 201, map[string]any{"success": true, "id": id})
}
func (s *Server) handleBOPOSKitchenRouteDelete(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	res, err := s.db.ExecContext(r.Context(), `DELETE FROM pos_kitchen_routes WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteError(w, 500, "Error deleting kitchen route")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, 404, "Kitchen route not found")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true})
}

type posKitchenLineSnapshot struct {
	LineID, ProductID int64
	CategoryID        sql.NullInt64
	Name, Notes       string
	Quantity          float64
}

func (s *Server) handleBOPOSKitchenDispatchCreate(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if ticketID <= 0 || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.IdempotencyKey) == "" {
		httpx.WriteError(w, 400, "Invalid kitchen dispatch")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error creating kitchen dispatch")
		return
	}
	defer tx.Rollback()
	var visitID int64
	var status string
	if err = tx.QueryRowContext(r.Context(), `SELECT visit_id,status FROM pos_tickets WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, ticketID).Scan(&visitID, &status); err != nil || status != "OPEN" {
		httpx.WriteError(w, 409, "Ticket is not open")
		return
	}
	rows, err := tx.QueryContext(r.Context(), `SELECT l.id,l.pos_product_id,p.category_id,l.product_name_snapshot,l.quantity,COALESCE(l.notes,'') FROM pos_ticket_lines l JOIN pos_products p ON p.restaurant_id=l.restaurant_id AND p.id=l.pos_product_id WHERE l.restaurant_id=? AND l.ticket_id=? AND l.status='ACTIVE' ORDER BY l.id`, a.ActiveRestaurantID, ticketID)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading kitchen lines")
		return
	}
	lines := []posKitchenLineSnapshot{}
	for rows.Next() {
		var line posKitchenLineSnapshot
		if err = rows.Scan(&line.LineID, &line.ProductID, &line.CategoryID, &line.Name, &line.Quantity, &line.Notes); err != nil {
			rows.Close()
			httpx.WriteError(w, 500, "Error reading kitchen lines")
			return
		}
		lines = append(lines, line)
	}
	rows.Close()
	if len(lines) == 0 {
		httpx.WriteError(w, 409, "Ticket has no kitchen lines")
		return
	}
	stations := map[int64][]posKitchenLineSnapshot{}
	for _, line := range lines {
		routeRows, qErr := tx.QueryContext(r.Context(), `SELECT DISTINCT station_id FROM pos_kitchen_routes WHERE restaurant_id=? AND is_active=1 AND (pos_product_id=? OR (pos_product_id IS NULL AND category_id=?)) ORDER BY priority,id`, a.ActiveRestaurantID, line.ProductID, line.CategoryID)
		if qErr != nil {
			httpx.WriteError(w, 500, "Error routing kitchen lines")
			return
		}
		for routeRows.Next() {
			var stationID int64
			if qErr = routeRows.Scan(&stationID); qErr != nil {
				routeRows.Close()
				httpx.WriteError(w, 500, "Error reading kitchen route")
				return
			}
			stations[stationID] = append(stations[stationID], line)
		}
		routeRows.Close()
	}
	priorStations, priorErr := tx.QueryContext(r.Context(), `SELECT DISTINCT station_id FROM pos_kitchen_dispatches WHERE restaurant_id=? AND ticket_id=?`, a.ActiveRestaurantID, ticketID)
	if priorErr != nil {
		httpx.WriteError(w, 500, "Error loading dispatch history")
		return
	}
	for priorStations.Next() {
		var stationID int64
		if priorErr = priorStations.Scan(&stationID); priorErr != nil {
			priorStations.Close()
			httpx.WriteError(w, 500, "Error reading dispatch history")
			return
		}
		if _, ok := stations[stationID]; !ok {
			stations[stationID] = nil
		}
	}
	priorStations.Close()
	if len(stations) == 0 {
		httpx.WriteJSON(w, 409, map[string]any{"success": false, "message": "No kitchen routes configured", "code": "KITCHEN_ROUTES_MISSING"})
		return
	}
	created := []int64{}
	for stationID, stationLines := range stations {
		current := map[int64]float64{}
		sent := map[int64]float64{}
		lineByID := map[int64]posKitchenLineSnapshot{}
		for _, line := range stationLines {
			current[line.LineID] = line.Quantity
			lineByID[line.LineID] = line
		}
		sentRows, qErr := tx.QueryContext(r.Context(), `SELECT dl.ticket_line_id,COALESCE(SUM(dl.quantity_delta),0) FROM pos_kitchen_dispatch_lines dl JOIN pos_kitchen_dispatches d ON d.restaurant_id=dl.restaurant_id AND d.id=dl.dispatch_id WHERE d.restaurant_id=? AND d.ticket_id=? AND d.station_id=? AND d.status<>'CANCELLED' GROUP BY dl.ticket_line_id`, a.ActiveRestaurantID, ticketID, stationID)
		if qErr != nil {
			httpx.WriteError(w, 500, "Error loading dispatch history")
			return
		}
		for sentRows.Next() {
			var id int64
			var qty float64
			if qErr = sentRows.Scan(&id, &qty); qErr != nil {
				sentRows.Close()
				httpx.WriteError(w, 500, "Error reading dispatch history")
				return
			}
			sent[id] = qty
		}
		sentRows.Close()
		deltas := calculatePOSKitchenDeltas(current, sent)
		if len(deltas) == 0 {
			continue
		}
		var sequence int
		if err = tx.QueryRowContext(r.Context(), `SELECT COALESCE(MAX(sequence_no),0)+1 FROM pos_kitchen_dispatches WHERE restaurant_id=? AND station_id=? AND ticket_id=?`, a.ActiveRestaurantID, stationID, ticketID).Scan(&sequence); err != nil {
			httpx.WriteError(w, 500, "Error sequencing dispatch")
			return
		}
		raw, _ := json.Marshal(deltas)
		hash := sha256.Sum256(raw)
		command := strings.TrimSpace(in.IdempotencyKey) + ":" + strconv.FormatInt(stationID, 10)
		res, insertErr := tx.ExecContext(r.Context(), `INSERT INTO pos_kitchen_dispatches (restaurant_id,station_id,visit_id,ticket_id,sequence_no,command_key,payload_hash,created_by) VALUES (?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, stationID, visitID, ticketID, sequence, command, hex.EncodeToString(hash[:]), a.User.ID)
		if insertErr != nil {
			if strings.Contains(strings.ToLower(insertErr.Error()), "duplicate") {
				var existing int64
				if err = tx.QueryRowContext(r.Context(), `SELECT id FROM pos_kitchen_dispatches WHERE restaurant_id=? AND station_id=? AND command_key=?`, a.ActiveRestaurantID, stationID, command).Scan(&existing); err == nil {
					created = append(created, existing)
					continue
				}
				httpx.WriteJSON(w, 409, map[string]any{"success": false, "message": "Kitchen dispatch changed concurrently; retry", "code": "KITCHEN_DISPATCH_CONFLICT"})
				return
			}
			httpx.WriteError(w, 500, "Kitchen dispatch could not be created")
			return
		}
		dispatchID, _ := res.LastInsertId()
		for _, delta := range deltas {
			line, ok := lineByID[delta.LineID]
			if !ok {
				if err = tx.QueryRowContext(r.Context(), `SELECT product_name_snapshot,COALESCE(void_reason,'') FROM pos_ticket_lines WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, delta.LineID).Scan(&line.Name, &line.Notes); err != nil {
					httpx.WriteError(w, 500, "Error loading voided kitchen line")
					return
				}
				line.LineID = delta.LineID
			}
			if _, err = tx.ExecContext(r.Context(), `INSERT INTO pos_kitchen_dispatch_lines (restaurant_id,dispatch_id,ticket_line_id,action,quantity_delta,product_name_snapshot,notes_snapshot) VALUES (?,?,?,?,?,?,?)`, a.ActiveRestaurantID, dispatchID, delta.LineID, delta.Action, delta.Quantity, line.Name, stockNullableString(line.Notes)); err != nil {
				httpx.WriteError(w, 500, "Kitchen dispatch line could not be created")
				return
			}
		}
		created = append(created, dispatchID)
	}
	if len(created) == 0 {
		httpx.WriteJSON(w, 200, map[string]any{"success": true, "duplicate": true, "dispatchIds": created})
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error creating kitchen dispatch")
		return
	}
	httpx.WriteJSON(w, 201, map[string]any{"success": true, "dispatchIds": created})
}

func (s *Server) handleBOPOSKitchenQueue(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	stationID, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("stationId")), 10, 64)
	args := []any{a.ActiveRestaurantID}
	where := "d.restaurant_id=? AND d.status IN ('PENDING','ACKNOWLEDGED','PREPARING','FAILED')"
	if stationID > 0 {
		where += " AND d.station_id=?"
		args = append(args, stationID)
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT d.id,d.station_id,s.name,d.ticket_id,t.ticket_number,COALESCE(rt.name,''),d.status,d.created_at FROM pos_kitchen_dispatches d JOIN pos_kitchen_stations s ON s.restaurant_id=d.restaurant_id AND s.id=d.station_id JOIN pos_tickets t ON t.restaurant_id=d.restaurant_id AND t.id=d.ticket_id JOIN pos_visits v ON v.restaurant_id=d.restaurant_id AND v.id=d.visit_id LEFT JOIN restaurant_tables rt ON rt.restaurant_id=v.restaurant_id AND rt.id=v.table_id WHERE `+where+` ORDER BY d.created_at,d.id`, args...)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading kitchen queue")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, station, ticket int64
		var stationName, number, table, status string
		var created any
		if err = rows.Scan(&id, &station, &stationName, &ticket, &number, &table, &status, &created); err != nil {
			httpx.WriteError(w, 500, "Error reading kitchen queue")
			return
		}
		lineRows, qErr := s.db.QueryContext(r.Context(), `SELECT id,product_name_snapshot,quantity_delta,action,COALESCE(notes_snapshot,'') FROM pos_kitchen_dispatch_lines WHERE restaurant_id=? AND dispatch_id=? ORDER BY id`, a.ActiveRestaurantID, id)
		if qErr != nil {
			httpx.WriteError(w, 500, "Error loading kitchen dispatch lines")
			return
		}
		lines := []map[string]any{}
		for lineRows.Next() {
			var lineID int64
			var name, action, notes string
			var quantity float64
			if qErr = lineRows.Scan(&lineID, &name, &quantity, &action, &notes); qErr != nil {
				lineRows.Close()
				httpx.WriteError(w, 500, "Error reading kitchen dispatch lines")
				return
			}
			lines = append(lines, map[string]any{"id": lineID, "productName": name, "quantity": quantity, "action": action, "notes": notes})
		}
		lineRows.Close()
		items = append(items, map[string]any{"id": id, "stationId": station, "stationName": stationName, "ticketId": ticket, "ticketNumber": number, "tableName": table, "status": status, "createdAt": created, "lines": lines})
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "items": items})
}

func (s *Server) handleBOPOSKitchenDispatchStatus(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		Status string `json:"status"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		httpx.WriteError(w, 400, "Invalid kitchen status")
		return
	}
	in.Status = strings.ToUpper(strings.TrimSpace(in.Status))
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error updating kitchen dispatch")
		return
	}
	defer tx.Rollback()
	var current string
	if err = tx.QueryRowContext(r.Context(), `SELECT status FROM pos_kitchen_dispatches WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, id).Scan(&current); errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, 404, "Kitchen dispatch not found")
		return
	} else if err != nil {
		httpx.WriteError(w, 500, "Error loading kitchen dispatch")
		return
	}
	if current == in.Status {
		httpx.WriteJSON(w, 200, map[string]any{"success": true, "duplicate": true})
		return
	}
	if !validPOSKitchenTransition(current, in.Status) {
		httpx.WriteJSON(w, 409, map[string]any{"success": false, "message": "Invalid kitchen status transition", "code": "KITCHEN_STATUS_INVALID"})
		return
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE pos_kitchen_dispatches SET status=?,acknowledged_at=CASE WHEN ? IN ('ACKNOWLEDGED','PREPARING','READY') AND acknowledged_at IS NULL THEN NOW() ELSE acknowledged_at END,ready_at=CASE WHEN ?='READY' THEN NOW() ELSE ready_at END,last_error=CASE WHEN ?<>'FAILED' THEN NULL ELSE last_error END WHERE restaurant_id=? AND id=?`, in.Status, in.Status, in.Status, in.Status, a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteError(w, 500, "Error updating kitchen dispatch")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error updating kitchen dispatch")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true})
}

func (s *Server) loadPOSActivationReadiness(ctx context.Context, restaurantID int) (map[string]any, error) {
	var active, mapped, invalid, periods, openExceptions, stockDiffs, coverDiffs int
	queries := []struct {
		query string
		args  []any
		scan  []any
	}{{`SELECT COUNT(*),COALESCE(SUM(EXISTS(SELECT 1 FROM pos_product_stock_rules r WHERE r.restaurant_id=p.restaurant_id AND r.pos_product_id=p.id AND r.is_active=1)),0),COALESCE(SUM(EXISTS(SELECT 1 FROM pos_product_stock_rules r JOIN stock_items i ON i.restaurant_id=r.restaurant_id AND i.id=r.stock_item_id JOIN stock_warehouses sw ON sw.restaurant_id=r.restaurant_id AND sw.id=r.warehouse_id WHERE r.restaurant_id=p.restaurant_id AND r.pos_product_id=p.id AND r.is_active=1 AND (i.is_active=0 OR i.deleted_at IS NOT NULL OR i.deduction_source='PRODUCTION' OR sw.is_active=0 OR sw.deleted_at IS NOT NULL))),0) FROM pos_products p WHERE p.restaurant_id=? AND p.is_active=1 AND p.deleted_at IS NULL`, []any{restaurantID}, []any{&active, &mapped, &invalid}}, {`SELECT COUNT(*) FROM pos_service_periods WHERE restaurant_id=? AND is_active=1`, []any{restaurantID}, []any{&periods}}, {`SELECT COUNT(*) FROM pos_stock_exceptions WHERE restaurant_id=? AND status='OPEN'`, []any{restaurantID}, []any{&openExceptions}}, {`SELECT COUNT(*) FROM (SELECT l.stock_item_id,l.warehouse_id FROM stock_levels l LEFT JOIN (SELECT restaurant_id,stock_item_id,warehouse_id,SUM(qty_base) qty FROM stock_movements WHERE restaurant_id=? GROUP BY restaurant_id,stock_item_id,warehouse_id) m ON m.restaurant_id=l.restaurant_id AND m.stock_item_id=l.stock_item_id AND m.warehouse_id=l.warehouse_id WHERE l.restaurant_id=? AND ABS(l.qty_base-COALESCE(m.qty,0))>0.0001) d`, []any{restaurantID, restaurantID}, []any{&stockDiffs}}, {`SELECT COUNT(*) FROM (SELECT v.service_date,v.service_type,SUM(CASE WHEN v.status='CLOSED' AND v.channel='DINE_IN' THEN v.covers ELSE 0 END)+COALESCE((SELECT SUM(c.delta_covers) FROM pos_cover_adjustments c WHERE c.restaurant_id=v.restaurant_id AND c.service_date=v.service_date AND c.service_type=v.service_type),0) expected,COALESCE(a.covers,0) actual FROM pos_visits v LEFT JOIN stock_affluence_daily a ON a.restaurant_id=v.restaurant_id AND a.service_date=v.service_date AND a.service_type=v.service_type WHERE v.restaurant_id=? GROUP BY v.restaurant_id,v.service_date,v.service_type,a.covers HAVING expected<>actual) d`, []any{restaurantID}, []any{&coverDiffs}}}
	for _, query := range queries {
		if err := s.db.QueryRowContext(ctx, query.query, query.args...).Scan(query.scan...); err != nil {
			return nil, err
		}
	}
	return map[string]any{"success": true, "activeProducts": active, "mappedProducts": mapped, "unmappedProducts": active - mapped, "invalidMappings": invalid, "activeServicePeriods": periods, "openStockExceptions": openExceptions, "stockLedgerDifferences": stockDiffs, "coverDifferences": coverDiffs, "stockLiveReady": active > 0 && active == mapped && invalid == 0 && openExceptions == 0 && stockDiffs == 0, "coversLiveReady": periods > 0 && coverDiffs == 0}, nil
}
func (s *Server) handleBOPOSActivationReadiness(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	out, err := s.loadPOSActivationReadiness(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, 500, "Error checking POS readiness")
		return
	}
	httpx.WriteJSON(w, 200, out)
}

func (s *Server) handleBOPOSActivationAccept(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var in struct {
		Type         string `json:"type"`
		EvidenceNote string `json:"evidenceNote"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.EvidenceNote) == "" {
		httpx.WriteError(w, 400, "Invalid activation acceptance")
		return
	}
	in.Type = strings.ToUpper(strings.TrimSpace(in.Type))
	if !validPOSMode(in.Type, "STOCK_LIVE", "COVERS_LIVE") {
		httpx.WriteError(w, 400, "Invalid activation acceptance")
		return
	}
	readiness, err := s.loadPOSActivationReadiness(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, 500, "Error checking POS readiness")
		return
	}
	ready, _ := readiness[map[string]string{"STOCK_LIVE": "stockLiveReady", "COVERS_LIVE": "coversLiveReady"}[in.Type]].(bool)
	if !ready {
		httpx.WriteJSON(w, 409, map[string]any{"success": false, "message": "POS readiness checks failed", "code": "POS_ACTIVATION_NOT_READY"})
		return
	}
	snapshot, _ := json.Marshal(readiness)
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO pos_activation_acceptances (restaurant_id,acceptance_type,evidence_note,snapshot_json,accepted_by) VALUES (?,?,?,?,?)`, a.ActiveRestaurantID, in.Type, strings.TrimSpace(in.EvidenceNote), string(snapshot), a.User.ID)
	if err != nil {
		httpx.WriteError(w, 500, "Error saving activation acceptance")
		return
	}
	id, _ := res.LastInsertId()
	httpx.WriteJSON(w, 201, map[string]any{"success": true, "id": id})
}
