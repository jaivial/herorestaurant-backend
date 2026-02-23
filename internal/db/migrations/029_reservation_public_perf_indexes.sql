-- Improve read performance for public reservation bootstrap endpoints.
-- Idempotent: each index is added only if missing and table exists.

SET @table_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_days'
);
SET @idx_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'restaurant_days'
    AND INDEX_NAME = 'idx_restaurant_days_rest_date_open'
);
SET @ddl := IF(
  @table_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `restaurant_days` ADD KEY `idx_restaurant_days_rest_date_open` (`restaurant_id`, `date`, `is_open`)',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @table_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'FINDE'
);
SET @idx_exists = (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'FINDE'
    AND INDEX_NAME = 'idx_finde_rest_tipo_active_desc'
);
SET @ddl := IF(
  @table_exists = 1 AND @idx_exists = 0,
  'ALTER TABLE `FINDE` ADD KEY `idx_finde_rest_tipo_active_desc` (`restaurant_id`, `TIPO`, `active`, `DESCRIPCION`(191))',
  'SELECT 1'
);
PREPARE stmt FROM @ddl; EXECUTE stmt; DEALLOCATE PREPARE stmt;
