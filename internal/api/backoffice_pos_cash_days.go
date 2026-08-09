package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

// A cash day (jornada de caja) is the restaurant-wide till session for one
// business date. It answers the question pos_shifts cannot: "is the till open
// today for this restaurant?", regardless of how many terminals are running.
//
// Closing a cash day is a Z closure. The handler reuses loadPOSCashSummary's
// accounting through a day-scoped posCashScope and writes the same
// pos_cash_closures snapshot, so there is exactly one implementation of the
// money rules.

type posCashDay struct {
	ID           int64
	BusinessDate string
	Status       string
	OpenedBy     int64
	OpenedByName string
	ClosedBy     sql.NullInt64
	ClosedByName string
	OpeningCash  int64
	OpenedAt     time.Time
	ClosedAt     sql.NullTime
	ForcedOpen   bool
	Notes        sql.NullString
}

func (d posCashDay) asMap() map[string]any {
	closedAt := any(nil)
	if d.ClosedAt.Valid {
		closedAt = d.ClosedAt.Time
	}
	closedBy := any(nil)
	if d.ClosedBy.Valid {
		closedBy = d.ClosedBy.Int64
	}
	return map[string]any{
		"id": d.ID, "date": d.BusinessDate, "status": d.Status,
		"openedBy": d.OpenedBy, "openedByName": d.OpenedByName,
		"closedBy": closedBy, "closedByName": d.ClosedByName,
		"openingCashCents": d.OpeningCash,
		"openedAt":         d.OpenedAt, "closedAt": closedAt,
		"forcedOpen": d.ForcedOpen, "notes": posCashNullableString(d.Notes),
	}
}

const posCashDaySelect = `SELECT d.id,d.business_date,d.status,d.opened_by,COALESCE(ou.name,''),d.closed_by,COALESCE(cu.name,''),d.opening_cash_cents,d.opened_at,d.closed_at,d.forced_open,d.notes
	FROM pos_cash_days d
	LEFT JOIN bo_users ou ON ou.id=d.opened_by
	LEFT JOIN bo_users cu ON cu.id=d.closed_by`

func scanPOSCashDay(scan func(dest ...any) error) (posCashDay, error) {
	var d posCashDay
	var businessDate time.Time
	err := scan(&d.ID, &businessDate, &d.Status, &d.OpenedBy, &d.OpenedByName, &d.ClosedBy, &d.ClosedByName, &d.OpeningCash, &d.OpenedAt, &d.ClosedAt, &d.ForcedOpen, &d.Notes)
	d.BusinessDate = businessDate.Format("2006-01-02")
	return d, err
}

// posValidBusinessDate accepts only a strict YYYY-MM-DD calendar date, so a
// malformed query string can never reach a query as a silent empty filter.
func posValidBusinessDate(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return "", false
	}
	return parsed.Format("2006-01-02"), true
}

// posResolveBusinessDate returns the requested ?date= when present, otherwise
// the derived business date from the cutoff settings.
func (s *Server) posResolveBusinessDate(ctx context.Context, restaurantID int, requested string) (string, error) {
	if requested != "" {
		date, ok := posValidBusinessDate(requested)
		if !ok {
			return "", fmt.Errorf("invalid date")
		}
		return date, nil
	}
	settings, err := s.loadPOSSettings(ctx, restaurantID)
	if err != nil {
		return "", err
	}
	moment, err := s.loadPOSBusinessMoment(ctx, restaurantID, settings)
	if err != nil {
		return "", err
	}
	return normalizePOSDate(moment.ServiceDate), nil
}

// currentPOSCashDayID returns the id of the OPEN cash day for the current
// business date, or NULL when none exists. It never fails the caller for a
// missing day: opening a shift before a cash day is legal today and the guard
// that changes that belongs to the request gate, not here.
func (s *Server) currentPOSCashDayID(ctx context.Context, restaurantID int) (any, error) {
	date, err := s.posResolveBusinessDate(ctx, restaurantID, "")
	if err != nil {
		return nil, err
	}
	var id int64
	err = s.db.QueryRowContext(ctx, `SELECT id FROM pos_cash_days WHERE restaurant_id=? AND business_date=? AND status='OPEN'`, restaurantID, date).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return id, nil
}

func (s *Server) loadPOSCashDayByDate(ctx context.Context, q posCashQueryer, restaurantID int, date string) (posCashDay, error) {
	return scanPOSCashDay(q.QueryRowContext(ctx, posCashDaySelect+` WHERE d.restaurant_id=? AND d.business_date=?`, restaurantID, date).Scan)
}

func (s *Server) loadPOSCashDayByID(ctx context.Context, q posCashQueryer, restaurantID int, id int64) (posCashDay, error) {
	return scanPOSCashDay(q.QueryRowContext(ctx, posCashDaySelect+` WHERE d.restaurant_id=? AND d.id=?`, restaurantID, id).Scan)
}

// loadPOSUnclosedPreviousDays lists cash days before `date` that are still OPEN,
// oldest first, each with its takings and covers so the UI can render the alert
// cards without a second round trip.
func (s *Server) loadPOSUnclosedPreviousDays(ctx context.Context, restaurantID int, date string) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, posCashDaySelect+` WHERE d.restaurant_id=? AND d.status='OPEN' AND d.business_date<? ORDER BY d.business_date ASC LIMIT 60`, restaurantID, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	days := []posCashDay{}
	for rows.Next() {
		day, scanErr := scanPOSCashDay(rows.Scan)
		if scanErr != nil {
			return nil, scanErr
		}
		days = append(days, day)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	dates := make([]string, 0, len(days))
	for _, day := range days {
		dates = append(dates, day.BusinessDate)
	}
	totals, err := s.loadPOSCashDayTotals(ctx, restaurantID, dates)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(days))
	for _, day := range days {
		items = append(items, withPOSCashDayTotals(day.asMap(), totals[day.BusinessDate]))
	}
	return items, nil
}

// withPOSCashDayTotals merges a day's headline figures into its serialized form,
// defaulting to zeros for a day that has no activity at all.
func withPOSCashDayTotals(day map[string]any, totals map[string]any) map[string]any {
	if totals == nil {
		totals = map[string]any{"totalGrossCents": int64(0), "ticketCount": int64(0), "covers": int64(0)}
	}
	for key, value := range totals {
		day[key] = value
	}
	return day
}

// loadPOSCashDayTotals is the single source of truth for a day's headline
// figures: net takings across every table and the guest count. It mirrors the
// sales report (net = gross - refunded) and rebuildPOSAffluenceKey (closed
// dine-in covers plus signed adjustments) so the numbers agree everywhere.
// It resolves every requested date in three grouped queries rather than three
// per date, because the unclosed-days alert can ask for weeks at once and this
// runs on the sell screen's polling path.
func (s *Server) loadPOSCashDayTotals(ctx context.Context, restaurantID int, dates []string) (map[string]map[string]any, error) {
	out := map[string]map[string]any{}
	if len(dates) == 0 {
		return out, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(dates)), ",")
	args := make([]any, 0, 1+len(dates))
	args = append(args, restaurantID)
	for _, date := range dates {
		args = append(args, date)
		out[date] = map[string]any{"totalGrossCents": int64(0), "ticketCount": int64(0), "covers": int64(0)}
	}
	covers := map[string]int64{}

	collect := func(query string, apply func(date string, a, b int64)) error {
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var day time.Time
			var first, second int64
			if err = rows.Scan(&day, &first, &second); err != nil {
				return err
			}
			apply(day.Format("2006-01-02"), first, second)
		}
		return rows.Err()
	}

	if err := collect(`
		SELECT v.service_date,COALESCE(SUM(t.total_gross_cents-t.refunded_cents),0),COUNT(*)
		FROM pos_tickets t
		JOIN pos_visits v ON v.restaurant_id=t.restaurant_id AND v.id=t.visit_id
		WHERE t.restaurant_id=? AND v.service_date IN (`+placeholders+`) AND t.status IN ('PAID','PARTIALLY_REFUNDED','REFUNDED')
		GROUP BY v.service_date`, func(date string, gross, count int64) {
		if entry, ok := out[date]; ok {
			entry["totalGrossCents"], entry["ticketCount"] = gross, count
		}
	}); err != nil {
		return nil, err
	}
	if err := collect(`
		SELECT v.service_date,COALESCE(SUM(v.covers),0),0
		FROM pos_visits v
		WHERE v.restaurant_id=? AND v.service_date IN (`+placeholders+`) AND v.status='CLOSED' AND v.channel='DINE_IN'
		GROUP BY v.service_date`, func(date string, seated, _ int64) {
		covers[date] += seated
	}); err != nil {
		return nil, err
	}
	if err := collect(`
		SELECT service_date,COALESCE(SUM(delta_covers),0),0
		FROM pos_cover_adjustments
		WHERE restaurant_id=? AND service_date IN (`+placeholders+`)
		GROUP BY service_date`, func(date string, delta, _ int64) {
		covers[date] += delta
	}); err != nil {
		return nil, err
	}
	for date, total := range covers {
		if total < 0 {
			total = 0
		}
		if entry, ok := out[date]; ok {
			entry["covers"] = total
		}
	}
	return out, nil
}

func (s *Server) handleBOPOSCashDayCurrent(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	date, err := s.posResolveBusinessDate(r.Context(), a.ActiveRestaurantID, r.URL.Query().Get("date"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid date")
		return
	}
	payload := map[string]any{"success": true, "date": date, "cashDay": nil}
	day, err := s.loadPOSCashDayByDate(r.Context(), s.db, a.ActiveRestaurantID, date)
	if err == nil {
		totals, totalsErr := s.loadPOSCashDayTotals(r.Context(), a.ActiveRestaurantID, []string{date})
		if totalsErr != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error loading cash day totals")
			return
		}
		payload["cashDay"] = withPOSCashDayTotals(day.asMap(), totals[date])
	} else if !errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading cash day")
		return
	}
	unclosed, err := s.loadPOSUnclosedPreviousDays(r.Context(), a.ActiveRestaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading unclosed cash days")
		return
	}
	payload["unclosedPrevious"] = unclosed
	httpx.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) handleBOPOSCashDayOpen(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var in struct {
		Date             string `json:"date"`
		OpeningCashCents int64  `json:"openingCashCents"`
		Force            bool   `json:"force"`
		Notes            string `json:"notes"`
	}
	if !posDecodeBody(w, r, &in) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid cash day")
		return
	}
	in.Notes = strings.TrimSpace(in.Notes)
	if in.OpeningCashCents < 0 || len(in.Notes) > 500 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid cash day")
		return
	}
	date, err := s.posResolveBusinessDate(r.Context(), a.ActiveRestaurantID, in.Date)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid date")
		return
	}
	// A day ahead of the current business date can never be traded, and once
	// created it would sit there blocking every later open as an "unclosed
	// previous day". A mistyped year must not be able to wedge the till.
	current, err := s.posResolveBusinessDate(r.Context(), a.ActiveRestaurantID, "")
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error resolving business date")
		return
	}
	if date > current {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "code": "FUTURE_CASH_DAY", "message": "A cash day cannot be opened in the future"})
		return
	}
	if !in.Force {
		unclosed, unclosedErr := s.loadPOSUnclosedPreviousDays(r.Context(), a.ActiveRestaurantID, date)
		if unclosedErr != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error loading unclosed cash days")
			return
		}
		if len(unclosed) > 0 {
			httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "code": "UNCLOSED_PREVIOUS_DAYS", "message": "There are earlier cash days still open", "unclosedPrevious": unclosed})
			return
		}
	}
	forced := 0
	if in.Force {
		forced = 1
	}
	// uq_pos_cash_days_date makes this insert the arbiter between terminals
	// racing to open the same day: exactly one wins, the rest get 409.
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO pos_cash_days (restaurant_id,business_date,opened_by,opening_cash_cents,forced_open,notes) VALUES (?,?,?,?,?,?)`, a.ActiveRestaurantID, date, a.User.ID, in.OpeningCashCents, forced, stockNullableString(in.Notes))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "code": "CASH_DAY_ALREADY_OPEN", "message": "This day already has a cash day"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error opening cash day")
		return
	}
	id, _ := res.LastInsertId()
	// A terminal that claimed its drawer before anyone opened the day has a shift
	// with no cash day, and its movements would be invisible to the close. Adopt
	// those orphans now, otherwise the link can never be established.
	if _, err = s.db.ExecContext(r.Context(), `UPDATE pos_shifts SET cash_day_id=? WHERE restaurant_id=? AND cash_day_id IS NULL AND status='OPEN'`, id, a.ActiveRestaurantID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error linking open shifts to the cash day")
		return
	}
	_, _ = s.db.ExecContext(r.Context(), `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,after_json,actor_user_id) VALUES (?,'cash_day',?,'OPEN',JSON_OBJECT('date',?,'openingCashCents',?,'forced',?),?)`, a.ActiveRestaurantID, id, date, in.OpeningCashCents, forced, a.User.ID)
	day, err := s.loadPOSCashDayByID(r.Context(), s.db, a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading cash day")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"success": true, "cashDay": day.asMap()})
}

func (s *Server) handleBOPOSCashDayClose(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var in struct {
		CountedCashCents  *int64 `json:"countedCashCents"`
		Notes             string `json:"notes"`
		DiscrepancyReason string `json:"discrepancyReason"`
	}
	if !posDecodeBody(w, r, &in) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid cash day close")
		return
	}
	in.Notes = strings.TrimSpace(in.Notes)
	in.DiscrepancyReason = strings.TrimSpace(in.DiscrepancyReason)
	if id <= 0 || len(in.Notes) > 500 || len(in.DiscrepancyReason) > 500 || (in.CountedCashCents != nil && *in.CountedCashCents < 0) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid cash day close")
		return
	}
	if in.CountedCashCents == nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "code": "COUNTED_CASH_REQUIRED", "message": "Closing a cash day requires counted cash"})
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error closing cash day")
		return
	}
	defer tx.Rollback()

	var businessDate time.Time
	var status string
	var openingCash int64
	var openedAt time.Time
	if err = tx.QueryRowContext(r.Context(), `SELECT business_date,status,opening_cash_cents,opened_at FROM pos_cash_days WHERE restaurant_id=? AND id=? FOR UPDATE`, a.ActiveRestaurantID, id).Scan(&businessDate, &status, &openingCash, &openedAt); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Cash day not found")
		return
	}
	if status != "OPEN" {
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "code": "CASH_DAY_ALREADY_CLOSED", "message": "This cash day is already closed"})
		return
	}
	date := businessDate.Format("2006-01-02")

	summary := posCashSummary{TerminalKey: "day-" + date, Status: status, OpenedAt: openedAt, OpeningCash: openingCash}
	summary, err = s.loadPOSCashSummaryScoped(r.Context(), tx, a.ActiveRestaurantID, posCashDayScope(id, date), summary)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error calculating cash day")
		return
	}
	if summary.OpenVisitCount > 0 || summary.OpenTicketCount > 0 {
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "code": "OPEN_POS_ITEMS", "message": "Close all open visits and tickets before closing the cash day", "openVisitCount": summary.OpenVisitCount, "openTicketCount": summary.OpenTicketCount})
		return
	}
	difference := *in.CountedCashCents - summary.ExpectedCash
	if difference != 0 && in.DiscrepancyReason == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "code": "DISCREPANCY_REASON_REQUIRED", "message": "A cash discrepancy requires a reason", "expectedCashCents": summary.ExpectedCash, "differenceCents": difference})
		return
	}
	closedAt := time.Now()
	idempotencyKey := "cash-day-close:" + strconv.FormatInt(id, 10)
	res, err := tx.ExecContext(r.Context(), `INSERT INTO pos_cash_closures (restaurant_id,cash_day_id,shift_id,terminal_key,closure_type,opened_at,closed_at,opening_cash_cents,sales_gross_cents,refunds_cents,discounts_cents,surcharges_cents,tips_cents,cash_sales_cents,cash_tips_cents,card_sales_cents,card_tips_cents,bank_sales_cents,bank_tips_cents,other_sales_cents,other_tips_cents,cash_refunds_cents,cash_in_cents,cash_out_cents,expected_cash_cents,counted_cash_cents,difference_cents,ticket_count,voided_ticket_count,covers,open_visit_count,open_ticket_count,note,discrepancy_reason,idempotency_key,created_by) VALUES (?,?,NULL,?,'Z',?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ActiveRestaurantID, id, summary.TerminalKey, summary.OpenedAt, closedAt, summary.OpeningCash, summary.SalesGross, summary.Refunds, summary.Discounts, summary.Surcharges, summary.Tips, summary.CashSales, summary.CashTips, summary.CardSales, summary.CardTips, summary.BankSales, summary.BankTips, summary.OtherSales, summary.OtherTips, summary.CashRefunds, summary.CashIn, summary.CashOut, summary.ExpectedCash, *in.CountedCashCents, difference, summary.TicketCount, summary.VoidedTicketCount, summary.Covers, summary.OpenVisitCount, summary.OpenTicketCount, stockNullableString(in.Notes), stockNullableString(in.DiscrepancyReason), idempotencyKey, a.User.ID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			httpx.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "code": "CASH_DAY_ALREADY_CLOSED", "message": "This cash day is already closed"})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error recording cash day closure")
		return
	}
	closureID, _ := res.LastInsertId()
	if _, err = tx.ExecContext(r.Context(), `UPDATE pos_cash_days SET status='CLOSED',closed_by=?,closed_at=?,notes=COALESCE(?,notes) WHERE restaurant_id=? AND id=?`, a.User.ID, closedAt, stockNullableString(in.Notes), a.ActiveRestaurantID, id); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error closing cash day")
		return
	}
	// Terminal shifts belonging to this day cannot outlive it, otherwise the next
	// day would inherit an open drawer.
	if _, err = tx.ExecContext(r.Context(), `UPDATE pos_shifts SET status='CLOSED',closed_by=?,closed_at=? WHERE restaurant_id=? AND cash_day_id=? AND status='OPEN'`, a.User.ID, closedAt, a.ActiveRestaurantID, id); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error closing cash day shifts")
		return
	}
	_, _ = tx.ExecContext(r.Context(), `INSERT INTO pos_audit_events (restaurant_id,entity_type,entity_id,action,after_json,actor_user_id) VALUES (?,'cash_day',?,'CLOSE',JSON_OBJECT('date',?,'expectedCashCents',?,'countedCashCents',?,'differenceCents',?),?)`, a.ActiveRestaurantID, id, date, summary.ExpectedCash, *in.CountedCashCents, difference, a.User.ID)
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error closing cash day")
		return
	}
	day, err := s.loadPOSCashDayByID(r.Context(), s.db, a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading cash day")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "cashDay": day.asMap(), "closureId": closureID, "summary": summary.asMap(), "countedCashCents": *in.CountedCashCents, "differenceCents": difference})
}
