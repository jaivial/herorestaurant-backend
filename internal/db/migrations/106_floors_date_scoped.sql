-- Date-scoped floors: a floor created for a specific date only exists on that
-- date (e.g. an extra room level set up for an event). Global floors keep
-- specific_date NULL. Mirrors 104_salons_date_scoped.sql: scope_key normalizes
-- NULL (global) into a sentinel so the unique index also covers global
-- duplicates, and a date-scoped floor with the same floor_number shadows the
-- global one on that date.
--
-- Per-date activation overrides keep living in restaurant_floor_overrides
-- (floor_id based), which works for both global and date-scoped rows.
-- All statements guarded (idempotent).

SET @col1 = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_floors' AND COLUMN_NAME = 'specific_date');
SET @ddl1 := IF(@col1 = 0, 'ALTER TABLE `restaurant_floors` ADD COLUMN `specific_date` DATE NULL DEFAULT NULL AFTER `is_active`', 'SELECT 1');
PREPARE stmt1 FROM @ddl1; EXECUTE stmt1; DEALLOCATE PREPARE stmt1;

SET @col2 = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_floors' AND COLUMN_NAME = 'scope_key');
SET @ddl2 := IF(@col2 = 0, 'ALTER TABLE `restaurant_floors` ADD COLUMN `scope_key` VARCHAR(10) GENERATED ALWAYS AS (IFNULL(DATE_FORMAT(specific_date, ''%Y-%m-%d''), ''global'')) STORED', 'SELECT 1');
PREPARE stmt2 FROM @ddl2; EXECUTE stmt2; DEALLOCATE PREPARE stmt2;

SET @idx_old = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_floors' AND INDEX_NAME = 'uniq_restaurant_floors_restaurant_number');
SET @ddl3 := IF(@idx_old > 0, 'ALTER TABLE `restaurant_floors` DROP INDEX `uniq_restaurant_floors_restaurant_number`', 'SELECT 1');
PREPARE stmt3 FROM @ddl3; EXECUTE stmt3; DEALLOCATE PREPARE stmt3;

SET @idx_new = (SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_floors' AND INDEX_NAME = 'uniq_restaurant_floors_scope');
SET @ddl4 := IF(@idx_new = 0, 'ALTER TABLE `restaurant_floors` ADD UNIQUE KEY `uniq_restaurant_floors_scope` (`restaurant_id`, `floor_number`, `scope_key`)', 'SELECT 1');
PREPARE stmt4 FROM @ddl4; EXECUTE stmt4; DEALLOCATE PREPARE stmt4;
