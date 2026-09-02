package api

import (
	"context"
	"database/sql"
	"strconv"
)

// loadPOSProductStock builds the per-product stock status map for the sell
// screen: "out" when any rule's warehouse level is at or below zero, "low"
// when any level sits below its reorder point, "ok" otherwise. Levels missing
// from stock_levels count as zero, matching the real-time deduction path that
// materialises them at zero on first sale. Only products with active rules on
// tracked, POS-deductible items appear; OFF mode always returns an empty map.
func (s *Server) loadPOSProductStock(ctx context.Context, restaurantID int, stockMode string) (map[int64]string, error) {
	if stockMode == "OFF" {
		return map[int64]string{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.pos_product_id,
		       MIN(CASE WHEN COALESCE(l.qty_base,0) <= 0 THEN 0
		                WHEN l.reorder_point_base > 0 AND COALESCE(l.qty_base,0) < l.reorder_point_base THEN 1
		                ELSE 2 END)
		FROM pos_product_stock_rules r
		JOIN stock_items i ON i.restaurant_id = r.restaurant_id AND i.id = r.stock_item_id
		JOIN stock_warehouses w ON w.restaurant_id = r.restaurant_id AND w.id = r.warehouse_id
		LEFT JOIN stock_levels l ON l.restaurant_id = r.restaurant_id AND l.stock_item_id = r.stock_item_id AND l.warehouse_id = r.warehouse_id
		WHERE r.restaurant_id = ? AND r.is_active = 1
		  AND i.is_active = 1 AND i.deleted_at IS NULL AND i.is_tracked = 1 AND i.deduction_source <> 'PRODUCTION'
		  AND w.is_active = 1 AND w.deleted_at IS NULL
		GROUP BY r.pos_product_id
	`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	status := map[int64]string{}
	for rows.Next() {
		var productID int64
		var worst int
		if err = rows.Scan(&productID, &worst); err != nil {
			return nil, err
		}
		switch worst {
		case 0:
			status[productID] = "out"
		case 1:
			status[productID] = "low"
		default:
			status[productID] = "ok"
		}
	}
	return status, rows.Err()
}

// posStockRule represents a mapping from POS product to stock item
type posStockRule struct {
	RuleID          int64
	StockItemID     int64
	WarehouseID     int64
	QtyBasePerSale  float64
	IsTracked       bool
	DeductionSource string
}

// posRealtimeStockResult holds the result of a real-time stock operation
type posRealtimeStockResult struct {
	Snapshots []posStockSnapshot
	Partial   bool
}

// getStockRulesForProduct fetches active stock rules for a POS product
func (s *Server) getStockRulesForProduct(ctx context.Context, tx *sql.Tx, restaurantID int, productID int64) ([]posStockRule, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT r.id, r.stock_item_id, r.warehouse_id, r.qty_base_per_sale, i.is_tracked, i.deduction_source
		FROM pos_product_stock_rules r
		JOIN stock_items i ON i.restaurant_id = r.restaurant_id AND i.id = r.stock_item_id
		JOIN stock_warehouses w ON w.restaurant_id = r.restaurant_id AND w.id = r.warehouse_id
		WHERE r.restaurant_id = ? AND r.pos_product_id = ? AND r.is_active = 1
		  AND i.is_active = 1 AND i.deleted_at IS NULL
		  AND w.is_active = 1 AND w.deleted_at IS NULL
		ORDER BY r.warehouse_id, r.stock_item_id, r.id
	`, restaurantID, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rules := []posStockRule{}
	for rows.Next() {
		var rule posStockRule
		var tracked int
		if err = rows.Scan(&rule.RuleID, &rule.StockItemID, &rule.WarehouseID, &rule.QtyBasePerSale, &tracked, &rule.DeductionSource); err != nil {
			return nil, err
		}
		rule.IsTracked = tracked != 0
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

// deductStockForLine deducts stock when a line is added or quantity increased.
// Returns the created snapshots for tracking.
func (s *Server) deductStockForLine(ctx context.Context, tx *sql.Tx, restaurantID, actorID int, ticketID, lineID, productID int64, quantity float64, idempotencyPrefix string) (*posRealtimeStockResult, error) {
	rules, err := s.getStockRulesForProduct(ctx, tx, restaurantID, productID)
	if err != nil {
		return nil, err
	}

	result := &posRealtimeStockResult{
		Snapshots: []posStockSnapshot{},
		Partial:   len(rules) == 0,
	}

	for _, rule := range rules {
		// Skip production-only items (can't be deducted by POS)
		if rule.DeductionSource == "PRODUCTION" {
			result.Partial = true
			continue
		}

		// Skip untracked items
		if !rule.IsTracked {
			continue
		}

		qtyBase, err := posStockPlannedQuantity(quantity, rule.QtyBasePerSale)
		if err != nil {
			continue
		}

		// Ensure stock level row exists
		_, err = tx.ExecContext(ctx, `
			INSERT INTO stock_levels (restaurant_id, stock_item_id, warehouse_id, qty_base)
			VALUES (?, ?, ?, 0)
			ON DUPLICATE KEY UPDATE stock_item_id = VALUES(stock_item_id)
		`, restaurantID, rule.StockItemID, rule.WarehouseID)
		if err != nil {
			return nil, err
		}

		// Lock and get current stock level
		var current float64
		if err = tx.QueryRowContext(ctx, `
			SELECT qty_base FROM stock_levels
			WHERE restaurant_id = ? AND stock_item_id = ? AND warehouse_id = ?
			FOR UPDATE
		`, restaurantID, rule.StockItemID, rule.WarehouseID).Scan(&current); err != nil {
			return nil, err
		}

		// Get default unit for movement record
		var unitID int64
		var factor float64
		if err = tx.QueryRowContext(ctx, `
			SELECT id, factor_to_base FROM stock_item_units
			WHERE restaurant_id = ? AND stock_item_id = ? AND is_default_display = 1
			LIMIT 1
		`, restaurantID, rule.StockItemID).Scan(&unitID, &factor); err != nil {
			return nil, err
		}

		// Create stock movement (SALE type for deduction)
		idempotencyKey := idempotencyPrefix + ":line:" + strconv.FormatInt(lineID, 10) + ":rule:" + strconv.FormatInt(rule.RuleID, 10)
		res, err := tx.ExecContext(ctx, `
			INSERT INTO stock_movements (restaurant_id, stock_item_id, warehouse_id, qty_base, type, entered_qty, entered_unit_id, ref_type, ref_id, idempotency_key, note, actor_user_id)
			VALUES (?, ?, ?, ?, 'SALE', ?, ?, 'pos_ticket_line', ?, ?, 'POS realtime deduction', ?)
		`, restaurantID, rule.StockItemID, rule.WarehouseID, -qtyBase, qtyBase/factor, unitID, lineID, idempotencyKey, actorID)
		if err != nil {
			return nil, err
		}
		movementID, _ := res.LastInsertId()

		// Deduct from stock level
		_, err = tx.ExecContext(ctx, `
			UPDATE stock_levels SET qty_base = qty_base - ?, version = version + 1
			WHERE restaurant_id = ? AND stock_item_id = ? AND warehouse_id = ?
		`, qtyBase, restaurantID, rule.StockItemID, rule.WarehouseID)
		if err != nil {
			return nil, err
		}

		// Create pos_ticket_line_stock record
		stockRes, err := tx.ExecContext(ctx, `
			INSERT INTO pos_ticket_line_stock (restaurant_id, ticket_id, ticket_line_id, stock_rule_id, stock_item_id, warehouse_id, quantity_sold, qty_base_planned, status, sale_movement_id, applied_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'APPLIED', ?, NOW())
		`, restaurantID, ticketID, lineID, rule.RuleID, rule.StockItemID, rule.WarehouseID, quantity, qtyBase, movementID)
		if err != nil {
			return nil, err
		}
		snapshotID, _ := stockRes.LastInsertId()

		// Track negative stock anomaly
		quantityAfter := current - qtyBase
		if quantityAfter < 0 {
			_, _ = tx.ExecContext(ctx, `
				INSERT IGNORE INTO pos_stock_anomalies (restaurant_id, ticket_id, ticket_line_stock_id, stock_item_id, warehouse_id, quantity_after_base)
				VALUES (?, ?, ?, ?, ?, ?)
			`, restaurantID, ticketID, snapshotID, rule.StockItemID, rule.WarehouseID, quantityAfter)
		}

		result.Snapshots = append(result.Snapshots, posStockSnapshot{
			LineID:      lineID,
			RuleID:      rule.RuleID,
			ItemID:      rule.StockItemID,
			WarehouseID: rule.WarehouseID,
			QtyBase:     qtyBase,
			Status:      "APPLIED",
			SnapshotID:  snapshotID,
		})
	}

	return result, nil
}

// restoreStockForLine restores stock when a line is voided or quantity decreased.
func (s *Server) restoreStockForLine(ctx context.Context, tx *sql.Tx, restaurantID, actorID int, lineID int64, idempotencyPrefix string) error {
	// Find all APPLIED stock records for this line
	rows, err := tx.QueryContext(ctx, `
		SELECT id, stock_item_id, warehouse_id, qty_base_planned
		FROM pos_ticket_line_stock
		WHERE restaurant_id = ? AND ticket_line_id = ? AND status = 'APPLIED'
		ORDER BY id
	`, restaurantID, lineID)
	if err != nil {
		return err
	}
	defer rows.Close()

	type stockRecord struct {
		ID          int64
		ItemID      int64
		WarehouseID int64
		QtyBase     float64
	}
	records := []stockRecord{}
	for rows.Next() {
		var rec stockRecord
		if err = rows.Scan(&rec.ID, &rec.ItemID, &rec.WarehouseID, &rec.QtyBase); err != nil {
			return err
		}
		records = append(records, rec)
	}
	rows.Close()

	for _, rec := range records {
		// Get default unit
		var unitID int64
		var factor float64
		if err = tx.QueryRowContext(ctx, `
			SELECT id, factor_to_base FROM stock_item_units
			WHERE restaurant_id = ? AND stock_item_id = ? AND is_default_display = 1
			LIMIT 1
		`, restaurantID, rec.ItemID).Scan(&unitID, &factor); err != nil {
			return err
		}

		// Create RETURN movement
		idempotencyKey := idempotencyPrefix + ":restore:snapshot:" + strconv.FormatInt(rec.ID, 10)
		moveRes, err := tx.ExecContext(ctx, `
			INSERT INTO stock_movements (restaurant_id, stock_item_id, warehouse_id, qty_base, type, entered_qty, entered_unit_id, ref_type, ref_id, idempotency_key, note, actor_user_id)
			VALUES (?, ?, ?, ?, 'RETURN', ?, ?, 'pos_ticket_line_stock', ?, ?, 'POS line void/cancel', ?)
		`, restaurantID, rec.ItemID, rec.WarehouseID, rec.QtyBase, rec.QtyBase/factor, unitID, rec.ID, idempotencyKey, actorID)
		if err != nil {
			return err
		}
		movementID, _ := moveRes.LastInsertId()

		// Restore stock level
		_, err = tx.ExecContext(ctx, `
			UPDATE stock_levels SET qty_base = qty_base + ?, version = version + 1
			WHERE restaurant_id = ? AND stock_item_id = ? AND warehouse_id = ?
		`, rec.QtyBase, restaurantID, rec.ItemID, rec.WarehouseID)
		if err != nil {
			return err
		}

		// Update pos_ticket_line_stock status
		_, err = tx.ExecContext(ctx, `
			UPDATE pos_ticket_line_stock SET status = 'REVERSED', return_movement_id = ?, reversed_at = NOW()
			WHERE restaurant_id = ? AND id = ?
		`, movementID, restaurantID, rec.ID)
		if err != nil {
			return err
		}
	}

	return nil
}

// adjustStockForQuantityChange handles quantity changes by deducting/restoring the delta.
func (s *Server) adjustStockForQuantityChange(ctx context.Context, tx *sql.Tx, restaurantID, actorID int, ticketID, lineID, productID int64, oldQuantity, newQuantity float64, idempotencyPrefix string) error {
	delta := newQuantity - oldQuantity
	if delta == 0 {
		return nil
	}

	// Get stock rules for this product
	rules, err := s.getStockRulesForProduct(ctx, tx, restaurantID, productID)
	if err != nil {
		return err
	}

	for _, rule := range rules {
		if rule.DeductionSource == "PRODUCTION" || !rule.IsTracked {
			continue
		}

		deltaQtyBase := delta * rule.QtyBasePerSale

		// Get default unit
		var unitID int64
		var factor float64
		if err = tx.QueryRowContext(ctx, `
			SELECT id, factor_to_base FROM stock_item_units
			WHERE restaurant_id = ? AND stock_item_id = ? AND is_default_display = 1
			LIMIT 1
		`, restaurantID, rule.StockItemID).Scan(&unitID, &factor); err != nil {
			return err
		}

		// Ensure stock level exists
		_, err = tx.ExecContext(ctx, `
			INSERT INTO stock_levels (restaurant_id, stock_item_id, warehouse_id, qty_base)
			VALUES (?, ?, ?, 0)
			ON DUPLICATE KEY UPDATE stock_item_id = VALUES(stock_item_id)
		`, restaurantID, rule.StockItemID, rule.WarehouseID)
		if err != nil {
			return err
		}

		// Lock stock level
		var current float64
		if err = tx.QueryRowContext(ctx, `
			SELECT qty_base FROM stock_levels
			WHERE restaurant_id = ? AND stock_item_id = ? AND warehouse_id = ?
			FOR UPDATE
		`, restaurantID, rule.StockItemID, rule.WarehouseID).Scan(&current); err != nil {
			return err
		}

		// Create movement
		var movementType, note string
		var qtyBaseMovement float64
		if delta > 0 {
			// Quantity increased - deduct more stock
			movementType = "SALE"
			qtyBaseMovement = -deltaQtyBase
			note = "POS quantity increase"
		} else {
			// Quantity decreased - restore stock
			movementType = "RETURN"
			qtyBaseMovement = -deltaQtyBase // -deltaQtyBase is positive since delta is negative
			note = "POS quantity decrease"
		}

		idempotencyKey := idempotencyPrefix + ":adjust:rule:" + strconv.FormatInt(rule.RuleID, 10)
		_, err = tx.ExecContext(ctx, `
			INSERT INTO stock_movements (restaurant_id, stock_item_id, warehouse_id, qty_base, type, entered_qty, entered_unit_id, ref_type, ref_id, idempotency_key, note, actor_user_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, 'pos_ticket_line', ?, ?, ?, ?)
		`, restaurantID, rule.StockItemID, rule.WarehouseID, qtyBaseMovement, movementType, -qtyBaseMovement/factor, unitID, lineID, idempotencyKey, note, actorID)
		if err != nil {
			return err
		}

		// Update stock level
		_, err = tx.ExecContext(ctx, `
			UPDATE stock_levels SET qty_base = qty_base + ?, version = version + 1
			WHERE restaurant_id = ? AND stock_item_id = ? AND warehouse_id = ?
		`, qtyBaseMovement, restaurantID, rule.StockItemID, rule.WarehouseID)
		if err != nil {
			return err
		}

		// Update pos_ticket_line_stock record with new quantity
		_, err = tx.ExecContext(ctx, `
			UPDATE pos_ticket_line_stock
			SET quantity_sold = ?, qty_base_planned = ?
			WHERE restaurant_id = ? AND ticket_line_id = ? AND stock_rule_id = ? AND status = 'APPLIED'
		`, newQuantity, newQuantity*rule.QtyBasePerSale, restaurantID, lineID, rule.RuleID)
		if err != nil {
			return err
		}

		// Track negative stock anomaly if applicable
		if delta > 0 {
			quantityAfter := current - deltaQtyBase
			if quantityAfter < 0 {
				// Find the snapshot ID
				var snapshotID int64
				_ = tx.QueryRowContext(ctx, `
					SELECT id FROM pos_ticket_line_stock
					WHERE restaurant_id = ? AND ticket_line_id = ? AND stock_rule_id = ? AND status = 'APPLIED'
				`, restaurantID, lineID, rule.RuleID).Scan(&snapshotID)
				if snapshotID > 0 {
					_, _ = tx.ExecContext(ctx, `
						INSERT IGNORE INTO pos_stock_anomalies (restaurant_id, ticket_id, ticket_line_stock_id, stock_item_id, warehouse_id, quantity_after_base)
						VALUES (?, ?, ?, ?, ?, ?)
					`, restaurantID, ticketID, snapshotID, rule.StockItemID, rule.WarehouseID, quantityAfter)
				}
			}
		}
	}

	return nil
}

// restoreStockForTicket restores all stock for a ticket (used when voiding/cancelling)
func (s *Server) restoreStockForTicket(ctx context.Context, tx *sql.Tx, restaurantID, actorID int, ticketID int64, idempotencyPrefix string) error {
	// Get all ACTIVE lines from this ticket
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM pos_ticket_lines
		WHERE restaurant_id = ? AND ticket_id = ? AND status = 'ACTIVE'
	`, restaurantID, ticketID)
	if err != nil {
		return err
	}
	defer rows.Close()

	lineIDs := []int64{}
	for rows.Next() {
		var lineID int64
		if err = rows.Scan(&lineID); err != nil {
			return err
		}
		lineIDs = append(lineIDs, lineID)
	}
	rows.Close()

	for _, lineID := range lineIDs {
		lineIdemKey := idempotencyPrefix + ":ticket:" + strconv.FormatInt(ticketID, 10) + ":line:" + strconv.FormatInt(lineID, 10)
		if err = s.restoreStockForLine(ctx, tx, restaurantID, actorID, lineID, lineIdemKey); err != nil {
			return err
		}
	}

	return nil
}

// restoreStockForVisit restores all stock for a visit (used when cancelling visit)
func (s *Server) restoreStockForVisit(ctx context.Context, tx *sql.Tx, restaurantID, actorID int, visitID int64, idempotencyPrefix string) error {
	// Get all open tickets for this visit
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM pos_tickets
		WHERE restaurant_id = ? AND visit_id = ? AND status = 'OPEN'
	`, restaurantID, visitID)
	if err != nil {
		return err
	}
	defer rows.Close()

	ticketIDs := []int64{}
	for rows.Next() {
		var ticketID int64
		if err = rows.Scan(&ticketID); err != nil {
			return err
		}
		ticketIDs = append(ticketIDs, ticketID)
	}
	rows.Close()

	for _, ticketID := range ticketIDs {
		ticketIdemKey := idempotencyPrefix + ":visit:" + strconv.FormatInt(visitID, 10)
		if err = s.restoreStockForTicket(ctx, tx, restaurantID, actorID, ticketID, ticketIdemKey); err != nil {
			return err
		}
	}

	return nil
}

// hasRealtimeStockDeductions checks if a ticket line already has APPLIED stock deductions
func (s *Server) hasRealtimeStockDeductions(ctx context.Context, tx *sql.Tx, restaurantID int, lineID int64) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pos_ticket_line_stock
		WHERE restaurant_id = ? AND ticket_line_id = ? AND status = 'APPLIED'
	`, restaurantID, lineID).Scan(&count)
	return count > 0, err
}
