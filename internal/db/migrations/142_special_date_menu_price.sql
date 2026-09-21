-- Coordination id: special_dates_v1.
-- Adds per-menu variable price for the special date wizard. The price is
-- snapshotted onto the booking in the public booking form (handled in a
-- follow-up) and is used by the backoffice form so operators can compute
-- totals / adelantos from real menu prices.

SET @col_exists := (
  SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'special_date_menus'
    AND COLUMN_NAME = 'price'
);
SET @ddl := IF(@col_exists = 0,
  'ALTER TABLE `special_date_menus` ADD COLUMN `price` DECIMAL(10,2) NULL AFTER `adelanto_amount`',
  'SELECT 1');
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
