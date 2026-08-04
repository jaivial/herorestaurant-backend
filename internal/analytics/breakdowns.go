package analytics

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

// OverviewWithBreakdowns carries the regular overview plus the real
// POS/invoice breakdowns used by the frontend charts. It lives in its own
// file so the core Overview type keeps its stable shape.
type OverviewWithBreakdowns struct {
	Overview
	TopItems           []TopItemEntry         `json:"topItems"`
	PaymentMethods     []PaymentMethodEntry   `json:"paymentMethods"`
	DayOfWeek          []DayOfWeekEntry       `json:"dayOfWeek"`
	HourlyDistribution []HourlyEntry          `json:"hourlyDistribution"`
	RevenueByCategory  []CategoryRevenueEntry `json:"revenueByCategory"`
}

type TopItemEntry struct {
	Name       string  `json:"name"`
	Quantity   float64 `json:"quantity"`
	RevenueEUR float64 `json:"revenueEUR"`
}

type PaymentMethodEntry struct {
	Method    string  `json:"method"`
	AmountEUR float64 `json:"amountEUR"`
	Count     int     `json:"count"`
}

type DayOfWeekEntry struct {
	Day        string  `json:"day"`
	RevenueEUR float64 `json:"revenueEUR"`
	Covers     int     `json:"covers"`
}

type HourlyEntry struct {
	Hour       string  `json:"hour"`
	Covers     int     `json:"covers"`
	RevenueEUR float64 `json:"revenueEUR"`
}

type CategoryRevenueEntry struct {
	Category   string  `json:"category"`
	AmountEUR  float64 `json:"amountEUR"`
	Percentage float64 `json:"percentage"`
}

const paidTicketStatusList = "'PAID','PARTIALLY_REFUNDED','REFUNDED'"

var weekdayLabels = [...]string{"Lun", "Mar", "Mié", "Jue", "Vie", "Sáb", "Dom"}

func paymentMethodLabel(method string) string {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "CASH":
		return "Efectivo"
	case "CARD":
		return "Tarjeta"
	case "BANK":
		return "Transferencia"
	default:
		return "Otros"
	}
}

// OverviewWithBreakdowns loads the overview and enriches it with the real
// breakdowns for the same tenant and date range.
func (s *Service) OverviewWithBreakdowns(ctx context.Context, tenantID int, dateRange DateRange, granularity Granularity, comparePrevious bool) (OverviewWithBreakdowns, error) {
	overview, err := s.Overview(ctx, tenantID, dateRange, granularity, comparePrevious)
	if err != nil {
		return OverviewWithBreakdowns{}, err
	}
	topItems, err := s.loadTopItems(ctx, tenantID, dateRange)
	if err != nil {
		return OverviewWithBreakdowns{}, fmt.Errorf("load analytics top items: %w", err)
	}
	paymentMethods, err := s.loadPaymentMethods(ctx, tenantID, dateRange)
	if err != nil {
		return OverviewWithBreakdowns{}, fmt.Errorf("load analytics payment methods: %w", err)
	}
	dayOfWeek, err := s.loadDayOfWeek(ctx, tenantID, dateRange)
	if err != nil {
		return OverviewWithBreakdowns{}, fmt.Errorf("load analytics day of week: %w", err)
	}
	hourly, err := s.loadHourlyDistribution(ctx, tenantID, dateRange)
	if err != nil {
		return OverviewWithBreakdowns{}, fmt.Errorf("load analytics hourly distribution: %w", err)
	}
	categories, err := s.loadRevenueByCategory(ctx, tenantID, dateRange)
	if err != nil {
		return OverviewWithBreakdowns{}, fmt.Errorf("load analytics revenue by category: %w", err)
	}
	return OverviewWithBreakdowns{
		Overview:           overview,
		TopItems:           topItems,
		PaymentMethods:     paymentMethods,
		DayOfWeek:          dayOfWeek,
		HourlyDistribution: hourly,
		RevenueByCategory:  categories,
	}, nil
}

// loadTopItems aggregates the best selling POS products in the period.
// Refunded quantities/amounts are netted out so the list stays honest.
func (s *Service) loadTopItems(ctx context.Context, tenantID int, dateRange DateRange) ([]TopItemEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT l.product_name_snapshot,
		       SUM(l.quantity) - COALESCE(SUM(CASE WHEN r.status='COMPLETED' THEN rl.quantity ELSE 0 END),0) AS quantity,
		       SUM(l.line_total_gross_cents) - COALESCE(SUM(CASE WHEN r.status='COMPLETED' THEN rl.amount_cents ELSE 0 END),0) AS revenue
		FROM pos_ticket_lines l
		JOIN pos_tickets t ON t.restaurant_id=l.restaurant_id AND t.id=l.ticket_id
		JOIN pos_visits v ON v.restaurant_id=t.restaurant_id AND v.id=t.visit_id
		LEFT JOIN pos_refund_lines rl ON rl.restaurant_id=l.restaurant_id AND rl.ticket_line_id=l.id
		LEFT JOIN pos_refunds r ON r.restaurant_id=rl.restaurant_id AND r.id=rl.refund_id
		WHERE l.restaurant_id=? AND l.status='ACTIVE'
		  AND t.status IN (`+paidTicketStatusList+`)
		  AND v.service_date BETWEEN ? AND ?
		GROUP BY l.product_name_snapshot
		ORDER BY revenue DESC
		LIMIT 10
	`, tenantID, dateRange.From, dateRange.To)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]TopItemEntry, 0, 10)
	for rows.Next() {
		var name string
		var quantity, revenue float64
		if err := rows.Scan(&name, &quantity, &revenue); err != nil {
			return nil, err
		}
		if revenue <= 0 {
			continue
		}
		result = append(result, TopItemEntry{
			Name:       name,
			Quantity:   maxFloat(0, quantity),
			RevenueEUR: roundEUR(float64(revenue) / 100),
		})
	}
	return result, rows.Err()
}

// loadPaymentMethods aggregates captured POS payments by method.
func (s *Service) loadPaymentMethods(ctx context.Context, tenantID int, dateRange DateRange) ([]PaymentMethodEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.method, COALESCE(SUM(p.amount_cents),0), COUNT(*)
		FROM pos_payments p
		JOIN pos_tickets t ON t.restaurant_id=p.restaurant_id AND t.id=p.ticket_id
		JOIN pos_visits v ON v.restaurant_id=t.restaurant_id AND v.id=t.visit_id
		WHERE p.restaurant_id=? AND p.status='CAPTURED'
		  AND v.service_date BETWEEN ? AND ?
		GROUP BY p.method
		ORDER BY 2 DESC
	`, tenantID, dateRange.From, dateRange.To)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]PaymentMethodEntry, 0, 4)
	for rows.Next() {
		var method string
		var amount int64
		var count int
		if err := rows.Scan(&method, &amount, &count); err != nil {
			return nil, err
		}
		result = append(result, PaymentMethodEntry{
			Method:    paymentMethodLabel(method),
			AmountEUR: roundEUR(float64(amount) / 100),
			Count:     count,
		})
	}
	return result, rows.Err()
}

// loadDayOfWeek builds revenue per weekday from the synced rollups and covers
// from POS visits that were actually settled in the period.
func (s *Service) loadDayOfWeek(ctx context.Context, tenantID int, dateRange DateRange) ([]DayOfWeekEntry, error) {
	revenueByDay := make(map[int]float64)
	rows, err := s.db.QueryContext(ctx, `
		SELECT WEEKDAY(rollup_date), SUM(invoiced_revenue_cents+pos_revenue_cents)
		FROM analytics_daily_rollups
		WHERE restaurant_id=? AND rollup_date BETWEEN ? AND ?
		GROUP BY WEEKDAY(rollup_date)
	`, tenantID, dateRange.From, dateRange.To)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var weekday int
		var revenue int64
		if err := rows.Scan(&weekday, &revenue); err != nil {
			rows.Close()
			return nil, err
		}
		revenueByDay[weekday] = roundEUR(float64(revenue) / 100)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	coversByDay := make(map[int]int)
	coversRows, err := s.db.QueryContext(ctx, `
		SELECT WEEKDAY(v.service_date), SUM(v.covers)
		FROM pos_visits v
		WHERE v.restaurant_id=? AND v.service_date BETWEEN ? AND ?
		  AND v.status <> 'CANCELLED'
		  AND EXISTS (
			SELECT 1 FROM pos_tickets t
			WHERE t.restaurant_id=v.restaurant_id AND t.visit_id=v.id
			  AND t.status IN (`+paidTicketStatusList+`)
		  )
		GROUP BY WEEKDAY(v.service_date)
	`, tenantID, dateRange.From, dateRange.To)
	if err != nil {
		return nil, err
	}
	for coversRows.Next() {
		var weekday int
		var covers int64
		if err := coversRows.Scan(&weekday, &covers); err != nil {
			coversRows.Close()
			return nil, err
		}
		coversByDay[weekday] = int(covers)
	}
	if err = coversRows.Err(); err != nil {
		coversRows.Close()
		return nil, err
	}
	coversRows.Close()

	result := make([]DayOfWeekEntry, 0, 7)
	for weekday := 0; weekday < 7; weekday++ {
		revenue := revenueByDay[weekday]
		covers := coversByDay[weekday]
		if revenue <= 0 && covers <= 0 {
			continue
		}
		result = append(result, DayOfWeekEntry{
			Day:        weekdayLabels[weekday],
			RevenueEUR: revenue,
			Covers:     covers,
		})
	}
	return result, nil
}

// loadHourlyDistribution aggregates settled visits by the hour of their first
// paid ticket. Each visit contributes its covers once, so a visit with several
// tickets is not double counted.
func (s *Service) loadHourlyDistribution(ctx context.Context, tenantID int, dateRange DateRange) ([]HourlyEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT v.id, v.covers, HOUR(MIN(t.paid_at)), COALESCE(SUM(t.total_gross_cents),0)
		FROM pos_visits v
		JOIN pos_tickets t ON t.restaurant_id=v.restaurant_id AND t.visit_id=v.id
		WHERE v.restaurant_id=? AND v.service_date BETWEEN ? AND ?
		  AND v.status <> 'CANCELLED'
		  AND t.status IN (`+paidTicketStatusList+`)
		  AND t.paid_at IS NOT NULL
		GROUP BY v.id
	`, tenantID, dateRange.From, dateRange.To)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	coversByHour := make(map[int]int)
	revenueByHour := make(map[int]float64)
	for rows.Next() {
		var visitID int64
		var covers int
		var hour int
		var revenue int64
		if err := rows.Scan(&visitID, &covers, &hour, &revenue); err != nil {
			return nil, err
		}
		coversByHour[hour] += covers
		revenueByHour[hour] += roundEUR(float64(revenue) / 100)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	hours := make([]int, 0, len(coversByHour))
	for hour := range coversByHour {
		hours = append(hours, hour)
	}
	sort.Ints(hours)
	result := make([]HourlyEntry, 0, len(hours))
	for _, hour := range hours {
		result = append(result, HourlyEntry{
			Hour:       fmt.Sprintf("%02d:00", hour),
			Covers:     coversByHour[hour],
			RevenueEUR: revenueByHour[hour],
		})
	}
	return result, nil
}

// loadRevenueByCategory groups POS revenue by the product category assigned in
// the catalog. Lines without a catalog product fall under "Sin categoría".
func (s *Service) loadRevenueByCategory(ctx context.Context, tenantID int, dateRange DateRange) ([]CategoryRevenueEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(pc.name,'Sin categoría'),
		       SUM(l.line_total_gross_cents) - COALESCE(SUM(CASE WHEN r.status='COMPLETED' THEN rl.amount_cents ELSE 0 END),0) AS revenue
		FROM pos_ticket_lines l
		JOIN pos_tickets t ON t.restaurant_id=l.restaurant_id AND t.id=l.ticket_id
		JOIN pos_visits v ON v.restaurant_id=t.restaurant_id AND v.id=t.visit_id
		LEFT JOIN pos_products pp ON pp.restaurant_id=l.restaurant_id AND pp.id=l.pos_product_id
		LEFT JOIN pos_product_categories pc ON pc.restaurant_id=pp.restaurant_id AND pc.id=pp.category_id
		LEFT JOIN pos_refund_lines rl ON rl.restaurant_id=l.restaurant_id AND rl.ticket_line_id=l.id
		LEFT JOIN pos_refunds r ON r.restaurant_id=rl.restaurant_id AND r.id=rl.refund_id
		WHERE l.restaurant_id=? AND l.status='ACTIVE'
		  AND t.status IN (`+paidTicketStatusList+`)
		  AND v.service_date BETWEEN ? AND ?
		GROUP BY pc.name
		ORDER BY revenue DESC
	`, tenantID, dateRange.From, dateRange.To)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type categoryRow struct {
		name    string
		revenue float64
	}
	rowsData := make([]categoryRow, 0, 8)
	var total float64
	for rows.Next() {
		var name string
		var revenue int64
		if err := rows.Scan(&name, &revenue); err != nil {
			return nil, err
		}
		if revenue <= 0 {
			continue
		}
		value := roundEUR(float64(revenue) / 100)
		rowsData = append(rowsData, categoryRow{name: name, revenue: value})
		total += value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]CategoryRevenueEntry, 0, len(rowsData))
	for _, row := range rowsData {
		percentage := 0.0
		if total > 0 {
			percentage = roundEUR(row.revenue / total * 100)
		}
		result = append(result, CategoryRevenueEntry{
			Category:   row.name,
			AmountEUR:  row.revenue,
			Percentage: percentage,
		})
	}
	return result, nil
}

func maxFloat(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}

func roundEUR(value float64) float64 {
	return math.Round(value*100) / 100
}
