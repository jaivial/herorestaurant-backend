package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

func posReportRange(r *http.Request) (string, string, bool) {
	from := strings.TrimSpace(r.URL.Query().Get("from"))
	to := strings.TrimSpace(r.URL.Query().Get("to"))
	if from == "" {
		from = time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	}
	if to == "" {
		to = time.Now().Format("2006-01-02")
	}
	_, e1 := time.Parse("2006-01-02", from)
	_, e2 := time.Parse("2006-01-02", to)
	return from, to, e1 == nil && e2 == nil && from <= to
}

func (s *Server) handleBOPOSSalesReport(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	from, to, ok := posReportRange(r)
	if !ok {
		httpx.WriteError(w, 400, "Invalid report range")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT v.service_date,v.service_type,COUNT(DISTINCT t.id),COALESCE(SUM(t.total_gross_cents),0),COALESCE(SUM(t.refunded_cents),0),COALESCE((SELECT SUM(v2.covers) FROM pos_visits v2 WHERE v2.restaurant_id=v.restaurant_id AND v2.service_date=v.service_date AND v2.service_type=v.service_type AND v2.channel='DINE_IN' AND v2.status='CLOSED'),0) FROM pos_tickets t JOIN pos_visits v ON v.restaurant_id=t.restaurant_id AND v.id=t.visit_id WHERE t.restaurant_id=? AND v.service_date BETWEEN ? AND ? AND t.status IN ('PAID','PARTIALLY_REFUNDED','REFUNDED') GROUP BY v.restaurant_id,v.service_date,v.service_type ORDER BY v.service_date DESC,v.service_type`, a.ActiveRestaurantID, from, to)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading sales report")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var date, service string
		var tickets, covers int
		var gross, refund int64
		if err = rows.Scan(&date, &service, &tickets, &gross, &refund, &covers); err != nil {
			httpx.WriteError(w, 500, "Error reading sales report")
			return
		}
		items = append(items, map[string]any{"date": normalizePOSDate(date), "serviceType": service, "tickets": tickets, "grossCents": gross, "refundedCents": refund, "netCents": gross - refund, "covers": covers})
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "from": from, "to": to, "items": items})
}
func (s *Server) handleBOPOSCardReconciliation(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	from, to, ok := posReportRange(r)
	if !ok {
		httpx.WriteError(w, 400, "Invalid report range")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT v.service_date,COUNT(*),COALESCE(SUM(p.amount_cents),0),SUM(CASE WHEN p.provider_reference IS NOT NULL AND p.provider_reference<>'' THEN 1 ELSE 0 END),COUNT(DISTINCT p.provider_reference) FROM pos_payments p JOIN pos_tickets t ON t.restaurant_id=p.restaurant_id AND t.id=p.ticket_id JOIN pos_visits v ON v.restaurant_id=t.restaurant_id AND v.id=t.visit_id WHERE p.restaurant_id=? AND p.method='CARD' AND p.status='CAPTURED' AND v.service_date BETWEEN ? AND ? GROUP BY v.service_date ORDER BY v.service_date DESC`, a.ActiveRestaurantID, from, to)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading card reconciliation")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var date string
		var payments, referenced, uniqueReferences int
		var amount int64
		if err = rows.Scan(&date, &payments, &amount, &referenced, &uniqueReferences); err != nil {
			httpx.WriteError(w, 500, "Error reading card reconciliation")
			return
		}
		items = append(items, map[string]any{"date": normalizePOSDate(date), "payments": payments, "amountCents": amount, "referencedPayments": referenced, "uniqueReferences": uniqueReferences, "referencesComplete": referenced == payments, "referencesUnique": uniqueReferences == payments})
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "from": from, "to": to, "items": items})
}

func (s *Server) handleBOPOSStockReport(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	from, to, ok := posReportRange(r)
	if !ok {
		httpx.WriteError(w, 400, "Invalid report range")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT s.status,COUNT(*),COALESCE(SUM(s.qty_base_planned),0) FROM pos_ticket_line_stock s JOIN pos_tickets t ON t.restaurant_id=s.restaurant_id AND t.id=s.ticket_id WHERE s.restaurant_id=? AND DATE(t.paid_at) BETWEEN ? AND ? GROUP BY s.status ORDER BY s.status`, a.ActiveRestaurantID, from, to)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading POS stock report")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var status string
		var count int
		var qty float64
		if err = rows.Scan(&status, &count, &qty); err != nil {
			httpx.WriteError(w, 500, "Error reading POS stock report")
			return
		}
		items = append(items, map[string]any{"status": status, "snapshots": count, "quantityBase": qty})
	}
	var exceptions int
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM pos_stock_exceptions WHERE restaurant_id=? AND status='OPEN'`, a.ActiveRestaurantID).Scan(&exceptions)
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "from": from, "to": to, "openExceptions": exceptions, "items": items})
}

func (s *Server) handleBOPOSCoversReconciliation(w http.ResponseWriter, r *http.Request) {
	s.handleBOPOSCoversReport(w, r)
}
func (s *Server) handleBOPOSCoversRebuild(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	from, to, ok := posReportRange(r)
	if !ok {
		httpx.WriteError(w, 400, "Invalid report range")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error rebuilding covers")
		return
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(r.Context(), `SELECT DISTINCT service_date,service_type FROM pos_visits WHERE restaurant_id=? AND service_date BETWEEN ? AND ? UNION SELECT DISTINCT service_date,service_type FROM pos_cover_adjustments WHERE restaurant_id=? AND service_date BETWEEN ? AND ?`, a.ActiveRestaurantID, from, to, a.ActiveRestaurantID, from, to)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading cover keys")
		return
	}
	type key struct{ date, service string }
	keys := []key{}
	for rows.Next() {
		var k key
		if err = rows.Scan(&k.date, &k.service); err != nil {
			rows.Close()
			httpx.WriteError(w, 500, "Error reading cover keys")
			return
		}
		keys = append(keys, k)
	}
	rows.Close()
	for _, k := range keys {
		if _, err = s.rebuildPOSAffluenceKey(r.Context(), tx, a.ActiveRestaurantID, k.date, k.service); err != nil {
			httpx.WriteError(w, 500, "Error rebuilding covers")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error rebuilding covers")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "rebuilt": len(keys)})
}

func (s *Server) handleBOPOSStockExceptionsList(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	rows, err := s.db.QueryContext(r.Context(), `SELECT e.id,e.ticket_id,e.ticket_line_id,e.code,e.status,e.message,e.created_at,COALESCE(l.product_name_snapshot,'') FROM pos_stock_exceptions e LEFT JOIN pos_ticket_lines l ON l.restaurant_id=e.restaurant_id AND l.id=e.ticket_line_id WHERE e.restaurant_id=? ORDER BY e.status='OPEN' DESC,e.created_at DESC LIMIT 500`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading stock exceptions")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, ticketID int64
		var lineID sql.NullInt64
		var code, status, message, name string
		var created time.Time
		if err = rows.Scan(&id, &ticketID, &lineID, &code, &status, &message, &created, &name); err != nil {
			httpx.WriteError(w, 500, "Error reading stock exceptions")
			return
		}
		items = append(items, map[string]any{"id": id, "ticketId": ticketID, "ticketLineId": stockNullableDBInt(lineID), "code": code, "status": status, "message": message, "productName": name, "createdAt": created})
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "items": items})
}
func (s *Server) handleBOPOSStockExceptionsReplay(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	if !s.checkPOSRateLimit("replay", a.ActiveRestaurantID, a.User.ID, 10) {
		httpx.WriteError(w, http.StatusTooManyRequests, "Too many replay requests")
		return
	}
	var in struct {
		ExceptionIDs   []int64 `json:"exceptionIds"`
		IdempotencyKey string  `json:"idempotencyKey"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || len(in.ExceptionIDs) == 0 || len(in.ExceptionIDs) > 500 || strings.TrimSpace(in.IdempotencyKey) == "" {
		httpx.WriteError(w, 400, "Invalid replay")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error replaying stock")
		return
	}
	defer tx.Rollback()
	applied := 0
	for _, exceptionID := range in.ExceptionIDs {
		var ticketID, lineID, productID int64
		var quantity float64
		var status string
		if err = tx.QueryRowContext(r.Context(), `SELECT e.ticket_id,e.ticket_line_id,e.status,l.pos_product_id,l.quantity FROM pos_stock_exceptions e JOIN pos_ticket_lines l ON l.restaurant_id=e.restaurant_id AND l.id=e.ticket_line_id WHERE e.restaurant_id=? AND e.id=? FOR UPDATE`, a.ActiveRestaurantID, exceptionID).Scan(&ticketID, &lineID, &status, &productID, &quantity); err != nil || status != "OPEN" {
			continue
		}
		ruleRows, queryErr := tx.QueryContext(r.Context(), `SELECT r.id,r.stock_item_id,r.warehouse_id,r.qty_base_per_sale,i.is_tracked,i.deduction_source FROM pos_product_stock_rules r JOIN stock_items i ON i.restaurant_id=r.restaurant_id AND i.id=r.stock_item_id WHERE r.restaurant_id=? AND r.pos_product_id=? AND r.is_active=1 ORDER BY r.warehouse_id,r.stock_item_id`, a.ActiveRestaurantID, productID)
		if queryErr != nil {
			httpx.WriteError(w, 500, "Error loading replay mapping")
			return
		}
		candidates := []posStockSnapshot{}
		for ruleRows.Next() {
			var x posStockSnapshot
			var perSale float64
			var tracked int
			if err = ruleRows.Scan(&x.RuleID, &x.ItemID, &x.WarehouseID, &perSale, &tracked, &x.DeductionSource); err != nil {
				ruleRows.Close()
				httpx.WriteError(w, 500, "Error reading replay mapping")
				return
			}
			if x.DeductionSource == "PRODUCTION" {
				continue
			}
			x.LineID, x.QuantitySold, x.Tracked = lineID, quantity, tracked != 0
			x.QtyBase, _ = posStockPlannedQuantity(quantity, perSale)
			if !x.Tracked {
				x.Status = "SKIPPED_UNTRACKED"
			} else {
				x.Status = "APPLIED"
			}
			candidates = append(candidates, x)
		}
		ruleRows.Close()
		snapshots := []posStockSnapshot{}
		for _, x := range candidates {
			res, insertErr := tx.ExecContext(r.Context(), `INSERT IGNORE INTO pos_ticket_line_stock (restaurant_id,ticket_id,ticket_line_id,stock_rule_id,stock_item_id,warehouse_id,quantity_sold,qty_base_planned,status) VALUES (?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, ticketID, lineID, x.RuleID, x.ItemID, x.WarehouseID, x.QuantitySold, x.QtyBase, x.Status)
			if insertErr != nil {
				httpx.WriteError(w, 500, "Error saving replay snapshot")
				return
			}
			if n, _ := res.RowsAffected(); n > 0 {
				x.SnapshotID, _ = res.LastInsertId()
				snapshots = append(snapshots, x)
			}
		}
		if len(snapshots) == 0 {
			continue
		}
		if err = s.applyPOSTicketStock(r.Context(), tx, a.ActiveRestaurantID, a.User.ID, ticketID, snapshots); err != nil {
			httpx.WriteError(w, 500, "Error applying replay stock")
			return
		}
		_, err = tx.ExecContext(r.Context(), `UPDATE pos_stock_exceptions SET status='RESOLVED',resolved_by=?,resolved_at=NOW(),replay_idempotency_key=? WHERE restaurant_id=? AND id=?`, a.User.ID, in.IdempotencyKey+"-"+strconv.FormatInt(exceptionID, 10), a.ActiveRestaurantID, exceptionID)
		if err != nil {
			httpx.WriteError(w, 500, "Error resolving stock exception")
			return
		}
		var remaining int
		_ = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM pos_stock_exceptions WHERE restaurant_id=? AND ticket_id=? AND status='OPEN'`, a.ActiveRestaurantID, ticketID).Scan(&remaining)
		if remaining == 0 {
			_, _ = tx.ExecContext(r.Context(), `UPDATE pos_tickets SET stock_status='COMPLETE' WHERE restaurant_id=? AND id=? AND stock_status='PARTIAL'`, a.ActiveRestaurantID, ticketID)
		}
		applied++
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error replaying stock")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "resolved": applied})
}
