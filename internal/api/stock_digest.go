package api

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Daily WhatsApp digest of stock alerts for restaurant operators. Follows the
// pre-shift reminder pattern: a one-minute ticker claims a per-recipient
// idempotency key in message_deliveries, gates on the WhatsApp premium feature
// plus a connected gateway, and sends one tracked message. Recipients are the
// members with a verified WhatsApp number linked to a root/admin backoffice
// user — the same roles stock UI allows by default. Days with nothing to
// report stay silent.

const (
	stockDigestExpiringDays = 7
	stockDigestMaxLines     = 5
	stockDigestSource       = "stock_digest"
	stockDigestQueryTimeout = 10 * time.Second
)

func (s *Server) runStockDigestLoop(ctx context.Context) {
	w := &stockDigestWorker{s: s, hour: s.cfg.StockDigestHour}
	w.run(ctx)
}

type stockDigestWorker struct {
	s    *Server
	hour int
}

func (w *stockDigestWorker) run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = w.runOnce(ctx)
		}
	}
}

func (w *stockDigestWorker) runOnce(ctx context.Context) error {
	now := time.Now()
	if now.Hour() != w.hour {
		return nil
	}
	restaurantIDs, err := w.s.stockDigestRestaurantIDs(ctx)
	if err != nil {
		return err
	}
	day := now.Format("20060102")
	for _, rid := range restaurantIDs {
		if err = w.deliverForRestaurant(ctx, rid, day); err != nil {
			return err
		}
	}
	return nil
}

func (w *stockDigestWorker) deliverForRestaurant(ctx context.Context, rid int, day string) error {
	ok, err := w.s.hasActiveRecurringFeature(ctx, rid, boPremiumWhatsAppFeatureKey)
	if err != nil || !ok {
		return err
	}
	if _, connected := w.s.botGatewayFor(ctx, rid); !connected {
		return nil
	}
	body, err := w.s.buildStockDigestBody(ctx, rid, time.Now())
	if err != nil {
		return err
	}
	if body == "" {
		return nil
	}
	recipients, err := w.s.stockDigestRecipients(ctx, rid)
	if err != nil {
		return err
	}
	store := sqlPreShiftStore{w.s.db}
	for _, phone := range recipients {
		key := fmt.Sprintf("stock_digest:%d:%s:%s", rid, day, phone)
		claimed, err := store.Claim(ctx, key)
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		if err = w.s.stockDigestSend(ctx, rid, phone, body); err != nil {
			_ = store.Release(ctx, key)
			return err
		}
	}
	return nil
}

func (s *Server) stockDigestSend(ctx context.Context, rid int, to, text string) error {
	g, ok := s.botGatewayFor(ctx, rid)
	if !ok {
		return fmt.Errorf("whatsapp not connected")
	}
	return s.sendWhatsAppTextTracked(ctx, rid, g, to, text, stockDigestSource)
}

func (s *Server) stockDigestRestaurantIDs(ctx context.Context) ([]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM restaurants ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int{}
	for rows.Next() {
		var id int
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Server) stockDigestRecipients(ctx context.Context, restaurantID int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT rm.whatsapp_number
		FROM restaurant_members rm
		JOIN bo_user_restaurants ur ON ur.user_id = rm.bo_user_id AND ur.restaurant_id = rm.restaurant_id
		WHERE rm.restaurant_id = ? AND rm.is_active = 1
		  AND rm.whatsapp_verified_at IS NOT NULL AND rm.whatsapp_number IS NOT NULL AND rm.whatsapp_number <> ''
		  AND ur.role IN ('root','admin')`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var phone string
		if err = rows.Scan(&phone); err != nil {
			return nil, err
		}
		out = append(out, phone)
	}
	return out, rows.Err()
}

// buildStockDigestBody renders the digest text; empty when there is nothing to
// report (no low-stock items and nothing expiring within the window).
func (s *Server) buildStockDigestBody(ctx context.Context, restaurantID int, now time.Time) (string, error) {
	low, err := s.stockDigestLowStock(ctx, restaurantID)
	if err != nil {
		return "", err
	}
	expiring, err := s.stockDigestExpiring(ctx, restaurantID)
	if err != nil {
		return "", err
	}
	if len(low) == 0 && len(expiring) == 0 {
		return "", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Stock · %s\n", now.Format("2/1"))
	if len(low) > 0 {
		b.WriteString("\nBajo mínimo:\n")
		writeDigestLines(&b, low)
	}
	if len(expiring) > 0 {
		fmt.Fprintf(&b, "\nCaducan en %d días:\n", stockDigestExpiringDays)
		writeDigestLines(&b, expiring)
	}
	return b.String(), nil
}

func writeDigestLines(b *strings.Builder, lines []string) {
	for i, line := range lines {
		if i >= stockDigestMaxLines {
			fmt.Fprintf(b, "y %d más\n", len(lines)-stockDigestMaxLines)
			return
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

func (s *Server) stockDigestLowStock(ctx context.Context, restaurantID int) ([]string, error) {
	qctx, cancel := context.WithTimeout(ctx, stockDigestQueryTimeout)
	defer cancel()
	rows, err := s.db.QueryContext(qctx, `
		SELECT i.name, i.base_unit, COALESCE(SUM(l.qty_base),0), COALESCE(SUM(l.reorder_point_base),0)
		FROM stock_items i
		JOIN stock_levels l ON l.restaurant_id = i.restaurant_id AND l.stock_item_id = i.id
		WHERE i.restaurant_id = ? AND i.is_active = 1 AND i.is_tracked = 1 AND i.deleted_at IS NULL
		GROUP BY i.id, i.name, i.base_unit
		HAVING SUM(l.reorder_point_base) > 0 AND SUM(l.qty_base) < SUM(l.reorder_point_base)
		ORDER BY SUM(l.qty_base) - SUM(l.reorder_point_base), i.name
		LIMIT 20`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name, unit string
		var qty, reorder float64
		if err = rows.Scan(&name, &unit, &qty, &reorder); err != nil {
			return nil, err
		}
		out = append(out, fmt.Sprintf("· %s: %s/%s %s", name, stockDigestQty(qty), stockDigestQty(reorder), unit))
	}
	return out, rows.Err()
}

func (s *Server) stockDigestExpiring(ctx context.Context, restaurantID int) ([]string, error) {
	qctx, cancel := context.WithTimeout(ctx, stockDigestQueryTimeout)
	defer cancel()
	rows, err := s.db.QueryContext(qctx, `
		SELECT i.name, i.base_unit, MIN(m.expires_at), SUM(m.qty_base),
		       (SELECT COALESCE(SUM(-o.qty_base),0) FROM stock_movements o
		          WHERE o.restaurant_id = m.restaurant_id AND o.stock_item_id = m.stock_item_id
		            AND o.warehouse_id = m.warehouse_id AND o.qty_base < 0
		            AND o.occurred_at >= MIN(m.occurred_at))
		FROM stock_movements m
		JOIN stock_items i ON i.id = m.stock_item_id AND i.restaurant_id = m.restaurant_id AND i.deleted_at IS NULL
		WHERE m.restaurant_id = ? AND m.expires_at IS NOT NULL AND m.expires_at > NOW()
		  AND m.expires_at <= DATE_ADD(NOW(), INTERVAL ? DAY) AND m.qty_base > 0
		GROUP BY i.id, i.name, i.base_unit
		ORDER BY MIN(m.expires_at), i.name
		LIMIT 20`, restaurantID, stockDigestExpiringDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name, unit string
		var expires time.Time
		var purchased, consumed float64
		if err = rows.Scan(&name, &unit, &expires, &purchased, &consumed); err != nil {
			return nil, err
		}
		if remaining := purchased - consumed; remaining > 0 {
			out = append(out, fmt.Sprintf("· %s: %s %s (%s)", name, stockDigestQty(remaining), unit, expires.Format("2/1")))
		}
	}
	return out, rows.Err()
}

func stockDigestQty(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
