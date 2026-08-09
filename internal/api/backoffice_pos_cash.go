package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

// posCashQueryer is implemented by both *sql.DB and *sql.Tx. Keeping the
// summary query here makes the preview and the atomic Z close use identical
// accounting rules.
type posCashQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// posCashScope selects which tickets a cash summary covers. A terminal X/Y/Z
// closure scopes by shift; a cash-day close scopes by business date. Both go
// through loadPOSCashSummaryScoped so the accounting rules cannot drift apart.
type posCashScope struct {
	// ticketWhere is a predicate over the aliases t (pos_tickets) and v (pos_visits).
	ticketWhere string
	ticketArgs  []any
	// movementWhere is a predicate over the aliases m (pos_cash_movements) and
	// sh (pos_shifts). Cash movements always hang off a shift, so a day scope
	// reaches them through pos_shifts.cash_day_id.
	movementWhere string
	movementArgs  []any
	// visitWhere narrows the open-visit count over the alias v (pos_visits). A
	// shift closure counts every open visit in the restaurant, because a drawer
	// must not be handed over while any table is live. A day close only cares
	// about its own date, so an operator can still settle Saturday's takings on
	// Monday while Monday's tables are open.
	visitWhere string
	visitArgs  []any
}

func posCashShiftScope(shiftID int64) posCashScope {
	return posCashScope{
		ticketWhere: "t.shift_id=?", ticketArgs: []any{shiftID},
		movementWhere: "m.shift_id=?", movementArgs: []any{shiftID},
	}
}

func posCashDayScope(cashDayID int64, businessDate string) posCashScope {
	return posCashScope{
		ticketWhere: "v.service_date=?", ticketArgs: []any{businessDate},
		movementWhere: "sh.cash_day_id=?", movementArgs: []any{cashDayID},
		visitWhere: "v.service_date=?", visitArgs: []any{businessDate},
	}
}

// ticketQueryArgs prefixes the tenant id, matching the
// "WHERE t.restaurant_id=? AND <ticketWhere>" shape used below.
func (c posCashScope) ticketQueryArgs(restaurantID int, extra ...any) []any {
	out := make([]any, 0, 1+len(c.ticketArgs)+len(extra))
	out = append(out, restaurantID)
	out = append(out, c.ticketArgs...)
	return append(out, extra...)
}

type posCashSummary struct {
	ShiftID           int64
	TerminalKey       string
	Status            string
	OpenedAt          time.Time
	ClosedAt          sql.NullTime
	OpeningCash       int64
	SalesGross        int64
	Refunds           int64
	Discounts         int64
	Surcharges        int64
	Tips              int64
	CashSales         int64
	CashTips          int64
	CardSales         int64
	CardTips          int64
	BankSales         int64
	BankTips          int64
	OtherSales        int64
	OtherTips         int64
	CashRefunds       int64
	CashIn            int64
	CashOut           int64
	ExpectedCash      int64
	TicketCount       int
	VoidedTicketCount int
	Covers            int
	OpenVisitCount    int
	OpenTicketCount   int
}

func (s posCashSummary) asMap() map[string]any {
	closedAt := any(nil)
	if s.ClosedAt.Valid {
		closedAt = s.ClosedAt.Time
	}
	return map[string]any{
		"shiftId": s.ShiftID, "terminalKey": s.TerminalKey, "status": s.Status,
		"openedAt": s.OpenedAt, "closedAt": closedAt, "openingCashCents": s.OpeningCash,
		"salesGrossCents": s.SalesGross, "refundsCents": s.Refunds, "netSalesCents": s.SalesGross - s.Refunds,
		"discountsCents": s.Discounts, "surchargesCents": s.Surcharges, "tipsCents": s.Tips,
		"cashSalesCents": s.CashSales, "cashTipsCents": s.CashTips,
		"cardSalesCents": s.CardSales, "cardTipsCents": s.CardTips,
		"bankSalesCents": s.BankSales, "bankTipsCents": s.BankTips,
		"otherSalesCents": s.OtherSales, "otherTipsCents": s.OtherTips,
		"cashRefundsCents": s.CashRefunds, "cashInCents": s.CashIn, "cashOutCents": s.CashOut,
		"expectedCashCents": s.ExpectedCash, "ticketCount": s.TicketCount,
		"voidedTicketCount": s.VoidedTicketCount, "covers": s.Covers,
		"openVisitCount": s.OpenVisitCount, "openTicketCount": s.OpenTicketCount,
	}
}

func (s *Server) loadPOSCashSummary(ctx context.Context, q posCashQueryer, restaurantID int, shiftID int64) (posCashSummary, error) {
	var out posCashSummary
	out.ShiftID = shiftID
	if err := q.QueryRowContext(ctx, `SELECT terminal_key,status,opening_cash_cents,opened_at,closed_at FROM pos_shifts WHERE restaurant_id=? AND id=?`, restaurantID, shiftID).Scan(&out.TerminalKey, &out.Status, &out.OpeningCash, &out.OpenedAt, &out.ClosedAt); err != nil {
		return out, err
	}
	return s.loadPOSCashSummaryScoped(ctx, q, restaurantID, posCashShiftScope(shiftID), out)
}

// loadPOSCashSummaryScoped fills the money side of a summary for the given
// scope. The caller supplies the header fields (terminal, status, opened_at and
// the opening float) because those come from the shift or the cash day row.
func (s *Server) loadPOSCashSummaryScoped(ctx context.Context, q posCashQueryer, restaurantID int, scope posCashScope, out posCashSummary) (posCashSummary, error) {
	// Every ticket belongs to a visit (pos_tickets.visit_id is NOT NULL), so the
	// join is lossless and lets a scope filter on either side.
	ticketFrom := `FROM pos_tickets t JOIN pos_visits v ON v.restaurant_id=t.restaurant_id AND v.id=t.visit_id WHERE t.restaurant_id=? AND ` + scope.ticketWhere
	if err := q.QueryRowContext(ctx, `
		SELECT
			COUNT(CASE WHEN t.status IN ('PAID','PARTIALLY_REFUNDED','REFUNDED') THEN 1 END),
			COALESCE(SUM(CASE WHEN t.status IN ('PAID','PARTIALLY_REFUNDED','REFUNDED') THEN t.total_gross_cents ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN t.status IN ('PAID','PARTIALLY_REFUNDED','REFUNDED') THEN t.refunded_cents ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN t.status IN ('PAID','PARTIALLY_REFUNDED','REFUNDED') THEN t.discount_cents ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN t.status IN ('PAID','PARTIALLY_REFUNDED','REFUNDED') THEN t.surcharge_cents ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN t.status IN ('PAID','PARTIALLY_REFUNDED','REFUNDED') THEN t.tip_cents ELSE 0 END),0),
			COUNT(CASE WHEN t.status='VOIDED' THEN 1 END)
		`+ticketFrom, scope.ticketQueryArgs(restaurantID)...).
		Scan(&out.TicketCount, &out.SalesGross, &out.Refunds, &out.Discounts, &out.Surcharges, &out.Tips, &out.VoidedTicketCount); err != nil {
		return out, err
	}
	// The outer restaurant_id binds first, then the subquery's own tenant id and
	// scope arguments.
	scopedTicketIDs := `SELECT t.id ` + ticketFrom
	subqueryArgs := append([]any{restaurantID}, scope.ticketQueryArgs(restaurantID)...)
	rows, err := q.QueryContext(ctx, `SELECT method,COALESCE(SUM(amount_cents),0),COALESCE(SUM(tip_cents),0) FROM pos_payments WHERE restaurant_id=? AND status='CAPTURED' AND ticket_id IN (`+scopedTicketIDs+`) GROUP BY method`, subqueryArgs...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var method string
		var amount, tips int64
		if err = rows.Scan(&method, &amount, &tips); err != nil {
			rows.Close()
			return out, err
		}
		switch method {
		case "CASH":
			out.CashSales, out.CashTips = amount, tips
		case "CARD":
			out.CardSales, out.CardTips = amount, tips
		case "BANK":
			out.BankSales, out.BankTips = amount, tips
		default:
			out.OtherSales, out.OtherTips = amount, tips
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	if err = q.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN payment_method='CASH' THEN amount_cents ELSE 0 END),0),COALESCE(SUM(amount_cents),0) FROM pos_refunds WHERE restaurant_id=? AND status='COMPLETED' AND ticket_id IN (`+scopedTicketIDs+`)`, subqueryArgs...).Scan(&out.CashRefunds, new(int64)); err != nil {
		return out, err
	}
	movementArgs := append([]any{restaurantID}, scope.movementArgs...)
	if err = q.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN m.movement_type='IN' THEN m.amount_cents ELSE 0 END),0),COALESCE(SUM(CASE WHEN m.movement_type='OUT' THEN m.amount_cents ELSE 0 END),0) FROM pos_cash_movements m JOIN pos_shifts sh ON sh.restaurant_id=m.restaurant_id AND sh.id=m.shift_id WHERE m.restaurant_id=? AND `+scope.movementWhere, movementArgs...).Scan(&out.CashIn, &out.CashOut); err != nil {
		return out, err
	}
	if err = q.QueryRowContext(ctx, `SELECT COALESCE(SUM(x.covers),0) FROM (SELECT DISTINCT v.id,v.covers `+ticketFrom+` AND t.status IN ('PAID','PARTIALLY_REFUNDED','REFUNDED')) x`, scope.ticketQueryArgs(restaurantID)...).Scan(&out.Covers); err != nil {
		return out, err
	}
	visitWhere, visitArgs := "", []any{restaurantID}
	if scope.visitWhere != "" {
		visitWhere = " AND " + scope.visitWhere
		visitArgs = append(visitArgs, scope.visitArgs...)
	}
	if err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM pos_visits v WHERE v.restaurant_id=? AND v.status='OPEN'`+visitWhere, visitArgs...).Scan(&out.OpenVisitCount); err != nil {
		return out, err
	}
	if err = q.QueryRowContext(ctx, `SELECT COUNT(*) `+ticketFrom+` AND t.status='OPEN'`, scope.ticketQueryArgs(restaurantID)...).Scan(&out.OpenTicketCount); err != nil {
		return out, err
	}
	// Tips are collected on top of the sale. Cash tips therefore remain in the
	// drawer even though they are excluded from net sales and VAT.
	out.ExpectedCash = out.OpeningCash + out.CashSales + out.CashTips - out.CashRefunds + out.CashIn - out.CashOut
	return out, nil
}

func (s *Server) posCashShiftID(r *http.Request, requested int64) (int64, error) {
	a, _ := boAuthFromContext(r.Context())
	if requested > 0 {
		return requested, nil
	}
	var id int64
	err := s.db.QueryRowContext(r.Context(), `SELECT id FROM pos_shifts WHERE restaurant_id=? AND status='OPEN' ORDER BY opened_at DESC LIMIT 1`, a.ActiveRestaurantID).Scan(&id)
	return id, err
}

func (s *Server) handleBOPOSCashSummary(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	requested, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("shiftId")), 10, 64)
	shiftID, err := s.posCashShiftID(r, requested)
	if errors.Is(err, sql.ErrNoRows) && requested == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "summary": nil})
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Shift not found")
		return
	}
	summary, err := s.loadPOSCashSummary(r.Context(), s.db, a.ActiveRestaurantID, shiftID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading cash summary")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "summary": summary.asMap()})
}

func (s *Server) handleBOPOSCashMovements(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	requested, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("shiftId")), 10, 64)
	shiftID, err := s.posCashShiftID(r, requested)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Shift not found")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,terminal_key,movement_type,amount_cents,reason,idempotency_key,created_by,created_at FROM pos_cash_movements WHERE restaurant_id=? AND shift_id=? ORDER BY created_at DESC,id DESC LIMIT 500`, a.ActiveRestaurantID, shiftID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading cash movements")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, amount, actor int64
		var terminal, kind, reason, key string
		var created time.Time
		if err = rows.Scan(&id, &terminal, &kind, &amount, &reason, &key, &actor, &created); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading cash movements")
			return
		}
		items = append(items, map[string]any{"id": id, "shiftId": shiftID, "terminalKey": terminal, "type": kind, "amountCents": amount, "reason": reason, "idempotencyKey": key, "createdBy": actor, "createdAt": created})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "shiftId": shiftID, "items": items})
}

func (s *Server) handleBOPOSCashMovementCreate(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var in struct {
		ShiftID        int64  `json:"shiftId"`
		TerminalKey    string `json:"terminalKey"`
		Type           string `json:"type"`
		AmountCents    int64  `json:"amountCents"`
		Reason         string `json:"reason"`
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if !posDecodeBody(w, r, &in) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid cash movement")
		return
	}
	in.Type = strings.ToUpper(strings.TrimSpace(in.Type))
	in.TerminalKey = strings.TrimSpace(in.TerminalKey)
	in.Reason = strings.TrimSpace(in.Reason)
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	if !validPOSMode(in.Type, "IN", "OUT") || in.AmountCents <= 0 || in.AmountCents > 1000000000 || in.Reason == "" || len(in.Reason) > 500 || in.IdempotencyKey == "" || len(in.IdempotencyKey) > 120 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid cash movement")
		return
	}
	shiftID, err := s.posCashShiftID(r, in.ShiftID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "Open a cash shift before recording a movement", "code": "POS_SHIFT_REQUIRED"})
		return
	}
	var shiftTerminal, status string
	if err = s.db.QueryRowContext(r.Context(), `SELECT terminal_key,status FROM pos_shifts WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, shiftID).Scan(&shiftTerminal, &status); err != nil || status != "OPEN" {
		httpx.WriteError(w, http.StatusConflict, "Only an open shift accepts cash movements")
		return
	}
	if in.TerminalKey == "" {
		in.TerminalKey = shiftTerminal
	}
	var existing int64
	if err = s.db.QueryRowContext(r.Context(), `SELECT id FROM pos_cash_movements WHERE restaurant_id=? AND idempotency_key=?`, a.ActiveRestaurantID, in.IdempotencyKey).Scan(&existing); err == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "duplicate": true, "movementId": existing, "shiftId": shiftID})
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusInternalServerError, "Error checking cash movement")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO pos_cash_movements (restaurant_id,shift_id,terminal_key,movement_type,amount_cents,reason,idempotency_key,created_by) VALUES (?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, shiftID, in.TerminalKey, in.Type, in.AmountCents, in.Reason, in.IdempotencyKey, a.User.ID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			_ = s.db.QueryRowContext(r.Context(), `SELECT id FROM pos_cash_movements WHERE restaurant_id=? AND idempotency_key=?`, a.ActiveRestaurantID, in.IdempotencyKey).Scan(&existing)
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "duplicate": true, "movementId": existing, "shiftId": shiftID})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error recording cash movement")
		return
	}
	id, _ := res.LastInsertId()
	_, _ = s.db.ExecContext(r.Context(), `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,after_json,actor_user_id) VALUES (?,'cash_movement',?,'CREATE',JSON_OBJECT('shiftId',?,'type',?,'amountCents',?,'reason',?),?)`, a.ActiveRestaurantID, id, shiftID, in.Type, in.AmountCents, in.Reason, a.User.ID)
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "movementId": id, "shiftId": shiftID, "type": in.Type, "amountCents": in.AmountCents})
}

func (s *Server) handleBOPOSCashClosures(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	requested, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("shiftId")), 10, 64)
	args := []any{a.ActiveRestaurantID}
	where := "restaurant_id=?"
	if requested > 0 {
		where += " AND shift_id=?"
		args = append(args, requested)
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,shift_id,terminal_key,closure_type,generated_at,opening_cash_cents,sales_gross_cents,refunds_cents,tips_cents,expected_cash_cents,counted_cash_cents,difference_cents,ticket_count,covers,note,created_by FROM pos_cash_closures WHERE `+where+` ORDER BY generated_at DESC,id DESC LIMIT 200`, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading cash closures")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, shiftID, opening, gross, refunds, tips, expected, createdBy int64
		var count, covers int
		var terminal, kind string
		var generated time.Time
		var counted, difference sql.NullInt64
		var note sql.NullString
		if err = rows.Scan(&id, &shiftID, &terminal, &kind, &generated, &opening, &gross, &refunds, &tips, &expected, &counted, &difference, &count, &covers, &note, &createdBy); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading cash closures")
			return
		}
		items = append(items, map[string]any{"id": id, "shiftId": shiftID, "terminalKey": terminal, "closureType": kind, "generatedAt": generated, "openingCashCents": opening, "salesGrossCents": gross, "refundsCents": refunds, "tipsCents": tips, "expectedCashCents": expected, "countedCashCents": nullableInt64(counted), "differenceCents": nullableInt64(difference), "ticketCount": count, "covers": covers, "note": posCashNullableString(note), "createdBy": createdBy})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "items": items})
}

func (s *Server) handleBOPOSCashClosureCreate(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var in struct {
		ShiftID           int64  `json:"shiftId"`
		TerminalKey       string `json:"terminalKey"`
		ClosureType       string `json:"closureType"`
		CountedCashCents  *int64 `json:"countedCashCents"`
		Note              string `json:"note"`
		DiscrepancyReason string `json:"discrepancyReason"`
		IdempotencyKey    string `json:"idempotencyKey"`
	}
	if !posDecodeBody(w, r, &in) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid cash closure")
		return
	}
	in.ClosureType = strings.ToUpper(strings.TrimSpace(in.ClosureType))
	in.TerminalKey = strings.TrimSpace(in.TerminalKey)
	in.Note = strings.TrimSpace(in.Note)
	in.DiscrepancyReason = strings.TrimSpace(in.DiscrepancyReason)
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	if !validPOSMode(in.ClosureType, "X", "Y", "Z") || in.IdempotencyKey == "" || len(in.IdempotencyKey) > 120 || len(in.Note) > 500 || len(in.DiscrepancyReason) > 500 || (in.CountedCashCents != nil && *in.CountedCashCents < 0) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid cash closure")
		return
	}
	shiftID, err := s.posCashShiftID(r, in.ShiftID)
	if err != nil {
		httpx.WriteError(w, http.StatusConflict, "Open a cash shift before closing")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creating cash closure")
		return
	}
	defer tx.Rollback()
	var existingID int64
	if err = tx.QueryRowContext(r.Context(), `SELECT id FROM pos_cash_closures WHERE restaurant_id=? AND idempotency_key=?`, a.ActiveRestaurantID, in.IdempotencyKey).Scan(&existingID); err == nil {
		tx.Rollback()
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "duplicate": true, "closureId": existingID, "shiftId": shiftID})
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusInternalServerError, "Error checking cash closure")
		return
	}
	var status string
	if err = tx.QueryRowContext(r.Context(), `SELECT status FROM pos_shifts WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, shiftID).Scan(&status); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Shift not found")
		return
	}
	if in.ClosureType == "Z" && status != "OPEN" {
		httpx.WriteError(w, http.StatusConflict, "Shift is already closed")
		return
	}
	summary, err := s.loadPOSCashSummary(r.Context(), tx, a.ActiveRestaurantID, shiftID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error calculating cash closure")
		return
	}
	if in.ClosureType == "Z" && (summary.OpenVisitCount > 0 || summary.OpenTicketCount > 0) {
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "message": "Close all open visits and tickets before the final Z close", "code": "OPEN_POS_ITEMS", "openVisitCount": summary.OpenVisitCount, "openTicketCount": summary.OpenTicketCount})
		return
	}
	counted := any(nil)
	difference := any(nil)
	if in.CountedCashCents != nil {
		counted = *in.CountedCashCents
		difference = *in.CountedCashCents - summary.ExpectedCash
		if difference.(int64) != 0 && in.DiscrepancyReason == "" {
			httpx.WriteError(w, http.StatusBadRequest, "A cash discrepancy requires a reason")
			return
		}
	}
	if in.ClosureType == "Z" && in.CountedCashCents == nil {
		httpx.WriteError(w, http.StatusBadRequest, "The final Z close requires counted cash")
		return
	}
	closedAt := any(nil)
	if in.ClosureType == "Z" {
		closedAt = time.Now()
	}
	res, err := tx.ExecContext(r.Context(), `INSERT INTO pos_cash_closures (restaurant_id,shift_id,terminal_key,closure_type,opened_at,closed_at,opening_cash_cents,sales_gross_cents,refunds_cents,discounts_cents,surcharges_cents,tips_cents,cash_sales_cents,cash_tips_cents,card_sales_cents,card_tips_cents,bank_sales_cents,bank_tips_cents,other_sales_cents,other_tips_cents,cash_refunds_cents,cash_in_cents,cash_out_cents,expected_cash_cents,counted_cash_cents,difference_cents,ticket_count,voided_ticket_count,covers,open_visit_count,open_ticket_count,note,discrepancy_reason,idempotency_key,created_by) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, shiftID, summary.TerminalKey, in.ClosureType, summary.OpenedAt, closedAt, summary.OpeningCash, summary.SalesGross, summary.Refunds, summary.Discounts, summary.Surcharges, summary.Tips, summary.CashSales, summary.CashTips, summary.CardSales, summary.CardTips, summary.BankSales, summary.BankTips, summary.OtherSales, summary.OtherTips, summary.CashRefunds, summary.CashIn, summary.CashOut, summary.ExpectedCash, counted, difference, summary.TicketCount, summary.VoidedTicketCount, summary.Covers, summary.OpenVisitCount, summary.OpenTicketCount, stockNullableString(in.Note), stockNullableString(in.DiscrepancyReason), in.IdempotencyKey, a.User.ID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			_ = tx.QueryRowContext(r.Context(), `SELECT id FROM pos_cash_closures WHERE restaurant_id=? AND idempotency_key=?`, a.ActiveRestaurantID, in.IdempotencyKey).Scan(&existingID)
			tx.Rollback()
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "duplicate": true, "closureId": existingID, "shiftId": shiftID})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error recording cash closure")
		return
	}
	closureID, _ := res.LastInsertId()
	if in.ClosureType == "Z" {
		if _, err = tx.ExecContext(r.Context(), `UPDATE pos_shifts SET status='CLOSED',closed_by=?,closing_cash_counted_cents=?,expected_cash_cents=?,closed_at=NOW(),notes=COALESCE(?,notes) WHERE restaurant_id=? AND id=?`, a.User.ID, *in.CountedCashCents, summary.ExpectedCash, stockNullableString(in.Note), a.ActiveRestaurantID, shiftID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error closing shift")
			return
		}
	}
	_, _ = tx.ExecContext(r.Context(), `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,after_json,actor_user_id) VALUES (?,'cash_closure',?,'CREATE',JSON_OBJECT('shiftId',?,'closureType',?,'expectedCashCents',?,'countedCashCents',?),?)`, a.ActiveRestaurantID, closureID, shiftID, in.ClosureType, summary.ExpectedCash, counted, a.User.ID)
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creating cash closure")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "closureId": closureID, "shiftId": shiftID, "closureType": in.ClosureType, "summary": summary.asMap(), "countedCashCents": counted, "differenceCents": difference})
}

func nullableInt64(value sql.NullInt64) any {
	if value.Valid {
		return value.Int64
	}
	return nil
}

func posCashNullableString(value sql.NullString) any {
	if value.Valid {
		return value.String
	}
	return nil
}

func (s *Server) handleBOPOSCashClosureGet(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var shiftID int64
	var kind, terminal string
	var generated time.Time
	var counted, difference sql.NullInt64
	var opening, gross, refunds, discounts, surcharges, tips, cashSales, cashTips, cardSales, cardTips, bankSales, bankTips, otherSales, otherTips, cashRefunds, cashIn, cashOut, expected int64
	var ticketCount, voidedCount, covers, openVisits, openTickets int
	if err := s.db.QueryRowContext(r.Context(), `SELECT shift_id,closure_type,terminal_key,generated_at,opening_cash_cents,sales_gross_cents,refunds_cents,discounts_cents,surcharges_cents,tips_cents,cash_sales_cents,cash_tips_cents,card_sales_cents,card_tips_cents,bank_sales_cents,bank_tips_cents,other_sales_cents,other_tips_cents,cash_refunds_cents,cash_in_cents,cash_out_cents,expected_cash_cents,counted_cash_cents,difference_cents,ticket_count,voided_ticket_count,covers,open_visit_count,open_ticket_count FROM pos_cash_closures WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, id).Scan(&shiftID, &kind, &terminal, &generated, &opening, &gross, &refunds, &discounts, &surcharges, &tips, &cashSales, &cashTips, &cardSales, &cardTips, &bankSales, &bankTips, &otherSales, &otherTips, &cashRefunds, &cashIn, &cashOut, &expected, &counted, &difference, &ticketCount, &voidedCount, &covers, &openVisits, &openTickets); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Cash closure not found")
		return
	}
	snapshot := map[string]any{
		"shiftId": shiftID, "terminalKey": terminal, "status": "COMPLETED", "generatedAt": generated,
		"openingCashCents": opening, "salesGrossCents": gross, "refundsCents": refunds, "netSalesCents": gross - refunds,
		"discountsCents": discounts, "surchargesCents": surcharges, "tipsCents": tips,
		"cashSalesCents": cashSales, "cashTipsCents": cashTips, "cardSalesCents": cardSales, "cardTipsCents": cardTips,
		"bankSalesCents": bankSales, "bankTipsCents": bankTips, "otherSalesCents": otherSales, "otherTipsCents": otherTips,
		"cashRefundsCents": cashRefunds, "cashInCents": cashIn, "cashOutCents": cashOut, "expectedCashCents": expected,
		"ticketCount": ticketCount, "voidedTicketCount": voidedCount, "covers": covers, "openVisitCount": openVisits, "openTicketCount": openTickets,
	}
	snapshot["countedCashCents"] = nullableInt64(counted)
	snapshot["differenceCents"] = nullableInt64(difference)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "closure": map[string]any{"id": id, "shiftId": shiftID, "closureType": kind, "terminalKey": terminal, "generatedAt": generated, "summary": snapshot}})
}
