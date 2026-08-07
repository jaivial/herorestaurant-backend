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
		var total int64
		if e := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(amount_cents),0) FROM pos_payments WHERE restaurant_id=? AND status='CAPTURED'`, rid).Scan(&total); e != nil {
			return "", e
		}
		return botJSON(map[string]any{"metric": "revenue", "value_cents": total}), nil
	case "stock":
		return s.assistantCount(ctx, rid, "stock_items", "items")
	}
	return "", fmt.Errorf("métrica no permitida: %s", in.Metric)
}
