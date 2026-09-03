-- Barcode for stock items, for scanner-driven lookups (count sheets, pickers,
-- search). Non-unique on purpose: the column ships empty, and reusing a barcode
-- across pack formats is legitimate; lookups take the first match. Additive only.

SET @col_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='stock_items' AND COLUMN_NAME='barcode');
SET @ddl := IF(@col_exists=0, 'ALTER TABLE stock_items ADD COLUMN barcode VARCHAR(64) NULL AFTER sku', 'SELECT 1'); PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @idx_exists := (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='stock_items' AND INDEX_NAME='idx_stock_items_barcode');
SET @ddl := IF(@idx_exists=0, 'ALTER TABLE stock_items ADD KEY idx_stock_items_barcode (restaurant_id, barcode)', 'SELECT 1'); PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
