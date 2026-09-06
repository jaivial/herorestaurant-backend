-- Per food-type public page visibility and web placement for cafes, vinos and
-- bebidas, mirroring the columns postres already owns. Each food type gets its
-- own placement so the public navigation can render it inside the menus
-- accordion or as its own top level item, independently of the others.
-- Coordination id: foodtype_page_visibility_v1

SET @table_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'restaurant_page_visibility'
);

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'restaurant_page_visibility'
    AND COLUMN_NAME = 'vinos_page_active'
);

SET @ddl := IF(
  @table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `restaurant_page_visibility` ADD COLUMN `vinos_page_active` TINYINT(1) NOT NULL DEFAULT 1',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'restaurant_page_visibility'
    AND COLUMN_NAME = 'cafes_web_placement'
);

SET @ddl := IF(
  @table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `restaurant_page_visibility` ADD COLUMN `cafes_web_placement` VARCHAR(64) NOT NULL DEFAULT ''inside_menus''',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'restaurant_page_visibility'
    AND COLUMN_NAME = 'vinos_web_placement'
);

SET @ddl := IF(
  @table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `restaurant_page_visibility` ADD COLUMN `vinos_web_placement` VARCHAR(64) NOT NULL DEFAULT ''inside_menus''',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'restaurant_page_visibility'
    AND COLUMN_NAME = 'bebidas_web_placement'
);

SET @ddl := IF(
  @table_exists = 1 AND @col_exists = 0,
  'ALTER TABLE `restaurant_page_visibility` ADD COLUMN `bebidas_web_placement` VARCHAR(64) NOT NULL DEFAULT ''inside_menus''',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
