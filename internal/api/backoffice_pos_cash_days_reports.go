package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

// Read-only views over cash days: the month grid of the POS calendar modal and
// the per-table breakdown of a single day. Both reuse loadPOSCashDayTotals so
// the takings and covers shown here cannot drift from the sales report or from
// stock_affluence_daily.

// posCashDayRangeLimit caps a range request. The calendar shows one month at a
// time; 92 days covers a quarter and keeps the IN list bounded.
const posCashDayRangeLimit = 92

// posDateRange expands an inclusive from..to span into calendar dates.
func posDateRange(from, to string) ([]string, error) {
	start, ok := posValidBusinessDate(from)
	if !ok {
		return nil, errors.New("invalid from")
	}
	end, ok := posValidBusinessDate(to)
	if !ok {
		return nil, errors.New("invalid to")
	}
	startAt, _ := time.Parse("2006-01-02", start)
	endAt, _ := time.Parse("2006-01-02", end)
	if endAt.Before(startAt) {
		return nil, errors.New("to is before from")
	}
	dates := []string{}
	for day := startAt; !day.After(endAt); day = day.AddDate(0, 0, 1) {
		if len(dates) >= posCashDayRangeLimit {
			return nil, errors.New("range too wide")
		}
		dates = append(dates, day.Format("2006-01-02"))
	}
	return dates, nil
}

// handleBOPOSCashDaysRange returns one row per day in the range that has either
// a cash day or POS activity, so the calendar can distinguish "closed with
// takings" from "nothing happened" from "open and still trading".
func (s *Server) handleBOPOSCashDaysRange(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	dates, err := posDateRange(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid date range")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), posCashDaySelect+` WHERE d.restaurant_id=? AND d.business_date BETWEEN ? AND ? ORDER BY d.business_date ASC`, a.ActiveRestaurantID, dates[0], dates[len(dates)-1])
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading cash days")
		return
	}
	defer rows.Close()
	days := map[string]posCashDay{}
	for rows.Next() {
		day, scanErr := scanPOSCashDay(rows.Scan)
		if scanErr != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading cash days")
			return
		}
		days[day.BusinessDate] = day
	}
	if err = rows.Err(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reading cash days")
		return
	}
	totals, err := s.loadPOSCashDayTotals(r.Context(), a.ActiveRestaurantID, dates)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading cash day totals")
		return
	}
	data := []map[string]any{}
	for _, date := range dates {
		dayTotals := totals[date]
		day, hasDay := days[date]
		// A date with neither a cash day nor activity is simply absent, so the
		// calendar can render it as a blank cell instead of a zero-takings day.
		if !hasDay && !posCashDayHasActivity(dayTotals) {
			continue
		}
		entry := map[string]any{"date": date, "status": nil, "openedAt": nil, "closedAt": nil,
			"openedByName": "", "closedByName": "", "openingCashCents": int64(0), "forcedOpen": false}
		if hasDay {
			entry = day.asMap()
		}
		data = append(data, withPOSCashDayTotals(entry, dayTotals))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

func posCashDayHasActivity(totals map[string]any) bool {
	if totals == nil {
		return false
	}
	for _, key := range []string{"ticketCount", "covers", "totalGrossCents"} {
		if value, ok := totals[key].(int64); ok && value != 0 {
			return true
		}
	}
	return false
}

// handleBOPOSCashDayTables breaks a single day down by table, visit and ticket
// for the calendar's detail panel.
func (s *Server) handleBOPOSCashDayTables(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	date, ok := posValidBusinessDate(chi.URLParam(r, "date"))
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid date")
		return
	}
	// A day is read-only unless its cash day exists and is still open. A date
	// that was never opened is history too, so it is read-only as well.
	readOnly := true
	day, err := s.loadPOSCashDayByDate(r.Context(), s.db, a.ActiveRestaurantID, date)
	if err == nil {
		readOnly = day.Status != "OPEN"
	} else if !errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading cash day")
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT v.id,v.table_id,COALESCE(rt.name,''),v.status,v.covers,v.channel,v.opened_at,v.closed_at,
		       t.id,COALESCE(t.ticket_number,''),COALESCE(t.status,''),COALESCE(t.total_gross_cents,0),COALESCE(t.refunded_cents,0)
		FROM pos_visits v
		LEFT JOIN restaurant_tables rt ON rt.restaurant_id=v.restaurant_id AND rt.id=v.table_id
		LEFT JOIN pos_tickets t ON t.restaurant_id=v.restaurant_id AND t.visit_id=v.id
		WHERE v.restaurant_id=? AND v.service_date=?
		ORDER BY COALESCE(rt.display_order,0),v.table_id,v.opened_at,v.id,t.id`, a.ActiveRestaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading day tables")
		return
	}
	defer rows.Close()

	type visitAcc struct {
		payload map[string]any
		tickets []map[string]any
		total   int64
	}
	type tableAcc struct {
		payload map[string]any
		visits  []*visitAcc
		byVisit map[int64]*visitAcc
		total   int64
		covers  int64
	}
	order := []int64{}
	tables := map[int64]*tableAcc{}

	for rows.Next() {
		var visitID int64
		var tableID sql.NullInt64
		var tableName, visitStatus, channel string
		var covers int
		var openedAt time.Time
		var closedAt sql.NullTime
		var ticketID sql.NullInt64
		var ticketNumber, ticketStatus string
		var ticketGross, ticketRefunded int64
		if err = rows.Scan(&visitID, &tableID, &tableName, &visitStatus, &covers, &channel, &openedAt, &closedAt,
			&ticketID, &ticketNumber, &ticketStatus, &ticketGross, &ticketRefunded); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading day tables")
			return
		}
		// Takeaway and delivery have no table; they are grouped under key 0 so
		// the day's takings still add up in the detail panel.
		key := int64(0)
		if tableID.Valid {
			key = tableID.Int64
		}
		table, seen := tables[key]
		if !seen {
			// A null tableId is signal enough for the UI to label the tableless
			// group itself, so no user-facing string is invented here.
			table = &tableAcc{
				payload: map[string]any{"tableId": stockNullableDBInt(tableID), "tableName": tableName},
				byVisit: map[int64]*visitAcc{},
			}
			tables[key] = table
			order = append(order, key)
		}
		visit, seenVisit := table.byVisit[visitID]
		if !seenVisit {
			visit = &visitAcc{payload: map[string]any{
				"visitId": visitID, "status": visitStatus, "covers": covers, "channel": channel,
				"openedAt": openedAt, "closedAt": posNullableTime(closedAt),
			}}
			table.byVisit[visitID] = visit
			table.visits = append(table.visits, visit)
			if visitStatus == "CLOSED" && channel == "DINE_IN" {
				table.covers += int64(covers)
			}
		}
		if !ticketID.Valid {
			continue
		}
		// Net of refunds, matching the sales report and the day totals.
		net := ticketGross - ticketRefunded
		visit.tickets = append(visit.tickets, map[string]any{
			"id": ticketID.Int64, "ticketNumber": ticketNumber, "status": ticketStatus,
			"totalGrossCents": net, "refundedCents": ticketRefunded,
		})
		if ticketStatus == "PAID" || ticketStatus == "PARTIALLY_REFUNDED" || ticketStatus == "REFUNDED" {
			visit.total += net
			table.total += net
		}
	}
	if err = rows.Err(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reading day tables")
		return
	}

	out := make([]map[string]any, 0, len(order))
	for _, key := range order {
		table := tables[key]
		visits := make([]map[string]any, 0, len(table.visits))
		for _, visit := range table.visits {
			if visit.tickets == nil {
				visit.tickets = []map[string]any{}
			}
			visit.payload["tickets"] = visit.tickets
			visit.payload["totalGrossCents"] = visit.total
			visits = append(visits, visit.payload)
		}
		table.payload["visits"] = visits
		table.payload["totalGrossCents"] = table.total
		table.payload["covers"] = table.covers
		out = append(out, table.payload)
	}
	// Manual cover adjustments belong to the day, not to any single table, so
	// they are reported apart. Without this the sum of the tables would silently
	// disagree with the covers shown for the same day in the calendar.
	adjustedCovers, err := s.loadPOSCoverAdjustmentTotal(r.Context(), a.ActiveRestaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading cover adjustments")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "date": date, "readOnly": readOnly,
		"tables": out, "adjustedCovers": adjustedCovers})
}

// loadPOSCoverAdjustmentTotal sums the manual cover corrections of a day.
func (s *Server) loadPOSCoverAdjustmentTotal(ctx context.Context, restaurantID int, date string) (int64, error) {
	var total int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(delta_covers),0) FROM pos_cover_adjustments WHERE restaurant_id=? AND service_date=?`, restaurantID, date).Scan(&total)
	return total, err
}

func posNullableTime(value sql.NullTime) any {
	if value.Valid {
		return value.Time
	}
	return nil
}

// posQueryDate reads an optional ?date= filter, reporting whether it was present
// and whether it parsed. Callers return 400 on a malformed value rather than
// silently falling back to today, which would show the wrong day's money.
func posQueryDate(r *http.Request) (string, bool, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("date"))
	if raw == "" {
		return "", false, true
	}
	date, ok := posValidBusinessDate(raw)
	return date, true, ok
}
