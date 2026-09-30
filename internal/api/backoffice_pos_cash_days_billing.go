package api

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

// posBillingMethods is the fixed order of tenders in the day billing summary,
// so the UI never has to guess which methods exist.
var posBillingMethods = []string{"CASH", "CARD", "BANK", "OTHER"}

// handleBOPOSCashDayBilling answers "how much has this day invoiced so far".
//
//   - totalCents: every table of the day, open or closed. Open tickets count at
//     their live total; paid tickets count net of refunds (same rule as the
//     day totals and the sales report). Voided tickets and cancelled visits
//     never count.
//   - closedCents / openCents: the part of that total already charged vs. still
//     sitting on open tickets.
//   - byMethod: how the closed part was charged, net of refunds per tender.
//     Tips are reported apart because they are not takings.
func (s *Server) handleBOPOSCashDayBilling(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	date, ok := posValidBusinessDate(chi.URLParam(r, "date"))
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid date")
		return
	}
	ctx := r.Context()
	restaurantID := a.ActiveRestaurantID

	rows, err := s.db.QueryContext(ctx, `
		SELECT v.table_id,COALESCE(rt.name,''),v.channel,
		       COALESCE(SUM(CASE WHEN t.status='OPEN' THEN t.total_gross_cents ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN t.status IN ('PAID','PARTIALLY_REFUNDED','REFUNDED') THEN t.total_gross_cents-t.refunded_cents ELSE 0 END),0),
		       COUNT(CASE WHEN t.status='OPEN' THEN 1 END),
		       COUNT(CASE WHEN t.status IN ('PAID','PARTIALLY_REFUNDED','REFUNDED') THEN 1 END),
		       MAX(CASE WHEN v.status='OPEN' THEN 1 ELSE 0 END)
		FROM pos_visits v
		LEFT JOIN restaurant_tables rt ON rt.restaurant_id=v.restaurant_id AND rt.id=v.table_id
		LEFT JOIN pos_tickets t ON t.restaurant_id=v.restaurant_id AND t.visit_id=v.id
		WHERE v.restaurant_id=? AND v.service_date=? AND v.status<>'CANCELLED'
		GROUP BY v.table_id,rt.name,v.channel,rt.display_order
		ORDER BY v.table_id IS NULL,COALESCE(rt.display_order,0),v.table_id,v.channel`, restaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading day billing")
		return
	}
	defer rows.Close()

	tables := []map[string]any{}
	var openCents, closedCents, openTickets, closedTickets, openTables int64
	for rows.Next() {
		var tableID sql.NullInt64
		var tableName, channel string
		var tableOpen, tableClosed, tableOpenTickets, tableClosedTickets int64
		var hasOpenVisit int
		if err = rows.Scan(&tableID, &tableName, &channel, &tableOpen, &tableClosed, &tableOpenTickets, &tableClosedTickets, &hasOpenVisit); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading day billing")
			return
		}
		openCents += tableOpen
		closedCents += tableClosed
		openTickets += tableOpenTickets
		closedTickets += tableClosedTickets
		if hasOpenVisit == 1 {
			openTables++
		}
		// A null tableId (barra / para llevar / delivery) is labelled by the UI
		// from its channel, so no user-facing string is invented here.
		tables = append(tables, map[string]any{
			"tableId": stockNullableDBInt(tableID), "tableName": tableName, "channel": channel,
			"open": hasOpenVisit == 1, "openCents": tableOpen, "closedCents": tableClosed,
			"totalCents": tableOpen + tableClosed,
		})
	}
	if err = rows.Err(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reading day billing")
		return
	}

	byMethod := map[string]int64{}
	for _, method := range posBillingMethods {
		byMethod[method] = 0
	}
	var tipsCents int64
	payRows, err := s.db.QueryContext(ctx, `
		SELECT p.method,COALESCE(SUM(p.amount_cents),0),COALESCE(SUM(p.tip_cents),0)
		FROM pos_payments p
		JOIN pos_tickets t ON t.restaurant_id=p.restaurant_id AND t.id=p.ticket_id
		JOIN pos_visits v ON v.restaurant_id=t.restaurant_id AND v.id=t.visit_id
		WHERE p.restaurant_id=? AND v.service_date=? AND p.status='CAPTURED'
		  AND t.status IN ('PAID','PARTIALLY_REFUNDED','REFUNDED')
		GROUP BY p.method`, restaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading day payments")
		return
	}
	defer payRows.Close()
	for payRows.Next() {
		var method string
		var amount, tips int64
		if err = payRows.Scan(&method, &amount, &tips); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading day payments")
			return
		}
		byMethod[posBillingMethodKey(method)] += amount
		tipsCents += tips
	}
	if err = payRows.Err(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reading day payments")
		return
	}

	refundRows, err := s.db.QueryContext(ctx, `
		SELECT r.payment_method,COALESCE(SUM(r.amount_cents),0)
		FROM pos_refunds r
		JOIN pos_tickets t ON t.restaurant_id=r.restaurant_id AND t.id=r.ticket_id
		JOIN pos_visits v ON v.restaurant_id=t.restaurant_id AND v.id=t.visit_id
		WHERE r.restaurant_id=? AND v.service_date=? AND r.status='COMPLETED'
		GROUP BY r.payment_method`, restaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading day refunds")
		return
	}
	defer refundRows.Close()
	for refundRows.Next() {
		var method string
		var amount int64
		if err = refundRows.Scan(&method, &amount); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading day refunds")
			return
		}
		byMethod[posBillingMethodKey(method)] -= amount
	}
	if err = refundRows.Err(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reading day refunds")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":       true,
		"date":          date,
		"totalCents":    openCents + closedCents,
		"closedCents":   closedCents,
		"openCents":     openCents,
		"openTickets":   openTickets,
		"closedTickets": closedTickets,
		"openTables":    openTables,
		"byMethod":      byMethod,
		"tipsCents":     tipsCents,
		"tables":        tables,
	})
}

// posBillingMethodKey folds any unknown tender into OTHER so the four buckets
// always add up to the closed amount.
func posBillingMethodKey(method string) string {
	switch method {
	case "CASH", "CARD", "BANK":
		return method
	default:
		return "OTHER"
	}
}
