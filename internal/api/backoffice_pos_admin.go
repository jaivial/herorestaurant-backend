package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleBOPOSTicketsList(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	status := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("status")))
	where := "t.restaurant_id=?"
	args := []any{a.ActiveRestaurantID}
	if status != "" {
		where += " AND t.status=?"
		args = append(args, status)
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT t.id,t.ticket_number,t.status,t.total_gross_cents,t.refunded_cents,t.stock_status,t.paid_at,v.id,v.table_id,COALESCE(rt.name,''),v.covers,v.service_date,v.service_type FROM pos_tickets t JOIN pos_visits v ON v.restaurant_id=t.restaurant_id AND v.id=t.visit_id LEFT JOIN restaurant_tables rt ON rt.restaurant_id=v.restaurant_id AND rt.id=v.table_id WHERE `+where+` ORDER BY COALESCE(t.paid_at,t.opened_at) DESC LIMIT 500`, args...)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading tickets")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, visitID int64
		var number, ticketStatus, stockStatus, tableName, date, service string
		var tableID sql.NullInt64
		var total, refunded int64
		var paidAt sql.NullTime
		var covers int
		if err = rows.Scan(&id, &number, &ticketStatus, &total, &refunded, &stockStatus, &paidAt, &visitID, &tableID, &tableName, &covers, &date, &service); err != nil {
			httpx.WriteError(w, 500, "Error reading tickets")
			return
		}
		items = append(items, map[string]any{"id": id, "ticketNumber": number, "status": ticketStatus, "totalGrossCents": total, "refundedCents": refunded, "stockStatus": stockStatus, "paidAt": func() any {
			if paidAt.Valid {
				return paidAt.Time
			}
			return nil
		}(), "visitId": visitID, "tableId": stockNullableDBInt(tableID), "tableName": tableName, "covers": covers, "serviceDate": date, "serviceType": service})
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "items": items})
}
func (s *Server) handleBOPOSTicketGet(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	ticket, err := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, id)
	if err == sql.ErrNoRows {
		httpx.WriteError(w, 404, "Ticket not found")
		return
	}
	if err != nil {
		httpx.WriteError(w, 500, "Error loading ticket")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "ticket": ticket})
}

func (s *Server) handleBOPOSProductGet(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var name, sku, description, sourceType string
	var categoryID, vatID, sourceID sql.NullInt64
	var price int64
	var active, version int
	err := s.db.QueryRowContext(r.Context(), `SELECT name,COALESCE(sku,''),COALESCE(description,''),source_type,source_id,category_id,price_gross_cents,vat_rate_id,is_active,version FROM pos_products WHERE restaurant_id=? AND id=? AND deleted_at IS NULL`, a.ActiveRestaurantID, id).Scan(&name, &sku, &description, &sourceType, &sourceID, &categoryID, &price, &vatID, &active, &version)
	if err == sql.ErrNoRows {
		httpx.WriteError(w, 404, "POS product not found")
		return
	}
	if err != nil {
		httpx.WriteError(w, 500, "Error loading POS product")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "product": map[string]any{"id": id, "name": name, "sku": sku, "description": description, "sourceType": sourceType, "sourceId": stockNullableDBInt(sourceID), "categoryId": stockNullableDBInt(categoryID), "priceGrossCents": price, "vatRateId": stockNullableDBInt(vatID), "isActive": active != 0, "version": version}})
}

func (s *Server) handleBOPOSExportSales(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	from, to, ok := posReportRange(r)
	if !ok {
		httpx.WriteError(w, 400, "Invalid report range")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT t.ticket_number,v.service_date,v.service_type,v.channel,COALESCE(rt.name,''),v.covers,t.status,t.total_gross_cents,t.refunded_cents,t.stock_status FROM pos_tickets t JOIN pos_visits v ON v.restaurant_id=t.restaurant_id AND v.id=t.visit_id LEFT JOIN restaurant_tables rt ON rt.restaurant_id=v.restaurant_id AND rt.id=v.table_id WHERE t.restaurant_id=? AND v.service_date BETWEEN ? AND ? ORDER BY v.service_date,t.id`, a.ActiveRestaurantID, from, to)
	if err != nil {
		httpx.WriteError(w, 500, "Error exporting sales")
		return
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="pos-sales-`+from+`-`+to+`.csv"`)
	_, _ = w.Write([]byte("ticket,date,service,channel,table,covers,status,total_cents,refunded_cents,stock_status\n"))
	for rows.Next() {
		var ticket, date, service, channel, table, status, stock string
		var covers int
		var total, refunded int64
		if err = rows.Scan(&ticket, &date, &service, &channel, &table, &covers, &status, &total, &refunded, &stock); err != nil {
			return
		}
		line := strings.Join([]string{csvCell(ticket), date, service, channel, csvCell(table), strconv.Itoa(covers), status, strconv.FormatInt(total, 10), strconv.FormatInt(refunded, 10), stock}, ",") + "\n"
		_, _ = w.Write([]byte(line))
	}
}
func csvCell(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }

func (s *Server) handleBOPOSHealth(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var openVisits, oldVisits, exceptions, partialTickets, negativeStockAnomalies, oldShifts int
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*),SUM(opened_at<DATE_SUB(NOW(),INTERVAL 12 HOUR)) FROM pos_visits WHERE restaurant_id=? AND status='OPEN'`, a.ActiveRestaurantID).Scan(&openVisits, &oldVisits)
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM pos_stock_exceptions WHERE restaurant_id=? AND status='OPEN'`, a.ActiveRestaurantID).Scan(&exceptions)
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM pos_tickets WHERE restaurant_id=? AND stock_status='PARTIAL'`, a.ActiveRestaurantID).Scan(&partialTickets)
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM pos_stock_anomalies WHERE restaurant_id=? AND status='OPEN'`, a.ActiveRestaurantID).Scan(&negativeStockAnomalies)
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM pos_shifts WHERE restaurant_id=? AND status='OPEN' AND opened_at<DATE_SUB(NOW(),INTERVAL 24 HOUR)`, a.ActiveRestaurantID).Scan(&oldShifts)
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "openVisits": openVisits, "oldOpenVisits": oldVisits, "openStockExceptions": exceptions, "partialStockTickets": partialTickets, "negativeStockAnomalies": negativeStockAnomalies, "oldOpenShifts": oldShifts, "checkedAt": time.Now().UTC()})
}

var posPermissionKeys = []string{posPermissionView, posPermissionSell, posPermissionVisitManage, posPermissionLineVoid, posPermissionDiscount, posPermissionCheckout, posPermissionRefund, posPermissionRestock, posPermissionShiftManage, posPermissionCatalog, posPermissionStockMapping, posPermissionCoversAdjust, posPermissionReports, posPermissionSettings, posPermissionKitchen}

func (s *Server) handleBOPOSRolePermissionsGet(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	role := normalizeBORole(chi.URLParam(r, "slug"))
	if role == "" {
		httpx.WriteError(w, 400, "Invalid role")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT permission_key,is_allowed FROM pos_role_permissions WHERE restaurant_id=? AND role_slug=?`, a.ActiveRestaurantID, role)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading POS permissions")
		return
	}
	defer rows.Close()
	overrides := map[string]bool{}
	for rows.Next() {
		var key string
		var allowed int
		if err = rows.Scan(&key, &allowed); err != nil {
			httpx.WriteError(w, 500, "Error reading POS permissions")
			return
		}
		overrides[key] = allowed != 0
	}
	items := []map[string]any{}
	for _, key := range posPermissionKeys {
		allowed, exists := overrides[key]
		if !exists {
			allowed = role == "root" || role == "admin"
		}
		items = append(items, map[string]any{"key": key, "allowed": allowed})
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "role": role, "items": items})
}
func (s *Server) handleBOPOSRolePermissionsPut(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	role := normalizeBORole(chi.URLParam(r, "slug"))
	var in struct {
		Permissions []string `json:"permissions"`
	}
	if role == "" || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		httpx.WriteError(w, 400, "Invalid POS permissions")
		return
	}
	selected := map[string]bool{}
	for _, key := range in.Permissions {
		selected[key] = true
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error saving POS permissions")
		return
	}
	defer tx.Rollback()
	for _, key := range posPermissionKeys {
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO pos_role_permissions (restaurant_id,role_slug,permission_key,is_allowed) VALUES (?,?,?,?) ON DUPLICATE KEY UPDATE is_allowed=VALUES(is_allowed)`, a.ActiveRestaurantID, role, key, stockBoolInt(selected[key])); err != nil {
			httpx.WriteError(w, 500, "Error saving POS permissions")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error saving POS permissions")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true})
}

func (s *Server) handleBOPOSStockAnomalyResolve(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	res, err := s.db.ExecContext(r.Context(), `UPDATE pos_stock_anomalies SET status='RESOLVED',resolved_by=?,resolved_at=NOW() WHERE restaurant_id=? AND id=? AND status='OPEN'`, a.User.ID, a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteError(w, 500, "Error resolving stock anomaly")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, 404, "Open stock anomaly not found")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true})
}
