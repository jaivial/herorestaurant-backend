package api

import (
	"context"
	"encoding/json"
	"fmt"
)

func (s *Server) assistantAnalyticsTool(ctx context.Context, rid int, raw json.RawMessage) (string, error) {
	var in struct{ Metric, DateFrom, DateTo string }
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", err
	}
	if rid <= 0 {
		return "", fmt.Errorf("restaurante activo no disponible")
	}
	switch in.Metric {
	case "bookings":
		return s.assistantBookingSeries(ctx, rid, in.DateFrom, in.DateTo)
	case "bookings_by_hour":
		return s.assistantBookingHourSeries(ctx, rid, in.DateFrom, in.DateTo)
	case "products":
		var rows []map[string]any
		rs, e := s.db.QueryContext(ctx, `SELECT COALESCE(pp.name,'Producto'), COUNT(*) FROM pos_ticket_lines l LEFT JOIN pos_products pp ON pp.restaurant_id=l.restaurant_id AND pp.id=l.pos_product_id WHERE l.restaurant_id=? GROUP BY l.pos_product_id,pp.name ORDER BY COUNT(*) DESC LIMIT 20`, rid)
		if e != nil {
			return "", e
		}
		defer rs.Close()
		for rs.Next() {
			var n string
			var v int
			if e = rs.Scan(&n, &v); e != nil {
				return "", e
			}
			rows = append(rows, map[string]any{"label": n, "value": v})
		}
		return botJSON(map[string]any{"chart": "bar", "data": rows}), rs.Err()
	case "revenue":
		q := `SELECT COALESCE(SUM(amount_cents),0) FROM pos_payments WHERE restaurant_id=? AND status='CAPTURED'`
		args := []any{rid}
		if in.DateFrom != "" {
			q += ` AND received_at >= ?`
			args = append(args, in.DateFrom)
		}
		if in.DateTo != "" {
			q += ` AND received_at < DATE_ADD(?, INTERVAL 1 DAY)`
			args = append(args, in.DateTo)
		}
		var total int64
		if e := s.db.QueryRowContext(ctx, q, args...).Scan(&total); e != nil {
			return "", e
		}
		return botJSON(map[string]any{"metric": "revenue", "value_cents": total, "date_from": in.DateFrom, "date_to": in.DateTo}), nil
	case "revenue_by_day":
		q := `SELECT DATE_FORMAT(received_at, '%Y-%m-%d'), SUM(amount_cents) FROM pos_payments WHERE restaurant_id=? AND status='CAPTURED'`
		args := []any{rid}
		if in.DateFrom != "" {
			q += ` AND received_at >= ?`
			args = append(args, in.DateFrom)
		}
		if in.DateTo != "" {
			q += ` AND received_at < DATE_ADD(?, INTERVAL 1 DAY)`
			args = append(args, in.DateTo)
		}
		q += ` GROUP BY DATE_FORMAT(received_at, '%Y-%m-%d') ORDER BY 1 LIMIT 500`
		rows, e := s.db.QueryContext(ctx, q, args...)
		if e != nil {
			return "", e
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var d string
			var v int64
			if e = rows.Scan(&d, &v); e != nil {
				return "", e
			}
			out = append(out, map[string]any{"label": d, "value": v})
		}
		return botJSON(map[string]any{"chart": "line", "data": out}), rows.Err()
	case "stock":
		return s.assistantCount(ctx, rid, "stock_items", "items")
	}
	return "", fmt.Errorf("métrica no permitida: %s", in.Metric)
}

// assistantBookingHourSeries groups bookings by reservation hour so the model
// can render an occupancy-by-hour chart.
func (s *Server) assistantBookingHourSeries(ctx context.Context, rid int, from, to string) (string, error) {
	q := `SELECT HOUR(reservation_time), COUNT(*) FROM bookings WHERE restaurant_id=?`
	args := []any{rid}
	if from != "" {
		q += ` AND reservation_date >= ?`
		args = append(args, from)
	}
	if to != "" {
		q += ` AND reservation_date <= ?`
		args = append(args, to)
	}
	q += ` GROUP BY HOUR(reservation_time) ORDER BY 1 LIMIT 24`
	rows, e := s.db.QueryContext(ctx, q, args...)
	if e != nil {
		return "", e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var h int
		var n int
		if e = rows.Scan(&h, &n); e != nil {
			return "", e
		}
		out = append(out, map[string]any{"label": fmt.Sprintf("%02d:00", h), "value": n})
	}
	return botJSON(map[string]any{"chart": "bar", "data": out}), rows.Err()
}
