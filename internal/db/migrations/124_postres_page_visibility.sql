-- Postres food-type page visibility settings, stored per restaurant.
-- Mirrors the cafes/bebidas page toggles and adds where the section should be
-- rendered in the public navigation (inside the menus accordion or as its own
-- top level nav item).
-- Coordination id: postres_page_visibility_v1

SET @table_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.TABLES
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'restaurant_page_visibility'
);

SET @postres_active_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'restaurant_page_visibility'
    AND COLUMN_NAME = 'postres_page_active'
);

SET @ddl := IF(
  @table_exists = 1 AND @postres_active_exists = 0,
  'ALTER TABLE `restaurant_page_visibility` ADD COLUMN `postres_page_active` TINYINT(1) NOT NULL DEFAULT 1',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @postres_placement_exists := (
  SELECT COUNT(*)
  FROM INFORMATION_SCHEMA.COLUMNS
  WHERE TABLE_SCHEMA = DATABASE()
    AND TABLE_NAME = 'restaurant_page_visibility'
    AND COLUMN_NAME = 'postres_web_placement'
);

SET @ddl := IF(
  @table_exists = 1 AND @postres_placement_exists = 0,
  'ALTER TABLE `restaurant_page_visibility` ADD COLUMN `postres_web_placement` VARCHAR(64) NOT NULL DEFAULT ''inside_menus''',
  'SELECT 1'
);

PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
