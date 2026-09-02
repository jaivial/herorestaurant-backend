-- Expiry tracking on inbound stock movements: PURCHASE, ADJUSTMENT (ADD) and
-- INVENTORY_COUNT rows may carry expires_at. "Expiring soon" is an estimate:
-- unexpired inbound quantities minus outbound consumption since the earliest
-- still-valid inbound movement (no lots table by design). Column and index are
-- additive and nullable, so existing rows and deployments without the new
-- writes keep working unchanged.

SET @col_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'stock_movements' AND COLUMN_NAME = 'expires_at');
SET @stmt := IF(@col_exists = 0, 'ALTER TABLE stock_movements ADD COLUMN expires_at DATETIME NULL AFTER occurred_at', 'SELECT 1');
PREPARE stmt FROM @stmt; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @idx_exists = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'stock_movements' AND INDEX_NAME = 'idx_stock_movements_expiry');
SET @stmt := IF(@idx_exists = 0, 'ALTER TABLE stock_movements ADD KEY idx_stock_movements_expiry (restaurant_id, expires_at, stock_item_id, warehouse_id)', 'SELECT 1');
PREPARE stmt FROM @stmt; EXECUTE stmt; DEALLOCATE PREPARE stmt;
