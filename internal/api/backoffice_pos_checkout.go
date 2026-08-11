package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

type posCheckoutPayment struct {
	Method            string `json:"method"`
	AmountCents       int64  `json:"amountCents"`
	IdempotencyKey    string `json:"idempotencyKey"`
	Provider          string `json:"provider"`
	ProviderReference string `json:"providerReference"`
	CardLast4         string `json:"cardLast4"`
	// TipCents is money handed over on top of the sale. It is never part of the
	// payment-vs-total match, so net sales and VAT stay untouched.
	TipCents int64 `json:"tipCents"`
}

type posCheckoutInput struct {
	IdempotencyKey  string               `json:"idempotencyKey"`
	ExpectedVersion int                  `json:"expectedVersion"`
	Payments        []posCheckoutPayment `json:"payments"`
	CloseVisit      bool                 `json:"closeVisit"`
}

type posStockSnapshot struct {
	LineID, RuleID, ItemID, WarehouseID int64
	QuantitySold, QtyBase               float64
	Tracked                             bool
	DeductionSource                     string
	Status                              string
	SnapshotID                          int64
}

func validPOSPaymentMethod(value string) bool {
	return validPOSMode(strings.ToUpper(strings.TrimSpace(value)), "CASH", "CARD", "BANK", "OTHER")
}

func (s *Server) rebuildPOSAffluenceKey(ctx context.Context, tx *sql.Tx, restaurantID int, date, serviceType string) (int, error) {
	var covers, adjustments int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(covers),0) FROM pos_visits WHERE restaurant_id=? AND service_date=? AND service_type=? AND channel='DINE_IN' AND status='CLOSED'`, restaurantID, date, serviceType).Scan(&covers); err != nil {
		return 0, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(delta_covers),0) FROM pos_cover_adjustments WHERE restaurant_id=? AND service_date=? AND service_type=?`, restaurantID, date, serviceType).Scan(&adjustments); err != nil {
		return 0, err
	}
	covers = aggregatePOSCovers(nil, covers+adjustments)
	_, err := tx.ExecContext(ctx, `INSERT INTO stock_affluence_daily (restaurant_id,service_date,service_type,covers,source) VALUES (?,?,?,?,'POS') ON DUPLICATE KEY UPDATE covers=VALUES(covers),source='POS'`, restaurantID, date, serviceType, covers)
	return covers, err
}

func (s *Server) preparePOSTicketStock(ctx context.Context, tx *sql.Tx, restaurantID int, ticketID int64, stockMode string) ([]posStockSnapshot, bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT l.id,l.pos_product_id,l.quantity FROM pos_ticket_lines l WHERE l.restaurant_id=? AND l.ticket_id=? AND l.status='ACTIVE' ORDER BY l.id`, restaurantID, ticketID)
	if err != nil {
		return nil, false, err
	}
	type saleLine struct {
		ID        int64
		ProductID sql.NullInt64
		Quantity  float64
	}
	lines := []saleLine{}
	for rows.Next() {
		var line saleLine
		if err = rows.Scan(&line.ID, &line.ProductID, &line.Quantity); err != nil {
			rows.Close()
			return nil, false, err
		}
		lines = append(lines, line)
	}
	rows.Close()
	snapshots := []posStockSnapshot{}
	partial := false
	for _, line := range lines {
		// Check if this line already has real-time stock deductions applied
		alreadyDeducted, checkErr := s.hasRealtimeStockDeductions(ctx, tx, restaurantID, line.ID)
		if checkErr != nil {
			return nil, false, checkErr
		}
		if alreadyDeducted {
			// Line already has APPLIED stock records from real-time deduction
			// Load existing snapshots for the return value but don't create new ones
			existingRows, existingErr := tx.QueryContext(ctx, `
				SELECT id, stock_rule_id, stock_item_id, warehouse_id, quantity_sold, qty_base_planned, status
				FROM pos_ticket_line_stock
				WHERE restaurant_id = ? AND ticket_line_id = ? AND status = 'APPLIED'
			`, restaurantID, line.ID)
			if existingErr != nil {
				return nil, false, existingErr
			}
			for existingRows.Next() {
				var snapshot posStockSnapshot
				snapshot.LineID = line.ID
				if err = existingRows.Scan(&snapshot.SnapshotID, &snapshot.RuleID, &snapshot.ItemID, &snapshot.WarehouseID, &snapshot.QuantitySold, &snapshot.QtyBase, &snapshot.Status); err != nil {
					existingRows.Close()
					return nil, false, err
				}
				snapshot.Tracked = true
				snapshots = append(snapshots, snapshot)
			}
			existingRows.Close()
			continue
		}
		if !line.ProductID.Valid {
			partial = true
			_, err = tx.ExecContext(ctx, `INSERT INTO pos_stock_exceptions (restaurant_id,ticket_id,ticket_line_id,code,message) VALUES (?,?,?,'UNMAPPED_PRODUCT','Ticket line has no POS product')`, restaurantID, ticketID, line.ID)
			if err != nil {
				return nil, false, err
			}
			continue
		}
		ruleRows, queryErr := tx.QueryContext(ctx, `SELECT r.id,r.stock_item_id,r.warehouse_id,r.qty_base_per_sale,i.is_tracked,i.deduction_source FROM pos_product_stock_rules r JOIN stock_items i ON i.restaurant_id=r.restaurant_id AND i.id=r.stock_item_id JOIN stock_warehouses w ON w.restaurant_id=r.restaurant_id AND w.id=r.warehouse_id WHERE r.restaurant_id=? AND r.pos_product_id=? AND r.is_active=1 AND i.is_active=1 AND i.deleted_at IS NULL AND w.is_active=1 AND w.deleted_at IS NULL ORDER BY r.warehouse_id,r.stock_item_id,r.id`, restaurantID, line.ProductID.Int64)
		if queryErr != nil {
			return nil, false, queryErr
		}
		lineSnapshots := []posStockSnapshot{}
		for ruleRows.Next() {
			var snapshot posStockSnapshot
			var qtyPerSale float64
			var tracked int
			if err = ruleRows.Scan(&snapshot.RuleID, &snapshot.ItemID, &snapshot.WarehouseID, &qtyPerSale, &tracked, &snapshot.DeductionSource); err != nil {
				ruleRows.Close()
				return nil, false, err
			}
			snapshot.LineID, snapshot.QuantitySold, snapshot.Tracked = line.ID, line.Quantity, tracked != 0
			snapshot.QtyBase, err = posStockPlannedQuantity(line.Quantity, qtyPerSale)
			if err != nil {
				ruleRows.Close()
				return nil, false, err
			}
			switch {
			case snapshot.DeductionSource == "PRODUCTION":
				snapshot.Status, partial = "ERROR", true
			case !snapshot.Tracked:
				snapshot.Status = "SKIPPED_UNTRACKED"
			case stockMode == "SHADOW":
				snapshot.Status = "SHADOW"
			case stockMode == "LIVE":
				snapshot.Status = "APPLIED"
			default:
				snapshot.Status = "SHADOW"
			}
			lineSnapshots = append(lineSnapshots, snapshot)
		}
		ruleRows.Close()
		for _, snapshot := range lineSnapshots {
			res, insertErr := tx.ExecContext(ctx, `INSERT INTO pos_ticket_line_stock (restaurant_id,ticket_id,ticket_line_id,stock_rule_id,stock_item_id,warehouse_id,quantity_sold,qty_base_planned,status,error_code,error_message) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, restaurantID, ticketID, line.ID, snapshot.RuleID, snapshot.ItemID, snapshot.WarehouseID, snapshot.QuantitySold, snapshot.QtyBase, snapshot.Status, func() any {
				if snapshot.Status == "ERROR" {
					return "INVALID_MAPPING"
				}
				return nil
			}(), func() any {
				if snapshot.Status == "ERROR" {
					return "Production-only item cannot be deducted by POS"
				}
				return nil
			}())
			if insertErr != nil {
				return nil, false, insertErr
			}
			snapshot.SnapshotID, _ = res.LastInsertId()
			if snapshot.Status == "ERROR" {
				if _, insertErr = tx.ExecContext(ctx, `INSERT INTO pos_stock_exceptions (restaurant_id,ticket_id,ticket_line_id,code,message) VALUES (?,?,?,'INVALID_MAPPING','Production-only item cannot be deducted by POS')`, restaurantID, ticketID, line.ID); insertErr != nil {
					return nil, false, insertErr
				}
			}
			snapshots = append(snapshots, snapshot)
		}
		if len(lineSnapshots) == 0 {
			partial = true
			_, err = tx.ExecContext(ctx, `INSERT INTO pos_stock_exceptions (restaurant_id,ticket_id,ticket_line_id,code,message) VALUES (?,?,?,'UNMAPPED_PRODUCT','POS product has no stock mapping')`, restaurantID, ticketID, line.ID)
			if err != nil {
				return nil, false, err
			}
		}
	}
	return snapshots, partial, nil
}

func (s *Server) applyPOSTicketStock(ctx context.Context, tx *sql.Tx, restaurantID, actorID int, ticketID int64, snapshots []posStockSnapshot) error {
	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].WarehouseID == snapshots[j].WarehouseID {
			return snapshots[i].ItemID < snapshots[j].ItemID
		}
		return snapshots[i].WarehouseID < snapshots[j].WarehouseID
	})
	for _, snapshot := range snapshots {
		if snapshot.Status != "APPLIED" {
			continue
		}
		// Check if this snapshot was already applied (real-time deduction during order)
		var existingMovementID sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT sale_movement_id FROM pos_ticket_line_stock WHERE restaurant_id=? AND id=?`, restaurantID, snapshot.SnapshotID).Scan(&existingMovementID); err == nil && existingMovementID.Valid {
			// Stock was already deducted during real-time, skip
			continue
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO stock_levels (restaurant_id,stock_item_id,warehouse_id,qty_base) VALUES (?,?,?,0) ON DUPLICATE KEY UPDATE stock_item_id=VALUES(stock_item_id)`, restaurantID, snapshot.ItemID, snapshot.WarehouseID)
		if err != nil {
			return err
		}
		var current float64
		if err = tx.QueryRowContext(ctx, `SELECT qty_base FROM stock_levels WHERE restaurant_id=? AND stock_item_id=? AND warehouse_id=? FOR UPDATE`, restaurantID, snapshot.ItemID, snapshot.WarehouseID).Scan(&current); err != nil {
			return err
		}
		var unitID int64
		var factor float64
		if err = tx.QueryRowContext(ctx, `SELECT id,factor_to_base FROM stock_item_units WHERE restaurant_id=? AND stock_item_id=? AND is_default_display=1 LIMIT 1`, restaurantID, snapshot.ItemID).Scan(&unitID, &factor); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO stock_movements (restaurant_id,stock_item_id,warehouse_id,qty_base,type,entered_qty,entered_unit_id,ref_type,ref_id,idempotency_key,note,actor_user_id) VALUES (?,?,?,?,'SALE',?,?,'pos_ticket_line_stock',?,?,?,?)`, restaurantID, snapshot.ItemID, snapshot.WarehouseID, -snapshot.QtyBase, snapshot.QtyBase/factor, unitID, snapshot.SnapshotID, "pos-ticket:"+strconv.FormatInt(ticketID, 10)+":line-stock:"+strconv.FormatInt(snapshot.SnapshotID, 10)+":sale", "POS ticket sale", actorID)
		if err != nil {
			return err
		}
		movementID, _ := res.LastInsertId()
		if _, err = tx.ExecContext(ctx, `UPDATE stock_levels SET qty_base=qty_base-?,version=version+1 WHERE restaurant_id=? AND stock_item_id=? AND warehouse_id=?`, snapshot.QtyBase, restaurantID, snapshot.ItemID, snapshot.WarehouseID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE pos_ticket_line_stock SET sale_movement_id=?,applied_at=NOW() WHERE restaurant_id=? AND id=?`, movementID, restaurantID, snapshot.SnapshotID); err != nil {
			return err
		}
		quantityAfter := current - snapshot.QtyBase
		if quantityAfter < 0 {
			if _, err = tx.ExecContext(ctx, `INSERT IGNORE INTO pos_stock_anomalies (restaurant_id,ticket_id,ticket_line_stock_id,stock_item_id,warehouse_id,quantity_after_base) VALUES (?,?,?,?,?,?)`, restaurantID, ticketID, snapshot.SnapshotID, snapshot.ItemID, snapshot.WarehouseID, quantityAfter); err != nil {
				return err
			}
		}
	}
	return nil
}

// posCheckoutTxError carries the HTTP status (and optional machine code) that
// both checkout entry points surface, so the shared money path returns plain
// errors instead of writing to a ResponseWriter.
type posCheckoutTxError struct {
	status int
	code   string
	msg    string
}

func (e *posCheckoutTxError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return http.StatusText(e.status)
}

// writeCheckoutTxError maps a posCheckoutTxError back to its HTTP response.
// Plain errors become a generic 500 so an internal detail never leaks as 200.
func writeCheckoutTxError(w http.ResponseWriter, err error) {
	var ce *posCheckoutTxError
	if errors.As(err, &ce) {
		if ce.code != "" {
			httpx.WriteJSON(w, ce.status, map[string]any{"success": false, "message": ce.msg, "code": ce.code})
			return
		}
		httpx.WriteError(w, ce.status, ce.msg)
		return
	}
	httpx.WriteError(w, http.StatusInternalServerError, err.Error())
}

type posCheckoutTxResult struct {
	TotalCents  int64
	StockStatus string
	VisitClosed bool
	VisitID     int64
	TableID     sql.NullInt64
	Duplicate   bool
}

// checkoutTicketInTx pays one open ticket inside the caller's transaction.
// handleBOPOSCheckout and the bulk day-close checkout share it so the payment,
// stock and visit-close rules cannot drift between the two. The caller owns the
// tx (begin/commit/rollback) and resolves settings + shift once.
func (s *Server) checkoutTicketInTx(ctx context.Context, tx *sql.Tx, restaurantID int, ticketID int64, payments []posCheckoutPayment, idempotencyKey string, expectedVersion int, closeVisit bool, settings posSettings, shiftID *int64, userID int) (posCheckoutTxResult, error) {
	var status string
	var visitID int64
	var total, ticketDiscount int64
	var version int
	var checkoutKey sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT status,visit_id,total_gross_cents,ticket_discount_cents,version,checkout_idempotency_key FROM pos_tickets WHERE restaurant_id=? AND id=? FOR UPDATE`, restaurantID, ticketID).Scan(&status, &visitID, &total, &ticketDiscount, &version, &checkoutKey); err != nil {
		return posCheckoutTxResult{}, &posCheckoutTxError{status: http.StatusNotFound, msg: "Ticket not found"}
	}
	if checkoutKey.Valid && checkoutKey.String == idempotencyKey && status != "OPEN" {
		return posCheckoutTxResult{Duplicate: true, VisitID: visitID}, nil
	}
	if status != "OPEN" {
		return posCheckoutTxResult{}, &posCheckoutTxError{status: http.StatusConflict, msg: "Ticket is not open"}
	}
	if expectedVersion > 0 && version != expectedVersion {
		return posCheckoutTxResult{}, &posCheckoutTxError{status: http.StatusConflict, code: "STALE_TICKET", msg: "Ticket changed"}
	}
	totals, totalErr := s.recalculatePOSTicket(ctx, tx, restaurantID, ticketID, ticketDiscount)
	if totalErr != nil {
		return posCheckoutTxResult{}, &posCheckoutTxError{status: http.StatusBadRequest, msg: totalErr.Error()}
	}
	total = totals.TotalGrossCents
	paymentTotal := int64(0)
	for _, payment := range payments {
		paymentTotal += payment.AmountCents
	}
	// Fully comped tickets still close and deduct stock without inventing a
	// zero-value tender, which pos_payments intentionally forbids.
	if total < 0 || paymentTotal != total || (total > 0 && len(payments) == 0) {
		return posCheckoutTxResult{}, &posCheckoutTxError{status: http.StatusConflict, code: "PAYMENT_MISMATCH", msg: "Payment total does not match ticket"}
	}
	var tipTotal int64
	for _, payment := range payments {
		if payment.Method == "CARD" {
			if strings.TrimSpace(payment.ProviderReference) == "" {
				return posCheckoutTxResult{}, &posCheckoutTxError{status: http.StatusConflict, code: "CARD_REFERENCE_REQUIRED", msg: "Card terminal reference required"}
			}
			if payment.Provider == "" {
				payment.Provider = "STANDALONE"
			}
		}
		if payment.TipCents < 0 {
			return posCheckoutTxResult{}, &posCheckoutTxError{status: http.StatusBadRequest, msg: "Tip cannot be negative"}
		}
		tipTotal += payment.TipCents
		if _, err := tx.ExecContext(ctx, `INSERT INTO pos_payments (restaurant_id,ticket_id,method,amount_cents,tip_cents,provider,provider_reference,card_last4,idempotency_key,received_by) VALUES (?,?,?,?,?,?,?,?,?,?)`, restaurantID, ticketID, payment.Method, payment.AmountCents, payment.TipCents, stockNullableString(payment.Provider), stockNullableString(payment.ProviderReference), stockNullableString(payment.CardLast4), payment.IdempotencyKey, userID); err != nil {
			return posCheckoutTxResult{}, &posCheckoutTxError{status: http.StatusBadRequest, msg: "Payment could not be recorded"}
		}
	}
	if tipTotal > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE pos_tickets SET tip_cents=? WHERE restaurant_id=? AND id=?`, tipTotal, restaurantID, ticketID); err != nil {
			return posCheckoutTxResult{}, &posCheckoutTxError{status: http.StatusInternalServerError, msg: "Error recording tip"}
		}
	}
	stockStatus := "NOT_APPLICABLE"
	if settings.StockMode != "OFF" {
		snapshots, partial, stockErr := s.preparePOSTicketStock(ctx, tx, restaurantID, ticketID, settings.StockMode)
		if stockErr != nil {
			return posCheckoutTxResult{}, &posCheckoutTxError{status: http.StatusInternalServerError, msg: "Error preparing stock deduction"}
		}
		if settings.StockMode == "LIVE" {
			if stockErr = s.applyPOSTicketStock(ctx, tx, restaurantID, userID, ticketID, snapshots); stockErr != nil {
				return posCheckoutTxResult{}, &posCheckoutTxError{status: http.StatusInternalServerError, msg: "Error applying stock deduction"}
			}
			stockStatus = "COMPLETE"
			if partial {
				stockStatus = "PARTIAL"
			}
		} else {
			stockStatus = "SHADOW"
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pos_tickets SET status='PAID',paid_cents=?,stock_status=?,checkout_idempotency_key=?,shift_id=?,closed_by=?,paid_at=NOW(),version=version+1 WHERE restaurant_id=? AND id=?`, total, stockStatus, idempotencyKey, shiftID, userID, restaurantID, ticketID); err != nil {
		return posCheckoutTxResult{}, &posCheckoutTxError{status: http.StatusInternalServerError, msg: "Error closing ticket"}
	}
	visitClosed := false
	var tableID sql.NullInt64
	if closeVisit || settings.AutoCloseVisit {
		var openTickets int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pos_tickets WHERE restaurant_id=? AND visit_id=? AND id<>? AND status='OPEN'`, restaurantID, visitID, ticketID).Scan(&openTickets); err != nil {
			return posCheckoutTxResult{}, &posCheckoutTxError{status: http.StatusInternalServerError, msg: "Error closing visit"}
		}
		if openTickets == 0 {
			var serviceDate, serviceType, channel string
			if err := tx.QueryRowContext(ctx, `SELECT service_date,service_type,channel,table_id FROM pos_visits WHERE restaurant_id=? AND id=? FOR UPDATE`, restaurantID, visitID).Scan(&serviceDate, &serviceType, &channel, &tableID); err != nil {
				return posCheckoutTxResult{}, &posCheckoutTxError{status: http.StatusInternalServerError, msg: "Error closing visit"}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE pos_visits SET status='CLOSED',closed_by=?,closed_at=NOW(),version=version+1 WHERE restaurant_id=? AND id=? AND status='OPEN'`, userID, restaurantID, visitID); err != nil {
				return posCheckoutTxResult{}, &posCheckoutTxError{status: http.StatusInternalServerError, msg: "Error closing visit"}
			}
			serviceDate = normalizePOSDate(serviceDate)
			visitClosed = true
			if channel == "DINE_IN" && settings.CoversMode == "LIVE" {
				if _, err := s.rebuildPOSAffluenceKey(ctx, tx, restaurantID, serviceDate, serviceType); err != nil {
					log.Printf("[pos checkout] covers update failed restaurant=%d ticket=%d visit=%d date=%s service=%s: %v", restaurantID, ticketID, visitID, serviceDate, serviceType, err)
					return posCheckoutTxResult{}, &posCheckoutTxError{status: http.StatusInternalServerError, msg: "Error updating covers"}
				}
			}
		}
	}
	_, _ = tx.ExecContext(ctx, `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,after_json,actor_user_id) VALUES (?,'ticket',?,'CHECKOUT',JSON_OBJECT('stockStatus',?,'totalCents',?),?)`, restaurantID, ticketID, stockStatus, total, userID)
	return posCheckoutTxResult{TotalCents: total, StockStatus: stockStatus, VisitClosed: visitClosed, VisitID: visitID, TableID: tableID}, nil
}

func (s *Server) handleBOPOSCheckout(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	if !s.checkPOSRateLimit("checkout", a.ActiveRestaurantID, a.User.ID, 120) {
		httpx.WriteError(w, http.StatusTooManyRequests, "Too many checkout requests")
		return
	}
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	// A closed cash day is a signed Z closure; mutating it afterwards would
	// invalidate an accounting document that has already been reported.
	if posWriteCashDayGuard(w, s.requireOpenCashDayForTicket(r.Context(), a.ActiveRestaurantID, ticketID)) {
		return
	}
	var in posCheckoutInput
	if ticketID <= 0 || json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&in) != nil || strings.TrimSpace(in.IdempotencyKey) == "" || len(in.Payments) > 10 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid checkout")
		return
	}
	paymentTotal := int64(0)
	for index := range in.Payments {
		in.Payments[index].Method = strings.ToUpper(strings.TrimSpace(in.Payments[index].Method))
		if !validPOSPaymentMethod(in.Payments[index].Method) || in.Payments[index].AmountCents <= 0 || strings.TrimSpace(in.Payments[index].IdempotencyKey) == "" {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid payment")
			return
		}
		if in.Payments[index].AmountCents > math.MaxInt64-paymentTotal {
			httpx.WriteError(w, http.StatusBadRequest, "Payment total is too large")
			return
		}
		paymentTotal += in.Payments[index].AmountCents
	}
	settings, err := s.loadPOSSettings(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading POS settings")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error checking out ticket")
		return
	}
	defer tx.Rollback()
	// The open shift is locked once for this ticket; the bulk day-close checkout
	// resolves it a single time for the whole batch.
	var shiftID *int64
	if settings.RequireOpenShift {
		var currentShift int64
		if err = tx.QueryRowContext(r.Context(), `SELECT id FROM pos_shifts WHERE restaurant_id=? AND status='OPEN' ORDER BY opened_at DESC LIMIT 1 FOR UPDATE`, a.ActiveRestaurantID).Scan(&currentShift); err != nil {
			httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "Open POS shift required", "code": "SHIFT_REQUIRED"})
			return
		}
		shiftID = &currentShift
	}
	result, cerr := s.checkoutTicketInTx(r.Context(), tx, a.ActiveRestaurantID, ticketID, in.Payments, in.IdempotencyKey, in.ExpectedVersion, in.CloseVisit, settings, shiftID, a.User.ID)
	if cerr != nil {
		writeCheckoutTxError(w, cerr)
		return
	}
	if result.Duplicate {
		ticket, _ := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, ticketID)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "duplicate": true, "ticket": ticket})
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error checking out ticket")
		return
	}
	s.broadcastBOTablesEvent(a.ActiveRestaurantID, "pos_ticket_paid", map[string]any{"ticketId": ticketID, "visitId": result.VisitID, "visitClosed": result.VisitClosed, "tableId": stockNullableDBInt(result.TableID)})
	s.broadcastBOFichajeRevenue(a.ActiveRestaurantID, boTodayDate())
	// The totals belong to the visit's business date, which after the cutoff is
	// not the calendar day the sale is being rung up on.
	s.broadcastPOSCashDayTotals(a.ActiveRestaurantID, s.posVisitServiceDate(r.Context(), a.ActiveRestaurantID, result.VisitID))
	ticket, _ := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, ticketID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "ticket": ticket, "stockStatus": result.StockStatus, "visitClosed": result.VisitClosed})
}

// handleBOPOSCashDayBulkCheckout checks out every still-open ticket on a
// business date with one payment method, then closes each ticket's visit. It is
// the "close all open tables" sweep that unblocks the end-of-day Z close, which
// refuses while any visit is open. One transaction makes the whole batch atomic
// and idempotent: a replay finds no OPEN tickets and closes zero.
func (s *Server) handleBOPOSCashDayBulkCheckout(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	date := strings.TrimSpace(chi.URLParam(r, "date"))
	if _, err := time.Parse("2006-01-02", date); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid date")
		return
	}
	var in struct {
		PaymentMethod  string `json:"paymentMethod"`
		IdempotencyKey string `json:"idempotencyKey"`
		CloseVisits    *bool  `json:"closeVisits"`
	}
	if !posDecodeBody(w, r, &in) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid bulk checkout")
		return
	}
	method := strings.ToUpper(strings.TrimSpace(in.PaymentMethod))
	if method == "" || !validPOSPaymentMethod(method) || strings.TrimSpace(in.IdempotencyKey) == "" || len(in.IdempotencyKey) > 120 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid bulk checkout")
		return
	}
	closeVisits := true
	if in.CloseVisits != nil {
		closeVisits = *in.CloseVisits
	}
	// A closed cash day is a signed Z closure; checking tickets out into it
	// would mutate an accounting document that has already been reported.
	if posWriteCashDayGuard(w, s.requireOpenCashDayForDate(r.Context(), a.ActiveRestaurantID, date)) {
		return
	}
	if !s.checkPOSRateLimit("checkout", a.ActiveRestaurantID, a.User.ID, 120) {
		httpx.WriteError(w, http.StatusTooManyRequests, "Too many checkout requests")
		return
	}
	settings, err := s.loadPOSSettings(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading POS settings")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error starting bulk checkout")
		return
	}
	defer tx.Rollback()
	var shiftID *int64
	if settings.RequireOpenShift {
		var currentShift int64
		if err = tx.QueryRowContext(r.Context(), `SELECT id FROM pos_shifts WHERE restaurant_id=? AND status='OPEN' ORDER BY opened_at DESC LIMIT 1 FOR UPDATE`, a.ActiveRestaurantID).Scan(&currentShift); err != nil {
			httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "Open POS shift required", "code": "SHIFT_REQUIRED"})
			return
		}
		shiftID = &currentShift
	}
	rows, err := tx.QueryContext(r.Context(), `SELECT t.id, t.total_gross_cents FROM pos_tickets t JOIN pos_visits v ON v.id=t.visit_id WHERE v.restaurant_id=? AND v.service_date=? AND v.status='OPEN' AND t.status='OPEN' ORDER BY t.id FOR UPDATE`, a.ActiveRestaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading open tickets")
		return
	}
	type pendingTicket struct {
		ticketID int64
		total    int64
	}
	var batch []pendingTicket
	for rows.Next() {
		var p pendingTicket
		if err = rows.Scan(&p.ticketID, &p.total); err != nil {
			rows.Close()
			httpx.WriteError(w, http.StatusInternalServerError, "Error loading open tickets")
			return
		}
		batch = append(batch, p)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading open tickets")
		return
	}
	closedTickets, skippedTickets, closedVisits := 0, 0, 0
	var totalGross int64
	byMethod := map[string]int64{"CASH": 0, "CARD": 0, "BANK": 0, "OTHER": 0}
	for _, p := range batch {
		// A zero-value open ticket cannot take a pos_payments row (the column is
		// strictly positive); skip it. It stays OPEN and the day-close guard will
		// still block until it is handled individually.
		if p.total <= 0 {
			skippedTickets++
			continue
		}
		ticketKey := in.IdempotencyKey + ":t:" + strconv.FormatInt(p.ticketID, 10)
		payments := []posCheckoutPayment{{Method: method, AmountCents: p.total, IdempotencyKey: ticketKey}}
		result, cerr := s.checkoutTicketInTx(r.Context(), tx, a.ActiveRestaurantID, p.ticketID, payments, ticketKey, 0, closeVisits, settings, shiftID, a.User.ID)
		if cerr != nil {
			writeCheckoutTxError(w, cerr)
			return
		}
		if result.Duplicate {
			continue
		}
		closedTickets++
		if result.VisitClosed {
			closedVisits++
		}
		totalGross += result.TotalCents
		byMethod[method] += result.TotalCents
	}
	_, _ = tx.ExecContext(r.Context(), `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,after_json,actor_user_id) VALUES (?,'cash_day',0,'BULK_CHECKOUT',JSON_OBJECT('date',?,'paymentMethod',?,'closedTickets',?,'closedVisits',?,'skippedTickets',?,'totalGrossCents',?),?)`, a.ActiveRestaurantID, date, method, closedTickets, closedVisits, skippedTickets, totalGross, a.User.ID)
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error committing bulk checkout")
		return
	}
	s.broadcastPOSCashDayTotals(a.ActiveRestaurantID, date)
	s.broadcastBOFichajeRevenue(a.ActiveRestaurantID, boTodayDate())
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "date": date, "paymentMethod": method, "closedTickets": closedTickets, "skippedTickets": skippedTickets, "closedVisits": closedVisits, "totalGrossCents": totalGross, "byMethod": byMethod})
}

type posRefundLineInput struct {
	TicketLineID     int64   `json:"ticketLineId"`
	Quantity         float64 `json:"quantity"`
	AmountCents      int64   `json:"amountCents"`
	RestockRequested bool    `json:"restockRequested"`
	Reason           string  `json:"reason"`
}

func (s *Server) handleBOPOSRefund(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	if !s.checkPOSRateLimit("refund", a.ActiveRestaurantID, a.User.ID, 20) {
		httpx.WriteError(w, http.StatusTooManyRequests, "Too many refund requests")
		return
	}
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	// A closed cash day is a signed Z closure; mutating it afterwards would
	// invalidate an accounting document that has already been reported.
	if posWriteCashDayGuard(w, s.requireOpenCashDayForTicket(r.Context(), a.ActiveRestaurantID, ticketID)) {
		return
	}
	var in struct {
		AmountCents    int64                `json:"amountCents"`
		Reason         string               `json:"reason"`
		PaymentMethod  string               `json:"paymentMethod"`
		IdempotencyKey string               `json:"idempotencyKey"`
		Lines          []posRefundLineInput `json:"lines"`
	}
	if ticketID <= 0 || json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&in) != nil || in.AmountCents <= 0 || strings.TrimSpace(in.Reason) == "" || strings.TrimSpace(in.IdempotencyKey) == "" || !validPOSPaymentMethod(strings.ToUpper(in.PaymentMethod)) {
		httpx.WriteError(w, 400, "Invalid refund")
		return
	}
	for _, line := range in.Lines {
		if line.RestockRequested {
			allowed, err := s.boPOSPermissionAllowed(r.Context(), a, posPermissionRestock)
			if err != nil || !allowed {
				httpx.WriteError(w, 403, "Restock permission required")
				return
			}
		}
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error refunding ticket")
		return
	}
	defer tx.Rollback()
	var paid, refunded int64
	var status string
	if err = tx.QueryRowContext(r.Context(), `SELECT paid_cents,refunded_cents,status FROM pos_tickets WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, ticketID).Scan(&paid, &refunded, &status); err != nil || status == "OPEN" || status == "VOIDED" {
		httpx.WriteError(w, 409, "Ticket cannot be refunded")
		return
	}
	if refunded+in.AmountCents > paid {
		httpx.WriteError(w, 409, "Refund exceeds paid amount")
		return
	}
	res, err := tx.ExecContext(r.Context(), `INSERT INTO pos_refunds (restaurant_id,ticket_id,amount_cents,reason,payment_method,idempotency_key,created_by) VALUES (?,?,?,?,?,?,?)`, a.ActiveRestaurantID, ticketID, in.AmountCents, strings.TrimSpace(in.Reason), strings.ToUpper(in.PaymentMethod), in.IdempotencyKey, a.User.ID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			tx.Rollback()
			httpx.WriteJSON(w, 200, map[string]any{"success": true, "duplicate": true})
			return
		}
		httpx.WriteError(w, 400, "Refund could not be recorded")
		return
	}
	refundID, _ := res.LastInsertId()
	lineAmountTotal := int64(0)
	for _, line := range in.Lines {
		lineAmountTotal += line.AmountCents
	}
	if len(in.Lines) > 0 && lineAmountTotal != in.AmountCents {
		httpx.WriteError(w, http.StatusBadRequest, "Refund line amounts must match refund total")
		return
	}
	for _, line := range in.Lines {
		if line.TicketLineID <= 0 || line.Quantity <= 0 || line.AmountCents < 0 || strings.TrimSpace(line.Reason) == "" {
			httpx.WriteError(w, 400, "Invalid refund line")
			return
		}
		var sold, already float64
		var lineTotal int64
		if err = tx.QueryRowContext(r.Context(), `SELECT quantity,line_total_gross_cents,(SELECT COALESCE(SUM(rl.quantity),0) FROM pos_refund_lines rl JOIN pos_refunds rr ON rr.restaurant_id=rl.restaurant_id AND rr.id=rl.refund_id WHERE rl.restaurant_id=l.restaurant_id AND rl.ticket_line_id=l.id AND rr.status='COMPLETED') FROM pos_ticket_lines l WHERE l.restaurant_id=? AND l.ticket_id=? AND l.id=?`, a.ActiveRestaurantID, ticketID, line.TicketLineID).Scan(&sold, &lineTotal, &already); err != nil || already+line.Quantity > sold {
			httpx.WriteError(w, 409, "Refund quantity exceeds sale")
			return
		}
		maxAmount := int64(math.Round(float64(lineTotal) * line.Quantity / sold))
		if line.AmountCents > maxAmount {
			httpx.WriteError(w, http.StatusConflict, "Refund line amount exceeds selected quantity")
			return
		}
		_, err = tx.ExecContext(r.Context(), `INSERT INTO pos_refund_lines (restaurant_id,refund_id,ticket_line_id,quantity,amount_cents,restock_requested,reason) VALUES (?,?,?,?,?,?,?)`, a.ActiveRestaurantID, refundID, line.TicketLineID, line.Quantity, line.AmountCents, stockBoolInt(line.RestockRequested), strings.TrimSpace(line.Reason))
		if err != nil {
			httpx.WriteError(w, 500, "Error saving refund line")
			return
		}
		if line.RestockRequested {
			rows, rowErr := tx.QueryContext(r.Context(), `SELECT id,stock_item_id,warehouse_id,quantity_sold,qty_base_planned,status FROM pos_ticket_line_stock WHERE restaurant_id=? AND ticket_line_id=? AND status IN ('APPLIED','REVERSED') ORDER BY id`, a.ActiveRestaurantID, line.TicketLineID)
			if rowErr != nil {
				httpx.WriteError(w, 500, "Error loading stock return")
				return
			}
			for rows.Next() {
				var snapshotID, itemID, warehouseID int64
				var originalQty, base float64
				var snapshotStatus string
				if err = rows.Scan(&snapshotID, &itemID, &warehouseID, &originalQty, &base, &snapshotStatus); err != nil {
					rows.Close()
					httpx.WriteError(w, 500, "Error reading stock return")
					return
				}
				returnBase := base * (line.Quantity / originalQty)
				var unitID int64
				var factor float64
				if err = tx.QueryRowContext(r.Context(), `SELECT id,factor_to_base FROM stock_item_units WHERE restaurant_id=? AND stock_item_id=? AND is_default_display=1 LIMIT 1`, a.ActiveRestaurantID, itemID).Scan(&unitID, &factor); err != nil {
					rows.Close()
					httpx.WriteError(w, 500, "Error preparing stock return")
					return
				}
				_, err = tx.ExecContext(r.Context(), `INSERT INTO stock_levels (restaurant_id,stock_item_id,warehouse_id,qty_base) VALUES (?,?,?,0) ON DUPLICATE KEY UPDATE stock_item_id=VALUES(stock_item_id)`, a.ActiveRestaurantID, itemID, warehouseID)
				if err != nil {
					rows.Close()
					httpx.WriteError(w, 500, "Error returning stock")
					return
				}
				var current float64
				if err = tx.QueryRowContext(r.Context(), `SELECT qty_base FROM stock_levels WHERE restaurant_id=? AND stock_item_id=? AND warehouse_id=? FOR UPDATE`, a.ActiveRestaurantID, itemID, warehouseID).Scan(&current); err != nil {
					rows.Close()
					httpx.WriteError(w, 500, "Error returning stock")
					return
				}
				moveRes, moveErr := tx.ExecContext(r.Context(), `INSERT INTO stock_movements (restaurant_id,stock_item_id,warehouse_id,qty_base,type,entered_qty,entered_unit_id,ref_type,ref_id,idempotency_key,note,actor_user_id) VALUES (?,?,?,?,'RETURN',?,?,'pos_refund',?,?,?,?)`, a.ActiveRestaurantID, itemID, warehouseID, returnBase, returnBase/factor, unitID, refundID, "pos-refund:"+strconv.FormatInt(refundID, 10)+":snapshot:"+strconv.FormatInt(snapshotID, 10), "POS refund restock", a.User.ID)
				if moveErr != nil {
					rows.Close()
					httpx.WriteError(w, 500, "Error returning stock")
					return
				}
				movementID, _ := moveRes.LastInsertId()
				_, err = tx.ExecContext(r.Context(), `UPDATE stock_levels SET qty_base=qty_base+?,version=version+1 WHERE restaurant_id=? AND stock_item_id=? AND warehouse_id=?`, returnBase, a.ActiveRestaurantID, itemID, warehouseID)
				if err != nil {
					rows.Close()
					httpx.WriteError(w, 500, "Error returning stock")
					return
				}
				var totalRestocked float64
				_ = tx.QueryRowContext(r.Context(), `SELECT COALESCE(SUM(rl.quantity),0) FROM pos_refund_lines rl JOIN pos_refunds rr ON rr.restaurant_id=rl.restaurant_id AND rr.id=rl.refund_id WHERE rl.restaurant_id=? AND rl.ticket_line_id=? AND rl.restock_requested=1 AND rr.status='COMPLETED'`, a.ActiveRestaurantID, line.TicketLineID).Scan(&totalRestocked)
				if totalRestocked >= originalQty {
					_, _ = tx.ExecContext(r.Context(), `UPDATE pos_ticket_line_stock SET status='REVERSED',return_movement_id=?,reversed_at=NOW() WHERE restaurant_id=? AND id=?`, movementID, a.ActiveRestaurantID, snapshotID)
				}
				_ = snapshotStatus
				_ = current
			}
			rows.Close()
		}
	}
	newRefunded := refunded + in.AmountCents
	newStatus := "PARTIALLY_REFUNDED"
	if newRefunded == paid {
		newStatus = "REFUNDED"
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE pos_tickets SET refunded_cents=?,status=?,stock_status=CASE WHEN ?='REFUNDED' AND EXISTS(SELECT 1 FROM pos_ticket_line_stock s WHERE s.restaurant_id=pos_tickets.restaurant_id AND s.ticket_id=pos_tickets.id) AND NOT EXISTS(SELECT 1 FROM pos_ticket_line_stock s WHERE s.restaurant_id=pos_tickets.restaurant_id AND s.ticket_id=pos_tickets.id AND s.status<>'REVERSED') THEN 'REVERSED' ELSE stock_status END,version=version+1 WHERE restaurant_id=? AND id=?`, newRefunded, newStatus, newStatus, a.ActiveRestaurantID, ticketID)
	if err != nil {
		httpx.WriteError(w, 500, "Error refunding ticket")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error refunding ticket")
		return
	}
	ticket, _ := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, ticketID)
	s.broadcastPOSCashDayTotals(a.ActiveRestaurantID, s.posTicketServiceDate(r.Context(), a.ActiveRestaurantID, ticketID))
	httpx.WriteJSON(w, 201, map[string]any{"success": true, "id": refundID, "ticket": ticket})
}

func (s *Server) handleBOPOSVisitClose(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	visitID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	// A closed cash day is a signed Z closure; mutating it afterwards would
	// invalidate an accounting document that has already been reported.
	if posWriteCashDayGuard(w, s.requireOpenCashDayForVisit(r.Context(), a.ActiveRestaurantID, visitID)) {
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error closing visit")
		return
	}
	defer tx.Rollback()
	var date, service, channel string
	var tableID sql.NullInt64
	var status string
	if err = tx.QueryRowContext(r.Context(), `SELECT service_date,service_type,channel,table_id,status FROM pos_visits WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, visitID).Scan(&date, &service, &channel, &tableID, &status); err != nil || status != "OPEN" {
		httpx.WriteError(w, 409, "Visit is not open")
		return
	}
	var open int
	if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM pos_tickets WHERE restaurant_id=? AND visit_id=? AND status='OPEN'`, a.ActiveRestaurantID, visitID).Scan(&open); err != nil || open > 0 {
		httpx.WriteError(w, 409, "Visit has open tickets")
		return
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE pos_visits SET status='CLOSED',closed_by=?,closed_at=NOW(),version=version+1 WHERE restaurant_id=? AND id=?`, a.User.ID, a.ActiveRestaurantID, visitID)
	if err != nil {
		httpx.WriteError(w, 500, "Error closing visit")
		return
	}
	settings, _ := s.loadPOSSettings(r.Context(), a.ActiveRestaurantID)
	date = normalizePOSDate(date)
	if channel == "DINE_IN" && settings.CoversMode == "LIVE" {
		if _, err = s.rebuildPOSAffluenceKey(r.Context(), tx, a.ActiveRestaurantID, date, service); err != nil {
			httpx.WriteError(w, 500, "Error updating covers")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error closing visit")
		return
	}
	s.broadcastBOTablesEvent(a.ActiveRestaurantID, "pos_visit_closed", map[string]any{"visitId": visitID, "tableId": stockNullableDBInt(tableID)})
	s.broadcastPOSCashDayTotals(a.ActiveRestaurantID, s.posVisitServiceDate(r.Context(), a.ActiveRestaurantID, visitID))
	httpx.WriteJSON(w, 200, map[string]any{"success": true})
}

func (s *Server) handleBOPOSCoverAdjustment(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var in struct {
		VisitID        *int64 `json:"visitId"`
		Date           string `json:"date"`
		ServiceType    string `json:"serviceType"`
		Delta          int    `json:"delta"`
		Reason         string `json:"reason"`
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || in.Delta == 0 || strings.TrimSpace(in.Reason) == "" || strings.TrimSpace(in.IdempotencyKey) == "" {
		httpx.WriteError(w, 400, "Invalid cover adjustment")
		return
	}
	if _, err := time.Parse("2006-01-02", in.Date); err != nil || !validPOSMode(in.ServiceType, "LUNCH", "DINNER", "OTHER") {
		httpx.WriteError(w, 400, "Invalid cover key")
		return
	}
	// A closed cash day is a signed Z closure; mutating it afterwards would
	// invalidate an accounting document that has already been reported. The
	// adjustment names its own date, so that is what gets checked.
	if posWriteCashDayGuard(w, s.requireOpenCashDayForDate(r.Context(), a.ActiveRestaurantID, in.Date)) {
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error adjusting covers")
		return
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(r.Context(), `INSERT INTO pos_cover_adjustments (restaurant_id,visit_id,service_date,service_type,delta_covers,reason,idempotency_key,actor_user_id) VALUES (?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, in.VisitID, in.Date, in.ServiceType, in.Delta, strings.TrimSpace(in.Reason), in.IdempotencyKey, a.User.ID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			tx.Rollback()
			httpx.WriteJSON(w, 200, map[string]any{"success": true, "duplicate": true})
			return
		}
		httpx.WriteError(w, 400, "Cover adjustment could not be saved")
		return
	}
	covers, err := s.rebuildPOSAffluenceKey(r.Context(), tx, a.ActiveRestaurantID, in.Date, in.ServiceType)
	if err != nil {
		httpx.WriteError(w, 500, "Error rebuilding covers")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error adjusting covers")
		return
	}
	id, _ := res.LastInsertId()
	s.broadcastPOSCashDayTotals(a.ActiveRestaurantID, in.Date)
	httpx.WriteJSON(w, 201, map[string]any{"success": true, "id": id, "covers": covers})
}

func (s *Server) handleBOPOSCoversReport(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	from := strings.TrimSpace(r.URL.Query().Get("from"))
	to := strings.TrimSpace(r.URL.Query().Get("to"))
	if from == "" {
		from = time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	}
	if to == "" {
		to = time.Now().Format("2006-01-02")
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT keys.service_date,keys.service_type,keys.pos_covers,keys.adjustments,COALESCE(a.covers,0),COALESCE(a.source,'') FROM (SELECT v.service_date,v.service_type,SUM(CASE WHEN v.status='CLOSED' AND v.channel='DINE_IN' THEN v.covers ELSE 0 END) pos_covers,COALESCE((SELECT SUM(c.delta_covers) FROM pos_cover_adjustments c WHERE c.restaurant_id=v.restaurant_id AND c.service_date=v.service_date AND c.service_type=v.service_type),0) adjustments FROM pos_visits v WHERE v.restaurant_id=? AND v.service_date BETWEEN ? AND ? GROUP BY v.service_date,v.service_type) keys LEFT JOIN stock_affluence_daily a ON a.restaurant_id=? AND a.service_date=keys.service_date AND a.service_type=keys.service_type ORDER BY keys.service_date DESC,keys.service_type`, a.ActiveRestaurantID, from, to, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading covers")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var date, service, source string
		var pos, adjustments, aggregate int
		if err = rows.Scan(&date, &service, &pos, &adjustments, &aggregate, &source); err != nil {
			httpx.WriteError(w, 500, "Error reading covers")
			return
		}
		items = append(items, map[string]any{"date": normalizePOSDate(date), "serviceType": service, "posCovers": pos, "adjustments": adjustments, "computedCovers": aggregatePOSCovers(nil, pos+adjustments), "aggregateCovers": aggregate, "source": source})
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "items": items})
}
