package api

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"time"

	"preactvillacarmen/internal/httpx"
)

// posRevenueSnapshot returns the open-ticket revenue for a business date,
// grouped by hour (0-23), plus the running total across the day.
func (s *Server) posRevenueSnapshot(ctx context.Context, restaurantID int, date time.Time) (map[string]any, error) {
	dateISO := date.Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx, `SELECT HOUR(t.opened_at),COALESCE(SUM(t.total_gross_cents),0) FROM pos_tickets t JOIN pos_visits v ON v.restaurant_id=t.restaurant_id AND v.id=t.visit_id WHERE t.restaurant_id=? AND v.service_date=? AND t.status IN ('OPEN','PARTIALLY_REFUNDED') GROUP BY HOUR(t.opened_at)`, restaurantID, dateISO)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byHour := make([]map[string]any, 0, 24)
	total := int64(0)
	for rows.Next() {
		var hour int
		var cents int64
		if err := rows.Scan(&hour, &cents); err != nil {
			return nil, err
		}
		byHour = append(byHour, map[string]any{"hour": hour, "grossCents": cents})
		total += cents
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"date": dateISO, "totalGrossCents": total, "byHour": byHour}, nil
}

// broadcastBOFichajeRevenue emits pos_revenue_updated to the fichaje hub so the
// admin panel can refresh labour-cost income data in real time.
func (s *Server) broadcastBOFichajeRevenue(restaurantID int, date time.Time) {
	if s.fichajeHub == nil || restaurantID <= 0 {
		return
	}
	snapshot, err := s.posRevenueSnapshot(context.Background(), restaurantID, date)
	if err != nil {
		return
	}
	payload := map[string]any{
		"type":            "pos_revenue_updated",
		"restaurantId":    restaurantID,
		"at":              time.Now().In(boMadridTZ).Format(time.RFC3339),
		"date":            date.Format("2006-01-02"),
		"totalGrossCents": snapshot["totalGrossCents"],
		"byHour":          snapshot["byHour"],
	}
	s.fichajeHub.broadcast(restaurantID, payload)
}

// handleBOFichajeHourlyCosts returns each active member's effective hourly cost
// valid on the given business date. Used by the admin panel to compute live
// labour cost of in-progress clock entries.
func (s *Server) handleBOFichajeHourlyCosts(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	date, err := parseBODateQuery(r.URL.Query().Get("date"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "date invalido")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT m.id,CONCAT(m.first_name,' ',m.last_name),c.pay_type,c.gross_amount,c.monthly_hours,c.employer_cost_pct FROM restaurant_members m LEFT JOIN member_compensations c ON c.restaurant_id=m.restaurant_id AND c.restaurant_member_id=m.id AND c.deleted_at IS NULL AND ? BETWEEN c.effective_from AND COALESCE(c.effective_to,'9999-12-31') WHERE m.restaurant_id=? AND m.is_active=1 ORDER BY m.last_name,m.first_name`, date.Format("2006-01-02"), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando costes por hora")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int
		var name string
		var payType sql.NullString
		var gross, monthlyHours, pct sql.NullFloat64
		if err := rows.Scan(&id, &name, &payType, &gross, &monthlyHours, &pct); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo costes por hora")
			return
		}
		hourly := 0.0
		if payType.Valid && gross.Valid && pct.Valid {
			hourly, _ = effectiveHourlyCost(payType.String, gross.Float64, monthlyHours.Float64, pct.Float64)
		}
		items = append(items, map[string]any{"memberId": id, "name": name, "effectiveHourlyCost": hourly})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "date": date.Format("2006-01-02"), "members": items})
}

// handleBOPOSTicketsHourly returns open-ticket revenue for the business date
// grouped by hour of ticket open time.
func (s *Server) handleBOPOSTicketsHourly(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	date, err := parseBODateQuery(r.URL.Query().Get("date"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "date invalido")
		return
	}
	snapshot, err := s.posRevenueSnapshot(r.Context(), a.ActiveRestaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando ingresos por hora")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "date": snapshot["date"], "totalGrossCents": snapshot["totalGrossCents"], "byHour": snapshot["byHour"]})
}

// handleBOPOSTicketsSeries returns day-series buckets every 5 minutes for the
// selected business date: cumulative open-ticket revenue (facturado) and
// cumulative labour cost from member_time_entries. Each bucket is labeled with
// the HH:MM time it ends.
func (s *Server) handleBOPOSTicketsSeries(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	date, err := parseBODateQuery(r.URL.Query().Get("date"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "date invalido")
		return
	}
	dateISO := date.Format("2006-01-02")

	// Revenue by 5' bucket: open tickets grouped by the 5-minute bucket of
	// their open time.
	revRows, err := s.db.QueryContext(r.Context(), `SELECT DATE_FORMAT(t.opened_at,'%H:%i'),COALESCE(SUM(t.total_gross_cents),0) FROM pos_tickets t JOIN pos_visits v ON v.restaurant_id=t.restaurant_id AND v.id=t.visit_id WHERE t.restaurant_id=? AND v.service_date=? AND t.status IN ('OPEN','PARTIALLY_REFUNDED') GROUP BY DATE_FORMAT(t.opened_at,'%H:%i')`, a.ActiveRestaurantID, dateISO)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando serie de ingresos")
		return
	}
	revByBucket := map[string]int64{}
	for revRows.Next() {
		var bucket string
		var cents int64
		if err := revRows.Scan(&bucket, &cents); err != nil {
			revRows.Close()
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo serie de ingresos")
			return
		}
		revByBucket[bucket] = cents
	}
	revRows.Close()

	// Labour cost per hour for the day from persisted time entries.
	costRows, err := s.db.QueryContext(r.Context(), `SELECT LPAD(HOUR(e.start_time),2,'0'),SUM(e.minutes_worked/60*(CASE c.pay_type WHEN 'MONTHLY' THEN c.gross_amount/c.monthly_hours ELSE c.gross_amount END)*(1+c.employer_cost_pct/100)) FROM member_time_entries e LEFT JOIN member_compensations c ON c.restaurant_id=e.restaurant_id AND c.restaurant_member_id=e.restaurant_member_id AND c.deleted_at IS NULL AND e.work_date BETWEEN c.effective_from AND COALESCE(c.effective_to,'9999-12-31') WHERE e.restaurant_id=? AND e.work_date=? GROUP BY LPAD(HOUR(e.start_time),2,'0')`, a.ActiveRestaurantID, dateISO)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando serie de coste")
		return
	}
	costByHour := map[string]float64{}
	for costRows.Next() {
		var hour string
		var cost float64
		if err := costRows.Scan(&hour, &cost); err != nil {
			costRows.Close()
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo serie de coste")
			return
		}
		costByHour[hour] = cost
	}
	costRows.Close()

	// Sorted list of revenue buckets for cumulative walk.
	bucketKeys := make([]string, 0, len(revByBucket))
	for bucket := range revByBucket {
		bucketKeys = append(bucketKeys, bucket)
	}
	sort.Strings(bucketKeys)

	// Build 288 buckets of 5 minutes (00:00 .. 23:55).
	series := make([]map[string]any, 0, 288)
	start := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, boMadridTZ)
	var cumRev int64
	var cumCost float64
	revIdx := 0
	for i := 0; i < 288; i++ {
		bucketStart := start.Add(time.Duration(i) * 5 * time.Minute)
		label := bucketStart.Format("15:04")
		hour := bucketStart.Format("15")

		// Accumulate all revenue buckets at or before this label.
		for revIdx < len(bucketKeys) && bucketKeys[revIdx] <= label {
			cumRev += revByBucket[bucketKeys[revIdx]]
			revIdx++
		}
		// Labour cost is per hour; spread evenly across the 12 buckets.
		if hourCost, ok := costByHour[hour]; ok {
			cumCost += hourCost / 12
		}
		series = append(series, map[string]any{
			"time":       label,
			"grossCents": cumRev,
			"costCents":  round2(cumCost * 100),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "date": dateISO, "series": series})
}
