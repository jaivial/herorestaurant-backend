-- Promote restaurant_tables.numero_mesa from INT to a per-restaurant unique
-- alphanumeric identifier (e.g. "4", "4B", "4-B") so users can set/label the
-- table number shown on the map node. Existing integer values are preserved
-- verbatim as strings (1 -> "1", 2 -> "2", ...).
--
-- PREREQUISITE: before deploying, audit for duplicate (restaurant_id, numero_mesa)
-- pairs. The auto-derivation in the backend used MAX(numero_mesa)+1 so duplicates
-- are very unlikely, but ADD UNIQUE below will fail if any exist. See
-- backoffice_premium.go createBOPremiumTable for the legacy derivation.
SET @table_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables'
);

-- 1) Widen numero_mesa to VARCHAR(32) NOT NULL (only if still an integer type).
SET @numero_data_type := (
  SELECT DATA_TYPE
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND COLUMN_NAME = 'numero_mesa'
);
SET @ddl_numero := IF(
  @table_exists = 1 AND @numero_data_type IN ('int', 'tinyint', 'smallint', 'mediumint', 'bigint'),
  "ALTER TABLE `restaurant_tables` MODIFY COLUMN `numero_mesa` VARCHAR(32) NOT NULL",
  'SELECT 1'
);
PREPARE stmt_numero FROM @ddl_numero;
EXECUTE stmt_numero;
DEALLOCATE PREPARE stmt_numero;

-- 2) Add per-restaurant uniqueness on (restaurant_id, numero_mesa) if missing.
SET @idx_numero_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.STATISTICS
  WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'restaurant_tables' AND INDEX_NAME = 'uniq_restaurant_tables_restaurant_numero'
);
SET @ddl_idx_numero := IF(
  @table_exists = 1 AND @idx_numero_exists = 0,
  'ALTER TABLE restaurant_tables ADD UNIQUE KEY uniq_restaurant_tables_restaurant_numero (restaurant_id, numero_mesa)',
  'SELECT 1'
);
PREPARE stmt_idx_numero FROM @ddl_idx_numero;
EXECUTE stmt_idx_numero;
DEALLOCATE PREPARE stmt_idx_numero;
