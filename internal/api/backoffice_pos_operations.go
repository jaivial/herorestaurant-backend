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

type posServicePeriodInput struct {
	Name        string `json:"name"`
	ServiceType string `json:"serviceType"`
	StartTime   string `json:"startTime"`
	EndTime     string `json:"endTime"`
	SortOrder   int    `json:"sortOrder"`
	IsActive    bool   `json:"isActive"`
}

func (s *Server) handleBOPOSServicePeriodsList(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,name,service_type,TIME_FORMAT(start_time,'%H:%i'),TIME_FORMAT(end_time,'%H:%i'),sort_order,is_active FROM pos_service_periods WHERE restaurant_id=? ORDER BY sort_order,id`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading service periods")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, kind, start, end string
		var sortOrder, active int
		if err = rows.Scan(&id, &name, &kind, &start, &end, &sortOrder, &active); err != nil {
			httpx.WriteError(w, 500, "Error reading service periods")
			return
		}
		items = append(items, map[string]any{"id": id, "name": name, "serviceType": kind, "startTime": start, "endTime": end, "sortOrder": sortOrder, "isActive": active != 0})
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "items": items})
}
func (s *Server) saveBOPOSServicePeriod(w http.ResponseWriter, r *http.Request, id int64) {
	a, _ := boAuthFromContext(r.Context())
	var in posServicePeriodInput
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" || !validPOSMode(in.ServiceType, "LUNCH", "DINNER", "OTHER") {
		httpx.WriteError(w, 400, "Invalid service period")
		return
	}
	if _, err := posClockMinutes(in.StartTime); err != nil {
		httpx.WriteError(w, 400, "Invalid start time")
		return
	}
	if _, err := posClockMinutes(in.EndTime); err != nil {
		httpx.WriteError(w, 400, "Invalid end time")
		return
	}
	if id == 0 {
		res, err := s.db.ExecContext(r.Context(), `INSERT INTO pos_service_periods (restaurant_id,name,service_type,start_time,end_time,sort_order,is_active) VALUES (?,?,?,?,?,?,?)`, a.ActiveRestaurantID, strings.TrimSpace(in.Name), in.ServiceType, in.StartTime, in.EndTime, in.SortOrder, stockBoolInt(in.IsActive))
		if err != nil {
			httpx.WriteError(w, 400, "Service period could not be created")
			return
		}
		id, _ = res.LastInsertId()
		httpx.WriteJSON(w, 201, map[string]any{"success": true, "id": id})
		return
	}
	res, err := s.db.ExecContext(r.Context(), `UPDATE pos_service_periods SET name=?,service_type=?,start_time=?,end_time=?,sort_order=?,is_active=? WHERE restaurant_id=? AND id=?`, strings.TrimSpace(in.Name), in.ServiceType, in.StartTime, in.EndTime, in.SortOrder, stockBoolInt(in.IsActive), a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteError(w, 400, "Service period could not be updated")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, 404, "Service period not found")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true})
}
func (s *Server) handleBOPOSServicePeriodCreate(w http.ResponseWriter, r *http.Request) {
	s.saveBOPOSServicePeriod(w, r, 0)
}
func (s *Server) handleBOPOSServicePeriodPatch(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	s.saveBOPOSServicePeriod(w, r, id)
}
func (s *Server) handleBOPOSServicePeriodDelete(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	res, err := s.db.ExecContext(r.Context(), `DELETE FROM pos_service_periods WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteError(w, 500, "Error deleting service period")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, 404, "Service period not found")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true})
}

func (s *Server) handleBOPOSVisitGet(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	visitID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var visit map[string]any
	rows, err := s.loadPOSVisits(r.Context(), a.ActiveRestaurantID, "")
	if err != nil {
		httpx.WriteError(w, 500, "Error loading visit")
		return
	}
	for _, candidate := range rows {
		if candidate["id"] == visitID {
			visit = candidate
			break
		}
	}
	if visit == nil {
		httpx.WriteError(w, 404, "Visit not found")
		return
	}
	ticketRows, err := s.db.QueryContext(r.Context(), `SELECT id FROM pos_tickets WHERE restaurant_id=? AND visit_id=? ORDER BY id`, a.ActiveRestaurantID, visitID)
	if err != nil {
		httpx.WriteError(w, 500, "Error loading visit tickets")
		return
	}
	tickets := []map[string]any{}
	for ticketRows.Next() {
		var ticketID int64
		if err = ticketRows.Scan(&ticketID); err != nil {
			ticketRows.Close()
			httpx.WriteError(w, 500, "Error reading visit tickets")
			return
		}
		ticket, loadErr := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, ticketID)
		if loadErr != nil {
			ticketRows.Close()
			httpx.WriteError(w, 500, "Error loading ticket")
			return
		}
		tickets = append(tickets, ticket)
	}
	ticketRows.Close()
	visit["tickets"] = tickets
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "visit": visit})
}
func (s *Server) handleBOPOSVisitPatch(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	visitID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		TableID         *int64 `json:"tableId"`
		Covers          int    `json:"covers"`
		ExpectedVersion int    `json:"expectedVersion"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || in.Covers <= 0 {
		httpx.WriteError(w, 400, "Invalid visit update")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error updating visit")
		return
	}
	defer tx.Rollback()
	var status, channel string
	var version int
	if err = tx.QueryRowContext(r.Context(), `SELECT status,channel,version FROM pos_visits WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, visitID).Scan(&status, &channel, &version); err != nil || status != "OPEN" || channel != "DINE_IN" {
		httpx.WriteError(w, 409, "Visit cannot be updated")
		return
	}
	if in.ExpectedVersion > 0 && in.ExpectedVersion != version {
		httpx.WriteJSON(w, 409, map[string]any{"success": false, "message": "Visit changed", "code": "STALE_VISIT"})
		return
	}
	if in.TableID != nil {
		var ok int
		if err = tx.QueryRowContext(r.Context(), `SELECT 1 FROM restaurant_tables WHERE restaurant_id=? AND id=? AND is_active=1 FOR UPDATE`, a.ActiveRestaurantID, *in.TableID).Scan(&ok); err != nil {
			httpx.WriteError(w, 404, "Table not found")
			return
		}
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE pos_visits SET table_id=?,covers=?,version=version+1 WHERE restaurant_id=? AND id=?`, in.TableID, in.Covers, a.ActiveRestaurantID, visitID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			httpx.WriteJSON(w, 409, map[string]any{"success": false, "message": "Table is occupied", "code": "TABLE_OCCUPIED"})
			return
		}
		httpx.WriteError(w, 500, "Error updating visit")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error updating visit")
		return
	}
	s.broadcastBOTablesEvent(a.ActiveRestaurantID, "pos_visit_updated", map[string]any{"visitId": visitID, "tableId": in.TableID})
	httpx.WriteJSON(w, 200, map[string]any{"success": true})
}
func (s *Server) handleBOPOSVisitCancel(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	visitID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error cancelling visit")
		return
	}
	defer tx.Rollback()
	var paid int
	if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM pos_tickets WHERE restaurant_id=? AND visit_id=? AND status NOT IN ('OPEN','VOIDED')`, a.ActiveRestaurantID, visitID).Scan(&paid); err != nil || paid > 0 {
		httpx.WriteError(w, 409, "Paid visit cannot be cancelled")
		return
	}
	// Real-time stock restoration when stock_mode is LIVE (before cancelling)
	settings, settingsErr := s.loadPOSSettings(r.Context(), a.ActiveRestaurantID)
	if settingsErr == nil && settings.StockMode == "LIVE" {
		_ = s.restoreStockForVisit(r.Context(), tx, a.ActiveRestaurantID, a.User.ID, visitID, "pos-visit-cancel:"+strconv.FormatInt(visitID, 10))
	}
	var tableID sql.NullInt64
	res, err := tx.ExecContext(r.Context(), `UPDATE pos_visits SET status='CANCELLED',closed_by=?,closed_at=NOW(),version=version+1 WHERE restaurant_id=? AND id=? AND status='OPEN'`, a.User.ID, a.ActiveRestaurantID, visitID)
	if err != nil {
		httpx.WriteError(w, 500, "Error cancelling visit")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, 404, "Open visit not found")
		return
	}
	_ = tx.QueryRowContext(r.Context(), `SELECT table_id FROM pos_visits WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, visitID).Scan(&tableID)
	_, _ = tx.ExecContext(r.Context(), `UPDATE pos_tickets SET status='VOIDED',voided_at=NOW(),closed_by=? WHERE restaurant_id=? AND visit_id=? AND status='OPEN'`, a.User.ID, a.ActiveRestaurantID, visitID)
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error cancelling visit")
		return
	}
	s.broadcastBOTablesEvent(a.ActiveRestaurantID, "pos_visit_cancelled", map[string]any{"visitId": visitID, "tableId": stockNullableDBInt(tableID)})
	httpx.WriteJSON(w, 200, map[string]any{"success": true})
}

func (s *Server) handleBOPOSVisitTicketCreate(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	visitID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.IdempotencyKey) == "" {
		httpx.WriteError(w, 400, "Idempotency key is required")
		return
	}
	settings, _ := s.loadPOSSettings(r.Context(), a.ActiveRestaurantID)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error creating ticket")
		return
	}
	defer tx.Rollback()
	var date, status string
	if err = tx.QueryRowContext(r.Context(), `SELECT service_date,status FROM pos_visits WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, visitID).Scan(&date, &status); err != nil || status != "OPEN" {
		httpx.WriteError(w, 409, "Visit is not open")
		return
	}
	date = normalizePOSDate(date)
	number, err := nextPOSTicketNumber(r.Context(), tx, a.ActiveRestaurantID, date, settings.ReceiptPrefix)
	if err != nil {
		httpx.WriteError(w, 500, "Error creating ticket")
		return
	}
	res, err := tx.ExecContext(r.Context(), `INSERT INTO pos_tickets (restaurant_id,visit_id,ticket_number,creation_idempotency_key,opened_by) VALUES (?,?,?,?,?)`, a.ActiveRestaurantID, visitID, number, in.IdempotencyKey, a.User.ID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			var existingID int64
			if duplicateErr := tx.QueryRowContext(r.Context(), `SELECT id FROM pos_tickets WHERE restaurant_id=? AND creation_idempotency_key=?`, a.ActiveRestaurantID, in.IdempotencyKey).Scan(&existingID); duplicateErr == nil {
				tx.Rollback()
				ticket, _ := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, existingID)
				httpx.WriteJSON(w, 200, map[string]any{"success": true, "duplicate": true, "ticket": ticket})
				return
			}
		}
		httpx.WriteError(w, 500, "Error creating ticket")
		return
	}
	id, _ := res.LastInsertId()
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error creating ticket")
		return
	}
	ticket, _ := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, id)
	httpx.WriteJSON(w, 201, map[string]any{"success": true, "ticket": ticket})
}

func (s *Server) handleBOPOSLinePatch(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	lineID, _ := strconv.ParseInt(chi.URLParam(r, "lineId"), 10, 64)
	var in struct {
		Quantity float64 `json:"quantity"`
		// Notes is a pointer so omitting it preserves an existing Comentario;
		// sending "" explicitly clears it.
		Notes           *string `json:"notes"`
		ExpectedVersion int     `json:"expectedVersion"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || in.Quantity <= 0 {
		httpx.WriteError(w, 400, "Invalid ticket line")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error updating line")
		return
	}
	defer tx.Rollback()
	var status string
	var version int
	var ticketDiscount int64
	if err = tx.QueryRowContext(r.Context(), `SELECT status,version,ticket_discount_cents FROM pos_tickets WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, ticketID).Scan(&status, &version, &ticketDiscount); err != nil || status != "OPEN" {
		httpx.WriteError(w, 409, "Ticket is not open")
		return
	}
	if in.ExpectedVersion > 0 && version != in.ExpectedVersion {
		httpx.WriteJSON(w, 409, map[string]any{"success": false, "message": "Ticket changed", "code": "STALE_TICKET"})
		return
	}
	// Get current quantity and product ID for stock adjustment
	var oldQuantity float64
	var productID sql.NullInt64
	if err = tx.QueryRowContext(r.Context(), `SELECT quantity, pos_product_id FROM pos_ticket_lines WHERE restaurant_id=? AND ticket_id=? AND id=? AND status='ACTIVE' FOR UPDATE`, a.ActiveRestaurantID, ticketID, lineID).Scan(&oldQuantity, &productID); err != nil {
		httpx.WriteError(w, 404, "Ticket line not found")
		return
	}
	var res sql.Result
	if in.Notes != nil {
		res, err = tx.ExecContext(r.Context(), `UPDATE pos_ticket_lines SET quantity=?,line_total_gross_cents=ROUND(?*unit_price_gross_cents),notes=? WHERE restaurant_id=? AND ticket_id=? AND id=? AND status='ACTIVE'`, in.Quantity, in.Quantity, stockNullableString(*in.Notes), a.ActiveRestaurantID, ticketID, lineID)
	} else {
		res, err = tx.ExecContext(r.Context(), `UPDATE pos_ticket_lines SET quantity=?,line_total_gross_cents=ROUND(?*unit_price_gross_cents) WHERE restaurant_id=? AND ticket_id=? AND id=? AND status='ACTIVE'`, in.Quantity, in.Quantity, a.ActiveRestaurantID, ticketID, lineID)
	}
	if err != nil {
		httpx.WriteError(w, 500, "Error updating line")
		return
	}
	// A comped line stays free after a quantity change: re-clamp its discount to
	// the new gross so it never exceeds it (which would fail totals validation).
	if _, err = tx.ExecContext(r.Context(), `UPDATE pos_ticket_lines SET discount_cents=ROUND(quantity*unit_price_gross_cents) WHERE restaurant_id=? AND ticket_id=? AND id=? AND status='ACTIVE' AND comped_at IS NOT NULL`, a.ActiveRestaurantID, ticketID, lineID); err != nil {
		httpx.WriteError(w, 500, "Error updating comped line")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, 404, "Ticket line not found")
		return
	}
	// Real-time stock adjustment when stock_mode is LIVE
	if productID.Valid && oldQuantity != in.Quantity {
		settings, settingsErr := s.loadPOSSettings(r.Context(), a.ActiveRestaurantID)
		if settingsErr == nil && settings.StockMode == "LIVE" {
			idempotencyKey := "pos-line-patch:" + strconv.FormatInt(ticketID, 10) + ":" + strconv.FormatInt(lineID, 10) + ":" + strconv.FormatInt(time.Now().UnixNano(), 10)
			_ = s.adjustStockForQuantityChange(r.Context(), tx, a.ActiveRestaurantID, a.User.ID, ticketID, lineID, productID.Int64, oldQuantity, in.Quantity, idempotencyKey)
		}
	}
	if _, err = s.recalculatePOSTicket(r.Context(), tx, a.ActiveRestaurantID, ticketID, ticketDiscount); err != nil {
		httpx.WriteError(w, 500, "Error calculating ticket")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error updating line")
		return
	}
	ticket, _ := s.loadPOSTicket(r.Context(), a.ActiveRestaurantID, ticketID)
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "ticket": ticket})
}

func (s *Server) handleBOPOSShiftCurrent(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var id int64
	var terminal, status string
	var opening int64
	var opened time.Time
	err := s.db.QueryRowContext(r.Context(), `SELECT id,terminal_key,status,opening_cash_cents,opened_at FROM pos_shifts WHERE restaurant_id=? AND status='OPEN' ORDER BY opened_at DESC LIMIT 1`, a.ActiveRestaurantID).Scan(&id, &terminal, &status, &opening, &opened)
	if err == sql.ErrNoRows {
		httpx.WriteJSON(w, 200, map[string]any{"success": true, "shift": nil})
		return
	}
	if err != nil {
		httpx.WriteError(w, 500, "Error loading shift")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "shift": map[string]any{"id": id, "terminalKey": terminal, "status": status, "openingCashCents": opening, "openedAt": opened}})
}
func (s *Server) handleBOPOSShiftOpen(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var in struct {
		TerminalKey      string `json:"terminalKey"`
		OpeningCashCents int64  `json:"openingCashCents"`
		Notes            string `json:"notes"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || in.OpeningCashCents < 0 {
		httpx.WriteError(w, 400, "Invalid shift")
		return
	}
	if strings.TrimSpace(in.TerminalKey) == "" {
		in.TerminalKey = "main"
	}
	// A shift belongs to the cash day that is open when the drawer is claimed.
	// Without this link the day close cannot see the shift's cash movements and
	// would compute an expected cash figure that ignores every payout and drop.
	cashDayID, err := s.currentPOSCashDayID(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, 500, "Error resolving cash day")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO pos_shifts (restaurant_id,cash_day_id,terminal_key,opened_by,opening_cash_cents,notes) VALUES (?,?,?,?,?,?)`, a.ActiveRestaurantID, cashDayID, strings.TrimSpace(in.TerminalKey), a.User.ID, in.OpeningCashCents, stockNullableString(in.Notes))
	if err != nil {
		httpx.WriteJSON(w, 409, map[string]any{"success": false, "message": "Terminal already has an open shift", "code": "SHIFT_ALREADY_OPEN"})
		return
	}
	id, _ := res.LastInsertId()
	httpx.WriteJSON(w, 201, map[string]any{"success": true, "id": id})
}
func (s *Server) handleBOPOSShiftClose(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		CountedCashCents int64  `json:"countedCashCents"`
		Notes            string `json:"notes"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || in.CountedCashCents < 0 {
		httpx.WriteError(w, 400, "Invalid shift close")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, 500, "Error closing shift")
		return
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRowContext(r.Context(), `SELECT status FROM pos_shifts WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, id).Scan(&status); err != nil || status != "OPEN" {
		httpx.WriteError(w, 409, "Shift is not open")
		return
	}
	summary, summaryErr := s.loadPOSCashSummary(r.Context(), tx, a.ActiveRestaurantID, id)
	if summaryErr != nil {
		httpx.WriteError(w, 500, "Error calculating cash")
		return
	}
	expected := summary.ExpectedCash
	difference := in.CountedCashCents - expected
	if _, err = tx.ExecContext(r.Context(), `UPDATE pos_shifts SET status='CLOSED',closed_by=?,closing_cash_counted_cents=?,expected_cash_cents=?,closed_at=NOW(),notes=COALESCE(?,notes) WHERE restaurant_id=? AND id=?`, a.User.ID, in.CountedCashCents, expected, stockNullableString(in.Notes), a.ActiveRestaurantID, id); err != nil {
		httpx.WriteError(w, 500, "Error closing shift")
		return
	}
	legacyKey := "legacy-shift-close:" + strconv.FormatInt(id, 10)
	_, err = tx.ExecContext(r.Context(), `INSERT INTO pos_cash_closures (restaurant_id,shift_id,terminal_key,closure_type,opened_at,closed_at,opening_cash_cents,sales_gross_cents,refunds_cents,discounts_cents,surcharges_cents,tips_cents,cash_sales_cents,cash_tips_cents,card_sales_cents,card_tips_cents,bank_sales_cents,bank_tips_cents,other_sales_cents,other_tips_cents,cash_refunds_cents,cash_in_cents,cash_out_cents,expected_cash_cents,counted_cash_cents,difference_cents,ticket_count,voided_ticket_count,covers,open_visit_count,open_ticket_count,note,discrepancy_reason,idempotency_key,created_by) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, a.ActiveRestaurantID, id, summary.TerminalKey, "Z", summary.OpenedAt, time.Now(), summary.OpeningCash, summary.SalesGross, summary.Refunds, summary.Discounts, summary.Surcharges, summary.Tips, summary.CashSales, summary.CashTips, summary.CardSales, summary.CardTips, summary.BankSales, summary.BankTips, summary.OtherSales, summary.OtherTips, summary.CashRefunds, summary.CashIn, summary.CashOut, expected, in.CountedCashCents, difference, summary.TicketCount, summary.VoidedTicketCount, summary.Covers, summary.OpenVisitCount, summary.OpenTicketCount, stockNullableString(in.Notes), func() any {
		if difference != 0 {
			return "Legacy shift close"
		}
		return nil
	}(), legacyKey, a.User.ID)
	if err != nil {
		httpx.WriteError(w, 500, "Error closing shift")
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, 500, "Error closing shift")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "expectedCashCents": expected, "differenceCents": difference})
}
